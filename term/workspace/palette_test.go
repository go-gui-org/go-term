package workspace

import (
	"math"
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// labelsOf lists the palette's current match labels, for order-sensitive
// assertions.
func labelsOf(ws *Workspace) []string {
	out := make([]string, 0, len(ws.palette.matches))
	for _, i := range ws.palette.matches {
		out = append(out, ws.palette.items[i].label)
	}
	return out
}

func TestPalette_OpensWithBothRegistries(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	if !ws.palette.visible {
		t.Fatal("palette not visible after toggle")
	}

	// A palette that covered only one registry would be missing one of these.
	var sawWorkspace, sawTerm bool
	for _, l := range labelsOf(ws) {
		switch l {
		case "New tab":
			sawWorkspace = true
		case "Copy mode":
			sawTerm = true
		}
	}
	if !sawWorkspace {
		t.Error("palette is missing workspace commands (New tab)")
	}
	if !sawTerm {
		t.Error("palette is missing terminal actions (Copy mode)")
	}
}

// The palette does not list itself: an entry that reopens the list you picked
// it from is not a command.
func TestPalette_DoesNotListItself(t *testing.T) {
	ws := newLiveWorkspace(t)

	var own string
	for i := range ws.commands {
		if ws.commands[i].ID == paletteCommandID {
			own = ws.commands[i].Label
		}
	}
	if own == "" {
		t.Fatal("the palette command has no Label; the fixture proves nothing")
	}

	ws.togglePalette()
	for _, l := range labelsOf(ws) {
		if l == own {
			t.Errorf("palette lists itself as %q", own)
		}
	}
}

// Both list overlays hand their filter Input a fresh view ID on every open.
// go-gui keys an Input's text by view ID and refresh no longer wipes that
// registry, so a reused ID would bring a previous open's text back into a box
// whose filter field is empty.
func TestOverlayFilterID_ChangesOnEveryOpen(t *testing.T) {
	ws := newLiveWorkspace(t)
	// toggleThemeBrowser no-ops on an empty corpus, so give it one to open.
	ws.cfg.Themes = []term.NamedTheme{{Name: "Dark", Theme: term.DefaultTheme}}

	for _, tc := range []struct {
		name   string
		toggle func()
		id     func() string
	}{
		{"palette", ws.togglePalette, ws.paletteFilterID},
		{"themeBrowser", ws.toggleThemeBrowser, ws.browserFilterID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.toggle()
			first := tc.id()
			tc.toggle() // close
			tc.toggle() // reopen
			if got := tc.id(); got == first {
				t.Errorf("filter ID %q reused across a close/reopen", got)
			}
			tc.toggle() // leave closed for the next case
		})
	}
}

func TestPalette_SkipsUnnamedAndGatedCommands(t *testing.T) {
	ws := newLiveWorkspace(t)

	// What the palette should take from the command registry: named, ungated,
	// and not the palette itself. The tab 1-9 commands carry no Label, and the
	// overlay navigation keys are CanExecute-gated — neither is something a user
	// can search for by name.
	wantWS := make(map[string]bool)
	for i := range ws.commands {
		c := ws.commands[i]
		if c.Label != "" && c.CanExecute == nil && c.ID != paletteCommandID {
			wantWS[c.Label] = true
		}
	}
	if len(wantWS) == 0 {
		t.Fatal("no eligible workspace commands; the fixture proves nothing")
	}

	ws.togglePalette()
	gotWS := 0
	for _, l := range labelsOf(ws) {
		if l == "" {
			t.Error("palette contains an unnamed entry")
		}
		if wantWS[l] {
			gotWS++
		}
	}
	if gotWS != len(wantWS) {
		t.Errorf("palette carries %d of %d eligible workspace commands", gotWS, len(wantWS))
	}

	// And nothing gated slipped in. Every gated command is label-less today, so
	// assert that directly: a future gated command that grows a Label would
	// otherwise start appearing in the palette unnoticed.
	for i := range ws.commands {
		c := ws.commands[i]
		if c.CanExecute != nil && c.Label != "" {
			t.Errorf("gated command %q has a Label and would leak into the palette", c.ID)
		}
	}
}

func TestPalette_ViewBuilds(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	buildView(t, ws)

	// And with a filter that matches nothing, which takes the empty-list branch.
	ws.setPaletteFilter("zzz-no-such-command")
	buildView(t, ws)

	// And paging past the ends, where a row-window calculation that assumed a
	// single-step cursor move would go out of bounds.
	ws.setPaletteFilter("")
	for range 5 {
		ws.palettePage(1)
		buildView(t, ws)
	}
	for range 5 {
		ws.palettePage(-1)
		buildView(t, ws)
	}
}

