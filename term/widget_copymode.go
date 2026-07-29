package term

import "github.com/go-gui-org/go-gui/gui"

// Keyboard copy mode: a modal, vim-keyed way to scroll the buffer, place a
// selection, and yank it without a mouse (issue #102).
//
// The mode is widget-level only — the parser knows nothing about it. It layers
// on machinery that already exists: grid_selection.go for the anchor/head
// model, grid_search.go for '/', grid_mark.go for '[' / ']', and the viewport
// offset in grid_scroll.go. What it adds is a cursor in content coordinates, a
// key dispatch that consumes keys instead of forwarding them to the child, and
// a freeze so output can't move the target out from under the user.
//
// Chords come from the same binding table as every other action; the copy-mode
// group is simply only consulted while the mode is active. See shortcuts.go.

// copyBarRows is how many viewport rows copy mode's status bar consumes at the
// top of the canvas. Also the first viewport row the draw passes paint while
// the mode is active (drawState.renderTop) — one constant so the reveal
// arithmetic and the render can't disagree.
const copyBarRows = 1

// enterCopyMode activates copy mode with the cursor at the terminal cursor's
// content position and the viewport frozen. Main-thread only.
func (t *Term) enterCopyMode(w *gui.Window) {
	if t.copy.active {
		return
	}
	t.copy = copyState{active: true}
	// A search bar already open belongs to the pre-copy-mode search: copy mode
	// swallows the keys that would drive it (see onChar), so leaving it up
	// would paint a bar nothing can type into. Its own '/' opens a fresh one.
	t.search.active = false
	func() {
		g := t.grid
		g.Mu.Lock()
		defer g.Mu.Unlock()
		g.ViewFrozen = true
		// The copy bar reserves whole rows off the top of the canvas, so a
		// sub-pixel scroll offset would slide the content out from under it and
		// leave a gap. Snap to a row boundary on entry.
		g.ViewSubPx = 0
		g.ClearSelection()
		// Start where the user is looking: the terminal cursor, in content
		// coordinates so it stays put as the viewport moves.
		t.copy.cursor = contentPos{
			Row: g.Scrollback.Len() + g.CursorR,
			Col: g.CursorC,
		}
		t.clampCopyCursor()
	}()
	t.revealCursor(w)
}

// exitCopyMode leaves copy mode, always dropping the selection — including on
// the yank path. Leaving the highlight up after 'y' looked like the mode had
// not exited: it survived until the next keystroke happened to clear it, which
// is state with no owner. vim and tmux both drop it on yank. Main-thread only.
func (t *Term) exitCopyMode(w *gui.Window) {
	if !t.copy.active {
		return
	}
	t.copy = copyState{}
	// A search bar opened from copy mode has no owner once the mode is gone.
	t.search.active = false
	func() {
		g := t.grid
		g.Mu.Lock()
		defer g.Mu.Unlock()
		g.ViewFrozen = false
		g.ClearSelection()
	}()
	t.scheduleViewUpdate(w)
}

// clampCopyCursor pins the cursor inside the buffer. Called after every motion
// and on entry, and it is also what absorbs the coordinate shift when the
// scrollback ring evicts rows under a frozen viewport. Caller holds Mu.
func (t *Term) clampCopyCursor() {
	g := t.grid
	t.copy.cursor.Row = clamp(t.copy.cursor.Row, 0, max(g.ContentRows()-1, 0))
	t.copy.cursor.Col = clamp(t.copy.cursor.Col, 0, max(g.Cols-1, 0))
}

