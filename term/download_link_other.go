//go:build !windows

package term

// platformLinkUnsupported lists the errors, beyond the portable ones, that mean
// this filesystem has no hard links. Unix reports that through the portable
// errors already; see linkUnsupported.
var platformLinkUnsupported []error
