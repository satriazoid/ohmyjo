//go:build windows

package app

import (
	"math"

	"ohmyjo/internal/session"
	"ohmyjo/internal/ui"
)

// The slide panel's metrics, in logical (96 dpi) pixels. Every use goes through
// px(), so the panel keeps its proportions at 125% and above.
const (
	// panelHeaderHeight is the title row reserved above the session list.
	panelHeaderHeight = 34
	// panelRowHeight is the pitch of one session row.
	panelRowHeight = 32
	// panelRowGap is the space between two session rows, so a list of them does
	// not read as one solid block.
	panelRowGap = 4
	// panelPad is the inset of the list from the panel's edges.
	panelPad = 8
	// panelKillWidth is the width of the kill button at a row's trailing edge.
	panelKillWidth = 30
	// panelCloseWidth is the width of the close button in the panel's header.
	// It matches the kill buttons below so the panel's two crosses read as the
	// same control at two levels.
	panelCloseWidth = 30
	// panelEdgeWidth is the divider drawn along the panel's inner edge, which
	// is what separates it from the panes underneath.
	panelEdgeWidth = 1
	// panelMinColumns is the narrowest a pane may become before the panel width
	// is clamped. Forty columns is the width a shell's output still reads at,
	// and a pane narrower than that wraps every command's output.
	panelMinColumns = 40
)

// panelStepTimerID identifies the slide animation timer.
const panelStepTimerID = 2

// panelStepInterval is the animation frame period. 16 ms is one frame at 60 Hz,
// which is as smooth as a GDI blit needs to be.
const panelStepInterval = 16

// panelSlideStep is how much of the slide one frame completes. Six frames is
// 96 ms, fast enough to read as a slide rather than as a redraw.
const panelSlideStep = 1.0 / 6.0

// panelState is the slide-out kill panel: whether it is open, how far it has
// slid on screen, and the rows it shows.
//
// The panel floats over the panes instead of displacing them. Reserving its
// width would resize every shell's ConPTY once per animation frame, and the
// panel is a transient overlay: the user opens it to end a shell, not to work
// beside it.
type panelState struct {
	// open is the target state, which the slide animates towards.
	open bool
	// slide is the animation's progress: 0 fully off screen, 1 fully out.
	slide float64
	// rows is scratch space for panelRowsLocked, reused between calls so
	// painting and hit testing do not allocate a list per frame. A returned
	// slice is only valid until the next panelRowsLocked call.
	rows []panelRow
}

// panelRow is one session row: the row itself, the kill button at its trailing
// edge, and the session they stand for. The geometry is derived from the panel
// rectangle on every pass, so a row cannot be drawn where it is not clickable.
type panelRow struct {
	rect ui.Rect
	kill ui.Rect
	info session.Info
	// active is true for the session the focused pane runs, which is the one
	// the rest of the window is showing. Without it the list is a set of names
	// with no answer to "which one am I looking at", and a user who has just
	// split a pane cannot tell the new shell from the old one.
	active bool
}

// panelShellRect is the panel at its full width, in physical pixels.
//
// The slide clips this rectangle rather than shrinking it: shrinking would
// re-wrap every row's text once per frame, so a title would flicker its way in
// instead of sliding.
func panelShellRect(side string, width, cw, ch, top int) ui.Rect {
	width = clampInt(width, 0, cw)
	h := maxInt(0, ch-top)
	if side == "right" {
		return ui.Rect{X: cw - width, Y: top, W: width, H: h}
	}
	return ui.Rect{X: 0, Y: top, W: width, H: h}
}

