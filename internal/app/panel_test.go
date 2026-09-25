//go:build windows

package app

import (
	"testing"

	"ohmyjo/internal/ui"
)

// The panel's slide is the one piece of geometry that both draws the panel and
// hit-tests the clicks on it, so an error there is a panel whose rows are
// visible but not clickable — or worse, a click that lands on a pane hidden
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
