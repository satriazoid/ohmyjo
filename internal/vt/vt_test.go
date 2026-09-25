package vt

import (
	"strings"
	"testing"
)

// newTestTerm returns a sized terminal with scrollback, plus a helper to feed
// it text.
func newTestTerm(cols, rows int) *terminal {
	return newTerminal(TerminalInfo{w: nil, cols: cols, rows: rows})
}

// lineText renders any Line as a string, trailing blanks trimmed.
func lineText(l Line) string {
	var b strings.Builder
	for _, g := range l {
		if g.Char == 0 {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(g.Char)
	}
	return strings.TrimRight(b.String(), " ")
}

// rowText renders one view row as a string, trailing blanks trimmed.
func rowText(t *terminal, y int) string {
	l, ok := t.ViewRow(y)
	if !ok {
		return ""
	}
	return lineText(l)
}

func TestScrollbackAccumulatesLines(t *testing.T) {
	// 3 rows tall. Five CRLF-terminated lines: the trailing CRLF of lines 2,
	// 3 and 4 each scroll a line off, so history holds line0..line2 and the
	// screen holds line3, line4 and a blank row.
	term := newTestTerm(20, 3)
	term.SetScrollbackSize(100)
	for i := range 5 {
		term.Write([]byte("line" + string(rune('0'+i)) + "\r\n"))
	}

	if got := term.ScrollbackLen(); got != 3 {
		t.Fatalf("ScrollbackLen = %d, want 3", got)
	}
	if got := lineText(term.ScrollbackLine(0)); got != "line0" {
		t.Errorf("oldest history line = %q, want line0", got)
	}
	if got := lineText(term.ScrollbackLine(2)); got != "line2" {
		t.Errorf("newest history line = %q, want line2", got)
	}
	// Live screen after the three scrolls: line3, line4, then a blank row.
	if got := rowText(term, 0); got != "line3" {
		t.Errorf("screen row 0 = %q, want line3", got)
	}
	if got := rowText(term, 1); got != "line4" {
		t.Errorf("screen row 1 = %q, want line4", got)
	}
	if got := rowText(term, 2); got != "" {
		t.Errorf("screen row 2 = %q, want blank", got)
	}
	// History plus the live rows is the whole session, in order, which is what
	// a scrolling viewer walks.
	if got := term.TotalRows(); got != 6 {
		t.Errorf("TotalRows = %d, want 6", got)
	}
}

func TestScrollbackRingEvictsOldest(t *testing.T) {
	term := newTestTerm(20, 2)
	term.SetScrollbackSize(3)
	for i := range 8 {
		term.Write([]byte("l" + string(rune('0'+i)) + "\r\n"))
	}
	// 8 CRLF-terminated lines on 2 rows scroll 7 lines off; only the newest 3
	// survive the cap.
	if got := term.ScrollbackLen(); got != 3 {
		t.Fatalf("ScrollbackLen = %d, want 3", got)
	}
	want := []rune{'4', '5', '6'}
	for i, w := range want {
		if g := term.ScrollbackLine(i)[1].Char; g != w {
			t.Errorf("history[%d] = l%c, want l%c", i, g, w)
		}
	}
}

func TestScrollbackDisabledKeepsNothing(t *testing.T) {
	term := newTestTerm(20, 2)
	term.SetScrollbackSize(0)
	for i := range 5 {
		term.Write([]byte("l" + string(rune('0'+i)) + "\r\n"))
	}
	if got := term.ScrollbackLen(); got != 0 {
		t.Errorf("ScrollbackLen = %d with scrollback disabled, want 0", got)
	}
}

func TestAlternateScreenDoesNotPolluteScrollback(t *testing.T) {
	term := newTestTerm(20, 2)
	term.SetScrollbackSize(100)
	term.Write([]byte("a\r\nb\r\n"))
	before := term.ScrollbackLen()

	// Enter the alternate screen, as a full-screen app such as vim would.
	term.Write([]byte("\x1b[?1049h"))
	for i := range 6 {
		term.Write([]byte("alt" + string(rune('0'+i)) + "\r\n"))
	}
	if got := term.ScrollbackLen(); got != before {
		t.Errorf("alternate screen added %d lines to scrollback", got-before)
	}

	// Leaving restores the primary screen with its history intact.
	term.Write([]byte("\x1b[?1049l"))
	if got := term.ScrollbackLen(); got != before {
		t.Errorf("after leaving alt screen ScrollbackLen = %d, want %d", got, before)
	}
}

func TestScrollRegionScrollDoesNotPolluteScrollback(t *testing.T) {
	term := newTestTerm(20, 5)
	term.SetScrollbackSize(100)
	term.Write([]byte("one\r\ntwo\r\nthree\r\nfour\r\nfive"))
	// A scrolling region that does not span the screen (DECSTBM, as used by
	// pagers) must not push lines into history.
	term.Write([]byte("\x1b[1;3r"))
	term.Write([]byte("\x1b[3;1H\x1bM")) // scroll region up by one
	if got := term.ScrollbackLen(); got != 0 {
		t.Errorf("scrolling region added %d lines to scrollback, want 0", got)
	}
}

func TestViewportScrollShowsHistory(t *testing.T) {
	term := newTestTerm(20, 3)
	term.SetScrollbackSize(100)
	for i := range 6 {
		term.Write([]byte("l" + string(rune('0'+i)) + "\r\n"))
	}
	// Six CRLF-terminated lines on three rows: four lines have scrolled off
	// (l0..l3) and the screen shows l4, l5, blank.
	if got := rowText(term, 0); got != "l4" {
		t.Fatalf("live row 0 = %q, want l4", got)
	}
	if got := term.ScrollbackLen(); got != 4 {
		t.Fatalf("ScrollbackLen = %d, want 4", got)
	}
	if got := term.ScrollUp(1); got != 1 {
		t.Fatalf("ScrollUp(1) = %d, want 1", got)
	}
	// Scrolled back one line: row 0 shows the newest history line, l3.
	if got := rowText(term, 0); got != "l3" {
		t.Errorf("scrolled row 0 = %q, want l3", got)
	}
	if _, _, vis := term.CursorCell(); vis {
		t.Error("cursor reported visible while scrolled back")
	}
	// Scrolling forward past the end clamps to the oldest retained line.
	if got := term.ScrollUp(999); got != 4 {
		t.Errorf("ScrollUp(999) = %d, want clamp to 4", got)
	}
	if got := rowText(term, 0); got != "l0" {
		t.Errorf("fully scrolled row 0 = %q, want l0", got)
	}
	if got := term.ScrollTo(0); got != 0 {
		t.Errorf("ScrollTo(0) = %d, want 0", got)
	}
	if got := rowText(term, 0); got != "l4" {
		t.Errorf("back to live row 0 = %q, want l4", got)
	}
}

func TestDirtyTrackingMarksChangedRowsOnly(t *testing.T) {
	term := newTestTerm(20, 4)
	// A fresh terminal reports every row dirty so the first paint fills the
	// window; that is the baseline, not damage from output.
	term.Lock()
	if _, _, any := term.DirtyLines(); !any {
		t.Error("fresh terminal reported no damage, so it would never be painted")
	}
	term.ClearDamage()
	term.Unlock()

	term.Write([]byte("hello"))
	if !term.DirtyRow(0) {
		t.Error("row 0 not dirty after write")
	}
	if term.DirtyRow(3) {
		t.Error("row 3 dirty without being written to")
	}
	if y0, y1, any := term.DirtyLines(); !any || y0 != 0 || y1 != 0 {
		t.Errorf("DirtyLines() = (%d,%d,%v), want only row 0", y0, y1, any)
	}
	// ClearDamage resets damage, matching the renderer contract: read a frame
	// under Lock, paint, then ClearDamage.
	term.Lock()
	term.ClearDamage()
	term.Unlock()
	if _, _, any := term.DirtyLines(); any {
		t.Error("damage survived ClearDamage")
	}
}

func TestResizeKeepsHistory(t *testing.T) {
	term := newTestTerm(20, 3)
	term.SetScrollbackSize(100)
	for i := range 6 {
		term.Write([]byte("l" + string(rune('0'+i)) + "\r\n"))
	}
	have := term.ScrollbackLen()
	term.Resize(10, 2)
	if got := term.ScrollbackLen(); got != have {
		t.Errorf("resize changed history: %d -> %d", have, got)
	}
	// History lines keep their original width so text is not reflowed into
	// garbage; the new columns read as blanks.
	if got := lineText(term.ScrollbackLine(0)); got != "l0" {
		t.Errorf("history line after narrow resize = %q, want %q", got, "l0")
	}
	if got := len(term.ScrollbackLine(0)); got != 20 {
		t.Errorf("history line width = %d, want original 20", got)
	}
}

func TestTruecolorSurvivesToCell(t *testing.T) {
	term := newTestTerm(20, 2)
	term.Write([]byte("\x1b[38;2;122;162;247mX\x1b[0m"))
	g := term.Cell(0, 0)
	if g.Char != 'X' {
		t.Fatalf("Cell(0,0).Char = %q, want X", g.Char)
	}
	if want := RGBColor(122, 162, 247); g.FG != want {
		t.Errorf("FG = %#x, want %#x", uint32(g.FG), uint32(want))
	}
}

// TestTruecolorDoesNotCollideWithPaletteIndex pins the representation that
// makes direct RGB distinguishable from a palette index. Encoding RGB as
// r<<16|g<<8|b aliases #000000 with palette index 0 and #0000ff with index 255,
// so `\x1b[48;2;0;0;0m` would paint the theme's palette black and
// `\x1b[38;2;0;0;255m` would come out as a palette entry.
func TestTruecolorDoesNotCollideWithPaletteIndex(t *testing.T) {
	term := newTestTerm(20, 2)
	term.Write([]byte("\x1b[48;2;0;0;0m\x1b[38;2;0;0;255mX"))
	g := term.Cell(0, 0)
	if r, gg, b, ok := g.BG.RGB(); !ok || r != 0 || gg != 0 || b != 0 {
		t.Errorf("BG = %#x, want direct RGB 0,0,0", uint32(g.BG))
	}
	if r, gg, b, ok := g.FG.RGB(); !ok || r != 0 || gg != 0 || b != 255 {
		t.Errorf("FG = %#x, want direct RGB 0,0,255", uint32(g.FG))
	}
	// A palette index must still be read as an index, not as RGB.
	term.Write([]byte("\x1b[0m"))
	if _, _, _, ok := term.Cell(1, 0).FG.RGB(); ok {
		t.Error("default foreground reported as direct RGB")
	}
	if got := term.Cell(1, 0).FG; got != DefaultFG {
		t.Errorf("default FG = %#x, want DefaultFG", uint32(got))
	}
}

func TestExportedAttributesAreReadable(t *testing.T) {
	term := newTestTerm(20, 2)
	term.Write([]byte("\x1b[1mB\x1b[0m\x1b[4mU\x1b[0m"))
	if g := term.Cell(0, 0); g.Mode&AttrBold == 0 {
		t.Errorf("bold cell Mode = %#x, want AttrBold set", g.Mode)
	}
	if g := term.Cell(1, 0); g.Mode&AttrUnderline == 0 {
		t.Errorf("underline cell Mode = %#x, want AttrUnderline set", g.Mode)
	}
}

func TestCursorCellHiddenWhenScrolledBack(t *testing.T) {
	term := newTestTerm(20, 3)
	term.SetScrollbackSize(100)
	term.Write([]byte("a\r\nb\r\nc\r\nd\r\n"))
	x, y, vis := term.CursorCell()
	if !vis {
		t.Fatal("cursor not visible on live view")
	}
	if x != 0 || y != 2 {
		t.Errorf("cursor at (%d,%d), want (0,2)", x, y)
	}
	term.ScrollUp(1)
	if _, _, vis := term.CursorCell(); vis {
		t.Error("cursor visible while scrolled back")
	}
}

func TestClearScrollback(t *testing.T) {
	term := newTestTerm(20, 2)
	term.SetScrollbackSize(100)
	for i := range 6 {
		term.Write([]byte("l" + string(rune('0'+i)) + "\r\n"))
	}
	term.ScrollUp(2)
	term.ClearScrollback()
	if got := term.ScrollbackLen(); got != 0 {
		t.Errorf("ScrollbackLen after clear = %d, want 0", got)
	}
	if got := term.ScrollOffset(); got != 0 {
		t.Errorf("scroll offset after clear = %d, want 0", got)
	}
}

func TestWideRunesOccupyTwoCells(t *testing.T) {
	term := New(WithSize(10, 3))
	term.Write([]byte("a\u4e2db"))

	// "a" at 0, "中" spanning 1 and 2, "b" at 3.
	if got := term.Cell(0, 0).Char; got != 'a' {
		t.Errorf("cell(0,0) = %q, want 'a'", got)
	}
	if got := term.Cell(1, 0).Char; got != '\u4e2d' {
		t.Errorf("cell(1,0) = %q, want the wide rune", got)
	}
	if got := term.Cell(1, 0).Mode; got&AttrWide == 0 {
		t.Errorf("cell(1,0) mode = %b, want AttrWide set", got)
	}
	if got := term.Cell(2, 0).Mode; got&AttrWideTail == 0 {
		t.Errorf("cell(2,0) mode = %b, want AttrWideTail set", got)
	}
	// The tail cell must carry no glyph of its own.
	if got := term.Cell(2, 0).Char; got != ' ' {
		t.Errorf("tail cell char = %q, want a blank", got)
	}
	// "b" must land at column 3: treating the wide rune as one cell would put
	// it at 2 and shift the whole rest of the row.
	if got := term.Cell(3, 0).Char; got != 'b' {
		t.Errorf("cell(3,0) = %q, want 'b'", got)
	}
	if c := term.Cursor(); c.X != 4 {
		t.Errorf("cursor x = %d, want 4", c.X)
	}
}

func TestCombiningMarkDoesNotAdvanceCursor(t *testing.T) {
	term := New(WithSize(10, 3))
	// "e" followed by U+0301 COMBINING ACUTE ACCENT.
	term.Write([]byte("e\u0301x"))

	if got := term.Cell(0, 0).Char; got != 'e' {
		t.Errorf("cell(0,0) = %q, want 'e'", got)
	}
	// The mark consumed no cell, so "x" is at column 1, not 2.
	if got := term.Cell(1, 0).Char; got != 'x' {
		t.Errorf("cell(1,0) = %q, want 'x'", got)
	}
}

func TestOverwritingWideGlyphClearsBothCells(t *testing.T) {
	term := New(WithSize(10, 3))
	term.Write([]byte("\u4e2d"))
	// Overwrite only the head.
	term.Write([]byte("\x1b[1;1H"))
	term.Write([]byte("z"))

	if got := term.Cell(0, 0).Char; got != 'z' {
		t.Errorf("cell(0,0) = %q, want 'z'", got)
	}
	// The orphaned tail must be gone, or the row would render a stray cell.
	if m := term.Cell(1, 0).Mode; m&AttrWideTail != 0 {
		t.Errorf("cell(1,0) mode = %b, want AttrWideTail cleared", m)
	}
	if got := term.Cell(1, 0).Char; got != ' ' {
		t.Errorf("cell(1,0) = %q, want a blank", got)
	}

	// Now overwrite only the tail; the head must be dropped too.
	term2 := New(WithSize(10, 3))
	term2.Write([]byte("\u4e2d"))
	term2.Write([]byte("\x1b[1;2H"))
	term2.Write([]byte("q"))
	if m := term2.Cell(0, 0).Mode; m&AttrWide != 0 {
		t.Errorf("cell(0,0) mode = %b, want AttrWide cleared", m)
	}
	if got := term2.Cell(0, 0).Char; got != ' ' {
		t.Errorf("cell(0,0) = %q, want a blank", got)
	}
}

func TestWideGlyphWrapsInsteadOfSplitting(t *testing.T) {
	term := New(WithSize(4, 3))
	// Columns 0,1,2 hold "abc"; a wide rune cannot fit in the single remaining
	// column, so it must move to the next row rather than straddle the edge.
	term.Write([]byte("abc\u4e2d"))

	if got := term.Cell(3, 0).Char; got != ' ' {
		t.Errorf("cell(3,0) = %q, want a blank (the wide rune must not split)", got)
	}
	if got := term.Cell(0, 1).Char; got != '\u4e2d' {
		t.Errorf("cell(0,1) = %q, want the wide rune on the next row", got)
	}
}

func TestRuneWidthTable(t *testing.T) {
	cases := []struct {
		r    rune
		want int
	}{
		{'a', 1}, {'Z', 1}, {'~', 1}, {'é', 1},
		{'\u4e2d', 2},     // CJK unified ideograph
		{'\u3042', 2},     // hiragana
		{'\uac00', 2},     // hangul syllable
		{'\uff21', 2},     // fullwidth Latin capital A
		{'\U0001f600', 2}, // emoji
		{'\u0301', 0},     // combining acute accent
		{'\u200b', 0},     // zero width space
		{'\ufe0f', 0},     // variation selector-16
	}
	for _, tc := range cases {
		if got := RuneWidth(tc.r); got != tc.want {
			t.Errorf("RuneWidth(%U) = %d, want %d", tc.r, got, tc.want)
		}
	}
	if got := StringWidth("a\u4e2d\u0301b"); got != 4 {
		t.Errorf("StringWidth = %d, want 4", got)
	}
}
