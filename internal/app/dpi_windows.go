//go:build windows

package app

import (
	"log"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")

	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procGetDpiForSystem               = user32.NewProc("GetDpiForSystem")
	procGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	procSetWindowLongPtrW             = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW               = user32.NewProc("CallWindowProcW")
	procSetWindowPos                  = user32.NewProc("SetWindowPos")
	procGetWindowRect                 = user32.NewProc("GetWindowRect")
)

// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 and GWLP_WNDPROC are negative, and
// the Win32 signatures take them as pointer-sized signed values, so they are
// written here in two's complement.
const (
	dpiAwarenessPerMonitorV2 = ^uintptr(3) // -4
	gwlpWndProc              = ^uintptr(3) // -4

	wmDpiChanged  = 0x02E0
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
)

// enableDpiAwareness opts the process into per-monitor DPI awareness. It must
// run before any window is created.
//
// This is not cosmetic. A process that never declares awareness is treated as
// DPI-unaware: Windows lays the window out at 96 DPI and then bitmap-stretches
// the whole thing to the monitor's real scale. On a 125% display that upscale is
// exactly what makes every glyph look blurry. Declaring awareness lets WebView2
// render at native device pixels instead.
func enableDpiAwareness() {
	if ok, _, err := procSetProcessDpiAwarenessContext.Call(dpiAwarenessPerMonitorV2); ok == 0 {
		log.Printf("dpi awareness: %v (falling back to DPI scaling)", err)
	}
}

// systemDpi reports the primary monitor's DPI so window sizes can be expressed
// in logical units. Call it only after enableDpiAwareness: before that the
// process is told 96 regardless of the real display.
func systemDpi() float64 {
	dpi, _, _ := procGetDpiForSystem.Call()
	if dpi == 0 {
		return 96
	}
	return float64(dpi)
}

func windowDpi(hwnd uintptr) float64 {
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	if dpi == 0 {
		return systemDpi()
	}
	return float64(dpi)
}

// origWndProc is the window procedure installed by go-webview2, saved so the DPI
// subclass can pass every message it does not handle straight through.
var origWndProc uintptr

// windowDpiState remembers the DPI the window was last laid out at, so a move
// between monitors can be resized by the ratio between the two.
var windowDpiState float64

// installSubclass installs a thin window-procedure wrapper. It does two jobs:
// react to WM_DPICHANGED (go-webview2 does not, so a per-monitor aware window
// would keep its old pixel size on a new monitor) and replace the native frame
// (see frame_windows.go).
func installSubclass(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	windowDpiState = windowDpi(hwnd)
	prev, _, _ := procSetWindowLongPtrW.Call(hwnd, gwlpWndProc, windows.NewCallback(appWndProc))
	if prev == 0 {
		// Nothing to chain to; leave the window alone rather than route every
		// message into a procedure that has no default handling.
		return
	}
	origWndProc = prev
}

func appWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	if origWndProc == 0 {
		return 0
	}
	if ret, handled := handleFrameMessage(hwnd, msg, wp, lp); handled {
		return ret
	}
	if msg == wmDpiChanged {
		// wp packs the new DPI as two 16-bit values (x in the low word, y in the
		// high word). The suggested rectangle in lp is ignored on purpose:
		// resizing the current rectangle keeps the window where the user put it,
		// and avoids reading a raw pointer out of a message.
		newDpi := float64(uint16(wp))
		if newDpi == 0 {
			newDpi = float64(wp >> 16)
		}
		if newDpi > 0 && windowDpiState > 0 && newDpi != windowDpiState {
			scale := newDpi / windowDpiState
			windowDpiState = newDpi
			var r struct{ Left, Top, Right, Bottom int32 }
			if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok != 0 {
				w, h := int32(float64(r.Right-r.Left)*scale), int32(float64(r.Bottom-r.Top)*scale)
				_, _, _ = procSetWindowPos.Call(
					hwnd, 0,
					uintptr(r.Left), uintptr(r.Top),
					uintptr(w), uintptr(h),
					swpNoZOrder|swpNoActivate,
				)
			}
		}
		return 0
	}
	ret, _, _ := procCallWindowProcW.Call(origWndProc, hwnd, msg, wp, lp)
	return ret
}