// syncSelection derives grid.SelAnchor/SelHead from the copy cursor and anchor.
//
// The grid stores selection endpoints as cell *boundaries* in [0, Cols] with a
// half-open span, while copy mode tracks whole cells; the conversion is where
// vim's inclusive selection meets the grid's half-open one. Both the anchor
// cell and the cursor cell must end up inside the span, so whichever side is
// leading gets the +1. Caller holds Mu.
func (t *Term) syncSelection() {
	g := t.grid
	if t.copy.sel == copySelNone {
		g.SelActive = false
		return
	}
	a, c := t.copy.anchor, t.copy.cursor
	forward := c.Row > a.Row || (c.Row == a.Row && c.Col >= a.Col)

	if t.copy.sel == copySelLine {
		// Whole rows: the earlier row starts at the left edge, the later row
		// runs to the right edge (boundary Cols).
		if forward {
			g.SelAnchor = contentPos{Row: a.Row, Col: 0}
			g.SelHead = contentPos{Row: c.Row, Col: g.Cols}
		} else {
			g.SelAnchor = contentPos{Row: a.Row, Col: g.Cols}
			g.SelHead = contentPos{Row: c.Row, Col: 0}
		}
	} else if forward {
		g.SelAnchor = a
		g.SelHead = contentPos{Row: c.Row, Col: c.Col + 1}
	} else {
		g.SelAnchor = contentPos{Row: a.Row, Col: a.Col + 1}
		g.SelHead = c
	}
	g.SelActive = true
	g.hasSelAnchor = true
}

// revealCursor scrolls the viewport by the minimum needed to bring the copy
// cursor's row on screen, refreshes the derived selection, and repaints.
// Main-thread only; takes Mu itself, so callers must not hold it.
func (t *Term) revealCursor(w *gui.Window) {
	func() {
		g := t.grid
		g.Mu.Lock()
		defer g.Mu.Unlock()
		t.clampCopyCursor()
		// ContentRowToScreen is unclamped, so its sign tells us both the
		// direction and the distance to scroll. ScrollView takes positive =
		// back into history, hence the negations.
		//
		// The floor is viewport row 1, not 0: the copy bar covers row 0 and
		// that row is not painted, so a cursor there would be invisible. At
		// the very top of the buffer ScrollView clamps and the cursor does
		// land on row 0 — the one place the bar can hide it, and only for the
		// oldest line in the scrollback.
		switch sr := g.ContentRowToScreen(t.copy.cursor.Row); {
		case sr < copyBarRows:
			g.ScrollView(copyBarRows - sr)
		case sr >= g.Rows:
			g.ScrollView(-(sr - g.Rows + 1))
		}
		t.syncSelection()
	}()
	t.scheduleViewUpdate(w)
}

// copyPageStep returns the row distance for a full-page motion, matching
// scrollByPage so keyboard paging and PageUp scrolling cover the same ground.
func (t *Term) copyPageStep() int {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return max(t.grid.Rows-1, 1)
}

// contentRows reports the total content rows (scrollback + live) under Mu, for
// callers that need it outside an existing critical section.
func (t *Term) contentRows() int {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return t.grid.ContentRows()
}

// copyHalfPageStep returns the row distance for Ctrl+U / Ctrl+D.
func (t *Term) copyHalfPageStep() int {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return max(t.grid.Rows/2, 1)
}

// moveCopyRows moves the cursor by delta rows (positive = toward newer output).
func (t *Term) moveCopyRows(delta int, w *gui.Window) {
	t.copy.cursor.Row += delta
	t.revealCursor(w)
}

// moveCopyCols moves the cursor by delta columns within its row. Deliberately
// does not wrap to the neighbouring row: h/l in vim stay on the line, and a
// terminal row is a fixed-width thing users navigate as a grid.
func (t *Term) moveCopyCols(delta int, w *gui.Window) {
	t.copy.cursor.Col += delta
	t.revealCursor(w)
}

// copyLineEnd returns the column of the last non-blank cell on the cursor's
// row, or 0 for a blank row — the '$' target.
func (t *Term) copyLineEnd() int {
	g := t.grid
	g.Mu.Lock()
	defer g.Mu.Unlock()
	row := t.copy.cursor.Row
	last := 0
	for c := range g.Cols {
		if ch := g.ContentCellAt(row, c).Ch; ch != 0 && ch != ' ' {
			last = c
		}
	}
	return last
}

// startCopySelection begins (or switches) a selection at the cursor.
// Pressing the same selection key again cancels, matching vim.
func (t *Term) startCopySelection(mode copySelMode, w *gui.Window) {
	if t.copy.sel == mode {
		t.copy.sel = copySelNone
	} else {
		if t.copy.sel == copySelNone {
			t.copy.anchor = t.copy.cursor
		}
		t.copy.sel = mode
	}
	t.revealCursor(w)
}