// panelVisibleRect is the part of the panel on screen at the current slide, and
// the only part hit-tested. mid-slide a row is therefore clickable exactly where
// it is drawn.
func panelVisibleRect(shell ui.Rect, side string, slide float64) ui.Rect {
	if shell.Empty() {
		return ui.Rect{}
	}
	shown := int(math.Round(float64(shell.W) * slide))
	if shown <= 0 {
		return ui.Rect{}
	}
	if shown > shell.W {
		shown = shell.W
	}
	if side == "right" {
		return ui.Rect{X: shell.X + shell.W - shown, Y: shell.Y, W: shown, H: shell.H}
	}
	return ui.Rect{X: shell.X, Y: shell.Y, W: shown, H: shell.H}
}

// panelRectPhysical is the panel's full width in physical pixels, clamped so the
// panes behind it keep a usable number of columns.
//
// A panel the user configured to 720 logical pixels on a 1536 logical display
// would leave a 40-column shell nothing, so the width yields to the panes rather
// than the other way round.
func (v *View) panelRectPhysical() ui.Rect {
	cw, ch := v.win.ClientSize()
	width := v.px(v.cfg.Sidebar.Width)
	// The panes keep their minimum: the window is the user's, so the panel
	// gives way rather than the text. The cell advance comes from the live font
	// metrics, so the floor follows a font size change.
	cellW := maxInt(1, v.metrics.CellW)
	if room := cw - panelMinColumns*cellW; width > room {
		width = room
	}
	return panelShellRect(v.cfg.Sidebar.Side, width, cw, ch, v.stripHeightLocked())
}

// panelCloseRect is the close button in the panel's header.
//
// It is derived from the visible rectangle rather than stored, for the same
// reason the rows are: a button that is drawn from one rectangle and hit-tested
// against another is a button that stops working mid-slide. Anchoring it to the
// trailing edge puts it in the panel's top-right corner whichever side the panel
// is docked to, which is where the user looks for it.
func (v *View) panelCloseRect(vis ui.Rect) ui.Rect {
	if vis.Empty() {
		return ui.Rect{}
	}
	w := v.px(panelCloseWidth)
	pad := v.px(panelPad)
	if vis.W < 2*pad+w {
		return ui.Rect{}
	}
	h := v.px(panelHeaderHeight) - v.px(panelPad)
	y := vis.Y + (v.px(panelHeaderHeight)-h)/2
	return ui.Rect{X: vis.X + vis.W - pad - w, Y: y, W: w, H: h}
}

// panelVisibleRectLocked is the on-screen part of the panel, empty when it is
// closed. Every other panel function starts here, so "is the panel there" has
// one answer.
func (v *View) panelVisibleRectLocked() ui.Rect {
	if v.panel.slide <= 0 {
		return ui.Rect{}
	}
	return panelVisibleRect(v.panelRectPhysical(), v.cfg.Sidebar.Side, v.panel.slide)
}

// toggleSidebarLocked opens or closes the panel and starts its animation.
func (v *View) toggleSidebarLocked() {
	v.panel.open = !v.panel.open
	v.win.SetTimer(panelStepTimerID, panelStepInterval)
	v.win.Invalidate()
}

// stepPanelLocked advances the slide one frame, and reports whether it moved.
// The timer is stopped once the slide reaches its target, so a settled panel
// costs nothing.
func (v *View) stepPanelLocked() bool {
	target := 0.0
	if v.panel.open {
		target = 1
	}
	if v.panel.slide == target {
		v.win.KillTimer(panelStepTimerID)
		return false
	}
	if v.panel.slide < target {
		v.panel.slide = math.Min(target, v.panel.slide+panelSlideStep)
	} else {
		v.panel.slide = math.Max(target, v.panel.slide-panelSlideStep)
	}
	// A sixth is not representable in binary, so six additions land just short
	// of one. Snapping keeps "fully open" an exact value the rest of the code
	// can compare against, instead of a range of near-one floats.
	if math.Abs(v.panel.slide-target) < 1e-9 {
		v.panel.slide = target
		v.win.KillTimer(panelStepTimerID)
	}
	return true
}

// closePanelLocked closes the panel without animating, which is what a window
// going away wants.
func (v *View) closePanelLocked() {
	v.panel.open = false
	v.panel.slide = 0
	v.win.KillTimer(panelStepTimerID)
}

