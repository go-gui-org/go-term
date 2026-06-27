package workspace

import (
	"testing"
)

// ---------------------------------------------------------------------------
// truncateTitle
// ---------------------------------------------------------------------------

func TestTruncateTitle_ShortPassthrough(t *testing.T) {
	if got := truncateTitle("hello", 10); got != "hello" {
		t.Errorf("got %q, want \"hello\"", got)
	}
}

func TestTruncateTitle_ExactlyMax(t *testing.T) {
	title := "1234567890" // 10 runes
	if got := truncateTitle(title, 10); got != title {
		t.Errorf("got %q, want %q", got, title)
	}
}

func TestTruncateTitle_LongerThanMax(t *testing.T) {
	if got := truncateTitle("hello world", 8); got != "hello..." {
		t.Errorf("got %q, want \"hello...\"", got)
	}
}

func TestTruncateTitle_MultiByteRuneAtBoundary(t *testing.T) {
	// "café" is 4 runes: c a f é. Truncating to max=4 leaves "café".
	// Truncating to max=3 should give "..." (keep = 0 runes + ellipsis).
	title := "café"
	if got := truncateTitle(title, 4); got != title {
		t.Errorf("got %q, want %q", got, title)
	}
	if got := truncateTitle(title, 3); got != "..." {
		t.Errorf("got %q, want \"...\"", got)
	}
}

func TestTruncateTitle_Empty(t *testing.T) {
	if got := truncateTitle("", 5); got != "" {
		t.Errorf("got %q, want \"\"", got)
	}
}

func TestTruncateTitle_MaxLessThanThree(t *testing.T) {
	// max=2: keep = max-3 = -1 → clamped to 0 → "..." (3 runes,
	// longer than max, but ellipsis is non-negotiable).
	if got := truncateTitle("abcdef", 2); got != "..." {
		t.Errorf("got %q, want \"...\"", got)
	}
}

func TestTruncateTitle_MaxZero(t *testing.T) {
	// max=0: keep = max-3 = -3 → clamped to 0 → "..."
	if got := truncateTitle("abcdef", 0); got != "..." {
		t.Errorf("got %q, want \"...\"", got)
	}
}

// ---------------------------------------------------------------------------
// New
// ---------------------------------------------------------------------------

func TestNew_NilWindowReturnsError(t *testing.T) {
	ws, err := New(nil, Cfg{})
	if err == nil {
		t.Fatal("expected error for nil window, got nil")
	}
	if ws != nil {
		t.Errorf("expected nil Workspace on error, got %v", ws)
	}
}

// ---------------------------------------------------------------------------
// Tab navigation no-op paths
//
// These exercise the early-return guards that do not touch the window, so a
// Workspace can be hand-built with a nil window. Index changes that would
// reach refresh()/activateTab's switch path need a live *gui.Window and are
// covered visually via examples/demo.
// ---------------------------------------------------------------------------

func TestGoToTab_OutOfRangeNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{}, {}}, activeTab: 1}
	ws.GoToTab(-1)
	if ws.activeTab != 1 {
		t.Errorf("negative index changed activeTab to %d, want 1", ws.activeTab)
	}
	ws.GoToTab(5)
	if ws.activeTab != 1 {
		t.Errorf("too-large index changed activeTab to %d, want 1", ws.activeTab)
	}
}

func TestGoToTab_SameIndexNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{}, {}}, activeTab: 1}
	ws.GoToTab(1) // activateTab returns early when idx == activeTab
	if ws.activeTab != 1 {
		t.Errorf("same-index GoToTab changed activeTab to %d, want 1", ws.activeTab)
	}
}

func TestNextTab_SingleTabNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{}}, activeTab: 0}
	ws.NextTab()
	if ws.activeTab != 0 {
		t.Errorf("NextTab with one tab changed activeTab to %d, want 0", ws.activeTab)
	}
}

func TestPrevTab_SingleTabNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{}}, activeTab: 0}
	ws.PrevTab()
	if ws.activeTab != 0 {
		t.Errorf("PrevTab with one tab changed activeTab to %d, want 0", ws.activeTab)
	}
}
