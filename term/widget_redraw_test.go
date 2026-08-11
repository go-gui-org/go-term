package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// Why applyChunk asks for UpdateWindow (a full layout refresh) rather than the
// cheaper RequestRedraw (render-only) after every screen-changing PTY read.
//
// The terminal repaints by bumping drawVersion, which reaches go-gui as the
// DrawCanvas Version — the canvas tessellation cache re-invokes OnDraw only when
// that value differs from the cached one. But Version is carried on the *layout
// node*, and only view generation rebuilds those. A render-only refresh reuses
// the existing tree, so the canvas still advertises the old Version, the cache
// hits, and OnDraw never runs: the grid would change with nothing repainting it.
//
// This test pins that asymmetry against go-gui, because the swap is an obvious
// optimization to reach for (a terminal's layout does not change between
// keystrokes) and it fails by freezing the screen rather than by not compiling.
// If this ever starts failing, go-gui grew a way to invalidate a canvas from a
// render-only pass and the cheaper path is worth revisiting.
func TestRenderOnlyRefreshSkipsCanvasVersionBump(t *testing.T) {
	var version uint64 = 1
	draws := 0
	w := gui.NewWindow(gui.WindowCfg{Title: "redraw-probe", Width: 400, Height: 300})
	w.UpdateView(func(*gui.Window) gui.View {
		return gui.DrawCanvas(gui.DrawCanvasCfg{
			ID:      "redraw-probe-canvas",
			Version: version,
			Width:   200,
			Height:  100,
			OnDraw:  func(*gui.DrawContext) { draws++ },
		})
	})

	w.FrameFn()
	if draws != 1 {
		t.Fatalf("initial frame: draws = %d, want 1", draws)
	}

	// What RequestRedraw would buy — and why it cannot be used here.
	version++
	w.RequestRedraw()
	w.FrameFn()
	if draws != 1 {
		t.Fatalf("render-only refresh: draws = %d, want 1 (see comment above)", draws)
	}

	// What applyChunk actually does.
	version++
	w.UpdateWindow()
	w.FrameFn()
	if draws != 2 {
		t.Fatalf("full refresh: draws = %d, want 2", draws)
	}
}
