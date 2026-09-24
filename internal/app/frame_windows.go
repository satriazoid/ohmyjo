//go:build windows

package app

import (
	"log"
	"unsafe"
)

// Borderless-window support.
//
// The window is created by go-webview2 with WS_OVERLAPPEDWINDOW, which brings
// the native caption bar with it. A terminal should own its whole surface, so
// WM_NCCALCSIZE is answered with "client area = window rect" and the frame
// disappears. Removing the frame also removes the drag area and the resize
// borders, both of which are reimplemented here:
//
//   - dragging is delegated to the window manager on request from the frontend
//     (WM_NCLBUTTONDOWN with HTCAPTION), which is the Win32 equivalent of a CSS
//     drag region and keeps hit testing in the DOM where the drag handle lives
//   - resize borders come back as WM_NCHITTEST results
//   - minimising, maximising and closing become frontend buttons, because the
//     system menu they used to live in is gone
type rect struct{ Left, Top, Right, Bottom int32 }

type monitorInfoT struct {
	CbSize    uint32
	RcMonitor rect
	RcWork    rect
	DwFlags   uint32
}

// ncCalcSizeParams is the NCCALCSIZE_PARAMS the system passes in lParam of
// WM_NCCALCSIZE when wParam is TRUE. Only the first rectangle is used here: it is
// the proposed client rectangle, on input and on output.
type ncCalcSizeParams struct {
	Rgrc  [3]rect
	Lppos uintptr
}

const (
	wmNcCalcSize    = 0x0083
	wmNcHitTest     = 0x0084
	wmNcLButtonDown = 0x00A1
	wmClose         = 0x0010

	htClient      = 1
	htCaption     = 2
	htLeft        = 10
	htRight       = 11
	htTop         = 12
	htTopLeft     = 13
	htTopRight    = 14
	htBottom      = 15
	htBottomLeft  = 16
	htBottomRight = 17

	swMinimize = 6
	swMaximize = 3
	swRestore  = 9

	swpNoMove       = 0x0002
	swpNoSize       = 0x0001
	swpFrameChanged = 0x0020

	smCxSizeFrame  = 32
	smCxPaddedBord = 92
	monitorNearest = 2
)

var (
	procIsZoomed               = user32.NewProc("IsZoomed")
	procMonitorFromWindow      = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW        = user32.NewProc("GetMonitorInfoW")
	procSendMessageW           = user32.NewProc("SendMessageW")
	procPostMessageW           = user32.NewProc("PostMessageW")
	procReleaseCapture         = user32.NewProc("ReleaseCapture")
	procShowWindow             = user32.NewProc("ShowWindow")
	procGetSystemMetricsForDpi = user32.NewProc("GetSystemMetricsForDpi")
)

// bindable is the slice of webview.WebView the window controls need; keeping it
// structural means this file does not import the webview package.
type bindable interface {
	Bind(name string, f interface{}) error
}

// appHwnd is the application window. There is exactly one per process, so a
// package variable is enough to let the frontend's window controls reach it.
var appHwnd uintptr

func isZoomed(hwnd uintptr) bool {
	v, _, _ := procIsZoomed.Call(hwnd)
	return v != 0
}

// resizeBorder is the invisible margin that still resizes the window. It tracks
// the system border metric so the grab area matches what Windows uses itself.
func resizeBorder(hwnd uintptr) int32 {
	dpi := uintptr(windowDpi(hwnd))
	f, _, _ := procGetSystemMetricsForDpi.Call(smCxSizeFrame, dpi)
	p, _, _ := procGetSystemMetricsForDpi.Call(smCxPaddedBord, dpi)
	b := int32(f + p)
	if b < 4 {
		b = 4
	}
	// A terminal wants its edges back; a wide margin would swallow clicks near
	// the first and last column of text.
	if b > 10 {
		b = 10
	}
	return b
}

// monitorRects reports the window monitor's full and usable (taskbar-excluded)
// rectangles in one GetMonitorInfoW call.
func monitorRects(hwnd uintptr) (monitor, work rect, ok bool) {
	h, _, _ := procMonitorFromWindow.Call(hwnd, monitorNearest)
	if h == 0 {
		return rect{}, rect{}, false
	}
	mi := monitorInfoT{CbSize: uint32(unsafe.Sizeof(monitorInfoT{}))}
	if r, _, _ := procGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return rect{}, rect{}, false
	}
	return mi.RcMonitor, mi.RcWork, true
}

