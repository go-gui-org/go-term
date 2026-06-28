package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gui-org/go-term/term"
)

// ---------------------------------------------------------------------------
// cwdLocalPath
// ---------------------------------------------------------------------------

func TestCwdLocalPath_FileURI(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"file:///home/u/src", "/home/u/src"},
		{"file://hostname/home/u", "/home/u"},
		{"file://", ""},
		{"/bare/path", "/bare/path"},
		{"", ""},
	}
	for _, c := range cases {
		if got := cwdLocalPath(c.in); got != c.want {
			t.Errorf("cwdLocalPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// snapshot / round-trip
// ---------------------------------------------------------------------------

// buildTestWorkspace constructs a Workspace in memory (no window, no PTY)
// with the given split tree structure.
func buildTestWorkspace(tabs []*Tab, activeTab int) *Workspace {
	return &Workspace{tabs: tabs, activeTab: activeTab}
}

func TestSnapshot_SingleLeaf(t *testing.T) {
	tab := &Tab{
		id:      "tab-0",
		root:    leaf("tab-0-pane-0"),
		terms:   map[string]*term.Term{},
		focused: "tab-0-pane-0",
	}
	ws := buildTestWorkspace([]*Tab{tab}, 0)
	snap := ws.snapshot()

	if snap.Version != 1 {
		t.Errorf("version = %d, want 1", snap.Version)
	}
	if snap.ActiveTab != 0 {
		t.Errorf("activeTab = %d, want 0", snap.ActiveTab)
	}
	if len(snap.Tabs) != 1 {
		t.Fatalf("tabs len = %d, want 1", len(snap.Tabs))
	}
	root := snap.Tabs[0].Root
	if root.LeafID != "tab-0-pane-0" {
		t.Errorf("root.LeafID = %q, want tab-0-pane-0", root.LeafID)
	}
}

func TestSnapshot_VerticalSplit(t *testing.T) {
	root := split(SplitVertical, 0.4,
		leaf("tab-0-pane-0"),
		leaf("tab-0-pane-1"),
	)
	tab := &Tab{
		id:      "tab-0",
		root:    root,
		terms:   map[string]*term.Term{},
		focused: "tab-0-pane-1",
	}
	ws := buildTestWorkspace([]*Tab{tab}, 0)
	snap := ws.snapshot()

	r := snap.Tabs[0].Root
	if r.Dir != "vertical" {
		t.Errorf("dir = %q, want vertical", r.Dir)
	}
	if r.Ratio != 0.4 {
		t.Errorf("ratio = %v, want 0.4", r.Ratio)
	}
	if r.First == nil || r.First.LeafID != "tab-0-pane-0" {
		t.Errorf("first leaf = %+v", r.First)
	}
	if r.Second == nil || r.Second.LeafID != "tab-0-pane-1" {
		t.Errorf("second leaf = %+v", r.Second)
	}
}

func TestSnapshot_HorizontalSplit(t *testing.T) {
	root := split(SplitHorizontal, 0.6,
		leaf("tab-0-pane-0"),
		leaf("tab-0-pane-1"),
	)
	tab := &Tab{id: "tab-0", root: root, terms: map[string]*term.Term{}, focused: "tab-0-pane-0"}
	ws := buildTestWorkspace([]*Tab{tab}, 0)
	snap := ws.snapshot()
	if snap.Tabs[0].Root.Dir != "horizontal" {
		t.Errorf("dir = %q, want horizontal", snap.Tabs[0].Root.Dir)
	}
}

func TestSnapshot_TwoTabs(t *testing.T) {
	tab0 := &Tab{id: "tab-0", root: leaf("tab-0-pane-0"), terms: map[string]*term.Term{}, focused: "tab-0-pane-0"}
	tab1 := &Tab{id: "tab-1", root: leaf("tab-1-pane-0"), terms: map[string]*term.Term{}, focused: "tab-1-pane-0"}
	ws := buildTestWorkspace([]*Tab{tab0, tab1}, 1)
	snap := ws.snapshot()
	if snap.ActiveTab != 1 {
		t.Errorf("activeTab = %d, want 1", snap.ActiveTab)
	}
	if len(snap.Tabs) != 2 {
		t.Errorf("tab count = %d, want 2", len(snap.Tabs))
	}
}

// TestSnapshotRoundTrip marshals a workspace, unmarshals it, and checks
// the tree shape + ratios. No PTY involved.
func TestSnapshotRoundTrip(t *testing.T) {
	// Build a 2-tab workspace: tab-0 has a vertical split, tab-1 is a single leaf.
	tab0Root := split(SplitVertical, 0.3,
		leaf("tab-0-pane-0"),
		split(SplitHorizontal, 0.7,
			leaf("tab-0-pane-1"),
			leaf("tab-0-pane-2"),
		),
	)
	tab0 := &Tab{
		id:      "tab-0",
		root:    tab0Root,
		terms:   map[string]*term.Term{},
		focused: "tab-0-pane-1",
	}
	tab1 := &Tab{
		id:      "tab-1",
		root:    leaf("tab-1-pane-0"),
		terms:   map[string]*term.Term{},
		focused: "tab-1-pane-0",
	}
	ws := buildTestWorkspace([]*Tab{tab0, tab1}, 0)
	snap := ws.snapshot()

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got persistedWorkspace
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Version != 1 {
		t.Errorf("version = %d, want 1", got.Version)
	}
	if got.ActiveTab != 0 {
		t.Errorf("activeTab = %d, want 0", got.ActiveTab)
	}
	if len(got.Tabs) != 2 {
		t.Fatalf("tabs len = %d, want 2", len(got.Tabs))
	}

	// tab-0 root should be a vertical split at ratio 0.3.
	r0 := got.Tabs[0].Root
	if r0.Dir != "vertical" || r0.First == nil || r0.Second == nil {
		t.Errorf("tab-0 root = %+v, want vertical split", r0)
	}
	if r0.Ratio != 0.3 {
		t.Errorf("tab-0 ratio = %v, want 0.3", r0.Ratio)
	}
	if r0.First.LeafID != "tab-0-pane-0" {
		t.Errorf("tab-0 first = %q, want tab-0-pane-0", r0.First.LeafID)
	}
	// Second child is a horizontal split.
	if r0.Second.Dir != "horizontal" {
		t.Errorf("tab-0 second.dir = %q, want horizontal", r0.Second.Dir)
	}

	// tab-1 should be a single leaf.
	r1 := got.Tabs[1].Root
	if r1.LeafID == "" {
		t.Errorf("tab-1 root should be a leaf")
	}
	if got.Tabs[1].ActiveLeaf != "tab-1-pane-0" {
		t.Errorf("tab-1 activeLeaf = %q", got.Tabs[1].ActiveLeaf)
	}
}

// ---------------------------------------------------------------------------
// Save / atomic write
// ---------------------------------------------------------------------------

func TestSave_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "workspace.json")

	tab := &Tab{id: "tab-0", root: leaf("tab-0-pane-0"), terms: map[string]*term.Term{}, focused: "tab-0-pane-0"}
	ws := buildTestWorkspace([]*Tab{tab}, 0)
	if err := ws.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// File must exist and be valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var pw persistedWorkspace
	if err := json.Unmarshal(data, &pw); err != nil {
		t.Fatalf("unmarshal saved file: %v", err)
	}
	if pw.Version != 1 {
		t.Errorf("version = %d, want 1", pw.Version)
	}
	// No .tmp file left behind.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp file should not exist after successful save")
	}
}

