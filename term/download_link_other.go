//go:build !windows

package term

// platformLinkUnsupported reports whether err, from a link under dir, means the
// filesystem has no hard links, beyond the portable errors. Unix reports that
// through the portable errors already; see linkUnsupported.
func platformLinkUnsupported(string, error) bool { return false }
