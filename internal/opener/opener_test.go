//go:build !windows

package opener

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A child started by Start must be reaped after it exits. An unreaped child stays
// a zombie: kill(pid, 0) keeps succeeding for it. Once it is reaped the pid is
// gone and kill reports ESRCH.
func TestStart_ReapsChild(t *testing.T) {
	cmd := exec.Command("true")
	if err := Start(cmd); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child %d not reaped after it exited (zombie)", pid)
}

func TestStart_ReportsStartError(t *testing.T) {
	if err := Start(exec.Command("/nonexistent/opener")); err == nil {
		t.Fatal("Start of a missing program: want error")
	}
}

// Command must pass the target as one argv element of the platform handler,
// never through a shell.
func TestCommand_UsesPlatformHandler(t *testing.T) {
	const target = "https://example.com"
	cmd := Command(target)
	if cmd.Args[len(cmd.Args)-1] != target {
		t.Fatalf("Command args = %q, want target last", cmd.Args)
	}
	var want string
	switch runtime.GOOS {
	case "darwin":
		want = "open"
	case "windows":
		want = "rundll32"
	default:
		want = "xdg-open"
	}
	if base := cmd.Args[0]; !strings.HasSuffix(base, want) {
		t.Errorf("handler = %q, want %q (args %q)", cmd.Args[0], want, cmd.Args)
	}
}