func TestPalette_FilterNarrows(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	all := len(ws.palette.matches)

	ws.setPaletteFilter("split")
	if len(ws.palette.matches) == 0 {
		t.Fatal("filter 'split' matched nothing")
	}
	if len(ws.palette.matches) >= all {
		t.Errorf("filter did not narrow: %d of %d", len(ws.palette.matches), all)
	}
	for _, l := range labelsOf(ws) {
		if !strings.Contains(strings.ToLower(l), "split") {
			t.Errorf("filter 'split' matched %q", l)
		}
	}
}

func TestPalette_FilterIsCaseInsensitive(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	ws.setPaletteFilter("SPLIT")
	if len(ws.palette.matches) == 0 {
		t.Error("uppercase filter matched nothing; fold is not being applied")
	}
}

func TestPalette_CursorSurvivesNarrowing(t *testing.T) {
	// Narrowing a filter around the current selection must not move it —
	// otherwise typing one more character silently retargets Enter.
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	ws.setPaletteFilter("split")

	ws.paletteMove(1)
	want, ok := ws.paletteSelected()
	if !ok {
		t.Fatal("nothing selected")
	}

	ws.setPaletteFilter("split ")
	got, ok := ws.paletteSelected()
	if !ok {
		t.Fatal("nothing selected after narrowing")
	}
	if got.label != want.label {
		t.Errorf("cursor moved from %q to %q across a narrowing", want.label, got.label)
	}
}

func TestPalette_NavigationWraps(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	n := len(ws.palette.matches)
	if n < 2 {
		t.Fatalf("need at least 2 entries, got %d", n)
	}

	ws.palette.idx = 0
	ws.paletteMove(-1)
	if ws.palette.idx != n-1 {
		t.Errorf("moving up from the top gave %d, want %d", ws.palette.idx, n-1)
	}
	ws.paletteMove(1)
	if ws.palette.idx != 0 {
		t.Errorf("moving down from the bottom gave %d, want 0", ws.palette.idx)
	}
}

func TestPalette_ConfirmRunsEntryAndCloses(t *testing.T) {
	ws := newLiveWorkspace(t)
	before := len(ws.tabs)

	ws.togglePalette()
	ws.setPaletteFilter("New tab")
	if len(ws.palette.matches) != 1 {
		t.Fatalf("got %d matches for 'New tab', want 1: %v", len(ws.palette.matches), labelsOf(ws))
	}
	ws.paletteConfirm()

	if ws.palette.visible {
		t.Error("palette still visible after confirm")
	}
	if len(ws.tabs) != before+1 {
		t.Errorf("tabs = %d, want %d — the entry did not run", len(ws.tabs), before+1)
	}
}

func TestPalette_ConfirmWithNoMatchIsNoOp(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	ws.setPaletteFilter("zzz-no-such-command")
	ws.paletteConfirm()
	if !ws.palette.visible {
		t.Error("palette closed on a confirm with nothing selected")
	}
}

func TestPalette_EscapeClearsFilterThenCloses(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	ws.setPaletteFilter("split")

	ws.paletteDismiss()
	if !ws.palette.visible {
		t.Fatal("first Escape closed the palette instead of clearing the filter")
	}
	if ws.palette.filter != "" {
		t.Errorf("filter = %q, want cleared", ws.palette.filter)
	}

	ws.paletteDismiss()
	if ws.palette.visible {
		t.Error("second Escape did not close the palette")
	}
}

func TestPalette_ClosesThemeBrowser(t *testing.T) {
	// Both overlays own Up/Down/Enter and focus; they must not be up together.
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.toggleThemeBrowser()
	ws.togglePalette()

	if ws.browser.visible {
		t.Error("theme browser still visible after opening the palette")
	}
	if !ws.palette.visible {
		t.Error("palette not visible")
	}
}

func TestPalette_FilterIsTruncated(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	ws.setPaletteFilter(strings.Repeat("x", paletteFilterMax*3))
	if len(ws.palette.filter) > paletteFilterMax {
		t.Errorf("filter length = %d, want <= %d", len(ws.palette.filter), paletteFilterMax)
	}
}

