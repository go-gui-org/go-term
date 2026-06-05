package session

// NodeType distinguishes the three SplitNode variants.
type NodeType uint8

const (
	NodeLeaf NodeType = iota // leaf holds a Pane
	NodeHorz                 // left/right split (side-by-side)
	NodeVert                 // top/bottom split (stacked)
)

// SplitNode is a node in the binary split tree.
//
// Leaf: Pane is non-nil, Left/Right are nil, Ratio is unused.
// Split (Horz/Vert): Pane is nil, Left/Right are non-nil, Ratio is the
// fraction of parent space given to the Left child.
type SplitNode struct {
	Type  NodeType
	Pane  *Pane      // non-nil iff Type == NodeLeaf
	Left  *SplitNode // non-nil iff Type != NodeLeaf
	Right *SplitNode // non-nil iff Type != NodeLeaf
	Ratio float64    // fraction of parent space for Left, 0..1
}

// NewPaneNode creates a leaf node holding the given pane. If pane is nil
// the leaf stores a nil Pane (caller must ensure the tree is not walked
// with nil panes downstream).
func NewPaneNode(pane *Pane) *SplitNode {
	return &SplitNode{Type: NodeLeaf, Pane: pane}
}

// Add splits a leaf node into two panes. The original leaf becomes the
// Left child; a new leaf for pane becomes the Right child. Ratio is set to
// 0.5 (equal split). Panics if n is not a leaf, dir is not NodeHorz or
// NodeVert, or pane is nil.
func (n *SplitNode) Add(dir NodeType, pane *Pane) {
	if n.Type != NodeLeaf {
		panic("session: Add called on non-leaf SplitNode")
	}
	if dir != NodeHorz && dir != NodeVert {
		panic("session: Add called with invalid direction")
	}
	if pane == nil {
		panic("session: Add called with nil pane")
	}
	old := *n
	n.Type = dir
	n.Pane = nil
	n.Left = &old
	n.Right = &SplitNode{Type: NodeLeaf, Pane: pane}
	n.Ratio = 0.5
}

// Remove removes all leaf nodes matching pred and returns the new root.
// If removal leaves a split with a single child, the split is collapsed
// into that child. If all leaves are removed, nil is returned. The
// caller must capture the return value. If pred is nil, Remove is a no-op
// and returns n unchanged.
func (n *SplitNode) Remove(pred func(*SplitNode) bool) *SplitNode {
	if n == nil || pred == nil {
		return n
	}
	switch n.Type {
	case NodeLeaf:
		if pred(n) {
			return nil
		}
		return n
	case NodeHorz, NodeVert:
		n.Left = n.Left.Remove(pred)
		n.Right = n.Right.Remove(pred)
		if n.Left == nil && n.Right == nil {
			return nil
		}
		if n.Left == nil {
			return n.Right
		}
		if n.Right == nil {
			return n.Left
		}
		return n
	}
	panic("session: unreachable")
}

// Find returns the first node (depth-first, left-to-right) for which pred
// returns true, or nil if no node matches. If pred is nil, returns nil.
func (n *SplitNode) Find(pred func(*SplitNode) bool) *SplitNode {
	if n == nil || pred == nil {
		return nil
	}
	if pred(n) {
		return n
	}
	if n.Type != NodeLeaf {
		if found := n.Left.Find(pred); found != nil {
			return found
		}
		return n.Right.Find(pred)
	}
	return nil
}

// Walk calls fn for every node in depth-first, left-to-right order.
// If fn is nil, Walk is a no-op.
func (n *SplitNode) Walk(fn func(*SplitNode)) {
	if n == nil || fn == nil {
		return
	}
	fn(n)
	if n.Type != NodeLeaf {
		n.Left.Walk(fn)
		n.Right.Walk(fn)
	}
}

// Leaves collects all leaf panes in depth-first, left-to-right order.
// Returns nil when n is nil.
func (n *SplitNode) Leaves() []*Pane {
	if n == nil {
		return nil
	}
	var out []*Pane
	n.Walk(func(sn *SplitNode) {
		if sn.Type == NodeLeaf && sn.Pane != nil {
			out = append(out, sn.Pane)
		}
	})
	return out
}

// LeafCount returns the number of leaf (pane) nodes in the tree.
func (n *SplitNode) LeafCount() int {
	if n == nil {
		return 0
	}
	if n.Type == NodeLeaf {
		return 1
	}
	return n.Left.LeafCount() + n.Right.LeafCount()
}
