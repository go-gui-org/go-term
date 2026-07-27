package workspace

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// ---------------------------------------------------------------------------
// parseConfig
// ---------------------------------------------------------------------------

func TestParseConfig_Empty(t *testing.T) {
	cfg, errs := parseConfig(strings.NewReader(""))
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(cfg.keybindings) != 0 {
		t.Errorf("expected empty keybindings, got %v", cfg.keybindings)
	}
}

func TestParseConfig_CommentsAndBlanks(t *testing.T) {
	input := `
# top comment

# another

[keybindings]
# inline comment
`
	cfg, errs := parseConfig(strings.NewReader(input))
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(cfg.keybindings) != 0 {
		t.Errorf("expected empty keybindings, got %v", cfg.keybindings)
	}
}

func TestParseConfig_ValidKeybindings(t *testing.T) {
	input := `
[keybindings]
splitVertical = Cmd+E
closePane     = Cmd+Shift+W
nextTab       = Ctrl+Tab
`
	cfg, errs := parseConfig(strings.NewReader(input))
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if cfg.keybindings["splitVertical"] != "Cmd+E" {
		t.Errorf("splitVertical = %q, want Cmd+E", cfg.keybindings["splitVertical"])
	}
	if cfg.keybindings["closePane"] != "Cmd+Shift+W" {
		t.Errorf("closePane = %q, want Cmd+Shift+W", cfg.keybindings["closePane"])
	}
	if cfg.keybindings["nextTab"] != "Ctrl+Tab" {
		t.Errorf("nextTab = %q, want Ctrl+Tab", cfg.keybindings["nextTab"])
	}
}

func TestParseConfig_BadLineReportsError(t *testing.T) {
	input := `
[keybindings]
splitVertical = Cmd+E
no-equals-sign
`
	_, errs := parseConfig(strings.NewReader(input))
	if len(errs) == 0 {
		t.Error("expected error for line without '=', got none")
	}
}

func TestParseConfig_MultiSection(t *testing.T) {
	// Lines in unknown sections are silently ignored.
	input := `
[unknown]
foo = bar

[keybindings]
splitVertical = Cmd+D
`
	cfg, errs := parseConfig(strings.NewReader(input))
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if _, ok := cfg.keybindings["foo"]; ok {
		t.Error("unknown section key should not appear in keybindings")
	}
	if cfg.keybindings["splitVertical"] != "Cmd+D" {
		t.Errorf("splitVertical = %q, want Cmd+D", cfg.keybindings["splitVertical"])
	}
}

// ---------------------------------------------------------------------------
// parseShortcut
// ---------------------------------------------------------------------------

func TestParseShortcut_SingleKey(t *testing.T) {
	sc, ok := parseShortcut("Tab")
	if !ok {
		t.Fatal("parseShortcut(Tab) failed")
	}
	if sc.Key != gui.KeyTab || sc.Modifiers != 0 {
		t.Errorf("got %+v, want KeyTab mods=0", sc)
	}
}

func TestParseShortcut_CmdD(t *testing.T) {
	sc, ok := parseShortcut("Cmd+D")
	if !ok {
		t.Fatal("parseShortcut(Cmd+D) failed")
	}
	if sc.Key != gui.KeyD || sc.Modifiers != gui.ModSuper {
		t.Errorf("got %+v", sc)
	}
}

func TestParseShortcut_CtrlShiftBracket(t *testing.T) {
	sc, ok := parseShortcut("Ctrl+Shift+[")
	if !ok {
		t.Fatal("parseShortcut(Ctrl+Shift+[) failed")
	}
	if sc.Key != gui.KeyLeftBracket || sc.Modifiers != gui.ModCtrl|gui.ModShift {
		t.Errorf("got %+v", sc)
	}
}

func TestParseShortcut_FKey(t *testing.T) {
	sc, ok := parseShortcut("F5")
	if !ok {
		t.Fatal("parseShortcut(F5) failed")
	}
	if sc.Key != gui.KeyF5 {
		t.Errorf("key = %v, want KeyF5 (%v)", sc.Key, gui.KeyF5)
	}
}

