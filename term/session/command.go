package session

import (
	"strconv"

	"github.com/go-gui-org/go-gui/gui"
)

// registerCommands registers all session keyboard shortcuts on the window.
// All commands use Global: true so they fire before the focused terminal
// consumes the key.
func (s *Session) registerCommands() {
	cmds := []gui.Command{
		// Split pane.
		{
			ID:       "session.splitVertical",
			Label:    "Split Vertical",
			Shortcut: gui.Shortcut{Key: gui.KeyD, Modifiers: gui.ModSuper},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { s.SplitPane(false) },
		},
		{
			ID:       "session.splitHorizontal",
			Label:    "Split Horizontal",
			Shortcut: gui.Shortcut{Key: gui.KeyD, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { s.SplitPane(true) },
		},
		// Close pane.
		{
			ID:       "session.closePane",
			Label:    "Close Pane",
			Shortcut: gui.Shortcut{Key: gui.KeyW, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { s.ClosePane() },
		},
		// Pane navigation.
		{
			ID:       "session.nextPane",
			Label:    "Next Pane",
			Shortcut: gui.Shortcut{Key: gui.KeyRightBracket, Modifiers: gui.ModSuper},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { s.NextPane() },
		},
		{
			ID:       "session.prevPane",
			Label:    "Previous Pane",
			Shortcut: gui.Shortcut{Key: gui.KeyLeftBracket, Modifiers: gui.ModSuper},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { s.PrevPane() },
		},
		// Tab management.
		{
			ID:       "session.newTab",
			Label:    "New Tab",
			Shortcut: gui.Shortcut{Key: gui.KeyT, Modifiers: gui.ModSuper},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { s.AddTab() },
		},
		{
			ID:       "session.closeTab",
			Label:    "Close Tab",
			Shortcut: gui.Shortcut{Key: gui.KeyW, Modifiers: gui.ModSuper | gui.ModCtrl},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { s.CloseTab() },
		},
		{
			ID:       "session.nextTab",
			Label:    "Next Tab",
			Shortcut: gui.Shortcut{Key: gui.KeyRightBracket, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { s.NextTab() },
		},
		{
			ID:       "session.prevTab",
			Label:    "Previous Tab",
			Shortcut: gui.Shortcut{Key: gui.KeyLeftBracket, Modifiers: gui.ModSuper | gui.ModShift},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { s.PrevTab() },
		},
	}
	// Tab 1–9 shortcuts.
	for i := 0; i < 9; i++ {
		idx := i // capture
		cmds = append(cmds, gui.Command{
			ID:       "session.tab" + strconv.Itoa(i+1),
			Shortcut: gui.Shortcut{Key: gui.KeyCode(uint16(gui.Key1) + uint16(i)), Modifiers: gui.ModSuper},
			Global:   true,
			Execute:  func(_ *gui.Event, w *gui.Window) { s.GoToTab(idx) },
		})
	}
	_ = s.w.RegisterCommands(cmds...)
}

// SplitPane splits the focused pane. If horizontal is true, splits top/bottom;
// otherwise splits left/right. Creates a new PTY with a fresh shell.
func (s *Session) SplitPane(horizontal bool) {
	tab := s.tabs[s.activeTab]
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
	if err := tab.addPane(s.w, s.cfg, newLeafID, s.onPaneExit, s.onPaneFocus, s.onPaneTitle); err != nil {
		return
	}
	newRoot := splitLeaf(tab.root, tab.focused, newLeafID, dir)
	if newRoot != nil {
		tab.root = newRoot
		tab.focused = newLeafID
		s.refresh()
	}
}

// ClosePane closes the focused pane in the active tab. Falls back to the
// nearest surviving pane. If the last pane, closes the tab.
func (s *Session) ClosePane() {
	tab := s.tabs[s.activeTab]
	s.closePaneInTab(tab, tab.focused)
}

// NextPane cycles focus to the next pane, wrapping to first after last.
func (s *Session) NextPane() {
	tab := s.tabs[s.activeTab]
	if next := nextLeaf(tab.root, tab.focused); next != "" {
		s.focusPaneInTab(tab, next)
	}
}

// PrevPane cycles focus to the previous pane, wrapping to last after first.
func (s *Session) PrevPane() {
	tab := s.tabs[s.activeTab]
	if prev := prevLeaf(tab.root, tab.focused); prev != "" {
		s.focusPaneInTab(tab, prev)
	}
}

// AddTab creates a new tab with a single terminal and switches to it.
func (s *Session) AddTab() {
	// Unfocus old tab's pane.
	oldIdx := s.activeTab
	if oldIdx >= 0 && oldIdx < len(s.tabs) {
		oldTab := s.tabs[oldIdx]
		if t, ok := oldTab.terms[oldTab.focused]; ok {
			t.SetFocused(false)
			t.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
		}
	}
	_, err := s.addTab()
	if err != nil {
		return
	}
	// Focus the new tab's pane.
	tab := s.tabs[s.activeTab]
	if t, ok := tab.terms[tab.focused]; ok {
		t.SetFocused(true)
		t.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
	}
	s.refresh()
}

// CloseTab closes the active tab. If it's the last tab, replaces it with
// a fresh single-pane tab.
func (s *Session) CloseTab() {
	s.closeTabAt(s.activeTab)
}

// NextTab switches to the next tab (wraps around).
func (s *Session) NextTab() {
	if len(s.tabs) < 2 {
		return
	}
	s.activateTab((s.activeTab + 1) % len(s.tabs))
}

// PrevTab switches to the previous tab (wraps around).
func (s *Session) PrevTab() {
	if len(s.tabs) < 2 {
		return
	}
	idx := s.activeTab - 1
	if idx < 0 {
		idx = len(s.tabs) - 1
	}
	s.activateTab(idx)
}

// GoToTab switches to the tab at the given 0-based index.
func (s *Session) GoToTab(idx int) {
	if idx >= 0 && idx < len(s.tabs) {
		s.activateTab(idx)
	}
}

// FocusPane makes the given leaf the focused pane in its tab.
func (s *Session) FocusPane(leafID string) {
	for _, tab := range s.tabs {
		if _, ok := tab.terms[leafID]; ok {
			s.focusPaneInTab(tab, leafID)
			return
		}
	}
}
