package ui

import (
	"strconv"
	"strings"
)

// Palette is a theme resolved into colours the renderer can use directly. Hex
// strings are parsed once at load rather than per frame.
type Palette struct {
	// Terminal palette, indexed by the ANSI colour values 0..15.
	ANSI [16]Color

	Background Color
	Foreground Color
	Cursor     Color
	CursorText Color
	Selection  Color

	// Chrome surfaces.
	UIBackground    Color
	UIBackgroundAlt Color
	UIBorder        Color
	UIForeground    Color
	UIForegroundDim Color
	UIAccent        Color
	TabActive       Color
	TabInactive     Color
	PaneBorder      Color
	Splitter        Color
	Danger          Color
}

// DefaultPalette is the fallback when a theme is missing or unparsable. It is
// tokyo-night, the same default the config ships.
func DefaultPalette() Palette {
	p, err := PaletteFromHex(defaultThemeHex())
	if err != nil {
		// Every value in defaultThemeHex is a literal; a parse failure here is a
		// programming error, and the zero palette at least renders.
		return Palette{}
	}
	return p
}

// paletteSource is the subset of a theme the renderer needs, as hex strings.
// It mirrors config.Theme so this package does not depend on config, which
// keeps the renderer usable from tests and from the headless path.
type paletteSource struct {
	Background, Foreground, Cursor, CursorAccent, Selection string
	Black, Red, Green, Yellow, Blue, Magenta, Cyan, White   string
	BrightBlack, BrightRed, BrightGreen, BrightYellow       string
	BrightBlue, BrightMagenta, BrightCyan, BrightWhite      string
	UIBackground, UIBackgroundAlt, UIBorder                 string
	UIForeground, UIForegroundDim, UIAccent                 string
	TabActive, TabInactive, PaneBorder, Splitter            string
}

func defaultThemeHex() paletteSource {
	return paletteSource{
		Background: "#1a1b26", Foreground: "#c0caf5", Cursor: "#c0caf5",
		CursorAccent: "#1a1b26", Selection: "#33467c",
		Black: "#15161e", Red: "#f7768e", Green: "#9ece6a", Yellow: "#e0af68",
		Blue: "#7aa2f7", Magenta: "#bb9af7", Cyan: "#7dcfff", White: "#a9b1d6",
		BrightBlack: "#414868", BrightRed: "#f7768e", BrightGreen: "#9ece6a",
		BrightYellow: "#e0af68", BrightBlue: "#7aa2f7", BrightMagenta: "#bb9af7",
		BrightCyan: "#7dcfff", BrightWhite: "#c0caf5",
		UIBackground: "#16161e", UIBackgroundAlt: "#1a1b26", UIBorder: "#2f334d",
		UIForeground: "#c0caf5", UIForegroundDim: "#565f89", UIAccent: "#7aa2f7",
		TabActive: "#1a1b26", TabInactive: "#16161e", PaneBorder: "#2f334d",
		Splitter: "#3b4261",
	}
}

// PaletteFromTheme builds a palette from a config.Theme. Any missing or
// malformed colour falls back to the default theme's value for that slot, so a
// half-specified user theme still produces a coherent UI.
func PaletteFromTheme(t ThemeSource) Palette {
	d := defaultThemeHex()
	pick := func(v, def string) string {
		if strings.TrimSpace(v) == "" {
			return def
		}
		return v
	}
	return buildPalette(paletteSource{
		Background:      pick(t.Background, d.Background),
		Foreground:      pick(t.Foreground, d.Foreground),
		Cursor:          pick(t.Cursor, d.Cursor),
		CursorAccent:    pick(t.CursorAccent, d.CursorAccent),
		Selection:       pick(t.Selection, d.Selection),
		Black:           pick(t.Black, d.Black),
		Red:             pick(t.Red, d.Red),
		Green:           pick(t.Green, d.Green),
		Yellow:          pick(t.Yellow, d.Yellow),
		Blue:            pick(t.Blue, d.Blue),
		Magenta:         pick(t.Magenta, d.Magenta),
		Cyan:            pick(t.Cyan, d.Cyan),
		White:           pick(t.White, d.White),
		BrightBlack:     pick(t.BrightBlack, d.BrightBlack),
		BrightRed:       pick(t.BrightRed, d.BrightRed),
		BrightGreen:     pick(t.BrightGreen, d.BrightGreen),
		BrightYellow:    pick(t.BrightYellow, d.BrightYellow),
		BrightBlue:      pick(t.BrightBlue, d.BrightBlue),
		BrightMagenta:   pick(t.BrightMagenta, d.BrightMagenta),
		BrightCyan:      pick(t.BrightCyan, d.BrightCyan),
		BrightWhite:     pick(t.BrightWhite, d.BrightWhite),
		UIBackground:    pick(t.UIBackground, d.UIBackground),
		UIBackgroundAlt: pick(t.UIBackgroundAlt, d.UIBackgroundAlt),
		UIBorder:        pick(t.UIBorder, d.UIBorder),
		UIForeground:    pick(t.UIForeground, d.UIForeground),
		UIForegroundDim: pick(t.UIForegroundDim, d.UIForegroundDim),
		UIAccent:        pick(t.UIAccent, d.UIAccent),
		TabActive:       pick(t.TabActive, d.TabActive),
		TabInactive:     pick(t.TabInactive, d.TabInactive),
		PaneBorder:      pick(t.PaneBorder, d.PaneBorder),
		Splitter:        pick(t.Splitter, d.Splitter),
	})
}