// ---------------------------------------------------------------------------
// buildSplitTree
// ---------------------------------------------------------------------------

func TestBuildSplitTree_Leaf(t *testing.T) {
	pn := &persistedNode{LeafID: "old-pane-0", Cwd: "/tmp"}
	idMap := make(map[string]string)
	nextID := 0
	var spawned []string
	node := buildSplitTree("tab-0", pn, func(id, cwd string) {
		spawned = append(spawned, id+":"+cwd)
	}, idMap, &nextID)

	if node == nil || !node.isLeaf() {
		t.Fatal("expected leaf node")
	}
	if node.LeafID != "tab-0-pane-0" {
		t.Errorf("leafID = %q, want tab-0-pane-0", node.LeafID)
	}
	if idMap["old-pane-0"] != "tab-0-pane-0" {
		t.Errorf("idMap[old-pane-0] = %q, want tab-0-pane-0", idMap["old-pane-0"])
	}
	if len(spawned) != 1 || spawned[0] != "tab-0-pane-0:/tmp" {
		t.Errorf("spawned = %v", spawned)
	}
	if nextID != 1 {
		t.Errorf("nextID = %d, want 1", nextID)
	}
}

func TestBuildSplitTree_VerticalSplit(t *testing.T) {
	pn := &persistedNode{
		Dir:   "vertical",
		Ratio: 0.5,
		First: &persistedNode{LeafID: "old-0"},
		Second: &persistedNode{
			Dir:    "horizontal",
			Ratio:  0.6,
			First:  &persistedNode{LeafID: "old-1", Cwd: "/a"},
			Second: &persistedNode{LeafID: "old-2", Cwd: "/b"},
		},
	}
	idMap := make(map[string]string)
	nextID := 0
	var order []string
	node := buildSplitTree("tab-0", pn, func(id, cwd string) {
		order = append(order, id)
	}, idMap, &nextID)

	if node == nil || node.isLeaf() {
		t.Fatal("expected internal node")
	}
	if node.Dir != SplitVertical {
		t.Errorf("dir = %v, want SplitVertical", node.Dir)
	}
	// Depth-first: old-0 → pane-0, old-1 → pane-1, old-2 → pane-2.
	if idMap["old-0"] != "tab-0-pane-0" {
		t.Errorf("old-0 → %q, want tab-0-pane-0", idMap["old-0"])
	}
	if idMap["old-1"] != "tab-0-pane-1" {
		t.Errorf("old-1 → %q, want tab-0-pane-1", idMap["old-1"])
	}
	if idMap["old-2"] != "tab-0-pane-2" {
		t.Errorf("old-2 → %q, want tab-0-pane-2", idMap["old-2"])
	}
	if len(order) != 3 {
		t.Errorf("spawn count = %d, want 3", len(order))
	}
}

