package term

import (
	"slices"
	"strings"

	"github.com/go-gui-org/go-gui/gui"
)

// Action names a rebindable Term-level keyboard action. Bindings for these
// live in a per-Term table seeded from Cfg.KeyBindings and mutated at runtime
// by Term.SetKeyBindings.
//
// Workspace-level actions (tabs, panes, theme) are not Actions — they are
// gui.Commands in the workspace command registry and are rebound there.
type Action string

// Term-level rebindable actions. The "term." prefix keeps them distinct from
// workspace command IDs when both are named in one config file.
const (
	ActionCopy           Action = "term.copy"
	ActionPaste          Action = "term.paste"
	ActionFind           Action = "term.find"
	ActionToggleRegex    Action = "term.toggle-regex"
	ActionNextMatch      Action = "term.next-match"
	ActionPrevMatch      Action = "term.prev-match"
	ActionPrevPrompt     Action = "term.prev-prompt"
	ActionNextPrompt     Action = "term.next-prompt"
	ActionScrollPageUp   Action = "term.scroll-page-up"
	ActionScrollPageDown Action = "term.scroll-page-down"
	ActionScrollTop      Action = "term.scroll-top"
	ActionScrollBottom   Action = "term.scroll-bottom"
	ActionFontInc        Action = "term.font-inc"
	ActionFontDec        Action = "term.font-dec"
	ActionFontReset      Action = "term.font-reset"
)

// KeyMap overrides the default chord for individual Actions. Actions absent
// from the map keep their defaults. An entry whose gui.Shortcut has Key == 0
// (gui.KeyInvalid) unbinds the action entirely, so that key reaches the child
// process instead of being intercepted.
//
// An override replaces the action's whole default chord list with the single
// given chord, but inherits the action's Shift tolerance — see binding.
type KeyMap map[Action]gui.Shortcut

// binding is one action's resolved chord set.
//
// shiftOptional records whether Shift is meaningful for this action or merely
// a keyboard artefact. It has to be per-action rather than a property of the
// matcher: Shift is noise for font zoom (typing '+' on the '=' key presses
// Shift) but load-bearing for search direction (Enter is next match,
// Shift+Enter is previous). A matcher that tolerated Shift everywhere would
// let next-match swallow Shift+Enter and make previous-match unreachable.
//
// A chord that names ModShift itself always matches exactly, regardless of the
// flag — the flag only ever relaxes chords that don't mention Shift.
type binding struct {
	chords        []gui.Shortcut
	shiftOptional bool
}

// modKeyMask selects the keyboard modifier bits. gui.Modifier also carries
// mouse-button state (ModLMB/ModRMB/ModMMB), which must not participate in
// chord comparison — otherwise a shortcut would stop working while a mouse
// button is held, e.g. PageUp during a selection drag.
const modKeyMask = gui.ModShift | gui.ModCtrl | gui.ModAlt | gui.ModSuper

// matches reports whether a key event's code and modifiers satisfy this
// binding. See binding.shiftOptional for the Shift rule.
func (b binding) matches(key gui.KeyCode, mods gui.Modifier) bool {
	mods &= modKeyMask
	for _, c := range b.chords {
		if c.Key == gui.KeyInvalid || c.Key != key {
			continue // unbound, or a different key
		}
		want := c.Modifiers & modKeyMask
		got := mods
		// Relax Shift only for chords that don't name it themselves.
		if b.shiftOptional && want&gui.ModShift == 0 {
			got &^= gui.ModShift
		}
		if got == want {
			return true
		}
	}
	return false
}

// actionOrder lists the Actions in help-overlay display order. Shortcuts()
// walks it, so it also fixes the order of the cheatsheet.
var actionOrder = []Action{
	ActionCopy, ActionPaste, ActionFind, ActionToggleRegex,
	ActionNextMatch, ActionPrevMatch, ActionPrevPrompt, ActionNextPrompt,
	ActionScrollPageUp, ActionScrollPageDown, ActionScrollTop, ActionScrollBottom,
	ActionFontInc, ActionFontDec, ActionFontReset,
}

// actionLabels are the human-readable names shown in the help overlay.
var actionLabels = map[Action]string{
	ActionCopy:           "Copy",
	ActionPaste:          "Paste",
	ActionFind:           "Find",
	ActionToggleRegex:    "Toggle regex (in Find)",
	ActionNextMatch:      "Next match (in Find)",
	ActionPrevMatch:      "Previous match (in Find)",
	ActionPrevPrompt:     "Previous prompt mark",
	ActionNextPrompt:     "Next prompt mark",
	ActionScrollPageUp:   "Scroll page up",
	ActionScrollPageDown: "Scroll page down",
	ActionScrollTop:      "Scroll to top",
	ActionScrollBottom:   "Scroll to bottom",
	ActionFontInc:        "Increase font size",
	ActionFontDec:        "Decrease font size",
	ActionFontReset:      "Reset font size",
}