func TestParseShortcut_SuperAlias(t *testing.T) {
	sc1, ok1 := parseShortcut("Cmd+T")
	sc2, ok2 := parseShortcut("Super+T")
	if !ok1 || !ok2 {
		t.Fatal("parse failed")
	}
	if sc1 != sc2 {
		t.Errorf("Cmd+T %+v != Super+T %+v", sc1, sc2)
	}
}

func TestParseShortcut_InvalidModifier(t *testing.T) {
	if _, ok := parseShortcut("Win+D"); ok {
		t.Error("expected failure for unknown modifier Win")
	}
}

func TestParseShortcut_InvalidKey(t *testing.T) {
	if _, ok := parseShortcut("Cmd+XYZ"); ok {
		t.Error("expected failure for unknown key XYZ")
	}
}

func TestParseShortcut_Empty(t *testing.T) {
	if _, ok := parseShortcut(""); ok {
		t.Error("expected failure for empty string")
	}
}

func TestParseShortcut_CaseInsensitiveModifier(t *testing.T) {
	sc, ok := parseShortcut("cmd+shift+d")
	if !ok {
		t.Fatal("parseShortcut(cmd+shift+d) failed")
	}
	if sc.Modifiers != gui.ModSuper|gui.ModShift {
		t.Errorf("modifiers = %v, want ModSuper|ModShift", sc.Modifiers)
	}
}

// ---------------------------------------------------------------------------
// applyKeybindingOverrides
// ---------------------------------------------------------------------------

func sampleCmds() []gui.Command {
	return []gui.Command{
		{ID: "workspace.splitVertical", Shortcut: gui.Shortcut{Key: gui.KeyD, Modifiers: gui.ModSuper}},
		{ID: "workspace.closePane", Shortcut: gui.Shortcut{Key: gui.KeyW, Modifiers: gui.ModSuper | gui.ModShift}},
		{ID: "workspace.newTab", Shortcut: gui.Shortcut{Key: gui.KeyT, Modifiers: gui.ModSuper}},
	}
}

func TestApplyKeybindingOverrides_ValidOverride(t *testing.T) {
	cmds := sampleCmds()
	applyKeybindingOverrides(cmds, map[string]string{
		"splitVertical": "Cmd+E",
	})
	if cmds[0].Shortcut.Key != gui.KeyE {
		t.Errorf("splitVertical key = %v, want KeyE", cmds[0].Shortcut.Key)
	}
}

func TestApplyKeybindingOverrides_UnknownCommand(t *testing.T) {
	cmds := sampleCmds()
	orig := cmds[0].Shortcut
	applyKeybindingOverrides(cmds, map[string]string{
		"nonexistentCommand": "Cmd+X",
	})
	if cmds[0].Shortcut != orig {
		t.Error("unknown command should not affect existing shortcuts")
	}
}

func TestApplyKeybindingOverrides_BadChord(t *testing.T) {
	cmds := sampleCmds()
	orig := cmds[0].Shortcut
	applyKeybindingOverrides(cmds, map[string]string{
		"splitVertical": "Win+XYZ",
	})
	if cmds[0].Shortcut != orig {
		t.Error("bad chord should keep the default shortcut")
	}
}

func TestApplyKeybindingOverrides_Collision(t *testing.T) {
	cmds := sampleCmds()
	orig := cmds[0].Shortcut
	// Try to assign Cmd+T (already used by newTab) to splitVertical.
	applyKeybindingOverrides(cmds, map[string]string{
		"splitVertical": "Cmd+T",
	})
	if cmds[0].Shortcut != orig {
		t.Error("collision should keep the default shortcut")
	}
}

func TestApplyKeybindingOverrides_EmptyMap(t *testing.T) {
	cmds := sampleCmds()
	orig := make([]gui.Shortcut, len(cmds))
	for i, c := range cmds {
		orig[i] = c.Shortcut
	}
	applyKeybindingOverrides(cmds, nil)
	for i, c := range cmds {
		if c.Shortcut != orig[i] {
			t.Errorf("cmd[%d] shortcut changed unexpectedly", i)
		}
	}
}

func TestApplyKeybindingOverrides_NilCmds(t *testing.T) {
	// Should not panic with nil cmds slice.
	applyKeybindingOverrides(nil, map[string]string{"splitVertical": "Cmd+E"})
}

