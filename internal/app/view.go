//go:build windows

package app

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"ohmyjo/internal/config"
	"ohmyjo/internal/history"
	"ohmyjo/internal/profiles"
	"ohmyjo/internal/session"
	"ohmyjo/internal/ui"
	"ohmyjo/internal/vt"
)

// Chrome metrics, in logical (96 dpi) pixels. Every use goes through px(), so
// the window keeps its proportions at 125% and above.
const (
	tabStripHeight = 34
	tabMaxWidth    = 220
	tabMinWidth    = 112
	tabGap         = 2
	tabCloseWidth  = 22
	tabLeftPad     = 6
	newTabWidth    = 30
	// splitButtonWidth is the width of each of the two split buttons, which
	// follow the "+" button along the strip.
	splitButtonWidth = 30
	// splitIconW and splitIconH size the two-pane diagram drawn on a split
	// button.
	splitIconW    = 15
	splitIconH    = 13
	splitterWidth = 5
	// scrollbarGutter is the strip reserved to the right of every pane for its
	// scrollbar. Reserving it always keeps the last text column from sliding
	// under the bar when the pane is scrolled.
	scrollbarGutter = 11
	// scrollbarWidth is the drawn width of that bar.
	scrollbarWidth = 7
	// controlWidth is the width of each of the window's own minimise, maximise
	// and close buttons. The window is frameless, so they are drawn and
	// hit-tested here.
	controlWidth  = 46
	controlHeight = 34
	// focusBorderWidth is the outline drawn around the panes of a split tab, so
	// the focused one is identifiable.
	focusBorderWidth = 2
)

// blinkTimerID identifies the cursor-blink timer.
const blinkTimerID = 1

// blinkInterval is the cursor blink period in milliseconds. It matches the
// 530 ms Windows uses for its own caret, so the terminal cursor blinks in step
// with the rest of the system.
const blinkInterval = 530

// minPaneRatio keeps a splitter from being dragged so far that a pane vanishes,
// which would leave the user with no way to drag it back.
const minPaneRatio = 0.08

// Axis is the direction a split divides its area.
type Axis int

const (
	// SplitAlongX places the children side by side, splitting a vertical line.
	SplitAlongX Axis = iota
	// SplitAlongY stacks the children, splitting a horizontal line.
	SplitAlongY
)

// View is the application's window content: the tab strip, the pane layout tree
// inside the active tab, and input routing.
//
// It implements ui.Host, so the window calls into it for painting, layout and
// unhandled messages. Nothing here involves a browser engine: the whole surface
// is drawn with GDI into the window's one bitmap.
type View struct {
	// mu guards the layout tree, the tabs and the focus. It is held while
	// painting and while laying out, so a session reader's repaint request can
	// never observe a half-built tree. Take it through lock()/unlock(), never
	// directly: those tolerate the nested, same-thread acquisition Windows
	// forces on a few messages.
	mu sync.Mutex

	win  *ui.Window
	mgr  *session.Manager
	load *config.Loader

	styles *ui.StyleResolver
	pal    ui.Palette

	cfg *config.Config

	// hist records commands as the user types them, so a new pane is seeded
	// with the recall it would have had before.
	hist     *history.Store
	histFile *history.File

	tabs []*Tab
	// active indexes tabs.
	active int

	// chords maps the configured keybindings onto action names.
	chords map[chordKey]string

	// fonts are the logical slots the window scales to the display DPI.
	fonts map[ui.FontID]ui.FontSlot

	// blinkOn is the cursor blink phase.
	blinkOn bool

	dpi float64
	// metrics is the mono geometry at the current DPI, used to size new grids.
	metrics ui.Metrics

	// splash is the message shown when no tab is open.
	splash string

	// closing is set while the window is going away, so a pane that exits
	// during shutdown does not schedule layout on a dying window.
	closing bool

	// drag is the splitter drag in progress, if any.
	drag *splitDrag

	// panel is the slide-out kill panel. It floats over the panes, so it takes
	// no part in layoutLocked: nothing behind it is resized when it opens.
	panel panelState

	// pendingKey is set when the key press just handled by onKeyDown was encoded
	// and sent to a pane; it is consumed by the WM_CHAR that Windows derived from
	// the same press.
	//
	// Windows turns one key press into two messages: WM_KEYDOWN, which carries no
	// character, and then the WM_CHAR that TranslateMessage derived from it. The
	// encoder answers the first for the keys that have no usable WM_CHAR of their
	// own — Enter, Tab, Backspace, Escape — so forwarding the second sends the
	// input twice: Enter runs the command and then opens an extra prompt,
	// Backspace deletes two characters, and Escape reaches the shell as two
	// escapes. The flag is owned by the message thread, the only thread that
	// translates keys, so it needs no lock of its own.
	pendingKey bool

	// lockDepth counts the nested lock() calls made by the message thread. See
	// lock().
	lockDepth int
}

// lock takes the view lock, and is safe to call again from the message thread.
//
// The lock is not a plain mutex because Windows delivers several messages
// synchronously, on the message thread, from inside another message. The worst
// offender is ShowWindow(SW_MAXIMIZE): it sends WM_SIZE before it returns, and
// the host answers WM_SIZE by calling Resize, which needs the lock the caller
// is still holding. A sync.Mutex there deadlocks the message thread and the
// window stops responding to everything, including the system's own close.
//
// Only the message thread ever locks — background goroutines hand work over
// through Window.Post — so a same-thread re-entry is always the nested case,
// never contention. It is served without touching the mutex and balanced by
// the matching unlock.
func (v *View) lock() {
	if v.lockDepth > 0 {
		v.lockDepth++
		return
	}
	v.mu.Lock()
	v.lockDepth = 1
}

// unlock releases one level of lock.
func (v *View) unlock() {
	if v.lockDepth > 0 {
		v.lockDepth--
		if v.lockDepth > 0 {
			return
		}
	}
	v.mu.Unlock()
}

// splitDrag tracks a splitter being dragged. The container's origin and extent
// are captured at the press, so a relayout mid-drag cannot make the ratio jump
// under the pointer.
type splitDrag struct {
	n      *node
	axis   Axis
	extent int
}

// Tab is one tab: a layout tree of panes plus its own title.
type Tab struct {
	root *node
	// title overrides the derived name when the user renamed the tab.
	title string
	// focus is the pane keys are sent to.
	focus *Pane
	// maximized is the pane temporarily covering the tab, if any.
	maximized *Pane
}

// node is one element of a tab's layout tree: either a leaf holding a pane, or
// an internal split.
type node struct {
	// pane is set on a leaf.
	pane *Pane
	// axis and ratio describe an internal split. ratio is the fraction of the
	// area given to a, clamped so neither child can vanish.
	axis  Axis
	ratio float64
	a, b  *node
	// rect is the area this node was last laid out in, in physical pixels.
	rect ui.Rect
}

// NewView builds the view around a window.
func NewView(win *ui.Window, mgr *session.Manager, load *config.Loader, hist *history.Store, histFile *history.File) *View {
	cfg := load.Get()
	v := &View{
		win:      win,
		mgr:      mgr,
		load:     load,
		hist:     hist,
		histFile: histFile,
		cfg:      cfg,
		dpi:      win.DPI(),
		blinkOn:  true,
		splash:   "No terminal open \u00b7 Ctrl+Shift+T opens one",
	}
	v.pal = paletteFor(cfg)
	v.styles = ui.NewStyleResolver(v.pal)
	v.fonts = fontsFor(cfg, v.dpi)
	win.SetClear(v.pal.UIBackground)
	// No host is installed yet, so this call cannot re-enter Resize.
	win.SetFonts(v.fonts)
	v.metrics = win.Metrics(ui.FontMono)
	v.reloadChords()
	// The panel's configured visibility is a startup state only; toggling it at
	// runtime never writes the config back, so a shortcut press cannot rewrite
	// the user's file.
	if cfg.Sidebar.Visible {
		v.panel.open = true
		v.panel.slide = 1
	}
	win.SetTimer(blinkTimerID, blinkInterval)
	return v
}

