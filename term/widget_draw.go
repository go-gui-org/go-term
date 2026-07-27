package term

import (
	"math"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// realNumber reports whether f is non-NaN and non-Inf. Used for inputs
// (mouse coords, scroll deltas) where zero and negative are legal.
func realNumber(f float32) bool {
	x := float64(f)
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}

// finite reports whether f is a usable, positive cell metric. Rejects
// NaN, Inf, and non-positive values which would otherwise produce
// garbage row/col counts in OnDraw.
func finite(f float32) bool { return realNumber(f) && f > 0 }

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
	imeCursor     int
	renderYOff    float32
	live          bool
	doResize      bool
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
	t.drawGraphics(ds.dc, ds.g, ds.renderRows, ds.renderYOff)
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
	if ds.rows != t.grid.Rows || ds.cols != t.grid.Cols {
		now := ds.now
		if ds.rows != t.resize.pendingRows ||
			ds.cols != t.resize.pendingCols ||
			t.resize.pendingSince.IsZero() {
			t.resize.pendingRows = ds.rows
			t.resize.pendingCols = ds.cols
			t.resize.pendingSince = now
		}
		if elapsed := now.Sub(t.resize.pendingSince); elapsed >= resizeDebounce {
			t.grid.Resize(ds.rows, ds.cols)
			ds.doResize = true
			t.resize.pendingSince = time.Time{}
		} else {
			t.scheduleResizeWake(resizeDebounce - elapsed)
		}
	} else if !t.resize.pendingSince.IsZero() {
		t.resize.pendingSince = time.Time{}
	}
	// Publish cell size in device pixels so image footprint math matches the
	// device-pixel dimensions stored in image files.
	scale := ds.dc.Scale
	if scale == 0 || !realNumber(scale) {
		scale = 1
	}
	t.grid.CellPxW = t.cellW * scale
	t.grid.CellPxH = t.cellH * scale
	t.grid.ClearDirty()

	// Refresh ds dims after potential Resize.
	ds.rows, ds.cols = t.grid.Rows, t.grid.Cols
}

// prepareFastPath computes the fast-path flag, the effective render row count
// (accounting for search-bar overlap), and aliases the grid and cell buffer.
func (t *Term) prepareFastPath(ds *drawState) {
	g := ds.g
	ds.renderYOff = g.ViewSubPx
	ds.live = g.ViewOffset == 0 && ds.renderYOff == 0 && !g.SelActive && !t.search.active
	ds.cells = g.Cells
	ds.renderRows = ds.rows
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
		if vr, ok := g.ContentRowToViewport(m.Row); ok && vr < ds.renderRows {
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
	for r := range rows {
		cr := g.viewportToContent(r)
		if cr < s.Row || cr > e.Row {
			continue
		}
		// Columns are cell boundaries; the span is half-open [s.Col, e.Col).
		// c1 is the last selected cell index, so the end row stops one cell
		// short of the boundary. Rows whose span collapses (c1 < c0) are not
		// highlighted.
		c0, c1 := 0, cols-1
		if cr == s.Row {
			c0 = s.Col
		}
		if cr == e.Row {
			c1 = e.Col - 1
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
	for r := range renderRows {
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
	if ds.renderYOff <= 0 {
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
