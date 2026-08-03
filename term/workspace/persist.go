package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

const (
	maxWorkspaceSize = 1 << 20 // 1 MiB — real workspace JSON is < 100 KiB
	maxWorkspaceTabs = 100     // generous upper bound on tab count
	maxSplitDepth    = 64      // cap recursion in buildSplitTree
)

// persistedWorkspace is the top-level JSON schema (version 1).
type persistedWorkspace struct {
	Version   int            `json:"version"`
	ActiveTab int            `json:"activeTab"`
	Tabs      []persistedTab `json:"tabs"`
	// Theme is the user's active theme name, saved on quit and restored
	// on next launch. Empty when the default theme (Themes[0]) is active
	// or the theme list is empty.
	Theme string `json:"theme,omitempty"`
}

// persistedTab captures one tab's split tree and which leaf was active.
type persistedTab struct {
	ActiveLeaf string        `json:"activeLeaf"`
	Root       persistedNode `json:"root"`
}

// persistedNode is either a leaf (LeafID set) or a split (Dir/First/Second set).
type persistedNode struct {
	// Split fields.
	Dir    string         `json:"dir,omitempty"`
	Ratio  float32        `json:"ratio,omitempty"`
	First  *persistedNode `json:"first,omitempty"`
	Second *persistedNode `json:"second,omitempty"`
	// Leaf fields.
	LeafID string `json:"leafID,omitempty"`
	Cwd    string `json:"cwd,omitempty"`
	// FontSize is the pane's runtime zoom in points. Omitted (0) when the pane
	// is at the workspace default so unchanged panes keep the JSON clean; on
	// restore a zero size means "inherit the default."
	FontSize float32 `json:"fontSize,omitempty"`
}

// snapshot captures the current workspace state. Pure, no I/O.
func (ws *Workspace) snapshot() persistedWorkspace {
	defaultSize := ws.cfg.TextStyle.Size
	tabs := make([]persistedTab, len(ws.tabs))
	for i, tab := range ws.tabs {
		tabs[i] = persistedTab{
			ActiveLeaf: tab.focused,
			Root:       snapshotNode(tab.root, tab.terms, defaultSize),
		}
	}
	themeName := ws.persistableThemeName()
	return persistedWorkspace{
		Version:   1,
		ActiveTab: ws.activeTab,
		Tabs:      tabs,
		Theme:     themeName,
	}
}

// persistableThemeName returns the non-default theme name for snapshot,
// or "" when no theme is active or the default is selected. Uses
// cfg.opts.theme (which applyTheme and applyPaneTheme both maintain)
// rather than probing the active pane, so it works even when tabs have
// been cleared (last-shell-exit path).
func (ws *Workspace) persistableThemeName() string {
	if ws.cfg.opts.theme == nil || len(ws.cfg.Themes) == 0 {
		return ""
	}
	for _, nt := range ws.cfg.Themes {
		if nt.Theme == *ws.cfg.opts.theme {
			// Omit the default (Themes[0]) so a fresh workspace JSON stays
			// clean and only user-initiated theme changes leave a trace.
			if nt.Name == ws.cfg.Themes[0].Name {
				return ""
			}
			return nt.Name
		}
	}
	return ""
}

func snapshotNode(n *splitNode, terms map[string]*term.Term, defaultSize float32) persistedNode {
	if n.isLeaf() {
		node := persistedNode{LeafID: n.LeafID}
		if tm, ok := terms[n.LeafID]; ok {
			node.Cwd = cwdLocalPath(tm.Cwd())
			if fs := tm.FontSize(); fs != defaultSize {
				node.FontSize = fs
			}
		}
		return node
	}
	dir := "vertical"
	if n.Dir == SplitHorizontal {
		dir = "horizontal"
	}
	first := snapshotNode(n.First, terms, defaultSize)
	second := snapshotNode(n.Second, terms, defaultSize)
	return persistedNode{
		Dir:    dir,
		Ratio:  n.Ratio,
		First:  &first,
		Second: &second,
	}
}

// cwdLocalPath extracts the local filesystem path from an OSC 7 CWD value.
// Handles file://[host]/path → /path and bare /path → /path. The path portion
// of a file:// URI is percent-encoded by every shell integration that emits
// one, so it is decoded here — without that, any directory with a space or a
// non-ASCII name fails to resolve. A decode failure (stray '%') keeps the raw
// bytes: a slightly wrong path is caught later by the os.Stat guard, whereas
// dropping the value loses a usable CWD outright.
//
// On Windows the URI path carries a leading slash before the drive letter
// ("/C:/Users/x"); VolumeName is empty on every other OS, so the strip is
// inherently platform-correct.
func cwdLocalPath(cwd string) string {
	p := cwd
	if strings.HasPrefix(cwd, "file://") {
		rest := cwd[len("file://"):]
		slash := strings.IndexByte(rest, '/')
		if slash < 0 {
			return ""
		}
		p = rest[slash:]
		if dec, err := url.PathUnescape(p); err == nil {
			p = dec
		}
	}
	if len(p) > 1 && p[0] == '/' && filepath.VolumeName(p[1:]) != "" {
		p = p[1:]
	}
	return p
}

