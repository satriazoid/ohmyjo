//go:build !windows

package app

// Console ownership is a Windows concern: on other platforms the process has
// whatever stdio the shell gave it.
func setupConsole(bool) {}
