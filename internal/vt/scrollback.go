package vt

// Scrollback and damage tracking.
//
// The upstream vt10x has neither: it keeps only the visible screen and exposes
// no per-line damage information. Both are added here because the native
// renderer needs them. History lines are appended by pointer from scrollUp, so
// the cost of keeping scrollback is bounded by the work the terminal already
// does rather than by the configured limit.

const (
	// DefaultScrollback is the number of history lines retained per screen.
	DefaultScrollback = 10000
	// maxScrollback caps what callers may configure, so a bad config value
	// cannot make one terminal reserve gigabytes.
	maxScrollback = 200000
)

// Exported character attributes. Upstream keeps these unexported, which makes
// it impossible for a renderer to tell bold from blink.
const (
	AttrReverse   = attrReverse
	AttrUnderline = attrUnderline
	AttrBold      = attrBold
	AttrGfx       = attrGfx
	AttrItalic    = attrItalic
	AttrBlink     = attrBlink
	AttrWrap      = attrWrap
	// AttrWide marks the first of the two cells a wide glyph occupies.
	AttrWide = attrWide
	// AttrWideTail marks the continuation cell of a wide glyph, which carries
	// no glyph of its own and must not be painted.
	AttrWideTail = attrWideTail
)

// Cursor shape states, exported so a renderer can draw the cursor.
const (
	CursorDefault  = cursorDefault
	CursorWrapNext = cursorWrapNext
	CursorOrigin   = cursorOrigin
)

// Cursor shapes, as requested by DECSCUSR; the emulator implements the
// sequence in csi.go and the renderer draws the result.
const (
	CursorBlock = iota
	CursorBlockBlink
	CursorUnderline
	CursorUnderlineBlink
	CursorBar
	CursorBarBlink
)

// CursorShape reports the DECSCUSR cursor style.
func (t *State) CursorShape() int { return int(t.cursorShape) }

// CursorBlink reports whether the cursor was asked to blink.
func (t *State) CursorBlink() bool {
	switch t.cursorShape {
	case CursorBlockBlink, CursorUnderlineBlink, CursorBarBlink:
		return true
	}
	return false
}

// Line is one screen row of cells. Its length is the terminal width at the
// time the line was produced, which may differ from the current width for
// history lines taken before a resize.
type Line []Glyph

// history is a ring buffer of scrolled-off lines.
type history struct {
	buf  []Line
	head int // index of the oldest element
	n    int // number of valid elements
	cap  int
}

func newHistory(capacity int) *history {
	capacity = clamp(capacity, 0, maxScrollback)
	return &history{cap: capacity}
}

func (h *history) len() int { return h.n }

// push appends a line, evicting the oldest when at capacity.
func (h *history) push(l Line) {
	if h.cap == 0 {
		return
	}
	if h.n < h.cap {
		if h.buf == nil {
			// Grow geometrically but never past the configured cap, so an
			// idle terminal pays for what it uses and not for the ceiling.
			initial := h.cap
			if initial > 256 {
				initial = 256
			}
			h.buf = make([]Line, initial)
		}
		if len(h.buf) <= h.n {
			grown := len(h.buf) * 2
			if grown > h.cap {
				grown = h.cap
			}
			nb := make([]Line, grown)
			for i := range h.n {
				nb[i] = h.at(i)
			}
			h.buf, h.head = nb, 0
		}
		h.buf[(h.head+h.n)%len(h.buf)] = l
		h.n++
		return
	}
	h.buf[h.head] = l
	h.head = (h.head + 1) % len(h.buf)
}

// at returns the i'th oldest line. The caller must hold the state lock.
func (h *history) at(i int) Line {
	if i < 0 || i >= h.n {
		return nil
	}
	if h.n < len(h.buf) {
		return h.buf[i]
	}
	return h.buf[(h.head+i)%len(h.buf)]
}

func (h *history) clear() {
	h.buf, h.head, h.n = nil, 0, 0
}

