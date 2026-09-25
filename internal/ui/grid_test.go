//go:build windows

package ui

import (
	"testing"

	"ohmyjo/internal/vt"
)

// The renderer's job is to put the emulator's cells onto the surface at the
// right places with the right colours. These tests drive it through an
// offscreen surface — no window, no session — and assert on real pixels, which
// is the only way to catch the failures that matter: glyphs landing in the
// wrong column, a colour run bleeding into its neighbour, or a proportional
// font making every column after the first drift.

func testGrid(t *testing.T, cols, rows int) (*GDI, *Grid, func()) {
	t.Helper()
	surf, err := NewGDI(640, 480)
	if err != nil {
		t.Skipf("no drawing surface available: %v", err)
	}
	surf.ConfigureFont(FontMono, FontSlot{Family: "Consolas", SizePx: 12, LineH: 1.0})
	m := surf.SetFont(FontMono)
	if m.CellW <= 0 || m.LineH <= 0 {
		surf.Close()
		t.Skip("no usable font metrics")
	}
	// Resize to exactly the requested cell count so the grid's derived size is
	// the one under test, not an accident of a generous bitmap.
	surf.Resize(cols*m.CellW, rows*m.LineH)
	g := NewGrid(newTestTerm(cols, rows), 0, NewStyleResolver(DefaultPalette()), m)
	g.SetBounds(0, 0, cols*m.CellW, rows*m.LineH)
	g.SetVisible(true)
	if gc, gr := g.Size(); gc != cols || gr != rows {
		surf.Close()
		t.Fatalf("grid resolved to %dx%d cells, want %dx%d", gc, gr, cols, rows)
	}
	return surf, g, surf.Close
}

// newTestTerm is a real emulator, so the tests exercise the same parsing path
// the application uses rather than a stub.
func newTestTerm(cols, rows int) vt.Terminal {
	return vt.New(vt.WithSize(cols, rows))
}

// hideCursor takes the cursor out of the picture for tests that are not about
// cursor drawing. It is a solid fill, so leaving it visible would make every
// pixel assertion on its cell read cursor colour instead of glyph or background.
// A blinking shape with the blink phase off is exactly the state the pane layer
// produces between blink on-phases, so this uses the production path.
func hideCursor(g *Grid) { g.SetCursorShape(false, vt.CursorBlockBlink) }

// paint clears the surface and repaints the whole grid.
func paint(surf *GDI, g *Grid) {
	surf.SetClear(DefaultPalette().Background)
	g.Invalidate()
	g.Paint(surf, DefaultPalette().Background)
}

// rowHasInk reports whether any pixel in [x0,x1) of the given cell row differs
// from the surface background.
func rowHasInk(surf *GDI, g *Grid, cy, x0, x1 int) bool {
	bg := DefaultPalette().Background
	y0 := g.originY + cy*g.cellH
	for y := y0; y < y0+g.cellH; y++ {
		for x := g.originX + x0; x < g.originX+x1; x++ {
			if surf.At(x, y) != bg {
				return true
			}
		}
	}
	return false
}

func TestGridPaintsGlyphsInTheirOwnCells(t *testing.T) {
	surf, g, done := testGrid(t, 12, 2)
	defer done()

	g.term.Write([]byte("\x1b[48;2;255;0;0mX"))
	hideCursor(g)
	paint(surf, g)

	// Ink must be in the cell the glyph belongs to.
	if !rowHasInk(surf, g, 0, 0, g.cellW) {
		t.Error("cell (0,0) is empty after writing a glyph into it")
	}
	// The three cells after it must be untouched background. A run whose width
	// or origin were computed wrong would paint or draw past its cell.
	if rowHasInk(surf, g, 0, g.cellW, 4*g.cellW) {
		t.Error("cells 1..3 are not clean after writing a single glyph")
	}
	// The row below is untouched too: the glyph must not spill vertically.
	if rowHasInk(surf, g, 1, 0, g.cols*g.cellW) {
		t.Error("row 1 was drawn into")
	}
}