// The duplicate-shortcut trap: go-gui's registry rejects a duplicate shortcut,
// and a rejection drops that command silently. Generalizing the overlay
// navigation keys instead of adding a second Up/Down/Enter set is what keeps
// this passing — a regression here is what once disabled Cmd+1..9.
func TestCommands_RegisterWithoutCollision(t *testing.T) {
	ws := newLiveWorkspace(t)

	seenID := make(map[string]bool, len(ws.commands))
	seenChord := make(map[gui.Shortcut]string, len(ws.commands))
	for i := range ws.commands {
		c := ws.commands[i]
		if seenID[c.ID] {
			t.Errorf("duplicate command ID %q", c.ID)
		}
		seenID[c.ID] = true
		// Palette-only commands carry no chord. The registry compares
		// shortcuts only when one is set, so several unset ones are not a
		// collision — mirror that rule here rather than flagging them.
		if !c.Shortcut.IsSet() {
			continue
		}
		if prev, dup := seenChord[c.Shortcut]; dup {
			t.Errorf("commands %q and %q share shortcut %v", prev, c.ID, c.Shortcut)
		}
		seenChord[c.Shortcut] = c.ID
	}

	// The canary: a dropped batch takes the tab shortcuts with it.
	for _, want := range []string{"workspace.tab1", "workspace.tab9", "workspace.commandPalette"} {
		if !seenID[want] {
			t.Errorf("command %q is missing", want)
		}
	}
}

// The overlay navigation commands must route to whichever overlay is up.
func TestOverlayNav_RoutesToVisibleOverlay(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))

	if ws.listOverlayOpen(nil) {
		t.Error("listOverlayOpen is true with no overlay up")
	}

	ws.toggleThemeBrowser()
	if !ws.listOverlayOpen(nil) {
		t.Fatal("listOverlayOpen is false with the browser up")
	}
	browserIdx := ws.browser.idx
	ws.overlayMove(1)
	if ws.browser.idx == browserIdx {
		t.Error("overlayMove did not move the theme browser cursor")
	}

	ws.togglePalette() // closes the browser
	paletteIdx := ws.palette.idx
	ws.overlayMove(1)
	if ws.palette.idx == paletteIdx {
		t.Error("overlayMove did not move the palette cursor")
	}
}

// A click runs the row under the pointer, not whatever the keyboard had
// selected — the two can disagree, because hover deliberately does not move
// the cursor.
func TestPalette_ClickRunsPointedRowNotSelected(t *testing.T) {
	ws := newLiveWorkspace(t)
	before := len(ws.tabs)

	ws.togglePalette()
	ws.setPaletteFilter("tab")
	target := -1
	for j, i := range ws.palette.matches {
		if ws.palette.items[i].label == "New tab" {
			target = j
			break
		}
	}
	if target < 0 {
		t.Fatalf("'New tab' not among matches: %v", labelsOf(ws))
	}
	// Park the keyboard cursor somewhere else, so running the selected entry
	// instead of the clicked one would be visible.
	ws.palette.idx = (target + 1) % len(ws.palette.matches)

	ws.paletteClick(target)

	if len(ws.tabs) != before+1 {
		t.Errorf("tabs = %d, want %d — the clicked entry did not run", len(ws.tabs), before+1)
	}
	if ws.palette.visible {
		t.Error("palette still visible after a click")
	}
}

func TestPalette_ClickOutOfRangeIsNoOp(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	ws.paletteClick(-1)
	ws.paletteClick(len(ws.palette.matches))
	if !ws.palette.visible {
		t.Error("an out-of-range click closed the palette")
	}
}

// --- scrolling ---

// The reveal is a no-op until a frame has measured the list; calling it before
// then must not scroll anything or panic.
func TestPalette_RevealNoOpsWithoutGeometry(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	ws.palette.rowH, ws.palette.listH = 0, 0
	ws.palette.idx = 5
	ws.revealPaletteRow()
	if got := ws.w.ScrollVerticalOffset(paletteScrollID); got != 0 {
		t.Errorf("scroll offset = %v, want 0 (no geometry, no scroll)", got)
	}
}

// Offsets run from 0 (top) downward. A cursor below the viewport scrolls just
// far enough to put its row at the bottom edge; one above puts it at the top;
// one already inside does not move the list at all.
func TestPalette_RevealScrollsMinimally(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	// Row pitch is rowH plus the inter-row gap, so rows sit 11px apart.
	ws.palette.rowH, ws.palette.listH = 10, 100

	// Row 14 spans [154,164); the viewport is [0,100). Bottom-align it.
	ws.palette.idx = 14
	ws.revealPaletteRow()
	if got := ws.w.ScrollVerticalOffset(paletteScrollID); got != -64 {
		t.Errorf("scrolling down: offset = %v, want -64", got)
	}

	// Row 12 spans [132,142), inside the now-visible [64,164). No movement.
	ws.palette.idx = 12
	ws.revealPaletteRow()
	if got := ws.w.ScrollVerticalOffset(paletteScrollID); got != -64 {
		t.Errorf("already visible: offset = %v, want -64 (unchanged)", got)
	}

	// Row 2 spans [22,32), above the viewport. Top-align it.
	ws.palette.idx = 2
	ws.revealPaletteRow()
	if got := ws.w.ScrollVerticalOffset(paletteScrollID); got != -22 {
		t.Errorf("scrolling up: offset = %v, want -22", got)
	}
}

