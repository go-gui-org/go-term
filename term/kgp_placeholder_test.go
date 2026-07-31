package term

import (
	"strings"
	"testing"
)

// Diacritics used throughout: the protocol's numbers 0, 1 and 2.
const (
	dia0 = "̅"
	dia1 = "̍"
	dia2 = "̎"
)

// ph builds one placeholder cluster: the placeholder rune plus diacritics.
func ph(diacritics ...string) string {
	return string(kgpPlaceholderRune) + strings.Join(diacritics, "")
}

func TestKGPDiacriticValue(t *testing.T) {
	// The spec's worked examples: U+0305 is 0, U+030D is 1, U+030E is 2.
	for _, tc := range []struct {
		r    rune
		want int
	}{{0x0305, 0}, {0x030D, 1}, {0x030E, 2}} {
		got, ok := kgpDiacriticValue(tc.r)
		if !ok || got != tc.want {
			t.Errorf("kgpDiacriticValue(U+%04X) = %d, %v; want %d, true",
				tc.r, got, ok, tc.want)
		}
	}
	if _, ok := kgpDiacriticValue('a'); ok {
		t.Error("kgpDiacriticValue('a') reported a value")
	}
	// The table is the protocol's fixed 297-entry list, sorted by code point.
	if len(kgpDiacritics) != 297 {
		t.Fatalf("kgpDiacritics has %d entries; want 297", len(kgpDiacritics))
	}
	for i := 1; i < len(kgpDiacritics); i++ {
		if kgpDiacritics[i].r <= kgpDiacritics[i-1].r {
			t.Fatalf("kgpDiacritics not sorted at index %d", i)
		}
	}
}

// The spec's own 2×2 example: image id 42 in the foreground color, row and
// column diacritics on every cell.
func TestDecodePlaceholder_SpecExample(t *testing.T) {
	fg := paletteColor(42)
	var prev placeholderCell
	pc, ok := decodePlaceholder(ph(dia0, dia0), fg, DefaultColor, prev)
	if !ok {
		t.Fatal("(0,0) did not decode")
	}
	if pc.imageID != 42 || pc.row != 0 || pc.col != 0 {
		t.Fatalf("(0,0) decoded as id=%d row=%d col=%d", pc.imageID, pc.row, pc.col)
	}
	pc, ok = decodePlaceholder(ph(dia0, dia1), fg, DefaultColor, pc)
	if !ok || pc.row != 0 || pc.col != 1 {
		t.Fatalf("(0,1) decoded as row=%d col=%d ok=%v", pc.row, pc.col, ok)
	}
	pc, ok = decodePlaceholder(ph(dia1, dia0), fg, DefaultColor, placeholderCell{})
	if !ok || pc.row != 1 || pc.col != 0 {
		t.Fatalf("(1,0) decoded as row=%d col=%d ok=%v", pc.row, pc.col, ok)
	}
}

// The third diacritic supplies the most significant byte of the image id:
// the spec's 33554474 = 42 + (2 << 24).
func TestDecodePlaceholder_MostSignificantByte(t *testing.T) {
	pc, ok := decodePlaceholder(ph(dia0, dia0, dia2),
		paletteColor(42), DefaultColor, placeholderCell{})
	if !ok {
		t.Fatal("did not decode")
	}
	if pc.imageID != 33554474 {
		t.Fatalf("imageID = %d; want 33554474", pc.imageID)
	}
}

// True-color foreground carries a 24-bit image id directly.
func TestDecodePlaceholder_TrueColorID(t *testing.T) {
	pc, ok := decodePlaceholder(ph(dia0, dia0),
		rgbColor(0x01, 0x02, 0x03), DefaultColor, placeholderCell{})
	if !ok || pc.imageID != 0x010203 {
		t.Fatalf("imageID = %#x ok=%v; want 0x010203", pc.imageID, ok)
	}
}

// The underline color names the placement.
func TestDecodePlaceholder_PlacementID(t *testing.T) {
	pc, ok := decodePlaceholder(ph(dia0, dia0),
		paletteColor(42), rgbColor(0, 0, 9), placeholderCell{})
	if !ok || pc.placementID != 9 {
		t.Fatalf("placementID = %d ok=%v; want 9", pc.placementID, ok)
	}
}

