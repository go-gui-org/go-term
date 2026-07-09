//go:build !windows

package term

import "github.com/go-gui-org/go-gui/gui"

// modPrimary is the base application-shortcut modifier: Super (Command on
// macOS, the Super key on Linux).
const modPrimary = gui.ModSuper

// remapMod is the identity on non-Windows platforms; Super-based combos are
// used as-is. See the Windows build for the remapping rationale.
func remapMod(m gui.Modifier) gui.Modifier { return m }