// yankCopySelection copies the selection to the clipboard and exits. With no
// selection yet, yanks the single cell under the cursor — a keystroke that did
// nothing would just look broken.
func (t *Term) yankCopySelection(w *gui.Window) {
	if t.copy.sel == copySelNone {
		t.copy.anchor = t.copy.cursor
		t.copy.sel = copySelChar
		func() {
			t.grid.Mu.Lock()
			defer t.grid.Mu.Unlock()
			t.syncSelection()
		}()
	}
	t.copySelection(w)
	t.exitCopyMode(w)
}

// copyWordMotion moves the cursor a word forward or back.
func (t *Term) copyWordMotion(forward bool, w *gui.Window) {
	func() {
		g := t.grid
		g.Mu.Lock()
		defer g.Mu.Unlock()
		if forward {
			t.copy.cursor = g.wordFwd(t.copy.cursor)
		} else {
			t.copy.cursor = g.wordBack(t.copy.cursor)
		}
	}()
	t.revealCursor(w)
}

// copyMarkJump moves the cursor to the previous/next OSC 133 prompt mark
// relative to its current row. Distinct from jumpToMark, which works from the
// viewport top and moves only the view. Suppressed on the alt screen, which
// has no marks. Returns without moving when there is none in that direction.
func (t *Term) copyMarkJump(backward bool, w *gui.Window) {
	moved := func() bool {
		g := t.grid
		g.Mu.Lock()
		defer g.Mu.Unlock()
		if g.AltActive {
			return false
		}
		var (
			row int
			ok  bool
		)
		if backward {
			row, ok = g.PrevMark(t.copy.cursor.Row, markPromptStart)
		} else {
			row, ok = g.NextMark(t.copy.cursor.Row, markPromptStart)
		}
		if !ok {
			return false
		}
		t.copy.cursor = contentPos{Row: row}
		return true
	}()
	if moved {
		t.revealCursor(w)
	}
}

// openCopySearch opens the search bar on copy mode's behalf. The bar itself
// behaves exactly as it does outside copy mode; copy.searching is what routes
// the outcome back here.
func (t *Term) openCopySearch(backward bool, w *gui.Window) {
	t.copy.searching = true
	t.copy.backward = backward
	t.search.active = true
	t.search.query = ""
	t.search.matches = nil
	t.search.idx = 0
	t.scheduleViewUpdate(w)
}

// copySearchJump moves the cursor to the next match in the given direction.
// Unlike the search bar's Enter handler, which uses findMatch (search-bar
// match state), n/N search from the cursor so each press advances to the next
// match rather than restarting from a stale t.search.idx.
func (t *Term) copySearchJump(forward bool, w *gui.Window) {
	pos, ok := t.findMatchFrom(t.copy.cursor, forward)
	if !ok {
		return
	}
	t.copy.cursor = pos
	t.revealCursor(w)
}

// Which event carries a key depends on whether it is printable, and that split
// is what decides where copy mode dispatches it.
//
// On macOS the view routes keyDown: through interpretKeyEvents:, so an
// unmodified printable key arrives *only* as OnChar — there is no OnKeyDown for
// a bare 'j'. Holding Cmd/Ctrl/Alt suppresses insertText:, so those chords do
// arrive as OnKeyDown, which is why Cmd+C has always worked. Other backends may
// deliver both events for the same keystroke.
//
// So the source is chosen by the chord, not the platform: bare printable chords
// are dispatched from onChar and *ignored* by onKeyDown, everything else the
// reverse. Neither can double-fire, whatever the backend sends.

// producesChar reports whether this key event will also arrive as an OnChar.
// True for printable keys with no Ctrl/Alt/Super held — Shift doesn't count,
// since Shift+v still types a character. onKeyDown ignores these in copy mode
// and lets onChar dispatch them.
func producesChar(e *gui.Event) bool {
	if e.Modifiers&(gui.ModCtrl|gui.ModAlt|gui.ModSuper) != 0 {
		return false
	}
	k := e.KeyCode
	switch {
	case k >= gui.KeyA && k <= gui.KeyZ,
		k >= gui.Key0 && k <= gui.Key9:
		return true
	}
	switch k {
	case gui.KeySpace, gui.KeyApostrophe, gui.KeyComma, gui.KeyMinus,
		gui.KeyPeriod, gui.KeySlash, gui.KeySemicolon, gui.KeyEqual,
		gui.KeyLeftBracket, gui.KeyBackslash, gui.KeyRightBracket,
		gui.KeyGraveAccent:
		return true
	}
	return false
}