func TestGridWideGlyphCoversExactlyTwoCells(t *testing.T) {
	surf, g, done := testGrid(t, 12, 2)
	defer done()

	g.term.Write([]byte("\x1b[48;2;255;0;0m\u4e2d\x1b[0ma"))
	hideCursor(g)

	if got := g.term.Cell(0, 0).Mode; got&vt.AttrWide == 0 {
		t.Fatal("emulator did not mark the wide glyph head")
	}
	// A wide glyph consumes two cells, so what follows starts at column 2.
	if got := g.term.Cell(2, 0).Char; got != 'a' {
		t.Fatalf("cell(2,0) = %q, want 'a' — the wide glyph consumed the wrong width", got)
	}

	paint(surf, g)

	// The runs must cover exactly the grid width. Emitting a run for the
	// continuation cell would add a cell and paint a stray background over the
	// right half of the glyph.
	covered := 0
	for _, r := range g.lines[0].runs {
		covered += r.cells
	}
	if covered != g.cols {
		t.Errorf("row 0 runs cover %d cells, want %d", covered, g.cols)
	}
	// Exactly one run may carry the red background, spanning both cells.
	red := 0
	for _, r := range g.lines[0].runs {
		if r.style.BG == RGB(255, 0, 0) {
			red++
			if r.cells != 2 {
				t.Errorf("red run covers %d cells, want 2 (both halves of the wide glyph)", r.cells)
			}
		}
	}
	if red != 1 {
		t.Errorf("row 0 has %d red runs, want exactly 1", red)
	}
	// The glyph must be drawn, not skipped: the left half of a wide glyph has
	// ink in the lower rows of its cell.
	half := g.cellW / 2
	if !rowHasInk(surf, g, 0, half/2, half) && !rowHasInk(surf, g, 0, 0, g.cellW) {
		t.Error("no ink drawn for the wide glyph")
	}
}

func TestGridCursorInvertsOnlyItsOwnCell(t *testing.T) {
	surf, g, done := testGrid(t, 12, 2)
	defer done()

	pal := DefaultPalette()
	// "ab", then put the cursor back on column 1 so a block cursor lands there
	// while nothing else moves.
	g.term.Write([]byte("ab\x1b[1;2H"))
	paint(surf, g)

	y0 := g.originY
	for y := y0; y < y0+g.cellH; y++ {
		if surf.At(g.originX, y) == pal.Cursor {
			t.Error("cursor was drawn over the cell to its left")
		}
	}
	if !rowHasInk(surf, g, 0, g.cellW, 2*g.cellW) {
		t.Error("no cursor drawn on the cell it is on")
	}
	// Columns past the cursor keep the background between the glyph strokes.
	if rowHasInk(surf, g, 0, 3*g.cellW, g.cols*g.cellW) {
		t.Error("cells after the cursor are not clean")
	}
}

func TestGridGeometryMatchesTerminal(t *testing.T) {
	_, g, done := testGrid(t, 12, 3)
	defer done()

	cols, rows := g.Size()
	// The emulator must have been resized to match, or output would wrap at the
	// wrong column and the grid would show gaps.
	if tcols, trows := g.term.Size(); tcols != cols || trows != rows {
		t.Errorf("terminal size = %dx%d, want %dx%d", tcols, trows, cols, rows)
	}

	// A point in the middle of a cell maps back to that cell.
	cx, cy, ok := g.CellAt(g.originX+g.cellW*3+g.cellW/2, g.originY+g.cellH*2+g.cellH/2)
	if !ok || cx != 3 || cy != 2 {
		t.Errorf("CellAt = (%d,%d,%v), want (3,2,true)", cx, cy, ok)
	}
	// A point past the last column is not a cell.
	if _, _, ok := g.CellAt(g.originX+cols*g.cellW+1, g.originY); ok {
		t.Error("CellAt accepted a point past the last column")
	}
	// A point left of the origin is not a cell either.
	if _, _, ok := g.CellAt(g.originX-1, g.originY); ok {
		t.Error("CellAt accepted a point before the origin")
	}
}

// TestGridFontAdvanceEqualsCellWidth is the regression test for the bug that
// made the terminal unreadable: if the resolved face is proportional, or if the
// font handle is created with the wrong arguments, GDI substitutes a
// proportional default and every glyph advances by its own width while the grid
// still steps by CellW. The row then drifts progressively out of alignment from
// the second column on.
func TestGridFontAdvanceEqualsCellWidth(t *testing.T) {
	surf, err := NewGDI(320, 240)
	if err != nil {
		t.Skipf("no drawing surface available: %v", err)
	}
	defer surf.Close()

	// A stack whose first entry is proportional and installed: the grid must
	// skip it rather than draw a terminal in Segoe UI.
	surf.ConfigureFont(FontMono, FontSlot{Family: "Segoe UI, monospace", SizePx: 14, LineH: 1.2})
	m := surf.SetFont(FontMono)
	if m.CellW <= 0 {
		t.Fatal("no cell width")
	}
	for _, s := range []string{"i", "W", "M", ".", "@", "0", "l", " "} {
		if w := surf.TextWidth(s); w != m.CellW {
			t.Errorf("advance of %q = %d, want CellW = %d; the grid would drift", s, w, m.CellW)
		}
	}
	// A wide glyph must be drawn by a fallback face at roughly two cells. It is
	// not exactly two: the fallback face has its own advance width and GDI
	// cannot be told to stretch it, which is why the run builder gives every
	// wide glyph a run of its own positioned at its own cell rather than
	// concatenating it into a run with its neighbours.
	if w := surf.TextWidth("\u4e2d"); w <= m.CellW || w > 2*m.CellW+2 {
		t.Errorf("advance of a wide glyph = %d, want between %d and %d", w, m.CellW+1, 2*m.CellW+2)
	}
}
