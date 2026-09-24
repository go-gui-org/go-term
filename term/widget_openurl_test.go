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

	openURL("https://user:hunter2@example.com/reset?token=s3cret#frag")

	out := buf.String()
	if !strings.Contains(out, "https://example.com") ||
		!strings.Contains(out, exec.ErrNotFound.Error()) {
		t.Errorf("log = %q, want the URL's host and the start error", out)
	}
	// Links carry secrets (reset tokens, userinfo passwords); the log line
	// must not copy them into a file the user may attach to a bug report.
	for _, secret := range []string{"hunter2", "s3cret", "/reset", "frag"} {
		if strings.Contains(out, secret) {
			t.Errorf("log = %q, leaks %q", out, secret)
		}
	}
}

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com":                  "https://example.com",
		"http://u:p@host:8080/a?b=c":           "http://host:8080",
		"mailto:someone@example.com?subject=x": "mailto:",
		"https://[::1":                         "<unparsable URL>",
	} {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}
