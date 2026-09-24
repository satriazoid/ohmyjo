//go:build windows

package system

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Native folder/file picker.
//
// The Windows common file dialogs are COM objects, so they are driven through
// hand-rolled vtables rather than a cgo binding: the app is built with
// CGO_ENABLED=0 to keep the binary small and dependency-free. The dialog runs on
// its own apartment-threaded goroutine because the webview message loop owns the
// main thread and a modal dialog there would deadlock the UI.

var (
	clsidFileOpenDialog = windows.GUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidFileOpenDialog   = windows.GUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidShellItem        = windows.GUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE, Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

const (
	coinitApartmentThreaded = 0x2
	rpcEChangedMode         = 0x80010106

	fosPickFolders       = 0x00000020
	fosForceFileSystem   = 0x00000040
	fosPathMustExist     = 0x00000800
	sigdnDesktopAbsolute = 0x80028000
)

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")
)

// vtableCall invokes method `index` on a COM interface pointer.
//
// The vtable pointer is kept as a typed Go pointer so the only uintptr in play
// is the final function address passed to the syscall, which is the pattern the
// unsafe rules allow.
func vtableCall(iface unsafe.Pointer, index int, args ...uintptr) uintptr {
	vtbl := *(**uintptr)(iface)
	fn := *(*uintptr)(unsafe.Add(unsafe.Pointer(vtbl), uintptr(index)*unsafe.Sizeof(uintptr(0))))
	call := append([]uintptr{uintptr(iface)}, args...)
	ret, _, _ := syscall.SyscallN(fn, call...)
	return ret
}

func comRelease(iface unsafe.Pointer) {
	if iface != nil {
		vtableCall(iface, 2)
	}
}

// PickFolder shows the native folder picker and returns the chosen path.
// An empty string with a nil error means the user cancelled.
func (s *Service) PickFolder(title string) (string, error) {
	return runDialog(title, true)
}

// PickFile shows the native open-file dialog.
func (s *Service) PickFile(title string) (string, error) {
	return runDialog(title, false)
}

func runDialog(title string, folder bool) (string, error) {
	type result struct {
		path string
		err  error
	}
	// The dialog needs a COM apartment. A fresh OS thread is requested so the
	// apartment is owned solely by this call and torn down afterwards.
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
		// S_FALSE (1) means the apartment was already initialised; either way
		// this thread now owns a reference that must be released.
		initialised := hr == 0 || hr == 1
		if !initialised && uint32(hr) != rpcEChangedMode {
			done <- result{err: errors.New("could not initialise COM")}
			return
		}
		if initialised {
			defer procCoUninitialize.Call()
		}

		var dialog unsafe.Pointer
		// CoCreateInstance(rclsid, pUnkOuter, dwClsContext, riid, ppv) — note
		// pUnkOuter is a pointer argument, so it takes a slot in the arg list.
		hr, _, _ = procCoCreateInstance.Call(
			uintptr(unsafe.Pointer(&clsidFileOpenDialog)),
			0,
			1, // CLSCTX_INPROC_SERVER
			uintptr(unsafe.Pointer(&iidFileOpenDialog)),
			uintptr(unsafe.Pointer(&dialog)),
		)
		if hr != 0 || dialog == nil {
			done <- result{err: errors.New("could not create the file dialog")}
			return
		}
		defer comRelease(dialog)

		// IFileDialog::SetOptions is vtable slot 9.
		opts := uintptr(fosForceFileSystem | fosPathMustExist)
		if folder {
			opts |= fosPickFolders
		}
		if hr := vtableCall(dialog, 9, opts); hr != 0 {
			done <- result{err: errors.New("file dialog rejected the options")}
			return
		}
		if title != "" {
			// IFileDialog::SetTitle is slot 17.
			wide, err := windows.UTF16PtrFromString(title)
			if err == nil {
				vtableCall(dialog, 17, uintptr(unsafe.Pointer(wide)))
			}
		}

		// IModalWindow::Show is slot 3; a nil owner keeps it usable while the
		// WebView keeps rendering.
		if hr := vtableCall(dialog, 3, 0); hr != 0 {
			// 0x800704C7 is the documented "cancelled by the user" code.
			if uint32(hr) == 0x800704C7 {
				done <- result{}
				return
			}
			done <- result{err: errors.New("the file dialog could not be shown")}
			return
		}

		var item unsafe.Pointer
		// IFileDialog::GetResult is vtable slot 20. Slot 27 would be
		// IFileOpenDialog::GetResults, which returns an array, not an item.
		if hr := vtableCall(dialog, 20, uintptr(unsafe.Pointer(&item))); hr != 0 || item == nil {
			done <- result{err: errors.New("the dialog returned no selection")}
			return
		}
		defer comRelease(item)

		var name *uint16
		// IShellItem::GetDisplayName is slot 5.
		if hr := vtableCall(item, 5, sigdnDesktopAbsolute, uintptr(unsafe.Pointer(&name))); hr != 0 || name == nil {
			done <- result{err: errors.New("the selection has no filesystem path")}
			return
		}
		defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(name)))
		done <- result{path: windows.UTF16PtrToString(name)}
	}()

	res := <-done
	return res.path, res.err
}
