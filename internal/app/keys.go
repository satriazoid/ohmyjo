//go:build windows

package app

import (
	"strings"

	"ohmyjo/internal/vt"
)

// Virtual key codes the encoder needs. They are stable Win32 values.
const (
	vkBack     = 0x08
	vkTab      = 0x09
	vkReturn   = 0x0D
	vkShift    = 0x10
	vkControl  = 0x11
	vkMenu     = 0x12
	vkEscape   = 0x1B
	vkSpace    = 0x20
	vkPrior    = 0x21 // Page Up
	vkNext     = 0x22 // Page Down
	vkEnd      = 0x23
	vkHome     = 0x24
	vkLeft     = 0x25
	vkUp       = 0x26
	vkRight    = 0x27
	vkDown     = 0x28
	vkInsert   = 0x2D
	vkDelete   = 0x2E
	vkF1       = 0x70
	vkF12      = 0x7B
	vkLWin     = 0x5B
	vkRWin     = 0x5C
	vkOEMPlus  = 0xBB
	vkOEMMinus = 0xBD
	vkComma    = 0xBC
	vkSlash    = 0xBF
	vkBacktick = 0xC0
	vkDigit0   = 0x30
)

// KeyEvent is one key press, already translated from a WM_KEYDOWN.
type KeyEvent struct {
	// VK is the virtual key code.
	VK uint32
	// Rune is the character the key produced, or 0 when it produced none (a
	// function key, or a chord Windows refuses to translate). The application
	// fills it from the WM_CHAR that follows the key press.
	Rune  rune
	Ctrl  bool
	Alt   bool
	Shift bool
}

// altEscaped reports whether this chord is already encoded with its own ESC
// prefix, so the Alt prefix is not applied twice. Alt+Escape and Alt+Backspace
// are metasends that a shell reads as ESC ESC and ESC DEL.
func (k KeyEvent) altEscaped() bool {
	switch k.VK {
	case vkEscape, vkReturn:
		return k.Ctrl // Ctrl+Alt+Escape / +Return are Ctrl chords, not metasends
	}
	return false
}

// EncodeKey converts a key press into the bytes a terminal emulator would send.
// mode selects between the application and normal variants of the cursor and
// keypad sequences, which is what makes arrows work inside full-screen programs.
//
// It returns nil when the key produces nothing, so callers can tell "no input"
// from "input that happens to be empty" — the difference between swallowing a
// chord and passing it to the shell.
func EncodeKey(k KeyEvent, mode vt.ModeFlag) []byte {
	out := encodeKey(k, mode)
	if len(out) == 0 {
		return nil
	}
	if k.Alt && !k.altEscaped() {
		return append([]byte{0x1b}, out...)
	}
	return out
}

// encodeKey is EncodeKey without the Alt prefix, which is applied once at the
// end so the sequences below stay readable.
func encodeKey(k KeyEvent, mode vt.ModeFlag) []byte {
	switch k.VK {
	case vkReturn:
		return []byte{'\r'}
	case vkTab:
		if k.Shift {
			return []byte("\x1b[Z") // CBT, back tab
		}
		return []byte{'\t'}
	case vkEscape:
		return []byte{0x1b}
	case vkBack:
		if k.Ctrl {
			// Ctrl+Backspace deletes a word; 0x08 is the rubout a shell expects.
			return []byte{0x08}
		}
		// The line editor wants DEL, not BS. Windows produces neither on
		// keydown, so it comes from here.
		return []byte{0x7f}
	}

	// A control character is the key's letter with the top bits cleared. This is
	// the only path for Ctrl+letter: Windows produces no WM_CHAR for it.
	if k.Ctrl {
		if b, ok := controlByte(k.VK); ok {
			return []byte{b}
		}
	}

	if seq := keySequence(k, mode); seq != "" {
		return []byte(seq)
	}
	// A printable character reaches the shell through WM_CHAR, so a keydown
	// with no sequence and no Rune must not emit anything — emitting the raw
	// rune here would send every keystroke twice.
	if k.Rune != 0 {
		return appendRune(nil, k.Rune)
	}
	return nil
}

