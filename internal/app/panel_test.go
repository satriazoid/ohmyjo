//go:build windows

package app

import (
	"testing"

	"ohmyjo/internal/config"
	"ohmyjo/internal/ui"
)

// The panel's slide is the one piece of geometry that both draws the panel and
// hit-tests the clicks on it, so an error there is a panel whose rows are
// visible but not clickable, or worse, a click that lands on a pane hidden
// under the panel. These are the invariants that keep the two in step.
func TestPanelSlideRevealsFromTheInnerEdge(t *testing.T) {
	shell := panelShellRect("left", 260, 1600, 1000, 34)
	if shell.X != 0 || shell.Y != 34 || shell.W != 260 || shell.H != 966 {
		t.Fatalf("left shell = %+v, want x=0 y=34 w=260 h=966", shell)
	}
	right := panelShellRect("right", 260, 1600, 1000, 34)
	if right.X != 1340 || right.W != 260 || right.Y != 34 || right.H != 966 {
		t.Fatalf("right shell = %+v, want x=1340 w=260", right)
	}

	for _, tc := range []struct {
		name  string
		side  string
		slide float64
		want  int
		wantX int
	}{
		{"closed-left", "left", 0, 0, 0},
		{"half-left", "left", 0.5, 130, 0},
		{"open-left", "left", 1, 260, 0},
		// A right-anchored panel grows leftwards, so its left edge moves while
		// its right edge stays pinned to the window.
		{"half-right", "right", 0.5, 130, 1470},
		{"open-right", "right", 1, 260, 1340},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := panelShellRect(tc.side, 260, 1600, 1000, 34)
			vis := panelVisibleRect(base, tc.side, tc.slide)
			if vis.W != tc.want {
				t.Fatalf("slide %.2f revealed %d px, want %d", tc.slide, vis.W, tc.want)
			}
			if tc.want == 0 {
				if !vis.Empty() {
					t.Fatalf("a closed panel must cover nothing, got %+v", vis)
				}
				return
			}
			if vis.X != tc.wantX {
				t.Fatalf("revealed rect x = %d, want %d", vis.X, tc.wantX)
			}
			// The revealed strip must stay inside the panel it slides out of,
			// or a click would be accepted outside the drawn body.
			if vis.X < base.X || vis.X+vis.W > base.X+base.W {
				t.Fatalf("revealed rect %+v escapes its shell %+v", vis, base)
			}
			if vis.Y != base.Y || vis.H != base.H {
				t.Fatalf("revealed rect %+v does not span the shell's height", vis)
			}
		})
	}
}

// TestPanelDrawsNoDividerBetweenItAndThePanes pins the shape the user asked
// for: the panel's inner edge is left blank, and the hairlines it does draw sit
// against the window frame.
//
// The inner edge is the one place a line is always full height and, on a theme
// whose border contrasts with both surfaces, the loudest thing on the screen.
// It was drawn there and read as a bar wedged between the panel and the
// terminal. Asserting the rectangles rather than pixels keeps the check
// runnable without a live window, which the paint path needs.
func TestPanelDrawsNoDividerBetweenItAndThePanes(t *testing.T) {
	vis := ui.Rect{X: 0, Y: 43, W: 260, H: 707}
	for _, tc := range []struct {
		name string
		side string
	}{
		{"left", "left"},
		{"right", "right"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &View{dpi: 96, cfg: &config.Config{}}
			v.cfg.Sidebar.Side = tc.side
			edges := v.panelEdgeRects(vis)
			if len(edges) == 0 {
				t.Fatal("a visible panel must still carry an outline")
			}
			for _, e := range edges {
				if e.Empty() {
					t.Fatalf("side %s: empty edge %+v", tc.side, e)
				}
				// Every edge must lie inside the panel it outlines, or it would
				// draw over the pane behind it.
				if e.X < vis.X || e.Y < vis.Y ||
					e.X+e.W > vis.X+vis.W || e.Y+e.H > vis.Y+vis.H {
					t.Fatalf("side %s: edge %+v escapes the panel %+v", tc.side, e, vis)
				}
			}
			// The side the panel faces must carry nothing vertical: a bar
			// running the panel's height there is the divider the user saw.
			// The frame's top and bottom hairlines cross that column at one
			// corner cell each, which is a corner, not a divider.
			innerX := vis.X + vis.W - 1
			if tc.side == "right" {
				innerX = vis.X
			}
			for _, e := range edges {
				if e.H <= e.W {
					continue
				}
				if innerX >= e.X && innerX < e.X+e.W {
					t.Fatalf("side %s: vertical edge %+v draws a divider on the pane-facing column %d",
						tc.side, e, innerX)
				}
			}
		})
	}
}

