package atomicfile

import (
	"os"
	"path/filepath"
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
