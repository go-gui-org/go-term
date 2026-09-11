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

// TestMenubarCfg_HelpMenu guards falcon's only custom menu. The shortcuts
// item dispatches by ID through the command registry (see cmdToggleHelp), so
// a wrong ID leaves a dead menu item with no error; the About item relies on
// OnAction, so a nil handler would break it — and About must not also remain
// in the app menu.
func TestMenubarCfg_HelpMenu(t *testing.T) {
	a := &app{}
	cfg := a.menubarCfg(gui.NewWindow(gui.WindowCfg{}))

	// About moved to Help: the app menu must not carry one, and
	// AboutActionID stays unset (OmitAboutItem takes precedence over it).
	if !cfg.OmitAboutItem {
		t.Error("OmitAboutItem = false: About would appear in both menus")
	}
	if cfg.AboutActionID != "" {
		t.Errorf("AboutActionID = %q, want unset", cfg.AboutActionID)
	}
	// Installing a menubar replaces the backend's default, so without this
	// the app loses Cmd+W and Cmd+M.
	if !cfg.IncludeWindowMenu {
		t.Error("IncludeWindowMenu = false: Cmd+W / Cmd+M would be dropped")
	}
	if cfg.OnAction == nil {
		t.Error("OnAction is nil: the About click would go nowhere")
	}

	if len(cfg.Menus) != 1 {
		t.Fatalf("menus: got %d, want 1 (Help)", len(cfg.Menus))
	}
	help := cfg.Menus[0]
	if help.Title != "Help" {
		t.Errorf("menu title = %q, want %q", help.Title, "Help")
	}
	if len(help.Items) != 3 {
		t.Fatalf("Help items: got %d, want 3", len(help.Items))
	}
	item := help.Items[0]
	if item.ID != cmdToggleHelp {
		t.Errorf("shortcuts ID = %q, want %q", item.ID, cmdToggleHelp)
	}
	if item.CommandID != cmdToggleHelp {
		t.Errorf("shortcuts CommandID = %q, want %q",
			item.CommandID, cmdToggleHelp)
	}
	if item.Text != helpShortcutsLabel {
		t.Errorf("shortcuts Text = %q, want %q", item.Text, helpShortcutsLabel)
	}
	// The menu hint must name the chord that actually toggles the overlay.
	wantShortcut := gui.Shortcut{Key: gui.KeySlash, Modifiers: gui.ModSuper}
	if item.Shortcut != wantShortcut {
		t.Errorf("shortcuts Shortcut = %v, want %v", item.Shortcut, wantShortcut)
	}
	if !help.Items[1].Separator {
		t.Error("Help item 1 is not a separator")
	}
	about := help.Items[2]
	if about.ID != actionAbout {
		t.Errorf("about ID = %q, want %q", about.ID, actionAbout)
	}
	if about.Text == "" {
		t.Error("about Text is empty")
	}
}
