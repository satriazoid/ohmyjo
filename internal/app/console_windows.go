//go:build windows

package app

import (
	"log"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"ohmyjo/internal/config"
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procAttachConsole = kernel32.NewProc("AttachConsole")
	procFreeConsole   = kernel32.NewProc("FreeConsole")
)

// attachParentProcess and freeConsole are the kernel32 values; ATTACH_PARENT_PROCESS
// is -1 and is passed as the pointer-sized signed value.
const attachParentProcess = ^uintptr(0)

// setupConsole makes logging behave the way a desktop application should.
//
// A GUI build is linked as a Windows-subsystem binary (see the Makefile), so
// double-clicking it never opens a console window. A plain `go build` produces a
// console-subsystem binary instead, and Windows then creates a console window
// for it, which is exactly the "opening the terminal opens another terminal"
// behaviour, so the console is detached here as well.
//
// With no console left there is nowhere for the log to go, so a windowed run
// writes it to a file next to the config. Headless is a diagnostic mode, so it
// re-attaches to the console it was launched from and keeps logging there.
func setupConsole(headless bool) {
	if headless {
		attachParentConsole()
		return
	}
	if ok, _, _ := procFreeConsole.Call(); ok == 0 {
		// No console to detach from: already a GUI-subsystem binary.
		return
	}
	f, err := os.OpenFile(logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		// Dropping the log is better than a windowed app that cannot start.
		return
	}
	log.SetOutput(f)
}

// attachParentConsole borrows the console of the process that launched us and
// rebinds the standard streams onto it; a GUI-subsystem process starts without
// usable handles, so writing to os.Stdout would otherwise go nowhere.
func attachParentConsole() {
	if ok, _, _ := procAttachConsole.Call(attachParentProcess); ok == 0 {
		return
	}
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = out
		os.Stderr = out
		log.SetOutput(out)
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = in
	}
}

// logPath is the windowed run's log file, beside config.json so both live and
// die together.
func logPath() string {
	dir := filepath.Dir(config.DefaultPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return filepath.Join(os.TempDir(), "ohmyjo.log")
	}
	return filepath.Join(dir, "ohmyjo.log")
}
