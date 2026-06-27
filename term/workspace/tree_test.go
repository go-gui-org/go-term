package workspace

import (
	"testing"
)

// ---------------------------------------------------------------------------
// splitLeaf
// ---------------------------------------------------------------------------

func TestSplitLeaf_ReplacesTarget(t *testing.T) {
	// Tree: [A]
	// Split A → [ [A] | [B] ]
	root := leaf("a")
	newRoot := splitLeaf(root, "a", "b", SplitVertical)
	if newRoot == nil {
		t.Fatal("splitLeaf returned nil")
	}
	if newRoot.isLeaf() {
		t.Fatal("expected internal node after split")
	}
	if newRoot.Dir != SplitVertical {
		t.Errorf("Dir = %v, want SplitVertical", newRoot.Dir)
	}
	if newRoot.First.LeafID != "a" {
		t.Errorf("First.LeafID = %q, want \"a\"", newRoot.First.LeafID)
	}
	if newRoot.Second.LeafID != "b" {
		t.Errorf("Second.LeafID = %q, want \"b\"", newRoot.Second.LeafID)
	}
}

func TestSplitLeaf_NotFoundReturnsNil(t *testing.T) {
	root := leaf("a")
	newRoot := splitLeaf(root, "nonexistent", "b", SplitHorizontal)
	if newRoot != nil {
		t.Errorf("expected nil, got %v", newRoot)
	}
}

func TestSplitLeaf_SplitsInDeepTree(t *testing.T) {
	// Tree: [ [A] | [B] ]
	// Split B → [ [A] | [ [B] | [C] ] ]
	root := split(SplitVertical, 0.5, leaf("a"), leaf("b"))
	newRoot := splitLeaf(root, "b", "c", SplitHorizontal)
	if newRoot == nil {
		t.Fatal("splitLeaf returned nil")
	}
	// Second child should now be an internal horizontal split.
	second := newRoot.Second
	if second.isLeaf() {
		t.Fatal("expected internal node after split of B")
	}
	if second.Dir != SplitHorizontal {
		t.Errorf("Second.Dir = %v, want SplitHorizontal", second.Dir)
	}
	if second.First.LeafID != "b" {
		t.Errorf("Second.First.LeafID = %q, want \"b\"", second.First.LeafID)
	}
	if second.Second.LeafID != "c" {
		t.Errorf("Second.Second.LeafID = %q, want \"c\"", second.Second.LeafID)
	}
}

// ---------------------------------------------------------------------------
// removeLeaf
// ---------------------------------------------------------------------------

func TestRemoveLeaf_CollapsesParent(t *testing.T) {
	// Tree: [ [A] | [B] ]
	// Remove A → [B]
	root := split(SplitVertical, 0.5, leaf("a"), leaf("b"))
	newRoot, survivor := removeLeaf(root, "a")
	if newRoot == nil {
		t.Fatal("removeLeaf returned nil")
	}
	if !newRoot.isLeaf() {
		t.Fatal("expected leaf after collapse")
	}
	if newRoot.LeafID != "b" {
		t.Errorf("LeafID = %q, want \"b\"", newRoot.LeafID)
	}
	if survivor != "b" {
		t.Errorf("survivor = %q, want \"b\"", survivor)
	}
}

func TestRemoveLeaf_RemovesSecondChild(t *testing.T) {
	// Tree: [ [A] | [B] ]
	// Remove B → [A]
	root := split(SplitHorizontal, 0.5, leaf("a"), leaf("b"))
	newRoot, survivor := removeLeaf(root, "b")
	if newRoot == nil {
		t.Fatal("removeLeaf returned nil")
	}
	if !newRoot.isLeaf() {
		t.Fatal("expected leaf after collapse")
	}
	if newRoot.LeafID != "a" {
		t.Errorf("LeafID = %q, want \"a\"", newRoot.LeafID)
	}
	if survivor != "a" {
		t.Errorf("survivor = %q, want \"a\"", survivor)
	}
}

func TestRemoveLeaf_SingleLeafReturnsNil(t *testing.T) {
	root := leaf("a")
	newRoot, survivor := removeLeaf(root, "a")
	if newRoot != nil {
		t.Errorf("expected nil root, got %v", newRoot)
	}
	if survivor != "" {
		t.Errorf("expected empty survivor, got %q", survivor)
	}
}

