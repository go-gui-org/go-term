package session

import (
	"strconv"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// Tab is a single workspace tab containing a split tree of terminals.
type Tab struct {
	id      string
	root    *splitNode            // split tree within this tab
	terms   map[string]*term.Term // leafID → Term
	titles  map[string]string     // leafID → OSC 0/2 title
	focused string                // leafID of focused pane
	nextID  int                   // monotonic leaf ID counter
}

// newTab creates a Tab with a single leaf running a shell.
func newTab(
	w *gui.Window,
	cfg Cfg,
	tabID string,
	onExit func(leafID string),
	onFocus func(leafID string),
	onTitle func(leafID, title string),
) (*Tab, error) {
	leafID := tabID + "-pane-0"
	t := &Tab{
		id:     tabID,
		root:   leaf(leafID),
		terms:  make(map[string]*term.Term),
		titles: make(map[string]string),
		nextID: 1,
	}
	tm, err := term.New(w, t.termCfg(w, cfg, leafID, onExit, onFocus, onTitle))
	if err != nil {
		return nil, err
	}
	t.terms[leafID] = tm
	t.titles[leafID] = leafID
	t.focused = leafID
	return t, nil
}

// termCfg builds a term.Cfg for a panel in this tab.
func (t *Tab) termCfg(
	w *gui.Window,
	cfg Cfg,
	panelID string,
	onExit func(leafID string),
	onFocus func(leafID string),
	onTitle func(leafID, title string),
) term.Cfg {
	return term.Cfg{
		NoWindowHandler: true,
		TextStyle:       cfg.TextStyle,
		Themes:          cfg.Themes,
		OnTitle: func(title string) {
			w.QueueCommand(func(w *gui.Window) {
				t.titles[panelID] = title
				onTitle(panelID, title)
			})
		},
		OnExit: func() {
			w.QueueCommand(func(w *gui.Window) {
				onExit(panelID)
			})
		},
		OnClickFocus: func() {
			onFocus(panelID)
		},
	}
}

// addPane creates a new Term as a leaf in this tab.
func (t *Tab) addPane(
	w *gui.Window,
	cfg Cfg,
	leafID string,
	onExit func(leafID string),
	onFocus func(leafID string),
	onTitle func(leafID, title string),
) error {
	tm, err := term.New(w, t.termCfg(w, cfg, leafID, onExit, onFocus, onTitle))
	if err != nil {
		return err
	}
	t.terms[leafID] = tm
	t.titles[leafID] = leafID
	return nil
}

// removePane closes a Term and removes it from the tab's state.
func (t *Tab) removePane(leafID string) {
	if tm, ok := t.terms[leafID]; ok {
		_ = tm.Close()
		delete(t.terms, leafID)
		delete(t.titles, leafID)
	}
}

// closeAll closes all Terms in the tab.
func (t *Tab) closeAll() {
	for leafID := range t.terms {
		t.removePane(leafID)
	}
}

// focusedTitle returns the OSC title of the focused pane, or the tab's
// fallback title.
func (t *Tab) focusedTitle() string {
	if title, ok := t.titles[t.focused]; ok && title != "" {
		return title
	}
	return "go-term"
}

// allocLeafID allocates a unique leaf ID within this tab.
func (t *Tab) allocLeafID() string {
	id := t.nextID
	t.nextID++
	return t.id + "-pane-" + strconv.Itoa(id)
}
