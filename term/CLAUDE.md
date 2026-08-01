# term/ — emulator internals

Loaded when working under `term/`. The always-loaded root `CLAUDE.md`
carries the layering invariant, the concurrency model, and the repo-wide
conventions; this file holds the per-subsystem detail.

## Render loop

1. `OnDraw` runs on the main thread inside go-gui's frame pipeline.
2. First call: measure cell width via `dc.TextWidth("M", style)` and
   line height via `dc.FontHeight(style)`. These can return 0 before
   the backend's `TextMeasurer` is ready — the function returns early
   in that case and a later frame populates them.
3. Each frame: derive `rows = floor(dc.Height/cellH)`,
   `cols = floor(dc.Width/cellW)`. If they changed, `Grid.Resize` and
   `PTY.Resize` (sends `TIOCSWINSZ` so the child sees `SIGWINCH`).
4. Passes per frame: coalesced bg-rect runs per row (`drawBgPass`),
   coalesced foreground text runs (`drawFgPass`), graphics
   (`drawGraphics` — sixel/kitty/iTerm2 images), IME composition text
   (`drawIME`), cursor (`drawCursor`), then overlays (`drawOverlays` —
   bell flash, search bar). Cursor shape depends on `CursorShape`
   (block/underline/bar) and `CursorColor`; block falls back to
   cell-inversion when no color set.
5. `DrawCanvas` uses a unique per-Term `canvasID` (`"term-canvas-N"` where
   N is a monotonically-incrementing sequence number) and a `Version`
   counter — go-gui's tessellation cache skips `OnDraw` entirely when the
   version is unchanged. `readLoop` only bumps the version when `HasDirtyRows`
   is true, so no-op PTY sequences do not invalidate the cache.

## Parser scope

Supports a modern xterm/kitty-compatible subset:

- C0: `BEL`, `BS`, `HT`, `LF`, `VT`, `FF`, `CR`, `SO`, `SI`, `ESC`. A C0
  control arriving *inside* an escape/CSI sequence is executed immediately and
  the sequence resumes around it (ECMA-48 §5.4) — `execC0` is the single
  routine for both cases, so ground-state and mid-sequence handling cannot
  drift. `CAN`/`SUB` cancel the sequence instead, and `ESC` abandons it and
  opens a new one — it is not an intermediate byte, so absorbing it would
  splice the next sequence's parameters onto the abandoned one.
- Deferred wrap is encoded as `CursorC == Cols`; `grid.settledCol` collapses it
  for every cursor-relative operation (BS, CUF/CUB, HT, CPR). Without it a
  backspace out of the pending state lands on the right margin instead of one
  column left of it, which drifts every subsequent glyph on the row.
- `ESC # 8` (DECALN) fills the screen with `E`, homes the cursor and resets the
  region. `ESC # 3`–`6` (double-height/width lines) are consumed but ignored —
  the grid has no double-size line attribute.
- DECSCNM (`CSI ?5h`/`?5l`) — reverse video for the whole screen. Render-only:
  folded into `fgOf`/`bgOf` as an XOR against `attrInverse` (one comparison, so
  `resolveColor` keeps inlining), with `grid.defaultFG`/`defaultBG` for the
  paint sites that use theme colors directly — the canvas fill in `View`,
  `fillRun`'s skip test, the IME strip. Those must agree with what `bgOf`
  resolves for a default cell or the screen reverses only halfway. The cells
  keep their real colors, so copy/search/recording are unaffected. RIS clears
  it; DECSTR does not.