// Reload reapplies the configuration after the file changed: theme, fonts,
// scrollback and bindings.
func (v *View) Reload() {
	v.win.Post(func() {
		v.lock()
		defer v.unlock()
		cfg := v.load.Get()
		v.cfg = cfg
		v.pal = paletteFor(cfg)
		v.styles.SetPalette(v.pal)
		v.fonts = fontsFor(cfg, v.win.DPI())
		v.reloadChords()
		for _, p := range v.allPanesLocked() {
			p.SetScrollback(cfg.Behavior.Scrollback)
			p.grid.Invalidate()
		}
		v.win.SetClear(v.pal.UIBackground)
		// SetFonts rebuilds the slots and then calls Resize back to lay out
		// with the new metrics. lock() lets that nested call through on this
		// thread.
		v.win.SetFonts(v.fonts)
		v.metrics = v.win.Metrics(ui.FontMono)
		v.layoutLocked()
		v.win.Invalidate()
	})
}

// paletteFor resolves the config's theme into renderer colours. An unknown
// theme name falls back to the built-in palette rather than to a half-applied
// one.
func paletteFor(cfg *config.Config) ui.Palette {
	theme, ok := cfg.Themes[cfg.Appearance.Theme]
	if !ok {
		return ui.DefaultPalette()
	}
	return ui.PaletteFromTheme(themeSource(theme))
}

// themeSource adapts a config theme to the shape the renderer consumes. The two
// name the same colours differently: the config keeps the frontend's JSON keys,
// the renderer uses renderer-shaped names.
func themeSource(t config.Theme) ui.ThemeSource {
	return ui.ThemeSource{
		Background:      t.Background,
		Foreground:      t.Foreground,
		Cursor:          t.Cursor,
		CursorAccent:    t.CursorAccent,
		Selection:       t.Selection,
		Black:           t.Black,
		Red:             t.Red,
		Green:           t.Green,
		Yellow:          t.Yellow,
		Blue:            t.Blue,
		Magenta:         t.Magenta,
		Cyan:            t.Cyan,
		White:           t.White,
		BrightBlack:     t.BrightBlack,
		BrightRed:       t.BrightRed,
		BrightGreen:     t.BrightGreen,
		BrightYellow:    t.BrightYellow,
		BrightBlue:      t.BrightBlue,
		BrightMagenta:   t.BrightMagenta,
		BrightCyan:      t.BrightCyan,
		BrightWhite:     t.BrightWhite,
		UIBackground:    t.UIBackground,
		UIBackgroundAlt: t.UIBackground2,
		UIBorder:        t.UIBorder,
		UIForeground:    t.UIForeground,
		UIForegroundDim: t.UIForeground2,
		UIAccent:        t.UIAccent,
		TabActive:       t.UITabActive,
		TabInactive:     t.UITabInactive,
		PaneBorder:      t.UIPaneBorder,
		Splitter:        t.UISplitter,
	}
}

// fontsFor builds the logical font slots from the appearance config. The slots
// stay logical (96 dpi); the window scales them to the display.
func fontsFor(cfg *config.Config, dpi float64) map[ui.FontID]ui.FontSlot {
	a := cfg.Appearance
	size := a.FontSize
	if size <= 0 {
		size = 14
	}
	lineH := a.LineHeight
	if lineH <= 0 {
		lineH = 1.2
	}
	mono := a.FontFamily
	if strings.TrimSpace(mono) == "" {
		mono = "Consolas"
	}
	uiSize := size - 1
	if uiSize < 9 {
		uiSize = 9
	}
	return map[ui.FontID]ui.FontSlot{
		ui.FontMono:   {Family: mono, SizePx: size, LineH: lineH, DPI: dpi},
		ui.FontUI:     {Family: "Segoe UI", SizePx: uiSize, DPI: dpi},
		ui.FontUIBold: {Family: "Segoe UI", SizePx: uiSize, Bold: true, DPI: dpi},
	}
}

// px converts a logical measurement to physical pixels at the window's scale.
// Zero stays zero, so an unset metric does not become one pixel.
func (v *View) px(logical int) int {
	if logical == 0 {
		return 0
	}
	return int(math.Round(float64(logical) * v.dpi / 96))
}

// allPanesLocked lists every pane in every tab. Called with v.mu held.
func (v *View) allPanesLocked() []*Pane {
	var out []*Pane
	for _, t := range v.tabs {
		out = append(out, paneOrder(t.root)...)
	}
	return out
}

// ---------------------------------------------------------------------------
// Keybindings

// chordKey is a key combination in the shape the config's specs describe: the
// key plus exactly the modifiers held, so a chord without Shift does not match a
// press that had it.
type chordKey struct {
	key              string
	ctrl, alt, shift bool
}

// reloadChords parses the configured bindings. Called with v.mu held and before
// any input can arrive.
func (v *View) reloadChords() {
	v.chords = make(map[chordKey]string, len(v.cfg.Keybindings)+4)

	// Deterministic order: two actions bound to one chord must resolve the same
	// way on every run, and map iteration order would not.
	actions := make([]string, 0, len(v.cfg.Keybindings))
	for action := range v.cfg.Keybindings {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		c, ok := parseChord(v.cfg.Keybindings[action])
		if !ok {
			continue
		}
		if _, taken := v.chords[c]; taken {
			continue
		}
		v.chords[c] = action
	}

	// Copy, paste and select-all are not in the shipped keybinding table, but a
	// terminal without them is unusable. A user binding on the same chord was
	// laid down first and still wins.
	for _, extra := range []struct {
		action string
		chord  chordKey
	}{
		{"copy", chordKey{key: "c", ctrl: true, shift: true}},
		{"paste", chordKey{key: "v", ctrl: true, shift: true}},
		{"selectAll", chordKey{key: "a", ctrl: true, shift: true}},
	} {
		if _, taken := v.chords[extra.chord]; taken {
			continue
		}
		v.chords[extra.chord] = extra.action
	}
}

// parseChord parses a specification such as "ctrl+shift+t". It reports false for
// a specification with no key, which would otherwise match every press of its
// modifiers.
func parseChord(spec string) (chordKey, bool) {
	var c chordKey
	for _, raw := range strings.Split(strings.ToLower(spec), "+") {
		part := strings.TrimSpace(raw)
		switch part {
		case "":
		case "ctrl", "control":
			c.ctrl = true
		case "alt", "option":
			c.alt = true
		case "shift":
			c.shift = true
		case "cmd", "meta", "super", "win":
			// The Windows key is owned by the shell and never reaches the
			// window, so a binding on it could never fire. Dropping the
			// modifier is better than matching the bare key.
		default:
			c.key = chordKeyName(part)
		}
	}
	return c, c.key != ""
}

// chordKeyName normalises a specification's key token. The shipped specs use
// readable names for keys that have no character ("digit0", "comma"), so both
// those and the literal character are accepted.
func chordKeyName(tok string) string {
	switch tok {
	case "comma":
		return ","
	case "equal":
		return "="
	case "minus":
		return "-"
	case "space":
		return " "
	case "esc":
		return "escape"
	case "plus":
		return "+"
	}
	if len(tok) > 5 && strings.HasPrefix(tok, "digit") {
		return tok[5:]
	}
	return tok
}

// eventKeyName reports the name a key press matches a binding by.
func eventKeyName(k KeyEvent) string {
	switch k.VK {
	case vkReturn:
		return "enter"
	case vkTab:
		return "tab"
	case vkEscape:
		return "escape"
	case vkBack:
		return "backspace"
	case vkDelete:
		return "delete"
	case vkInsert:
		return "insert"
	case vkHome:
		return "home"
	case vkEnd:
		return "end"
	case vkPrior:
		return "pageup"
	case vkNext:
		return "pagedown"
	case vkLeft:
		return "arrowleft"
	case vkRight:
		return "arrowright"
	case vkUp:
		return "arrowup"
	case vkDown:
		return "arrowdown"
	case vkSpace:
		return " "
	case vkComma:
		return ","
	case vkOEMPlus:
		return "="
	case vkOEMMinus:
		return "-"
	}
	switch {
	case k.VK >= vkF1 && k.VK <= vkF12:
		return "f" + itoa(int(k.VK-vkF1)+1)
	case k.VK >= vkDigit0 && k.VK <= vkDigit0+9:
		return string(rune('0' + (k.VK - vkDigit0)))
	case k.VK >= '0' && k.VK <= '9':
		return string(rune(k.VK))
	case k.VK >= 'A' && k.VK <= 'Z':
		// Key presses are named in lowercase so "ctrl+shift+t" matches whether
		// or not Shift produced an uppercase character.
		return string(rune(k.VK + ('a' - 'A')))
	}
	return ""
}

