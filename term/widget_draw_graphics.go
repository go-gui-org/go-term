package term

import "github.com/mike-ward/go-gui/gui"

// drawGraphics paints decoded Sixel / Kitty / iTerm2 inline images on top of
// the background fill, under the cursor. Cells covered by an image are blanked
// at AddGraphic time so the text passes wrote nothing there. Each image's
// content-row origin maps to a viewport row via ContentRowToScreen; off-screen
// graphics are skipped.
//
// Called from onDraw while grid.Mu is held.
func (t *Term) drawGraphics(dc *gui.DrawContext, g *grid, rows int, renderYOff float32) {
	if len(g.Graphics) == 0 {
		return
	}
	for _, gr := range g.Graphics {
		// Defensive: skip degenerate graphics that somehow bypassed
		// parser-level validation.
		if gr.Rows <= 0 || gr.Cols <= 0 {
			continue
		}
		vr := g.ContentRowToScreen(gr.OriginR)
		// Skip only when the image rectangle has no overlap with the
		// viewport. A negative vr means the top is above the viewport;
		// dc.Image clips to the canvas so the visible portion renders.
		if vr >= rows || vr+gr.Rows <= 0 {
			continue
		}
		x := float32(gr.OriginC) * t.cellW
		y := float32(vr)*t.cellH + renderYOff
		w := float32(gr.Cols) * t.cellW
		h := float32(gr.Rows) * t.cellH
		dc.Image(x, y, w, h, gr.Src,
			gui.Opt[float32]{}, gui.Color{})
	}
}