// Save writes the current workspace layout to path atomically (temp + rename).
// Intermediate directories are created as needed.
func (ws *Workspace) Save(path string) error {
	snap := ws.snapshot()
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("workspace.Save: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("workspace.Save: mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("workspace.Save: write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("workspace.Save: rename: %w", err)
	}
	return nil
}

// Restore reads path and rebuilds the workspace from the saved layout.
// Falls back to New on missing file, parse error, or version mismatch;
// the fallback is always logged but never fatal.
func Restore(w *gui.Window, cfg Cfg, path string) (*Workspace, error) {
	pw, err := loadPersistedWorkspace(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("workspace.Restore: %v; starting fresh", err)
		}
		return New(w, cfg)
	}
	if len(pw.Tabs) == 0 {
		// A zero-tab workspace (last-shell-exit path) starts fresh but
		// still carries a user's theme choice when one was persisted. The
		// choice is settled before the first tab is built rather than applied
		// after: a pane's COLORFGBG is fixed at spawn (see selectThemeByName).
		ws, err := newWorkspace(w, cfg)
		if err != nil {
			return nil, err
		}
		ws.selectThemeByName(pw.Theme)
		if _, err := ws.addTab(""); err != nil {
			return nil, err
		}
		return ws, nil
	}
	return restoreWorkspace(w, cfg, pw)
}