// lookupChord returns the action bound to a key press, if any.
func (v *View) lookupChord(k KeyEvent, mods ui.Modifiers) (string, bool) {
	name := eventKeyName(k)
	if name == "" {
		return "", false
	}
	action, ok := v.chords[chordKey{key: name, ctrl: mods.Ctrl, alt: mods.Alt, shift: mods.Shift}]
	return action, ok
}

// ---------------------------------------------------------------------------
// Tabs and panes

// NewTab creates a tab with one pane running the default profile and makes it
// active. It reports whether a tab could be created.
func (v *View) NewTab() bool {
	v.lock()
	defer v.unlock()
	return v.newTabLocked()
}

// newTabLocked creates a tab and focuses it. Called with v.mu held.
func (v *View) newTabLocked() bool {
	p := v.newPaneLocked("", "")
	if p == nil {
		return false
	}
	t := &Tab{root: &node{pane: p}, focus: p}
	v.tabs = append(v.tabs, t)
	v.active = len(v.tabs) - 1
	v.blinkOn = true
	v.layoutLocked()
	v.win.Invalidate()
	return true
}

// newPaneLocked creates a pane and starts its shell.
//
// profID selects the profile; empty means the configured default. cwd overrides
// the working directory, which is how a duplicate inherits its sibling's. It
// returns nil when there is no shell to run, because a pane with no process
// looks idle while being dead.
func (v *View) newPaneLocked(profID, cwd string) *Pane {
	prof, ok := v.resolveProfile(profID)
	if !ok {
		return nil
	}
	m := v.win.Metrics(ui.FontMono)
	v.metrics = m

	pad := v.px(v.cfg.Appearance.Padding)
	p := NewPane(80, 24, v.styles, m, maxInt(0, pad))
	// The emulator is sized before the shell starts: ConPTY is created at a
	// fixed size, and a shell that begins at the wrong width wraps its first
	// output at the wrong column until something resizes it.
	// The scrollbar strip is reserved inside the grid rather than carved out of
	// the bounds, so hit testing and drawing agree on where the text area ends.
	p.grid.SetGutter(v.px(scrollbarGutter))
	x, y, w, h := v.stageRect()
	p.grid.SetBounds(x, y, w, h)

	if cwd == "" {
		cwd = v.cfg.Behavior.DefaultCwd
	}
	if cwd == "" {
		cwd = prof.Cwd
	}
	p.onDirty = v.onPaneDirty
	if err := p.Attach(v.mgr, prof, cwd); err != nil {
		return nil
	}
	p.SetScrollback(v.cfg.Behavior.Scrollback)
	return p
}

// resolveProfile finds the profile to run: the requested one, the configured
// default, or the first available. An empty result means the machine has no
// usable shell.
func (v *View) resolveProfile(id string) (config.Profile, bool) {
	if id == "" {
		id = v.cfg.Behavior.DefaultProfile
	}
	if p, ok := profiles.Find(v.cfg.Profiles, id); ok && p.Available {
		return p, true
	}
	for _, p := range v.cfg.Profiles {
		if !p.Hidden && p.Available {
			return p, true
		}
	}
	return config.Profile{}, false
}

// onPaneDirty schedules a repaint. It runs on a session's reader goroutine, so
// it must not block: Post only queues, and the repaint happens on the message
// thread where the drawing surface lives.
func (v *View) onPaneDirty(*Pane) {
	if v.win != nil {
		v.win.Post(v.win.Invalidate)
	}
}

// ---------------------------------------------------------------------------
// ui.Host

// Resize lays the window out. It is called after the client area changed and
// after a DPI change, so it also refreshes the scale.
func (v *View) Resize(int, int) {
	v.lock()
	defer v.unlock()
	v.resizeLocked()
}

// resizeLocked lays out the panes for the current client area.
func (v *View) resizeLocked() {
	if dpi := v.win.DPI(); dpi != v.dpi {
		// The window has already rebuilt the font slots for the new DPI; only
		// the derived layout measurements are stale here.
		v.dpi = dpi
		v.fonts = fontsFor(v.cfg, dpi)
		v.metrics = v.win.Metrics(ui.FontMono)
		for _, p := range v.allPanesLocked() {
			p.SetPadding(v.px(v.cfg.Appearance.Padding))
		}
	}
	v.layoutLocked()
}

// Close shuts the shells down. The window is already going away, so no further
// layout is scheduled.
func (v *View) Close() {
	v.lock()
	v.closing = true
	tabs := v.tabs
	v.tabs = nil
	v.unlock()
	for _, t := range tabs {
		closeTabPanes(t)
	}
}

// Message handles the messages the window shell does not consume itself.
func (v *View) Message(msg uint32, wp, lp uintptr) bool {
	switch msg {
	case ui.WMKeyDown, ui.WMSysKeyDown:
		return v.onKeyDown(wp)
	case ui.WMChar, ui.WMSysChar:
		return v.onChar(wp)
	case ui.WMDeadChar:
		// A dead key begins a composition; the composed result arrives as
		// WM_CHAR, so the accent on its own must not be sent.
		return true
	case ui.WMLButtonDown:
		return v.onMouseDown(lp, ui.ModifiersFromWParam(wp))
	case ui.WMLButtonUp:
		return v.onMouseUp(lp)
	case ui.WMRButtonDown:
		return v.onRightDown(lp)
	case ui.WMMouseMove:
		return v.onMouseMove(lp, ui.ModifiersFromWParam(wp))
	case ui.WMMouseWheel:
		return v.onWheel(wp, lp)
	case ui.WMMouseHWheel:
		// There is nothing to scroll sideways here, and letting it through
		// would reach the shell as a horizontal wheel it cannot use.
		return true
	case ui.WMTimer:
		return v.onTimer(wp)
	case ui.WMSetFocus:
		v.setBlink(true)
		return false
	case ui.WMKillFocus:
		return false
	}
	return false
}

// ---------------------------------------------------------------------------
// Input

// onKeyDown routes a key press: the application's shortcuts first, then the
// terminal's own encoding.
func (v *View) onKeyDown(vk uintptr) bool {
	mods := ui.ModifiersNow()
	k := KeyEvent{
		VK:    uint32(vk),
		Ctrl:  mods.Ctrl,
		Alt:   mods.Alt,
		Shift: mods.Shift,
	}
	// A modifier held on its own produces no input, so it is swallowed rather
	// than encoded as a bare key.
	if isModifierKey(k.VK) {
		return true
	}

	v.lock()
	if action, ok := v.lookupChord(k, mods); ok {
		// The press belongs to the application, so the character Windows
		// derives from it must not go to the shell: ToUnicode maps Ctrl+letter
		// to a control code, so Ctrl+Shift+B would reach the shell as STX — the
		// same class of duplicate the encoded keys above are guarded against.
		v.pendingKey = true
		v.runActionLocked(action)
		v.unlock()
		return true
	}
	p := v.focusedPaneLocked()
	v.unlock()
	if p == nil {
		return true
	}
	// The character, if the key has one, arrives as WM_CHAR; encoding it here as
	// well would send every keystroke twice.
	data := EncodeKey(k, p.Mode())
	if len(data) == 0 {
		// Nothing was sent, so the WM_CHAR that follows is the only copy and
		// must be forwarded. Cleared here rather than left over from an earlier
		// press, so a key with no encoding cannot swallow its own character.
		v.lock()
		v.pendingKey = false
		v.unlock()
		return false
	}
	v.lock()
	// The press is recorded as encoded: its WM_CHAR is a duplicate of what was
	// just sent and onChar will drop it.
	v.pendingKey = true
	v.unlock()
	v.recordHistory(p, string(data))
	_ = p.Write(data)
	return true
}

