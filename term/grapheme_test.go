package term

import "testing"

// cellAt is a tiny helper for these tests; returns the live-grid cell.
func gcell(g *grid, r, c int) cell { return *g.At(r, c) }

// feedStr feeds a string and flushes (Feed flushes at end of batch).
func feedStr(t *testing.T, g *grid, p *parser, s string) {
	t.Helper()
	feed(t, g, p, []byte(s))
}

// TestGrapheme_VS16 verifies an emoji variation selector promotes a
// default-text-presentation base to width 2 (one cell, full cluster stored).
func TestGrapheme_VS16(t *testing.T) {
	g, p := newParserGrid(1, 10)
	feedStr(t, g, p, "❤️") // ❤️ heart + VS16
	if g.CursorC != 2 {
		t.Fatalf("cursor col = %d, want 2", g.CursorC)
	}
	c := gcell(g, 0, 0)
	if c.Width != 2 {
		t.Errorf("width = %d, want 2", c.Width)
	}
	if c.clusterID == 0 {
		t.Fatalf("clusterID = 0, want multi-rune cluster")
	}
	if got := g.clusters[c.clusterID]; got != "❤️" {
		t.Errorf("cluster = %q, want heart+VS16", got)
	}
}

// TestGrapheme_VS15 verifies the text variation selector narrows a
// default-emoji base to width 1.
func TestGrapheme_VS15(t *testing.T) {
	g, p := newParserGrid(1, 10)
	feedStr(t, g, p, "⌚︎") // ⌚ watch + VS15 (text)
	if g.CursorC != 1 {
		t.Fatalf("cursor col = %d, want 1", g.CursorC)
	}
	if w := gcell(g, 0, 0).Width; w != 1 {
		t.Errorf("width = %d, want 1", w)
	}
}

// TestGrapheme_ZWJ verifies a ZWJ emoji sequence occupies a single width-2
// cell with the whole cluster preserved.
func TestGrapheme_ZWJ(t *testing.T) {
	g, p := newParserGrid(1, 10)
	seq := "\U0001f469\u200d\U0001f680" // woman + ZWJ + rocket
	feedStr(t, g, p, seq)
	if g.CursorC != 2 {
		t.Fatalf("cursor col = %d, want 2", g.CursorC)
	}
	c := gcell(g, 0, 0)
	if c.Width != 2 {
		t.Errorf("width = %d, want 2", c.Width)
	}
	if got := g.clusters[c.clusterID]; got != seq {
		t.Errorf("cluster = %q, want full ZWJ sequence", got)
	}
}

// TestGrapheme_Flag verifies a regional-indicator pair forms one width-2 flag.
func TestGrapheme_Flag(t *testing.T) {
	g, p := newParserGrid(1, 10)
	feedStr(t, g, p, "\U0001f1fa\U0001f1f8") // 🇺🇸
	if g.CursorC != 2 {
		t.Fatalf("cursor col = %d, want 2", g.CursorC)
	}
	if w := gcell(g, 0, 0).Width; w != 2 {
		t.Errorf("width = %d, want 2", w)
	}
	// A third regional indicator must start a new flag at col 2.
	feedStr(t, g, p, "\U0001f1e6") // 🇦 (lone RI)
	if g.CursorC < 2 {
		t.Errorf("third RI did not advance: col = %d", g.CursorC)
	}
}

// TestGrapheme_Combining verifies a base + combining mark is one cell whose
// cluster carries both runes (Indic / Latin diacritic case).
func TestGrapheme_Combining(t *testing.T) {
	g, p := newParserGrid(1, 10)
	feedStr(t, g, p, "é") // e + combining acute = é
	if g.CursorC != 1 {
		t.Fatalf("cursor col = %d, want 1", g.CursorC)
	}
	c := gcell(g, 0, 0)
	if c.Ch != 'e' {
		t.Errorf("base rune = %q, want 'e'", c.Ch)
	}
	if got := g.clusters[c.clusterID]; got != "é" {
		t.Errorf("cluster = %q, want e+acute", got)
	}
}

