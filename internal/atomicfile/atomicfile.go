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
	"strconv"
)

// maxTempTries bounds the search for a free staging name. Names carry a random
// 32-bit suffix, so a collision is rare; the bound only stops a loop on a broken
// filesystem.
const maxTempTries = 100

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
// On any error the staging file is removed and path is left unchanged.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	f, err := createTemp(path, perm)
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// createTemp opens a new, empty staging file in path's directory. The name
// starts with a dot so file managers hide it, and holds the target's base name
// so a leftover (after a crash) shows what it belonged to.
func createTemp(path string, perm fs.FileMode) (*os.File, error) {
	dir, base := filepath.Split(path)
	for range maxTempTries {
		name := filepath.Join(dir, "."+base+"-"+strconv.FormatUint(uint64(rand.Uint32()), 10)+".tmp")
		// O_EXCL makes the "is it free?" test and the claim one atomic step.
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, err
	}
	return nil, &fs.PathError{Op: "createtemp", Path: path, Err: fs.ErrExist}
}