// ThemeSource is the shape of config.Theme that this package consumes.
type ThemeSource struct {
	Background, Foreground, Cursor, CursorAccent, Selection string
	Black, Red, Green, Yellow, Blue, Magenta, Cyan, White   string
	BrightBlack, BrightRed, BrightGreen, BrightYellow       string
	BrightBlue, BrightMagenta, BrightCyan, BrightWhite      string
	UIBackground, UIBackgroundAlt, UIBorder                 string
	UIForeground, UIForegroundDim, UIAccent                 string
	TabActive, TabInactive, PaneBorder, Splitter            string
}

func PaletteFromHex(s paletteSource) (Palette, error) {
	return buildPalette(s), nil
}

func buildPalette(s paletteSource) Palette {
	var p Palette
	fallback := defaultThemeHex()
	set := func(dst *Color, hex string, def string) {
		c, ok := ParseHex(hex)
		if !ok {
			c, _ = ParseHex(def)
		}
		*dst = c
	}
	set(&p.Background, s.Background, fallback.Background)
	set(&p.Foreground, s.Foreground, fallback.Foreground)
	set(&p.Cursor, s.Cursor, fallback.Cursor)
	set(&p.CursorText, s.CursorAccent, fallback.CursorAccent)
	set(&p.Selection, s.Selection, fallback.Selection)
	set(&p.UIBackground, s.UIBackground, fallback.UIBackground)
	set(&p.UIBackgroundAlt, s.UIBackgroundAlt, fallback.UIBackgroundAlt)
	set(&p.UIBorder, s.UIBorder, fallback.UIBorder)
	set(&p.UIForeground, s.UIForeground, fallback.UIForeground)
	set(&p.UIForegroundDim, s.UIForegroundDim, fallback.UIForegroundDim)
	set(&p.UIAccent, s.UIAccent, fallback.UIAccent)
	set(&p.TabActive, s.TabActive, fallback.TabActive)
	set(&p.TabInactive, s.TabInactive, fallback.TabInactive)
	set(&p.PaneBorder, s.PaneBorder, fallback.PaneBorder)
	set(&p.Splitter, s.Splitter, fallback.Splitter)

	ansi := [16][2]string{
		{s.Black, fallback.Black}, {s.Red, fallback.Red},
		{s.Green, fallback.Green}, {s.Yellow, fallback.Yellow},
		{s.Blue, fallback.Blue}, {s.Magenta, fallback.Magenta},
		{s.Cyan, fallback.Cyan}, {s.White, fallback.White},
		{s.BrightBlack, fallback.BrightBlack}, {s.BrightRed, fallback.BrightRed},
		{s.BrightGreen, fallback.BrightGreen}, {s.BrightYellow, fallback.BrightYellow},
		{s.BrightBlue, fallback.BrightBlue}, {s.BrightMagenta, fallback.BrightMagenta},
		{s.BrightCyan, fallback.BrightCyan}, {s.BrightWhite, fallback.BrightWhite},
	}
	for i, pair := range ansi {
		set(&p.ANSI[i], pair[0], pair[1])
	}
	// The chrome needs a danger colour; no theme ships one, so derive it from
	// the palette's red rather than hardcoding a hex that ignores the theme.
	p.Danger = p.ANSI[1]
	return p
}

// ParseHex parses "#rgb", "#rrggbb" or "rrggbb".
func ParseHex(s string) (Color, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	switch len(s) {
	case 3:
		r, err1 := strconv.ParseUint(s[0:1], 16, 8)
		g, err2 := strconv.ParseUint(s[1:2], 16, 8)
		b, err3 := strconv.ParseUint(s[2:3], 16, 8)
		if err1 != nil || err2 != nil || err3 != nil {
			return 0, false
		}
		return RGB(uint8(r*17), uint8(g*17), uint8(b*17)), true
	case 6:
		v, err := strconv.ParseUint(s, 16, 32)
		if err != nil {
			return 0, false
		}
		return Color(v), true
	}
	return 0, false
}
