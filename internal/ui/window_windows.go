//go:build windows

package ui

import (
	"errors"
	"math"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	procInvalidateRect                = user32.NewProc("InvalidateRect")
	procSetWindowTextW                = user32.NewProc("SetWindowTextW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procLoadIconW                     = user32.NewProc("LoadIconW")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procSetCursor                     = user32.NewProc("SetCursor")
	procAdjustWindowRectExForDpi      = user32.NewProc("AdjustWindowRectExForDpi")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")

	kernel32GetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	kernel32GetLastError     = kernel32.NewProc("GetLastError")
)

// Window messages the shell consumes itself.
const (
	WM_NCCREATE    = 0x0081
	WM_DESTROY     = 0x0002
	WM_SIZE        = 0x0005
	WM_PAINT       = 0x000F
	WM_CLOSE       = 0x0010
	WM_ERASEBKGND  = 0x0014
	WM_SETCURSOR   = 0x0020
	WM_DPICHANGED  = 0x02E0
	WM_APP         = 0x8000
	WM_TIMER       = 0x0113
	WM_KEYDOWN     = 0x0100
	WM_CHAR        = 0x0102
	WM_MOUSEMOVE   = 0x0200
	WM_LBUTTONDOWN = 0x0201
	WM_RBUTTONDOWN = 0x0204
	WM_MOUSEWHEEL  = 0x020A

	IDC_ARROW       = 32512
	IDI_APPLICATION = 32512

	// icons loaded from the executable's resources; rsrc names the group icon 1.
	appIconID = 1
)

// wmRunOnUI carries a func() that must run on the window's message thread.
const wmRunOnUI = WM_APP + 1

type point struct{ X, Y int32 }

type msgT struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type rectT struct{ Left, Top, Right, Bottom int32 }

var (
	errRegisterClass = errors.New("RegisterClassExW failed")
	errCreateWindow  = errors.New("CreateWindowExW failed")
	errNoSurface     = errors.New("could not create the drawing surface")
)

// Host receives the parts of window behaviour that are application-specific.
// It runs on the window's message thread.
type Host interface {
	// Paint draws one frame. The surface is already clipped to the client area
	// and its size is w by h physical pixels.
	Paint(s Surface, w, h int)
	// Message handles a window message the shell does not consume. Returning
	// true means the message was handled and DefWindowProc must not see it.
	Message(msg uint32, wparam, lparam uintptr) bool
	// Resize is called after the client area changed to w by h physical pixels.
	Resize(w, h int)
	// Close is called when the window is closing. The message loop stops when
	// it returns.
	Close()
}

// WindowOptions configures a new window.
type WindowOptions struct {
	Title  string
	Width  int // logical units, 96 dpi
	Height int
	Center bool
	// Fonts are the logical (96 dpi) font slots the renderer may select. The
	// window scales them to the display DPI and re-scales on WM_DPICHANGED, so
	// callers never deal in physical sizes.
	Fonts map[FontID]FontSlot
}

// Window is a native top-level window with its own window procedure and a GDI
// drawing surface. There is no child control and no browser engine: the whole
// client area is painted by the application.
type Window struct {
	hwnd uintptr
	host Host
	g    *GDI
	opts WindowOptions

	dpi       float64
	fonts     map[FontID]FontSlot
	pendingMu sync.Mutex
	pending   []func()
}

// wndProcCallback must be created before any window is created and kept for the
// process lifetime; each syscall.NewCallback consumes a slot that is never
// released.
var wndProcCallback = syscall.NewCallback(windowProc)

func windowProc(hwnd, msg, wp, lp uintptr) uintptr {
	if r, handled := handleFrameMessage(hwnd, msg, wp, lp); handled {
		return r
	}
	// WM_NCCREATE carries the Window pointer in CREATESTRUCT.lpCreateParams,
	// which is how the instance reaches its own messages.
	if msg == WM_NCCREATE {
		if cs := ptrAt[createStruct](lp); cs != nil {
			// cs.CreateParams is CreateWindowExW's lpParam: the Window pointer
			// passed by New, handed back to us for exactly this purpose.
			if w := ptrAt[Window](cs.CreateParams); w != nil {
				w.hwnd = hwnd
				setWindowPtr(hwnd, w)
			}
		}
	}
	w := windowFromHwnd(hwnd)
	if w == nil {
		// Messages that arrive before WM_NCCREATE (and after WM_NCDESTROY) have
		// no instance to dispatch to.
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
		return r
	}
	return w.proc(msg, wp, lp)
}

type createStruct struct {
	CreateParams uintptr
	Instance     uintptr
	Menu         uintptr
	Parent       uintptr
	Cy, Cx       int32
	Y, X         int32
	Style        int32
	Name         uintptr
	Class        uintptr
	ExStyle      uintptr
}

// windowPtrs maps a window handle to its Window. Windows delivers messages on
// the thread that created the window, so a single map guarded by nothing more
// than that fact is enough, but the map is still guarded, because Post and
// Close are called from other goroutines and read it too.
var windowPtrs = map[uintptr]*Window{}

func setWindowPtr(hwnd uintptr, w *Window) {
	windowPtrsMu.Lock()
	windowPtrs[hwnd] = w
	windowPtrsMu.Unlock()
}

func windowFromHwnd(hwnd uintptr) *Window {
	windowPtrsMu.RLock()
	w := windowPtrs[hwnd]
	windowPtrsMu.RUnlock()
	return w
}

// New creates the window and its drawing surface. It must be called on the
// goroutine that will run the message loop, because a window procedure runs on
// the thread that created the window.
func New(opts WindowOptions, host Host) (*Window, error) {
	if runtime.GOMAXPROCS(0) > 1 {
		// Windows requires the window's creating thread to pump its messages.
		// Go may migrate a goroutine between OS threads at any preemption point,
		// which would leave the message loop on a different thread than the one
		// that owns the window and deadlock the UI. Pinning this goroutine makes
		// the invariant explicit instead of accidental.
		runtime.LockOSThread()
	}

	hInst, _, _ := kernel32GetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("ohmyjoNativeWindow")
	cursor, _, _ := procLoadCursorW.Call(0, IDC_ARROW)
	icon, _, _ := procLoadIconW.Call(hInst, appIconID)
	if icon == 0 {
		icon, _, _ = procLoadIconW.Call(0, IDI_APPLICATION)
	}
	title, _ := syscall.UTF16PtrFromString(opts.Title)

	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   wndProcCallback,
		HInstance:     hInst,
		HIcon:         icon,
		HIconSm:       icon,
		HCursor:       cursor,
		LpszClassName: className,
		// No background brush: every pixel is painted by Paint, and letting the
		// system erase the background first would flicker.
		HbrBackground: 0,
	}
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		if e, ok := err.(syscall.Errno); !ok || e != 1410 { // ERROR_CLASS_ALREADY_EXISTS
			return nil, errRegisterClass
		}
	}

	w := &Window{host: host, opts: opts, dpi: 96, fonts: cloneFonts(opts.Fonts)}
	// WS_OVERLAPPEDWINDOW, minus the caption and the frame that WM_NCCALCSIZE
	// then removes. Keeping the flags means Windows still treats it as a normal
	// resizable top-level window for snapping, minimising and restoring.
	const (
		wsOverlappedWindow = 0x00CF0000
		cwUseDefault       = 0x80000000
	)
	style := uintptr(wsOverlappedWindow)
	exStyle := uintptr(0)
	// The requested size is in logical units; convert to physical so the window
	// has the same apparent size on every display scale.
	dpi := systemDPI()
	w.dpi = dpi
	pxW := int(math.Round(float64(opts.Width) * dpi / 96))
	pxH := int(math.Round(float64(opts.Height) * dpi / 96))
	if opts.Width == 0 || opts.Height == 0 {
		pxW, pxH = cwUseDefault, cwUseDefault
	} else {
		var r rectT
		rect := rectT{0, 0, int32(pxW), int32(pxH)}
		procAdjustWindowRectExForDpi.Call(uintptr(unsafe.Pointer(&rect)), style, 0, exStyle, uintptr(uint32(dpi)))
		r = rect
		pxW = int(r.Right - r.Left)
		pxH = int(r.Bottom - r.Top)
	}

	hwnd, _, _ := procCreateWindowExW.Call(
		exStyle,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		style,
		cwUseDefault, cwUseDefault, uintptr(pxW), uintptr(pxH),
		0, 0, hInst,
		uintptr(unsafe.Pointer(w)),
	)
	if hwnd == 0 {
		return nil, errCreateWindow
	}
	w.hwnd = hwnd

	cw, ch := ClientSize(hwnd)
	g, err := NewGDI(cw, ch)
	if err != nil {
		procDestroyWindow.Call(hwnd)
		return nil, errNoSurface
	}
	g.SetTarget(hwnd)
	w.g = g
	// Select a slot before the first paint so the grid always has metrics even
	// if a host forgets to set one.
	w.applyFonts()

	applyFrameless(hwnd)
	if opts.Center {
		fitAndCenter(hwnd)
	}
	return w, nil
}

