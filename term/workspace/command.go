package workspace

import (
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
			ID:       "workspace.cycleTheme",
			Label:    "Cycle Theme",
			Shortcut: gui.Shortcut{Key: gui.KeyT, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { ws.CycleTheme() },
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
	_ = ws.w.RegisterCommands(cmds...)
}

// SplitPane splits the focused pane. If horizontal is true, splits top/bottom;
// otherwise splits left/right. Creates a new PTY with a fresh shell.
func (ws *Workspace) SplitPane(horizontal bool) {
	tab := ws.tabs[ws.activeTab]
	dir := SplitVertical
	if horizontal {
		dir = SplitHorizontal
	}
	// Unfocus the old pane so it stops asserting IDFocus during layout.
	// The new pane defaults to focused=true.
	if old, ok := tab.terms[tab.focused]; ok {
		old.SetFocused(false)
		old.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
	}
	newLeafID := tab.allocLeafID()
	if err := tab.addPane(ws.w, ws.cfg, newLeafID, ws.onPaneExit, ws.onPaneFocus, ws.onPaneTitle); err != nil {
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
