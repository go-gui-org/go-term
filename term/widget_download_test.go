package term

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/go-gui-org/go-gui/gui"
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
	if entries[0].Name() != "x.bin" {
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
			<-tm.dlExited
		}
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

// A file another program saves under the download's name while the payload is
// being written must survive: the download takes the next free name instead.
// Before the stage-then-link order, the name was claimed first and the finished
// payload renamed over it, which silently destroyed such a file.
func TestWriteDownload_KeepsFileSavedMidWrite(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "report.pdf")
	orig := linkFile
	t.Cleanup(func() { linkFile = orig })
	first := true
	linkFile = func(oldname, newname string) error {
		if first {
			// The staged payload is on disk; another program saves now.
			first = false
			if err := os.WriteFile(dest, []byte("user data"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return orig(oldname, newname)
	}
	got, err := writeDownload(dir, "report.pdf", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "report (1).pdf"); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if b, err := os.ReadFile(dest); err != nil || string(b) != "user data" {
		t.Errorf("other program's file = %q, %v; want it kept", b, err)
	}
	assertDirEntries(t, dir, "report (1).pdf", "report.pdf")
}

// A filesystem without hard links (FAT, exFAT) still saves, through the
// placeholder-and-rename fallback, and leaves no staging file.
func TestWriteDownload_FallbackWithoutHardLinks(t *testing.T) {
	dir := t.TempDir()
	orig := linkFile
	t.Cleanup(func() { linkFile = orig })
	linkFile = func(oldname, newname string) error {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: syscall.EPERM}
	}
	got, err := writeDownload(dir, "x.bin", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(got); err != nil || string(b) != "payload" {
		t.Errorf("content = %q, %v; want \"payload\"", b, err)
	}
	assertDirEntries(t, dir, "x.bin")
}

// A download that fails after the payload is staged (every name taken) must
// leave neither the staging file nor a new placeholder behind.
func TestWriteDownload_FailureLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	want := make([]string, 0, maxDownloadCollisions)
	for i := range maxDownloadCollisions {
		p := downloadCandidate(dir, "x.bin", i)
		if err := os.WriteFile(p, []byte("taken"), 0o600); err != nil {
			t.Fatal(err)
		}
		want = append(want, filepath.Base(p))
	}
	if _, err := writeDownload(dir, "x.bin", []byte("payload")); err == nil {
		t.Fatal("writeDownload with every name taken: want error")
	}
	assertDirEntries(t, dir, want...)
}

// assertDirEntries fails unless dir holds exactly the named entries.
func assertDirEntries(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	have := make(map[string]bool, len(entries))
	for _, e := range entries {
		have[e.Name()] = true
	}
	ok := len(entries) == len(names)
	for _, n := range names {
		ok = ok && have[n]
	}
	if !ok {
		t.Errorf("dir entries = %v, want %v", keys(have), names)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The link-less fallback must not leave its claimed 0-byte placeholder under the
// real name when the rename fails. That failure cannot be forced portably, so
// removeClaim, the cleanup that path runs, is tested directly.
func TestRemoveClaim_RemovesOwnPlaceholder(t *testing.T) {
	dir := t.TempDir()
	dest, claim, err := claimDownloadName(dir, "a.bin", 0)
	if err != nil {
		t.Fatal(err)
	}
	removeClaim(dest, claim)
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Errorf("placeholder still present after removeClaim: %v", err)
	}
}

// The download dir is shared. If another program replaced the placeholder with
// a real file before the failure, removeClaim must leave that file alone.
func TestRemoveClaim_KeepsReplacedFile(t *testing.T) {
	dir := t.TempDir()
	dest, claim, err := claimDownloadName(dir, "report.pdf", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Replace by rename, as an editor or browser saving over the name would.
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("user data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(other, dest); err != nil {
		t.Fatal(err)
	}
	removeClaim(dest, claim)
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "user data" {
		t.Errorf("replaced file = %q, %v; want it kept", got, err)
	}
}

// Only "this filesystem has no hard links" errors may switch to the rename
// fallback. Any other link failure (a full disk, a name too long for its " (N)"
// suffix, a staging file a cleaner deleted) must fail the download: the rename
// path can overwrite a file another program saves between claim and rename,
// which is exactly what linking exists to prevent.
func TestWriteDownload_OtherLinkErrorsDoNotFallBack(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.ENOSPC, syscall.ENAMETOOLONG, syscall.ENOENT} {
		t.Run(errno.Error(), func(t *testing.T) {
			dir := t.TempDir()
			orig := linkFile
			t.Cleanup(func() { linkFile = orig })
			linkFile = func(oldname, newname string) error {
				return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: errno}
			}
			if got, err := writeDownload(dir, "x.bin", []byte("payload")); err == nil {
				t.Fatalf("writeDownload = %q, want the link error", got)
			}
			assertDirEntries(t, dir)
		})
	}
}

// When every candidate name is taken the download fails before the payload is
// staged. Staging writes and full-flushes up to the pending-bytes cap, so a
// sender repeating one name would otherwise cost a large synced write per
// transfer only to delete it.
func TestWriteDownload_AllNamesTakenSkipsStaging(t *testing.T) {
	dir := t.TempDir()
	for i := range maxDownloadCollisions {
		if err := os.WriteFile(downloadCandidate(dir, "x.bin", i), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	orig := stageDownload
	t.Cleanup(func() { stageDownload = orig })
	staged := 0
	stageDownload = func(dir, base string, data []byte, perm os.FileMode) (string, error) {
		staged++
		return orig(dir, base, data, perm)
	}
	if _, err := writeDownload(dir, "x.bin", []byte("payload")); err == nil {
		t.Fatal("writeDownload with every name taken: want error")
	}
	if staged != 0 {
		t.Errorf("payload staged %d times, want 0", staged)
	}
}

// The pre-check already found the first free name; publishing must start there
// rather than probe every taken name a second time with a link attempt.
func TestWriteDownload_PublishStartsAtFirstFreeName(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		if err := os.WriteFile(downloadCandidate(dir, "x.bin", i), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	orig := linkFile
	t.Cleanup(func() { linkFile = orig })
	var tried []string
	linkFile = func(oldname, newname string) error {
		tried = append(tried, filepath.Base(newname))
		return orig(oldname, newname)
	}
	got, err := writeDownload(dir, "x.bin", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if want := downloadCandidate(dir, "x.bin", 3); got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}
	if len(tried) != 1 {
		t.Errorf("link attempts = %v, want one, on the first free name", tried)
	}
}

// blockStaging makes the next staged write wait until the returned release is
// called. entered is closed once the worker reaches the write.
func blockStaging(t *testing.T) (entered <-chan struct{}, release func()) {
	t.Helper()
	in := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	orig := stageDownload
	t.Cleanup(func() { stageDownload = orig })
	stageDownload = func(dir, base string, data []byte, perm os.FileMode) (string, error) {
		close(in)
		<-gate
		return orig(dir, base, data, perm)
	}
	return in, func() { once.Do(func() { close(gate) }) }
}

// newClosableDownloadTerm builds a real Term (so Close runs its full path)
// with the built-in writer saving to dir.
func newClosableDownloadTerm(t *testing.T, dir string, onNotify func(string, string)) *Term {
	t.Helper()
	tm, err := New(gui.NewWindow(gui.WindowCfg{}), Cfg{DownloadDir: dir, OnNotify: onNotify})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tm
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal(what)
	}
}

// An embedder usually exits the process right after Close. A transfer being
// written at that moment must be published before Close returns; otherwise the
// exit kills the write and leaves the hidden staging file in the download dir.
func TestClose_WaitsForDownloadWrite(t *testing.T) {
	entered, release := blockStaging(t)
	dir := t.TempDir()
	tm := newClosableDownloadTerm(t, dir, func(string, string) {})
	t.Cleanup(func() { release(); <-tm.dlExited })

	feedTermDownload(t, tm, "x.bin", []byte("payload"))
	waitClosed(t, entered, "download never reached the staging write")
	// Release the write a moment after Close starts, well inside the wait.
	time.AfterFunc(50*time.Millisecond, release)
	_ = tm.Close()

	b, err := os.ReadFile(filepath.Join(dir, "x.bin"))
	if err != nil || string(b) != "payload" {
		t.Fatalf("after Close: content = %q, %v; want the published payload", b, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("download dir = %v, want only x.bin (no staging file left)", entries)
	}
}

// Close runs on the main thread, so its wait for a slow write is bounded. When
// the wait runs out, the write still finishes in the background, but no
// embedder callback may run after Close has returned.
func TestClose_BoundedWaitThenNoCallbacks(t *testing.T) {
	origWait := downloadCloseWait
	t.Cleanup(func() { downloadCloseWait = origWait })
	downloadCloseWait = 50 * time.Millisecond

	entered, release := blockStaging(t)
	dir := t.TempDir()
	var notified atomic.Int32
	tm := newClosableDownloadTerm(t, dir, func(string, string) { notified.Add(1) })
	t.Cleanup(func() { release(); <-tm.dlExited })

	feedTermDownload(t, tm, "x.bin", []byte("payload"))
	waitClosed(t, entered, "download never reached the staging write")

	closed := make(chan struct{})
	go func() { _ = tm.Close(); close(closed) }()
	waitClosed(t, closed, "Close blocked past its bounded wait")

	release()
	waitClosed(t, tm.dlExited, "worker never exited")
	if n := notified.Load(); n != 0 {
		t.Errorf("OnNotify called %d times after Close returned, want 0", n)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "x.bin")); err != nil || string(b) != "payload" {
		t.Errorf("background write: content = %q, %v; want payload", b, err)
	}
}

// The gate is what keeps OnDownload and OnNotify from running after Close
// returns: once shut, no new callback starts, and a shut during a callback
// is not undone when the callback ends.
func TestWithDownloadGate_ShutBlocksCallbacks(t *testing.T) {
	var tm Term
	ran := tm.withDownloadGate(func() {})
	if !ran {
		t.Fatal("open gate refused the callback")
	}
	tm.dlGate.Store(dlGateShut)
	if tm.withDownloadGate(func() { t.Error("callback ran after the gate shut") }) {
		t.Error("shut gate reported the callback as run")
	}
	// Shutting the gate while a callback runs must keep it shut afterwards.
	tm.dlGate.Store(dlGateOpen)
	tm.withDownloadGate(func() { tm.dlGate.Store(dlGateShut) })
	if got := tm.dlGate.Load(); got != dlGateShut {
		t.Errorf("gate = %d after a shut during a callback, want shut", got)
	}
}