// cloneFonts copies the option map so a caller mutating its own map cannot
// change the window's fonts behind its back.
func cloneFonts(in map[FontID]FontSlot) map[FontID]FontSlot {
	out := make(map[FontID]FontSlot, len(in))
	for id, slot := range in {
		out[id] = slot
	}
	return out
}

// proc dispatches one message.
func (w *Window) proc(msg, wp, lp uintptr) uintptr {
	switch msg {
	case WM_PAINT:
		// Validate the update region before painting; painting inside
		// BeginPaint/EndPaint is what keeps Windows from repeating WM_PAINT
		// forever.
		var ps paintStruct
		hdc, _, _ := procBeginPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
		w.paint()
		procEndPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
		_ = hdc
		return 0

	case WM_ERASEBKGND:
		// The surface covers every pixel; erasing first would flicker.
		return 1

	case WM_SIZE:
		cw, ch := int(lp&0xffff), int((lp>>16)&0xffff)
		if w.g != nil && cw > 0 && ch > 0 {
			w.g.Resize(cw, ch)
			if w.host != nil {
				w.host.Resize(cw, ch)
			}
			w.Invalidate()
		}
		return 0

	case WM_DPICHANGED:
		w.dpi = float64(wp & 0xffff)
		if lp != 0 {
			r := ptrAt[rectT](lp)
			procSetWindowPos.Call(w.hwnd, 0,
				uintptr(r.Left), uintptr(r.Top),
				uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top),
				swpNoZOrder|swpNoActivate)
		}
		w.applyFonts()
		return 0

	case WM_CLOSE:
		if w.host != nil {
			w.host.Close()
		}
		procDestroyWindow.Call(w.hwnd)
		return 0

	case WM_DESTROY:
		procPostQuitMessage.Call(0)
		return 0

	case WM_SETCURSOR:
		// The caption is gone, so the arrow must be set explicitly over the
		// client area or the resize cursor would stick after a drag.
		procSetCursor.Call(loadArrow())
		return 1

	case wmRunOnUI:
		w.drain()
		return 0
	}

	if w.host != nil && w.host.Message(uint32(msg), wp, lp) {
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(w.hwnd, msg, wp, lp)
	return r
}

