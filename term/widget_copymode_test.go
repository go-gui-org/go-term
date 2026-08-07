package term

import (
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// copyTerm builds a Term whose rows hold the given strings and whose pty
// writer records everything sent to the child, so a test can assert that copy
// mode swallowed a key rather than forwarding it.
func copyTerm(cols int, rows ...string) (*Term, *[]byte) {
	tm, buf := newKeyboardTerm(len(rows), cols)
	// onChar's search-bar path queues a redraw; without a scheduler that is a
	// nil dereference.
	tm.cmd = syncScheduler{}
	for r, s := range rows {
		for c, ch := range s {
			if c >= cols {
				break
			}
			tm.grid.At(r, c).Ch = ch
		}
	}
	return tm, buf
}

// clipTerm is copyTerm plus a window that captures SetClipboard, for the yank
// path. The returned pointer holds the last clipboard write.
func clipTerm(cols int, rows ...string) (*Term, *gui.Window, *string) {
	tm, _ := copyTerm(cols, rows...)
	var clip string
	w := &gui.Window{}
	w.SetClipboardFn(func(s string) { clip = s })
	return tm, w, &clip
}

// key feeds one non-printable key (arrows, Escape, Ctrl+letter, the entry
// chord) through onKeyDown, the event a real backend delivers for those.
func key(tm *Term, w *gui.Window, k gui.KeyCode, mods gui.Modifier) {
	tm.onKeyDown(gui.EventCtx{Layout: nil, Event: &gui.Event{KeyCode: k, Modifiers: mods}, Window: w})
}

// press types a printable character, the *only* event macOS delivers for an
// unmodified printable key: keyDown: routes through interpretKeyEvents:, so a
// bare 'v' produces OnChar and never OnKeyDown. Copy mode's vim keys all come
// this way, so tests must too — driving them through onKeyDown tests a path
// that does not exist on the platform.
func press(tm *Term, w *gui.Window, r rune) {
	tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: uint32(r)}, Window: w})
}

// enterKey is the mode-entry chord as a real event would carry it.
var copyModeMods = remapMod(gui.ModSuper | gui.ModShift)

// --- entry / exit -----------------------------------------------------------

func TestCopyMode_EnterExit(t *testing.T) {
	tm, _ := copyTerm(8, "hello", "world")
	key(tm, nil, gui.KeySpace, copyModeMods)
	if !tm.copy.active {
		t.Fatal("entry chord did not activate copy mode")
	}
	if !tm.grid.ViewFrozen {
		t.Error("copy mode should freeze the viewport")
	}
	// The cursor starts at the terminal cursor, in content coordinates.
	if got := tm.copy.cursor; got != (contentPos{0, 0}) {
		t.Errorf("initial cursor = %v, want {0 0}", got)
	}
	key(tm, nil, gui.KeyEscape, 0)
	if tm.copy.active {
		t.Error("Escape did not exit copy mode")
	}
	if tm.grid.ViewFrozen {
		t.Error("exit should unfreeze the viewport")
	}
}

// The entry chord toggles: pressing it again leaves the mode.
func TestCopyMode_EntryChordToggles(t *testing.T) {
	tm, _ := copyTerm(8, "hello")
	key(tm, nil, gui.KeySpace, copyModeMods)
	key(tm, nil, gui.KeySpace, copyModeMods)
	if tm.copy.active {
		t.Error("second entry chord should exit copy mode")
	}
}

// --- key swallowing ---------------------------------------------------------

// Nothing typed in copy mode may reach the child process.
func TestCopyMode_SwallowsKeys(t *testing.T) {
	tm, buf := copyTerm(8, "hello", "world")
	key(tm, nil, gui.KeySpace, copyModeMods)
	// Printable keys as characters, special keys as key events — the two event
	// kinds a real backend produces. No exit action ('q', 'y', Escape) in the
	// list, so the mode is still active throughout; unbound letters like 'a'
	// and 'z' must be swallowed just as firmly as bound ones.
	for _, r := range "hjklazbwgn0$[]" {
		press(tm, nil, r)
	}
	for _, k := range []gui.KeyCode{gui.KeyTab, gui.KeyBackspace, gui.KeyF1, gui.KeyDelete} {
		key(tm, nil, k, 0)
	}
	if !tm.copy.active {
		t.Fatal("test bug: an exit action slipped into the key list")
	}
	if len(*buf) != 0 {
		t.Errorf("copy mode leaked %q to the pty", *buf)
	}
}

