package workspace

import (
	"math"
	"testing"
)

// ---------------------------------------------------------------------------
// ratioSplit
// ---------------------------------------------------------------------------

func TestRatioSplit_Proportional(t *testing.T) {
	// Plenty of room: exact proportional split, no flooring.
	if got := ratioSplit(1000, 0.25); got != 250 {
		t.Errorf("ratioSplit(1000, 0.25) = %v, want 250", got)
	}
	if got := ratioSplit(1000, 0.5); got != 500 {
		t.Errorf("ratioSplit(1000, 0.5) = %v, want 500", got)
	}
}

func TestRatioSplit_FloorsBothEnds(t *testing.T) {
	// avail=200 (>= 2*minPanePx), ratio 0.01 would give 2px → floored up.
	if got := ratioSplit(200, 0.01); got != minPanePx {
		t.Errorf("low ratio = %v, want %v", got, minPanePx)
	}
	// ratio 0.99 → 198px, floored so second keeps minPanePx.
	if got := ratioSplit(200, 0.99); got != 200-minPanePx {
		t.Errorf("high ratio = %v, want %v", got, 200-minPanePx)
	}
}

func TestRatioSplit_TooSmallFallsBackToProportional(t *testing.T) {
	// avail < 2*minPanePx: cannot floor both, use raw proportion.
	avail := minPanePx // 40 < 80
	if got := ratioSplit(avail, 0.5); got != avail*0.5 {
		t.Errorf("tiny avail = %v, want %v", got, avail*0.5)
	}
}

func TestRatioSplit_NonPositiveAvail(t *testing.T) {
	if got := ratioSplit(0, 0.5); got != 0 {
		t.Errorf("ratioSplit(0,...) = %v, want 0", got)
	}
	if got := ratioSplit(-10, 0.5); got != 0 {
		t.Errorf("ratioSplit(-10,...) = %v, want 0", got)
	}
	if got := ratioSplit(float32(math.NaN()), 0.5); got != 0 {
		t.Errorf("ratioSplit(NaN avail) = %v, want 0", got)
	}
}

func TestRatioSplit_SanitizesRatio(t *testing.T) {
	// A corrupt ratio must never yield NaN or an out-of-bounds size; it
	// collapses to an even split (or the floored bound) instead.
	if got := ratioSplit(1000, float32(math.NaN())); got != 500 {
		t.Errorf("ratioSplit(1000, NaN) = %v, want 500 (even split)", got)
	}
	if got := ratioSplit(1000, float32(math.Inf(1))); got != 1000-minPanePx {
		t.Errorf("ratioSplit(1000, +Inf) = %v, want %v", got, 1000-minPanePx)
	}
	if got := ratioSplit(1000, -5); got != 500 {
		t.Errorf("ratioSplit(1000, -5) = %v, want 500 (even split)", got)
	}
}

// ---------------------------------------------------------------------------
// clampRatio
// ---------------------------------------------------------------------------

func TestClampRatio(t *testing.T) {
	if got := clampRatio(0.5); got != 0.5 {
		t.Errorf("clampRatio(0.5) = %v, want 0.5", got)
	}
	if got := clampRatio(-1); got != minRatio {
		t.Errorf("clampRatio(-1) = %v, want %v", got, minRatio)
	}
	if got := clampRatio(2); got != maxRatio {
		t.Errorf("clampRatio(2) = %v, want %v", got, maxRatio)
	}
}

func TestClampRatio_NonFinite(t *testing.T) {
	nan := float32(math.NaN())
	if got := clampRatio(nan); got != 0.5 {
		t.Errorf("clampRatio(NaN) = %v, want 0.5", got)
	}
	posInf := float32(math.Inf(1))
	if got := clampRatio(posInf); got != maxRatio {
		t.Errorf("clampRatio(+Inf) = %v, want %v", got, maxRatio)
	}
	negInf := float32(math.Inf(-1))
	if got := clampRatio(negInf); got != minRatio {
		t.Errorf("clampRatio(-Inf) = %v, want %v", got, minRatio)
	}
}

// ---------------------------------------------------------------------------
// resizeDir.params
// ---------------------------------------------------------------------------

func TestResizeDirParams(t *testing.T) {
	cases := []struct {
		dir      resizeDir
		axis     SplitDir
		positive bool
	}{
		{resizeRight, SplitVertical, true},
		{resizeLeft, SplitVertical, false},
		{resizeDown, SplitHorizontal, true},
		{resizeUp, SplitHorizontal, false},
	}
	for _, c := range cases {
		axis, delta := c.dir.params()
		if axis != c.axis || (delta > 0) != c.positive {
			t.Errorf("dir %d: got axis=%v delta=%v", c.dir, axis, delta)
		}
	}
}

// ---------------------------------------------------------------------------
// findResizeSplit
// ---------------------------------------------------------------------------

func TestFindResizeSplit_DirectSplit(t *testing.T) {
	// [ [A] | [B] ]  vertical. Either pane resolves to the one vertical
	// split regardless of arrow direction, so all four side/arrow combos
	// stay live (the bug fix: B → Right used to be a dead key).
	root := split(SplitVertical, 0.5, leaf("a"), leaf("b"))

	if got := findResizeSplit(root, "a", SplitVertical); got != root {
		t.Errorf("a/vertical = %v, want root", got)
	}
	if got := findResizeSplit(root, "b", SplitVertical); got != root {
		t.Errorf("b/vertical = %v, want root", got)
	}
	// Wrong axis: no horizontal split contains A → nil.
	if got := findResizeSplit(root, "a", SplitHorizontal); got != nil {
		t.Errorf("a/horizontal = %v, want nil", got)
	}
}

