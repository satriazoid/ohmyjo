// Package config owns the user settings file: defaults, load, validate, save
// and hot reload. A malformed file never crashes the app; it falls back to
// defaults and reports the problem through Diagnostics.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Current schema version written into the config file.
const SchemaVersion = 1

type Appearance struct {
	Theme           string  `json:"theme"`
	FontFamily      string  `json:"fontFamily"`
	FontSize        float64 `json:"fontSize"`
	LineHeight      float64 `json:"lineHeight"`
	LetterSpacing   float64 `json:"letterSpacing"`
	CursorStyle     string  `json:"cursorStyle"`
	CursorBlink     bool    `json:"cursorBlink"`
	Background      string  `json:"background"`
	BackgroundAlpha float64 `json:"backgroundOpacity"`
	Padding         int     `json:"padding"`
	BorderRadius    int     `json:"borderRadius"`
}

type Behavior struct {
	DefaultProfile  string `json:"defaultProfile"`
	DefaultCwd      string `json:"defaultCwd"`
	CopyOnSelect    bool   `json:"copyOnSelect"`
	RightClickPaste bool   `json:"rightClickPaste"`
	Scrollback      int    `json:"scrollback"`
}

type Sidebar struct {
	Visible  bool   `json:"visible"`
	Side     string `json:"side"`
	Width    int    `json:"width"`
	Mode     string `json:"mode"`
	AutoHide bool   `json:"autoHide"`
}