// TestApplyKeybindingOverrides_CollisionIsDeterministic pins the reason
// applyWorkspaceBindings sorts its keys. Two entries claim the same chord, so
// exactly one must win — and it must be the *same* one on every run. Ranging
// over the map directly picked the winner (and logged the loser) at random.
func TestApplyKeybindingOverrides_CollisionIsDeterministic(t *testing.T) {
	kb := map[string]string{
		"closePane":     "Cmd+E",
		"splitVertical": "Cmd+E",
	}
	var first []gui.Shortcut
	// Enough iterations that random map order would almost certainly differ.
	for i := 0; i < 50; i++ {
		cmds := sampleCmds()
		applyKeybindingOverrides(cmds, kb)
		got := make([]gui.Shortcut, len(cmds))
		for j, c := range cmds {
			got[j] = c.Shortcut
		}
		if first == nil {
			first = got
			continue
		}
		if !slices.Equal(got, first) {
			t.Fatalf("run %d differs: %v vs %v", i, got, first)
		}
	}
	// "closePane" sorts before "splitVertical", so it takes the chord and
	// splitVertical keeps its default.
	if first[1].Key != gui.KeyE {
		t.Errorf("closePane key = %v, want KeyE (first in sort order wins)", first[1].Key)
	}
	if first[0] != (gui.Shortcut{Key: gui.KeyD, Modifiers: gui.ModSuper}) {
		t.Errorf("splitVertical = %v, want its default (lost the collision)", first[0])
	}
}

// TestSetFont_RejectsNonFinite guards the hole that let a NaN font size reach
// the widget: NaN fails every range comparison, and Term.style()'s clamp was
// gated on "Size > 0", which NaN also fails — so it passed through unclamped
// into text measurement.
func TestSetFont_RejectsNonFinite(t *testing.T) {
	for _, val := range []string{"nan", "NaN", "+Inf", "inf"} {
		var c workspaceConfig
		if err := c.setFont("size", val); err == nil {
			t.Errorf("setFont(size, %q) accepted, want error", val)
		}
		if c.hasFontSize {
			t.Errorf("setFont(size, %q) set hasFontSize", val)
		}
	}
}

// TestSetGeneral_RejectsNonFiniteScrollbar is the scrollbar half of the same
// hole. -Inf is legal input in the sense that any negative value hides the
// scrollbar, but a non-finite one still defeats the equality early-out in
// Term.SetScrollbarWidth, so it is rejected too.
func TestSetGeneral_RejectsNonFiniteScrollbar(t *testing.T) {
	for _, val := range []string{"nan", "NaN", "+Inf", "-Inf"} {
		var c workspaceConfig
		if err := c.setGeneral("scrollbar", val); err == nil {
			t.Errorf("setGeneral(scrollbar, %q) accepted, want error", val)
		}
		if c.hasScrollbar {
			t.Errorf("setGeneral(scrollbar, %q) set hasScrollbar", val)
		}
	}
}

// ---------------------------------------------------------------------------
// parseConfig hardening bounds
// ---------------------------------------------------------------------------

func TestParseConfig_KeyTooLong(t *testing.T) {
	key := strings.Repeat("a", maxKeyLen+1)
	input := "[keybindings]\n" + key + " = Cmd+D\n"
	_, errs := parseConfig(strings.NewReader(input))
	if len(errs) == 0 {
		t.Error("expected error for key longer than maxKeyLen, got none")
	}
}

func TestParseConfig_ValTooLong(t *testing.T) {
	val := strings.Repeat("x", maxValLen+1)
	input := "[keybindings]\nsplitVertical = " + val + "\n"
	_, errs := parseConfig(strings.NewReader(input))
	if len(errs) == 0 {
		t.Error("expected error for value longer than maxValLen, got none")
	}
}

func TestParseConfig_KeybindingLimitReached(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("[keybindings]\n")
	for i := 0; i <= maxKeybindings; i++ {
		fmt.Fprintf(&sb, "key%d = Cmd+A\n", i)
	}
	cfg, errs := parseConfig(strings.NewReader(sb.String()))
	if len(errs) == 0 {
		t.Error("expected error when keybinding limit is exceeded, got none")
	}
	if len(cfg.keybindings) > maxKeybindings {
		t.Errorf("keybindings count %d exceeds limit %d", len(cfg.keybindings), maxKeybindings)
	}
}

