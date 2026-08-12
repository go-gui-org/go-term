package term

import "github.com/go-gui-org/go-gui/gui"

// actionFn runs one Term-level action directly, with no keyboard event to
// match. It reports whether the action ran: the copy-mode actions only run
// while copy mode owns the keyboard, and the alt-screen page keys still
// refuse to run there — their Shift-gated passthrough is the keyboard's
// escape hatch and has no direct-dispatch equivalent.
type actionFn func(t *Term, w *gui.Window) bool

// actionDispatch is the single place an Action meets its implementation.
// RunAction reaches through this table, so an action a user unbinds stays
// invocable from a command palette: the chord is only the keyboard's handle
// on the action, not the action itself.
//
// Each handler reproduces the conditional-passthrough rules of the keyboard
// path (onKeyDown → handleSearchKey/handleClipboardKey/scrollbackIntercept/
// handleDisplayKey) for the state it is dispatched from. The mode gates that
// decide *which* handlers are reachable live in runActionDirect, mirroring
// onKeyDown's dispatch order; what each handler does once reached lives
// here, shared with the keyboard path's operations.
var actionDispatch = map[Action]actionFn{
	ActionCopyMode: func(t *Term, w *gui.Window) bool {
		if t.copy.active {
			t.exitCopyMode(w)
		} else {
			t.enterCopyMode(w)
		}
		return true
	},
	ActionHints: func(t *Term, w *gui.Window) bool {
		t.toggleHints(hintOpen, w)
		return true
	},
	ActionHintsCopy: func(t *Term, w *gui.Window) bool {
		t.toggleHints(hintCopy, w)
		return true
	},
	ActionFind: func(t *Term, w *gui.Window) bool {
		t.openSearchBar(w)
		return true
	},
	ActionPrevPrompt: func(t *Term, w *gui.Window) bool {
		t.jumpToMark(true, w)
		return true
	},
	ActionNextPrompt: func(t *Term, w *gui.Window) bool {
		t.jumpToMark(false, w)
		return true
	},
	ActionJumpFailure: func(t *Term, w *gui.Window) bool {
		t.jumpToFailure(w)
		return true
	},
	ActionSelectOutput: func(t *Term, w *gui.Window) bool {
		t.selectCommandOutput(w)
		return true
	},
	ActionToggleRegex: func(t *Term, w *gui.Window) bool {
		if !t.search.active {
			return false
		}
		t.search.regex = !t.search.regex
		t.recompileSearchRE()
		t.bumpVersion()
		if w != nil {
			w.UpdateWindow()
		}
		return true
	},
	ActionNextMatch: func(t *Term, w *gui.Window) bool {
		if !t.search.active {
			return false
		}
		if t.copy.searching {
			t.finishCopySearch(true, w)
		} else {
			t.searchJump(true, w)
		}
		return true
	},
	ActionPrevMatch: func(t *Term, w *gui.Window) bool {
		if !t.search.active {
			return false
		}
		if t.copy.searching {
			t.copy.backward = !t.copy.backward
			t.finishCopySearch(true, w)
		} else {
			t.searchJump(false, w)
		}
		return true
	},
	ActionScrollPageUp: func(t *Term, w *gui.Window) bool {
		if t.isAltActive() {
			return false
		}
		t.scrollByPage(+1, w)
		return true
	},
	ActionScrollPageDown: func(t *Term, w *gui.Window) bool {
		if t.isAltActive() {
			return false
		}
		t.scrollByPage(-1, w)
		return true
	},
	ActionScrollTop: func(t *Term, w *gui.Window) bool {
		t.scrollToTop(w)
		return true
	},
	ActionScrollBottom: func(t *Term, w *gui.Window) bool {
		t.scrollToBottom(w)
		return true
	},
	ActionFontInc: func(t *Term, w *gui.Window) bool {
		t.AdjustFontSize(0.25)
		return true
	},
	ActionFontDec: func(t *Term, w *gui.Window) bool {
		t.AdjustFontSize(-0.25)
		return true
	},
	ActionFontReset: func(t *Term, w *gui.Window) bool {
		t.ResetFontSize()
		return true
	},
	ActionCopy: func(t *Term, w *gui.Window) bool {
		return t.copySelection(w)
	},
	ActionPaste: func(t *Term, w *gui.Window) bool {
		t.pasteFromClipboard(w)
		return true
	},
}

