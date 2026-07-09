//go:build windows

package term

import "github.com/go-gui-org/go-gui/gui"

// modPrimary is the base application-shortcut modifier. On Windows the Super
// (Windows) key is reserved by the OS, so Ctrl+Shift stands in for macOS's
// Command. See remapMod for the full four-layer mapping.
const modPrimary = gui.ModCtrlShift

// remapMod translates a macOS-style Super-based modifier combo to its Windows
// equivalent. Super is remapped to Ctrl+Shift; combos that layer another
// modifier on Super shift to a distinct pair so no two bindings collide
// (verified across the go-term/workspace binding set):
//
//	Super            -> Ctrl+Shift
//	Super+Shift      -> Ctrl+Alt
//	Super+Ctrl       -> Ctrl+Alt+Shift
//	Super+Alt        -> Alt+Shift
//
// Combos without Super pass through unchanged.
func remapMod(m gui.Modifier) gui.Modifier {
	switch m {
	case gui.ModSuper:
		return gui.ModCtrlShift
	case gui.ModSuper | gui.ModShift:
		return gui.ModCtrlAlt
	case gui.ModSuper | gui.ModCtrl:
		return gui.ModCtrlAltShift
	case gui.ModSuper | gui.ModAlt:
		return gui.ModAltShift
	}
	return m
}
