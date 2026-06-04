package session

import (
	"testing"
)

func newStubPane() *Pane {
	return &Pane{}
}

func TestNewPaneNode(t *testing.T) {
	p := newStubPane()
	n := NewPaneNode(p)
	if n.Type != NodeLeaf {
		t.Errorf("Type = %d, want NodeLeaf", n.Type)
	}
	if n.Pane != p {
		t.Error("Pane not stored")
	}
	if n.Left != nil || n.Right != nil {
		t.Error("Leaf should have nil children")
	}
}

func TestAdd_Vertical(t *testing.T) {
	left := newStubPane()
	right := newStubPane()
	n := NewPaneNode(left)
	n.Add(NodeVert, right)

	if n.Type != NodeVert {
		t.Errorf("Type = %d, want NodeVert", n.Type)
	}
	if n.Pane != nil {
		t.Error("Split should have nil Pane")
	}
	if n.Left == nil || n.Right == nil {
		t.Fatal("Split should have both children")
	}
	if n.Left.Pane != left {
		t.Error("Left should hold original pane")
	}
	if n.Right.Pane != right {
		t.Error("Right should hold new pane")
	}
	if n.Ratio != 0.5 {
		t.Errorf("Ratio = %f, want 0.5", n.Ratio)
	}
}

func TestAdd_Horizontal(t *testing.T) {
	top := newStubPane()
	bottom := newStubPane()
	n := NewPaneNode(top)
	n.Add(NodeHorz, bottom)

	if n.Type != NodeHorz {
		t.Errorf("Type = %d, want NodeHorz", n.Type)
	}
	if n.Left.Pane != top {
		t.Error("Left should hold original pane")
	}
	if n.Right.Pane != bottom {
		t.Error("Right should hold new pane")
	}
}

func TestAdd_PanicsOnNonLeaf(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Add on split node should panic")
		}
	}()
	root := NewPaneNode(newStubPane())
	root.Add(NodeVert, newStubPane()) // now a split
	root.Add(NodeVert, newStubPane()) // should panic
}

func TestRemove_SingleLeaf(t *testing.T) {
	n := NewPaneNode(newStubPane())
	result := n.Remove(func(sn *SplitNode) bool { return true })
	if result != nil {
		t.Error("Removing only leaf should return nil")
	}
}

func TestRemove_TargetLeafCollapses(t *testing.T) {
	// Create a split with two leaves, remove one.
	root := NewPaneNode(newStubPane())
	keep := newStubPane()
	root.Add(NodeVert, keep)

	pred := func(sn *SplitNode) bool { return sn.Pane != keep }
	result := root.Remove(pred)
	if result == nil {
		t.Fatal("Expected one surviving leaf")
	}
	if result.Type != NodeLeaf {
		t.Errorf("Split should collapse to leaf, got Type=%d", result.Type)
	}
	if result.Pane != keep {
		t.Error("Surviving pane mismatch")
	}
}

func TestRemove_BothLeaves(t *testing.T) {
	root := NewPaneNode(newStubPane())
	root.Add(NodeVert, newStubPane())
	result := root.Remove(func(sn *SplitNode) bool { return true })
	if result != nil {
		t.Errorf("Removing all leaves should return nil, got %v", result)
	}
}

func TestRemove_ThreePaneTree(t *testing.T) {
	// Right leaf is itself a split, forming a 3-pane tree.
	root := NewPaneNode(newStubPane())
	rightA := newStubPane()
	rightB := newStubPane()
	root.Add(NodeVert, rightA)
	root.Right.Add(NodeHorz, rightB)

	// Remove rightB. The Horz split collapses, Vert remains with 2 leaves.
	result := root.Remove(func(sn *SplitNode) bool { return sn.Pane == rightB })
	if result == nil {
		t.Fatal("Expected surviving tree")
	}
	if result.LeafCount() != 2 {
		t.Errorf("LeafCount = %d, want 2", result.LeafCount())
	}
	if result.Right.Pane != rightA {
		t.Error("Right should collapse back to rightA")
	}
}

func TestFind_LocatePane(t *testing.T) {
	target := newStubPane()
	root := NewPaneNode(newStubPane())
	root.Add(NodeVert, target)

	found := root.Find(func(sn *SplitNode) bool {
		return sn.Type == NodeLeaf && sn.Pane == target
	})
	if found == nil {
		t.Fatal("Find returned nil")
	}
	if found.Pane != target {
		t.Error("Find returned wrong node")
	}
}

func TestFind_NotFound(t *testing.T) {
	root := NewPaneNode(newStubPane())
	root.Add(NodeVert, newStubPane())

	found := root.Find(func(sn *SplitNode) bool { return false })
	if found != nil {
		t.Error("Find should return nil when no match")
	}
}

func TestWalk_VisitsDepthFirst(t *testing.T) {
	a := newStubPane()
	b := newStubPane()
	c := newStubPane()
	root := NewPaneNode(a)
	root.Add(NodeVert, b)       // split: a (left), b (right)
	root.Right.Add(NodeHorz, c) // b (left), c (right) … so order: a, b, c

	var order []*Pane
	root.Walk(func(sn *SplitNode) {
		if sn.Type == NodeLeaf {
			order = append(order, sn.Pane)
		}
	})
	if len(order) != 3 {
		t.Fatalf("Walk visited %d leaves, want 3", len(order))
	}
	if order[0] != a || order[1] != b || order[2] != c {
		t.Error("Walk order incorrect (expected depth-first left-to-right)")
	}
}

func TestLeafCount(t *testing.T) {
	root := NewPaneNode(newStubPane())
	if n := root.LeafCount(); n != 1 {
		t.Errorf("Leaf leaf-count = %d, want 1", n)
	}

	root.Add(NodeVert, newStubPane())
	if n := root.LeafCount(); n != 2 {
		t.Errorf("Split leaf-count = %d, want 2", n)
	}

	root.Right.Add(NodeHorz, newStubPane())
	if n := root.LeafCount(); n != 3 {
		t.Errorf("3-pane leaf-count = %d, want 3", n)
	}
}

func TestLeafCount_Nil(t *testing.T) {
	var n *SplitNode
	if n.LeafCount() != 0 {
		t.Errorf("nil leaf-count = %d, want 0", n.LeafCount())
	}
}

func TestAdd_InvalidDir(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Add with NodeLeaf dir should panic")
		}
	}()
	root := NewPaneNode(newStubPane())
	root.Add(NodeLeaf, newStubPane()) // NodeLeaf is not a split direction
}

func TestAdd_NilPane(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Add with nil pane should panic")
		}
	}()
	root := NewPaneNode(newStubPane())
	root.Add(NodeVert, nil)
}

func TestRemove_NilPred(t *testing.T) {
	root := NewPaneNode(newStubPane())
	root.Add(NodeVert, newStubPane())
	result := root.Remove(nil)
	if result.LeafCount() != 2 {
		t.Errorf("Remove with nil pred should be no-op, leaf-count=%d", result.LeafCount())
	}
}

func TestFind_NilPred(t *testing.T) {
	root := NewPaneNode(newStubPane())
	if found := root.Find(nil); found != nil {
		t.Error("Find with nil pred should return nil")
	}
}

func TestWalk_NilFn(t *testing.T) {
	root := NewPaneNode(newStubPane())
	root.Add(NodeVert, newStubPane())
	root.Left.Add(NodeHorz, newStubPane())
	// Must not panic.
	root.Walk(nil)
}
