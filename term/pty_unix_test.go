//go:build !windows

package term

import (
	"os"
	"strings"
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

// envValue returns the last value for key in a cmd.Env slice ("" when absent),
// matching execve semantics where later entries win.
func envValue(env []string, key string) string {
	prefix := key + "="
	val := ""
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			val = e[len(prefix):]
		}
	}
	return val
}

// A child inheriting no locale (the macOS GUI-launch case) must be given a
// UTF-8 one, or ncurses apps emit mangled bytes for wide glyphs.
func TestStartPTY_SuppliesUTF8LocaleWhenUnset(t *testing.T) {
	t.Setenv("LANG", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	p, err := startPTY(24, 80, Cfg{Command: "/bin/sh", Args: []string{"-c", "exit 0"}})
	if err != nil {
		t.Skipf("startPTY: %v", err)
	}
	defer func() { _ = p.Close() }()
	if got := envValue(p.cmd.Env, "LANG"); !strings.HasSuffix(got, ".UTF-8") {
		t.Errorf("LANG = %q, want a .UTF-8 locale", got)
	}
}

// An explicit locale is the user's choice — including a non-UTF-8 one.
func TestStartPTY_KeepsExplicitLocale(t *testing.T) {
	t.Setenv("LANG", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LC_ALL", "C")
	p, err := startPTY(24, 80, Cfg{Command: "/bin/sh", Args: []string{"-c", "exit 0"}})
	if err != nil {
		t.Skipf("startPTY: %v", err)
	}
	defer func() { _ = p.Close() }()
	if got := envValue(p.cmd.Env, "LANG"); got != "" {
		t.Errorf("LANG = %q, want it left unset when LC_ALL is set", got)
	}
	if got := envValue(p.cmd.Env, "LC_ALL"); got != "C" {
		t.Errorf("LC_ALL = %q, want C", got)
	}
}

// cfg.Env is applied last, so a caller can always override the default.
func TestStartPTY_CfgEnvOverridesLocale(t *testing.T) {
	t.Setenv("LANG", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	cfg := Cfg{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 0"},
		Env:     []string{"LANG=ja_JP.UTF-8"},
	}
	p, err := startPTY(24, 80, cfg)
	if err != nil {
		t.Skipf("startPTY: %v", err)
	}
	defer func() { _ = p.Close() }()
	if got := envValue(p.cmd.Env, "LANG"); got != "ja_JP.UTF-8" {
		t.Errorf("LANG = %q, want ja_JP.UTF-8", got)
	}
}

// The widget renders 24-bit color, so the child must be told: TERM alone
// only promises the 256-color palette and TUI toolkits quantize without
// COLORTERM. cfg.Env still wins, per TestStartPTY_CfgEnvOverridesLocale.
func TestStartPTY_AdvertisesTruecolor(t *testing.T) {
	p, err := startPTY(24, 80, Cfg{Command: "/bin/sh", Args: []string{"-c", "exit 0"}})
	if err != nil {
		t.Skipf("startPTY: %v", err)
	}
	defer func() { _ = p.Close() }()
	if got := envValue(p.cmd.Env, "COLORTERM"); got != "truecolor" {
		t.Errorf("COLORTERM = %q, want %q", got, "truecolor")
	}
	if got := envValue(p.cmd.Env, "TERM"); got != "xterm-256color" {
		t.Errorf("TERM = %q, want %q", got, "xterm-256color")
	}
}

// A caller that deliberately downgrades color must still win: cfg.Env is
// appended last and envValue takes the final occurrence.
func TestStartPTY_CfgEnvOverridesColorterm(t *testing.T) {
	p, err := startPTY(24, 80, Cfg{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 0"},
		Env:     []string{"COLORTERM="},
	})
	if err != nil {
		t.Skipf("startPTY: %v", err)
	}
	defer func() { _ = p.Close() }()
	if got := envValue(p.cmd.Env, "COLORTERM"); got != "" {
		t.Errorf("COLORTERM = %q, want empty (caller override)", got)
	}
}

// A child must probe this terminal's capabilities, not inherit the host's
// identity: yazi selects its image protocol from TERM_PROGRAM, so leaking it
// made preview behavior depend on which terminal launched the app.
func TestStartPTY_ScrubsHostTerminalIdentity(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("TERM_PROGRAM_VERSION", "3.5")
	t.Setenv("ITERM_SESSION_ID", "w0t0p0")

	p, err := startPTY(24, 80, Cfg{Command: "/bin/sh", Args: []string{"-c", "exit 0"}})
	if err != nil {
		t.Skipf("startPTY: %v", err)
	}
	defer func() { _ = p.Close() }()
	if got := envValue(p.cmd.Env, "ITERM_SESSION_ID"); got != "" {
		t.Errorf("ITERM_SESSION_ID leaked to child as %q", got)
	}
	// The host's name is replaced by this terminal's own, not merely removed:
	// an unset TERM_PROGRAM reads as "no information" rather than "not the
	// terminal that launched me".
	if got := envValue(p.cmd.Env, "TERM_PROGRAM"); got != "go-term" {
		t.Errorf("TERM_PROGRAM = %q; want go-term", got)
	}
	if got := envValue(p.cmd.Env, "TERM_PROGRAM_VERSION"); got != termVersion {
		t.Errorf("TERM_PROGRAM_VERSION = %q; want %q", got, termVersion)
	}
}

// cfg.Env is applied after the scrub, so an embedder can still say who it is.
func TestStartPTY_CfgEnvCanRestoreTermProgram(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	p, err := startPTY(24, 80, Cfg{
		Command: "/bin/sh", Args: []string{"-c", "exit 0"},
		Env: []string{"TERM_PROGRAM=falcon"},
	})
	if err != nil {
		t.Skipf("startPTY: %v", err)
	}
	defer func() { _ = p.Close() }()
	if got := envValue(p.cmd.Env, "TERM_PROGRAM"); got != "falcon" {
		t.Errorf("TERM_PROGRAM = %q; want falcon", got)
	}
}
