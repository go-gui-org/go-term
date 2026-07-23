package workspace

import (
	"log"
	"strconv"

	"github.com/go-gui-org/go-gui/gui"
)

// registerCommands registers all workspace keyboard shortcuts on the window.
// All commands use Global: true so they fire before the focused terminal
// consumes the key.
func (ws *Workspace) registerCommands() {
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
		// Theme.
		{
			ID:       "workspace.chooseTheme",
			Label:    "Choose Theme...",
			Shortcut: gui.Shortcut{Key: gui.KeyT, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.ToggleThemePicker() },
		},
		// Theme picker keyboard navigation — only active when picker is visible.
		{
			ID:         "workspace.themePickerUp",
			Shortcut:   gui.Shortcut{Key: gui.KeyUp},
			Global:     true,
			CanExecute: func(_ *gui.Window) bool { return ws.themePickerVisible },
			Execute:    func(_ *gui.Event, w *gui.Window) { ws.themePickerMoveUp() },
		},
		{
			ID:         "workspace.themePickerDown",
			Shortcut:   gui.Shortcut{Key: gui.KeyDown},
			Global:     true,
			CanExecute: func(_ *gui.Window) bool { return ws.themePickerVisible },
			Execute:    func(_ *gui.Event, w *gui.Window) { ws.themePickerMoveDown() },
		},
		{
			ID:         "workspace.themePickerConfirm",
			Shortcut:   gui.Shortcut{Key: gui.KeyEnter},
			Global:     true,
			CanExecute: func(_ *gui.Window) bool { return ws.themePickerVisible },
			Execute:    func(_ *gui.Event, w *gui.Window) { ws.themePickerConfirm() },
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
				return ws.themePickerVisible || ws.helpVisible
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
	// Apply any [keybindings] overrides from the config file before
	// registering, so the help overlay reflects live bindings.
	kbCfg := loadConfig(ws.cfg)
	applyKeybindingOverrides(cmds, kbCfg.keybindings)

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

// dismissOverlay closes the topmost visible overlay. The theme picker is
// drawn above the help panel, so it wins when both are open.
func (ws *Workspace) dismissOverlay() {
	switch {
	case ws.themePickerVisible:
		ws.ToggleThemePicker()
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
	// The new pane defaults to focused=true.
	if old, ok := tab.terms[tab.focused]; ok {
		old.SetFocused(false)
		old.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
	}
	newLeafID := tab.allocLeafID()
	if err := tab.addPane(ws.w, ws.cfg, newLeafID, "", ws.onPaneExit, ws.onPaneFocus, ws.onPaneTitle); err != nil {
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

// AddTab creates a new tab with a single terminal and switches to it.
func (ws *Workspace) AddTab() {
	// Unfocus old tab's pane.
	oldIdx := ws.activeTab
	if oldIdx >= 0 && oldIdx < len(ws.tabs) {
		oldTab := ws.tabs[oldIdx]
		if t, ok := oldTab.terms[oldTab.focused]; ok {
			t.SetFocused(false)
			t.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
		}
	}
	_, err := ws.addTab()
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
