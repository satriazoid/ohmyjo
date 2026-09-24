// Package profiles resolves the shell executables installed on the machine and
// merges them with user-declared custom profiles.
package profiles

import (
	"os"
	"path/filepath"
	"strings"

	"ohmyjo/internal/config"
)

// Resolve merges detected built-in profiles with the user's configured list.
//
// Detected profiles supply availability and default command lines; user entries
// win on conflict so a customised pwsh profile keeps its own args while still
// reporting whether pwsh exists on this machine.
func Resolve(user []config.Profile, home string) []config.Profile {
	detected := Detect(home)
	byID := map[string]config.Profile{}
	order := []string{}
	for _, p := range detected {
		byID[p.ID] = p
		order = append(order, p.ID)
	}
	out := make([]config.Profile, 0, len(detected)+len(user))
	used := map[string]bool{}
	for _, id := range order {
		base := byID[id]
		merged := base
		for _, u := range user {
			if u.ID != id {
				continue
			}
			merged = merge(base, u)
			used[u.ID] = true
			break
		}
		out = append(out, merged)
	}
	for _, u := range user {
		if used[u.ID] {
			continue
		}
		out = append(out, u)
	}
	return out
}

func merge(base, user config.Profile) config.Profile {
	out := base
	out.Name = pick(user.Name, base.Name)
	out.Shell = pick(user.Shell, base.Shell)
	if len(user.Args) > 0 {
		out.Args = user.Args
	}
	out.Cwd = pick(user.Cwd, base.Cwd)
	out.Icon = pick(user.Icon, base.Icon)
	out.Color = pick(user.Color, base.Color)
	out.Hidden = user.Hidden
	out.Env = map[string]string{}
	for k, v := range base.Env {
		out.Env[k] = v
	}
	for k, v := range user.Env {
		out.Env[k] = v
	}
	out.Builtin = base.Builtin
	// Availability follows the resolved executable so a user override pointing
	// at a real binary stays usable.
	out.Available = ExecutableExists(out.Shell)
	if out.Available {
		if abs, err := LookPath(out.Shell); err == nil {
			out.Shell = abs
		}
	}
	return out
}

func pick(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// Find returns the profile with the given id.
func Find(list []config.Profile, id string) (config.Profile, bool) {
	for _, p := range list {
		if p.ID == id {
			return p, true
		}
	}
	return config.Profile{}, false
}

// DefaultCwd returns dir when it exists, otherwise the user's home directory.
func DefaultCwd(dir, home string) string {
	if dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir
		}
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		home = "."
	}
	return home
}

func abs(p string) string {
	if p == "" {
		return ""
	}
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}
