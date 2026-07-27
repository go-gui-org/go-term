package main

// Subcommand tests over a recording built in-process. The CLI is the tool a
// user reaches for when triaging a bug report, so the failure that matters is
// a command that stops working — not one whose formatting drifted. Assertions
// stay on substance: the bytes cat emits, the fixture file's contents, the
// asciicast structure, and the error paths.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-gui-org/go-term/internal/recfmt"
)

// recording writes a .gtr with a known shape: two output frames, one input
// frame, and one resize. Returns its path.
func recording(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.gtr")
	r, err := recfmt.NewRecorder(path, 24, 80, map[string]string{
		"TERM":  "xterm-256color",
		"SHELL": "/bin/bash",
	}, true)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	r.Output([]byte("hello "))
	r.Input([]byte("x"))
	r.Output([]byte("world\r\n"))
	r.Resize(30, 100)
	if err := r.Close(); err != nil {
		t.Fatalf("Recorder.Close: %v", err)
	}
	return path
}

// wantOutputBytes is the concatenation of the recording's output frames —
// what cat and play must reproduce byte for byte.
const wantOutputBytes = "hello world\r\n"

// captureStdout redirects os.Stdout for the duration of fn. The commands
// write there directly, so there is no injection seam to use instead.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()

	cmdErr := fn()

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out, cmdErr
}

// info reports the header and frame tallies.
func TestCmdInfo(t *testing.T) {
	path := recording(t)

	out, err := captureStdout(t, func() error { return cmdInfo([]string{path}) })
	if err != nil {
		t.Fatalf("cmdInfo: %v", err)
	}

	for _, want := range []string{
		"80×24",    // starting size, cols×rows
		"100×30",   // after the resize frame
		"2 output", // frame tallies
		"1 input",
		"1 resize",
		"13 bytes",       // len(wantOutputBytes)
		"xterm-256color", // env carried in the header
	} {
		if !strings.Contains(out, want) {
			t.Errorf("info output missing %q\n---\n%s", want, out)
		}
	}
}

// cat reproduces the output frames byte for byte and drops input frames —
// the point of the command is a stream identical to what the pty produced.
func TestCmdCat_OutputBytesOnly(t *testing.T) {
	path := recording(t)

	out, err := captureStdout(t, func() error { return cmdCat([]string{path}) })
	if err != nil {
		t.Fatalf("cmdCat: %v", err)
	}
	if out != wantOutputBytes {
		t.Errorf("cat = %q, want %q", out, wantOutputBytes)
	}
}

// play emits the same bytes as cat. Speed is cranked up so the test does not
// pay the recording's wall-clock timing.
func TestCmdPlay_EmitsSameBytesAsCat(t *testing.T) {
	path := recording(t)

	out, err := captureStdout(t, func() error {
		return cmdPlay([]string{path, "-speed", "1000"})
	})
	if err != nil {
		t.Fatalf("cmdPlay: %v", err)
	}
	if out != wantOutputBytes {
		t.Errorf("play = %q, want %q", out, wantOutputBytes)
	}
}

// A non-positive speed is treated as 1 rather than dividing by zero.
func TestCmdPlay_ZeroSpeedDoesNotHang(t *testing.T) {
	path := recording(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = captureStdout(t, func() error {
			return cmdPlay([]string{path, "-speed", "0", "-idle-limit", "1ms"})
		})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cmdPlay with -speed 0 did not finish")
	}
}

