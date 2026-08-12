package term

import (
	"sync"
	"time"

	"github.com/rivo/uniseg"
)

// contentPos is a stable content-row coordinate, independent of ViewOffset.
// Rows 0..len(Scrollback)-1 index scrollback oldest-first;
// rows len(Scrollback)..len(Scrollback)+Rows-1 index the live grid.
type contentPos struct{ Row, Col int }

// searchMatch pairs a content position with the column-span of the match.
// Len is in rune columns (not bytes), matching the cell column space.
type searchMatch struct {
	contentPos
	Len int
}

// runeWidth returns the display width of r in cells: 0 (drop / combining),
// 1 (normal), or 2 (east-asian wide, emoji). ASCII fast-paths to 1
// without entering uniseg. Non-ASCII allocates a 1- to 4-byte string for
// the uniseg call — acceptable for correctness, optimized later if the
// Put path becomes hot.
func runeWidth(r rune) int {
	if r < 0x80 {
		if r < 0x20 {
			return 0
		}
		return 1
	}
	w := uniseg.StringWidth(string(r))
	switch {
	case w <= 0:
		// uniseg zero-widths emoji skin-tone modifiers (they are
		// Grapheme_Extend), but wcwidth renders a standalone one as wide.
		// Match wcwidth; genuine combining marks (uniseg 0, not an emoji
		// modifier) stay 0.
		if isEmojiModifier(r) {
			return 2
		}
		return 0
	case w >= 2:
		return 2
	}
	// uniseg reports width 1, but its width data is frozen at Unicode 15.0.0.
	// Consult the current East Asian Width table (eawWide) so codepoints
	// reclassified Wide in a later release match the wcwidth model. This only
	// ever upgrades 1 -> 2; the zero-width and existing-wide cases are settled
	// above and left to uniseg.
	if eawWide(r) {
		return 2
	}
	return 1
}

//go:generate go run gen_eaw.go
//go:generate go run gen_kgpdiacritics.go