// onChar must not forward printable input either — motion keys arrive there
// as well as in onKeyDown.
func TestCopyMode_OnCharSwallowed(t *testing.T) {
	tm, buf := copyTerm(8, "hello")
	key(tm, nil, gui.KeySpace, copyModeMods)
	e := &gui.Event{CharCode: 'j'}
	tm.onChar(gui.EventCtx{Layout: nil, Event: e, Window: nil})
	if !e.IsHandled {
		t.Error("onChar should mark the event handled in copy mode")
	}
	if len(*buf) != 0 {
		t.Errorf("onChar leaked %q to the pty", *buf)
	}
}

// After exiting, keys reach the child again.
func TestCopyMode_ExitRestoresPtyInput(t *testing.T) {
	tm, buf := copyTerm(8, "hello")
	key(tm, nil, gui.KeySpace, copyModeMods)
	key(tm, nil, gui.KeyEscape, 0)
	tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: 'x'}, Window: nil})
	if string(*buf) != "x" {
		t.Errorf("after exit, pty got %q, want %q", *buf, "x")
	}
}

// --- motion -----------------------------------------------------------------

func TestCopyMode_Motion(t *testing.T) {
	tm, _ := copyTerm(10, "alpha beta", "gamma", "delta")
	key(tm, nil, gui.KeySpace, copyModeMods)

	// Characters, not key codes: these are the events macOS actually sends.
	cases := []struct {
		name string
		ch   rune
		want contentPos
	}{
		{"l moves right", 'l', contentPos{0, 1}},
		{"j moves down", 'j', contentPos{1, 1}},
		{"h moves left", 'h', contentPos{1, 0}},
		{"k moves up", 'k', contentPos{0, 0}},
		{"w next word", 'w', contentPos{0, 6}},
		{"b previous word", 'b', contentPos{0, 0}},
		{"$ line end", '$', contentPos{0, 9}},
		{"0 line start", '0', contentPos{0, 0}},
		{"G buffer bottom", 'G', contentPos{2, 0}},
		{"g buffer top", 'g', contentPos{0, 0}},
	}
	for _, tc := range cases {
		press(tm, nil, tc.ch)
		if got := tm.copy.cursor; got != tc.want {
			t.Errorf("%s: cursor = %v, want %v", tc.name, got, tc.want)
		}
	}

	// Arrows are non-printable, so they do arrive as key events.
	key(tm, nil, gui.KeyDown, 0)
	if got := tm.copy.cursor; got != (contentPos{1, 0}) {
		t.Errorf("arrow down: cursor = %v, want {1 0}", got)
	}
}

// Regression for the macOS dispatch split: a bare printable key arrives *only*
// as OnChar. Driving it through onKeyDown must not move anything, or a backend
// that sends both events would apply every motion twice.
func TestCopyMode_PrintableKeysComeFromOnCharOnly(t *testing.T) {
	tm, _ := copyTerm(10, "alpha beta")
	key(tm, nil, gui.KeySpace, copyModeMods)

	// onKeyDown with a bare 'l': ignored, but still consumed.
	e := &gui.Event{KeyCode: gui.KeyL}
	tm.onKeyDown(gui.EventCtx{Layout: nil, Event: e, Window: nil})
	if !e.IsHandled {
		t.Error("copy mode must consume the key even when it defers to onChar")
	}
	if got := tm.copy.cursor; got != (contentPos{0, 0}) {
		t.Errorf("onKeyDown moved the cursor to %v; onChar owns printable chords", got)
	}

	// The same keystroke as a character does move it, exactly once.
	press(tm, nil, 'l')
	if got := tm.copy.cursor; got != (contentPos{0, 1}) {
		t.Errorf("onChar: cursor = %v, want {0 1}", got)
	}
}

