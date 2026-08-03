package term

import (
	"regexp"
	"sort"
	"unicode/utf8"
)

// urlRe matches an implicit (un-marked-up) URL: an http/https/mailto scheme
// followed by one or more non-terminating bytes. The stop set excludes
// whitespace and the delimiter characters that commonly bracket a URL in
// prose (<>"'`). A \b anchor before the scheme prevents matching a scheme
// embedded mid-word (e.g. "xhttp://"). Trailing sentence punctuation that the
// class still admits (".,;:!?)]}") is trimmed afterwards by trimTrailingURL,
// which the RE2 grammar cannot express with balanced-paren awareness.
var urlRe = regexp.MustCompile("(?i)\\b(?:https?://|mailto:)[^\\s<>\"'`]+")

// maxURLScanRows bounds how far detectURLAt walks a soft-wrapped logical line
// in each direction from the hovered row. A URL wrapping across 8 rows at 80
// columns already spans ~640 characters, well beyond any realistic link, so
// this caps the work without ever splitting a plausible URL.
const maxURLScanRows = 8

// urlSpan is the inclusive grid-column range [C0, C1] on one content Row that a
// detected URL occupies. A URL that spans soft-wrapped rows yields one urlSpan
// per row it touches, in top-to-bottom order.
type urlSpan struct {
	Row, C0, C1 int
}

// rowWrapped reports whether content row contentRow ended with an autowrap,
// i.e. it continues into contentRow+1 as one logical line. Scrollback rows
// carry the flag in the ring; live rows in g.RowWrapped. Caller holds Mu.
func (g *grid) rowWrapped(contentRow int) bool {
	sb := g.Scrollback.Len()
	if contentRow < sb {
		return g.Scrollback.Wrapped(contentRow)
	}
	lr := contentRow - sb
	if lr < 0 || lr >= len(g.RowWrapped) {
		return false
	}
	return g.RowWrapped[lr]
}

// detectURLAt scans the logical line containing content-position cp for an
// implicit URL covering that cell, joining soft-wrapped rows so a URL broken at
// the right margin is found as one link. On a hit it returns the URL text and
// the per-row grid-column spans it covers. Caller must hold Mu.
//
// This is an on-demand path driven by Cmd-hover / Cmd-click, not the render hot
// loop, so a modest amount of work per call is fine; the persisted scratch
// buffers keep it allocation-light after warmup.
func (g *grid) detectURLAt(cp contentPos) (url string, spans []urlSpan, ok bool) {
	if g.Cols <= 0 || cp.Row < 0 || cp.Row >= g.ContentRows() {
		return "", nil, false
	}

	// Walk the wrap flags in both directions from cp to find the logical line,
	// then join it. Continuation cells (Ch == 0, the trailing half of a wide
	// glyph or an empty tail) are stripped by searchRow, so a hover over blank
	// space finds no rune at cp and yields no match.
	start := g.logicalLineStart(cp.Row)
	end := g.logicalLineEnd(cp.Row, maxURLScanRows)
	runes, rows, cols, bytes, byteLen := g.joinRows(start, end)
	if len(runes) == 0 {
		return "", nil, false
	}

	// Locate cp's rune index within the joined line. Linear, but bounded by
	// maxURLScanRows rows and only ever reached from a hover/click.
	cpRune := -1
	for i := range rows {
		if rows[i] == cp.Row && cols[i] == cp.Col {
			cpRune = i
			break
		}
	}
	if cpRune < 0 {
		return "", nil, false
	}

	// Pick the match covering cpRune.
	g.urlMatches = urlRangesIn(runes, bytes, byteLen, g.urlMatches[:0])
	for _, m := range g.urlMatches {
		if cpRune >= m[0] && cpRune < m[1] {
			return string(runes[m[0]:m[1]]), spansFor(rows, cols, m[0], m[1]), true
		}
	}
	return "", nil, false
}

// logicalLineStart returns the first content row of the soft-wrapped logical
// line containing row, capped at maxURLScanRows rows above it. On the alt
// screen the walk stops at the scrollback/live boundary — the alt buffer has
// its own wrap state, and the old main-screen scrollback below it is unrelated.
// Caller holds Mu.
func (g *grid) logicalLineStart(row int) int {
	lo := 0
	if g.AltActive {
		lo = g.Scrollback.Len()
	}
	start := row
	for start > lo && start > row-maxURLScanRows && g.rowWrapped(start-1) {
		start--
	}
	return start
}

