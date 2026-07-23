package term

import (
	"math"
	"strings"
	"time"

	glyph "github.com/go-gui-org/go-glyph"
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

// imeCompRuneLimit caps the number of runes accepted from the IME
// composition string. Typical compositions contain fewer than 50 runes;
// this prevents excessive allocation from a malformed or malicious
// platform input.
const imeCompRuneLimit = 256

// asciiStr caches single-rune strings for runes 0..127 to avoid the
// per-cell allocation that string(rune) incurs in the OnDraw hot path.
var asciiStr = func() [128]string {
	var a [128]string
	for i := range a {
		a[i] = string(rune(i))
	}
	return a
}()

// termRuneStr returns string(r) without allocating for runes already in the
// per-Term cache. Wide-char cells and the cursor call this once per distinct
// rune seen, then reuse the cached string every subsequent frame.
func (t *Term) termRuneStr(r rune) string {
	if uint32(r) < 128 {
		return asciiStr[r]
	}
	if s, ok := t.draw.runeCache[r]; ok {
		return s
	}
	s := string(r)
	if t.draw.runeCache == nil {
		t.draw.runeCache = make(map[rune]string, 64)
	}
	t.draw.runeCache[r] = s
	return s
}

// cellText returns the text to render for a cell: the interned grapheme
// cluster string for multi-codepoint cells, otherwise the (cached) single
// rune. Caller holds grid.Mu (OnDraw does).
func (t *Term) cellText(c cell) string {
	if c.clusterID != 0 && int(c.clusterID) < len(t.grid.clusters) {
		return t.grid.clusters[c.clusterID]
	}
	return t.termRuneStr(c.Ch)
}

func isGeometryGlyph(r rune) bool {
	switch {
	case r >= 0x2500 && r <= 0x25FF: // Box Drawing, Block Elements, Geometric Shapes
		return true
	case r >= 0x23BA && r <= 0x23BD: // Misc Technical horizontal scan lines (⎺⎻⎼⎽)
		return true
	case r >= 0x2800 && r <= 0x28FF: // Braille Patterns
		return true
	default:
		return false
	}
}

// scrollbarGeometry computes the scrollbar thumb Y position and height.
// sbLen = len(Scrollback), viewH = canvas pixel height. Caller ensures sbLen > 0.
const minScrollbarThumbH float32 = 10

// scrollbarInset is the horizontal gap between the window's right edge and
// the drawn thumb, applied only to panes flush against that edge. macOS
// reserves an interior band just inside a resizable window's frame where
// mouseDown starts a live resize before the event reaches the content view;
// insetting the thumb keeps it (and its hit region) clear of that band.
const scrollbarInset float32 = 6

// scrollbarHitWidth is the minimum width of the clickable thumb region. The
// grabbable area extends inward (leftward) from the thumb's right edge so a
// narrow visual thumb is still easy to hit; the drawn thumb width is
// unchanged. Decoupling hit width from visual width is what makes an
// edge-hugging scrollbar usable despite the OS resize band.
const scrollbarHitWidth float32 = 16

func scrollbarGeometry(sbLen, rows int, viewOffset float32, viewH float32) (thumbY, thumbH float32) {
	if viewH <= 0 || math.IsNaN(float64(viewH)) || math.IsInf(float64(viewH), 0) ||
		math.IsNaN(float64(viewOffset)) || math.IsInf(float64(viewOffset), 0) {
		return
	}
	total := float32(sbLen + rows)
	if total <= 0 {
		return
	}
	thumbH = float32(rows) / total * viewH
	if thumbH < minScrollbarThumbH {
		thumbH = minScrollbarThumbH
	}
	thumbY = (float32(sbLen) - viewOffset) / total * viewH
	return
}

// scrollbarOffsetForY inverts scrollbarGeometry: given a desired thumb-top
// pixel y, it returns the fractional view offset (in rows, matching
// ViewOffset+ViewSubPx/cellH) that places the thumb there. Used for
// click-to-jump and thumb drag. Clamped to [0, sbLen]. Mirrors the thumbY
// formula: y = (sbLen - off)/total * viewH  ⇒  off = sbLen - y*total/viewH.
func scrollbarOffsetForY(sbLen, rows int, y, viewH float32) float32 {
	if viewH <= 0 || sbLen <= 0 ||
		math.IsNaN(float64(y)) || math.IsInf(float64(y), 0) {
		return 0
	}
	total := float32(sbLen + rows)
	off := float32(sbLen) - y*total/viewH
	if off < 0 {
		off = 0
	} else if off > float32(sbLen) {
		off = float32(sbLen)
	}
	return off
}

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

// runKey captures the rendering-relevant properties of a cell for
// run-coalescing in the foreground pass. Two cells with equal runKey
// can be drawn in a single dc.Text call.
//
// Hyperlink ID is deliberately not part of the key: adjacent cells
// belonging to different links but rendered with the same visual style
// (color, underline, typeface) can coalesce into one dc.Text call, and
// thus one go-glyph layout-cache entry. With OSC 8 streams like ls
// --hyperlink, every filename has a unique link ID — keying on it
// fragments runs that visually merge anyway. Hover-induced recolor
// already lives in the color field, so hovered vs non-hovered cells
// break the run correctly. Click hit-testing reads cell.LinkID
// directly via ViewCellAt and is unaffected.
type runKey struct {
	typeface      glyph.Typeface
	color         gui.Color
	ulColor       gui.Color
	ulStyle       uint8 // ulNone..ulDashed; drives underline rendering
	strikethrough bool
}

// cellRunKey computes the runKey for cell, applying attribute and
// hyperlink-hover color transforms. Must be called under grid.Mu.
// Link underline is always applied; hover recolor is gated on cmdHeld.
func cellRunKey(cell cell, base gui.TextStyle, g *grid, hoverR, hoverC int, cmdHeld bool) runKey {
	rawFG := g.fgOf(cell)
	color := rawFG
	if cell.Attrs&attrDim != 0 {
		color = gui.RGB(rawFG.R/2, rawFG.G/2, rawFG.B/2)
	}
	tf := base.Typeface
	bold, italic := cell.Attrs&attrBold != 0, cell.Attrs&attrItalic != 0
	if isGeometryGlyph(cell.Ch) {
		bold = false
	}
	switch {
	case bold && italic:
		tf = glyph.TypefaceBoldItalic
	case bold:
		tf = glyph.TypefaceBold
	case italic:
		tf = glyph.TypefaceItalic
	}
	ulStyle := cell.ULStyle
	ulColor := g.resolveColor(cell.ULColor, rawFG)
	if cell.LinkID != 0 {
		if ulStyle == ulNone {
			ulStyle = ulSingle
		}
		if cmdHeld && hoverR >= 0 && hoverC >= 0 {
			if g.ViewCellAt(hoverR, hoverC).LinkID == cell.LinkID {
				col := color
				color = gui.RGB(col.R/2, col.G/2, 255)
			}
		}
	}
	return runKey{
		color:         color,
		ulColor:       ulColor,
		typeface:      tf,
		ulStyle:       ulStyle,
		strikethrough: cell.Attrs&attrStrikethrough != 0,
	}
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

// resolveCell returns the cell at viewport (r, c), applying selection
// and search-highlight inversions. Uses the fast path (direct Cells
// index) when ds.live; otherwise goes through ViewCellAt.
func (ds *drawState) resolveCell(r, c int) cell {
	if ds.live {
		return ds.cells[r*ds.cols+c]
	}
	cell := ds.g.ViewCellAt(r, c)
	if ds.rowSel != nil {
		if rb := ds.rowSel[r]; rb.active && c >= rb.c0 && c <= rb.c1 {
			cell.Attrs ^= attrInverse
		}
	}
	if ds.vMatchesByRow != nil {
		for _, m := range ds.vMatchesByRow[r] {
			if c >= m.col && c < m.col+m.len {
				cell.Attrs ^= attrInverse
				break
			}
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
// row so the per-cell resolveCell path can apply attrInverse without
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

// drawBgPass paints background-color runs. One call to fillRun per
// contiguous same-color span. Skips DefaultBG cells (canvas already filled).
func (t *Term) drawBgPass(ds *drawState) {
	dc := ds.dc
	yOff := ds.renderYOff
	cols := ds.cols
	if ds.partialRow != nil {
		t.drawBgPrecomputed(dc, -1, ds.partialRow, yOff, cols, ds.g)
	}
	for r := range ds.renderRows {
		t.drawBgResolved(dc, r, yOff, ds)
	}
}

// drawBgPrecomputed coalesces background-color runs from a pre-resolved cell
// slice (partial row or BiDi-reordered row).
func (t *Term) drawBgPrecomputed(dc *gui.DrawContext, r int, row []cell, yOff float32, cols int, g *grid) {
	if len(row) == 0 {
		return
	}
	runStart := 0
	runColor := g.bgOf(row[0])
	for c := 1; c < cols; c++ {
		cur := g.bgOf(row[c])
		if cur != runColor {
			t.fillRun(dc, r, runStart, c, runColor, yOff)
			runStart = c
			runColor = cur
		}
	}
	t.fillRun(dc, r, runStart, cols, runColor, yOff)
}

// drawBgResolved coalesces background-color runs for a single row.
// Uses resolveVisual so BiDi-reordered rows and regular rows share one path.
func (t *Term) drawBgResolved(dc *gui.DrawContext, r int, yOff float32, ds *drawState) {
	g := ds.g
	cols := ds.cols
	runStart := 0
	runColor := g.bgOf(ds.resolveVisual(r, 0))
	for c := 1; c < cols; c++ {
		cur := g.bgOf(ds.resolveVisual(r, c))
		if cur != runColor {
			t.fillRun(dc, r, runStart, c, runColor, yOff)
			runStart = c
			runColor = cur
		}
	}
	t.fillRun(dc, r, runStart, cols, runColor, yOff)
}

// textBlinkOff reports whether SGR 5/6 text is in the hidden half of its blink
// cycle. The phase comes from the wall clock rather than a per-Term epoch so
// every pane in a window blinks in step, and so the phase does not restart on
// unrelated events (a keystroke resets the *cursor* epoch, not this one).
func textBlinkOff(now time.Time) bool {
	return (now.UnixNano()/int64(cursorBlinkPeriod))%2 == 1
}

// maskGlyph blanks a cell's glyph when SGR 8 (conceal) is set, or when SGR 5/6
// (blink) is set and the cycle is currently in its hidden half. Background,
// selection inversion and underline decoration are untouched — only the glyph
// disappears, matching xterm. Conceal must be honored: ncurses maps A_INVIS to
// SGR 8 and password prompts rely on it, so ignoring the attribute would show
// the typed secret.
func maskGlyph(c cell, blinkOff bool) cell {
	if c.Attrs&attrConceal != 0 || (blinkOff && c.Attrs&attrBlink != 0) {
		c.Ch = ' '
		c.clusterID = 0
	}
	return c
}

// drawFgPass paints foreground text, coalescing adjacent cells with identical
// visual style into single dc.Text calls. Wide chars break the run and emit
// individually. Continuation cells are skipped. Plain spaces extend same-style
// runs without starting new ones.
func (t *Term) drawFgPass(ds *drawState) {
	dc := ds.dc
	style := ds.style
	yOff := ds.renderYOff
	cols := ds.cols
	g := ds.g
	hR, hC := int(t.mouse.hoverR.Load()), int(t.mouse.hoverC.Load())
	cmdHeld := t.mouse.cmdHeld.Load()

	// sawBlink tracks whether any painted cell carries SGR 5/6, so the blink
	// ticker knows whether periodic repaints are needed at all. Recomputed
	// every frame: it clears itself once the blinking text scrolls away.
	sawBlink := false

	// Partial top row: per-cell emit, no run coalescing.
	if ds.partialRow != nil {
		partialY := -t.cellH + yOff
		for c := range cols {
			cell := ds.partialRow[c]
			if cell.Width == 0 && cell.Ch == 0 {
				continue
			}
			sawBlink = sawBlink || cell.Attrs&attrBlink != 0
			cell = maskGlyph(cell, ds.blinkOff)
			if cell.Ch == ' ' && cell.Attrs == 0 && cell.LinkID == 0 {
				continue
			}
			k := cellRunKey(cell, style, g, hR, hC, cmdHeld)
			t.emitCell(dc, float32(c)*t.cellW, partialY, cell, k, style)
		}
	}

	var fr flushState
	for r := range ds.renderRows {
		fr.open = false
		t.draw.runBuf.Reset()
		fr.cols = 0
		for c := range cols {
			cell := ds.resolveVisual(r, c)
			if cell.Width == 0 && cell.Ch == 0 {
				continue // continuation cell; skip without breaking run
			}
			sawBlink = sawBlink || cell.Attrs&attrBlink != 0
			cell = maskGlyph(cell, ds.blinkOff)
			k := cellRunKey(cell, style, g, hR, hC, cmdHeld)
			isPlainSpace := cell.Ch == ' ' && cell.Attrs == 0 && cell.LinkID == 0
			if cell.Width == 2 {
				t.flushRun(dc, r, style, yOff, &fr)
				t.emitCell(dc, float32(c)*t.cellW, float32(r)*t.cellH+yOff, cell, k, style)
				continue
			}
			// Non-ASCII glyphs may trigger font fallback with metrics
			// that differ from the monospace cellW measured via 'M'.
			// Accumulated drift inside a coalesced text run can cause
			// visual overlap with the next run. Emit individually so
			// each glyph stays pinned to its cell origin. Multi-codepoint
			// clusters (clusterID != 0) must also emit individually so the
			// full cluster string is drawn even when the base rune is ASCII.
			if cell.Ch > 0x7F || cell.clusterID != 0 {
				t.flushRun(dc, r, style, yOff, &fr)
				t.emitCell(dc, float32(c)*t.cellW, float32(r)*t.cellH+yOff, cell, k, style)
				continue
			}
			if isPlainSpace {
				if fr.open && k == fr.key {
					t.draw.runBuf.WriteRune(' ')
					fr.cols++
				} else {
					t.flushRun(dc, r, style, yOff, &fr)
				}
				continue
			}
			if fr.open && k == fr.key {
				t.draw.runBuf.WriteRune(cell.Ch)
				fr.cols++
			} else {
				t.flushRun(dc, r, style, yOff, &fr)
				fr.open = true
				fr.start = c
				fr.cols = 1
				fr.key = k
				t.draw.runBuf.WriteRune(cell.Ch)
			}
		}
		t.flushRun(dc, r, style, yOff, &fr)
	}
	t.blinkCells.Store(sawBlink)
}

// flushRun draws the accumulated text run as a single dc.Text call with
// optional underline decoration, then resets the run state.
func (t *Term) flushRun(dc *gui.DrawContext, r int, style gui.TextStyle, yOff float32, fr *flushState) {
	if !fr.open || t.draw.runBuf.Len() == 0 {
		fr.open = false
		return
	}
	text := t.draw.runBuf.String()
	// Trim trailing spaces when no decoration spans them: "abc   " and
	// "abc" share a layout-cache entry, so trimming keeps cache hits
	// stable as tail padding wobbles frame to frame.
	if fr.key.ulStyle == ulNone && !fr.key.strikethrough {
		text = strings.TrimRight(text, " ")
		if text == "" {
			fr.open = false
			t.draw.runBuf.Reset()
			fr.cols = 0
			return
		}
	}
	cs := style
	cs.Color = fr.key.color
	cs.Typeface = fr.key.typeface
	cs.Underline = false
	cs.Strikethrough = fr.key.strikethrough
	rowY := float32(r)*t.cellH + yOff
	dc.Text(float32(fr.start)*t.cellW, rowY, text, cs)
	if fr.key.ulStyle != ulNone {
		t.drawUnderlineDecor(dc,
			float32(fr.start)*t.cellW, rowY,
			float32(fr.cols)*t.cellW,
			fr.key.ulStyle, fr.key.ulColor)
	}
	fr.open = false
	t.draw.runBuf.Reset()
	fr.cols = 0
}

// drawIME renders the IME composition string at the cursor position. Fills
// the background under the composition, draws each rune, and underlines the
// full span. Populates ds.ime* fields for consumption by drawCursor.
func (t *Term) drawIME(ds *drawState) {
	if !t.ime.composing {
		return
	}
	g := ds.g
	ds.imeComposing = true
	ds.imeRunes = []rune(t.ime.compText)
	if len(ds.imeRunes) > imeCompRuneLimit {
		ds.imeRunes = ds.imeRunes[:imeCompRuneLimit]
	}
	ds.imeWidths = make([]int, len(ds.imeRunes))
	var totalCols int
	for i, r := range ds.imeRunes {
		w := max(runeWidth(r), 1)
		ds.imeWidths[i] = w
		totalCols += w
	}
	ds.imeCursor = min(t.ime.compCursor, len(ds.imeRunes))

	if len(ds.imeRunes) == 0 || g.CursorR >= ds.renderRows || g.ViewOffset != 0 || ds.renderYOff != 0 {
		return
	}
	startX := float32(g.CursorC) * t.cellW
	rowY := float32(g.CursorR)*t.cellH + ds.renderYOff

	bgCol := g.Theme.DefaultBG
	ds.dc.FilledRect(startX, rowY, float32(totalCols)*t.cellW, t.cellH, bgCol)

	cs := ds.style
	cs.Color = g.Theme.DefaultFG
	cs.Underline = false

	currX := startX
	for i, r := range ds.imeRunes {
		ds.dc.Text(currX, rowY, t.termRuneStr(r), cs)
		currX += float32(ds.imeWidths[i]) * t.cellW
	}
	t.drawUnderlineDecor(ds.dc, startX, rowY, float32(totalCols)*t.cellW, ulSingle, cs.Color)
}

// drawCursor renders the text cursor at the current grid position, honoring
// DECSCUSR shape, blink phase, scrollback state, and IME composition offset.
// When the cursor row has BiDi reordering the logical column is mapped to a
// visual column.
func (t *Term) drawCursor(ds *drawState) {
	g := ds.g
	if !g.CursorVisible || g.CursorR >= ds.renderRows || g.ViewOffset != 0 || ds.renderYOff != 0 {
		return
	}
	if t.cursorBlinkOff(ds.now) {
		return
	}
	cc := g.CursorC
	if ds.imeComposing {
		colOffset := 0
		for i := range ds.imeCursor {
			colOffset += ds.imeWidths[i]
		}
		cc = g.CursorC + colOffset
	}
	if cc >= ds.cols {
		cc = ds.cols - 1
	}
	// When the cursor's row has bidi reordering, find the visual column
	// that corresponds to the logical cursor column.
	if cr := g.CursorR; cr >= 0 && cr < ds.renderRows && ds.bidiV2LRows[cr] != nil {
		if !ds.imeComposing {
			for v, l := range ds.bidiV2LRows[cr] {
				if l == g.CursorC {
					cc = v
					break
				}
			}
		}
	}

	cursorCell := cell{Ch: ' '}
	if cell := g.At(g.CursorR, g.CursorC); cell != nil {
		// Masked like the fg pass: a block cursor redraws the glyph beneath
		// it, which would otherwise expose a concealed character.
		cursorCell = maskGlyph(*cell, ds.blinkOff)
	}
	if ds.imeComposing && ds.imeCursor >= 0 && ds.imeCursor < len(ds.imeRunes) {
		cursorCell.Ch = ds.imeRunes[ds.imeCursor]
	}
	t.drawCursorShape(ds.dc, cc, g.CursorR, cursorCell, g.cursorShape, ds.style)

	// Report cursor rect to the platform for candidate window placement.
	if ds.imeComposing && t.win != nil {
		imeX := t.ime.layoutX + float32(cc)*t.cellW
		imeY := t.ime.layoutY + float32(g.CursorR)*t.cellH
		t.win.IMESetRect(imeX, imeY, t.cellW, t.cellH)
	}
}

// drawOverlays paints the scrollbar thumb, search bar, and visual-bell flash.
// All three are drawn on top of the terminal content.
func (t *Term) drawOverlays(ds *drawState) {
	g := ds.g
	// Scrollbar: pill-shaped thumb on the right edge. Visible while scrolled
	// back or within scrollbarDuration of the last scroll event. Held visible
	// for the whole drag so releasing the thumb doesn't hide it mid-gesture.
	sb := g.Scrollback.Len()
	sw := t.effectiveScrollbarWidth()
	visible := ds.now.Before(t.scrollbar.until) || g.ViewOffset > 0 || g.ViewSubPx > 0 || t.scrollbar.dragging
	active := visible && sb > 0 && ds.dc.Width >= sw && sw > 0
	t.scrollbar.active = active
	if active {
		// Inset the thumb from the window's right edge only for panes flush
		// against it, so the thumb clears the OS window-resize band. The
		// clickable region extends inward (leftward) to scrollbarHitWidth so
		// the grabbable area stays wide even when the visual thumb is narrow.
		inset := t.scrollbarEdgeInset(ds.dc.Width)
		thumbX := ds.dc.Width - sw - inset
		viewOffsetVal := float32(g.ViewOffset) + g.ViewSubPx/t.cellH
		thumbY, thumbH := scrollbarGeometry(sb, g.Rows, viewOffsetVal, ds.dc.Height)
		thumbColor := gui.RGBA(128, 128, 128, 120)
		if t.scrollbar.hovered {
			thumbColor = gui.RGBA(180, 180, 180, 150)
		}
		ds.dc.FilledRoundedRect(thumbX, thumbY, sw, thumbH,
			sw/2, thumbColor)
		hitX0 := thumbX + sw - scrollbarHitWidth
		if hitX0 < 0 {
			hitX0 = 0
		}
		t.scrollbar.hitX0 = hitX0
		t.scrollbar.viewH = ds.dc.Height
	} else {
		t.scrollbar.hovered = false
	}

	if t.search.active {
		t.drawSearchBar(ds.dc, ds.style)
	}

	// Visual bell: a faint white wash that eases out over the flash
	// duration rather than switching on and off, so an incidental BEL
	// registers peripherally instead of strobing the whole pane.
	t.drawBellFlash(ds)
}

// drawBellFlash paints the visual-bell overlay at an alpha derived from how
// much of the flash duration remains, and schedules the next fade frame
// while the flash is still running. Main-thread only (called from
// drawOverlays), which is what makes the unsynchronized fadeTimer access
// safe.
func (t *Term) drawBellFlash(ds *drawState) {
	fu := t.bell.flashUntil.Load()
	if fu == 0 {
		return
	}
	remaining := fu - ds.now.UnixNano()
	if remaining <= 0 {
		return
	}
	total := t.bell.flashNanos.Load()
	if total <= 0 {
		return
	}
	// progress runs 0→1 across the flash; clamped because a BEL landing
	// between the Store of flashNanos and flashUntil can briefly make
	// remaining exceed total.
	progress := 1 - float64(remaining)/float64(total)
	if progress < 0 {
		progress = 0
	}
	// Ease out quadratically: near-peak at the leading edge where the eye
	// catches the event, then a long shallow tail instead of a cliff.
	fade := (1 - progress) * (1 - progress)
	alpha := uint8(float64(bellFlashPeakAlpha)*fade + 0.5)
	if alpha == 0 {
		return
	}
	ds.dc.FilledRect(0, 0, ds.dc.Width, ds.dc.Height,
		gui.RGBA(255, 255, 255, alpha))

	// Drive the next step of the fade. scheduleBellClear already covers the
	// final repaint that removes the overlay; this only fills in the frames
	// between, and stops on its own once remaining hits zero.
	next := time.Duration(remaining)
	if next > bellFadeFrame {
		next = bellFadeFrame
	}
	t.scheduleDelayedUpdate(next, &t.bell.fadeTimer)
}

// scrollbarEdgeInset returns the horizontal gap to leave between the drawn
// scrollbar thumb and the pane's right edge. It is scrollbarInset only when
// the pane is flush against the window's right edge (where the OS reserves a
// resize band); interior panes get 0. canvasW is the pane's canvas width;
// t.ime.layoutX is the pane's absolute left X (set by onAmendLayout).
func (t *Term) scrollbarEdgeInset(canvasW float32) float32 {
	if t.win == nil || !realNumber(canvasW) {
		return 0
	}
	winW, _ := t.win.WindowSize()
	if winW <= 0 {
		return 0
	}
	rightEdge := t.ime.layoutX + canvasW
	// 1px tolerance absorbs fractional layout/rounding at the window edge.
	if float32(winW)-rightEdge <= 1 {
		return scrollbarInset
	}
	return 0
}

// cursorBlinkOff reports whether the cursor is currently in the
// hidden half of its blink cycle. Returns false (always visible) for
// steady cursors. Caller holds grid.Mu.
func (t *Term) cursorBlinkOff(now time.Time) bool {
	if !t.cursorBlinks() {
		return false
	}
	elapsed := now.Sub(t.cursorEpoch)
	return (elapsed/cursorBlinkPeriod)%2 == 1
}

// drawCursorShape renders the cursor at viewport (row, col) using the
// current shape. Block inverts the cell (filled bg + cell glyph in
// fg's color); underline/bar overlay a thin filled rect on top of the
// regular foreground glyph already drawn in the foreground pass.
func (t *Term) drawCursorShape(dc *gui.DrawContext, col, row int, cell cell,
	shape cursorShape, style gui.TextStyle) {
	x := float32(col) * t.cellW
	y := float32(row) * t.cellH

	// Dim the cursor to 40% when the terminal doesn't have pane focus.
	opacity := float32(1.0)
	if !t.focused.Load() {
		opacity = 0.4
	}

	switch shape {
	case cursorUnderline:
		// Bottom-aligned bar 1/8th of the cell height (min 2px) so it
		// stays visible at smaller font sizes.
		h := t.cellH / 8
		if h < 2 {
			h = 2
		}
		dc.FilledRect(x, y+t.cellH-h, t.cellW, h,
			t.grid.fgOf(cell).WithOpacity(opacity))
	case cursorBar:
		w := t.cellW / 6
		if w < 2 {
			w = 2
		}
		dc.FilledRect(x, y, w, t.cellH,
			t.grid.fgOf(cell).WithOpacity(opacity))
	default: // cursorBlock
		fillColor := t.grid.fgOf(cell)
		if t.grid.CursorColor != DefaultColor {
			fillColor = rgbToGUIColor(t.grid.CursorColor)
		}
		dc.FilledRect(x, y, t.cellW, t.cellH, fillColor.WithOpacity(opacity))
		cs := style
		cs.Color = t.grid.bgOf(cell)
		cs.EmojiBoxWidth = float32(cell.Width) * t.cellW
		dc.Text(x, y, t.cellText(cell), cs)
	}
}

// drawUnderlineDecor renders underline decorations for a text run.
// x,y are the top-left of the run; w is its pixel width. Handles all
// ULStyle values including ulSingle (drawn as a rect so ulColor is honored).
func (t *Term) drawUnderlineDecor(dc *gui.DrawContext, x, y, w float32, ulStyle uint8, ulColor gui.Color) {
	thick := t.cellH / 14
	if thick < 1 {
		thick = 1
	}
	baseY := y + t.cellH - 2*thick - 1
	switch ulStyle {
	case ulSingle:
		dc.FilledRect(x, baseY, w, thick, ulColor)
	case ulDouble:
		dc.FilledRect(x, baseY-thick-1, w, thick, ulColor)
		dc.FilledRect(x, baseY, w, thick, ulColor)
	case ulCurly:
		// Approximate curly as alternating up/down segments.
		seg := t.cellW * 2
		if seg < 4 {
			seg = 4
		}
		xi := x
		up := true
		for xi < x+w {
			ww := seg
			if xi+ww > x+w {
				ww = x + w - xi
			}
			yy := baseY
			if up {
				yy = baseY - thick - 1
			}
			dc.FilledRect(xi, yy, ww, thick, ulColor)
			xi += ww
			up = !up
		}
	case ulDotted:
		step := thick * 3
		if step < 3 {
			step = 3
		}
		xi := x
		for xi+thick <= x+w {
			dc.FilledRect(xi, baseY, thick, thick, ulColor)
			xi += step
		}
	case ulDashed:
		dash := t.cellW * 3
		if dash < 6 {
			dash = 6
		}
		gap := dash / 2
		xi := x
		for xi < x+w {
			ww := dash
			if xi+ww > x+w {
				ww = x + w - xi
			}
			dc.FilledRect(xi, baseY, ww, thick, ulColor)
			xi += dash + gap
		}
	}
}

func (t *Term) emitCell(dc *gui.DrawContext, x, y float32, cell cell, k runKey, base gui.TextStyle) {
	cs := base
	cs.Color = k.color
	cs.Typeface = k.typeface
	cs.Underline = false
	cs.Strikethrough = k.strikethrough
	// Tell go-glyph the cell box this glyph occupies so color/emoji fill the
	// full reserved width (e.g. a width-2 emoji fills 2 cells) instead of the
	// font's narrower natural emoji advance. Ignored for non-color glyphs.
	cs.EmojiBoxWidth = float32(cell.Width) * t.cellW
	dc.Text(x, y, t.cellText(cell), cs)
	if k.ulStyle != ulNone {
		t.drawUnderlineDecor(dc, x, y, float32(cell.Width)*t.cellW, k.ulStyle, k.ulColor)
	}
}

func (t *Term) fillRun(dc *gui.DrawContext, row, c0, c1 int, color gui.Color, yOff float32) {
	if color == t.grid.Theme.DefaultBG {
		return // canvas already painted with default bg.
	}
	x := float32(c0) * t.cellW
	y := float32(row)*t.cellH + yOff
	w := float32(c1-c0) * t.cellW
	dc.FilledRect(x, y, w, t.cellH, color)
}

// drawSearchBar paints a status bar over the bottom cellH pixels of the
// canvas showing the active search query. Called under Mu (inside onDraw).
func (t *Term) drawSearchBar(dc *gui.DrawContext, style gui.TextStyle) {
	y := dc.Height - t.cellH
	noMatch := t.search.query != "" && len(t.search.matches) == 0
	bgColor := gui.RGB(40, 40, 90)
	if noMatch {
		bgColor = gui.RGB(90, 20, 20)
	}
	dc.FilledRect(0, y, dc.Width, dc.Height-y, bgColor)
	var label string
	switch {
	case t.search.regex && t.search.reErr != nil:
		label = "/re/ " + t.search.query + " [invalid]▌"
	case t.search.regex:
		label = "/re/ " + t.search.query + "▌"
	default:
		label = "Find (^R=regex): " + t.search.query + "▌"
	}
	cs := style
	cs.Color = gui.RGB(220, 220, 220)
	cs.Typeface = glyph.TypefaceRegular
	dc.Text(0, y, label, cs)
}
