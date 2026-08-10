package workspace

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	glyph "github.com/go-gui-org/go-glyph"
	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// The full-window theme browser (Cmd+Shift+T).
//
// It replaces the pane area rather than floating over it. The old picker was a
// 320px panel over a 47%-opaque scrim, which had two problems that got worse
// as the theme list grew from 17 to ~600: the list had no filter and no
// scrolling, and the scrim dimmed the very cells the user was trying to judge
// a theme by.
//
// So the browser takes the window and brings its own preview: a filterable
// list on the left, and on the right the theme's 16 ANSI colors plus sample
// terminal output rendered on that theme's own background. Nothing is applied
// to a live pane until Enter, and Escape puts back whatever was active when
// the browser opened.

// Browser geometry. Fixed pixel values rather than fractions where the content
// dictates the size (a swatch, a row's padding) and fractions where the window
// does (the list column).
const (
	// Fraction of the content width given to the list. The preview needs the
	// larger share: it is showing text at terminal size, and a preview too
	// narrow to fit a sample line teaches nothing.
	browserListFrac = 0.38
	// Clamps on that fraction, so the list stays usable in a narrow window and
	// does not sprawl in a wide one.
	browserListMinW = 220
	browserListMaxW = 420

	browserPad     = 18
	browserRowPadV = 3
	browserRowPadH = 8

	// Cap on the filter text. A paste into the box is unbounded input, and
	// every keystroke folds the needle and scans ~600 names with it; no theme
	// name is anywhere near this long, so anything past it can only cost time.
	browserFilterMax = 128

	// Swatch grid: two rows of eight, matching how ANSI 0-15 is conventionally
	// shown (normal above, bright below).
	browserSwatch    = 22
	browserSwatchGap = 4

	// Estimated row height, as a multiple of the type size. No DrawContext
	// exists at view-build time, so the number of rows that fit has to be
	// estimated the same way help.go estimates its columns.
	browserRowFactor = 1.9
	// Floor on the visible row count, so a very short window still shows a
	// usable window of the list rather than one row.
	browserMinRows = 5
	// Ceiling on it, so no window height or type size can make the row window
	// approach the size of the list it exists to avoid materialising.
	browserMaxRows = 200

	// Stable IDs. The filter input's focus and text live in go-gui's window
	// state, keyed by ID, so it must not change between rebuilds — the view is
	// rebuilt on every keystroke.
	browserFilterID = "workspace-theme-filter"
	// The preview's scroll offset survives the rebuild that every cursor move
	// causes, so it must not change between frames.
	browserPreviewScrollID = "workspace-theme-preview"
	// The name list's scroll container. Stable across rebuilds, or the wheel
	// position would reset on every keystroke.
	browserScrollID = "workspace-theme-list"

	// Rows built beyond each edge of the visible range. Two is go-gui's own
	// default for rows this size; it covers the partial row a fractional scroll
	// offset exposes at each end.
	browserOverscan = 2
)

// themeBrowser is the browser's state. The zero value is closed.
type themeBrowser struct {
	visible bool

	// filter is the text typed into the search box; matches holds the indices
	// into cfg.Themes that survive it, and idx is the cursor's position within
	// matches (not within cfg.Themes).
	filter  string
	matches []int
	idx     int

	// prev is the theme that was active when the browser opened, and prevOK
	// records whether there was one. Escape restores it; without this the
	// browser would have no cancel, which is what the old picker lacked.
	prev   term.NamedTheme
	prevOK bool

	// applied tracks whether a theme was committed this session, so Escape can
	// skip re-applying when nothing changed.
	applied bool

	// gen makes the filter Input's ID unique per "cleared" state. See
	// themeBrowserDismiss.
	gen int

	// rowH and listH cache the list's pixel geometry as the last view build
	// measured it, so cursor movement can work out whether the selected row is
	// inside the scrolled viewport. Zero means no frame has measured it yet and
	// the reveal no-ops. See revealBrowserRow.
	rowH, listH float32

	// revealPending defers the opening scroll until geometry exists. The
	// browser opens on the *active* theme, which in a 600-entry corpus is
	// usually far down the list, but on the frame that opens it nothing has
	// measured a row yet — so the reveal is armed here and consumed by the
	// first build that has the numbers.
	revealPending bool
}

// browserFilterID is the filter Input's view ID for the current generation.
// Stable across the rebuilds that happen on every keystroke — which is what
// keeps focus and the caret where they are — and deliberately *not* stable
// across a filter clear.
func (ws *Workspace) browserFilterID() string {
	return browserFilterID + strconv.Itoa(ws.browser.gen)
}