// A panel wider than the window would leave the pane behind it no columns, so
// the shell is clamped to the client area.
func TestPanelShellClampsToClientWidth(t *testing.T) {
	got := panelShellRect("left", 900, 500, 400, 34)
	if got.W != 500 {
		t.Fatalf("shell width = %d, want the client width 500", got.W)
	}
	if got := panelShellRect("right", 900, 500, 400, 34); got.X != 0 || got.W != 500 {
		t.Fatalf("clamped right shell = %+v, want x=0 w=500", got)
	}
}

// The panel's close button is derived from the visible rectangle rather than
// stored, so the two things that can go wrong are worth pinning: it can be
// placed outside the panel it belongs to, and it can collide with the session
// rows below it. Either one produces a button that is drawn where it cannot be
// clicked, or a click in the header that ends a session instead of the panel.
func TestPanelCloseButtonStaysInTheHeader(t *testing.T) {
	for _, dpi := range []float64{96, 120, 144} {
		v := &View{dpi: dpi}
		for _, tc := range []struct {
			name string
			vis  ui.Rect
		}{
			{"left", ui.Rect{X: 0, Y: 34, W: 260, H: 966}},
			{"right", ui.Rect{X: 1340, Y: 34, W: 260, H: 966}},
			// Mid-slide the panel is its own narrow strip, and the button has
			// to stay inside whatever part of it is on screen.
			{"partly-revealed", ui.Rect{X: 0, Y: 34, W: 87, H: 966}},
			{"exactly-wide-enough", ui.Rect{X: 0, Y: 34, W: 46, H: 966}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := v.panelCloseRect(tc.vis)
				if got.Empty() {
					// Only permitted when the panel is genuinely too narrow to
					// hold the button and its padding.
					if tc.vis.W >= 2*v.px(panelPad)+v.px(panelCloseWidth) {
						t.Fatalf("no close button in a %d px panel", tc.vis.W)
					}
					return
				}
				if got.X < tc.vis.X || got.X+got.W > tc.vis.X+tc.vis.W {
					t.Fatalf("close button %+v escapes the panel %+v", got, tc.vis)
				}
				// The user asked for the corner: trailing edge, not leading.
				// Anchoring to the wrong side still leaves the button inside
				// the panel, so nothing else here would catch it.
				if want := tc.vis.X + tc.vis.W - v.px(panelPad); got.X+got.W != want {
					t.Fatalf("close button ends at %d, want the panel's trailing pad %d",
						got.X+got.W, want)
				}
				if got.Y < tc.vis.Y || got.Y+got.H > tc.vis.Y+v.px(panelHeaderHeight) {
					t.Fatalf("close button %+v is not inside the %d px header",
						got, v.px(panelHeaderHeight))
				}
				// The rows start below the header, so a button that reaches
				// past it would sit on top of the first session's kill target.
				if got.Y+got.H > tc.vis.Y+v.px(panelHeaderHeight) {
					t.Fatalf("close button %+v overlaps the session rows", got)
				}
			})
		}
	}
}

// A panel too narrow to hold the button and its padding has to report no button
// rather than a rectangle with a negative width, which would otherwise be
// hit-tested as a wide target.
func TestPanelCloseButtonVanishesWhenTooNarrow(t *testing.T) {
	v := &View{dpi: 96}
	got := v.panelCloseRect(ui.Rect{X: 0, Y: 34, W: 20, H: 400})
	if !got.Empty() {
		t.Fatalf("close button %+v was placed in a 20 px panel", got)
	}
}

