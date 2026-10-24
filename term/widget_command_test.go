package term

import (
	"math"
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// cmdTerm builds a Term with the given rows plus a mark stream, for testing
// the two mark-driven chords without a real window.
func cmdTerm(cols int, rows []string, marks ...mark) *Term {
	tm, _ := copyTerm(cols, rows...)
	tm.grid.Marks = append(tm.grid.Marks, marks...)
	tm.grid.marksVer++
	return tm
}

func TestJumpToFailure_ScrollsToFailedPrompt(t *testing.T) {
	// 8 live rows; two commands, the first failing.
	tm := cmdTerm(20,
		[]string{"$ false", "", "$ true", "ok", "", "", "", ""},
		mark{Row: 0, Kind: markPromptStart},
		end(1, 1),
		mark{Row: 2, Kind: markPromptStart},
		end(4, 0),
	)
	// Push the live rows into scrollback so there is somewhere to scroll to.
	// The cap must be set first, or the rows are simply discarded and the
	// assertion below passes vacuously against ViewOffset 0.
	tm.grid.ScrollbackCap = 200
	for range 6 {
		tm.grid.scrollUpRegion(1)
	}

	w := &gui.Window{}
	tm.jumpToFailure(true, w)

	sb := tm.grid.Scrollback.Len()
	wantOff := sb - 0 // failed command's prompt is content row 0
	if tm.grid.ViewOffset != wantOff {
		t.Errorf("ViewOffset = %d, want %d (sb=%d)", tm.grid.ViewOffset, wantOff, sb)
	}
	if tm.grid.ViewSubPx != 0 {
		t.Errorf("ViewSubPx = %v, want 0", tm.grid.ViewSubPx)
	}
}

func TestJumpToFailure_NoFailuresIsNoOp(t *testing.T) {
	tm := cmdTerm(20,
		[]string{"$ true", "ok", "", ""},
		mark{Row: 0, Kind: markPromptStart},
		end(2, 0),
	)
	tm.grid.ViewOffset = 0
	tm.jumpToFailure(true, &gui.Window{})
	if tm.grid.ViewOffset != 0 {
		t.Errorf("ViewOffset moved to %d with no failures", tm.grid.ViewOffset)
	}
}

func TestJumpToFailure_SuppressedOnAltScreen(t *testing.T) {
	tm := cmdTerm(20,
		[]string{"$ false", "", "", ""},
		mark{Row: 0, Kind: markPromptStart},
		end(1, 1),
	)
	tm.grid.AltActive = true
	tm.grid.ViewOffset = 0
	tm.jumpToFailure(true, &gui.Window{})
	if tm.grid.ViewOffset != 0 {
		t.Errorf("alt screen: ViewOffset moved to %d", tm.grid.ViewOffset)
	}
}

func TestSelectCommandOutput_SelectsOutputRegion(t *testing.T) {
	// Cursor sits on the fresh prompt at row 4, so the fallback must pick
	// the finished command above it.
	tm := cmdTerm(20,
		[]string{"$ echo hi", "line one", "line two", "", "$ "},
		mark{Row: 0, Kind: markPromptStart},
		mark{Row: 0, Kind: markCommandStart},
		mark{Row: 1, Kind: markOutputStart},
		end(3, 0),
		mark{Row: 4, Kind: markPromptStart},
	)
	tm.grid.CursorR = 4

	w := &gui.Window{}
	tm.selectCommandOutput(w)

	if !tm.copy.active {
		t.Fatal("should have entered copy mode")
	}
	if tm.copy.sel != copySelLine {
		t.Errorf("sel = %v, want copySelLine", tm.copy.sel)
	}
	if tm.copy.anchor.Row != 1 || tm.copy.cursor.Row != 2 {
		t.Errorf("selection rows = %d..%d, want 1..2",
			tm.copy.anchor.Row, tm.copy.cursor.Row)
	}
	if !tm.grid.SelActive {
		t.Error("grid selection not synced")
	}
	got := tm.grid.SelectedText()
	if want := "line one\nline two"; got != want {
		t.Errorf("SelectedText() = %q, want %q", got, want)
	}
}

func TestSelectCommandOutput_NoCommandLeavesNoState(t *testing.T) {
	tm := cmdTerm(20, []string{"plain", "text", "", ""})
	tm.selectCommandOutput(&gui.Window{})
	if tm.copy.active {
		t.Error("copy mode left active with no command region")
	}
	if tm.grid.SelActive {
		t.Error("selection left active with no command region")
	}
}

func TestSelectCommandOutput_SuppressedOnAltScreen(t *testing.T) {
	tm := cmdTerm(20,
		[]string{"$ x", "out", "", ""},
		mark{Row: 0, Kind: markPromptStart},
		mark{Row: 1, Kind: markOutputStart},
		end(3, 0),
	)
	tm.grid.AltActive = true
	tm.selectCommandOutput(&gui.Window{})
	if tm.copy.active {
		t.Error("entered copy mode on the alt screen")
	}
}

// The selection must be yankable straight away — that is the whole point of
// leaving it live in copy mode rather than copying immediately.
func TestSelectCommandOutput_ThenYankCopies(t *testing.T) {
	tm, w, clip := clipTerm(20, "$ echo hi", "line one", "line two", "", "$ ")
	tm.grid.Marks = append(tm.grid.Marks,
		mark{Row: 0, Kind: markPromptStart},
		mark{Row: 1, Kind: markOutputStart},
		end(3, 0),
		mark{Row: 4, Kind: markPromptStart},
	)
	tm.grid.marksVer++
	tm.grid.CursorR = 4

	tm.selectCommandOutput(w)
	tm.yankCopySelection(w)

	if want := "line one\nline two"; *clip != want {
		t.Errorf("clipboard = %q, want %q", *clip, want)
	}
	if tm.copy.active {
		t.Error("yank should exit copy mode")
	}
}

func TestScrollbarRowY(t *testing.T) {
	const (
		sbLen = 90
		rows  = 10
		viewH = 100
	)
	// Row 0 sits at the top of the track; the last content row sits just
	// under the bottom, on the same total-rows scale as scrollbarGeometry.
	if got := scrollbarRowY(0, sbLen, rows, viewH); got != 0 {
		t.Errorf("row 0 -> %v, want 0", got)
	}
	if got := scrollbarRowY(sbLen+rows, sbLen, rows, viewH); got != viewH {
		t.Errorf("last row -> %v, want %v", got, viewH)
	}
	if got := scrollbarRowY(50, sbLen, rows, viewH); got != 50 {
		t.Errorf("row 50 -> %v, want 50", got)
	}
	// Monotonic.
	prev := float32(-1)
	for row := 0; row <= sbLen+rows; row++ {
		y := scrollbarRowY(row, sbLen, rows, viewH)
		if y < prev {
			t.Fatalf("not monotonic at row %d: %v < %v", row, y, prev)
		}
		prev = y
	}
	// Degenerate inputs must not produce NaN or a negative offset.
	for _, c := range []struct {
		row, sbLen, rows int
		viewH            float32
	}{
		{0, 0, 0, 100}, {5, 10, 10, 0}, {5, 10, 10, float32(math.NaN())},
		{-5, 10, 10, 100}, {1000, 10, 10, 100},
	} {
		y := scrollbarRowY(c.row, c.sbLen, c.rows, c.viewH)
		if math.IsNaN(float64(y)) || y < 0 || y > max(c.viewH, 0) {
			t.Errorf("scrollbarRowY(%+v) = %v, out of range", c, y)
		}
	}
}

// The chord pair has to compose: Cmd+Shift+E scrolls to a failure, then
// Cmd+Shift+O selects *that* command's output. Resolving the reference row
// after entering copy mode broke this — entering parks the copy cursor on the
// terminal cursor at the live bottom, so the selection silently retargeted the
// newest command instead of the one on screen.
func TestSelectCommandOutput_UsesScrolledViewportNotLiveCursor(t *testing.T) {
	tm := cmdTerm(20,
		[]string{"$ false", "bad output", "$ true", "good output", "$ ", "", "", ""},
		mark{Row: 0, Kind: markPromptStart},
		mark{Row: 1, Kind: markOutputStart},
		end(2, 1),
		mark{Row: 2, Kind: markPromptStart},
		mark{Row: 3, Kind: markOutputStart},
		end(4, 0),
		mark{Row: 4, Kind: markPromptStart},
	)
	// Push everything into scrollback so the viewport has room to scroll.
	tm.grid.ScrollbackCap = 200
	for range 8 {
		tm.grid.scrollUpRegion(1)
	}
	tm.grid.CursorR = tm.grid.Rows - 1

	w := &gui.Window{}
	tm.jumpToFailure(true, w)
	tm.selectCommandOutput(w)

	if !tm.copy.active {
		t.Fatal("should have entered copy mode")
	}
	if got, want := tm.grid.SelectedText(), "bad output"; got != want {
		t.Errorf("SelectedText() = %q, want %q", got, want)
	}
}

// Output longer than the screen must be shown from its first line. The cursor
// sits on the last row so motions extend downward, but scrolling to reveal it
// would leave the user looking at the tail of a selection whose start is off
// the top of the viewport.
func TestSelectCommandOutput_ShowsFirstOutputLine(t *testing.T) {
	tm, _ := copyTerm(20, "", "", "", "", "", "", "", "")
	g := tm.grid
	g.ScrollbackCap = 200

	g.AddMark(markPromptStart)
	writeLine(g, "$ run")
	g.AddMark(markOutputStart)
	for i := range 12 { // 12 output rows into an 8-row viewport
		writeLine(g, "out "+string(rune('a'+i)))
	}
	g.AddCommandEnd(0)
	g.AddMark(markPromptStart)
	writeLine(g, "$ ")

	tm.selectCommandOutput(&gui.Window{})

	if got := contentRowText(g, tm.copy.anchor.Row); got != "out a" {
		t.Fatalf("selection starts on %q, want %q", got, "out a")
	}
	if sr := g.ContentRowToScreen(tm.copy.anchor.Row); sr != copyBarRows {
		t.Errorf("first output row at screen row %d, want %d (below the copy bar)",
			sr, copyBarRows)
	}
}

// Copy mode's cursor and anchor are content rows owned by the widget, so they
// must ride the same reflow re-map the grid applies to its marks. Without it a
// resize leaves the selection on whatever text drifted into those row numbers,
// and Resize's ViewOffset reset strands the frozen viewport at the bottom.
func TestSelectCommandOutput_SelectionSurvivesResize(t *testing.T) {
	tm, _ := copyTerm(20, "", "", "", "", "", "", "", "")
	g := tm.grid
	g.ScrollbackCap = 200

	// Written through the real autowrap path so the long line is one logical
	// line: re-wrapping it at a narrower width is what moves every row below
	// it, which is precisely what a flat row shift gets wrong.
	g.AddMark(markPromptStart)
	g.AddMark(markCommandStart)
	writeLine(g, "$ run")
	g.AddMark(markOutputStart)
	writeLine(g, strings.Repeat("a", 45)) // 3 rows at 20 cols, 4 at 12
	writeLine(g, "tail")
	g.AddCommandEnd(0)
	g.AddMark(markPromptStart)
	writeLine(g, "$ ")

	w := &gui.Window{}
	tm.selectCommandOutput(w)
	if got := contentRowText(g, tm.copy.cursor.Row); got != "tail" {
		t.Fatalf("before resize: selection ends on %q, want %q", got, "tail")
	}
	oldEnd := tm.copy.cursor.Row

	// Drive the real resize path so the tracking hook is exercised.
	var track [2]int
	track[0], track[1] = tm.copy.cursor.Row, tm.copy.anchor.Row
	tm.grid.resizeTrack = track[:]
	tm.grid.Resize(8, 12)
	tm.grid.resizeTrack = nil
	tm.applyCopyResize(track[0], track[1])

	if got := contentRowText(g, tm.copy.cursor.Row); got != "tail" {
		t.Errorf("after resize: selection ends on %q, want %q", got, "tail")
	}
	if tm.copy.cursor.Row == oldEnd {
		t.Fatal("re-wrap did not move the row; the test proves nothing")
	}
	if got := contentRowText(g, tm.copy.anchor.Row); !strings.HasPrefix(got, "a") {
		t.Errorf("after resize: selection starts on %q, want the wrapped output", got)
	}
	// The frozen viewport must still show the selection.
	if sr := tm.grid.ContentRowToScreen(tm.copy.cursor.Row); sr < 0 || sr >= tm.grid.Rows {
		t.Errorf("cursor off screen after resize: screen row %d of %d", sr, tm.grid.Rows)
	}
}

// failBoxTerm is two failed commands and one success on a 10-row screen, with
// no scrollback, so content rows and screen rows coincide.
func failBoxTerm() *Term {
	return cmdTerm(20,
		[]string{"$ false", "boom", "$ true", "ok", "$ false", "err", "$", "", "", ""},
		// D lands on the row after the output, which is where the next
		// prompt starts.
		mark{Row: 0, Kind: markPromptStart},
		end(2, 1),
		mark{Row: 2, Kind: markPromptStart},
		end(4, 0),
		mark{Row: 4, Kind: markPromptStart},
		end(6, 2),
		mark{Row: 6, Kind: markPromptStart},
	)
}

func TestJumpToFailure_SetsFailBox(t *testing.T) {
	tm := failBoxTerm()
	tm.jumpToFailure(true, &gui.Window{})
	if !tm.failBox.active {
		t.Fatal("failBox not set after a jump")
	}
	// The newest failure: prompt row 4 through its output, which ends just
	// above the next prompt.
	if got := tm.failBox.span; got.Prompt != 4 || got.OutEnd != 5 {
		t.Errorf("failBox span = prompt %d..%d, want 4..5", got.Prompt, got.OutEnd)
	}
	sr0, sr1, top, bottom, ok := tm.failBoxRows(0, tm.grid.Rows)
	if !ok || sr0 != 4 || sr1 != 6 || !top || !bottom {
		t.Errorf("failBoxRows = %d,%d top=%v bottom=%v ok=%v, want 4,6 true true true",
			sr0, sr1, top, bottom, ok)
	}
}

func TestJumpToFailure_SecondJumpMovesBox(t *testing.T) {
	tm := failBoxTerm()
	w := &gui.Window{}
	// Copy mode, so the second press measures from the first target rather
	// than from a viewport that cannot scroll (no scrollback here).
	tm.enterCopyMode(w)
	tm.jumpToFailure(true, w)
	tm.jumpToFailure(true, w)
	if got := tm.failBox.span; got.Prompt != 0 || got.OutEnd != 1 {
		t.Errorf("failBox span = prompt %d..%d, want 0..1", got.Prompt, got.OutEnd)
	}
}

func TestJumpToFailure_NoFailureNoBox(t *testing.T) {
	tm := cmdTerm(20,
		[]string{"$ true", "ok", "", ""},
		mark{Row: 0, Kind: markPromptStart},
		end(2, 0),
	)
	tm.jumpToFailure(true, &gui.Window{})
	if tm.failBox.active {
		t.Error("failBox set with no failed command")
	}
	if _, _, _, _, ok := tm.failBoxRows(0, tm.grid.Rows); ok {
		t.Error("failBoxRows ok with no failed command")
	}
}

func TestFailBox_ClearedByMarkChange(t *testing.T) {
	tm := failBoxTerm()
	tm.jumpToFailure(true, &gui.Window{})
	// Any mark change (a new command, scrollback eviction, reflow) may have
	// moved the rows the box was recorded against.
	tm.grid.marksVer++
	if _, _, _, _, ok := tm.failBoxRows(0, tm.grid.Rows); ok {
		t.Error("failBoxRows still ok after the mark stream changed")
	}
}

func TestFailBox_ClearedByInput(t *testing.T) {
	tm := failBoxTerm()
	tm.jumpToFailure(true, &gui.Window{})
	tm.writeBytes([]byte("x"))
	if tm.failBox.active {
		t.Error("failBox survived a keystroke sent to the child")
	}
}

func TestFailBox_ClearedByPaste(t *testing.T) {
	tm := failBoxTerm()
	tm.jumpToFailure(true, &gui.Window{})
	tm.pasteText("ls")
	if tm.failBox.active {
		t.Error("failBox survived a paste sent to the child")
	}
}

func TestFailBoxRows_Clipping(t *testing.T) {
	tests := []struct {
		name                    string
		top, rows               int
		sr0, sr1                int
		drawTop, drawBottom, ok bool
	}{
		{name: "fully visible", top: 0, rows: 10, sr0: 4, sr1: 6, drawTop: true, drawBottom: true, ok: true},
		// Copy mode reserves row 0 for its bar; the box is below it anyway.
		{name: "copy bar", top: 1, rows: 10, sr0: 4, sr1: 6, drawTop: true, drawBottom: true, ok: true},
		// Prompt hidden under the top bound: its top edge is not the box's.
		{name: "top clipped", top: 5, rows: 10, sr0: 5, sr1: 6, drawTop: false, drawBottom: true, ok: true},
		// Output runs past the bottom bound: no bottom edge.
		{name: "bottom clipped", top: 0, rows: 5, sr0: 4, sr1: 5, drawTop: true, drawBottom: false, ok: true},
		{name: "below view", top: 0, rows: 4, ok: false},
		{name: "above view", top: 6, rows: 10, ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tm := failBoxTerm()
			tm.jumpToFailure(true, &gui.Window{})
			sr0, sr1, top, bottom, ok := tm.failBoxRows(tc.top, tc.rows)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if sr0 != tc.sr0 || sr1 != tc.sr1 || top != tc.drawTop || bottom != tc.drawBottom {
				t.Errorf("got %d,%d top=%v bottom=%v, want %d,%d top=%v bottom=%v",
					sr0, sr1, top, bottom, tc.sr0, tc.sr1, tc.drawTop, tc.drawBottom)
			}
		})
	}
}

