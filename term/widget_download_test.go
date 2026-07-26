package term

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWriteDownload_Basic(t *testing.T) {
	dir := t.TempDir()
	got, err := writeDownload(dir, "notes.txt", []byte("hello"))
	if err != nil {
		t.Fatalf("writeDownload: %v", err)
	}
	if want := filepath.Join(dir, "notes.txt"); got != want {
		t.Errorf("path = %q; want %q", got, want)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("contents = %q; want hello", data)
	}
}

func TestWriteDownload_CreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "downloads")
	if _, err := writeDownload(dir, "a.bin", []byte("x")); err != nil {
		t.Fatalf("writeDownload: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.bin")); err != nil {
		t.Fatalf("stat: %v", err)
	}
}

func TestWriteDownload_CollisionSuffix(t *testing.T) {
	dir := t.TempDir()
	// Three transfers of the same name must produce three distinct files;
	// an existing download is never overwritten.
	for i, want := range []string{"a.txt", "a (1).txt", "a (2).txt"} {
		got, err := writeDownload(dir, "a.txt", []byte{byte('0' + i)})
		if err != nil {
			t.Fatalf("writeDownload %d: %v", i, err)
		}
		if filepath.Base(got) != want {
			t.Errorf("path %d = %q; want base %q", i, got, want)
		}
	}
	// The first file still holds its original contents.
	data, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "0" {
		t.Errorf("a.txt = %q; want \"0\" (overwritten)", data)
	}
}

func TestWriteDownload_CollisionSuffix_NoExtension(t *testing.T) {
	dir := t.TempDir()
	for _, want := range []string{"README", "README (1)"} {
		got, err := writeDownload(dir, "README", []byte("x"))
		if err != nil {
			t.Fatalf("writeDownload: %v", err)
		}
		if filepath.Base(got) != want {
			t.Errorf("base = %q; want %q", filepath.Base(got), want)
		}
	}
}

func TestWriteDownload_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "downloads")
	// The parser sanitizes these away, but writeDownload is independently
	// reachable and must refuse to escape its directory on its own.
	for _, name := range []string{
		"../escape.txt",
		"../../escape.txt",
		"sub/escape.txt",
		"/etc/passwd",
	} {
		got, err := writeDownload(dir, name, []byte("x"))
		if err == nil {
			// filepath.Base collapses most of these to a bare name, which is
			// a safe outcome too — what matters is nothing lands outside dir.
			if filepath.Dir(got) != filepath.Clean(dir) {
				t.Errorf("%q escaped to %q", name, got)
			}
			continue
		}
	}
	// Nothing may exist above the download directory.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "downloads" {
			t.Errorf("stray entry above download dir: %q", e.Name())
		}
	}
}

func TestWriteDownload_Perms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	dir := t.TempDir()
	got, err := writeDownload(dir, "secret.bin", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != downloadFileMode {
		t.Errorf("perm = %o; want %o", perm, downloadFileMode)
	}
}

func TestWriteDownload_LeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := writeDownload(dir, "x.bin", []byte("payload")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d; want 1", len(entries))
	}
	if strings.HasPrefix(entries[0].Name(), ".goterm-dl-") {
		t.Errorf("staging file left behind: %q", entries[0].Name())
	}
}

func TestWriteDownload_EmptyDirIsError(t *testing.T) {
	if _, err := writeDownload("", "a.txt", []byte("x")); err == nil {
		t.Fatal("expected an error with no download directory")
	}
}

func TestWriteDownload_EmptyPayload(t *testing.T) {
	dir := t.TempDir()
	got, err := writeDownload(dir, "empty.bin", []byte{})
	if err != nil {
		t.Fatalf("writeDownload: %v", err)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("len = %d; want 0", len(data))
	}
}

// newDownloadTerm builds a bare Term with just enough wiring to exercise the
// OSC 1337 download path: grid, parser, and the worker goroutine. OnNotify is
// always stubbed so tests never fire a real desktop notification.
func newDownloadTerm(t *testing.T, cfg Cfg) *Term {
	t.Helper()
	if cfg.OnNotify == nil {
		cfg.OnNotify = func(string, string) {}
	}
	tm := &Term{cfg: cfg, grid: newGrid(10, 80)}
	tm.parser = newParser(tm.grid)
	tm.registerDownloadHandler()
	t.Cleanup(func() {
		if tm.dlDone != nil {
			close(tm.dlDone)
		}
		tm.loopWg.Wait()
	})
	return tm
}

