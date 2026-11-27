package main

import (
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// TestAppCommands_Shape guards falcon's own window command: Settings (ID,
// palette label, Cmd+, chord). The palette matches on the label text, so a
// rename that drops "Settings" would silently remove the entry the command
// panel searches for.
func TestAppCommands_Shape(t *testing.T) {
	cmds := appCommands()
	if len(cmds) != 1 {
		t.Fatalf("appCommands() = %d commands, want 1 (Settings)", len(cmds))
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
}

// TestMenubarCfg_HelpMenu guards falcon's only custom menu. The shortcuts
// item dispatches by ID through the command registry (see cmdToggleHelp), so
// a wrong ID leaves a dead menu item with no error. There is no About item:
// the version lives on the help page below the shortcut columns.
func TestMenubarCfg_HelpMenu(t *testing.T) {
	a := &app{}
	cfg := a.menubarCfg(gui.NewWindow(gui.WindowCfg{}))

	// No About item anywhere: the version lives on the help page, and the
	// system panel would show the wrong name and no version.
	// AboutActionID stays unset (OmitAboutItem takes precedence over it).
	if !cfg.OmitAboutItem {
		t.Error("OmitAboutItem = false: an About item would appear in the app menu")
	}
	if cfg.AboutActionID != "" {
		t.Errorf("AboutActionID = %q, want unset", cfg.AboutActionID)
	}
	// Installing a menubar replaces the backend's default, so without this
	// the app loses Cmd+W and Cmd+M.
	if !cfg.IncludeWindowMenu {
		t.Error("IncludeWindowMenu = false: Cmd+W / Cmd+M would be dropped")
	}

	if len(cfg.Menus) != 1 {
		t.Fatalf("menus: got %d, want 1 (Help)", len(cfg.Menus))
	}
	help := cfg.Menus[0]
	if help.Title != "Help" {
		t.Errorf("menu title = %q, want %q", help.Title, "Help")
	}
	if len(help.Items) != 1 {
		t.Fatalf("Help items: got %d, want 1", len(help.Items))
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
}

// TestAboutInfo_NamesVersion guards the about lines shown below the help
// page's shortcut columns: the first line names the app and its version on
// every build path, and later lines only add build metadata.
func TestAboutInfo_NamesVersion(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "v9.9.9"
	lines := aboutInfo()
	if len(lines) == 0 {
		t.Fatal("aboutInfo() is empty")
	}
	if want := appName + " v9.9.9"; lines[0] != want {
		t.Errorf("aboutInfo()[0] = %q, want %q", lines[0], want)
	}

	version = ""
	lines = aboutInfo()
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("aboutInfo() names no version on an unstamped build")
	}
	if !strings.Contains(lines[0], appName) {
		t.Errorf("aboutInfo()[0] = %q, want it to name %q", lines[0], appName)
	}
	if !strings.Contains(lines[0], releaseVersion) && !strings.Contains(lines[0], "v") {
		t.Errorf("aboutInfo()[0] = %q, want a release-naming version", lines[0])
	}
}

// TestShortCommitRev covers the Commit line's label rule: a full hash shows
// abbreviated to commitLen, a dirty tree gains the suffix, and an empty
// revision stays empty so no "Commit -dirty" line can render.
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
