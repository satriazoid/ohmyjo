//go:build windows

package app

import (
	"strings"
	"sync"
	"time"
	"unicode"

	"ohmyjo/internal/config"
	"ohmyjo/internal/session"
	"ohmyjo/internal/ui"
	"ohmyjo/internal/vt"
)

// Pane is one terminal: a shell session, an emulator fed from it, and the grid
// that renders it.
//
// Output arrives on the session's reader goroutine while painting happens on the
// window's message thread, so every composite access to the emulator is guarded
// by mu. The lock is held for the duration of a parse or a paint and never
// across a call into the shell, so a slow shell can never block the UI.
//
// Lock order is mu before the emulator's own lock, never the reverse.
type Pane struct {
	mu   sync.Mutex
	grid *ui.Grid
	term vt.Terminal

	// id is the session this pane runs; empty until Attach succeeds.
	id string
	// name is the profile the pane runs, for the tab title.
	name string
	// sess is the session, nil after Close. It is an interface rather than a
	// *session.Session so the input path can be tested without a real console
	// host: what the pane needs from a session is exactly these three calls.
	sess paneSession
	// cancel detaches this pane from the session's output fanout.
	cancel func()
	// release ends the session through the manager, so closing a pane also
	// drops it from the registry the panel's rows are built from. Kept apart
	// from sess so Close ends the session the registry knows about rather than
	// the handle it happens to hold.
	release func()

	// exited and exitCode describe the shell's end state, so a dead pane is
	// never mistaken for an idle one.
	exited   bool
	exitCode int

	// autoScroll pins the viewport to the live screen while output arrives. It
	// is cleared when the user scrolls up and re-armed when they reach the
	// bottom, which is what every terminal does.
	autoScroll bool

	sel selection

	// click bookkeeping for double and triple click.
	lastClick time.Time
	clickRow  int
	clickCol  int
	clickN    int

	// dragging is true between a scrollbar press and its release.
	dragging bool
	// dragGrab is the offset within the thumb at the moment of the press, so a
	// drag does not snap the thumb under the pointer.
	dragGrab int

	// lastCols and lastRows are the size last reported to the shell, so a
	// layout pass that did not actually change the size does not resize ConPTY.
	lastCols, lastRows int

	onDirty func(*Pane)
}

// paneSession is the part of a session a pane uses after it is attached. It is
// satisfied by *session.Session, and by a fake in tests.
type paneSession interface {
	// Write sends input to the shell.
	Write([]byte) error
	// Resize reports a new grid size to the shell.
	Resize(cols, rows int) error
	// Info describes the running process.
	Info() session.Info
	// Close ends the shell.
	Close() error
}

// selection is a text range in absolute content coordinates, so scrolling does
// not move it. ar/ac is the anchor, row/col the moving end.
type selection struct {
	active bool
	block  bool
	ar, ac int
	row    int
	col    int
}

func (s selection) empty() bool {
	return !s.active || (s.ar == s.row && s.ac == s.col)
}

// NewPane creates a pane with no session attached. pad is the inner padding in
// physical pixels, so the caller resolves logical padding against the DPI.
func NewPane(cols, rows int, styles *ui.StyleResolver, mono ui.Metrics, pad int) *Pane {
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	term := vt.New(vt.WithSize(cols, rows))
	return &Pane{
		term:       term,
		grid:       ui.NewGrid(term, pad, styles, mono),
		autoScroll: true,
	}
}

// Grid exposes the renderer for layout, metrics and hit testing.
func (p *Pane) Grid() *ui.Grid { return p.grid }

// ID is the session this pane runs.
func (p *Pane) ID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.id
}

// Exited reports whether the shell has ended.
func (p *Pane) Exited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited
}

// ExitCode is the shell's exit status, valid once Exited is true.
func (p *Pane) ExitCode() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitCode
}

