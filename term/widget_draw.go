package term

import (
	"math"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// realNumber reports whether f is non-NaN and non-Inf. Used for inputs
// (mouse coords, scroll deltas) where zero and negative are legal.
//
// Generic over both float widths rather than duplicated per width: the pixel
// metrics are float32 and the contrast ratio is float64 (WCAG luminance math
// works in float64), and one predicate is what keeps the two from drifting.
func realNumber[T ~float32 | ~float64](f T) bool {
	x := float64(f)
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}

// finite reports whether f is a usable, positive cell metric. Rejects
// NaN, Inf, and non-positive values which would otherwise produce
// garbage row/col counts in OnDraw.
func finite(f float32) bool { return realNumber(f) && f > 0 }

// snapPx rounds a logical coordinate to the physical pixel grid.
//
// Cell metrics are fractional (cellW comes from TextWidth("M"), cellH from
// FontHeight), so a cell origin computed as col*cellW lands on a different
// sub-pixel phase in every column, and row*cellH likewise in every row.
// go-glyph rasterizes each glyph into one of four horizontal sub-pixel bins
// and rounds vertically to a whole pixel — but it does that on the *layout*
// origin only, and the widget-supplied x/y is added afterwards
// (draw_atlas.go emitGlyphQuad / the fill path). A fractional x therefore
// puts a 1px stem fully inside one pixel column in some cells and splits it
// across two in others, which is what makes long box-drawing runs look
// banded: bright where the stroke is whole, dim where it is halved.
//
// Snapping the origin here restores go-glyph's own alignment and costs one
// round per emitted run. Advances inside a coalesced run are still
// fractional, but a run is a single layout that go-glyph already snaps.
func (t *Term) snapPx(v float32) float32 {
	s := t.draw.pxScale
	if s <= 0 {
		return v
	}
	// finite() admits an absurdly large cell metric (it only rejects NaN, Inf
	// and non-positive), and v*s can then overflow float32 to Inf. That would
	// be survivable on its own, but spanW/rowH subtract two snapped
	// coordinates, and Inf-Inf is NaN — which reaches dc.FilledRect as a
	// dimension. Fall back to the unsnapped value instead.
	snapped := float32(math.Round(float64(v)*float64(s))) / s
	if !realNumber(snapped) {
		return v
	}
	return snapped
}

// colX returns the pixel-snapped x origin of column c.
func (t *Term) colX(c int) float32 { return t.snapPx(float32(c) * t.cellW) }

// rowY returns the pixel-snapped y origin of viewport row r, including the
// smooth-scroll sub-cell offset. Snapping still leaves smooth scrolling
// smooth — it just quantizes it to whole device pixels, which is the finest
// step the display can show anyway.
func (t *Term) rowY(r int, yOff float32) float32 {
	return t.snapPx(float32(r)*t.cellH + yOff)
}

// spanW returns the snapped width of columns [c0, c1) — the difference of two
// snapped origins, so adjacent spans tile exactly with no seam or overlap.
// Row heights follow the same rule but have no helper: every caller has the
// row's own snapped origin in hand already and subtracts from rowY(r+1).
func (t *Term) spanW(c0, c1 int) float32 { return t.colX(c1) - t.colX(c0) }

// searchOverlap returns the number of grid rows whose text footprint
// overlaps the search bar's pixel region. Row r's text footprint spans
// [r*cellH+renderYOff, (r+1)*cellH+renderYOff). The search bar occupies
// [canvasHeight-cellH, canvasHeight). go-gui renders all Text on top of
// all FilledRects within a single frame, so terminal text in that region
// would paint over the search bar background — we keep it out by
// reserving overlapping rows.
func searchOverlap(cellH, renderYOff, canvasHeight float32, rows int) int {
	searchBarTop := canvasHeight - cellH
	r := rows - 1
	for r >= 0 && float32(r+1)*cellH+renderYOff > searchBarTop {
		r--
	}
	return rows - 1 - r
}

// vMatch records a single search-highlight span within a viewport row.
type vMatch struct{ col, len int }

// rowBounds records the selection column span for one viewport row.
type rowBounds struct {
	c0, c1 int
	active bool
}

// drawState holds per-frame state computed under grid.Mu and threaded
// through the phase methods that replaced the anonymous function in onDraw.
type drawState struct {
	now           time.Time
	dc            *gui.DrawContext
	g             *grid
	cells         []cell
	vMatchesByRow [][]vMatch
	rowSel        []rowBounds
	rowURL        []rowBounds // hover-detected implicit-URL span per viewport row
	bidiVisRows   [][]cell
	bidiV2LRows   [][]int
	partialRow    []cell
	imeRunes      []rune
	imeWidths     []int
	style         gui.TextStyle
	rows, cols    int
	renderRows    int
	// renderTop is the first viewport row the text passes paint, reserving the
	// rows above it for an overlay bar. Copy mode sets it to 1: its status bar
	// sits at the top, so the row it covers must not be painted under it.
	renderTop  int
	imeCursor  int
	renderYOff float32
	live       bool
	doResize   bool
	// blinkOff is true during the hidden half of the SGR 5/6 blink cycle.
	blinkOff bool
	// IME composition state, populated by drawIME and consumed by drawCursor.
	imeComposing bool
}

// resolveCell returns the cell at viewport (r, c), applying the selection
// tint and search-highlight inversion. Uses the fast path (direct Cells
// index) when ds.live; otherwise goes through ViewCellAt.
func (ds *drawState) resolveCell(r, c int) cell {
	if ds.live {
		return ds.cells[r*ds.cols+c]
	}
	cell := ds.g.ViewCellAt(r, c)
	// Search matches invert; selection tints. Search runs first so a cell that
	// is both keeps the inverted match colors, with the selection tint blended
	// on top of them rather than the two effects cancelling out.
	if ds.vMatchesByRow != nil {
		for _, m := range ds.vMatchesByRow[r] {
			if c >= m.col && c < m.col+m.len {
				cell.Attrs ^= attrInverse
				break
			}
		}
	}
	if ds.rowSel != nil {
		if rb := ds.rowSel[r]; rb.active && c >= rb.c0 && c <= rb.c1 {
			cell = ds.g.highlightSelected(cell)
		}
	}
	return cell
}

// rawCell returns the cell at viewport (r, c) with no visual transforms at
// all — no selection tint, no search inversion, no BiDi reorder. The Kitty
// placeholder decode needs it: the image id lives in the cell's foreground
// color, and highlightSelected rewrites exactly that.
func (ds *drawState) rawCell(r, c int) cell {
	if ds.live {
		return ds.cells[r*ds.cols+c]
	}
	return ds.g.ViewCellAt(r, c)
}

// resolveVisual returns the cell at viewport (r, c), routing through the
// BiDi visual-reorder map when row r contains RTL content.
func (ds *drawState) resolveVisual(r, c int) cell {
	if ds.bidiVisRows[r] != nil {
		return ds.bidiVisRows[r][c]
	}
	return ds.resolveCell(r, c)
}

// flushState tracks an in-progress text-run for fg-pass run coalescing.
type flushState struct {
	start int
	cols  int // columns spanned (for underline width)
	key   runKey
	open  bool
}

// onDraw is the DrawCanvas callback. Measures cell size on first call,
// reflows the grid + pty when the canvas size changes, then paints the
// grid as a sequence of background rects + per-cell text + cursor.
func (t *Term) onDraw(dc *gui.DrawContext) {
	style := t.style()
	if t.cellW == 0 {
		t.cellW = dc.TextWidth("M", style)
		t.cellH = dc.FontHeight(style)
	}
	if !finite(t.cellW) || !finite(t.cellH) {
		return
	}
	if !finite(dc.Width) || !finite(dc.Height) {
		return
	}
	// Captured before any pass: every cell origin is snapped against it.
	t.draw.pxScale = 1
	if s := dc.Scale; s > 0 && realNumber(s) {
		t.draw.pxScale = s
	}
	cols := clampDim(int(dc.Width / t.cellW))
	rows := clampDim(int(dc.Height / t.cellH))
	t.draw.runBuf.Grow(cols * 4) // one row of text, worst-case UTF-8; no-op when cap sufficient

	now := time.Now()
	ds := drawState{
		dc:       dc,
		style:    style,
		g:        t.grid,
		rows:     rows,
		cols:     cols,
		now:      now,
		blinkOff: textBlinkOff(now),
	}

	t.grid.Mu.Lock()

	// Cancel selection drag when canvas dimensions change between frames.
	// A window-resize drag started from the border can leak a mouse-down
	// into the terminal (go-gui regression); the native Cocoa resize
	// tracking loop then consumes the mouse-up, so neither the locked
	// onMouseUp nor HandleWindowEvent ever sees the release. Without this,
	// t.mouse.dragging stays true permanently and every subsequent pointer
	// motion spuriously extends the selection.
	if t.mouse.dragging && !t.mouse.dragReport {
		if rows != t.grid.Rows || cols != t.grid.Cols {
			t.mouse.dragging = false
			t.autoScrollDir.Store(0)
			t.grid.ClearSelection()
			t.unlockMouse(t.win)
		}
	}
	// Same rationale for a scrollbar thumb drag: a resize gesture can steal
	// the mouse-up, leaving dragging stuck true so every later frame keeps
	// repositioning the viewport. Drop the drag when the grid reflows.
	if t.scrollbar.dragging && (rows != t.grid.Rows || cols != t.grid.Cols) {
		t.scrollbar.dragging = false
		t.unlockMouse(t.win)
	}

	// Phase order matters: prepareFastPath sets ds.renderRows / ds.live which
	// which all subsequent phases read. prepareBiDi sets ds.bidiVisRows consumed
	// by drawBgPass / drawFgPass / drawCursor. drawIME populates ds.ime* fields
	// consumed by drawCursor.
	t.prepareResize(&ds)
	t.prepareFastPath(&ds)
	t.prepareSearch(&ds)
	t.prepareSelection(&ds)
	t.prepareHoverURL(&ds)
	t.prepareBiDi(&ds)
	t.preparePartialRow(&ds)
	t.drawBgPass(&ds)
	t.drawFgPass(&ds)
	t.drawGraphics(ds.dc, ds.g, ds.renderTop, ds.renderRows, ds.renderYOff)
	t.drawPlaceholders(&ds)
	t.drawIME(&ds)
	t.drawCursor(&ds)
	t.drawOverlays(&ds)
	t.grid.Mu.Unlock()

	if ds.doResize {
		// Defer pty resize to the resizeLoop goroutine so the
		// TIOCSWINSZ ioctl never runs on the main thread.
		// During live resize on macOS, onDraw is called from
		// within SDL's event watch callback inside Cocoa's
		// modal tracking loop — calling the ioctl inline (or
		// via QueueCommand, which executes in flushCommands
		// during the next FrameFn from the same callback) can
		// deadlock against the SDL event queue when the shell's
		// SIGWINCH response causes readLoop to push a user event.
		// Store the dims before setting pending; resizeLoop reads
		// them only after observing pending via Swap.
		t.ptyResizeRows.Store(int32(ds.rows))
		t.ptyResizeCols.Store(int32(ds.cols))
		t.ptyResizePending.Store(true)
		// Non-blocking: a buffered kick already in flight is enough —
		// resizeLoop re-reads the latch after every wake. Nil channel
		// (bare test Terms) falls through to default.
		select {
		case t.ptyResizeKick <- struct{}{}:
		default:
		}
	}
}

// ---------------------------------------------------------------------------
// Phase methods — called sequentially from onDraw under grid.Mu.
// ---------------------------------------------------------------------------

// prepareResize debounces canvas-size changes and applies grid.Resize when
// the target dimensions have been stable for resizeDebounce. Sets ds.doResize
// so the caller can resize the pty outside the lock.
func (t *Term) prepareResize(ds *drawState) {
	// DECCOLM pins the width. Rows still follow the canvas — only the right
	// margin is under the application's control — so a pinned grid narrower
	// than the canvas simply leaves the surplus width unpainted.
	if cm := t.grid.ColumnMode; cm > 0 {
		ds.cols = clampDim(cm)
	}
	if ds.rows != t.grid.Rows || ds.cols != t.grid.Cols {
		now := ds.now
		// Show the size readout for the candidate dims. Driven from here, not
		// from the applied resize below, so the number tracks the pointer
		// through the debounce window instead of lagging it.
		t.showSizeBadge(ds.rows, ds.cols, now)
		if ds.rows != t.resize.pendingRows ||
			ds.cols != t.resize.pendingCols ||
			t.resize.pendingSince.IsZero() {
			t.resize.pendingRows = ds.rows
			t.resize.pendingCols = ds.cols
			t.resize.pendingSince = now
		}
		if elapsed := now.Sub(t.resize.pendingSince); elapsed >= resizeDebounce {
			// Copy mode's cursor and anchor are content rows living in the
			// widget, so they must ride the same reflow re-map the grid
			// applies to its own marks and selection. Without this a resize
			// leaves the selection anchored to whatever text drifted into
			// those row numbers, and Resize's ViewOffset reset drops the
			// frozen viewport at the live bottom, far from the selection.
			var track [2]int
			if t.copy.active {
				track[0] = t.copy.cursor.Row
				track[1] = t.copy.anchor.Row
				t.grid.resizeTrack = track[:]
			}
			t.grid.Resize(ds.rows, ds.cols)
			if t.copy.active {
				t.grid.resizeTrack = nil
				t.applyCopyResize(track[0], track[1])
			}
			t.resize.sized = true
			t.resize.pendingSince = time.Time{}
		} else {
			t.scheduleResizeWake(resizeDebounce - elapsed)
		}
	} else if !t.resize.pendingSince.IsZero() {
		t.resize.pendingSince = time.Time{}
	}
	// Publish cell size in device pixels so image footprint math matches the
	// device-pixel dimensions stored in image files.
	// pxScale is sanitized once, in onDraw, which is this phase's only caller.
	t.grid.CellPxW = t.cellW * t.draw.pxScale
	t.grid.CellPxH = t.cellH * t.draw.pxScale
	// Hint labels address fixed grid positions captured on entry, so any
	// content change — pty output, a scroll, a resize — leaves them pointing at
	// rows that moved. Dropping the mode is the honest response: silently
	// opening whatever slid under the label would be worse than making the user
	// press the chord again. One dirty-row test covers all three causes.
	if t.hints.active && t.grid.HasDirtyRows() {
		t.hints.active = false
		t.hints.targets = t.hints.targets[:0]
		t.hints.typed = ""
	}
	t.grid.ClearDirty()

	// Refresh ds dims after potential Resize.
	ds.rows, ds.cols = t.grid.Rows, t.grid.Cols

	// Publish to the pty whenever the grid's dims differ from what the child
	// was last told, rather than only on the debounced canvas path above.
	// DECCOLM resizes the grid from the parser (reader goroutine) with no
	// canvas change at all, so a check keyed on the canvas would leave the
	// child believing the old width — which is precisely the geometry
	// disagreement DECCOLM exists to prevent.
	if ds.rows != t.resize.ptyLastRows || ds.cols != t.resize.ptyLastCols {
		t.resize.ptyLastRows, t.resize.ptyLastCols = ds.rows, ds.cols
		ds.doResize = true
	}
}

// prepareFastPath computes the fast-path flag, the effective render row count
// (accounting for search-bar overlap), and aliases the grid and cell buffer.
func (t *Term) prepareFastPath(ds *drawState) {
	g := ds.g
	ds.renderYOff = g.ViewSubPx
	ds.live = g.ViewOffset == 0 && ds.renderYOff == 0 && !g.SelActive &&
		!t.search.active && !t.copy.active
	ds.cells = g.Cells
	ds.renderRows = ds.rows
	// Copy mode's bar occupies the top cellH pixels. Reserving from the *top*
	// (rather than the bottom, as the search bar does) keeps the last row on
	// screen: that is where the shell prompt and the newest output sit, and it
	// is what the user entered copy mode to select. The cost is the oldest
	// visible row, one 'k' away.
	if t.copy.active {
		ds.renderTop = copyBarRows
	}
	if t.search.active {
		ds.renderRows -= searchOverlap(t.cellH, ds.renderYOff, ds.dc.Height, ds.rows)
		if ds.renderRows < 0 {
			ds.renderRows = 0
		}
	}
}

// prepareSearch pre-computes search-match spans per viewport row, reusing the
// cached match list unless the query or draw version has changed.
func (t *Term) prepareSearch(ds *drawState) {
	if !t.search.active || t.search.query == "" {
		return
	}
	g := ds.g
	rows := ds.rows
	if cap(t.draw.vMatchBuf) < rows {
		t.draw.vMatchBuf = make([][]vMatch, rows)
	} else {
		t.draw.vMatchBuf = t.draw.vMatchBuf[:rows]
		for i := range t.draw.vMatchBuf {
			t.draw.vMatchBuf[i] = t.draw.vMatchBuf[i][:0]
		}
	}
	ds.vMatchesByRow = t.draw.vMatchBuf
	curVer := t.drawVersion.Load()
	if curVer != t.search.cacheVer || t.search.query != t.search.cacheQuery || t.search.regex != t.search.cacheRegex {
		var matches []searchMatch
		if t.search.regex && t.search.re != nil {
			matches = g.ViewportMatchesRegex(t.search.re)
		} else if !t.search.regex {
			matches = g.ViewportMatches(t.search.query)
		}
		t.search.matches = matches
		t.search.cacheVer = curVer
		t.search.cacheQuery = t.search.query
		t.search.cacheRegex = t.search.regex
	}
	for _, m := range t.search.matches {
		if vr, ok := g.ContentRowToViewport(m.Row); ok && vr >= ds.renderTop && vr < ds.renderRows {
			ds.vMatchesByRow[vr] = append(ds.vMatchesByRow[vr], vMatch{m.Col, m.Len})
		}
	}
}

// prepareSelection pre-computes the selection column span for each viewport
// row so the per-cell resolveCell path can apply the selection tint without
// re-computing selOrder on every cell.
func (t *Term) prepareSelection(ds *drawState) {
	g := ds.g
	if !g.SelActive {
		return
	}
	rows := ds.rows
	cols := ds.cols
	if cap(t.draw.selBuf) < rows {
		t.draw.selBuf = make([]rowBounds, rows)
	} else {
		t.draw.selBuf = t.draw.selBuf[:rows]
		clear(t.draw.selBuf)
	}
	ds.rowSel = t.draw.selBuf
	s, e := g.selOrder()
	total := g.Scrollback.Len() + g.Rows
	s.Row, s.Col = clamp(s.Row, 0, total-1), clamp(s.Col, 0, g.Cols)
	e.Row, e.Col = clamp(e.Row, 0, total-1), clamp(e.Col, 0, g.Cols)
	for r := range rows {
		cr := g.viewportToContent(r)
		if cr < s.Row || cr > e.Row {
			continue
		}
		// Geometry (linear vs. block band) comes from selRowSpan, shared with
		// SelectedText so the highlight matches what a copy would yield.
		c0, c1, ok := g.selRowSpan(cr, s, e)
		if !ok {
			continue
		}
		// The grid may be wider than the viewport being drawn; clamp so a
		// stale span cannot index past the row buffer.
		if c1 > cols-1 {
			c1 = cols - 1
		}
		if c1 < c0 {
			continue
		}
		ds.rowSel[r] = rowBounds{c0, c1, true}
	}
}

// prepareHoverURL translates the Cmd-hovered implicit-URL span (issue 72),
// stored by updateHover in content coordinates, into a per-viewport-row column
// range consumed by drawFgPass. No-op unless Cmd is held and a URL is under the
// pointer, so the render path pays nothing in the common case.
func (t *Term) prepareHoverURL(ds *drawState) {
	if !t.mouse.cmdHeld.Load() || len(t.mouse.hoverSpans) == 0 {
		return
	}
	rows := ds.rows
	if cap(t.draw.urlBuf) < rows {
		t.draw.urlBuf = make([]rowBounds, rows)
	} else {
		t.draw.urlBuf = t.draw.urlBuf[:rows]
		clear(t.draw.urlBuf)
	}
	active := false
	for _, sp := range t.mouse.hoverSpans {
		vr, ok := ds.g.ContentRowToViewport(sp.Row)
		if !ok {
			continue
		}
		t.draw.urlBuf[vr] = rowBounds{sp.C0, sp.C1, true}
		active = true
	}
	if active {
		ds.rowURL = t.draw.urlBuf
	}
}

// prepareBiDi detects viewport rows containing RTL characters and computes
// their visual-reordered cell slices + logical→visual column maps. For live
// LTR-only terminals rowHasRTL returns false immediately — zero allocations.
func (t *Term) prepareBiDi(ds *drawState) {
	renderRows := ds.renderRows
	if renderRows == 0 {
		return
	}
	if cap(t.draw.bidiVisRows) < renderRows {
		t.draw.bidiVisRows = make([][]cell, renderRows)
		t.draw.bidiV2LRows = make([][]int, renderRows)
	}
	t.draw.bidiVisRows = t.draw.bidiVisRows[:renderRows]
	t.draw.bidiV2LRows = t.draw.bidiV2LRows[:renderRows]
	for i := range t.draw.bidiVisRows {
		t.draw.bidiVisRows[i] = nil
		t.draw.bidiV2LRows[i] = nil
	}
	ds.bidiVisRows = t.draw.bidiVisRows
	ds.bidiV2LRows = t.draw.bidiV2LRows
	cols := ds.cols
	for r := ds.renderTop; r < renderRows; r++ {
		var hasRTL bool
		if ds.live {
			hasRTL = rowHasRTL(ds.cells[r*cols:(r+1)*cols], cols)
		} else {
			for c := range cols {
				if isRTLRune(ds.g.ViewCellAt(r, c).Ch) {
					hasRTL = true
					break
				}
			}
		}
		if !hasRTL {
			continue
		}
		if cap(t.draw.bidiScratch) < cols {
			t.draw.bidiScratch = make([]cell, cols)
		} else {
			t.draw.bidiScratch = t.draw.bidiScratch[:cols]
		}
		for c := range cols {
			t.draw.bidiScratch[c] = ds.resolveCell(r, c)
		}
		ds.bidiVisRows[r], ds.bidiV2LRows[r] = visualReorder(t.draw.bidiScratch, cols)
	}
}

// preparePartialRow resolves the partial top row (visible when sub-pixel
// scrolled) and applies BiDi reordering when needed.
func (t *Term) preparePartialRow(ds *drawState) {
	// The partial row draws above viewport row 0 — exactly where a reserved
	// top bar sits, and text paints over rects, so it would show through.
	if ds.renderYOff <= 0 || ds.renderTop > 0 {
		return
	}
	row := ds.g.partialTopRow()
	if row != nil && rowHasRTL(row, ds.cols) {
		if vis, _ := visualReorder(row, ds.cols); vis != nil {
			row = vis
		}
	}
	ds.partialRow = row
}
