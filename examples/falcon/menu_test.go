package main

import (
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// TestAppCommands_Shape guards falcon's own window commands: About (ID,
// palette label, no shortcut) and Settings (ID, label, Cmd+, chord). The
// palette matches on the label text, so a rename that drops "About" or
// "Settings" would silently remove the entry the command panel searches for.
func TestAppCommands_Shape(t *testing.T) {
	cmds := appCommands()
	if len(cmds) != 2 {
		t.Fatalf("appCommands() = %d commands, want 2 (About, Settings)", len(cmds))
	}
	byID := make(map[string]gui.Command, len(cmds))
	for _, cmd := range cmds {
		if _, dup := byID[cmd.ID]; dup {
			t.Errorf("duplicate command ID %q", cmd.ID)
		}
		byID[cmd.ID] = cmd
		if cmd.Execute == nil {
			t.Errorf("command %q has nil Execute", cmd.ID)
		}
		// Without Global the focused pane sees the chord first and the
		// command never fires.
		if !cmd.Global {
			t.Errorf("command %q: Global = false, want true", cmd.ID)
		}
	}

	about, ok := byID[actionAbout]
	if !ok {
		t.Fatalf("no command with ID %q (About)", actionAbout)
	}
	if about.Label == "" || !strings.Contains(strings.ToLower(about.Label), "about") {
		t.Errorf("About label = %q, want it to name About", about.Label)
	}
	if about.Shortcut.IsSet() {
		t.Errorf("About shortcut = %v, want unset (palette-only)", about.Shortcut)
	}

	settings, ok := byID[cmdOpenConfig]
	if !ok {
		t.Fatalf("no command with ID %q (Settings)", cmdOpenConfig)
	}
	if settings.Label == "" || !strings.Contains(strings.ToLower(settings.Label), "settings") {
		t.Errorf("Settings label = %q, want it to name Settings", settings.Label)
	}
	want := gui.Shortcut{Key: gui.KeyComma, Modifiers: gui.ModSuper}
	if settings.Shortcut != want {
		t.Errorf("Settings shortcut = %v, want %v", settings.Shortcut, want)
	}
}

// mustRegisterAppCommands registers falcon's own window commands on w,
// failing the test on the first registration error — the same silent-drop
// hazard the register test guards.
func mustRegisterAppCommands(t *testing.T, w *gui.Window) {
	t.Helper()
	for _, cmd := range appCommands() {
		if err := w.RegisterCommand(cmd); err != nil {
			t.Fatalf("register %s: %v", cmd.ID, err)
		}
	}
}

// TestAppCommands_Register guards the Cmd+, binding. The chord can't be
// a native key equivalent (the encoder handles only A-Z/0-9), so the command
// registry is the only thing that makes it work — a silent registration
// failure would leave both the shortcut and the palette entry dead.
func TestAppCommands_Register(t *testing.T) {
	w := gui.NewWindow(gui.WindowCfg{})
	mustRegisterAppCommands(t, w)

	cmd, ok := w.CommandByID(cmdOpenConfig)
	if !ok {
		t.Fatalf("command %s not registered", cmdOpenConfig)
	}
	want := gui.Shortcut{Key: gui.KeyComma, Modifiers: gui.ModSuper}
	if cmd.Shortcut != want {
		t.Errorf("shortcut = %v, want %v", cmd.Shortcut, want)
	}
	if !cmd.Global {
		t.Error("Global = false, want true")
	}
	if cmd.Execute == nil {
		t.Error("Execute is nil")
	}
	if _, ok := w.CommandByID(actionAbout); !ok {
		t.Errorf("command %s not registered", actionAbout)
	}
}

// TestAppCommands_AboutOpensDialog drives the About palette entry end to
// end: the registered command's Execute must open the dialog, not just
// exist. The shape test catches a label or shortcut regression; only this
// one proves the entry does something.
func TestAppCommands_AboutOpensDialog(t *testing.T) {
	w := gui.NewTestWindow(gui.WindowCfg{})
	w.TestRender(func(*gui.Window) gui.View {
		return gui.Column(gui.ContainerCfg{ID: "root"})
	})
	mustRegisterAppCommands(t, w)
	cmd, ok := w.CommandByID(actionAbout)
	if !ok {
		t.Fatalf("command %s not registered", actionAbout)
	}
	cmd.Execute(&gui.Event{}, w)
	w.TestRender(nil)
	if !w.DialogIsVisible() {
		t.Fatal("About dialog did not open via the palette command")
	}
	esc := gui.Event{Type: gui.EventKeyDown, KeyCode: gui.KeyEscape}
	w.EventFn(&esc)
	w.TestRender(nil)
	if w.DialogIsVisible() {
		t.Error("About dialog still visible after Escape")
	}
}

// TestMenubarCfg_HelpMenu guards falcon's only custom menu. The shortcuts
// item dispatches by ID through the command registry (see cmdToggleHelp), so
// a wrong ID leaves a dead menu item with no error; the About item is backed
// by falcon's own window command (see appCommands) with OnAction as the
// fallback — and About must not also remain in the app menu.
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
	if about.CommandID != actionAbout {
		t.Errorf("about CommandID = %q, want %q: the menu click would miss the command registry",
			about.CommandID, actionAbout)
	}
	if about.Text == "" {
		t.Error("about Text is empty")
	}
	if about.Text != aboutLabel {
		t.Errorf("about Text = %q, want %q (the palette row)", about.Text, aboutLabel)
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
	if cfg.CustomView == nil {
		t.Fatal("CustomView is nil: the About panel has no body")
	}
	// The view builds fresh on every dialog frame, so it must hand back
	// the key holder each time — not a cached or empty subtree.
	w := gui.NewTestWindow(gui.WindowCfg{})
	view := cfg.CustomView(w)
	if view == nil {
		t.Fatal("CustomView returned nil")
	}
	layout := gui.GenerateViewLayout(view, w)
	if _, ok := layout.FindByID(aboutKeysID); !ok {
		t.Errorf("CustomView result has no %q holder: Enter would hit "+
			"a link, not dismiss", aboutKeysID)
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
