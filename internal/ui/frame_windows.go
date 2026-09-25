//go:build windows

package ui

import (
	"sync"
	"unsafe"
)

// Borderless-window support.
//
// The window is created with WS_OVERLAPPEDWINDOW so Windows still treats it as
// a normal resizable top-level window for snapping, minimising and restoring,
// and WM_NCCALCSIZE then answers "the client area is the whole window". That
// removes the caption bar and the frame, and with them the drag area and the
// resize borders, both of which are reimplemented here:
//
//   - WM_NCHITTEST returns the resize border codes near the edges
//   - the shell asks for HTCAPTION when the user drags the tab strip
//   - minimise, maximise and close are ordinary client-area buttons
type ncCalcSizeParams struct {
	Rgrc  [3]rectT
	Lppos uintptr
}

const (
	wmNcCalcSize    = 0x0083
	wmNcHitTest     = 0x0084
	wmNcLButtonDown = 0x00A1

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
	swShow     = 5

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020

	smCxSizeFrame  = 32
	smCxPaddedBord = 92
	monitorNearest = 2

	// monitorDefaultToPrimary selects the primary monitor when none of the
	// window-specific selectors apply.
	monitorDefaultToPrimary = 1

	// workAreaPercent is how much of the usable screen the default window size
	// may occupy. The rest is the desktop margin that keeps the window's drag
	// area and resize edges reachable.
	workAreaPercent = 90
)

var (
	procSendMessageW           = user32.NewProc("SendMessageW")
	procSetWindowPos           = user32.NewProc("SetWindowPos")
	procGetWindowRect          = user32.NewProc("GetWindowRect")
	procIsZoomed               = user32.NewProc("IsZoomed")
	procMonitorFromWindow      = user32.NewProc("MonitorFromWindow")
	procMonitorFromPoint       = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW        = user32.NewProc("GetMonitorInfoW")
	procReleaseCapture         = user32.NewProc("ReleaseCapture")
	procShowWindow             = user32.NewProc("ShowWindow")
	procGetSystemMetricsForDpi = user32.NewProc("GetSystemMetricsForDpi")
	procBeginPaint             = user32.NewProc("BeginPaint")
	procEndPaint               = user32.NewProc("EndPaint")
)

type paintStruct struct {
	Hdc       uintptr
	Erase     int32
	RcPaint   rectT
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type monitorInfoT struct {
	CbSize    uint32
	RcMonitor rectT
	RcWork    rectT
	DwFlags   uint32
}

var windowPtrsMu sync.RWMutex

// applyFrameless recomputes the non-client area so the caption and frame are
// removed.
func applyFrameless(hwnd uintptr) {
	procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZOrder|swpFrameChanged)
}

// isZoomed reports whether the window is maximized.
func isZoomed(hwnd uintptr) bool {
	v, _, _ := procIsZoomed.Call(hwnd)
	return v != 0
}

// resizeBorder is the invisible margin that still resizes the window. It tracks
// the system border metric so the grab area matches what Windows uses itself,
// capped so that it cannot swallow clicks near the first and last text column.
func resizeBorder(hwnd uintptr) int32 {
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	if dpi == 0 {
		dpi = 96
	}
	f, _, _ := procGetSystemMetricsForDpi.Call(smCxSizeFrame, dpi)
	p, _, _ := procGetSystemMetricsForDpi.Call(smCxPaddedBord, dpi)
	b := int32(f + p)
	if b < 4 {
		b = 4
	}
	if b > 10 {
		b = 10
	}
	return b
}

// monitorRects reports the window monitor's full and usable rectangles.
func monitorRects(hwnd uintptr) (monitor, work rectT, ok bool) {
	h, _, _ := procMonitorFromWindow.Call(hwnd, monitorNearest)
	if h == 0 {
		return rectT{}, rectT{}, false
	}
	mi := monitorInfoT{CbSize: uint32(unsafe.Sizeof(monitorInfoT{}))}
	if r, _, _ := procGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return rectT{}, rectT{}, false
	}
	return mi.RcMonitor, mi.RcWork, true
}

// primaryWork reports the primary monitor's usable rectangle, the part of the
// screen the taskbar does not cover. It is needed before a window exists, when
// its initial size has to be chosen, so it asks for the monitor containing the
// origin rather than for one belonging to a window.
func primaryWork() (work rectT, ok bool) {
	h, _, _ := procMonitorFromPoint.Call(0, 0, monitorDefaultToPrimary)
	if h == 0 {
		return rectT{}, false
	}
	mi := monitorInfoT{CbSize: uint32(unsafe.Sizeof(monitorInfoT{}))}
	if r, _, _ := procGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return rectT{}, false
	}
	return mi.RcWork, true
}