// Ctrl-modified keys keep coming through onKeyDown: holding a modifier
// suppresses the char event.
func TestCopyMode_CtrlKeysStillDispatchFromOnKeyDown(t *testing.T) {
	tm, _ := copyTerm(6, "r0", "r1", "r2", "r3", "r4", "r5")
	key(tm, nil, gui.KeySpace, copyModeMods)
	key(tm, nil, gui.KeyD, gui.ModCtrl) // half page down
	if got := tm.copy.cursor.Row; got == 0 {
		t.Error("Ctrl+D did not move the cursor")
	}
}

// Motion clamps at every buffer edge instead of running off it.
func TestCopyMode_MotionClamps(t *testing.T) {
	tm, _ := copyTerm(4, "ab", "cd")
	key(tm, nil, gui.KeySpace, copyModeMods)
	for range 5 {
		press(tm, nil, 'h')
		press(tm, nil, 'k')
	}
	if got := tm.copy.cursor; got != (contentPos{0, 0}) {
		t.Errorf("top-left clamp: cursor = %v, want {0 0}", got)
	}
	for range 5 {
		press(tm, nil, 'l')
		press(tm, nil, 'j')
	}
	if got := tm.copy.cursor; got != (contentPos{1, 3}) {
		t.Errorf("bottom-right clamp: cursor = %v, want {1 3}", got)
	}
}

// --- selection and yank -----------------------------------------------------

func TestCopyMode_CharwiseYank(t *testing.T) {
	tm, w, clip := clipTerm(10, "hello")
	key(tm, w, gui.KeySpace, copyModeMods)
	press(tm, w, 'v') // anchor at {0,0}
	for range 4 {
		press(tm, w, 'l') // cursor to {0,4}
	}
	press(tm, w, 'y')
	if *clip != "hello" {
		t.Errorf("yanked %q, want %q", *clip, "hello")
	}
	if tm.copy.active {
		t.Error("yank should exit copy mode")
	}
	// The highlight must go with the mode. Leaving it up read as "still in copy
	// mode" and it lingered until some later keystroke happened to clear it.
	if tm.grid.SelActive {
		t.Error("yank left the selection highlighted after exiting")
	}
}

// Selecting backwards must yield the same text as selecting forwards: both
// the anchor cell and the cursor cell stay inside the span.
func TestCopyMode_CharwiseYankBackward(t *testing.T) {
	tm, w, clip := clipTerm(10, "hello")
	key(tm, w, gui.KeySpace, copyModeMods)
	for range 4 {
		press(tm, w, 'l') // cursor to {0,4}
	}
	press(tm, w, 'v') // anchor at {0,4}
	for range 4 {
		press(tm, w, 'h') // cursor back to {0,0}
	}
	press(tm, w, 'y')
	if *clip != "hello" {
		t.Errorf("backward yank = %q, want %q", *clip, "hello")
	}
}

func TestCopyMode_LinewiseYank(t *testing.T) {
	tm, w, clip := clipTerm(10, "one", "two", "three")
	key(tm, w, gui.KeySpace, copyModeMods)
	press(tm, w, 'l') // off column 0, to prove V ignores it
	press(tm, w, 'V') // line-wise from row 0
	press(tm, w, 'j') // extend to row 1
	press(tm, w, 'y')
	if *clip != "one\ntwo" {
		t.Errorf("line-wise yank = %q, want %q", *clip, "one\ntwo")
	}
}

// With no selection, y takes the single cell under the cursor rather than
// doing nothing.
func TestCopyMode_YankWithoutSelection(t *testing.T) {
	tm, w, clip := clipTerm(10, "hello")
	key(tm, w, gui.KeySpace, copyModeMods)
	press(tm, w, 'l')
	press(tm, w, 'y')
	if *clip != "e" {
		t.Errorf("bare yank = %q, want %q", *clip, "e")
	}
}

// Pressing v twice cancels the selection, as in vim.
func TestCopyMode_SelectTogglesOff(t *testing.T) {
	tm, _ := copyTerm(10, "hello")
	key(tm, nil, gui.KeySpace, copyModeMods)
	press(tm, nil, 'v')
	if tm.copy.sel != copySelChar {
		t.Fatalf("sel = %v, want copySelChar", tm.copy.sel)
	}
	press(tm, nil, 'v')
	if tm.copy.sel != copySelNone {
		t.Errorf("sel = %v, want copySelNone", tm.copy.sel)
	}
	if tm.grid.SelActive {
		t.Error("cancelling the selection should deactivate it on the grid")
	}
}

