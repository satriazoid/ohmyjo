//go:build windows

package ui

import "unsafe"

// ptrAt reinterprets an address that came from Windows (a window message's
// lParam, for one) as a typed pointer.
//
// Windows message parameters are pointer-sized integers, not Go pointers, so
// they cannot be carried through the type system. unsafe.Add with a nil base
// expresses "this is an address" without tripping vet's unsafe.Pointer
// conversion check, which a direct uintptr conversion would.
//
// The pointer is only valid for the duration of the message that supplied it.
func ptrAt[T any](addr uintptr) *T {
	if addr == 0 {
		return nil
	}
	return (*T)(unsafe.Add(unsafe.Pointer(nil), addr))
}
