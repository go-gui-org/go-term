package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile_WritesAndReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	for _, want := range []string{"one", "two"} {
		if err := WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("content = %q, want %q", got, want)
		}
	}
	assertOnlyEntry(t, filepath.Dir(path), "f.json")
}

// A failed rename (the target is a non-empty directory) must return the error and
// leave no staging file in the directory.
func TestWriteFile_FailureLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "busy")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(target, []byte("x"), 0o644); err == nil {
		t.Fatal("WriteFile over a non-empty directory: want error")
	}
	assertOnlyEntry(t, dir, "busy")
}

func assertOnlyEntry(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir entries = %v, want only %q", names, name)
	}
}

// A target name near the 255-byte component limit must still save. The staging
// name adds about 16 bytes, so before the hint was cut short the staging open
// failed with ENAMETOOLONG even though the target name itself was legal.
func TestWriteFile_LongNameFits(t *testing.T) {
	dir := t.TempDir()
	name := strings.Repeat("a", 250) + ".bin"
	path := filepath.Join(dir, name)
	if err := WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile with a %d-byte name: %v", len(name), err)
	}
	assertOnlyEntry(t, dir, name)
}

// Stage leaves one synced file that holds the data; the caller owns placing it.
func TestStage_WritesHiddenFile(t *testing.T) {
	dir := t.TempDir()
	tmp, err := Stage(dir, "x.bin", []byte("data"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(tmp) != dir || !strings.HasPrefix(filepath.Base(tmp), ".x.bin-") {
		t.Errorf("staging path = %q, want a hidden .x.bin-* file in %q", tmp, dir)
	}
	got, err := os.ReadFile(tmp)
	if err != nil || string(got) != "data" {
		t.Errorf("staged content = %q, %v; want \"data\"", got, err)
	}
}

func TestTruncateName_KeepsWholeRunes(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abcdef", 3, "abc"},
		{"ab€", 4, "ab"}, // € is 3 bytes; cutting at 4 leaves 2 of them
		{"ab😀", 5, "ab"}, // 😀 is 4 bytes; cutting at 5 leaves 3 of them
		{"ab😀c", 6, "ab😀"},
	}
	for _, c := range cases {
		if got := truncateName(c.in, c.n); got != c.want {
			t.Errorf("truncateName(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

// A Stage into a missing directory must fail without creating anything.
func TestStage_BadDirReturnsError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	if _, err := Stage(dir, "x.bin", []byte("data"), 0o600); err == nil {
		t.Fatal("Stage into a missing directory: want error")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Stat(%q) = %v, want it to stay missing", dir, err)
	}
}

// SyncDir is best effort; it must never fail the caller, even on empty or
// missing input.
func TestSyncDir_NeverPanics(t *testing.T) {
	dir := t.TempDir()
	SyncDir(dir)
	SyncDir("")
	SyncDir(filepath.Join(dir, "missing"))
}