// Escaping out of copy mode drops the selection rather than leaving a stale
// highlight behind.
func TestCopyMode_EscapeClearsSelection(t *testing.T) {
	tm, _ := copyTerm(10, "hello")
	key(tm, nil, gui.KeySpace, copyModeMods)
	press(tm, nil, 'v')
	press(tm, nil, 'l')
	key(tm, nil, gui.KeyEscape, 0)
	if tm.grid.SelActive {
		t.Error("Escape should clear the selection")
	}
}

// --- viewport ---------------------------------------------------------------

// Moving above the visible rows scrolls the viewport just enough to reveal the
// cursor, rather than jumping or leaving it off-screen.
func TestCopyMode_RevealScrollsViewport(t *testing.T) {
	tm, _ := copyTerm(8, "live0", "live1")
	g := tm.grid
	g.ScrollbackCap = 10
	g.Scrollback.SetGeom(10, 8)
	row := make([]cell, 8)
	for i := range 3 {
		row[0] = cell{Ch: rune('a' + i), Width: 1}
		g.Scrollback.Push(row, false)
	}
	key(tm, nil, gui.KeySpace, copyModeMods)
	// The cursor starts on the first live row (content row 3), which is viewport
	// row 0 — the row the status bar reserves. Entry alone therefore scrolls one
	// row so the cursor sits below the bar.
	if g.ViewOffset != copyBarRows {
		t.Fatalf("on entry: ViewOffset = %d, want %d (cursor out from under the bar)",
			g.ViewOffset, copyBarRows)
	}
	// Each k then takes the cursor one row above the reserved top and scrolls by
	// exactly one — the minimum that reveals it, not a jump to the buffer top.
	press(tm, nil, 'k')
	if g.ViewOffset != 2 {
		t.Fatalf("after one k: ViewOffset = %d, want 2", g.ViewOffset)
	}
	press(tm, nil, 'k')
	if g.ViewOffset != 3 {
		t.Errorf("after two k: ViewOffset = %d, want 3", g.ViewOffset)
	}
	if _, ok := g.ContentRowToViewport(tm.copy.cursor.Row); !ok {
		t.Error("cursor row should be visible after reveal")
	}
}

// While frozen, rows scrolling off the top must not move the content the user
// is looking at.
func TestCopyMode_FreezesAgainstOutput(t *testing.T) {
	tm, _ := copyTerm(3, "r0", "r1", "r2")
	g := tm.grid
	g.ScrollbackCap = 20
	g.Scrollback.SetGeom(20, 3)
	g.Bottom = g.Rows - 1

	key(tm, nil, gui.KeySpace, copyModeMods)
	cursorRow := tm.copy.cursor.Row
	before := g.ContentRowToScreen(cursorRow)

	// Simulate output: five rows scroll off the top into scrollback.
	for range 5 {
		g.scrollUpRegion(1)
	}
	if g.ViewOffset != 5 {
		t.Errorf("ViewOffset = %d, want 5 (one per pushed row)", g.ViewOffset)
	}
	if after := g.ContentRowToScreen(cursorRow); after != before {
		t.Errorf("frozen view drifted: screen row %d -> %d", before, after)
	}
}

// Without the freeze the viewport is unchanged by output, which is the
// pre-existing behavior every other scroll path relies on.
func TestScrollUpRegion_UnfrozenViewOffsetUnchanged(t *testing.T) {
	g := newGrid(3, 3)
	g.ScrollbackCap = 20
	g.Scrollback.SetGeom(20, 3)
	g.Bottom = g.Rows - 1
	g.ViewOffset = 2
	g.scrollUpRegion(1)
	if g.ViewOffset != 2 {
		t.Errorf("ViewOffset = %d, want 2 (unchanged when not frozen)", g.ViewOffset)
	}
}

// --- mouse ------------------------------------------------------------------

