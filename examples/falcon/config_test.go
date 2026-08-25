package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestFlagSet returns a silent, non-exiting FlagSet for parseFlags.
func newTestFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("falcon", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// TestParseFlagsPopulatesConfigs is the regression test for the by-value
// return bug: parseFlags once handed back a pre-Parse snapshot, silently
// zeroing --workspace, --save-workspace, and --record.
func TestParseFlagsPopulatesConfigs(t *testing.T) {
	start, replay, err := parseFlags(newTestFlagSet(), []string{
		"-workspace", "/tmp/ws.json",
		"-save-workspace", "/tmp/out.json",
		"-record", "/tmp/s.gtr",
		"-replay", "/tmp/r.gtr",
		"-replay-speed", "2.5",
		"-replay-idle-limit", "250ms",
		"-replay-loop",
	})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if start.workspacePath != "/tmp/ws.json" {
		t.Errorf("workspacePath = %q, want /tmp/ws.json", start.workspacePath)
	}
	if start.saveWorkspacePath != "/tmp/out.json" {
		t.Errorf("saveWorkspacePath = %q, want /tmp/out.json", start.saveWorkspacePath)
	}
	if start.recordPath != "/tmp/s.gtr" {
		t.Errorf("recordPath = %q, want /tmp/s.gtr", start.recordPath)
	}
	if replay.path != "/tmp/r.gtr" {
		t.Errorf("replay.path = %q, want /tmp/r.gtr", replay.path)
	}
	if replay.speed != 2.5 {
		t.Errorf("replay.speed = %v, want 2.5", replay.speed)
	}
	if replay.idle != 250*time.Millisecond {
		t.Errorf("replay.idle = %v, want 250ms", replay.idle)
	}
	if !replay.loop {
		t.Error("replay.loop = false, want true")
	}
}

func TestParseFlagsDefaults(t *testing.T) {
	start, replay, err := parseFlags(newTestFlagSet(), nil)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if start.workspacePath != "" || start.saveWorkspacePath != "" || start.recordPath != "" {
		t.Errorf("start = %+v, want zero", *start)
	}
	if replay.path != "" || replay.speed != 1 || replay.idle != 0 || replay.loop {
		t.Errorf("replay = %+v, want zero path, speed 1", *replay)
	}
}

// TestParseFlagsError checks the error path returns no half-built configs:
// callers must not be handed a struct they could mistake for parsed input.
func TestParseFlagsError(t *testing.T) {
	start, replay, err := parseFlags(newTestFlagSet(), []string{"-nope"})
	if err == nil {
		t.Fatal("parseFlags(-nope) = nil error, want a parse error")
	}
	if start != nil || replay != nil {
		t.Errorf("start = %v, replay = %v, want both nil on error", start, replay)
	}
}

func TestResolvedWorkspacePath(t *testing.T) {
	// An explicit --workspace wins even when the file doesn't exist:
	// Restore falls back to New, and the path is still the save target.
	c := &startCfg{workspacePath: "/nonexistent/ws.json"}
	if got := c.resolvedWorkspacePath(); got != "/nonexistent/ws.json" {
		t.Errorf("resolvedWorkspacePath = %q, want the explicit flag value", got)
	}

	// With no flag, the default path only applies once it exists on disk.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	empty := &startCfg{}
	if got := empty.resolvedWorkspacePath(); got != "" {
		t.Errorf("resolvedWorkspacePath = %q, want \"\" when no default file exists", got)
	}
}

// TestResolvedWorkspacePathDefaultExists covers the other half of the Stat
// branch: once the default file is on disk it becomes the restore source
// with no flag at all. That is the second-launch path for a fresh install.
func TestResolvedWorkspacePathDefaultExists(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)

	want := filepath.Join(cfgHome, "falcon", "workspace.json")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &startCfg{}
	if got := c.resolvedWorkspacePath(); got != want {
		t.Errorf("resolvedWorkspacePath = %q, want %q", got, want)
	}
	// Save target must agree, or a restore would be written back elsewhere.
	if got := c.effectiveSavePath(); got != want {
		t.Errorf("effectiveSavePath = %q, want %q", got, want)
	}
}

// TestEffectiveSavePathFreshInstall pins the fresh-install behavior: with no
// flags and no existing workspace file there is nothing to restore, but the
// default path is still the save target, so quitting creates it.
func TestEffectiveSavePathFreshInstall(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)

	c := &startCfg{}
	want := filepath.Join(cfgHome, "falcon", "workspace.json")
	if got := c.resolvedWorkspacePath(); got != "" {
		t.Errorf("resolvedWorkspacePath = %q, want \"\"", got)
	}
	if got := c.effectiveSavePath(); got != want {
		t.Errorf("effectiveSavePath = %q, want %q", got, want)
	}
}

func TestEffectiveSavePathPrecedence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tests := []struct {
		name string
		cfg  startCfg
		want string
	}{
		{"save flag wins", startCfg{workspacePath: "a", saveWorkspacePath: "b"}, "b"},
		{"workspace flag when no save flag", startCfg{workspacePath: "a"}, "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.effectiveSavePath(); got != tt.want {
				t.Errorf("effectiveSavePath = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDefaultTextStyle pins the font request. The family string is
// load-bearing: go-text matches the abbreviated "NFM" name from the font's
// name table, so a well-meaning edit to "JetBrains Mono Nerd Font" would
// silently fall back to a different face at runtime.
func TestDefaultTextStyle(t *testing.T) {
	ts := defaultTextStyle()
	if ts.Family != "JetBrainsMono NFM" {
		t.Errorf("Family = %q, want JetBrainsMono NFM", ts.Family)
	}
	if ts.Size <= 0 {
		t.Errorf("Size = %v, want positive", ts.Size)
	}
}

func TestDefaultDownloadDir(t *testing.T) {
	if got := defaultDownloadDir(); got != "" && filepath.Base(got) != "Downloads" {
		t.Errorf("defaultDownloadDir = %q, want a path ending in Downloads", got)
	}
}

// TestThemeList guards the hand-maintained theme table against the failure
// mode it invites: a copy-pasted entry with a duplicate or empty name, which
// would show twice in the picker and make one theme unreachable.
func TestThemeList(t *testing.T) {
	themes := themeList()
	if len(themes) == 0 {
		t.Fatal("themeList is empty")
	}
	if themes[0].Name != "Default" {
		t.Errorf("themes[0].Name = %q, want Default first", themes[0].Name)
	}
	seen := make(map[string]bool, len(themes))
	for i, th := range themes {
		if strings.TrimSpace(th.Name) == "" {
			t.Errorf("themes[%d] has an empty name", i)
		}
		if seen[th.Name] {
			t.Errorf("duplicate theme name %q", th.Name)
		}
		seen[th.Name] = true
	}

	// At least one theme of each character must be registered. A user with no
	// light option is the reason this list grew light themes; a list with no
	// dark one would be the same bug pointed the other way. It is also what
	// makes mode 2031 reachable through the UI at all — Cmd+Shift+T can only
	// notify a subscribed child if two registered themes disagree about the
	// scheme.
	var dark, light int
	for _, th := range themes {
		if th.Theme.IsDark() {
			dark++
		} else {
			light++
		}
	}
	if dark == 0 || light == 0 {
		t.Errorf("themeList has %d dark and %d light themes; want at least one of each",
			dark, light)
	}
}
