//go:build windows

package term

import (
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ptyDev drives a child shell through a Windows pseudoconsole (ConPTY).
// ConPTY does not expose a single bidirectional fd like a Unix master, so
// input and output are separate anonymous pipes wired into the console at
// creation. cmd is unused at runtime; it exists only so cross-platform
// tests that construct ptyDev directly keep compiling.
type ptyDev struct {
	cmd    *exec.Cmd      // unused; present for cross-platform test construction
	file   *os.File       // console output read-end (reader goroutine Read())
	in     *os.File       // console input write-end (Write())
	hpc    windows.Handle // pseudoconsole (HPCON)
	proc   windows.Handle // child process handle
	thread windows.Handle // child primary thread handle
	pid    int

	closeOnce sync.Once
}

// startPTY spawns the configured shell (default $env:ComSpec, fallback
// cmd.exe) attached to a new pseudoconsole sized rows×cols. TERM is forced to
// xterm-256color so cross-platform tools emit standard SGR sequences. cfg
// overrides command, args, environment, and working directory.
func startPTY(rows, cols int, cfg Cfg) (*ptyDev, error) {
	shell := cfg.Command
	args := cfg.Args
	if shell == "" {
		shell = os.Getenv("ComSpec")
		if shell == "" {
			shell = "cmd.exe"
		}
	}

	// Two anonymous pipes via the Win32 API directly (not os.Pipe, whose
	// handles are associated with Go's runtime poller and misbehave when
	// handed to conhost). inR/inW feed the child's stdin, outR/outW carry its
	// stdout. CreatePseudoConsole dups the console-side ends, closed locally
	// afterward; the kept ends are wrapped as *os.File for blocking I/O.
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		_ = windows.CloseHandle(inR)
		_ = windows.CloseHandle(inW)
		return nil, err
	}

	var hpc windows.Handle
	err := windows.CreatePseudoConsole(coordSize(rows, cols), inR, outW, 0, &hpc)
	// ConPTY dup'd (or failed on) the console-side ends; release them locally.
	_ = windows.CloseHandle(inR)
	_ = windows.CloseHandle(outW)
	if err != nil {
		_ = windows.CloseHandle(inW)
		_ = windows.CloseHandle(outR)
		return nil, err
	}
	inFile := os.NewFile(uintptr(inW), "conpty-in")
	outFile := os.NewFile(uintptr(outR), "conpty-out")

	// Attach the pseudoconsole to the child via a proc-thread attribute list.
	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = inFile.Close()
		_ = outFile.Close()
		return nil, err
	}
	defer attrList.Delete()
	// The PSEUDOCONSOLE attribute value is the HPCON itself, passed as the
	// lpValue pointer (not a pointer to it). Reinterpret the handle's bits as
	// unsafe.Pointer via its address so go vet's unsafeptr check does not flag
	// a direct uintptr→Pointer conversion.
	hpcValue := *(*unsafe.Pointer)(unsafe.Pointer(&hpc))
	if err := attrList.Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		hpcValue, unsafe.Sizeof(hpc)); err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = inFile.Close()
		_ = outFile.Close()
		return nil, err
	}

	si := &windows.StartupInfoEx{
		StartupInfo:             windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))},
		ProcThreadAttributeList: attrList.List(),
	}
	// Force the child onto the pseudoconsole's std handles instead of
	// inheriting the parent's real console.
	si.Flags |= windows.STARTF_USESTDHANDLES

	env := append(os.Environ(), "TERM=xterm-256color")
	env = append(env, cfg.Env...)
	envBlock, err := createEnvBlock(env)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = inFile.Close()
		_ = outFile.Close()
		return nil, err
	}

	dir := cfg.Dir
	if dir != "" {
		if _, err := os.Stat(dir); err != nil {
			if home, herr := os.UserHomeDir(); herr == nil {
				dir = home
			} else {
				dir = ""
			}
		}
	}
	var dirPtr *uint16
	if dir != "" {
		if dirPtr, err = windows.UTF16PtrFromString(dir); err != nil {
			windows.ClosePseudoConsole(hpc)
			_ = inFile.Close()
			_ = outFile.Close()
			return nil, err
		}
	}

	cmdLine, err := windows.UTF16PtrFromString(
		windows.ComposeCommandLine(append([]string{shell}, args...)))
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = inFile.Close()
		_ = outFile.Close()
		return nil, err
	}

	var pi windows.ProcessInformation
	err = windows.CreateProcess(
		nil, cmdLine, nil, nil, false,
		windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT,
		envBlock, dirPtr, &si.StartupInfo, &pi)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = inFile.Close()
		_ = outFile.Close()
		return nil, err
	}

	p := &ptyDev{
		cmd:    exec.Command(shell, args...),
		file:   outFile,
		in:     inFile,
		hpc:    hpc,
		proc:   pi.Process,
		thread: pi.Thread,
		pid:    int(pi.ProcessId),
	}

	// ConPTY's output pipe does not reliably reach EOF when the child exits
	// until the console is closed. Wait for the child, then tear the console
	// down so the reader goroutine's Read returns and readDone closes (drives
	// Alive() and ExitWhenLastShellExits). This goroutine owns the process and
	// thread handles.
	go func() {
		_, _ = windows.WaitForSingleObject(p.proc, windows.INFINITE)
		p.release()
		_ = windows.CloseHandle(p.thread)
		_ = windows.CloseHandle(p.proc)
	}()

	return p, nil
}

