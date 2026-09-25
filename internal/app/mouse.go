//go:build windows

package app

import (
	"time"

	"ohmyjo/internal/ui"
)

// MouseButton names the button a mouse event reports.
type MouseButton int

const (
	MouseNone MouseButton = iota
	MouseLeft
	MouseMiddle
	MouseRight
	MouseWheel
)

// MouseEvent is one reportable mouse action. X and Y are zero-based viewport
// cells; the encoders add the one-based bias the protocols want.
type MouseEvent struct {
	X, Y   int
	Button MouseButton
	// Press is false for a release, which only the SGR encoding expresses
	// distinctly; the legacy encoding encodes it as button 3.
	Press bool
	// Up reports a wheel notch away from the user.
	Up bool
	// Modifiers are folded into the button code, as the protocol requires.
	Shift, Alt, Ctrl bool
	// SGR selects the 1006 extended encoding.
	SGR bool
}

// EncodeMouse converts a mouse event into the sequence the application asked
// for. It returns nil when mouse reporting is off, which is the caller's signal
// to fall back to local selection.
//
// The two encodings differ in more than syntax: X10 packs coordinates into a
// single byte each, so cells past column 95 (and any negative coordinate) cannot
// be expressed. Reporting them would send a wrapped, wrong coordinate, so an
// unrepresentable event is dropped instead.
func EncodeMouse(ev MouseEvent) []byte {
	btn := mouseButtonCode(ev)
	if ev.Shift {
		btn |= 4
	}
	if ev.Alt {
		btn |= 8
	}
	if ev.Ctrl {
		btn |= 16
	}
	if ev.SGR {
		final := byte('M')
		if !ev.Press {
			final = 'm'
		}
		return appendCsi(nil, "<", itoa(btn), ";", itoa(ev.X+1), ";", itoa(ev.Y+1), string(final))
	}
	if ev.X < 0 || ev.Y < 0 || ev.X > 222 || ev.Y > 222 {
		return nil
	}
	out := []byte("\x1b[M")
	out = append(out, byte(32+btn), byte(32+ev.X+1), byte(32+ev.Y+1))
	return out
}

// mouseButtonCode is the button number the protocol assigns.
func mouseButtonCode(ev MouseEvent) int {
	switch ev.Button {
	case MouseLeft:
		return 0
	case MouseMiddle:
		return 1
	case MouseRight:
		return 2
	case MouseWheel:
		if ev.Up {
			return 64
		}
		return 65
	}
	// A release in the legacy encoding is reported as "no button".
	if !ev.Press && !ev.SGR {
		return 3
	}
	return 3
}

// appendCsi appends a CSI sequence from its parts.
func appendCsi(dst []byte, parts ...string) []byte {
	dst = append(dst, 0x1b, '[')
	for _, p := range parts {
		dst = append(dst, p...)
	}
	return dst
}

// mouseButton maps a window message to the button it reports.
func mouseButton(msg uint32) MouseButton {
	switch msg {
	case ui.WMLButtonDown, ui.WMLButtonUp, ui.WMLButtonDbl:
		return MouseLeft
	case ui.WMRButtonDown, ui.WMRButtonUp:
		return MouseRight
	}
	return MouseNone
}

// sign reports the direction of a wheel delta: +1 up, -1 down, 0 for none.
func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	}
	return 0
}

// doubleClickTime is the system's double-click interval, so click counting
// matches every other Windows application.
func doubleClickTime() time.Duration {
	return time.Duration(ui.DoubleClickTime()) * time.Millisecond
}
