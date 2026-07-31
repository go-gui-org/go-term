package term

import "github.com/go-gui-org/go-gui/gui"

// drawGraphics paints decoded Sixel / Kitty / iTerm2 inline images on top of
// the background fill, under the cursor. Cells covered by an image are blanked
// at AddGraphic time so the text passes wrote nothing there. Each image's
// content-row origin maps to a viewport row via ContentRowToScreen; off-screen
// graphics are skipped.
//
// Called from onDraw while grid.Mu is held.
// top is the first paintable viewport row (see drawState.renderTop): an image
// lying entirely in the rows a status bar reserved is skipped. One straddling
// the boundary still paints across it — images are drawn whole, and clipping
// them per row is not worth the code for an overlay the user dismisses with a
// keystroke.
func (t *Term) drawGraphics(dc *gui.DrawContext, g *grid, top, rows int, renderYOff float32) {
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
		if vr >= rows || vr+gr.Rows <= top {
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

// placeholderRect is a run of Kitty Unicode-placeholder cells that all belong
// to one virtual placement and form a solid rectangle on screen: screen rows
// [sr0, sr1] × columns [sc0, sc1], showing the placement's tiles starting at
// (dr0, dc0). Because the run is solid and contiguous in both coordinate
// spaces, the whole rectangle is one blit.
// maxPlaceholderRects bounds the per-frame placeholder scan. Coalescing means
// a real image costs one rectangle (a partly-occluded one a handful), so this
// is unreachable by legitimate output; what it stops is a screen of
// deliberately non-coalescing placeholders — alternating image ids, scrambled
// tile coordinates — turning one frame into tens of thousands of draw calls
// and a scratch buffer that never shrinks.
const maxPlaceholderRects = 4096

type placeholderRect struct {
	imageID     uint32
	placementID uint32
	sr0, sr1    int
	sc0, sc1    int
	dr0, dc0    int
}

// drawPlaceholders paints Kitty virtual placements (U=1). Unlike an ordinary
// placement, a virtual one owns no cells: the application prints U+10EEEE
// placeholder cells whose colors name the image and whose combining
// diacritics name the tile, and the image appears wherever those cells are.
// That indirection is the whole point — a host application that knows nothing
// about graphics (tmux, vim, yazi's own layout) moves the image simply by
// moving text.
//
// So this pass scans the visible cells rather than a list of rectangles: runs
// of contiguous tiles are coalesced into the largest rectangles they form —
// the common case, a full block of placeholders, coalesces to exactly one —
// and each rectangle is drawn by mapping the *whole* image onto the
// placement's rectangle and letting the clip decide which part shows.
//
// Called from onDraw while grid.Mu is held.
func (t *Term) drawPlaceholders(ds *drawState) {
	g := ds.g
	if len(g.virtualImages) == 0 || t.cellW <= 0 || t.cellH <= 0 {
		return
	}
	rects := t.collectPlaceholderRects(ds)
	for i := range rects {
		t.blitPlaceholderRect(ds, &rects[i])
	}
}

// collectPlaceholderRects decodes every placeholder cell in the paintable
// viewport and coalesces them into rectangles, first across a row and then
// down. The returned slice aliases the per-Term scratch buffer.
func (t *Term) collectPlaceholderRects(ds *drawState) []placeholderRect {
	g := ds.g
	rects := t.draw.phRects[:0]

	// mergeFrom is the first index that can hold a rectangle whose bottom edge
	// is the row just scanned — the previous row's own runs, plus any taller
	// rectangle those runs were merged into. Rectangles below that index have
	// stopped growing and are never revisited.
	mergeFrom, mergeTo := 0, 0

	// One-entry memo for the virtual-store lookup: a rectangle's worth of
	// cells all name the same image, so this turns a per-cell map lookup into
	// a per-run one. memoID 0 means "memo empty" — a decoded image id is never
	// 0, kgpImageIDLow24 rejects it — so a *miss* is memoised too and a screen
	// of placeholders naming an unknown image costs one lookup, not one per
	// cell.
	var (
		memoID    uint32
		memoImg   virtualImage
		memoFound bool
	)

	// capped stops the scan once maxPlaceholderRects is reached; the rows
	// already collected still draw.
	var capped bool

	for r := ds.renderTop; r < ds.renderRows && !capped; r++ {
		rowStart := len(rects)
		var prev placeholderCell
		for c := range ds.cols {
			cell := ds.rawCell(r, c)
			if !isPlaceholderCell(cell) {
				prev = placeholderCell{}
				continue
			}
			// A bare placeholder (no diacritics) is a single-rune cell, so
			// skip cellText's rune-cache lookup for it — that is the majority
			// of cells in a large image.
			text := kgpPlaceholderStr
			if cell.clusterID != 0 {
				text = t.cellText(cell)
			}
			pc, ok := decodePlaceholder(text, cell.FG, cell.ULColor, prev)
			if !ok {
				prev = placeholderCell{}
				continue
			}
			prev = pc
			if pc.imageID != memoID {
				memoImg, memoFound = g.virtualImages[pc.imageID]
				memoID = pc.imageID
			}
			// An unknown image, or a tile outside the placement rectangle,
			// draws nothing. The spec is explicit that printing more
			// placeholder cells than the placement has rows/columns shows only
			// the part that fits. The run still continues through such a cell:
			// its decode remains the inheritance base for the cell to its
			// right, which is what the columns beyond the image expect.
			if !memoFound || pc.row >= memoImg.Rows || pc.col >= memoImg.Cols {
				continue
			}
			// Extend the run to the left when this tile continues it in both
			// coordinate spaces; otherwise start a new one.
			if n := len(rects); n > rowStart {
				last := &rects[n-1]
				if last.imageID == pc.imageID && last.placementID == pc.placementID &&
					last.sc1+1 == c && last.dr0 == pc.row &&
					last.dc0+(last.sc1-last.sc0)+1 == pc.col {
					last.sc1 = c
					continue
				}
			}
			if len(rects) >= maxPlaceholderRects {
				capped = true
				break
			}
			rects = append(rects, placeholderRect{
				imageID: pc.imageID, placementID: pc.placementID,
				sr0: r, sr1: r, sc0: c, sc1: c,
				dr0: pc.row, dc0: pc.col,
			})
		}

		// Merge this row's runs into the rectangles directly above them: same
		// columns, same image, next tile row down. Both ranges are in
		// ascending column order, so a linear walk finds every pair.
		mergedMin := rowStart
		if mergeTo > mergeFrom {
			rects, mergedMin = mergePlaceholderRows(rects, mergeFrom, mergeTo, rowStart, r)
		}
		mergeFrom, mergeTo = min(mergedMin, rowStart), len(rects)
	}
	t.draw.phRects = rects
	return rects
}

// mergePlaceholderRows folds the runs in rects[curStart:] into the rectangles
// in rects[prevStart:prevEnd] where one sits directly below the other — same
// image, same screen columns, and the next tile row. Merged runs are removed
// from the current row; the compacted slice is returned, together with the
// lowest index merged into, so the caller knows how far back the next row has
// to look.
//
// row is the screen row the current runs are on: a run can only extend a
// rectangle whose bottom edge is the row immediately above.
func mergePlaceholderRows(
	rects []placeholderRect, prevStart, prevEnd, curStart, row int,
) ([]placeholderRect, int) {
	keep := curStart
	mergedMin := curStart
	p := prevStart
	for i := curStart; i < len(rects); i++ {
		cur := rects[i]
		merged := false
		// Advance the previous-row cursor to the first candidate that could
		// still match this column span.
		for p < prevEnd && rects[p].sc1 < cur.sc0 {
			p++
		}
		if p < prevEnd {
			above := &rects[p]
			if above.sr1+1 == row && above.sc0 == cur.sc0 && above.sc1 == cur.sc1 &&
				above.imageID == cur.imageID && above.placementID == cur.placementID &&
				above.dc0 == cur.dc0 && above.dr0+(above.sr1-above.sr0)+1 == cur.dr0 {
				above.sr1 = row
				merged = true
				mergedMin = min(mergedMin, p)
			}
		}
		if !merged {
			rects[keep] = cur
			keep++
		}
	}
	return rects[:keep], mergedMin
}

// blitPlaceholderRect draws one placeholder rectangle.
//
// The image is mapped onto the *placement's* rectangle, not onto the visible
// cells: the placement origin is extrapolated backwards from the rectangle's
// first tile ((sr0, sc0) shows tile (dr0, dc0), so the placement starts
// dr0 rows above and dc0 columns left of it). The visible cells then act as a
// window onto that mapping, which is what makes a partly-scrolled image line
// up with the cells that survived.
//
// The window is applied with a clip only when it actually cuts the image
// somewhere inside the viewport. An image that runs off the top of the
// viewport needs no clip — the canvas already clips there — and that is the
// common case, so the ordinary path stays a plain dc.Image.
func (t *Term) blitPlaceholderRect(ds *drawState, rect *placeholderRect) {
	v, ok := ds.g.virtualImages[rect.imageID]
	if !ok || v.Cols <= 0 || v.Rows <= 0 || v.WidthPx <= 0 || v.HeightPx <= 0 {
		return
	}

	// Placement rectangle in canvas coordinates.
	px := float32(rect.sc0-rect.dc0) * t.cellW
	py := float32(rect.sr0-rect.dr0)*t.cellH + ds.renderYOff
	pw := float32(v.Cols) * t.cellW
	ph := float32(v.Rows) * t.cellH

	// The image is fit inside that rectangle with its aspect ratio preserved
	// (spec wording for virtual placements), anchored top-left so the tile
	// grid the application addressed still starts at the placement's origin.
	scale := min(pw/float32(v.WidthPx), ph/float32(v.HeightPx))
	iw := float32(v.WidthPx) * scale
	ih := float32(v.HeightPx) * scale

	// The window: the cells actually holding placeholders.
	wx0 := float32(rect.sc0) * t.cellW
	wy0 := float32(rect.sr0)*t.cellH + ds.renderYOff
	wx1 := float32(rect.sc1+1) * t.cellW
	wy1 := float32(rect.sr1+1)*t.cellH + ds.renderYOff

	// The paintable viewport. Anything the image spills outside this is
	// clipped by the canvas anyway, so it must not count as a reason to clip.
	vx0, vy0 := float32(0), float32(ds.renderTop)*t.cellH+ds.renderYOff
	vx1 := float32(ds.cols) * t.cellW
	vy1 := float32(ds.renderRows)*t.cellH + ds.renderYOff

	// eps absorbs the float noise in cellW*column arithmetic; a fraction of a
	// pixel of overhang is not a reason to take the clipped path.
	const eps = 0.5
	needClip := max(px, vx0) < wx0-eps || min(px+iw, vx1) > wx1+eps ||
		max(py, vy0) < wy0-eps || min(py+ih, vy1) > wy1+eps

	if needClip {
		ds.dc.ImageClipped(px, py, iw, ih, v.Src,
			gui.Opt[float32]{}, gui.Color{},
			wx0, wy0, wx1-wx0, wy1-wy0)
		return
	}
	ds.dc.Image(px, py, iw, ih, v.Src, gui.Opt[float32]{}, gui.Color{})
}