// release closes the pseudoconsole and the process-owned pipe ends exactly
// once. Closing the console terminates the attached child and unblocks a
// reader parked in Read.
func (p *ptyDev) release() {
	p.closeOnce.Do(func() {
		windows.ClosePseudoConsole(p.hpc)
		_ = p.file.Close()
		_ = p.in.Close()
	})
}

// Read forwards from the console output pipe.
func (p *ptyDev) Read(b []byte) (int, error) { return p.file.Read(b) }

// Write forwards to the console input pipe.
func (p *ptyDev) Write(b []byte) (int, error) { return p.in.Write(b) }

// Resize updates the pseudoconsole dimensions; the child sees the new size on
// its next query (ConPTY raises the equivalent of SIGWINCH internally).
func (p *ptyDev) Resize(rows, cols int) error {
	return windows.ResizePseudoConsole(p.hpc, coordSize(rows, cols))
}

// Close tears down the console, which terminates the child. Safe to call
// repeatedly; the wait goroutine reaps the process and thread handles once
// WaitForSingleObject returns.
func (p *ptyDev) Close() error {
	p.release()
	return nil
}

// PID returns the child process ID, or 0 when not started.
func (p *ptyDev) PID() int { return p.pid }

// coordSize builds a ConPTY Coord, clamping rows/cols to the positive int16
// range so a degenerate caller cannot hand the console a 0- or overflowed
// dimension.
func coordSize(rows, cols int) windows.Coord {
	return windows.Coord{X: clampCoord(cols), Y: clampCoord(rows)}
}

func clampCoord(n int) int16 {
	if n < 1 {
		return 1
	}
	if n > 0x7FFF {
		return 0x7FFF
	}
	return int16(n)
}

// createEnvBlock builds the UTF-16, double-null-terminated environment block
// CreateProcess expects with CREATE_UNICODE_ENVIRONMENT. Returns nil for an
// empty slice (child inherits the parent environment).
func createEnvBlock(env []string) (*uint16, error) {
	if len(env) == 0 {
		return nil, nil
	}
	var block []uint16
	for _, e := range env {
		u, err := windows.UTF16FromString(e)
		if err != nil {
			return nil, err // embedded NUL: skip the whole block build
		}
		block = append(block, u...) // u carries its own trailing NUL
	}
	block = append(block, 0) // terminating NUL for the block
	return &block[0], nil
}