func TestRemoveLeaf_NotFoundReturnsNil(t *testing.T) {
	root := split(SplitVertical, 0.5, leaf("a"), leaf("b"))
	newRoot, survivor := removeLeaf(root, "c")
	if newRoot != nil {
		t.Errorf("expected nil, got %v", newRoot)
	}
	if survivor != "" {
		t.Errorf("expected empty survivor, got %q", survivor)
	}
}

func TestRemoveLeaf_DeepRemove(t *testing.T) {
	// Tree: [ [A] | [ [B] | [C] ] ]
	// Remove B → [ [A] | [C] ]
	root := split(SplitVertical, 0.5,
		leaf("a"),
		split(SplitHorizontal, 0.5, leaf("b"), leaf("c")),
	)
	newRoot, survivor := removeLeaf(root, "b")
	if newRoot == nil {
		t.Fatal("removeLeaf returned nil")
	}
	if newRoot.isLeaf() {
		t.Fatal("expected internal node, root should still have a and c")
	}
	// The second child should have collapsed to just leaf "c".
	second := newRoot.Second
	if !second.isLeaf() {
		t.Fatal("expected second child to collapse to leaf")
	}
	if second.LeafID != "c" {
		t.Errorf("second.LeafID = %q, want \"c\"", second.LeafID)
	}
	if survivor != "c" {
		t.Errorf("survivor = %q, want \"c\"", survivor)
	}
}

// ---------------------------------------------------------------------------
// nextLeaf / prevLeaf
// ---------------------------------------------------------------------------

func makeThreeLeafTree() *splitNode {
	// [ [A] | [ [B] | [C] ] ]  — DFS order: A, B, C
	return split(SplitVertical, 0.5,
		leaf("a"),
		split(SplitHorizontal, 0.5, leaf("b"), leaf("c")),
	)
}

func TestNextLeaf_Basic(t *testing.T) {
	root := makeThreeLeafTree()
	if got := nextLeaf(root, "a"); got != "b" {
		t.Errorf("next after a = %q, want \"b\"", got)
	}
	if got := nextLeaf(root, "b"); got != "c" {
		t.Errorf("next after b = %q, want \"c\"", got)
	}
}

func TestNextLeaf_WrapsToFirst(t *testing.T) {
	root := makeThreeLeafTree()
	if got := nextLeaf(root, "c"); got != "a" {
		t.Errorf("next after c = %q, want \"a\" (wrap)", got)
	}
}

func TestNextLeaf_SingleLeaf(t *testing.T) {
	root := leaf("only")
	if got := nextLeaf(root, "only"); got != "only" {
		t.Errorf("nextLeaf single = %q, want \"only\"", got)
	}
}

func TestPrevLeaf_Basic(t *testing.T) {
	root := makeThreeLeafTree()
	if got := prevLeaf(root, "c"); got != "b" {
		t.Errorf("prev before c = %q, want \"b\"", got)
	}
	if got := prevLeaf(root, "b"); got != "a" {
		t.Errorf("prev before b = %q, want \"a\"", got)
	}
}

func TestPrevLeaf_WrapsToLast(t *testing.T) {
	root := makeThreeLeafTree()
	if got := prevLeaf(root, "a"); got != "c" {
		t.Errorf("prev before a = %q, want \"c\" (wrap)", got)
	}
}

func TestPrevLeaf_SingleLeaf(t *testing.T) {
	root := leaf("only")
	if got := prevLeaf(root, "only"); got != "only" {
		t.Errorf("prevLeaf single = %q, want \"only\"", got)
	}
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// firstLeafID / rightmostLeaf
// ---------------------------------------------------------------------------

func TestFirstLeafID(t *testing.T) {
	root := makeThreeLeafTree()
	if got := firstLeafID(root); got != "a" {
		t.Errorf("firstLeafID = %q, want \"a\"", got)
	}
}

func TestRightmostLeaf(t *testing.T) {
	root := makeThreeLeafTree()
	if got := rightmostLeaf(root); got != "c" {
		t.Errorf("rightmostLeaf = %q, want \"c\"", got)
	}
}
