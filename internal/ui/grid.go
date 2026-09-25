//go:build windows

package ui

import "ohmyjo/internal/vt"

// Font slots the renderer knows about. The terminal grid is monospaced, so one
// Mono slot serves every pane; the UI slot is proportional and used for chrome.
const (
	FontMono FontID = iota
	FontUIBold
	FontUI
)

// Grid draws a vt terminal into a Surface.
//
// The window repaints its whole client area every frame — a partial-rect WM_PAINT
// is not worth the bookkeeping when a full frame is one BitBlt — so the row
// cache here is what keeps that affordable: a row is rebuilt only when the
// emulator reports it dirty or the cache was invalidated, and a frame where one
// line of output arrived rebuilds exactly one row.
type Grid struct {
	term vt.Terminal

	// cell geometry, derived from the Mono font slot
	cellW, cellH int
	textTop      int // offset from the cell top to the glyph box top
	originX      int
	originY      int

	pad int
	// gutter is the strip reserved on the right edge for the scrollbar, so text
	// never runs under the bar.
	gutter int
	styles *StyleResolver

	// x, y, w, h is the pane rectangle the grid is clipped to.
	x, y, w, h int

	// cols, rows is the viewport size in whole cells.
	cols, rows int

	lines []row
	// damage marks rows the emulator changed since the last paint.
	damage []bool

	visible bool
	blink   bool
	shape   int
}

// row is one rendered row: the glyph bytes and the style runs over them.
type row struct {
	text  []byte
	runs  []run
	valid bool
}

// run is a horizontal span of cells sharing one style.
type run struct {
	cells int
	off   int
	n     int
	style Style
}

// NewGrid creates a grid renderer for one terminal.
func NewGrid(term vt.Terminal, pad int, styles *StyleResolver, mono Metrics) *Grid {
	g := &Grid{term: term, pad: pad, styles: styles}
	g.SetMetrics(mono)
	return g
}

// SetMetrics records the cell geometry and recomputes the viewport size for the
// current rectangle. Called when the font or DPI changes.
func (g *Grid) SetMetrics(m Metrics) {
	if m.CellW <= 0 {
		return
	}
	g.cellW = m.CellW
	g.cellH = m.LineH
	if g.cellH <= 0 {
		g.cellH = m.TextH()
	}
	// Centre the font's line box in the cell, the same way a browser-based
	// terminal does. The config's line height is usually tighter than the
	// font's natural height, so this offset is legitimately negative: the
	// glyphs overflow the cell slightly, which is what keeps descenders from
	// being clipped at the bottom while the text still advances by lineHeight.
	g.textTop = (g.cellH - m.TextH()) / 2
	g.cols, g.rows = 0, 0 // force a resize so the grid is recomputed
	g.resize()
}

// SetBounds moves and resizes the grid. The grid never exceeds the rectangle,
// so a pane smaller than one cell renders nothing.
func (g *Grid) SetBounds(x, y, w, h int) {
	if x == g.x && y == g.y && w == g.w && h == g.h {
		return
	}
	g.x, g.y, g.w, g.h = x, y, w, h
	g.resize()
}

// SetGutter reserves a strip on the right edge for a scrollbar. The grid keeps
// its full rectangle; only the cell area shrinks, so the bar is drawn in the
// reserved strip rather than over the last column.
func (g *Grid) SetGutter(n int) {
	if n < 0 {
		n = 0
	}
	if n == g.gutter {
		return
	}
	g.gutter = n
	g.resize()
}

// SetPadding changes the pane's inner padding. The grid is re-measured, so a
// DPI change moves the text block with the pane instead of leaving the old
// inset behind.
func (g *Grid) SetPadding(pad int) {
	if pad == g.pad {
		return
	}
	g.pad = pad
	g.resize()
}

// SetVisible marks whether the pane is on screen. A hidden pane is never
// painted, but its emulator keeps parsing output so its scrollback stays
// current — the invariant that keeps a tab switch from blanking a terminal.
func (g *Grid) SetVisible(v bool) { g.visible = v }

// Visible reports whether the pane is on screen.
func (g *Grid) Visible() bool { return g.visible }

// Terminal is the emulator this grid renders.
func (g *Grid) Terminal() vt.Terminal { return g.term }

// Size reports the terminal size in cells.
func (g *Grid) Size() (cols, rows int) { return g.cols, g.rows }

// Bounds reports the pane rectangle.
func (g *Grid) Bounds() (x, y, w, h int) { return g.x, g.y, g.w, g.h }

// Origin is the pixel position of cell (0,0).
func (g *Grid) Origin() (x, y int) { return g.originX, g.originY }

// CellWidth and CellHeight are the metrics every cell uses.
func (g *Grid) CellWidth() int  { return g.cellW }
func (g *Grid) CellHeight() int { return g.cellH }

// CellRect maps a cell to a rectangle in surface coordinates.
func (g *Grid) CellRect(cx, cy int) (x, y, w, h int) {
	return g.originX + cx*g.cellW, g.originY + cy*g.cellH, g.cellW, g.cellH
}