// pushHistory moves the top screen line into scrollback. The line is cloned
// because scrollUp clears and rotates the popped screen row in place, so
// retaining the slice itself would let later output overwrite history. It is a
// no-op on the alternate screen, which has no scrollback by definition.
func (t *State) pushHistory(l Line) {
	if t.mode&ModeAltScreen != 0 || t.history == nil {
		return
	}
	if t.history.cap == 0 {
		return
	}
	t.history.push(cloneLine(l))
	if t.sbOffset > 0 {
		// Keep the viewport glued to the same content while the user is
		// scrolled back.
		if t.sbOffset < t.history.len() {
			t.sbOffset++
		}
	}
	t.changed |= ChangedScrollback
}

func cloneLine(l Line) Line {
	c := make(Line, len(l))
	copy(c, l)
	return c
}

// SetScrollbackSize sets how many history lines to retain. Shrinking discards
// the oldest lines immediately.
func (t *State) SetScrollbackSize(n int) {
	t.lock()
	defer t.unlock()
	n = clamp(n, 0, maxScrollback)
	if t.history != nil && n == t.history.cap {
		return
	}
	old := t.history
	t.history = newHistory(n)
	if old != nil && n > 0 {
		keep := old.len()
		if keep > n {
			keep = n
		}
		for i := old.len() - keep; i < old.len(); i++ {
			t.history.push(old.at(i))
		}
	}
	if t.sbOffset > t.history.len() {
		t.sbOffset = t.history.len()
	}
}

// ScrollbackLen reports how many lines are currently in scrollback.
func (t *State) ScrollbackLen() int {
	if t.history == nil {
		return 0
	}
	return t.history.len()
}

// ScrollbackLine returns the i'th oldest history line, or nil if out of range.
func (t *State) ScrollbackLine(i int) Line {
	if t.history == nil {
		return nil
	}
	return t.history.at(i)
}

// ClearScrollback discards all history lines.
func (t *State) ClearScrollback() {
	if t.history == nil {
		return
	}
	t.history.clear()
	t.sbOffset = 0
	t.changed |= ChangedScrollback
}

// ViewportTop is the absolute content index of the first visible row. Screen row
// y corresponds to content row ViewportTop()+y, which is how a selection
// expressed in content coordinates is mapped back for drawing.
func (t *State) ViewportTop() int { return t.ScrollbackLen() - t.sbOffset }

// ScrollOffset reports how many lines the view is scrolled back from the live
// screen. 0 means the live view.
func (t *State) ScrollOffset() int { return t.sbOffset }

// ScrollUp adjusts the viewport offset by delta lines (positive scrolls back
// into history). It returns the resulting offset.
func (t *State) ScrollUp(delta int) int {
	t.lock()
	defer t.unlock()
	return t.scrollTo(t.sbOffset + delta)
}

// ScrollTo sets the viewport offset, clamped to the available history.
func (t *State) ScrollTo(offset int) int {
	t.lock()
	defer t.unlock()
	return t.scrollTo(offset)
}

func (t *State) scrollTo(offset int) int {
	maxOff := t.ScrollbackLen()
	if offset > maxOff {
		offset = maxOff
	}
	if offset < 0 {
		offset = 0
	}
	if offset == t.sbOffset {
		return offset
	}
	old := t.sbOffset
	t.sbOffset = offset
	// Repaint the band that actually changed rather than everything: shifting
	// the viewport by k lines only makes sense as a damage rectangle when the
	// move is smaller than the screen.
	if d := offset - old; d > -t.rows && d < t.rows {
		if d > 0 {
			t.markLinesDirty(0, t.rows-d-1)
		} else {
			t.markLinesDirty(-d, t.rows-1)
		}
	}
	// A wide glyph whose head scrolled just off the top loses its tail: the new
	// first row would begin with a continuation cell that has nothing to
	// continue. The same holds at the bottom edge.
	if t.rows > 0 {
		if t.sbOffset > 0 && t.history != nil && t.history.len() > 0 {
			idx := t.history.len() - t.sbOffset
			if idx > 0 && idx < t.history.len() {
				t.repairEdges(t.history.at(idx), t.history.at(idx-1))
			}
		}
	}
	t.changed |= ChangedScreen
	return t.sbOffset
}

