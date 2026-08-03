package workspace

import (
	"path/filepath"
	"strconv"
	"time"

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

	// lastActivity is when any pane in this tab last produced output, and
	// bell whether one rang the bell. Both accumulate only while the tab is
	// in the background and are cleared when it becomes active — an
	// indicator on the tab you are already reading has nothing to tell you.
	// The silence indicator is derived from lastActivity rather than stored;
	// see tabIndicator.
	lastActivity time.Time
	bell         bool

	// broadcast mirrors user input from the focused pane to every other
	// live pane in *this* tab. Toggled unconditionally, including on a
	// single-pane tab — the status pill, not a pane-count rule, is what
	// keeps the mode from being a surprise. Deliberately not persisted:
	// a restored workspace starts with it off (see broadcast.go).
	broadcast bool
}

// paneHooks bundles the per-pane callbacks handed to every Term a Tab builds.
// Grouped rather than passed individually because every construction path
// (new tab, split, restore) needs the whole set, and threading them one at a
// time grew each signature with the feature list.
type paneHooks struct {
	onExit     func(leafID string)
	onFocus    func(leafID string)
	onTitle    func(leafID, title string)
	onInput    func(leafID string, p []byte, kind term.InputKind)
	onActivity func(leafID string, kind term.ActivityKind)
}

// newTab creates a Tab with a single leaf running a shell. dir sets the
// shell's working directory (empty = inherit the process CWD).
func newTab(w *gui.Window, cfg Cfg, tabID, dir string, hooks paneHooks) (*Tab, error) {
	leafID := tabID + "-pane-0"
	t := &Tab{
		id:     tabID,
		root:   leaf(leafID),
		terms:  make(map[string]*term.Term),
		titles: make(map[string]string),
		nextID: 1,
	}
	tm, err := term.New(w, t.termCfg(w, cfg, leafID, dir, hooks))
	if err != nil {
		return nil, err
	}
	applyPaneTheme(tm, cfg)
	t.terms[leafID] = tm
	t.titles[leafID] = leafID
	t.focused = leafID
	return t, nil
}

// termCfg builds a term.Cfg for a pane. dir sets the child's working
// directory (empty = inherit process CWD).
//
// Every pane path — new tab, split, restore — funnels through here, so this is
// where an untrustworthy dir is filtered. Both sources are outside our control:
// OSC 7 is whatever the child process printed, and a restored dir comes from a
// hand-editable workspace.json. A relative value would resolve against the
// *process* CWD, spawning the shell somewhere the user never named, so only
// absolute paths survive. A non-existent absolute path is left to term's own
// os.Stat guard, which falls back to $HOME.
func (t *Tab) termCfg(w *gui.Window, cfg Cfg, panelID, dir string, hooks paneHooks) term.Cfg {
	if dir != "" && !filepath.IsAbs(dir) {
		dir = ""
	}
	return term.Cfg{
		NoWindowHandler: true,
		TextStyle:       cfg.TextStyle,
		Themes:          paneThemes(cfg),
		Dir:             dir,
		RecordInput:     cfg.RecordInput,
		DownloadDir:     cfg.DownloadDir,
		ScrollbackRows:  cfg.opts.scrollback,
		ScrollbarWidth:  cfg.opts.scrollbar,
		MinimumContrast: cfg.opts.minContrast,
		BellMode:        cfg.opts.bell,
		NotifyAfter:     cfg.opts.notifyAfter,
		KeyBindings:     cfg.opts.keys,

		MiddleClickPaste: cfg.opts.middleClickPaste,
		OnTitle: func(title string) {
			w.QueueCommand(func(w *gui.Window) {
				t.titles[panelID] = title
				hooks.onTitle(panelID, title)
			})
		},
		OnExit: func() {
			w.QueueCommand(func(w *gui.Window) {
				hooks.onExit(panelID)
			})
		},
		OnClickFocus: func() {
			hooks.onFocus(panelID)
		},
		// Runs synchronously on the main thread inside the pane's key or
		// paste handler; the workspace mirrors it to siblings when this
		// tab is broadcasting.
		OnInput: func(p []byte, kind term.InputKind) {
			hooks.onInput(panelID, p, kind)
		},
		// Already on the main thread (Term dispatches it through its own
		// queueCommand), so no further hop is needed here.
		OnActivity: func(kind term.ActivityKind) {
			hooks.onActivity(panelID, kind)
		},
	}
}

// addPane creates a new Term as a leaf in this tab. dir sets the child's
// working directory (empty = inherit process CWD). A non-zero fontSize applies
// a per-pane zoom via Term.SetFontSize — an override layered over the workspace
// default, so Cmd+0 in the pane still resets to that default (not the inherited
// size). Zero inherits the default with no override.
func (t *Tab) addPane(
	w *gui.Window,
	cfg Cfg,
	leafID string,
	dir string,
	fontSize float32,
	hooks paneHooks,
) error {
	tm, err := term.New(w, t.termCfg(w, cfg, leafID, dir, hooks))
	if err != nil {
		return err
	}
	applyPaneTheme(tm, cfg)
	if fontSize > 0 {
		tm.SetFontSize(fontSize)
	}
	t.terms[leafID] = tm
	t.titles[leafID] = leafID
	return nil
}

// paneThemes returns the theme list a pane's term.Cfg gets: just the selected
// theme, or the workspace's first when none is selected.
//
// term reads Themes[0] twice and only one of them can be corrected afterwards:
// it seeds the grid (which applyPaneTheme then overrides) *and* it decides the
// COLORFGBG the child is spawned with, which cannot be changed in a running
// process. Handing the pane the unselected theme meant a user who set
// `theme = Solarized Light` got light cells and a child told it was running on
// a dark background — the exact question COLORFGBG exists to answer.
//
// Only element 0 is ever read, so the rest is dead weight: term uses
// Cfg.Themes for nothing else, and the workspace's own list (browser,
// persisted name) reads ws.cfg.Themes, which is untouched. With ~600 bundled
// themes, returning the whole slice meant copying it per pane for one lookup.
func paneThemes(cfg Cfg) []term.NamedTheme {
	if cfg.opts.theme == nil {
		if len(cfg.Themes) == 0 {
			return nil
		}
		return cfg.Themes[:1:1]
	}
	return []term.NamedTheme{{Name: cfg.opts.themeName, Theme: *cfg.opts.theme}}
}

// applyPaneTheme applies the config file's [general] theme to a freshly built
// pane. Redundant with paneThemes for a theme that is in the list, and the
// backstop for one that is not — opts.theme is a value, not an index, so
// nothing guarantees it appears there.
func applyPaneTheme(tm *term.Term, cfg Cfg) {
	if cfg.opts.theme != nil {
		tm.SetTheme(*cfg.opts.theme)
	}
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