// loadPersistedWorkspace reads and validates the workspace JSON at path.
// Returns the parsed struct on success. An os.IsNotExist error signals a
// missing file (silent fallback); other errors carry a log-ready message.
func loadPersistedWorkspace(path string) (persistedWorkspace, error) {
	f, err := os.Open(path)
	if err != nil {
		return persistedWorkspace{}, err
	}
	defer func() { _ = f.Close() }()
	// Read at most maxWorkspaceSize+1 bytes to detect oversized files without
	// loading them fully into memory.
	data, err := io.ReadAll(io.LimitReader(f, maxWorkspaceSize+1))
	if err != nil {
		return persistedWorkspace{}, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxWorkspaceSize {
		return persistedWorkspace{}, fmt.Errorf("%s exceeds size limit (%d bytes)", path, maxWorkspaceSize)
	}
	var pw persistedWorkspace
	if err := json.Unmarshal(data, &pw); err != nil {
		return persistedWorkspace{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if pw.Version != 1 {
		return persistedWorkspace{}, fmt.Errorf("unknown version %d in %s", pw.Version, path)
	}
	if len(pw.Tabs) > maxWorkspaceTabs {
		return persistedWorkspace{}, fmt.Errorf("%s has %d tabs (limit %d)", path, len(pw.Tabs), maxWorkspaceTabs)
	}
	return pw, nil
}

func restoreWorkspace(w *gui.Window, cfg Cfg, pw persistedWorkspace) (*Workspace, error) {
	ws := &Workspace{
		w:           w,
		cfg:         cfg,
		baseCfg:     cfg,
		prevOnEvent: w.OnEvent,
	}
	// Resolve the config file (and register the command table) before any pane
	// is built, so restored panes get the configured font, theme, scrollback
	// and keybindings at construction rather than being corrected afterwards.
	ws.loadAndApplyConfig()
	// The user's last picker choice outranks the config file's theme, and it
	// has to win *here*, not at the applyThemeByName below: every pane about
	// to be built takes its COLORFGBG from ws.cfg and keeps it for the life of
	// the child process. See selectThemeByName.
	ws.selectThemeByName(pw.Theme)

	for _, pt := range pw.Tabs {
		tabID := "tab-" + strconv.Itoa(ws.nextTabID)
		tab, err := newTabFromPersisted(w, ws.cfg, tabID, pt, ws.hooks())
		if err != nil {
			for _, t := range ws.tabs {
				t.closeAll()
			}
			log.Printf("workspace.Restore: build tab: %v; starting fresh", err)
			return New(w, cfg)
		}
		ws.nextTabID++
		ws.tabs = append(ws.tabs, tab)
	}

	w.OnEvent = ws.onWindowEvent

	activeTab := pw.ActiveTab
	if activeTab < 0 || activeTab >= len(ws.tabs) {
		activeTab = 0
	}
	ws.activeTab = activeTab

	// Push the same choice at the panes. Redundant with selectThemeByName for
	// panes that built cleanly — they already have it — and the backstop for
	// anything constructed off a different path.
	ws.applyThemeByName(pw.Theme)

	tab := ws.tabs[ws.activeTab]
	if t, ok := tab.terms[tab.focused]; ok {
		t.SetFocused(true)
		t.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
	}
	return ws, nil
}

// newTabFromPersisted builds a Tab from persisted data, spawning each
// pane with its saved CWD. Leaf IDs are regenerated deterministically
// (tabID-pane-N in depth-first order); the persisted activeLeaf is
// mapped to the new ID via oldID→newID.
func newTabFromPersisted(
	w *gui.Window,
	cfg Cfg,
	tabID string,
	pt persistedTab,
	hooks paneHooks,
) (*Tab, error) {
	t := &Tab{
		id:     tabID,
		terms:  make(map[string]*term.Term),
		titles: make(map[string]string),
	}
	idMap := make(map[string]string) // persisted leafID → new leafID
	nextID := 0
	var spawnErr error
	t.root = buildSplitTree(tabID, &pt.Root, func(leafID, cwd string, fontSize float32) {
		if spawnErr != nil {
			return
		}
		if err := t.addPane(w, cfg, leafID, cwd, fontSize, hooks); err != nil {
			spawnErr = err
		}
	}, idMap, &nextID)

	if t.root == nil || spawnErr != nil {
		t.closeAll()
		if spawnErr != nil {
			return nil, spawnErr
		}
		return nil, fmt.Errorf("malformed split tree for tab %s", tabID)
	}

	t.nextID = nextID

	// Wire focused pane: translate the persisted leaf ID to the new one.
	if newID, ok := idMap[pt.ActiveLeaf]; ok {
		if _, exists := t.terms[newID]; exists {
			t.focused = newID
		}
	}
	if t.focused == "" {
		t.focused = firstLeafID(t.root)
	}
	return t, nil
}

// buildSplitTree recursively rebuilds a splitNode tree from persisted data.
// For each leaf it calls spawn(newLeafID, cwd, fontSize) and records the
// old→new ID mapping in idMap. nextID is incremented for each leaf (depth-first
// order). A zero fontSize means the pane was unzoomed → inherit the default.
func buildSplitTree(
	tabID string,
	pn *persistedNode,
	spawn func(leafID, cwd string, fontSize float32),
	idMap map[string]string,
	nextID *int,
) *splitNode {
	var recurse func(*persistedNode, int) *splitNode
	recurse = func(pn *persistedNode, depth int) *splitNode {
		if pn == nil || depth > maxSplitDepth {
			return nil
		}
		if pn.LeafID != "" {
			newID := tabID + "-pane-" + strconv.Itoa(*nextID)
			*nextID++
			idMap[pn.LeafID] = newID
			spawn(newID, pn.Cwd, pn.FontSize)
			return leaf(newID)
		}
		if pn.First == nil || pn.Second == nil {
			return nil
		}
		dir := SplitVertical
		if pn.Dir == "horizontal" {
			dir = SplitHorizontal
		}
		ratio := clampRatio(pn.Ratio)
		first := recurse(pn.First, depth+1)
		second := recurse(pn.Second, depth+1)
		if first == nil || second == nil {
			return nil
		}
		return split(dir, ratio, first, second)
	}
	return recurse(pn, 0)
}

// DefaultWorkspacePath returns the default workspace JSON path
// ($XDG_CONFIG_HOME/go-term/workspace.json or equivalent). The file may not
// exist; callers should check with os.Stat before using it.
func DefaultWorkspacePath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workspace.json"), nil
}

// DefaultConfigPath returns the path loadConfig reads when the embedder
// leaves Cfg.ConfigPath empty. Exported so an embedder can show or open the
// file (a "Preferences"/"Open config" menu item) without re-deriving the
// resolution order — see docs/config.md. Empty when no config dir resolves.
func DefaultConfigPath() string {
	dir, err := configDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "config")
}

// configDir returns the directory for go-term configuration files.
// Resolution order:
//  1. $XDG_CONFIG_HOME/go-term
//  2. ~/.config/go-term (when ~/.config exists)
//  3. os.UserConfigDir()/go-term
func configDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "go-term"), nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		dir := filepath.Join(home, ".config")
		if _, err := os.Stat(dir); err == nil {
			return filepath.Join(dir, "go-term"), nil
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "go-term"), nil
}
