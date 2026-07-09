//go:build windows

package term

import (
	"errors"
	"os"
)

// ErrUnsupported signals that ConPTY is not yet implemented on Windows.
var ErrUnsupported = errors.New("term: ConPTY not yet supported on Windows")

// startPTY returns ErrUnsupported on Windows.
func startPTY(rows, cols int, cfg Cfg) (*ptyDev, error) {
	shell := cfg.Command
	if shell == "" {
		shell = os.Getenv("ComSpec")
		if shell == "" {
			shell = "cmd.exe"
		}
	}
	return nil, ErrUnsupported
}

// Read always returns ErrUnsupported.
func (p *ptyDev) Read(b []byte) (int, error) { return 0, ErrUnsupported }

// Write always returns ErrUnsupported.
func (p *ptyDev) Write(b []byte) (int, error) { return 0, ErrUnsupported }

// Resize always returns ErrUnsupported.
func (p *ptyDev) Resize(rows, cols int) error { return ErrUnsupported }

// Close is a no-op and returns nil.
func (p *ptyDev) Close() error { return nil }

// PID returns 0 on Windows.
func (p *ptyDev) PID() int { return 0 }
