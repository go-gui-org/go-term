//go:build !windows

package term

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/creack/pty"
)

// ptyDev wraps a pty master (file) and the child shell process (cmd).
type ptyDev struct {
	cmd  *exec.Cmd
	file *os.File
	// closeOnce makes Close idempotent: the first call closes and reaps,
	// later calls are no-ops. Matches the Windows Once-guarded teardown so
	// both platforms honor one contract.
	closeOnce sync.Once
}

// startPTY spawns the shell configured in cfg (default $SHELL, fallback
// /bin/sh) attached to a new pty sized rows×cols. TERM is forced to
// xterm-256color so apps emit standard SGR sequences, a UTF-8 LANG is
// supplied when the environment carries no locale, and the host terminal's
// identity variables are scrubbed (hostTerminalEnvKeys) then replaced with
// this terminal's own name (selfIdentity). cfg.Command,
// cfg.Args, and cfg.Env allow callers to override the command and
// environment.
func startPTY(rows, cols int, cfg Cfg) (*ptyDev, error) {
	shell := cfg.Command
	args := cfg.Args
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
	}
	cmd := exec.Command(shell, args...)
	if err := checkIdentity(cfg.Identity); err != nil {
		return nil, err
	}
	// Identity of the *host* terminal is replaced by this one's first: what
	// follows describes this terminal, and a leftover TERM_PROGRAM would
	// outrank it. cfg.Identity names it; empty falls back to "go-term".
	env := baseChildEnv(cfg, os.Environ())
	// On macOS, GUI apps inherit a minimal PATH from launchd that omits
	// Homebrew directories (/opt/homebrew/bin, /usr/local/bin). Run
	// path_helper to construct the full system PATH from /etc/paths and
	// /etc/paths.d so tools such as starship and fzf are reachable from
	// shell startup files.
	if runtime.GOOS == "darwin" {
		if sp := cachedDarwinSystemPath(); sp != "" {
			env = setEnvEntry(env, "PATH="+sp)
		}
	}
	// A GUI launch (Finder, or any parent shell without LANG set) leaves the
	// child in the "C" locale, where ncurses and friends cannot emit UTF-8 —
	// wide glyphs arrive as mangled bytes. Supply a UTF-8 locale only when
	// the inherited environment pins none, so an explicit LC_ALL=C is kept.
	if !hasLocaleEnv(env) {
		env = setEnvEntry(env, "LANG="+defaultUTF8Locale())
	}
	// cfg.Env goes last so callers can override anything set above.
	cmd.Env = applyCfgEnv(env, cfg.Env)
	if cfg.Dir != "" {
		if st, err := os.Stat(cfg.Dir); err == nil && st.IsDir() {
			cmd.Dir = cfg.Dir
		} else if home, err := os.UserHomeDir(); err == nil {
			cmd.Dir = home
		}
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: clampWinsize(rows),
		Cols: clampWinsize(cols),
	})
	if err != nil {
		return nil, err
	}
	return &ptyDev{cmd: cmd, file: f}, nil
}

// Read forwards from the pty master.
func (p *ptyDev) Read(b []byte) (int, error) { return p.file.Read(b) }

// Write forwards to the pty master.
func (p *ptyDev) Write(b []byte) (int, error) { return p.file.Write(b) }

// Resize updates the pty winsize so child processes see the new
// rows/cols on their next stty/SIGWINCH.
func (p *ptyDev) Resize(rows, cols int) error {
	return pty.Setsize(p.file, &pty.Winsize{
		Rows: clampWinsize(rows),
		Cols: clampWinsize(cols),
	})
}

// Close releases the pty master and reaps the child if still alive.
// Idempotent best-effort teardown on the shared cross-platform contract: the
// first call closes and reaps, later calls are no-ops, and the return is
// always nil — a pty close error carries nothing the caller can act on.
func (p *ptyDev) Close() error {
	p.closeOnce.Do(func() {
		_ = p.file.Close()
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
			_, _ = p.cmd.Process.Wait()
		}
	})
	return nil
}

// PID returns the child process ID, or 0 when not started.
func (p *ptyDev) PID() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// cachedDarwinSystemPath memoizes darwinSystemPath: it forks path_helper on
// every call, and /etc/paths rarely changes under a running process. A ""
// (helper missing or unparseable) is cached too, avoiding a failing exec per
// spawn.
var cachedDarwinSystemPath = sync.OnceValue(darwinSystemPath)

// darwinSystemPath returns the standard macOS system PATH by running
// /usr/libexec/path_helper, which reads /etc/paths and /etc/paths.d/*.
// Returns "" when path_helper is unavailable or its output is unparseable.
func darwinSystemPath() string {
	out, err := exec.Command("/usr/libexec/path_helper", "-s").Output()
	if err != nil {
		return ""
	}
	// Output is: PATH="..."; export PATH;
	s := string(out)
	const prefix = `PATH="`
	i := strings.Index(s, prefix)
	if i < 0 {
		return ""
	}
	s = s[i+len(prefix):]
	if j := strings.IndexByte(s, '"'); j >= 0 {
		return s[:j]
	}
	return ""
}
