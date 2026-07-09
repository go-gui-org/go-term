//go:build !windows

package workspace

import "github.com/go-gui-org/go-gui/gui"

// remapMod is the identity on non-Windows platforms; the Super-based default
// bindings are used as-is. See the Windows build for the remapping rationale.
func remapMod(m gui.Modifier) gui.Modifier { return m }
