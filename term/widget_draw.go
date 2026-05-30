package term

import (
	"log"
	"math"
	"strings"
	"time"

	glyph "github.com/mike-ward/go-glyph"
	"github.com/mike-ward/go-gui/gui"
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
func scrollbarGeometry(sbLen, rows int, viewOffset float32, viewH float32) (thumbY, thumbH float32) {
	total := float32(sbLen + rows)
	if total <= 0 {
		return
	}
	thumbH = float32(rows) / total * viewH
	thumbY = (float32(sbLen) - viewOffset) / total * viewH
	return
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
	color         gui.Color
	ulColor       gui.Color
	typeface      glyph.Typeface
	ulStyle       uint8 // ulNone..ulDashed; drives underline rendering
	strikethrough bool
}

// cellRunKey computes the runKey for cell, applying attribute and
// hyperlink-hover color transforms. Must be called under grid.Mu.
func cellRunKey(cell cell, base gui.TextStyle, g *grid, hoverR, hoverC int) runKey {
	rawFG := g.Theme.fg(cell)
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
	ulColor := g.Theme.resolve(cell.ULColor, rawFG)
	if cell.LinkID != 0 {
		if ulStyle == ulNone {
			ulStyle = ulSingle
		}
		if hoverR >= 0 && hoverC >= 0 {
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

	var doResize bool // set when grid.Resize fires; pty.Resize deferred to after Mu unlock
	func() {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		if rows != t.grid.Rows || cols != t.grid.Cols {
			now := time.Now()
			if rows != t.resize.pendingRows ||
				cols != t.resize.pendingCols ||
				t.resize.pendingSince.IsZero() {
				t.resize.pendingRows = rows
				t.resize.pendingCols = cols
				t.resize.pendingSince = now
			}
			if elapsed := now.Sub(t.resize.pendingSince); elapsed >= resizeDebounce {
				t.grid.Resize(rows, cols)
				doResize = true
				t.resize.pendingSince = time.Time{}
			} else {
				// Schedule a wake so the apply still happens after the
				// mouse stops moving (no further frame events would fire).
				t.scheduleResizeWake(resizeDebounce - elapsed)
			}
		} else if !t.resize.pendingSince.IsZero() {
			t.resize.pendingSince = time.Time{}
		}
		// Publish cell size in device pixels so image footprint math matches the
		// device-pixel dimensions stored in image files. dc.Scale is the backing
		// scale factor (2.0 on Retina); cellW/cellH are in logical points.
		scale := dc.Scale
		if scale == 0 {
			scale = 1
		}
		t.grid.CellPxW = t.cellW * scale
		t.grid.CellPxH = t.cellH * scale
		t.grid.ClearDirty()

		// Fast path: live viewport with no selection and no active search reads
		// directly from the cell buffer, skipping ViewOffset / scrollback
		// branches and per-cell InSelection / search-match work.
		g := t.grid
		rows, cols = g.Rows, g.Cols
		renderYOff := g.ViewSubPx
		live := g.ViewOffset == 0 && renderYOff == 0 && !g.SelActive && !t.search.active
		cells := g.Cells

		// Pre-map search matches and selection to viewport rows to avoid O(N)
		// checks inside the per-cell loop.
		var vMatchesByRow [][]vMatch
		if t.search.active && t.search.query != "" {
			if cap(t.draw.vMatchBuf) < rows {
				t.draw.vMatchBuf = make([][]vMatch, rows)
			} else {
				t.draw.vMatchBuf = t.draw.vMatchBuf[:rows]
				for i := range t.draw.vMatchBuf {
					t.draw.vMatchBuf[i] = t.draw.vMatchBuf[i][:0]
				}
			}
			vMatchesByRow = t.draw.vMatchBuf
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
				if vr, ok := g.ContentRowToViewport(m.Row); ok && vr < rows {
					vMatchesByRow[vr] = append(vMatchesByRow[vr], vMatch{m.Col, m.Len})
				}
			}
		}

		var rowSel []rowBounds
		if g.SelActive {
			if cap(t.draw.selBuf) < rows {
				t.draw.selBuf = make([]rowBounds, rows)
			} else {
				t.draw.selBuf = t.draw.selBuf[:rows]
				clear(t.draw.selBuf)
			}
			rowSel = t.draw.selBuf
			s, e := g.selOrder()
			for r := range rows {
				cr := g.viewportToContent(r)
				if cr < s.Row || cr > e.Row {
					continue
				}
				c0, c1 := 0, cols-1
				if cr == s.Row {
					c0 = s.Col
				}
				if cr == e.Row {
					c1 = e.Col
				}
				rowSel[r] = rowBounds{c0, c1, true}
			}
		}

		resolveCell := func(r, c int) cell {
			if live {
				return cells[r*cols+c]
			}
			cell := g.ViewCellAt(r, c)
			if rowSel != nil {
				if rb := rowSel[r]; rb.active && c >= rb.c0 && c <= rb.c1 {
					cell.Attrs ^= attrInverse
				}
			}
			if vMatchesByRow != nil {
				for _, m := range vMatchesByRow[r] {
					if c >= m.col && c < m.col+m.len {
						cell.Attrs ^= attrInverse
						break
					}
				}
			}
			return cell
		}

		// When the search bar is active, reserve grid rows whose text
		// footprint (shifted by sub-pixel scroll) overlaps the search
		// bar's pixel region. go-gui renders all Text on top of all
		// FilledRects, so terminal text in that region would paint over
		// the search bar background.
		renderRows := rows
		if t.search.active {
			renderRows -= searchOverlap(t.cellH, renderYOff, dc.Height, rows)
			if renderRows < 0 {
				renderRows = 0
			}
		}

		// BiDi pre-pass: compute visual-reordered rows for any viewport row
		// containing RTL characters. For live LTR-only terminals (the common
		// case) rowHasRTL returns false immediately — zero allocations.
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
		for r := range renderRows {
			var hasRTL bool
			if live {
				hasRTL = rowHasRTL(cells[r*cols:(r+1)*cols], cols)
			} else {
				for c := range cols {
					if isRTLRune(g.ViewCellAt(r, c).Ch) {
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
				t.draw.bidiScratch[c] = resolveCell(r, c)
			}
			t.draw.bidiVisRows[r], t.draw.bidiV2LRows[r] = visualReorder(t.draw.bidiScratch, cols)
		}
		resolveVisual := func(r, c int) cell {
			if t.draw.bidiVisRows[r] != nil {
				return t.draw.bidiVisRows[r][c]
			}
			return resolveCell(r, c)
		}

		// Resolve partial top row once for both bg and fg passes.
		// Nil when there is no scrollback row above the current viewport.
		var partialRow []cell
		if renderYOff > 0 {
			partialRow = g.partialTopRow()
			if partialRow != nil && rowHasRTL(partialRow, cols) {
				if vis, _ := visualReorder(partialRow, cols); vis != nil {
					partialRow = vis
				}
			}
		}

		// Background pass: optional partial top row (renders at y = -cellH + renderYOff
		// so only its bottom renderYOff pixels are visible; canvas clips y < 0), then
		// all regular rows.
		if partialRow != nil {
			runStart := 0
			runColor := g.Theme.bg(partialRow[0])
			for c := 1; c < cols; c++ {
				cur := g.Theme.bg(partialRow[c])
				if cur != runColor {
					t.fillRun(dc, -1, runStart, c, runColor, renderYOff)
					runStart = c
					runColor = cur
				}
			}
			t.fillRun(dc, -1, runStart, cols, runColor, renderYOff)
		}
		for r := range renderRows {
			runStart := 0
			runColor := g.Theme.bg(resolveVisual(r, 0))
			for c := 1; c < cols; c++ {
				cur := g.Theme.bg(resolveVisual(r, c))
				if cur != runColor {
					t.fillRun(dc, r, runStart, c, runColor, renderYOff)
					runStart = c
					runColor = cur
				}
			}
			t.fillRun(dc, r, runStart, cols, runColor, renderYOff)
		}

		// Foreground pass: coalesce adjacent cells with identical style into a
		// single dc.Text call. Wide chars break the run and are emitted
		// individually (their glyph spans two columns). Continuation cells
		// (right half of a wide char, Width==0 Ch==0) are skipped without
		// breaking the current run. Plain spaces with no attrs or link don't
		// start a new run but extend an existing same-style one.
		hR, hC := int(t.mouse.hoverR.Load()), int(t.mouse.hoverC.Load())
		t.draw.runBuf.Reset()
		var (
			runStart int
			runCols  int // columns spanned by the open run (for underline width)
			runStyle runKey
			runOpen  bool
		)
		flushRun := func(r int) {
			if !runOpen || t.draw.runBuf.Len() == 0 {
				runOpen = false
				return
			}
			text := t.draw.runBuf.String()
			// Trim trailing spaces when no decoration spans them: "abc   " and
			// "abc" share a layout-cache entry, so trimming keeps cache hits
			// stable as tail padding wobbles frame to frame.
			if runStyle.ulStyle == ulNone && !runStyle.strikethrough {
				text = strings.TrimRight(text, " ")
				if text == "" {
					runOpen = false
					t.draw.runBuf.Reset()
					runCols = 0
					return
				}
			}
			cs := style
			cs.Color = runStyle.color
			cs.Typeface = runStyle.typeface
			cs.Underline = false
			cs.Strikethrough = runStyle.strikethrough
			rowY := float32(r)*t.cellH + renderYOff
			dc.Text(float32(runStart)*t.cellW, rowY, text, cs)
			if runStyle.ulStyle != ulNone {
				t.drawUnderlineDecor(dc,
					float32(runStart)*t.cellW, rowY,
					float32(runCols)*t.cellW,
					runStyle.ulStyle, runStyle.ulColor)
			}
			runOpen = false
			t.draw.runBuf.Reset()
			runCols = 0
		}
		// Partial top row foreground pass: per-cell (no run coalescing).
		if partialRow != nil {
			partialY := -t.cellH + renderYOff
			for c := range cols {
				cell := partialRow[c]
				if cell.Width == 0 && cell.Ch == 0 {
					continue
				}
				if cell.Ch == ' ' && cell.Attrs == 0 && cell.LinkID == 0 {
					continue
				}
				k := cellRunKey(cell, style, g, hR, hC)
				t.emitCell(dc, float32(c)*t.cellW, partialY, cell, k, style)
			}
		}
		for r := range renderRows {
			runOpen = false
			t.draw.runBuf.Reset()
			runCols = 0
			for c := range cols {
				cell := resolveVisual(r, c)
				if cell.Width == 0 && cell.Ch == 0 {
					continue // continuation cell; skip without breaking run
				}
				k := cellRunKey(cell, style, g, hR, hC)
				isPlainSpace := cell.Ch == ' ' && cell.Attrs == 0 && cell.LinkID == 0
				if cell.Width == 2 {
					flushRun(r)
					t.emitCell(dc, float32(c)*t.cellW, float32(r)*t.cellH+renderYOff, cell, k, style)
					continue
				}
				if isPlainSpace {
					if runOpen && k == runStyle {
						t.draw.runBuf.WriteRune(' ')
						runCols++
					} else {
						flushRun(r)
					}
					continue
				}
				if runOpen && k == runStyle {
					t.draw.runBuf.WriteRune(cell.Ch)
					runCols++
				} else {
					flushRun(r)
					runOpen = true
					runStart = c
					runCols = 1
					runStyle = k
					t.draw.runBuf.WriteRune(cell.Ch)
				}
			}
			flushRun(r)
		}

		// Graphics pass: paint decoded Sixel (or other) images on top of the
		// background fill, under the cursor. Cells covered by an image are
		// blanked at AddGraphic time so the text passes wrote nothing there.
		// Each image's content-row origin maps to a viewport row via
		// ContentRowToViewport; off-screen graphics are skipped.
		if len(g.Graphics) > 0 {
			for _, gr := range g.Graphics {
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

		now := time.Now()

		// Cursor: shape per DECSCUSR (block / underline / bar). Suppress
		// entirely when DEC ?25 has hidden it OR when the viewport is
		// scrolled back into history. Honor blink-off half-cycle when
		// blinking is enabled.
		if g.CursorVisible && g.CursorR < renderRows && g.ViewOffset == 0 && renderYOff == 0 && !t.cursorBlinkOff(now) {
			cc := g.CursorC
			if cc >= cols {
				cc = cols - 1
			}
			// When the cursor's row has bidi reordering, find the visual column
			// that corresponds to the logical cursor column.
			if cr := g.CursorR; cr >= 0 && cr < renderRows && t.draw.bidiV2LRows[cr] != nil {
				for v, l := range t.draw.bidiV2LRows[cr] {
					if l == g.CursorC {
						cc = v
						break
					}
				}
			}
			if cell := g.At(g.CursorR, g.CursorC); cell != nil {
				t.drawCursor(dc, cc, g.CursorR, *cell, g.cursorShape, style)
			}
		}

		// Scrollbar: pill-shaped thumb on the right edge. Visible while scrolled
		// back or within scrollbarDuration of the last scroll event. Drawn before
		// the search bar so the thumb doesn't overdraw it.
		sb := g.Scrollback.Len()
		if (now.Before(t.scrollbar.until) || g.ViewOffset > 0 || g.ViewSubPx > 0) && sb > 0 && dc.Width >= scrollbarWidth {
			viewOffsetVal := float32(g.ViewOffset) + g.ViewSubPx/t.cellH
			thumbY, thumbH := scrollbarGeometry(sb, g.Rows, viewOffsetVal, dc.Height)
			dc.FilledRoundedRect(dc.Width-scrollbarWidth, thumbY, scrollbarWidth, thumbH,
				scrollbarWidth/2, gui.RGBA(128, 128, 128, 120))
		}

		if t.search.active {
			t.drawSearchBar(dc, style)
		}

		// Visual bell: brief semi-transparent overlay that fades within bellFlashDuration.
		if now.Before(t.bell.flashUntil) {
			dc.FilledRect(0, 0, dc.Width, dc.Height, gui.RGBA(255, 255, 255, 40))
		}

	}()

	// Resize the pty outside the lock: the ioctl can block if the pty fd
	// is in a degraded state, and holding Mu would stall readLoop.
	if doResize {
		if err := t.pty.Resize(rows, cols); err != nil {
			log.Printf("term: pty resize: %v", err)
		}
	}
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

// drawCursor renders the cursor at viewport (row, col) using the
// current shape. Block inverts the cell (filled bg + cell glyph in
// fg's color); underline/bar overlay a thin filled rect on top of the
// regular foreground glyph already drawn in the foreground pass.
func (t *Term) drawCursor(dc *gui.DrawContext, col, row int, cell cell,
	shape cursorShape, style gui.TextStyle) {
	x := float32(col) * t.cellW
	y := float32(row) * t.cellH
	switch shape {
	case cursorUnderline:
		// Bottom-aligned bar 1/8th of the cell height (min 2px) so it
		// stays visible at smaller font sizes.
		h := t.cellH / 8
		if h < 2 {
			h = 2
		}
		dc.FilledRect(x, y+t.cellH-h, t.cellW, h, t.grid.Theme.fg(cell))
	case cursorBar:
		w := t.cellW / 6
		if w < 2 {
			w = 2
		}
		dc.FilledRect(x, y, w, t.cellH, t.grid.Theme.fg(cell))
	default: // cursorBlock
		fillColor := t.grid.Theme.fg(cell)
		if t.grid.CursorColor != DefaultColor {
			fillColor = gui.RGB(uint8(t.grid.CursorColor>>16), uint8(t.grid.CursorColor>>8), uint8(t.grid.CursorColor))
		}
		dc.FilledRect(x, y, t.cellW, t.cellH, fillColor)
		cs := style
		cs.Color = t.grid.Theme.bg(cell)
		dc.Text(x, y, t.termRuneStr(cell.Ch), cs)
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
	dc.Text(x, y, t.termRuneStr(cell.Ch), cs)
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
