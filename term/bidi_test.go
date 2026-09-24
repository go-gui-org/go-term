package term

import (
	"slices"
	"testing"
)

// makeTestRow builds a cols-wide row of cells from the given runes.
// Cells beyond len(runes) are space-filled (Ch=' ', Width=1), matching
// the production grid — newGrid fills every cell with defaultCell, so
// real rows never hold Ch==0 outside wide-char continuations. A bidi
// test that leaves them null would reorder differently from production
// (nulls are excluded from the paragraph, spaces participate).
func makeTestRow(runes []rune, cols int) []cell {
	row := make([]cell, cols)
	for i := range row {
		row[i] = cell{Ch: ' ', Width: 1, FG: defaultColor, BG: defaultColor}
	}
	for i, r := range runes {
		if i >= cols {
			break
		}
		row[i] = cell{Ch: r, Width: 1, FG: defaultColor, BG: defaultColor}
	}
	return row
}

// makeNullRow builds a cols-wide row where untouched cells stay
// zero-initialized (Ch=0). Only for pinning the null-exclusion rule;
// production code paths must prefer makeTestRow.
func makeNullRow(runes []rune, cols int) []cell {
	row := make([]cell, cols) // zero-value: Ch=0, Width=0
	for i, r := range runes {
		if i >= cols {
			break
		}
		row[i] = cell{Ch: r, Width: 1, FG: defaultColor, BG: defaultColor}
	}
	return row
}

