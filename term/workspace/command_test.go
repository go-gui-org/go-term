package workspace

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// newTestWorkspace builds a Workspace wired to a bare window and an isolated
// (nonexistent) config path, so registerCommands can be exercised without a
// live GUI backend or the developer's real ~/.config file.
func newTestWorkspace(t *testing.T) *Workspace {
	t.Helper()
	return &Workspace{
		w:   &gui.Window{},
		cfg: Cfg{ConfigPath: filepath.Join(t.TempDir(), "no-such-config")},
	}
}

// TestRegisterCommands_NoDuplicateShortcuts guards the failure mode that
// silently disabled Cmd+1..9: go-gui's RegisterCommand rejects a duplicate
// shortcut, and the batch helper aborts on the first rejection, so a single
// collision drops every command declared after it.
func TestRegisterCommands_NoDuplicateShortcuts(t *testing.T) {
	ws := newTestWorkspace(t)
	ws.registerCommands()

	seenShortcut := make(map[gui.Shortcut]string, len(ws.commands))
	seenID := make(map[string]bool, len(ws.commands))
	for _, cmd := range ws.commands {
		if seenID[cmd.ID] {
			t.Errorf("duplicate command ID %q", cmd.ID)
		}
		seenID[cmd.ID] = true
		if !cmd.Shortcut.IsSet() {
			continue
		}
		if prev, dup := seenShortcut[cmd.Shortcut]; dup {
			t.Errorf("commands %q and %q share shortcut %s",
				prev, cmd.ID, cmd.Shortcut.String())
		}
		seenShortcut[cmd.Shortcut] = cmd.ID
	}
}

// TestRegisterCommands_AllReachRegistry asserts every declared command is
// actually live on the window — the registry, not the ws.commands slice, is
// what dispatches keystrokes.
func TestRegisterCommands_AllReachRegistry(t *testing.T) {
	ws := newTestWorkspace(t)
	ws.registerCommands()

	for _, cmd := range ws.commands {
		if _, ok := ws.w.CommandByID(cmd.ID); !ok {
			t.Errorf("command %q declared but not registered", cmd.ID)
		}
	}
}

// TestRegisterCommands_TabDigitsBound pins the Cmd+1..Cmd+9 tab-selection
// bindings: correct IDs, contiguous digit key codes, and the platform-remapped
// modifier.
func TestRegisterCommands_TabDigitsBound(t *testing.T) {
	ws := newTestWorkspace(t)
	ws.registerCommands()

	wantMods := remapMod(gui.ModSuper)
	for i := 0; i < 9; i++ {
		id := "workspace.tab" + strconv.Itoa(i+1)
		cmd, ok := ws.w.CommandByID(id)
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		wantKey := gui.KeyCode(uint16(gui.Key1) + uint16(i))
		if cmd.Shortcut.Key != wantKey {
			t.Errorf("%s key = %d, want %d", id, cmd.Shortcut.Key, wantKey)
		}
		if cmd.Shortcut.Modifiers != wantMods {
			t.Errorf("%s modifiers = %v, want %v", id,
				cmd.Shortcut.Modifiers, wantMods)
		}
		if !cmd.Global {
			t.Errorf("%s must be Global to outrank the focused terminal", id)
		}
	}
}

// TestRegisterCommands_TabDigitsSelectTab drives each registered Cmd+digit
// command and checks it activates the matching tab.
func TestRegisterCommands_TabDigitsSelectTab(t *testing.T) {
	ws := newTestWorkspace(t)
	ws.registerCommands()
	// Three empty tabs: activateTab only touches Terms when present, and a
	// nil-map Tab has none, so refresh/focus work stays out of the way.
	ws.tabs = []*Tab{{}, {}, {}}
	ws.activeTab = 0

	for _, want := range []int{2, 1, 0} {
		id := "workspace.tab" + strconv.Itoa(want+1)
		cmd, ok := ws.w.CommandByID(id)
		if !ok {
			t.Fatalf("%s not registered", id)
		}
		cmd.Execute(&gui.Event{}, ws.w)
		if ws.activeTab != want {
			t.Errorf("%s: activeTab = %d, want %d", id, ws.activeTab, want)
		}
	}
}

// TestDismissOverlay_ThemePickerWinsOverHelp covers the merged Escape binding:
// one command now owns both overlays, closing the topmost first.
func TestDismissOverlay_ThemePickerWinsOverHelp(t *testing.T) {
	ws := newTestWorkspace(t)
	ws.cfg.Themes = []term.NamedTheme{{Name: "test"}}
	ws.helpVisible = true
	ws.themePickerVisible = true

	ws.dismissOverlay()
	if ws.themePickerVisible {
		t.Error("theme picker still visible after first Escape")
	}
	if !ws.helpVisible {
		t.Error("help closed by the same Escape that closed the picker")
	}

	ws.dismissOverlay()
	if ws.helpVisible {
		t.Error("help still visible after second Escape")
	}
}

// TestDismissOverlay_NoOverlayOpen asserts dismissOverlay is a safe no-op
// when neither overlay is visible (Escape with nothing to dismiss).
func TestDismissOverlay_NoOverlayOpen(t *testing.T) {
	ws := newTestWorkspace(t)
	ws.helpVisible = false
	ws.themePickerVisible = false
	// Must not panic, and flags must stay false.
	ws.dismissOverlay()
	if ws.helpVisible {
		t.Error("help flag flipped from false to true")
	}
	if ws.themePickerVisible {
		t.Error("picker flag flipped from false to true")
	}
}