// feedTermDownload pushes a non-inline File= transfer through the parser the
// way the reader goroutine does — under grid.Mu.
func feedTermDownload(t *testing.T, tm *Term, name string, payload []byte) {
	t.Helper()
	seq := "\x1b]1337;File=name=" +
		base64.StdEncoding.EncodeToString([]byte(name)) +
		";inline=0:" + base64.StdEncoding.EncodeToString(payload) + "\x07"
	tm.grid.Mu.Lock()
	tm.parser.Feed([]byte(seq))
	tm.grid.Mu.Unlock()
}

func TestTermDownload_WritesToDownloadDir(t *testing.T) {
	dir := t.TempDir()
	notified := make(chan string, 1)
	tm := newDownloadTerm(t, Cfg{
		DownloadDir: dir,
		OnNotify:    func(_, body string) { notified <- body },
	})
	feedTermDownload(t, tm, "report.txt", []byte("payload"))

	select {
	case path := <-notified:
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(data) != "payload" {
			t.Errorf("contents = %q; want payload", data)
		}
		if filepath.Base(path) != "report.txt" {
			t.Errorf("base = %q; want report.txt", filepath.Base(path))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the download to land")
	}
}

func TestTermDownload_OnDownloadWins(t *testing.T) {
	dir := t.TempDir()
	got := make(chan string, 1)
	tm := newDownloadTerm(t, Cfg{
		DownloadDir: dir,
		OnDownload:  func(name string, _ []byte) { got <- name },
	})
	feedTermDownload(t, tm, "a.bin", []byte("x"))

	select {
	case name := <-got:
		if name != "a.bin" {
			t.Errorf("name = %q; want a.bin", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for OnDownload")
	}
	// The host took the bytes, so the built-in writer must not have run.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("DownloadDir has %d entries; want 0 when OnDownload is set", len(entries))
	}
}

func TestTermDownload_OnDownloadOnly(t *testing.T) {
	got := make(chan string, 1)
	tm := newDownloadTerm(t, Cfg{
		OnDownload: func(name string, _ []byte) { got <- name },
	})
	feedTermDownload(t, tm, "data.bin", []byte("x"))
	select {
	case name := <-got:
		if name != "data.bin" {
			t.Errorf("name = %q; want data.bin", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for OnDownload")
	}
}

func TestTermDownload_DisabledByDefault(t *testing.T) {
	tm := newDownloadTerm(t, Cfg{})
	if tm.dlQueue != nil {
		t.Error("dlQueue allocated with neither OnDownload nor DownloadDir set")
	}
	// Feeding a transfer must be a silent no-op, not a panic on a nil channel.
	feedTermDownload(t, tm, "a.bin", []byte("x"))
}

func TestTermDownload_TraversalNameStaysInDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dl")
	notified := make(chan string, 1)
	tm := newDownloadTerm(t, Cfg{
		DownloadDir: dir,
		OnNotify:    func(_, body string) { notified <- body },
	})
	feedTermDownload(t, tm, "../../../../tmp/evil.sh", []byte("#!/bin/sh"))

	select {
	case path := <-notified:
		if filepath.Dir(path) != dir {
			t.Fatalf("landed at %q; want inside %q", path, dir)
		}
		if filepath.Base(path) != "evil.sh" {
			t.Errorf("base = %q; want evil.sh", filepath.Base(path))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the download to land")
	}
}

func TestTermDownload_PendingBytesCapDropsOverflow(t *testing.T) {
	// A payload that alone exceeds the pending-bytes budget is refused
	// outright, and the accounting is restored so later transfers still work.
	release := make(chan struct{})
	seen := make(chan string, 4)
	tm := newDownloadTerm(t, Cfg{
		OnDownload: func(name string, _ []byte) {
			seen <- name
			<-release
		},
	})
	// Park the worker on the first job so the queue accounting is observable.
	feedTermDownload(t, tm, "first.bin", []byte("x"))
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never picked up the first job")
	}

	tm.dlPending.Store(maxPendingDownloadBytes)
	feedTermDownload(t, tm, "dropped.bin", []byte("y"))
	if got := tm.dlPending.Load(); got != maxPendingDownloadBytes {
		t.Errorf("dlPending = %d; want %d (reservation not released on drop)",
			got, maxPendingDownloadBytes)
	}
	tm.dlPending.Store(0)

	close(release)
	select {
	case name := <-seen:
		t.Fatalf("over-cap transfer %q was delivered", name)
	case <-time.After(100 * time.Millisecond):
	}
}
