//go:build windows

package ui

import (
	"errors"
	"math"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCreateCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection      = gdi32.NewProc("CreateDIBSection")
	procDeleteDC              = gdi32.NewProc("DeleteDC")
	procSelectObject          = gdi32.NewProc("SelectObject")
	procDeleteObject          = gdi32.NewProc("DeleteObject")
	procCreateFontW           = gdi32.NewProc("CreateFontW")
	procExtTextOutW           = gdi32.NewProc("ExtTextOutW")
	procGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")
	procGetTextMetricsW       = gdi32.NewProc("GetTextMetricsW")
	procSetTextColor          = gdi32.NewProc("SetTextColor")
	procSetBkMode             = gdi32.NewProc("SetBkMode")
	procBitBlt                = gdi32.NewProc("BitBlt")
	procSaveDC                = gdi32.NewProc("SaveDC")
	procRestoreDC             = gdi32.NewProc("RestoreDC")
	procIntersectClipRect     = gdi32.NewProc("IntersectClipRect")

	procGetDC         = user32.NewProc("GetDC")
	procReleaseDC     = user32.NewProc("ReleaseDC")
	procGetClientRect = user32.NewProc("GetClientRect")
)

var (
	errGetDC      = errors.New("GetDC failed")
	errCreateDC   = errors.New("CreateCompatibleDC failed")
	errCreateFont = errors.New("CreateFontW failed")
	errNoBitmap   = errors.New("CreateDIBSection failed")
)

