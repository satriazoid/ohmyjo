//go:build windows

package session

import (
	"github.com/UserExistsError/conpty"
)

// spawnProcess creates a ConPTY of the given size and starts cmdline inside it.
func spawnProcess(cmdline, cwd string, env []string, cols, rows int) (process, error) {
	if !conpty.IsConPtyAvailable() {
		return nil, ErrNoConPTY
	}
	return conpty.Start(cmdline,
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyWorkDir(cwd),
		conpty.ConPtyEnv(env),
	)
}
