//go:build windows

package term

import "syscall"

// Windows error codes that CreateHardLink returns when the volume cannot make
// hard links at all. None of them matches errors.ErrUnsupported, EPERM or
// EXDEV: on Windows those syscall constants are Go-invented values that no
// system call returns.
const (
	errorInvalidFunction  = syscall.Errno(1)  // ERROR_INVALID_FUNCTION: FAT32, exFAT
	errorNotSameDevice    = syscall.Errno(17) // ERROR_NOT_SAME_DEVICE: link across volumes
	errorNotSupported     = syscall.Errno(50) // ERROR_NOT_SUPPORTED: some SMB shares
	errorInvalidParameter = syscall.Errno(87) // ERROR_INVALID_PARAMETER: some SMB shares
)

// platformLinkUnsupported lists the errors, beyond the portable ones, that mean
// this filesystem has no hard links. Without them every download to a FAT32
// or exFAT drive failed, where the rename fallback would have worked.
var platformLinkUnsupported = []error{
	errorInvalidFunction,
	errorNotSameDevice,
	errorNotSupported,
	errorInvalidParameter,
}
