package term

// Term-level session recording control. The container format itself lives in
// record.go; this file is only the widget's start/stop plumbing.

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/go-gui-org/go-term/internal/recfmt"
)

// errAlreadyRecording is returned by StartRecording when a recording is
// already running on this Term. Stopping first is the caller's decision —
// silently rolling one recording into another would truncate a file the
// user believes they are still filling.
var errAlreadyRecording = errors.New("term: already recording")

// StartRecording begins a session recording at path, overwriting any
// existing file. The recording captures pty output with timing, plus grid
// resizes, and can be replayed with NewReplay or the gotermrec tool.
//
// Keystrokes are recorded only when Cfg.RecordInput is set. Returns an error
// if a recording is already running or the file cannot be created. Call from
// the GUI main thread.
func (t *Term) StartRecording(path string) error {
	if t.closed.Load() {
		return errors.New("term: closed")
	}
	if t.rec.Load() != nil {
		return errAlreadyRecording
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	// Geometry goes in the header, so a recording is self-describing even
	// when it starts mid-session.
	rows, cols := t.Rows(), t.Cols()
	r, err := recfmt.NewRecorder(path, rows, cols, recordEnv(), t.cfg.RecordInput)
	if err != nil {
		return err
	}
	// Swap rather than Store: losing a race with a second StartRecording
	// would otherwise leak the displaced recorder's open file.
	if old := t.rec.Swap(r); old != nil {
		_ = old.Close()
	}
	return nil
}

// StopRecording finishes and closes the current recording. It is a no-op
// when nothing is being recorded. Call from the GUI main thread.
func (t *Term) StopRecording() error {
	return t.rec.Swap(nil).Close()
}

// Recording reports whether a session recording is currently running.
// Safe to call from any goroutine.
func (t *Term) Recording() bool { return t.rec.Load() != nil }

// recordingElapsed reports how long the current recording has been running,
// for the on-screen indicator. Zero when not recording.
func (t *Term) recordingElapsed() time.Duration { return t.rec.Load().Elapsed() }

// recordEnv collects the environment context stored in a recording header.
// Purely informational — replay never applies it — but it is the first thing
// worth knowing when a bug report arrives.
func recordEnv() map[string]string {
	env := make(map[string]string, 2)
	if s := os.Getenv("SHELL"); s != "" {
		env["SHELL"] = s
	}
	if s := os.Getenv("TERM"); s != "" {
		env["TERM"] = s
	}
	return env
}