// onChar sends the character Windows produced for the key that was pressed. A
// Ctrl chord produces none, so this only ever carries real text.
//
// A key that onKeyDown already encoded — Enter, Tab, Backspace, Escape — has
// its WM_CHAR dropped, because that character is the same input the keydown
// already delivered. Forwarding it too would run every command twice.
func (v *View) onChar(ch uintptr) bool {
	if ch == 0 {
		return true
	}
	v.lock()
	// The flag is consumed either way: a duplicate is dropped once, and a
	// character is forwarded once.
	encoded := v.pendingKey
	v.pendingKey = false
	defer v.unlock()
	if encoded {
		return true
	}
	p := v.focusedPaneLocked()
	if p == nil {
		return true
	}
	buf := appendRune(nil, rune(ch))
	v.recordHistory(p, string(buf))
	_ = p.Write(buf)
	return true
}

// recordHistory feeds keystrokes to the history store, which returns the
// command that Enter completed. A completed command is appended to the shared
// history file so the next run can recall it.
func (v *View) recordHistory(p *Pane, data string) {
	if v.hist == nil || data == "" {
		return
	}
	if cmd := v.hist.Type(p.ID(), data); cmd != "" && v.histFile != nil {
		v.histFile.Append(cmd)
	}
}

// onMouseDown starts a selection, grabs a splitter, works the window chrome, or
// reports the click to the application when it asked for mouse reporting.
func (v *View) onMouseDown(lp uintptr, mods ui.Modifiers) bool {
	x, y := ui.PointFromLParam(lp)
	now := time.Now()

	v.lock()
	defer v.unlock()

	if v.chromeClickLocked(x, y) {
		return true
	}
	if drag := v.splitterAtLocked(x, y); drag != nil {
		v.drag = drag
		// Capture keeps the drag alive when the pointer leaves the window,
		// which a splitter drag does constantly.
		v.win.SetCapture()
		return true
	}

	p := v.paneAtLocked(x, y)
	if p == nil {
		return false
	}
	v.setFocusLocked(p)

	v.win.SetCapture()
	if v.reportMouse(p, x, y, MouseLeft, true, mods) {
		return true
	}
	row, col, ok := p.ContentAt(x, y)
	if !ok {
		return true
	}
	clicks := p.ClickCount(now, row, col, doubleClickTime())
	p.BeginSelection(x, y, clicks, mods.Alt)
	v.win.Invalidate()
	return true
}

// onMouseUp finishes a selection or a splitter drag.
func (v *View) onMouseUp(lp uintptr) bool {
	x, y := ui.PointFromLParam(lp)
	v.lock()
	defer v.unlock()

	if v.drag != nil {
		v.drag = nil
		v.win.ReleaseCapture()
		// The divider was drawn highlighted for the drag; without a repaint it
		// stays highlighted until something else happens to draw a frame.
		v.win.Invalidate()
		return true
	}
	v.win.ReleaseCapture()
	p := v.paneAtLocked(x, y)
	if p == nil {
		return false
	}
	if v.reportMouse(p, x, y, MouseLeft, false, ui.ModifiersNow()) {
		return true
	}
	if v.cfg.Behavior.CopyOnSelect && p.HasSelection() {
		ui.SetClipboardText(v.win.HWND(), p.SelectionText())
	}
	return true
}

// onMouseMove drags a splitter, extends a selection, or forwards motion to the
// application when it asked for mouse reporting.
func (v *View) onMouseMove(lp uintptr, mods ui.Modifiers) bool {
	x, y := ui.PointFromLParam(lp)
	v.lock()
	defer v.unlock()

	if d := v.drag; d != nil {
		v.dragSplitLocked(d, x, y)
		return true
	}
	p := v.focusedPaneLocked()
	if p == nil {
		return false
	}
	if mods.Left {
		if v.reportMouse(p, x, y, MouseLeft, true, mods) {
			return true
		}
		p.ExtendSelection(x, y)
		v.win.Invalidate()
		return true
	}
	return v.reportMouse(p, x, y, MouseNone, true, mods)
}

// dragSplitLocked moves a splitter to follow the pointer.
//
// The ratio is derived from the pointer's offset inside the container captured
// at the press, not from its movement: a pane resized by this very drag cannot
// feed back into the calculation and make the splitter chase the pointer.
func (v *View) dragSplitLocked(d *splitDrag, x, y int) {
	pos := x - d.n.rect.X
	if d.axis == SplitAlongY {
		pos = y - d.n.rect.Y
	}
	if d.extent <= 0 {
		return
	}
	d.n.ratio = clampRatio(float64(pos) / float64(d.extent))
	v.layoutLocked()
	v.win.Invalidate()
}

// onRightDown pastes when the configuration asks for it, and otherwise reports
// the click to the application.
func (v *View) onRightDown(lp uintptr) bool {
	x, y := ui.PointFromLParam(lp)
	v.lock()
	defer v.unlock()

	if v.chromeClickLocked(x, y) {
		return true
	}
	p := v.paneAtLocked(x, y)
	if p == nil {
		return false
	}
	v.setFocusLocked(p)
	if v.reportMouse(p, x, y, MouseRight, true, ui.ModifiersNow()) {
		return true
	}
	if v.cfg.Behavior.RightClickPaste {
		v.pasteLocked(p)
	}
	return true
}

// onWheel scrolls the viewport, or reports the wheel when the application asked
// for mouse reporting. One notch is three lines and Alt multiplies that by
// five, which is the convention every terminal shares.
func (v *View) onWheel(wp, lp uintptr) bool {
	// Unlike the other mouse messages, the wheel carries screen coordinates.
	sx, sy := ui.PointFromLParam(lp)
	notches := int(int16(wp>>16)) / 120
	if notches == 0 {
		notches = sign(int(int16(wp >> 16)))
	}

	v.lock()
	defer v.unlock()
	x, y := v.win.ScreenToClient(sx, sy)
	// The panel floats over the panes, so a wheel over it must not scroll the
	// text the user cannot see.
	if v.panelVisibleRectLocked().Contains(x, y) {
		return true
	}
	p := v.paneAtLocked(x, y)
	if p == nil {
		return false
	}
	mods := ui.ModifiersNow()
	if v.reportMouse(p, x, y, MouseWheel, true, mods) {
		return true
	}
	// Wheel-up carries a positive notch count and means "back into history";
	// Scroll's positive direction is that same one, so the count is passed
	// through unchanged.
	lines := 3 * notches
	if mods.Alt {
		lines *= 5
	}
	if p.Scroll(lines) {
		v.win.Invalidate()
	}
	return true
}

// onTimer advances the cursor blink, and the panel's slide while it is moving.
// Each timer stops itself when its work is done, so an idle window costs no
// repaints at all.
func (v *View) onTimer(id uintptr) bool {
	if id == panelStepTimerID {
		v.lock()
		moved := v.stepPanelLocked()
		v.unlock()
		if moved {
			v.win.Invalidate()
		}
		return true
	}
	if id != blinkTimerID {
		return false
	}
	v.lock()
	// A sliding panel repaints anyway, and its frames repaint more often than
	// the blink, so the phase is left alone while it moves.
	sliding := v.panel.slide != 0 && v.panel.slide != 1
	p := v.focusedPaneLocked()
	blinking := p != nil && p.CursorBlink()
	if blinking && !sliding {
		v.blinkOn = !v.blinkOn
	}
	v.unlock()
	if blinking && !sliding {
		v.win.Invalidate()
	}
	return true
}

func (v *View) setBlink(on bool) {
	v.lock()
	v.blinkOn = on
	v.unlock()
}

// reportMouse forwards a mouse event to the application when it enabled mouse
// reporting, and reports whether the event was consumed.
func (v *View) reportMouse(p *Pane, x, y int, btn MouseButton, press bool, mods ui.Modifiers) bool {
	mode := p.Mode()
	if mode&vt.ModeMouseMask == 0 {
		return false
	}
	// Motion is only reported when the application asked for it. Without this
	// check a plain move would flood it with events it never requested.
	if btn == MouseNone && mode&vt.ModeMouseMotion == 0 {
		return false
	}
	col, row, ok := p.ViewportCell(x, y)
	if !ok {
		return false
	}
	seq := EncodeMouse(MouseEvent{
		X: col, Y: row, Button: btn, Press: press,
		Shift: mods.Shift, Alt: mods.Alt, Ctrl: mods.Ctrl,
		SGR: mode&vt.ModeMouseSgr != 0,
	})
	if len(seq) == 0 {
		return false
	}
	_ = p.Write(seq)
	return true
}