// The panel's toggle on the strip is the only mouse route to the panel; the
// chord can hide it too, so losing the button loses the panel. It also has to
// stay clear of the three things that share its row: the tab bar's toggle, the
// window's own buttons, and the buttons that flow after the tabs.
func TestStripPanelToggleIsReachableAndClear(t *testing.T) {
	for _, dpi := range []float64{96, 120, 144} {
		v := &View{dpi: dpi}
		for _, cw := range []int{1400, 1618, 1920} {
			buttons := v.stripButtonsAt(cw)
			byAction := map[string]stripButton{}
			for _, b := range buttons {
				byAction[b.action] = b
			}
			panel, ok := byAction["toggleSidebar"]
			if !ok {
				t.Fatalf("dpi %v cw %d: no panel toggle in %v", dpi, cw, buttons)
			}
			bar := byAction["toggleTabBar"]
			// It must be on the row, not off the leading or trailing edge.
			if panel.x < 0 || panel.x+v.px(panel.width) > cw {
				t.Fatalf("dpi %v cw %d: panel toggle at %d+%d is off the row",
					dpi, cw, panel.x, v.px(panel.width))
			}
			// The two toggles must not overlap, or one silently eats the other.
			if panel.x+v.px(panel.width) > bar.x {
				t.Fatalf("dpi %v cw %d: panel toggle ends at %d, over the tab bar's at %d",
					dpi, cw, panel.x+v.px(panel.width), bar.x)
			}
			// And neither may be drawn under the window's own buttons.
			windowButtonsX := cw - 3*v.px(controlWidth)
			if bar.x+v.px(bar.width) > windowButtonsX {
				t.Fatalf("dpi %v cw %d: tab bar toggle ends at %d, over the window buttons at %d",
					dpi, cw, bar.x+v.px(bar.width), windowButtonsX)
			}
		}
	}
}

// The buttons that flow after the tabs are the one part of the row whose width
// depends on the titles, so a long title is what would grow over the pinned
// toggles. The limit they stop at is the panel toggle's left edge, not the tab
// bar's: the panel's sits to its left, and a limit of the tab bar's would let
// the flow draw over it.
func TestStripFlowStopsBeforeThePinnedToggles(t *testing.T) {
	for _, dpi := range []float64{96, 120, 144} {
		v := &View{dpi: dpi}
		for _, cw := range []int{1400, 1618, 1920} {
			limit, panelX, present := v.stripFlowLimitAt(cw)
			if !present {
				continue
			}
			if limit != panelX {
				t.Fatalf("dpi %v cw %d: flow stops at %d, want the panel toggle's %d",
					dpi, cw, limit, panelX)
			}
			// Dropping the panel toggle must not stop the flow at the tab bar
			// toggle instead, or the flow would be laid out under a button that
			// is no longer there.
			narrowLimit, _, narrowPresent := v.stripFlowLimitAt(v.stripCollapsedWide() - 1)
			if narrowPresent {
				t.Fatal("a window too narrow for both toggles still placed the panel's")
			}
			if narrowLimit != v.tabBarButtonXAt(v.stripCollapsedWide()-1) {
				t.Fatalf("dpi %v cw %d: without the panel toggle the flow stops at %d, want the tab bar toggle's %d",
					dpi, cw, narrowLimit, v.tabBarButtonXAt(v.stripCollapsedWide()-1))
			}
		}
	}
}

// A window too narrow for both toggles keeps the tab bar's, which is the one
// that restores the row's own affordances, and drops the panel's rather than
// drawing it over the window's buttons.
func TestStripDropsPanelToggleRatherThanOverlapping(t *testing.T) {
	v := &View{dpi: 96}
	narrow := v.stripCollapsedWide() - 1
	for _, b := range v.stripButtonsAt(narrow) {
		if b.action == "toggleSidebar" {
			t.Fatalf("panel toggle %+v was laid out in a %d px window", b, narrow)
		}
		if b.x+b.width > narrow-3*v.px(controlWidth) {
			t.Fatalf("button %q ends at %d, over the window buttons", b.action, b.x+b.width)
		}
	}
	// The tab bar's toggle is the one that must survive.
	found := false
	for _, b := range v.stripButtonsAt(narrow) {
		found = found || b.action == "toggleTabBar"
	}
	if !found {
		t.Fatal("row too narrow for the panel toggle dropped the tab bar's as well")
	}
}