// repairEdges drops a wide-glyph half that a viewport boundary has separated
// from its partner. prev may be nil when there is no row above.
func (t *State) repairEdges(first, prev Line) {
	if len(first) == 0 {
		return
	}
	if first[0].Mode&attrWideTail != 0 {
		if prev == nil || len(prev) == 0 || prev[len(prev)-1].Mode&attrWide == 0 {
			if t.history != nil && t.history.len() > 0 && t.sbOffset > 0 {
				l := t.history.at(t.history.len() - t.sbOffset)
				if len(l) > 0 {
					l[0].Mode &^= attrWideTail
					l[0].Char = ' '
				}
			}
		}
	}
	if n := len(first); first[n-1].Mode&attrWide != 0 {
		first[n-1].Mode &^= attrWide
		first[n-1].Char = ' '
	}
}

// ContentRows is the total number of addressable lines: scrollback plus the
// live screen.
func (t *State) ContentRows() int {
	if t.sbOffset > 0 {
		// While scrolled back the history was trimmed to keep the viewport
		// anchored, so report what is still addressable.
		return t.ScrollbackLen() + t.rows
	}
	return t.ScrollbackLen() + t.rows
}

// ContentRow returns the line at an absolute index, where 0 is the oldest
// addressable line and ContentRows()-1 is the last screen row. A selection is
// anchored to these indices rather than to screen rows so that scrolling does
// not move it.
func (t *State) ContentRow(idx int) (Line, bool) {
	h := t.ScrollbackLen()
	if idx < 0 {
		return nil, false
	}
	if idx < h {
		return t.history.at(idx), true
	}
	s := idx - h
	if s >= t.rows {
		return nil, false
	}
	return t.lines[s], true
}

// ViewRow returns the cells to draw at screen row y, honouring the current
// viewport offset. The second result is false when y is past the end of the
// available content (only possible mid-scroll after history was trimmed).
func (t *State) ViewRow(y int) (Line, bool) {
	if y < 0 || y >= t.rows {
		return nil, false
	}
	if t.sbOffset == 0 {
		return t.lines[y], true
	}
	h := t.ScrollbackLen()
	idx := h - t.sbOffset + y
	if idx < 0 {
		return nil, false
	}
	if idx < h {
		return t.history.at(idx), true
	}
	srow := idx - h
	if srow >= t.rows {
		return nil, false
	}
	return t.lines[srow], true
}

// markLinesDirty marks rows in [y0,y1] for repaint.
func (t *State) markLinesDirty(y0, y1 int) {
	if y0 < 0 {
		y0 = 0
	}
	if y1 >= len(t.dirty) {
		y1 = len(t.dirty) - 1
	}
	for y := y0; y <= y1; y++ {
		t.dirty[y] = true
	}
}

// DirtyLines reports whether any line changed since the last Unlock and, if
// so, the inclusive range that covers all dirty lines.
func (t *State) DirtyLines() (y0, y1 int, any bool) {
	y0, y1 = -1, -1
	for y, d := range t.dirty {
		if !d {
			continue
		}
		if y0 < 0 {
			y0 = y
		}
		y1 = y
	}
	if y0 < 0 {
		return 0, 0, false
	}
	return y0, y1, true
}

// DirtyRow reports whether screen row y changed since the last Unlock.
func (t *State) DirtyRow(y int) bool {
	if y < 0 || y >= len(t.dirty) {
		return false
	}
	return t.dirty[y]
}

// CursorCell reports the cursor position, visible only on the live view.
func (t *State) CursorCell() (x, y int, visible bool) {
	if t.sbOffset != 0 || !t.CursorVisible() {
		return 0, 0, false
	}
	c := t.cur
	if c.Y < 0 || c.Y >= t.rows || c.X < 0 || c.X >= t.cols {
		return 0, 0, false
	}
	return c.X, c.Y, true
}

// TotalRows is the number of rows of content available including history.
func (t *State) TotalRows() int { return t.ScrollbackLen() + t.rows }

// ScreenRows returns the current screen height.
func (t *State) ScreenRows() int { return t.rows }

// ScreenCols returns the current screen width.
func (t *State) ScreenCols() int { return t.cols }
