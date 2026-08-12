package workspace

// Session-recording control for the focused pane. The recording itself is
// entirely term-level (Term.StartRecording); this file only decides where
// the file goes and reports what happened.

import (
	"log"
	"os"
	"path/filepath"
	"time"
)

// recordingsDirName is the subdirectory of the go-term config directory used
// when Cfg.RecordDir is unset.
const recordingsDirName = "recordings"

// toggleRecording starts a session recording on the focused pane, or stops
// the one already running there. Bound to Cmd+Shift+R.
//
// Recording is per-pane, not per-workspace: the interesting session is the
// one the user is looking at, and a workspace-wide recording would interleave
// unrelated byte streams into a file that could not be replayed.
func (ws *Workspace) toggleRecording() {
	tm := ws.ActivePane()
	if tm == nil {
		return
	}
	if tm.Recording() {
		if err := tm.StopRecording(); err != nil {
			log.Printf("workspace: stop recording: %v", err)
			return
		}
		log.Printf("workspace: recording stopped")
		ws.refresh()
		return
	}
	path, err := ws.recordingPath()
	if err != nil {
		log.Printf("workspace: recording path: %v", err)
		return
	}
	if err := tm.StartRecording(path); err != nil {
		log.Printf("workspace: start recording: %v", err)
		return
	}
	log.Printf("workspace: recording to %s", path)
	ws.refresh()
}

// recordingPath builds the destination for a new recording:
// <RecordDir or config-dir/recordings>/<timestamp>.gtr.
//
// The timestamp is filename-safe rather than RFC 3339 proper — colons are
// legal on unix but awkward everywhere else, and a recording is a file a
// user will want to attach to a bug report from any platform.
func (ws *Workspace) recordingPath() (string, error) {
	dir := ws.cfg.RecordDir
	if dir == "" {
		base, err := configDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, recordingsDirName)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := time.Now().Format("2006-01-02T15-04-05") + ".gtr"
	return filepath.Join(dir, name), nil
}

// Teardown needs no special handling: Workspace.Close closes every pane
// through Term.Close, which finishes any recording still running there.