// Hiding the tab bar puts the tab titles away. It must not take the panel with
// them: the panel has its own toggle on the same row, and a hide that also
// hides something else is what made the panel look unopenable.
func TestTogglingTheTabBarLeavesThePanelAlone(t *testing.T) {
	// A zero window is inert: it has no handle to invalidate and no tabs are
	// open, so the layout has nothing to reflow. The toggle itself still runs.
	v := &View{dpi: 120, win: &ui.Window{}, panel: panelState{open: true, slide: 1}}
	v.toggleTabBarLocked()
	if !v.panel.open || v.panel.slide != 1 {
		t.Fatalf("hiding the tab bar left the panel at open=%v slide=%v, want it untouched",
			v.panel.open, v.panel.slide)
	}
	if !v.tabBarHidden {
		t.Fatal("the toggle did not hide the tab bar")
	}
	v.toggleTabBarLocked()
	if !v.panel.open || v.panel.slide != 1 {
		t.Fatalf("restoring the tab bar left the panel at open=%v slide=%v, want it untouched",
			v.panel.open, v.panel.slide)
	}
	if v.tabBarHidden {
		t.Fatal("the toggle did not restore the tab bar")
	}
}

// The marker says which state the button will leave behind, so a panel that is
// up and one that is away do not look like the same button.
func TestPanelToggleMarkerFollowsThePanel(t *testing.T) {
	v := &View{dpi: 96}
	away := v.panelIcon()
	v.panel.open = true
	up := v.panelIcon()
	if away == up || away == "" || up == "" {
		t.Fatalf("marker is %q with the panel away and %q with it up, want two distinct markers", away, up)
	}
}

// The panel's rows are built from the session registry, so a session that is
// ended must be taken out of that registry. Otherwise a row the user kills
// stays in the list for the rest of the run, which reads as a kill button that
// does nothing. The pane therefore ends its session through its owner rather
// than through the handle it holds, and says so exactly once.
func TestClosingAPaneReleasesItsSessionOnce(t *testing.T) {
	p := NewPane(80, 24, ui.NewStyleResolver(ui.DefaultPalette()),
		ui.Metrics{CellW: 8, Ascent: 12, Descent: 3, LineH: 15}, 0)
	fake := &fakePaneSession{}
	released := 0
	p.sess, p.id, p.release = fake, "s1", func() { released++ }

	p.Close()
	if released != 1 {
		t.Fatalf("Close released the session %d times, want 1", released)
	}
	if p.sess != nil || p.release != nil {
		t.Fatalf("Close left the pane attached: sess=%v release=%v", p.sess, p.release != nil)
	}
	// A second Close is what a layout pass that ran twice would do, and it must
	// not end a session id that has already been handed back.
	p.Close()
	if released != 1 {
		t.Fatalf("a second Close released the session again (%d releases)", released)
	}
}

// twoTabView builds a view with one pane per tab, each running the named
// session. The panes are not attached to a real shell: what the panel needs to
// know is which session each pane stands for, and that is the pane's id.
func twoTabView(ids ...string) *View {
	v := &View{dpi: 96, win: &ui.Window{}}
	for _, id := range ids {
		p := NewPane(80, 24, ui.NewStyleResolver(ui.DefaultPalette()),
			ui.Metrics{CellW: 8, Ascent: 12, Descent: 3, LineH: 15}, 0)
		p.id = id
		v.tabs = append(v.tabs, &Tab{root: &node{pane: p}, focus: p})
	}
	if len(v.tabs) > 0 {
		v.active = 0
	}
	return v
}

// The row the user is looking at has to be the marked one, or a list of several
// shells is a list of names with no answer to "which one is in front", which
// is what made the panel useless after a split.
func TestPanelMarksTheSessionInFront(t *testing.T) {
	v := twoTabView("s1", "s2")
	if got := v.focusedSessionIDLocked(); got != "s1" {
		t.Fatalf("focused session = %q, want s1 for the first tab", got)
	}
	v.active = 1
	if got := v.focusedSessionIDLocked(); got != "s2" {
		t.Fatalf("focused session = %q, want s2 after switching tabs", got)
	}
}

