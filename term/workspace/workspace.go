package workspace

import (
	"errors"
	"math"
	"strconv"

	glyph "github.com/go-gui-org/go-glyph"
	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// Cfg configures a Workspace. All fields are optional.
type Cfg struct {
	TextStyle gui.TextStyle
	Themes    []term.NamedTheme

	// ConfigPath is the path to the human-edited config file (INI-style).
	// When empty, the default location is used: $XDG_CONFIG_HOME/go-term/config,
	// ~/.config/go-term/config, or os.UserConfigDir()/go-term/config.
	// A missing file is silently ignored (defaults apply).
	ConfigPath string

	// ExitWhenLastShellExits closes the window when the last shell
	// process exits, rather than replacing it with a fresh tab.
	ExitWhenLastShellExits bool
}

// Workspace manages a multi-tab, multi-pane terminal workspace.
// Create via New, render via View, tear down via Close.
type Workspace struct {
	w   *gui.Window
	cfg Cfg

	tabs      []*Tab
	activeTab int
	nextTabID int

	commands    []gui.Command // metadata source for the help overlay
	helpVisible bool

	prevOnEvent func(*gui.Event, *gui.Window)
}

// New creates a Workspace with a single tab containing a single terminal.
func New(w *gui.Window, cfg Cfg) (*Workspace, error) {
	if w == nil {
		return nil, errors.New("workspace.New: nil window")
	}
	ws := &Workspace{
		w:           w,
		cfg:         cfg,
		prevOnEvent: w.OnEvent,
	}
	w.OnEvent = ws.onWindowEvent
	ws.registerCommands()

	_, err := ws.addTab()
	if err != nil {
		return nil, err
	}
	return ws, nil
}

// addTab creates a new tab and appends it.
func (ws *Workspace) addTab() (*Tab, error) {
	tabID := "tab-" + strconv.Itoa(ws.nextTabID)
	ws.nextTabID++
	tab, err := newTab(ws.w, ws.cfg, tabID, ws.onPaneExit, ws.onPaneFocus, ws.onPaneTitle)
	if err != nil {
		return nil, err
	}
	ws.tabs = append(ws.tabs, tab)
	ws.activeTab = len(ws.tabs) - 1
	return tab, nil
}

// removeTab closes all Terms in the tab and removes it. The last tab is
// replaced with a fresh one (the workspace is never empty). Returns false
// only when the replacement fails; the window has already been scheduled
// for close in that case and callers must not touch workspace state further.
func (ws *Workspace) removeTab(idx int) bool {
	if idx < 0 || idx >= len(ws.tabs) {
		return true
	}
	ws.tabs[idx].closeAll()
	ws.tabs = append(ws.tabs[:idx], ws.tabs[idx+1:]...)
	if len(ws.tabs) == 0 {
		if _, err := ws.addTab(); err != nil {
			ws.w.Close()
			return false
		}
	}
	if ws.activeTab >= len(ws.tabs) {
		ws.activeTab = len(ws.tabs) - 1
	}
	return true
}

// closeTabAt closes the tab at idx, focuses a survivor, and refreshes
// the view. Used by the tab bar close button.
func (ws *Workspace) closeTabAt(idx int) {
	wasActive := idx == ws.activeTab
	if !ws.removeTab(idx) {
		return // window is closing
	}
	if wasActive {
		// Focus the new active tab's pane.
		tab := ws.tabs[ws.activeTab]
		if t, ok := tab.terms[tab.focused]; ok {
			t.SetFocused(true)
			t.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
		}
	}
	ws.refresh()
}

// onPaneFocus is called synchronously from Term.onClick when the user
// clicks on a terminal pane.
func (ws *Workspace) onPaneFocus(leafID string) {
	for _, tab := range ws.tabs {
		if _, ok := tab.terms[leafID]; ok {
			if tab.focused != leafID {
				ws.focusPaneInTab(tab, leafID)
			}
			return
		}
	}
}

// onPaneExit is called via QueueCommand when a shell exits.
func (ws *Workspace) onPaneExit(leafID string) {
	for _, tab := range ws.tabs {
		if _, ok := tab.terms[leafID]; ok {
			ws.closePaneInTab(tab, leafID)
			return
		}
	}
}

// closePaneInTab closes a pane within a specific tab.
func (ws *Workspace) closePaneInTab(tab *Tab, leafID string) {
	tab.removePane(leafID)
	if tab.root.isLeaf() {
		// Last pane in this tab — if it's also the only tab and we
		// should exit when the last shell dies, close the window
		// instead of spawning a replacement tab.
		if ws.cfg.ExitWhenLastShellExits && len(ws.tabs) == 1 {
			ws.tabs = nil
			ws.w.Close()
			return
		}
		removed := false
		for i, t := range ws.tabs {
			if t == tab {
				if !ws.removeTab(i) {
					return // window is closing
				}
				removed = true
				break
			}
		}
		if removed {
			// Tab was removed — focus the surviving tab's pane
			// and rebuild the view.
			tab := ws.tabs[ws.activeTab]
			if t, ok := tab.terms[tab.focused]; ok {
				t.SetFocused(true)
				t.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
			}
			ws.refresh()
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
	ws.refresh()
}

// Close tears down all terminals and restores the original OnEvent.
func (ws *Workspace) Close() error {
	ws.w.OnEvent = ws.prevOnEvent
	for _, tab := range ws.tabs {
		tab.closeAll()
	}
	ws.tabs = nil
	return nil
}

// LiveTermCount returns the number of terminal panes with a live shell
// process across all tabs. Useful for confirm-before-quit prompts.
func (ws *Workspace) LiveTermCount() int {
	n := 0
	for _, tab := range ws.tabs {
		for _, tm := range tab.terms {
			if tm.Alive() {
				n++
			}
		}
	}
	return n
}

// ActivePane returns the focused *term.Term, or nil.
func (ws *Workspace) ActivePane() *term.Term {
	if ws.activeTab < 0 || ws.activeTab >= len(ws.tabs) {
		return nil
	}
	tab := ws.tabs[ws.activeTab]
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
// the invariant "active terminal always owns keyboard focus" holds regardless of
// which code path triggered the refresh.
func (ws *Workspace) refresh() {
	if ws.activeTab >= 0 && ws.activeTab < len(ws.tabs) {
		ws.w.SetTitle(ws.tabs[ws.activeTab].focusedTitle())
		// Ensure the active pane owns keyboard focus. No-op when already
		// correct — cheap atomic compare-and-swap.
		tab := ws.tabs[ws.activeTab]
		if t, ok := tab.terms[tab.focused]; ok {
			t.SetFocused(true)
		}
	}
	ws.w.UpdateView(ws.View)
}

// View returns the workspace's go-gui view tree.
func (ws *Workspace) View(w *gui.Window) gui.View {
	ww, wh := w.WindowSize()
	if len(ws.tabs) == 0 || ws.activeTab >= len(ws.tabs) {
		return gui.Column(tight(gui.FillFill))
	}
	tab := ws.tabs[ws.activeTab]

	// Thread the content-area pixel box into the split tree so each node
	// can honor its flex Ratio. The tab bar (only present with 2+ tabs)
	// consumes height above the panes.
	contentW := float32(ww)
	contentH := float32(wh)
	if len(ws.tabs) > 1 {
		contentH -= ws.tabBarHeight()
	}
	split := ws.splitView(tab.root, tab, contentW, contentH)

	outer := tight(gui.FixedFixed)
	outer.Width = float32(ww)
	outer.Height = float32(wh)
	var content []gui.View
	if len(ws.tabs) > 1 {
		area := tight(gui.FillFill)
		area.Content = []gui.View{split}
		content = []gui.View{ws.tabBarView(), gui.Column(area)}
	} else {
		content = []gui.View{split}
	}
	if ws.helpVisible {
		// Float children are excluded from normal flow, so the backdrop
		// and panel overlay the panes without disturbing their layout.
		content = append(content, ws.helpBackdrop(ww, wh), ws.helpPanel())
	}
	outer.Content = content
	return gui.Column(outer)
}

// ToggleHelp shows or hides the keyboard-shortcut overlay and rebuilds
// the view. Bound to Cmd+/ (toggle) and Escape (close, when visible).
func (ws *Workspace) ToggleHelp() {
	ws.helpVisible = !ws.helpVisible
	ws.refresh()
}

// helpBackdrop is a window-sized translucent float behind the panel that
// dims the panes and dismisses the overlay on click.
func (ws *Workspace) helpBackdrop(ww, wh int) gui.View {
	b := tight(gui.FixedFixed)
	b.Width = float32(ww)
	b.Height = float32(wh)
	b.Float = true
	b.FloatAnchor = gui.FloatTopLeft
	b.FloatTieOff = gui.FloatTopLeft
	b.FloatZIndex = 999
	b.Color = gui.RGBA(0, 0, 0, 120)
	b.OnClick = func(_ *gui.Layout, e *gui.Event, w *gui.Window) {
		ws.helpVisible = false
		ws.refresh()
		e.IsHandled = true
	}
	return gui.Column(b)
}

// helpPanel is the centered float listing every keyboard shortcut. The
// workspace section is generated from the live command registry; the
// terminal section from term.Shortcuts(). Neither is hand-maintained.
func (ws *Workspace) helpPanel() gui.View {
	theme := gui.CurrentTheme()
	rows := []gui.View{ws.helpHeader("Workspace", theme)}
	for _, cmd := range ws.commands {
		if cmd.Label == "" || !cmd.Shortcut.IsSet() {
			continue
		}
		rows = append(rows, ws.helpRow(cmd.Label, cmd.Shortcut.String(), theme))
	}
	rows = append(rows, ws.helpHeader("Terminal", theme))
	for _, s := range term.Shortcuts() {
		rows = append(rows, ws.helpRow(s.Label, s.Keys, theme))
	}

	panel := tight(gui.FixedFit)
	panel.Width = 460
	panel.Float = true
	panel.FloatAnchor = gui.FloatMiddleCenter
	panel.FloatTieOff = gui.FloatMiddleCenter
	panel.FloatZIndex = 1000
	panel.Color = theme.ColorPanel
	panel.ColorBorder = theme.ColorBorder
	panel.SizeBorder = gui.SomeF(1)
	panel.Radius = gui.SomeF(6)
	panel.Padding = gui.SomeP(14, 18, 14, 18)
	panel.Spacing = gui.SomeF(3)
	// Swallow clicks so they don't fall through to the backdrop, which
	// would dismiss the overlay when clicking inside the panel.
	panel.OnClick = func(_ *gui.Layout, e *gui.Event, _ *gui.Window) { e.IsHandled = true }
	panel.Content = rows
	return gui.Column(panel)
}

// helpHeader renders a section label with a thin divider below.
func (ws *Workspace) helpHeader(text string, theme gui.Theme) gui.View {
	style := theme.M5
	style.Typeface = glyph.TypefaceBold
	headerRow := tight(gui.FillFit)
	headerRow.Padding = gui.SomeP(6, 0, 2, 0)
	headerRow.Content = []gui.View{gui.Text(gui.TextCfg{Text: text, TextStyle: style})}

	col := tight(gui.FillFit)
	col.Spacing = gui.SomeF(0)
	col.Content = []gui.View{
		gui.Row(headerRow),
		gui.Rectangle(gui.RectangleCfg{
			Sizing: gui.FillFixed,
			Height: 1.2,
			Color:  theme.ColorBorder,
		}),
	}
	return gui.Column(col)
}

// helpRow renders one "label … keys" line with the keys right-aligned.
func (ws *Workspace) helpRow(label, keys string, theme gui.Theme) gui.View {
	labelStyle := theme.M5
	keyStyle := theme.M5
	fill := tight(gui.FillFit)
	row := tight(gui.FillFit)
	row.Content = []gui.View{
		gui.Text(gui.TextCfg{Text: label, TextStyle: labelStyle}),
		gui.Row(fill),
		gui.Text(gui.TextCfg{Text: keys, TextStyle: keyStyle}),
	}
	return gui.Row(row)
}

// focusPaneInTab switches focus to the given leaf.
func (ws *Workspace) focusPaneInTab(tab *Tab, leafID string) {
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
	ws.refresh()
}

// onWindowEvent routes window-level focus events to the active pane.
func (ws *Workspace) onWindowEvent(e *gui.Event, w *gui.Window) {
	if e == nil {
		return
	}
	if e.Type == gui.EventFocused || e.Type == gui.EventUnfocused {
		if pane := ws.ActivePane(); pane != nil {
			pane.HandleWindowEvent(e)
		}
	}
	if ws.prevOnEvent != nil {
		ws.prevOnEvent(e, w)
	}
}

// tabBarView renders the tab bar. Only called when 2+ tabs exist.
func (ws *Workspace) tabBarView() gui.View {
	theme := gui.CurrentTheme()
	buttons := make([]gui.View, 0, len(ws.tabs)*2)
	for i, tab := range ws.tabs {
		if i > 0 {
			sep := tight(gui.FixedFill)
			sep.Width = 1
			sep.Color = theme.ColorBorder
			buttons = append(buttons, gui.Column(sep))
		}
		isActive := i == ws.activeTab
		buttons = append(buttons, ws.tabButton(tab, isActive, i))
	}
	bar := tight(gui.FillFit)
	bar.Color = theme.ColorPanel
	bar.Content = buttons
	return gui.Row(bar)
}

// tabButton renders a single tab.
func (ws *Workspace) tabButton(tab *Tab, isActive bool, idx int) gui.View {
	theme := gui.CurrentTheme()
	bg := theme.ColorPanel
	style := theme.M5
	if isActive {
		bg = theme.ColorActive
	} else {
		style.Color = style.Color.WithOpacity(0.65)
	}
	title := tab.focusedTitle()
	title = truncateTitle(title, 30)
	inner := tight(gui.FillFit)
	inner.Padding = gui.SomeP(1, 6, 1, 6)

	content := []gui.View{
		gui.Text(gui.TextCfg{Text: title, TextStyle: style}),
	}
	// closeID names the "×" text shape so the hover handlers below can
	// recolor it by ID without disturbing the title text.
	closeID := tab.id + "-close"
	if len(ws.tabs) > 1 {
		// Fill spacer pushes × to the far right.
		fill := tight(gui.FillFit)

		// "×" is always laid out (reserving its slot avoids a reflow
		// when it appears), but hidden on inactive tabs until the tab is
		// hovered. The hover handlers mutate its color; the per-frame view
		// rebuild restores these defaults when the pointer leaves, so no
		// mouse-leave handling is needed.
		closeColor := theme.ColorInterior // active tab: always visible
		if !isActive {
			closeColor = gui.Color{} // inactive: transparent until hovered
		}

		closeBtn := tight(gui.FitFit)
		closeBtn.Padding = gui.SomeP(0, 0, 0, 4)
		closeBtn.OnClick = func(_ *gui.Layout, e *gui.Event, w *gui.Window) {
			ws.closeTabAt(idx)
			e.IsHandled = true
		}
		// Direct hover over × brightens it to the high-contrast title color.
		closeBtn.OnHover = func(layout *gui.Layout, _ *gui.Event, w *gui.Window) {
			w.SetMouseCursorPointingHand()
			setTextColorByID(layout, closeID, style.Color)
		}
		closeBtn.Content = []gui.View{
			gui.Text(gui.TextCfg{
				ID:        closeID,
				Text:      "×",
				TextStyle: gui.TextStyle{Color: closeColor, Size: style.Size},
			}),
		}
		content = append(content, gui.Row(fill), gui.Column(closeBtn))
	}
	inner.Content = content

	outer := tight(gui.FillFit)
	outer.Color = bg
	outer.OnClick = func(_ *gui.Layout, e *gui.Event, w *gui.Window) {
		ws.activateTab(idx)
		e.IsHandled = true
	}
	// Hovering anywhere on an inactive tab reveals a muted "×" — the title
	// text color at reduced opacity, so it stays legible against the panel.
	// The child closeBtn's own OnHover (above) takes over when the pointer
	// is directly on the glyph, brightening it to full opacity.
	if len(ws.tabs) > 1 && !isActive {
		revealColor := theme.M5.Color.WithOpacity(0.6)
		outer.OnHover = func(layout *gui.Layout, _ *gui.Event, w *gui.Window) {
			setTextColorByID(layout, closeID, revealColor)
		}
	}
	outer.Content = []gui.View{gui.Row(inner)}
	return gui.Column(outer)
}

// setTextColorByID recolors the text shape with the given ID found under
// layout, depth-first. Returns true once recolored. Used by the tab hover
// handlers to reveal/brighten the close "×" without touching the title.
func setTextColorByID(layout *gui.Layout, id string, c gui.Color) bool {
	if s := layout.Shape; s != nil && s.ID == id && s.TC != nil &&
		s.TC.TextStyle != nil {
		s.TC.TextStyle.Color = c
		return true
	}
	for i := range layout.Children {
		if setTextColorByID(&layout.Children[i], id, c) {
			return true
		}
	}
	return false
}

// activateTab switches to the tab at idx.
func (ws *Workspace) activateTab(idx int) {
	if idx < 0 || idx >= len(ws.tabs) || idx == ws.activeTab {
		return
	}
	if old := ws.activeTab; old >= 0 && old < len(ws.tabs) {
		oldTab := ws.tabs[old]
		if t, ok := oldTab.terms[oldTab.focused]; ok {
			t.SetFocused(false)
			t.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
		}
	}
	ws.activeTab = idx
	tab := ws.tabs[idx]
	if t, ok := tab.terms[tab.focused]; ok {
		t.SetFocused(true)
		t.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
	}
	ws.refresh()
}

// splitView renders a split tree into a box of boxW×boxH pixels, honoring
// each node's flex Ratio. The first child of a split is sized Fixed along
// the split axis (its ratio share, floored by ratioSplit); the second child
// Fills the remainder so rounding never leaves a gap. Window resize re-runs
// View with new dimensions, redistributing space proportionally.
func (ws *Workspace) splitView(node *splitNode, tab *Tab, boxW, boxH float32) gui.View {
	if node.First != nil {
		const borderPx = float32(1)
		if node.Dir == SplitVertical {
			avail := boxW - borderPx
			firstW := ratioSplit(avail, node.Ratio)
			first := ws.splitView(node.First, tab, firstW, boxH)
			second := ws.splitView(node.Second, tab, avail-firstW, boxH)

			firstC := tight(gui.FixedFill)
			firstC.Width = firstW
			firstC.Content = []gui.View{first}
			border := tight(gui.FixedFill)
			border.Width = borderPx
			border.Color = gui.CurrentTheme().ColorBorder
			secondC := tight(gui.FillFill)
			secondC.Content = []gui.View{second}

			row := tight(gui.FillFill)
			row.Content = []gui.View{gui.Column(firstC), gui.Column(border), gui.Column(secondC)}
			return gui.Row(row)
		}
		avail := boxH - borderPx
		firstH := ratioSplit(avail, node.Ratio)
		first := ws.splitView(node.First, tab, boxW, firstH)
		second := ws.splitView(node.Second, tab, boxW, avail-firstH)

		firstC := tight(gui.FillFixed)
		firstC.Height = firstH
		firstC.Content = []gui.View{first}
		border := tight(gui.FillFixed)
		border.Height = borderPx
		border.Color = gui.CurrentTheme().ColorBorder
		secondC := tight(gui.FillFill)
		secondC.Content = []gui.View{second}

		col := tight(gui.FillFill)
		col.Content = []gui.View{gui.Column(firstC), gui.Column(border), gui.Column(secondC)}
		return gui.Column(col)
	}
	// Leaf: FillFill so the enclosing Fixed slot determines actual size.
	// Focus ID ensures Tab navigation reaches this pane.
	tm, ok := tab.terms[node.LeafID]
	if !ok {
		return gui.Column(tight(gui.FillFill))
	}
	leaf := tight(gui.FillFill)
	leaf.Content = []gui.View{tm.View(ws.w)}
	return gui.Column(leaf)
}

// tabBarHeight estimates the pixel height the tab bar consumes: one line of
// the tab text style plus the 1px top and bottom padding in tabButton. A
// measurer that reports a non-finite or non-positive line height is ignored
// so the content-area height can never be poisoned by a bad measurement.
func (ws *Workspace) tabBarHeight() float32 {
	style := gui.CurrentTheme().M5
	if tm := ws.w.TextMeasurer(); tm != nil {
		if h := tm.FontHeight(style); h > 0 && !math.IsInf(float64(h), 1) {
			return h + 2
		}
	}
	if style.Size > 0 {
		return style.Size + 2
	}
	return 18
}

// CycleTheme applies the next theme from cfg.Themes to every pane in
// every tab, wrapping after the last. The active pane's current theme
// determines the starting point. No-op when no themes are configured.
func (ws *Workspace) CycleTheme() {
	if len(ws.cfg.Themes) == 0 {
		return
	}
	cur := 0
	if p := ws.ActivePane(); p != nil {
		curTheme := p.Theme()
		for i, nt := range ws.cfg.Themes {
			if nt.Theme == curTheme {
				cur = i
				break
			}
		}
	}
	next := (cur + 1) % len(ws.cfg.Themes)
	for _, tab := range ws.tabs {
		for _, tm := range tab.terms {
			tm.SetTheme(ws.cfg.Themes[next].Theme)
		}
	}
	ws.w.UpdateWindow()
}

// onPaneTitle is called from the Term's OnTitle callback (via
// QueueCommand, so on the main thread). It refreshes the workspace
// view so the tab bar and window title reflect the new title.
func (ws *Workspace) onPaneTitle(leafID, title string) {
	ws.refresh()
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
