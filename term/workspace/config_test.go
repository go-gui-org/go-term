package workspace

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
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
