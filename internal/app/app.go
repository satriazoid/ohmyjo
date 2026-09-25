// Package app assembles the desktop application: configuration, shell
// sessions, and the native window that renders them.
//
// There is no browser engine anywhere in this package. The window is a plain
// Win32 window, the terminal grids and the chrome are drawn with GDI, and the
// shells are ConPTY processes owned directly by this process.
package app

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"ohmyjo/internal/config"
	"ohmyjo/internal/history"
	"ohmyjo/internal/profiles"
	"ohmyjo/internal/session"
	"ohmyjo/internal/ui"
)

// Options are the command line knobs.
type Options struct {
	// Headless starts the shells and the config watcher without a window. It
	// exists so a misbehaving shell can be diagnosed from a console, where the
	// log is visible.
	Headless bool
	// ConfigPath overrides the config file location.
	ConfigPath string
}

// Run starts the application and blocks until the window closes.
func Run(opts Options) error {
	// Must precede any window creation, so the window and its fonts are sized
	// for the display's real pixel density instead of being stretched blurry.
	ui.EnableDPIAwareness()
	// A windowed run must not drag a console window along with it.
	setupConsole(opts.Headless)

	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	loader, err := config.NewLoader(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	// The shell list is resolved once at startup: it fills in each profile's
	// absolute shell path and whether it is actually installed, neither of which
	// the raw config has.
	cfg := loader.Get()
	home, _ := os.UserHomeDir()
	cfg.Profiles = profiles.Resolve(cfg.Profiles, home)

	if stopWatch, err := loader.Watch(); err != nil {
		// Losing the watcher disables live reload; it must not stop the app.
		log.Printf("config watch unavailable: %v", err)
	} else {
		defer stopWatch()
	}

	hist := history.New()
	histFile := history.OpenFile(config.HistoryPath())
	// A shell loads its own history file at startup; new panes get the same
	// treatment so Up recalls commands from previous runs.
	mgr := session.NewManager(nil)
	mgr.Seed = func(id string) { hist.Seed(id, histFile.Load()) }
	defer mgr.Shutdown()

	if opts.Headless {
		log.Printf("ohmyjo %s headless (config %s)", Version, loader.Path())
		waitForSignal()
		return nil
	}

	win, err := ui.New(ui.WindowOptions{
		Title: "ohmyjo",
		// A logical request, clamped at creation to the work area: a window
		// larger than the usable desktop would put its own drag area and resize
		// edges off screen, and a borderless window has no native caption to
		// recover with.
		Width:  1280,
		Height: 800,
		Center: true,
	}, nil)
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	// The window is created without a host so its DPI and font slots exist
	// before the view reads them; the view is then installed as the host.
	view := NewView(win, mgr, loader, hist, histFile)
	win.SetHost(view)

	loader.OnChange(func(*config.Config, []string) { view.Reload() })
	view.NewTab()

	log.Printf("ohmyjo %s (config %s)", Version, loader.Path())
	if err := win.Run(); err != nil {
		return err
	}
	view.Close()
	return nil
}

// Version returns the backend version string.
const Version = "0.2.0"

func waitForSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
}
