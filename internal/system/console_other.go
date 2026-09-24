//go:build !windows

package system

import "os/exec"

// HideConsole is a no-op away from Windows, where starting a child never opens
// a console window of its own.
func HideConsole(cmd *exec.Cmd) {}