// Info describes the pane's session, or the zero value when the pane has no
// session. Callers use it to restart or duplicate the pane in the same place.
func (p *Pane) Info() session.Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sess == nil {
		return session.Info{}
	}
	return p.sess.Info()
}

// Name is the pane's profile name, used as the tab title until the shell sets
// one of its own.
func (p *Pane) Name() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.name
}

// Title is the title the shell set with an escape sequence, empty when it has
// not set one.
func (p *Pane) Title() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.term.Title()
}

// CursorBlink reports whether the cursor should blink: the application asked
// for a blinking style and the cursor is on screen. An idle steady cursor must
// not drive a repaint every blink interval.
func (p *Pane) CursorBlink() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.term.CursorVisible() || p.term.ScrollOffset() > 0 {
		return false
	}
	return p.term.CursorBlink()
}

// Clear empties the screen and discards the scrollback.
//
// The screen is cleared through the emulator rather than by dropping cells, so
// the shell's own idea of the screen stays consistent with what is drawn, and
// the cursor is homed the way a clear is expected to leave it.
func (p *Pane) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	// The clear goes through the emulator's parser rather than straight at the
	// cells, so the cursor is homed and the damage set is built the same way it
	// is for a shell that clears its own screen.
	p.term.Write([]byte("\x1b[2J\x1b[H"))
	p.term.ClearScrollback()
	p.sel = selection{}
	p.autoScroll = true
	p.grid.Invalidate()
}

// ViewportCell maps a surface point to a viewport cell, which is what mouse
// reporting needs: applications receive screen-relative coordinates, not
// content coordinates, or a scrolled-back view would report the wrong rows.
func (p *Pane) ViewportCell(px, py int) (col, row int, ok bool) {
	return p.grid.CellAt(px, py)
}

