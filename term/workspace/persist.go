package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
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
}

// snapshot captures the current workspace state. Pure, no I/O.
func (ws *Workspace) snapshot() persistedWorkspace {
	tabs := make([]persistedTab, len(ws.tabs))
	for i, tab := range ws.tabs {
		tabs[i] = persistedTab{
			ActiveLeaf: tab.focused,
			Root:       snapshotNode(tab.root, tab.terms),
		}
	}
	return persistedWorkspace{
		Version:   1,
		ActiveTab: ws.activeTab,
		Tabs:      tabs,
	}
}

func snapshotNode(n *splitNode, terms map[string]*term.Term) persistedNode {
	if n.isLeaf() {
		cwd := ""
		if tm, ok := terms[n.LeafID]; ok {
			cwd = cwdLocalPath(tm.Cwd())
		}
		return persistedNode{LeafID: n.LeafID, Cwd: cwd}
	}
	dir := "vertical"
	if n.Dir == SplitHorizontal {
		dir = "horizontal"
	}
	first := snapshotNode(n.First, terms)
	second := snapshotNode(n.Second, terms)
	return persistedNode{
		Dir:    dir,
		Ratio:  n.Ratio,
		First:  &first,
		Second: &second,
	}
}

// cwdLocalPath extracts the local filesystem path from an OSC 7 CWD value.
// Handles file://[host]/path → /path and bare /path → /path.
func cwdLocalPath(cwd string) string {
	if strings.HasPrefix(cwd, "file://") {
		rest := cwd[len("file://"):]
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			return rest[slash:]
		}
		return ""
	}
	return cwd
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
		return New(w, cfg)
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
		prevOnEvent: w.OnEvent,
	}

	for _, pt := range pw.Tabs {
		tabID := "tab-" + strconv.Itoa(ws.nextTabID)
		tab, err := newTabFromPersisted(w, cfg, tabID, pt,
			ws.onPaneExit, ws.onPaneFocus, ws.onPaneTitle)
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
	ws.registerCommands()

	activeTab := pw.ActiveTab
	if activeTab < 0 || activeTab >= len(ws.tabs) {
		activeTab = 0
	}
	ws.activeTab = activeTab

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
	onExit func(string),
	onFocus func(string),
	onTitle func(string, string),
) (*Tab, error) {
	t := &Tab{
		id:     tabID,
		terms:  make(map[string]*term.Term),
		titles: make(map[string]string),
	}
	idMap := make(map[string]string) // persisted leafID → new leafID
	nextID := 0
	var spawnErr error
	t.root = buildSplitTree(tabID, &pt.Root, func(leafID, cwd string) {
		if spawnErr != nil {
			return
		}
		if err := t.addPane(w, cfg, leafID, cwd, onExit, onFocus, onTitle); err != nil {
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
// For each leaf it calls spawn(newLeafID, cwd) and records the old→new ID
// mapping in idMap. nextID is incremented for each leaf (depth-first order).
func buildSplitTree(
	tabID string,
	pn *persistedNode,
	spawn func(leafID, cwd string),
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
			spawn(newID, pn.Cwd)
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