// ---------------------------------------------------------------------------
// Actions

// runActionLocked performs a bound action. Called with v.mu held.
//
// An action with no implementation is still consumed rather than passed to the
// shell: a chord the user believes the application owns must not silently
// become a control byte the shell acts on.
func (v *View) runActionLocked(action string) {
	switch action {
	case "newTab":
		v.newTabLocked()
	case "closeTab":
		v.closeTabLocked(v.active)
	case "closePane":
		v.closePaneLocked()
	case "splitRight":
		v.splitLocked(SplitAlongX)
	case "splitDown":
		v.splitLocked(SplitAlongY)
	case "focusNextPane":
		v.cycleFocusLocked(1)
	case "focusPrevPane":
		v.cycleFocusLocked(-1)
	case "nextTab":
		v.cycleTabLocked(1)
	case "prevTab":
		v.cycleTabLocked(-1)
	case "maximizePane":
		v.toggleMaximizeLocked()
	case "fontIncrease":
		v.stepFontLocked(1)
	case "fontDecrease":
		v.stepFontLocked(-1)
	case "fontReset":
		v.setFontSizeLocked(config.Default().Appearance.FontSize)
	case "clearTerminal":
		if p := v.focusedPaneLocked(); p != nil {
			p.Clear()
			v.win.Invalidate()
		}
	case "restartPane":
		v.restartPaneLocked()
	case "duplicatePane":
		v.duplicatePaneLocked()
	case "copy":
		if p := v.focusedPaneLocked(); p != nil && p.HasSelection() {
			ui.SetClipboardText(v.win.HWND(), p.SelectionText())
		}
	case "paste":
		if p := v.focusedPaneLocked(); p != nil {
			v.pasteLocked(p)
		}
	case "selectAll":
		if p := v.focusedPaneLocked(); p != nil {
			p.SelectAll()
			v.win.Invalidate()
		}
	case "toggleSidebar":
		v.toggleSidebarLocked()
	case "detachTab", "settings", "commandPalette", "searchTerminal", "history":
		// Consumed deliberately. These were panels in the browser frontend and
		// have no implementation here yet; letting the chord through would send
		// the shell a control byte the user never asked for.
	}
}

// restartPaneLocked replaces the focused pane with a fresh shell running the
// same profile in the same directory, in the same place in the layout.
func (v *View) restartPaneLocked() {
	if v.active < 0 || v.active >= len(v.tabs) {
		return
	}
	t := v.tabs[v.active]
	old := t.focus
	if old == nil {
		return
	}
	info := old.Info()
	fresh := v.newPaneLocked(info.Profile, info.Cwd)
	if fresh == nil {
		return
	}
	old.Close()
	// The new pane takes the old one's place in the tree, so the layout does
	// not shift under the user.
	if !replacePane(t.root, old, fresh) {
		fresh.Close()
		return
	}
	t.focus = fresh
	v.layoutLocked()
	v.win.Invalidate()
}

// duplicatePaneLocked splits the focused pane and runs the same profile in the
// same directory, which is what "duplicate" means in every tabbed terminal.
func (v *View) duplicatePaneLocked() {
	if v.active < 0 || v.active >= len(v.tabs) {
		return
	}
	t := v.tabs[v.active]
	if t.focus == nil {
		return
	}
	info := t.focus.Info()
	fresh := v.newPaneLocked(info.Profile, info.Cwd)
	if fresh == nil {
		return
	}
	// Split along the longer edge so both halves stay usable.
	_, _, w, h := t.focus.grid.Bounds()
	axis := SplitAlongX
	if h > w {
		axis = SplitAlongY
	}
	v.insertSplitLocked(t, fresh, axis)
}

// replacePane swaps one pane for another in place, preserving the surrounding
// splits.
func replacePane(n *node, old, fresh *Pane) bool {
	if n == nil {
		return false
	}
	if n.pane == old {
		n.pane = fresh
		return true
	}
	return replacePane(n.a, old, fresh) || replacePane(n.b, old, fresh)
}

// cycleTabLocked moves to the next tab in order.
func (v *View) cycleTabLocked(delta int) {
	if len(v.tabs) < 2 {
		return
	}
	n := len(v.tabs)
	v.active = ((v.active+delta)%n + n) % n
	// Every tab keeps its emulator and scrollback alive, so switching is only a
	// visibility change: nothing is disposed and nothing has to replay.
	v.blinkOn = true
	v.layoutLocked()
	v.win.Invalidate()
}

// closePaneLocked removes the focused pane, collapsing its parent split. A tab
// whose last pane closes is removed, and closing the last tab closes the
// window, which is what every terminal does.
func (v *View) closePaneLocked() bool {
	if v.active < 0 || v.active >= len(v.tabs) {
		return false
	}
	t := v.tabs[v.active]
	if t.focus == nil {
		return false
	}
	return v.removePaneLocked(t, t.focus)
}

// removePaneLocked drops one pane from a tab, collapsing the split it lived in
// and closing the shell with it. A tab whose only pane goes disappears, window
// and all.
//
// Both the focused-pane shortcut and the panel's kill button land here, so a
// session ended from the panel leaves the layout exactly as a closed pane
// would: no empty split, and no pane still drawing a shell that is gone.
func (v *View) removePaneLocked(t *Tab, doomed *Pane) bool {
	if t == nil || doomed == nil {
		return false
	}
	if t.maximized == doomed {
		t.maximized = nil
	}
	// A tab whose only pane closes disappears, window and all.
	if t.root.pane == doomed {
		for i, other := range v.tabs {
			if other == t {
				v.closeTabLocked(i)
				return true
			}
		}
		return false
	}
	parent, _ := findParent(t.root, t.root, doomed)
	if parent == nil {
		return false
	}
	doomed.Close()
	sib := parent.a
	if sib != nil && sib.pane == doomed {
		sib = parent.b
	}
	if sib == nil {
		return false
	}
	*parent = *sib
	t.focus = firstPane(t.root)
	v.layoutLocked()
	v.win.Invalidate()
	return true
}

// findParent returns the split node whose direct child is target.
func findParent(root, cur *node, target *Pane) (parent, side *node) {
	if cur == nil || cur.pane != nil {
		return nil, nil
	}
	if cur.a != nil && cur.a.pane == target {
		return cur, cur.a
	}
	if cur.b != nil && cur.b.pane == target {
		return cur, cur.b
	}
	if p, s := findParent(root, cur.a, target); p != nil {
		return p, s
	}
	return findParent(root, cur.b, target)
}

// firstPane returns the leftmost, topmost pane in a subtree, which is the focus
// a collapsed split hands over.
func firstPane(n *node) *Pane {
	if n == nil {
		return nil
	}
	if n.pane != nil {
		return n.pane
	}
	if p := firstPane(n.a); p != nil {
		return p
	}
	return firstPane(n.b)
}

// splitLocked splits the focused pane with a new shell running the default
// profile.
func (v *View) splitLocked(axis Axis) bool {
	if v.active < 0 || v.active >= len(v.tabs) {
		return false
	}
	t := v.tabs[v.active]
	if t.focus == nil {
		return false
	}
	fresh := v.newPaneLocked("", "")
	if fresh == nil {
		return false
	}
	v.insertSplitLocked(t, fresh, axis)
	return true
}

// insertSplitLocked puts a new pane beside the focused one. Called with v.mu
// held.
func (v *View) insertSplitLocked(t *Tab, fresh *Pane, axis Axis) {
	switch {
	case t.root.pane == t.focus:
		t.root = &node{axis: axis, ratio: 0.5, a: &node{pane: t.focus}, b: &node{pane: fresh}}
	default:
		target, side := findParent(t.root, t.root, t.focus)
		if target == nil || side == nil {
			fresh.Close()
			return
		}
		replacement := &node{axis: axis, ratio: 0.5, a: side, b: &node{pane: fresh}}
		if target.a == side {
			target.a = replacement
		} else {
			target.b = replacement
		}
	}
	t.focus = fresh
	v.blinkOn = true
	v.layoutLocked()
	v.win.Invalidate()
}