// A maximized pane covers its tab, so it is the session being looked at even
// when the focus sits on a pane hidden underneath it. Marking the hidden one
// would point the user at a shell they cannot see.
func TestMaximizedPaneIsTheSessionInFront(t *testing.T) {
	v := twoTabView("s1", "s2")
	tab := v.tabs[0]
	hidden := NewPane(80, 24, ui.NewStyleResolver(ui.DefaultPalette()),
		ui.Metrics{CellW: 8, Ascent: 12, Descent: 3, LineH: 15}, 0)
	hidden.id = "s2"
	tab.root = &node{axis: SplitAlongX, ratio: 0.5, a: &node{pane: tab.focus}, b: &node{pane: hidden}}
	tab.maximized = hidden

	if got := v.focusedSessionIDLocked(); got != "s2" {
		t.Fatalf("focused session = %q, want the maximized pane's s2", got)
	}
}

// Picking a row is how the user gets to a session they can see in the list. The
// session may live in a background tab, so the tab has to come forward with it;
// a row that only moved the focus would leave the user looking at the old shell.
func TestPickingARowBringsItsSessionForward(t *testing.T) {
	v := twoTabView("s1", "s2")
	hidden := NewPane(80, 24, ui.NewStyleResolver(ui.DefaultPalette()),
		ui.Metrics{CellW: 8, Ascent: 12, Descent: 3, LineH: 15}, 0)
	hidden.id = "s3"
	sibling := v.tabs[1].focus
	v.tabs[1].root = &node{axis: SplitAlongX, ratio: 0.5, a: &node{pane: sibling}, b: &node{pane: hidden}}

	v.revealSessionLocked("s3")
	if v.active != 1 {
		t.Fatalf("active tab = %d, want tab 1 to come forward with s3", v.active)
	}
	if got := v.tabs[1].focus; got == nil || got.ID() != "s3" {
		t.Fatalf("focus = %v, want the s3 pane", got)
	}
	if got := v.focusedSessionIDLocked(); got != "s3" {
		t.Fatalf("focused session = %q, want s3", got)
	}
}

// A maximized pane hides its tab's other panes, so bringing a hidden one
// forward without un-maximizing would leave the user staring at the same shell
// they were already on while the panel claims the selection moved.
func TestPickingAHiddenRowUnmaximizes(t *testing.T) {
	v := twoTabView("s1", "s2")
	tab := v.tabs[0]
	under := NewPane(80, 24, ui.NewStyleResolver(ui.DefaultPalette()),
		ui.Metrics{CellW: 8, Ascent: 12, Descent: 3, LineH: 15}, 0)
	under.id = "s2"
	tab.root = &node{axis: SplitAlongX, ratio: 0.5, a: &node{pane: tab.focus}, b: &node{pane: under}}
	tab.maximized = tab.focus

	v.revealSessionLocked("s2")
	if tab.maximized != nil {
		t.Fatalf("maximized pane survived a pick of the pane it covered: %v", tab.maximized)
	}
	if got := v.focusedSessionIDLocked(); got != "s2" {
		t.Fatalf("focused session = %q, want s2", got)
	}
}

// Picking the row that is already in front must change nothing: the user is
// checking which shell is active as often as they are switching, and a click
// that reflowed the layout would make the panel's own rows jump under the
// pointer.
func TestPickingTheRowAlreadyInFrontChangesNothing(t *testing.T) {
	v := twoTabView("s1", "s2")
	before := v.active
	v.revealSessionLocked("s1")
	if v.active != before {
		t.Fatalf("active tab moved from %d to %d with no change of session", before, v.active)
	}
	// An id no pane owns is the state between a session closing and the layout
	// letting go of it. It must not move the focus to nothing.
	v.revealSessionLocked("s9")
	if v.active != before || v.focusedSessionIDLocked() != "s1" {
		t.Fatalf("picking an unknown id moved the view to tab %d session %q",
			v.active, v.focusedSessionIDLocked())
	}
	v.revealSessionLocked("")
	if v.active != before || v.focusedSessionIDLocked() != "s1" {
		t.Fatal("an empty id moved the view")
	}
}