var arrowCursor uintptr

func loadArrow() uintptr {
	if arrowCursor == 0 {
		arrowCursor, _, _ = procLoadCursorW.Call(0, IDC_ARROW)
	}
	return arrowCursor
}

// applyFonts rebuilds every surface font slot for the current DPI, after a DPI
// change or a settings update, then tells the host to re-measure its layout.
func (w *Window) applyFonts() {
	if w.g == nil {
		return
	}
	for id, slot := range w.fonts {
		slot.DPI = w.dpi
		w.g.ConfigureFont(id, slot)
	}
	if w.host != nil {
		cw, ch := ClientSize(w.hwnd)
		w.host.Resize(cw, ch)
	}
	w.Invalidate()
}

// SetHost installs the delegate the window calls for painting, layout and
// unhandled messages.
//
// The window is created with its surface before its host exists, because the
// host needs the window's DPI and font metrics to build itself. Messages that
// arrive in that window are dropped, and the host must repaint once installed.
func (w *Window) SetHost(host Host) {
	if w.host != nil && host != nil {
		return
	}
	w.host = host
	if w.host != nil {
		cw, ch := ClientSize(w.hwnd)
		w.host.Resize(cw, ch)
	}
	w.Invalidate()
}

// SetFonts replaces the font slots and repaints. Called from the message thread.
func (w *Window) SetFonts(fonts map[FontID]FontSlot) {
	w.fonts = cloneFonts(fonts)
	w.applyFonts()
}

