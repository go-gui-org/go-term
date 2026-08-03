package term

import "math"

// Grid-side graphics buffer management: registering decoded images
// (AddGraphic / AddGraphicKitty), moving and trimming their origins as
// rows scroll, and removing them (occlusion by painted-over cells, KGP
// deletes). Image *decoding* lives in graphics.go; the grid half of the
// virtual (Unicode-placeholder) layer lives in grid_virtual.go.

// mainGraphics returns the main screen's image list and its occlusion bound:
// the live pair when the main screen is active, the stashed pair while the alt
// screen is up. Marks and graphics describe main-screen content, so the reflow
// and scrollback-trim paths address the main list either way.
func (g *grid) mainGraphics() (*[]graphic, *int) {
	if g.AltActive {
		return &g.mainSaved.graphics, &g.mainSaved.occludeMaxR
	}
	return &g.Graphics, &g.occludeMaxR
}

// gfxTrim drops `extra` rows from the front of every origin in list,
// discarding any whose covered range falls entirely above row 0.
func gfxTrim(list []graphic, extra int) []graphic {
	if len(list) == 0 {
		return list
	}
	j := 0
	for _, gr := range list {
		gr.OriginR -= extra
		if gr.OriginR+gr.Rows > 0 {
			list[j] = gr
			j++
		}
	}
	return list[:j]
}

// gfxShift applies a flat delta to every origin in list, dropping any pushed
// outside [0, total). bound is the list's occlusion bound, marked unknown when
// origins moved down (the invariant allows too high, never too low).
func gfxShift(list *[]graphic, bound *int, delta, total int) {
	if len(*list) == 0 {
		return
	}
	j := 0
	for _, gr := range *list {
		gr.OriginR += delta
		if gr.OriginR+gr.Rows > 0 && gr.OriginR < total {
			(*list)[j] = gr
			j++
		}
	}
	*list = (*list)[:j]
	if delta > 0 {
		*bound = occludeBoundUnknown
	}
}

// gfxDropSrc removes every placement in list drawing the file at src,
// reporting how many went.
func gfxDropSrc(list []graphic, src string) ([]graphic, int) {
	j, removed := 0, 0
	for _, gr := range list {
		if gr.Src == src {
			removed++
			continue
		}
		list[j] = gr
		j++
	}
	return list[:j], removed
}

// scrollGraphicsRegion moves the active screen's images with a region scroll
// whose rows did *not* go into scrollback — the alt screen (which never pushes)
// and any DECSTBM region on the main screen. Where rows are pushed instead, the
// content-row space absorbs the scroll and origins stay put; here the text
// slides under fixed origins, so an image would drift away from the cells it
// was placed on and the occlusion rectangle would stop describing it.
//
// top/bottom are the inclusive live-screen region rows and n the row count;
// down selects the SD/IL direction. Images that leave the region are dropped,
// matching the text that scrolled out with them. Caller holds Mu.
func (g *grid) scrollGraphicsRegion(top, bottom, n int, down bool) {
	if n <= 0 || len(g.Graphics) == 0 {
		return
	}
	base := g.Scrollback.Len()
	rTop, rBot := base+top, base+bottom // inclusive, content coordinates
	delta := -n
	if down {
		delta = n
	}
	j := 0
	for _, gr := range g.Graphics {
		// Only images overlapping the scrolled region move; one sitting
		// outside it (scrollback, or rows the region excludes) is untouched.
		if gr.OriginR+gr.Rows > rTop && gr.OriginR <= rBot {
			gr.OriginR += delta
			if gr.OriginR+gr.Rows <= rTop || gr.OriginR > rBot {
				continue // scrolled out of the region entirely
			}
		}
		g.Graphics[j] = gr
		j++
	}
	g.Graphics = g.Graphics[:j]
	if down {
		g.occludeMaxR = occludeBoundUnknown // origins moved down
	}
}

// trimGraphics drops `extra` rows from the front of all graphic origins,
// discarding any whose covered range falls entirely above row 0. Called
// after scrollback is trimmed. Both screens' lists are anchored to the same
// content-row space, so an alt screen's own images move with them. Caller
// holds Mu.
func (g *grid) trimGraphics(extra int) {
	if extra <= 0 {
		return
	}
	g.Graphics = gfxTrim(g.Graphics, extra)
	if g.AltActive {
		g.mainSaved.graphics = gfxTrim(g.mainSaved.graphics, extra)
	}
}