// TestGrapheme_Virama verifies a Brahmic conjunct (consonant + virama +
// consonant, here Javanese "ꦏ꧀ꦏ") fuses into one width-2 cell. uniseg's
// UAX#29 boundaries would split it at the pangkon (U+A9C0) and over-advance;
// leadingAkshara fuses across the virama to match the terminal-cell model.
func TestGrapheme_Virama(t *testing.T) {
	g, p := newParserGrid(1, 10)
	seq := "ꦏ꧀ꦏ" // KA + PANGKON + KA
	feedStr(t, g, p, seq)
	if g.CursorC != 2 {
		t.Fatalf("cursor col = %d, want 2", g.CursorC)
	}
	c := gcell(g, 0, 0)
	if c.Width != 2 {
		t.Errorf("width = %d, want 2", c.Width)
	}
	if c.Ch != 0xa98f {
		t.Errorf("base rune = %U, want U+A98F", c.Ch)
	}
	if got := g.clusters[c.clusterID]; got != seq {
		t.Errorf("cluster = %q, want full conjunct", got)
	}
}

// TestGrapheme_ViramaSpacingMarks verifies a syllable with trailing spacing
// vowel signs (Mc) caps at width 2 (Javanese "ꦏꦺꦴ" = KA + TALING + TARUNG,
// which uniseg alone widths as 3) and that two such syllables advance 4.
func TestGrapheme_ViramaSpacingMarks(t *testing.T) {
	g, p := newParserGrid(1, 10)
	feedStr(t, g, p, "ꦏꦺꦴꦏꦺꦴ")
	if g.CursorC != 4 {
		t.Fatalf("cursor col = %d, want 4", g.CursorC)
	}
	for _, col := range []int{0, 2} {
		if w := gcell(g, 0, col).Width; w != 2 {
			t.Errorf("col %d width = %d, want 2", col, w)
		}
	}
}

// TestGrapheme_ViramaZWJConjunct verifies an explicit-conjunct request
// (consonant + virama + ZWJ + consonant + vowel sign, Marathi "र्<ZWJ>या") fuses
// into one width-2 cell. The ZWJ-terminated cluster must still be recognized
// as virama-ending for fusion.
func TestGrapheme_ViramaZWJConjunct(t *testing.T) {
	g, p := newParserGrid(1, 10)
	feedStr(t, g, p, "र्\u200dया") // RA virama ZWJ YA AA
	if g.CursorC != 2 {
		t.Fatalf("cursor col = %d, want 2", g.CursorC)
	}
	if w := gcell(g, 0, 0).Width; w != 2 {
		t.Errorf("width = %d, want 2", w)
	}
}

// TestGrapheme_DanglingVirama verifies a dead consonant shown in isolation
// (base + trailing virama, no following consonant — Javanese "ꦏ꧀") is width 1:
// the virama is zero-width in the cell model, even though uniseg widths the
// pangkon cluster at 2.
func TestGrapheme_DanglingVirama(t *testing.T) {
	g, p := newParserGrid(1, 10)
	feedStr(t, g, p, "ꦏ꧀") // KA + PANGKON
	if g.CursorC != 1 {
		t.Fatalf("cursor col = %d, want 1", g.CursorC)
	}
	if w := gcell(g, 0, 0).Width; w != 1 {
		t.Errorf("width = %d, want 1", w)
	}
}

