//go:build windows

package app

import "testing"

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