func TestJumpToFailure_RepeatWalksBackOnLiveScreen(t *testing.T) {
	// Both failures sit on the live screen with no scrollback, so the jump
	// cannot scroll and the viewport top never moves. The second press must
	// still walk back to the older failure instead of re-finding the newest.
	tm := failBoxTerm()
	w := &gui.Window{}
	tm.jumpToFailure(true, w)
	tm.jumpToFailure(true, w)
	if got := tm.failBox.span.Prompt; got != 0 {
		t.Fatalf("second press: box on prompt %d, want 0", got)
	}
	// Third press wraps back to the newest.
	tm.jumpToFailure(true, w)
	if got := tm.failBox.span.Prompt; got != 4 {
		t.Errorf("third press: box on prompt %d, want 4 (wrap)", got)
	}
}

func TestJumpToFailureNext_WalksForward(t *testing.T) {
	tm := failBoxTerm()
	w := &gui.Window{}
	// Back to the newest (4), back again to the oldest (0), then forward
	// returns to the newest.
	tm.jumpToFailure(true, w)
	tm.jumpToFailure(true, w)
	tm.jumpToFailure(false, w)
	if got := tm.failBox.span.Prompt; got != 4 {
		t.Fatalf("next from 0: box on prompt %d, want 4", got)
	}
	// Past the newest it wraps to the oldest.
	tm.jumpToFailure(false, w)
	if got := tm.failBox.span.Prompt; got != 0 {
		t.Errorf("next from 4: box on prompt %d, want 0 (wrap)", got)
	}
}

