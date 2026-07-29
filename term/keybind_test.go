package term

import (
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// ev builds a key event for binding tests.
func ev(key gui.KeyCode, mods gui.Modifier) *gui.Event {
	return &gui.Event{KeyCode: key, Modifiers: mods}
}

// primary is the platform's primary shortcut modifier already remapped, i.e.
// what a real event carries: Super on macOS/Linux, Ctrl+Shift on Windows.
var primary = remapMod(gui.ModSuper)

// --- default table fidelity ---

// The labels the hand-maintained Shortcuts() literal produced before the table
// became the source of truth, plus anything added since. Order is the
// help-overlay order and is part of the contract.
//
// "Copy mode" is the only entry copy mode contributes: the in-mode vim keys
// live in copyActionOrder, which shortcutsFrom deliberately skips so the flat
// overlay doesn't grow by twenty rows.
var legacyShortcutLabels = []string{
	"Copy", "Paste", "Find", "Toggle regex (in Find)",
	"Next match (in Find)", "Previous match (in Find)",
	"Previous prompt mark", "Next prompt mark",
	"Scroll page up", "Scroll page down", "Scroll to top", "Scroll to bottom",
	"Increase font size", "Decrease font size", "Reset font size",
	"Copy mode",
}

func TestShortcuts_MatchesLegacyList(t *testing.T) {
	got := Shortcuts()
	if len(got) != len(legacyShortcutLabels) {
		t.Fatalf("Shortcuts() has %d entries, want %d", len(got), len(legacyShortcutLabels))
	}
	for i, want := range legacyShortcutLabels {
		if got[i].Label != want {
			t.Errorf("entry %d: label = %q, want %q", i, got[i].Label, want)
		}
		if got[i].Keys == "" {
			t.Errorf("entry %d (%q): empty Keys", i, want)
		}
	}
}

// Copy answers to both the primary chord and Ctrl+Shift. On Windows remapMod
// collapses them onto the same combo, which must print once, not twice.
func TestShortcuts_CopyChordsDeduped(t *testing.T) {
	var keys string
	for _, s := range Shortcuts() {
		if s.Label == "Copy" {
			keys = s.Keys
		}
	}
	if keys == "" {
		t.Fatal("no Copy entry")
	}
	wantAlt := remapMod(gui.ModSuper) != remapMod(gui.ModCtrlShift)
	if gotAlt := strings.Contains(keys, " / "); gotAlt != wantAlt {
		t.Errorf("Copy keys = %q; alternatives present = %v, want %v", keys, gotAlt, wantAlt)
	}
}

// remapMod must be applied when the table is built, not baked into literals:
// on Windows the default Copy chord is Ctrl+Shift+C, not Super+C.
func TestDefaultBindings_RemapModApplied(t *testing.T) {
	tbl := defaultBindings()
	if !tbl[ActionCopy].matches(gui.KeyC, primary) {
		t.Errorf("Copy does not match the platform primary modifier %v", primary)
	}
	if !tbl[ActionFontInc].matches(gui.KeyEqual, primary) {
		t.Errorf("FontInc does not match the platform primary modifier %v", primary)
	}
}

// --- matching semantics ---

func TestBindingMatching_Semantics(t *testing.T) {
	tbl := defaultBindings()
	cases := []struct {
		name   string
		action Action
		key    gui.KeyCode
		mods   gui.Modifier
		want   bool
	}{
		// shiftOptional: Shift is an artefact of typing '+' on the '=' key.
		{"primary+= zooms", ActionFontInc, gui.KeyEqual, primary, true},
		{"primary+Shift+= still zooms", ActionFontInc, gui.KeyEqual, primary | gui.ModShift, true},
		{"primary+Alt+= does not zoom", ActionFontInc, gui.KeyEqual, primary | gui.ModAlt, false},
		{"bare = does not zoom", ActionFontInc, gui.KeyEqual, 0, false},

		// Search direction: Shift picks the direction, so it must NOT be
		// tolerated. This is the case that rules out a blanket-lenient matcher.
		{"Enter is next match", ActionNextMatch, gui.KeyEnter, 0, true},
		{"Shift+Enter is not next match", ActionNextMatch, gui.KeyEnter, gui.ModShift, false},
		{"Shift+Enter is prev match", ActionPrevMatch, gui.KeyEnter, gui.ModShift, true},
		{"Enter is not prev match", ActionPrevMatch, gui.KeyEnter, 0, false},
		{"KPEnter is next match", ActionNextMatch, gui.KeyKPEnter, 0, true},

		// One chord covers both plain and shifted paging; the alt-screen gate
		// lives in the handler, not the table.
		{"PageUp scrolls", ActionScrollPageUp, gui.KeyPageUp, 0, true},
		{"Shift+PageUp scrolls", ActionScrollPageUp, gui.KeyPageUp, gui.ModShift, true},

		// Intentional narrowing: modifiers used to be ignored entirely here.
		{"Ctrl+PageUp passes through", ActionScrollPageUp, gui.KeyPageUp, gui.ModCtrl, false},
		{"Alt+PageUp passes through", ActionScrollPageUp, gui.KeyPageUp, gui.ModAlt, false},

		// Shift-bearing chords always match exactly, flag or not.
		{"Shift+Home scrolls to top", ActionScrollTop, gui.KeyHome, gui.ModShift, true},
		{"plain Home does not", ActionScrollTop, gui.KeyHome, 0, false},

		// Mouse-button bits must not participate: a shortcut has to keep
		// working while a button is held (e.g. PageUp mid selection-drag).
		{"PageUp during drag still scrolls", ActionScrollPageUp, gui.KeyPageUp, gui.ModLMB, true},
		{"primary+C during drag still copies", ActionCopy, gui.KeyC, primary | gui.ModRMB, true},
	}
	for _, tc := range cases {
		if got := tbl[tc.action].matches(tc.key, tc.mods); got != tc.want {
			t.Errorf("%s: matches(%v, %v) = %v, want %v", tc.name, tc.key, tc.mods, got, tc.want)
		}
	}
}

// Ctrl beyond the primary modifier is rejected. Skipped on Windows, where the
// primary modifier already contains Ctrl so the combo is bit-identical.
func TestBindingMatching_ExtraCtrlRejected(t *testing.T) {
	if primary.Has(gui.ModCtrl) {
		t.Skip("primary modifier already includes Ctrl on this platform")
	}
	tbl := defaultBindings()
	if tbl[ActionFontInc].matches(gui.KeyEqual, primary|gui.ModCtrl) {
		t.Error("primary+Ctrl+= should not zoom")
	}
	if tbl[ActionToggleRegex].matches(gui.KeyR, primary|gui.ModCtrl) {
		t.Error("primary+Ctrl+R should no longer toggle regex")
	}
}

// --- overrides ---

func TestMergeBindings_OverrideReplacesChord(t *testing.T) {
	tbl := mergeBindings(KeyMap{ActionFind: {Key: gui.KeyG, Modifiers: primary}})
	if !tbl[ActionFind].matches(gui.KeyG, primary) {
		t.Error("rebound Find should match primary+G")
	}
	if tbl[ActionFind].matches(gui.KeyF, primary) {
		t.Error("old Find chord should no longer match")
	}
	// The shiftOptional flag is a property of the action and survives rebinding.
	if !tbl[ActionFind].matches(gui.KeyG, primary|gui.ModShift) {
		t.Error("rebound Find should inherit Shift tolerance")
	}
}

func TestMergeBindings_ZeroKeyUnbinds(t *testing.T) {
	tbl := mergeBindings(KeyMap{ActionCopy: {}})
	if tbl[ActionCopy].matches(gui.KeyC, primary) {
		t.Error("unbound Copy should not match its old chord")
	}
	if len(tbl[ActionCopy].chords) != 0 {
		t.Errorf("unbound Copy has %d chords, want 0", len(tbl[ActionCopy].chords))
	}
}

func TestMergeBindings_UnknownActionIgnored(t *testing.T) {
	tbl := mergeBindings(KeyMap{Action("term.nope"): {Key: gui.KeyX}})
	if len(tbl) != len(defaultBindings()) {
		t.Errorf("unknown action changed table size: %d", len(tbl))
	}
}

// Other actions must keep their defaults when one is overridden.
func TestMergeBindings_OtherActionsUntouched(t *testing.T) {
	tbl := mergeBindings(KeyMap{ActionFind: {Key: gui.KeyG, Modifiers: primary}})
	if !tbl[ActionCopy].matches(gui.KeyC, primary) {
		t.Error("Copy default lost when Find was overridden")
	}
}

// Unbound actions are dropped from the cheatsheet rather than shown blank.
func TestShortcuts_UnboundActionOmitted(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8)}
	tm.SetKeyBindings(KeyMap{ActionCopy: {}})
	for _, s := range tm.Shortcuts() {
		if s.Label == "Copy" {
			t.Error("unbound Copy should not appear in Shortcuts()")
		}
	}
}

