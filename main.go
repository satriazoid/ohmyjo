// Command ohmyjo is a lightweight Windows terminal: a Go backend that owns the
// shells and a React frontend rendered inside WebView2.
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
		headless   = flag.Bool("headless", false, "serve the UI over HTTP without opening a window")
		port       = flag.Int("port", 0, "loopback port to listen on (0 = pick a free port)")
		devServer  = flag.String("dev", "", "load the UI from a Vite dev server URL instead of the embedded bundle")
		configPath = flag.String("config", "", "path to config.json (default: %APPDATA%\\ohmyjo\\config.json)")
		printPath  = flag.Bool("config-path", false, "print the resolved config path and exit")
		version    = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *version {
		fmt.Printf("ohmyjo %s\n", app.Version())
		return
	}
	if *printPath {
		fmt.Println(config.DefaultPath())
		return
	}

	log.SetFlags(log.Ltime)
	if err := app.Run(app.Options{
		Headless:   *headless,
		Port:       *port,
		DevServer:  *devServer,
		ConfigPath: *configPath,
	}); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}
