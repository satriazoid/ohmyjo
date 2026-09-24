//go:build !windows

package app

// DPI awareness is a Windows-only concern; other platforms have no equivalent
// bitmap-stretching behaviour to opt out of.
func enableDpiAwareness() {}

func systemDpi() float64 { return 96 }
