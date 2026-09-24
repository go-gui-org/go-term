//go:build windows

package term

import "syscall"

// Windows error codes that CreateHardLink returns when the volume cannot make
// hard links at all. ERROR_NOT_SUPPORTED (50) is not listed: Go's Errno.Is
// already maps it to errors.ErrUnsupported, which linkUnsupported checks. EPERM
// and EXDEV do not help here: on Windows those syscall constants are values Go
// invented, and no system call returns them.
//
// ERROR_INVALID_FUNCTION (1) and ERROR_INVALID_PARAMETER (87) are not listed
// either. Some link-less volumes do return them, but so do many unrelated
// failures on volumes that support links, and a wrong fallback there opens the
// rename path's overwrite window. Such a download fails with the error instead.
const errorNotSameDevice = syscall.Errno(17) // ERROR_NOT_SAME_DEVICE: link across volumes

// platformLinkUnsupported lists the errors, beyond the portable ones, that mean
// this filesystem has no hard links.
var platformLinkUnsupported = []error{errorNotSameDevice}