// CellAt maps a surface point to a viewport cell. It reports false for a point
// outside the grid or on a trailing partial cell, so a click past the last
// column does not select a cell that does not exist.
func (g *Grid) CellAt(px, py int) (cx, cy int, ok bool) {
	if g.cellW <= 0 || g.cellH <= 0 || g.cols == 0 || g.rows == 0 {
		return 0, 0, false
	}
	cx = (px - g.originX) / g.cellW
	cy = (py - g.originY) / g.cellH
	if px < g.originX || py < g.originY || cx < 0 || cy < 0 || cx >= g.cols || cy >= g.rows {
		return 0, 0, false
	}
	return cx, cy, true
}

// SetCursorShape records the cursor style and whether the blink phase is on.
func (g *Grid) SetCursorShape(blink bool, shape int) {
	g.blink, g.shape = blink, shape
}

// resize recomputes the cell grid from the pixel bounds and resizes the
// emulator to match.
func (g *Grid) resize() {
	if g.cellW <= 0 || g.cellH <= 0 {
		return
	}
	innerW := g.w - 2*g.pad - g.gutter
	innerH := g.h - 2*g.pad
	if innerW < g.cellW || innerH < g.cellH {
		g.originX, g.originY = g.x+g.pad, g.y+g.pad
		if g.cols != 0 || g.rows != 0 {
			g.cols, g.rows = 0, 0
			g.lines = g.lines[:0]
			g.damage = g.damage[:0]
		}
		return
	}
	cols := innerW / g.cellW
	rows := innerH / g.cellH
	// Centre the block of cells so the leftover pixels are split evenly rather
	// than all landing on the right and bottom edges.
	g.originX = g.x + g.pad + (innerW-cols*g.cellW)/2
	g.originY = g.y + g.pad + (innerH-rows*g.cellH)/2
	if cols == g.cols && rows == g.rows {
		return
	}
	g.cols, g.rows = cols, rows
	g.realloc()
	g.term.Resize(cols, rows)
}

func (g *Grid) realloc() {
	if cap(g.lines) < g.rows {
		g.lines = make([]row, g.rows)
	} else {
		g.lines = g.lines[:g.rows]
	}
	for i := range g.lines {
		g.lines[i].valid = false
	}
	if cap(g.damage) < g.rows {
		g.damage = make([]bool, g.rows)
	} else {
		g.damage = g.damage[:g.rows]
	}
	for i := range g.damage {
		g.damage[i] = true
	}
}

// Invalidate forces every row to be rebuilt on the next paint. Called after a
// theme or font change, when the cached glyphs and colours are both stale.
func (g *Grid) Invalidate() {
	for i := range g.lines {
		g.lines[i].valid = false
		g.damage[i] = true
	}
}

// damageLines merges the emulator's dirty set into the grid's.
func (g *Grid) damageLines() {
	y0, y1, anyDirty := g.term.DirtyLines()
	if !anyDirty {
		return
	}
	if y0 < 0 {
		y0 = 0
	}
	if y1 >= len(g.damage) {
		y1 = len(g.damage) - 1
	}
	for y := y0; y <= y1; y++ {
		g.damage[y] = true
	}
}

// Paint draws the grid into the surface.
func (g *Grid) Paint(s Surface, bg Color) {
	if !g.visible || g.cols == 0 || g.rows == 0 {
		return
	}
	// The surface keeps a selected font between calls and it is set by map
	// iteration order, so the terminal font is selected here rather than assumed.
	s.SetFont(FontMono)
	s.SaveClip()
	defer s.RestoreClip()
	if !s.Clip(g.x, g.y, g.w, g.h) {
		return
	}

	// The surface is a reused bitmap, so the pane must be cleared before the
	// rows are drawn over it; rows then only fill the cells that differ from
	// the theme background.
	s.Fill(g.x, g.y, g.w, g.h, bg)

	// Everything below reads the emulator, so it runs under the lock. Unlock
	// does not reset damage — the renderer owns that — which is why the damage
	// is consumed explicitly after the rows are built.
	g.term.Lock()
	g.damageLines()

	cursorX, cursorY, cursorVisible := g.term.CursorCell()
	if !g.blink && blinkingShape(g.shape) {
		cursorVisible = false
	}

	for y := 0; y < g.rows; y++ {
		if g.damage[y] || !g.lines[y].valid {
			g.buildRow(y)
		}
		g.paintRow(s, y, bg)
	}

	if cursorVisible {
		g.paintCursor(s, cursorX, cursorY)
	}

	g.term.ClearDamage()
	for i := range g.damage {
		g.damage[i] = false
	}
	g.term.Unlock()
}

// Selection is the selection highlight colour.
func (g *Grid) Selection() Color { return g.styles.Selection() }