// SetPadding changes the pane's inner padding after a DPI change.
func (p *Pane) SetPadding(pad int) {
	if pad < 0 {
		pad = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.grid.SetPadding(pad)
}

// SetScrollback sets how many history lines the pane keeps.
func (p *Pane) SetScrollback(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.term.SetScrollbackSize(n)
	p.grid.Invalidate()
}

// Attach starts a shell and wires the pane to its output.
//
// The subscription is taken and its replay buffer parsed before the pane can be
// painted or accept input: a pane that painted first would show an empty screen
// until the shell happened to redraw, which for an idle prompt is never.
func (p *Pane) Attach(mgr *session.Manager, prof config.Profile, cwd string) error {
	p.mu.Lock()
	cols, rows := p.grid.Size()
	p.mu.Unlock()
	if cols < 1 || rows < 1 {
		cols, rows = 80, 24
	}
	id, err := mgr.Start(session.Options{Profile: prof, Cols: cols, Rows: rows, Cwd: cwd})
	if err != nil {
		return err
	}
	sess, err := mgr.Get(id)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.sess, p.id, p.name = sess, id, prof.Name
	// The pane does not keep the manager, so the id is bound to its owner here,
	// once, rather than looked up again on every close.
	p.release = func() { _ = mgr.Close(id) }
	replay, cancel := sess.Subscribe(p.onOutput)
	p.cancel = cancel
	if replay != "" {
		// Parsed under the pane lock, so no live byte can be interleaved ahead
		// of the replay it is supposed to follow.
		p.term.Write([]byte(replay))
	}
	p.lastCols, p.lastRows = cols, rows
	p.mu.Unlock()

	p.notifyDirty()

	go func() {
		<-sess.Done()
		p.mu.Lock()
		p.exited = true
		if code := sess.Info().ExitCode; code != nil {
			p.exitCode = *code
		}
		p.mu.Unlock()
		p.notifyDirty()
	}()
	return nil
}

// onOutput is the session's fanout sink. It runs on the session's reader
// goroutine and must not block, so it only parses and asks for a repaint.
func (p *Pane) onOutput(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	p.mu.Lock()
	p.term.Write(chunk)
	// Feeding a scrolled-back viewport new output must not move it: the reader
	// is looking at history, and jumping to the bottom would lose their place.
	p.mu.Unlock()
	p.notifyDirty()
}

func (p *Pane) notifyDirty() {
	if p.onDirty != nil {
		p.onDirty(p)
	}
}

// Write sends input to the shell.
func (p *Pane) Write(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	p.mu.Lock()
	sess := p.sess
	p.mu.Unlock()
	if sess == nil {
		return nil
	}
	return sess.Write(data)
}

// Mode is the emulator's current mode, needed to encode keys and report mouse
// events the way the running application asked for.
func (p *Pane) Mode() vt.ModeFlag {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.term.Mode()
}

// Size is the pane's grid size in cells.
func (p *Pane) Size() (cols, rows int) { return p.grid.Size() }

// Resize reports the grid size to the shell. The emulator was already resized
// by the grid when the layout moved.
func (p *Pane) Resize(cols, rows int) {
	p.mu.Lock()
	sess := p.sess
	p.lastCols, p.lastRows = cols, rows
	p.mu.Unlock()
	if sess == nil {
		return
	}
	// A shell that just exited is not a resize failure worth surfacing.
	_ = sess.Resize(cols, rows)
}

// Close detaches from the session and ends it.
//
// The session is ended through the manager rather than through the session
// handle, so it leaves the registry as it dies. Closing the handle directly
// killed the shell but left it listed: the panel's rows come from the registry,
// so a session ended from the panel would keep its row for the rest of the run
// and the row could never be cleared.
func (p *Pane) Close() {
	p.mu.Lock()
	cancel, release := p.cancel, p.release
	p.cancel, p.sess, p.onDirty, p.release = nil, nil, nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if release != nil {
		release()
	}
}

// ---------------------------------------------------------------------------
// Scrolling

// Scroll moves the viewport by delta lines, positive meaning back into history,
// and returns whether it moved.
func (p *Pane) Scroll(delta int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	before := p.term.ScrollOffset()
	after := p.term.ScrollUp(delta)
	if after == before {
		return false
	}
	p.autoScroll = after == 0
	p.grid.Invalidate()
	return true
}

// ScrollToBottom returns the viewport to the live screen.
func (p *Pane) ScrollToBottom() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.term.ScrollOffset() == 0 {
		return
	}
	p.term.ScrollTo(0)
	p.autoScroll = true
	p.grid.Invalidate()
}

// ScrolledBack reports whether the viewport is above the live screen.
func (p *Pane) ScrolledBack() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.term.ScrollOffset() > 0
}

// scrollMetrics describes the scrollbar's state: how many lines of history
// exist, how many are visible, and where the viewport sits.
func (p *Pane) scrollMetrics() (total, visible, top int) {
	total = p.term.ContentRows()
	_, visible = p.grid.Size()
	top = p.term.ViewportTop()
	return total, visible, top
}

// ScrollToContentLine puts the viewport so that content line top is its first
// visible row. It is what a scrollbar drag ultimately does.
func (p *Pane) ScrollToContentLine(top int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, visible := p.grid.Size()
	maxTop := len2(p.term.ContentRows() - visible)
	top = clampInt(top, 0, maxTop)
	// ViewportTop is ScrollbackLen minus the scroll offset, so the offset that
	// puts content line top first is exactly that difference.
	off := len2(p.term.ScrollbackLen() - top)
	p.term.ScrollTo(off)
	p.autoScroll = p.term.ScrollOffset() == 0
	p.grid.Invalidate()
}

// ---------------------------------------------------------------------------
// Selection

// HasSelection reports whether anything is selected.
func (p *Pane) HasSelection() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.sel.empty()
}

// ClearSelection drops the selection.
func (p *Pane) ClearSelection() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.sel.active {
		return
	}
	p.sel = selection{}
	p.grid.Invalidate()
}