// closeSidebarLocked closes the panel the way the user's own close button does:
// it animates shut rather than snapping.
//
// closePanelLocked is the opposite case: a window going away cannot wait for
// an animation, so the two are kept apart instead of one growing a flag.
func (v *View) closeSidebarLocked() {
	if !v.panel.open {
		return
	}
	v.panel.open = false
	v.win.SetTimer(panelStepTimerID, panelStepInterval)
	v.win.Invalidate()
}

// panelRowsLocked lists the sessions the panel shows, in creation order, with
// their geometry. The returned slice is scratch space owned by the view and is
// only valid until the next call.
//
// Rows are derived from the visible rectangle, not from the last paint, so the
// hit test and the drawn list are the same list by construction.
func (v *View) panelRowsLocked() []panelRow {
	v.panel.rows = v.panel.rows[:0]
	vis := v.panelVisibleRectLocked()
	if vis.Empty() || v.mgr == nil {
		return v.panel.rows
	}
	infos := v.mgr.List()
	pad := v.px(panelPad)
	rowH := v.px(panelRowHeight)
	gap := v.px(panelRowGap)
	killW := v.px(panelKillWidth)
	// A row narrower than its own padding and kill button has no room for a
	// title, so it is dropped rather than drawn as an empty band.
	if vis.W < 2*pad+killW {
		return v.panel.rows
	}
	y := vis.Y + v.px(panelHeaderHeight)
	limit := vis.Y + vis.H
	active := v.focusedSessionIDLocked()
	for _, info := range infos {
		if y+rowH > limit {
			break
		}
		row := panelRow{
			rect:   ui.Rect{X: vis.X + pad, Y: y, W: vis.W - 2*pad, H: rowH},
			info:   info,
			active: active != "" && info.ID == active,
		}
		row.kill = ui.Rect{X: row.rect.X + row.rect.W - killW, Y: y, W: killW, H: rowH}
		v.panel.rows = append(v.panel.rows, row)
		y += rowH + gap
	}
	return v.panel.rows
}

// focusedSessionIDLocked is the session the focused pane runs: the one the
// window is actually showing. It is the single answer to "which session is
// active", used both to mark the panel's row and to decide whether a row the
// user picked is already the one in front.
//
// A maximized pane covers its tab's other panes, so it is the one being looked
// at regardless of where the focus happens to be.
func (v *View) focusedSessionIDLocked() string {
	if v.active < 0 || v.active >= len(v.tabs) {
		return ""
	}
	t := v.tabs[v.active]
	p := t.focus
	if t.maximized != nil {
		p = t.maximized
	}
	if p == nil {
		return ""
	}
	return p.ID()
}

// panelRowHit resolves a point to the row under it and, when the point landed on
// that row's kill button, to the button.
func (v *View) panelRowHit(x, y int) (row panelRow, kill bool, ok bool) {
	for _, r := range v.panelRowsLocked() {
		if !r.rect.Contains(x, y) {
			continue
		}
		return r, r.kill.Contains(x, y), true
	}
	return panelRow{}, false, false
}

// panelClickLocked handles a click inside the panel, and reports whether it was
// consumed.
//
// The whole panel is consumed, even where it has neither a row nor a button: a
// click that fell through to the panes would start a selection under the panel,
// and the user cannot see what they are selecting.
func (v *View) panelClickLocked(x, y int) bool {
	vis := v.panelVisibleRectLocked()
	if !vis.Contains(x, y) {
		return false
	}
	// The header's close button is resolved before the rows, so its band is not
	// also read as the top of the list.
	if close := v.panelCloseRect(vis); !close.Empty() && close.Contains(x, y) {
		v.closeSidebarLocked()
		return true
	}
	row, kill, ok := v.panelRowHit(x, y)
	if ok && kill {
		v.killSessionLocked(row.info.ID)
		return true
	}
	// A row that was not killed selects its session: the user opened the list
	// to see what is running, so pointing at a name has to be the way to get to
	// it. A row already in front is a no-op rather than a relayout.
	if ok {
		v.revealSessionLocked(row.info.ID)
	}
	return true
}

