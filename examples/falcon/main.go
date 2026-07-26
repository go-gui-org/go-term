// Falcon is a full-featured terminal emulator built on go-term and the go-gui
// framework. It spawns a real shell over a PTY and renders through a
// GPU-accelerated DrawCanvas, covering the protocol surface expected by modern
// CLI tools and TUI frameworks (e.g. vim, less, htop). Supports multi-tab,
// multi-pane workspaces, workspace save/restore, and multiple color themes.
// Targets macOS, Linux, and Windows (ConPTY). Used as a daily-driver terminal
// on macOS.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-gui/gui/backend"
	"github.com/go-gui-org/go-term/term"
	"github.com/go-gui-org/go-term/term/workspace"
)

// confirmOnQuit asks for confirmation before closing the window while
// terminal panes still have a live shell. Set to false to quit silently.
const confirmOnQuit = true

// defaultFontFamily is the JetBrains Mono Nerd Font (Mono variant) family
// name as reported by go-glyph's pure-Go go-text font discovery. go-text
// reads the abbreviated "NF"/"NFM" family from the font's name table
// (not the spelled-out "Nerd Font"), so the request must use that form.
const defaultFontFamily = "JetBrainsMono NFM"

func main() {
	cfg := parseStartCfg()

	var replayPath string
	var replaySpeed float64
	var replayIdle time.Duration
	var replayLoop bool
	flag.StringVar(&replayPath, "replay", "", "play back a .gtr recording instead of starting a shell")
	flag.Float64Var(&replaySpeed, "replay-speed", 1, "playback speed multiplier for --replay")
	flag.DurationVar(&replayIdle, "replay-idle-limit", 0,
		"cap on any single gap between recorded frames (0 = none)")
	flag.BoolVar(&replayLoop, "replay-loop", false, "restart playback at end of recording")
	flag.Parse()

	// Replay is a viewer, not a multiplexer: one pane, no tabs, no
	// workspace persistence. Handled entirely separately from the normal
	// startup path below.
	if replayPath != "" {
		runReplay(replayPath, term.ReplayCfg{
			Path:      replayPath,
			Speed:     replaySpeed,
			IdleLimit: replayIdle,
			Loop:      replayLoop,
			Controls:  true,
		})
		return
	}

	workspacePath := cfg.resolvedWorkspacePath()
	savePath := cfg.effectiveSavePath()

	gui.SetTheme(gui.ThemeDark.WithBorders(true))

	wc := workspace.Cfg{
		TextStyle:              gui.TextStyle{Family: defaultFontFamily, Size: 12},
		ExitWhenLastShellExits: true,
		DownloadDir:            defaultDownloadDir(),
		Themes:                 themeList(),
	}

	var s *workspace.Workspace
	w := gui.NewWindow(newLiveWindowCfg(wc, workspacePath, savePath, cfg.recordPath, &s))
	defer func() {
		if s != nil {
			_ = s.Close()
		}
	}()
	// Use the multi-window app loop: only it honors an OnCloseRequest
	// veto (single-window backend.Run quits unconditionally on Cmd+Q).
	backend.RunApp(gui.NewApp(), w)
}

// runReplay opens a single-pane window playing back a recording. There is no
// shell, so no quit confirmation and no workspace to save; playback controls
// (space, +/-, '.', '0') are keystrokes routed to the replay source.
func runReplay(path string, rc term.ReplayCfg) {
	gui.SetTheme(gui.ThemeDark.WithBorders(true))
	var tm *term.Term
	w := gui.NewWindow(gui.WindowCfg{
		Title:  "go-term replay — " + filepath.Base(path),
		Width:  900,
		Height: 600,
		OnInit: func(w *gui.Window) {
			var err error
			tm, err = term.NewReplay(w, term.Cfg{
				TextStyle: gui.TextStyle{Family: defaultFontFamily, Size: 12},
				Themes:    []term.NamedTheme{{Name: "Default", Theme: term.DefaultTheme}},
			}, rc)
			if err != nil {
				log.Fatalf("replay: %v", err)
			}
			w.UpdateView(tm.View)
		},
	})
	defer func() {
		if tm != nil {
			_ = tm.Close()
		}
	}()
	log.Printf("replaying %s — space pauses, +/- speed, . steps, 0 restarts", path)
	backend.RunApp(gui.NewApp(), w)
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
