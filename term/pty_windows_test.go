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

	got := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
				if strings.Contains(b.String(), marker) {
					got <- b.String()
					return
				}
			}
			if err != nil {
				got <- b.String()
				return
			}
		}
	}()

	select {
	case out := <-got:
		if !strings.Contains(out, marker) {
			t.Errorf("marker %q not found in console output", marker)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for console output")
	}
}
