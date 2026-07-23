package term

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
)

// These tests pin go-term's cell-width model to jquast's python-wcwidth — the
// oracle ucs-detect measures against. The codepoint lists under testdata/ are
// ucs-detect's WIDE_CONTESTED and NARROW_CONTESTED sets: the characters where
// terminals historically disagree. Width is checked as cursor advance, exactly
// how ucs-detect measures it (write the char, read the cursor via CPR).
//
// The motivating gap: rivo/uniseg's width data is frozen at Unicode 15.0.0, so
// it under-counts symbols reclassified Wide in 16.0 (Yijing hexagrams, Tai Xuan
// Jing, counting rods, trigrams, …) plus wcwidth's wide regional indicators.
// The eawWide override (grid.go) closes that gap.

// readCodepoints loads a testdata file of one hex codepoint per line ('#'
// comments and blanks ignored).
func readCodepoints(t *testing.T, path string) []rune {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	var out []rune
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		v, err := strconv.ParseInt(line, 16, 32)
		if err != nil {
			t.Fatalf("bad codepoint %q in %s: %v", line, path, err)
		}
		out = append(out, rune(v))
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return out
}

// advance feeds a single codepoint into a fresh grid through the parser (the
// same streaming path the PTY reader uses) and returns the resulting cursor
// column — the character's measured display width.
func advance(cp rune) int {
	g, p := newParserGrid(1, 16)
	g.Mu.Lock()
	p.Feed([]byte(string(cp))) // Feed flushes any pending grapheme at end
	g.Mu.Unlock()
	return g.CursorC
}

// readSequences loads a testdata file of one space-separated hex codepoint
// sequence per line ('#' comments and blanks ignored).
func readSequences(t *testing.T, path string) [][]rune {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	var out [][]rune
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<16)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		seq := make([]rune, 0, 4)
		for _, h := range strings.Fields(line) {
			v, err := strconv.ParseInt(h, 16, 32)
			if err != nil {
				t.Fatalf("bad codepoint %q in %s: %v", h, path, err)
			}
			seq = append(seq, rune(v))
		}
		out = append(out, seq)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return out
}

// seqAdvance feeds a multi-codepoint sequence through the parser and returns the
// cursor column — the sequence's measured display width.
func seqAdvance(seq []rune) int {
	g, p := newParserGrid(1, 32)
	g.Mu.Lock()
	p.Feed([]byte(string(seq)))
	g.Mu.Unlock()
	return g.CursorC
}

// checkSequences asserts every sequence in path advances the cursor by want.
func checkSequences(t *testing.T, label, path string, want int) {
	t.Helper()
	seqs := readSequences(t, path)
	if len(seqs) == 0 {
		t.Fatal("no sequences loaded")
	}
	var fails [][]rune
	for _, s := range seqs {
		if seqAdvance(s) != want {
			fails = append(fails, s)
		}
	}
	t.Logf("%s: %d/%d width %d (%.1f%%)", label, len(seqs)-len(fails), len(seqs), want,
		100*float64(len(seqs)-len(fails))/float64(len(seqs)))
	if len(fails) != 0 {
		t.Errorf("%s: %d/%d sequences not width %d; e.g. %s",
			label, len(fails), len(seqs), want, sampleSeqHex(fails))
	}
}

// TestWidth_VS16Contested asserts a base + VS16 (emoji presentation) is width 2,
// including the ASCII keycap bases (# * 0-9) uniseg leaves narrow. This is the
// ucs-detect VS16 score.
func TestWidth_VS16Contested(t *testing.T) {
	checkSequences(t, "VS16", "testdata/vs16_contested.txt", 2)
}

// TestWidth_ZWJContested asserts every contested emoji ZWJ sequence collapses to
// a single width-2 cluster (ucs-detect ZWJ score).
func TestWidth_ZWJContested(t *testing.T) {
	checkSequences(t, "ZWJ", "testdata/zwj_contested.txt", 2)
}

// TestWidth_VS15Contested asserts a base + VS15 (text presentation) is width 1 —
// a regression guard that the VS16 widening does not spill onto VS15.
func TestWidth_VS15Contested(t *testing.T) {
	checkSequences(t, "VS15", "testdata/vs15_contested.txt", 1)
}

// TestWidth_WideContested asserts every wcwidth-wide contested codepoint
// advances the cursor by two cells. This is the ucs-detect WIDE score: uniseg
// alone scores ~55%; the eawWide override should bring it to 100%.
func TestWidth_WideContested(t *testing.T) {
	cps := readCodepoints(t, "testdata/wide_contested.txt")
	if len(cps) == 0 {
		t.Fatal("no codepoints loaded")
	}
	var fails []rune
	for _, cp := range cps {
		if advance(cp) != 2 {
			fails = append(fails, cp)
		}
	}
	pct := 100 * float64(len(cps)-len(fails)) / float64(len(cps))
	t.Logf("WIDE: %d/%d wide (%.1f%%)", len(cps)-len(fails), len(cps), pct)
	if len(fails) != 0 {
		t.Errorf("%d/%d contested codepoints not width 2; e.g. %s",
			len(fails), len(cps), sampleHex(fails))
	}
}

// TestWidth_NarrowContestedNoRegression guards against over-widening: no
// narrow contested codepoint may advance the cursor past one cell. The eawWide
// override only ever upgrades 1->2 for currently-wide codepoints, and this set
// (soft hyphen, Arabic format signs, combining marks) is disjoint from it.
func TestWidth_NarrowContestedNoRegression(t *testing.T) {
	cps := readCodepoints(t, "testdata/narrow_contested.txt")
	if len(cps) == 0 {
		t.Fatal("no codepoints loaded")
	}
	var fails []rune
	for _, cp := range cps {
		if advance(cp) > 1 {
			fails = append(fails, cp)
		}
	}
	if len(fails) != 0 {
		t.Errorf("%d narrow codepoints wrongly widened past 1 cell; e.g. %s",
			len(fails), sampleHex(fails))
	}
}

// TestWidth_RegionalIndicator checks the wcwidth special case: a lone regional
// indicator is wide (2), but a pair is one grapheme cluster still worth 2 (a
// flag) — not 4.
func TestWidth_RegionalIndicator(t *testing.T) {
	if w := advance('\U0001F1FA'); w != 2 { // lone 🇺
		t.Errorf("lone regional indicator width = %d, want 2", w)
	}
	g, p := newParserGrid(1, 16)
	g.Mu.Lock()
	p.Feed([]byte("\U0001F1FA\U0001F1F8")) // 🇺🇸 flag pair
	g.Mu.Unlock()
	if g.CursorC != 2 {
		t.Errorf("flag pair advance = %d, want 2 (one cluster, not 4)", g.CursorC)
	}
}

func sampleSeqHex(seqs [][]rune) string {
	const max = 6
	var b strings.Builder
	for i, s := range seqs {
		if i == max {
			b.WriteString("…")
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		for j, r := range s {
			if j > 0 {
				b.WriteByte('+')
			}
			b.WriteString("U+")
			b.WriteString(strings.ToUpper(strconv.FormatInt(int64(r), 16)))
		}
	}
	return b.String()
}

func sampleHex(rs []rune) string {
	const max = 8
	var b strings.Builder
	for i, r := range rs {
		if i == max {
			b.WriteString("…")
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("U+")
		b.WriteString(strings.ToUpper(strconv.FormatInt(int64(r), 16)))
	}
	return b.String()
}
