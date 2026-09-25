//go:build windows

package ui

import (
	"strings"
	"syscall"
	"unsafe"
)

var enumFontProc = gdi32.NewProc("EnumFontFamiliesExW")

type logFont struct {
	Height         int32
	Width          int32
	Escapement     int32
	Orientation    int32
	Weight         int32
	Italic         byte
	Underline      byte
	StrikeOut      byte
	CharSet        byte
	OutPrecision   byte
	ClipPrecision  byte
	Quality        byte
	PitchAndFamily byte
	FaceName       [32]uint16
}

// FontStackFallback is used when nothing in the configured stack is installed.
const FontStackFallback = "Consolas"

// faceNameOffset is where the face name sits inside the ENUMLOGFONTEXW the
// enumeration callback receives: LOGFONTW is its first member and FaceName is
// its last field.
var faceNameOffset = unsafe.Offsetof(logFont{}.FaceName)

// fontEnumTarget is the name the callback is looking for, and fontMatchFlag
// records whether it was seen. Both are set immediately before the enumeration
// call and read immediately after it, on the same thread, so no synchronisation
// is needed. They are package-level because syscall.NewCallback cannot close
// over anything.
var (
	fontEnumTarget []uint16
	fontMatchFlag  uintptr
)

// fontEnumCallback is created once: syscall.NewCallback never releases its slot,
// so creating one per lookup would leak a slot on every config reload.
//
// It must compare the enumerated face name against the request. EnumFontFamiliesExW
// does not fail for an unknown name — it enumerates every installed font instead
// — so a callback that accepted the first result would report every font as
// installed, including ones that are not, and GDI would then substitute a
// proportional face for a name the user believed was monospaced.
var fontEnumCallback = syscall.NewCallback(func(enumFont, _, _, _ uintptr) uintptr {
	if enumFont != 0 && fontEnumTarget != nil {
		face := ptrAt[[32]uint16](enumFont + faceNameOffset)
		if strings.EqualFold(syscall.UTF16ToString(face[:]), syscall.UTF16ToString(fontEnumTarget)) {
			fontMatchFlag = 1
			return 0 // found it; stop enumerating
		}
	}
	return 1 // keep going
})

// SplitFontStack turns a CSS font-family list into individual family names.
// Generic CSS keywords are dropped: a terminal needs a concrete monospaced face
// and "monospace" is not an installed font name.
func SplitFontStack(stack string) []string {
	parts := strings.Split(stack, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `'"`)
		if p == "" {
			continue
		}
		switch strings.ToLower(p) {
		case "monospace", "sans-serif", "serif", "cursive", "fantasy",
			"system-ui", "ui-monospace", "ui-sans-serif", "inherit", "initial":
			continue
		}
		out = append(out, p)
	}
	return out
}

// ResolveFontFamily returns the first installed family from a CSS font stack.
//
// This cannot be skipped: GDI's CreateFontW silently substitutes an arbitrary
// face for an unknown name, and the substitute is usually proportional, which
// would make the terminal grid misalign. Resolving here means the renderer
// either gets a real monospaced face or a known-good fallback.
func ResolveFontFamily(stack string) string {
	for _, name := range SplitFontStack(stack) {
		if fontInstalled(name) {
			return name
		}
	}
	if fontInstalled(FontStackFallback) {
		return FontStackFallback
	}
	return ""
}

func fontInstalled(name string) bool {
	// LF_FACESIZE-1: the field in LOGFONTW is fixed width, and a longer name is
	// truncated silently, so it could never match.
	if name == "" || len(name) > 31 {
		return false
	}
	face, err := syscall.UTF16FromString(name)
	if err != nil {
		return false
	}
	// EnumFontFamiliesExW requires a device context; passing 0 makes it fail
	// without ever calling the callback, which would report nothing as
	// installed.
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		// Without a DC the check cannot be made. Assume installed and let GDI
		// substitute, rather than rejecting a font that is probably present.
		return true
	}
	defer procReleaseDC.Call(0, hdc)

	lf := logFont{CharSet: defaultCharset}
	copy(lf.FaceName[:], face)
	fontEnumTarget = face
	fontMatchFlag = 0
	enumFontProc.Call(hdc, uintptr(unsafe.Pointer(&lf)), fontEnumCallback, 0)
	fontEnumTarget = nil
	return fontMatchFlag != 0
}

// ResolveMonoFamily is ResolveFontFamily for a terminal grid: on top of
// preferring an installed family it verifies the face is actually monospaced.
//
// This is the guard against the failure that motivated the whole resolution
// path. GDI silently substitutes a face for an unknown name, and its substitute
// is usually proportional; the grid would then draw each cell's glyph at that
// font's own advance width and every column after the first would drift.
func ResolveMonoFamily(stack string) string {
	for _, name := range SplitFontStack(stack) {
		if fontInstalled(name) && isMonospaced(name) {
			return name
		}
	}
	if fontInstalled(FontStackFallback) && isMonospaced(FontStackFallback) {
		return FontStackFallback
	}
	return FontStackFallback
}

// isMonospaced reports whether a family's glyphs all share one advance width.
// A spread of probe characters is used rather than the pitch bits in
// TEXTMETRIC, because the pitch bits are unreliable for linked fonts — notably
// for CJK faces, where GDI reports variable pitch regardless.
func isMonospaced(name string) bool {
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		// No way to measure; assume the caller knows. Rejecting here would make
		// a working configuration fall back for no reason.
		return true
	}
	defer procReleaseDC.Call(0, hdc)

	h := createFont(name, 16, fontKey{})
	if h == 0 {
		return false
	}
	old, _, _ := procSelectObject.Call(hdc, h)
	if old == 0 {
		procDeleteObject.Call(h)
		return false
	}

	widthOf := func(r rune) int {
		u16, err := syscall.UTF16FromString(string(r))
		if err != nil || len(u16) <= 1 {
			return 0
		}
		var sz sizeT
		procGetTextExtentPoint32W.Call(hdc,
			uintptr(unsafe.Pointer(&u16[0])), uintptr(len(u16)-1),
			uintptr(unsafe.Pointer(&sz)))
		return int(sz.CX)
	}
	base := widthOf('M')
	mono := base > 0
	if mono {
		// Narrow, wide, punctuation, digit and lower case: a proportional face
		// differs on at least one of these.
		for _, r := range []rune{'i', 'W', '.', '@', 'm', '0', 'l'} {
			if widthOf(r) != base {
				mono = false
				break
			}
		}
	}

	procSelectObject.Call(hdc, old)
	procDeleteObject.Call(h)
	return mono
}
