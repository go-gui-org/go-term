//go:build windows

package term

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

// stubVolumeHardLinks makes the volume query report has (or fail with err) for
// the rest of the test.
func stubVolumeHardLinks(t *testing.T, has bool, err error) {
	t.Helper()
	orig := volumeHasHardLinks
	t.Cleanup(func() { volumeHasHardLinks = orig })
	volumeHasHardLinks = func(string) (bool, error) { return has, err }
}

// failLinks makes every hard link fail with errno for the rest of the test.
func failLinks(t *testing.T, errno syscall.Errno) {
	t.Helper()
	orig := linkFile
	t.Cleanup(func() { linkFile = orig })
	linkFile = func(oldname, newname string) error {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: errno}
	}
}

// A link across volumes and ERROR_NOT_SUPPORTED (through Go's mapping to
// errors.ErrUnsupported) mean no hard links here; both take the rename fallback
// without asking the volume.
func TestWriteDownload_FallbackOnWindowsLinkErrors(t *testing.T) {
	for _, errno := range []syscall.Errno{errorNotSameDevice, syscall.Errno(50)} {
		t.Run(errno.Error(), func(t *testing.T) {
			dir := t.TempDir()
			failLinks(t, errno)
			stubVolumeHardLinks(t, true, nil)
			got, err := writeDownload(dir, "x.bin", []byte("payload"))
			if err != nil {
				t.Fatalf("writeDownload: %v", err)
			}
			if b, err := os.ReadFile(got); err != nil || string(b) != "payload" {
				t.Errorf("content = %q, %v; want \"payload\"", b, err)
			}
		})
	}
}

// FAT32 and exFAT answer CreateHardLink with ERROR_INVALID_FUNCTION, and some
// SMB shares with ERROR_INVALID_PARAMETER. Those codes are generic, so they
// take the rename fallback only when the volume itself reports no hard links.
// Regression: #239 dropped both codes, and every download to a FAT32 drive
// failed.
func TestWriteDownload_GenericErrorOnLinklessVolumeFallsBack(t *testing.T) {
	for _, errno := range []syscall.Errno{errorInvalidFunction, errorInvalidParameter} {
		t.Run(errno.Error(), func(t *testing.T) {
			dir := t.TempDir()
			failLinks(t, errno)
			stubVolumeHardLinks(t, false, nil)
			got, err := writeDownload(dir, "x.bin", []byte("payload"))
			if err != nil {
				t.Fatalf("writeDownload: %v", err)
			}
			if b, err := os.ReadFile(got); err != nil || string(b) != "payload" {
				t.Errorf("content = %q, %v; want \"payload\"", b, err)
			}
		})
	}
}

// On a volume that has hard links, the same generic codes are some other
// failure. Falling back there would open the rename path's overwrite window
// for nothing, so the download fails. A volume that cannot be asked counts
// the same way: the safe answer is the error, not the weaker publish.
func TestLinkUnsupported_GenericErrorOnLinkVolumeFails(t *testing.T) {
	cases := []struct {
		name string
		has  bool
		err  error
	}{
		{"volume has links", true, nil},
		{"volume query fails", false, errors.New("query failed")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubVolumeHardLinks(t, c.has, c.err)
			for _, errno := range []syscall.Errno{errorInvalidFunction, errorInvalidParameter} {
				err := &os.LinkError{Op: "link", Old: "a", New: "b", Err: errno}
				if linkUnsupported(t.TempDir(), err) {
					t.Errorf("linkUnsupported(errno %d) = true, want false", errno)
				}
			}
		})
	}
}
