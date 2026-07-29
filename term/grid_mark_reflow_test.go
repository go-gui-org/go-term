package term

import (
	"strings"
	"testing"
)

// writeLine types s at the cursor and terminates it with a hard newline,
// letting autowrap set RowWrapped so reflow sees a real logical line.
func writeLine(g *grid, s string) {
	for _, r := range s {
		g.PutRune(r)
	}
	g.FlushGrapheme()
	g.CarriageReturn()
	g.Newline()
}

// contentRowText returns the trimmed text of a content row, for asserting
// that a remapped mark still sits on the text it was placed on.
func contentRowText(g *grid, row int) string {
	var b strings.Builder
	for c := range g.Cols {
		b.WriteRune(g.ContentCellAt(row, c).Ch)
	}
	return strings.TrimRight(b.String(), " ")
}

// markRowText returns the text under the first mark of the given kind.
func markRowText(t *testing.T, g *grid, kind markKind) string {
	t.Helper()
	for _, m := range g.Marks {
		if m.Kind == kind {
			return contentRowText(g, m.Row)
		}
	}
	t.Fatalf("no mark of kind %d survived (marks=%v)", kind, g.Marks)
	return ""
}

// TestReflow_MarksFollowTheirTextOnNarrow is the case the old flat
// shift-by-scrollback-delta got wrong: a long line re-wraps into more
// physical rows, so every mark below it drifts by the accumulated extra
// rows. The mark must stay on "target" regardless of the row count change.
func TestReflow_MarksFollowTheirTextOnNarrow(t *testing.T) {
	g := newGrid(8, 20)
	g.ScrollbackCap = 200

	// A long line that wraps into 3 rows at width 20 and 6 rows at width 10.
	writeLine(g, strings.Repeat("x", 55))
	writeLine(g, "short")
	g.AddMark(markPromptStart) // sits on the row about to hold "target"
	writeLine(g, "target")

	if got := markRowText(t, g, markPromptStart); got != "target" {
		t.Fatalf("setup: mark on %q, want %q", got, "target")
	}

	g.Resize(8, 10)
	if got := markRowText(t, g, markPromptStart); got != "target" {
		t.Errorf("after narrow: mark on %q, want %q", got, "target")
	}

	g.Resize(8, 40)
	if got := markRowText(t, g, markPromptStart); got != "target" {
		t.Errorf("after widen: mark on %q, want %q", got, "target")
	}

	g.Resize(8, 20)
	if got := markRowText(t, g, markPromptStart); got != "target" {
		t.Errorf("after round trip: mark on %q, want %q", got, "target")
	}
}

// TestReflow_MarksSurviveMultipleMarks checks that several marks in one
// resize each land on their own text — the per-mark binary search into the
// logical-line table must not confuse neighbours.
func TestReflow_MarksSurviveMultipleMarks(t *testing.T) {
	g := newGrid(10, 20)
	g.ScrollbackCap = 200

	want := []string{"alpha", "beta", "gamma"}
	for _, s := range want {
		writeLine(g, strings.Repeat("y", 45)) // wrapping filler between marks
		g.AddMark(markPromptStart)
		writeLine(g, s)
	}

	g.Resize(10, 7)

	if len(g.Marks) != len(want) {
		t.Fatalf("marks dropped: got %d, want %d", len(g.Marks), len(want))
	}
	for i, m := range g.Marks {
		if got := contentRowText(g, m.Row); got != want[i] {
			t.Errorf("mark %d on %q, want %q", i, got, want[i])
		}
	}
}

// TestReflow_MarksDroppedWhenScrolledOut verifies a mark whose text falls
// out of the capped scrollback is removed rather than clamped to row 0,
// where it would become a bogus jump target.
func TestReflow_MarksDroppedWhenScrolledOut(t *testing.T) {
	g := newGrid(4, 20)
	g.ScrollbackCap = 2

	g.AddMark(markPromptStart)
	writeLine(g, "oldest")
	for range 20 {
		writeLine(g, strings.Repeat("z", 45))
	}

	g.Resize(4, 10)

	for _, m := range g.Marks {
		if contentRowText(g, m.Row) == "oldest" {
			continue // still in range: fine
		}
		if m.Row < 0 || m.Row >= g.ContentRows() {
			t.Errorf("mark row %d outside content [0,%d)", m.Row, g.ContentRows())
		}
	}
	// The mark must not have been silently parked on unrelated text.
	for _, m := range g.Marks {
		if txt := contentRowText(g, m.Row); strings.HasPrefix(txt, "z") {
			t.Errorf("mark landed on filler text %q", txt)
		}
	}
}