func TestBuildSplitTree_MalformedReturnsNil(t *testing.T) {
	// Internal node with missing children.
	pn := &persistedNode{Dir: "vertical", Ratio: 0.5}
	idMap := make(map[string]string)
	nextID := 0
	node := buildSplitTree("tab-0", pn, func(id, cwd string) {}, idMap, &nextID)
	if node != nil {
		t.Errorf("expected nil for malformed node, got %+v", node)
	}
}

// ---------------------------------------------------------------------------
// Restore fallback (no real window needed — test the parse path only)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// buildSplitTree depth limit
// ---------------------------------------------------------------------------

func TestBuildSplitTree_DepthLimitReturnsNil(t *testing.T) {
	// Build a right-leaning chain of maxSplitDepth+1 split nodes so the
	// deepest child is reached at depth > maxSplitDepth and the guard fires.
	pn := &persistedNode{LeafID: "leaf-bottom"}
	for i := 0; i <= maxSplitDepth; i++ {
		pn = &persistedNode{
			Dir:    "vertical",
			Ratio:  0.5,
			First:  &persistedNode{LeafID: "leaf"},
			Second: pn,
		}
	}
	idMap := make(map[string]string)
	nextID := 0
	node := buildSplitTree("tab-0", pn, func(id, cwd string) {}, idMap, &nextID)
	if node != nil {
		t.Errorf("expected nil for tree exceeding maxSplitDepth (%d), got non-nil", maxSplitDepth)
	}
}

// ---------------------------------------------------------------------------
// Restore fallback paths (nil window is safe for pre-restore guards)
// ---------------------------------------------------------------------------

func TestRestore_OversizedFile_FallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.json")
	if err := os.WriteFile(path, make([]byte, maxWorkspaceSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := Restore(nil, Cfg{}, path)
	if ws != nil || err == nil {
		t.Errorf("Restore(oversized) = (%v, %v), want (nil, non-nil error)", ws, err)
	}
}

func TestRestore_VersionMismatch_FallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.json")
	data := `{"version":2,"activeTab":0,"tabs":[{"activeLeaf":"p","root":{"leafID":"p"}}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := Restore(nil, Cfg{}, path)
	if ws != nil || err == nil {
		t.Errorf("Restore(version mismatch) = (%v, %v), want (nil, non-nil error)", ws, err)
	}
}

func TestRestore_TooManyTabs_FallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.json")
	tabs := make([]persistedTab, maxWorkspaceTabs+1)
	for i := range tabs {
		tabs[i] = persistedTab{ActiveLeaf: "p", Root: persistedNode{LeafID: "p"}}
	}
	data, err := json.Marshal(persistedWorkspace{Version: 1, Tabs: tabs})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := Restore(nil, Cfg{}, path)
	if ws != nil || err == nil {
		t.Errorf("Restore(too many tabs) = (%v, %v), want (nil, non-nil error)", ws, err)
	}
}

// ---------------------------------------------------------------------------
// DefaultWorkspacePath / configDir
// ---------------------------------------------------------------------------

func TestDefaultWorkspacePath_NonEmpty(t *testing.T) {
	p, err := DefaultWorkspacePath()
	if err != nil {
		t.Skipf("DefaultWorkspacePath: %v", err)
	}
	if p == "" {
		t.Error("expected non-empty path")
	}
	if !strings.HasSuffix(p, "workspace.json") {
		t.Errorf("path %q should end with workspace.json", p)
	}
}

func TestDefaultWorkspacePath_XDGEnvVar(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	p, err := DefaultWorkspacePath()
	if err != nil {
		t.Fatalf("DefaultWorkspacePath: %v", err)
	}
	want := filepath.Join(tmp, "go-term", "workspace.json")
	if p != want {
		t.Errorf("got %q, want %q", p, want)
	}
}

func TestConfigDir_XDGEnvVar(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	got, err := configDir()
	if err != nil {
		t.Fatalf("configDir: %v", err)
	}
	want := filepath.Join(tmp, "go-term")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSnapshotJSON_Schema(t *testing.T) {
	// Verify the JSON schema matches the roadmap example.
	tab := &Tab{
		id:      "tab-0",
		root:    split(SplitVertical, 0.5, leaf("tab-0-pane-0"), leaf("tab-0-pane-1")),
		terms:   map[string]*term.Term{},
		focused: "tab-0-pane-1",
	}
	ws := buildTestWorkspace([]*Tab{tab}, 0)
	snap := ws.snapshot()
	data, _ := json.MarshalIndent(snap, "", "  ")
	s := string(data)

	for _, want := range []string{`"version": 1`, `"activeTab": 0`, `"activeLeaf"`, `"leafID"`, `"dir": "vertical"`} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON missing %q:\n%s", want, s)
		}
	}
}
