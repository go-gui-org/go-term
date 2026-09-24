//go:build windows

package term

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// Windows error codes that CreateHardLink returns when a link cannot be made.
// ERROR_NOT_SUPPORTED (50) is not listed: Go's Errno.Is already maps it to
// errors.ErrUnsupported, which linkUnsupported checks. EPERM and EXDEV do not
// help here: on Windows those syscall constants are values Go invented, and no
// system call returns them.
const (
	errorInvalidFunction  = syscall.Errno(1)  // ERROR_INVALID_FUNCTION: FAT32, exFAT
	errorNotSameDevice    = syscall.Errno(17) // ERROR_NOT_SAME_DEVICE: link across volumes
	errorInvalidParameter = syscall.Errno(87) // ERROR_INVALID_PARAMETER: some SMB shares
)

// platformLinkUnsupported reports whether err, from a link under dir, means the
// volume has no hard links. See the download contract in widget_download.go.
//
// ERROR_NOT_SAME_DEVICE always means that. ERROR_INVALID_FUNCTION and
// ERROR_INVALID_PARAMETER are what FAT32, exFAT and some SMB shares return, but
// many unrelated failures return them too. For those two the volume decides:
// the rename fallback is taken only when the volume reports no hard-link
// support. If the volume cannot be asked, the download fails with err, because
// a wrong fallback on a volume with links opens the rename overwrite window.
func platformLinkUnsupported(dir string, err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case errorNotSameDevice:
		return true
	case errorInvalidFunction, errorInvalidParameter:
		has, verr := volumeHasHardLinks(dir)
		return verr == nil && !has
	}
	return false
}

// volumeHasHardLinks reports whether the volume that holds dir supports hard
// links. A var so tests can stand in for a FAT32 volume.
var volumeHasHardLinks = func(dir string) (bool, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return false, err
	}
	// The volume root ("C:\", or a mount point folder). MAX_PATH+1 is enough:
	// the root is never longer than the path it came from, and a longer dir
	// fails the call with an error, which counts as "cannot ask".
	root := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(p, &root[0], uint32(len(root))); err != nil {
		return false, err
	}
	var flags uint32
	if err := windows.GetVolumeInformation(&root[0], nil, 0, nil, nil, &flags, nil, 0); err != nil {
		return false, err
	}
	return flags&windows.FILE_SUPPORTS_HARD_LINKS != 0, nil
}
