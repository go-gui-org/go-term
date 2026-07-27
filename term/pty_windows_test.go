//go:build windows

package term

import (
	"strings"
	"testing"
	"time"
)

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