// SelectAll selects the whole scrollback plus the visible screen.
func (p *Pane) SelectAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	last := p.term.ContentRows() - 1
	if last < 0 {
		return
	}
	end, _ := p.term.ContentRow(last)
	if p.term.ScrollOffset() > 0 {
		// Select All while scrolled back should also bring the user to the live
		// screen, otherwise the selection is mostly off screen.
		p.term.ScrollTo(0)
		p.autoScroll = true
	}
	p.sel = selection{active: true, ar: 0, ac: 0, row: last, col: len(end)}
	p.grid.Invalidate()
}

// BeginSelection starts a selection at a surface point. clicks is the number of
// clicks in the current gesture: one selects a cell, two a word, three a line.
func (p *Pane) BeginSelection(px, py int, clicks int, block bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	row, col, ok := p.contentAt(px, py)
	if !ok {
		return
	}
	p.selectionFor(row, col, clicks, block)
	p.grid.Invalidate()
}

// ExtendSelection moves the far end of the selection to a surface point.
func (p *Pane) ExtendSelection(px, py int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	row, col, ok := p.contentAt(px, py)
	if !ok {
		return
	}
	if !p.sel.active {
		p.sel = selection{active: true, ar: row, ac: col, row: row, col: col}
	}
	p.sel.row, p.sel.col = row, col
	p.sel.active = !(p.sel.row == p.sel.ar && p.sel.col == p.sel.ac)
	p.grid.Invalidate()
}

// selectionFor applies a click's selection.
func (p *Pane) selectionFor(row, col, clicks int, block bool) {
	switch clicks {
	case 2:
		r0, c0, r1, c1 := p.wordAt(row, col)
		p.sel = selection{active: true, block: block, ar: r0, ac: c0, row: r1, col: c1}
	case 3:
		line, _ := p.term.ContentRow(row)
		p.sel = selection{active: true, block: block, ar: row, ac: 0, row: row, col: len(line)}
	default:
		p.sel = selection{active: true, block: block, ar: row, ac: col, row: row, col: col}
	}
}

// ClickCount tracks successive clicks at the same cell so a double click
// selects a word and a triple click a line. The gesture resets when the cell
// changes or the clicks are further apart than the double-click interval.
func (p *Pane) ClickCount(now time.Time, row, col int, dblClick time.Duration) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if now.Sub(p.lastClick) > dblClick || row != p.clickRow || col != p.clickCol {
		p.clickN = 0
	}
	p.clickN++
	if p.clickN > 3 {
		p.clickN = 1
	}
	p.lastClick, p.clickRow, p.clickCol = now, row, col
	return p.clickN
}

// ContentAt maps a surface point to an absolute content cell.
func (p *Pane) ContentAt(px, py int) (row, col int, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.contentAt(px, py)
}

// contentAt maps a surface point to an absolute content coordinate.
//
// A point outside the viewport clamps to its edge, which is what makes a drag
// past the edge extend the selection to the end of the visible area instead of
// stopping wherever the pointer happened to be.
func (p *Pane) contentAt(px, py int) (row, col int, ok bool) {
	cols, rows := p.grid.Size()
	if cols == 0 || rows == 0 {
		return 0, 0, false
	}
	ox, oy := p.grid.Origin()
	cw, ch := p.grid.CellWidth(), p.grid.CellHeight()
	if cw <= 0 || ch <= 0 {
		return 0, 0, false
	}
	col = (px - ox) / cw
	scr := (py - oy) / ch
	col = clampInt(col, 0, cols)
	scr = clampInt(scr, 0, rows-1)
	return p.term.ViewportTop() + scr, col, true
}

