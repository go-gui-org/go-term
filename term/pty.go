package term

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/creack/pty"
)

// ptyDev wraps a pseudoterminal master and the child shell process.
type ptyDev struct {
	cmd  *exec.Cmd
	file *os.File
}

// clampWinsize bounds rows/cols to the uint16 range expected by the
// kernel ioctl, with a sane lower bound so a degenerate caller can't
// hand the shell a 0-row terminal.
func clampWinsize(n int) uint16 {
	if n < 1 {
		return 1
	}
	if n > 0xFFFF {
		return 0xFFFF
	}
	return uint16(n)
}

// startPTY spawns the shell configured in cfg (default $SHELL, fallback
// /bin/sh) attached to a new pty sized rows×cols. TERM is forced to
// xterm-256color so apps emit standard SGR sequences. cfg.Command, cfg.Args,
// and cfg.Env allow callers to override the command and environment.
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
	env := os.Environ()
	// On macOS, GUI apps inherit a minimal PATH from launchd that omits
	// Homebrew directories (/opt/homebrew/bin, /usr/local/bin). Run
	// path_helper to construct the full system PATH from /etc/paths and
	// /etc/paths.d so tools such as starship and fzf are reachable from
	// shell startup files.
	if runtime.GOOS == "darwin" {
		if sp := darwinSystemPath(); sp != "" {
			env = replaceEnv(env, "PATH", sp)
		}
	}
	env = append(env, "TERM=xterm-256color")
	env = append(env, cfg.Env...)
	cmd.Env = env
	if cfg.Dir != "" {
		if _, err := os.Stat(cfg.Dir); err == nil {
			cmd.Dir = cfg.Dir
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
func (p *ptyDev) Close() error {
	err := p.file.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_, _ = p.cmd.Process.Wait()
	}
	return err
}

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

// replaceEnv replaces the first occurrence of key in env with key=val,
// or appends key=val if key is not present. The caller's slice is not
// mutated; a new slice is returned only when a replacement is made.
func replaceEnv(env []string, key, val string) []string {
	prefix := key + "="
	entry := prefix + val
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			out := make([]string, len(env))
			copy(out, env)
			out[i] = entry
			return out
		}
	}
	return append(env, entry)
}
