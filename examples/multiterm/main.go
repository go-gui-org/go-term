// Package main is a multi-terminal docking example demonstrating multiple
// term.Term widgets arranged in a go-gui DockLayout with native menus and
// keyboard shortcuts.
package main

import (
	"fmt"
	"log"
	"sort"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-gui/gui/backend"
	"github.com/go-gui-org/go-term/term"
)

const (
	groupMain  = "main"
	groupEmpty = "__dock_empty__"
	maxTitle   = 256
)

// AppState holds the multi-terminal application state persisted across frames.
type AppState struct {
	Root    *gui.DockNode
	Terms   map[string]*term.Term
	Titles  map[string]string
	Focused string
	NextID  int

	// Cached theme values (set once at init; theme never changes).
	colorTab       gui.Color
	colorTabActive gui.Color
}

func main() {
	gui.SetTheme(gui.ThemeDark.WithBorders(true))

	theme := gui.CurrentTheme()
	state := &AppState{
		Root:    initialLayout(),
		Terms:   map[string]*term.Term{},
		Titles:  map[string]string{},
		Focused: "term-0",
		NextID:  1,

		colorTab:       theme.ColorBackground,
		colorTabActive: theme.ColorPanel,
	}

	app := gui.NewApp()
	w := gui.NewWindow(gui.WindowCfg{
		State:  state,
		Title:  "go-term",
		Width:  900,
		Height: 600,
		OnCloseRequest: func(w *gui.Window) {
			confirmQuit(w)
		},
		OnInit: func(w *gui.Window) {
			w.UpdateView(mainView)

			_ = w.RegisterCommands(
				gui.Command{
					ID: "term.new", Label: "New Terminal",
					Shortcut: gui.Shortcut{Key: gui.KeyT, Modifiers: gui.ModSuper},
					Global:   true,
					Execute: func(_ *gui.Event, w *gui.Window) {
						st := gui.State[AppState](w)
						id := fmt.Sprintf("term-%d", st.NextID)
						t, err := term.New(w, newTermCfg(id, w))
						if err != nil {
							log.Printf("term.New: %v", err)
							return
						}
						st.NextID++
						st.Terms[id] = t
						st.Titles[id] = id
						if g, ok := gui.DockTreeFindGroupByPanel(st.Root, st.Focused); ok {
							st.Root = gui.DockTreeAddTab(st.Root, g.ID, id)
						} else {
							st.Root = gui.DockPanelGroup(groupMain, []string{id}, id)
						}
						focusPanel(st, id, w)
					},
				},
				gui.Command{
					ID: "term.close", Label: "Close Terminal",
					Shortcut: gui.Shortcut{Key: gui.KeyW, Modifiers: gui.ModSuper},
					Global:   true,
					Execute: func(_ *gui.Event, w *gui.Window) {
						app := gui.State[AppState](w)
						closePanel(app, app.Focused, w)
					},
				},
				gui.Command{
					ID: "term.nextTab", Label: "Next Tab",
					Shortcut: gui.Shortcut{Key: gui.KeyRightBracket, Modifiers: gui.ModSuper | gui.ModShift},
					Global:   true,
					Execute: func(_ *gui.Event, w *gui.Window) {
						cycleTab(gui.State[AppState](w), +1, w)
					},
				},
				gui.Command{
					ID: "term.prevTab", Label: "Previous Tab",
					Shortcut: gui.Shortcut{Key: gui.KeyLeftBracket, Modifiers: gui.ModSuper | gui.ModShift},
					Global:   true,
					Execute: func(_ *gui.Event, w *gui.Window) {
						cycleTab(gui.State[AppState](w), -1, w)
					},
				},
				gui.Command{
					ID: "file.quit", Label: "Quit",
					Shortcut: gui.Shortcut{Key: gui.KeyQ, Modifiers: gui.ModSuper},
					Global:   true,
					Execute: func(_ *gui.Event, w *gui.Window) {
						confirmQuit(w)
					},
				},
			)

			app.SetNativeMenubar(gui.NativeMenubarCfg{
				AppName:         "go-term",
				IncludeEditMenu: true,
				Menus: []gui.NativeMenuCfg{
					{
						Title: "File",
						Items: []gui.NativeMenuItemCfg{
							{Text: "New Terminal", CommandID: "term.new",
								Shortcut: gui.Shortcut{Key: gui.KeyT, Modifiers: gui.ModSuper}},
							{Text: "Close Terminal", CommandID: "term.close",
								Shortcut: gui.Shortcut{Key: gui.KeyW, Modifiers: gui.ModSuper}},
							{Separator: true},
							{Text: "Quit", CommandID: "file.quit",
								Shortcut: gui.Shortcut{Key: gui.KeyQ, Modifiers: gui.ModSuper}},
						},
					},
					{
						Title: "Window",
						Items: []gui.NativeMenuItemCfg{
							{Text: "Next Tab", CommandID: "term.nextTab",
								Shortcut: gui.Shortcut{Key: gui.KeyRightBracket, Modifiers: gui.ModSuper | gui.ModShift}},
							{Text: "Previous Tab", CommandID: "term.prevTab",
								Shortcut: gui.Shortcut{Key: gui.KeyLeftBracket, Modifiers: gui.ModSuper | gui.ModShift}},
						},
					},
				},
			})

			// Create the first terminal.
			id := "term-0"
			t, err := term.New(w, newTermCfg(id, w))
			if err != nil {
				log.Fatalf("term.New: %v", err)
			}
			st := gui.State[AppState](w)
			st.Terms[id] = t
			st.Titles[id] = id
			st.Focused = id

			// Route window-level events to the focused terminal.
			w.OnEvent = func(e *gui.Event, w *gui.Window) {
				st := gui.State[AppState](w)
				if t, ok := st.Terms[st.Focused]; ok {
					t.HandleWindowEvent(e)
				}
			}
		},
	})
	defer closeAllTerms(state)
	backend.RunApp(app, w)
}

