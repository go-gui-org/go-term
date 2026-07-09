//go:build !windows

package term

import (
	"os"
	"testing"
)

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
