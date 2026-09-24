// Package webui embeds the built React bundle so the shipped binary is a single
// self-contained executable with no external asset directory.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// FS returns the bundle rooted at dist/. It returns nil when the bundle is
// absent (dist/.gitkeep only), which lets the backend report a clear error
// instead of serving 404s for every asset.
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// Built reports whether a real bundle was embedded at compile time.
func Built() bool { return FS() != nil }
