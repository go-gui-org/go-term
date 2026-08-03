package term

import "github.com/go-gui-org/go-gui/gui"

// SetKeyBindings replaces this terminal's Term-level shortcut overrides.
//
// Actions absent from km revert to their built-in chords — km is the complete
// override set, not a patch on the current one, so a config reload can simply
// pass whatever the file now says without tracking what it said before. An
// entry whose gui.Shortcut has Key == 0 unbinds its action, letting that key
// reach the child process. Unknown action names are ignored.
//
// This is the live counterpart to Cfg.KeyBindings, which only seeds the table
// at construction. Both funnel through the same merge, so a terminal
// configured either way behaves identically.
//
// Main-thread only: onKeyDown reads the table without a lock, and both run on
// the main thread.
func (t *Term) SetKeyBindings(km KeyMap) {
	t.bindings = mergeBindings(km)
}

// KeyBindings returns the chord currently bound to each Term-level action.
//
// Actions with several default chords (Copy answers to both Cmd+C and
// Ctrl+Shift+C) report the first, since a KeyMap holds one chord per action;
// use Term.Shortcuts for display, which renders every alternative. Unbound
// actions are present with a zero-value gui.Shortcut, so the result round-trips
// through SetKeyBindings unchanged.
func (t *Term) KeyBindings() KeyMap {
	tbl := t.bindingTable()
	km := make(KeyMap, len(tbl))
	for a, b := range tbl {
		if len(b.chords) == 0 {
			km[a] = gui.Shortcut{} // explicitly unbound
			continue
		}
		km[a] = b.chords[0]
	}
	return km
}

// RunAction invokes a Term-level action as though its chord had been pressed,
// and reports whether it ran. This is what lets a command palette act on the
// entries AvailableShortcuts hands it.
//
// It works by synthesizing the action's first bound chord and feeding it to the
// ordinary key handlers — the same trick copy mode already uses to turn a typed
// character back into a chord (see chordForRune). Dispatching through the real
// handlers is deliberate: each one carries conditional passthrough rules (Cmd+C
// only copies when a selection exists, PageUp only scrolls off the alt screen)
// that a side-door dispatch table would have to duplicate and could then get
// wrong.
//
// An unbound action returns false. There is no chord to synthesize, so an
// action a user has explicitly unbound stays unreachable — which is also why
// AvailableShortcuts omits it from the list in the first place.
//
// Main-thread only.
func (t *Term) RunAction(a Action, w *gui.Window) bool {
	b, ok := t.bindingTable()[a]
	if !ok || len(b.chords) == 0 {
		return false
	}
	c := b.chords[0]
	e := &gui.Event{KeyCode: c.Key, Modifiers: c.Modifiers}

	// Copy-mode actions bypass onKeyDown. Their chords are bare letters, which
	// onKeyDown routes to onChar on the grounds that a real keypress would
	// arrive that way; a synthesized one would be swallowed by that same rule
	// and do nothing. Going straight to the mode's handler is what makes the
	// palette able to drive it.
	if _, isCopyAction := copyActionSet[a]; isCopyAction {
		if !t.copy.active {
			return false
		}
		t.handleCopyModeKey(e, w)
		return true
	}
	t.onKeyDown(nil, e, w)
	return true
}