// A click hands selection back to the mouse.
func TestCopyMode_ClickExits(t *testing.T) {
	tm, _ := copyTerm(8, "hello")
	w := &gui.Window{}
	key(tm, w, gui.KeySpace, copyModeMods)
	tm.onClick(gui.EventCtx{Layout: nil, Event: &gui.Event{MouseButton: gui.MouseLeft}, Window: w})
	if tm.copy.active {
		t.Error("a click should exit copy mode")
	}
}

// --- search and marks -------------------------------------------------------

// '/' opens the search bar on copy mode's behalf: typing goes to the query,
// and Enter closes the bar and moves the copy cursor onto the match.
func TestCopyMode_SearchMovesCursor(t *testing.T) {
	tm, _ := copyTerm(10, "alpha", "bravo", "charlie")
	key(tm, nil, gui.KeySpace, copyModeMods)
	press(tm, nil, '/')
	if !tm.search.active || !tm.copy.searching {
		t.Fatal("'/' should open the search bar for copy mode")
	}
	for _, r := range "bravo" {
		tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: uint32(r)}, Window: nil})
	}
	if tm.search.query != "bravo" {
		t.Fatalf("query = %q, want %q", tm.search.query, "bravo")
	}
	key(tm, nil, gui.KeyEnter, 0)
	if tm.search.active || tm.copy.searching {
		t.Error("Enter should close the search bar")
	}
	if !tm.copy.active {
		t.Fatal("Enter in copy-mode search must not exit copy mode")
	}
	if got := tm.copy.cursor.Row; got != 1 {
		t.Errorf("cursor row = %d, want 1 (the match)", got)
	}
}

// Escape dismisses the search bar but stays in copy mode; a second Escape
// leaves the mode.
func TestCopyMode_SearchEscapeStaysInMode(t *testing.T) {
	tm, _ := copyTerm(10, "alpha", "bravo")
	key(tm, nil, gui.KeySpace, copyModeMods)
	press(tm, nil, '/')
	key(tm, nil, gui.KeyEscape, 0)
	if tm.search.active {
		t.Error("Escape should close the search bar")
	}
	if !tm.copy.active {
		t.Fatal("Escape from search should stay in copy mode")
	}
	key(tm, nil, gui.KeyEscape, 0)
	if tm.copy.active {
		t.Error("second Escape should exit copy mode")
	}
}

// '[' and ']' walk OSC 133 prompt marks by cursor row, not viewport top.
func TestCopyMode_MarkJump(t *testing.T) {
	tm, _ := copyTerm(8, "p0", "out", "p1", "out")
	g := tm.grid
	g.Marks = []mark{
		{Row: 0, Kind: markPromptStart},
		{Row: 2, Kind: markPromptStart},
	}
	key(tm, nil, gui.KeySpace, copyModeMods)
	press(tm, nil, ']')
	if got := tm.copy.cursor.Row; got != 2 {
		t.Errorf("next mark: cursor row = %d, want 2", got)
	}
	press(tm, nil, '[')
	if got := tm.copy.cursor.Row; got != 0 {
		t.Errorf("prev mark: cursor row = %d, want 0", got)
	}
	// No mark past the last one: the cursor stays put rather than jumping.
	press(tm, nil, '[')
	if got := tm.copy.cursor.Row; got != 0 {
		t.Errorf("no prev mark: cursor row = %d, want 0", got)
	}
}

// n and N continue the last search from the cursor rather than restarting
// from a stale search-bar index.
func TestCopyMode_nNAfterSearch(t *testing.T) {
	tm, _ := copyTerm(10, "alpha", "bravo", "charlie", "bravo2", "delta")
	key(tm, nil, gui.KeySpace, copyModeMods)
	// Move off row 0 so the first match is unambiguous.
	press(tm, nil, 'j')
	press(tm, nil, '/')
	for _, r := range "bravo" {
		tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: uint32(r)}, Window: nil})
	}
	key(tm, nil, gui.KeyEnter, 0) // jump to row 1 (first "bravo")
	if got := tm.copy.cursor.Row; got != 1 {
		t.Fatalf("Enter: cursor row = %d, want 1", got)
	}
	press(tm, nil, 'n') // next match → row 3
	if got := tm.copy.cursor.Row; got != 3 {
		t.Errorf("n: cursor row = %d, want 3", got)
	}
	press(tm, nil, 'N') // prev match → back to row 1
	if got := tm.copy.cursor.Row; got != 1 {
		t.Errorf("N: cursor row = %d, want 1", got)
	}
}

