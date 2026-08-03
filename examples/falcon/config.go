package main

import (
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
	"github.com/go-gui-org/go-term/term/workspace"
)

// Window geometry shared by the live and replay windows, so the two paths
// can't drift apart.
const (
	windowWidth  = 900
	windowHeight = 600
)

// defaultFontFamily is the JetBrains Mono Nerd Font (Mono variant) family
// name as reported by go-glyph's pure-Go go-text font discovery. go-text
// reads the abbreviated "NF"/"NFM" family from the font's name table
// (not the spelled-out "Nerd Font"), so the request must use that form.
const defaultFontFamily = "JetBrainsMono NFM"

// startCfg holds the CLI arguments for the normal (non-replay) startup path.
type startCfg struct {
	workspacePath     string
	saveWorkspacePath string
	recordPath        string
}

// replayCfg holds the CLI arguments for the --replay viewer path.
type replayCfg struct {
	path  string
	speed float64
	idle  time.Duration
	loop  bool
}

// parseFlags registers every falcon flag on fs, parses args, and returns
// the populated configs.
//
// All flags are registered and parsed in this one function on purpose.
// Registering in one place and parsing in another leaves a window where
// the config structs are still zero-valued, and nothing but a comment
// stops a caller from reading them too early — a bug that has already
// bitten this file once. Here, a returned config is always parsed.
//
// fs is a parameter rather than flag.CommandLine so tests can parse a
// synthetic argv against a throwaway FlagSet.
func parseFlags(fs *flag.FlagSet, args []string) (*startCfg, *replayCfg, error) {
	start := &startCfg{}
	fs.StringVar(&start.workspacePath, "workspace", "",
		"workspace JSON to load on startup")
	fs.StringVar(&start.saveWorkspacePath, "save-workspace", "",
		"workspace JSON to write on quit (defaults to --workspace path when set)")
	fs.StringVar(&start.recordPath, "record", "",
		"record the first pane's session to this .gtr file")

	replay := &replayCfg{}
	fs.StringVar(&replay.path, "replay", "",
		"play back a .gtr recording instead of starting a shell")
	fs.Float64Var(&replay.speed, "replay-speed", 1,
		"playback speed multiplier for --replay")
	fs.DurationVar(&replay.idle, "replay-idle-limit", 0,
		"cap on any single gap between recorded frames (0 = none)")
	fs.BoolVar(&replay.loop, "replay-loop", false,
		"restart playback at end of recording")

	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	return start, replay, nil
}

// resolvedWorkspacePath returns the workspace to restore on startup:
// --workspace if given, else the default path when that file already
// exists. Empty means "start fresh".
func (c *startCfg) resolvedWorkspacePath() string {
	if c.workspacePath != "" {
		return c.workspacePath
	}
	// Restoring requires an existing file, so the default only applies
	// once it's on disk.
	def := defaultWorkspacePath()
	if def == "" {
		return ""
	}
	if _, err := os.Stat(def); err != nil {
		return ""
	}
	return def
}

// effectiveSavePath returns the path to write the workspace on quit:
// --save-workspace if set, else --workspace if set, else the default
// workspace path.
//
// Unlike resolvedWorkspacePath, the default fallback here does *not*
// require the file to already exist: saving creates it (Workspace.Save
// does MkdirAll on the parent). That's what makes a fresh install persist
// its layout on first quit, so the next launch has something to restore.
func (c *startCfg) effectiveSavePath() string {
	if c.saveWorkspacePath != "" {
		return c.saveWorkspacePath
	}
	if c.workspacePath != "" {
		return c.workspacePath
	}
	return defaultWorkspacePath()
}

// defaultWorkspacePath returns the default workspace JSON path, or "" when
// the config directory can't be determined. The file need not exist.
func defaultWorkspacePath() string {
	def, err := workspace.DefaultWorkspacePath()
	if err != nil {
		return ""
	}
	return def
}

// defaultTextStyle is the terminal font used by both the live and replay
// windows.
func defaultTextStyle() gui.TextStyle {
	return gui.TextStyle{Family: defaultFontFamily, Size: 12}
}

// applyChrome sets the go-gui widget theme to match the terminal's light/dark
// character. Shared by the live and replay paths for the same reason as the
// window geometry above: two call sites setting the chrome independently is
// two chances to drift.
//
// Wired to workspace.Cfg.OnColorScheme, so switching to a light terminal theme
// takes the tab bar and borders with it instead of leaving a light pane inside
// dark chrome. go-gui's theme is process-global and its Default*Style vars are
// read when views are built; the workspace rebuilds its whole view tree on
// every refresh, so a swap here shows up in the next frame.
func applyChrome(dark bool) {
	base := gui.ThemeLight
	if dark {
		base = gui.ThemeDark
	}
	gui.SetTheme(base.WithBorders(true))
}

// defaultDownloadDir picks where OSC 1337 File= transfers land. Downloads are
// opt-in per embedder, so this is falcon's policy, not go-term's: the user's
// home Downloads folder, matching what iTerm2 does. Returns "" when the home
// directory can't be determined, which leaves file transfers disabled.
func defaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Downloads")
}

// themeList returns the built-in color themes available in falcon.
func themeList() []term.NamedTheme {
	return []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Dracula", Theme: term.DraculaTheme},
		{Name: "Catppuccin Mocha", Theme: term.CatppuccinMochaTheme},
		{Name: "Tokyo Night", Theme: term.TokyoNightTheme},
		{Name: "Monokai", Theme: term.MonokaiTheme},
		{Name: "One Dark", Theme: term.OneDarkTheme},
		{Name: "Rosé Pine", Theme: term.RosePineTheme},
		{Name: "Kanagawa", Theme: term.KanagawaTheme},
		{Name: "Ayu Dark", Theme: term.AyuDarkTheme},
		{Name: "Everforest", Theme: term.EverforestTheme},
		{Name: "GitHub Dark", Theme: term.GitHubDarkTheme},
		{Name: "Gruvbox", Theme: term.GruvboxTheme},
		{Name: "Nord", Theme: term.NordTheme},
		{Name: "Solarized Dark", Theme: term.SolarizedDarkTheme},
		{Name: "Solarized Light", Theme: term.SolarizedLightTheme},
		{Name: "GitHub Light", Theme: term.GitHubLightTheme},
		{Name: "Catppuccin Latte", Theme: term.CatppuccinLatteTheme},
	}
}