// Missing diacritics inherit from the cell to the left: the three rules in
// the spec, in one row.
func TestDecodePlaceholder_Inheritance(t *testing.T) {
	fg, ul := paletteColor(42), rgbColor(0, 0, 5)

	// Rule 0: row + column + msb, establishing the run.
	first, ok := decodePlaceholder(ph(dia1, dia0, dia2), fg, ul, placeholderCell{})
	if !ok {
		t.Fatal("first cell did not decode")
	}
	// Rule 3: row + column present, msb inherited.
	second, ok := decodePlaceholder(ph(dia1, dia1), fg, ul, first)
	if !ok || second.row != 1 || second.col != 1 ||
		second.imageID>>24 != 2 {
		t.Fatalf("row+col cell = row %d col %d id %#x", second.row, second.col, second.imageID)
	}
	// Rule 2: only the row present; column continues, msb inherited.
	third, ok := decodePlaceholder(ph(dia1), fg, ul, second)
	if !ok || third.row != 1 || third.col != 2 || third.imageID>>24 != 2 {
		t.Fatalf("row-only cell = row %d col %d id %#x", third.row, third.col, third.imageID)
	}
	// Rule 1: nothing present; row, column and msb all continue.
	fourth, ok := decodePlaceholder(ph(), fg, ul, third)
	if !ok || fourth.row != 1 || fourth.col != 3 || fourth.imageID>>24 != 2 {
		t.Fatalf("bare cell = row %d col %d id %#x", fourth.row, fourth.col, fourth.imageID)
	}
}

// Inheritance requires matching colors: a different foreground or underline
// color means a different image or placement, so the run does not carry.
func TestDecodePlaceholder_InheritanceNeedsSameColors(t *testing.T) {
	fg, ul := paletteColor(42), rgbColor(0, 0, 5)
	first, ok := decodePlaceholder(ph(dia0, dia0), fg, ul, placeholderCell{})
	if !ok {
		t.Fatal("first cell did not decode")
	}
	if _, ok := decodePlaceholder(ph(), paletteColor(43), ul, first); ok {
		t.Error("bare cell inherited across a foreground-color change")
	}
	if _, ok := decodePlaceholder(ph(), fg, rgbColor(0, 0, 6), first); ok {
		t.Error("bare cell inherited across an underline-color change")
	}
}

// A bare placeholder with no run to its left cannot be resolved.
func TestDecodePlaceholder_BareWithoutRun(t *testing.T) {
	if _, ok := decodePlaceholder(ph(), paletteColor(42), DefaultColor,
		placeholderCell{}); ok {
		t.Error("bare placeholder decoded with no preceding run")
	}
}

// Cells that are not placeholders, or that cannot name an image, are rejected.
func TestDecodePlaceholder_Rejects(t *testing.T) {
	cases := []struct {
		name string
		text string
		fg   uint32
	}{
		{"ordinary text", "a", paletteColor(42)},
		{"empty", "", paletteColor(42)},
		{"default foreground", ph(dia0, dia0), DefaultColor},
		{"palette index 0", ph(dia0, dia0), paletteColor(0)},
		{"non-table combining mark", ph(dia0) + "́", paletteColor(42)},
		{"four diacritics", ph(dia0, dia0, dia0, dia0), paletteColor(42)},
	}
	for _, tc := range cases {
		if _, ok := decodePlaceholder(tc.text, tc.fg, DefaultColor,
			placeholderCell{}); ok {
			t.Errorf("%s: decoded as a placeholder", tc.name)
		}
	}
}

// End-to-end through the parser: the spec's own 2x2 printf example, fed as
// bytes. This exercises the grapheme path — the placeholder plus its
// diacritics must land in one cell as one interned cluster, or the decode
// sees a bare placeholder and gives up.
func TestParser_PlaceholderCellsDecodeFromStream(t *testing.T) {
	h := newAPCHelper(t)
	_, b64 := makePNG(t)
	h.feedAPC("a=T,f=100,i=42,U=1,c=2,r=2,q=2;" + b64)

	// printf "\e[38;5;42m\U10EEEE\U0305\U0305\U10EEEE\U0305\U030D\n"
	// printf "\e[38;5;42m\U10EEEE\U030D\U0305\U10EEEE\U030D\U030D\e[39m\n"
	h.feed("\x1b[38;5;42m" + ph(dia0, dia0) + ph(dia0, dia1) + "\r\n")
	h.feed("\x1b[38;5;42m" + ph(dia1, dia0) + ph(dia1, dia1) + "\x1b[39m")
	h.p.Feed(nil) // flush any pending grapheme

	want := [][2]int{{0, 0}, {0, 1}, {1, 0}, {1, 1}}
	var prev placeholderCell
	i := 0
	for r := range 2 {
		prev = placeholderCell{}
		for c := range 2 {
			cell := h.g.Cells[r*h.g.Cols+c]
			if !isPlaceholderCell(cell) {
				t.Fatalf("cell (%d,%d) is %q, not a placeholder", r, c, cell.Ch)
			}
			text := string(cell.Ch)
			if cell.clusterID != 0 {
				text = h.g.clusters[cell.clusterID]
			}
			pc, ok := decodePlaceholder(text, cell.FG, cell.ULColor, prev)
			if !ok {
				t.Fatalf("cell (%d,%d) did not decode", r, c)
			}
			if pc.imageID != 42 {
				t.Fatalf("cell (%d,%d) imageID = %d; want 42", r, c, pc.imageID)
			}
			if pc.row != want[i][0] || pc.col != want[i][1] {
				t.Fatalf("cell (%d,%d) tile = (%d,%d); want %v",
					r, c, pc.row, pc.col, want[i])
			}
			prev = pc
			i++
		}
	}
}