- DECCOLM (`CSI ?3h`/`?3l`), gated on `CSI ?40h` as xterm gates it on
  `allow80to132`. Pins `grid.ColumnMode` to 132/80; `prepareResize` honors the
  pin so the window no longer drives the width, and the surplus canvas width
  goes unpainted. Switching erases the screen, homes the cursor and resets the
  region. RIS releases both the pin and the permission; DECSTR leaves them
  (VT510's soft-reset table does not list DECCOLM).
- SGR (`CSI … m`): reset; bold/dim/italic/underline/inverse/strikethrough;
  blink (5/6, one attribute) and conceal (8) with their 25/28 resets;
  extended underlines (4:1–4:5, SGR 21); underline color (58); fg/bg
  16-color, 256-color, 24-bit truecolor. Blink and conceal are rendering-
  only: the grid keeps the real text so selection copy and search are
  unaffected (`maskGlyph` in `widget_draw.go` hides the glyph).
- CSI: cursor movement and positioning, erase in line/display, scroll
  regions (DECSTBM), IND/RI/NEL, IL/DL/ICH/DCH/SU/SD, DECSCUSR (cursor
  shape/blink), DA1 (advertises Sixel via extension 4: `CSI ?1;2;4c`),
  DA2, XTVERSION (`CSI > q` → `DCS >| go-term(ver) ST`), XTWINOPS pixel
  geometry (`CSI 14 t`/`CSI 16 t` → text-area / cell size in pixels)
  and title stack (`CSI 22 t` push / `CSI 23 t` pop; other manipulation
  ops ignored), tab stop clear (TBC), tab navigation
  (CHT `CSI Ps I` / CBT `CSI Ps Z`), erase characters (ECH `CSI Ps X`),
  repeat (REP `CSI Ps b` — ncurses emits it wherever terminfo has `rep`).
- Character protection + rectangular areas (VT420, `grid_rect.go`): DECSCA
  (`CSI Ps " q`) marks characters protected; only the selective erases honor
  it — DECSEL (`CSI ? Ps K`), DECSED (`CSI ? Ps J`), DECSERA (`CSI … $ {`).
  DECERA (`$ z`), DECFRA (`$ x`), DECCARA (`$ r`), DECRARA (`$ t`) and DECCRA
  (`$ v`) ignore protection, as does every ordinary erase/scroll/overwrite.
  DECSACE (`CSI Ps * x`) picks the stream (default) or rectangle extent for
  DECCARA/DECRARA. Protection lives in `cell.Attrs` bit 8 (`attrProtected`)
  so DECSC/DECRC and the alt-screen swap carry it; SGR 0 must *not* clear it,
  DECSTR and RIS must. DA1 still reports VT100 level — apps that gate these
  on `CSI ?64…` will not emit them.
- Reports: DSR 5 (`CSI 5 n` → `CSI 0 n`), CPR (`CSI 6 n`), DECXCPR
  (`CSI ? 6 n`), DECRQM in both the private (`CSI ? Ps $ p`) and ANSI
  (`CSI Ps $ p`) forms, color scheme (`CSI ? 996 n` → `CSI ? 997 ; 1 n` dark /
  `; 2 n` light), and XTSMGRAPHICS (`CSI ? Pi ; Pa ; Pv S`) for sixel color
  registers (256) and geometry — current geometry is the text area, maximum is
  the decoder cap; setting always reports failure because both limits are
  compile-time constants. `img2sixel` and chafa issue it before emitting.
- Reset: RIS (`ESC c`, terminfo rs1) clears screen + scrollback, leaves
  alt screen, and drops every host-set mode; DECSTR (`CSI ! p`, in rs2
  and is2) resets modes/SGR without touching the screen. Both live in
  `grid_reset.go`. DECSTR restores autowrap ON, diverging from VT510 —
  see the comment there.
- Modes: alt screen (1049/1047/47), mouse (1000/1002/1003/1006/1016),
  bracketed paste (2004), focus reporting (1004), synchronized updates
  (2026 — DECSET begins a block, DECRST ends + flushes; a 500 ms watchdog
  in the widget force-ends a block whose end never arrives),
  grapheme clustering (2027 — always on; DECRQM reports it
  permanently set, DECSET/DECRST are no-ops), color-scheme change
  notification (2031 — `Term.SetTheme` pushes `CSI ? 997` at a subscribed
  child, but only when the theme's light/dark character actually flips;
  light vs. dark is the Rec.601 luma of `Theme.DefaultBG`, which OSC 11
  writes through, and deliberately ignores DECSCNM).
- Kitty Keyboard Protocol: `CSI > u` / `< u` / `= u` / `? u` (push/pop/
  set/query); key-release events; left/right modifier distinction.
- DEC Special Graphics: `SI`/`SO`, `ESC (0` / `ESC (B`.
- OSC: window title (0/1/2), palette set/query (4) and reset (104 —
  one index, or all with a bare `OSC 104`), CWD (7), hyperlinks (8),
  desktop notifications (9/777) — `OSC 9 ; 4 ; …` splits off *before* the
  notification path as ConEmu progress, rendered as a fill in the scrollbar
  track — mouse cursor shape (22, mapped onto go-gui's ten cursors by
  `pointer.go`; unknown names leave the shape alone),
  dynamic colors (10/11/12),
  clipboard (52), semantic shell marks (133), iTerm2 `File=` (1337 — both
  `inline=1` images and `inline=0` file transfers). The 1337 key list is
  parsed for real: `name` (base64, sanitized to a bare filename), `size`,
  `width`/`height` (`N` cells, `Npx`, `N%`, `auto`) and
  `preserveAspectRatio`. Transfers reach the host only when it opted in via
  `Cfg.OnDownload`/`Cfg.DownloadDir`; the parser never touches disk itself.
  Color specs accept `rgb:H/H/H`…`rgb:HHHH/HHHH/HHHH` and `#RGB` through
  `#RRRRGGGGBBBB`; X11 color *names* are not supported.
- DCS: DECRQSS (`m`, `r`, ` q`, `"q` DECSCA, `*x` DECSACE), XTGETTCAP (incl.
  `Smulx`/`Setulc` to advertise styled + colored underlines), sixel graphics,
  synchronized updates.
- APC: Kitty Graphics Protocol (transmit/display/place/delete; PNG, raw
  RGBA/RGB; chunked base64 — the opening chunk's `f=`/`s=`/`v=`/`a=`/`C=`
  govern the whole transfer, continuation chunks carry only `m=` and payload).
  `C=1` suppresses the post-placement cursor advance on both `a=T` and `a=p`.
  `c=`/`r=` set the placement's cell footprint; giving only one derives the
  other from the pixel aspect ratio (`kgpCellRect`). `U=1` creates a *virtual*
  placement — see below. Not implemented: relative placements (`P=`/`Q=`),
  z-index (`z=`), source rectangles (`x`/`y`/`w`/`h`), pixel offsets
  (`X=`/`Y=`).

Unicode placeholders (`U=1`) are a second, cell-driven image layer and the
only one that survives a layout-managed host (tmux, vim, yazi). A virtual
placement owns no rectangle and moves no cursor: it records "image N fits a
`c`×`r` rectangle" in `grid.virtualImages` (`grid_virtual.go`) and waits. The
image appears where the application prints U+10EEEE cells whose foreground
color carries the image id (palette index in 256-color mode, RGB in
true-color), whose underline color carries the placement id, and whose
combining diacritics carry row, column and the image id's high byte
(`kgp_placeholder.go`; the 297-entry table is generated from
`ucd/rowcolumn-diacritics.txt`). Omitted diacritics inherit from the cell to
the left, which is why the decode carries a `placeholderCell` across the row.
The cluster — placeholder plus diacritics — is one grapheme, so the decode
reads the interned cluster string, not `cell.Ch`.

Because of that indirection the virtual store is deliberately unlike
`Graphics`: not per-screen (the *cells* are), and untouched by scrollback
trim, reflow and `scrollGraphicsRegion` (there is no origin row to move — the
cells reflow as text). `occludeGraphics` can never fire on it either, since
placeholder cells are ordinary text and the store holds no placements. `a=d`
splits accordingly: `d=i` drops the named image's virtual placement, `d=a`
does not (it means "placements visible on screen", and a virtual one occupies
none), uppercase frees the data and takes the virtual placement with it.

`drawPlaceholders` (`widget_draw_graphics.go`) is the render half. It scans
visible cells, coalesces contiguous tiles into rectangles (a full block
collapses to one), extrapolates the placement origin backwards from a
rectangle's first tile, and maps the whole image onto that rectangle. A
rectangle cut off only by the viewport edge draws unclipped — the canvas
clips — so the common path stays one `dc.Image`; a cut *inside* the viewport
(another pane, a partial block) uses `dc.ImageClipped` so the image cannot
spill over neighbouring text. `maskGlyph` blanks the placeholder character so
no tofu box shows through, and `SelectedText` treats those cells as blank so
copy never yields U+10EEEE.

Image lifetime differs by protocol and this distinction is load-bearing.
Kitty placements are their own layer: text drawn over their cells leaves them
alone, and only `a=d` removes them (`kittyDeleteID` — lowercase `d=` drops the
placement, uppercase also frees the stored data). Sixel and iTerm2 images have
no delete sequence, so a client clears one by painting over the cells it
occupies; `grid.occludeGraphics`, called from `putCell`, `eraseSpan` (so EL,
ED and ECH alike) and every flat fill that bypasses `eraseSpan` — ED 2/3,
`ClearAll` (DECCOLM) and `ScreenAlignment` (DECALN) — is what makes that work.

The two layers share one `grid.Graphics` list, so each removal path filters on
`graphic.kgp` and the split is symmetric: `occludeGraphics` skips KGP entries,
`deleteGraphics` touches *only* KGP entries. In particular `a=d,d=a` deletes
every Kitty placement and leaves sixel/iTerm2 images standing — `a=d` is a
Kitty command, and painting over the cells is the only removal signal the
other protocols have.

Images are per-screen, exactly like `Cells`: `EnterAlt` parks the main-screen
list (and its occlusion bound) in `mainSaved`, the alt screen starts with none,
`ExitAlt` restores the main list and drops whatever the alt screen placed. So a
sixel left by `chafa` vanishes when yazi takes the alt screen and returns when
it exits, and a full-screen app can only ever occlude images it placed itself.
`grid.mainGraphics` is how the paths that describe *main-screen* content —
scrollback trim, reflow remap — reach the right list from either screen; the
alt screen's own images move by the flat scrollback delta on resize, like the
selection. Origins are absolute content rows, so a scroll that pushes rows into
scrollback needs no adjustment at all — but one that does not (the alt screen,
a DECSTBM region, IL/DL, `ScrollbackCap == 0`) slides text under fixed origins,
which is what `grid.scrollGraphicsRegion` corrects: images inside the scrolled
region move with it and those pushed out of the region are dropped. `grid.occludeMaxR` bounds the per-glyph scan: it is one past
the highest content row any occludable image covers, so once they have all
scrolled into scrollback the check costs one comparison instead of walking up
to `maxGraphics` entries (measured: 81µs → 0.6µs per 80-column row). The
invariant is one-directional — the bound may be too high, never too low — so
paths that move origins downward (`shiftGraphics`, `remapGraphics`) just set
`occludeBoundUnknown` and let the next scan recompute it exactly.

All of this is needed for yazi, which picks its image protocol from
`TERM_PROGRAM`. `startPTY` scrubs the host terminal's identity variables
(`hostTerminalEnvKeys`) so that choice reflects go-term's own advertised
capabilities rather than whichever emulator launched the app; `cfg.Env` is
applied after the scrub, so an embedder can still declare an identity.

When extending: add cases in the appropriate `parser_*.go` file.
Don't let parser code reach into go-gui — it must stay grid-only.

## Grapheme clusters

Printable input is segmented into orthographic syllables (aksharas), not
single runes. The streaming path is `grid.PutRune` (accumulates runes into
`gphBuf`, committing a leading syllable only once its boundary is observed)
and `grid.FlushGrapheme` (commits the pending syllable). Both go through
`grid.drainAksharas` → `leadingAkshara`, which uses uniseg for grapheme-cluster
boundaries but *fuses* clusters joined by a virama — optionally a virama+ZWJ
explicit conjunct (`isVirama`, jquast's 41-codepoint set; `clusterFusesRight`)
— into one Brahmic syllable, so Javanese `ꦏ꧀ꦏ` or Marathi `र्‍या` occupy a single
cell group. Width matches the terminal-cell model (wcwidth `wcswidth` /
ucs-detect), which diverges from uniseg's per-rune widths: `brahmicWidth`
recomputes any syllable carrying a virama or spacing mark (category Mc) — a
virama is zero-width but caps a conjunct at 2, an Mc mark forces width 2 (so
Sinhala `කා`/Tamil `கா` are 2, not uniseg's 1), and a dangling dead consonant
`ꦏ꧀` is 1, not uniseg's 2. Non-Brahmic clusters (emoji, CJK, RI flags,
variation selectors) keep uniseg's width. The parser flushes before
any control byte in ground state — so DSR/CPR see the advanced cursor.
`parser.Feed` (batch path, tests/direct callers) also flushes at the end;
`parser.feedChunk` (the PTY reader's path) does not, so a grapheme cluster
straddling a read boundary stays pending and is completed by the next chunk
instead of being committed as broken pieces. `readLoop` defers the flush while
the input burst is still draining (the read filled its buffer) and flushes on a
short/final read, so a ZWJ emoji split at the 4096-byte edge renders correctly
while interactive echo and trailing clusters still appear promptly. `grid.Put`
is the immediate single-rune path, kept for tests and direct callers.

Cluster width comes from uniseg (handles VS15/VS16, ZWJ, regional-indicator
flags, combining marks), not per-rune `runeWidth`. Storage: `cell.Ch` holds
the base rune; multi-codepoint clusters set `cell.clusterID`, indexing the
grid-level intern pool `grid.clusters` (0 = single rune — the common,
allocation-free case). The pool grows only, deduped via `clusterIDs`, capped
at `maxClusters`; on exhaustion cells degrade to the base rune (width kept).
Renderers (`drawFgPass`/`emitCell`/cursor) and selection copy use
`cellText` / the pool; cluster cells always emit individually (run coalescing
is base-rune-only). This is what Mode 2027 advertises.

## Keyboard input

`onChar` (printable runes via `gui.ContainerCfg.OnChar`) writes UTF-8
to the PTY. `onKeyDown` translates non-printable keys (arrows, Enter,
Backspace (DEL), Delete, Page Up/Down, Home/End, Ctrl+letter, F1–F12,
numeric keypad) into terminal byte sequences. Alt+key prefixes with ESC.
Set `e.IsHandled = true` so go-gui doesn't propagate.

When `KittyKeyFlags != 0` the widget emits KKP sequences (`CSI codepoint
; modifiers u`) instead of legacy bytes for Backspace, Enter, Tab, Escape,
Ctrl+letters, and functional keys. `onKeyUp` emits release events when
flag bit 2 is set.

`writeBytes` is the single choke point for everything `onChar`/`onKeyDown`
send to the child, which is why `Cfg.OnInput` taps there rather than at each
call site. The paste path taps separately, with the *unwrapped* text, because
bracketed paste (`?2004`) is a per-child mode — a mirrored paste must be
re-encoded by the receiver. Mouse reports, focus reports and pty replies bypass
`writeBytes` and are therefore never tapped, which is correct: they describe
this pane's viewport.

`Term.SendInput` is the receiving half of that tap and the only place the
per-kind replay rules live: `InputKey` bytes go through verbatim, `InputPaste`
text is re-wrapped for the receiver's own `?2004` state. Both snap the pane to
live first, matching local typing — a mirrored keystroke that left a
scrolled-back pane frozen was the first bug this pairing produced. `SendInput`
shares `writeRaw` with `writeBytes` but skips the tap, which is what stops two
panes mirroring to each other from looping.

The widget claims focus via `IDFocus` set to a unique per-Term `focusID`
on its outer `gui.Column`. In multi-Term windows the pane manager calls
`SetFocused` to route `IDFocus` to the active Term.
If keystrokes don't reach the PTY, focus is the first place to look.
