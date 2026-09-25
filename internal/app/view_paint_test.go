//go:build windows

package app

import (
	"testing"

	"ohmyjo/internal/ui"
)

// splitPaintedView builds a view holding one tab of two painted panes over an
// offscreen surface, so the frame the user sees can be asserted pixel by pixel
// without a window. The panes carry no session: the terminal grid paints its
// own background, which is all these assertions read around.
func splitPaintedView(t *testing.T, axis Axis, area ui.Rect) (*View, *node, *ui.GDI, func()) {
	t.Helper()
	surf, err := ui.NewGDI(area.W, area.H)
	if err != nil {
		t.Skipf("no drawing surface available: %v", err)
	}
	if !surf.Clip(0, 0, area.W, area.H) {
		surf.Close()
		t.Skip("surface has no usable area")
	}
	surf.ConfigureFont(ui.FontMono, ui.FontSlot{Family: "Consolas", SizePx: 12, LineH: 1.0})
	surf.ConfigureFont(ui.FontUI, ui.FontSlot{Family: "Segoe UI", SizePx: 12, LineH: 1.0})
	if m := surf.SetFont(ui.FontMono); m.CellW <= 0 || m.LineH <= 0 {
		surf.Close()
		t.Skip("no usable font metrics")
	}

	v, root := splitView(t, axis, area)
	v.pal = ui.DefaultPalette()
	return v, root, surf, surf.Close
}

// paintSplit paints the split's panes and the drag highlight the way Paint
// does, minus the tab strip and the panel, which need a window to measure.
func paintSplit(v *View, surf *ui.GDI) {
	surf.FillAll(v.pal.UIBackground)
	t := v.tabs[v.active]
	for _, p := range paneOrder(t.root) {
		p.grid.SetFocused(p == t.focus)
		p.Paint(surf, v.pal, false)
	}
	if v.drag != nil {
		v.paintSplitHighlight(surf, t.root)
	}
}

// TestSplitGapIsBlankUntilDragged is the pixel-level half of the regression:
// the gap between two panes shows the window background, so a split reads as
// two panels with space between them. The user's complaint was a bar drawn
// there, so the assertion is that no non-background colour appears in the gap
// at all, not merely that some particular divider colour is gone.
func TestSplitGapIsBlankUntilDragged(t *testing.T) {
	area := ui.Rect{X: 0, Y: 0, W: 400, H: 300}
	for _, axis := range []Axis{SplitAlongX, SplitAlongY} {
		name := "side by side"
		if axis == SplitAlongY {
			name = "stacked"
		}
		t.Run(name, func(t *testing.T) {
			v, root, surf, done := splitPaintedView(t, axis, area)
			defer done()

			paintSplit(v, surf)

			gx, gy, gw, gh, ok := splitGap(root)
			if !ok {
				t.Fatal("a split of two panes left no gap between them")
			}
			bg := v.pal.UIBackground
			// The panes must actually be on the surface, or every pixel below
			// would read as background and the assertions would be vacuous. A
			// pane paints its own background, which is a different colour from
			// the window's, so finding one inside the pane proves it painted.
			if v.pal.Background == bg {
				t.Fatal("palette has one background for both the window and the panes, " +
					"so a painted pane cannot be told from an unpainted one")
			}
			ax, ay, aw, ah := root.a.pane.grid.Bounds()
			painted := false
			for y := ay; y < ay+ah && !painted; y++ {
				for x := ax; x < ax+aw; x++ {
					if surf.At(x, y) == v.pal.Background {
						painted = true
						break
					}
				}
			}
			if !painted {
				t.Fatal("the first pane painted nothing, so this test proves nothing")
			}
			for y := gy; y < gy+gh; y++ {
				for x := gx; x < gx+gw; x++ {
					if got := surf.At(x, y); got != bg {
						t.Fatalf("gap pixel (%d,%d) = %08X, want the background %08X: "+
							"something is drawn between the panes", x, y, got, bg)
					}
				}
			}

			// While a drag is in flight the gap is highlighted: it is the only
			// resize affordance left once no bar marks where the gap is.
			extent := gw
			if axis == SplitAlongY {
				extent = gh
			}
			v.drag = &splitDrag{n: root, axis: axis, extent: extent}
			paintSplit(v, surf)
			// The whole gap lights up, not a sliver of it: the highlight is
			// what tells the user which edge is moving.
			for y := gy; y < gy+gh; y++ {
				for x := gx; x < gx+gw; x++ {
					if got := surf.At(x, y); got != v.pal.UIAccent {
						t.Fatalf("gap pixel (%d,%d) during a drag = %08X, want the accent %08X",
							x, y, got, v.pal.UIAccent)
					}
				}
			}

			// And it goes away again: a bar that stayed on screen after the
			// mouse was released would be the original complaint.
			v.drag = nil
			paintSplit(v, surf)
			if got := surf.At(gx+gw/2, gy+gh/2); got != bg {
				t.Fatalf("gap pixel after the drag = %08X, want the background %08X", got, bg)
			}
		})
	}
}