// ToggleThemeBrowser opens or closes the full-window theme browser. Bound to
// Cmd+Shift+T. No-op when no themes are configured.
func (ws *Workspace) ToggleThemeBrowser() {
	if len(ws.cfg.Themes) == 0 {
		return
	}
	if ws.browser.visible {
		ws.closeThemeBrowser(true)
		return
	}
	// Bump the generation on every open, never resetting it — same reason the
	// palette does (see TogglePalette): refresh no longer wipes go-gui's state
	// registry, so an Input ID a previous open typed into still holds that text.
	ws.browser = themeBrowser{visible: true, gen: ws.browser.gen + 1}
	active := ws.activeThemeIdx()
	if active >= 0 {
		ws.browser.prev = ws.cfg.Themes[active]
		ws.browser.prevOK = true
	}
	ws.refilterThemes()
	// Open on the active theme rather than at the top of the list.
	// refilterThemes cannot do this itself: it preserves the *current*
	// selection, and on open there is not one yet.
	for j, ti := range ws.browser.matches {
		if ti == active {
			ws.browser.idx = j
			break
		}
	}
	// The scroll offset lives in window state keyed by the container's ID, so
	// it outlives a close. Reset to the top, then let the first frame's
	// measurements drive the reveal that brings the active theme into view —
	// revealBrowserRow no-ops until then, since it has no geometry yet.
	ws.w.ScrollVerticalTo(browserScrollID, 0)
	ws.browser.revealPending = true
	// Focus the filter box so typing searches immediately — no mode to enter
	// and no "/" to press. The panes are out of the tree, so nothing else
	// wants the keys.
	ws.w.SetFocus(ws.browserFilterID())
	ws.refresh()
}

// closeThemeBrowser hides the browser, reverting the theme when cancel is set
// and something was actually applied.
func (ws *Workspace) closeThemeBrowser(cancel bool) {
	revert := cancel && ws.browser.applied && ws.browser.prevOK
	prev := ws.browser.prev
	ws.browser = themeBrowser{gen: ws.browser.gen} // gen must survive; see ToggleThemeBrowser
	if revert {
		ws.applyThemeImpl(prev)
	}
	// Hand focus back to the pane that had it, or the terminal stays deaf
	// after the browser closes.
	if p := ws.ActivePane(); p != nil {
		ws.w.SetFocus(p.FocusID())
	}
	ws.refresh()
}

// — filtering ————————————————————————————————————————————————

// refilterThemes recomputes the match list from the current filter text,
// keeping the cursor on the same theme when it survives the narrowing.
//
// Substring rather than fuzzy: theme names are short and users type real
// prefixes ("gruv", "solar", "light"). Fuzzy ranking would reorder the list
// under the cursor on every keystroke, which is worse than a few extra
// characters typed.
func (ws *Workspace) refilterThemes() {
	var wasOn string
	if nt, ok := ws.browserSelected(); ok {
		wasOn = nt.Name
	}

	needle, wantDark, haveChar := parseThemeFilter(ws.browser.filter)
	// Fold the needle once rather than per candidate: this runs over ~600
	// themes on every keystroke, and foldName allocates.
	folded := foldName(needle)
	ws.browser.matches = ws.browser.matches[:0]
	for i, nt := range ws.cfg.Themes {
		if haveChar && nt.Theme.IsDark() != wantDark {
			continue
		}
		if needle != "" && !strings.Contains(foldName(nt.Name), folded) {
			continue
		}
		ws.browser.matches = append(ws.browser.matches, i)
	}

	// Keep the cursor on the theme it was on, so narrowing a filter around the
	// current selection does not move it. Otherwise start at the top: leaving
	// the cursor on a filtered-out row would preview one theme while
	// highlighting another.
	ws.browser.idx = 0
	if wasOn != "" {
		for j, ti := range ws.browser.matches {
			if ws.cfg.Themes[ti].Name == wasOn {
				ws.browser.idx = j
				break
			}
		}
	}
}

// parseThemeFilter splits a leading "dark:" or "light:" token off the query.
// Narrowing to one character is the most common way through a corpus this
// size, and it is information the browser already has via Theme.IsDark.
func parseThemeFilter(q string) (needle string, wantDark, haveChar bool) {
	q = strings.TrimSpace(q)
	lower := strings.ToLower(q)
	switch {
	case strings.HasPrefix(lower, "dark:"):
		return strings.TrimSpace(q[len("dark:"):]), true, true
	case strings.HasPrefix(lower, "light:"):
		return strings.TrimSpace(q[len("light:"):]), false, true
	}
	return q, false, false
}

// foldName lowercases and strips combining marks, which is what makes the
// filter case- and accent-insensitive: "rose pine" finds "Rosé Pine" and
// "cafe" finds "Café". Deliberately narrow — it only has to make the bundled
// names typeable on a US keyboard. Names are short and this runs once per
// theme per keystroke, so the per-rune fold is cheap enough not to warrant a
// precomputed index.
func foldName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.Is(unicode.Mn, r) {
			continue // combining mark left over from decomposition
		}
		b.WriteRune(foldRune(r))
	}
	return b.String()
}

