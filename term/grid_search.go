package term

import (
	"regexp"
	"unicode"
	"unicode/utf8"
)

// equalFoldRune reports whether a and b are equal under Unicode case-folding.
func equalFoldRune(a, b rune) bool {
	return unicode.ToLower(a) == unicode.ToLower(b)
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
// cells (Ch == 0). It returns a "clean" rune slice and a mapping table
// where colMap[cleanIdx] is the original grid column index.
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
		base := liveRow * g.Cols
		src = g.Cells[base : base+g.Cols]
	}

	rr = rrBuf[:0]
	colMap = colBuf[:0]
	for i, cell := range src {
		if cell.Ch != 0 {
			rr = append(rr, cell.Ch)
			colMap = append(colMap, i)
		}
	}
	return rr, colMap
}

// cleanIdxGT returns the first index i in colMap where colMap[i] > gridCol.
// Returns len(colMap) if no entry qualifies.
func cleanIdxGT(colMap []int, gridCol int) int {
	for i, orig := range colMap {
		if orig > gridCol {
			return i
		}
	}
	return len(colMap)
}

// cleanIdxGE returns the first index i in colMap where colMap[i] >= gridCol.
// Returns -1 if no entry qualifies.
func cleanIdxGE(colMap []int, gridCol int) int {
	for i, orig := range colMap {
		if orig >= gridCol {
			return i
		}
	}
	return -1
}

// matchGridSpan returns the grid column and column-span for a match at
// clean-index ci with clean rune length cl. colMap and rr come from searchRow.
func matchGridSpan(colMap []int, rr []rune, ci, cl int) (col, width int) {
	col = colMap[ci]
	lastCol := colMap[ci+cl-1]
	return col, lastCol - col + runeWidth(rr[ci+cl-1])
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
	total := g.ContentRows()
	if total == 0 {
		return contentPos{}, false
	}
	start.Row = clamp(start.Row, 0, total-1)
	var rrBuf []rune
	var colBuf []int
	for i := range total {
		var row int
		if forward {
			row = (start.Row + i) % total
		} else {
			row = (start.Row - i + total) % total
		}
		rr, colMap := g.searchRow(row, rrBuf, colBuf)
		rrBuf, colBuf = rr, colMap
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
	qLen := len(qRunes)
	sb := g.Scrollback.Len()
	off := clamp(g.ViewOffset, 0, sb)
	n := min(off, g.Rows)
	var matches []searchMatch
	var rrBuf []rune
	var colBuf []int
	for vr := range g.Rows {
		contentRow := viewportContentRow(vr, sb, off, n)
		rr, colMap := g.searchRow(contentRow, rrBuf, colBuf)
		rrBuf, colBuf = rr, colMap
		idx := 0
		for {
			idx = runeSliceSearch(rr, qRunes, idx)
			if idx < 0 {
				break
			}
			col, matchWidth := matchGridSpan(colMap, rr, idx, qLen)

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

// regexSearchForward returns the first regex match in s with rune column >=
// fromCol. s must be a string of the grid row's content runes (e.g. from
// searchRow). Returns column, match length in rune columns, and true on
// success.
func regexSearchForward(s string, re *regexp.Regexp, fromCol int) (col, matchLen int, found bool) {
	for _, loc := range re.FindAllStringIndex(s, -1) {
		c := utf8.RuneCountInString(s[:loc[0]])
		if c >= fromCol {
			return c, utf8.RuneCountInString(s[:loc[1]]) - c, true
		}
	}
	return 0, 0, false
}

// regexSearchLast returns the last regex match in s with rune column <
// upToCol. s must be a string of the grid row's content runes (e.g. from
// searchRow).
func regexSearchLast(s string, re *regexp.Regexp, upToCol int) (col, matchLen int, found bool) {
	col = -1
	for _, loc := range re.FindAllStringIndex(s, -1) {
		c := utf8.RuneCountInString(s[:loc[0]])
		if c < upToCol {
			col = c
			matchLen = utf8.RuneCountInString(s[:loc[1]]) - c
		}
	}
	if col < 0 {
		return 0, 0, false
	}
	return col, matchLen, true
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
	var rrBuf []rune
	var colBuf []int
	for i := range total {
		var row int
		if forward {
			row = (start.Row + i) % total
		} else {
			row = (start.Row - i + total) % total
		}
		rr, colMap := g.searchRow(row, rrBuf, colBuf)
		rrBuf, colBuf = rr, colMap
		s := string(rr)
		if forward {
			fromCleanIdx := 0
			if i == 0 {
				fromCleanIdx = cleanIdxGT(colMap, start.Col)
			}
			if c, l, ok := regexSearchForward(s, re, fromCleanIdx); ok {
				col, width := matchGridSpan(colMap, rr, c, l)
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
				col, width := matchGridSpan(colMap, rr, c, l)
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
	var rrBuf []rune
	var colBuf []int
	for vr := range g.Rows {
		contentRow := viewportContentRow(vr, sb, off, n)
		rr, colMap := g.searchRow(contentRow, rrBuf, colBuf)
		rrBuf, colBuf = rr, colMap
		s := string(rr)
		idx := 0
		for {
			c, l, ok := regexSearchForward(s, re, idx)
			if !ok {
				break
			}
			col, width := matchGridSpan(colMap, rr, c, l)
			matches = append(matches, searchMatch{
				contentPos: contentPos{Row: contentRow, Col: col},
				Len:        width,
			})
			if len(matches) >= maxSearchHighlights {
				return matches
			}
			idx = c + max(l, 1)
		}
	}
	return matches
}