// initialLayout returns a single panel group containing term-0.
func initialLayout() *gui.DockNode {
	return gui.DockPanelGroup(groupMain, []string{"term-0"}, "term-0")
}

// newTermCfg returns a term.Cfg for the given panel ID. NoWindowHandler is
// set because the example owns window-level event dispatch. OnTitle updates
// the tab label; OnExit auto-closes the tab when the shell dies.
func newTermCfg(panelID string, w *gui.Window) term.Cfg {
	return term.Cfg{
		NoWindowHandler: true,
		TextStyle:       gui.TextStyle{Family: "JetBrainsMono Nerd Font", Size: 12},
		OnTitle: func(title string) {
			if len(title) > maxTitle {
				title = title[:maxTitle]
			}
			w.QueueCommand(func(w *gui.Window) {
				app := gui.State[AppState](w)
				app.Titles[panelID] = title
				w.UpdateWindow()
			})
		},
		OnExit: func() {
			w.QueueCommand(func(w *gui.Window) {
				app := gui.State[AppState](w)
				closePanel(app, panelID, w)
			})
		},
	}
}

// mainView is the window's view generator. It rebuilds the dock layout each
// frame from the current application state.
func mainView(w *gui.Window) gui.View {
	ww, wh := w.WindowSize()
	app := gui.State[AppState](w)

	panels := collectPanelDefs(app.Root, app.Terms, app.Titles, w)

	return gui.Column(gui.ContainerCfg{
		Width:   float32(ww),
		Height:  float32(wh),
		Sizing:  gui.FixedFixed,
		Padding: gui.NoPadding,
		Content: []gui.View{
			gui.DockLayout(gui.DockLayoutCfg{
				Root:           app.Root,
				Panels:         panels,
				HideSingleTab:  true,
				ColorTab:       app.colorTab,
				ColorTabActive: app.colorTabActive,
				OnLayoutChange: func(root *gui.DockNode, w *gui.Window) {
					gui.State[AppState](w).Root = root
				},
				OnPanelSelect: func(groupID, panelID string, w *gui.Window) {
					focusPanel(gui.State[AppState](w), panelID, w)
				},
				OnPanelClose: func(panelID string, w *gui.Window) {
					closePanel(gui.State[AppState](w), panelID, w)
				},
			}),
		},
	})
}

// collectPanelDefs builds DockPanelDef entries for every panel ID found in
// the dock tree that is still present in the terms map. Fuses tree traversal
// with definition construction to avoid an intermediate ID slice.
func collectPanelDefs(
	root *gui.DockNode,
	terms map[string]*term.Term,
	titles map[string]string,
	w *gui.Window,
) []gui.DockPanelDef {
	if root == nil {
		return nil
	}
	closable := len(terms) > 1
	var panels []gui.DockPanelDef
	for _, g := range gui.DockTreeCollectPanelNodes(root) {
		for _, id := range g.PanelIDs {
			t, ok := terms[id]
			if !ok {
				continue
			}
			label := titles[id]
			if label == "" {
				label = id
			}
			panels = append(panels, gui.DockPanelDef{
				ID:       id,
				Label:    label,
				Content:  []gui.View{t.View(w)},
				Closable: closable,
			})
		}
	}
	return panels
}