// TestGrapheme_ViramaSplitFeed verifies a conjunct split across two feeds
// (the PTY read-boundary case) still fuses: the pending virama-terminated
// syllable waits for the trailing consonant instead of committing early.
func TestGrapheme_ViramaSplitFeed(t *testing.T) {
	g, p := newParserGrid(1, 10)
	g.Mu.Lock()
	p.feedChunk([]byte("ꦏ꧀")) // KA + PANGKON, mid-burst: stays pending
	if g.CursorC != 0 {
		g.Mu.Unlock()
		t.Fatalf("virama committed early: col = %d, want 0 pending", g.CursorC)
	}
	p.feedChunk([]byte("ꦏ")) // KA arrives
	g.FlushGrapheme()        // burst drained
	g.Mu.Unlock()
	if g.CursorC != 2 {
		t.Fatalf("cursor col = %d, want 2", g.CursorC)
	}
	if w := gcell(g, 0, 0).Width; w != 2 {
		t.Errorf("width = %d, want 2", w)
	}
}

// TestGrapheme_FlushBeforeControl verifies the pending cluster is committed
// before a CSI sequence so cursor-position reports are accurate. Here a DSR
// after an emoji must reflect the advanced column.
func TestGrapheme_FlushBeforeControl(t *testing.T) {
	g, p := newParserGrid(1, 10)
	var replies [][]byte
	p.SetReplyHandler(func(b []byte) { replies = append(replies, append([]byte(nil), b...)) })
	feedStr(t, g, p, "\U0001f469\u200d\U0001f680\x1b[6n") // ZWJ emoji then DSR
	if len(replies) != 1 {
		t.Fatalf("got %d replies, want 1", len(replies))
	}
	// Cursor was at col 2 (0-based) => reported column 3 (1-based).
	want := "\x1b[1;3R"
	if string(replies[0]) != want {
		t.Errorf("DSR reply = %q, want %q", replies[0], want)
	}
}

// TestGrapheme_PlainASCIIUnaffected guards the common path: ordinary ASCII
// text still lands one rune per cell with no cluster allocation.
func TestGrapheme_PlainASCIIUnaffected(t *testing.T) {
	g, p := newParserGrid(1, 10)
	feedStr(t, g, p, "hello")
	if g.CursorC != 5 {
		t.Fatalf("cursor col = %d, want 5", g.CursorC)
	}
	for c, want := range []rune("hello") {
		got := gcell(g, 0, c)
		if got.Ch != want || got.clusterID != 0 || got.Width != 1 {
			t.Errorf("cell %d = %+v, want rune %q width 1 no cluster", c, got, want)
		}
	}
	if len(g.clusters) != 0 {
		t.Errorf("cluster pool grew to %d for plain ASCII", len(g.clusters))
	}
}

// TestGrapheme_EawWide verifies a codepoint reclassified Wide since
// Unicode 15.0.0 (U+1FADF SPLATTER) renders at width 2 through the full
// grapheme pipeline (PutRune → leadingAkshara → wcwidthWidth → eawWide).
func TestGrapheme_EawWide(t *testing.T) {
	g, p := newParserGrid(1, 10)
	feedStr(t, g, p, "\U0001FADF") // SPLATTER — wcwidth says 2, uniseg 15 says 1
	if g.CursorC != 2 {
		t.Fatalf("cursor col = %d, want 2", g.CursorC)
	}
	if w := gcell(g, 0, 0).Width; w != 2 {
		t.Errorf("width = %d, want 2", w)
	}
}

// TestGrapheme_StandaloneEmojiModifier verifies a standalone emoji skin-tone
// modifier (U+1F3FB) renders at width 2 through the grapheme pipeline.
// uniseg zero-widths it (Grapheme_Extend), but wcwidth renders it wide.
func TestGrapheme_StandaloneEmojiModifier(t *testing.T) {
	g, p := newParserGrid(1, 10)
	feedStr(t, g, p, "\U0001F3FB") // EMOJI MODIFIER FITZPATRICK TYPE-1-2
	if g.CursorC != 2 {
		t.Fatalf("cursor col = %d, want 2", g.CursorC)
	}
	if w := gcell(g, 0, 0).Width; w != 2 {
		t.Errorf("width = %d, want 2", w)
	}
}
