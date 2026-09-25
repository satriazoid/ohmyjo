//go:build windows

package app

import (
	"testing"

	"ohmyjo/internal/ui"
)

// splitView builds a view holding one tab of two settled panes, laid out in
// r. The panes carry no session: what these tests read is geometry, which the
// layout assigns before any shell is involved.
func splitView(t *testing.T, axis Axis, r ui.Rect) (*View, *node) {
	t.Helper()
	a := NewPane(80, 24, ui.NewStyleResolver(ui.DefaultPalette()),
		ui.Metrics{CellW: 8, Ascent: 12, Descent: 3, LineH: 15}, 0)
	b := NewPane(80, 24, ui.NewStyleResolver(ui.DefaultPalette()),
		ui.Metrics{CellW: 8, Ascent: 12, Descent: 3, LineH: 15}, 0)
	root := &node{axis: axis, ratio: 0.5, a: &node{pane: a}, b: &node{pane: b}}
	v := &View{dpi: 96, metrics: ui.Metrics{CellW: 8, Ascent: 12, Descent: 3, LineH: 15}}
	v.tabs = []*Tab{{root: root, focus: a}}
	v.active = 0
	// layoutLocked marks the panes visible after laying them out; this walks
	// the same sequence, minus the window measurements layoutLocked needs.
	v.layoutNode(root, r)
	for _, p := range paneOrder(root) {
		p.grid.SetVisible(true)
	}
	return v, root
}

// TestSplitPanesAreSeparatedOnlyByABlankGap is the regression test for the bar
// the user asked to remove. Every pane used to be outlined and the splitter
// between them filled, so a split carried a band of chrome between the two
// panes, tens of pixels thick on a scaled display.
//
// The separation is now the gap alone: the panes stop either side of it and
// nothing else is reserved, so what shows through is the window background.
// Asserting the rectangles keeps this runnable without a live window, which a
// pixel comparison would need.
func TestSplitPanesAreSeparatedOnlyByABlankGap(t *testing.T) {
	area := ui.Rect{X: 0, Y: 43, W: 800, H: 600}
	for _, axis := range []Axis{SplitAlongX, SplitAlongY} {
		name := "side by side"
		if axis == SplitAlongY {
			name = "stacked"
		}
		t.Run(name, func(t *testing.T) {
			v, root := splitView(t, axis, area)

			gx, gy, gw, gh, ok := splitGap(root)
			if !ok {
				t.Fatal("a split of two panes left no gap between them")
			}
			// The gap is exactly the configured spacing: any wider and a pane
			// is losing pixels nobody asked it to give up, which is what an
			// inset per pane used to do on top of the gap itself.
			if axis == SplitAlongX {
				if gw != v.px(splitterWidth) {
					t.Fatalf("gap width = %d, want %d", gw, v.px(splitterWidth))
				}
				if gh != area.H {
					t.Fatalf("gap height = %d, want the pane area's %d", gh, area.H)
				}
			} else {
				if gh != v.px(splitterWidth) {
					t.Fatalf("gap height = %d, want %d", gh, v.px(splitterWidth))
				}
				if gw != area.W {
					t.Fatalf("gap width = %d, want the pane area's %d", gw, area.W)
				}
			}

			ax, ay, aw, ah := root.a.pane.grid.Bounds()
			bx, by, bw, bh := root.b.pane.grid.Bounds()

			// Each pane has to stop at the gap, with no margin of its own: a
			// band thicker than the gap is exactly the bar that was removed.
			if axis == SplitAlongX {
				if got := ax + aw; got != gx {
					t.Fatalf("first pane ends at x=%d, want the gap at x=%d", got, gx)
				}
				if bx != gx+gw {
					t.Fatalf("second pane starts at x=%d, want %d", bx, gx+gw)
				}
			} else {
				if got := ay + ah; got != gy {
					t.Fatalf("first pane ends at y=%d, want the gap at y=%d", got, gy)
				}
				if by != gy+gh {
					t.Fatalf("second pane starts at y=%d, want %d", by, gy+gh)
				}
			}
			// Both panes span the whole cross axis, so the gap is the only
			// break between them.
			if axis == SplitAlongX {
				if ay != area.Y || ah != area.H || by != area.Y || bh != area.H {
					t.Fatalf("panes do not span the pane area: a=%d..%d b=%d..%d",
						ay, ay+ah, by, by+bh)
				}
			} else {
				if ax != area.X || aw != area.W || bx != area.X || bw != area.W {
					t.Fatalf("panes do not span the pane area: a=%d..%d b=%d..%d",
						ax, ax+aw, bx, bx+bw)
				}
			}

			// Every pixel of the gap resizes the split. A band derived from the
			// ratio rather than the gap left its trailing edge inert, which is
			// invisible while the gap is painted and a dead zone once it is not.
			switch axis {
			case SplitAlongX:
				for x := gx; x < gx+gw; x++ {
					if splitterAt(root, x, gy+gh/2) != root {
						t.Fatalf("gap column x=%d does not resize the split", x)
					}
				}
			default:
				for y := gy; y < gy+gh; y++ {
					if splitterAt(root, gx+gw/2, y) != root {
						t.Fatalf("gap row y=%d does not resize the split", y)
					}
				}
			}

			// The gap centre is inside the split, so the hit test and the
			// layout agree on where the split is.
			cx, cy := gx+gw/2, gy+gh/2
			if !root.rect.Contains(cx, cy) {
				t.Fatalf("gap centre (%d,%d) is outside the split %+v", cx, cy, root.rect)
			}
			if got := splitterAt(root, cx, cy); got != root {
				t.Fatal("the gap between the panes is not a drag target")
			}
		})
	}
}