// cycleFocusLocked moves focus to the next pane in visual order.
func (v *View) cycleFocusLocked(delta int) {
	if v.active < 0 || v.active >= len(v.tabs) {
		return
	}
	t := v.tabs[v.active]
	panes := paneOrder(t.root)
	if len(panes) < 2 {
		return
	}
	idx := 0
	for i, p := range panes {
		if p == t.focus {
			idx = i
			break
		}
	}
	n := len(panes)
	t.focus = panes[((idx+delta)%n+n)%n]
	v.blinkOn = true
	v.win.Invalidate()
}

// paneOrder lists panes in visual order: left to right, then top to bottom.
func paneOrder(n *node) []*Pane {
	var out []*Pane
	var walk func(*node)
	walk = func(cur *node) {
		if cur == nil {
			return
		}
		if cur.pane != nil {
			out = append(out, cur.pane)
			return
		}
		walk(cur.a)
		walk(cur.b)
	}
	walk(n)
	return out
}

// toggleMaximizeLocked makes the focused pane cover its tab, and restores the
// split when pressed again.
func (v *View) toggleMaximizeLocked() {
	if v.active < 0 || v.active >= len(v.tabs) {
		return
	}
	t := v.tabs[v.active]
	if t.maximized != nil {
		t.maximized = nil
	} else if t.focus != nil {
		t.maximized = t.focus
	}
	v.layoutLocked()
	v.win.Invalidate()
}

// stepFontLocked steps the font size by one point.
func (v *View) stepFontLocked(delta int) {
	v.setFontSizeLocked(v.cfg.Appearance.FontSize + float64(delta))
}

// setFontSizeLocked resizes the terminal font.
//
// The size is applied to the running configuration but never written back to
// the file: a shortcut press must not rewrite the user's config.
func (v *View) setFontSizeLocked(size float64) {
	size = math.Round(size*2) / 2
	if size < 6 {
		size = 6
	}
	if size > 48 {
		size = 48
	}
	if size == v.cfg.Appearance.FontSize {
		return
	}
	v.cfg.Appearance.FontSize = size
	v.fonts = fontsFor(v.cfg, v.win.DPI())
	// SetFonts rebuilds the slots and then calls Resize back to lay out with
	// the new metrics. lock() lets that nested call through on this thread.
	v.win.SetFonts(v.fonts)
	v.metrics = v.win.Metrics(ui.FontMono)
	for _, p := range v.allPanesLocked() {
		p.grid.Invalidate()
	}
	v.layoutLocked()
	v.win.Invalidate()
}

// pasteLocked sends the clipboard to a pane.
func (v *View) pasteLocked(p *Pane) {
	text, ok := ui.ClipboardText(v.win.HWND())
	if !ok || text == "" {
		return
	}
	// The emulator decides whether the payload needs the bracketed-paste
	// wrapper, so a program that asked for it is never fed a paste it would
	// interpret as typing.
	_ = p.Write(Paste(text, p.Mode()&vt.ModeBracketedPaste != 0))
}

// ---------------------------------------------------------------------------
// Chrome

// chromeClickLocked handles a click on the tab strip, the window controls or
// the empty stage, and reports whether it was consumed.
func (v *View) chromeClickLocked(x, y int) bool {
	// The panel begins below the strip, so it can never cover the window's own
	// buttons. It is still resolved before the panes: a click on a row would
	// otherwise start a text selection in the pane beneath it.
	if v.panelClickLocked(x, y) {
		return true
	}
	if y >= v.px(controlHeight) {
		return false
	}
	// The window's own buttons sit at the right end of the tab strip. They are
	// tested first because they overlap the strip's row, and a click there is
	// never meant for a tab.
	cw, _ := v.win.ClientSize()
	btn := v.px(controlWidth)
	if x >= cw-3*btn {
		switch {
		case x >= cw-btn:
			v.win.Close()
		case x >= cw-2*btn:
			v.win.ToggleMaximize()
		default:
			v.win.Minimize()
		}
		return true
	}

	// The "+" and split buttons end the strip and occupy their own widths only;
	// everything past them stays a window-drag region.
	stripBtn, onButton := v.stripButtonHit(x, y)

	tab, close := v.tabHitLocked(x)
	switch {
	case close >= 0:
		v.closeTabLocked(close)
	case tab >= 0:
		v.active = tab
		v.blinkOn = true
		v.layoutLocked()
		v.win.Invalidate()
	case onButton:
		v.runActionLocked(stripBtn.action)
	case y < v.px(tabStripHeight):
		// Empty strip past the buttons: a drag here moves the window, the same
		// as dragging a native title bar.
		v.win.StartDrag()
	default:
		return false
	}
	return true
}

// stripButtons returns the chrome buttons that follow the last tab along the
// strip, in the order they are drawn and hit-tested. They are described once so
// a button cannot be drawn where it is not clickable.
//
// The strip is the only chrome a frameless window still shows, so it is where
// the split actions live: a keystroke that has to be remembered is not an
// affordance, and the marker explains what the button does without a tooltip.
func (v *View) stripButtons() []stripButton {
	// The window's own buttons own the right end of the row. A button that
	// would reach under them is dropped rather than drawn as a target that
	// cannot be clicked, which is what happens once the tabs fill the strip.
	cw, _ := v.win.ClientSize()
	limit := cw - 3*v.px(controlWidth)

	x := v.newTabXLocked()
	out := make([]stripButton, 0, 3)
	for _, spec := range []struct {
		width  int
		action string
	}{
		{newTabWidth, "newTab"},
		{splitButtonWidth, "splitRight"},
		{splitButtonWidth, "splitDown"},
	} {
		w := v.px(spec.width)
		if x+w > limit {
			break
		}
		out = append(out, stripButton{x: x, width: spec.width, action: spec.action})
		x += w
	}
	return out
}

// stripButton is one chrome button on the tab strip.
type stripButton struct {
	x      int
	width  int
	action string
}

// stripButtonHit resolves a point in the tab strip to the button under it. The
// buttons are hit-tested before the tabs, so a wide strip of tabs cannot cover
// them.
func (v *View) stripButtonHit(x, y int) (stripButton, bool) {
	if y >= v.px(tabStripHeight) {
		return stripButton{}, false
	}
	for _, b := range v.stripButtons() {
		w := v.px(b.width)
		if x >= b.x && x < b.x+w {
			return b, true
		}
	}
	return stripButton{}, false
}

// paintSplitIcon draws a two-pane diagram. The focused pane is filled and the
// other is outlined, so each button shows which way the split will divide the
// window: a vertical divider between side-by-side panes means "right", a
// horizontal one between stacked panes means "down".
func (v *View) paintSplitIcon(s ui.Surface, cx, cy int, axis Axis, fg, bg, accent ui.Color) {
	w, h := v.px(splitIconW), v.px(splitIconH)
	if w < 6 || h < 6 {
		return
	}
	x, y := cx-w/2, cy-h/2
	b := maxInt(1, v.px(1))

	// The frame, the other pane and the divider, so the icon reads as a window
	// that has been divided even at 125% where a thin outline blurs.
	s.Fill(x, y, w, h, fg)
	s.Fill(x+b, y+b, w-2*b, h-2*b, bg)

	switch axis {
	case SplitAlongX:
		s.Fill(x+b, y+b, (w-2*b)/2, h-2*b, accent)
		s.Fill(x+w/2-b/2, y, b, h, fg)
	default:
		s.Fill(x+b, y+b, w-2*b, (h-2*b)/2, accent)
		s.Fill(x, y+h/2-b/2, w, b, fg)
	}
}

// newTabXLocked is the left edge of the "+" button, which follows the last tab.
func (v *View) newTabXLocked() int {
	pos := v.px(tabLeftPad)
	for _, t := range v.tabs {
		pos += v.tabWidthLocked(t) + v.px(tabGap)
	}
	return pos
}

// tabHitLocked resolves a tab-strip coordinate to a tab index, and to a
// close-button index when the click landed on the button.
func (v *View) tabHitLocked(x int) (tab, close int) {
	tab, close = -1, -1
	pos := v.px(tabLeftPad)
	// Only the strip row is a tab; the window buttons share its height but were
	// already handled.
	btn := v.px(tabCloseWidth)
	for i, t := range v.tabs {
		w := v.tabWidthLocked(t)
		if x >= pos && x < pos+w {
			if x >= pos+w-btn {
				return i, i
			}
			return i, -1
		}
		pos += w + v.px(tabGap)
	}
	return -1, -1
}