// copyModeOps maps the actions that only mean something inside copy mode to
// the operation each one performs. Membership in this map is what makes an
// action copy-mode-scoped: handleCopyModeKey iterates it in copyModeOrder,
// and runActionDirect consults it directly, so the keyboard path and the
// palette cannot apply different operations to the same action.
//
// The three Term-level actions with copy-mode behavior (ActionCopy yanks,
// ActionJumpFailure and ActionSelectOutput keep working) are included with
// their copy-mode meaning; runActionDirect resolves their state first.
var copyModeOps = map[Action]actionFn{
	ActionCopyModeExit: func(t *Term, w *gui.Window) bool {
		t.exitCopyMode(w)
		return true
	},
	ActionCopyModeLeft: func(t *Term, w *gui.Window) bool {
		t.moveCopyCols(-1, w)
		return true
	},
	ActionCopyModeDown: func(t *Term, w *gui.Window) bool {
		t.moveCopyRows(+1, w)
		return true
	},
	ActionCopyModeUp: func(t *Term, w *gui.Window) bool {
		t.moveCopyRows(-1, w)
		return true
	},
	ActionCopyModeRight: func(t *Term, w *gui.Window) bool {
		t.moveCopyCols(+1, w)
		return true
	},
	ActionCopyModeWordFwd: func(t *Term, w *gui.Window) bool {
		t.copyWordMotion(true, w)
		return true
	},
	ActionCopyModeWordBack: func(t *Term, w *gui.Window) bool {
		t.copyWordMotion(false, w)
		return true
	},
	ActionCopyModeLineStart: func(t *Term, w *gui.Window) bool {
		t.copy.cursor.Col = 0
		t.revealCursor(w)
		return true
	},
	ActionCopyModeLineEnd: func(t *Term, w *gui.Window) bool {
		t.copy.cursor.Col = t.copyLineEnd()
		t.revealCursor(w)
		return true
	},
	ActionCopyModeBottom: func(t *Term, w *gui.Window) bool {
		t.copy.cursor.Row = t.contentRows() - 1
		t.revealCursor(w)
		return true
	},
	ActionCopyModeTop: func(t *Term, w *gui.Window) bool {
		t.copy.cursor = contentPos{}
		t.revealCursor(w)
		return true
	},
	ActionCopyModeHalfPageUp: func(t *Term, w *gui.Window) bool {
		t.moveCopyRows(-t.copyHalfPageStep(), w)
		return true
	},
	ActionCopyModeHalfPageDown: func(t *Term, w *gui.Window) bool {
		t.moveCopyRows(+t.copyHalfPageStep(), w)
		return true
	},
	ActionCopyModePageUp: func(t *Term, w *gui.Window) bool {
		t.moveCopyRows(-t.copyPageStep(), w)
		return true
	},
	ActionCopyModePageDown: func(t *Term, w *gui.Window) bool {
		t.moveCopyRows(+t.copyPageStep(), w)
		return true
	},
	ActionCopyModeSelectLine: func(t *Term, w *gui.Window) bool {
		t.startCopySelection(copySelLine, w)
		return true
	},
	ActionCopyModeSelectChar: func(t *Term, w *gui.Window) bool {
		t.startCopySelection(copySelChar, w)
		return true
	},
	ActionCopyModeYank: func(t *Term, w *gui.Window) bool {
		t.yankCopySelection(w)
		return true
	},
	ActionCopy: func(t *Term, w *gui.Window) bool {
		t.yankCopySelection(w)
		return true
	},
	ActionCopyModeSearchBack: func(t *Term, w *gui.Window) bool {
		t.openCopySearch(true, w)
		return true
	},
	ActionCopyModeSearchFwd: func(t *Term, w *gui.Window) bool {
		t.openCopySearch(false, w)
		return true
	},
	ActionCopyModePrevMatch: func(t *Term, w *gui.Window) bool {
		t.copySearchJump(t.copy.backward, w)
		return true
	},
	ActionCopyModeNextMatch: func(t *Term, w *gui.Window) bool {
		t.copySearchJump(!t.copy.backward, w)
		return true
	},
	ActionCopyModePrevMark: func(t *Term, w *gui.Window) bool {
		t.copyMarkJump(true, w)
		return true
	},
	ActionCopyModeNextMark: func(t *Term, w *gui.Window) bool {
		t.copyMarkJump(false, w)
		return true
	},
	ActionJumpFailure: func(t *Term, w *gui.Window) bool {
		t.jumpToFailure(w)
		return true
	},
	ActionSelectOutput: func(t *Term, w *gui.Window) bool {
		t.selectCommandOutput(w)
		return true
	},
}