// fixture writes a replay-test fixture carrying the recording's output.
func TestCmdFixture_WritesUsableJSON(t *testing.T) {
	path := recording(t)
	outDir := t.TempDir()

	_, err := captureStdout(t, func() error {
		return cmdFixture([]string{path, "-name", "demo", "-rows", "10", "-cols", "40", "-out", outDir})
	})
	if err != nil {
		t.Fatalf("cmdFixture: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(outDir, "demo.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var fix map[string]any
	if err := json.Unmarshal(b, &fix); err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}
	if fix["name"] != "demo" {
		t.Errorf("fixture name = %v, want demo", fix["name"])
	}
	// Rows and cols must reflect the flags, not the recording's header —
	// the fixture describes the grid the replay test will build.
	if got, ok := fix["rows"].(float64); !ok || int(got) != 10 {
		t.Errorf("fixture rows = %v, want 10", fix["rows"])
	}
	if got, ok := fix["cols"].(float64); !ok || int(got) != 40 {
		t.Errorf("fixture cols = %v, want 40", fix["cols"])
	}
}

// fixture without -name is a usage error, not a file called ".json".
func TestCmdFixture_RequiresName(t *testing.T) {
	path := recording(t)
	outDir := t.TempDir()

	_, err := captureStdout(t, func() error {
		return cmdFixture([]string{path, "-out", outDir})
	})
	if err == nil {
		t.Fatal("cmdFixture without -name returned nil error")
	}
	entries, rerr := os.ReadDir(outDir)
	if rerr != nil {
		t.Fatalf("ReadDir: %v", rerr)
	}
	if len(entries) != 0 {
		t.Errorf("files written despite the error: %v", entries)
	}
}

// export produces asciicast v2: a header object followed by one event array
// per frame.
func TestCmdExport_AsciicastStructure(t *testing.T) {
	path := recording(t)
	castPath := filepath.Join(t.TempDir(), "out.cast")

	_, err := captureStdout(t, func() error {
		return cmdExport([]string{path, "-cast", castPath})
	})
	if err != nil {
		t.Fatalf("cmdExport: %v", err)
	}

	b, err := os.ReadFile(castPath)
	if err != nil {
		t.Fatalf("reading cast: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) < 2 {
		t.Fatalf("cast has %d lines, want a header plus events:\n%s", len(lines), b)
	}

	var hdr map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &hdr); err != nil {
		t.Fatalf("cast header is not JSON: %v", err)
	}
	if v, _ := hdr["version"].(float64); int(v) != 2 {
		t.Errorf("cast version = %v, want 2", hdr["version"])
	}
	if w, _ := hdr["width"].(float64); int(w) != 80 {
		t.Errorf("cast width = %v, want 80", hdr["width"])
	}

	// Output and input frames become events; the resize frame has no
	// asciicast equivalent and must be dropped.
	codes := make([]string, 0, len(lines)-1)
	for _, ln := range lines[1:] {
		var ev []any
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Fatalf("event line is not JSON: %v (%s)", err, ln)
		}
		if len(ev) != 3 {
			t.Fatalf("event has %d fields, want 3: %s", len(ev), ln)
		}
		code, _ := ev[1].(string)
		codes = append(codes, code)
	}
	if got := strings.Join(codes, ""); got != "oio" {
		t.Errorf("event codes = %q, want \"oio\" (resize dropped)", got)
	}
}

// export without -cast is an error and writes nothing.
func TestCmdExport_RequiresCastFlag(t *testing.T) {
	path := recording(t)

	_, err := captureStdout(t, func() error { return cmdExport([]string{path}) })
	if err == nil {
		t.Error("cmdExport without -cast returned nil error")
	}
}

// Invalid UTF-8 cannot survive asciicast's JSON strings. The command must
// still produce a file rather than failing outright — the loss is reported,
// not fatal.
func TestCmdExport_InvalidUTF8StillExports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary.gtr")
	r, err := recfmt.NewRecorder(path, 24, 80, nil, false)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	r.Output([]byte{0xff, 0xfe, 0x80}) // never valid UTF-8
	if err := r.Close(); err != nil {
		t.Fatalf("Recorder.Close: %v", err)
	}

	castPath := filepath.Join(t.TempDir(), "out.cast")
	if _, err := captureStdout(t, func() error {
		return cmdExport([]string{path, "-cast", castPath})
	}); err != nil {
		t.Fatalf("cmdExport: %v", err)
	}

	b, rerr := os.ReadFile(castPath)
	if rerr != nil {
		t.Fatalf("reading cast: %v", rerr)
	}
	if len(strings.Split(strings.TrimSpace(string(b)), "\n")) < 2 {
		t.Errorf("no event written for the invalid-UTF-8 frame:\n%s", b)
	}
}

// Every command rejects a missing path, a nonexistent file, and a file that
// is not a recording — with an error, never a panic.
func TestCommands_BadInput(t *testing.T) {
	notARecording := filepath.Join(t.TempDir(), "garbage.gtr")
	if err := os.WriteFile(notARecording, []byte("this is not a gtr file"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "absent.gtr")

	cmds := map[string]func([]string) error{
		"info":    cmdInfo,
		"cat":     cmdCat,
		"play":    cmdPlay,
		"fixture": cmdFixture,
		"export":  cmdExport,
	}
	inputs := map[string][]string{
		"no arguments":    {},
		"missing file":    {missing},
		"not a recording": {notARecording},
	}

	for name, fn := range cmds {
		for label, args := range inputs {
			t.Run(name+"/"+label, func(t *testing.T) {
				_, err := captureStdout(t, func() error { return fn(args) })
				if err == nil {
					t.Errorf("%s with %s returned nil error", name, label)
				}
			})
		}
	}
}

// A recording truncated mid-frame — the shape a crash leaves behind — still
// yields the frames that survived rather than an error.
func TestCommands_TruncatedRecording(t *testing.T) {
	full := recording(t)
	b, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Chop the last few bytes so the final frame is incomplete.
	truncated := filepath.Join(t.TempDir(), "truncated.gtr")
	if err := os.WriteFile(truncated, b[:len(b)-3], 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := captureStdout(t, func() error { return cmdCat([]string{truncated}) })
	if err != nil {
		t.Fatalf("cat on a truncated recording: %v", err)
	}
	if !strings.HasPrefix(wantOutputBytes, out) {
		t.Errorf("cat = %q, want a prefix of %q", out, wantOutputBytes)
	}

	if _, err := captureStdout(t, func() error { return cmdInfo([]string{truncated}) }); err != nil {
		t.Errorf("info on a truncated recording: %v", err)
	}
}