// --- rendering --------------------------------------------------------------

// The copy cursor must be visually distinct from the terminal cursor, and the
// terminal cursor must be suppressed, so exactly one cursor is on screen and
// the user can see their motions land.
func TestCopyMode_DrawsDistinctCursor(t *testing.T) {
	tm, dc := newDrawTerm(4, 10, 10, 20)
	for c, ch := range "hello" {
		tm.grid.At(1, c).Ch = ch
	}
	// Row 1, not 0: row 0 is under the status bar and deliberately unpainted, so
	// a cursor there would be invisible by design rather than by bug.
	tm.grid.CursorR = 1
	tm.onDraw(dc)
	normalRects := len(dc.Batches())

	key(tm, nil, gui.KeySpace, copyModeMods)
	press(tm, nil, 'l')
	tm.onDraw(dc)

	var found bool
	for _, b := range dc.Batches() {
		if b.Color == tm.grid.ov.copyCurFill {
			found = true
			break
		}
	}
	if !found {
		t.Error("no copy-mode cursor drawn in the distinct color")
	}
	if normalRects == 0 {
		t.Error("baseline draw produced nothing; harness is wrong")
	}
}

// The status bar must not have terminal text painted through it: go-gui draws
// all Text above all FilledRects, so the covered row has to be left unpainted
// rather than merely drawn under the band.
func TestCopyMode_ReservesTopRow(t *testing.T) {
	// A DrawContext accumulates entries across frames, so each draw needs its
	// own — comparing before/after on one context would always see the baseline.
	build := func() (*Term, *gui.DrawContext) {
		tm, dc := newDrawTerm(4, 10, 10, 20)
		for c, ch := range "TOPROW" {
			tm.grid.At(0, c).Ch = ch
		}
		for c, ch := range "LASTROW" {
			tm.grid.At(3, c).Ch = ch
		}
		tm.grid.CursorR = 2
		return tm, dc
	}

	base, baseDC := build()
	base.onDraw(baseDC)
	if !drewText(baseDC, "TOPROW") {
		t.Fatal("baseline: top row not drawn; harness is wrong")
	}

	tm, dc := build()
	key(tm, nil, gui.KeySpace, copyModeMods)
	tm.onDraw(dc)
	if drewText(dc, "TOPROW") {
		t.Error("copy mode painted the row its status bar covers")
	}
	// The bottom row keeps rendering — reserving from the top is the whole point,
	// since the newest output is what the user came to copy.
	if !drewText(dc, "LASTROW") {
		t.Error("copy mode dropped the last row, the one worth keeping")
	}
}

// drewText reports whether any text entry in the frame contains s.
func drewText(dc *gui.DrawContext, s string) bool {
	for _, e := range dc.Texts() {
		if strings.Contains(e.Text, s) {
			return true
		}
	}
	return false
}

// --- chord resolution -------------------------------------------------------

// chordForRune is the layout mapping onChar depends on: every copy-mode key
// that is a printable character has to resolve back to the chord the binding
// table stores, or the action never matches.
func TestChordForRune(t *testing.T) {
	cases := []struct {
		r    rune
		key  gui.KeyCode
		mods gui.Modifier
	}{
		{'j', gui.KeyJ, 0},
		{'G', gui.KeyG, gui.ModShift},
		{'0', gui.Key0, 0},
		{'$', gui.Key4, gui.ModShift},     // line end
		{'?', gui.KeySlash, gui.ModShift}, // search backward
		{'/', gui.KeySlash, 0},            // search forward
		{'[', gui.KeyLeftBracket, 0},      // prev prompt mark
		{']', gui.KeyRightBracket, 0},     // next prompt mark
		{' ', gui.KeySpace, 0},
	}
	for _, tc := range cases {
		k, mods, ok := chordForRune(tc.r)
		if !ok || k != tc.key || mods != tc.mods {
			t.Errorf("chordForRune(%q) = (%v, %v, %v), want (%v, %v, true)",
				tc.r, k, mods, ok, tc.key, tc.mods)
		}
	}
	// Non-ASCII has no chord; copy mode has no binding for it either.
	for _, r := range []rune{'é', '€', 0x1F600, 0} {
		if _, _, ok := chordForRune(r); ok {
			t.Errorf("chordForRune(%q) reported a chord; want none", r)
		}
	}
}