// shiftedDigits maps the shifted forms of the number row to their base keys, so
// a character like '$' resolves to the Key4+Shift chord the binding table
// stores. US layout, like every other chord literal in shortcuts.go.
var shiftedDigits = map[rune]gui.KeyCode{
	'!': gui.Key1, '@': gui.Key2, '#': gui.Key3, '$': gui.Key4, '%': gui.Key5,
	'^': gui.Key6, '&': gui.Key7, '*': gui.Key8, '(': gui.Key9, ')': gui.Key0,
}

// shiftedPunct maps the shifted forms of the punctuation keys to their bases.
var shiftedPunct = map[rune]gui.KeyCode{
	'"': gui.KeyApostrophe, '<': gui.KeyComma, '_': gui.KeyMinus,
	'>': gui.KeyPeriod, '?': gui.KeySlash, ':': gui.KeySemicolon,
	'+': gui.KeyEqual, '{': gui.KeyLeftBracket, '|': gui.KeyBackslash,
	'}': gui.KeyRightBracket, '~': gui.KeyGraveAccent,
}

// unshiftedPunct maps punctuation characters to their key codes.
var unshiftedPunct = map[rune]gui.KeyCode{
	'\'': gui.KeyApostrophe, ',': gui.KeyComma, '-': gui.KeyMinus,
	'.': gui.KeyPeriod, '/': gui.KeySlash, ';': gui.KeySemicolon,
	'=': gui.KeyEqual, '[': gui.KeyLeftBracket, '\\': gui.KeyBackslash,
	']': gui.KeyRightBracket, '`': gui.KeyGraveAccent, ' ': gui.KeySpace,
}

// chordForRune resolves a typed character back to the (KeyCode, Modifiers)
// chord the binding table is written in, so a character-only event can be
// matched against the same table as a key event. Reports false for characters
// with no chord — non-ASCII input, which copy mode has no binding for anyway.
//
// This is a keyboard-layout mapping, not a second binding table: it decides
// only which chord was typed, never which action it runs.
func chordForRune(r rune) (gui.KeyCode, gui.Modifier, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return gui.KeyA + gui.KeyCode(r-'a'), 0, true
	case r >= 'A' && r <= 'Z':
		return gui.KeyA + gui.KeyCode(r-'A'), gui.ModShift, true
	case r >= '0' && r <= '9':
		return gui.Key0 + gui.KeyCode(r-'0'), 0, true
	}
	if k, ok := shiftedDigits[r]; ok {
		return k, gui.ModShift, true
	}
	if k, ok := shiftedPunct[r]; ok {
		return k, gui.ModShift, true
	}
	if k, ok := unshiftedPunct[r]; ok {
		return k, 0, true
	}
	return 0, 0, false
}

// handleCopyModeChar dispatches a printable character typed in copy mode by
// resolving it to its chord and running the ordinary key dispatch. This is the
// only path bare vim keys take on macOS.
func (t *Term) handleCopyModeChar(r rune, w *gui.Window) {
	k, mods, ok := chordForRune(r)
	if !ok {
		return
	}
	t.handleCopyModeKey(&gui.Event{KeyCode: k, Modifiers: mods}, w)
}

