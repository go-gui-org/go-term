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
	ActionJumpFailure    Action = "term.jump-failure"
	ActionSelectOutput   Action = "term.select-output"
	ActionScrollPageUp   Action = "term.scroll-page-up"
	ActionScrollPageDown Action = "term.scroll-page-down"
	ActionScrollTop      Action = "term.scroll-top"
	ActionScrollBottom   Action = "term.scroll-bottom"
	ActionFontInc        Action = "term.font-inc"
	ActionFontDec        Action = "term.font-dec"
	ActionFontReset      Action = "term.font-reset"
	ActionCopyMode       Action = "term.copy-mode"
	ActionHints          Action = "term.hints"
	ActionHintsCopy      Action = "term.hints-copy"
)

// Copy-mode actions. These are only consulted while copy mode is active, so
// their chords are bare letters that would otherwise reach the child process.
// They live in the same binding table as every other Action — the mode gates
// *when* the table is consulted, not which table — so they stay rebindable
// through [keybindings] without a second matcher.
//
// They are deliberately absent from actionOrder: the help overlay is a flat
// list, and twenty vim keys would swamp it. Copy mode shows its own key hints
// in its indicator bar, and docs/config.md lists the action names.
const (
	ActionCopyModeExit         Action = "term.copy-mode.exit"
	ActionCopyModeLeft         Action = "term.copy-mode.left"
	ActionCopyModeDown         Action = "term.copy-mode.down"
	ActionCopyModeUp           Action = "term.copy-mode.up"
	ActionCopyModeRight        Action = "term.copy-mode.right"
	ActionCopyModeWordFwd      Action = "term.copy-mode.word-fwd"
	ActionCopyModeWordBack     Action = "term.copy-mode.word-back"
	ActionCopyModeLineStart    Action = "term.copy-mode.line-start"
	ActionCopyModeLineEnd      Action = "term.copy-mode.line-end"
	ActionCopyModeTop          Action = "term.copy-mode.top"
	ActionCopyModeBottom       Action = "term.copy-mode.bottom"
	ActionCopyModeHalfPageUp   Action = "term.copy-mode.half-page-up"
	ActionCopyModeHalfPageDown Action = "term.copy-mode.half-page-down"
	ActionCopyModePageUp       Action = "term.copy-mode.page-up"
	ActionCopyModePageDown     Action = "term.copy-mode.page-down"
	ActionCopyModeSelectChar   Action = "term.copy-mode.select-char"
	ActionCopyModeSelectLine   Action = "term.copy-mode.select-line"
	ActionCopyModeYank         Action = "term.copy-mode.yank"
	ActionCopyModeSearchFwd    Action = "term.copy-mode.search-fwd"
	ActionCopyModeSearchBack   Action = "term.copy-mode.search-back"
	ActionCopyModeNextMatch    Action = "term.copy-mode.next-match"
	ActionCopyModePrevMatch    Action = "term.copy-mode.prev-match"
	ActionCopyModePrevMark     Action = "term.copy-mode.prev-mark"
	ActionCopyModeNextMark     Action = "term.copy-mode.next-mark"
)

// actionSet indexes both action lists for ParseAction. Built once at init
// rather than scanned per lookup — a config file naming every action would
// otherwise be quadratic, and this keeps the two order slices the only lists
// to maintain. Copy-mode actions are included: they are rebindable even though
// they don't appear in the help overlay.
var actionSet = func() map[Action]struct{} {
	m := make(map[Action]struct{}, len(actionOrder)+len(copyActionOrder))
	for _, a := range actionOrder {
		m[a] = struct{}{}
	}
	for _, a := range copyActionOrder {
		m[a] = struct{}{}
	}
	return m
}()

// copyActionSet is membership in copyActionOrder alone. RunAction needs to know
// which actions only mean something inside copy mode, and which therefore have
// to be dispatched to the mode's handler rather than through onKeyDown.
var copyActionSet = func() map[Action]struct{} {
	m := make(map[Action]struct{}, len(copyActionOrder))
	for _, a := range copyActionOrder {
		m[a] = struct{}{}
	}
	return m
}()

