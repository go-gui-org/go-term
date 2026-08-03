package term

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The shell integration scripts are the only part of the OSC 133 feature set
// that is not Go, and a syntax error in one is silent: the shell prints a
// complaint into the user's session and every mark-driven feature quietly
// stops working. Each shell's own parser is the check.
//
// Behavior is not asserted here — driving three interactive shells through a
// pty is too environment-dependent for CI. What this catches is the breakage
// that actually happens when the scripts are edited.
func TestShellIntegrationScripts_Syntax(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shells and fish are not the Windows integration path")
	}
	dir := shellIntegrationDir(t)
	tests := []struct {
		shell  string
		script string
		args   []string
	}{
		{"bash", "goterm.bash", []string{"-n"}},
		{"zsh", "goterm.zsh", []string{"-n"}},
		{"fish", "goterm.fish", []string{"--no-execute"}},
	}
	for _, tc := range tests {
		t.Run(tc.shell, func(t *testing.T) {
			bin, err := exec.LookPath(tc.shell)
			if err != nil {
				t.Skipf("%s not installed", tc.shell)
			}
			path := filepath.Join(dir, tc.script)
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("script missing: %v", err)
			}
			out, err := exec.Command(bin, append(tc.args, path)...).CombinedOutput()
			if err != nil {
				t.Errorf("%s syntax check failed: %v\n%s", tc.script, err, out)
			}
		})
	}
}

// Sourcing an integration script twice is normal — a nested shell, a reloaded
// rc file — and must not append a second copy of the prompt mark or chain the
// hooks onto themselves. Every script therefore opens with the same guard.
func TestShellIntegrationScripts_HaveLoadGuard(t *testing.T) {
	dir := shellIntegrationDir(t)
	for _, name := range []string{"goterm.bash", "goterm.zsh", "goterm.fish"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(data), "__goterm_integration_loaded") {
			t.Errorf("%s has no __goterm_integration_loaded guard", name)
		}
	}
}

// shellIntegrationDir locates scripts/shell-integration/ from this package.
func shellIntegrationDir(t *testing.T) string {
	t.Helper()
	// term → repo root.
	dir := filepath.Join("..", "scripts", "shell-integration")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("shell-integration directory not found: %v", err)
	}
	return dir
}
