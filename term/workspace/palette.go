package workspace

import (
	"strconv"
	"strings"
	"unicode/utf8"

	glyph "github.com/go-gui-org/go-glyph"
	"github.com/go-gui-org/go-gui/gui"
)

// The command palette (Cmd+Shift+P).
//
// The help overlay already lists every shortcut, but listing is not reaching:
// a user who knows a command exists still has to find and hold its chord. The
// palette closes that gap by making the *name* the address.
//
// It spans both command registries deliberately. Workspace commands (tabs,
// splits, theme) are gui.Commands here; terminal actions (copy, find, scroll,
// font, copy-mode motions) are term.Actions inside the pane. A palette that
// covered only one of them would be a palette that cannot open a tab, or one
// that cannot start a search — so it builds from both and hides the seam.

// Palette geometry. Fixed pixels where the content dictates the size, fractions
// where the window does.
const (
	palettePanelW     = 520
	palettePad        = 14
	paletteRowPadV    = 3
	paletteRowPadH    = 8
	paletteHeightFrac = 0.6

	// Cap on the filter text, for the same reason the theme browser caps its
	// own: a paste into the box is unbounded input, and every keystroke folds
	// the needle and rescans every item with it.
	paletteFilterMax = 128

	// Floor on the list's pixel height, so a very short window still shows a
	// usable slice of the list rather than collapsing to one row.
	paletteMinListH = 120

	// Row height as a multiple of the type size. Rows are laid out at a fixed
	// height rather than fitting their content, because that is what makes the
	// scroll position of row N computable without querying the layout tree —
	// see revealPaletteRow.
	paletteRowFactor = 1.9

	// Vertical gap between rows. Named because the pinned viewport height has
	// to account for it — n rows carry n-1 gaps.
	paletteRowGap = 1

	// PageUp/PageDown step, in rows. A fixed count rather than the actual
	// visible row count, which only the layout knows — the same trade
	// themeBrowserPage makes, and it keeps view geometry out of state.
	palettePageRows = 10

	// The filter Input's ID is generation-suffixed for the same reason the
	// theme browser's is — see paletteDismiss.
	paletteFilterID = "workspace-palette-filter"

	// The scroll container's ID. Stable across rebuilds, or the wheel position
	// would reset on every keystroke.
	paletteScrollID = "workspace-palette-list"

	// Capacity hint only: roughly how many actions a pane contributes with
	// copy mode active. Being wrong costs one slice growth, not correctness.
	paletteTermActionsEstimate = 48

	// The palette's own command ID, so paletteItems can leave it out — an entry
	// that reopens the list you picked it from is not a command.
	paletteCommandID = "workspace.commandPalette"
)

// paletteItem is one invocable entry. run is resolved late — it looks the
// active pane up when invoked rather than capturing it at build time, so a
// palette left open across a pane switch acts on the pane that has focus now.
type paletteItem struct {
	label string
	keys  string
	run   func(w *gui.Window)
}

// palette is the palette's state. The zero value is closed.
type palette struct {
	visible bool

	// items is the full list, built once on open; matches holds the indices
	// into items that survive the filter, and idx is the cursor's position
	// within matches (not within items).
	items   []paletteItem
	filter  string
	matches []int
	idx     int

	// gen makes the filter Input's ID unique per "cleared" state.
	gen int

	// rowH and listH cache the list's pixel geometry as the last view build
	// measured it. Cursor movement needs them to work out whether the selected
	// row is inside the scrolled viewport, and go-gui's ScrollToView — the
	// call that would answer that from the layout tree instead — dereferences
	// the window's root layout without a nil guard, so it panics on a window
	// that has not rendered yet. Deriving the offset here avoids depending on
	// it. Zero means "no frame has measured the list", and the reveal no-ops.
	rowH, listH float32
}

// paletteFilterID is the filter Input's view ID for the current generation.
// Stable across the rebuilds every keystroke causes — which is what keeps focus
// and the caret where they are — and deliberately not stable across a clear.
func (ws *Workspace) paletteFilterID() string {
	return paletteFilterID + strconv.Itoa(ws.palette.gen)
}