// ParseAction resolves an action name — the full "term."-prefixed form, e.g.
// "term.copy" — to an Action, reporting whether it names a real one.
//
// Config-file parsers need this: KeyMap silently ignores unknown actions (so a
// map built in code can't panic), which would turn a typo in a user's config
// into a binding that mysteriously does nothing. Checking here lets the
// embedder log it instead. Matching is exact; no fuzzy resolution.
func ParseAction(name string) (Action, bool) {
	a := Action(name)
	_, ok := actionSet[a]
	return a, ok
}

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
	ActionJumpFailure, ActionSelectOutput,
	ActionScrollPageUp, ActionScrollPageDown, ActionScrollTop, ActionScrollBottom,
	ActionFontInc, ActionFontDec, ActionFontReset,
	ActionCopyMode, ActionHints, ActionHintsCopy,
}

// copyActionOrder lists the copy-mode Actions. Kept separate from actionOrder
// so shortcutsFrom (and therefore the help overlay) skips them while
// ParseAction and defaultBindings still cover them.
var copyActionOrder = []Action{
	ActionCopyModeExit,
	ActionCopyModeLeft, ActionCopyModeDown, ActionCopyModeUp, ActionCopyModeRight,
	ActionCopyModeWordFwd, ActionCopyModeWordBack,
	ActionCopyModeLineStart, ActionCopyModeLineEnd,
	ActionCopyModeTop, ActionCopyModeBottom,
	ActionCopyModeHalfPageUp, ActionCopyModeHalfPageDown,
	ActionCopyModePageUp, ActionCopyModePageDown,
	ActionCopyModeSelectChar, ActionCopyModeSelectLine, ActionCopyModeYank,
	ActionCopyModeSearchFwd, ActionCopyModeSearchBack,
	ActionCopyModeNextMatch, ActionCopyModePrevMatch,
	ActionCopyModePrevMark, ActionCopyModeNextMark,
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
	ActionJumpFailure:    "Jump to last failed command",
	ActionSelectOutput:   "Select command output",
	ActionScrollPageUp:   "Scroll page up",
	ActionScrollPageDown: "Scroll page down",
	ActionScrollTop:      "Scroll to top",
	ActionScrollBottom:   "Scroll to bottom",
	ActionFontInc:        "Increase font size",
	ActionFontDec:        "Decrease font size",
	ActionFontReset:      "Reset font size",
	ActionCopyMode:       "Copy mode",
	ActionHints:          "Open link (keyboard hints)",
	ActionHintsCopy:      "Copy link (keyboard hints)",

	// Copy-mode labels are unused by the flat help overlay (copyActionOrder is
	// not walked by shortcutsFrom) but kept complete so an embedder rendering
	// its own copy-mode cheatsheet from the table has names to show.
	ActionCopyModeExit:         "Exit copy mode",
	ActionCopyModeLeft:         "Move left",
	ActionCopyModeDown:         "Move down",
	ActionCopyModeUp:           "Move up",
	ActionCopyModeRight:        "Move right",
	ActionCopyModeWordFwd:      "Next word",
	ActionCopyModeWordBack:     "Previous word",
	ActionCopyModeLineStart:    "Line start",
	ActionCopyModeLineEnd:      "Line end",
	ActionCopyModeTop:          "Buffer top",
	ActionCopyModeBottom:       "Buffer bottom",
	ActionCopyModeHalfPageUp:   "Half page up",
	ActionCopyModeHalfPageDown: "Half page down",
	ActionCopyModePageUp:       "Page up",
	ActionCopyModePageDown:     "Page down",
	ActionCopyModeSelectChar:   "Select (character-wise)",
	ActionCopyModeSelectLine:   "Select (line-wise)",
	ActionCopyModeYank:         "Yank selection",
	ActionCopyModeSearchFwd:    "Search forward",
	ActionCopyModeSearchBack:   "Search backward",
	ActionCopyModeNextMatch:    "Next match",
	ActionCopyModePrevMatch:    "Previous match",
	ActionCopyModePrevMark:     "Previous prompt mark",
	ActionCopyModeNextMark:     "Next prompt mark",
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

		// shiftOptional is moot for a chord that already names Shift.
		ActionJumpFailure:  b(false, k(gui.KeyE, gui.ModSuper|gui.ModShift)),
		ActionSelectOutput: b(false, k(gui.KeyO, gui.ModSuper|gui.ModShift)),

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

		// Copy mode entry. Two chords for the same reason Copy has two: the
		// macOS form and the Ctrl+Shift form Linux terminals use — Alacritty
		// binds its Vi mode to Ctrl+Shift+Space. Writing only the Super form
		// would remap to Ctrl+Alt+Space on Windows, which nobody expects.
		// Cmd+Space (Spotlight) and Ctrl+Space (input source) are avoided.
		ActionCopyMode: b(false,
			k(gui.KeySpace, gui.ModSuper|gui.ModShift),
			k(gui.KeySpace, gui.ModCtrlShift)),

		// Keyboard link hints. Shift is part of both chords because the
		// unshifted Cmd+U / Cmd+Y are widely taken (readline kill-line, yank),
		// and because Cmd+Shift+U is what kitty and WezTerm use for the same
		// gesture. The copy variant sits next to it on Y for "yank".
		ActionHints: b(false,
			k(gui.KeyU, gui.ModSuper|gui.ModShift),
			k(gui.KeyU, gui.ModCtrlShift)),
		ActionHintsCopy: b(false,
			k(gui.KeyY, gui.ModSuper|gui.ModShift),
			k(gui.KeyY, gui.ModCtrlShift)),

		// --- copy mode: bare vim keys, only matched while the mode is active.
		//
		// shiftOptional is false throughout: Shift is what tells v from V,
		// g from G, and n from N, so tolerating it would make the uppercase
		// form unreachable.
		ActionCopyModeExit: b(false, k(gui.KeyEscape, 0), k(gui.KeyQ, 0)),

		// Arrows are alternates on the same action, so a user who rebinds
		// (say) .down still keeps the arrow key — the override replaces the
		// whole chord list, which is the documented behavior of KeyMap.
		ActionCopyModeLeft:  b(false, k(gui.KeyH, 0), k(gui.KeyLeft, 0)),
		ActionCopyModeDown:  b(false, k(gui.KeyJ, 0), k(gui.KeyDown, 0)),
		ActionCopyModeUp:    b(false, k(gui.KeyK, 0), k(gui.KeyUp, 0)),
		ActionCopyModeRight: b(false, k(gui.KeyL, 0), k(gui.KeyRight, 0)),

		ActionCopyModeWordFwd:  b(false, k(gui.KeyW, 0)),
		ActionCopyModeWordBack: b(false, k(gui.KeyB, 0)),

		// '0' is unshifted; '$' is Shift+4 on a US layout. Layout-dependent
		// like every other chord literal here, and rebindable.
		ActionCopyModeLineStart: b(false, k(gui.Key0, 0), k(gui.KeyHome, 0)),
		ActionCopyModeLineEnd:   b(false, k(gui.Key4, gui.ModShift), k(gui.KeyEnd, 0)),

		ActionCopyModeTop:    b(false, k(gui.KeyG, 0)),
		ActionCopyModeBottom: b(false, k(gui.KeyG, gui.ModShift)),

		ActionCopyModeHalfPageUp:   b(false, k(gui.KeyU, gui.ModCtrl)),
		ActionCopyModeHalfPageDown: b(false, k(gui.KeyD, gui.ModCtrl)),
		ActionCopyModePageUp:       b(false, k(gui.KeyPageUp, 0), k(gui.KeyB, gui.ModCtrl)),
		ActionCopyModePageDown:     b(false, k(gui.KeyPageDown, 0), k(gui.KeyF, gui.ModCtrl)),

		ActionCopyModeSelectChar: b(false, k(gui.KeyV, 0)),
		ActionCopyModeSelectLine: b(false, k(gui.KeyV, gui.ModShift)),
		ActionCopyModeYank:       b(false, k(gui.KeyY, 0), k(gui.KeyEnter, 0)),

		// '?' is Shift+/ on a US layout.
		ActionCopyModeSearchFwd:  b(false, k(gui.KeySlash, 0)),
		ActionCopyModeSearchBack: b(false, k(gui.KeySlash, gui.ModShift)),
		ActionCopyModeNextMatch:  b(false, k(gui.KeyN, 0)),
		ActionCopyModePrevMatch:  b(false, k(gui.KeyN, gui.ModShift)),

		ActionCopyModePrevMark: b(false, k(gui.KeyLeftBracket, 0)),
		ActionCopyModeNextMark: b(false, k(gui.KeyRightBracket, 0)),
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
	// Action identifies the entry so a caller can act on it — a command palette
	// needs to invoke what it lists, not just print it. A pure cheatsheet can
	// ignore this field.
	Action Action
}