// controlByte encodes Ctrl+key as a C0 control byte.
func controlByte(vk uint32) (byte, bool) {
	switch vk {
	case vkSpace, '2', 0x40: // Ctrl+Space, Ctrl+2, Ctrl+@
		return 0x00, true
	case vkSlash: // Ctrl+/
		return 0x1f, true
	case vkBacktick: // Ctrl+`
		return 0x00, true
	}
	if vk >= 'A' && vk <= 'Z' {
		return byte(vk-'A') + 1, true
	}
	switch vk {
	case 0xDB: // [
		return 0x1b, true
	case 0xDC: // backslash
		return 0x1c, true
	case 0xDD: // ]
		return 0x1d, true
	case 0x36: // 6, same as ^
		return 0x1e, true
	case vkOEMMinus: // -, same as _
		return 0x1f, true
	}
	return 0, false
}

// keySequence is the encoding for keys with no character: arrows, navigation
// and function keys. An empty result means the key has no sequence.
func keySequence(k KeyEvent, mode vt.ModeFlag) string {
	appCursor := mode&vt.ModeAppCursor != 0

	// CSI 1 ; modifier <final> is the general form; the modifier defaults to 1
	// and is omitted then. It counts shift=1, alt=2, ctrl=4, biased by one.
	mod := 1 + btoi(k.Shift) + 2*btoi(k.Alt) + 4*btoi(k.Ctrl)

	csi := func(final string) string {
		if mod == 1 {
			return "\x1b[" + final
		}
		return "\x1b[1;" + itoa(mod) + final
	}
	// ss3 is the unmodified form of the keys that have one; with any modifier
	// the CSI form is used instead, which is what xterm does.
	ss3 := func(letter string) string {
		if mod == 1 {
			return "\x1bO" + letter
		}
		return "\x1b[1;" + itoa(mod) + letter
	}

	switch k.VK {
	case vkUp:
		if appCursor {
			return ss3("A")
		}
		return csi("A")
	case vkDown:
		if appCursor {
			return ss3("B")
		}
		return csi("B")
	case vkRight:
		if appCursor {
			return ss3("C")
		}
		return csi("C")
	case vkLeft:
		if appCursor {
			return ss3("D")
		}
		return csi("D")
	case vkHome:
		if appCursor {
			return ss3("H")
		}
		return csi("H")
	case vkEnd:
		if appCursor {
			return ss3("F")
		}
		return csi("F")
	case vkInsert:
		return csi("2~")
	case vkDelete:
		return csi("3~")
	case vkPrior:
		return csi("5~")
	case vkNext:
		return csi("6~")
	}

	switch k.VK {
	case vkF1, vkF1 + 1, vkF1 + 2, vkF1 + 3:
		// F1..F4 have SS3 forms: P, Q, R, S.
		return ss3(string(rune('P' + (k.VK - vkF1))))
	}
	if n, ok := fnKeyNumber(k.VK); ok {
		if mod == 1 {
			return "\x1b[" + itoa(n) + "~"
		}
		return "\x1b[" + itoa(n) + ";" + itoa(mod) + "~"
	}
	return ""
}

// fnKeyNumber maps a function key to its xterm CSI number. The sequence skips
// numbers, which is historical: 16 and 22 are unused, and F11/F12 are 23 and 24.
func fnKeyNumber(vk uint32) (int, bool) {
	switch vk {
	case vkF1 + 4:
		return 15, true
	case vkF1 + 5:
		return 17, true
	case vkF1 + 6:
		return 18, true
	case vkF1 + 7:
		return 19, true
	case vkF1 + 8:
		return 20, true
	case vkF1 + 9:
		return 21, true
	case vkF1 + 10:
		return 23, true
	case vkF1 + 11:
		return 24, true
	}
	return 0, false
}

// isModifierKey reports whether a virtual key is a modifier held on its own,
// which produces no input and must not reach the shell.
func isModifierKey(vk uint32) bool {
	switch vk {
	case vkShift, vkControl, vkMenu, vkLWin, vkRWin:
		return true
	}
	return false
}

// Paste encodes text for the shell as a paste.
//
// Shells read a line ending as CR: a bare LF leaves the cursor where it is in a
// ConPTY and the command never runs. Line endings are normalised to CR for that
// reason. When the application has asked for bracketed paste the payload is
// wrapped so it is not interpreted as typed input, which is what stops a pasted
// newline inside a quoted string from executing it.
func Paste(text string, bracketed bool) []byte {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\n", "\r")
	if !bracketed {
		return []byte(text)
	}
	out := make([]byte, 0, len(text)+12)
	out = append(out, "\x1b[200~"...)
	out = append(out, text...)
	out = append(out, "\x1b[201~"...)
	return out
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// appendRune appends the UTF-8 encoding of r, so the encoder does not allocate
// through a string conversion on every keystroke.
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
