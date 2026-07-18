# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Roadmap

Post-MVP feature plan with progress checkboxes lives in `ROADMAP.md`
at the repo root. Tick boxes there as work lands.

## What this is

`go-term` is a full-featured terminal-emulator widget built on the `go-gui`
framework (sibling repo `../go-gui`). It spawns a real shell via PTY and
renders the cell grid through `gui.DrawCanvas` (GPU-accelerated). Targets
macOS, Linux, and Windows (ConPTY).

## Common commands

```bash
# Run the demo window
cd examples/loon && go run .

# Run the full test suite
go test ./...

# Run the replay-style emulator checks only
go test ./term -run EmulatorReplay

# Build everything
go build ./...

# Vet
go vet ./...

# Tidy module graph
go mod tidy
```

### Debugging rendering bugs

Set `GOTERM_CAPTURE=<path-prefix>` before launching to tee each pty's raw
output to `<prefix>-<seq>.bin` (one file per Term). Replay with `cat` in a
reference terminal (kitty, Terminal.app) to assign blame — same corruption
there means the child app is at fault — or feed the bytes to
`CaptureFixture` / `script2fixture` for the EmulatorReplay harness.

There are automated tests for the grid, parser, PTY, widget helpers,
and replay-style emulator behavior. The widget itself is still partly
GUI-bound, so keep validating visually by running `examples/loon` and trying
`ls`, `cat`, ANSI color output, window resize, selection/copy, and
full-screen apps such as `vim` or `less`.

## Local dev with sibling dependencies

`go.mod` references published versions of `go-gui` and `go-glyph`.
For local development against in-progress sibling changes, copy
`go.work.example` to `go.work` at the repo root. The workspace file
wires sibling working trees at `../go-gui` and `../go-glyph` into the
module graph so `go build` picks up uncommitted edits immediately.

Both sibling repos must be present at those paths. Remove or unset
`GOWORK` to switch back to the published versions.

## Architecture

Three layers; dependencies flow strictly downward. Each layer is split
across multiple files by concern — the layering invariant is what matters,
not the file count.

```
examples/loon/main.go         gui.NewWindow + term.New + backend.Run
        │
        ▼
term/widget.go           Term struct, New, View, Close; reader goroutine.
term/widget_draw.go      OnDraw: bg/fg/graphics/cursor render passes.
term/widget_keyboard.go  onChar, onKeyDown, onKeyUp; KKP encoding.
term/widget_mouse.go     Mouse button/motion/wheel; SGR encoding.
term/widget_clipboard.go Cmd+C/V; opt-in OSC 52 clipboard write.
term/widget_scroll.go    Scrollbar, momentum scroll, ViewSubPx math.
term/widget_draw_graphics.go Graphics render pass (sixel/kitty/iTerm2).
        │
        ▼
term/parser.go           VT state machine entry point. Bytes → grid mutations.
term/parser_csi.go       CSI dispatch (SGR, cursor, erase, modes, DECSCUSR, KKP…)
term/parser_osc.go       OSC dispatch (0/1/2/7/8/9/10/11/12/52/133/777/1337)
term/parser_dcs.go       DCS dispatch (DECRQSS, XTGETTCAP, sixel, sync)
term/parser_apc.go       APC dispatch (Kitty Graphics Protocol)
        │
        ▼
term/grid.go             Cell buffer + cursor state + alt-screen. Pure data.
term/grid_cursor.go      Cursor move, save/restore, DECSCUSR.
term/grid_edit.go        putCell write path; Put + streaming grapheme assembly.
term/grid_mark.go        OSC 133 semantic shell marks.
term/grid_reflow.go      Logical line reflow on resize.
term/grid_scroll.go      Scroll regions; pixel-accurate ViewSubPx math.
term/grid_search.go      Literal and RE2 regex search.
term/grid_selection.go   Content-relative text selection.
term/scrollback.go       Scrollback ring buffer.
term/bidi.go             Unicode Bidirectional Algorithm (UAX#9) for RTL text.
term/graphics.go         Graphic type; sixel decoder; PNG data-URL encoder.

term/pty.go              ptyIO interface + shared helpers (clampWinsize…).
term/pty_unix.go         creack/pty wrapper (!windows). Spawns $SHELL, resize ioctl.
term/pty_windows.go      ConPTY wrapper (windows). Spawns ComSpec, ResizePseudoConsole.
term/palette.go          256-color ANSI table (16 + 6×6×6 cube + 24 grayscale) +
                         RGB resolution helpers.
```

### Concurrency model

- One PTY reader goroutine, started in `term.New`.
- `Grid.Mu` is the single lock. The reader goroutine takes it to feed
  the parser; `OnDraw` takes it to read cells. Never hold it across a
  go-gui call.
- After feeding bytes, the reader calls `win.QueueCommand(...)` to
  schedule a redraw on the main thread. **Never touch `*gui.Window`
  state directly from the reader goroutine** — `QueueCommand` is the
  only thread-safe path.