// revealSessionLocked brings the pane that runs id into view and focuses it.
//
// The pane may live in a background tab or behind a maximized sibling, so
// showing it is not just a focus change: the tab is selected, the maximized
// pane steps aside, and the pane is given focus. Selecting a session that is
// already in front changes nothing, so clicking the active row does not
// reshuffle the layout under the user's cursor.
func (v *View) revealSessionLocked(id string) {
	if id == "" || id == v.focusedSessionIDLocked() {
		return
	}
	for i, t := range v.tabs {
		for _, p := range paneOrder(t.root) {
			if p.ID() != id {
				continue
			}
			v.active = i
			t.focus = p
			// A maximized pane hides the rest of its tab. Focusing a hidden
			// pane under it would leave the user looking at the wrong shell.
			if t.maximized != nil && t.maximized != p {
				t.maximized = nil
			}
			v.blinkOn = true
			v.layoutLocked()
			v.win.Invalidate()
			return
		}
	}
}

// killSessionLocked ends the shell a panel row stands for.
//
// The kill goes through the pane that owns the session, so the pane's own Close
// detaches the subscriber and cancels the reader before the process is reaped,
// and the layout is collapsed exactly as closing the pane with the keyboard
// would. A pane left drawing a shell that is gone would look like a hung
// terminal, and its row would be the only way back to it.
func (v *View) killSessionLocked(id string) {
	if id == "" || v.mgr == nil {
		return
	}
	for _, t := range v.tabs {
		for _, p := range paneOrder(t.root) {
			if p.ID() != id {
				continue
			}
			v.removePaneLocked(t, p)
			return
		}
	}
	// A session no pane owns is one the layout has already let go of; closing
	// it through the manager is what keeps it from outliving the window.
	_ = v.mgr.Close(id)
	v.win.Invalidate()
}