// ---------------------------------------------------------------------------
// parseShortcut hardening bounds
// ---------------------------------------------------------------------------

func TestParseShortcut_TooLong(t *testing.T) {
	long := strings.Repeat("Cmd+", maxShortcutLen/4+1)
	if _, ok := parseShortcut(long); ok {
		t.Errorf("expected failure for shortcut string of length %d (limit %d)", len(long), maxShortcutLen)
	}
}

func TestParseShortcut_OnlyPlus(t *testing.T) {
	if _, ok := parseShortcut("+"); ok {
		t.Error("expected failure for bare '+' shortcut")
	}
}

// ---------------------------------------------------------------------------
// [font] / [general] sections
// ---------------------------------------------------------------------------

func TestParseConfig_FontAndGeneral(t *testing.T) {
	input := `
[font]
family = JetBrainsMono NFM
size   = 14.5

[general]
theme      = dracula
scrollback = 12000
bell       = visual
scrollbar  = 6
`
	cfg, errs := parseConfig(strings.NewReader(input))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if cfg.fontFamily != "JetBrainsMono NFM" {
		t.Errorf("fontFamily = %q", cfg.fontFamily)
	}
	if !cfg.hasFontSize || cfg.fontSize != 14.5 {
		t.Errorf("fontSize = %v (has=%v), want 14.5", cfg.fontSize, cfg.hasFontSize)
	}
	if cfg.theme != "dracula" {
		t.Errorf("theme = %q", cfg.theme)
	}
	if !cfg.hasScrollback || cfg.scrollback != 12000 {
		t.Errorf("scrollback = %v (has=%v)", cfg.scrollback, cfg.hasScrollback)
	}
	if !cfg.hasBell || cfg.bell != term.BellVisual {
		t.Errorf("bell = %v (has=%v)", cfg.bell, cfg.hasBell)
	}
	if !cfg.hasScrollbar || cfg.scrollbar != 6 {
		t.Errorf("scrollbar = %v (has=%v)", cfg.scrollbar, cfg.hasScrollbar)
	}
}

// TestParseConfig_UnsetKeysHaveNoFlag pins the distinction reload depends on:
// a key absent from the file must not look like a key set to zero.
func TestParseConfig_UnsetKeysHaveNoFlag(t *testing.T) {
	cfg, errs := parseConfig(strings.NewReader("[general]\ntheme = nord\n"))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if cfg.hasScrollback || cfg.hasBell || cfg.hasScrollbar || cfg.hasFontSize {
		t.Errorf("absent keys marked present: %+v", cfg)
	}
}

// TestParseConfig_NegativeValuesAccepted covers the two settings whose
// negative form is meaningful rather than malformed.
func TestParseConfig_NegativeValuesAccepted(t *testing.T) {
	cfg, errs := parseConfig(strings.NewReader(
		"[general]\nscrollback = -1\nscrollbar = -1\n"))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !cfg.hasScrollback || cfg.scrollback != -1 {
		t.Errorf("scrollback = %v, want -1 (scrollback disabled)", cfg.scrollback)
	}
	if !cfg.hasScrollbar || cfg.scrollbar != -1 {
		t.Errorf("scrollbar = %v, want -1 (scrollbar hidden)", cfg.scrollbar)
	}
}

// TestParseConfig_MalformedValuesKeepValidLines is the forgiving-parser
// contract: one bad line is reported but does not discard its neighbours.
func TestParseConfig_MalformedValuesKeepValidLines(t *testing.T) {
	input := `
[font]
size   = huge
family = Menlo

[general]
scrollback = lots
bell       = klaxon
scrollbar  = 1e9
theme      = nord
`
	cfg, errs := parseConfig(strings.NewReader(input))
	if len(errs) != 4 {
		t.Errorf("errors = %d (%v), want 4", len(errs), errs)
	}
	if cfg.fontFamily != "Menlo" {
		t.Errorf("fontFamily = %q, want Menlo", cfg.fontFamily)
	}
	if cfg.theme != "nord" {
		t.Errorf("theme = %q, want nord", cfg.theme)
	}
	if cfg.hasFontSize || cfg.hasScrollback || cfg.hasBell || cfg.hasScrollbar {
		t.Errorf("malformed value was accepted: %+v", cfg)
	}
}