### Render loop

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

### Parser scope

Supports a modern xterm/kitty-compatible subset:

- C0: `BEL`, `BS`, `HT`, `LF`, `CR`, `ESC`.
- SGR (`CSI … m`): reset; bold/dim/italic/underline/inverse/strikethrough;
  extended underlines (4:1–4:5, SGR 21); underline color (58); fg/bg
  16-color, 256-color, 24-bit truecolor.
- CSI: cursor movement and positioning, erase in line/display, scroll
  regions (DECSTBM), IND/RI/NEL, IL/DL/ICH/DCH/SU/SD, DECSCUSR (cursor
  shape/blink), DA1 (advertises Sixel via extension 4: `CSI ?1;2;4c`),
  DA2, XTVERSION (`CSI > q` → `DCS >| go-term(ver) ST`), XTWINOPS pixel
  geometry (`CSI 14 t`/`CSI 16 t` → text-area / cell size in pixels;
  manipulation ops ignored), tab stop clear (TBC).
- Modes: alt screen (1049/1047/47), mouse (1000/1002/1003/1006/1016),
  bracketed paste (2004), focus reporting (1004), synchronized updates
  (2026 — DECSET begins a block, DECRST ends + flushes; a 500 ms watchdog
  in the widget force-ends a block whose end never arrives),
  grapheme clustering (2027 — always on; DECRQM reports it
  permanently set, DECSET/DECRST are no-ops).
- Kitty Keyboard Protocol: `CSI > u` / `< u` / `= u` / `? u` (push/pop/
  set/query); key-release events; left/right modifier distinction.
- DEC Special Graphics: `SI`/`SO`, `ESC (0` / `ESC (B`.
- OSC: window title (0/1/2), CWD (7), hyperlinks (8), desktop
  notifications (9/777), dynamic colors (10/11/12), clipboard (52),
  semantic shell marks (133), iTerm2 inline images (1337).
- DCS: DECRQSS, XTGETTCAP (incl. `Smulx`/`Setulc` to advertise styled +
  colored underlines), sixel graphics, synchronized updates.
- APC: Kitty Graphics Protocol (transmit/display/place/delete; PNG, raw
  RGBA/RGB; chunked base64).

When extending: add cases in the appropriate `parser_*.go` file.
Don't let parser code reach into go-gui — it must stay grid-only.

### Grapheme clusters

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

### Keyboard input

`onChar` (printable runes via `gui.ContainerCfg.OnChar`) writes UTF-8
to the PTY. `onKeyDown` translates non-printable keys (arrows, Enter,
Backspace (DEL), Delete, Page Up/Down, Home/End, Ctrl+letter, F1–F12,
numeric keypad) into terminal byte sequences. Alt+key prefixes with ESC.
Set `e.IsHandled = true` so go-gui doesn't propagate.

When `KittyKeyFlags != 0` the widget emits KKP sequences (`CSI codepoint
; modifiers u`) instead of legacy bytes for Backspace, Enter, Tab, Escape,
Ctrl+letters, and functional keys. `onKeyUp` emits release events when
flag bit 2 is set.

The widget claims focus via `IDFocus` set to a unique per-Term `focusID`
on its outer `gui.Column`. In multi-Term windows the pane manager calls
`SetFocused` to route `IDFocus` to the active Term.
If keystrokes don't reach the PTY, focus is the first place to look.

## Conventions

- Comments wrap at ~90 columns.
- Public API in `term/` is small on purpose: `Cfg`, `Term`, `NamedTheme`,
  `Theme`, `New`, `View`, `Close`, `Cwd`, `SetTheme`, `Rows`, `Cols`,
  `Write`, `PID`, `Alive`, `SetFocused`, `HandleWindowEvent`, plus
  `Shortcuts`/`ShortcutInfo` (display metadata for help overlays). Keep it
  that way; add unexported helpers freely.
- Performance target: reduce heap allocations. The OnDraw hot path
  must not allocate per cell — keep `string(rune)` conversions and
  slice growth out of the inner loop if perf work begins.
- `Grid.Mu` is the single lock — don't add per-feature mutexes.
- `Term.queueCommand` (which wraps `cmd.QueueCommand` with a closed-Term
  guard) is the only thread-safe path from reader goroutine to gui state.
  Title updates, clipboard writes, and notifications triggered by the
  parser must go through it. Never call `cmd.QueueCommand` directly.
- `dispatchCSI`, `dispatchOSC`, `dispatchDCS`, `dispatchAPC` are the
  single dispatch sites for their respective sequences — extend, don't
  add parallel dispatchers.
- When rendering visual dividers (horizontal or vertical lines in UI
  overlays), use `gui.Rectangle` with `FillFixed` (horizontal: fill width,
  fixed height) or `FixedFill` (vertical: fixed width, fill height).
  `Rectangle` has no padding, no axis, and no child layout — unlike
  `gui.Column`/`gui.Row` wrappers, it won't pick up theme container
  padding that indents the edges.