// TogglePalette opens or closes the command palette. Bound to Cmd+Shift+P.
func (ws *Workspace) TogglePalette() {
	if ws.palette.visible {
		ws.closePalette()
		return
	}
	// The theme browser removes the panes from the view tree; the two overlays
	// cannot be up at once without fighting over focus and Escape.
	if ws.browser.visible {
		ws.closeThemeBrowser(true)
	}
	// Bump the generation on every open, never resetting it. An Input's text
	// lives in go-gui's window state keyed by its view ID, and refresh no
	// longer wipes that registry (see Workspace.refresh) — so reusing an ID a
	// previous open already typed into would bring that text back in a box
	// whose filter field is empty.
	ws.palette = palette{visible: true, gen: ws.palette.gen + 1, items: ws.paletteItems()}
	ws.refilterPalette()
	// The scroll offset lives in window state keyed by the container's ID,
	// which is stable across rebuilds — so it also survives a close. Reset it,
	// or a palette reopened after scrolling would come up mid-list with its
	// cursor on a row that is not on screen.
	ws.w.ScrollVerticalTo(paletteScrollID, 0)
	// Focus the filter box so typing searches immediately — no mode to enter.
	ws.w.SetFocus(ws.paletteFilterID())
	ws.refresh()
}

// closePalette hides the palette and hands focus back to the active pane, or
// the terminal stays deaf afterwards — the same failure closeThemeBrowser
// documents.
func (ws *Workspace) closePalette() {
	ws.palette = palette{gen: ws.palette.gen} // gen must survive; see TogglePalette
	if p := ws.ActivePane(); p != nil {
		ws.w.SetFocus(p.FocusID())
	}
	ws.refresh()
}

// paletteItems builds the full entry list: workspace commands first, then the
// focused pane's terminal actions.
//
// Both sources are filtered to what is actually invocable. A workspace command
// with no Label is internal plumbing (the tab 1-9 entries, the overlay
// navigation keys) and has no name to search for; a terminal action with no
// chord cannot be synthesized by RunAction and would be a dead row.
func (ws *Workspace) paletteItems() []paletteItem {
	// Rough capacity: the command registry plus the terminal's action list.
	// Sizing it off term.Shortcuts() would mean building that whole slice just
	// to read its length.
	items := make([]paletteItem, 0, len(ws.commands)+paletteTermActionsEstimate)
	for i := range ws.commands {
		cmd := ws.commands[i]
		// A nil Execute is a registry entry that exists only to reserve a
		// shortcut; running it from the palette would panic.
		if cmd.Label == "" || cmd.Execute == nil || cmd.ID == paletteCommandID {
			continue
		}
		// CanExecute-gated commands are the overlay navigation keys, which are
		// meaningless as palette entries — they only apply while the overlay
		// they drive is up, and the palette is up instead.
		if cmd.CanExecute != nil {
			continue
		}
		exec := cmd.Execute
		items = append(items, paletteItem{
			label: cmd.Label,
			keys:  cmd.Shortcut.String(),
			run:   func(w *gui.Window) { exec(&gui.Event{}, w) },
		})
	}
	if p := ws.ActivePane(); p != nil {
		for _, s := range p.AvailableShortcuts() {
			action := s.Action
			items = append(items, paletteItem{
				label: s.Label,
				keys:  s.Keys,
				run: func(w *gui.Window) {
					if p := ws.ActivePane(); p != nil {
						p.RunAction(action, w)
					}
				},
			})
		}
	}
	return items
}

// — filtering ————————————————————————————————————————————————

// refilterPalette recomputes the match list from the current filter, keeping
// the cursor on the same entry when it survives the narrowing.
//
// Substring rather than fuzzy, matching the theme browser: fuzzy ranking would
// reorder the list under the cursor on every keystroke, which over a list this
// size costs more than the few extra characters typed. foldName is reused so
// the match is case- and accent-insensitive here too.
func (ws *Workspace) refilterPalette() {
	p := &ws.palette
	var wasOn string
	if it, ok := ws.paletteSelected(); ok {
		wasOn = it.label
	}

	folded := foldName(strings.TrimSpace(p.filter))
	p.matches = p.matches[:0]
	for i := range p.items {
		if folded != "" && !strings.Contains(foldName(p.items[i].label), folded) {
			continue
		}
		p.matches = append(p.matches, i)
	}

	p.idx = 0
	if wasOn != "" {
		for j, ii := range p.matches {
			if p.items[ii].label == wasOn {
				p.idx = j
				break
			}
		}
	}
}