// --- Cfg seed vs runtime setter parity ---

func TestKeyBindings_CfgAndSetterAgree(t *testing.T) {
	km := KeyMap{
		ActionFind: {Key: gui.KeyG, Modifiers: primary},
		ActionCopy: {},
	}
	seeded := mergeBindings(km) // the path newWithPTY takes

	tm := &Term{grid: newGrid(4, 8)}
	tm.SetKeyBindings(km)

	if len(seeded) != len(tm.bindings) {
		t.Fatalf("table sizes differ: %d vs %d", len(seeded), len(tm.bindings))
	}
	for a, want := range seeded {
		got := tm.bindings[a]
		if got.shiftOptional != want.shiftOptional || len(got.chords) != len(want.chords) {
			t.Errorf("%s: %+v != %+v", a, got, want)
			continue
		}
		for i := range want.chords {
			if got.chords[i] != want.chords[i] {
				t.Errorf("%s chord %d: %v != %v", a, i, got.chords[i], want.chords[i])
			}
		}
	}
}

// A Term built as a bare struct literal must behave as default-configured,
// not as one with every shortcut disabled.
func TestBinds_ZeroValueTermUsesDefaults(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8)}
	if !tm.binds(ActionCopy, ev(gui.KeyC, primary)) {
		t.Error("zero-value Term should still match the default Copy chord")
	}
}

