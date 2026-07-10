package term

// ptyIO is the PTY interface: platform-specific implementations
// satisfy this. Read, Write, Resize, Close, and PID cover the
// full lifecycle.
type ptyIO interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(int, int) error
	Close() error
	PID() int
}

// ptyDev wraps a pseudoterminal master and the child shell process. The
// concrete struct is platform-specific (pty_unix.go / pty_windows.go); both
// keep cmd and file fields so cross-platform tests can construct it directly.

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
