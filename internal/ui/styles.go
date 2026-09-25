//go:build windows

package ui

import "ohmyjo/internal/vt"

// StyleResolver turns emulator cell attributes into surface styles. It is the
// only place that knows how a theme's palette maps onto vt colour values, so
// the grid renderer never touches colours directly.
type StyleResolver struct {
	pal Palette
}

// NewStyleResolver builds a resolver over a palette.
func NewStyleResolver(pal Palette) *StyleResolver {
	return &StyleResolver{pal: pal}
}

// SetPalette swaps the theme. Cached rows must be invalidated by the caller.
func (r *StyleResolver) SetPalette(pal Palette) { r.pal = pal }

// Palette returns the current palette.
func (r *StyleResolver) Palette() Palette { return r.pal }

// Default is the style of an unstyled cell.
func (r *StyleResolver) Default() Style {
	return Style{FG: r.pal.Foreground, BG: r.pal.Background}
}

// color maps an emulator colour to a surface colour. The second result is
// false for the default foreground/background sentinels, which the caller
// resolves per slot.
func (r *StyleResolver) color(c vt.Color) (Color, bool) {
	if cr, cg, cb, ok := c.RGB(); ok {
		return RGB(uint8(cr), uint8(cg), uint8(cb)), true
	}
	if c.ANSI() {
		return r.pal.ANSI[c], true
	}
	return 0, false
}

// Resolve builds the draw style for one cell.
//
// The emulator has already applied reverse video and promoted bold ANSI colours
// to their bright variants, so this only has to map palette indices to colours
// and carry the font attributes across.
func (r *StyleResolver) Resolve(g vt.Glyph) Style {
	st := Style{
		FG:        r.pal.Foreground,
		BG:        r.pal.Background,
		Bold:      g.Mode&vt.AttrBold != 0,
		Italic:    g.Mode&vt.AttrItalic != 0,
		Underline: g.Mode&vt.AttrUnderline != 0,
	}
	if g.FG != vt.DefaultFG {
		if c, ok := r.color(g.FG); ok {
			st.FG = c
		}
	}
	if g.BG != vt.DefaultBG {
		if c, ok := r.color(g.BG); ok {
			st.BG = c
		}
	}
	return st
}

// ResolveCursorText is the style for the glyph under a block cursor: the
// foreground and background swapped, so the character stays readable against
// the cursor fill.
func (r *StyleResolver) ResolveCursorText(g vt.Glyph) Style {
	st := r.Resolve(g)
	st.FG, st.BG = r.pal.CursorText, r.pal.Cursor
	return st
}

// Cursor is the cursor fill colour.
func (r *StyleResolver) Cursor() Color { return r.pal.Cursor }

// Selection is the selection highlight colour.
func (r *StyleResolver) Selection() Color { return r.pal.Selection }
