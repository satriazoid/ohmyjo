// Package ui implements the native window: a Win32 shell that renders terminal
// grids and chrome directly with GDI, with no browser engine involved.
package ui

// Color is a 24-bit RGB colour.
type Color uint32

func RGB(r, g, b uint8) Color { return Color(r)<<16 | Color(g)<<8 | Color(b) }

func (c Color) R() uint8 { return uint8(c >> 16) }
func (c Color) G() uint8 { return uint8(c >> 8) }
func (c Color) B() uint8 { return uint8(c) }

// Mix blends c towards other by t (0..1); used for the dimmed/alt surfaces
// derived from a theme instead of hardcoding extra theme keys.
func (c Color) Mix(other Color, t float64) Color {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }
	return RGB(mix(c.R(), other.R()), mix(c.G(), other.G()), mix(c.B(), other.B()))
}

// FontID names a font slot configured on a Surface.
type FontID int

// Metrics is the font geometry in physical pixels.
type Metrics struct {
	// CellW is the advance width of one character.
	CellW int
	// Ascent and Descent bound the glyphs around the text baseline.
	Ascent, Descent int
	// LineH is the row advance: the font height adjusted by the configured
	// line height, so the caller decides the leading.
	LineH int
}

// TextH is the height of a glyph box.
func (m Metrics) TextH() int { return m.Ascent + m.Descent }

// Cursor is the pixel rectangle of the text cursor, reported so the caller can
// place an IME candidate window over it.
type Cursor struct {
	X, Y, W, H int
}

// Style is the appearance of a run of text. BG is the colour of the cells the
// run covers, so a run carries its own background and the surface does not need
// a separate fill call per cell.
type Style struct {
	FG        Color
	BG        Color
	Bold      bool
	Italic    bool
	Underline bool
	Strikeout bool
}

// Surface is the minimal drawing interface the UI needs. Keeping it abstract
// means the layout and painting code is testable without a window, and that a
// different backend (Direct2D, an offscreen buffer) can be dropped in.
//
// All coordinates are physical pixels in the surface's own space.
type Surface interface {
	// Fill paints a solid rectangle.
	Fill(x, y, w, h int, c Color)
	// Text draws a single line of text with its top-left at (x, y).
	Text(x, y int, s string, st Style)
	// TextWidth measures a single line of text.
	TextWidth(s string) int
	// SetFont selects a font slot and returns its metrics.
	SetFont(id FontID) Metrics
	// Clip restricts subsequent drawing to the rectangle, or to the whole
	// surface when w or h is non-positive. SaveClip/RestoreClip nest. It
	// reports whether the rectangle overlaps the surface, so a caller can skip
	// drawing entirely.
	Clip(x, y, w, h int) bool
	SaveClip()
	RestoreClip()
	// Sync presents the accumulated drawing.
	Sync()
}
