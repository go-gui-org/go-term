package workspace

import "math"

// Keyboard pane resize and ratio-based split layout math.
//
// A split node's Ratio is the first child's share of the axis (width for
// vertical splits, height for horizontal). resizeActivePane moves the nearest
// same-axis split divider toward the arrow direction — Right/Down raise the
// ratio (divider moves right/down), Left/Up lower it. This keeps every arrow
// responsive regardless of which side of the split the focused pane is on.
// splitView turns ratios into pixel sizes with a min-pane floor.

const (
	// resizeStep is the ratio delta applied per keyboard resize keystroke.
	resizeStep = float32(0.05)
	// minRatio/maxRatio bound a split ratio so neither child vanishes in
	// the tree model regardless of pixel size; ratioSplit additionally
	// enforces the pixel floor at layout time.
	minRatio = float32(0.05)
	maxRatio = float32(0.95)
	// minPanePx is the pixel floor below which a pane is not allowed to
	// shrink during ratio-based layout.
	minPanePx = float32(40)
)

// resizeDir is a keyboard resize direction.
type resizeDir uint8

const (
	resizeLeft resizeDir = iota
	resizeRight
	resizeUp
	resizeDown
)

// params maps a direction to the split axis it acts on and the ratio delta.
// Right/Down move the divider toward the high end of the axis (delta > 0);
// Left/Up move it toward the low end (delta < 0).
func (d resizeDir) params() (axis SplitDir, delta float32) {
	switch d {
	case resizeRight:
		return SplitVertical, resizeStep
	case resizeLeft:
		return SplitVertical, -resizeStep
	case resizeDown:
		return SplitHorizontal, resizeStep
	default: // resizeUp
		return SplitHorizontal, -resizeStep
	}
}

// resizeActivePane moves the divider of the focused pane's nearest same-axis
// split toward dir. No-op when no split exists on that axis (e.g. a single
// pane, or only splits on the other axis) or the ratio is already at its
// bound.
func (ws *Workspace) resizeActivePane(dir resizeDir) {
	if ws.activeTab < 0 || ws.activeTab >= len(ws.tabs) {
		return
	}
	tab := ws.tabs[ws.activeTab]
	axis, delta := dir.params()
	node := findResizeSplit(tab.root, tab.focused, axis)
	if node == nil {
		return
	}
	next := clampRatio(node.Ratio + delta)
	if next == node.Ratio {
		return
	}
	node.Ratio = next
	ws.refresh()
}

// findResizeSplit returns the split node closest to leafID (deepest ancestor)
// whose direction is axis and whose subtree contains leafID. Returns nil when
// no split on that axis contains the leaf.
func findResizeSplit(root *splitNode, leafID string, axis SplitDir) *splitNode {
	var best *splitNode
	var walk func(n *splitNode) bool // reports whether leafID is in n's subtree
	walk = func(n *splitNode) bool {
		if n.isLeaf() {
			return n.LeafID == leafID
		}
		if !walk(n.First) && !walk(n.Second) {
			return false
		}
		// Post-order: the deepest containing ancestor runs before its
		// parents, so the first match recorded is the nearest one.
		if best == nil && n.Dir == axis {
			best = n
		}
		return true
	}
	walk(root)
	return best
}

// clampRatio bounds r to [minRatio, maxRatio]. NaN collapses to an even
// split so a corrupt ratio can never propagate into pixel sizing; ±Inf
// fall to the respective bound.
func clampRatio(r float32) float32 {
	if math.IsNaN(float64(r)) {
		return 0.5
	}
	if r < minRatio {
		return minRatio
	}
	if r > maxRatio {
		return maxRatio
	}
	return r
}

// ratioSplit returns the first child's pixel size for an axis of length
// avail given ratio, enforcing minPanePx on both children when there is
// room. When avail cannot fit two floors it falls back to the raw
// proportional split.
func ratioSplit(avail, ratio float32) float32 {
	if !(avail > 0) { // false for NaN, 0, negative
		return 0
	}
	// Sanitize ratio so a corrupt node.Ratio (NaN/Inf/out-of-range) can
	// never yield a NaN or negative pixel width that the floor below would
	// silently pass through.
	switch {
	case math.IsNaN(float64(ratio)), ratio < 0:
		ratio = 0.5
	case ratio > 1:
		ratio = 1
	}
	first := avail * ratio
	if avail >= 2*minPanePx {
		if first < minPanePx {
			first = minPanePx
		}
		if hi := avail - minPanePx; first > hi {
			first = hi
		}
	}
	return first
}
