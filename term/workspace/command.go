package workspace

import (
	"log"
	"strconv"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// buildCommands returns the workspace command table with built-in shortcuts,
// before any config-file overrides. All commands use Global: true so they fire
// before the focused terminal consumes the key.
func (ws *Workspace) buildCommands() []gui.Command {
	cmds := []gui.Command{
		// Split pane.
		{
			ID:       "workspace.splitVertical",
			Label:    "Split Vertical",
			Shortcut: gui.Shortcut{Key: gui.KeyD, Modifiers: gui.ModSuper},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.SplitPane(false) },
		},
		{
			ID:       "workspace.splitHorizontal",
			Label:    "Split Horizontal",
			Shortcut: gui.Shortcut{Key: gui.KeyD, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.SplitPane(true) },
		},
		// Close pane.
		{
			ID:       "workspace.closePane",
			Label:    "Close Pane",
			Shortcut: gui.Shortcut{Key: gui.KeyW, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.ClosePane() },
		},
		// Pane navigation.
		{
			ID:       "workspace.nextPane",
			Label:    "Next Pane",
			Shortcut: gui.Shortcut{Key: gui.KeyRightBracket, Modifiers: gui.ModSuper},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.NextPane() },
		},
		{
			ID:       "workspace.prevPane",
			Label:    "Previous Pane",
			Shortcut: gui.Shortcut{Key: gui.KeyLeftBracket, Modifiers: gui.ModSuper},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.PrevPane() },
		},
		// Pane resize: move the focused pane's nearest same-axis split
		// divider toward the arrow direction.
		{
			ID:       "workspace.resizeLeft",
			Label:    "Resize Pane Left",
			Shortcut: gui.Shortcut{Key: gui.KeyLeft, Modifiers: gui.ModSuper | gui.ModCtrl},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.resizeActivePane(resizeLeft) },
		},
		{
			ID:       "workspace.resizeRight",
			Label:    "Resize Pane Right",
			Shortcut: gui.Shortcut{Key: gui.KeyRight, Modifiers: gui.ModSuper | gui.ModCtrl},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.resizeActivePane(resizeRight) },
		},
		{
			ID:       "workspace.resizeUp",
			Label:    "Resize Pane Up",
			Shortcut: gui.Shortcut{Key: gui.KeyUp, Modifiers: gui.ModSuper | gui.ModCtrl},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.resizeActivePane(resizeUp) },
		},
		{
			ID:       "workspace.resizeDown",
			Label:    "Resize Pane Down",
			Shortcut: gui.Shortcut{Key: gui.KeyDown, Modifiers: gui.ModSuper | gui.ModCtrl},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.resizeActivePane(resizeDown) },
		},
		// Tab management.
		{
			ID:       "workspace.newTab",
			Label:    "New Tab",
			Shortcut: gui.Shortcut{Key: gui.KeyT, Modifiers: gui.ModSuper},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.AddTab() },
		},
		{
			ID:       "workspace.closeTab",
			Label:    "Close Tab",
			Shortcut: gui.Shortcut{Key: gui.KeyW, Modifiers: gui.ModSuper | gui.ModCtrl},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.CloseTab() },
		},
		{
			ID:       "workspace.moveTabLeft",
			Label:    "Move Tab Left",
			Shortcut: gui.Shortcut{Key: gui.KeyLeftBracket, Modifiers: gui.ModSuper | gui.ModAlt},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.MoveTabLeft() },
		},
		{
			ID:       "workspace.moveTabRight",
			Label:    "Move Tab Right",
			Shortcut: gui.Shortcut{Key: gui.KeyRightBracket, Modifiers: gui.ModSuper | gui.ModAlt},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.MoveTabRight() },
		},
		{
			ID:       "workspace.nextTab",
			Label:    "Next Tab",
			Shortcut: gui.Shortcut{Key: gui.KeyRightBracket, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.NextTab() },
		},
		{
			ID:       "workspace.prevTab",
			Label:    "Previous Tab",
			Shortcut: gui.Shortcut{Key: gui.KeyLeftBracket, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.PrevTab() },
		},
		// Session recording of the focused pane.
		{
			ID:       "workspace.toggleRecording",
			Label:    "Start / Stop Recording",
			Shortcut: gui.Shortcut{Key: gui.KeyR, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.ToggleRecording() },
		},
		// Broadcast input to every pane in the active tab.
		{
			ID:       "workspace.toggleBroadcast",
			Label:    "Broadcast Input to All Panes",
			Shortcut: gui.Shortcut{Key: gui.KeyI, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.ToggleBroadcast() },
		},
		// Theme.
		{
			ID:       "workspace.chooseTheme",
			Label:    "Choose Theme...",
			Shortcut: gui.Shortcut{Key: gui.KeyT, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.ToggleThemeBrowser() },
		},
		// List-overlay navigation — only active while one of the list overlays
		// (theme browser, command palette) is open, so these bare keys still
		// reach the child the rest of the time.
		//
		// One registration per key, shared by both overlays, with the Execute
		// switching on which is up. Adding a second Up/Down/Enter entry for the
		// palette is not an option: go-gui's registry rejects duplicate
		// shortcuts, and one rejection aborts the whole batch — see the note on
		// dismissOverlay below.
		{
			ID:         "workspace.overlayUp",
			Shortcut:   gui.Shortcut{Key: gui.KeyUp},
			Global:     true,
			CanExecute: ws.listOverlayOpen,
			Execute:    func(_ *gui.Event, w *gui.Window) { ws.overlayMove(-1) },
		},
		{
			ID:         "workspace.overlayDown",
			Shortcut:   gui.Shortcut{Key: gui.KeyDown},
			Global:     true,
			CanExecute: ws.listOverlayOpen,
			Execute:    func(_ *gui.Event, w *gui.Window) { ws.overlayMove(1) },
		},
		{
			ID:         "workspace.overlayPageUp",
			Shortcut:   gui.Shortcut{Key: gui.KeyPageUp},
			Global:     true,
			CanExecute: ws.listOverlayOpen,
			Execute:    func(_ *gui.Event, w *gui.Window) { ws.overlayPage(-1) },
		},
		{
			ID:         "workspace.overlayPageDown",
			Shortcut:   gui.Shortcut{Key: gui.KeyPageDown},
			Global:     true,
			CanExecute: ws.listOverlayOpen,
			Execute:    func(_ *gui.Event, w *gui.Window) { ws.overlayPage(1) },
		},
		{
			ID:         "workspace.overlayConfirm",
			Shortcut:   gui.Shortcut{Key: gui.KeyEnter},
			Global:     true,
			CanExecute: ws.listOverlayOpen,
			Execute:    func(_ *gui.Event, w *gui.Window) { ws.overlayConfirm() },
		},
		// Command palette.
		{
			ID:       paletteCommandID,
			Label:    "Command Palette",
			Shortcut: gui.Shortcut{Key: gui.KeyP, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.TogglePalette() },
		},
		// Config reload. Cmd+, is deliberately left free for a future
		// settings UI, so the reload lives on the Shift variant.
		{
			ID:       "workspace.reloadConfig",
			Label:    "Reload Config",
			Shortcut: gui.Shortcut{Key: gui.KeyComma, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.ReloadConfig() },
		},
		// Help overlay.
		{
			ID:       "workspace.toggleHelp",
			Label:    "Show / Hide Shortcuts",
			Shortcut: gui.Shortcut{Key: gui.KeySlash, Modifiers: gui.ModSuper},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.ToggleHelp() },
		},
		{
			// Escape dismisses whichever overlay is up. A *single* Escape
			// command owns both overlays: go-gui's registry rejects
			// duplicate shortcuts, and one rejection aborts the whole
			// RegisterCommands batch, so two Escape entries would silently
			// drop every command registered after them. CanExecute gates on
			// visibility so Escape still reaches the child process (vim,
			// less, …) whenever no overlay is open.
			ID:       "workspace.dismissOverlay",
			Shortcut: gui.Shortcut{Key: gui.KeyEscape},
			Global:   true,
			CanExecute: func(_ *gui.Window) bool {
				return ws.palette.visible || ws.browser.visible || ws.helpVisible
			},
			Execute: func(_ *gui.Event, w *gui.Window) { ws.dismissOverlay() },
		},
	}
	// Tab 1–9 shortcuts.
	for i := 0; i < 9; i++ {
		idx := i // capture
		cmds = append(cmds, gui.Command{
			ID:       "workspace.tab" + strconv.Itoa(i+1),
			Shortcut: gui.Shortcut{Key: gui.KeyCode(uint16(gui.Key1) + uint16(i)), Modifiers: gui.ModSuper},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.GoToTab(idx) },
		})
	}
	// Remap the Super-based defaults to platform-appropriate modifiers
	// (identity except on Windows, where Super is OS-reserved — see remapMod).
	// Applied before config overrides so explicit user bindings are honored
	// verbatim.
	for i := range cmds {
		cmds[i].Shortcut.Modifiers = remapMod(cmds[i].Shortcut.Modifiers)
	}
	return cmds
}

