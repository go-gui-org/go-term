// Package session manages multi-terminal workspaces with tabs and splits.
//
// Session sits above the term package, wiring *term.Term instances together
// through their public API. No pane logic lives inside term/widget.go.
//
// Model: workspace tabs + split panes (like kitty). Each tab contains a
// binary split tree of independent terminals. No tabs within splits.
package session

// SplitDir is the split direction.
type SplitDir uint8

const (
	// SplitVertical splits left | right.
	SplitVertical SplitDir = iota
	// SplitHorizontal splits top | bottom.
	SplitHorizontal
)

// splitNode is a node in the binary split tree.
//
// Internal nodes have First and Second set (both non-nil).
// Leaf nodes have LeafID set (non-empty) and both children nil.
type splitNode struct {
	Dir    SplitDir
	Ratio  float32    // first child's share of available space (0.0–1.0)
	First  *splitNode // nil if leaf
	Second *splitNode // nil if leaf
	LeafID string     // non-empty only for leaf nodes
}

// leaf creates a new leaf node.
func leaf(id string) *splitNode {
	return &splitNode{LeafID: id, Ratio: 0.5}
}

// split creates a new internal split node.
func split(dir SplitDir, ratio float32, first, second *splitNode) *splitNode {
	return &splitNode{
		Dir:    dir,
		Ratio:  ratio,
		First:  first,
		Second: second,
	}
}

// isLeaf reports whether the node is a leaf.
func (n *splitNode) isLeaf() bool { return n.First == nil }

// splitLeaf replaces the leaf identified by leafID with a new internal split
// node containing the old leaf and a new leaf. Returns the new root (or nil
// if leafID was not found).
func splitLeaf(root *splitNode, leafID, newLeafID string, dir SplitDir) *splitNode {
	if root.isLeaf() {
		if root.LeafID == leafID {
			return split(dir, 0.5, leaf(leafID), leaf(newLeafID))
		}
		return nil
	}
	if newFirst := splitLeaf(root.First, leafID, newLeafID, dir); newFirst != nil {
		return &splitNode{
			Dir: root.Dir, Ratio: root.Ratio,
			First: newFirst, Second: root.Second,
		}
	}
	if newSecond := splitLeaf(root.Second, leafID, newLeafID, dir); newSecond != nil {
		return &splitNode{
			Dir: root.Dir, Ratio: root.Ratio,
			First: root.First, Second: newSecond,
		}
	}
	return nil
}

// removeLeaf removes the leaf identified by leafID from the tree. The parent
// split collapses to the surviving sibling. Returns the new root and the
// surviving leaf ID. If the tree has only one leaf or leafID is not found,
// returns nil, "".
func removeLeaf(root *splitNode, leafID string) (*splitNode, string) {
	if root.isLeaf() {
		return nil, ""
	}
	// Check if the leaf to remove is a direct child.
	if root.First.isLeaf() && root.First.LeafID == leafID {
		return root.Second, firstLeafID(root.Second)
	}
	if root.Second.isLeaf() && root.Second.LeafID == leafID {
		return root.First, firstLeafID(root.First)
	}
	// Recurse into children.
	if !root.First.isLeaf() {
		newFirst, survivor := removeLeaf(root.First, leafID)
		if newFirst != nil {
			return &splitNode{
				Dir: root.Dir, Ratio: root.Ratio,
				First: newFirst, Second: root.Second,
			}, survivor
		}
	}
	if !root.Second.isLeaf() {
		newSecond, survivor := removeLeaf(root.Second, leafID)
		if newSecond != nil {
			return &splitNode{
				Dir: root.Dir, Ratio: root.Ratio,
				First: root.First, Second: newSecond,
			}, survivor
		}
	}
	return nil, ""
}

// firstLeafID returns the first leaf ID encountered in a depth-first walk.
func firstLeafID(node *splitNode) string {
	if node.isLeaf() {
		return node.LeafID
	}
	return firstLeafID(node.First)
}

// nextLeaf returns the leaf ID after the given one in depth-first order,
// wrapping to the first leaf if current is the last. Does not allocate.
func nextLeaf(root *splitNode, current string) string {
	var first string
	found := false
	var result string
	nextLeafWalk(root, current, &first, &found, &result)
	if result != "" {
		return result
	}
	return first
}

func nextLeafWalk(n *splitNode, current string, first *string, found *bool, result *string) {
	if n == nil || *result != "" {
		return
	}
	if n.isLeaf() {
		if *first == "" {
			*first = n.LeafID
		}
		if *found {
			*result = n.LeafID
			return
		}
		if n.LeafID == current {
			*found = true
		}
		return
	}
	nextLeafWalk(n.First, current, first, found, result)
	nextLeafWalk(n.Second, current, first, found, result)
}

// prevLeaf returns the leaf ID before the given one in depth-first order,
// wrapping to the last leaf if current is the first. Does not allocate.
func prevLeaf(root *splitNode, current string) string {
	var prev string
	found := false
	prevLeafWalk(root, current, &prev, &found)
	if prev != "" {
		return prev
	}
	return rightmostLeaf(root)
}

func prevLeafWalk(n *splitNode, current string, prev *string, found *bool) {
	if n == nil || *found {
		return
	}
	if n.isLeaf() {
		if n.LeafID == current {
			*found = true
			return
		}
		*prev = n.LeafID
		return
	}
	prevLeafWalk(n.First, current, prev, found)
	prevLeafWalk(n.Second, current, prev, found)
}

// rightmostLeaf returns the last leaf in depth-first order (the rightmost
// node in the tree). O(depth), no allocation.
func rightmostLeaf(n *splitNode) string {
	if n.isLeaf() {
		return n.LeafID
	}
	return rightmostLeaf(n.Second)
}
