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

// TestAboutDialogKeyboardContract locks in the About panel's dismissal rules:
// no OK button and no default button, with the focus parked on the invisible
// key holder so Enter dismisses instead of opening a link.
func TestAboutDialogKeyboardContract(t *testing.T) {
	cfg := aboutDialogCfg(gui.CurrentTheme())

	if cfg.DialogType != gui.DialogCustom {
		t.Errorf("DialogType = %v, want DialogCustom", cfg.DialogType)
	}
	if cfg.FocusID != aboutKeysID {
		t.Errorf("FocusID = %q, want %q: Enter would hit a link, not dismiss",
			cfg.FocusID, aboutKeysID)
	}
	// A confirm-style default would highlight a target the panel doesn't
	// have; it reports, it does not ask. OnCancelNo is set, but only as
	// go-gui's Escape notification — see aboutDialogCfg.
	if cfg.OnOkYes != nil {
		t.Error("About dialog set OnOkYes; it has no confirm button")
	}
	if cfg.OnCancelNo == nil {
		t.Error("OnCancelNo is nil: Escape would leave the hook installed")
	}
	if len(cfg.CustomContent) != 1 {
		t.Errorf("CustomContent has %d views, want 1 (the key holder)",
			len(cfg.CustomContent))
	}
}

// TestAboutDialogDismissal drives the real dismissal paths through a headless
// window: the panel has no button to click, so Escape and Enter are the whole
// keyboard contract and a regression in either leaves the dialog stuck.
func TestAboutDialogDismissal(t *testing.T) {
	for _, key := range []struct {
		name string
		code gui.KeyCode
	}{
		{"escape", gui.KeyEscape},
		{"enter", gui.KeyEnter},
		{"keypad enter", gui.KeyKPEnter},
	} {
		t.Run(key.name, func(t *testing.T) {
			w := gui.NewTestWindow(gui.WindowCfg{})
			w.TestRender(func(*gui.Window) gui.View {
				return gui.Column(gui.ContainerCfg{ID: "root"})
			})
			showAbout(w)
			w.TestRender(nil)
			if !w.DialogIsVisible() {
				t.Fatal("About dialog did not open")
			}
			// The event goes straight to the window rather than
			// through TestKey: the dialog is a floating layer, and
			// opening it already parked focus on FocusID, so there
			// is nothing to focus by ID first.
			down := gui.Event{Type: gui.EventKeyDown, KeyCode: key.code}
			w.EventFn(&down)
			w.TestRender(nil)
			if w.DialogIsVisible() {
				t.Errorf("dialog still visible after %s", key.name)
			}
		})
	}
}

// TestAboutDialogClickOutside covers the third dismissal path: a click on the
// terminal behind the panel closes it. The panel has no OK button, so a user
// who reaches for the mouse has nothing else to aim at.
func TestAboutDialogClickOutside(t *testing.T) {
	w := gui.NewTestWindow(gui.WindowCfg{})
	w.TestRender(func(*gui.Window) gui.View {
		return gui.Column(gui.ContainerCfg{ID: "root"})
	})
	showAbout(w)
	w.TestRender(nil)
	if !w.DialogIsVisible() {
		t.Fatal("About dialog did not open")
	}
	// Top-left corner: outside the centered panel at any window size.
	click := gui.Event{
		Type: gui.EventMouseDown, MouseX: 5, MouseY: 5,
		MouseButton: gui.MouseLeft,
	}
	w.EventFn(&click)
	w.TestRender(nil)
	if w.DialogIsVisible() {
		t.Error("dialog still visible after a click outside it")
	}
	if aboutHookInstalled {
		t.Error("outside-click hook stayed installed after dismissal")
	}
}

// TestAboutDialogHookRetires guards the hook's other exit: a key dismissal
// must unwrap it there and then. A hook that never retired would wrap itself
// on the next open and stay in the terminal's event path for good.
func TestAboutDialogHookRetires(t *testing.T) {
	w := gui.NewTestWindow(gui.WindowCfg{})
	w.TestRender(func(*gui.Window) gui.View {
		return gui.Column(gui.ContainerCfg{ID: "root"})
	})
	showAbout(w)
	w.TestRender(nil)
	esc := gui.Event{Type: gui.EventKeyDown, KeyCode: gui.KeyEscape}
	w.EventFn(&esc)
	w.TestRender(nil)
	if aboutHookInstalled {
		t.Error("hook still installed after the dialog closed")
	}
	// Reopening must work, and must not chain the hook to itself.
	showAbout(w)
	w.TestRender(nil)
	if !w.DialogIsVisible() {
		t.Fatal("About dialog did not reopen")
	}
	click := gui.Event{
		Type: gui.EventMouseDown, MouseX: 5, MouseY: 5,
		MouseButton: gui.MouseLeft,
	}
	w.EventFn(&click)
	w.TestRender(nil)
	if w.DialogIsVisible() {
		t.Error("reopened dialog did not close on an outside click")
	}
}

// TestShortCommitRev covers the Commit row's label rule: a full hash shows
// abbreviated to commitLen, a dirty tree gains the suffix, and an empty
// revision stays empty so no "Commit -dirty" row can render.
func TestShortCommitRev(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name  string
		rev   string
		dirty bool
		want  string
	}{
		{"full hash truncates", full, false, full[:commitLen]},
		{"dirty suffix keeps the link base", full, true, full[:commitLen] + "-dirty"},
		{"short hash passes through", "abc1234", false, "abc1234"},
		{"empty stays empty", "", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortCommitRev(tt.rev, tt.dirty); got != tt.want {
				t.Errorf("shortCommitRev(%q, %v) = %q, want %q",
					tt.rev, tt.dirty, got, tt.want)
			}
		})
	}
}