// loadAndApplyConfig re-reads the config file and rebuilds everything derived
// from it: the workspace command table (registered on the window), the term.*
// keybindings, and the effective Cfg used to build panes.
//
// It does not touch live panes — ReloadConfig does that, so New can call this
// before any pane exists.
func (ws *Workspace) loadAndApplyConfig() {
	fc := loadConfig(ws.baseCfg)
	cmds := ws.buildCommands()
	// Overrides are applied before registering so the help overlay, which
	// reads ws.commands, reflects the live bindings.
	keys := applyKeybindingOverrides(cmds, fc.keybindings)
	ws.cfg = applySettings(ws.baseCfg, fc, keys)
	ws.installCommands(cmds)
	// Covers both startup and reload: `theme =` in the config file is the
	// other way the active scheme changes without anyone touching the picker.
	ws.notifyColorScheme()
}

// ReloadConfig re-reads the config file and applies it to the running
// workspace: command shortcuts are re-registered and every live pane is
// updated in place. Bound to Cmd+Shift+, by default.
//
// Parse errors are logged and the offending setting keeps its current value —
// a malformed config must never wedge the app. Settings that did not change
// are not pushed to panes, so a reload triggered for one key doesn't disturb
// unrelated per-pane state (notably font zoom, which SetTextStyle resets).
func (ws *Workspace) ReloadConfig() {
	prev := ws.cfg
	ws.loadAndApplyConfig()
	ws.applyTermSettings(prev)
	ws.refresh()
}

