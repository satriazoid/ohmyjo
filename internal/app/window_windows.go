//go:build windows

package app

import (
	"log"
	"os"
	"path/filepath"

	webview "github.com/jchv/go-webview2"
)

type windowConfig struct {
	Title   string
	URL     string
	Width   int
	Height  int
	DataDir string
}

// runWindow creates the WebView2 window and pumps its message loop. Run() must
// own the main goroutine on Windows, so this blocks until the window closes.
func runWindow(cfg windowConfig) error {
	// go-webview2 passes these straight to CreateWindowEx, which takes physical
	// pixels. Now that the process is DPI-aware the numbers must be scaled, or
	// the window would shrink to 80% of its intended size at 125%.
	scale := systemDpi() / 96
	log.Printf("dpi: system %g dpi (scale %.2f), window %dx%d physical",
		systemDpi(), scale, uint(float64(cfg.Width)*scale), uint(float64(cfg.Height)*scale))
	w := webview.NewWithOptions(webview.WebViewOptions{
		Debug:     debugEnabled(),
		DataPath:  cfg.DataDir,
		AutoFocus: true,
		WindowOptions: webview.WindowOptions{
			Title:  cfg.Title,
			Width:  uint(float64(cfg.Width) * scale),
			Height: uint(float64(cfg.Height) * scale),
			Center: true,
		},
	})
	if w == nil {
		return ErrNoWebView
	}
	hwnd := uintptr(w.Window())
	installSubclass(hwnd)
	// Install the frame replacement before the window is shown so the caption
	// never has a chance to paint.
	applyFrameless(hwnd)
	bindWindowControls(w)
	w.Navigate(cfg.URL)
	w.Run()
	return nil
}

func debugEnabled() bool { return os.Getenv("OHMYJO_DEBUG") == "1" }

// webviewDataDir keeps the WebView2 user data folder out of the install
// directory, which may be read-only.
func webviewDataDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "ohmyjo", "webview")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("webview data dir: %v", err)
	}
	return dir
}