func TestJumpToFailureNext_CopyModeMovesCursor(t *testing.T) {
	tm := failBoxTerm()
	w := &gui.Window{}
	tm.enterCopyMode(w)
	tm.copy.cursor = contentPos{Row: 1}
	tm.jumpToFailure(false, w)
	if got := tm.copy.cursor.Row; got != 4 {
		t.Errorf("copy cursor on row %d, want 4", got)
	}
}

func TestJumpFailureNext_DefaultChord(t *testing.T) {
	tm := failBoxTerm()
	w := &gui.Window{}
	tm.jumpToFailure(true, w) // newest, 4
	tm.jumpToFailure(true, w) // oldest, 0
	e := &gui.Event{KeyCode: gui.KeyE, Modifiers: remapMod(gui.ModSuper | gui.ModCtrl)}
	if !tm.binds(ActionJumpFailureNext, e) {
		t.Fatal("default chord does not bind ActionJumpFailureNext")
	}
	if !tm.RunAction(ActionJumpFailureNext, nil) {
		t.Fatal("RunAction(ActionJumpFailureNext) did not run")
	}
	if got := tm.failBox.span.Prompt; got != 4 {
		t.Errorf("box on prompt %d, want 4", got)
	}
}

// failBoxClip is failBoxTerm plus a window whose clipboard writes are
// captured, for the copy-failure path.
func failBoxClip() (*Term, *gui.Window, *string) {
	tm := failBoxTerm()
	var clip string
	w := &gui.Window{}
	w.SetClipboardFn(func(s string) { clip = s })
	return tm, w, &clip
}