const (
	transparent = 1
	srcCopy     = 0x00CC0020

	biRGB        = 0
	dibRGBColors = 0

	defaultCharset = 1 // DEFAULT_CHARSET: lets font linking pick CJK/symbol faces
	outTTPrecis    = 7 // OUT_TT_PRECIS
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

type sizeT struct{ CX, CY int32 }

type textMetric struct {
	Height           int32
	Ascent           int32
	Descent          int32
	InternalLeading  int32
	ExternalLeading  int32
	AveCharWidth     int32
	MaxCharWidth     int32
	Weight           int32
	Overhang         int32
	DigitizedAspectX int32
	DigitizedAspectY int32
	FirstChar        uint16
	LastChar         uint16
	DefaultChar      uint16
	BreakChar        uint16
	Italic           byte
	Underlined       byte
	StruckOut        byte
	PitchAndFamily   byte
	FaceName         [32]uint16
}

type fontKey struct {
	bold, italic, underline, strike bool
}

// fontCacheLimit bounds the style-variant cache per slot. Real output uses a
// handful of combinations; the cap only guards against adversarial churn.
const fontCacheLimit = 64

// FontSlot describes one font the renderer may select.
type FontSlot struct {
	// Family is a CSS-style stack; the first installed family is used.
	Family string
	// SizePx is the em size in logical units at 96 dpi.
	SizePx float64
	Bold   bool
	// LineH is the row advance as a multiple of the em size. Zero means "the
	// font's own height", which is right for chrome and wrong for a terminal
	// grid, where the configured line height must be honoured.
	LineH float64
	// DPI is the scale the slot is laid out at.
	DPI float64
}

type fontVariant struct {
	key  fontKey
	face string
	h    uintptr
}

// fontSet is one configured font slot: a family at a size, plus the bold /
// italic / underline variants GDI needs separately created handles for.
type fontSet struct {
	face  string
	emPx  int
	slots []fontVariant
	m     Metrics
	lineH int
}

// GDI is a Surface that draws into a top-down 32-bit DIB section and presents
// it with one BitBlt per frame.
//
// Drawing into memory rather than straight at the window is what keeps this
// cheap: a full repaint is one ExtTextOutW per same-colour run per row plus a
// single BitBlt, instead of one GDI call per cell — the per-cell shape is two
// orders of magnitude slower.
type GDI struct {
	mem    uintptr // memory DC, owns the selected font and bitmap
	bmp    uintptr
	old    uintptr
	target uintptr  // window Sync presents to
	pix    []uint32 // DIB pixels, len w*h
	w, h   int

	sets     map[FontID]*fontSet
	cur      FontID
	selected uintptr

	clear Color
}

// NewGDI creates a drawing surface with no fonts configured. ConfigureFont must
// be called for every FontID the renderer will use before drawing.
func NewGDI(width, height int) (*GDI, error) {
	screen, _, _ := procGetDC.Call(0)
	if screen == 0 {
		return nil, errGetDC
	}
	mem, _, _ := procCreateCompatibleDC.Call(screen)
	procReleaseDC.Call(0, screen)
	if mem == 0 {
		return nil, errCreateDC
	}
	// cur starts at -1, not 0: FontMono is 0, so a zero value would claim the
	// mono slot was already selected and no font would ever be applied — leaving
	// GDI's proportional default in place and misaligning every cell.
	g := &GDI{mem: mem, sets: map[FontID]*fontSet{}, cur: -1}
	g.Resize(width, height)
	if g.pix == nil {
		g.Close()
		return nil, errNoBitmap
	}
	return g, nil
}

// ConfigureFont creates or replaces a font slot. A family that is not installed
// falls back through the stack and finally to the renderer's monospace default,
// because CreateFontW silently substitutes an arbitrary — possibly
// proportional — face for an unknown name, which would misalign the grid.
func (g *GDI) ConfigureFont(id FontID, slot FontSlot) {
	// The grid needs a face whose advances are uniform; a proportional face
	// would make every column after the first drift.
	face := ResolveMonoFamily(slot.Family)
	size := slot.SizePx
	if size <= 0 {
		size = 14
	}
	dpi := slot.DPI
	if dpi <= 0 {
		dpi = 96
	}
	em := int(math.Round(size * dpi / 96))
	if em < 1 {
		em = 1
	}
	lineH := 0
	if slot.LineH > 0 {
		lineH = int(math.Round(size * slot.LineH * dpi / 96))
	}
	key := id
	if old := g.sets[key]; old != nil {
		old.release()
	}
	fs := &fontSet{face: face, emPx: em, lineH: lineH}
	if slot.Bold {
		fs.slots = append(fs.slots, fontVariant{key: fontKey{bold: true}})
	} else {
		fs.slots = append(fs.slots, fontVariant{key: fontKey{}})
	}
	if !fs.build() {
		return
	}
	g.sets[key] = fs
	if g.cur == id {
		// Re-select: the previously selected handle has been deleted.
		g.cur = -1
		g.SetFont(id)
	}
}

func (fs *fontSet) release() {
	for _, v := range fs.slots {
		if v.h != 0 {
			procDeleteObject.Call(v.h)
		}
	}
	fs.slots = nil
}

// build creates the handles this slot needs, measuring the base one.
func (fs *fontSet) build() bool {
	for i := range fs.slots {
		h := createFont(fs.face, fs.emPx, fs.slots[i].key)
		if h == 0 {
			fs.release()
			return false
		}
		fs.slots[i].h = h
	}
	return true
}

func createFont(face string, emPx int, k fontKey) uintptr {
	p, err := syscall.UTF16PtrFromString(face)
	if err != nil {
		return 0
	}
	weight := uintptr(400)
	if k.bold {
		weight = 700
	}
	b2i := func(b bool) uintptr {
		if b {
			return 1
		}
		return 0
	}
	// A negative height asks for a character height, not a cell height, which is
	// what makes the glyphs track the configured font size.
	// CreateFontW takes fourteen arguments: height, width, escapement,
	// orientation, weight, italic, underline, strikeout, charset, output
	// precision, clip precision, quality, pitch-and-family, then the face name.
	// Dropping any one of them shifts the face name into the wrong slot and GDI
	// silently returns the default GUI font — proportional, and the same
	// whatever family was asked for.
	h, _, _ := procCreateFontW.Call(
		uintptr(int32(-emPx)),
		0, 0, 0,
		weight,
		b2i(k.italic), b2i(k.underline), b2i(k.strike),
		defaultCharset, outTTPrecis, 0, 0, 0,
		uintptr(unsafe.Pointer(p)),
	)
	return h
}

// SetFont selects the slot used by Text and TextWidth and returns its metrics.
func (g *GDI) SetFont(id FontID) Metrics {
	fs := g.sets[id]
	if fs == nil {
		return Metrics{}
	}
	if g.cur != id || g.selected != fs.slots[0].h {
		procSelectObject.Call(g.mem, fs.slots[0].h)
		g.selected = fs.slots[0].h
		g.cur = id
		g.measure(fs)
	}
	return fs.metrics()
}

func (fs *fontSet) metrics() Metrics {
	return fs.m
}

// measure reads the font geometry for a slot. The caller has selected it.
func (g *GDI) measure(fs *fontSet) {
	var tm textMetric
	procGetTextMetricsW.Call(g.mem, uintptr(unsafe.Pointer(&tm)))
	cellW := int(tm.AveCharWidth)
	if cellW <= 0 {
		cellW = int(tm.MaxCharWidth)
	}
	if cellW <= 0 {
		cellW = fs.emPx / 2
	}
	lineH := fs.lineH
	if lineH <= 0 {
		lineH = int(tm.Height)
	}
	if lineH <= 0 {
		lineH = fs.emPx
	}
	ascent := int(tm.Ascent)
	if ascent <= 0 {
		ascent = fs.emPx * 3 / 4
	}
	descent := int(tm.Descent)
	if descent <= 0 {
		descent = fs.emPx - ascent
	}
	fs.m = Metrics{CellW: cellW, Ascent: ascent, Descent: descent, LineH: lineH}
}

// Metrics reports the current slot's geometry.
func (g *GDI) Metrics() Metrics {
	if fs := g.sets[g.cur]; fs != nil {
		return fs.m
	}
	return Metrics{}
}

// variant returns the font handle for a style, creating it on first use.
func (g *GDI) variant(st Style) uintptr {
	fs := g.sets[g.cur]
	if fs == nil {
		return 0
	}
	k := fontKey{bold: st.Bold, italic: st.Italic, underline: st.Underline, strike: st.Strikeout}
	for _, v := range fs.slots {
		if v.key == k {
			return v.h
		}
	}
	if len(fs.slots) >= fontCacheLimit {
		// Evict the oldest non-base variant. Styles are few in practice, so this
		// only triggers on adversarial output.
		victim := 1
		procDeleteObject.Call(fs.slots[victim].h)
		fs.slots = append(fs.slots[:victim], fs.slots[victim+1:]...)
	}
	h := createFont(fs.face, fs.emPx, k)
	if h == 0 {
		return fs.slots[0].h
	}
	fs.slots = append(fs.slots, fontVariant{key: k, h: h})
	return h
}

// Resize reallocates the backing bitmap. Called on window resize and DPI change.
func (g *GDI) Resize(width, height int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	if g.pix != nil && width == g.w && height == g.h {
		return
	}
	bi := bitmapInfo{}
	bi.Header.Size = uint32(unsafe.Sizeof(bitmapInfoHeader{}))
	bi.Header.Width = int32(width)
	// Negative height: a top-down DIB, so row 0 is the top and rows can be
	// indexed directly.
	bi.Header.Height = -int32(height)
	bi.Header.Planes = 1
	bi.Header.BitCount = 32
	bi.Header.Compression = biRGB
	bi.Header.SizeImage = uint32(width * height * 4)

	var bits unsafe.Pointer
	bmp, _, _ := procCreateDIBSection.Call(g.mem, uintptr(unsafe.Pointer(&bi)), dibRGBColors,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return
	}
	if g.bmp != 0 {
		if g.old != 0 {
			procSelectObject.Call(g.mem, g.old)
		}
		procDeleteObject.Call(g.bmp)
	}
	g.old, _, _ = procSelectObject.Call(g.mem, bmp)
	g.bmp = bmp
	g.w, g.h = width, height
	g.pix = unsafe.Slice((*uint32)(unsafe.Pointer(bits)), width*height)
	g.FillAll(g.clear)
	// Selecting the bitmap resets the DC's font, so the cached selection is
	// stale and the next SetFont must re-apply.
	g.cur = -1
}

// Size reports the surface size in physical pixels.
func (g *GDI) Size() (int, int) { return g.w, g.h }

// SetClear records the colour FillAll paints with, so a resize repaints in the
// theme background instead of uninitialised memory.
func (g *GDI) SetClear(c Color) { g.clear = c }

// At reports the pixel at (x, y). It exists so tests and diagnostics can assert
// that something was actually drawn, without a screenshot.
func (g *GDI) At(x, y int) Color {
	if g.pix == nil || x < 0 || y < 0 || x >= g.w || y >= g.h {
		return 0
	}
	return Color(g.pix[y*g.w+x])
}

// FillAll paints the whole surface.
func (g *GDI) FillAll(c Color) {
	if g.pix == nil {
		return
	}
	v := uint32(c)
	p := g.pix
	for i := range p {
		p[i] = v
	}
}

// Fill paints a solid rectangle.
func (g *GDI) Fill(x, y, w, h int, c Color) {
	if w <= 0 || h <= 0 || g.pix == nil {
		return
	}
	x0, y0, x1, y1 := x, y, x+w, y+h
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > g.w {
		x1 = g.w
	}
	if y1 > g.h {
		y1 = g.h
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}
	v := uint32(c)
	p := g.pix
	for yy := y0; yy < y1; yy++ {
		row := p[yy*g.w+x0 : yy*g.w+x1]
		for i := range row {
			row[i] = v
		}
	}
}

// Text draws s with its top-left at (x, y).
func (g *GDI) Text(x, y int, s string, st Style) {
	if s == "" {
		return
	}
	if g.cur == -1 {
		return
	}
	h := g.variant(st)
	if h == 0 {
		return
	}
	if g.selected != h {
		procSelectObject.Call(g.mem, h)
		g.selected = h
	}
	u16, err := syscall.UTF16FromString(s)
	if err != nil || len(u16) <= 1 {
		return
	}
	procSetTextColor.Call(g.mem, uintptr(colorRef(st.FG)))
	procSetBkMode.Call(g.mem, transparent)
	procExtTextOutW.Call(g.mem,
		uintptr(int32(x)), uintptr(int32(y)),
		0, 0,
		uintptr(unsafe.Pointer(&u16[0])), uintptr(len(u16)-1), 0, 0)
	runtime.KeepAlive(s)
}

// TextWidth measures one line of text in the current font.
func (g *GDI) TextWidth(s string) int {
	if s == "" || g.cur == -1 {
		return 0
	}
	u16, err := syscall.UTF16FromString(s)
	if err != nil || len(u16) <= 1 {
		return 0
	}
	var sz sizeT
	procGetTextExtentPoint32W.Call(g.mem,
		uintptr(unsafe.Pointer(&u16[0])), uintptr(len(u16)-1),
		uintptr(unsafe.Pointer(&sz)))
	runtime.KeepAlive(s)
	return int(sz.CX)
}

// Sync presents the frame to the target window in one BitBlt. The window DC is
// acquired per frame rather than held, so a resize or DPI change cannot leave a
// stale DC behind.
func (g *GDI) Sync() {
	if g.bmp == 0 || g.target == 0 || g.pix == nil {
		return
	}
	win, _, _ := procGetDC.Call(g.target)
	if win == 0 {
		return
	}
	procBitBlt.Call(win, 0, 0, uintptr(g.w), uintptr(g.h), g.mem, 0, 0, srcCopy)
	procReleaseDC.Call(g.target, win)
}

// SetTarget names the window Sync presents to.
func (g *GDI) SetTarget(hwnd uintptr) { g.target = hwnd }

// SaveClip pushes the current clip region.
func (g *GDI) SaveClip() { procSaveDC.Call(g.mem) }

// RestoreClip pops to the last saved clip region.
func (g *GDI) RestoreClip() { procRestoreDC.Call(g.mem, ^uintptr(0)) }

// Clip intersects the clip region with a rectangle and reports whether any part
// of the rectangle lies inside the surface, so a caller can skip drawing
// entirely.
func (g *GDI) Clip(x, y, w, h int) bool {
	if x >= g.w || y >= g.h || x+w <= 0 || y+h <= 0 {
		return false
	}
	procIntersectClipRect.Call(g.mem,
		uintptr(int32(x)), uintptr(int32(y)),
		uintptr(int32(x+w)), uintptr(int32(y+h)))
	return true
}

func colorRef(c Color) uint32 {
	return uint32(c.R()) | uint32(c.G())<<8 | uint32(c.B())<<16
}

func (g *GDI) Close() {
	for id, fs := range g.sets {
		fs.release()
		delete(g.sets, id)
	}
	g.sets = nil
	g.cur = -1
	if g.bmp != 0 {
		if g.old != 0 {
			procSelectObject.Call(g.mem, g.old)
		}
		procDeleteObject.Call(g.bmp)
		g.bmp, g.old, g.pix = 0, 0, nil
	}
	if g.mem != 0 {
		procDeleteDC.Call(g.mem)
		g.mem = 0
	}
}

// ClientSize reports a window's client area in physical pixels.
func ClientSize(hwnd uintptr) (int, int) {
	var r rectT
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return int(r.Right - r.Left), int(r.Bottom - r.Top)
}
