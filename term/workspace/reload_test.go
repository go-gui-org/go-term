package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// newConfiguredWorkspace builds a paneless Workspace pointed at a config file
// under a temp dir, and returns it plus the path so a test can rewrite the
// file and reload. Paneless because building a real pane needs a pty and a
// live GUI backend; everything reload resolves — settings, command shortcuts,
// term keybindings — is decided before panes are touched.
func newConfiguredWorkspace(t *testing.T, body string) (*Workspace, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	writeConfig(t, path, body)
	base := Cfg{
		ConfigPath: path,
		TextStyle:  gui.TextStyle{Family: "Menlo", Size: 12},
		Themes: []term.NamedTheme{
			{Name: "Default", Theme: term.DefaultTheme},
			{Name: "Dracula", Theme: term.DraculaTheme},
		},
	}
	ws := &Workspace{w: &gui.Window{}, cfg: base, baseCfg: base}
	ws.loadAndApplyConfig()
	return ws, path
}

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestLoadAndApplyConfig_AppliesFileSettings(t *testing.T) {
	ws, _ := newConfiguredWorkspace(t, `
[font]
family = Iosevka
size   = 15

[general]
theme      = Dracula
scrollback = 777
bell       = visual
scrollbar  = 3

[keybindings]
workspace.splitVertical = Cmd+E
term.find               = Cmd+G
`)
	if ws.cfg.TextStyle.Family != "Iosevka" || ws.cfg.TextStyle.Size != 15 {
		t.Errorf("TextStyle = %+v", ws.cfg.TextStyle)
	}
	if ws.cfg.opts.scrollback != 777 || ws.cfg.opts.bell != term.BellVisual ||
		ws.cfg.opts.scrollbar != 3 {
		t.Errorf("opts = %+v", ws.cfg.opts)
	}
	if ws.cfg.opts.theme == nil || *ws.cfg.opts.theme != term.DraculaTheme {
		t.Error("theme not resolved to Dracula")
	}
	if got := ws.cfg.opts.keys[term.ActionFind]; got.Key != gui.KeyG {
		t.Errorf("term.find = %+v, want Cmd+G", got)
	}
	cmd, ok := ws.w.CommandByID("workspace.splitVertical")
	if !ok {
		t.Fatal("workspace.splitVertical not registered")
	}
	if cmd.Shortcut.Key != gui.KeyE {
		t.Errorf("splitVertical = %v, want KeyE", cmd.Shortcut.Key)
	}
}

// TestReloadConfig_PicksUpChanges is the core reload contract: a rewritten
// file changes the effective settings and re-registers the command table.
func TestReloadConfig_PicksUpChanges(t *testing.T) {
	ws, path := newConfiguredWorkspace(t, "[font]\nsize = 15\n")
	writeConfig(t, path, `
[font]
size = 18

[keybindings]
workspace.splitVertical = Cmd+E
`)
	ws.ReloadConfig()

	if ws.cfg.TextStyle.Size != 18 {
		t.Errorf("size = %v, want 18", ws.cfg.TextStyle.Size)
	}
	cmd, ok := ws.w.CommandByID("workspace.splitVertical")
	if !ok {
		t.Fatal("workspace.splitVertical missing after reload")
	}
	if cmd.Shortcut.Key != gui.KeyE {
		t.Errorf("splitVertical = %v, want KeyE after reload", cmd.Shortcut.Key)
	}
}

// TestReloadConfig_RemovedKeysRevertToBase pins why the pristine Cfg is kept:
// deleting a line must restore the embedder's default, not leave the
// last-loaded value applied.
func TestReloadConfig_RemovedKeysRevertToBase(t *testing.T) {
	ws, path := newConfiguredWorkspace(t, `
[font]
family = Iosevka
size   = 15

[general]
theme      = Dracula
scrollback = 777

[keybindings]
workspace.splitVertical = Cmd+E
`)
	writeConfig(t, path, "")
	ws.ReloadConfig()

	if ws.cfg.TextStyle != (gui.TextStyle{Family: "Menlo", Size: 12}) {
		t.Errorf("TextStyle = %+v, want the embedder's Menlo/12", ws.cfg.TextStyle)
	}
	if ws.cfg.opts.scrollback != 0 {
		t.Errorf("scrollback = %d, want 0 (term's default)", ws.cfg.opts.scrollback)
	}
	if ws.cfg.opts.theme != nil {
		t.Error("theme override survived its removal from the config")
	}
	if len(ws.cfg.opts.keys) != 0 {
		t.Errorf("keys = %+v, want empty", ws.cfg.opts.keys)
	}
	cmd, ok := ws.w.CommandByID("workspace.splitVertical")
	if !ok {
		t.Fatal("workspace.splitVertical missing after reload")
	}
	if cmd.Shortcut.Key != gui.KeyD {
		t.Errorf("splitVertical = %v, want the default KeyD", cmd.Shortcut.Key)
	}
}

// TestReloadConfig_BrokenFileKeepsRunning covers the "must never wedge the
// app" rule: a file full of garbage leaves the workspace on its defaults with
// a fully registered command table.
func TestReloadConfig_BrokenFileKeepsRunning(t *testing.T) {
	ws, path := newConfiguredWorkspace(t, "[font]\nsize = 15\n")
	writeConfig(t, path, "not an ini\n[font\nsize = = =\n[general]\nbell = klaxon\n")
	ws.ReloadConfig()

	if ws.cfg.TextStyle.Size != 12 {
		t.Errorf("size = %v, want the embedder's 12", ws.cfg.TextStyle.Size)
	}
	if ws.cfg.opts.bell != term.BellAuto {
		t.Errorf("bell = %v, want the default", ws.cfg.opts.bell)
	}
	for _, cmd := range ws.commands {
		if _, ok := ws.w.CommandByID(cmd.ID); !ok {
			t.Errorf("command %q not registered after a broken reload", cmd.ID)
		}
	}
}

// TestReloadConfig_MissingFileIsFine — the default install has no config file
// at all, so a missing one is not an error path.
func TestReloadConfig_MissingFileIsFine(t *testing.T) {
	ws, path := newConfiguredWorkspace(t, "[font]\nsize = 15\n")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	ws.ReloadConfig()
	if ws.cfg.TextStyle.Size != 12 {
		t.Errorf("size = %v, want the embedder's 12", ws.cfg.TextStyle.Size)
	}
}

// TestReloadConfig_CommandRegistered checks the reload command itself is live,
// since a workspace that can't be told to reload can only be fixed by restart.
func TestReloadConfig_CommandRegistered(t *testing.T) {
	ws, _ := newConfiguredWorkspace(t, "")
	cmd, ok := ws.w.CommandByID("workspace.reloadConfig")
	if !ok {
		t.Fatal("workspace.reloadConfig not registered")
	}
	want := remapMod(gui.ModSuper | gui.ModShift)
	if cmd.Shortcut.Key != gui.KeyComma || cmd.Shortcut.Modifiers != want {
		t.Errorf("reloadConfig shortcut = %+v, want Comma+%v", cmd.Shortcut, want)
	}
}

// TestApplyTermSettings_NoPanes guards the paneless case reached when the last
// shell has exited but the workspace is still up.
func TestApplyTermSettings_NoPanes(t *testing.T) {
	ws, _ := newConfiguredWorkspace(t, "")
	ws.applyTermSettings(ws.cfg) // must not panic
}