func TestCopyFailure_EnterCopiesBoxAndReturnsToPrompt(t *testing.T) {
	tm, w, clip := failBoxClip()
	tm.jumpToFailure(true, w) // box on the newest failure, prompt 4..5
	tm.grid.ViewOffset = 0
	key(tm, w, gui.KeyEnter, 0)
	if *clip != "$ false\nerr" {
		t.Errorf("clipboard = %q, want %q", *clip, "$ false\nerr")
	}
	if tm.failBox.active {
		t.Error("box still showing after the copy")
	}
	if tm.grid.ViewOffset != 0 || tm.grid.SelActive {
		t.Errorf("not back at the prompt: ViewOffset=%d SelActive=%v",
			tm.grid.ViewOffset, tm.grid.SelActive)
	}
}

func TestCopyFailure_EnterNotSentToChild(t *testing.T) {
	tm, w, _ := failBoxClip()
	tm.jumpToFailure(true, w)
	if !tm.RunAction(ActionCopyFailure, w) {
		t.Fatal("RunAction(ActionCopyFailure) did not run with a box showing")
	}
	// A second Enter, with the box gone, belongs to the shell again.
	if tm.RunAction(ActionCopyFailure, w) {
		t.Error("ActionCopyFailure ran with no box showing")
	}
}