// paintPanel draws the panel: its body, the sessions it lists, and the kill
// button on each row.
func (v *View) paintPanel(s ui.Surface) {
	vis := v.panelVisibleRectLocked()
	if vis.Empty() {
		return
	}
	side := v.cfg.Sidebar.Side

	// The body is the window's darker surface and the rows sit on it in the
	// lighter one, so the list reads as a list rather than as more terminal.
	// It is drawn in UIBackground rather than UIBackgroundAlt because a theme
	// may set the latter to the terminal's own background (Dracula does), and
	// the panel would then be invisible against the pane it covers.
	s.Fill(vis.X, vis.Y, vis.W, vis.H, v.pal.UIBackground)
	// The edge is drawn on the side the panel faces, so it separates the panel
	// from the panes rather than from the window frame.
	ew := maxInt(1, v.px(panelEdgeWidth))
	if side == "right" {
		s.Fill(vis.X, vis.Y, ew, vis.H, v.pal.UIBorder)
	} else {
		s.Fill(vis.X+vis.W-ew, vis.Y, ew, vis.H, v.pal.UIBorder)
	}

	fm := s.SetFont(ui.FontUIBold)
	title := "Sessions"
	pad := v.px(panelPad)
	s.Text(vis.X+pad, vis.Y+(v.px(panelHeaderHeight)-fm.TextH())/2, title,
		ui.Style{FG: v.pal.UIForeground, BG: v.pal.UIBackground})

	// The pointer has to be read before the header is drawn, because the close
	// button highlights under it exactly as the kill buttons below do.
	hx, hy := -1, -1
	if mx, my := v.win.CursorPos(); my >= vis.Y && my < vis.Y+vis.H {
		hx, hy = v.win.ScreenToClient(mx, my)
	}

	// The panel's own close button, in the header's top-right corner. It is
	// drawn with the same fill-and-glyph the row kills use, so the two crosses
	// read as the same control: this one ends the panel, those end a session.
	if close := v.panelCloseRect(vis); !close.Empty() {
		bg := v.pal.UIBackground
		fg := v.pal.UIForegroundDim
		if hx >= close.X && hx < close.X+close.W && hy >= close.Y && hy < close.Y+close.H {
			bg = v.pal.UIAccent
			fg = v.pal.UIBackground
		}
		s.Fill(close.X+1, close.Y+2, close.W-2, close.H-4, bg)
		glyph := "\u2715"
		tw := s.TextWidth(glyph)
		s.Text(close.X+(close.W-tw)/2, close.Y+(close.H-fm.TextH())/2, glyph,
			ui.Style{FG: fg, BG: bg})
	}

	rows := v.panelRowsLocked()
	if len(rows) == 0 {
		msg := "No sessions"
		fm = s.SetFont(ui.FontUI)
		s.Text(vis.X+pad, vis.Y+v.px(panelHeaderHeight)+pad, msg,
			ui.Style{FG: v.pal.UIForegroundDim, BG: v.pal.UIBackground})
		return
	}

	// The pointer highlights the row it is over, so the list reads as something
	// the user points at rather than as a read-only report.
	for _, row := range rows {
		bg := v.pal.UIBackgroundAlt
		fg := v.pal.UIForeground
		// The row the window is showing is filled with the same lighter
		// surface the pointer uses, so the list answers "which one am I looking
		// at" without the user matching names against the tab strip. A row that
		// is both active and under the pointer keeps that fill: it is already
		// the one being shown, and flashing it would promise a change that a
		// click does not make.
		if row.active || (hx >= row.rect.X && hx < row.rect.X+row.rect.W &&
			hy >= row.rect.Y && hy < row.rect.Y+row.rect.H) {
			bg = v.pal.UIBorder
		}
		s.Fill(row.rect.X, row.rect.Y, row.rect.W, row.rect.H, bg)

		// A dead session is still listed: its exit code is the reason the user
		// opened the panel, and its kill button is what clears the row.
		status := "running"
		if row.info.Status != "running" {
			fg = v.pal.UIForegroundDim
			status = "exited"
		}
		// The stronger marker is a leading accent bar, the same one the active
		// tab puts on its leading edge, so a fill that a hover also uses cannot
		// be mistaken for the active state on its own.
		bw := 0
		if row.active {
			bw = v.px(2)
			s.Fill(row.rect.X, row.rect.Y, bw, row.rect.H, v.pal.UIAccent)
		}
		fm = s.SetFont(ui.FontUI)
		label := row.info.Name
		if label == "" {
			label = row.info.Profile
		}
		text := clipText(s, label+"  "+status, row.rect.W-v.px(panelKillWidth)-pad-bw)
		s.Text(row.rect.X+pad+bw, row.rect.Y+(row.rect.H-fm.TextH())/2, text,
			ui.Style{FG: fg, BG: bg})

		// The kill button is the row's own "x", drawn in the same accent the
		// rest of the chrome uses for the thing the pointer is aimed at.
		killBG := bg
		killFG := v.pal.UIForegroundDim
		if hx >= row.kill.X && hx < row.kill.X+row.kill.W && hy >= row.kill.Y && hy < row.kill.Y+row.kill.H {
			killBG = v.pal.UIAccent
			killFG = v.pal.UIBackground
		}
		s.Fill(row.kill.X+1, row.kill.Y+2, row.kill.W-2, row.kill.H-4, killBG)
		glyph := "\u2715"
		tw := s.TextWidth(glyph)
		s.Text(row.kill.X+(row.kill.W-tw)/2, row.kill.Y+(row.kill.H-fm.TextH())/2, glyph,
			ui.Style{FG: killFG, BG: killBG})
	}
}
