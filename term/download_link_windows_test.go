//go:build windows

package term

import (
	"os"
	"syscall"
	"testing"
)

// FAT32, exFAT and some SMB shares answer CreateHardLink with these codes.
// Each must take the rename fallback; before, every download there failed.
func TestWriteDownload_FallbackOnWindowsLinkErrors(t *testing.T) {
	for _, errno := range []syscall.Errno{
		errorInvalidFunction, errorNotSameDevice, errorNotSupported, errorInvalidParameter,
	} {
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
