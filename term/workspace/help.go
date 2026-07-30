package workspace

import (
	glyph "github.com/go-gui-org/go-glyph"
	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// Help-overlay geometry. The panel flows its sections into as few columns as
// will fit the window: a single 460px column ran ~700px tall once the command
// registry grew past ~35 entries, which overflows a laptop viewport.
const (
	helpColW    = 330 // per-column content width, px
	helpColGap  = 22  // gap between columns, px
	helpMaxCols = 3

	// helpPanelChrome is the vertical space the panel spends on padding and
	// border, subtracted from the height budget before the rows are measured.
	helpPanelChrome = 24

	// helpHeaderExtra is the per-section cost above one text line: the
	// header's own padding plus the divider rule.
	helpHeaderExtra = 6

	// helpLineFactor converts a font size to a rendered line height. An
	// estimate — no DrawContext exists at view-build time — deliberately
	// rounded up so the column choice never under-reports height.
	helpLineFactor = 1.45

	// helpHeightFrac / helpWidthFrac cap the panel against the window so the
	// dimmed backdrop stays visible around it.
	helpHeightFrac = 0.85
	helpWidthFrac  = 0.90
)

// helpScrollID keys the scroll state of the overflow fallback. Stable across
// rebuilds so the scroll position survives a re-render of the overlay.
const helpScrollID = "goterm-help-scroll"

// helpSection is one titled block of the cheatsheet. Building the overlay as
// data first lets the column packer measure it before any view is built.
type helpSection struct {
	title string
	rows  []term.ShortcutInfo // reuses the existing Label/Keys pair
}

// ToggleHelp shows or hides the keyboard-shortcut overlay and rebuilds
// the view. Bound to Cmd+/ (toggle) and Escape (close, when visible).
func (ws *Workspace) ToggleHelp() {
	ws.helpVisible = !ws.helpVisible
	ws.refresh()
}

// helpBackdrop is a window-sized translucent float behind the panel that
// dims the panes and dismisses the overlay on click.
func (ws *Workspace) helpBackdrop(ww, wh int) gui.View {
	b := tight(gui.FixedFixed)
	b.Width = float32(ww)
	b.Height = float32(wh)
	b.Float = true
	b.FloatAnchor = gui.FloatTopLeft
	b.FloatTieOff = gui.FloatTopLeft
	b.FloatZIndex = 999
	b.Color = gui.RGBA(0, 0, 0, 120)
	b.OnClick = func(_ *gui.Layout, e *gui.Event, w *gui.Window) {
		// Ignore clicks near window edges — the platform (notably
		// macOS) dispatches MouseDown to the content view even when
		// the user is starting a window-resize drag at a corner or
		// edge. Without this guard the dialog would dismiss on the
		// first touch of a resize instead of on an intentional
		// click-outside-to-dismiss.
		const edgePx = float32(30)
		if e.MouseX < edgePx || e.MouseX > float32(ww)-edgePx ||
			e.MouseY < edgePx || e.MouseY > float32(wh)-edgePx {
			return
		}
		ws.helpVisible = false
		ws.refresh()
		e.IsHandled = true
	}
	return gui.Column(b)
}

// helpSections builds the cheatsheet content. The workspace section is
// generated from the live command registry; the terminal section from the
// active pane's effective bindings, so rebound shortcuts show their real
// chords rather than the built-in defaults. Neither is hand-maintained.
func (ws *Workspace) helpSections() []helpSection {
	wsRows := make([]term.ShortcutInfo, 0, len(ws.commands))
	for _, cmd := range ws.commands {
		// The tab 1–9 commands carry no Label and are skipped.
		if cmd.Label == "" || !cmd.Shortcut.IsSet() {
			continue
		}
		wsRows = append(wsRows, term.ShortcutInfo{
			Label: cmd.Label,
			Keys:  cmd.Shortcut.String(),
		})
	}

	// Fall back to the package defaults when no pane is live (e.g. the last
	// shell exited but the overlay is still up).
	termRows := term.Shortcuts()
	if active := ws.ActivePane(); active != nil {
		termRows = active.Shortcuts()
	}

	return []helpSection{
		{title: "Workspace", rows: wsRows},
		{title: "Terminal", rows: termRows},
	}
}

// sectionUnits is a section's height in text lines: one per row plus the
// header line. The divider is sub-line height and is folded into
// helpHeaderExtra instead.
func sectionUnits(s helpSection) int { return len(s.rows) + 1 }

// packSections distributes sections across cols, splitting a section's rows
// across a column boundary only when it alone overruns the target. The
// continuation repeats the title so a column never opens with orphaned rows.
//
// With today's two near-equal sections at cols == 2 this lands Workspace left
// and Terminal right without splitting either — the natural outcome of the
// balance rule, not a special case.
func packSections(secs []helpSection, cols int) [][]helpSection {
	if cols < 1 {
		cols = 1
	}
	total := 0
	for _, s := range secs {
		total += sectionUnits(s)
	}
	target := (total + cols - 1) / cols // ceil: the tallest column allowed

	out := make([][]helpSection, 0, cols)
	var cur []helpSection
	curUnits := 0

	for _, s := range secs {
		rows := s.rows
		for {
			// The last column is the remainder bucket: never split into a
			// column that does not exist.
			if len(out) == cols-1 {
				cur = append(cur, helpSection{title: s.title, rows: rows})
				curUnits += len(rows) + 1
				break
			}
			free := target - curUnits - 1 // -1 for this section's header line
			if free <= 0 {
				// No room for even a header here; start the next column.
				out = append(out, cur)
				cur, curUnits = nil, 0
				continue
			}
			if len(rows) <= free {
				cur = append(cur, helpSection{title: s.title, rows: rows})
				curUnits += len(rows) + 1
				break
			}
			// Section overruns the column: take what fits, carry the rest
			// into the next column under a repeated title.
			cur = append(cur, helpSection{title: s.title, rows: rows[:free]})
			rows = rows[free:]
			out = append(out, cur)
			cur, curUnits = nil, 0
		}
	}
	out = append(out, cur)
	for len(out) < cols {
		out = append(out, nil) // keep the result shape predictable
	}
	return out
}

// packedHeight estimates the rendered height of the tallest column.
func packedHeight(packed [][]helpSection, rowH float32) float32 {
	var maxH float32
	for _, col := range packed {
		var h float32
		for _, s := range col {
			h += float32(sectionUnits(s))*rowH + helpHeaderExtra
		}
		if h > maxH {
			maxH = h
		}
	}
	return maxH
}

// helpPanelWidth is the panel's content width for n columns.
func helpPanelWidth(n int) float32 {
	return float32(n)*helpColW + float32(n-1)*helpColGap
}

// helpColumns picks the fewest columns whose packed height fits the window,
// bounded by what the window is wide enough to show. Returns the column count
// and the estimated content height, so the caller knows when the scroll
// fallback is needed.
func helpColumns(secs []helpSection, ww, wh int, rowH float32) (int, float32) {
	hBudget := float32(wh)*helpHeightFrac - helpPanelChrome
	wBudget := float32(ww) * helpWidthFrac

	// Widest column count the window can show, even if it still overflows
	// vertically — a too-narrow window falls back to one column and scrolls.
	best, bestH := 1, packedHeight(packSections(secs, 1), rowH)
	for n := 1; n <= helpMaxCols; n++ {
		if n > 1 && helpPanelWidth(n) > wBudget {
			break
		}
		h := packedHeight(packSections(secs, n), rowH)
		best, bestH = n, h
		if h <= hBudget {
			break // fewest columns that fit: stop widening
		}
	}
	return best, bestH
}

// helpPanel is the centered float listing every keyboard shortcut, flowed
// into as many columns as the window needs and can show.
func (ws *Workspace) helpPanel(ww, wh int) gui.View {
	theme := gui.CurrentTheme()
	// M6 is the mono style at SizeTextTiny+1; derive the line height from the
	// same size so a themed font shifts the column choice with it.
	rowStyle := theme.M6
	headStyle := theme.M6
	headStyle.Typeface = glyph.TypefaceBold
	rowH := (theme.SizeTextTiny + 1) * helpLineFactor

	secs := ws.helpSections()
	cols, contentH := helpColumns(secs, ww, wh, rowH)
	packed := packSections(secs, cols)

	colViews := make([]gui.View, 0, len(packed))
	for _, col := range packed {
		c := tight(gui.FixedFit)
		c.Width = helpColW
		c.Spacing = gui.SomeF(1)
		views := make([]gui.View, 0, len(col)*2)
		for _, s := range col {
			views = append(views, ws.helpHeader(s.title, theme, headStyle))
			for _, r := range s.rows {
				views = append(views, ws.helpRow(r.Label, r.Keys, rowStyle))
			}
		}
		c.Content = views
		colViews = append(colViews, gui.Column(c))
	}

	body := tight(gui.FitFit)
	body.Spacing = gui.SomeF(helpColGap)
	body.Content = colViews

	// Scroll fallback: even the widest column count can overflow a short
	// window (or a future, much longer command list). Cap and scroll rather
	// than letting the panel run off-screen.
	inner := tight(gui.FitFit)
	inner.Content = []gui.View{gui.Row(body)}
	if hBudget := float32(wh)*helpHeightFrac - helpPanelChrome; contentH > hBudget {
		inner.ID = helpScrollID
		inner.Scrollable = true
		inner.ScrollMode = gui.ScrollVerticalOnly
		inner.MaxHeight = hBudget
		inner.Clip = true
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
	panel.Padding = gui.SomeP(10, 14, 10, 14)
	panel.Spacing = gui.SomeF(1)
	// Swallow clicks so they don't fall through to the backdrop, which
	// would dismiss the overlay when clicking inside the panel.
	panel.OnClick = func(_ *gui.Layout, e *gui.Event, _ *gui.Window) { e.IsHandled = true }
	panel.Content = []gui.View{gui.Column(inner)}
	return gui.Column(panel)
}

// helpHeader renders a section label with a thin divider below. style is
// passed in so the help overlay can run tighter type than the theme picker,
// which shares this helper.
func (ws *Workspace) helpHeader(text string, theme gui.Theme, style gui.TextStyle) gui.View {
	headerRow := tight(gui.FillFit)
	headerRow.Padding = gui.SomeP(3, 0, 1, 0)
	headerRow.Content = []gui.View{gui.Text(gui.TextCfg{Text: text, TextStyle: style})}

	col := tight(gui.FillFit)
	col.Spacing = gui.SomeF(0)
	col.Content = []gui.View{
		gui.Row(headerRow),
		gui.Rectangle(gui.RectangleCfg{
			Sizing: gui.FillFixed,
			Height: 1.2,
			Color:  theme.ColorBorder,
		}),
	}
	return gui.Column(col)
}

// helpRow renders one "label … keys" line with the keys right-aligned.
func (ws *Workspace) helpRow(label, keys string, style gui.TextStyle) gui.View {
	fill := tight(gui.FillFit)
	row := tight(gui.FillFit)
	row.Content = []gui.View{
		gui.Text(gui.TextCfg{Text: label, TextStyle: style}),
		gui.Row(fill),
		gui.Text(gui.TextCfg{Text: keys, TextStyle: style}),
	}
	return gui.Row(row)
}
