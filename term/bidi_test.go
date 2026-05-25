package term

import "testing"

// makeTestRow builds a cols-wide row of cells from the given runes.
// Cells beyond len(runes) are zero-initialized (Ch=0, Width=0), matching
// the uninitialized state of a NewGrid cell buffer, so bidi ignores them.
func makeTestRow(runes []rune, cols int) []Cell {
	row := make([]Cell, cols) // zero-value: Ch=0, Width=0
	for i, r := range runes {
		if i >= cols {
			break
		}
		row[i] = Cell{Ch: r, Width: 1, FG: DefaultColor, BG: DefaultColor}
	}
	return row
}

func TestRowHasRTL(t *testing.T) {
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
			got := rowHasRTL(row, 20)
			if got != tc.want {
				t.Errorf("rowHasRTL = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRowHasRTL_SkipsContinuation(t *testing.T) {
	// A row with only continuation cells (Width=0, Ch=0) should return false
	// even though they occupy non-zero positions.
	row := []Cell{
		{Width: 2, Ch: '日', FG: DefaultColor, BG: DefaultColor}, // wide LTR
		{Width: 0, Ch: 0, FG: DefaultColor, BG: DefaultColor},    // continuation
	}
	if rowHasRTL(row, 2) {
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
	// "שלום" — Hebrew for "shalom". Logical order: ש ל ו ם (indices 0-3).
	// Visual order in RTL paragraph: ם ו ל ש.
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

	// First four visual positions carry the reversed Hebrew chars.
	wantVis := []rune{'ם', 'ו', 'ל', 'ש'}
	for i, want := range wantVis {
		if vis[i].Ch != want {
			t.Errorf("vis[%d].Ch = %q, want %q", i, string(vis[i].Ch), string(want))
		}
	}

	// v2l: visual[0] maps to logical 3 (ם), and so on; padding = -1.
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

func TestRowHasRTL_ColsExceedsSliceLen(t *testing.T) {
	// cols larger than the slice must not panic (scrollback-resize scenario).
	row := []Cell{
		{Ch: 'ש', Width: 1, FG: DefaultColor, BG: DefaultColor},
		{Ch: 'ל', Width: 1, FG: DefaultColor, BG: DefaultColor},
	}
	got := rowHasRTL(row, 100)
	if !got {
		t.Error("expected true: RTL chars present in the available cells")
	}
	// LTR slice narrower than cols must also not panic.
	ltr := []Cell{{Ch: 'A', Width: 1, FG: DefaultColor, BG: DefaultColor}}
	if rowHasRTL(ltr, 50) {
		t.Error("expected false: no RTL chars")
	}
}

func TestVisualReorder_ColsExceedsSliceLen(t *testing.T) {
	// cols larger than the slice must not panic and must still reorder correctly.
	row := []Cell{
		{Ch: 'ש', Width: 1, FG: DefaultColor, BG: DefaultColor},
		{Ch: 'ל', Width: 1, FG: DefaultColor, BG: DefaultColor},
		{Ch: 'ו', Width: 1, FG: DefaultColor, BG: DefaultColor},
		{Ch: 'ם', Width: 1, FG: DefaultColor, BG: DefaultColor},
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
	wide := Cell{Ch: 'ﺎ', Width: 2, FG: DefaultColor, BG: DefaultColor} // Arabic presentation form
	cont := Cell{Ch: 0, Width: 0, FG: DefaultColor, BG: DefaultColor}
	ltr := Cell{Ch: 'A', Width: 1, FG: DefaultColor, BG: DefaultColor}
	row := []Cell{wide, cont, ltr}

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
	row := []Cell{
		{Width: 2, Ch: '日', FG: DefaultColor, BG: DefaultColor},
		{Width: 0, Ch: 0, FG: DefaultColor, BG: DefaultColor}, // continuation
		{Width: 1, Ch: 'A', FG: DefaultColor, BG: DefaultColor},
	}
	vis, v2l := visualReorder(row, 3)
	if vis != nil || v2l != nil {
		t.Errorf("expected nil for LTR wide chars, got vis=%v v2l=%v", vis, v2l)
	}
}
