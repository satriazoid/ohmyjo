package vt

// ANSI color values
const (
	Black Color = iota
	Red
	Green
	Yellow
	Blue
	Magenta
	Cyan
	LightGrey
	DarkGrey
	LightRed
	LightGreen
	LightYellow
	LightBlue
	LightMagenta
	LightCyan
	White
)

// Default colors are potentially distinct to allow for special behavior.
// For example, a transparent background. Otherwise, the simple case is to
// map default colors to another color.
const (
	DefaultFG Color = 1<<24 + iota
	DefaultBG
	DefaultCursor
)

// Color maps to the ANSI colors [0, 16), the xterm colors [16, 256), the
// special Default* values at 1<<24, and direct RGB values tagged with rgbBit.
//
// The tag matters: without it, direct RGB #000000 (0,0,0) is indistinguishable
// from palette index 0, and #0000ff from index 255. A terminal that renders
// `\x1b[48;2;0;0;0m` black instead of the theme's black, or `\x1b[38;2;0;0;255m`
// as a palette entry, is exactly that collision. Direct RGB therefore lives in
// its own range, where the low 24 bits stay the r/g/b bytes.
type Color uint32

// rgbBit tags a Color as a direct RGB value rather than a palette index. It is
// above every palette index and below the Default* range.
const rgbBit Color = 1 << 25

// RGBColor builds a direct RGB colour.
func RGBColor(r, g, b int) Color {
	return rgbBit | Color(r&0xff)<<16 | Color(g&0xff)<<8 | Color(b&0xff)
}

// RGB reports whether c is a direct RGB value and returns its components.
func (c Color) RGB() (r, g, b int, ok bool) {
	if c&rgbBit == 0 {
		return 0, 0, 0, false
	}
	return int(c>>16) & 0xff, int(c>>8) & 0xff, int(c) & 0xff, true
}

// ANSI returns true if Color is within [0, 16).
func (c Color) ANSI() bool {
	return (c < 16)
}