// wordAt expands a cell into the word containing it.
func (p *Pane) wordAt(row, col int) (r0, c0, r1, c1 int) {
	line, ok := p.term.ContentRow(row)
	if !ok {
		return row, col, row, col
	}
	isSep := func(i int) bool {
		if i < 0 || i >= len(line) {
			return true
		}
		return !isWordRune(line[i].Char)
	}
	// A word character is selected to its boundaries; anything else selects the
	// single cell under the pointer, matching every terminal.
	if isSep(col) {
		return row, col, row, col + 1
	}
	c0 = col
	for c0 > 0 && !isSep(c0-1) {
		c0--
	}
	c1 = col
	for c1 < len(line) && !isSep(c1) {
		c1++
	}
	return row, c0, row, c1
}

// SelectionText returns the selected text, with lines joined by CRLF so a paste
// into a shell arrives as separate lines. Trailing blanks are trimmed, so
// copying a prompt does not drag a screen of spaces with it.
func (p *Pane) SelectionText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sel.empty() {
		return ""
	}
	r0 := minInt(p.sel.ar, p.sel.row)
	r1 := maxInt(p.sel.ar, p.sel.row)

	var b strings.Builder
	for row := r0; row <= r1; row++ {
		c0, c1, ok := p.selectionSpan(row)
		if !ok {
			continue
		}
		line, ok := p.term.ContentRow(row)
		if !ok {
			continue
		}
		c0 = clampInt(c0, 0, len(line))
		c1 = clampInt(c1, 0, len(line))
		var lb strings.Builder
		for c := c0; c < c1; c++ {
			g := line[c]
			// The continuation cell of a wide glyph holds a placeholder, not a
			// real character; emitting it would insert a stray space.
			if g.Mode&vt.AttrWideTail != 0 {
				continue
			}
			if g.Char == 0 {
				lb.WriteByte(' ')
			} else {
				lb.WriteRune(g.Char)
			}
		}
		b.WriteString(strings.TrimRight(lb.String(), " "))
		if row != r1 {
			b.WriteString("\r\n")
		}
	}
	return b.String()
}

// selectionSpan returns the selected column range on a content row, and whether
// the row is part of the selection at all.
func (p *Pane) selectionSpan(row int) (c0, c1 int, ok bool) {
	if p.sel.empty() {
		return 0, 0, false
	}
	r0, c0 := p.sel.ar, p.sel.ac
	r1, c1 := p.sel.row, p.sel.col
	if r0 > r1 || (r0 == r1 && c0 > c1) {
		r0, c0, r1, c1 = r1, c1, r0, c0
	}
	if row < r0 || row > r1 {
		return 0, 0, false
	}
	if p.sel.block {
		// Block selection takes the same columns on every row it covers.
		return minInt(c0, c1), maxInt(c0, c1), true
	}
	cols, _ := p.grid.Size()
	// Only the anchor row starts at the anchor column; every row below it runs
	// from the left edge. Likewise only the last row stops at the moving end.
	if row != r0 {
		c0 = 0
	}
	if row != r1 {
		c1 = cols
	}
	return c0, c1, true
}

// ---------------------------------------------------------------------------
// Painting

// Paint draws the pane: the grid, the selection over it, the scrollbar, and the
// exit notice if the shell is gone. It returns the cursor rectangle so the
// caller can place an IME candidate window, and whether a cursor was drawn.
func (p *Pane) Paint(s ui.Surface, theme ui.Palette, blinkOn bool) (ui.Cursor, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// The cursor is meaningless in the middle of history, so it is hidden there.
	shape := p.term.CursorShape()
	blink := blinkOn
	if p.term.ScrollOffset() > 0 {
		blink = false
	} else if !p.term.CursorBlink() {
		blink = true // a steady cursor is always in its on phase
	}
	p.grid.SetCursorShape(blink, shape)

	p.grid.Paint(s, theme.Background)
	if p.sel.active {
		p.paintSelection(s, theme)
	}
	p.paintScrollbar(s, theme)

	if p.exited {
		p.paintExit(s, theme)
	}

	x, y, visible := p.term.CursorCell()
	if !visible || p.term.ScrollOffset() > 0 {
		return ui.Cursor{}, false
	}
	// A block cursor would cover one and a half cells on the head of a wide
	// glyph, hiding the character its tail belongs to; such a cell gets no
	// cursor rather than a misleading one.
	if shape == vt.CursorBlock || shape == vt.CursorBlockBlink {
		if !p.grid.CursorFits(x, y) {
			return ui.Cursor{}, false
		}
	}
	px, py, w, h := p.grid.CellRect(x, y)
	return ui.Cursor{X: px, Y: py, W: w, H: h}, true
}

