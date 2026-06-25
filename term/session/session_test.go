package session

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