func TestCopyFailure_EnterPassesThroughWithoutBox(t *testing.T) {
	tm, buf := copyTerm(20, "$ ")
	var clip string
	w := &gui.Window{}
	w.SetClipboardFn(func(s string) { clip = s })
	key(tm, w, gui.KeyEnter, 0)
	if clip != "" {
		t.Errorf("clipboard written with no box: %q", clip)
	}
	if string(*buf) != "\r" {
		t.Errorf("child got %q, want %q", *buf, "\r")
	}
}

func TestCopyFailure_StaleBoxPassesThrough(t *testing.T) {
	tm, w, clip := failBoxClip()
	tm.jumpToFailure(true, w)
	tm.grid.marksVer++ // a new command started: the box is gone
	if tm.RunAction(ActionCopyFailure, w) {
		t.Error("ActionCopyFailure ran on a stale box")
	}
	if *clip != "" {
		t.Errorf("clipboard written from a stale box: %q", *clip)
	}
}

func TestCopyFailure_KeepsUserSelection(t *testing.T) {
	// The text is read through a temporary selection; a selection the user
	// had must not be replaced by it on the nil-window path.
	tm := failBoxTerm()
	tm.jumpToFailure(true, &gui.Window{})
	g := tm.grid
	g.SelActive, g.SelAnchor, g.SelHead = true, contentPos{Row: 2}, contentPos{Row: 2, Col: 3}
	if got := func() string {
		g.Mu.Lock()
		defer g.Mu.Unlock()
		return g.rowsText(4, 5)
	}(); got != "$ false\nerr" {
		t.Errorf("rowsText = %q", got)
	}
	if !g.SelActive || g.SelAnchor != (contentPos{Row: 2}) || g.SelHead != (contentPos{Row: 2, Col: 3}) {
		t.Errorf("selection clobbered: active=%v %v..%v", g.SelActive, g.SelAnchor, g.SelHead)
	}
}