// foldRune maps the precomposed Latin-1 letters that actually occur in the
// bundled theme names onto their ASCII base. A full Unicode normalisation
// would pull in golang.org/x/text for a handful of names.
func foldRune(r rune) rune {
	switch {
	case r >= 'à' && r <= 'å', r == 'ā', r == 'ă', r == 'ą':
		return 'a'
	case r >= 'è' && r <= 'ë', r == 'ē', r == 'ĕ', r == 'ė', r == 'ę', r == 'ě':
		return 'e'
	case r >= 'ì' && r <= 'ï', r == 'ī', r == 'į':
		return 'i'
	case r >= 'ò' && r <= 'ö', r == 'ø', r == 'ō', r == 'ő':
		return 'o'
	case r >= 'ù' && r <= 'ü', r == 'ū', r == 'ů', r == 'ű':
		return 'u'
	case r == 'ç', r == 'ć', r == 'č':
		return 'c'
	case r == 'ñ', r == 'ń', r == 'ň':
		return 'n'
	case r == 'ý', r == 'ÿ':
		return 'y'
	case r == 'š', r == 'ś':
		return 's'
	case r == 'ž', r == 'ź', r == 'ż':
		return 'z'
	}
	return r
}

// browserSelected returns the theme under the cursor.
func (ws *Workspace) browserSelected() (term.NamedTheme, bool) {
	b := &ws.browser
	if b.idx < 0 || b.idx >= len(b.matches) {
		return term.NamedTheme{}, false
	}
	ti := b.matches[b.idx]
	if ti < 0 || ti >= len(ws.cfg.Themes) {
		return term.NamedTheme{}, false
	}
	return ws.cfg.Themes[ti], true
}

// — navigation ———————————————————————————————————————————————

