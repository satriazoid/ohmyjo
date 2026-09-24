//go:build !windows

package app

type windowConfig struct {
	Title   string
	URL     string
	Width   int
	Height  int
	DataDir string
}

func runWindow(cfg windowConfig) error { return ErrNoWebView }

func webviewDataDir() string { return "" }