// SetClear is the colour a resize paints before the host draws, so a window
// being resized shows the theme background instead of uninitialised memory.
func (w *Window) SetClear(c Color) {
	if w.g != nil {
		w.g.SetClear(c)
	}
}

// paint draws one frame and presents it.
func (w *Window) paint() {
	if w.g == nil || w.host == nil {
		return
	}
	cw, ch := ClientSize(w.hwnd)
	w.g.Resize(cw, ch)
	w.g.SaveClip()
	w.g.Clip(0, 0, cw, ch)
	w.host.Paint(w.g, cw, ch)
	w.g.RestoreClip()
	w.g.Sync()
}

// Invalidate schedules a repaint.
func (w *Window) Invalidate() {
	if w.hwnd != 0 {
		procInvalidateRect.Call(w.hwnd, 0, 0)
	}
}

// Post queues fn to run on the window's message thread. It is how other
// goroutines (session readers, for one) hand work to the UI without touching
// GDI or the window from the wrong thread.
func (w *Window) Post(fn func()) {
	if w.hwnd == 0 || fn == nil {
		return
	}
	w.pendingMu.Lock()
	w.pending = append(w.pending, fn)
	w.pendingMu.Unlock()
	procPostMessageW.Call(w.hwnd, wmRunOnUI, 0, 0)
}

func (w *Window) drain() {
	w.pendingMu.Lock()
	fns := w.pending
	w.pending = nil
	w.pendingMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// Run pumps the message loop and blocks until the window is destroyed. It must
// be called on the goroutine that created the window.
func (w *Window) Run() error {
	procShowWindow.Call(w.hwnd, swShow)
	var m msgT
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(r) {
		case -1:
			return errors.New("GetMessageW failed")
		case 0:
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// HWND returns the native window handle.
func (w *Window) HWND() uintptr { return w.hwnd }

// DPI returns the window's current DPI.
func (w *Window) DPI() float64 { return w.dpi }

// SetTitle changes the window title.
func (w *Window) SetTitle(s string) {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return
	}
	procSetWindowTextW.Call(w.hwnd, uintptr(unsafe.Pointer(p)))
}

// Close requests the window to close.
func (w *Window) Close() {
	procPostMessageW.Call(w.hwnd, WM_CLOSE, 0, 0)
}

// Destroy closes the window from another goroutine.
func (w *Window) Destroy() {
	if w.hwnd != 0 {
		procPostMessageW.Call(w.hwnd, WM_CLOSE, 0, 0)
	}
}

// systemDPI reports the primary display DPI.
func systemDPI() float64 {
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return 96
	}
	dpi, _, _ := gdi32.NewProc("GetDeviceCaps").Call(hdc, 88) // LOGPIXELSX
	procReleaseDC.Call(0, hdc)
	if dpi == 0 {
		return 96
	}
	return float64(dpi)
}

// EnableDPIAwareness opts the process into per-monitor DPI awareness. It must
// run before any window exists; without it Windows lays out at 96 dpi and
// bitmap-stretches the result, which is what makes text look blurry.
func EnableDPIAwareness() {
	const DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = ^uintptr(3) // (HANDLE)-4
	if r, _, _ := procSetProcessDpiAwarenessContext.Call(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2); r != 0 {
		return
	}
	// Fall back for builds older than 1703.
	if shcore := syscall.NewLazyDLL("shcore.dll"); shcore.Load() == nil {
		if p := shcore.NewProc("SetProcessDpiAwareness"); p.Find() == nil {
			p.Call(2) // PROCESS_PER_MONITOR_DPI_AWARE
		}
	}
}

// centerOnScreen places the window in the middle of the primary display, used
// only when the monitor of the window cannot be determined.
func centerOnScreen(hwnd uintptr) {
	var wr rectT
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
	sw, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
	sh, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
	x := (int32(sw) - (wr.Right - wr.Left)) / 2
	y := (int32(sh) - (wr.Bottom - wr.Top)) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	procSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), 0, 0, swpNoSize|swpNoZOrder)
}
