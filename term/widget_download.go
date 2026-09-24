package term

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

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
	t.loopWg.Add(1)
	go t.downloadWorker()
}

// downloadWorker drains dlQueue one job at a time. Serializing the writes is
// what makes the collision suffixing in writeDownload race-free: no two jobs
// probe the same directory at once.
func (t *Term) downloadWorker() {
	defer t.loopWg.Done()
	for {
		select {
		case job := <-t.dlQueue:
			t.dlPending.Add(-int64(len(job.data)))
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
// the extension. The payload is written to a sibling temp file and renamed
// into place, so an interrupted transfer cannot leave a truncated file under
// the real name.
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

	dest, claim, err := claimDownloadName(cleanDir, base)
	if err != nil {
		return "", err
	}
	// The payload is staged in a sibling file, synced, and renamed over the
	// claimed placeholder. On failure the placeholder must not stay behind as
	// an empty file under the real name.
	if err := atomicfile.WriteFile(dest, data, downloadFileMode); err != nil {
		removeClaim(dest, claim)
		return "", err
	}
	return dest, nil
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

// claimDownloadName picks a free name under dir and claims it by creating an
// empty placeholder. It returns the destination and the placeholder's FileInfo,
// which removeClaim uses to recognize the placeholder later.
//
// The name is claimed with O_CREATE|O_EXCL so the "is it taken?" test and the
// claim are one atomic step — a plain os.Stat check would race another writer
// between the two.
func claimDownloadName(dir, base string) (string, os.FileInfo, error) {
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 0; i < maxDownloadCollisions; i++ {
		dest := filepath.Join(dir, base)
		if i > 0 {
			dest = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		}
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, downloadFileMode)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", nil, err
		}
		fi, err := f.Stat()
		_ = f.Close()
		if err != nil {
			_ = os.Remove(dest)
			return "", nil, err
		}
		return dest, fi, nil
	}
	return "", nil, fmt.Errorf("download %q: too many name collisions", base)
}