// handleCopyModeKey dispatches one key while copy mode is active. Every key is
// consumed, matched or not — apart from the mode-entry chord, which the caller
// handles first — so there is nothing for callers to branch on: copy mode must
// not leak keystrokes to the child.
func (t *Term) handleCopyModeKey(e *gui.Event, w *gui.Window) {
	switch {
	case t.binds(ActionCopyModeExit, e):
		t.exitCopyMode(w)

	// Motion. The cases are grouped by kind, not ordered by precedence: every
	// copy-mode binding is exact on the modifier bits (shiftOptional is false
	// throughout — see defaultBindings), so the pairs that differ only by Shift
	// (g/G, v/V, n/N) can't match each other's case and reordering them changes
	// nothing. Order decides a winner only if a user rebinds two actions onto
	// the same chord, where first-listed wins.
	case t.binds(ActionCopyModeLeft, e):
		t.moveCopyCols(-1, w)
	case t.binds(ActionCopyModeDown, e):
		t.moveCopyRows(+1, w)
	case t.binds(ActionCopyModeUp, e):
		t.moveCopyRows(-1, w)
	case t.binds(ActionCopyModeRight, e):
		t.moveCopyCols(+1, w)
	case t.binds(ActionCopyModeWordFwd, e):
		t.copyWordMotion(true, w)
	case t.binds(ActionCopyModeWordBack, e):
		t.copyWordMotion(false, w)
	case t.binds(ActionCopyModeLineStart, e):
		t.copy.cursor.Col = 0
		t.revealCursor(w)
	case t.binds(ActionCopyModeLineEnd, e):
		t.copy.cursor.Col = t.copyLineEnd()
		t.revealCursor(w)
	case t.binds(ActionCopyModeBottom, e):
		t.copy.cursor.Row = t.contentRows() - 1
		t.revealCursor(w)
	case t.binds(ActionCopyModeTop, e):
		t.copy.cursor = contentPos{}
		t.revealCursor(w)
	case t.binds(ActionCopyModeHalfPageUp, e):
		t.moveCopyRows(-t.copyHalfPageStep(), w)
	case t.binds(ActionCopyModeHalfPageDown, e):
		t.moveCopyRows(+t.copyHalfPageStep(), w)
	case t.binds(ActionCopyModePageUp, e):
		t.moveCopyRows(-t.copyPageStep(), w)
	case t.binds(ActionCopyModePageDown, e):
		t.moveCopyRows(+t.copyPageStep(), w)

	// Selection and yank.
	case t.binds(ActionCopyModeSelectLine, e):
		t.startCopySelection(copySelLine, w)
	case t.binds(ActionCopyModeSelectChar, e):
		t.startCopySelection(copySelChar, w)
	case t.binds(ActionCopyModeYank, e), t.binds(ActionCopy, e):
		t.yankCopySelection(w)

	// Search and marks.
	case t.binds(ActionCopyModeSearchBack, e):
		t.openCopySearch(true, w)
	case t.binds(ActionCopyModeSearchFwd, e):
		t.openCopySearch(false, w)
	case t.binds(ActionCopyModePrevMatch, e):
		t.copySearchJump(t.copy.backward, w)
	case t.binds(ActionCopyModeNextMatch, e):
		t.copySearchJump(!t.copy.backward, w)
	case t.binds(ActionCopyModePrevMark, e):
		t.copyMarkJump(true, w)
	case t.binds(ActionCopyModeNextMark, e):
		t.copyMarkJump(false, w)
	}
	// Consume unconditionally, matched or not: an unhandled letter reaching
	// the shell is the one failure mode a modal keymap must never have.
	e.IsHandled = true
}

// finishCopySearch returns control to copy mode after the search bar closes.
// accepted is true for Enter (jump to the match), false for Escape.
//
// The direction inversion (!t.copy.backward) is intentional: copy.backward
// records the initial search direction (true for '?'), and findMatch takes a
// "forward" bool where true means higher row numbers. So continuing the initial
// direction means passing !copy.backward to findMatch.
//
// The initial jump uses findMatch (search-bar match state via t.search.idx)
// so the jump goes to the incrementally-highlighted match, not a fresh search
// from the cursor. Subsequent n/N presses use copySearchJump, which searches
// from the cursor so each press advances.
func (t *Term) finishCopySearch(accepted bool, w *gui.Window) {
	t.copy.searching = false
	t.search.active = false
	if accepted {
		pos, ok := t.findMatch(!t.copy.backward)
		if ok {
			t.copy.cursor = pos
			t.revealCursor(w)
		}
		return
	}
	t.scheduleViewUpdate(w)
}
