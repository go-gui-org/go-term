package main

// End-to-end tests for the typescript → fixture conversion.
//
// Everything lives in main(), which reads the global flag set and calls
// os.Exit on failure. That shapes what is testable here: the success path is
// driven in-process by swapping os.Args and flag.CommandLine, while the error
// paths (missing flags, unreadable script) would take the test binary down
// with them and are left to the tool's own usage output. Extracting a
// run(args) error seam would cover those too, but that is a change to the
// tool rather than a test, so it is deliberately not done here.

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runMain drives main() with the given arguments, restoring the global flag
// state afterwards so tests do not contaminate one another. Redefining flags
// on a reused FlagSet panics, hence the fresh one per call.
func runMain(t *testing.T, args ...string) {
	t.Helper()
	origArgs, origFlags := os.Args, flag.CommandLine
	t.Cleanup(func() { os.Args, flag.CommandLine = origArgs, origFlags })

	flag.CommandLine = flag.NewFlagSet("script2fixture", flag.ContinueOnError)
	os.Args = append([]string{"script2fixture"}, args...)
	main()
}

// readFixture loads and decodes a written fixture.
func readFixture(t *testing.T, path string) (map[string]any, string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var fix map[string]any
	if err := json.Unmarshal(b, &fix); err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}
	return fix, string(b)
}

// A typescript of plain text converts to a fixture carrying that text.
func TestScript2Fixture_WritesFixture(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "session.script")
	if err := os.WriteFile(script, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	out := filepath.Join(dir, "fixtures")

	runMain(t, "-name", "demo", "-script", script, "-out", out)

	fix, raw := readFixture(t, filepath.Join(out, "demo.json"))
	if fix["name"] != "demo" {
		t.Errorf("name = %v, want demo", fix["name"])
	}
	// Default geometry when the flags are omitted.
	if got, _ := fix["rows"].(float64); int(got) != 24 {
		t.Errorf("rows = %v, want 24", fix["rows"])
	}
	if got, _ := fix["cols"].(float64); int(got) != 80 {
		t.Errorf("cols = %v, want 80", fix["cols"])
	}
	if !strings.Contains(raw, "hello world") {
		t.Errorf("fixture does not carry the script's text:\n%s", raw)
	}
}

// Geometry flags reach the fixture, and the output directory is created on
// demand rather than required to exist.
func TestScript2Fixture_GeometryAndNestedOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "session.script")
	if err := os.WriteFile(script, []byte("abc"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	out := filepath.Join(dir, "deeply", "nested")

	runMain(t, "-name", "sized", "-script", script,
		"-rows", "12", "-cols", "40", "-out", out)

	fix, _ := readFixture(t, filepath.Join(out, "sized.json"))
	if got, _ := fix["rows"].(float64); int(got) != 12 {
		t.Errorf("rows = %v, want 12", fix["rows"])
	}
	if got, _ := fix["cols"].(float64); int(got) != 40 {
		t.Errorf("cols = %v, want 40", fix["cols"])
	}
}

// Escape sequences in the typescript are interpreted by the parser rather
// than copied through — the tool captures a post-parse grid snapshot, which
// is the whole reason it exists.
func TestScript2Fixture_InterpretsEscapeSequences(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "session.script")
	// Write "XY", home the cursor, then overwrite the first cell with "Z".
	// A parsed grid holds "ZY"; raw bytes would still hold the CSI sequence.
	if err := os.WriteFile(script, []byte("XY\x1b[HZ"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	out := filepath.Join(dir, "fixtures")

	runMain(t, "-name", "escapes", "-script", script, "-out", out)

	_, raw := readFixture(t, filepath.Join(out, "escapes.json"))
	// JSON renders a literal ESC byte as the six characters ; finding
	// it would mean the input was passed through unparsed.
	if strings.Contains(raw, "\\u001b") {
		t.Error("fixture contains a raw escape sequence; input was not parsed")
	}
	if !strings.Contains(raw, "ZY") {
		t.Errorf("fixture does not show the overwritten row (want ZY):\n%s", raw)
	}
}
