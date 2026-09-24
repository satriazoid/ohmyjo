// Package system exposes the small OS helpers the UI needs: installed fonts,
// git status for a directory, directory listing and shell integration such as
// "reveal in Explorer".
package system

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Service answers system queries for the frontend.
type Service struct{}

func New() *Service { return &Service{} }

// Entry is one directory item.
type Entry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
}

// DirListing is a directory snapshot for the explorer panel.
type DirListing struct {
	Path    string  `json:"path"`
	Parent  string  `json:"parent,omitempty"`
	Entries []Entry `json:"entries"`
	Error   string  `json:"error,omitempty"`
}

// ListDir reads a directory. Hidden and system entries are included but sorted
// last among files, matching Explorer's ordering closely enough for a picker.
func (s *Service) ListDir(path string) DirListing {
	if path == "" {
		path, _ = os.UserHomeDir()
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	out := DirListing{Path: path, Entries: []Entry{}}
	parent := filepath.Dir(path)
	if parent != path {
		out.Parent = parent
	}
	items, err := os.ReadDir(path)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	for _, it := range items {
		e := Entry{Name: it.Name(), Dir: it.IsDir()}
		if info, err := it.Info(); err == nil && !e.Dir {
			e.Size = info.Size()
		}
		out.Entries = append(out.Entries, e)
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		if out.Entries[i].Dir != out.Entries[j].Dir {
			return out.Entries[i].Dir
		}
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	return out
}

// GitInfo describes the repository state of a directory.
type GitInfo struct {
	Repo   bool   `json:"repo"`
	Branch string `json:"branch,omitempty"`
	Dirty  int    `json:"dirty,omitempty"`
	Root   string `json:"root,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Git reports the branch, dirty count and repository root for dir. Output is
// parsed from porcelain formats so it works on any git version in PATH.
func (s *Service) Git(dir string) GitInfo {
	if dir == "" {
		return GitInfo{Error: "no directory"}
	}
	if _, err := os.Stat(dir); err != nil {
		return GitInfo{Error: "directory not found"}
	}
	root, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return GitInfo{Repo: false, Error: "not a git repository"}
	}
	info := GitInfo{Repo: true, Root: strings.TrimSpace(root)}
	if branch, err := gitOutput(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(branch)
		if info.Branch == "HEAD" {
			// Detached: show the short commit id instead of a useless "HEAD".
			if sha, err := gitOutput(dir, "rev-parse", "--short", "HEAD"); err == nil {
				info.Branch = "detached:" + strings.TrimSpace(sha)
			}
		}
	}
	if status, err := gitOutput(dir, "status", "--porcelain"); err == nil {
		for _, line := range strings.Split(strings.TrimRight(status, "\r\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				info.Dirty++
			}
		}
	}
	return info
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	HideConsole(cmd)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return buf.String(), err
		}
		return buf.String(), nil
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		return "", os.ErrDeadlineExceeded
	}
}

// FontFamilies returns installed font family names, sorted. The registry holds
// localised names with a "(TrueType)" suffix that we strip.
func (s *Service) FontFamilies() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, name := range installedFontNames() {
		name = strings.TrimSpace(name)
		for _, suffix := range []string{" (TrueType)", " (OpenType)", " (All res)", " (VGA res)", " (Plotter)"} {
			name = strings.TrimSuffix(name, suffix)
		}
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// Reveal opens the OS file browser at path, selecting file when given.
func (s *Service) Reveal(path string) error { return reveal(path) }

// Open launches a path with the OS shell (a file, a folder or a URL).
func (s *Service) Open(target string) error { return openTarget(target) }

// Home returns the user's home directory, used as the default starting folder.
func (s *Service) Home() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

// Exists reports whether a path is an existing directory.
func (s *Service) Exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// AtoiOr parses an int, falling back to def.
func AtoiOr(text string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return def
	}
	return n
}