// remapGraphics rewrites main-screen graphic origins from the mapping
// logicalReflow produced: rows[i] is the new content row of the main list's
// entry i, -1 meaning the re-wrap discarded it. Length mismatches are ignored
// defensively — a short slice simply drops the unmapped tail. Caller holds Mu.
func (g *grid) remapGraphics(rows []int) {
	list, bound := g.mainGraphics()
	if len(*list) == 0 {
		return
	}
	j := 0
	for i, gr := range *list {
		if i >= len(rows) || rows[i] < 0 {
			continue
		}
		gr.OriginR = rows[i]
		(*list)[j] = gr
		j++
	}
	*list = (*list)[:j]
	*bound = occludeBoundUnknown // re-wrap moved origins arbitrarily
}

// shiftGraphics applies a flat delta to every main-screen graphic origin. Only
// correct when no re-wrap happened (rows kept their identity); the reflow path
// uses remapGraphics instead. The alt screen's own images are shifted by
// Resize directly, alongside the selection, since they only ever move by the
// flat scrollback delta.
func (g *grid) shiftGraphics(delta, total int) {
	list, bound := g.mainGraphics()
	gfxShift(list, bound, delta, total)
}

// AddGraphic registers a decoded image at the cursor's current content
// position and blanks the cells it covers. cellPxW/cellPxH from the
// most recent measurement determine the cell rectangle; if those are
// zero (no frame drawn yet) a single-cell footprint is used. Caller
// holds Mu.
func (g *grid) AddGraphic(src string, widthPx, heightPx int) (int, int) {
	return g.addGraphicCells(src, widthPx, heightPx, 0, 0)
}

// AddGraphicKitty is AddGraphic for the Kitty Graphics Protocol: the
// placement records the KGP image id so a later delete (`a=d,d=i,i=…`) can
// find it. wantCols/wantRows carry the c=/r= cell footprint the client asked
// for; zero means "derive it from the pixel size". Caller holds Mu.
func (g *grid) AddGraphicKitty(
	src string, widthPx, heightPx, wantCols, wantRows int, id uint32,
) (int, int) {
	cols, rows := g.addGraphicCells(src, widthPx, heightPx, wantCols, wantRows)
	if cols <= 0 || rows <= 0 {
		return 0, 0 // rejected: nothing was appended to tag
	}
	last := &g.Graphics[len(g.Graphics)-1]
	last.ID, last.kgp = id, true
	return cols, rows
}

// occludeGraphics drops every non-Kitty image whose covered rectangle
// intersects rows [lr, lr+n) of the live screen, columns [from, to).
//
// Sixel and iTerm2 inline images have no delete sequence: an application
// clears one by painting over the cells it occupies, which is exactly what
// yazi does when the preview moves off an image. Without this the picture
// stays on screen for the rest of the session. Kitty placements are exempt —
// KGP is explicit about images being their own layer that text does not
// disturb, so those go away only via a=d (see kittyDeleteID).
//
// Callers guard on len(g.Graphics) != 0 so the common no-image case costs one
// length check on the write path. Caller holds Mu.
func (g *grid) occludeGraphics(lr, n, from, to int) {
	// g.Graphics is the active screen's list — EnterAlt parks the main
	// screen's images out of reach — so a full-screen app painting over its
	// own alt rows can only ever clear images it placed itself.
	base := g.Scrollback.Len()
	// Every occludable image sits entirely in scrollback, so no live-screen
	// write can reach one. Skipping here is what keeps a long previewer
	// session — which parks images in scrollback up to maxGraphics — from
	// paying a full scan on every glyph written afterwards.
	if g.occludeMaxR <= base {
		return
	}
	top, bottom := base+lr, base+lr+n // [top, bottom)
	j, removed, maxR := 0, 0, 0
	for _, gr := range g.Graphics {
		if !gr.kgp && gr.OriginR < bottom && top < gr.OriginR+gr.Rows &&
			gr.OriginC < to && from < gr.OriginC+gr.Cols {
			removed++
			continue
		}
		if !gr.kgp && gr.OriginR+gr.Rows > maxR {
			maxR = gr.OriginR + gr.Rows
		}
		g.Graphics[j] = gr
		j++
	}
	// The loop saw every graphic, so this tightens the bound exactly — the
	// one place it is ever allowed to shrink.
	g.occludeMaxR = maxR
	if removed > 0 {
		g.Graphics = g.Graphics[:j]
		g.markAllDirty()
	}
}

// graphicsUseSrc reports whether any placement draws the file at src. Caller
// holds Mu.
func (g *grid) graphicsUseSrc(src string) bool {
	for _, gr := range g.Graphics {
		if gr.Src == src {
			return true
		}
	}
	// The parked main-screen list counts too: its files must survive the alt
	// screen or the images come back to a dead path on ExitAlt.
	for _, gr := range g.mainSaved.graphics {
		if gr.Src == src {
			return true
		}
	}
	// Virtual placements have no rectangle but do have a file: a placeholder
	// cell on screen draws it, so evicting it out from under them would blank
	// a visible image.
	return g.virtualUseSrc(src)
}