// applyTermSettings pushes the settings that changed between prev and the
// current effective Cfg to every live pane in every tab.
func (ws *Workspace) applyTermSettings(prev Cfg) {
	cur := ws.cfg
	styleChanged := prev.TextStyle != cur.TextStyle
	// A theme removed from the config reverts to the embedder's first theme,
	// which is what term applies to a brand-new pane.
	newTheme, hasTheme := effectiveTheme(cur)
	oldTheme, hadTheme := effectiveTheme(prev)
	themeChanged := hasTheme && (!hadTheme || newTheme != oldTheme)

	for _, tab := range ws.tabs {
		for _, tm := range tab.terms {
			if styleChanged {
				tm.SetTextStyle(cur.TextStyle)
			}
			if themeChanged {
				tm.SetTheme(newTheme)
			}
			if prev.opts.scrollback != cur.opts.scrollback {
				tm.SetScrollbackRows(cur.opts.scrollback)
			}
			if prev.opts.bell != cur.opts.bell {
				tm.SetBellMode(cur.opts.bell)
			}
			if prev.opts.scrollbar != cur.opts.scrollbar {
				tm.SetScrollbarWidth(cur.opts.scrollbar)
			}
			if prev.opts.minContrast != cur.opts.minContrast {
				tm.SetMinimumContrast(cur.opts.minContrast)
			}
			if prev.opts.middleClickPaste != cur.opts.middleClickPaste {
				tm.SetMiddleClickPaste(cur.opts.middleClickPaste)
			}
			if prev.opts.notifyAfter != cur.opts.notifyAfter {
				tm.SetNotifyAfter(cur.opts.notifyAfter)
			}
			// KeyMap is a map, so it can't be compared for equality cheaply;
			// re-seeding is idempotent (mergeBindings rebuilds from the
			// defaults each time) and costs one small map per pane.
			tm.SetKeyBindings(cur.opts.keys)
		}
	}
	ws.w.UpdateWindow()
}

// effectiveTheme returns the theme a new pane would start with: the one named
// in the config file, else the embedder's first configured theme.
func effectiveTheme(cfg Cfg) (term.Theme, bool) {
	if cfg.opts.theme != nil {
		return *cfg.opts.theme, true
	}
	if len(cfg.Themes) > 0 {
		return cfg.Themes[0].Theme, true
	}
	return term.Theme{}, false
}