func TestRowNeedsBidi(t *testing.T) {
	cases := []struct {
		name  string
		runes []rune
		want  bool
	}{
		{"empty", nil, false},
		{"ascii", []rune("hello world"), false},
		{"hebrew", []rune("שלום"), true},
		{"arabic", []rune("مرحبا"), true},
		{"mixed_starts_ltr", []rune("hi שלום"), true},
		{"mixed_starts_rtl", []rune("שלום hi"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := makeTestRow(tc.runes, 20)
			got := rowNeedsBidi(row, 20)
			if got != tc.want {
				t.Errorf("rowNeedsBidi = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRowNeedsBidi_SkipsContinuation(t *testing.T) {
	// A row with only continuation cells (Width=0, Ch=0) should return false
	// even though they occupy non-zero positions.
	row := []cell{
		{Width: 2, Ch: '日', FG: defaultColor, BG: defaultColor}, // wide LTR
		{Width: 0, Ch: 0, FG: defaultColor, BG: defaultColor},   // continuation
	}
	if rowNeedsBidi(row, 2) {
		t.Error("continuation cell wrongly flagged as RTL")
	}
}

func TestVisualReorder_LTR(t *testing.T) {
	row := makeTestRow([]rune("hello"), 10)
	vis, v2l := visualReorder(row, 10)
	if vis != nil || v2l != nil {
		t.Error("expected nil, nil for LTR-only row")
	}
}

func TestVisualReorder_Empty(t *testing.T) {
	row := makeTestRow(nil, 8)
	vis, v2l := visualReorder(row, 8)
	if vis != nil || v2l != nil {
		t.Error("expected nil, nil for blank row")
	}
}

func TestVisualReorder_PureRTL(t *testing.T) {
	// "שלום" on a space-filled row — the production shape. Spaces
	// participate in the paragraph, so an RTL-base row renders
	// paragraph-aligned: trailing spaces swing to the front.
	// Logical:  ש ל ו ם ' ' ' ' ' ' ' '   (indices 0–7)
	// Visual:   ' ' ' ' ' ' ' ' ם ו ל ש
	runes := []rune{'ש', 'ל', 'ו', 'ם'}
	const cols = 8
	row := makeTestRow(runes, cols)

	vis, v2l := visualReorder(row, cols)
	if vis == nil {
		t.Fatal("expected visual reordering for RTL content, got nil")
	}
	if len(vis) != cols {
		t.Fatalf("len(vis) = %d, want %d", len(vis), cols)
	}
	if len(v2l) != cols {
		t.Fatalf("len(v2l) = %d, want %d", len(v2l), cols)
	}

	// Leading positions carry the swung-forward spaces.
	for i := 0; i < 4; i++ {
		if vis[i].Ch != ' ' {
			t.Errorf("vis[%d].Ch = %q, want space", i, string(vis[i].Ch))
		}
		if v2l[i] != 7-i {
			t.Errorf("v2l[%d] = %d, want %d", i, v2l[i], 7-i)
		}
	}
	// Trailing positions carry the reversed Hebrew chars.
	wantVis := []rune{'ם', 'ו', 'ל', 'ש'}
	for i, want := range wantVis {
		if vis[4+i].Ch != want {
			t.Errorf("vis[%d].Ch = %q, want %q", 4+i, string(vis[4+i].Ch), string(want))
		}
		if v2l[4+i] != 3-i {
			t.Errorf("v2l[%d] = %d, want %d", 4+i, v2l[4+i], 3-i)
		}
	}
}

func TestVisualReorder_NullExcluded(t *testing.T) {
	// Null cells (continuations, never-written buffer) stay out of the
	// paragraph; the output pads with blanks carrying v2l -1.
	runes := []rune{'ש', 'ל', 'ו', 'ם'}
	const cols = 8
	row := makeNullRow(runes, cols)

	vis, v2l := visualReorder(row, cols)
	if vis == nil {
		t.Fatal("expected visual reordering for RTL content, got nil")
	}
	wantVis := []rune{'ם', 'ו', 'ל', 'ש'}
	for i, want := range wantVis {
		if vis[i].Ch != want {
			t.Errorf("vis[%d].Ch = %q, want %q", i, string(vis[i].Ch), string(want))
		}
	}
	wantV2L := []int{3, 2, 1, 0, -1, -1, -1, -1}
	for i, want := range wantV2L {
		if v2l[i] != want {
			t.Errorf("v2l[%d] = %d, want %d", i, v2l[i], want)
		}
	}
}

func TestVisualReorder_Mixed(t *testing.T) {
	// "hi שלום end" — LTR paragraph with embedded RTL run.
	// Logical:  h i ' ' ש ל ו ם ' ' e n d   (indices 0–10)
	// Visual:   h i ' ' ם ו ל ש ' ' e n d
	runes := []rune{'h', 'i', ' ', 'ש', 'ל', 'ו', 'ם', ' ', 'e', 'n', 'd'}
	cols := len(runes)
	row := makeTestRow(runes, cols)

	vis, v2l := visualReorder(row, cols)
	if vis == nil {
		t.Fatal("expected visual reordering, got nil")
	}

	// LTR prefix unchanged.
	for i, want := range []rune{'h', 'i', ' '} {
		if vis[i].Ch != want {
			t.Errorf("vis[%d].Ch = %q, want %q", i, string(vis[i].Ch), string(want))
		}
	}
	// RTL run reversed: visual positions 3–6 carry ם ו ל ש.
	for i, want := range []rune{'ם', 'ו', 'ל', 'ש'} {
		if vis[3+i].Ch != want {
			t.Errorf("vis[%d].Ch = %q, want %q", 3+i, string(vis[3+i].Ch), string(want))
		}
	}
	// LTR suffix unchanged.
	for i, want := range []rune{' ', 'e', 'n', 'd'} {
		if vis[7+i].Ch != want {
			t.Errorf("vis[%d].Ch = %q, want %q", 7+i, string(vis[7+i].Ch), string(want))
		}
	}

	// v2l for Hebrew portion: visual[3]=6, [4]=5, [5]=4, [6]=3.
	for i, want := range []int{6, 5, 4, 3} {
		if v2l[3+i] != want {
			t.Errorf("v2l[%d] = %d, want %d", 3+i, v2l[3+i], want)
		}
	}
}

func TestRowNeedsBidi_ExplicitFormats(t *testing.T) {
	// Explicit controls without strong content are display-inert
	// (single LTR run through x/text), so the gate skips them — the
	// reorder pass would be identity. RLM is class R: a real trigger.
	cases := []struct {
		name  string
		runes []rune
		want  bool
	}{
		{"rlo_only", []rune{'\u202B', 'a', 'b', 'c', '\u202C'}, false},
		{"lro_only", []rune{'\u202D', 'a', '\u202C'}, false},
		{"rli_pdi_only", []rune{'\u2067', 'a', '\u2069'}, false},
		{"rlo_with_hebrew", []rune{'\u202B', 'ש', 'ל', '\u202C'}, true},
		{"rlm", []rune{'\u200F'}, true}, // RLM is class R
		{"lrm_only", []rune{'\u200E', 'a'}, false},
		{"digits_only", []rune("123 456"), false},
		{"arabic_digits_only", []rune("١٢٣"), false}, // AN without strong RTL: no reorder
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := makeTestRow(tc.runes, 12)
			if got := rowNeedsBidi(row, 12); got != tc.want {
				t.Errorf("rowNeedsBidi = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVisualReorder_ExplicitOnlyInert(t *testing.T) {
	// No strong content: the gate skips and display stays logical.
	row := makeNullRow([]rune{'\u202B', 'a', 'b', 'c', '\u202C'}, 5)
	if vis, v2l := visualReorder(row, 5); vis != nil || v2l != nil {
		t.Errorf("expected nil, nil for explicit-only row, got %v %v", vis, v2l)
	}
}

func TestVisualReorder_ExplicitWithStrong(t *testing.T) {
	// Strong content inside an override still reorders via its run.
	row := makeNullRow([]rune{'\u202B', 'ש', 'ל', '\u202C'}, 4)
	vis, _ := visualReorder(row, 4)
	if vis == nil {
		t.Fatal("expected reordering, got nil")
	}
	if vis[1].Ch != 'ל' || vis[2].Ch != 'ש' {
		t.Errorf("Hebrew run not reversed: %q %q", vis[1].Ch, vis[2].Ch)
	}
}

func TestVisualReorder_Mirroring(t *testing.T) {
	// UAX#9 rule L4: ASCII brackets in RTL runs render mirrored, so a
	// parenthesized Hebrew word keeps its parens outside, not swapped.
	row := makeNullRow([]rune{'(', 'א', 'ב', ')'}, 4)

	vis, _ := visualReorder(row, 4)
	if vis == nil {
		t.Fatal("expected reordering, got nil")
	}
	want := []rune{'(', 'ב', 'א', ')'}
	for i, w := range want {
		if vis[i].Ch != w {
			t.Errorf("vis[%d].Ch = %q, want %q", i, string(vis[i].Ch), string(w))
		}
	}
}

func TestMirrorRTL_Table(t *testing.T) {
	pairs := map[rune]rune{'(': ')', ')': '(', '[': ']', ']': '[', '{': '}', '}': '{', '<': '>', '>': '<'}
	for in, want := range pairs {
		if got := mirrorRTL(in); got != want {
			t.Errorf("mirrorRTL(%q) = %q, want %q", string(in), string(got), string(want))
		}
	}
	if got := mirrorRTL('א'); got != 'א' {
		t.Errorf("mirrorRTL must pass non-brackets through, got %q", string(got))
	}
}

func TestScanBidiCells_OneRunePerEntry(t *testing.T) {
	// The run.Pos()→entries index alignment depends on exactly one rune
	// per entry, even for cluster cells (tails dropped by design).
	row := makeTestRow([]rune{'a', 'ש', 'b'}, 6)
	row[1].clusterID = 1 // pretend the Hebrew cell carries a cluster tail
	entries, s := scanBidiCells(row, 6)
	if len(entries) != len([]rune(s)) {
		t.Errorf("entries=%d, runes=%d: alignment broken", len(entries), len([]rune(s)))
	}
	// Spaces participate: 3 content + 3 fill = 6 entries.
	if len(entries) != 6 {
		t.Errorf("len(entries) = %d, want 6 (spaces included)", len(entries))
	}
}

func TestFastPath_ZeroAlloc(t *testing.T) {
	row := makeTestRow([]rune("hello world"), 40)
	if n := testing.AllocsPerRun(50, func() { _ = rowNeedsBidi(row, 40) }); n != 0 {
		t.Errorf("rowNeedsBidi allocated %v times; want 0", n)
	}
	if n := testing.AllocsPerRun(50, func() {
		vis, v2l := visualReorder(row, 40)
		if vis != nil || v2l != nil {
			t.Error("expected nil for LTR row")
		}
	}); n != 0 {
		t.Errorf("visualReorder LTR path allocated %v times; want 0", n)
	}
}

func TestLogicalMappings(t *testing.T) {
	// Pure reversal of 8 columns, as a space-filled RTL row produces.
	v2l := []int{7, 6, 5, 4, 3, 2, 1, 0}
	const cols = 8
	if got := logicalCell(v2l, 0, cols); got != 7 {
		t.Errorf("logicalCell(v=0) = %d, want 7", got)
	}
	// A reversed row's visual left edge is its logical end, and its visual
	// right edge is logical 0.
	if got := logicalBoundary(v2l, 0, cols); got != 8 {
		t.Errorf("logicalBoundary(b=0) = %d, want 8", got)
	}
	if got := logicalBoundary(v2l, 8, cols); got != 0 {
		t.Errorf("logicalBoundary(b=8) = %d, want 0", got)
	}
	// Visual glyphs [2,5) are logical {5,4,3} → span [3,6).
	if l0, l1, ok := logicalSpan(v2l, 2, 5, cols); !ok || l0 != 3 || l1 != 6 {
		t.Errorf("logicalSpan(2,5) = (%d,%d,%v), want (3,6,true)", l0, l1, ok)
	}
	// Reversed args normalize.
	if l0, l1, ok := logicalSpan(v2l, 5, 2, cols); !ok || l0 != 3 || l1 != 6 {
		t.Errorf("logicalSpan(5,2) = (%d,%d,%v), want (3,6,true)", l0, l1, ok)
	}
	// Logical [3,5] covers visual {4,3,2} → [2,4].
	if v0, v1, ok := visualSpan(v2l, 3, 5); !ok || v0 != 2 || v1 != 4 {
		t.Errorf("visualSpan(3,5) = (%d,%d,%v), want (2,4,true)", v0, v1, ok)
	}
	if got := visualPoint(v2l, 5); got != 2 {
		t.Errorf("visualPoint(5) = %d, want 2", got)
	}
	if got := visualPoint(v2l, 99); got != -1 {
		t.Errorf("visualPoint(missing) = %d, want -1", got)
	}
	// Nil map is identity; padding (-1) holds position.
	if got := logicalCell(nil, 3, cols); got != 3 {
		t.Errorf("logicalCell(nil) = %d, want 3", got)
	}
	pad := []int{1, 0, -1, -1}
	if got := logicalCell(pad, 2, 4); got != 2 {
		t.Errorf("logicalCell(padding) = %d, want 2", got)
	}
	if _, _, ok := logicalSpan(pad, 2, 4, 4); ok {
		t.Error("logicalSpan over padding only must report !ok")
	}
}

func TestVisualReorder_OutputLen(t *testing.T) {
	// Output length must always equal cols regardless of content.
	for _, cols := range []int{1, 5, 10, 80} {
		runes := []rune("שלום")
		row := makeTestRow(runes, cols)
		vis, v2l := visualReorder(row, cols)
		if vis == nil {
			t.Errorf("cols=%d: expected non-nil visual for RTL content", cols)
			continue
		}
		if len(vis) != cols {
			t.Errorf("cols=%d: len(vis) = %d, want %d", cols, len(vis), cols)
		}
		if len(v2l) != cols {
			t.Errorf("cols=%d: len(v2l) = %d, want %d", cols, len(v2l), cols)
		}
	}
}

func TestVisualReorder_V2L_Roundtrip(t *testing.T) {
	// For every non-padding visual position, the logical cell's Ch must
	// match what we placed in the visual slice.
	runes := []rune("שלום abc")
	const cols = 12
	row := makeTestRow(runes, cols)

	vis, v2l := visualReorder(row, cols)
	if vis == nil {
		t.Fatal("expected reordering")
	}
	for v := range cols {
		l := v2l[v]
		if l < 0 {
			continue // padding
		}
		if vis[v].Ch != row[l].Ch {
			t.Errorf("v2l roundtrip broken at v=%d: vis.Ch=%q but row[v2l[v]].Ch=%q",
				v, string(vis[v].Ch), string(row[l].Ch))
		}
	}
}

func TestRowNeedsBidi_ColsExceedsSliceLen(t *testing.T) {
	// cols larger than the slice must not panic (scrollback-resize scenario).
	row := []cell{
		{Ch: 'ש', Width: 1, FG: defaultColor, BG: defaultColor},
		{Ch: 'ל', Width: 1, FG: defaultColor, BG: defaultColor},
	}
	got := rowNeedsBidi(row, 100)
	if !got {
		t.Error("expected true: RTL chars present in the available cells")
	}
	// LTR slice narrower than cols must also not panic.
	ltr := []cell{{Ch: 'A', Width: 1, FG: defaultColor, BG: defaultColor}}
	if rowNeedsBidi(ltr, 50) {
		t.Error("expected false: no RTL chars")
	}
}

func TestVisualReorder_ColsExceedsSliceLen(t *testing.T) {
	// cols larger than the slice must not panic and must still reorder correctly.
	row := []cell{
		{Ch: 'ש', Width: 1, FG: defaultColor, BG: defaultColor},
		{Ch: 'ל', Width: 1, FG: defaultColor, BG: defaultColor},
		{Ch: 'ו', Width: 1, FG: defaultColor, BG: defaultColor},
		{Ch: 'ם', Width: 1, FG: defaultColor, BG: defaultColor},
	}
	vis, v2l := visualReorder(row, 100) // cols >> len(row)
	if vis == nil {
		t.Fatal("expected non-nil visual for RTL content")
	}
	// clamped to len(row)=4; visual order reverses: ם ו ל ש
	if len(vis) != 4 {
		t.Fatalf("len(vis) = %d, want 4", len(vis))
	}
	wantVis := []rune{'ם', 'ו', 'ל', 'ש'}
	for i, want := range wantVis {
		if vis[i].Ch != want {
			t.Errorf("vis[%d].Ch = %q, want %q", i, string(vis[i].Ch), string(want))
		}
	}
	if len(v2l) != 4 {
		t.Fatalf("len(v2l) = %d, want 4", len(v2l))
	}
}

func TestVisualReorder_RTLExactFill(t *testing.T) {
	// RTL chars fill exactly cols — no padding cells needed.
	// visual[:cols] / v2l[:cols] slice must still be correct.
	runes := []rune{'ש', 'ל', 'ו', 'ם'}
	const cols = 4
	row := makeTestRow(runes, cols)
	vis, v2l := visualReorder(row, cols)
	if vis == nil {
		t.Fatal("expected non-nil visual")
	}
	if len(vis) != cols {
		t.Fatalf("len(vis) = %d, want %d", len(vis), cols)
	}
	wantVis := []rune{'ם', 'ו', 'ל', 'ש'}
	for i, want := range wantVis {
		if vis[i].Ch != want {
			t.Errorf("vis[%d].Ch = %q, want %q", i, string(vis[i].Ch), string(want))
		}
	}
	wantV2L := []int{3, 2, 1, 0}
	for i, want := range wantV2L {
		if v2l[i] != want {
			t.Errorf("v2l[%d] = %d, want %d", i, v2l[i], want)
		}
	}
}

func TestVisualReorder_WideRTL(t *testing.T) {
	// A Width==2 cell inside an RTL run exercises the appendVisualCell
	// continuation-insertion branch. The continuation cell (Width==0) must
	// follow immediately after the primary cell in visual output.
	wide := cell{Ch: 'ﺎ', Width: 2, FG: defaultColor, BG: defaultColor} // Arabic presentation form
	cont := cell{Ch: 0, Width: 0, FG: defaultColor, BG: defaultColor}
	ltr := cell{Ch: 'A', Width: 1, FG: defaultColor, BG: defaultColor}
	row := []cell{wide, cont, ltr}

	vis, v2l := visualReorder(row, 3)
	if vis == nil {
		// If the wide Arabic char doesn't register as RTL (bidi class AN/NSM
		// rather than R/AL), the function correctly returns nil. Skip.
		t.Skip("wide cell not classified as strong RTL by bidi package")
	}
	if len(vis) != 3 {
		t.Fatalf("len(vis) = %d, want 3", len(vis))
	}
	// Primary wide cell must be followed by a continuation (Width==0).
	foundWide := false
	for i, c := range vis {
		if c.Width == 2 {
			foundWide = true
			if i+1 < len(vis) && vis[i+1].Width != 0 {
				t.Errorf("wide cell at vis[%d] not followed by continuation", i)
			}
		}
	}
	if !foundWide {
		t.Error("wide cell missing from visual output")
	}
	_ = v2l
}

func TestVisualReorder_WideLTR(t *testing.T) {
	// Wide (Width=2) LTR cells produce nil — no RTL content.
	row := []cell{
		{Width: 2, Ch: '日', FG: defaultColor, BG: defaultColor},
		{Width: 0, Ch: 0, FG: defaultColor, BG: defaultColor}, // continuation
		{Width: 1, Ch: 'A', FG: defaultColor, BG: defaultColor},
	}
	vis, v2l := visualReorder(row, 3)
	if vis != nil || v2l != nil {
		t.Errorf("expected nil for LTR wide chars, got vis=%v v2l=%v", vis, v2l)
	}
}

func TestVisualReorder_PreserveAttributes(t *testing.T) {
	// 🍣 (LTR) followed by RTL text
	c := cell{Ch: '🍣', Width: 2, FG: 1, BG: 2, Attrs: attrBold, LinkID: 42}
	row := []cell{
		c, {Width: 0},
		{Ch: 'א', Width: 1},
	}

	visual, _ := visualReorder(row, 3)
	if visual == nil {
		t.Fatal("expected reordering")
	}

	found := false
	for i, v := range visual {
		if v.Ch == '🍣' {
			found = true
			if i+1 >= len(visual) {
				t.Error("🍣 at end of row, no room for continuation")
			} else {
				cont := visual[i+1]
				if cont.Width != 0 || cont.LinkID != 42 || cont.Attrs != attrBold {
					t.Errorf("continuation cell at index %d lost attributes: %+v", i+1, cont)
				}
			}
		}
	}
	if !found {
		t.Error("🍣 not found in visual order")
	}
}

// BenchmarkVisualReorder_FullRow measures the BiDi visual-reordering cost
// for a row with mixed LTR/RTL content at a realistic terminal width.
func BenchmarkVisualReorder_FullRow(b *testing.B) {
	cols := 120
	row := make([]cell, cols)
	// Mix of Latin, Hebrew, and Arabic characters.
	scripts := []rune{
		'A', ' ', 'H', 'e', 'l', 'l', 'o', ' ', // LTR
		0x5E9, 0x5DC, 0x5D5, 0x5DD, ' ', // RTL Hebrew
		'W', 'o', 'r', 'l', 'd', ' ', // LTR
		0x627, 0x644, 0x639, 0x631, 0x628, 0x64A, // RTL Arabic
	}
	for i := range row {
		row[i] = cell{Ch: scripts[i%len(scripts)], Width: 1}
	}
	b.ResetTimer()
	for range b.N {
		_, _ = visualReorder(row, cols)
	}
}

// BenchmarkVisualReorder_AllLTR measures the fast-path for fully LTR rows
// (the common case). Should return nil immediately after scan.
func BenchmarkVisualReorder_AllLTR(b *testing.B) {
	cols := 120
	row := make([]cell, cols)
	for i := range row {
		row[i] = cell{Ch: 'x', Width: 1}
	}
	b.ResetTimer()
	for range b.N {
		_, _ = visualReorder(row, cols)
	}
}

// Pointer hover and motion reports map through v2lForViewportRow on every
// move; an LTR-only row must cost no allocation, and an RTL row must still
// get its map.
func TestV2LForViewportRow_LTRNoAlloc(t *testing.T) {
	tm, _ := newTestTermCapture()
	for c, r := range "hello" {
		tm.grid.At(0, c).Ch = r
	}
	if allocs := testing.AllocsPerRun(100, func() {
		if tm.v2lForViewportRow(0) != nil {
			t.Fatal("LTR row got a v2l map")
		}
	}); allocs != 0 {
		t.Errorf("LTR row: %v allocs per call, want 0", allocs)
	}
	for c, r := range "שלום" {
		tm.grid.At(1, c).Ch = r
	}
	if tm.v2lForViewportRow(1) == nil {
		t.Error("RTL row: v2l map is nil")
	}
}

// Pointer moves over an RTL row must not rerun the bidi algorithm on every
// event: once the row's map is cached, a repeat call allocates nothing. The
// cache is exact, so a rewritten row gets a fresh map.
func TestV2LForViewportRow_RTLCached(t *testing.T) {
	tm, _ := newTestTermCapture()
	for c, r := range []rune("שלום") {
		tm.grid.At(1, c).Ch = r
		tm.grid.At(1, c).Width = 1
	}
	first := tm.v2lForViewportRow(1)
	if first == nil {
		t.Fatal("RTL row: v2l map is nil")
	}
	if allocs := testing.AllocsPerRun(100, func() {
		_ = tm.v2lForViewportRow(1)
	}); allocs != 0 {
		t.Errorf("cached RTL row: %v allocs per call, want 0", allocs)
	}
	// Hover checks the old and the new row on one move; both stay cached.
	for c, r := range []rune("אבג") {
		tm.grid.At(2, c).Ch = r
		tm.grid.At(2, c).Width = 1
	}
	_ = tm.v2lForViewportRow(2)
	if allocs := testing.AllocsPerRun(100, func() {
		_ = tm.v2lForViewportRow(1)
		_ = tm.v2lForViewportRow(2)
	}); allocs != 0 {
		t.Errorf("two cached RTL rows: %v allocs per pair, want 0", allocs)
	}

	// Rewrite row 1 with an LTR word in front: the map must change.
	for c, r := range []rune("ab שלום") {
		tm.grid.At(1, c).Ch = r
		tm.grid.At(1, c).Width = 1
	}
	got := tm.v2lForViewportRow(1)
	fresh := func() []int {
		scratch := make([]cell, tm.grid.Cols)
		for c := range scratch {
			scratch[c] = tm.grid.ViewCellAt(1, c)
		}
		_, v2l := visualReorder(scratch, tm.grid.Cols)
		return v2l
	}()
	if !slices.Equal(got, fresh) {
		t.Errorf("rewritten row: cached map %v, want %v", got, fresh)
	}
}