// Wheel scrolling must not retarget what Enter runs — the same rule hover
// follows. Only the arrows move the cursor.
func TestPalette_WheelDoesNotMoveCursor(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	ws.palette.rowH, ws.palette.listH = 10, 100
	before := ws.palette.idx

	ws.w.ScrollVerticalTo(paletteScrollID, -80) // as a wheel event would

	if ws.palette.idx != before {
		t.Errorf("cursor moved to %d on scroll, want %d", ws.palette.idx, before)
	}
}

func TestThemeBrowser_RevealScrollsMinimally(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.toggleThemeBrowser()
	ws.browser.rowH, ws.browser.listH = 10, 100

	ws.browser.idx = 14
	ws.revealBrowserRow()
	if got := ws.w.ScrollVerticalOffset(browserScrollID); got != -50 {
		t.Errorf("scrolling down: offset = %v, want -50", got)
	}

	ws.browser.idx = 1
	ws.revealBrowserRow()
	if got := ws.w.ScrollVerticalOffset(browserScrollID); got != -10 {
		t.Errorf("scrolling up: offset = %v, want -10", got)
	}
}

func TestThemeBrowser_RevealNoOpsWithoutGeometry(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.toggleThemeBrowser()
	ws.browser.rowH, ws.browser.listH = 0, 0
	ws.browser.idx = 2
	ws.revealBrowserRow()
	if got := ws.w.ScrollVerticalOffset(browserScrollID); got != 0 {
		t.Errorf("scroll offset = %v, want 0", got)
	}
}

// The pane must give up focus while the palette is up. A focused Term
// re-asserts window focus from inside its own View, so with the pane still
// focused the rebuild that follows every keystroke yanks focus out of the
// filter box — the box takes no input and the characters land in the shell.
func TestPalette_FilterKeepsFocusAcrossViewBuild(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	if !ws.w.IsFocus(ws.paletteFilterID()) {
		t.Fatal("filter box does not have focus right after open")
	}

	// Build the real tree against the workspace's own window: this is the step
	// that used to steal focus back.
	if v := ws.View(ws.w); v == nil {
		t.Fatal("View returned nil")
	}
	if !ws.w.IsFocus(ws.paletteFilterID()) {
		t.Errorf("focus left the filter box after a view build: now %q",
			ws.w.FocusID())
	}
}

// Closing hands the keyboard back, or the terminal stays deaf.
func TestPalette_CloseReturnsFocusToPane(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.togglePalette()
	ws.togglePalette()

	p := ws.ActivePane()
	if p == nil {
		t.Fatal("no active pane")
	}
	if !ws.w.IsFocus(p.FocusID()) {
		t.Errorf("focus did not return to the pane: now %q", ws.w.FocusID())
	}
}

// The panel must not resize while the filter narrows the list: a viewport that
// tracked the match count would shrink and grow under the cursor on every
// keystroke.
func TestPaletteViewportHeight_ConstantAcrossFiltering(t *testing.T) {
	const wh, rowH, items = 900, 20, 60

	full := paletteViewportHeight(wh, rowH, items)
	if full != paletteListHeight(wh) {
		t.Fatalf("a registry that overflows the budget should pin to it: got %v, want %v",
			full, paletteListHeight(wh))
	}
	// The item count is what drives the height, and filtering does not change
	// it — so the same call during a filter returns the same number.
	if again := paletteViewportHeight(wh, rowH, items); again != full {
		t.Errorf("height moved while filtering: %v then %v", full, again)
	}
}

// A registry too short to fill the budget pins to its own height instead, so
// the panel is not mostly empty space.
func TestPaletteViewportHeight_ShortRegistryPinsToContent(t *testing.T) {
	const wh, rowH = 2000, 20
	got := paletteViewportHeight(wh, rowH, 3)
	want := float32(3*rowH + 2*paletteRowGap)
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if budget := paletteListHeight(wh); got >= budget {
		t.Errorf("short registry should sit under the budget %v, got %v", budget, got)
	}
}

// Degenerate inputs fall back to the window budget rather than collapsing the
// panel to nothing.
func TestPaletteViewportHeight_DegenerateInputs(t *testing.T) {
	budget := paletteListHeight(900)
	for _, tc := range []struct {
		name  string
		rowH  float32
		items int
	}{
		{"no items", 20, 0},
		{"zero row height", 0, 30},
		{"NaN row height", float32(math.NaN()), 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := paletteViewportHeight(900, tc.rowH, tc.items); got != budget {
				t.Errorf("got %v, want the budget %v", got, budget)
			}
		})
	}
}
