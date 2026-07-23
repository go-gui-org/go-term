package term

import "testing"

// hover returns detectURLAt at content-row sb (the first live row) column col.
func hover(g *grid, col int) (string, []urlSpan, bool) {
	sb := g.Scrollback.Len()
	return g.detectURLAt(contentPos{Row: sb, Col: col})
}

func TestDetectURLAt_Basic(t *testing.T) {
	g := newGrid(3, 40)
	putRow(g, "see https://go.dev now") // URL at cols 4..17
	sb := g.Scrollback.Len()

	// Hovering the first, a middle, and the last cell of the URL all hit.
	for _, col := range []int{4, 10, 17} {
		url, spans, ok := hover(g, col)
		if !ok || url != "https://go.dev" {
			t.Fatalf("hover col %d: got (%q, %v), want https://go.dev", col, url, ok)
		}
		if len(spans) != 1 || spans[0] != (urlSpan{Row: sb, C0: 4, C1: 17}) {
			t.Fatalf("hover col %d spans = %v, want [{%d 4 17}]", col, spans, sb)
		}
	}

	// Hovering the space before, the space after, or ordinary words misses.
	for _, col := range []int{3, 0, 18, 19} {
		if _, _, ok := hover(g, col); ok {
			t.Errorf("hover col %d unexpectedly matched a URL", col)
		}
	}
}

func TestDetectURLAt_TrailingPunctuation(t *testing.T) {
	g := newGrid(3, 40)
	putRow(g, "visit https://go.dev.") // trailing '.' must be trimmed
	url, spans, ok := hover(g, 8)
	if !ok || url != "https://go.dev" {
		t.Fatalf("got (%q, %v), want https://go.dev", url, ok)
	}
	// URL occupies cols 6..19; the '.' at col 20 is excluded.
	if len(spans) != 1 || spans[0].C1 != 19 {
		t.Fatalf("spans = %v, want end col 19", spans)
	}
}

func TestDetectURLAt_BalancedParens(t *testing.T) {
	// A trailing ')' with no matching '(' is trimmed.
	g1 := newGrid(3, 40)
	putRow(g1, "(https://go.dev/a)")
	if url, _, ok := hover(g1, 5); !ok || url != "https://go.dev/a" {
		t.Fatalf("unmatched paren: got (%q, %v), want https://go.dev/a", url, ok)
	}
	// A trailing ')' balanced by a '(' inside the path is kept.
	g2 := newGrid(3, 40)
	putRow(g2, "https://ex.com/foo(bar)")
	if url, _, ok := hover(g2, 3); !ok || url != "https://ex.com/foo(bar)" {
		t.Fatalf("balanced paren: got (%q, %v), want https://ex.com/foo(bar)", url, ok)
	}
}

func TestDetectURLAt_Mailto(t *testing.T) {
	g := newGrid(3, 40)
	putRow(g, "ping mailto:foo@bar.com ok")
	url, _, ok := hover(g, 10)
	if !ok || url != "mailto:foo@bar.com" {
		t.Fatalf("got (%q, %v), want mailto:foo@bar.com", url, ok)
	}
}

func TestDetectURLAt_NoURL(t *testing.T) {
	g := newGrid(3, 40)
	putRow(g, "just some plain text here")
	for col := 0; col < 25; col++ {
		if _, _, ok := hover(g, col); ok {
			t.Fatalf("hover col %d matched a URL in plain text", col)
		}
	}
}

func TestDetectURLAt_EmbeddedSchemeNotMatched(t *testing.T) {
	// A scheme embedded mid-word must not match (the \b anchor).
	g := newGrid(3, 40)
	putRow(g, "notahttps://go.dev")
	if _, _, ok := hover(g, 12); ok {
		t.Error("embedded scheme should not match a URL")
	}
}

func TestDetectURLAt_NonASCIIPrefix(t *testing.T) {
	// A multi-byte rune before the URL exercises the byte→rune offset mapping.
	g := newGrid(3, 40)
	putRow(g, "café https://go.dev") // 'é' is 2 bytes; URL at cols 5..18
	url, spans, ok := hover(g, 8)
	if !ok || url != "https://go.dev" {
		t.Fatalf("got (%q, %v), want https://go.dev", url, ok)
	}
	if len(spans) != 1 || spans[0].C0 != 5 || spans[0].C1 != 18 {
		t.Fatalf("spans = %v, want [{_ 5 18}]", spans)
	}
}

func TestDetectURLAt_WrappedLine(t *testing.T) {
	// A URL longer than the grid width autowraps; detection must join the rows.
	g := newGrid(3, 20)
	const link = "https://example.com/verylongpath" // 32 > 20 cols
	putRow(g, link)
	sb := g.Scrollback.Len()

	if !g.rowWrapped(sb) {
		t.Fatal("expected live row 0 to be flagged wrapped")
	}
	// Hover on both the first and the continuation row.
	for _, cp := range []contentPos{{Row: sb, Col: 5}, {Row: sb + 1, Col: 3}} {
		url, spans, ok := g.detectURLAt(cp)
		if !ok || url != link {
			t.Fatalf("hover %v: got (%q, %v), want %q", cp, url, ok, link)
		}
		want := []urlSpan{{Row: sb, C0: 0, C1: 19}, {Row: sb + 1, C0: 0, C1: 11}}
		if len(spans) != 2 || spans[0] != want[0] || spans[1] != want[1] {
			t.Fatalf("hover %v spans = %v, want %v", cp, spans, want)
		}
	}
}