// TestReflow_GraphicsRideTheSameRemap pins graphics to the same tracking
// channel as marks: a graphic placed on a row must end up wherever the mark
// on that row ends up, however the line count changed underneath.
func TestReflow_GraphicsRideTheSameRemap(t *testing.T) {
	g := newGrid(8, 20)
	g.ScrollbackCap = 200

	writeLine(g, strings.Repeat("x", 55)) // 3 rows at 20 cols, 6 at 10
	g.AddMark(markPromptStart)
	g.AddGraphic("img", 4, 4) // same content row as the mark
	writeLine(g, "target")

	if len(g.Graphics) != 1 || len(g.Marks) != 1 {
		t.Fatalf("setup: %d graphics, %d marks", len(g.Graphics), len(g.Marks))
	}
	if g.Graphics[0].OriginR != g.Marks[0].Row {
		t.Fatalf("setup: graphic row %d != mark row %d", g.Graphics[0].OriginR, g.Marks[0].Row)
	}

	g.Resize(8, 10)

	if len(g.Graphics) != 1 || len(g.Marks) != 1 {
		t.Fatalf("after resize: %d graphics, %d marks", len(g.Graphics), len(g.Marks))
	}
	if g.Graphics[0].OriginR != g.Marks[0].Row {
		t.Errorf("graphic drifted off its mark: graphic %d, mark %d",
			g.Graphics[0].OriginR, g.Marks[0].Row)
	}
}

// TestResize_AltScreenSelectionKeepsScreenRow guards the one case that must
// *not* ride the reflow. On the alt screen a selection addresses alt rows,
// which resize only crops and pads; remapping them through the saved main
// buffer's re-wrap would scatter them across unrelated history. The screen
// row must survive even though the scrollback length changes underneath.
func TestResize_AltScreenSelectionKeepsScreenRow(t *testing.T) {
	g := newGrid(8, 20)
	g.ScrollbackCap = 200
	for range 12 {
		writeLine(g, strings.Repeat("q", 45)) // wrapping filler → deep scrollback
	}

	g.EnterAlt() // clears the selection, so select afterwards
	oldSB := g.Scrollback.Len()
	g.SelActive = true
	g.SelAnchor = contentPos{Row: oldSB + 2, Col: 0}
	g.SelHead = contentPos{Row: oldSB + 4, Col: 5}

	g.Resize(8, 10)

	newSB := g.Scrollback.Len()
	if newSB == oldSB {
		t.Fatalf("setup: scrollback unchanged (%d), test proves nothing", newSB)
	}
	if got := g.SelAnchor.Row - newSB; got != 2 {
		t.Errorf("anchor on alt screen row %d, want 2", got)
	}
	if got := g.SelHead.Row - newSB; got != 4 {
		t.Errorf("head on alt screen row %d, want 4", got)
	}
}

// TestResolveSelRows covers the branches the reflow only reaches when the
// re-wrap discards an endpoint's content: the survivor keeps its remapped
// row and the casualty collapses onto the edge it fell off.
func TestResolveSelRows(t *testing.T) {
	tests := []struct {
		name                          string
		newA, newH, oldA, oldH, total int
		wantA, wantH                  int
	}{
		{"both survive", 3, 7, 1, 9, 20, 3, 7},
		{"head discarded, was below", 3, -1, 1, 9, 20, 3, 19},
		{"head discarded, was above", 3, -1, 9, 1, 20, 3, 0},
		{"anchor discarded, was below", -1, 5, 9, 1, 20, 19, 5},
		{"anchor discarded, was above", -1, 5, 1, 9, 20, 0, 5},
		{"neither survives", -1, -1, 1, 9, 20, 19, 19},
		{"empty content", -1, -1, 0, 0, 0, 0, 0},
	}
	for _, tt := range tests {
		a, h := resolveSelRows(tt.newA, tt.newH, tt.oldA, tt.oldH, tt.total)
		if a != tt.wantA || h != tt.wantH {
			t.Errorf("%s: got (%d,%d), want (%d,%d)", tt.name, a, h, tt.wantA, tt.wantH)
		}
	}
}
