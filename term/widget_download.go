package term

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/go-gui-org/go-term/internal/atomicfile"
)

// downloadQueueDepth caps the number of transfers waiting on the worker.
// Small on purpose: the reader goroutine drops rather than blocks, and a
// terminal that is many files behind has already lost the race.
const downloadQueueDepth = 4

// maxPendingDownloadBytes caps the total payload sitting in dlQueue. Without
// it, a full queue of maximum-size transfers would pin close to 100 MiB.
const maxPendingDownloadBytes = 64 << 20

// downloadFileMode is deliberately owner-only: the payload arrived from
// whatever was writing to the pty and has no established provenance.
const downloadFileMode = 0o600

// downloadDirMode matches downloadFileMode's intent for a directory go-term
// creates itself. An existing directory keeps its own permissions.
const downloadDirMode = 0o700

// maxDownloadCollisions bounds the " (N)" suffix probe. A directory already
// holding 100 same-named downloads is a runaway, not a user.
const maxDownloadCollisions = 100

// downloadJob is one queued OSC 1337 File= transfer.
type downloadJob struct {
	name string
	data []byte
}

// registerDownloadHandler wires the OSC 1337 File= transfer path when the
// host opted in via Cfg.OnDownload or Cfg.DownloadDir. Leaving both unset
// leaves the parser handler nil, so transfers drop in the parser without
// allocating a decode buffer.
//
// The parser handler runs on the reader goroutine with grid.Mu held, so it
// only enqueues; downloadWorker does the blocking work.
func (t *Term) registerDownloadHandler() {
	if t.cfg.OnDownload == nil && t.cfg.DownloadDir == "" {
		return
	}
	t.dlQueue = make(chan downloadJob, downloadQueueDepth)
	t.dlDone = make(chan struct{})
	t.parser.SetDownloadHandler(func(name string, data []byte) {
		// Reserve the memory before queueing so the accounting can never
		// go negative or lag behind the channel.
		n := int64(len(data))
		if t.dlPending.Add(n) > maxPendingDownloadBytes {
			t.dlPending.Add(-n)
			return
		}
		select {
		case t.dlQueue <- downloadJob{name: name, data: data}:
		default:
			// Queue full: drop. Blocking here would stall the reader
			// goroutine while it holds grid.Mu, freezing the whole widget.
			t.dlPending.Add(-n)
		}
	})
	t.dlWg.Add(1)
	go t.downloadWorker()
}

// downloadWorker drains dlQueue one job at a time. Serializing the writes keeps
// two of this Term's own downloads from probing the same names at once. It is
// not what makes publishing race-free against other programs: the hard link in
// publishDownload is, since it fails instead of replacing a file.
//
// The worker can outlive Close: Close signals dlDone but does not wait, so a
// transfer being written when the pane closes finishes in the background
// instead of freezing the window during its fsync. That is safe because the
// worker reads only cfg (never changed after New), the atomic dlPending and
// the channels, and notify does not touch the window.
func (t *Term) downloadWorker() {
	defer t.dlWg.Done()
	for {
		select {
		case job := <-t.dlQueue:
			t.dlPending.Add(-int64(len(job.data)))
			// select picks at random among ready cases, so a closed dlDone
			// does not stop a queued job by itself. Check it first: jobs
			// still queued at Close are dropped, as Close documents.
			select {
			case <-t.dlDone:
				return
			default:
			}
			if fn := t.cfg.OnDownload; fn != nil {
				fn(job.name, job.data)
				continue
			}
			path, err := writeDownload(t.cfg.DownloadDir, job.name, job.data)
			if err != nil {
				log.Printf("term: download %q: %v", job.name, err)
				continue
			}
			t.notify("Download complete", path)
		case <-t.dlDone:
			return
		}
	}
}

// writeDownload saves one transfer under dir and returns the final path. The
// name is expected to be sanitized already (see sanitizeDownloadName) but is
// re-checked here: the parser and this writer are independently reachable, and
// a path escape is not the kind of bug to leave to a single layer.
//
// Existing files are never overwritten — collisions get a " (N)" suffix before
// the extension. The payload is staged in a hidden sibling file and synced
// first, then hard-linked to a free name. A link fails with EEXIST instead of
// replacing a file, so choosing the name and publishing the finished data are
// one atomic step: a truncated file never shows under the real name, and a file
// another program saves under that name meanwhile (~/Downloads is shared) is
// never destroyed.
func writeDownload(dir, name string, data []byte) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("no download directory")
	}
	base := filepath.Base(filepath.Clean(name))
	if base == "." || base == ".." || base == string(filepath.Separator) {
		base = defaultDownloadName
	}
	if err := os.MkdirAll(dir, downloadDirMode); err != nil {
		return "", err
	}
	// Containment check against the cleaned directory: whatever base is, the
	// join must stay one level under dir.
	cleanDir := filepath.Clean(dir)
	dest := filepath.Join(cleanDir, base)
	if filepath.Dir(dest) != cleanDir {
		return "", fmt.Errorf("unsafe download name %q", name)
	}

	// Fail cheaply when every candidate name is already taken. Staging writes
	// and full-flushes the whole payload, so a sender that repeats one name
	// would otherwise pay a large synced write per transfer only to delete it.
	// This is an early exit, not the guarantee: a name free now can be taken
	// before the link, and publishDownload handles that. The names before the
	// first free one were taken a moment ago, so publishing starts there
	// instead of probing them all again. A name freed in between is skipped,
	// which costs only a higher suffix.
	_, start, err := firstFree(cleanDir, base, 0, nameIsFree)
	if err != nil {
		return "", err
	}

	tmp, err := stageDownload(cleanDir, base, data, downloadFileMode)
	if err != nil {
		return "", err
	}
	// The staging file is always removed: after a successful link the data
	// lives on under dest, and after a failure nothing should stay behind.
	defer func() { _ = os.Remove(tmp) }()
	dest, err = publishDownload(cleanDir, base, tmp, start)
	if err != nil {
		return "", err
	}
	// Without this a power cut can undo the link, and the notification below
	// would announce a file that is gone after reboot. This runs on the
	// download worker, so the flush costs the GUI nothing.
	atomicfile.SyncDir(cleanDir)
	return dest, nil
}