type Profile struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Shell     string            `json:"shell"`
	Args      []string          `json:"args,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Icon      string            `json:"icon,omitempty"`
	Color     string            `json:"color,omitempty"`
	Hidden    bool              `json:"hidden,omitempty"`
	Builtin   bool              `json:"builtin,omitempty"`
	Available bool              `json:"available"`
}

type Snippet struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
	Profile string `json:"profile,omitempty"`
}

// Theme is the terminal palette plus UI surface knobs. Field names match the
// xterm.js ITheme keys so the frontend can pass it through untouched.
type Theme struct {
	Name          string  `json:"name"`
	Background    string  `json:"background"`
	Foreground    string  `json:"foreground"`
	Cursor        string  `json:"cursor"`
	CursorAccent  string  `json:"cursorAccent,omitempty"`
	Selection     string  `json:"selection,omitempty"`
	Black         string  `json:"black"`
	Red           string  `json:"red"`
	Green         string  `json:"green"`
	Yellow        string  `json:"yellow"`
	Blue          string  `json:"blue"`
	Magenta       string  `json:"magenta"`
	Cyan          string  `json:"cyan"`
	White         string  `json:"white"`
	BrightBlack   string  `json:"brightBlack"`
	BrightRed     string  `json:"brightRed"`
	BrightGreen   string  `json:"brightGreen"`
	BrightYellow  string  `json:"brightYellow"`
	BrightBlue    string  `json:"brightBlue"`
	BrightMagenta string  `json:"brightMagenta"`
	BrightCyan    string  `json:"brightCyan"`
	BrightWhite   string  `json:"brightWhite"`
	Opacity       float64 `json:"opacity,omitempty"`
	Blur          bool    `json:"blur,omitempty"`
	UIBackground  string  `json:"uiBackground,omitempty"`
	UIBackground2 string  `json:"uiBackgroundAlt,omitempty"`
	UIBorder      string  `json:"uiBorder,omitempty"`
	UIForeground  string  `json:"uiForeground,omitempty"`
	UIForeground2 string  `json:"uiForegroundDim,omitempty"`
	UIAccent      string  `json:"uiAccent,omitempty"`
	UITabActive   string  `json:"uiTabActive,omitempty"`
	UITabInactive string  `json:"uiTabInactive,omitempty"`
}

type Config struct {
	Version     int               `json:"version"`
	Appearance  Appearance        `json:"appearance"`
	Behavior    Behavior          `json:"behavior"`
	Keybindings map[string]string `json:"keybindings"`
	Sidebar     Sidebar           `json:"sidebar"`
	Profiles    []Profile         `json:"profiles"`
	Themes      map[string]Theme  `json:"themes"`
	Snippets    []Snippet         `json:"snippets"`

	// Diagnostics reports what had to be repaired while loading (unknown theme,
	// dropped profile, malformed JSON...). It is never written back to the file,
	// but it does travel to the frontend so a repaired config is visible
	// instead of silently ignored.
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// Loader keeps the live config and the watcher state.
type Loader struct {
	path    string
	mu      sync.RWMutex
	cfg     *Config
	onEvent []func(*Config, []string)
}

func DefaultPath() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			home = "."
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "ohmyjo", "config.json")
}

// HistoryPath is the shared command history, next to config.json so both live
// and die together.
func HistoryPath() string {
	return filepath.Join(filepath.Dir(DefaultPath()), "history.txt")
}

func Default() *Config {
	home, _ := os.UserHomeDir()
	cfg := &Config{
		Version: SchemaVersion,
		Appearance: Appearance{
			Theme:           "tokyo-night",
			FontFamily:      "'Cascadia Mono', 'JetBrains Mono', Consolas, monospace",
			FontSize:        14,
			LineHeight:      1.2,
			LetterSpacing:   0,
			CursorStyle:     "block",
			CursorBlink:     true,
			Background:      "",
			BackgroundAlpha: 0.25,
			Padding:         8,
			BorderRadius:    8,
		},
		Behavior: Behavior{
			DefaultProfile:  "powershell",
			DefaultCwd:      home,
			CopyOnSelect:    false,
			RightClickPaste: true,
			Scrollback:      10000,
		},
		Keybindings: map[string]string{
			"newTab":         "ctrl+shift+t",
			"closeTab":       "ctrl+shift+w",
			"detachTab":      "ctrl+shift+alt+w",
			"maximizePane":   "ctrl+shift+m",
			"nextTab":        "ctrl+tab",
			"prevTab":        "ctrl+shift+tab",
			"splitRight":     "alt+shift+d",
			"splitDown":      "alt+shift+e",
			"closePane":      "ctrl+shift+q",
			"toggleSidebar":  "ctrl+shift+b",
			"settings":       "ctrl+comma",
			"commandPalette": "ctrl+shift+p",
			"searchTerminal": "ctrl+shift+f",
			// Not plain ctrl+h: that is Backspace on a terminal, and stealing it
			// would break editing at the prompt.
			"history":       "ctrl+shift+h",
			"fontIncrease":  "ctrl+equal",
			"fontDecrease":  "ctrl+minus",
			"fontReset":     "ctrl+digit0",
			"focusNextPane": "alt+arrowright",
			"focusPrevPane": "alt+arrowleft",
			"clearTerminal": "ctrl+shift+k",
			"restartPane":   "ctrl+shift+r",
			"duplicatePane": "alt+shift+enter",
		},
		Sidebar: Sidebar{
			Visible:  false,
			Side:     "left",
			Width:    260,
			Mode:     "sessions",
			AutoHide: false,
		},
		Themes:   BuiltinThemes(),
		Snippets: []Snippet{},
	}
	return cfg
}

// NewLoader creates a loader for path, loading (or creating) the file.
func NewLoader(path string) (*Loader, error) {
	if path == "" {
		path = DefaultPath()
	}
	l := &Loader{path: path}
	cfg, diags := l.read()
	if cfg == nil {
		cfg = Default()
		diags = append(diags, "config missing or invalid; defaults applied")
	}
	cfg.Diagnostics = diags
	l.cfg = cfg
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := l.Write(cfg); err != nil {
			diags = append(diags, "could not create config file: "+err.Error())
			cfg.Diagnostics = diags
		}
	}
	return l, nil
}

func (l *Loader) Path() string { return l.path }

func (l *Loader) read() (*Config, []string) {
	raw, err := os.ReadFile(l.path)
	if err != nil {
		return nil, nil
	}
	var diags []string
	cfg := Default()
	// Decode over defaults so a partial file still yields a complete config.
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if err := dec.Decode(cfg); err != nil {
		return nil, []string{fmt.Sprintf("config parse error: %v", err)}
	}
	diags = append(diags, Validate(cfg)...)
	return cfg, diags
}

// Validate repairs out-of-range values in place and reports what it changed.
func Validate(c *Config) []string {
	var diags []string
	if c.Version == 0 {
		c.Version = SchemaVersion
	}
	if c.Appearance.FontSize < 6 || c.Appearance.FontSize > 48 {
		diags = append(diags, "appearance.fontSize out of range 6..48; reset to 14")
		c.Appearance.FontSize = 14
	}
	if c.Appearance.LineHeight < 0.8 || c.Appearance.LineHeight > 3 {
		c.Appearance.LineHeight = 1.2
	}
	if c.Appearance.Padding < 0 || c.Appearance.Padding > 64 {
		c.Appearance.Padding = 8
	}
	if c.Appearance.BorderRadius < 0 || c.Appearance.BorderRadius > 32 {
		c.Appearance.BorderRadius = 8
	}
	switch c.Appearance.CursorStyle {
	case "block", "underline", "bar":
	default:
		c.Appearance.CursorStyle = "block"
	}
	if c.Appearance.BackgroundAlpha < 0 || c.Appearance.BackgroundAlpha > 1 {
		c.Appearance.BackgroundAlpha = 0.25
	}
	if c.Behavior.Scrollback < 100 || c.Behavior.Scrollback > 500000 {
		c.Behavior.Scrollback = 10000
	}
	if c.Sidebar.Side != "left" && c.Sidebar.Side != "right" {
		c.Sidebar.Side = "left"
	}
	switch c.Sidebar.Mode {
	case "sessions", "profiles", "snippets", "explorer", "settings":
	default:
		c.Sidebar.Mode = "sessions"
	}
	if c.Sidebar.Width < 160 || c.Sidebar.Width > 720 {
		c.Sidebar.Width = 260
	}
	if c.Themes == nil {
		c.Themes = BuiltinThemes()
	} else {
		for name, theme := range BuiltinThemes() {
			if _, ok := c.Themes[name]; !ok {
				c.Themes[name] = theme
			}
		}
	}
	if _, ok := c.Themes[c.Appearance.Theme]; !ok {
		diags = append(diags, fmt.Sprintf("appearance.theme %q unknown; using tokyo-night", c.Appearance.Theme))
		c.Appearance.Theme = "tokyo-night"
	}
	if c.Keybindings == nil {
		c.Keybindings = Default().Keybindings
	}
	if c.Snippets == nil {
		c.Snippets = []Snippet{}
	}
	// Profiles: keep user overrides. A profile may deliberately omit the shell
	// to only customise args/env of a detected built-in, so an empty shell is
	// accepted as long as another profile (or detection) will supply it.
	seen := map[string]bool{}
	merged := make([]Profile, 0, len(c.Profiles))
	for _, p := range c.Profiles {
		if p.ID == "" || seen[p.ID] {
			diags = append(diags, "dropped profile with empty or duplicate id")
			continue
		}
		if p.Shell == "" && !p.Builtin {
			diags = append(diags, fmt.Sprintf("profile %q has no shell; it only overrides the detected profile", p.ID))
		}
		if p.Name == "" {
			p.Name = p.ID
		}
		if p.Cwd != "" {
			p.Cwd = expandHome(p.Cwd)
		}
		seen[p.ID] = true
		merged = append(merged, p)
	}
	c.Profiles = merged
	return diags
}

// expandHome resolves a leading ~ so config files stay portable between users.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~\\") && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

func (l *Loader) Get() *Config {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cfg
}

// Replace merges detected profiles into the live config and notifies watchers.
func (l *Loader) Replace(cfg *Config) {
	l.mu.Lock()
	l.cfg = cfg
	l.mu.Unlock()
	l.emit()
}

// Mutate applies fn to the live config under lock, then notifies watchers.
func (l *Loader) Mutate(fn func(*Config) error) error {
	l.mu.Lock()
	if err := fn(l.cfg); err != nil {
		l.mu.Unlock()
		return err
	}
	l.cfg.Diagnostics = Validate(l.cfg)
	l.mu.Unlock()
	l.emit()
	return nil
}

// Write persists cfg to disk (atomic replace) and updates the live copy.
func (l *Loader) Write(cfg *Config) error {
	l.mu.Lock()
	l.cfg = cfg
	l.mu.Unlock()
	return l.Save()
}

func (l *Loader) Save() error {
	l.mu.RLock()
	cfg := l.cfg
	l.mu.RUnlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	// Diagnostics describe the state of the file that was loaded; carrying them
	// back into the file would make every load report the previous load's
	// repairs.
	stored := *cfg
	stored.Diagnostics = nil
	body, err := json.MarshalIndent(&stored, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}

// Reload re-reads the file from disk; used by the file watcher.
func (l *Loader) Reload() []string {
	cfg, diags := l.read()
	if cfg == nil {
		// A broken file must not wipe the working configuration, but the user
		// still has to learn why the edit did nothing: attach the parse error
		// to the config that stays live so it reaches the UI as a warning.
		if len(diags) > 0 {
			l.mu.Lock()
			l.cfg.Diagnostics = diags
			l.mu.Unlock()
			l.emit()
		}
		return diags
	}
	l.mu.Lock()
	// Keep detected profiles' availability fresh but honor user edits.
	l.cfg = cfg
	l.mu.Unlock()
	l.emit()
	return diags
}

// OnChange registers a listener fired after any config mutation or reload.
func (l *Loader) OnChange(fn func(*Config, []string)) {
	l.mu.Lock()
	l.onEvent = append(l.onEvent, fn)
	l.mu.Unlock()
}

func (l *Loader) emit() {
	l.mu.RLock()
	cfg := l.cfg
	listeners := append([]func(*Config, []string){}, l.onEvent...)
	l.mu.RUnlock()
	for _, fn := range listeners {
		fn(cfg, cfg.Diagnostics)
	}
}
