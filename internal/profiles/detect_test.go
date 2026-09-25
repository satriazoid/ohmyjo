package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetectAdvertisesUsableShells pins what Detect promises: the profiles it
// returns can all be started, they are distinguishable from each other, and the
// shell each one names is the shell its name claims.
func TestDetectAdvertisesUsableShells(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := Detect(home)

	if len(got) == 0 {
		t.Fatal("Detect found no shells on a Windows machine")
	}

	seenID := map[string]bool{}
	for _, p := range got {
		t.Logf("%-12s %-22s available=%-5v %s %v", p.ID, p.Name, p.Available, p.Shell, p.Args)

		if p.ID == "" {
			t.Errorf("profile %q has an empty id", p.Name)
			continue
		}
		if seenID[p.ID] {
			t.Errorf("profile id %q is returned twice, so Find and Resolve cannot address it", p.ID)
		}
		seenID[p.ID] = true

		if p.Name == "" {
			t.Errorf("profile %q has no display name", p.ID)
		}
		if p.Shell == "" {
			t.Errorf("profile %q names no shell", p.ID)
			continue
		}
		if !filepath.IsAbs(p.Shell) {
			t.Errorf("profile %q shell %q is not an absolute path, so its meaning depends on PATH at launch", p.ID, p.Shell)
		}
		if p.Cwd == "" {
			t.Errorf("profile %q has no working directory", p.ID)
		}
		// A profile marked available that does not exist would be offered in the
		// UI and then fail to start.
		if p.Available && !ExecutableExists(p.Shell) {
			t.Errorf("profile %q is marked available but %q does not exist", p.ID, p.Shell)
		}
	}

	// cmd.exe ships with Windows, so its absence means detection is broken
	// rather than that the machine is bare.
	if !seenID["cmd"] {
		t.Error("cmd.exe was not detected; it is present on every Windows install")
	}

	// A profile that presents itself as WSL must run WSL. A PATH lookup for a
	// bare "bash" finds Git's bash, which would advertise Git Bash a second time
	// under a WSL name and start the wrong shell.
	for _, p := range got {
		if !strings.Contains(strings.ToLower(p.ID), "wsl") {
			continue
		}
		if !strings.EqualFold(filepath.Base(p.Shell), "wsl.exe") {
			t.Errorf("profile %q claims WSL but runs %s, which is not wsl.exe", p.ID, p.Shell)
		}
	}
}
