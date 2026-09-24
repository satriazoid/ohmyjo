// Package app assembles the desktop application: config, sessions, local HTTP
// server and the WebView2 window that renders the React frontend.
package app

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"

	"ohmyjo/internal/config"
	"ohmyjo/internal/history"
	"ohmyjo/internal/server"
	"ohmyjo/internal/session"
	"ohmyjo/internal/system"
	"ohmyjo/internal/webui"
)

// Options are the command line knobs.
type Options struct {
	Headless   bool
	Port       int
	DevServer  string
	ConfigPath string
	Cwd        string
}

// Run starts the application and blocks until the window closes (or, in
// headless mode, until the process is signalled).
func Run(opts Options) error {
	// Must happen before any window exists, so the WebView2 window is created
	// at the display's real pixel density instead of being stretched (blurry).
	enableDpiAwareness()
	// A windowed run must not drag a console window along with it; see
	// setupConsole.
	setupConsole(opts.Headless)

	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	loader, err := config.NewLoader(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if stopWatch, err := loader.Watch(); err != nil {
		// Losing the watcher disables hot reload; it must not stop the app.
		log.Printf("config watch unavailable: %v", err)
	} else {
		defer stopWatch()
	}

	mgr := session.NewManager(nil)
	hist := history.New()
	histFile := history.OpenFile(config.HistoryPath())
	mgr.Seed = func(id string) {
		// A shell loads its own history file at startup; new panes get the same
		// treatment so Up recalls commands from previous runs.
		hist.Seed(id, histFile.Load())
	}
	srv := server.New(loader, mgr, system.New(), &embedAssets{fsys: webui.FS()}, hist, histFile)
	loader.OnChange(func(*config.Config, []string) { srv.BroadcastConfig() })

	url, err := srv.Listen(opts.Port)
	if err != nil {
		return fmt.Errorf("listen on 127.0.0.1:%d: %w", opts.Port, err)
	}
	log.Printf("ohmyjo %s serving %s (config %s)", server.Version, url, loader.Path())

	go func() {
		if err := srv.Serve(); err != nil {
			log.Printf("http server: %v", err)
		}
	}()

	// Closing the window or signalling the process must take every shell with
	// it: a terminal that leaks pwsh.exe processes is worse than no terminal.
	shutdown := func() {
		mgr.Shutdown()
		srv.Close()
	}

	target := url
	if opts.DevServer != "" {
		// Vite serves the UI with HMR and proxies /ws back to this server.
		target = strings.TrimRight(opts.DevServer, "/")
	}

	if opts.Headless {
		waitForSignal()
		shutdown()
		return nil
	}

	err = runWindow(windowConfig{
		Title:   "ohmyjo",
		URL:     target,
		Width:   1280,
		Height:  800,
		DataDir: webviewDataDir(),
	})
	shutdown()
	return err
}

// Version returns the backend version string.
func Version() string { return server.Version }

func waitForSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
}

type embedAssets struct{ fsys fs.FS }

// Open serves a frontend path from the embedded bundle; "" means index.html.
func (e *embedAssets) Open(p string) ([]byte, string, error) {
	if e.fsys == nil {
		return nil, "", fs.ErrNotExist
	}
	name := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(p, "/")), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	body, err := fs.ReadFile(e.fsys, name)
	if err != nil {
		return nil, "", err
	}
	return body, contentType(name), nil
}

func contentType(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".ico":
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}

// ErrNoBundle is reported when the frontend was never built.
var ErrNoBundle = errors.New("web/dist is empty: run `npm install && npm run build` inside web/")

// ErrNoWebView is reported when the native webview could not be created.
var ErrNoWebView = errors.New("could not create the WebView2 window (is the WebView2 runtime installed?)")
