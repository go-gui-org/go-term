package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// batchBounds returns the axis-aligned bounds of the first batch drawn in c,
// and whether one was found. Batches carry a flat x,y triangle list.
func batchBounds(dc *gui.DrawContext, c gui.Color) (x0, y0, x1, y1 float32, ok bool) {
	for _, b := range dc.Batches() {
		if b.Color != c || len(b.Triangles) < 6 {
			continue
		}
		x0, y0 = b.Triangles[0], b.Triangles[1]
		x1, y1 = x0, y0
		for i := 0; i+1 < len(b.Triangles); i += 2 {
			x, y := b.Triangles[i], b.Triangles[i+1]
			x0, x1 = min(x0, x), max(x1, x)
			y0, y1 = min(y0, y), max(y1, y)
		}
		return x0, y0, x1, y1, true
	}
	return 0, 0, 0, 0, false
}

// TestDrawScrollbarProgress covers the OSC 9;4 fill: it runs down from the top
// of the track, is proportional to the reported percentage, picks a color per
// state, and — unlike the failure ticks — is drawn on the alt screen, because
// the jobs that report progress commonly run under a full-screen TUI.
func TestDrawScrollbarProgress(t *testing.T) {
	// 24 rows × 20px = 480px tall canvas.
	setup := func(state, value uint8, alt bool) (*Term, *gui.DrawContext) {
		tm, dc := newDrawTerm(24, 80, 10, 20)
		tm.grid.ProgressState, tm.grid.ProgressValue = state, value
		if alt {
			tm.grid.EnterAlt()
		}
		return tm, dc
	}

	idle := func(c gui.Color) gui.Color { c.A = scrollbarTickIdleAlpha; return c }

	t.Run("half_fills_top_half", func(t *testing.T) {
		tm, dc := setup(1, 50, false)
		tm.onDraw(dc)
		_, y0, _, y1, ok := batchBounds(dc, idle(tm.grid.ov.progress))
		if !ok {
			t.Fatal("no progress fill drawn")
		}
		if y0 != 0 {
			t.Errorf("fill starts at y=%v, want 0 (top of track)", y0)
		}
		if want := dc.Height / 2; y1 != want {
			t.Errorf("fill ends at y=%v, want %v", y1, want)
		}
	})

	t.Run("state_zero_draws_nothing", func(t *testing.T) {
		tm, dc := setup(0, 0, false)
		tm.onDraw(dc)
		if _, _, _, _, ok := batchBounds(dc, idle(tm.grid.ov.progress)); ok {
			t.Error("progress fill drawn while state is 0")
		}
	})

	t.Run("zero_percent_still_visible", func(t *testing.T) {
		// "Running, 0% done" is a real report; drawing nothing for it would
		// make a job that starts slowly look like a job that never started.
		tm, dc := setup(1, 0, false)
		tm.onDraw(dc)
		_, y0, _, y1, ok := batchBounds(dc, idle(tm.grid.ov.progress))
		if !ok {
			t.Fatal("no fill drawn for 0%")
		}
		if y1-y0 != scrollbarTickH {
			t.Errorf("0%% fill height = %v, want %v", y1-y0, scrollbarTickH)
		}
	})

	t.Run("error_state_uses_failure_color", func(t *testing.T) {
		tm, dc := setup(2, 30, false)
		tm.onDraw(dc)
		if _, _, _, _, ok := batchBounds(dc, idle(tm.grid.ov.fail)); !ok {
			t.Error("error progress not drawn in the failure color")
		}
	})

	t.Run("indeterminate_fills_whole_track", func(t *testing.T) {
		tm, dc := setup(3, 0, false)
		tm.onDraw(dc)
		_, y0, _, y1, ok := batchBounds(dc, idle(tm.grid.ov.indet))
		if !ok {
			t.Fatal("no indeterminate fill drawn")
		}
		if y0 != 0 || y1 != dc.Height {
			t.Errorf("indeterminate fill = y %v..%v, want 0..%v", y0, y1, dc.Height)
		}
	})

	t.Run("drawn_on_alt_screen", func(t *testing.T) {
		tm, dc := setup(1, 50, true)
		tm.onDraw(dc)
		if _, _, _, _, ok := batchBounds(dc, idle(tm.grid.ov.progress)); !ok {
			t.Error("progress suppressed on the alt screen")
		}
	})
}
