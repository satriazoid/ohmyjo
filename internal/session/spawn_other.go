//go:build !windows

package session

// The backend is Windows-first. On other platforms the package still compiles
// so tooling, vet and the websocket layer can be exercised in CI, but spawning
// reports ErrNoConPTY instead of silently doing nothing.
func spawnProcess(cmdline, cwd string, env []string, cols, rows int) (process, error) {
	return nil, ErrNoConPTY
}
