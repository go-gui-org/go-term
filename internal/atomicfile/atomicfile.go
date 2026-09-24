// Package atomicfile replaces a file's contents so that readers, and a crash, see
// either the old bytes or the new bytes, never a partial write.
//
// It is a leaf package: it imports nothing from the repo, so both term (downloads)
// and term/workspace (the saved layout) can share one copy of the sequence.
package atomicfile

import (
	"errors"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"unicode/utf8"
)

// maxTempTries bounds the search for a free staging name. Names carry a random
// 32-bit suffix, so a collision is rare; the bound only stops a loop on a broken
// filesystem.
const maxTempTries = 100

// maxNameBytes is the longest single path component the common filesystems
// accept (NAME_MAX on Linux and macOS, 255 UTF-16 units on NTFS). Counting bytes
// is the strict reading, so a name that fits here fits everywhere.
const maxNameBytes = 255

// tempOverhead is what a staging name adds around the target's base name:
// "." + base + "-" + up to 10 digits (a uint32) + ".tmp".
const tempOverhead = len(".") + len("-") + 10 + len(".tmp")

// WriteFile writes data to a staging file next to path, flushes it to disk, and
// renames it over path.
//
// The staging file is created with perm filtered by the process umask, the same
// as os.WriteFile. os.CreateTemp is not used: it always creates 0600, and a later
// chmod to perm would ignore the umask and widen the mode for a umask-077 user.
//
// Sync runs before the rename. Without it, some filesystems (XFS, btrfs, ext4
// with noauto_da_alloc) can commit the rename before the data after a power cut,
// which leaves an empty file under the real name.
//
// The directory is not synced after the rename. A power cut can then undo the
// rename, but that leaves the old contents, which is still "old or new". A sync
// would only make the new version durable, and it costs a second full flush
// (F_FULLFSYNC on macOS, about 4 ms each) on the caller's thread — the GUI main
// thread for a workspace save. A caller that needs the new name to be durable
// calls SyncDir.
//
// On any error the staging file is removed and path is left unchanged.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	dir, base := filepath.Split(path)
	tmp, err := Stage(dir, base, data, perm)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Stage writes data to a new hidden file in dir, flushes it to disk, and returns
// its path. The caller moves it into place (rename, or a hard link when an
// existing file must never be replaced) and removes it on failure.
//
// hint is the name the data is meant for. It goes into the staging name so a file
// left behind by a crash shows what it belonged to, and it is cut short when the
// full name would be too long for the filesystem.
func Stage(dir, hint string, data []byte, perm fs.FileMode) (tmp string, err error) {
	f, err := createTemp(dir, hint, perm)
	if err != nil {
		return "", err
	}
	// staged is the file to clean up. tmp is the named return and a later
	// `return "", err` clears it, so the deferred cleanup cannot use it.
	staged := f.Name()
	tmp = staged
	// One cleanup for every failure below, so a new error branch cannot forget
	// to remove the staging file.
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(staged)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	// Close is not in the deferred cleanup's success path, so its error is
	// checked here; on failure the deferred Close is a harmless second call.
	if err = f.Close(); err != nil {
		return "", err
	}
	return tmp, nil
}

// SyncDir flushes dir's entries to disk, so a rename or link made in it survives
// a power cut. It is best effort: the rename has already happened, so there is
// nothing useful for a caller to do with a failure. Windows cannot sync a
// directory handle, and NTFS journals the rename itself, so it is skipped there.
func SyncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if dir == "" {
		dir = "."
	}
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// createTemp opens a new, empty staging file in dir. The name starts with a dot
// so file managers hide it.
func createTemp(dir, hint string, perm fs.FileMode) (*os.File, error) {
	hint = truncateName(hint, maxNameBytes-tempOverhead)
	for range maxTempTries {
		name := filepath.Join(dir, "."+hint+"-"+strconv.FormatUint(uint64(rand.Uint32()), 10)+".tmp")
		// O_EXCL makes the "is it free?" test and the claim one atomic step.
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, err
	}
	return nil, &fs.PathError{Op: "createtemp", Path: filepath.Join(dir, hint), Err: fs.ErrExist}
}

// truncateName cuts s to at most n bytes without splitting a UTF-8 sequence. A
// staging name only has to be unique and readable, so losing the tail is fine.
func truncateName(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	// Drop a trailing partial rune. At most utf8.UTFMax-1 bytes can dangle.
	for i := 0; i < utf8.UTFMax && len(s) > 0; i++ {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}