// tabWidthLocked sizes a tab to its title, clamped so a long title cannot push
// every other tab off the strip.
func (v *View) tabWidthLocked(t *Tab) int {
	title := t.title
	if title == "" {
		title = t.name()
	}
	w := v.win.TextWidth(ui.FontUI, title) + v.px(38)
	return clampInt(w, v.px(tabMinWidth), v.px(tabMaxWidth))
}

// name is the tab's display title, taken from the shell's own title when it set
// one and from the profile otherwise.
func (t *Tab) name() string {
	if p := firstPane(t.root); p != nil {
		if title := p.Title(); title != "" {
			return title
		}
		if name := p.Name(); name != "" {
			return name
		}
	}
	return "Terminal"
}

// closeTabLocked closes one tab, and the window when it was the last.
func (v *View) closeTabLocked(idx int) {
	if idx < 0 || idx >= len(v.tabs) {
		return
	}
	closeTabPanes(v.tabs[idx])
	v.tabs = append(v.tabs[:idx], v.tabs[idx+1:]...)
	if len(v.tabs) == 0 {
		v.active = 0
		// The window exists to host terminals; with none left there is nothing
		// to look at, so it closes, as every terminal does.
		v.win.Close()
		return
	}
	// The removal shifts every tab after it down one, so the active index has
	// to follow rather than be clamped afterwards: closing the first of four
	// tabs while the last was active would otherwise land on the wrong one.
	if v.active > idx {
		v.active--
	}
	if v.active >= len(v.tabs) {
		v.active = len(v.tabs) - 1
	}
	v.layoutLocked()
	v.win.Invalidate()
}

// closeTabPanes stops every shell in a tab.
func closeTabPanes(t *Tab) {
	if t == nil {
		return
	}
	for _, p := range paneOrder(t.root) {
		p.Close()
	}
}

// paneAtLocked finds the pane under a point in the active tab.
func (v *View) paneAtLocked(x, y int) *Pane {
	if v.active < 0 || v.active >= len(v.tabs) {
		return nil
	}
	return paneAtNode(v.tabs[v.active].root, x, y)
}

func paneAtNode(n *node, x, y int) *Pane {
	if n == nil || !n.rect.Contains(x, y) {
		return nil
	}
	if n.pane != nil {
		return n.pane
	}
	if p := paneAtNode(n.a, x, y); p != nil {
		return p
	}
	return paneAtNode(n.b, x, y)
}

// splitterAtLocked finds the splitter under a point, which is the gap between
// the two children of a split.
func (v *View) splitterAtLocked(x, y int) *splitDrag {
	if v.active < 0 || v.active >= len(v.tabs) {
		return nil
	}
	t := v.tabs[v.active]
	if t.maximized != nil {
		// Nothing is splittable while a pane covers the tab.
		return nil
	}
	n := splitterAt(t.root, x, y, v.px(splitterWidth))
	if n == nil {
		return nil
	}
	d := &splitDrag{n: n, axis: n.axis, extent: n.rect.W}
	if n.axis == SplitAlongY {
		d.extent = n.rect.H
	}
	return d
}

func splitterAt(n *node, x, y, width int) *node {
	if n == nil || n.pane != nil || !n.rect.Contains(x, y) {
		return nil
	}
	if splitterHit(n, x, y, width) {
		return n
	}
	if got := splitterAt(n.a, x, y, width); got != nil {
		return got
	}
	return splitterAt(n.b, x, y, width)
}

// splitterHit reports whether a point lands on a split's divider.
func splitterHit(n *node, x, y, width int) bool {
	if n.axis == SplitAlongX {
		edge := n.rect.X + int(float64(n.rect.W)*n.ratio)
		return x >= edge-width/2 && x <= edge+width/2
	}
	edge := n.rect.Y + int(float64(n.rect.H)*n.ratio)
	return y >= edge-width/2 && y <= edge+width/2
}

// clampRatio keeps both halves of a split usable.
func clampRatio(r float64) float64 {
	if r < minPaneRatio {
		return minPaneRatio
	}
	if r > 1-minPaneRatio {
		return 1 - minPaneRatio
	}
	return r
}

func (v *View) focusedPaneLocked() *Pane {
	if v.active < 0 || v.active >= len(v.tabs) {
		return nil
	}
	return v.tabs[v.active].focus
}

func (v *View) setFocusLocked(p *Pane) {
	if v.active < 0 || v.active >= len(v.tabs) {
		return
	}
	v.tabs[v.active].focus = p
	v.blinkOn = true
	v.win.Invalidate()
}

// ---------------------------------------------------------------------------
// Layout

// stageRect is the area a tab's panes share: everything below the tab strip.
func (v *View) stageRect() (x, y, w, h int) {
	cw, ch := v.win.ClientSize()
	top := v.px(tabStripHeight)
	return 0, top, cw, maxInt(0, ch-top)
}

// layoutLocked rebuilds the active tab's pane rectangles and resizes its
// emulators. Hidden tabs keep their last geometry: they are laid out only when
// they become active, so a background tab costs nothing to keep alive.
func (v *View) layoutLocked() {
	if v.active < 0 || v.active >= len(v.tabs) {
		return
	}
	t := v.tabs[v.active]
	x, y, w, h := v.stageRect()

	if t.maximized != nil {
		// A maximized pane covers the tab; the others are hidden but keep
		// parsing, so restoring is instant and their history is current.
		for _, p := range paneOrder(t.root) {
			if p == t.maximized {
				p.grid.SetVisible(true)
				p.grid.SetBounds(x, y, w, h)
				v.syncPaneSize(p)
			} else {
				p.grid.SetVisible(false)
			}
		}
		return
	}

	for _, p := range paneOrder(t.root) {
		p.grid.SetVisible(true)
	}
	v.layoutNode(t.root, ui.Rect{X: x, Y: y, W: w, H: h}, false)
}

// layoutNode assigns rectangles down the tree.
//
// The pane rectangles are inset by the focus border only when the tab holds
// more than one pane, so a single-pane tab reclaims those pixels for text.
func (v *View) layoutNode(n *node, r ui.Rect, inset bool) {
	if n == nil {
		return
	}
	if inset {
		r = r.Inset(v.px(focusBorderWidth), v.px(focusBorderWidth))
	}
	n.rect = r
	if n.pane != nil {
		n.pane.grid.SetBounds(r.X, r.Y, r.W, r.H)
		v.syncPaneSize(n.pane)
		return
	}

	split := v.px(splitterWidth)
	if n.axis == SplitAlongX {
		lead := clampInt(int(float64(r.W)*n.ratio), 0, maxInt(0, r.W-split))
		n.a.setRect(r.X, r.Y, lead, r.H)
		n.b.setRect(r.X+lead+split, r.Y, maxInt(0, r.W-lead-split), r.H)
	} else {
		lead := clampInt(int(float64(r.H)*n.ratio), 0, maxInt(0, r.H-split))
		n.a.setRect(r.X, r.Y, r.W, lead)
		n.b.setRect(r.X, r.Y+lead+split, r.W, maxInt(0, r.H-lead-split))
	}
	v.layoutNode(n.a, n.a.rect, true)
	v.layoutNode(n.b, n.b.rect, true)
}

func (n *node) setRect(x, y, w, h int) {
	n.rect = ui.Rect{X: x, Y: y, W: w, H: h}
}

// syncPaneSize reports a pane's new cell size to its shell. The grid resized
// the emulator itself, so this only forwards the result to ConPTY.
func (v *View) syncPaneSize(p *Pane) {
	cols, rows := p.grid.Size()
	if cols < 1 || rows < 1 {
		return
	}
	p.Resize(cols, rows)
}

// ---------------------------------------------------------------------------
// Painting