// paletteSelected returns the entry under the cursor.
func (ws *Workspace) paletteSelected() (paletteItem, bool) {
	p := &ws.palette
	if p.idx < 0 || p.idx >= len(p.matches) {
		return paletteItem{}, false
	}
	ii := p.matches[p.idx]
	if ii < 0 || ii >= len(p.items) {
		return paletteItem{}, false
	}
	return p.items[ii], true
}

// — navigation ———————————————————————————————————————————————

// setPaletteFilter is the filter box's OnTextChanged handler. The text is
// whatever was typed or pasted, so it is truncated before anything scans it.
func (ws *Workspace) setPaletteFilter(s string) {
	if len(s) > paletteFilterMax {
		// Back off to a rune boundary so the cut cannot leave a half-encoded
		// rune for foldName to turn into U+FFFD.
		s = s[:paletteFilterMax]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	if !ws.palette.visible || s == ws.palette.filter {
		return
	}
	ws.palette.filter = s
	ws.refilterPalette()
	// Narrowing rebuilds the row set under a scroll offset that referred to the
	// old one, so the cursor's row has to be brought back into view.
	ws.revealPaletteRow()
	ws.refresh()
}

// paletteMove moves the cursor by delta, wrapping at both ends, and scrolls the
// list to keep the cursor in view.
//
// The reveal is what reconciles the two ways the list moves: the wheel scrolls
// the viewport without touching the cursor, so by the time an arrow is pressed
// the cursor may be well off-screen. Without this, the highlight would move
// somewhere the user cannot see.
func (ws *Workspace) paletteMove(delta int) {
	n := len(ws.palette.matches)
	if n == 0 {
		return
	}
	ws.palette.idx = ((ws.palette.idx+delta)%n + n) % n
	ws.revealPaletteRow()
	ws.refresh()
}

// revealPaletteRow keeps the selected row inside the scrolled viewport. The
// gap is passed because the palette's rows carry one and the theme browser's
// do not — ignoring it here would drift by one gap per row, enough to leave
// the cursor off-screen by the bottom of a long list.
func (ws *Workspace) revealPaletteRow() {
	p := &ws.palette
	ws.revealListRow(paletteScrollID, p.idx, p.rowH, p.listH, paletteRowGap)
}

// palettePage is PageUp/PageDown.
func (ws *Workspace) palettePage(dir int) {
	ws.paletteMove(dir * palettePageRows)
}

// paletteConfirm runs the highlighted entry and closes.
//
// The palette closes *before* the entry runs. Several entries open something
// of their own (the theme browser, a search bar, hints mode), and those would
// come up behind a palette that was still on screen and still holding focus.
func (ws *Workspace) paletteConfirm() {
	it, ok := ws.paletteSelected()
	if !ok {
		return
	}
	ws.closePalette()
	it.run(ws.w)
}

// paletteClick runs the entry at match position pos — what a click on that row
// does. The cursor is moved there first so the entry that runs is the one under
// the pointer, not whatever the keyboard had selected.
func (ws *Workspace) paletteClick(pos int) {
	if pos < 0 || pos >= len(ws.palette.matches) {
		return
	}
	ws.palette.idx = pos
	ws.paletteConfirm()
}

// paletteDismiss is Escape. The filter is cleared first when there is one: at
// that point Escape reads as "undo my search" rather than "close".
func (ws *Workspace) paletteDismiss() {
	if ws.palette.filter != "" {
		ws.palette.filter = ""
		// An Input's text lives in go-gui's window state keyed by the view's
		// ID, and there is no imperative setter — so clearing the box means
		// handing it a fresh identity, and focus has to follow it.
		ws.palette.gen++
		ws.w.SetFocus(ws.paletteFilterID())
		ws.refilterPalette()
		ws.refresh()
		return
	}
	ws.closePalette()
}

// — view —————————————————————————————————————————————————————

// paletteBackdrop is a window-sized translucent float behind the panel that
// dims the panes and dismisses the palette on click. Mirrors helpBackdrop,
// including its window-edge guard.
func (ws *Workspace) paletteBackdrop(ww, wh int) gui.View {
	b := tight(gui.FixedFixed)
	b.Width = float32(ww)
	b.Height = float32(wh)
	b.Float = true
	b.FloatAnchor = gui.FloatTopLeft
	b.FloatTieOff = gui.FloatTopLeft
	b.FloatZIndex = 999
	b.Color = gui.RGBA(0, 0, 0, 120)
	b.OnClick = func(ctx gui.EventCtx) {
		// macOS dispatches MouseDown to the content view even when the user is
		// starting a window-resize drag at an edge; without this guard the
		// palette would dismiss on the first touch of a resize.
		const edgePx = float32(30)
		if ctx.Event.MouseX < edgePx || ctx.Event.MouseX > float32(ww)-edgePx ||
			ctx.Event.MouseY < edgePx || ctx.Event.MouseY > float32(wh)-edgePx {
			// A resize drag, not a dismiss: let it through.
			return
		}
		ws.closePalette()
		// The dismiss is the whole click; nothing behind the backdrop
		// should also see it.
		ctx.Consume()
	}
	return gui.Column(b)
}

// palettePanel builds the floating palette: filter box above a scrolling list.
func (ws *Workspace) palettePanel(ww, wh int) gui.View {
	theme := gui.CurrentTheme()
	head := theme.M5
	head.Typeface = glyph.TypefaceBold

	inner := tight(gui.FixedFit)
	inner.Width = palettePanelW
	inner.Spacing = gui.SomeF(8)
	inner.Content = []gui.View{
		ws.helpHeader("Commands", theme, head),
		gui.Input(gui.InputCfg{
			ID:            ws.paletteFilterID(),
			Text:          ws.palette.filter,
			Placeholder:   "Type to filter…",
			Sizing:        gui.FillFit,
			OnTextChanged: func(s string, ctx gui.EventCtx) { ws.setPaletteFilter(s) },
		}),
		ws.paletteRows(theme, float32(wh)),
	}

	panel := tight(gui.FitFit)
	panel.Float = true
	panel.FloatAnchor = gui.FloatMiddleCenter
	panel.FloatTieOff = gui.FloatMiddleCenter
	panel.FloatZIndex = 1000
	panel.Color = theme.ColorPanel
	panel.ColorBorder = theme.ColorBorder
	panel.SizeBorder = gui.SomeF(1)
	panel.Radius = gui.SomeF(6)
	panel.Padding = gui.NewPadding(palettePad, palettePad, palettePad, palettePad)
	// Swallow clicks so they don't fall through to the backdrop, which would
	// dismiss the palette when clicking inside it.
	panel.OnClick = func(ctx gui.EventCtx) {}
	panel.Content = []gui.View{gui.Column(inner)}
	return gui.Column(panel)
}

// paletteRows renders the match list inside a scrollable container.
//
// Every match is materialised, unlike themeListRows, which windows around the
// cursor. That helper exists because its list is ~600 entries and is rebuilt on
// every keystroke; the palette's is bounded by the command registry plus one
// pane's actions — under a hundred — so windowing would buy nothing and cost
// the wheel, which only works over content the scroll container can actually
// measure.
func (ws *Workspace) paletteRows(theme gui.Theme, wh float32) gui.View {
	list := tight(gui.FillFit)
	list.Spacing = gui.SomeF(paletteRowGap)

	p := &ws.palette
	rowStyle := theme.M6

	// Publish the geometry the reveal arithmetic needs. Rows are fixed-height
	// so row N's offset is exactly N*rowH — a Fit height would make it depend
	// on content the layout has not measured yet.
	rowH := listRowH(rowStyle.Size, paletteRowFactor)
	listH := paletteViewportHeight(wh, rowH, len(p.items))
	p.rowH, p.listH = rowH, listH

	if len(p.matches) == 0 {
		list.Content = []gui.View{gui.Text(gui.TextCfg{
			Text:      "No commands match " + strconv.Quote(p.filter),
			TextStyle: rowStyle,
		})}
		return paletteViewport(list, listH)
	}

	rows := make([]gui.View, 0, len(p.matches))
	for j := range p.matches {
		it := p.items[p.matches[j]]
		row := tight(gui.FillFixed)
		row.Height = rowH
		row.Padding = gui.NewPadding(paletteRowPadV, paletteRowPadH, paletteRowPadV, paletteRowPadH)
		row.Radius = gui.SomeF(3)
		if j == p.idx {
			row.Color = theme.ColorActive
		}
		pos := j // capture
		// A single click runs the entry, unlike the theme browser's two-stage
		// click. Two-stage is coherent there because the first click repaints
		// the preview; here it would move a highlight and nothing else, which
		// reads as a click that was ignored.
		row.OnClick = func(ctx gui.EventCtx) {
			ws.paletteClick(pos)
		}
		// Hover is affordance only — deliberately not a selection change. With
		// a single click running the entry, letting the pointer drive idx would
		// mean a nudged mouse followed by Enter fires whatever the pointer
		// happens to rest on. The cursor shape says "clickable"; the keyboard
		// keeps ownership of what Enter targets.
		row.OnHover = func(ctx gui.EventCtx) {
			ctx.Window.SetMouseCursorPointingHand()
		}
		fill := tight(gui.FillFit)
		row.Content = []gui.View{
			gui.Text(gui.TextCfg{Text: it.label, TextStyle: rowStyle}),
			gui.Row(fill),
			gui.Text(gui.TextCfg{Text: it.keys, TextStyle: rowStyle}),
		}
		rows = append(rows, gui.Row(row))
	}
	list.Content = rows
	return paletteViewport(list, listH)
}

// paletteViewport wraps the list in the scroll container. This is what makes
// the wheel work, and what clips a match list longer than the panel's share of
// the window instead of letting the panel run off-screen.
//
// The height is pinned rather than capped: Min and Max are both h, so the panel
// keeps one size no matter how many rows the filter leaves. A viewport that
// merely capped its height would shrink toward the filter box with every
// keystroke and grow back on backspace, which puts the row under the cursor
// somewhere new on each frame — the panel jitters while the eye is trying to
// read it.
func paletteViewport(list gui.ContainerCfg, h float32) gui.View {
	scroll := tight(gui.FillFit)
	scroll.ID = paletteScrollID
	scroll.Scrollable = true
	scroll.ScrollMode = gui.ScrollVerticalOnly
	scroll.MinHeight = h
	scroll.MaxHeight = h
	scroll.Clip = true
	// Reserve the scrollbar's lane; otherwise the thumb paints over the key
	// column on the right of every row.
	scroll.Padding = gui.NewPadding(0, scrollGutter(), 0, 0)
	scroll.Content = []gui.View{gui.Column(list)}
	return gui.Column(scroll)
}

// paletteListHeight is the pixel budget for the scrolling list: the panel's
// share of the window, floored so a very short window still shows several rows
// rather than collapsing to a sliver. Guards a degenerate window height, which
// arrives as NaN or Inf from a backend that has not measured yet.
func paletteListHeight(wh float32) float32 {
	if !finiteF32(wh) || wh <= 0 {
		return paletteMinListH
	}
	return max(wh*paletteHeightFrac, paletteMinListH)
}

// paletteViewportHeight is the height the list is pinned to, derived from the
// window budget and the *unfiltered* item count.
//
// Using the unfiltered count is the whole point: the panel's size then depends
// on the window and the command registry, both of which hold still while the
// user types. A registry too short to fill the budget pins to its own height
// instead, so a workspace with a handful of commands doesn't get a panel that
// is mostly empty space.
func paletteViewportHeight(wh, rowH float32, items int) float32 {
	budget := paletteListHeight(wh)
	if items <= 0 || !finiteF32(rowH) || rowH <= 0 {
		return budget
	}
	// n rows carry n-1 gaps between them.
	full := float32(items)*rowH + float32(items-1)*paletteRowGap
	return min(full, budget)
}
