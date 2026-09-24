//go:build windows

package profiles

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"ohmyjo/internal/config"
	"ohmyjo/internal/system"
)

// Detect finds the shells installed on this Windows machine. Nothing here is
// hardcoded to a single install layout: each candidate is probed in order and
// only returned when the executable actually exists.
func Detect(home string) []config.Profile {
	out := []config.Profile{}

	// cmd.exe is always present on Windows; %ComSpec% is authoritative.
	cmd := os.Getenv("ComSpec")
	if cmd == "" {
		cmd = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	out = append(out, config.Profile{
		ID: "cmd", Name: "Command Prompt", Shell: cmd,
		Icon: "cmd", Color: "#c19c00", Builtin: true,
		Available: ExecutableExists(cmd),
	})

	// Windows PowerShell 5.1 ships in system32 and is never in PATH.
	ps51 := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	out = append(out, config.Profile{
		ID: "powershell", Name: "Windows PowerShell", Shell: ps51,
		Args: []string{"-NoLogo"},
		Icon: "powershell", Color: "#3a96dd", Builtin: true,
		Available: ExecutableExists(ps51),
	})

	// PowerShell 7+ lives under Program Files\PowerShell\<version>\pwsh.exe.
	if pwsh := findPwsh(); pwsh != "" {
		out = append(out, config.Profile{
			ID: "pwsh", Name: "PowerShell 7", Shell: pwsh,
			Args: []string{"-NoLogo"}, Icon: "pwsh", Color: "#7aa2f7",
			Builtin: true, Available: true,
		})
	}

	if bash := findGitBash(); bash != "" {
		out = append(out, config.Profile{
			ID: "git-bash", Name: "Git Bash", Shell: bash,
			Args: []string{"--login", "-i"},
			Icon: "git", Color: "#f7768e", Builtin: true, Available: true,
		})
	}

	// wsl.exe exists on Windows 10 1607+ even without a distro installed; we
	// only advertise it when at least one distribution is registered.
	wsl := filepath.Join(os.Getenv("SystemRoot"), "System32", "wsl.exe")
	if ExecutableExists(wsl) && wslHasDistro(wsl) {
		out = append(out, config.Profile{
			ID: "wsl", Name: "WSL", Shell: wsl,
			Icon: "wsl", Color: "#e0af68", Builtin: true, Available: true,
		})
	}

	for _, c := range []struct{ id, name, bin, colour string }{
		{"nushell", "Nushell", "nu", "#13a10e"},
		{"bash-wsl", "Bash", "bash", "#9ece6a"},
	} {
		if p, err := LookPath(c.bin); err == nil {
			out = append(out, config.Profile{
				ID: c.id, Name: c.name, Shell: p,
				Icon: "generic", Color: c.colour, Builtin: true, Available: true,
			})
		}
	}

	for i := range out {
		out[i].Available = ExecutableExists(out[i].Shell)
		if out[i].Cwd == "" {
			out[i].Cwd = home
		}
	}
	return out
}

func findPwsh() string {
	if p, err := LookPath("pwsh"); err == nil {
		return p
	}
	roots := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "PowerShell"),
		filepath.Join(os.Getenv("ProgramW6432"), "PowerShell"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "PowerShell"),
	}
	type cand struct {
		path string
		ver  string
	}
	var cands []cand
	for _, root := range roots {
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			exe := filepath.Join(root, e.Name(), "pwsh.exe")
			if ExecutableExists(exe) {
				cands = append(cands, cand{exe, e.Name()})
			}
		}
	}
	if len(cands) == 0 {
		return ""
	}
	// Highest version directory wins (lexical order works for "7.x" dirs, and
	// the preview/stable naming keeps 7.5 above 7.4).
	sort.Slice(cands, func(i, j int) bool { return cands[i].ver > cands[j].ver })
	return cands[0].path
}

func findGitBash() string {
	roots := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Git"),
		filepath.Join(os.Getenv("ProgramW6432"), "Git"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Git"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Git"),
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		for _, rel := range []string{
			filepath.Join("bin", "bash.exe"),
			filepath.Join("usr", "bin", "bash.exe"),
		} {
			exe := filepath.Join(root, rel)
			if ExecutableExists(exe) {
				return exe
			}
		}
	}
	if git, err := LookPath("git"); err == nil {
		// <Git>\cmd\git.exe or <Git>\mingw64\bin\git.exe -> <Git>\bin\bash.exe
		dir := filepath.Dir(git)
		for _, up := range []string{"..", filepath.Join("..", "..")} {
			exe := filepath.Join(dir, up, "bin", "bash.exe")
			if ExecutableExists(exe) {
				return abs(exe)
			}
		}
	}
	return ""
}

func wslHasDistro(wsl string) bool {
	// `wsl.exe -l -q` prints registered distribution names; a machine without
	// any prints a localised "no distributions" notice and exits non-zero.
	cmd := exec.Command(wsl, "-l", "-q")
	system.HideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	text := strings.TrimSpace(strings.ReplaceAll(string(out), "\x00", ""))
	if text == "" {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}

// LookPath resolves an executable the way exec.LookPath does but also accepts
// an absolute path that already exists.
func LookPath(bin string) (string, error) {
	if strings.ContainsAny(bin, `\/`) {
		if ExecutableExists(bin) {
			return abs(bin), nil
		}
		return "", os.ErrNotExist
	}
	return exec.LookPath(bin)
}

// ExecutableExists reports whether path points at an existing regular file.
func ExecutableExists(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !st.IsDir()
}
