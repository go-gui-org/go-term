//go:build windows

package workspace

import "github.com/go-gui-org/go-gui/gui"

// remapMod translates the macOS-style Super-based default bindings to their
// Windows equivalents, where the Super (Windows) key is OS-reserved. The
// mapping mirrors term.remapMod and is collision-free across the workspace
// binding set:
//
//	Super            -> Ctrl+Shift
//	Super+Shift      -> Ctrl+Alt
//	Super+Ctrl       -> Ctrl+Alt+Shift
//	Super+Alt        -> Alt+Shift
//
// Combos without Super (e.g. bare Escape) pass through unchanged.
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
