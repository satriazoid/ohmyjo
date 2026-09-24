//go:build !windows

package app

// The frame replacement and the window controls are Windows-specific. The other
// builds only have the headless server, so these are no-ops that keep the shared
// call sites compiling.

type bindable interface {
	Bind(name string, f interface{}) error
}

func installSubclass(hwnd uintptr) {}

func applyFrameless(hwnd uintptr) {}

func bindWindowControls(b bindable) {}