// focusPanel switches input focus to the given panel. It is the single
// focus-change path, used by OnPanelSelect, new terminal, close terminal,
// and tab cycling.
func focusPanel(app *AppState, panelID string, w *gui.Window) {
	if panelID == "" || panelID == app.Focused {
		return
	}
	if _, ok := app.Terms[panelID]; !ok {
		return
	}
	if prev, ok := app.Terms[app.Focused]; ok {
		prev.SetFocused(false)
		prev.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
	}
	app.Focused = panelID
	if g, ok := gui.DockTreeFindGroupByPanel(app.Root, panelID); ok {
		app.Root = gui.DockTreeSelectPanel(app.Root, g.ID, panelID)
	}
	if t, ok := app.Terms[panelID]; ok {
		t.SetFocused(true)
		t.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
	}
}

// closePanel closes a terminal tab and updates state. It is the single
// close implementation shared by ⌘W, OnPanelClose, and OnExit.
func closePanel(app *AppState, panelID string, w *gui.Window) {
	if panelID == "" {
		return
	}
	t, ok := app.Terms[panelID]
	if !ok {
		return
	}

	// Last terminal → quit immediately without confirmation.
	if len(app.Terms) == 1 {
		_ = t.Close()
		delete(app.Terms, panelID)
		delete(app.Titles, panelID)
		w.Close()
		return
	}

	// Find the group before mutating the tree.
	group, groupOK := gui.DockTreeFindGroupByPanel(app.Root, panelID)

	_ = t.Close()
	delete(app.Terms, panelID)
	delete(app.Titles, panelID)
	app.Root = gui.DockTreeRemovePanel(app.Root, panelID)

	// Closing a background tab — focus stays on the current panel.
	if panelID != app.Focused {
		return
	}

	// Pick a replacement focus: prefer same group, fall back to any term.
	var newFocus string
	if groupOK {
		for _, id := range group.PanelIDs {
			if id != panelID {
				if _, ok := app.Terms[id]; ok {
					newFocus = id
					break
				}
			}
		}
	}
	if newFocus == "" {
		for id := range app.Terms {
			newFocus = id
			break
		}
	}

	// Orphan recovery: focused panel was not in the dock tree.
	if !groupOK {
		app.Focused = newFocus
		rebuildTreeFromTerms(app)
		if newFocus != "" {
			focusPanel(app, newFocus, w)
		}
		return
	}

	focusPanel(app, newFocus, w)
}

// cycleTab moves focus to the next (+1) or previous (-1) tab in the
// focused panel's group.
func cycleTab(app *AppState, dir int, w *gui.Window) {
	g, ok := gui.DockTreeFindGroupByPanel(app.Root, app.Focused)
	if !ok || len(g.PanelIDs) <= 1 {
		return
	}
	n := len(g.PanelIDs)
	for i, id := range g.PanelIDs {
		if id != app.Focused {
			continue
		}
		i = (i + dir) % n
		if i < 0 {
			i += n
		}
		focusPanel(app, g.PanelIDs[i], w)
		return
	}
}

// confirmQuit shows a native confirmation dialog if terminals are still
// running, or quits immediately if none are alive.
func confirmQuit(w *gui.Window) {
	app := gui.State[AppState](w)
	alive := 0
	for _, t := range app.Terms {
		if t.Alive() {
			alive++
		}
	}
	if alive == 0 {
		closeAllTerms(app)
		w.Close()
		return
	}
	body := fmt.Sprintf("%d terminal(s) are still running. Quit anyway?", alive)
	w.NativeConfirmDialog(gui.NativeConfirmDialogCfg{
		Title: "Quit go-term",
		Body:  body,
		Level: gui.AlertWarning,
		OnDone: func(r gui.NativeAlertResult, w *gui.Window) {
			if r.Status != gui.DialogOK {
				return
			}
			closeAllTerms(app)
			w.Close()
		},
	})
}

// closeAllTerms closes every terminal and clears state. Used during quit.
func closeAllTerms(app *AppState) {
	for id, t := range app.Terms {
		_ = t.Close()
		delete(app.Terms, id)
		delete(app.Titles, id)
	}
	app.Root = gui.DockPanelGroup(groupEmpty, nil, "")
	app.Focused = ""
}

// rebuildTreeFromTerms reconstructs the dock tree from the surviving terms.
// Used as a fallback when state and tree drift apart (e.g. orphaned panel).
func rebuildTreeFromTerms(app *AppState) {
	ids := make([]string, 0, len(app.Terms))
	for id := range app.Terms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		app.Root = gui.DockPanelGroup(groupEmpty, nil, "")
		app.Focused = ""
		return
	}
	selected := ids[0]
	if app.Focused != "" {
		if _, ok := app.Terms[app.Focused]; ok {
			selected = app.Focused
		}
	}
	app.Root = gui.DockPanelGroup(groupMain, ids, selected)
}