func TestParseConfig_BellModes(t *testing.T) {
	for in, want := range map[string]term.BellMode{
		"auto":    term.BellAuto,
		"AUDIBLE": term.BellAudible,
		"visual":  term.BellVisual,
		"both":    term.BellBoth,
		"none":    term.BellNone,
		"off":     term.BellNone,
	} {
		cfg, errs := parseConfig(strings.NewReader("[general]\nbell = " + in + "\n"))
		if len(errs) != 0 {
			t.Errorf("bell = %s: %v", in, errs)
			continue
		}
		if cfg.bell != want {
			t.Errorf("bell = %s: got %v, want %v", in, cfg.bell, want)
		}
	}
}

func TestParseConfig_FontSizeOutOfRange(t *testing.T) {
	for _, val := range []string{"0", "-3", "10000"} {
		cfg, errs := parseConfig(strings.NewReader("[font]\nsize = " + val + "\n"))
		if len(errs) != 1 {
			t.Errorf("size = %s: errors = %v, want 1", val, errs)
		}
		if cfg.hasFontSize {
			t.Errorf("size = %s: accepted out-of-range value", val)
		}
	}
}

// ---------------------------------------------------------------------------
// applySettings
// ---------------------------------------------------------------------------

func testThemes() []term.NamedTheme {
	return []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Dracula", Theme: term.DraculaTheme},
	}
}

func TestApplySettings_OverridesBase(t *testing.T) {
	base := Cfg{
		TextStyle: gui.TextStyle{Family: "Menlo", Size: 12},
		Themes:    testThemes(),
	}
	fc, _ := parseConfig(strings.NewReader(`
[font]
family = Iosevka
size   = 16

[general]
theme      = dracula
scrollback = 999
bell       = none
scrollbar  = 8
`))
	got := applySettings(base, fc, nil)
	if got.TextStyle.Family != "Iosevka" || got.TextStyle.Size != 16 {
		t.Errorf("TextStyle = %+v", got.TextStyle)
	}
	if got.opts.scrollback != 999 || got.opts.bell != term.BellNone || got.opts.scrollbar != 8 {
		t.Errorf("opts = %+v", got.opts)
	}
	if got.opts.theme == nil || *got.opts.theme != term.DraculaTheme {
		t.Errorf("theme = %v, want Dracula (matched case-insensitively)", got.opts.theme)
	}
	if base.TextStyle.Family != "Menlo" {
		t.Error("applySettings mutated the base Cfg")
	}
}

func TestApplySettings_EmptyConfigKeepsBase(t *testing.T) {
	base := Cfg{
		TextStyle: gui.TextStyle{Family: "Menlo", Size: 12},
		Themes:    testThemes(),
	}
	got := applySettings(base, workspaceConfig{}, nil)
	if got.TextStyle != base.TextStyle {
		t.Errorf("TextStyle = %+v, want %+v", got.TextStyle, base.TextStyle)
	}
	if got.opts.theme != nil {
		t.Error("no [general] theme should leave the pane on term's default")
	}
	if got.opts.scrollback != 0 || got.opts.scrollbar != 0 ||
		got.opts.bell != term.BellAuto || len(got.opts.keys) != 0 {
		t.Errorf("opts = %+v, want zero", got.opts)
	}
}

func TestApplySettings_UnknownThemeKeepsDefault(t *testing.T) {
	base := Cfg{Themes: testThemes()}
	got := applySettings(base, workspaceConfig{theme: "no-such-theme"}, nil)
	if got.opts.theme != nil {
		t.Error("unknown theme name should not be applied")
	}
}

// ---------------------------------------------------------------------------
// term.* / workspace.* keybinding routing
// ---------------------------------------------------------------------------

