package term

import (
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// Put writes a single rune at the cursor with current attrs and advances.
// It is the immediate, non-clustering path used by tests and direct callers;
// the parser's streaming input goes through PutRune/FlushGrapheme instead.
// Honors east-asian wide / emoji widths via runeWidth; width-0 runes
// (combining marks, ZWJ, etc.) are dropped.
func (g *grid) Put(ch rune) {
	ch = g.translateRune(ch)
	w := runeWidth(ch)
	if w == 0 {
		return
	}
	g.putCell(ch, 0, w)
}

// PutRune feeds one rune of streaming input into the grapheme assembler.
// Runes are accumulated into g.gphBuf and a leading orthographic syllable is
// committed to the grid only once its boundary is observed (i.e. when the
// next rune cannot extend it). The trailing, possibly-incomplete syllable
// stays pending until a boundary, FlushGrapheme, or end of Feed. Callers must
// FlushGrapheme before any cursor/erase/report operation so the cursor
// position reflects committed cells.
func (g *grid) PutRune(r rune) {
	r = g.translateRune(r)
	// Fast path for runs of printable ASCII (the common case). A pending
	// single ASCII-printable rune is a complete width-1 cluster the moment
	// another ASCII-printable rune arrives: ASCII printable never extends a
	// prior cluster nor is extended by a following ASCII printable. Commit it
	// directly and keep the new rune pending, skipping the uniseg segmenter.
	if len(g.gphBuf) == 1 && g.gphBuf[0] >= 0x20 && g.gphBuf[0] < 0x7f &&
		r >= 0x20 && r < 0x7f {
		g.putCell(rune(g.gphBuf[0]), 0, 1)
		g.gphBuf[0] = byte(r)
		return
	}
	g.gphBuf = utf8.AppendRune(g.gphBuf, r)
	g.drainAksharas(false)
}

// FlushGrapheme commits any pending orthographic syllable(s) to the grid.
// Called before control sequences (so DSR/CPR see the advanced cursor) and at
// the end of a Feed batch (so a trailing syllable renders without lag).
func (g *grid) FlushGrapheme() {
	g.drainAksharas(true)
}

// drainAksharas commits leading orthographic syllables from g.gphBuf. A
// syllable is one or more uniseg grapheme clusters fused across a virama
// (Indic/Brahmic conjunct) — see leadingAkshara. When flush is false the
// trailing, still-extendable syllable stays pending in g.gphBuf; when flush
// is true (end of input) it is committed as-is.
func (g *grid) drainAksharas(flush bool) {
	for len(g.gphBuf) > 0 {
		n, width, complete := leadingAkshara(g.gphBuf)
		if n == 0 || (!complete && !flush) {
			return // nothing committable yet; keep pending
		}
		g.commitCluster(g.gphBuf[:n], width)
		m := copy(g.gphBuf, g.gphBuf[n:])
		g.gphBuf = g.gphBuf[:m]
	}
}

// leadingAkshara reports the byte length n and display width of the leading
// orthographic syllable in b. uniseg supplies grapheme-cluster boundaries;
// clusters joined by a virama (optionally followed by ZWJ) are fused into one
// syllable so a Brahmic conjunct such as "ꦏ꧀ꦏ" occupies a single cell group.
// The syllable's width is uniseg's for non-Brahmic text (emoji, CJK, flags,
// variation selectors) but recomputed by brahmicWidth for syllables carrying
// a virama or spacing mark, since uniseg's per-rune widths diverge from the
// terminal-cell model (wcwidth wcswidth / ucs-detect): it widths a dead
// consonant base+virama at 2 (model: 1) and a base+spacing-mark at 1 in some
// scripts (model: 2).
//
// complete is true only when a grapheme boundary is known to follow the
// syllable — i.e. it cannot grow with more input. While false the caller
// keeps the bytes pending (or commits them if at end of input).
func leadingAkshara(b []byte) (n, width int, complete bool) {
	for {
		cluster, rest, w, _ := uniseg.FirstGraphemeCluster(b[n:], -1)
		if len(cluster) == 0 {
			break // no complete cluster (empty buffer)
		}
		n += len(cluster)
		width += w
		if len(rest) == 0 {
			// Cluster consumed the buffer; uniseg cannot confirm a boundary
			// past it, so the syllable may still grow. Keep pending.
			break
		}
		if clusterFusesRight(cluster) {
			continue // virama (or virama+ZWJ): fuse the following cluster
		}
		complete = true
		break // self-contained cluster with a boundary after it
	}
	if brahmic, w := brahmicWidth(b[:n]); brahmic {
		width = w
	}
	return n, width, complete
}

// clusterFusesRight reports whether a grapheme cluster should fuse with the
// next one to form a single Brahmic syllable: it ends in a virama, or in a
// virama immediately followed by a ZWJ (the explicit-conjunct request used in
// Bengali/Malayalam/Devanagari, e.g. "र्‍या" = RA, virama, ZWJ, YA, sign AA).
func clusterFusesRight(cluster []byte) bool {
	r, sz := utf8.DecodeLastRune(cluster)
	if r == 0x200D && sz < len(cluster) { // trailing ZWJ over a virama
		r, _ = utf8.DecodeLastRune(cluster[:len(cluster)-sz])
	}
	return isVirama(r)
}

// brahmicWidth computes the terminal cell width of an orthographic syllable
// under the wcwidth wcswidth model, returning brahmic=false (and width 0) when
// b carries neither a virama nor a spacing combining mark — in which case the
// caller keeps uniseg's width. The model: a virama is zero-width but caps the
// conjunct it forms with the following consonant at 2 cells; a spacing mark
// (category Mc) forces the syllable to 2 cells; ZWJ preserves a pending
// virama, ZWNJ breaks it; non-spacing marks contribute nothing.
func brahmicWidth(b []byte) (brahmic bool, width int) {
	prevVirama := false
	for i := 0; i < len(b); {
		if b[i] < utf8.RuneSelf {
			width += 1 // an ASCII base, e.g. a digit between marks
			i++
			continue
		}
		r, sz := utf8.DecodeRune(b[i:])
		i += sz
		switch {
		case isVirama(r):
			brahmic = true
			prevVirama = true
		case r == 0x200D: // ZWJ: consumed, conjunct intent preserved
		case r == 0x200C: // ZWNJ: breaks the conjunct
			prevVirama = false
		case unicode.Is(unicode.Mc, r):
			brahmic = true
			if width > 0 {
				width = 2
			}
			prevVirama = false
		default:
			w := runeWidth(r)
			if w == 0 { // non-spacing combining mark
				prevVirama = false
				continue
			}
			switch {
			case prevVirama:
				width = 2
			case width == 0:
				width = w
			default:
				width += w
			}
			prevVirama = false
		}
	}
	if width > 2 {
		width = 2
	}
	return brahmic, width
}

// isVirama reports whether r is a Brahmic virama / halant / pangkon — the
// dead-consonant sign that forms a conjunct with the following consonant.
// Set per wcwidth's _ISC_VIRAMA_SET (Unicode general category derivation);
// matching it lets leadingAkshara fuse conjuncts across scripts (Devanagari,
// Tamil, Myanmar, Khmer, Balinese, Javanese, Brahmi-derived, …).
func isVirama(r rune) bool {
	switch r {
	case 0x094D, 0x09CD, 0x0A4D, 0x0ACD, 0x0B4D, 0x0BCD, 0x0C4D, 0x0CCD,
		0x0D4D, 0x0DCA, 0x1039, 0x17D2, 0x1A60, 0x1B44, 0x1BAB, 0xA806,
		0xA8C4, 0xA9C0, 0xAAF6, 0x10A3F, 0x11046, 0x110B9, 0x11133,
		0x111C0, 0x11235, 0x1134D, 0x113D0, 0x11442, 0x114C2, 0x115BF,
		0x1163F, 0x116B6, 0x11839, 0x1193E, 0x119E0, 0x11A47, 0x11A99,
		0x11C3F, 0x11D45, 0x11D97, 0x11F42:
		return true
	}
	return false
}

// commitCluster writes one grapheme cluster (UTF-8 bytes b, display width
// width as measured by uniseg) to the grid at the cursor. Single-rune
// clusters store the rune directly in cell.Ch; multi-rune clusters intern
// the string and store the resulting clusterID. Width-0 clusters (a lone
// combining/format run with no spacing base — only possible when split
// across Feed calls) are dropped, matching legacy behavior.
func (g *grid) commitCluster(b []byte, width int) {
	if width <= 0 {
		return
	}
	if width > 2 {
		width = 2
	}
	base, sz := utf8.DecodeRune(b)
	var cid uint16
	if sz != len(b) {
		cid = g.internCluster(string(b))
	}
	g.putCell(base, cid, width)
}

// internCluster returns the clusterID for s, allocating one on first sight.
// Returns 0 (degrade to base rune) when the pool is exhausted.
func (g *grid) internCluster(s string) uint16 {
	if id, ok := g.clusterIDs[s]; ok {
		return id
	}
	if len(g.clusters) >= maxClusters {
		return 0
	}
	if g.clusterIDs == nil {
		g.clusterIDs = make(map[string]uint16, 64)
		g.clusters = make([]string, 1, 64) // index 0 reserved (clusterID 0 = none)
	}
	id := uint16(len(g.clusters))
	g.clusters = append(g.clusters, s)
	g.clusterIDs[s] = id
	return id
}

// putCell writes a glyph (base rune ch, optional clusterID, display width w)
// at the cursor with current attrs and advances. Wraps to the next line at
// the right margin; scrolls up at bottom. A width-2 glyph occupies the
// current cell and the cell to its right (the "continuation"), wrapping early
// if only one column remains. Width and any clustering are resolved by the
// caller; this is the shared write path for Put and commitCluster.
func (g *grid) putCell(ch rune, clusterID uint16, w int) {
	justWrapped := false
	if !g.AutoWrap {
		if g.CursorC >= g.Cols {
			g.CursorC = g.Cols - 1
		}
		if w == 2 && g.CursorC+1 >= g.Cols {
			w = 1
		}
	} else if g.CursorC >= g.Cols {
		g.RowWrapped[g.CursorR] = true
		g.Newline()
		g.CursorC = 0
		justWrapped = true
	}

	// Guard with justWrapped: if we already wrapped once (cursor is now at
	// col 0), the wide-char check would fire again when Cols==1, producing a
	// second spurious Newline for a single Put call.
	if !justWrapped && g.AutoWrap && w == 2 && g.CursorC+1 >= g.Cols {
		if c := g.At(g.CursorR, g.CursorC); c != nil {
			*c = blankCell(g.CurFG, g.CurBG, g.CurAttrs)
		}
		g.RowWrapped[g.CursorR] = true
		g.Newline()
		g.CursorC = 0
	}

	if g.InsertMode {
		g.InsertChars(w)
	}
	g.eraseWideAt(g.CursorR, g.CursorC)
	if w == 2 {
		g.eraseWideAt(g.CursorR, g.CursorC+1)
	}
	head := cell{
		Ch: ch, FG: g.CurFG, BG: g.CurBG,
		Attrs: g.CurAttrs, Width: uint8(w), LinkID: g.CurLinkID,
		ULStyle: g.CurULStyle, ULColor: g.CurULColor,
		clusterID: clusterID,
	}
	if c := g.At(g.CursorR, g.CursorC); c != nil {
		*c = head
	}
	if w == 2 {
		if c := g.At(g.CursorR, g.CursorC+1); c != nil {
			*c = head.continuation()
		}
	}
	g.markDirty(g.CursorR)
	g.CursorC += w
	if !g.AutoWrap && g.CursorC >= g.Cols {
		g.CursorC = g.Cols - 1
	}
}

// eraseWideAt sanitizes the wide-char pair (if any) covering (r,c) so
// a subsequent overwrite doesn't leave half a glyph behind. If (r,c)
// is a wide head, blanks the continuation to its right. If it's a
// continuation, blanks the head to its left. No-op for normal cells.
func (g *grid) eraseWideAt(r, c int) {
	cell := g.At(r, c)
	if cell == nil {
		return
	}
	switch {
	case cell.Width == 2:
		if right := g.At(r, c+1); right != nil &&
			right.Width == 0 && right.Ch == 0 {
			*right = defaultCell()
		}
	case cell.Width == 0 && cell.Ch == 0:
		if left := g.At(r, c-1); left != nil && left.Width == 2 {
			*left = defaultCell()
		}
	}
}

// Newline moves to next row, scrolling the region if needed. Column
// unchanged (LF only); shells emit CRLF. When the cursor sits on the
// scroll region's Bottom row, scrollUpRegion is invoked so apps that
// shrink the active area (less, vim status line) don't blow away
// untouched rows below. When the cursor is below Bottom (outside the
// region), it advances toward Rows-1 without scrolling.
func (g *grid) Newline() {
	switch {
	case g.CursorR == g.Bottom:
		g.scrollUpRegion(1)
	case g.CursorR+1 < g.Rows:
		g.markDirty(g.CursorR)
		g.CursorR++
		g.markDirty(g.CursorR)
	}
}

// NextLine implements ESC E (NEL): CR + LF.
func (g *grid) NextLine() {
	g.CarriageReturn()
	g.Newline()
}

// CarriageReturn moves to column 0.
func (g *grid) CarriageReturn() { g.CursorC = 0 }

// Backspace moves cursor left one column. No erase.
func (g *grid) Backspace() {
	if g.CursorC > 0 {
		g.CursorC--
	}
}

// Tab advances the cursor to the next tab stop. Scans TabStops from
// CursorC+1; if no stop exists within the row, clamps to Cols-1.
func (g *grid) Tab() {
	if g.CursorC < 0 {
		g.CursorC = 0
	}
	for c := g.CursorC + 1; c < g.Cols; c++ {
		if g.TabStops[c] {
			g.CursorC = c
			return
		}
	}
	g.CursorC = g.Cols - 1
}

// SetTabStop sets a tab stop at the current cursor column. Implements ESC H (HTS).
func (g *grid) SetTabStop() {
	if g.CursorC >= 0 && g.CursorC < MaxGridDim {
		g.TabStops[g.CursorC] = true
	}
}

// ClearTabStop clears the tab stop at the current cursor column (all==false)
// or clears all tab stops (all==true). Implements CSI g (TBC).
func (g *grid) ClearTabStop(all bool) {
	if all {
		g.TabStops = [MaxGridDim]bool{}
		return
	}
	if g.CursorC >= 0 && g.CursorC < MaxGridDim {
		g.TabStops[g.CursorC] = false
	}
}

// EraseInLine implements CSI K. mode: 0 = cursor to EOL, 1 = SOL to
// cursor, 2 = entire line. Cleared cells use current bg/attrs so
// painted backgrounds persist.
func (g *grid) EraseInLine(mode int) {
	row := g.CursorR
	if row < 0 || row >= g.Rows {
		return
	}
	cFrom, cTo := 0, g.Cols
	switch mode {
	case 0:
		g.eraseWideAt(row, g.CursorC)
		cFrom = g.CursorC
	case 1:
		g.eraseWideAt(row, g.CursorC)
		cTo = g.CursorC + 1
	case 2:

	default:
		return
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for c := cFrom; c < cTo; c++ {
		g.Cells[row*g.Cols+c] = blank
	}
	g.markDirty(row)
}

// EraseInDisplay implements CSI J. mode: 0 = cursor to end of screen,
// 1 = start of screen to cursor, 2/3 = entire screen.
func (g *grid) EraseInDisplay(mode int) {
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	switch mode {
	case 0:
		g.EraseInLine(0)
		for r := g.CursorR + 1; r < g.Rows; r++ {
			for c := range g.Cols {
				g.Cells[r*g.Cols+c] = blank
			}
		}
		g.markAllDirty()
	case 1:
		g.EraseInLine(1)
		for r := range g.CursorR {
			for c := range g.Cols {
				g.Cells[r*g.Cols+c] = blank
			}
		}
		g.markAllDirty()
	case 2, 3:
		for i := range g.Cells {
			g.Cells[i] = blank
		}
		g.markAllDirty()
	}
}

// InsertLines implements CSI Ps L (IL): insert n blank lines at the
// cursor row, pushing existing rows toward Bottom; rows pushed past
// Bottom are discarded. No-op when the cursor is outside the active
// scroll region (DEC behavior).
func (g *grid) InsertLines(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	if g.CursorR < g.Top || g.CursorR > g.Bottom {
		return
	}
	height := g.Bottom - g.CursorR + 1
	if n > height {
		n = height
	}
	if n < height {
		for r := g.Bottom; r >= g.CursorR+n; r-- {
			copy(
				g.Cells[r*g.Cols:(r+1)*g.Cols],
				g.Cells[(r-n)*g.Cols:(r-n+1)*g.Cols],
			)
			g.RowWrapped[r] = g.RowWrapped[r-n]
		}
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.CursorR; r < g.CursorR+n && r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
		g.RowWrapped[r] = false
	}
	g.CursorC = 0
	g.markAllDirty()
}

// DeleteLines implements CSI Ps M (DL): delete n lines starting at the
// cursor row, shifting rows below up; blank rows fill the bottom of
// the region. No-op when cursor is outside the region.
func (g *grid) DeleteLines(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	if g.CursorR < g.Top || g.CursorR > g.Bottom {
		return
	}
	height := g.Bottom - g.CursorR + 1
	if n > height {
		n = height
	}
	if n < height {
		copy(
			g.Cells[g.CursorR*g.Cols:(g.Bottom+1)*g.Cols],
			g.Cells[(g.CursorR+n)*g.Cols:(g.Bottom+1)*g.Cols],
		)
		copy(g.RowWrapped[g.CursorR:g.Bottom+1-n], g.RowWrapped[g.CursorR+n:g.Bottom+1])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Bottom - n + 1; r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
		g.RowWrapped[r] = false
	}
	g.CursorC = 0
	g.markAllDirty()
}

// InsertChars implements CSI Ps @ (ICH): insert n blanks at the cursor,
// shifting existing cells right within the row; cells past the right
// margin are discarded. Blanks use current SGR bg/attrs.
func (g *grid) InsertChars(n int) {
	if n <= 0 || g.CursorR < 0 || g.CursorR >= g.Rows {
		return
	}
	if g.CursorC < 0 || g.CursorC >= g.Cols {
		return
	}
	width := g.Cols - g.CursorC
	if n > width {
		n = width
	}
	g.eraseWideAt(g.CursorR, g.CursorC)
	g.eraseWideAt(g.CursorR, g.Cols-1)
	row := g.Cells[g.CursorR*g.Cols : (g.CursorR+1)*g.Cols]
	if n < width {
		copy(row[g.CursorC+n:], row[g.CursorC:g.Cols-n])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for c := g.CursorC; c < g.CursorC+n; c++ {
		row[c] = blank
	}
	g.markDirty(g.CursorR)
}

// DeleteChars implements CSI Ps P (DCH): delete n cells at the cursor,
// shifting cells from the right inward; blanks fill at the right edge.
func (g *grid) DeleteChars(n int) {
	if n <= 0 || g.CursorR < 0 || g.CursorR >= g.Rows {
		return
	}
	if g.CursorC < 0 || g.CursorC >= g.Cols {
		return
	}
	width := g.Cols - g.CursorC
	if n > width {
		n = width
	}
	g.eraseWideAt(g.CursorR, g.CursorC)
	g.eraseWideAt(g.CursorR, g.CursorC+n)
	row := g.Cells[g.CursorR*g.Cols : (g.CursorR+1)*g.Cols]
	if n < width {
		copy(row[g.CursorC:], row[g.CursorC+n:g.Cols])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for c := g.Cols - n; c < g.Cols; c++ {
		row[c] = blank
	}
	g.markDirty(g.CursorR)
}