func TestCopyFailure_EnterKeyNotWrittenToChild(t *testing.T) {
	// failBoxTerm drops the child buffer, so rebuild the same screen with it.
	tm, buf := copyTerm(20, "$ false", "boom", "$ true", "ok", "$ false", "err", "$", "", "", "")
	tm.grid.Marks = append(tm.grid.Marks, failBoxTerm().grid.Marks...)
	tm.grid.marksVer++
	w := &gui.Window{}
	w.SetClipboardFn(func(string) {})
	tm.jumpToFailure(true, w)
	key(tm, w, gui.KeyEnter, 0)
	if len(*buf) != 0 {
		t.Errorf("child got %q, want nothing while the box shows", *buf)
	}
}

func TestCopyFailure_AltScreenPassesThrough(t *testing.T) {
	tm, w, clip := failBoxClip()
	tm.jumpToFailure(true, w)
	// A full-screen app took over: Enter is its key, and the box is not drawn.
	tm.grid.AltActive = true
	if tm.RunAction(ActionCopyFailure, w) {
		t.Error("ActionCopyFailure ran on the alt screen")
	}
	if *clip != "" {
		t.Errorf("clipboard written on the alt screen: %q", *clip)
	}
}

func TestCopyFailure_SearchBarEnterKeepsNextMatch(t *testing.T) {
	tm, w, clip := failBoxClip()
	tm.jumpToFailure(true, w)
	tm.search.active = true
	key(tm, w, gui.KeyEnter, 0)
	if *clip != "" {
		t.Errorf("Enter in the search bar copied the box: %q", *clip)
	}
	if !tm.failBox.active {
		t.Error("Enter in the search bar cleared the box")
	}
}