// formatChords renders a chord list for display, joining alternatives with
// " / " and dropping duplicates — on Windows remapMod collapses the macOS and
// Ctrl+Shift forms of Copy onto the same combo, which should print once.
func formatChords(chords []gui.Shortcut) string {
	if len(chords) == 1 {
		return chords[0].String() // the common case: no slice, no dedupe scan
	}
	parts := make([]string, 0, len(chords))
	for _, c := range chords {
		if s := c.String(); !slices.Contains(parts, s) {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " / ")
}

// shortcutsFrom renders a binding table as display entries, walking order.
// Unbound actions are omitted — a cheatsheet line with no keys is noise, and a
// palette entry with no chord could not be invoked anyway (see RunAction).
func shortcutsFrom(tbl map[Action]binding, order ...[]Action) []ShortcutInfo {
	n := 0
	for _, o := range order {
		n += len(o)
	}
	out := make([]ShortcutInfo, 0, n)
	for _, o := range order {
		for _, a := range o {
			b, ok := tbl[a]
			if !ok || len(b.chords) == 0 {
				continue
			}
			out = append(out, ShortcutInfo{
				Label:  actionLabels[a],
				Keys:   formatChords(b.chords),
				Action: a,
			})
		}
	}
	return out
}

// Shortcuts returns the *default* Term-level keyboard shortcuts in display
// order. Embedders that let users rebind should call Term.Shortcuts instead,
// which reflects the overrides actually in effect.
//
// Workspace-level shortcuts (tabs, panes, theme) live in the workspace
// command registry and are listed separately by the help overlay.
func Shortcuts() []ShortcutInfo { return shortcutsFrom(defaultBindings(), actionOrder) }

// Shortcuts returns this terminal's effective Term-level shortcuts, including
// any overrides from Cfg.KeyBindings or SetKeyBindings, in display order.
//
// Goes through bindingTable so a Term built as a bare struct literal reports
// the defaults, matching what its key handlers would actually do.
func (t *Term) Shortcuts() []ShortcutInfo { return shortcutsFrom(t.bindingTable(), actionOrder) }

// AvailableShortcuts returns the Term-level shortcuts that would actually do
// something right now: the ordinary actions always, plus the copy-mode actions
// only while copy mode is active.
//
// This is the list a command palette should show. Shortcuts is the wrong source
// for that — it is the flat cheatsheet, and it deliberately omits the copy-mode
// keys so the help overlay does not grow by twenty rows. The mode gating lives
// here rather than behind an exported "is copy mode on" query, because whether
// an action is live is this package's business, not the embedder's.
func (t *Term) AvailableShortcuts() []ShortcutInfo {
	tbl := t.bindingTable()
	if t.copy.active {
		return shortcutsFrom(tbl, actionOrder, copyActionOrder)
	}
	return shortcutsFrom(tbl, actionOrder)
}