func TestApplyKeybindingOverrides_RoutesByNamespace(t *testing.T) {
	cmds := sampleCmds()
	km := applyKeybindingOverrides(cmds, map[string]string{
		"workspace.splitVertical": "Cmd+E",
		"term.find":               "Cmd+G",
	})
	if cmds[0].Shortcut.Key != gui.KeyE {
		t.Errorf("splitVertical key = %v, want KeyE", cmds[0].Shortcut.Key)
	}
	want := gui.Shortcut{Key: gui.KeyG, Modifiers: gui.ModSuper}
	if km[term.ActionFind] != want {
		t.Errorf("term.find = %+v, want %+v", km[term.ActionFind], want)
	}
	if len(km) != 1 {
		t.Errorf("KeyMap = %+v, want only term.find", km)
	}
}

// TestApplyKeybindingOverrides_BareKeyIsWorkspace pins the back-compat rule:
// an unprefixed key still names a workspace command.
func TestApplyKeybindingOverrides_BareKeyIsWorkspace(t *testing.T) {
	cmds := sampleCmds()
	km := applyKeybindingOverrides(cmds, map[string]string{"splitVertical": "Cmd+E"})
	if cmds[0].Shortcut.Key != gui.KeyE {
		t.Errorf("splitVertical key = %v, want KeyE", cmds[0].Shortcut.Key)
	}
	if len(km) != 0 {
		t.Errorf("KeyMap = %+v, want empty", km)
	}
}

func TestApplyKeybindingOverrides_UnknownTermAction(t *testing.T) {
	km := applyKeybindingOverrides(sampleCmds(), map[string]string{
		"term.nonexistent": "Cmd+X",
	})
	if len(km) != 0 {
		t.Errorf("KeyMap = %+v, want empty", km)
	}
}

// TestApplyKeybindingOverrides_CrossNamespaceCollision covers the silent
// failure this guards against: go-gui's global commands outrank the widget's
// onKeyDown, so a term binding shadowed by a workspace command would never
// fire. The term binding must be rejected rather than registered.
func TestApplyKeybindingOverrides_CrossNamespaceCollision(t *testing.T) {
	cmds := sampleCmds() // workspace.newTab owns Cmd+T
	km := applyKeybindingOverrides(cmds, map[string]string{"term.find": "Cmd+T"})
	if _, ok := km[term.ActionFind]; ok {
		t.Error("term.find took a chord already owned by workspace.newTab")
	}
}

// TestApplyKeybindingOverrides_CollidesWithRemappedWorkspace checks the
// collision is judged against the *post-override* workspace shortcuts, not the
// built-in ones: Cmd+D is free once splitVertical moves off it.
func TestApplyKeybindingOverrides_CollidesWithRemappedWorkspace(t *testing.T) {
	cmds := sampleCmds()
	km := applyKeybindingOverrides(cmds, map[string]string{
		"workspace.splitVertical": "Cmd+E",
		"term.find":               "Cmd+D",
	})
	want := gui.Shortcut{Key: gui.KeyD, Modifiers: gui.ModSuper}
	if km[term.ActionFind] != want {
		t.Errorf("term.find = %+v, want %+v (Cmd+D was vacated)", km[term.ActionFind], want)
	}
}

func TestApplyKeybindingOverrides_TermVsTermCollision(t *testing.T) {
	km := applyKeybindingOverrides(sampleCmds(), map[string]string{
		"term.copy": "Cmd+J",
		"term.find": "Cmd+J",
	})
	if len(km) != 1 {
		t.Errorf("KeyMap = %+v, want exactly one winner", km)
	}
	// Entries are resolved in sorted order, so term.copy wins deterministically.
	if _, ok := km[term.ActionCopy]; !ok {
		t.Error("term.copy should win the chord it claimed first")
	}
}

func TestApplyKeybindingOverrides_Unbind(t *testing.T) {
	cmds := sampleCmds()
	km := applyKeybindingOverrides(cmds, map[string]string{
		"term.find":           "none",
		"workspace.closePane": "none",
	})
	sc, ok := km[term.ActionFind]
	if !ok || sc.Key != gui.KeyInvalid {
		t.Errorf("term.find = %+v (ok=%v), want the unbind sentinel", sc, ok)
	}
	if cmds[1].Shortcut.IsSet() {
		t.Errorf("workspace.closePane = %+v, want unbound", cmds[1].Shortcut)
	}
}
