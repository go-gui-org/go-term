//go:build windows

package term

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestPTY_GlyphWidthFlagAccepted pins that asking conhost for grapheme-based
// text measurement cannot break console creation. The flag is advisory: builds
// that understand it align their text buffer with the grid's width model,
// older ones are supposed to mask the bit off and carry on. "Supposed to" is
// the part worth testing — if some build ever validates the flag word instead
// of masking it, every Windows session dies at startup, and the plain-spawn
// tests above would report that as a skip rather than a failure.
//
// So compare the two spawns directly rather than asserting the flagged one
// succeeds outright: an unflagged failure means ConPTY is unavailable in this
// environment (skip), while an unflagged success paired with a flagged failure
// isolates the flag as the cause.
func TestPTY_GlyphWidthFlagAccepted(t *testing.T) {
	tryCreate := func(flags uint32) error {
		var inR, inW, outR, outW windows.Handle
		if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
			t.Fatalf("CreatePipe: %v", err)
		}
		defer func() { _ = windows.CloseHandle(inW) }()
		if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
			_ = windows.CloseHandle(inR)
			t.Fatalf("CreatePipe: %v", err)
		}
		defer func() { _ = windows.CloseHandle(outR) }()

		var hpc windows.Handle
		err := windows.CreatePseudoConsole(coordSize(24, 80), inR, outW, flags, &hpc)
		// ConPTY dup'd (or failed on) the console-side ends either way.
		_ = windows.CloseHandle(inR)
		_ = windows.CloseHandle(outW)
		if err == nil {
			windows.ClosePseudoConsole(hpc)
		}
		return err
	}

	if err := tryCreate(0); err != nil {
		t.Skipf("ConPTY unavailable: CreatePseudoConsole with no flags failed: %v", err)
	}
	if err := tryCreate(pseudoconsoleGlyphWidthGraphemes); err != nil {
		t.Fatalf("CreatePseudoConsole rejected the glyph-width flag (0x%x): %v\n"+
			"  the same call with flags=0 succeeded, so this conhost validates\n"+
			"  the flag word instead of masking unknown bits",
			pseudoconsoleGlyphWidthGraphemes, err)
	}
}

// TestPTY_WindowsEcho spawns cmd.exe through ConPTY, drives it to echo a
// marker, and confirms the marker comes back on the output pipe. Exercises
// the full spawn → Write → Read → Close path.
func TestPTY_WindowsEcho(t *testing.T) {
	p, err := startPTY(24, 80, Cfg{})
	if err != nil {
		t.Skipf("startPTY failed: %v", err)
	}
	defer func() { _ = p.Close() }()

	const marker = "go-term-conpty-ok"
	if _, err := p.Write([]byte("echo " + marker + "\r\nexit\r\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// result carries the read error alongside the output. Without the error
	// a failure is ambiguous: "no marker" reads the same whether the console
	// produced other text or the child's pipe broke before it echoed
	// anything, and those have different causes.
	type result struct {
		out  string
		err  error
		read int
	}

	got := make(chan result, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		reads := 0
		for {
			n, err := p.Read(buf)
			if n > 0 {
				reads++
				b.Write(buf[:n])
				if strings.Contains(b.String(), marker) {
					got <- result{out: b.String(), read: reads}
					return
				}
			}
			if err != nil {
				got <- result{out: b.String(), err: err, read: reads}
				return
			}
		}
	}()

	select {
	case r := <-got:
		if !strings.Contains(r.out, marker) {
			t.Errorf("marker %q not found in console output\n"+
				"  read error: %v\n"+
				"  successful reads: %d\n"+
				"  bytes received: %d\n"+
				"  output: %q",
				marker, r.err, r.read, len(r.out), r.out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for console output")
	}
}

// TestPTY_CloseBounded pins the teardown invariant: Close must return
// promptly even when the child ignores the console teardown. Regression for
// the intermittent workspace-test hang where closeOutput blocked forever in
// CancelIoEx: a child still alive at its prompt kept conhost holding the
// output pipe's write end, so the reader goroutine stayed parked in Read,
// and CancelIoEx cannot cancel a synchronous read issued from another
// goroutine. Close must force-kill the child rather than trust console
// close to terminate it.
func TestPTY_CloseBounded(t *testing.T) {
	p, err := startPTY(24, 80, Cfg{})
	if err != nil {
		t.Skipf("startPTY failed: %v", err)
	}
	// Feed nothing: the child stays at its interactive prompt, alive and
	// holding the console, which is exactly the state that used to hang.

	done := make(chan struct{})
	go func() {
		_ = p.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("pty.Close blocked longer than 10s with a live child")
	}
}