// logicalLineEnd returns the last content row of the soft-wrapped logical line
// containing row, following at most maxRows continuations downward. Caller
// holds Mu.
func (g *grid) logicalLineEnd(row, maxRows int) int {
	total := g.ContentRows()
	end := row
	for end < total-1 && end < row+maxRows && g.rowWrapped(end) {
		end++
	}
	return end
}

// joinRows builds the joined clean text of content rows [start, end] plus the
// rune→(row, col, byte) maps that map a regexp byte span back to grid
// positions. bytes[i] is the byte offset of rune i in string(runes); byteLen
// is the one-past-end sentinel.
//
// The returned slices alias the grid's persistent scratch buffers, so a caller
// must finish with them before the next call. Reusing those buffers is what
// keeps repeated scans allocation-light after warmup. Caller holds Mu.
func (g *grid) joinRows(start, end int) (runes []rune, rows, cols, bytes []int, byteLen int) {
	runes = g.urlRunes[:0]
	rows = g.urlRows[:0]
	cols = g.urlCols[:0]
	bytes = g.urlBytes[:0]
	for row := start; row <= end; row++ {
		rr, colMap := g.searchRow(row, g.searchRunes, g.searchCols)
		g.searchRunes, g.searchCols = rr, colMap
		for i, r := range rr {
			runes = append(runes, r)
			rows = append(rows, row)
			cols = append(cols, colMap[i])
			bytes = append(bytes, byteLen)
			byteLen += utf8.RuneLen(r)
		}
	}
	g.urlRunes, g.urlRows, g.urlCols, g.urlBytes = runes, rows, cols, bytes
	return runes, rows, cols, bytes, byteLen
}

// urlRangesIn appends the rune range [is, ie) of every implicit URL in a joined
// line to dst, in left-to-right order, with trailing punctuation already
// trimmed. Empty ranges (a match that trimmed away to nothing) are skipped.
func urlRangesIn(runes []rune, bytes []int, byteLen int, dst [][2]int) [][2]int {
	// Regexp works on the byte string; map each match's byte span back to rune
	// indices via the byte-offset table.
	line := string(runes)
	for _, m := range urlRe.FindAllStringIndex(line, -1) {
		is := sort.SearchInts(bytes, m[0])
		ie := runeIndexForByte(bytes, byteLen, m[1])
		ie = is + trimTrailingURL(runes[is:ie])
		if is < ie {
			dst = append(dst, [2]int{is, ie})
		}
	}
	return dst
}

// runeIndexForByte returns the rune index whose byte offset is b, treating b ==
// byteLen (one past the last rune) as len(bytes).
func runeIndexForByte(bytes []int, byteLen, b int) int {
	if b >= byteLen {
		return len(bytes)
	}
	return sort.SearchInts(bytes, b)
}

// spansFor groups the rune range [is, ie) of a detected URL into one urlSpan
// per content row it touches. Runes are contiguous and ordered, so each row
// appears once with its min/max grid column.
func spansFor(rows, cols []int, is, ie int) []urlSpan {
	var spans []urlSpan
	for k := is; k < ie; {
		row := rows[k]
		c0, c1 := cols[k], cols[k]
		for k < ie && rows[k] == row {
			c1 = cols[k]
			k++
		}
		spans = append(spans, urlSpan{Row: row, C0: c0, C1: c1})
	}
	return spans
}

// trimTrailingURL returns the length of runes with trailing sentence
// punctuation removed. A trailing closing bracket is kept when the span holds at
// least as many opening as closing brackets (so a link with a parenthesized
// path segment such as a Wikipedia "(disambiguation)" URL survives), otherwise
// it is trimmed. Mirrors the heuristic used by iTerm2/kitty/Windows Terminal.
//
// Pre-counts brackets once, then walks backward from the end decrementing close
// counters as punctuation is trimmed — O(n) instead of re-scanning the prefix
// for every bracket char.
func trimTrailingURL(runes []rune) int {
	var openP, clsP int
	var openB, clsB int
	var openC, clsC int
	for _, r := range runes {
		switch r {
		case '(':
			openP++
		case ')':
			clsP++
		case '[':
			openB++
		case ']':
			clsB++
		case '{':
			openC++
		case '}':
			clsC++
		}
	}
	n := len(runes)
	for n > 0 {
		switch runes[n-1] {
		case '.', ',', ';', ':', '!', '?':
			n--
		case ')':
			if openP >= clsP {
				return n
			}
			clsP--
			n--
		case ']':
			if openB >= clsB {
				return n
			}
			clsB--
			n--
		case '}':
			if openC >= clsC {
				return n
			}
			clsC--
			n--
		default:
			return n
		}
	}
	return n
}
