package term

import "testing"

func TestGrid_AddMark_ContentRow(t *testing.T) {
	g := newGrid(4, 10)
	g.ScrollbackCap = 100

	g.CursorR = 2
	g.AddMark(markPromptStart)
	if len(g.Marks) != 1 {
		t.Fatalf("want 1 mark, got %d", len(g.Marks))
	}
	if g.Marks[0].Row != 2 {
		t.Errorf("Row: got %d, want 2", g.Marks[0].Row)
	}

	g.scrollUpRegion(1)
	g.CursorR = 0
	g.AddMark(markCommandStart)
	if len(g.Marks) != 2 {
		t.Fatalf("want 2 marks, got %d", len(g.Marks))
	}

	if g.Marks[1].Row != 1 {
		t.Errorf("Row after scroll: got %d, want 1", g.Marks[1].Row)
	}
}

func TestGrid_PrevMark_NextMark(t *testing.T) {
	g := newGrid(10, 10)
	g.Marks = []mark{
		{Row: 2, Kind: markPromptStart},
		{Row: 5, Kind: markPromptStart},
		{Row: 8, Kind: markPromptStart},
	}

	row, ok := g.PrevMark(5, markPromptStart)
	if !ok || row != 2 {
		t.Errorf("PrevMark(5): got (%d,%v), want (2,true)", row, ok)
	}
	row, ok = g.PrevMark(2, markPromptStart)
	if ok {
		t.Errorf("PrevMark(2): want not-found, got row=%d", row)
	}
	row, ok = g.NextMark(5, markPromptStart)
	if !ok || row != 8 {
		t.Errorf("NextMark(5): got (%d,%v), want (8,true)", row, ok)
	}
	row, ok = g.NextMark(8, markPromptStart)
	if ok {
		t.Errorf("NextMark(8): want not-found, got row=%d", row)
	}
}

func TestGrid_TrimMarks_OnScrollbackTrim(t *testing.T) {
	g := newGrid(4, 10)
	g.ScrollbackCap = 3

	g.Marks = []mark{
		{Row: 0, Kind: markPromptStart},
		{Row: 1, Kind: markPromptStart},
		{Row: 2, Kind: markPromptStart},
	}

	g.trimMarks(1)
	if len(g.Marks) != 2 {
		t.Fatalf("after trim: want 2 marks, got %d", len(g.Marks))
	}
	if g.Marks[0].Row != 0 || g.Marks[1].Row != 1 {
		t.Errorf("rows after trim: got %d,%d; want 0,1", g.Marks[0].Row, g.Marks[1].Row)
	}
}

func TestGrid_Marks_ShiftOnResize(t *testing.T) {
	g := newGrid(4, 10)
	g.ScrollbackCap = 100

	g.Marks = []mark{{Row: 0, Kind: markPromptStart}}

	for i := range 3 {
		for c := range 10 {
			g.Cells[i*10+c] = cell{Ch: rune('a' + c), FG: defaultColor, BG: defaultColor, Width: 1}
		}
		g.RowWrapped[i] = false
	}
	g.CursorR, g.CursorC = 3, 0
	oldSbLen := g.Scrollback.Len()
	g.Resize(4, 10)
	if len(g.Marks) != 1 {
		t.Fatalf("no-op resize: want 1 mark, got %d", len(g.Marks))
	}
	_ = oldSbLen
}

func TestGrid_AddMark_AltScreenSuppressed(t *testing.T) {
	g := newGrid(4, 10)
	g.EnterAlt()
	g.AddMark(markPromptStart)
	if len(g.Marks) != 0 {
		t.Errorf("alt screen: want 0 marks, got %d", len(g.Marks))
	}
}

// The mark ring trims by copying down, not by reslicing forward: a forward
// reslice shrinks the remaining capacity on every trim, so the backing array
// reallocates and doubles once it runs out.
func TestGrid_AddMark_TrimKeepsCapacityStable(t *testing.T) {
	g := newGrid(5, 20)
	total := int16(0)
	add := func() {
		g.AddCommandEnd(total)
		total++
	}
	for range maxMarks + 1 {
		add()
	}
	capAfterFirstTrim := cap(g.Marks)

	const extra = 2000
	for range extra {
		add()
	}

	if len(g.Marks) != maxMarks {
		t.Fatalf("len(Marks) = %d; want %d", len(g.Marks), maxMarks)
	}
	if cap(g.Marks) != capAfterFirstTrim {
		t.Errorf("cap(Marks) = %d after %d more marks; want it pinned at %d",
			cap(g.Marks), extra, capAfterFirstTrim)
	}
	// Oldest-first: the surviving window ends at the newest mark and starts
	// exactly maxMarks back from it.
	if got, want := g.Marks[len(g.Marks)-1].Exit, total-1; got != want {
		t.Errorf("newest mark = %d; want %d", got, want)
	}
	if got, want := g.Marks[0].Exit, total-maxMarks; got != want {
		t.Errorf("oldest surviving mark = %d; want %d", got, want)
	}
}