// Paint draws one frame: the chrome, then every visible pane of the active tab.
func (v *View) Paint(s ui.Surface, w, h int) {
	v.lock()
	defer v.unlock()

	s.Fill(0, 0, w, h, v.pal.UIBackground)

	if len(v.tabs) == 0 {
		v.paintSplash(s, w, h)
		v.paintControls(s, w)
		v.paintPanel(s)
		return
	}

	v.paintTabs(s, w)
	v.paintControls(s, w)

	t := v.tabs[v.active]
	panes := paneOrder(t.root)
	multi := len(panes) > 1
	for _, p := range panes {
		if !p.grid.Visible() {
			continue
		}
		p.Paint(s, v.pal, v.blinkOn)
	}
	if multi {
		// The divider is drawn before the borders but after the panes, because
		// it lives in the gap between two pane rectangles that no pane covers.
		v.paintSplitters(s, t.root)
		// Drawn after the panes: the pane fill covers its own rectangle, so a
		// border painted first would be erased by the pane it belongs to.
		for _, p := range panes {
			if p.grid.Visible() {
				v.paintPaneBorder(s, p)
			}
		}
	}
	// Last, so the panel is not overpainted by the panes it floats above.
	v.paintPanel(s)
}

// splitGap is the space a split leaves between its two children. It is read
// from the children rather than recomputed from the ratio, so the drawn divider
// and the drag target cannot drift apart.
func splitGap(n *node) (x, y, w, h int, ok bool) {
	if n == nil || n.pane != nil || n.a == nil || n.b == nil {
		return 0, 0, 0, 0, false
	}
	if n.axis == SplitAlongX {
		left := n.a.rect.X + n.a.rect.W
		right := n.b.rect.X
		if right <= left {
			return 0, 0, 0, 0, false
		}
		return left, n.rect.Y, right - left, n.rect.H, true
	}
	top := n.a.rect.Y + n.a.rect.H
	bottom := n.b.rect.Y
	if bottom <= top {
		return 0, 0, 0, 0, false
	}
	return n.rect.X, top, n.rect.W, bottom - top, true
}

// paintSplitters fills the divider between the halves of every split.
//
// The panes are inset from the divider, leaving a gap that is the drag target
// for resizing them. Left unpainted that target is invisible, so a split reads
// as two windows that happen to touch and the drag is undiscoverable. The
// divider under the pointer is highlighted, which is what makes it findable.
func (v *View) paintSplitters(s ui.Surface, n *node) {
	if n == nil || n.pane != nil {
		return
	}
	x, y, w, h, ok := splitGap(n)
	if ok {
		col := v.pal.Splitter
		if v.drag != nil && v.drag.n == n {
			col = v.pal.UIAccent
		}
		s.Fill(x, y, w, h, col)
	}
	v.paintSplitters(s, n.a)
	v.paintSplitters(s, n.b)
}

// paintTabs draws the tab strip.
func (v *View) paintTabs(s ui.Surface, w int) {
	stripH := v.px(tabStripHeight)
	s.Fill(0, 0, w, stripH, v.pal.UIBackground)

	x := v.px(tabLeftPad)
	gap := v.px(tabGap)
	for i, t := range v.tabs {
		tw := v.tabWidthLocked(t)
		bg := v.pal.TabInactive
		fg := v.pal.UIForegroundDim
		if i == v.active {
			bg = v.pal.TabActive
			fg = v.pal.UIForeground
			// An accent bar on the leading edge is what marks the active tab at
			// a glance when several tabs share one title.
			s.Fill(x, 0, v.px(2), stripH, v.pal.UIAccent)
		}
		s.Fill(x, 0, tw, stripH, bg)

		title := t.title
		if title == "" {
			title = t.name()
		}
		fm := s.SetFont(ui.FontUIBold)
		if i != v.active {
			fm = s.SetFont(ui.FontUI)
		}
		pad := v.px(tabLeftPad)
		s.Text(x+pad, (stripH-fm.TextH())/2, clipText(s, title, tw-pad-v.px(tabCloseWidth)), ui.Style{FG: fg, BG: bg})

		// The close button only appears on the active tab, so a strip full of
		// tabs is not a row of targets that all do the same thing.
		if i == v.active {
			cx := x + tw - v.px(tabCloseWidth)
			s.SetFont(ui.FontUI)
			s.Text(cx+v.px(5), (stripH-fm.TextH())/2, "\u00d7", ui.Style{FG: fg, BG: bg})
		}
		x += tw + gap
	}
	// The strip buttons follow the last tab. Each is filled, labelled with its
	// icon, and highlighted under the pointer so it reads as a target rather
	// than as part of the strip.
	fm := s.SetFont(ui.FontUI)
	mx, my := v.win.CursorPos()
	hoverX, hoverY := v.win.ScreenToClient(mx, my)
	hover := hoverY < v.px(controlHeight)

	for _, b := range v.stripButtons() {
		bw := v.px(b.width)
		bx := b.x
		bh := stripH - v.px(12)
		by := v.px(6)
		bg := v.pal.UIBackgroundAlt
		fg := v.pal.UIForeground
		accent := v.pal.UIAccent
		if hover && hoverX >= bx && hoverX < bx+bw && hoverY < stripH {
			bg = v.pal.UIBorder
		}
		s.Fill(bx, by, bw, bh, bg)

		switch b.action {
		case "newTab":
			s.Text(bx+(bw-fm.CellW)/2, (stripH-fm.TextH())/2, "+", ui.Style{FG: fg, BG: bg})
		case "splitRight":
			v.paintSplitIcon(s, bx+bw/2, stripH/2, SplitAlongX, fg, bg, accent)
		case "splitDown":
			v.paintSplitIcon(s, bx+bw/2, stripH/2, SplitAlongY, fg, bg, accent)
		}
	}
}

// paintControls draws the window's own minimise, maximise and close buttons.
// The window is frameless, so nothing else draws them.
func (v *View) paintControls(s ui.Surface, w int) {
	bw := v.px(controlWidth)
	bh := v.px(controlHeight)
	if w < 3*bw {
		return
	}
	hover := -1
	if x, y := v.win.CursorPos(); y < bh {
		cx, _ := v.win.ScreenToClient(x, y)
		switch {
		case cx >= w-bw:
			hover = 2
		case cx >= w-2*bw:
			hover = 1
		case cx >= w-3*bw:
			hover = 0
		}
	}
	fm := s.SetFont(ui.FontUI)
	for i, glyph := range []string{"\u2013", "\u25a1", "\u2715"} {
		x := w - (3-i)*bw
		bg := v.pal.UIBackground
		fg := v.pal.UIForeground
		if hover == i {
			if i == 2 {
				bg, fg = ui.RGB(0xc0, 0x40, 0x40), ui.RGB(0xff, 0xff, 0xff)
			} else {
				bg = v.pal.UIBackgroundAlt
			}
		}
		s.Fill(x, 0, bw, bh, bg)
		tw := s.TextWidth(glyph)
		s.Text(x+(bw-tw)/2, (bh-fm.TextH())/2, glyph, ui.Style{FG: fg, BG: bg})
	}
}

// paintPaneBorder outlines an unfocused pane inside a split, so the user can
// tell which one receives keys.
func (v *View) paintPaneBorder(s ui.Surface, p *Pane) {
	x, y, w, h := p.grid.Bounds()
	bw := v.px(focusBorderWidth)
	col := v.pal.PaneBorder
	if v.active >= 0 && v.active < len(v.tabs) && v.tabs[v.active].focus == p {
		col = v.pal.UIAccent
	}
	s.Fill(x, y, w, bw, col)
	s.Fill(x, y+h-bw, w, bw, col)
	s.Fill(x, y, bw, h, col)
	s.Fill(x+w-bw, y, bw, h, col)
}

// paintSplash draws the placeholder shown while no tab is open.
func (v *View) paintSplash(s ui.Surface, w, h int) {
	fm := s.SetFont(ui.FontUI)
	tw := s.TextWidth(v.splash)
	s.Text((w-tw)/2, (h-fm.TextH())/2, v.splash, ui.Style{FG: v.pal.UIForegroundDim, BG: v.pal.UIBackground})
}

// clipText trims a string to fit a width, appending an ellipsis when it does
// not. A tab title is arbitrary, so it cannot be drawn unmeasured.
func clipText(s ui.Surface, text string, max int) string {
	if max <= 0 {
		return ""
	}
	if s.TextWidth(text) <= max {
		return text
	}
	r := []rune(text)
	for len(r) > 1 {
		r = r[:len(r)-1]
		if s.TextWidth(string(r)+"\u2026") <= max {
			break
		}
	}
	return string(r) + "\u2026"
}