// eawWide reports whether r should occupy two cells under the current Unicode
// East Asian Width data (Wide or Fullwidth) — the up-to-date override on
// uniseg's Unicode-15.0.0 width decision. eawWideRanges is generated from
// term/ucd/EastAsianWidth.txt by gen_eaw.go. Regional indicators
// (U+1F1E6..1F1FF) are East_Asian_Width Neutral, but wcwidth — the model
// ucs-detect measures against — renders a lone one as wide, so they are
// special-cased here. A regional-indicator *pair* forms a single grapheme
// cluster uniseg already widths at 2, so this only affects a solitary one.
func eawWide(r rune) bool {
	if r >= 0x1F1E6 && r <= 0x1F1FF {
		return true
	}
	// Binary search over the sorted, non-overlapping ranges.
	lo, hi := 0, len(eawWideRanges)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		switch {
		case r < eawWideRanges[mid].lo:
			hi = mid
		case r > eawWideRanges[mid].hi:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

// isEmojiModifier reports whether r is an emoji skin-tone modifier
// (Emoji_Modifier, U+1F3FB..1F3FF). uniseg zero-widths these (Grapheme_Extend),
// but wcwidth renders a standalone one as wide. Combined with an emoji base
// they form one grapheme cluster whose width comes from the base, so this only
// affects a solitary modifier — see runeWidth and leadingAkshara.
func isEmojiModifier(r rune) bool {
	return r >= 0x1F3FB && r <= 0x1F3FF
}

// cell attribute bits. cell.Attrs is uint16; bits 0..8 are the SGR visual
// attributes, bit 9 is DECSCA protection. Widening from uint8 was free —
// the second byte lands in padding cell already carried.
const (
	attrBold uint16 = 1 << iota
	attrUnderline
	attrInverse
	attrDim
	attrItalic
	attrStrikethrough
	attrBlink    // SGR 5/6 — glyph hidden on alternating half-cycles
	attrConceal  // SGR 8 — glyph never drawn (ncurses A_INVIS, password fields)
	attrOverline // SGR 53 — rule along the cell top, cleared by 55

	// attrProtected is DECSCA (CSI Ps " q), not an SGR attribute: SGR 0 must
	// not clear it, and it has no visual effect. Only the selective erases —
	// DECSEL (CSI ? K), DECSED (CSI ? J) and DECSERA (CSI … $ {) — honor it;
	// EL/ED/ECH, scrolling and ordinary overwrites ignore it entirely. Cells
	// blanked by any erase path while DECSCA is on come out protected, since
	// the blank carries CurAttrs (xterm does the same — its ClearCells masks
	// with ATTRIBUTES, which includes PROTECTED).
	attrProtected

	// attrVisual is every bit a renderer looks at — i.e. Attrs minus
	// protection. Blank-cell fast paths compare against this so a protected
	// space is still recognized as an untouched blank.
	attrVisual = attrProtected - 1
)

// Charset designator bytes used in ESC ( F / ESC ) F sequences.
const (
	charsetASCII      byte = 'B' // ECMA-48 default (US ASCII)
	charsetDECSpecial byte = '0' // DEC Special Graphics line-drawing set
)

// Underline style constants for cell.ULStyle and grid.CurULStyle.
// ulNone means no underline. The others select the decoration shape.
const (
	ulNone   uint8 = 0
	ulSingle uint8 = 1
	ulDouble uint8 = 2
	ulCurly  uint8 = 3
	ulDotted uint8 = 4
	ulDashed uint8 = 5
)

const (
	cursorBlock cursorShape = iota
	cursorUnderline
	cursorBar
)

// DECSCUSRParam returns the current cursor-style parameter for DECRQSS.
func (g *grid) DECSCUSRParam() int {
	switch g.cursorShape {
	case cursorUnderline:
		if g.CursorBlink {
			return 3
		}
		return 4
	case cursorBar:
		if g.CursorBlink {
			return 5
		}
		return 6
	default:
		if g.CursorBlink {
			return 1
		}
		return 2
	}
}

// MaxGridDim caps each dimension of the cell buffer. Real terminals stay
// well below this; the cap exists so a runaway resize (huge canvas, NaN
// metrics, malicious caller) can't allocate hundreds of megabytes.
const MaxGridDim = 1024

// MaxScrollbackCap bounds ScrollbackCap so a malicious or mistaken
// Cfg.ScrollbackRows can't lead to multi-GB allocations as rows scroll.
// At MaxGridDim cols and ~17 B/cell this is roughly 1.7 GB worst case;
// callers should pick a value far below this.
const MaxScrollbackCap = 100000

// clampScrollback bounds n to [0, MaxScrollbackCap].
func clampScrollback(n int) int {
	if n < 0 {
		return 0
	}
	if n > MaxScrollbackCap {
		return MaxScrollbackCap
	}
	return n
}

// clampDim bounds a row or column count to [1, MaxGridDim].
func clampDim(n int) int {
	if n < 1 {
		return 1
	}
	if n > MaxGridDim {
		return MaxGridDim
	}
	return n
}

// BeginSync starts a synchronized-update block (mode 2026 DECSET or the
// legacy DCS =1s): repaints are suppressed until EndSync or the widget's
// watchdog timeout. A begin while a block is already open is idempotent —
// SyncBegan is NOT refreshed, so an app spamming BSU without ever ending
// cannot extend suppression past one timeout window. Legit frame cycles
// (begin → draw → end) each get a full window because EndSync clears
// SyncActive between frames.
func (g *grid) BeginSync() {
	if !g.SyncActive {
		g.SyncBegan = time.Now()
		g.syncOpenSeq = g.mutSeq
	}
	g.SyncActive = true
}

// EndSync ends a synchronized-update block; the caller's next redraw
// check observes SyncActive == false and flushes accumulated dirty rows.
// SyncFrameReady records that a complete frame is now sitting in the grid
// unpainted, which lets the widget flush it even if the application
// immediately opened the next block (see SyncFrameQuiescent).
func (g *grid) EndSync() {
	g.SyncActive = false
	g.SyncFrameReady = true
}

// SyncFrameQuiescent reports whether the grid currently holds a finished,
// unpainted frame with nothing written on top of it — true when a block
// closed and any block opened since has not touched a cell yet. Painting is
// safe exactly then: once the next frame starts writing, the grid holds a
// half-drawn mix and only EndSync (or the watchdog) may release it.
// Caller holds Mu.
func (g *grid) SyncFrameQuiescent() bool {
	return g.SyncFrameReady && g.mutSeq == g.syncOpenSeq
}

// cell.FG and cell.BG are packed uint32 values. The high byte is the
// encoding tag:
//
//	0x00       — palette index, low byte 0..255 (xterm 256-color table)
//	0x01       — direct RGB,   low 24 bits = R<<16 | G<<8 | B
//	0xFF       — default-color sentinel (defer to defaultFG/defaultBG)
//
// SGR 39/49 reset to defaultColor. Plain palette indices encode as
// their numeric value (paletteColor(1) == 1) so equality comparisons
// against small int literals keep working in tests.
const (
	colorPalette uint32 = 0x00 << 24
	colorRGB     uint32 = 0x01 << 24
	defaultColor uint32 = 0xFF << 24
)

// paletteColor encodes a 256-color palette index.
func paletteColor(i uint8) uint32 { return colorPalette | uint32(i) }

// rgbColor encodes a 24-bit RGB triple.
func rgbColor(r, g, b uint8) uint32 {
	return colorRGB | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}

// cell is one terminal grid cell.
//
// Width encodes east-asian wide / emoji handling:
//
//	1 — normal single-cell glyph (default, including ASCII space)
//	2 — wide head; the cell at column+1 is its right-half continuation
//	0 — continuation cell (right half of a width-2 char to the left).
//	    Ch == 0 in this state; the renderer skips it.
//
// ULColor uses the same packed uint32 encoding as FG/BG. defaultColor
// means "use the cell's foreground color." ULStyle selects the decoration
// shape; 0 (ulNone) means no underline regardless of ULColor.
//
// clusterID handles multi-codepoint grapheme clusters (combining marks,
// ZWJ emoji, flags, variation selectors). 0 means the cell is a single
// rune in Ch (the overwhelmingly common case, no sidecar lookup). Non-zero
// indexes grid.clusters for the full cluster string; Ch still holds the
// base (first) rune so width/RTL/geometry checks stay allocation-free.
type cell struct {
	Ch        rune
	FG        uint32 // packed Color (palette index, RGB, or defaultColor)
	BG        uint32
	ULColor   uint32 // packed underline color; defaultColor = use FG
	Attrs     uint16
	Width     uint8
	ULStyle   uint8  // ulNone..ulDashed
	LinkID    uint16 // 0 = no link; non-zero indexes grid.links
	clusterID uint16 // 0 = single rune (Ch); non-zero indexes grid.clusters
}

func defaultCell() cell {
	return cell{Ch: ' ', FG: defaultColor, BG: defaultColor, ULColor: defaultColor, Width: 1}
}

// blankCell returns a space-filled cell carrying the supplied SGR
// state. Used by erase / insert / scroll paths that need to clear
// runs to the *current* attributes (so e.g. an Erase under inverse
// fills with inverse background). Blank cells never carry underline
// decoration (invisible on spaces; ULStyle=0 signals that).
func blankCell(fg, bg uint32, attrs uint16) cell {
	return cell{Ch: ' ', FG: fg, BG: bg, ULColor: defaultColor, Attrs: attrs, Width: 1}
}

// continuation returns a copy of c with Ch cleared and Width zeroed,
// preserving all visual attributes. Used when appending the trailing
// cell of a wide (Width==2) character.
func (c cell) continuation() cell {
	c.Ch = 0
	c.Width = 0
	c.clusterID = 0
	return c
}

const (
	markPromptStart  markKind = iota // OSC 133;A — beginning of prompt
	markCommandStart                 // OSC 133;B — start of user input
	markOutputStart                  // OSC 133;C — command submitted, output begins
	markCommandEnd                   // OSC 133;D — command finished (optional exit code)
)

// maxMarks caps the mark ring to avoid unbounded growth in very long sessions.
const maxMarks = 10000

// maxClusters caps the grapheme-cluster intern pool. clusterID is uint16, so
// 65535 distinct multi-codepoint clusters is the hard ceiling; once full, new
// clusters degrade to their base rune (width preserved, combining marks lost).
const maxClusters = 1<<16 - 1

// grid is a fixed-size character grid. All public methods are safe for
// concurrent callers via Mu; the parser writes under Mu, OnDraw reads
// under Mu.
type grid struct {
	links   map[uint16]string
	linkIDs map[string]uint16

	// Cwd is the most recent value reported via OSC 7 (e.g.
	// "file://host/path"). Embedders read it through Term.Cwd().
	// Empty until the shell emits an OSC 7.
	Cwd string

	Cells []cell // row-major, len = Rows*Cols

	// RowWrapped[r] is true when row r ended with an autowrap (the cursor
	// reached the right margin and wrapped onto row r+1). During Resize,
	// runs of wrapped rows are joined into a logical line and re-wrapped
	// at the new width. Reset to false whenever a row is filled with blank
	// cells (erase, insert, scroll).
	RowWrapped []bool // len = Rows, parallel to live cell buffer

	// Dirty[r] is set whenever row r has a cell-level mutation since the
	// last render. The widget's readLoop reads this (under Mu) to decide
	// whether to bump drawVersion; onDraw calls ClearDirty at the start
	// of each render cycle. dirtyCount tracks how many rows are dirty,
	// letting HasDirtyRows avoid a linear scan. Allocation mirrors
	// RowWrapped: len = Rows.
	Dirty []bool

	// Marks records OSC 133 semantic shell-integration boundaries in
	// content-row coordinates (same space as contentPos). Appended by
	// AddMark; adjusted by scrollback trim and Resize; capped at maxMarks.
	Marks []mark

	// marksVer is bumped on every Marks mutation. It is the cache key for
	// failRows below, so the draw path never rescans the mark stream.
	marksVer uint64

	// failRows caches the prompt rows of failed commands, oldest first, as
	// derived by failureRows(). failRowsValid distinguishes "never built"
	// from "built and legitimately empty at version 0".
	failRows      []int
	failRowsVer   uint64
	failRowsValid bool

	// resizeTrack lets the widget ride extra content rows through the same
	// reflow re-map Resize applies to marks, selection and graphics. Copy
	// mode's cursor and anchor live in the widget, so the grid cannot
	// collect them itself. Set it immediately before Resize; Resize rewrites
	// the entries in place (-1 for a row the re-wrap discarded) and the
	// caller clears it afterwards.
	resizeTrack []int

	kittyFlagStack []uint32

	// Graphics holds decoded images (Phase 32) belonging to the *active*
	// screen. Origin is in content coordinates so images travel through
	// scrollback alongside the text near them. Capped at maxGraphics; oldest
	// evicted first.
	//
	// The list is per-screen, like Cells: EnterAlt stashes the main-screen
	// images in mainSaved and starts the alt screen with none, ExitAlt
	// restores them and drops whatever the alt screen placed. That is what
	// every image-capable terminal does — an image belongs to the screen it
	// was drawn on — and it is what makes `chafa img.png` followed by yazi
	// behave: the picture disappears while the full-screen app is up and
	// comes back when it exits.
	Graphics []graphic

	// virtualImages holds Kitty *virtual* placements (U=1), keyed by image id.
	// Unlike Graphics these own no cells and no origin row, so they are neither
	// per-screen nor adjusted by scroll/reflow — see grid_virtual.go. The
	// renderer pairs them with U+10EEEE placeholder cells found in the
	// viewport. virtualSeq orders insertions for oldest-first eviction.
	virtualImages map[uint32]virtualImage
	virtualSeq    uint64

	// occludeMaxR is one past the highest content row covered by any
	// occludable (non-KGP) graphic — the bound that lets the per-write
	// occlusion check in putCell/eraseSpan skip its scan once every image has
	// scrolled off the live screen. occludeBoundUnknown means "not known,
	// scan and recompute". The invariant is one-directional on purpose: this
	// value may be too high (a wasted scan) but never too low (a missed
	// occlusion), so every path that moves origins upward may simply mark it
	// unknown rather than track the exact maximum.
	occludeMaxR int

	// searchRunes and searchCols are reusable buffers for searchRow,
	// persisted on the grid so repeated Find / ViewportMatches calls
	// don't re-allocate them from nil every time.
	searchRunes []rune
	searchCols  []int

	// URL-detection scratch (joinRows, shared by detectURLAt and hintTargets),
	// persisted for the same reason. urlRunes is the joined clean text of a
	// logical line; urlRows / urlCols map each rune back to its content row and
	// grid column; urlBytes maps each rune to its byte offset in
	// string(urlRunes) so regexp byte spans convert back to rune indices.
	// urlMatches collects the trimmed rune ranges of one line's URL matches.
	urlRunes   []rune
	urlRows    []int
	urlCols    []int
	urlBytes   []int
	urlMatches [][2]int

	// rectBuf is DECCRA's scratch copy of the source rectangle, kept on the
	// grid so an overlapping copy costs no allocation after the first call.
	rectBuf []cell

	// reflowArena is the rowArena reused across Resize reflows: the blocks
	// one reflow carves are re-carved by the next, so steady-state resizing
	// allocates no per-resize scrollback volume (issue #156). Reset to the
	// zero value wherever the scrollback backing is dropped (RIS, ED 3,
	// scrollback cap changes) so a shrink actually returns the memory.
	reflowArena rowArena

	// clusters interns multi-codepoint grapheme cluster strings. A cell's
	// clusterID indexes here (0 = none, index 0 is unused). clusterIDs is the
	// reverse map for deduplication. The pool grows only, bounded by the
	// number of distinct clusters ever seen (capped at maxClusters); short
	// sessions and a tiny realistic emoji/grapheme set keep it small.
	clusters   []string
	clusterIDs map[string]uint16

	// lastGraphic* remember the most recent character committed by putCell so
	// REP (CSI Ps b) can repeat it. lastGraphicW == 0 means "nothing printed
	// yet", which makes REP a no-op. The cluster ID stays valid because the
	// intern pool only grows.
	lastGraphic   rune
	lastGraphicID uint16
	lastGraphicW  uint8

	// gphBuf holds the UTF-8 of the in-progress grapheme cluster during
	// streaming input (PutRune/FlushGrapheme). Reused across cells; only the
	// trailing incomplete cluster is ever retained. Carries across Feed calls
	// only when input ends mid-rune (so a cluster split by the read buffer
	// still joins). Direct Put (tests) bypasses this entirely.
	gphBuf []byte

	// Scrollback ring of rows that have scrolled off the top. Index 0
	// is oldest, Len()-1 is newest. Cap of 0 disables scrollback (rows
	// are dropped on scrollUp). ViewOffset > 0 freezes the viewport at
	// `ViewOffset` rows back from live; OnDraw renders accordingly.
	Scrollback scrollbackRing
	mainSaved  altSavedScreen

	saved savedCursor

	// Selection state in content coordinates (scrollback + live, stable across
	// ViewOffset changes). SelActive == false means no selection (single-click
	// position pre-drag). Anchor and Head may appear in any order; helpers
	// normalize. contentPos row: 0..len(Scrollback)-1 for scrollback rows,
	// len(Scrollback)..len(Scrollback)+Rows-1 for live rows. Col is a cell
	// *boundary* in [0, Cols]; the selected span is half-open [start, end), so a
	// one-cell drag selects exactly one cell.
	SelAnchor contentPos
	SelHead   contentPos
	Rows      int
	Cols      int
	CursorR   int
	CursorC   int

	// BellCount is incremented each time the terminal receives BEL (0x07).
	// The widget watches for changes to trigger a visual flash.
	BellCount uint64

	// OverlayVersion is bumped by parser state that repaints an overlay but
	// dirties no row (OSC 9;4 progress today). Without it the readLoop's
	// HasDirtyRows test would swallow the change and go-gui's tessellation
	// cache would skip OnDraw entirely — the state would land in the grid and
	// never reach the screen. The widget mirrors the value the way it mirrors
	// BellCount.
	OverlayVersion uint64

	// ProgressState / ProgressValue are OSC 9;4 (ConEmu progress), rendered as
	// a fill in the scrollbar track. State: 0 none, 1 normal, 2 error,
	// 3 indeterminate, 4 paused. ProgressValue is 0..100 and meaningless in
	// states 0 and 3.
	ProgressState uint8
	ProgressValue uint8

	// PointerShape is OSC 22 — the mouse cursor an application asked for over
	// this pane. A hovered hyperlink outranks it (see updateHover).
	PointerShape pointerShape

	// Top, Bottom define the scroll region (inclusive, 0-based).
	// Default 0..Rows-1 (full screen). Set via DECSTBM (CSI Pt;Pb r).
	// scrollUpRegion / scrollDownRegion / IND / RI / IL / DL all
	// honor this window; rows outside are untouched.
	Top           int
	Bottom        int
	ScrollbackCap int
	ViewOffset    int
	dirtyCount    int

	// ViewFrozen pins the visible content against incoming output: while set,
	// every row pushed into scrollback bumps ViewOffset by one, so the same
	// content rows stay on screen instead of drifting upward as the buffer
	// grows. Copy mode sets it — a moving target is unusable to select from.
	//
	// The pin is exact only until the scrollback ring starts evicting. Past
	// that, content row indices themselves shift and the view slips by the
	// number of evicted rows; callers clamp their own coordinates rather than
	// this trying to rewrite history. Main-thread writes under Mu; read by the
	// reader goroutine in scrollUpRegion, which already holds Mu.
	ViewFrozen bool

	Mu         sync.Mutex
	CurFG      uint32 // packed Color
	CurBG      uint32
	CurULColor uint32 // current underline color; defaultColor = use fg
	// CursorColor is the fill color for the block cursor, set via OSC 12.
	// defaultColor means "invert the cell under the cursor" (the default).
	CursorColor uint32

	// 0 ≤ ViewSubPx < cellH; with ViewOffset gives the exact scroll position.
	ViewSubPx float32

	// Kitty Keyboard Protocol state. KittyKeyFlags is the current effective
	// flags bitset (0 = legacy mode). Flag bits:
	//   1 (bit 0) — disambiguate escape codes (Tab≠Ctrl+I, Enter≠Ctrl+M, …)
	//   2 (bit 1) — report event types (press/repeat/release)
	//   4 (bit 2) — report alternate keys
	//   8 (bit 3) — report all keys as escape codes
	//  16 (bit 4) — report associated text
	// kittyFlagStack supports CSI > u (push) / CSI < u (pop) nesting.
	KittyKeyFlags uint32

	// CellPxW, CellPxH are advisory cell-pixel sizes set by the widget
	// after its first measurement (under Mu in onDraw). Used to convert
	// pixel-space image dimensions into cell-space cursor advancement
	// at AddGraphic time. Zero before the first measurement.
	CellPxW, CellPxH float32

	// Hyperlink registry (OSC 8). CurLinkID is the active link applied
	// by Put; 0 means no link. links/linkIDs are a sidecar map so cell
	// stays compact — URLs live here, not in each cell. The maps grow
	// only, never shrink; sessions are short and links are rare.
	CurLinkID uint16
	nextLink  uint16

	// TabStops[c] is true when column c has a tab stop set. Initialized to
	// every 8 columns (xterm default). ESC H sets; CSI g clears. Tab()
	// advances to the next set stop, or to Cols-1 when none remains.
	TabStops [MaxGridDim]bool

	// Theme controls the 16 ANSI base colors and the default fg/bg used
	// when rendering cells. Set via Term.SetTheme; defaults to DefaultTheme.
	Theme Theme

	// pal is the effective 256-color table the render path reads: the
	// static xterm table with Theme.ANSI merged over 0–15 and any OSC 4
	// overrides merged on top. Derived state — never assign g.Theme
	// directly, go through setTheme so this stays in sync.
	//
	// palOverride is the sparse OSC 4 layer feeding it, nil until the child
	// app sets an entry (so sessions that never use OSC 4 pay nothing). It
	// is kept separate from Theme so an embedder SetTheme cannot clobber
	// child state, so Theme stays a small comparable value, and so RIS can
	// drop child colors without touching embedder ones. Both types live in
	// palette.go so grid.go needs no go-gui import.
	pal         palTable
	palOverride *palTable

	// MinContrast is the WCAG contrast ratio a cell's foreground must reach
	// against its background; <= 1 disables the clamp (the default). Set via
	// Term.SetMinimumContrast / Cfg.MinimumContrast. contrast memoizes the
	// per-pair result for the foreground pass — see palette_contrast.go.
	MinContrast float64
	contrast    contrastMemo

	// ov holds the overlay colors (scrollbar lane, bell wash, HUD pills)
	// resolved for the current theme. Derived state like pal — rebuilt by
	// rebuildPalette and SetDynColor, never assigned directly. See
	// palette_overlay.go for the derivation rules.
	ov overlayColors

	CurAttrs   uint16
	CurULStyle uint8 // current underline style (ulNone..ulDashed)

	// RectExtent is DECSACE (CSI Ps * x): 0/1 = stream (the default — the run
	// of character positions from the first corner to the second, wrapping
	// through whole rows in between), 2 = rectangle. Consulted only by
	// DECCARA and DECRARA; the erase/fill/copy rectangle operations are
	// always rectangular.
	RectExtent uint8

	CharsetG0      byte  // ESC ( F — designated set for GL when ActiveG=0
	CharsetG1      byte  // ESC ) F — designated set for GL when ActiveG=1
	ActiveG        uint8 // 0 = SI selects G0 into GL, 1 = SO selects G1
	AutoWrap       bool  // DEC ?7 — autowrap at right margin
	OriginMode     bool  // DEC ?6 — CUP/HVP/VPA relative to scroll region
	InsertMode     bool  // CSI 4 h/l — insert vs replace on Put
	CursorVisible  bool  // hidden via DEC ?25 l, shown via ?25 h
	BracketedPaste bool  // DEC ?2004 — wrap pasted text in markers
	FocusReporting bool  // DEC ?1004 — report focus in/out to host
	SyncOutput     bool  // DEC ?2026 — set/reset via DECSET/DECRST; gates the legacy DCS BSU/ESU form
	SyncActive     bool  // currently inside a synchronized update block (DECRQM-reported)
	AppCursorKeys  bool  // DEC ?1 — application cursor key mode
	AppKeypad      bool  // DECNKM — application keypad mode

	// ColorSchemeUpdates is DEC ?2031. When set, the widget pushes an
	// unsolicited CSI ? 997 ; Ps n at the child whenever the light/dark
	// character of the theme flips, so a running neovim/delta/bat can
	// re-pick its syntax palette without being restarted. Off by default:
	// an unsolicited report to an app that never asked for one lands in
	// its input stream as garbage.
	ColorSchemeUpdates bool

	// ReverseScreen is DECSCNM (DEC ?5) — reverse video applied to the whole
	// screen. Purely a rendering flag: the cells keep their real colors, so
	// selection copy, search and the recording format are unaffected.
	ReverseScreen bool

	// AllowColumnMode is DEC ?40 (xterm's allow80to132). DECCOLM is inert
	// until a client asks for it, because a stray ?3h from a confused app
	// would otherwise silently narrow the pane.
	AllowColumnMode bool

	// ColumnMode is the column count DECCOLM (?3) has pinned the grid to —
	// 80, 132, or 0 when the width follows the window as usual. The widget
	// derives rows from the canvas either way; only the width is pinned, so
	// a pinned grid simply leaves the surplus canvas width unpainted.
	//
	// This is what lets vttest's cursor-movement screens pass in a window
	// that is not 80 columns wide: they test clamping at the right margin,
	// and the margin has to be where vttest asked for it.
	ColumnMode int

	// SyncBegan records when the current synchronized-update block started
	// (see BeginSync). The widget's watchdog uses it as the deadline base so
	// a block whose end never arrives cannot suppress repaints forever.
	SyncBegan time.Time

	// SyncFrameReady is set by EndSync and cleared once the widget paints:
	// a completed frame is sitting in the grid. mutSeq counts cell-level
	// mutations; syncOpenSeq snapshots it when a block opens, so comparing
	// the two says whether the newly opened block has written anything yet.
	// Together they let a finished frame be flushed immediately even when
	// the application has already opened the next block — see
	// SyncFrameQuiescent.
	SyncFrameReady bool
	mutSeq         uint64
	syncOpenSeq    uint64

	// Cursor shape + blink. Set via DECSCUSR (CSI Ps SP q). Default is
	// a steady block. Embedders can override blink via
	// Cfg.CursorBlink without overriding shape.
	cursorShape cursorShape
	CursorBlink bool

	// Mouse reporting modes. Multiple may be active at once; the
	// widget emits the broadest report any of them enables. SGR
	// (?1006) is an encoding flag layered on top — without it, the
	// widget drops reports rather than fall back to legacy X10
	// byte-encoding.
	MouseTrack     bool // ?1000 — button press/release
	MouseTrackBtn  bool // ?1002 — press/release + drag (button held)
	MouseTrackAny  bool // ?1003 — any motion, even with no button
	MouseSGR       bool // ?1006 — SGR-style "<b;c;rM/m" encoding
	MouseSGRPixels bool // ?1016 — pixel-precise coordinates in SGR reports

	// Alt-screen state. EnterAlt swaps g.Cells with a fresh blank buffer
	// and stashes main-screen state in mainSaved; ExitAlt restores it.
	// While AltActive, scrollback writes are suppressed (kitty/iTerm/
	// ghostty default) so vim/htop/less don't fill history with their
	// repaint output.
	AltActive bool
	SelActive bool

	// hasSelAnchor is true once a left-click has placed SelAnchor and remains
	// true after a plain click collapses the selection (SelActive == false),
	// so a following Shift+click can extend from that anchor. ClearSelection
	// resets it, so any selection-invalidating event (scroll, reset, alt-screen
	// swap, reflow) forces the next Shift+click to start a fresh anchor rather
	// than extend from a now-meaningless content position.
	hasSelAnchor bool

	// SelMode is the shape of the current selection: how anchor and head are
	// turned into per-row column spans. selChar (the default) is the linear
	// stream everything used to assume; selWord/selLine are linear too but
	// were snapped to unit boundaries when they were made; selBlock is the
	// rectangle Alt+drag produces. Only selBlock changes the span geometry —
	// see selRowSpan, which is the single place the distinction is applied.
	SelMode selMode
}

// selMode is the geometry of a selection. Distinct from copySelMode
// (widget_state.go), which is copy mode's keyboard-driven notion of whether
// a selection is being built at all; this one describes the shape of the
// span that both mouse and copy mode ultimately produce.
type selMode uint8

// selMode values.
const (
	selChar  selMode = iota // linear, cell-granular
	selWord                 // linear, snapped to word bounds
	selLine                 // linear, whole logical lines
	selBlock                // rectangular column band
)

// PushKittyKeyFlags saves the current KittyKeyFlags on the stack and ORs in
// the new flags. Called by CSI > flags u. The stack is capped at 8 entries
// so runaway nesting can't grow it without bound.
func (g *grid) PushKittyKeyFlags(flags uint32) {
	const maxStack = 8
	if len(g.kittyFlagStack) < maxStack {
		g.kittyFlagStack = append(g.kittyFlagStack, g.KittyKeyFlags)
	}
	g.KittyKeyFlags |= flags
}

// PopKittyKeyFlags pops n entries from the KKP flag stack, restoring the
// last pushed flags each time. Called by CSI < n u. Popping past an empty
// stack sets flags to 0 (legacy mode).
func (g *grid) PopKittyKeyFlags(n int) {
	if n < 1 {
		n = 1
	}
	for range n {
		if len(g.kittyFlagStack) == 0 {
			g.KittyKeyFlags = 0
			return
		}
		last := len(g.kittyFlagStack) - 1
		g.KittyKeyFlags = g.kittyFlagStack[last]
		g.kittyFlagStack = g.kittyFlagStack[:last]
	}
}

// SetKittyKeyFlags sets KittyKeyFlags to flags without touching the stack.
// Called by CSI = flags u.
func (g *grid) SetKittyKeyFlags(flags uint32) { g.KittyKeyFlags = flags }

// viewportToContent converts a viewport row (0..Rows-1) to its content row
// (0..len(Scrollback)+Rows-1) at the current ViewOffset. Caller must hold Mu.
func (g *grid) viewportToContent(r int) int {
	sb := g.Scrollback.Len()
	off := clamp(g.ViewOffset, 0, sb)
	return sb - off + r
}

// MouseReporting reports whether any of the press/drag/any-motion
// modes (?1000/?1002/?1003) are active. The widget consults this to
// decide between local selection and host-side report emission.
func (g *grid) MouseReporting() bool {
	return g.MouseTrack || g.MouseTrackBtn || g.MouseTrackAny
}

// Bell increments BellCount. Called by the parser on 0x07 (BEL). Caller
// holds Mu.
func (g *grid) Bell() { g.BellCount++ }

func (g *grid) markDirty(r int) {
	if r < 0 || r >= len(g.Dirty) {
		return
	}
	// mutSeq counts every mutation, not just the first per row, so
	// SyncFrameQuiescent can tell "nothing written since this block opened"
	// from "this row was already dirty before it opened". Counted
	// unconditionally: gating it on SyncActive measured no better, and the
	// increment itself is within noise on BenchmarkParserFeed_PlainText
	// (82.6 MB/s without it, 84.3 with, medians of 6).
	g.mutSeq++
	if !g.Dirty[r] {
		g.Dirty[r] = true
		g.dirtyCount++
	}
}

func (g *grid) markAllDirty() {
	g.mutSeq++
	for i := range g.Dirty {
		g.Dirty[i] = true
	}
	g.dirtyCount = len(g.Dirty)
}

// SetDynColor updates the OSC dynamic color for ps (10=foreground,
// 11=background, 12=cursor). c must be an rgbColor-tagged packed value.
// Marks all rows dirty so the next render picks up the change.
// Called from the parser while Mu is held.
func (g *grid) SetDynColor(ps int, c uint32) {
	col := rgbToGUIColor(c)
	switch ps {
	case 10:
		g.Theme.DefaultFG = col
		g.rebuildOverlay()
	case 11:
		g.Theme.DefaultBG = col
		// A child that repaints the background light with OSC 11 has changed
		// the color scheme as far as DSR ?996 is concerned; the overlays have
		// to follow it for the same reason they follow a theme swap.
		g.rebuildOverlay()
	case 12:
		g.CursorColor = c
	}
	g.markAllDirty()
}

// dynColorRGB returns the r,g,b components of the dynamic color for ps.
// 10=foreground, 11=background, 12=cursor (falls back to DefaultFG when
// CursorColor is unset). Called from the parser while Mu is held.
func (g *grid) dynColorRGB(ps int) (r, gr, b uint8) {
	switch ps {
	case 10:
		c := g.Theme.DefaultFG
		return c.R, c.G, c.B
	case 11:
		c := g.Theme.DefaultBG
		return c.R, c.G, c.B
	default:
		if g.CursorColor != defaultColor {
			return uint8(g.CursorColor >> 16), uint8(g.CursorColor >> 8), uint8(g.CursorColor)
		}
		c := g.Theme.DefaultFG
		return c.R, c.G, c.B
	}
}

// HasDirtyRows reports whether any row is marked dirty since the last
// ClearDirty call. Called under Mu by the widget's readLoop.
func (g *grid) HasDirtyRows() bool { return g.dirtyCount > 0 }

// ClearDirty resets all dirty flags. Called by onDraw under Mu at the
// start of each render cycle so new mutations are captured next frame.
func (g *grid) ClearDirty() {
	clear(g.Dirty)
	g.dirtyCount = 0
}

// clamp bounds v to [lo, hi]. lo <= hi assumed.
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// newGrid allocates a rows×cols grid filled with default cells.
func newGrid(rows, cols int) *grid {
	rows = clampDim(rows)
	cols = clampDim(cols)
	g := &grid{
		Rows:          rows,
		Cols:          cols,
		Cells:         make([]cell, rows*cols),
		RowWrapped:    make([]bool, rows),
		Dirty:         make([]bool, rows),
		CurFG:         defaultColor,
		CurBG:         defaultColor,
		CurULColor:    defaultColor,
		CharsetG0:     charsetASCII,
		CharsetG1:     charsetASCII,
		AutoWrap:      true,
		CursorVisible: true,
		cursorShape:   cursorBlock,
		CursorBlink:   false,
		CursorColor:   defaultColor,
		Top:           0,
		Bottom:        rows - 1,
		Theme:         DefaultTheme,
		links:         make(map[uint16]string),
		linkIDs:       make(map[string]uint16),
		nextLink:      1,
	}
	for i := range g.Cells {
		g.Cells[i] = defaultCell()
	}
	for c := 8; c < MaxGridDim; c += 8 {
		g.TabStops[c] = true
	}
	g.rebuildPalette()
	return g
}

// maxLinkEntries caps the hyperlink registry so an OSC 8 stream with many
// unique URLs can't grow the maps without bound.
const maxLinkEntries = 4096

// internLink returns the ID for url, creating one if needed. Called under Mu.
// Returns 0 when the registry is full; those cells carry no link ID and become
// non-clickable for the life of the session. This is intentional: the cap
// prevents unbounded map growth during very long sessions.
func (g *grid) internLink(url string) uint16 {
	if id, ok := g.linkIDs[url]; ok {
		return id
	}
	if len(g.linkIDs) >= maxLinkEntries {
		return 0
	}
	id := g.nextLink
	g.nextLink++
	if g.nextLink == 0 {
		g.nextLink = 1
	}
	g.links[id] = url
	g.linkIDs[url] = id
	return id
}

// LinkURL returns the URL for the given link ID, or "" for ID 0 / unknown.
func (g *grid) LinkURL(id uint16) string {
	if id == 0 {
		return ""
	}
	return g.links[id]
}

// At returns a pointer to the cell at (r,c) or nil if out of range.
func (g *grid) At(r, c int) *cell {
	if r < 0 || c < 0 || r >= g.Rows || c >= g.Cols {
		return nil
	}
	return &g.Cells[r*g.Cols+c]
}

// translateRune maps printable bytes through the active GL charset.
// Today we honor the DEC Special Graphics set (`0`), which TUIs use for
// box-drawing via SI/SO or ESC ( 0 / ESC ) 0 designation.
func (g *grid) translateRune(ch rune) rune {
	if ch < 0x20 || ch > 0x7e {
		return ch
	}
	charset := g.CharsetG0
	if g.ActiveG == 1 {
		charset = g.CharsetG1
	}
	if charset != charsetDECSpecial {
		return ch
	}
	switch ch {
	case '`':
		return '◆'
	case 'a':
		return '▒'
	case 'f':
		return '°'
	case 'g':
		return '±'
	case 'h':
		return '␤'
	case 'i':
		return '␋'
	case 'j':
		return '┘'
	case 'k':
		return '┐'
	case 'l':
		return '┌'
	case 'm':
		return '└'
	case 'n':
		return '┼'
	case 'o':
		return '⎺'
	case 'p':
		return '⎻'
	case 'q':
		return '─'
	case 'r':
		return '⎼'
	case 's':
		return '⎽'
	case 't':
		return '├'
	case 'u':
		return '┤'
	case 'v':
		return '┴'
	case 'w':
		return '┬'
	case 'x':
		return '│'
	case 'y':
		return '≤'
	case 'z':
		return '≥'
	case '{':
		return 'π'
	case '|':
		return '≠'
	case '}':
		return '£'
	case '~':
		return '·'
	default:
		return ch
	}
}

// ReverseIndex moves the cursor up one row, scrolling the region down
// when at Top. Above Top (outside region) the cursor moves up without
// scrolling. Implements ESC M (RI).
func (g *grid) ReverseIndex() {
	switch {
	case g.CursorR == g.Top:
		g.scrollDownRegion(1)
	case g.CursorR > 0:
		g.markDirty(g.CursorR)
		g.CursorR--
		g.markDirty(g.CursorR)
	}
}

// ClearAll wipes every cell to default and homes the cursor.
func (g *grid) ClearAll() {
	for i := range g.Cells {
		g.Cells[i] = defaultCell()
	}
	// Sixel/iTerm2 images are removed only by painting over their cells, so
	// the flat fill has to do it explicitly — eraseSpan, which normally
	// carries this, is bypassed here. Matches ED 2's flat-fill path.
	if len(g.Graphics) != 0 {
		g.occludeGraphics(0, g.Rows, 0, g.Cols)
	}
	g.CursorR, g.CursorC = 0, 0
	g.markAllDirty()
}

// regionValid reports whether Top/Bottom describe a usable region.
// A degenerate region (Top > Bottom or out of bounds) is treated as
// "no region active" so callers fall back to full-screen behavior.
func (g *grid) regionValid() bool {
	return g.Top >= 0 && g.Bottom < g.Rows && g.Top <= g.Bottom
}

// regionFullScreen reports whether the scroll region spans every row.
// Only full-screen scrolls push to scrollback (DEC convention shared
// by xterm/iTerm/kitty); a status-line app shouldn't fill history with
// its top pane every keystroke.
func (g *grid) regionFullScreen() bool {
	return g.regionValid() && g.Top == 0 && g.Bottom == g.Rows-1
}

// ViewCellAt returns the cell visible at viewport position (r, c)
// honoring ViewOffset. When the viewport row falls inside scrollback,
// that row's stored cells are returned. Outside-range coords yield a
// default cell (never panics). Resize keeps scrollback row widths in
// sync with Cols, so no per-row width clamp is needed here.
func (g *grid) ViewCellAt(r, c int) cell {
	if r < 0 || r >= g.Rows || c < 0 || c >= g.Cols {
		return defaultCell()
	}
	sb := g.Scrollback.Len()
	off := clamp(g.ViewOffset, 0, sb)
	if off == 0 {
		return g.Cells[r*g.Cols+c]
	}
	n := min(off, g.Rows)
	if r < n {
		return g.Scrollback.Row(sb - off + r)[c]
	}
	return g.Cells[(r-n)*g.Cols+c]
}

// ContentRows returns the total number of content rows (scrollback + live).
func (g *grid) ContentRows() int { return g.Scrollback.Len() + g.Rows }

// ContentCellAt returns the cell at content-coordinate (row, col).
// Bounds-safe: out-of-range inputs return a default cell (never panics).
// Caller must hold Mu.
func (g *grid) ContentCellAt(row, col int) cell {
	sb := g.Scrollback.Len()
	if row < 0 || row >= sb+g.Rows || col < 0 || col >= g.Cols {
		return defaultCell()
	}
	if row < sb {
		return g.Scrollback.Row(row)[col]
	}
	return g.Cells[(row-sb)*g.Cols+col]
}

// ContentRowToViewport maps a content row to its viewport row at the current
// ViewOffset. Returns (vr, true) when the content row is visible, (0, false)
// when it is off-screen.
func (g *grid) ContentRowToViewport(contentRow int) (int, bool) {
	vr := g.ContentRowToScreen(contentRow)
	if vr >= 0 && vr < g.Rows {
		return vr, true
	}
	return 0, false
}

// ContentRowToScreen maps a content row to its screen row without clamping.
// The result may be negative (above viewport) or >= g.Rows (below viewport).
// Use ContentRowToViewport for the ok-gated form.
func (g *grid) ContentRowToScreen(contentRow int) int {
	sb := g.Scrollback.Len()
	off := clamp(g.ViewOffset, 0, sb)
	n := min(off, g.Rows)
	if contentRow < sb {
		return contentRow - (sb - off)
	}
	return contentRow - sb + n
}

// partialTopRow returns the scrollback row just above the current viewport top —
// visible when ViewSubPx > 0. Returns nil when no such row exists. Caller must hold Mu.
func (g *grid) partialTopRow() []cell {
	sb := g.Scrollback.Len()
	idx := sb - clamp(g.ViewOffset, 0, sb) - 1
	if idx < 0 {
		return nil
	}
	return g.Scrollback.Row(idx)
}
