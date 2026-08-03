package term

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// End-to-end: run a real bash with the integration script sourced, feed the
// bytes it writes through the parser, and check the command spans that come
// out. The unit tests all synthesize OSC 133 by hand, which proves the parser
// works but not that the *scripts* emit what it expects — a misplaced mark or
// a shell-quoting slip in goterm.bash would pass every other test in this
// package while silently breaking the feature for every user.
func TestShellIntegration_BashEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash integration is not the Windows path")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	script, err := filepath.Abs(filepath.Join("..", "scripts", "shell-integration", "goterm.bash"))
	if err != nil {
		t.Fatalf("resolve script: %v", err)
	}

	// A minimal rc: a fixed prompt (so the recorded mark column is
	// predictable) and the script sourced twice, which is also the
	// idempotency check — a second copy of the PS1 marker would show up as a
	// duplicated B and split every span.
	rc := filepath.Join(t.TempDir(), "rc.bash")
	content := "PS1='P> '\nsource " + script + "\nsource " + script + "\n"
	if err := os.WriteFile(rc, []byte(content), 0o600); err != nil {
		t.Fatalf("write rc: %v", err)
	}

	cmd := exec.Command(bash, "--rcfile", rc, "-i")
	cmd.Stdin = strings.NewReader("echo hello\nfalse\nexit\n")
	// HOME is redirected so a developer's own bashrc cannot join in, and TERM
	// keeps bash from emitting terminfo init sequences that are noise here.
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "TERM=dumb")
	out, _ := cmd.CombinedOutput() // interactive bash exits non-zero on EOF
	if !strings.Contains(string(out), "\x1b]133;") {
		t.Skipf("shell emitted no marks in this environment:\n%s", out)
	}

	g := newGrid(24, 80)
	p := newParser(g)
	feed(t, g, p, out)

	g.Mu.Lock()
	defer g.Mu.Unlock()

	var spans []commandSpan
	g.forEachCommand(func(s commandSpan) bool {
		spans = append(spans, s)
		return true
	})

	// `echo hello` succeeded and `false` did not: the exit statuses are the
	// whole point of the D mark, and jump-to-last-failure is built on them.
	var sawSuccess, sawFailure bool
	for _, s := range spans {
		if !s.Ended {
			continue
		}
		if s.Exit == 0 {
			sawSuccess = true
		}
		if s.failed() {
			sawFailure = true
		}
	}
	if !sawSuccess {
		t.Errorf("no successful command span; spans = %+v", spans)
	}
	if !sawFailure {
		t.Errorf("`false` did not produce a failed span; spans = %+v", spans)
	}

	// The command text must exclude the prompt: this is what the notification
	// body shows, and it depends on the B mark landing after PS1 rather than
	// before it.
	if got := g.commandText(); strings.Contains(got, "P>") {
		t.Errorf("commandText() = %q; prompt leaked into the command line", got)
	}

	// Sourcing twice must not double the prompt marker.
	if n := strings.Count(string(out), "\x1b]133;B"); n > 0 {
		perPrompt := strings.Count(string(out), "P> \x1b]133;B\x07\x1b]133;B")
		if perPrompt > 0 {
			t.Errorf("prompt marker emitted twice per prompt; script is not idempotent")
		}
	}
}
