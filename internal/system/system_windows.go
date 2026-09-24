//go:build windows

package system

import (
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// installedFontNames reads the font registry keys. There is no cheap Win32 API
// that enumerates families, and the registry is what AddFonts actually updates.
func installedFontNames() []string {
	var out []string
	keys := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts`},
		{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts`},
	}
	for _, k := range keys {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, err := key.ReadValueNames(-1)
		if err == nil {
			out = append(out, names...)
		}
		key.Close()
	}
	// Segoe UI Variable and the Cascadia family are the interesting modern ones;
	// if the registry is unreadable, at least ship a usable baseline.
	if len(out) == 0 {
		return []string{"Cascadia Mono", "Cascadia Code", "Consolas", "Courier New", "Segoe UI"}
	}
	return out
}

// reveal opens Explorer with the given path selected.
func reveal(path string) error {
	if path == "" {
		path = "."
	}
	cmd := exec.Command("explorer.exe", "/select,"+strings.ReplaceAll(path, "/", `\`))
	HideConsole(cmd)
	return cmd.Start()
}

// openTarget hands a path or URL to the OS default handler.
func openTarget(target string) error {
	if target == "" {
		return nil
	}
	// rundll32's FileProtocolHandler resolves URLs, local files and folders
	// through the same association path Explorer uses.
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", target)
	HideConsole(cmd)
	return cmd.Start()
}
