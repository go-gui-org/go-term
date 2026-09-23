package term

import (
	"regexp"
	"sort"
	"unicode"
	"unicode/utf8"
)

// maxSearchQueryRunes caps a plain-text query at the grid layer. Matches are
// single-row, so anything longer than a row can never hit; the widget already
// caps its search box here, and this guards direct grid callers (and bounds
// the O(n*m) per-row scan) the same way.
const maxSearchQueryRunes = MaxGridDim

// equalFoldRune reports whether a and b are equal under Unicode case-folding,
// following the full SimpleFold orbit (so U+017F matches s/S, etc.), not just
// ToLower. The a == b fast path keeps the ASCII common case branch-free.
func equalFoldRune(a, b rune) bool {
	if a == b {
		return true
	}
	for r := unicode.SimpleFold(b); r != b; r = unicode.SimpleFold(r) {
		if r == a {
			return true
		}
	}
	return false
}

// runeSliceSearch returns the first column index >= fromCol where needle
// occurs in haystack. Returns -1 when not found. Case-insensitive.
func runeSliceSearch(haystack, needle []rune, fromCol int) int {
	n, m := len(haystack), len(needle)
	if m == 0 || fromCol > n-m {
		return -1
	}
	if fromCol < 0 {
		fromCol = 0
	}
	for i := fromCol; i <= n-m; i++ {
		match := true
		for j := range m {
			if !equalFoldRune(haystack[i+j], needle[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// runeSliceSearchLast returns the rightmost column index < upToCol where
// needle occurs in haystack. Returns -1 when not found. Case-insensitive.
func runeSliceSearchLast(haystack, needle []rune, upToCol int) int {
	n, m := len(haystack), len(needle)
	if m == 0 || n < m {
		return -1
	}
	maxStart := min(upToCol-1, n-m)
	if maxStart < 0 {
		return -1
	}
	for i := maxStart; i >= 0; i-- {
		match := true
		for j := range m {
			if !equalFoldRune(haystack[i+j], needle[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// searchRow prepares a content row for searching by stripping continuation
// cells (Ch == 0). Multi-rune grapheme clusters expand in place, so a query
// can match inside a ZWJ sequence or a base+mark cluster instead of only its
// base rune; every rune of one cluster maps back to the same grid column.
// It returns a "clean" rune slice and a mapping table where colMap[cleanIdx]
// is the original grid column index.
//
// Buffers are grown once to src length, then reused across calls via
// direct indexing — no per-cell append/capacity checks in the hot loop
// beyond one bounds check that only fires while a row of wide clusters
// expands past its cell count.
func (g *grid) searchRow(row int, rrBuf []rune, colBuf []int) (rr []rune, colMap []int) {
	sb := g.Scrollback.Len()
	var src []cell
	if row < sb {
		if row < 0 {
			return nil, nil
		}
		src = g.Scrollback.Row(row)
	} else {
		liveRow := row - sb
		if liveRow < 0 || liveRow >= g.Rows || g.Cols == 0 {
			return nil, nil
		}
		src = g.row(liveRow)
	}

	n := len(src)
	if cap(rrBuf) < n {
		rrBuf = make([]rune, n)
	}
	if cap(colBuf) < n {
		colBuf = make([]int, n)
	}
	rr = rrBuf[:n]
	colMap = colBuf[:n]
	cnt := 0
	// grow makes room for one more expanded rune. A row of multi-rune
	// clusters holds more runes than cells; growth is geometric and only
	// ever triggers on such rows.
	grow := func() {
		nrr := make([]rune, len(rr)*2+1)
		copy(nrr, rr[:cnt])
		rr = nrr
		ncols := make([]int, len(colMap)*2+1)
		copy(ncols, colMap[:cnt])
		colMap = ncols
	}
	for i, cell := range src {
		if cell.Ch == 0 {
			continue
		}
		if cell.clusterID != 0 && int(cell.clusterID) < len(g.clusters) {
			for _, r := range g.clusters[cell.clusterID] {
				if cnt >= len(rr) {
					grow()
				}
				rr[cnt] = r
				colMap[cnt] = i
				cnt++
			}
			continue
		}
		if cnt >= len(rr) {
			grow()
		}
		rr[cnt] = cell.Ch
		colMap[cnt] = i
		cnt++
	}
	return rr[:cnt], colMap[:cnt]
}

// cleanIdxGT returns the first index i in colMap where colMap[i] > gridCol.
// Returns len(colMap) if no entry qualifies. colMap is non-decreasing, so
// this is a binary search, not the linear scan it replaced.
func cleanIdxGT(colMap []int, gridCol int) int {
	return sort.Search(len(colMap), func(i int) bool { return colMap[i] > gridCol })
}

// cleanIdxGE returns the first index i in colMap where colMap[i] >= gridCol.
// Returns -1 if no entry qualifies.
func cleanIdxGE(colMap []int, gridCol int) int {
	if i := sort.SearchInts(colMap, gridCol); i < len(colMap) {
		return i
	}
	return -1
}

// matchGridSpan returns the grid column and column-span for a match at
// clean-index ci with clean rune length cl. colMap and rr come from
// searchRow. ok is false when the span is empty or runs past the row, so a
// zero-width regex match can never index out of range. Caller holds Mu.
func matchGridSpan(colMap []int, rr []rune, ci, cl int) (col, width int, ok bool) {
	if cl <= 0 || ci < 0 || ci+cl > len(colMap) || ci+cl > len(rr) {
		return 0, 0, false
	}
	col = colMap[ci]
	lastCol := colMap[ci+cl-1]
	// The last matched rune can sit inside a multi-rune cluster, where a
	// combining mark or VS16 measures zero columns. Size the last cell by
	// where the next one starts instead (continuations are stripped, so a
	// wide cell leaves a gap); only a row's final cell falls back to the
	// rune's own width, floored at one cell.
	if j := cleanIdxGT(colMap, lastCol); j < len(colMap) {
		return col, colMap[j] - col, true
	}
	return col, lastCol - col + max(runeWidth(rr[ci+cl-1]), 1), true
}

// viewportContentRow maps a viewport row vr to a content row given the
// scrollback offset parameters (sb = Scrollback.Len, off = clamp(ViewOffset,0,sb),
// n = min(off, Rows)).
func viewportContentRow(vr, sb, off, n int) int {
	if vr < n {
		return sb - off + vr
	}
	return sb + (vr - n)
}

// Find searches for query (case-insensitive) starting at start, walking
// forward or backward through all content rows (scrollback + live), wrapping
// once. Multi-row spanning is not supported; matches must fit within one row.
// Returns the contentPos of the first cell of the match and true on success.
// Called under Mu.
func (g *grid) Find(query string, start contentPos, forward bool) (contentPos, bool) {
	if query == "" || g.Cols <= 0 {
		return contentPos{}, false
	}
	qRunes := []rune(query)
	if len(qRunes) > maxSearchQueryRunes {
		return contentPos{}, false
	}
	total := g.ContentRows()
	if total == 0 {
		return contentPos{}, false
	}
	start.Row = clamp(start.Row, 0, total-1)
	for i := range total {
		var row int
		if forward {
			row = (start.Row + i) % total
		} else {
			row = (start.Row - i + total) % total
		}
		rr, colMap := g.searchRow(row, g.searchRunes, g.searchCols)
		g.searchRunes, g.searchCols = rr, colMap
		if forward {
			fromCleanIdx := 0
			if i == 0 {
				fromCleanIdx = cleanIdxGT(colMap, start.Col)
			}
			if idx := runeSliceSearch(rr, qRunes, fromCleanIdx); idx >= 0 {
				return contentPos{Row: row, Col: colMap[idx]}, true
			}
		} else {
			upToCleanIdx := len(rr) + 1
			if i == 0 {
				if ci := cleanIdxGE(colMap, start.Col); ci >= 0 {
					upToCleanIdx = ci
				}
			}
			if idx := runeSliceSearchLast(rr, qRunes, upToCleanIdx); idx >= 0 {
				return contentPos{Row: row, Col: colMap[idx]}, true
			}
		}
	}
	return contentPos{}, false
}

// maxSearchHighlights caps the number of matches returned by viewport search
// functions. Prevents O(viewport) highlight work on patterns that match every
// cell (e.g. "." regex or single-character plain-text queries).
const maxSearchHighlights = 500

// ViewportMatches returns all plain-text matches visible at the current
// ViewOffset. Returns nil for an empty query, a zero-column grid, or while
// the alt screen is active. Called under Mu.
func (g *grid) ViewportMatches(query string) []searchMatch {
	if query == "" || g.Cols <= 0 || g.AltActive {
		return nil
	}
	qRunes := []rune(query)
	if len(qRunes) > maxSearchQueryRunes {
		return nil
	}
	qLen := len(qRunes)
	sb := g.Scrollback.Len()
	off := clamp(g.ViewOffset, 0, sb)
	n := min(off, g.Rows)
	var matches []searchMatch
	for vr := range g.Rows {
		contentRow := viewportContentRow(vr, sb, off, n)
		rr, colMap := g.searchRow(contentRow, g.searchRunes, g.searchCols)
		g.searchRunes, g.searchCols = rr, colMap
		idx := 0
		for {
			idx = runeSliceSearch(rr, qRunes, idx)
			if idx < 0 {
				break
			}
			col, matchWidth, ok := matchGridSpan(colMap, rr, idx, qLen)
			if !ok {
				idx++
				continue
			}
			matches = append(matches, searchMatch{
				contentPos: contentPos{Row: contentRow, Col: col},
				Len:        matchWidth,
			})
			if len(matches) >= maxSearchHighlights {
				return matches
			}
			idx++
		}
	}
	return matches
}

// regexRuneMatches reports every non-empty match of re in s as a rune column
// and a rune length, left to right, until yield returns false. s holds the
// grid row's content runes encoded as UTF-8 (see appendSearchBytes).
//
// One FindAllIndex runs over the whole row, so ^, $ and \b stay anchored to
// the row edges: re-running the regexp on a slice that starts mid-row would
// let them fire at the slice start. Zero-width matches (a*, x?) are skipped:
// they have no cell to highlight. One monotonic byte→rune cursor converts
// every span, so the walk is linear in the row length.
func regexRuneMatches(s []byte, re *regexp.Regexp, yield func(col, n int) bool) {
	bytePos, runeIdx := 0, 0
	for _, loc := range re.FindAllIndex(s, -1) {
		for bytePos < loc[0] {
			_, w := utf8.DecodeRune(s[bytePos:])
			bytePos += w
			runeIdx++
		}
		start := runeIdx
		for bytePos < loc[1] {
			_, w := utf8.DecodeRune(s[bytePos:])
			bytePos += w
			runeIdx++
		}
		if n := runeIdx - start; n > 0 && !yield(start, n) {
			return
		}
	}
}

// regexSearchForward returns the first non-empty regex match in s with rune
// column >= fromCol.
func regexSearchForward(s []byte, re *regexp.Regexp, fromCol int) (col, matchLen int, found bool) {
	regexRuneMatches(s, re, func(c, n int) bool {
		if c < fromCol {
			return true
		}
		col, matchLen, found = c, n, true
		return false
	})
	return col, matchLen, found
}

// regexSearchLast returns the last non-empty regex match in s with rune
// column < upToCol.
func regexSearchLast(s []byte, re *regexp.Regexp, upToCol int) (col, matchLen int, found bool) {
	regexRuneMatches(s, re, func(c, n int) bool {
		if c >= upToCol {
			return false
		}
		col, matchLen, found = c, n, true
		return true
	})
	return col, matchLen, found
}

// appendSearchBytes encodes rr as UTF-8 into the grid's reused scratch
// buffer, so regex search costs no per-row allocation after warmup. The
// returned slice aliases grid state; consume it before the next call.
// Caller holds Mu.
func (g *grid) appendSearchBytes(rr []rune) []byte {
	b := g.searchText[:0]
	for _, r := range rr {
		b = utf8.AppendRune(b, r)
	}
	g.searchText = b
	return b
}

// FindRegex searches for the first match of re starting at start, walking
// forward or backward through all content rows (scrollback + live), wrapping
// once. Returns the contentPos, match length in rune columns, and true on
// success. Called under Mu.
func (g *grid) FindRegex(re *regexp.Regexp, start contentPos, forward bool) (contentPos, int, bool) {
	if re == nil || g.Cols <= 0 {
		return contentPos{}, 0, false
	}
	total := g.ContentRows()
	if total == 0 {
		return contentPos{}, 0, false
	}
	start.Row = clamp(start.Row, 0, total-1)
	for i := range total {
		var row int
		if forward {
			row = (start.Row + i) % total
		} else {
			row = (start.Row - i + total) % total
		}
		rr, colMap := g.searchRow(row, g.searchRunes, g.searchCols)
		g.searchRunes, g.searchCols = rr, colMap
		s := g.appendSearchBytes(rr)
		if forward {
			fromCleanIdx := 0
			if i == 0 {
				fromCleanIdx = cleanIdxGT(colMap, start.Col)
			}
			if c, l, ok := regexSearchForward(s, re, fromCleanIdx); ok {
				col, width, ok := matchGridSpan(colMap, rr, c, l)
				if !ok {
					continue
				}
				return contentPos{Row: row, Col: col}, width, true
			}
		} else {
			upToCleanIdx := len(rr) + 1
			if i == 0 {
				if ci := cleanIdxGE(colMap, start.Col); ci >= 0 {
					upToCleanIdx = ci
				}
			}
			if c, l, ok := regexSearchLast(s, re, upToCleanIdx); ok {
				col, width, ok := matchGridSpan(colMap, rr, c, l)
				if !ok {
					continue
				}
				return contentPos{Row: row, Col: col}, width, true
			}
		}
	}
	return contentPos{}, 0, false
}

// ViewportMatchesRegex returns all regex matches visible at the current
// ViewOffset. Returns nil for a nil pattern or while the alt screen is active.
// Called under Mu.
func (g *grid) ViewportMatchesRegex(re *regexp.Regexp) []searchMatch {
	if re == nil || g.Cols <= 0 || g.AltActive {
		return nil
	}
	sb := g.Scrollback.Len()
	off := clamp(g.ViewOffset, 0, sb)
	n := min(off, g.Rows)
	var matches []searchMatch
	for vr := range g.Rows {
		contentRow := viewportContentRow(vr, sb, off, n)
		rr, colMap := g.searchRow(contentRow, g.searchRunes, g.searchCols)
		g.searchRunes, g.searchCols = rr, colMap
		s := g.appendSearchBytes(rr)
		// One pass per row: every match comes from a single FindAllIndex, so
		// anchors keep their whole-row meaning (see regexRuneMatches).
		regexRuneMatches(s, re, func(c, l int) bool {
			if col, width, ok := matchGridSpan(colMap, rr, c, l); ok {
				matches = append(matches, searchMatch{
					contentPos: contentPos{Row: contentRow, Col: col},
					Len:        width,
				})
			}
			return len(matches) < maxSearchHighlights
		})
		if len(matches) >= maxSearchHighlights {
			return matches
		}
	}
	return matches
}
