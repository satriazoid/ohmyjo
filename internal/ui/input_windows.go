//go:build windows

package ui

import (
	"unicode/utf16"
	"unsafe"
)

// Exported aliases for the window messages the application layer reacts to. The
// shell consumes the messages it must handle itself (paint, size, close) and
// forwards everything else to the Host, which needs to name the ones it cares
// about.
const (
	WMKeyDown     = WM_KEYDOWN
	WMSysKeyDown  = 0x0104
	WMChar        = WM_CHAR
	WMSysChar     = 0x0106
	WMDeadChar    = 0x0103
	WMKeyUp       = 0x0101
	WMLButtonDown = WM_LBUTTONDOWN
	WMLButtonUp   = 0x0202
	WMLButtonDbl  = 0x0203
	WMRButtonDown = WM_RBUTTONDOWN
	WMRButtonUp   = 0x0205
	WMMouseMove   = WM_MOUSEMOVE
	WMMouseWheel  = WM_MOUSEWHEEL
	WMMouseHWheel = 0x020E
	WMSetFocus    = 0x0007
	WMKillFocus   = 0x0008
	WMTimer       = WM_TIMER
)

// Key state bits carried in a mouse message's wParam.
const (
	MKShift = 0x0004
	MKCtrl  = 0x0008
	MKAlt   = 0x0020
	MKLeft  = 0x0001
)

// Rect is a rectangle in surface pixels. It is the geometry type layout code
// passes around; the point (X+W, Y+H) is outside it.
type Rect struct {
	X, Y, W, H int
}

// Contains reports whether a point lies inside the rectangle.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Empty reports whether the rectangle covers no area.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Inset shrinks the rectangle on every side, saturating at zero.
func (r Rect) Inset(dx, dy int) Rect {
	n := Rect{X: r.X + dx, Y: r.Y + dy, W: r.W - 2*dx, H: r.H - 2*dy}
	if n.W < 0 {
		n.W = 0
	}
	if n.H < 0 {
		n.H = 0
	}
	return n
}

// Modifiers is the modifier-key state of a key or mouse event.
type Modifiers struct {
	Ctrl, Alt, Shift bool
	// Left is set for mouse events while the left button is held, which is what
	// tells a drag from a move.
	Left bool
}

// PointFromLParam unpacks a client-area mouse coordinate. Win32 packs them as
// two signed 16-bit values, so a drag to a negative coordinate stays negative
// instead of wrapping to 65535.
func PointFromLParam(lp uintptr) (x, y int) {
	return int(int16(lp & 0xffff)), int(int16((lp >> 16) & 0xffff))
}

// ModifiersFromWParam decodes the modifier bits of a mouse message.
func ModifiersFromWParam(wp uintptr) Modifiers {
	return Modifiers{
		Ctrl:  wp&MKCtrl != 0,
		Alt:   wp&MKAlt != 0,
		Shift: wp&MKShift != 0,
		Left:  wp&MKLeft != 0,
	}
}

var procGetKeyState = user32.NewProc("GetKeyState")

// ModifiersNow reads the live keyboard state. Alt is read through the menu key,
// which is set both by a real Alt press and by the AltGr of European layouts,
// the same thing a terminal application sees.
func ModifiersNow() Modifiers {
	down := func(vk int) bool {
		s, _, _ := procGetKeyState.Call(uintptr(vk))
		return int16(s) < 0
	}
	return Modifiers{
		Ctrl:  down(0x11), // VK_CONTROL
		Alt:   down(0x12), // VK_MENU
		Shift: down(0x10), // VK_SHIFT
	}
}

// ClientSize reports the window's client area in physical pixels.
func (w *Window) ClientSize() (int, int) { return ClientSize(w.hwnd) }

// Metrics selects a font slot and returns its geometry.
//
// It changes the surface's selected font, so it is only safe on the window's
// message thread, which is also the only place layout runs.
func (w *Window) Metrics(id FontID) Metrics {
	if w.g == nil {
		return Metrics{}
	}
	return w.g.SetFont(id)
}

// TextWidth measures a string in a font slot, for sizing chrome.
func (w *Window) TextWidth(id FontID, s string) int {
	if w.g == nil {
		return 0
	}
	w.g.SetFont(id)
	return w.g.TextWidth(s)
}

var (
	procSetTimer       = user32.NewProc("SetTimer")
	procKillTimer      = user32.NewProc("KillTimer")
	procSetCapture     = user32.NewProc("SetCapture")
	procScreenToClient = user32.NewProc("ScreenToClient")
	procGetCursorPos   = user32.NewProc("GetCursorPos")
)