// fitToWork shrinks a window size so it fits inside the usable screen with a
// margin, leaving a border of desktop around it.
//
// A borderless window has no caption to grab, so a window that opens larger
// than the desktop puts its own drag area and resize edges off screen and
// cannot be moved or resized back. The margin is what keeps that from
// happening, and it also leaves room to grab the edges.
func fitToWork(w, h int32, work rectT) (int32, int32) {
	availW := (work.Right - work.Left) * workAreaPercent / 100
	availH := (work.Bottom - work.Top) * workAreaPercent / 100
	if w > availW {
		w = availW
	}
	if h > availH {
		h = availH
	}
	if min := int32(320); w < min {
		w = min
	}
	if min := int32(240); h < min {
		h = min
	}
	return w, h
}

// fitAndCenter clamps a window to its monitor's work area and centers it there.
func fitAndCenter(hwnd uintptr) {
	_, work, ok := monitorRects(hwnd)
	if !ok {
		centerOnScreen(hwnd)
		return
	}
	var wr rectT
	if r, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr))); r == 0 {
		return
	}
	w, h := wr.Right-wr.Left, wr.Bottom-wr.Top
	w, h = fitToWork(w, h, work)

	x := work.Left + (work.Right-work.Left-w)/2
	y := work.Top + (work.Bottom-work.Top-h)/2
	// Centering can still leave a window partly off a work area smaller than
	// the minimum size, so the position is clamped as well.
	if x < work.Left {
		x = work.Left
	}
	if y < work.Top {
		y = work.Top
	}
	if x+w > work.Right {
		x = work.Right - w
	}
	if y+h > work.Bottom {
		y = work.Bottom - h
	}
	procSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoZOrder)
}

// handleFrameMessage answers the messages that replace the native frame. The
// bool reports whether the message was fully handled.
func handleFrameMessage(hwnd, msg, wp, lp uintptr) (uintptr, bool) {
	switch msg {
	case wmNcCalcSize:
		if wp == 0 {
			// wParam FALSE carries a plain RECT, not NCCALCSIZE_PARAMS; leave
			// that path to DefWindowProc and only suppress the frame on the
			// sizing path.
			return 0, false
		}
		// "The client area is the whole window": this is what deletes the title
		// bar, the borders and the system menu.
		//
		// A maximized window is the exception. Windows sizes it from the work
		// area but then hands over a rectangle inflated by the frame it no
		// longer draws, so the client area would hang off the screen edge and
		// cover the taskbar. Returning the work area for that case keeps the two
		// mechanisms consistent.
		if isZoomed(hwnd) && lp != 0 {
			if _, work, ok := monitorRects(hwnd); ok {
				params := ptrAt[ncCalcSizeParams](lp)
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
	// lParam packs the cursor position as two signed 16-bit screen coordinates.
	x := int32(int16(lp & 0xffff))
	y := int32(int16((lp >> 16) & 0xffff))

	var r rectT
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return htClient
	}
	// A maximized window offers no resize edges: they would land under the
	// screen edge, fighting Windows' own edge gestures and the taskbar. Its
	// window rectangle also overhangs the client area by the frame width it no
	// longer draws, so that band must answer HTCLIENT.
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

// StartDrag asks the window manager to move the window, which is the Win32
// equivalent of a CSS drag region. The shell calls it from the tab strip.
func (w *Window) StartDrag() {
	procReleaseCapture.Call()
	procSendMessageW.Call(w.hwnd, wmNcLButtonDown, htCaption, 0)
}

// IsMaximized reports whether the window is currently maximized.
func (w *Window) IsMaximized() bool { return isZoomed(w.hwnd) }

// Minimize minimizes the window.
func (w *Window) Minimize() { procShowWindow.Call(w.hwnd, swMinimize) }

// ToggleMaximize maximizes or restores the window and reports the new state.
func (w *Window) ToggleMaximize() bool {
	if isZoomed(w.hwnd) {
		procShowWindow.Call(w.hwnd, swRestore)
		return false
	}
	procShowWindow.Call(w.hwnd, swMaximize)
	return true
}

// WorkArea reports the usable area of the window's monitor, excluding the
// taskbar. A window that fills it is "maximized" without the overhang problem
// maximized windows normally bring.
func (w *Window) WorkArea() (rectT, bool) {
	_, work, ok := monitorRects(w.hwnd)
	return work, ok
}
