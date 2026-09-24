package term

import (
	"bytes"
	"log"
	"os/exec"
	"strings"
	"testing"
)

// A link whose handler cannot start (no xdg-open on a minimal Linux) must leave
// a log line. Dropping the error made Cmd+click do nothing with no trace.
func TestOpenURL_LogsStartError(t *testing.T) {
	orig := startOpener
	t.Cleanup(func() { startOpener = orig })
	startOpener = func(*exec.Cmd) error { return exec.ErrNotFound }

	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })

	openURL("https://example.com")

	if out := buf.String(); !strings.Contains(out, "https://example.com") ||
		!strings.Contains(out, exec.ErrNotFound.Error()) {
		t.Errorf("log = %q, want the URL and the start error", out)
	}
}
