package term

import (
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// pushLiveTop moves the top n live rows into scrollback, mirroring how the
// mark-driven tests build history. The cap must be set first, or the rows
// are discarded and the assertions below pass vacuously.
func pushLiveTop(tm *Term, n int) {
	tm.grid.ScrollbackCap = 200
	for range n {
		tm.grid.scrollUpRegion(1)
	}
}

func TestSelectAll_SelectsScrollbackAndLive(t *testing.T) {
	tm, _ := copyTerm(8, "aaa", "bbb", "ccc", "ddd")
	pushLiveTop(tm, 2)
	sb := tm.grid.Scrollback.Len()
	if sb != 2 {
		t.Fatalf("scrollback = %d, want 2", sb)
	}

	tm.selectAll(&gui.Window{})

	total := sb + tm.grid.Rows
	g := tm.grid
	if !g.SelActive {
		t.Fatal("SelActive = false after selectAll")
	}
	if g.SelAnchor != (contentPos{Row: 0}) {
		t.Errorf("SelAnchor = %v, want {0 0}", g.SelAnchor)
	}
	if want := (contentPos{Row: total - 1, Col: g.Cols}); g.SelHead != want {
		t.Errorf("SelHead = %v, want %v", g.SelHead, want)
	}
	if g.SelMode != selChar {
		t.Errorf("SelMode = %v, want selChar", g.SelMode)
	}
	// The viewport stays put: selecting is not scrolling.
	if g.ViewOffset != 0 {
		t.Errorf("ViewOffset = %d, want 0", g.ViewOffset)
	}
	got := g.SelectedText()
	lines := strings.Split(got, "\n")
	if len(lines) != total {
		t.Fatalf("SelectedText has %d lines, want %d (%q)", len(lines), total, got)
	}
	if lines[0] != "aaa" {
		t.Errorf("first line = %q, want %q (scrollback head)", lines[0], "aaa")
	}
}

func TestSelectAll_AltScreenSelectsViewportOnly(t *testing.T) {
	tm, _ := copyTerm(8, "aaa", "bbb", "ccc", "ddd")
	pushLiveTop(tm, 2)
	sb := tm.grid.Scrollback.Len()
	tm.grid.AltActive = true

	tm.selectAll(&gui.Window{})

	g := tm.grid
	if want := (contentPos{Row: sb}); g.SelAnchor != want {
		t.Errorf("alt SelAnchor = %v, want %v (past main scrollback)", g.SelAnchor, want)
	}
	if want := (contentPos{Row: sb + g.Rows - 1, Col: g.Cols}); g.SelHead != want {
		t.Errorf("alt SelHead = %v, want %v", g.SelHead, want)
	}
	if n := len(strings.Split(g.SelectedText(), "\n")); n != g.Rows {
		t.Errorf("alt SelectedText has %d lines, want %d", n, g.Rows)
	}
}

func TestSelectAll_CopyModeExpandsSelection(t *testing.T) {
	tm, _ := copyTerm(8, "aaa", "bbb", "ccc", "ddd")
	pushLiveTop(tm, 2)
	tm.enterCopyMode(nil)
	frozen := tm.grid.ViewOffset
	total := tm.grid.ContentRows()

	tm.selectAll(&gui.Window{})

	if tm.copy.sel != copySelChar {
		t.Errorf("copy sel = %v, want copySelChar", tm.copy.sel)
	}
	if tm.copy.anchor != (contentPos{Row: 0}) {
		t.Errorf("copy anchor = %v, want {0 0}", tm.copy.anchor)
	}
	if want := (contentPos{Row: total - 1, Col: tm.grid.Cols - 1}); tm.copy.cursor != want {
		t.Errorf("copy cursor = %v, want %v", tm.copy.cursor, want)
	}
	if !tm.grid.SelActive {
		t.Error("grid selection not synced from copy selection")
	}
	// No reveal: the viewport must not move under a full-buffer select.
	if tm.grid.ViewOffset != frozen {
		t.Errorf("ViewOffset = %d, want %d (unchanged)", tm.grid.ViewOffset, frozen)
	}
	if got := tm.grid.SelectedText(); !strings.HasPrefix(got, "aaa") {
		t.Errorf("SelectedText = %q, want prefix %q", got, "aaa")
	}
}

func TestSelectAll_SearchOpenStillSelects(t *testing.T) {
	tm, _ := copyTerm(8, "aaa", "bbb")
	tm.search.active = true
	w := &gui.Window{}

	e := ev(gui.KeyA, primary)
	if !tm.handleSearchKey(e, w) {
		t.Fatal("Cmd+A should be consumed while search is open")
	}
	if !e.IsHandled {
		t.Error("event not marked handled")
	}
	if !tm.grid.SelActive {
		t.Error("no selection after Cmd+A with search open")
	}

	// The palette path agrees: RunAction is not swallowed by the bar.
	tm2, _ := copyTerm(8, "aaa", "bbb")
	tm2.search.active = true
	if !tm2.RunAction(ActionSelectAll, w) {
		t.Error("RunAction(select-all) refused while search is open")
	}
	if !tm2.grid.SelActive {
		t.Error("no selection after RunAction with search open")
	}
}

func TestSelectAll_DefaultChord(t *testing.T) {
	tbl := defaultBindings()
	if !tbl[ActionSelectAll].matches(gui.KeyA, primary) {
		t.Error("select-all does not match the primary chord")
	}
	// Shift is tolerated, as for Copy and Find — it names no direction.
	if !tbl[ActionSelectAll].matches(gui.KeyA, primary|gui.ModShift) {
		t.Error("select-all should tolerate a stray Shift")
	}
	// Ctrl+A is readline's beginning-of-line; it must reach the child.
	if tbl[ActionSelectAll].matches(gui.KeyA, gui.ModCtrl) {
		t.Error("select-all must not steal Ctrl+A")
	}
	if tbl[ActionSelectAll].matches(gui.KeyA, 0) {
		t.Error("bare A must not select all")
	}
	if _, ok := ParseAction("term.select-all"); !ok {
		t.Error(`ParseAction("term.select-all") = false`)
	}
	found := false
	for _, s := range Shortcuts() {
		if s.Action == ActionSelectAll {
			found = true
			if s.Label == "" || s.Keys == "" {
				t.Errorf("select-all cheatsheet entry incomplete: %+v", s)
			}
		}
	}
	if !found {
		t.Error("select-all missing from Shortcuts()")
	}
}

func TestSelectAll_KeyboardEndToEnd(t *testing.T) {
	tm, buf := copyTerm(8, "aaa", "bbb")
	w := &gui.Window{}

	key(tm, w, gui.KeyA, primary)
	if len(*buf) != 0 {
		t.Errorf("Cmd+A wrote %q to the pty", string(*buf))
	}
	if !tm.grid.SelActive {
		t.Error("Cmd+A did not select")
	}

	// Ctrl+A still encodes beginning-of-line for the child.
	tm2, buf2 := copyTerm(8, "aaa", "bbb")
	key(tm2, w, gui.KeyA, gui.ModCtrl)
	if string(*buf2) != "\x01" {
		t.Errorf("Ctrl+A bytes = %q, want %q", string(*buf2), "\x01")
	}
	if tm2.grid.SelActive {
		t.Error("Ctrl+A must not select")
	}
}

func TestSelectAll_CopyModeKeyExpands(t *testing.T) {
	tm, buf := copyTerm(8, "aaa", "bbb")
	w := &gui.Window{}
	key(tm, w, gui.KeySpace, copyModeMods) // enter copy mode
	if !tm.copy.active {
		t.Fatal("copy mode did not activate")
	}
	key(tm, w, gui.KeyA, primary)
	if len(*buf) != 0 {
		t.Errorf("Cmd+A in copy mode wrote %q to the pty", string(*buf))
	}
	if tm.copy.sel != copySelChar || !tm.grid.SelActive {
		t.Error("Cmd+A in copy mode did not expand the copy selection")
	}
}

func TestSelectAll_UnbindHidesAndFrees(t *testing.T) {
	tbl := mergeBindings(KeyMap{ActionSelectAll: {}})
	if tbl[ActionSelectAll].matches(gui.KeyA, primary) {
		t.Error("unbound select-all still matches Cmd+A")
	}
	tm := &Term{grid: newGrid(4, 8)}
	tm.SetKeyBindings(KeyMap{ActionSelectAll: {}})
	for _, s := range tm.Shortcuts() {
		if s.Action == ActionSelectAll {
			t.Error("unbound select-all should not appear in Shortcuts()")
		}
	}
}

func TestSelectAll_EmptyGridNoPanic(t *testing.T) {
	tm, _ := copyTerm(8, "aaa")
	tm.grid.Cols = 0
	tm.selectAll(&gui.Window{})
	if tm.grid.SelActive {
		t.Error("empty grid should not gain a selection")
	}

	tm2 := &Term{grid: newGrid(0, 0), cmd: syncScheduler{}}
	tm2.selectAll(nil) // nil window: scheduleViewUpdate must tolerate it
	// newGrid clamps dims to >=1, so this is a 1x1 grid that selects its
	// single cell rather than hitting the empty guard.
	if !tm2.grid.SelActive {
		t.Error("1x1 clamped grid should gain a selection")
	}
}

func TestSelectAll_CopyModeAltScreenSelectsViewportOnly(t *testing.T) {
	tm, _ := copyTerm(8, "aaa", "bbb", "ccc", "ddd")
	pushLiveTop(tm, 2)
	sb := tm.grid.Scrollback.Len()
	tm.grid.AltActive = true
	tm.enterCopyMode(nil)

	tm.selectAll(&gui.Window{})

	if tm.copy.anchor != (contentPos{Row: sb}) {
		t.Errorf("copy+alt anchor = %v, want {%d 0}", tm.copy.anchor, sb)
	}
	want := contentPos{Row: sb + tm.grid.Rows - 1, Col: tm.grid.Cols - 1}
	if tm.copy.cursor != want {
		t.Errorf("copy+alt cursor = %v, want %v", tm.copy.cursor, want)
	}
}