// deleteGraphicsBySrc drops every placement drawing the file at src. Used when
// the backing file is removed, so the renderer never reaches for a dead path.
// Caller holds Mu.
func (g *grid) deleteGraphicsBySrc(src string) {
	var removed int
	g.Graphics, removed = gfxDropSrc(g.Graphics, src)
	if removed > 0 {
		g.markAllDirty()
	}
	// The parked main-screen list is purged too: the file is gone, so a
	// placement restored by ExitAlt would draw a dead path. Nothing on screen
	// changes, so no repaint is needed for that half.
	g.mainSaved.graphics, _ = gfxDropSrc(g.mainSaved.graphics, src)
	g.deleteVirtualBySrc(src)
}

// deleteGraphics removes Kitty placements: every one when all is set (KGP
// `d=a`), otherwise those carrying the given Kitty image id.
//
// Only KGP placements are touched. `a=d` is a Kitty command and Kitty owns
// only its own layer — a sixel or iTerm2 image is removed by painting over it
// (see occludeGraphics), never by a delete sequence, so `d=a` must leave those
// alone even though they share one Graphics list.
//
// Dropping the placement is the visible half of a KGP delete — freeing the
// stored image data alone leaves the picture on screen forever. The cells
// under the image were blanked when it was added and stay blank, but the
// image layer changed, so the screen is marked dirty to force a repaint.
// Caller holds Mu.
func (g *grid) deleteGraphics(id uint32, all bool) {
	if len(g.Graphics) == 0 {
		return
	}
	j := 0
	for _, gr := range g.Graphics {
		if gr.kgp && (all || (id != 0 && gr.ID == id)) {
			continue
		}
		g.Graphics[j] = gr
		j++
	}
	if j != len(g.Graphics) {
		g.Graphics = g.Graphics[:j]
		g.markAllDirty()
	}
}

// cellsForPixels converts a pixel size to the cell footprint that covers it,
// rounding up. Falls back to a single cell when the cell size has not been
// measured yet (no frame drawn). Caller holds Mu.
func (g *grid) cellsForPixels(widthPx, heightPx int) (int, int) {
	if g.CellPxW <= 0 || g.CellPxH <= 0 {
		return 1, 1
	}
	cols := int(math.Ceil(float64(widthPx) / float64(g.CellPxW)))
	rows := int(math.Ceil(float64(heightPx) / float64(g.CellPxH)))
	return max(cols, 1), max(rows, 1)
}

// addGraphicCells is AddGraphic with an explicit cell footprint. Non-positive
// cols/rows mean "derive from the pixel size", which is what every caller but
// the OSC 1337 width=/height= path wants. The clamps apply either way: an
// explicit size is still capped to MaxGridDim rows and truncated at the right
// margin. Caller holds Mu.
func (g *grid) addGraphicCells(src string, widthPx, heightPx, cols, rows int) (int, int) {
	if src == "" || widthPx <= 0 || heightPx <= 0 {
		return 0, 0
	}
	if cols <= 0 || rows <= 0 {
		cols, rows = g.cellsForPixels(widthPx, heightPx)
	}
	if rows > MaxGridDim {
		rows = MaxGridDim
	}
	originR := g.Scrollback.Len() + g.CursorR
	originC := g.CursorC
	if originC+cols > g.Cols {
		cols = g.Cols - originC
		if cols <= 0 {
			return 0, 0
		}
	}
	if len(g.Graphics) >= maxGraphics {
		copy(g.Graphics, g.Graphics[1:])
		g.Graphics = g.Graphics[:maxGraphics-1]
	}
	// Widen the occlusion bound. AddGraphicKitty marks the entry kgp only
	// afterwards, so a KGP placement widens it too — costing at most a scan
	// that finds nothing, never a missed occlusion.
	if end := originR + rows; end > g.occludeMaxR {
		g.occludeMaxR = end
	}
	g.Graphics = append(g.Graphics, graphic{
		Src:      src,
		OriginR:  originR,
		OriginC:  originC,
		Cols:     cols,
		Rows:     rows,
		WidthPx:  widthPx,
		HeightPx: heightPx,
	})
	blank := blankCell(DefaultColor, DefaultColor, 0)
	for r := range rows {
		lr := g.CursorR + r
		if lr < 0 || lr >= g.Rows {
			continue
		}
		for c := range cols {
			cc := originC + c
			if cc < 0 || cc >= g.Cols {
				continue
			}
			g.Cells[lr*g.Cols+cc] = blank
		}
		g.RowWrapped[lr] = false
		g.markDirty(lr)
	}
	return cols, rows
}