// The user's own concern: ending the tab or the session that is in front must
// leave the list with a valid one marked, not with the marker on a tab that is
// gone or on nothing at all.
func TestClosingTheActiveTabLeavesAValidSessionInFront(t *testing.T) {
	v := twoTabView("s1", "s2", "s3")
	v.active = 2
	v.closeTabLocked(2)
	if v.active < 0 || v.active >= len(v.tabs) {
		t.Fatalf("active tab = %d with %d tabs left", v.active, len(v.tabs))
	}
	if got := v.focusedSessionIDLocked(); got != "s2" {
		t.Fatalf("focused session = %q, want the tab that moved up, s2", got)
	}
	// Closing a tab before the active one has to shift the index with it, or
	// the marked session silently changes to a neighbour.
	v2 := twoTabView("s1", "s2", "s3")
	v2.active = 1
	v2.closeTabLocked(0)
	if got := v2.focusedSessionIDLocked(); got != "s2" {
		t.Fatalf("focused session = %q, want s2 to stay in front", got)
	}
}

// A tab whose panes have all gone is dropped, which is what the panel's kill
// button does to a single-pane tab. The view must end up on a tab that still
// has a pane, because the marker and the keyboard focus are read from it every
// frame.
func TestKillingTheActiveSessionLeavesAValidOneInFront(t *testing.T) {
	v := twoTabView("s1", "s2")
	v.mgr = nil
	v.active = 0
	v.removePaneLocked(v.tabs[0], v.tabs[0].focus)
	if len(v.tabs) != 1 {
		t.Fatalf("tabs left = %d, want 1", len(v.tabs))
	}
	if got := v.focusedSessionIDLocked(); got != "s2" {
		t.Fatalf("focused session = %q, want s2 after s1 was killed", got)
	}
}

// Killing a background shell from the panel must not move the keyboard off the
// shell the user is typing into. The pane that had the focus is still in the
// tree, so it keeps it; only losing the focused pane itself hands the focus on.
func TestKillingABackgroundPaneKeepsTheFocus(t *testing.T) {
	v := &View{dpi: 96, win: &ui.Window{}}
	front := NewPane(80, 24, ui.NewStyleResolver(ui.DefaultPalette()),
		ui.Metrics{CellW: 8, Ascent: 12, Descent: 3, LineH: 15}, 0)
	front.id = "s1"
	back := NewPane(80, 24, ui.NewStyleResolver(ui.DefaultPalette()),
		ui.Metrics{CellW: 8, Ascent: 12, Descent: 3, LineH: 15}, 0)
	back.id = "s2"
	tab := &Tab{
		root:  &node{axis: SplitAlongX, ratio: 0.5, a: &node{pane: front}, b: &node{pane: back}},
		focus: front,
	}
	v.tabs = []*Tab{tab}
	v.active = 0

	if !v.removePaneLocked(tab, back) {
		t.Fatal("the split did not collapse")
	}
	if tab.focus != front {
		t.Fatalf("killing the background pane moved the focus to %v", tab.focus)
	}
	if got := v.focusedSessionIDLocked(); got != "s1" {
		t.Fatalf("focused session = %q, want s1 to stay in front", got)
	}
	// Killing the focused pane of a split is the other half: the focus has to
	// move to the pane that is left, or the tab would keep a pane that is gone.
	back2 := NewPane(80, 24, ui.NewStyleResolver(ui.DefaultPalette()),
		ui.Metrics{CellW: 8, Ascent: 12, Descent: 3, LineH: 15}, 0)
	back2.id = "s3"
	tab.root = &node{axis: SplitAlongX, ratio: 0.5, a: &node{pane: front}, b: &node{pane: back2}}
	tab.focus = front
	if !v.removePaneLocked(tab, front) {
		t.Fatal("the focused pane was not removed")
	}
	if tab.focus == front || tab.focus == nil {
		t.Fatalf("focus after the focused pane went = %v, want the remaining pane", tab.focus)
	}
	if got := v.focusedSessionIDLocked(); got != "s3" {
		t.Fatalf("focused session = %q, want s3 to inherit the focus", got)
	}
	// And taking away the only pane left closes the tab, which is where the
	// session that was in front goes with it.
	v.removePaneLocked(tab, tab.focus)
	if len(v.tabs) != 0 {
		t.Fatalf("tabs left = %d, want the last pane's tab to close", len(v.tabs))
	}
}
