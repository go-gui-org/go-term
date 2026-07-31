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

- C0: `BEL`, `BS`, `HT`, `LF`, `CR`, `ESC`.
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
  (`CSI Ps $ p`) forms.
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
  permanently set, DECSET/DECRST are no-ops).
- Kitty Keyboard Protocol: `CSI > u` / `< u` / `= u` / `? u` (push/pop/
  set/query); key-release events; left/right modifier distinction.
- DEC Special Graphics: `SI`/`SO`, `ESC (0` / `ESC (B`.
- OSC: window title (0/1/2), palette set/query (4) and reset (104 —
  one index, or all with a bare `OSC 104`), CWD (7), hyperlinks (8),
  desktop notifications (9/777), dynamic colors (10/11/12),
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
  Unicode placeholder placement (`U=1`) is *not* implemented — an image sent
  that way is placed at the cursor instead of at the placeholder cells.

Image lifetime differs by protocol and this distinction is load-bearing.
Kitty placements are their own layer: text drawn over their cells leaves them
alone, and only `a=d` removes them (`kittyDeleteID` — lowercase `d=` drops the
placement, uppercase also frees the stored data). Sixel and iTerm2 images have
no delete sequence, so a client clears one by painting over the cells it
occupies; `grid.occludeGraphics`, called from `putCell`, `eraseSpan` (so EL,
ED and ECH alike) and the ED 2/3 flat fill, is what makes that work.

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