// CursorFits reports whether a block cursor can be drawn over the whole cell at
// (x, y) without swallowing half of a wide glyph. A block over the head of a
// wide glyph would cover one and a half cells and hide the character whose tail
// continues into the next one.
func (g *Grid) CursorFits(x, y int) bool {
	if x < 0 || y < 0 || y >= g.rows {
		return false
	}
	row, ok := g.term.ViewRow(y)
	if !ok || x >= len(row) {
		return false
	}
	return row[x].Mode&vt.AttrWide == 0
}

// buildRow reads one viewport row out of the emulator and converts it into the
// bytes and style runs the surface draws.
//
// A run is a maximal span of cells sharing one style **and** made only of
// single-width glyphs. A wide glyph always gets a run of its own, drawn at its
// exact cell position: a run's text is drawn as a single ExtTextOutW call, so a
// wide character inside it would advance by whatever the font's fallback face
// decides — which need not be two cells — and the rest of the row would drift.
func (g *Grid) buildRow(y int) {
	line := row{text: g.lines[y].text[:0], runs: g.lines[y].runs[:0], valid: true}
	view, ok := g.term.ViewRow(y)
	if !ok || len(view) == 0 {
		line.text = append(line.text, ' ')
		line.runs = append(line.runs, run{cells: g.cols, n: 1, style: g.styles.Default()})
		g.lines[y] = line
		return
	}

	// cur is the run currently open: the cells it covers, where its text starts
	// in the row buffer, and whether it is part of a wide glyph.
	var cur struct {
		start, off int
		style      Style
		wide       bool
	}

	// emit closes the open run at cell end.
	emit := func(end int) {
		if end > cur.start {
			line.runs = append(line.runs, run{
				cells: end - cur.start,
				off:   cur.off,
				n:     len(line.text) - cur.off,
				style: cur.style,
			})
		}
		cur.start, cur.off, cur.wide = end, len(line.text), false
	}

	for col := 0; col < g.cols; col++ {
		var glyph vt.Glyph
		if col < len(view) {
			glyph = view[col]
		} else {
			glyph = vt.Glyph{Char: ' '}
		}
		if glyph.Mode&vt.AttrWideTail != 0 {
			// The continuation cell belongs to the wide run that owns it.
			// Opening a run here would repaint a background over the right half
			// of the glyph; skipping it is what makes the pair cost two cells.
			continue
		}

		st := g.styles.Resolve(glyph)
		isWide := glyph.Mode&vt.AttrWide != 0
		if col > cur.start && (cur.wide || isWide || st != cur.style) {
			emit(col)
		}
		cur.style = st

		if glyph.Char == 0 {
			line.text = append(line.text, ' ')
		} else {
			line.text = appendRune(line.text, glyph.Char)
		}

		if isWide {
			// Close the wide glyph as its own run immediately, so nothing that
			// follows can be appended to it.
			emit(col + 2)
			continue
		}
		cur.wide = false
	}
	emit(g.cols)

	g.lines[y] = line
}

// paintRow draws one built row.
func (g *Grid) paintRow(s Surface, y int, bg Color) {
	line := &g.lines[y]
	py := g.originY + y*g.cellH
	col := 0
	for _, r := range line.runs {
		rx := g.originX + col*g.cellW
		rw := r.cells * g.cellW
		if r.style.BG != bg {
			s.Fill(rx, py, rw, g.cellH, r.style.BG)
		}
		if r.n > 0 {
			s.Text(rx, py+g.textTop, string(line.text[r.off:r.off+r.n]), r.style)
		}
		col += r.cells
	}
}

// paintCursor draws the cursor over its cell.
func (g *Grid) paintCursor(s Surface, x, y int) {
	cx, cy, w, h := g.CellRect(x, y)
	switch g.shape {
	case vt.CursorBar, vt.CursorBarBlink:
		s.Fill(cx, cy, max(1, w/8), h, g.styles.Cursor())
	case vt.CursorUnderline, vt.CursorUnderlineBlink:
		th := max(1, h/12)
		s.Fill(cx, cy+h-th, w, th, g.styles.Cursor())
	default:
		s.Fill(cx, cy, w, h, g.styles.Cursor())
		if row, ok := g.term.ViewRow(y); ok && x < len(row) {
			glyph := row[x]
			ch := glyph.Char
			if ch <= ' ' || glyph.Mode&vt.AttrWideTail != 0 {
				return
			}
			s.Text(cx, cy+g.textTop, string(ch), g.styles.ResolveCursorText(glyph))
		}
	}
}

// blinkingShape reports whether a cursor style blinks.
func blinkingShape(shape int) bool {
	switch shape {
	case vt.CursorBlockBlink, vt.CursorUnderlineBlink, vt.CursorBarBlink:
		return true
	}
	return false
}

func appendRune(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, byte(0xC0|r>>6), byte(0x80|r&0x3F))
	case r < 0x10000:
		return append(dst, byte(0xE0|r>>12), byte(0x80|(r>>6)&0x3F), byte(0x80|r&0x3F))
	default:
		return append(dst, byte(0xF0|r>>18), byte(0x80|(r>>12)&0x3F), byte(0x80|(r>>6)&0x3F), byte(0x80|r&0x3F))
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
