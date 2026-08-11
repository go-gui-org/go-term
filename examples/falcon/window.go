package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
	"github.com/go-gui-org/go-term/term/workspace"
)

// confirmOnQuit asks for confirmation before closing the window while
// terminal panes still have a live shell. Set to false to quit silently.
const confirmOnQuit = true

// app owns the live (non-replay) window's mutable state. The window
// callbacks need to both write the workspace (OnInit creates it) and read
// it back later (OnCloseRequest counts live shells, saveAndClose persists
// it), so it lives on a struct the callbacks close over rather than being
// threaded through as a **workspace.Workspace out-parameter.
type app struct {
	wc workspace.Cfg

	loadPath   string // workspace to restore on startup ("" = start fresh)
	savePath   string // workspace to write on quit ("" = don't save)
	recordPath string // .gtr file for the starting pane ("" = don't record)

	ws *workspace.Workspace
	// gapp is the gui.App the window runs under. Held because the native
	// menubar is installed on the App, not the window, and onInit is the
	// first point where the main window is registered with it.
	gapp *gui.App
	// initErr records a fatal OnInit failure. OnInit can't return an
	// error and must not log.Fatal — that would skip the deferred
	// teardown in run() — so it's stashed here and reported after the
	// app loop unwinds.
	initErr error
}

// windowCfg builds the WindowCfg for the normal (non-replay) path.
func (a *app) windowCfg() gui.WindowCfg {
	return gui.WindowCfg{
		Title:          appName,
		Width:          windowWidth,
		Height:         windowHeight,
		IconPNG:        appIconPNG,
		WMClass:        appName,
		OnCloseRequest: a.onCloseRequest,
		OnInit:         a.onInit,
		// Frame-pipeline timings feed the GOTERM_LATENCY report, which pairs
		// them with the keystroke spans to say how much of a repaint is view
		// generation and layout versus the terminal's own draw passes. Off
		// unless the instrumentation is on: it timestamps every frame.
		Timings: latencyInstrumented(),
	}
}

// latencyInstrumented reports whether GOTERM_LATENCY asks for keystroke-to-frame
// instrumentation. The variable is parsed properly inside term/; falcon only
// needs to know whether to hand the window its timing switch, so it tests for a
// meaningfully-set value rather than importing a debug knob into the public API.
func latencyInstrumented() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GOTERM_LATENCY"))) {
	case "", "0", "false", "off", "no":
		return false
	}
	return true
}

// onInit creates or restores the workspace and installs its view.
func (a *app) onInit(w *gui.Window) {
	var s *workspace.Workspace
	var err error
	if a.loadPath != "" {
		// Restore already falls back to New on a missing, unparseable, or
		// version-mismatched file, so a non-nil error here is genuinely
		// unrecoverable rather than a bad workspace file.
		s, err = workspace.Restore(w, a.wc, a.loadPath)
	} else {
		s, err = workspace.New(w, a.wc)
	}
	if err != nil {
		a.initErr = err
		w.Close()
		return
	}
	a.ws = s
	// --record applies to the pane the user starts in. Other panes
	// are recorded on demand with Cmd+Shift+R.
	if a.recordPath != "" {
		tm := s.ActivePane()
		if tm == nil {
			// A restored workspace can come up with no live pane. Say so
			// rather than dropping an explicit --record on the floor.
			log.Printf("record: no active pane, not recording to %s", a.recordPath)
		} else if err := tm.StartRecording(a.recordPath); err != nil {
			log.Printf("record: %v", err)
		} else {
			log.Printf("recording to %s", a.recordPath)
		}
	}
	w.UpdateView(s.View)
	registerCommands(w)
	// After the workspace exists: the Help menu's shortcut item resolves
	// against the workspace commands registered by workspace.New.
	if a.gapp != nil {
		a.installMenubar(a.gapp, w)
	}
}

// onCloseRequest optionally confirms, then saves and closes.
func (a *app) onCloseRequest(w *gui.Window) {
	// A confirm dialog is already up (e.g. a repeated Cmd+Q or
	// a close-button click while confirming): don't stack a
	// second one. DialogIsVisible also drives the quit-request
	// dedup in go-gui, but the window-close path has no such
	// guard, so check here too.
	if w.DialogIsVisible() {
		return
	}
	n := 0
	if a.ws != nil {
		n = a.ws.LiveTermCount()
	}
	if confirmOnQuit && n > 0 {
		// Use go-gui's in-app dialog, not NativeConfirmDialog:
		// go-gui renders and keyboard-routes it itself (Enter,
		// Esc, Tab all work). The native NSAlert runModal path
		// loses keyboard focus under the metal backend's manual
		// event pump, and doesn't participate in the quit-request
		// dedup, so it could stack duplicate dialogs.
		w.Dialog(gui.DialogCfg{
			DialogType: gui.DialogConfirm,
			Title:      "Quit " + appName + "?",
			Body: fmt.Sprintf(
				"%d active terminal(s) will be terminated. Quit anyway?", n),
			OnOkYes: a.saveAndClose,
		})
		return
	}
	a.saveAndClose(w)
}

// saveAndClose persists the workspace (when a save path is configured) and
// closes the window.
func (a *app) saveAndClose(w *gui.Window) {
	if a.savePath != "" && a.ws != nil {
		if err := a.ws.Save(a.savePath); err != nil {
			log.Printf("workspace save: %v", err)
		}
	}
	w.Close()
}

// close tears down the workspace. Safe to call when OnInit never ran.
func (a *app) close() {
	if a.ws != nil {
		_ = a.ws.Close()
	}
}

// replayWindowCfg builds the WindowCfg for the --replay viewer. Split out
// of runReplay so the static fields are reachable from a test: runReplay
// itself opens a real window and runs the app loop, so nothing inside it
// can be asserted on.
func replayWindowCfg(path string, onInit func(*gui.Window)) gui.WindowCfg {
	return gui.WindowCfg{
		Title:   appName + " replay — " + filepath.Base(path),
		Width:   windowWidth,
		Height:  windowHeight,
		IconPNG: appIconPNG,
		WMClass: appName,
		OnInit:  onInit,
	}
}

// runReplay opens a single-pane window playing back a recording. There is no
// shell, so no quit confirmation and no workspace to save; playback controls
// (space, +/-, '.', '0') are keystrokes routed to the replay source.
func runReplay(rc replayCfg) int {
	// The replay viewer registers only DefaultTheme, so its chrome follows
	// that one theme and never changes.
	applyChrome(term.DefaultTheme.IsDark())
	var tm *term.Term
	var initErr error
	w := gui.NewWindow(replayWindowCfg(rc.path, func(w *gui.Window) {
		var err error
		tm, err = term.NewReplay(w, term.Cfg{
			TextStyle: defaultTextStyle(),
			Themes:    []term.NamedTheme{{Name: "Default", Theme: term.DefaultTheme}},
		}, term.ReplayCfg{
			Path:      rc.path,
			Speed:     rc.speed,
			IdleLimit: rc.idle,
			Loop:      rc.loop,
			Controls:  true,
		})
		if err != nil {
			initErr = err
			w.Close()
			return
		}
		w.UpdateView(tm.View)
	}))
	defer func() {
		if tm != nil {
			_ = tm.Close()
		}
	}()
	log.Printf("replaying %s — space pauses, +/- speed, . steps, 0 restarts", rc.path)
	backendRunApp(gui.NewApp(), w)
	if initErr != nil {
		log.Printf("replay: %v", initErr)
		return 1
	}
	return 0
}
