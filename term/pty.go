package term

import (
	"os"
	"os/exec"

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

// startPTY spawns $SHELL (fallback /bin/sh) attached to a new pty sized
// rows×cols. TERM is forced to xterm-256color so apps emit standard
// SGR sequences.
func startPTY(rows, cols int) (*ptyDev, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
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
