package term

// Virtual (Unicode-placeholder) Kitty images.
//
// A normal KGP placement owns a rectangle of cells: it has an origin row, a
// footprint, and the cells under it are blanked. A *virtual* placement (U=1)
// owns nothing. It only declares "image N, when it is eventually shown, is to
// be fit into a rows×cols rectangle". The image appears wherever the
// application later writes U+10EEEE placeholder cells naming it — ordinary
// text, which the host application (tmux, vim, yazi) moves around without
// knowing anything about graphics.
//
// That is why this store is deliberately unlike grid.Graphics:
//
//   - It is not per-screen. There is no rectangle to belong to a screen; the
//     placeholder *cells* are per-screen and they are what makes an image
//     visible, so the alt screen simply shows nothing until an app writes
//     placeholders of its own.
//   - Nothing adjusts it on scroll, scrollback trim, or reflow. Those paths
//     exist to keep a placement's origin row glued to the text near it — a
//     virtual placement has no origin. Its cells reflow as text already.

// maxVirtualImages caps the virtual-placement store. Mirrors
// maxKittyStoreEntries: an unbounded map here is a memory DoS via a stream of
// a=p,U=1 escapes with distinct i= values.
const maxVirtualImages = 64

// virtualImage is one virtual placement, keyed by KGP image id.
type virtualImage struct {
	// Src is the PNG on disk, the same file the off-screen store (kittyStore)
	// holds for this id. Its lifetime is governed there; graphicsUseSrc counts
	// virtual placements as users so eviction can't delete a file that
	// placeholder cells are still displaying.
	Src         string
	WidthPx     int
	HeightPx    int
	Cols        int    // c=: placement width in cells
	Rows        int    // r=: placement height in cells
	PlacementID uint32 // p=: 0 when the client did not name the placement
	seq         uint64 // insertion order, for oldest-first eviction
}

// addVirtualImage records (or replaces) the virtual placement for image id.
//
// One entry per image id, last write wins. The spec allows several virtual
// placements of one image distinguished by p=, but a client that wants two
// different rectangles of the same picture is vanishingly rare next to the
// cost of a second lookup key on the render path — and the spec explicitly
// permits the terminal to "choose any virtual placement of the given image"
// when the placeholder cells name none. PlacementID is still recorded so the
// renderer can prefer an exact match if that ever changes. Caller holds Mu.
func (g *grid) addVirtualImage(id uint32, v virtualImage) {
	if id == 0 || v.Src == "" {
		return
	}
	// A placement with no pixels or no cells can never be drawn, and storing it
	// would only make the renderer re-check it every frame. The upper clamp
	// mirrors addGraphicCells: c=/r= are client-supplied, and the footprint
	// bounds the tile coordinates the render scan will accept.
	if v.WidthPx <= 0 || v.HeightPx <= 0 || v.Cols <= 0 || v.Rows <= 0 {
		return
	}
	v.Cols = min(v.Cols, MaxGridDim)
	v.Rows = min(v.Rows, MaxGridDim)
	if g.virtualImages == nil {
		g.virtualImages = make(map[uint32]virtualImage)
	}
	if _, exists := g.virtualImages[id]; !exists &&
		len(g.virtualImages) >= maxVirtualImages {
		g.evictOldestVirtual()
	}
	g.virtualSeq++
	v.seq = g.virtualSeq
	g.virtualImages[id] = v
	// The image layer changed even though no cell did: placeholder cells
	// already on screen now resolve to a different picture (or to one at all).
	g.markAllDirty()
}

// evictOldestVirtual drops the least recently added entry. Linear over at most
// maxVirtualImages entries, which is cheaper than maintaining a second
// ordering structure for a map that is almost always tiny.
func (g *grid) evictOldestVirtual() {
	var (
		oldestID  uint32
		oldestSeq uint64
		found     bool
	)
	for id, v := range g.virtualImages {
		if !found || v.seq < oldestSeq {
			oldestID, oldestSeq, found = id, v.seq, true
		}
	}
	if found {
		delete(g.virtualImages, oldestID)
	}
}

// deleteVirtualImage drops the virtual placement for id, if any. Caller
// holds Mu.
func (g *grid) deleteVirtualImage(id uint32) {
	if _, ok := g.virtualImages[id]; !ok {
		return
	}
	delete(g.virtualImages, id)
	g.markAllDirty()
}

// deleteAllVirtualImages drops every virtual placement. Used by the
// data-freeing KGP deletes (uppercase d=) and by reset. Caller holds Mu.
func (g *grid) deleteAllVirtualImages() {
	if len(g.virtualImages) == 0 {
		return
	}
	g.virtualImages = nil
	g.markAllDirty()
}

// virtualUseSrc reports whether any virtual placement draws the file at src.
// Caller holds Mu.
func (g *grid) virtualUseSrc(src string) bool {
	for _, v := range g.virtualImages {
		if v.Src == src {
			return true
		}
	}
	return false
}

// deleteVirtualBySrc drops every virtual placement drawing the file at src.
// Used when the backing file is removed, so the renderer never reaches for a
// dead path. Caller holds Mu.
func (g *grid) deleteVirtualBySrc(src string) {
	var removed bool
	for id, v := range g.virtualImages {
		if v.Src == src {
			delete(g.virtualImages, id)
			removed = true
		}
	}
	if removed {
		g.markAllDirty()
	}
}

// kgpCellRect resolves a KGP placement's cell footprint from the c= / r= keys.
//
//	both given      — use them; the image is scaled to fill the rectangle
//	one given       — derive the other from the pixel aspect ratio, so the
//	                  image is displayed without distortion (spec wording)
//	neither given   — (0, 0), leaving addGraphicCells to derive both from the
//	                  pixel size, which is the pre-existing behaviour
//
// cellPxW/cellPxH are the measured cell size; when they are not known yet
// (no frame drawn) the derivation is skipped and the caller falls back to the
// pixel-derived footprint.
func kgpCellRect(cols, rows, widthPx, heightPx int, cellPxW, cellPxH float32) (int, int) {
	if cols <= 0 && rows <= 0 {
		return 0, 0
	}
	if cols > 0 && rows > 0 {
		return cols, rows
	}
	if widthPx <= 0 || heightPx <= 0 || cellPxW <= 0 || cellPxH <= 0 {
		// Nothing to derive the missing dimension from. One-cell fallback for
		// the given axis keeps the placement non-degenerate.
		if cols <= 0 {
			cols = 1
		}
		if rows <= 0 {
			rows = 1
		}
		return cols, rows
	}
	if cols > 0 {
		// Height in pixels the image will have once scaled to cols columns.
		px := float64(heightPx) * (float64(cols) * float64(cellPxW) / float64(widthPx))
		rows = max(int(px/float64(cellPxH)+0.5), 1)
		return cols, rows
	}
	px := float64(widthPx) * (float64(rows) * float64(cellPxH) / float64(heightPx))
	cols = max(int(px/float64(cellPxW)+0.5), 1)
	return cols, rows
}