// copyModeOrder is the dispatch order for copy-mode keys. It preserves the
// case order of the old binds-switch in handleCopyModeKey, which decided
// which action wins when a user rebinds two actions onto one chord —
// first-listed wins. The three Term-level tail actions keep their places at
// the end.
var copyModeOrder = []Action{
	ActionCopyModeExit,
	ActionCopyModeLeft,
	ActionCopyModeDown,
	ActionCopyModeUp,
	ActionCopyModeRight,
	ActionCopyModeWordFwd,
	ActionCopyModeWordBack,
	ActionCopyModeLineStart,
	ActionCopyModeLineEnd,
	ActionCopyModeBottom,
	ActionCopyModeTop,
	ActionCopyModeHalfPageUp,
	ActionCopyModeHalfPageDown,
	ActionCopyModePageUp,
	ActionCopyModePageDown,
	ActionCopyModeSelectLine,
	ActionCopyModeSelectChar,
	ActionCopyModeYank,
	ActionCopy,
	ActionCopyModeSearchBack,
	ActionCopyModeSearchFwd,
	ActionCopyModePrevMatch,
	ActionCopyModeNextMatch,
	ActionCopyModePrevMark,
	ActionCopyModeNextMark,
	ActionJumpFailure,
	ActionSelectOutput,
}

// runActionDirect executes one Term-level action from its name, with no
// keyboard event involved. It resolves the same mode gates the keyboard path
// applies, in the same order as onKeyDown:
//
//  1. Hints entry chords — first, so they work from inside copy mode and
//     while the search bar is open.
//  2. The copy-mode toggle — checked before copy mode owns the keyboard, so
//     it can leave the mode.
//  3. Copy mode owns the keyboard: the copy-mode operations run (including
//     ActionCopy's yank and the mark actions' copy-mode meanings); every
//     other action is a no-op, exactly as handleCopyModeKey consumes it.
//  4. The search bar owns the keyboard: only the actions handleSearchKey
//     dispatches run — the always-on mark/find actions plus the search-bar
//     editing actions; everything else is a no-op.
//  5. Ordinary handling, with each action's own passthrough rules (copy
//     needs a selection, the alt screen keeps its page keys).
//
// Returns false when the action cannot run in the current state.
func (t *Term) runActionDirect(a Action, w *gui.Window) bool {
	if a == ActionHints || a == ActionHintsCopy || a == ActionCopyMode {
		return actionDispatch[a](t, w)
	}
	if t.copy.active && !t.copy.searching {
		if op, ok := copyModeOps[a]; ok {
			op(t, w)
			return true
		}
		return false // consumed as a no-op, matching handleCopyModeKey
	}
	if t.search.active {
		switch a {
		case ActionFind, ActionPrevPrompt, ActionNextPrompt,
			ActionJumpFailure, ActionSelectOutput,
			ActionToggleRegex, ActionNextMatch, ActionPrevMatch:
			return actionDispatch[a](t, w)
		default:
			return false // the search bar consumes everything else
		}
	}
	if _, pureCopy := copyActionSet[a]; pureCopy {
		return false // a copy-mode action outside the mode
	}
	if fn, ok := actionDispatch[a]; ok {
		return fn(t, w)
	}
	return false
}