func TestDetectURLAt_IgnoresLinkID(t *testing.T) {
	// detectURLAt does not consult cell.LinkID: OSC 8 precedence is enforced by
	// the caller (updateHover / onMouseUp), which skips detection when a cell
	// already carries an explicit link. This test pins that contract so the
	// gating is not accidentally moved here.
	g := newGrid(3, 40)
	putRow(g, "https://go.dev")
	// Stamp an explicit link on the first cell; detection still finds the URL.
	sb := g.Scrollback.Len()
	g.Cells[0].LinkID = 1
	if _, _, ok := g.detectURLAt(contentPos{Row: sb, Col: 2}); !ok {
		t.Error("detectURLAt should match regardless of LinkID (gating is external)")
	}
}

// ---------------------------------------------------------------------------
// trimTrailingURL
// ---------------------------------------------------------------------------

func TestTrimTrailingURL_NoPunctuation(t *testing.T) {
	runes := []rune("https://go.dev/path")
	if got := trimTrailingURL(runes); got != len(runes) {
		t.Errorf("no punctuation: got %d, want %d", got, len(runes))
	}
}

func TestTrimTrailingURL_RemovesTrailingDot(t *testing.T) {
	if got := trimTrailingURL([]rune("https://go.dev.")); got != 14 {
		t.Errorf("trailing dot: got %d, want 14", got)
	}
}

func TestTrimTrailingURL_KeepsBalancedSquareBrackets(t *testing.T) {
	// IPv6-ish URL with balanced brackets.
	runes := []rune("https://[::1]/path")
	n := trimTrailingURL(runes)
	if n != len(runes) {
		t.Errorf("balanced brackets: got %d, want %d (full length)", n, len(runes))
	}
}

func TestTrimTrailingURL_TrimsUnbalancedSquareBracket(t *testing.T) {
	runes := []rune("https://go.dev/path]")
	n := trimTrailingURL(runes)
	if string(runes[:n]) != "https://go.dev/path" {
		t.Errorf("unbalanced ]: got %q, want 'https://go.dev/path'", string(runes[:n]))
	}
}

func TestTrimTrailingURL_KeepsBalancedCurlyBraces(t *testing.T) {
	runes := []rune("https://go.dev/path{id}rest")
	// No trailing punctuation — should keep full length.
	n := trimTrailingURL(runes)
	if n != len(runes) {
		t.Errorf("balanced braces: got %d, want %d", n, len(runes))
	}
	trailing := "https://go.dev/path{id}}"
	runes2 := []rune(trailing)
	n2 := trimTrailingURL(runes2)
	exp := "https://go.dev/path{id}"
	if string(runes2[:n2]) != exp {
		t.Errorf("unbalanced }: got %q, want %q", string(runes2[:n2]), exp)
	}
}

func TestTrimTrailingURL_Empty(t *testing.T) {
	if n := trimTrailingURL(nil); n != 0 {
		t.Errorf("nil: got %d, want 0", n)
	}
	if n := trimTrailingURL([]rune{}); n != 0 {
		t.Errorf("empty: got %d, want 0", n)
	}
}

func TestTrimTrailingURL_OnlyPunctuation(t *testing.T) {
	if n := trimTrailingURL([]rune(".,;:!?)]}")); n != 0 {
		t.Errorf("all punctuation: got %d, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// spansFor
// ---------------------------------------------------------------------------

func TestSpansFor_SingleRow(t *testing.T) {
	rows := []int{5, 5, 5}
	cols := []int{0, 3, 5}
	spans := spansFor(rows, cols, 0, 3)
	if len(spans) != 1 || spans[0] != (urlSpan{Row: 5, C0: 0, C1: 5}) {
		t.Fatalf("single row: got %v, want [{5 0 5}]", spans)
	}
}

func TestSpansFor_TwoRows(t *testing.T) {
	rows := []int{5, 5, 6, 6, 6}
	cols := []int{0, 3, 5, 3, 0}
	spans := spansFor(rows, cols, 0, 5)
	if len(spans) != 2 {
		t.Fatalf("two rows: got %d spans, want 2", len(spans))
	}
	if spans[0] != (urlSpan{Row: 5, C0: 0, C1: 3}) {
		t.Errorf("first span: got %v, want {5 0 3}", spans[0])
	}
	if spans[1] != (urlSpan{Row: 6, C0: 5, C1: 0}) {
		t.Errorf("second span: got %v, want {6 5 0}", spans[1])
	}
}

func TestSpansFor_PartialRange(t *testing.T) {
	// Test that is/ie slice into the middle of the input arrays.
	rows := []int{0, 0, 1, 1}
	cols := []int{2, 5, 0, 9}
	spans := spansFor(rows, cols, 2, 4) // only last two elements
	if len(spans) != 1 || spans[0] != (urlSpan{Row: 1, C0: 0, C1: 9}) {
		t.Fatalf("partial: got %v, want [{1 0 9}]", spans)
	}
}