// installCommands registers cmds on the window, replacing any previously
// registered table (a config reload re-registers everything).
func (ws *Workspace) installCommands(cmds []gui.Command) {
	// Unregister the previous table first: the registry rejects duplicate
	// IDs, so a reload would otherwise keep the stale shortcuts.
	for i := range ws.commands {
		ws.w.UnregisterCommand(ws.commands[i].ID)
	}
	// Retain Label+Shortcut metadata so the help overlay renders the live
	// bindings rather than a hand-maintained copy. The tab 1–9 commands
	// carry no Label and are skipped by the overlay.
	ws.commands = cmds
	// Register one at a time rather than via RegisterCommands: that helper
	// aborts the whole batch on the first duplicate ID/shortcut, which
	// silently drops every command declared after it — exactly how a
	// duplicate Escape binding once disabled Cmd+1..9 tab selection.
	// Per-command registration confines the damage to the offending entry
	// and logs it instead of failing invisibly.
	for i := range cmds {
		if err := ws.w.RegisterCommand(cmds[i]); err != nil {
			log.Printf("workspace: register %s: %v", cmds[i].ID, err)
		}
	}
}

// listOverlayOpen reports whether an overlay that owns Up/Down/Enter is up.
// Shared by every navigation command so the gate cannot drift between them.
// Same set as overlayOwnsKeys, and deliberately expressed as that call: an
// overlay that owns Up/Down/Enter is exactly one that has taken the keyboard
// off the pane, so the two must not drift apart.
func (ws *Workspace) listOverlayOpen(_ *gui.Window) bool {
	return ws.overlayOwnsKeys()
}

// overlayMove routes a cursor step to whichever list overlay is open. The
// palette floats above the browser and is checked first, matching both the
// z-order in View and the precedence in dismissOverlay.
func (ws *Workspace) overlayMove(delta int) {
	switch {
	case ws.palette.visible:
		ws.paletteMove(delta)
	case ws.browser.visible:
		ws.themeBrowserMove(delta)
	}
}

// overlayPage routes a coarse jump to whichever list overlay is open.
func (ws *Workspace) overlayPage(dir int) {
	switch {
	case ws.palette.visible:
		ws.palettePage(dir)
	case ws.browser.visible:
		ws.themeBrowserPage(dir)
	}
}

// overlayConfirm routes Enter to whichever list overlay is open.
func (ws *Workspace) overlayConfirm() {
	switch {
	case ws.palette.visible:
		ws.paletteConfirm()
	case ws.browser.visible:
		ws.themeBrowserConfirm()
	}
}

// dismissOverlay closes the topmost visible overlay. The palette floats above
// the theme picker, which is drawn above the help panel, so precedence runs in
// that order when more than one is open.
func (ws *Workspace) dismissOverlay() {
	switch {
	case ws.palette.visible:
		// Not TogglePalette: Escape has its own two-stage meaning inside the
		// palette (clear the filter, then close).
		ws.paletteDismiss()
	case ws.browser.visible:
		// Not ToggleThemeBrowser: Escape has its own two-stage meaning inside
		// the browser (clear the filter, then close-and-revert).
		ws.themeBrowserDismiss()
	case ws.helpVisible:
		ws.ToggleHelp()
	}
}

// SplitPane splits the focused pane. If horizontal is true, splits top/bottom;
// otherwise splits left/right. Creates a new PTY with a fresh shell.
func (ws *Workspace) SplitPane(horizontal bool) {
	tab := ws.tabs[ws.activeTab]
	dir := SplitVertical
	if horizontal {
		dir = SplitHorizontal
	}
	// Unfocus the old pane so it stops asserting focus during layout.
	// The new pane defaults to focused=true. Capture its effective font size so
	// the split inherits the source pane's zoom (matching Ghostty). An unzoomed
	// source reports the workspace default, so the new pane matches it either
	// way; addPane treats zero as "inherit default" for the no-source case.
	// The split also inherits the source pane's CWD (empty when the shell
	// never reported one via OSC 7 — then the child inherits the process CWD).
	var inheritSize float32
	cwd := ws.focusedCwd()
	if old, ok := tab.terms[tab.focused]; ok {
		inheritSize = old.FontSize()
		old.SetFocused(false)
		old.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
	}
	newLeafID := tab.allocLeafID()
	if err := tab.addPane(ws.w, ws.cfg, newLeafID, cwd, inheritSize, ws.hooks()); err != nil {
		return
	}
	newRoot := splitLeaf(tab.root, tab.focused, newLeafID, dir)
	if newRoot != nil {
		tab.root = newRoot
		tab.focused = newLeafID
		ws.refresh()
	}
}

// ClosePane closes the focused pane in the active tab. Falls back to the
// nearest surviving pane. If the last pane, closes the tab.
func (ws *Workspace) ClosePane() {
	tab := ws.tabs[ws.activeTab]
	ws.closePaneInTab(tab, tab.focused)
}