// paintSelection fills the selected cells. It is drawn after the glyphs so the
// highlight reads as a tint over them, and before the cursor so the cursor
// stays on top.
func (p *Pane) paintSelection(s ui.Surface, theme ui.Palette) {
	top := p.term.ViewportTop()
	_, rows := p.grid.Size()
	if rows == 0 {
		return
	}
	cw := p.grid.CellWidth()
	for scr := 0; scr < rows; scr++ {
		c0, c1, ok := p.selectionSpan(top + scr)
		if !ok || c1 <= c0 {
			continue
		}
		bx, y, _, h := p.grid.CellRect(c0, scr)
		s.Fill(bx, y, (c1-c0)*cw, h, theme.Selection)
	}
}

// paintScrollbar draws the scrollbar in the pane's right gutter.
//
// It is drawn even when the viewport is at the bottom, because its length is
// the only indication of how much history exists. When everything fits there is
// no bar at all, which is how a pane that has produced no output stays clean.
func (p *Pane) paintScrollbar(s ui.Surface, theme ui.Palette) {
	total, visible, top := p.scrollMetrics()
	if total <= visible || visible <= 0 {
		return
	}
	bx, by, bw, bh := p.grid.Bounds()
	if bw <= scrollbarWidth*2 {
		return
	}
	barX := bx + bw - scrollbarWidth
	s.Fill(barX, by, scrollbarWidth, bh, theme.UIBackground)
	thumbH := bh * visible / total
	if thumbH < 12 {
		thumbH = 12
	}
	if thumbH > bh {
		thumbH = bh
	}
	maxTop := total - visible
	ty := by
	if maxTop > 0 {
		ty += (bh - thumbH) * top / maxTop
	}
	col := theme.UIBorder
	if p.term.ScrollOffset() > 0 {
		// A distinct colour while scrolled back makes it obvious the view is not
		// live, which is otherwise easy to miss.
		col = theme.UIAccent
	}
	s.Fill(barX, ty, scrollbarWidth, thumbH, col)
}

// paintExit draws the shell's exit status over the last row, where the user is
// already looking.
func (p *Pane) paintExit(s ui.Surface, theme ui.Palette) {
	x, y, w, h := p.grid.Bounds()
	text := "process exited with code " + itoa(p.exitCode)
	fm := s.SetFont(ui.FontUI)
	tw := s.TextWidth(text)
	th := maxInt(1, h/5)
	if th > maxInt(1, h) {
		th = h
	}
	s.Fill(x, y+h-th, w, th, theme.UIBackgroundAlt)
	s.Text(x+maxInt(0, (w-tw)/2), y+h-th+maxInt(0, (th-fm.TextH())/2), text,
		ui.Style{FG: theme.UIForeground, BG: theme.UIBackgroundAlt})
}

// ---------------------------------------------------------------------------
// Helpers

func isWordRune(r rune) bool {
	if r == 0 || r <= ' ' {
		return false
	}
	// ASCII punctuation and the classic shell separators end a word; everything
	// else, including any non-ASCII letter, is part of one. That keeps paths
	// with accented characters selecting whole.
	switch r {
	case '(', ')', '[', ']', '{', '}', '\\', '\'', ',', '"', '`':
		return false
	}
	if r < 0x80 {
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	return !unicode.IsSpace(r)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// len2 clamps a length to at least zero.
func len2(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