// SetTimer arranges a WM_TIMER message carrying id every interval
// milliseconds. The timer belongs to the window, so it dies with it.
func (w *Window) SetTimer(id uintptr, interval int) bool {
	if w.hwnd == 0 || interval <= 0 {
		return false
	}
	r, _, _ := procSetTimer.Call(w.hwnd, id, uintptr(interval), 0)
	return r != 0
}

// KillTimer cancels a timer created by SetTimer.
func (w *Window) KillTimer(id uintptr) {
	if w.hwnd != 0 {
		procKillTimer.Call(w.hwnd, id)
	}
}

// SetCapture routes all mouse input to this window until ReleaseCapture, which
// is what keeps a drag selection alive when the pointer leaves the client area.
func (w *Window) SetCapture() { procSetCapture.Call(w.hwnd) }

// ReleaseCapture ends capture.
func (w *Window) ReleaseCapture() { procReleaseCapture.Call() }

// ScreenToClient converts a screen point to a client point. Mouse wheel and
// horizontal wheel messages carry screen coordinates, unlike the other mouse
// messages.
func (w *Window) ScreenToClient(x, y int) (int, int) {
	var p point
	p.X, p.Y = int32(x), int32(y)
	procScreenToClient.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
	return int(p.X), int(p.Y)
}

// CursorPos reports the pointer position in screen coordinates.
func (w *Window) CursorPos() (int, int) {
	var p point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return int(p.X), int(p.Y)
}

// DoubleClickTime is the system's double-click interval. Using the configured
// value keeps click counting consistent with every other Windows application.
func DoubleClickTime() int {
	t, _, _ := procGetDoubleClickTime.Call()
	return int(t)
}

var (
	procGetDoubleClickTime = user32.NewProc("GetDoubleClickTime")
	procOpenClipboard      = user32.NewProc("OpenClipboard")
	procCloseClipboard     = user32.NewProc("CloseClipboard")
	procGetClipboardData   = user32.NewProc("GetClipboardData")
	procSetClipboardData   = user32.NewProc("SetClipboardData")
	procEmptyClipboard     = user32.NewProc("EmptyClipboard")
	procIsClipboardFormat  = user32.NewProc("IsClipboardFormatAvailable")
	procGlobalAlloc        = kernel32.NewProc("GlobalAlloc")
	procGlobalLock         = kernel32.NewProc("GlobalLock")
	procGlobalUnlock       = kernel32.NewProc("GlobalUnlock")
	procGlobalFree         = kernel32.NewProc("GlobalFree")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

// ClipboardText reads the clipboard as text.
//
// The clipboard is a shared resource: OpenClipboard fails while another process
// holds it, so a read that loses the race reports false rather than blocking
// the UI thread.
func ClipboardText(hwnd uintptr) (string, bool) {
	if r, _, _ := procOpenClipboard.Call(hwnd); r == 0 {
		return "", false
	}
	defer procCloseClipboard.Call()

	ok, _, _ := procIsClipboardFormat.Call(cfUnicodeText)
	if ok == 0 {
		return "", false
	}
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", false
	}
	defer procGlobalUnlock.Call(h)
	s := utf16ToString(ptrAt[uint16](p))
	return s, s != ""
}

// SetClipboardText replaces the clipboard contents with text.
func SetClipboardText(hwnd uintptr, text string) bool {
	if text == "" {
		return false
	}
	if r, _, _ := procOpenClipboard.Call(hwnd); r == 0 {
		return false
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()

	u16 := append(utf16.Encode([]rune(text)), 0)
	size := uintptr(len(u16)) * unsafe.Sizeof(u16[0])
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return false
	}
	copy(unsafe.Slice(ptrAt[uint16](p), len(u16)), u16)
	procGlobalUnlock.Call(h)
	// Ownership passes to the clipboard on success; freeing it here would leave
	// the clipboard pointing at released memory.
	if r, _, _ := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return false
	}
	return true
}

// utf16ToString decodes a NUL-terminated UTF-16 string.
func utf16ToString(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	for ptr := unsafe.Pointer(p); *(*uint16)(ptr) != 0; ptr = unsafe.Add(ptr, 2) {
		n++
	}
	if n == 0 {
		return ""
	}
	return string(utf16.Decode(unsafe.Slice(p, n)))
}
