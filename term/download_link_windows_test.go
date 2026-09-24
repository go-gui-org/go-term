//go:build windows

package term

import (
	"os"
	"syscall"
	"testing"
)

// A link across volumes and ERROR_NOT_SUPPORTED (through Go's mapping to
// errors.ErrUnsupported) mean no hard links here; both take the rename fallback.
func TestWriteDownload_FallbackOnWindowsLinkErrors(t *testing.T) {
	for _, errno := range []syscall.Errno{errorNotSameDevice, syscall.Errno(50)} {
		t.Run(errno.Error(), func(t *testing.T) {
			dir := t.TempDir()
			orig := linkFile
			t.Cleanup(func() { linkFile = orig })
			linkFile = func(oldname, newname string) error {
				return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: errno}
			}
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

// ERROR_INVALID_FUNCTION and ERROR_INVALID_PARAMETER are too generic to mean
// "no hard links". Falling back on them would risk the rename path's overwrite
// window on a volume where links work, so the download fails instead.
func TestLinkUnsupported_GenericWindowsErrorsDoNotFallBack(t *testing.T) {
	for _, errno := range []syscall.Errno{1, 87} {
		err := &os.LinkError{Op: "link", Old: "a", New: "b", Err: errno}
		if linkUnsupported(err) {
			t.Errorf("linkUnsupported(errno %d) = true, want false", errno)
		}
	}
}
