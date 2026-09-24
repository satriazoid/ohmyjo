//go:build windows

package system

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW stops Windows from allocating a console at all.
const createNoWindow = 0x08000000

// HideConsole marks cmd so it never flashes a console window.
//
// The app is built with -H=windowsgui, so it owns no console. A console
// program started from such a process gets a brand new one, and Windows shows
// that console as a window — a small black rectangle that appears and vanishes
// as the helper runs. Every helper here exists to answer a question in the
// background, so the console is pure visual noise.
func HideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
