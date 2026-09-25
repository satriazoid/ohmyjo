// Command ohmyjo is a lightweight Windows terminal: a native Win32 window that
// renders ConPTY shells directly with GDI, with no browser engine.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"ohmyjo/internal/app"
	"ohmyjo/internal/config"
)

func main() {
	var (
		headless   = flag.Bool("headless", false, "run without a window, for diagnosing shells")
		configPath = flag.String("config", "", "path to config.json (default: %APPDATA%\\ohmyjo\\config.json)")
		printPath  = flag.Bool("config-path", false, "print the resolved config path and exit")
		version    = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *version {
		fmt.Printf("ohmyjo %s\n", app.Version)
		return
	}
	if *printPath {
		fmt.Println(config.DefaultPath())
		return
	}

	log.SetFlags(log.Ltime)
	if err := app.Run(app.Options{
		Headless:   *headless,
		ConfigPath: *configPath,
	}); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}