func TestKeyBindings_RoundTrips(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8)}
	tm.SetKeyBindings(KeyMap{ActionFind: {Key: gui.KeyG, Modifiers: primary}, ActionCopy: {}})
	km := tm.KeyBindings()

	other := &Term{grid: newGrid(4, 8)}
	other.SetKeyBindings(km)
	if !other.binds(ActionFind, ev(gui.KeyG, primary)) {
		t.Error("round-tripped Find binding lost")
	}
	if other.binds(ActionCopy, ev(gui.KeyC, primary)) {
		t.Error("round-tripped unbind lost")
	}
}

// --- handler behavior ---

// Copy with no selection: the primary chord is swallowed (it has no terminal
// encoding), but Ctrl+Shift+C must fall through so the child still gets SIGINT.
func TestHandleClipboardKey_NoSelectionPassthrough(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8)}
	e := ev(gui.KeyC, gui.ModCtrl|gui.ModShift)
	if tm.handleClipboardKey(e, nil) {
		t.Error("Ctrl+Shift+C without a selection must fall through to SIGINT")
	}
	if e.IsHandled {
		t.Error("event should not be marked handled")
	}

	if primary.Has(gui.ModCtrl) {
		return // on Windows the two chords are the same combo
	}
	e2 := ev(gui.KeyC, primary)
	if !tm.handleClipboardKey(e2, nil) {
		t.Error("primary+C without a selection should be swallowed, not sent to the pty")
	}
}

// Unbinding an intercepted key must let it reach the pty encoder.
func TestOnKeyDown_UnboundActionReachesPTY(t *testing.T) {
	tm, buf := newKeyboardTerm(24, 80)
	tm.grid.AltActive = true // so scrollback doesn't claim it either
	tm.SetKeyBindings(KeyMap{ActionScrollPageUp: {}})
	e := ev(gui.KeyPageUp, gui.ModShift)
	if tm.scrollbackIntercept(e, nil, true) {
		t.Fatal("unbound ScrollPageUp should not intercept")
	}
	out := tm.encodeKeyEvent(e, &gui.Window{}, true, false)
	*buf = append(*buf, out...)
	if string(*buf) != "\x1b[5~" {
		t.Errorf("PageUp bytes = %q, want %q", *buf, "\x1b[5~")
	}
}

// Shift+Enter must reach previous-match, not next-match: the handler tests the
// shifted binding first because both share the Enter key.
func TestHandleSearchKey_ShiftEnterSearchesBackward(t *testing.T) {
	tm, _ := newTestTermCapture()
	tm.cmd = &gui.Window{}
	putRow(tm.grid, "xhello")
	tm.search.active = true
	tm.search.query = "hello"

	// Forward first, to land on the match.
	fwd := ev(gui.KeyEnter, 0)
	if !tm.handleSearchKey(fwd, &gui.Window{}) {
		t.Fatal("Enter should be consumed while search is active")
	}

	back := ev(gui.KeyEnter, gui.ModShift)
	if !tm.handleSearchKey(back, &gui.Window{}) {
		t.Fatal("Shift+Enter should be consumed while search is active")
	}
	if tm.search.regex {
		t.Error("Shift+Enter must not toggle regex")
	}
	if tm.search.query != "hello" {
		t.Errorf("Shift+Enter must not edit the query, got %q", tm.search.query)
	}
}

// Ctrl+R toggles regex while searching; the primary+Ctrl variant no longer does.
func TestHandleSearchKey_ToggleRegex(t *testing.T) {
	tm, _ := newTestTermCapture()
	tm.cmd = &gui.Window{}
	tm.search.active = true

	if !tm.handleSearchKey(ev(gui.KeyR, gui.ModCtrl), &gui.Window{}) {
		t.Fatal("Ctrl+R should be consumed while search is active")
	}
	if !tm.search.regex {
		t.Error("Ctrl+R should have enabled regex")
	}
}