// producesChar decides which event source owns a chord. Getting it wrong either
// double-applies a motion or drops it entirely.
func TestProducesChar(t *testing.T) {
	cases := []struct {
		name string
		e    gui.Event
		want bool
	}{
		{"bare letter", gui.Event{KeyCode: gui.KeyJ}, true},
		{"shifted letter still types a character", gui.Event{KeyCode: gui.KeyV, Modifiers: gui.ModShift}, true},
		{"shifted digit", gui.Event{KeyCode: gui.Key4, Modifiers: gui.ModShift}, true},
		{"punctuation", gui.Event{KeyCode: gui.KeySlash}, true},
		{"ctrl suppresses insertText", gui.Event{KeyCode: gui.KeyD, Modifiers: gui.ModCtrl}, false},
		{"super suppresses insertText", gui.Event{KeyCode: gui.KeyC, Modifiers: gui.ModSuper}, false},
		{"alt suppresses insertText", gui.Event{KeyCode: gui.KeyB, Modifiers: gui.ModAlt}, false},
		{"escape is not printable", gui.Event{KeyCode: gui.KeyEscape}, false},
		{"arrow is not printable", gui.Event{KeyCode: gui.KeyDown}, false},
		{"enter is not printable", gui.Event{KeyCode: gui.KeyEnter}, false},
	}
	for _, tc := range cases {
		if got := producesChar(&tc.e); got != tc.want {
			t.Errorf("%s: producesChar = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// --- entry invariants -------------------------------------------------------

// A search bar left open from before copy mode has no driver once the mode
// starts swallowing printable keys, so entry closes it.
func TestCopyMode_EntryClosesOpenSearchBar(t *testing.T) {
	tm, _ := copyTerm(10, "alpha", "bravo")
	tm.search.active = true
	tm.search.query = "al"
	key(tm, nil, gui.KeySpace, copyModeMods)
	if tm.search.active {
		t.Error("entering copy mode left an unusable search bar on screen")
	}
	if tm.copy.searching {
		t.Error("a pre-existing search bar must not be adopted as copy mode's own")
	}
}

// The status bar reserves whole rows off the top, so a sub-pixel scroll offset
// would slide the content out from under it. Entry snaps to a row boundary.
func TestCopyMode_EntrySnapsSubPixelScroll(t *testing.T) {
	tm, _ := copyTerm(8, "r0", "r1")
	g := tm.grid
	g.ScrollbackCap = 10
	g.Scrollback.SetGeom(10, 8)
	g.Scrollback.Push(make([]cell, 8), false)
	g.ViewOffset = 1
	g.ViewSubPx = 4.5
	key(tm, nil, gui.KeySpace, copyModeMods)
	if g.ViewSubPx != 0 {
		t.Errorf("ViewSubPx = %v after entry, want 0", g.ViewSubPx)
	}
}

// Re-entering while already active must not reset the cursor the user has
// already moved.
func TestCopyMode_EnterIsIdempotent(t *testing.T) {
	tm, _ := copyTerm(10, "alpha", "bravo", "charlie")
	key(tm, nil, gui.KeySpace, copyModeMods)
	press(tm, nil, 'j')
	press(tm, nil, 'l')
	want := tm.copy.cursor
	tm.enterCopyMode(nil)
	if got := tm.copy.cursor; got != want {
		t.Errorf("re-entry moved the cursor to %v, want %v", got, want)
	}
}

// Exiting when not active is a no-op — in particular it must not unfreeze a
// viewport it never froze.
func TestCopyMode_ExitWhenInactiveIsNoOp(t *testing.T) {
	tm, _ := copyTerm(8, "hello")
	tm.grid.ViewFrozen = true
	tm.exitCopyMode(nil)
	if !tm.grid.ViewFrozen {
		t.Error("exitCopyMode cleared state it does not own when inactive")
	}
}

// --- paging -----------------------------------------------------------------

// Page and half-page motions cover the same ground as the equivalent scroll
// actions, and both the Ctrl chords and the PageUp/PageDown keys drive them.
func TestCopyMode_PageMotions(t *testing.T) {
	rows := make([]string, 12)
	for i := range rows {
		rows[i] = "r"
	}
	tm, _ := copyTerm(8, rows...)
	key(tm, nil, gui.KeySpace, copyModeMods)

	wantPage := max(tm.grid.Rows-1, 1)
	wantHalf := max(tm.grid.Rows/2, 1)

	key(tm, nil, gui.KeyPageDown, 0)
	if got := tm.copy.cursor.Row; got != wantPage {
		t.Errorf("PageDown: row = %d, want %d", got, wantPage)
	}
	key(tm, nil, gui.KeyPageUp, 0)
	if got := tm.copy.cursor.Row; got != 0 {
		t.Errorf("PageUp: row = %d, want 0", got)
	}
	key(tm, nil, gui.KeyF, gui.ModCtrl)
	if got := tm.copy.cursor.Row; got != wantPage {
		t.Errorf("Ctrl+F: row = %d, want %d", got, wantPage)
	}
	key(tm, nil, gui.KeyU, gui.ModCtrl)
	if got := tm.copy.cursor.Row; got != wantPage-wantHalf {
		t.Errorf("Ctrl+U: row = %d, want %d", got, wantPage-wantHalf)
	}
}

// --- marks and search edge cases --------------------------------------------

// The alt screen has no prompt marks; the jump must not fire against stale
// main-screen ones.
func TestCopyMode_MarkJumpSuppressedOnAltScreen(t *testing.T) {
	tm, _ := copyTerm(8, "p0", "out", "p1", "out")
	g := tm.grid
	g.Marks = []mark{
		{Row: 0, Kind: markPromptStart},
		{Row: 2, Kind: markPromptStart},
	}
	key(tm, nil, gui.KeySpace, copyModeMods)
	g.AltActive = true
	press(tm, nil, ']')
	if got := tm.copy.cursor.Row; got != 0 {
		t.Errorf("alt screen: cursor row = %d, want 0 (no mark jump)", got)
	}
}

// n/N with no query, and regex mode with a query that never compiled, report
// "no match" rather than searching against nil state.
func TestFindMatchFrom_NoQueryOrRegex(t *testing.T) {
	tm, _ := copyTerm(10, "alpha", "bravo")
	if _, ok := tm.findMatchFrom(contentPos{}, true); ok {
		t.Error("empty query reported a match")
	}
	if _, ok := tm.findMatch(true); ok {
		t.Error("empty query reported a match via findMatch")
	}
	tm.search.query = "["
	tm.search.regex = true
	tm.search.re = nil // an invalid pattern leaves re nil
	if _, ok := tm.findMatchFrom(contentPos{}, true); ok {
		t.Error("regex mode with no compiled pattern reported a match")
	}
	if _, ok := tm.findMatch(true); ok {
		t.Error("regex mode with no compiled pattern reported a match via findMatch")
	}
}

// A search that finds nothing leaves the copy cursor where it was.
func TestCopyMode_SearchNoMatchKeepsCursor(t *testing.T) {
	tm, _ := copyTerm(10, "alpha", "bravo")
	key(tm, nil, gui.KeySpace, copyModeMods)
	press(tm, nil, 'j')
	want := tm.copy.cursor
	press(tm, nil, '/')
	for _, r := range "zzz" {
		tm.onChar(gui.EventCtx{Layout: nil, Event: &gui.Event{CharCode: uint32(r)}, Window: nil})
	}
	key(tm, nil, gui.KeyEnter, 0)
	if !tm.copy.active {
		t.Fatal("a failed search must not exit copy mode")
	}
	if got := tm.copy.cursor; got != want {
		t.Errorf("cursor = %v after a failed search, want %v", got, want)
	}
}
