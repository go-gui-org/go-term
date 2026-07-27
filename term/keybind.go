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
