package main

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// TestRegisterCommands_OpenConfig guards the Cmd+, binding. The chord can't be
// a native key equivalent (the encoder handles only A-Z/0-9), so the command
// registry is the only thing that makes it work — a silent registration
// failure would leave both the shortcut and the menu item dead.
func TestRegisterCommands_OpenConfig(t *testing.T) {
	w := gui.NewWindow(gui.WindowCfg{})
	registerCommands(w)

	cmd, ok := w.CommandByID(cmdOpenConfig)
	if !ok {
		t.Fatalf("command %s not registered", cmdOpenConfig)
	}
	want := gui.Shortcut{Key: gui.KeyComma, Modifiers: gui.ModSuper}
	if cmd.Shortcut != want {
		t.Errorf("shortcut = %v, want %v", cmd.Shortcut, want)
	}
	// Without Global the focused pane sees the chord first and the command
	// never fires.
	if !cmd.Global {
		t.Error("Global = false, want true")
	}
	if cmd.Execute == nil {
		t.Error("Execute is nil")
	}
}

// TestMenubarCfg_AboutRouted guards the one menu item falcon still owns. With
// no Menus of its own, an empty AboutActionID would silently hand About to the
// system NSAboutPanel, which reads Info.plist and so shows nothing useful in
// an unbundled build.
func TestMenubarCfg_AboutRouted(t *testing.T) {
	a := &app{}
	cfg := a.menubarCfg(gui.NewWindow(gui.WindowCfg{}))

	if cfg.AboutActionID != actionAbout {
		t.Errorf("AboutActionID = %q, want %q", cfg.AboutActionID, actionAbout)
	}
	if cfg.OmitAboutItem {
		t.Error("OmitAboutItem = true: the app menu would have no About item")
	}
	// Installing a menubar replaces the backend's default, so without this
	// the app loses Cmd+W and Cmd+M.
	if !cfg.IncludeWindowMenu {
		t.Error("IncludeWindowMenu = false: Cmd+W / Cmd+M would be dropped")
	}
	if cfg.OnAction == nil {
		t.Error("OnAction is nil: the About click would go nowhere")
	}
}