// defaultBindings returns the built-in binding table, with every chord run
// through remapMod so Super-based combos become their Windows equivalents.
// Chords are written here in macOS terms (ModSuper) and translated once, at
// table-build time — never hardcode the Ctrl+Shift forms in the literals.
//
// Keep this in sync with the handlers in widget_keyboard.go; it is the single
// source of truth for both matching and the help overlay.
func defaultBindings() map[Action]binding {
	// b builds a binding from Super-written chords, remapping each.
	b := func(shiftOptional bool, chords ...gui.Shortcut) binding {
		out := make([]gui.Shortcut, len(chords))
		for i, c := range chords {
			out[i] = gui.Shortcut{Key: c.Key, Modifiers: remapMod(c.Modifiers)}
		}
		return binding{chords: out, shiftOptional: shiftOptional}
	}
	k := func(key gui.KeyCode, mods gui.Modifier) gui.Shortcut {
		return gui.Shortcut{Key: key, Modifiers: mods}
	}
	return map[Action]binding{
		// Copy/Paste accept the macOS chord and the Ctrl+Shift chord that
		// Linux terminals use; on Windows remapMod collapses them into one.
		ActionCopy:  b(true, k(gui.KeyC, gui.ModSuper), k(gui.KeyC, gui.ModCtrlShift)),
		ActionPaste: b(true, k(gui.KeyV, gui.ModSuper), k(gui.KeyV, gui.ModCtrlShift)),

		ActionFind:        b(true, k(gui.KeyF, gui.ModSuper)),
		ActionToggleRegex: b(true, k(gui.KeyR, gui.ModCtrl)),

		// Enter/Shift+Enter is the one pair where Shift picks the direction,
		// so next-match must not tolerate it.
		ActionNextMatch: b(false, k(gui.KeyEnter, 0), k(gui.KeyKPEnter, 0)),
		ActionPrevMatch: b(false, k(gui.KeyEnter, gui.ModShift), k(gui.KeyKPEnter, gui.ModShift)),

		ActionPrevPrompt: b(true, k(gui.KeyUp, gui.ModSuper)),
		ActionNextPrompt: b(true, k(gui.KeyDown, gui.ModSuper)),

		// One chord each: shiftOptional makes plain PageUp and Shift+PageUp
		// both match, and scrollbackIntercept's own alt-screen check decides
		// whether to steal the key or pass it through.
		ActionScrollPageUp:   b(true, k(gui.KeyPageUp, 0)),
		ActionScrollPageDown: b(true, k(gui.KeyPageDown, 0)),
		ActionScrollTop:      b(false, k(gui.KeyHome, gui.ModShift)),
		ActionScrollBottom:   b(false, k(gui.KeyEnd, gui.ModShift)),

		// Shift is tolerated so '+' on the '=' key still zooms in.
		ActionFontInc:   b(true, k(gui.KeyEqual, gui.ModSuper)),
		ActionFontDec:   b(true, k(gui.KeyMinus, gui.ModSuper)),
		ActionFontReset: b(true, k(gui.Key0, gui.ModSuper)),
	}
}

// mergeBindings returns the default table with km's overrides applied. An
// override replaces the action's chord list with the single given chord but
// keeps its shiftOptional flag, so rebinding Find to Cmd+G still tolerates
// Cmd+Shift+G. Unknown actions in km are ignored.
//
// Both the Cfg seed path (applyKeyBindings) and the runtime setter
// (SetKeyBindings) go through here, so the two can't diverge.
func mergeBindings(km KeyMap) map[Action]binding {
	tbl := defaultBindings()
	for a, s := range km {
		def, ok := tbl[a]
		if !ok {
			continue // unknown action name; ignore rather than panic
		}
		// Key == 0 means "unbind": an empty chord list never matches.
		if s.Key == gui.KeyInvalid {
			tbl[a] = binding{shiftOptional: def.shiftOptional}
			continue
		}
		tbl[a] = binding{
			chords:        []gui.Shortcut{{Key: s.Key, Modifiers: s.Modifiers & modKeyMask}},
			shiftOptional: def.shiftOptional,
		}
	}
	return tbl
}

// ShortcutInfo describes one Term-level keyboard shortcut for display in a
// help / cheatsheet overlay.
//
// The Term handles these shortcuts imperatively in onKeyDown (see
// handleSearchKey, handleClipboardKey, scrollbackIntercept) because each
// needs conditional passthrough to the child process — e.g. plain Ctrl+C
// must still send SIGINT, and Cmd+C only copies when a selection exists.
// A declarative command registry can't own that dispatch. The binding table
// in this file owns only the *matching*, which is why it can be data; the
// conditional passthrough stays in the handlers.
type ShortcutInfo struct {
	Label string
	Keys  string // human-readable, platform-formatted (macOS glyphs on darwin)
}

// formatChords renders a chord list for display, joining alternatives with
// " / " and dropping duplicates — on Windows remapMod collapses the macOS and
// Ctrl+Shift forms of Copy onto the same combo, which should print once.
func formatChords(chords []gui.Shortcut) string {
	var parts []string
	for _, c := range chords {
		if s := c.String(); !slices.Contains(parts, s) {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " / ")
}

// shortcutsFrom renders a binding table as display entries in actionOrder.
// Unbound actions are omitted — a cheatsheet line with no keys is noise.
func shortcutsFrom(tbl map[Action]binding) []ShortcutInfo {
	out := make([]ShortcutInfo, 0, len(actionOrder))
	for _, a := range actionOrder {
		b, ok := tbl[a]
		if !ok || len(b.chords) == 0 {
			continue
		}
		out = append(out, ShortcutInfo{Label: actionLabels[a], Keys: formatChords(b.chords)})
	}
	return out
}

// Shortcuts returns the *default* Term-level keyboard shortcuts in display
// order. Embedders that let users rebind should call Term.Shortcuts instead,
// which reflects the overrides actually in effect.
//
// Workspace-level shortcuts (tabs, panes, theme) live in the workspace
// command registry and are listed separately by the help overlay.
func Shortcuts() []ShortcutInfo { return shortcutsFrom(defaultBindings()) }

// Shortcuts returns this terminal's effective Term-level shortcuts, including
// any overrides from Cfg.KeyBindings or SetKeyBindings, in display order.
func (t *Term) Shortcuts() []ShortcutInfo { return shortcutsFrom(t.bindings) }