// NextPane cycles focus to the next pane, wrapping to first after last.
func (ws *Workspace) NextPane() {
	tab := ws.tabs[ws.activeTab]
	if next := nextLeaf(tab.root, tab.focused); next != "" {
		ws.focusPaneInTab(tab, next)
	}
}

// PrevPane cycles focus to the previous pane, wrapping to last after first.
func (ws *Workspace) PrevPane() {
	tab := ws.tabs[ws.activeTab]
	if prev := prevLeaf(tab.root, tab.focused); prev != "" {
		ws.focusPaneInTab(tab, prev)
	}
}

// focusedCwd returns the working directory of the focused pane of the active
// tab, as reported by the shell over OSC 7. Empty when there is no live pane
// or the shell never emitted OSC 7 — callers pass that straight through, which
// leaves the child inheriting the process CWD.
func (ws *Workspace) focusedCwd() string {
	if ws.activeTab < 0 || ws.activeTab >= len(ws.tabs) {
		return ""
	}
	tab := ws.tabs[ws.activeTab]
	tm, ok := tab.terms[tab.focused]
	if !ok {
		return ""
	}
	return cwdLocalPath(tm.Cwd())
}

// AddTab creates a new tab with a single terminal and switches to it. The new
// tab's shell starts in the CWD of the pane the command was issued from.
func (ws *Workspace) AddTab() {
	// Capture the source CWD before the active tab index moves.
	cwd := ws.focusedCwd()
	// Unfocus old tab's pane.
	oldIdx := ws.activeTab
	if oldIdx >= 0 && oldIdx < len(ws.tabs) {
		oldTab := ws.tabs[oldIdx]
		if t, ok := oldTab.terms[oldTab.focused]; ok {
			t.SetFocused(false)
			t.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
		}
	}
	_, err := ws.addTab(cwd)
	if err != nil {
		return
	}
	// Focus the new tab's pane.
	tab := ws.tabs[ws.activeTab]
	if t, ok := tab.terms[tab.focused]; ok {
		t.SetFocused(true)
		t.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
	}
	ws.refresh()
}

// CloseTab closes the active tab. If it's the last tab, replaces it with
// a fresh single-pane tab.
func (ws *Workspace) CloseTab() {
	ws.closeTabAt(ws.activeTab)
}

// MoveTabLeft swaps the active tab with the one to its left.
// No-op when the active tab is already the first tab.
func (ws *Workspace) MoveTabLeft() {
	if ws.activeTab <= 0 || len(ws.tabs) < 2 {
		return
	}
	ws.tabs[ws.activeTab], ws.tabs[ws.activeTab-1] =
		ws.tabs[ws.activeTab-1], ws.tabs[ws.activeTab]
	ws.activeTab--
	ws.refresh()
}

// MoveTabRight swaps the active tab with the one to its right.
// No-op when the active tab is already the last tab.
func (ws *Workspace) MoveTabRight() {
	if ws.activeTab < 0 || ws.activeTab >= len(ws.tabs)-1 || len(ws.tabs) < 2 {
		return
	}
	ws.tabs[ws.activeTab], ws.tabs[ws.activeTab+1] =
		ws.tabs[ws.activeTab+1], ws.tabs[ws.activeTab]
	ws.activeTab++
	ws.refresh()
}

// NextTab switches to the next tab (wraps around).
func (ws *Workspace) NextTab() {
	if len(ws.tabs) < 2 {
		return
	}
	ws.activateTab((ws.activeTab + 1) % len(ws.tabs))
}

// PrevTab switches to the previous tab (wraps around).
func (ws *Workspace) PrevTab() {
	if len(ws.tabs) < 2 {
		return
	}
	idx := ws.activeTab - 1
	if idx < 0 {
		idx = len(ws.tabs) - 1
	}
	ws.activateTab(idx)
}

// GoToTab switches to the tab at the given 0-based index.
func (ws *Workspace) GoToTab(idx int) {
	if idx >= 0 && idx < len(ws.tabs) {
		ws.activateTab(idx)
	}
}

// FocusPane makes the given leaf the focused pane in its tab.
func (ws *Workspace) FocusPane(leafID string) {
	for _, tab := range ws.tabs {
		if _, ok := tab.terms[leafID]; ok {
			ws.focusPaneInTab(tab, leafID)
			return
		}
	}
}