// stageDownload is atomicfile.Stage, replaceable so tests can see whether a
// payload was written at all.
var stageDownload = atomicfile.Stage

// linkFile is os.Link, replaceable so tests can simulate a filesystem without
// hard links.
var linkFile = os.Link

// publishDownload gives the staged file tmp a free name under dir: base, or
// base with a " (N)" suffix, trying candidates from index start on. It returns
// the name used.
func publishDownload(dir, base, tmp string, start int) (string, error) {
	dest, _, err := firstFree(dir, base, start, func(dest string) error {
		return linkFile(tmp, dest)
	})
	if err != nil && linkUnsupported(err) {
		// This filesystem has no hard links (FAT, exFAT, some network
		// mounts); it would fail the same way for every name.
		return publishByRename(dir, base, tmp, start)
	}
	return dest, err
}

// linkUnsupported reports whether a link error means the filesystem cannot
// make hard links at all, as opposed to failing this one link. Only these
// errors justify the weaker rename fallback. Anything else (ENOSPC, EDQUOT,
// ENAMETOOLONG on a suffixed name, ENOENT on a staging file a cleaner removed)
// fails the download: falling back there would trade a clear error for the
// rename path's overwrite window on a filesystem that supports links fine.
//
// Linux vfat reports EPERM; ENOSYS, ENOTSUP and EOPNOTSUPP all match
// errors.ErrUnsupported; EXDEV means the staging file and the target sit on
// different mounts, which a link cannot cross. Windows reports the same cases
// with its own codes, listed in platformLinkUnsupported.
func linkUnsupported(err error) bool {
	if errors.Is(err, errors.ErrUnsupported) ||
		errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.EXDEV) {
		return true
	}
	for _, target := range platformLinkUnsupported {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// firstFree runs try on each candidate name for base in order — base, then
// "stem (N).ext" — starting at candidate index start, and returns the first
// name try succeeds on together with its index. try returns an error matching
// fs.ErrExist to mean "taken, try the next"; any other error stops the probe
// and is returned as-is. This is the one copy of the probing policy (bound,
// suffix format, collision error) that the pre-check, the link publish and the
// rename fallback's claim all share.
func firstFree(dir, base string, start int, try func(dest string) error) (string, int, error) {
	for i := max(start, 0); i < maxDownloadCollisions; i++ {
		dest := downloadCandidate(dir, base, i)
		err := try(dest)
		if err == nil {
			return dest, i, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", 0, err
		}
	}
	return "", 0, fmt.Errorf("download %q: too many name collisions", base)
}

// nameIsFree is a firstFree probe that only looks: nil when nothing is at dest,
// fs.ErrExist when something is. Lstat, so a dangling symlink counts as taken —
// a link or O_EXCL create onto it would fail too.
func nameIsFree(dest string) error {
	_, err := os.Lstat(dest)
	switch {
	case err == nil:
		return fs.ErrExist
	case errors.Is(err, fs.ErrNotExist):
		return nil
	default:
		return err
	}
}

// publishByRename is the fallback for filesystems without hard links. It claims
// a free name with an empty placeholder and renames tmp over it. Unlike a link,
// the rename replaces whatever is at dest, so a file another program saves over
// the placeholder in between is lost; this is the best a link-less filesystem
// allows.
func publishByRename(dir, base, tmp string, start int) (string, error) {
	dest, claim, err := claimDownloadName(dir, base, start)
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		removeClaim(dest, claim)
		return "", err
	}
	return dest, nil
}

// downloadCandidate is the i-th name tried for base: base itself, then
// "stem (i).ext".
func downloadCandidate(dir, base string, i int) string {
	if i == 0 {
		return filepath.Join(dir, base)
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
}

// removeClaim deletes the empty placeholder claimDownloadName created, but only
// when dest is still that same file. The download dir is shared (~/Downloads),
// so between the claim and a failed rename another program may have put a real
// file at dest; deleting by name alone would destroy it.
func removeClaim(dest string, claim os.FileInfo) {
	fi, err := os.Lstat(dest)
	if err != nil || !os.SameFile(fi, claim) || fi.Size() != 0 {
		return
	}
	_ = os.Remove(dest)
}

// claimDownloadName picks a free name under dir, from candidate index start on,
// and claims it by creating an
// empty placeholder. It returns the destination and the placeholder's FileInfo,
// which removeClaim uses to recognize the placeholder later.
//
// The name is claimed with O_CREATE|O_EXCL so the "is it taken?" test and the
// claim are one atomic step — a plain os.Stat check would race another writer
// between the two.
func claimDownloadName(dir, base string, start int) (string, os.FileInfo, error) {
	var claim os.FileInfo
	dest, _, err := firstFree(dir, base, start, func(dest string) error {
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, downloadFileMode)
		if err != nil {
			return err
		}
		fi, err := f.Stat()
		_ = f.Close()
		if err != nil {
			_ = os.Remove(dest)
			return err
		}
		claim = fi
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	return dest, claim, nil
}
