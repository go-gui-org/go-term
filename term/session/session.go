package session

import (
	"errors"
	"strconv"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// Cfg configures a Session. All fields are optional.
type Cfg struct {
	TextStyle gui.TextStyle
	Themes    []term.NamedTheme
}

// Session manages a multi-tab, multi-pane terminal workspace.
// Create via New, render via View, tear down via Close.
type Session struct {
	w   *gui.Window
	cfg Cfg

	tabs      []*Tab
	activeTab int
	nextTabID int

	prevOnEvent func(*gui.Event, *gui.Window)
}

// New creates a Session with a single tab containing a single terminal.
func New(w *gui.Window, cfg Cfg) (*Session, error) {
	if w == nil {
		return nil, errors.New("session.New: nil window")
	}
	s := &Session{
		w:           w,
		cfg:         cfg,
		prevOnEvent: w.OnEvent,
	}
	w.OnEvent = s.onWindowEvent
	s.registerCommands()

	_, err := s.addTab()
	if err != nil {
		return nil, err
	}
	return s, nil
}

// addTab creates a new tab and appends it.
func (s *Session) addTab() (*Tab, error) {
	tabID := "tab-" + strconv.Itoa(s.nextTabID)
	s.nextTabID++
	tab, err := newTab(s.w, s.cfg, tabID, s.onPaneExit, s.onPaneFocus, s.onPaneTitle)
	if err != nil {
		return nil, err
	}
	s.tabs = append(s.tabs, tab)
	s.activeTab = len(s.tabs) - 1
	return tab, nil
}

// removeTab closes all Terms in the tab and removes it. The last tab is
// replaced with a fresh one (the session is never empty). Returns false
// only when the replacement fails; the window has already been scheduled
// for close in that case and callers must not touch session state further.
func (s *Session) removeTab(idx int) bool {
	if idx < 0 || idx >= len(s.tabs) {
		return true
	}
	s.tabs[idx].closeAll()
	s.tabs = append(s.tabs[:idx], s.tabs[idx+1:]...)
	if len(s.tabs) == 0 {
		if _, err := s.addTab(); err != nil {
			s.w.Close()
			return false
		}
	}
	if s.activeTab >= len(s.tabs) {
		s.activeTab = len(s.tabs) - 1
	}
	return true
}

// closeTabAt closes the tab at idx, focuses a survivor, and refreshes
// the view. Used by the tab bar close button.
func (s *Session) closeTabAt(idx int) {
	wasActive := idx == s.activeTab
	if !s.removeTab(idx) {
		return // window is closing
	}
	if wasActive {
		// Focus the new active tab's pane.
		tab := s.tabs[s.activeTab]
		if t, ok := tab.terms[tab.focused]; ok {
			t.SetFocused(true)
			t.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
		}
	}
	s.refresh()
}

// onPaneFocus is called synchronously from Term.onClick when the user
// clicks on a terminal pane.
func (s *Session) onPaneFocus(leafID string) {
	for _, tab := range s.tabs {
		if _, ok := tab.terms[leafID]; ok {
			if tab.focused != leafID {
				s.focusPaneInTab(tab, leafID)
			}
			return
		}
	}
}

// onPaneExit is called via QueueCommand when a shell exits.
func (s *Session) onPaneExit(leafID string) {
	for _, tab := range s.tabs {
		if _, ok := tab.terms[leafID]; ok {
			s.closePaneInTab(tab, leafID)
			return
		}
	}
}

// closePaneInTab closes a pane within a specific tab.
func (s *Session) closePaneInTab(tab *Tab, leafID string) {
	tab.removePane(leafID)
	if tab.root.isLeaf() {
		removed := false
		for i, t := range s.tabs {
			if t == tab {
				if !s.removeTab(i) {
					return // window is closing
				}
				removed = true
				break
			}
		}
		if removed {
			// Tab was removed — focus the surviving tab's pane
			// and rebuild the view.
			tab := s.tabs[s.activeTab]
			if t, ok := tab.terms[tab.focused]; ok {
				t.SetFocused(true)
				t.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
			}
			s.refresh()
			return
		}
	} else {
		newRoot, survivor := removeLeaf(tab.root, leafID)
		if newRoot != nil {
			tab.root = newRoot
			tab.focused = survivor
			if t, ok := tab.terms[survivor]; ok {
				t.SetFocused(true)
				t.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
			}
		}
	}
	s.refresh()
}

// Close tears down all terminals and restores the original OnEvent.
func (s *Session) Close() error {
	s.w.OnEvent = s.prevOnEvent
	for _, tab := range s.tabs {
		tab.closeAll()
	}
	s.tabs = nil
	return nil
}

// ActivePane returns the focused *term.Term, or nil.
func (s *Session) ActivePane() *term.Term {
	if s.activeTab < 0 || s.activeTab >= len(s.tabs) {
		return nil
	}
	tab := s.tabs[s.activeTab]
	return tab.terms[tab.focused]
}

// tight creates a ContainerCfg with all inherited spacing zeroed.
func tight(sizing gui.Sizing) gui.ContainerCfg {
	return gui.ContainerCfg{
		Sizing:     sizing,
		Padding:    gui.NoPadding,
		Spacing:    gui.SomeF(0),
		SizeBorder: gui.NoBorder,
	}
}

// refresh updates the window title from the active tab's focused pane
// and schedules a view rebuild. Call after any state change that affects
// the title or layout. It also ensures the active pane has pane focus so
// the invariant "active terminal always owns IDFocus" holds regardless of
// which code path triggered the refresh.
func (s *Session) refresh() {
	if s.activeTab >= 0 && s.activeTab < len(s.tabs) {
		s.w.SetTitle(s.tabs[s.activeTab].focusedTitle())
		// Ensure the active pane owns IDFocus. No-op when already
		// correct — cheap atomic compare-and-swap.
		tab := s.tabs[s.activeTab]
		if t, ok := tab.terms[tab.focused]; ok {
			t.SetFocused(true)
		}
	}
	s.w.UpdateView(s.View)
}

// View returns the session's go-gui view tree.
func (s *Session) View(w *gui.Window) gui.View {
	ww, wh := w.WindowSize()
	if len(s.tabs) == 0 || s.activeTab >= len(s.tabs) {
		return gui.Column(tight(gui.FillFill))
	}
	tab := s.tabs[s.activeTab]

	split := s.splitView(tab.root, tab)

	outer := tight(gui.FixedFixed)
	outer.Width = float32(ww)
	outer.Height = float32(wh)
	if len(s.tabs) > 1 {
		area := tight(gui.FillFill)
		area.Content = []gui.View{split}
		outer.Content = []gui.View{s.tabBarView(), gui.Column(area)}
	} else {
		outer.Content = []gui.View{split}
	}
	return gui.Column(outer)
}

// focusPaneInTab switches focus to the given leaf.
func (s *Session) focusPaneInTab(tab *Tab, leafID string) {
	if leafID == "" || leafID == tab.focused {
		return
	}
	if prev, ok := tab.terms[tab.focused]; ok {
		prev.SetFocused(false)
		prev.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
	}
	tab.focused = leafID
	if next, ok := tab.terms[leafID]; ok {
		next.SetFocused(true)
		next.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
	}
	s.refresh()
}

// onWindowEvent routes window-level focus events to the active pane.
func (s *Session) onWindowEvent(e *gui.Event, w *gui.Window) {
	if e == nil {
		return
	}
	if e.Type == gui.EventFocused || e.Type == gui.EventUnfocused {
		if pane := s.ActivePane(); pane != nil {
			pane.HandleWindowEvent(e)
		}
	}
	if s.prevOnEvent != nil {
		s.prevOnEvent(e, w)
	}
}

// tabBarView renders the tab bar. Only called when 2+ tabs exist.
func (s *Session) tabBarView() gui.View {
	theme := gui.CurrentTheme()
	buttons := make([]gui.View, 0, len(s.tabs)*2)
	for i, tab := range s.tabs {
		if i > 0 {
			sep := tight(gui.FixedFill)
			sep.Width = 1
			sep.Color = theme.ColorBorder
			buttons = append(buttons, gui.Column(sep))
		}
		isActive := i == s.activeTab
		buttons = append(buttons, s.tabButton(tab, isActive, i))
	}
	bar := tight(gui.FillFit)
	bar.Color = theme.ColorPanel
	bar.Content = buttons
	return gui.Row(bar)
}

// tabButton renders a single tab.
func (s *Session) tabButton(tab *Tab, isActive bool, idx int) gui.View {
	theme := gui.CurrentTheme()
	bg := theme.ColorPanel
	style := theme.M5
	if isActive {
		bg = theme.ColorActive
	}
	title := tab.focusedTitle()
	title = truncateTitle(title, 30)
	inner := tight(gui.FillFit)
	inner.Padding = gui.SomeP(1, 6, 1, 6)

	content := []gui.View{
		gui.Text(gui.TextCfg{Text: title, TextStyle: style}),
	}
	if len(s.tabs) > 1 {
		// Fill spacer pushes × to the far right.
		fill := tight(gui.FillFit)
		closeBtn := tight(gui.FitFit)
		closeBtn.Padding = gui.SomeP(0, 0, 0, 4)
		closeBtn.OnClick = func(_ *gui.Layout, e *gui.Event, w *gui.Window) {
			s.closeTabAt(idx)
			e.IsHandled = true
		}
		closeBtn.Content = []gui.View{
			gui.Text(gui.TextCfg{
				Text:      "×",
				TextStyle: gui.TextStyle{Color: theme.ColorInterior, Size: style.Size},
			}),
		}
		content = append(content, gui.Row(fill), gui.Column(closeBtn))
	}
	inner.Content = content

	outer := tight(gui.FillFit)
	outer.Color = bg
	outer.OnClick = func(_ *gui.Layout, e *gui.Event, w *gui.Window) {
		s.activateTab(idx)
		e.IsHandled = true
	}
	outer.Content = []gui.View{gui.Row(inner)}
	return gui.Column(outer)
}

// activateTab switches to the tab at idx.
func (s *Session) activateTab(idx int) {
	if idx < 0 || idx >= len(s.tabs) || idx == s.activeTab {
		return
	}
	if old := s.activeTab; old >= 0 && old < len(s.tabs) {
		oldTab := s.tabs[old]
		if t, ok := oldTab.terms[oldTab.focused]; ok {
			t.SetFocused(false)
			t.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
		}
	}
	s.activeTab = idx
	tab := s.tabs[idx]
	if t, ok := tab.terms[tab.focused]; ok {
		t.SetFocused(true)
		t.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
	}
	s.refresh()
}

// splitView renders a split tree using FillFill throughout. The parent
// container determines actual dimensions, so the tree adapts correctly
// when chrome (tab bar, etc.) consumes space. All FillFill siblings in
// a split get equal space — matching the 0.5 default ratio.
func (s *Session) splitView(node *splitNode, tab *Tab) gui.View {
	if node.First != nil {
		const borderPx = float32(1)
		first := s.splitView(node.First, tab)
		second := s.splitView(node.Second, tab)

		if node.Dir == SplitVertical {
			border := tight(gui.FixedFill)
			border.Width = borderPx
			border.Color = gui.CurrentTheme().ColorBorder
			row := tight(gui.FillFill)
			row.Content = []gui.View{first, gui.Column(border), second}
			return gui.Row(row)
		}
		border := tight(gui.FixedFill)
		border.Height = borderPx
		border.Color = gui.CurrentTheme().ColorBorder
		col := tight(gui.FillFill)
		col.Content = []gui.View{first, gui.Column(border), second}
		return gui.Column(col)
	}
	// Leaf: FillFill so the parent split determines actual size.
	// IDFocus ensures Tab navigation reaches this pane.
	tm, ok := tab.terms[node.LeafID]
	if !ok {
		return gui.Column(tight(gui.FillFill))
	}
	leaf := tight(gui.FillFill)
	leaf.IDFocus = tm.FocusID()
	leaf.Content = []gui.View{tm.View(s.w)}
	return gui.Column(leaf)
}

// CycleTheme applies the next theme from cfg.Themes to every pane in
// every tab, wrapping after the last. The active pane's current theme
// determines the starting point. No-op when no themes are configured.
func (s *Session) CycleTheme() {
	if len(s.cfg.Themes) == 0 {
		return
	}
	cur := 0
	if p := s.ActivePane(); p != nil {
		curTheme := p.Theme()
		for i, nt := range s.cfg.Themes {
			if nt.Theme == curTheme {
				cur = i
				break
			}
		}
	}
	next := (cur + 1) % len(s.cfg.Themes)
	for _, tab := range s.tabs {
		for _, tm := range tab.terms {
			tm.SetTheme(s.cfg.Themes[next].Theme)
		}
	}
	s.w.UpdateWindow()
}

// onPaneTitle is called from the Term's OnTitle callback (via
// QueueCommand, so on the main thread). It refreshes the session
// view so the tab bar and window title reflect the new title.
func (s *Session) onPaneTitle(leafID, title string) {
	s.refresh()
}

// truncateTitle returns title truncated to max runes, appending "..." if
// truncation occurred. Walks the string once, counting runes and finding
// the byte offset for truncation in a single pass.
func truncateTitle(title string, max int) string {
	keep := max - 3
	if keep < 0 {
		keep = 0
	}
	n := 0
	cutAt := -1
	for i := range title {
		if n == keep {
			cutAt = i
		}
		n++
	}
	if cutAt >= 0 && n > max {
		return title[:cutAt] + "..."
	}
	return title
}