// handleFrameMessage answers the messages that replace the native frame.
// The bool reports whether the message was fully handled.
func handleFrameMessage(hwnd, msg, wp, lp uintptr) (uintptr, bool) {
	switch msg {
	case wmNcCalcSize:
		if wp == 0 {
			// wParam FALSE carries a plain RECT, not NCCALCSIZE_PARAMS; leave
			// the classic path to DefWindowProc and only suppress the frame on
			// the sizing path.
			return 0, false
		}
		// "The client area is the whole window": this is what deletes the title
		// bar, the borders and the system menu frame.
		//
		// A maximized window is the exception. Windows sizes it from the work
		// area but then hands over a rectangle inflated by the frame it no
		// longer draws, so the client area would hang off the screen edge and
		// cover the taskbar. Returning the work area itself for that case keeps
		// the two mechanisms consistent.
		if isZoomed(hwnd) && lp != 0 {
			if _, work, ok := monitorRects(hwnd); ok {
				params := (*ncCalcSizeParams)(unsafe.Add(unsafe.Pointer(nil), lp))
				params.Rgrc[0] = work
			}
		}
		return 0, true

	case wmNcHitTest:
		return hitTest(hwnd, lp), true
	}
	return 0, false
}

func hitTest(hwnd, lp uintptr) uintptr {
	// lParam packs the cursor position in screen coordinates as two signed
	// 16-bit values. They are physical pixels now that the process is DPI aware.
	x := int32(int16(lp & 0xffff))
	y := int32(int16((lp >> 16) & 0xffff))

	var r rect
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return htClient
	}
	// A maximized window offers no resize edges: they would land under the
	// screen edge, fighting Windows' own edge gestures and the taskbar. The
	// window rectangle also overhangs the client area by the frame width it no
	// longer draws, so that band has to answer HTCLIENT or the system would
	// treat it as part of the (invisible) non-client area.
	if isZoomed(hwnd) {
		return htClient
	}
	b := resizeBorder(hwnd)
	left, right := x < r.Left+b, x >= r.Right-b
	top, bottom := y < r.Top+b, y >= r.Bottom-b
	switch {
	case top && left:
		return htTopLeft
	case top && right:
		return htTopRight
	case bottom && left:
		return htBottomLeft
	case bottom && right:
		return htBottomRight
	case left:
		return htLeft
	case right:
		return htRight
	case top:
		return htTop
	case bottom:
		return htBottom
	}
	return htClient
}

// applyFrameless records the window and forces the non-client area to be
// recomputed. The subclass is installed after the window already exists with a
// frame, so without this the caption would stay until the next manual resize.
func applyFrameless(hwnd uintptr) {
	appHwnd = hwnd
	_, _, _ = procSetWindowPos.Call(
		hwnd, 0, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZOrder|swpFrameChanged,
	)
}

// bindWindowControls exposes the native window actions to the frontend. These
// are the buttons that replace the caption bar.
func bindWindowControls(b bindable) {
	for name, fn := range map[string]any{
		"ojWindowDrag":           windowStartDrag,
		"ojWindowMinimize":       windowMinimize,
		"ojWindowToggleMaximize": windowToggleMaximize,
		"ojWindowClose":          windowClose,
		"ojWindowIsMaximized":    windowIsMaximized,
	} {
		if err := b.Bind(name, fn); err != nil {
			log.Printf("bind %s: %v", name, err)
		}
	}
}

func windowStartDrag() {
	if appHwnd == 0 {
		return
	}
	// Releasing the capture first is required, otherwise the mouse stays owned
	// by the DOM and the window manager never sees the drag.
	_, _, _ = procReleaseCapture.Call()
	_, _, _ = procSendMessageW.Call(appHwnd, wmNcLButtonDown, htCaption, 0)
}

func windowMinimize() {
	if appHwnd == 0 {
		return
	}
	_, _, _ = procShowWindow.Call(appHwnd, swMinimize)
}

// windowToggleMaximize returns the state the window ended up in, so the
// frontend can swap its restore icon without a second round trip.
func windowToggleMaximize() bool {
	if appHwnd == 0 {
		return false
	}
	if isZoomed(appHwnd) {
		_, _, _ = procShowWindow.Call(appHwnd, swRestore)
		return false
	}
	_, _, _ = procShowWindow.Call(appHwnd, swMaximize)
	return true
}

func windowClose() {
	if appHwnd == 0 {
		return
	}
	_, _, _ = procPostMessageW.Call(appHwnd, wmClose, 0, 0)
}

func windowIsMaximized() bool {
	return appHwnd != 0 && isZoomed(appHwnd)
}