func TestFindResizeSplit_NearestAncestorWins(t *testing.T) {
	// [ [ [A] | [B] ] | [C] ]  — both inner and outer splits are vertical
	// and contain A. The inner (deepest) must win: its divider is the one
	// adjacent to A.
	inner := split(SplitVertical, 0.5, leaf("a"), leaf("b"))
	root := split(SplitVertical, 0.5, inner, leaf("c"))

	if got := findResizeSplit(root, "a", SplitVertical); got != inner {
		t.Errorf("a/vertical = %v, want inner split", got)
	}
	if got := findResizeSplit(root, "b", SplitVertical); got != inner {
		t.Errorf("b/vertical = %v, want inner split", got)
	}
	// C only sits under the outer vertical split → root.
	if got := findResizeSplit(root, "c", SplitVertical); got != root {
		t.Errorf("c/vertical = %v, want root", got)
	}
}

func TestFindResizeSplit_MixedAxes(t *testing.T) {
	// [ [A] | ( [B] / [C] ) ]  outer vertical, inner horizontal.
	innerH := split(SplitHorizontal, 0.5, leaf("b"), leaf("c"))
	root := split(SplitVertical, 0.5, leaf("a"), innerH)

	// Up/Down for B or C act on the horizontal split.
	if got := findResizeSplit(root, "b", SplitHorizontal); got != innerH {
		t.Errorf("b/horizontal = %v, want innerH", got)
	}
	if got := findResizeSplit(root, "c", SplitHorizontal); got != innerH {
		t.Errorf("c/horizontal = %v, want innerH", got)
	}
	// Left/Right for B act on the only vertical split → root.
	if got := findResizeSplit(root, "b", SplitVertical); got != root {
		t.Errorf("b/vertical = %v, want root", got)
	}
	// A has no horizontal split above it → nil.
	if got := findResizeSplit(root, "a", SplitHorizontal); got != nil {
		t.Errorf("a/horizontal = %v, want nil", got)
	}
}

func TestFindResizeSplit_SingleLeaf(t *testing.T) {
	root := leaf("only")
	if got := findResizeSplit(root, "only", SplitVertical); got != nil {
		t.Errorf("single leaf = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// resizeActivePane — guard / no-op paths
//
// These exercise the early-return guards that never reach refresh(), so a
// Workspace can be hand-built with a nil window (matching the convention in
// workspace_test.go). The successful mutate-then-refresh path needs a live
// *gui.Window and is covered visually via examples/falcon.
// ---------------------------------------------------------------------------

func TestResizeActivePane_NoActiveTabNoop(t *testing.T) {
	// activeTab out of range must return before indexing ws.tabs.
	ws := &Workspace{} // no tabs, activeTab 0 → 0 >= len(0)
	ws.resizeActivePane(resizeRight)
	if len(ws.tabs) != 0 {
		t.Errorf("tabs mutated: %d, want 0", len(ws.tabs))
	}

	ws = &Workspace{tabs: []*Tab{{root: leaf("a"), focused: "a"}}, activeTab: 5}
	ws.resizeActivePane(resizeLeft) // out-of-range high index, no panic
}

func TestResizeActivePane_SinglePaneNoop(t *testing.T) {
	// A lone leaf has no split on any axis → findResizeSplit nil → no-op,
	// no nil-node deref.
	tab := &Tab{root: leaf("a"), focused: "a"}
	ws := &Workspace{tabs: []*Tab{tab}, activeTab: 0}
	ws.resizeActivePane(resizeRight)
	if !tab.root.isLeaf() {
		t.Error("root unexpectedly changed from leaf")
	}
}

func TestResizeActivePane_WrongAxisNoop(t *testing.T) {
	// Vertical split, but Up/Down act on the horizontal axis → no matching
	// split → ratio unchanged.
	root := split(SplitVertical, 0.5, leaf("a"), leaf("b"))
	tab := &Tab{root: root, focused: "a"}
	ws := &Workspace{tabs: []*Tab{tab}, activeTab: 0}
	ws.resizeActivePane(resizeUp)
	if root.Ratio != 0.5 {
		t.Errorf("Ratio changed on wrong-axis resize: %v, want 0.5", root.Ratio)
	}
}

func TestResizeActivePane_AtBoundNoMutation(t *testing.T) {
	// Ratio already at maxRatio; growing further clamps back to the same
	// value, so the method returns before refresh() (no window needed).
	root := split(SplitVertical, maxRatio, leaf("a"), leaf("b"))
	tab := &Tab{root: root, focused: "a"}
	ws := &Workspace{tabs: []*Tab{tab}, activeTab: 0}
	ws.resizeActivePane(resizeRight) // delta +step → clamp → maxRatio
	if root.Ratio != maxRatio {
		t.Errorf("Ratio mutated at bound: %v, want %v", root.Ratio, maxRatio)
	}
}
