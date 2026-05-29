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
			g.Cells[i*10+c] = cell{Ch: rune('a' + c), FG: DefaultColor, BG: DefaultColor, Width: 1}
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