// setThemeFilter is the filter box's OnTextChanged handler. The text is
// whatever the user typed or pasted, so it is truncated to browserFilterMax
// before anything scans with it.
func (ws *Workspace) setThemeFilter(s string) {
	if len(s) > browserFilterMax {
		// Back off to a rune boundary so the cut cannot leave a half-encoded
		// rune for foldName to turn into U+FFFD.
		s = s[:browserFilterMax]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	if !ws.browser.visible || s == ws.browser.filter {
		return
	}
	ws.browser.filter = s
	ws.refilterThemes()
	// Narrowing rebuilds the row set under a scroll offset that referred to the
	// old one, so the cursor's row has to be brought back into view.
	ws.revealBrowserRow()
	ws.refresh()
}

// themeBrowserMove moves the cursor by delta, wrapping at both ends. Only the
// preview changes: applying to live panes on every arrow press would churn
// notifyColorScheme and the mode-2031 reports for panes nobody can currently
// see.
func (ws *Workspace) themeBrowserMove(delta int) {
	n := len(ws.browser.matches)
	if n == 0 {
		return
	}
	ws.browser.idx = ((ws.browser.idx+delta)%n + n) % n
	// The wheel scrolls without touching the cursor, so by the time an arrow is
	// pressed the cursor may be well off-screen; bring it back.
	ws.revealBrowserRow()
	ws.refresh()
}

// themeBrowserPage is PageUp/PageDown: a coarse jump for walking a 600-entry
// list. The step is a fixed multiple of the row floor rather than the actual
// visible count, which only the view knows (it needs the window height) —
// close enough for a scan, and it keeps the state free of view geometry.
func (ws *Workspace) themeBrowserPage(dir int) {
	ws.themeBrowserMove(dir * browserMinRows * 2)
}

// themeBrowserConfirm applies the highlighted theme and closes. A no-op when
// the filter matched nothing — there is nothing to commit, and committing an
// arbitrary theme because Enter was pressed would be surprising.
func (ws *Workspace) themeBrowserConfirm() {
	nt, ok := ws.browserSelected()
	if !ok {
		return
	}
	ws.applyThemeImpl(nt)
	ws.browser.applied = true
	ws.closeThemeBrowser(false)
}

// themeBrowserDismiss is Escape. The filter is cleared first when there is
// one: at that point Escape reads as "undo my search", and closing outright
// would throw away a narrowing the user is still working with.
func (ws *Workspace) themeBrowserDismiss() {
	if ws.browser.filter != "" {
		ws.browser.filter = ""
		// An Input's text lives in go-gui's window state keyed by the view's
		// ID, and there is no imperative setter — so clearing the box means
		// handing it a fresh identity. The generation counter is that
		// identity, and focus has to follow it.
		ws.browser.gen++
		ws.w.SetFocus(ws.browserFilterID())
		ws.refilterThemes()
		ws.refresh()
		return
	}
	ws.closeThemeBrowser(true)
}

// — view —————————————————————————————————————————————————————

// themeBrowserView builds the whole browser page.
func (ws *Workspace) themeBrowserView(w, h float32) gui.View {
	theme := gui.CurrentTheme()

	// The dimensions come from the window; a NaN or Inf would propagate
	// through every Fixed size below and out into the layout. Fall back to a
	// size that still renders a usable browser rather than trying to draw an
	// undefined one.
	if !finiteF32(w) || w <= 0 {
		w = browserListMinW
	}
	if !finiteF32(h) || h <= 0 {
		h = browserListMinW
	}

	listW := w * browserListFrac
	listW = min(max(listW, browserListMinW), browserListMaxW)
	// A window too narrow for both columns drops the preview rather than
	// squeezing it to uselessness; the list is the part you cannot do without.
	showPreview := w-listW > browserListMinW

	page := tight(gui.FixedFixed)
	page.Width = w
	page.Height = h
	page.Color = theme.ColorPanel

	body := tight(gui.FillFill)
	body.Spacing = gui.SomeF(0)
	if showPreview {
		body.Content = []gui.View{
			ws.themeListColumn(theme, listW, h),
			ws.themeDivider(theme),
			ws.themePreviewColumn(theme),
		}
	} else {
		body.Content = []gui.View{ws.themeListColumn(theme, w, h)}
	}

	page.Content = []gui.View{gui.Row(body), ws.themeBrowserFooter(theme)}
	return gui.Column(page)
}

// themeDivider is the vertical rule between list and preview. Rectangle, not a
// thin Column: containers pick up the theme's padding and would inset the rule
// from the page edges.
func (ws *Workspace) themeDivider(theme gui.Theme) gui.View {
	return gui.Rectangle(gui.RectangleCfg{
		Sizing: gui.FixedFill,
		Width:  1,
		Color:  theme.ColorBorder,
	})
}

// themeListColumn is the filter box above the scrolling name list.
func (ws *Workspace) themeListColumn(theme gui.Theme, w, h float32) gui.View {
	col := tight(gui.FixedFill)
	col.Width = w
	col.Padding = gui.NewPadding(browserPad, browserPad, browserPad, browserPad)
	col.Spacing = gui.SomeF(10)

	head := theme.M5
	head.Typeface = glyph.TypefaceBold

	col.Content = []gui.View{
		ws.helpHeader("Themes", theme, head),
		gui.Input(gui.InputCfg{
			ID:            ws.browserFilterID(),
			Text:          ws.browser.filter,
			Placeholder:   "Type to filter…  (try dark: or light:)",
			Sizing:        gui.FillFit,
			OnTextChanged: func(s string, ctx gui.EventCtx) { ws.setThemeFilter(s) },
		}),
		ws.themeListRows(theme, h),
	}
	return gui.Column(col)
}

// themeListRows renders the visible slice of the match list.
//
// Only a window around the scroll position is built. The list is ~600 entries
// and the view is rebuilt on every keystroke, so materialising every row would
// mean hundreds of throwaway views per character typed. The window used to be
// driven by the cursor, which meant the list could only move in response to the
// arrow keys; it is driven by the scroll offset instead, so the wheel moves the
// viewport on its own and the cursor is pulled back into it by
// revealBrowserRow when the arrows are used.
func (ws *Workspace) themeListRows(theme gui.Theme, h float32) gui.View {
	list := tight(gui.FillFill)

	b := &ws.browser
	if len(b.matches) == 0 {
		empty := theme.M5
		list.Content = []gui.View{gui.Text(gui.TextCfg{
			Text:      "No themes match " + strconv.Quote(ws.browser.filter),
			TextStyle: empty,
		})}
		return gui.Column(list)
	}

	// The window is driven by the scroll offset rather than by the cursor, which
	// is what lets the wheel move the list independently of the selection. Rows
	// are a fixed height and the container's spacing is zero, so row N sits at
	// exactly N*rowH — the arithmetic both the virtualisation and the
	// keep-cursor-visible reveal depend on.
	rowH := listRowH(theme.M5.Size, browserRowFactor)
	listH := browserListHeight(h, theme.M5.Size)
	b.rowH, b.listH = rowH, listH
	list.Spacing = gui.SomeF(0)
	if b.revealPending {
		// Geometry exists now, so the deferred opening scroll can run. Doing it
		// before the range is computed means this build already shows the right
		// slice rather than scrolling on the frame after.
		b.revealPending = false
		ws.revealBrowserRow()
	}

	n := len(b.matches)
	first, last := gui.ListVisibleRange(n, rowH, listH,
		ws.w.ScrollVerticalOffset(browserScrollID), browserOverscan)

	activeIdx := ws.activeThemeIdx()
	rows := make([]gui.View, 0, last-first+3)
	// Spacers stand in for the rows that were not built, so the scrollable's
	// content height is the full list's and the thumb reflects all 600 entries
	// rather than just the handful on screen.
	if first > 0 {
		rows = append(rows, browserSpacer(float32(first)*rowH))
	}
	for j := first; j <= last && j < n; j++ {
		ti := b.matches[j]
		nt := ws.cfg.Themes[ti]

		row := tight(gui.FillFixed)
		row.Height = rowH
		row.Padding = gui.NewPadding(browserRowPadV, browserRowPadH, browserRowPadV, browserRowPadH)
		row.Radius = gui.SomeF(3)
		row.Spacing = gui.SomeF(8)
		if j == b.idx {
			row.Color = theme.ColorActive
		}
		pos := j // capture
		row.OnClick = func(ctx gui.EventCtx) {
			// First click moves the cursor (and so the preview); a click on the
			// already-selected row commits. Applying on any click would make a
			// mis-click a theme change.
			if ws.browser.idx == pos {
				ws.themeBrowserConfirm()
			} else {
				ws.browser.idx = pos
				ws.refresh()
			}
		}

		mark := "  "
		if ti == activeIdx {
			mark = "✓ "
		}
		row.Content = []gui.View{
			themeChip(nt.Theme),
			gui.Text(gui.TextCfg{Text: mark + nt.Name, TextStyle: theme.M5}),
		}
		rows = append(rows, gui.Row(row))
	}
	if last < n-1 {
		rows = append(rows, browserSpacer(float32(n-1-last)*rowH))
	}
	list.Content = rows

	// Scrollable wrapper: what makes the wheel work over the list.
	scroll := tight(gui.FillFit)
	scroll.ID = browserScrollID
	scroll.Scrollable = true
	scroll.ScrollMode = gui.ScrollVerticalOnly
	scroll.MaxHeight = listH
	scroll.Clip = true
	// Reserve the scrollbar's lane so the thumb doesn't paint over theme names.
	scroll.Padding = gui.NewPadding(0, scrollGutter(), 0, 0)
	scroll.Content = []gui.View{gui.Column(list)}
	return gui.Column(scroll)
}

// browserSpacer is an invisible fixed-height filler standing in for rows the
// virtualised list did not build.
func browserSpacer(h float32) gui.View {
	return gui.Rectangle(gui.RectangleCfg{Sizing: gui.FillFixed, Height: h})
}

// revealBrowserRow keeps the selected theme inside the scrolled viewport. The
// gap is zero: themeListRows pins the list's spacing to 0 precisely so row N
// sits at exactly N*rowH.
func (ws *Workspace) revealBrowserRow() {
	b := &ws.browser
	ws.revealListRow(browserScrollID, b.idx, b.rowH, b.listH, 0)
}

// finiteF32 reports whether x is a real number the layout can use.
func finiteF32(x float32) bool {
	return !math.IsNaN(float64(x)) && !math.IsInf(float64(x), 0)
}

// listFallbackSize is the type size the list overlays fall back to when the
// backend reports a degenerate one — zero, NaN or Inf before the first
// measurement.
const listFallbackSize = 13

// listRowH is the fixed pixel height of one list row at the given type size,
// with degenerate sizes clamped away. Both list overlays pin their rows to this
// so row N sits at exactly N*rowH — the arithmetic the theme browser's
// virtualisation and both reveal helpers compute against. Clamping at the
// source is what keeps a NaN out of the scroll offsets those helpers write,
// where a `<= 0` guard would not catch it.
func listRowH(size, factor float32) float32 {
	if size <= 0 || !finiteF32(size) {
		size = listFallbackSize
	}
	return size * factor
}

// browserListHeight is the pixel budget for the scrolling name list, derived
// from the same clamped row count browserVisibleRows reports so the two cannot
// disagree about how tall the list is.
func browserListHeight(h, size float32) float32 {
	return float32(browserVisibleRows(h, size)) * listRowH(size, browserRowFactor)
}

// browserVisibleRows estimates how many list rows fit in h pixels.
func browserVisibleRows(h float32, size float32) int {
	if !finiteF32(h) {
		return browserMinRows
	}
	if size <= 0 || !finiteF32(size) {
		size = listFallbackSize
	}
	// The filter box, header and footer eat into the list's share.
	usable := h - 3*browserPad - 90
	rows := usable / (size * browserRowFactor)
	// Clamp before the int conversion: a degenerate type size makes the
	// quotient arbitrarily large, and converting an out-of-range float to int
	// is undefined in Go. The ceiling is also what stops a tall window from
	// materialising the whole 600-entry list, which is the point of windowing.
	if rows > browserMaxRows {
		return browserMaxRows
	}
	return max(int(rows), browserMinRows)
}

// themeChip is the tiny two-tone square beside a name: the theme's background
// with its foreground inset. Enough to tell dark from light and warm from cool
// while scrolling, without reading anything.
func themeChip(th term.Theme) gui.View {
	outer := tight(gui.FixedFixed)
	outer.Width = 14
	outer.Height = 14
	outer.Color = th.DefaultBG
	outer.Radius = gui.SomeF(3)
	outer.ColorBorder = th.ANSI[8]
	outer.SizeBorder = gui.SomeF(1)
	outer.Padding = gui.NewPadding(4, 4, 4, 4)
	outer.Content = []gui.View{gui.Rectangle(gui.RectangleCfg{
		Sizing: gui.FillFill,
		Color:  th.DefaultFG,
	})}
	return gui.Column(outer)
}

// themePreviewColumn renders the highlighted theme: its 16 ANSI colors, then
// sample terminal output, all on that theme's own background.
//
// This is what replaces seeing the real panes, so it has to answer the
// questions a user actually has, which a row of swatches alone does not: is
// the background too bright, is the comment gray legible, do red and green
// read as different, does bold stand out, is a selection visible. Those are
// judged from realistic output, so the sample is a syntax-highlighted source
// file and a paragraph of prose rather than a color chart.
func (ws *Workspace) themePreviewColumn(theme gui.Theme) gui.View {
	nt, ok := ws.browserSelected()
	if !ok {
		return gui.Column(tight(gui.FillFill))
	}
	th := nt.Theme

	title := theme.M5
	title.Typeface = glyph.TypefaceBold

	character := "dark"
	if !th.IsDark() {
		character = "light"
	}

	inner := tight(gui.FillFit)
	inner.Spacing = gui.SomeF(14)
	inner.Content = []gui.View{
		gui.Text(gui.TextCfg{Text: nt.Name, TextStyle: styled(title, th.DefaultFG)}),
		gui.Text(gui.TextCfg{
			Text:      character + " · " + strconv.Itoa(len(ws.browser.matches)) + " shown",
			TextStyle: styled(theme.M6, th.ANSI[8]),
		}),
		swatchGrid(th, theme),
		ws.themeCodeSample(th),
		ws.themeProse(th),
	}

	// The sample is taller than a short window. Scrolling it beats dropping
	// content: the prose at the bottom is where body-text legibility actually
	// shows, and that is the whole question on a light theme. The ID is stable
	// so the scroll position survives the rebuild that every cursor move causes.
	inner.ID = browserPreviewScrollID
	inner.Scrollable = true
	inner.ScrollMode = gui.ScrollVerticalOnly
	inner.Clip = true
	// Reserve the scrollbar's lane so the thumb doesn't paint over the sample.
	inner.Padding = gui.NewPadding(0, scrollGutter(), 0, 0)

	col := tight(gui.FillFill)
	col.Color = th.DefaultBG
	col.Padding = gui.NewPadding(browserPad, browserPad, browserPad, browserPad)
	col.Content = []gui.View{gui.Column(inner)}
	return gui.Column(col)
}

// swatchGrid lays ANSI 0-15 out as two rows of eight, each swatch labelled
// with its index: normal above, bright below, so a bright variant that
// collapses onto its base shows up as a repeated column rather than having to
// be hunted for. The indices matter because that is how an application names
// these colors — "my prompt uses color 4" is the question being answered.
func swatchGrid(th term.Theme, theme gui.Theme) gui.View {
	grid := tight(gui.FillFit)
	grid.Spacing = gui.SomeF(browserSwatchGap)

	label := theme.M6

	// Every label gets the same box, wide enough for two digits. Without it
	// the cell's width tracks its label ("8" vs "15"), so the bottom row's
	// swatches drift right of the top row's and the columns stop lining up —
	// which defeats the whole point of stacking bright over normal.
	labelW := label.Size * 1.2
	if !finiteF32(labelW) || labelW < 10 {
		labelW = 14
	}

	makeRow := func(base int) gui.View {
		row := tight(gui.FillFit)
		row.Spacing = gui.SomeF(browserSwatchGap)
		cells := make([]gui.View, 0, 8)
		for i := base; i < base+8; i++ {
			num := tight(gui.FixedFit)
			num.Width = labelW
			// Right-aligned so the digits sit against their swatch rather
			// than leaving a gap that reads as belonging to the next one.
			num.HAlign = gui.HAlignRight
			num.Content = []gui.View{
				gui.Text(gui.TextCfg{
					Text:      strconv.Itoa(i),
					TextStyle: styled(label, th.ANSI[8]),
				}),
			}

			cell := tight(gui.FitFit)
			cell.Spacing = gui.SomeF(3)
			cell.Content = []gui.View{
				gui.Row(num),
				gui.Rectangle(gui.RectangleCfg{
					Sizing: gui.FixedFixed,
					Width:  browserSwatch,
					Height: browserSwatch,
					Color:  th.ANSI[i],
					Radius: 3,
				}),
			}
			cells = append(cells, gui.Row(cell))
		}
		row.Content = cells
		return gui.Row(row)
	}
	grid.Content = []gui.View{makeRow(0), makeRow(8)}
	return gui.Column(grid)
}

// span is one run of text in a single color, optionally selected. A line is a
// row of spans, because gui.Text carries one color for its whole string.
type span struct {
	text string
	col  gui.Color
	sel  bool
}

// monoStyle is the pane's own font at the pane's own size, so the preview
// shows the theme as the terminal will actually render it — a theme judged in
// the UI font is not the theme you get.
func (ws *Workspace) monoStyle() gui.TextStyle {
	st := ws.cfg.TextStyle
	// Size feeds the sample's line gap as well as its glyphs; a zero or
	// non-finite one would collapse or poison the layout.
	if st.Size <= 0 || !finiteF32(st.Size) {
		st.Size = 13
	}
	return st
}

// renderSpans turns lines of spans into a column of rows.
func renderSpans(lines [][]span, mono gui.TextStyle, selBG gui.Color, gap float32) gui.View {
	out := tight(gui.FillFit)
	out.Spacing = gui.SomeF(2)
	views := make([]gui.View, 0, len(lines))
	for _, ln := range lines {
		if len(ln) == 0 {
			// A blank line is a spacer, not an empty Text: an empty string
			// measures as zero height and would collapse the gap it exists for.
			views = append(views, gui.Rectangle(gui.RectangleCfg{
				Sizing: gui.FillFixed,
				Height: gap,
			}))
			continue
		}
		row := tight(gui.FillFit)
		row.Spacing = gui.SomeF(0)
		spans := make([]gui.View, 0, len(ln))
		for _, sp := range ln {
			st := styled(mono, sp.col)
			if sp.sel {
				st.BgColor = selBG
			}
			spans = append(spans, gui.Text(gui.TextCfg{Text: sp.text, TextStyle: st}))
		}
		row.Content = spans
		views = append(views, gui.Row(row))
	}
	out.Content = views
	return gui.Column(out)
}

// themeCodeSample renders a short Go file the way a syntax-highlighting pager
// would, plus the prompt line that invoked it and a selected range.
//
// Hand-tokenised rather than lexed: the point is to exercise the palette
// slots an editor or `bat` reaches for — keyword, string, number, type,
// function, comment — not to be a Go parser. Each slot is named by its ANSI
// index, so the sample re-colors with the theme instead of being a picture of
// one theme.
func (ws *Workspace) themeCodeSample(th term.Theme) gui.View {
	mono := ws.monoStyle()

	var (
		fg      = th.DefaultFG
		gutter  = th.ANSI[8]  // line numbers, comments
		keyword = th.ANSI[5]  // magenta: func, const, if, return
		str     = th.ANSI[2]  // green: string literals
		num     = th.ANSI[4]  // blue: numeric literals
		typ     = th.ANSI[6]  // cyan: type names
		fn      = th.ANSI[12] // bright blue: function names
		prompt  = th.ANSI[10] // bright green: the shell prompt glyph
		path    = th.ANSI[14] // bright cyan: the file argument
	)

	ln := func(n int) span { return span{fmt.Sprintf("%2d  ", n), gutter, false} }

	lines := [][]span{
		{{"→ ", prompt, false}, {"bat ", fg, false}, {"palette.go", path, false}},
		{},
		{ln(1), {"// resolve decodes a packed color value.", gutter, false}},
		{ln(2), {"func ", keyword, false}, {"(th *", fg, false}, {"Theme", typ, false},
			{") ", fg, false}, {"resolve", fn, false}, {"(c ", fg, false},
			{"uint32", typ, false}, {") ", fg, false}, {"Color", typ, false}, {" {", fg, false}},
		{ln(3), {"    idx := c & ", fg, false}, {"0xFF", num, false}},
		{ln(4), {"    ", fg, false}, {"if ", keyword, false}, {"idx < ", fg, false},
			{"16", num, false}, {" {", fg, false}},
		// One selected span, so the theme's selection tint is visible rather
		// than left to the imagination — it is derived, not authored, so it is
		// the one color a user cannot predict from the swatches.
		{ln(5), {"        ", fg, false}, {"return", keyword, true},
			{" th.ANSI[idx]", fg, true}},
		{ln(6), {"    }", fg, false}},
		{ln(7), {"    ", fg, false}, {"log", fn, false}, {".Printf(", fg, false},
			{`"index %d out of range"`, str, false}, {", idx)", fg, false}},
		{ln(8), {"    ", fg, false}, {"return ", keyword, false}, {"palette[idx]", fg, false}},
		{ln(9), {"}", fg, false}},
		{},
		{{"→ ", prompt, false}, {"go test ", fg, false}, {"./term", path, false}},
		{{"ok  ", th.ANSI[10], false}, {"github.com/go-gui-org/go-term/term", fg, false},
			{"  3.7s", gutter, false}},
	}
	return renderSpans(lines, mono, th.SelectionBG(), mono.Size/2)
}

// themeProse is a paragraph of body text plus a row of the text attributes a
// terminal can apply.
//
// Prose is here because a color chart cannot answer "can I read this for an
// hour". The attribute row is here because bold, dim and italic are where a
// theme most often fails quietly: dim is drawn from ANSI 8, and on a theme
// whose ANSI 8 sits near the background it disappears entirely.
func (ws *Workspace) themeProse(th term.Theme) gui.View {
	mono := ws.monoStyle()

	bold := mono
	bold.Typeface = glyph.TypefaceBold
	italic := mono
	italic.Typeface = glyph.TypefaceItalic
	under := mono
	under.Underline = true
	strike := mono
	strike.Strikethrough = true

	attrs := tight(gui.FillFit)
	attrs.Spacing = gui.SomeF(14)
	attrs.Content = []gui.View{
		gui.Text(gui.TextCfg{Text: "normal", TextStyle: styled(mono, th.DefaultFG)}),
		gui.Text(gui.TextCfg{Text: "bold", TextStyle: styled(bold, th.DefaultFG)}),
		gui.Text(gui.TextCfg{Text: "italic", TextStyle: styled(italic, th.DefaultFG)}),
		gui.Text(gui.TextCfg{Text: "underline", TextStyle: styled(under, th.DefaultFG)}),
		gui.Text(gui.TextCfg{Text: "strike", TextStyle: styled(strike, th.DefaultFG)}),
		// Dim is ANSI 8 on the default background — the pairing
		// TestThemeDimSlotIsVisible guards, shown here so a user can check it.
		gui.Text(gui.TextCfg{Text: "dim", TextStyle: styled(mono, th.ANSI[8])}),
		// Reverse video swaps the defaults, which is how a status line or a
		// search match renders.
		gui.Text(gui.TextCfg{
			Text:      " reverse ",
			TextStyle: reversed(mono, th),
		}),
		gui.Text(gui.TextCfg{
			Text:      " selected ",
			TextStyle: selectedStyle(mono, th),
		}),
	}

	body := styled(mono, th.DefaultFG)
	para := gui.Text(gui.TextCfg{
		Sizing: gui.FillFit,
		Mode:   gui.TextModeWrapKeepSpaces,
		Text: "The quick brown fox jumps over the lazy dog. Pack my box with " +
			"five dozen liquor jugs; a wizard's job is to vex chumps quickly " +
			"in fog. Sphinx of black quartz, judge my vow — 0O1lI|! " +
			"{}[]()<> &*#@$%^~`'\"/\\ →←↑↓ ✓✗ ─│┌┐└┘├┤┬┴┼",
		TextStyle: body,
	})

	out := tight(gui.FillFit)
	out.Spacing = gui.SomeF(10)
	out.Content = []gui.View{gui.Row(attrs), para}
	return gui.Column(out)
}

// reversed swaps the theme's defaults, the way attrInverse does per cell.
func reversed(st gui.TextStyle, th term.Theme) gui.TextStyle {
	st.Color = th.DefaultBG
	st.BgColor = th.DefaultFG
	return st
}

// selectedStyle is ordinary text sitting on the theme's selection tint.
func selectedStyle(st gui.TextStyle, th term.Theme) gui.TextStyle {
	st.Color = th.DefaultFG
	st.BgColor = th.SelectionBG()
	return st
}

// themeCountText is the footer's inventory readout: how many themes are
// listed, and — once a filter narrows the list — out of how many. Unfiltered
// it stays a bare total, since "600 of 600" says nothing the total doesn't.
func (ws *Workspace) themeCountText() string {
	total := len(ws.cfg.Themes)
	shown := len(ws.browser.matches)
	noun := " themes"
	if total == 1 {
		noun = " theme"
	}
	if shown == total {
		return strconv.Itoa(total) + noun
	}
	return strconv.Itoa(shown) + " of " + strconv.Itoa(total) + noun
}

// themeBrowserFooter is the key-hint strip.
func (ws *Workspace) themeBrowserFooter(theme gui.Theme) gui.View {
	bar := tight(gui.FillFit)
	bar.Padding = gui.NewPadding(8, browserPad, 8, browserPad)
	bar.Spacing = gui.SomeF(18)

	hint := func(s string) gui.View {
		return gui.Text(gui.TextCfg{Text: s, TextStyle: theme.M6})
	}
	// Spacer pushes the count to the right edge, away from the key hints.
	fill := tight(gui.FillFit)
	bar.Content = []gui.View{
		hint("↑↓ move"),
		hint("PgUp/PgDn page"),
		hint("Enter apply"),
		hint("Esc cancel"),
		gui.Row(fill),
		hint(ws.themeCountText()),
	}

	wrap := tight(gui.FillFit)
	wrap.Content = []gui.View{
		gui.Rectangle(gui.RectangleCfg{
			Sizing: gui.FillFixed,
			Height: 1,
			Color:  theme.ColorBorder,
		}),
		gui.Row(bar),
	}
	return gui.Column(wrap)
}

// styled returns st with its text color set. gui.TextCfg carries no color of
// its own — the style is where it lives — and the preview needs a different
// color per span.
func styled(st gui.TextStyle, c gui.Color) gui.TextStyle {
	st.Color = c
	return st
}
