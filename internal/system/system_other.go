//go:build !windows

package system

import "os/exec"

func installedFontNames() []string {
	return []string{"monospace"}
}

func reveal(path string) error {
	if path == "" {
		path = "."
	}
	return exec.Command("xdg-open", path).Start()
}

func openTarget(target string) error {
	if target == "" {
		return nil
	}
	return exec.Command("xdg-open", target).Start()
}
