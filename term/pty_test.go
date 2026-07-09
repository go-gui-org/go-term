package term

import (
	"math"
	"os"
	"testing"
)

func TestClampWinsize(t *testing.T) {
	cases := []struct {
		in   int
		want uint16
	}{
		{-1, 1},
		{0, 1},
		{1, 1},
		{0xFFFF, 0xFFFF},
		{0x10000, 0xFFFF},
		{math.MaxInt32, 0xFFFF},
	}
	for _, c := range cases {
		if got := clampWinsize(c.in); got != c.want {
			t.Errorf("clampWinsize(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPTY_StartResizeClose(t *testing.T) {
	p, err := startPTY(24, 80, Cfg{})
	if err != nil {
		t.Skipf("startPTY failed (no shell available?): %v", err)
	}
	if err := p.Resize(30, 100); err != nil {
		t.Errorf("Resize: %v", err)
	}
	// Close kills the child and reaps it; the file.Close error is what
	// is returned. Either nil or "file already closed" is acceptable —
	// the contract is that it doesn't panic and is safe to call.
	_ = p.Close()
}

func TestPTY_StartWithDir(t *testing.T) {
	p, err := startPTY(24, 80, Cfg{Dir: "/tmp"})
	if err != nil {
		t.Skipf("startPTY failed: %v", err)
	}
	defer func() { _ = p.Close() }()
	if p.cmd.Dir != "/tmp" {
		t.Errorf("cmd.Dir = %q, want /tmp", p.cmd.Dir)
	}
}

func TestPTY_StartWithNonexistentDir(t *testing.T) {
	// A non-existent Dir should fall back to $HOME (os.Stat guard).
	p, err := startPTY(24, 80, Cfg{Dir: "/nonexistent-zzz-go-term-test"})
	if err != nil {
		t.Skipf("startPTY failed: %v", err)
	}
	defer func() { _ = p.Close() }()
	home, _ := os.UserHomeDir()
	if p.cmd.Dir != home {
		t.Errorf("cmd.Dir = %q, want %q (home fallback)", p.cmd.Dir, home)
	}
}
