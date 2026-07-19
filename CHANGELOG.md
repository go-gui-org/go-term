# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.6.0] - 2026-07-19

### Added

- Expand XTGETTCAP capability table to the full xterm-256color subset,
  improving capability queries for `tput`, `vim`, and other terminal
  programs (#53).

### Changed

- Hyperlink hover recolor and pointing-hand cursor are now gated on Cmd
  being held, matching the activation model (#54).
- Reuse SetGeom backing store and arena-carve reflow rows to reduce
  allocations during resize and scrolling (#55, #56) (#57).
- Bump go-glyph to v1.17.3 and go-gui to v0.40.0 (#58).

## [0.5.0] - 2026-07-17

### Added

- Mode 2026 synchronized-update watchdog with a 500 ms timeout that
  force-ends a block whose end never arrives (#50).
- Dedicated PTY resize goroutine (`resizeLoop`) for responsive resize
  that doesn't stall the reader (#49).
- `GOTERM_CAPTURE` debug tee that records each PTY's raw output to
  `<prefix>-<seq>.bin` for offline replay and debugging (#49).
- `lockMouse`/`unlockMouse` helpers on `Term` (#42).
- Multi-tick SGR mouse wheel reports with `ScrollPrecise`-based
  wheel-vs-trackpad detection (#37).
- **Windows support** via ConPTY backend (#19): `ptyIO` interface with
  split Unix/Windows PTY implementations (#17), platform-aware shortcut
  modifiers (#20), and toast notifications (#23).
- `ExitWhenLastShellExits` workspace option (#14).
- `Cmd+=`/`Cmd+-` keyboard shortcuts to adjust font size by 0.25 pt (#13).
- Tab reordering via `Cmd+Alt+[` / `Cmd+Alt+]` (#12).

### Fixed

- Cancel drag on window resize; guard the help-dialog backdrop from edge
  clicks (#46).
- Mouse-selection off-by-one when the canvas is vertically offset by a
  tab bar (#34).
- `posToCell` row mapping when smooth-scrolled (ViewSubPx) (#29).
- Clear scrollback on CSI 3 J (#30).
- Mouse reporting-drag coordinate offset when canvas is offset (#42).
- Fall back to `$HOME` when the saved CWD directory no longer exists.
- Brahmic akshara cell width: virama fusion, Mc marks, and dangling
  virama are now sized correctly.
- Benchmark regression gate: `ns/op` is advisory-only; the hard gate
  checks only `allocs/B-op` (#7).

### Changed

- Help dialog: headings and key labels use the default text color;
  sections separated by thin dividers (#45).
- Inactive tab title text is dimmed to distinguish active from inactive
  tabs (#35).
- Scrollbar now has hover brightness, click/drag, and an edge inset
  (#33); the thumb is clamped to a minimum pixel height (#31).
- Mouse-wheel scroll sensitivity reduced from 15 to 5 rows (#32).
- Scroll momentum decay shortened (#36).
- Selection boundaries use half-open intervals (#30).
- Renamed `examples/demo` to `examples/loon` (#16); consolidated the
  font-family constant (#26).
- Compressed ROADMAP from 606 to 135 lines.

## [0.4.0] - 2026-06-28

### Added

- Fuzz testing for parser input on PRs that touch parser files.
- Benchmark regression gates with a zero-allocation hard gate for the
  foreground-pass hot path.
- Conformance smoke tests for vttest-parity VT/xterm edge cases.
- Whole-app replay fixtures covering tmux, paste, graphics, BiDi, and mouse.
- `script2fixture` tool for capturing replay fixtures from `script(1)`
  typescripts.
- Emoji fill their reserved cell box at any DPI via go-glyph's `EmojiBoxWidth`
  hint (requires go-gui v0.29.0 / go-glyph v1.12.0).

### Fixed

- Grapheme clusters split across a PTY read boundary (e.g. a ZWJ emoji at the
  4096-byte edge) are no longer committed as broken pieces; the trailing,
  still-growing cluster is carried to the next read and flushed when the input
  burst drains.

## [0.2.0-rc.1] - 2026-05-30

### Added

- **256-color and 24-bit truecolor** (`CSI 38;2;r;g;b m` / `CSI 38;5;n m`).
- **Scrollback ring buffer** with mouse wheel, PgUp/PgDn, and pixel-perfect
  sub-row scrolling with two-phase momentum deceleration.
- **Text selection** with content-relative coordinates that survive scroll and
  resize; clipboard copy/paste (`Cmd+C`/`Cmd+V`); OSC 52 clipboard write
  (opt-in).
- **Alt screen** (DECSET 47 / 1047 / 1049); scrollback suppressed while
  active.
- **Scroll regions** (DECSTBM), IL/DL/ICH/DCH, and IND/RI.
- **OSC protocol**: window title (0/1/2), CWD (7), hyperlinks (8; Cmd+click
  opens browser), desktop notifications (9/777), dynamic colors (10/11/12),
  clipboard (52), semantic shell marks (133), iTerm2 inline images (1337).
- **Mouse reporting**: X10 (`?1000`), button-event (`?1002`), any-motion
  (`?1003`), SGR encoding (`?1006`), SGR-Pixels mode (`?1016`).
- **Cursor styles** (DECSCUSR): block, underline, bar; steady or blinking;
  `Cfg.CursorBlink` override.
- **Extended SGR**: italic, dim, strikethrough, extended underlines
  (double/curly/dotted/dashed with per-cell color via `CSI 58`).
- **East Asian Wide characters** and ZWJ combining marks via `uniseg`.
- **Logical line reflow** on window resize.
- **Kitty Keyboard Protocol** (`CSI u`) with key-release events and
  left/right modifier distinction.
- **Search in scrollback**: `Cmd+F` literal, `Ctrl+R` regex; match
  highlighting; `Enter`/`Shift+Enter` cycling.
- **Color themes**: `Theme` struct with 16 ANSI colors + default fg/bg;
  runtime switching; bundled Gruvbox, Nord, and Solarized Dark.
- **Sixel graphics**: 256-register color, RLE, up to 4096×4096 px.
- **Kitty Graphics Protocol**: chunked base64 transmission; PNG / raw RGBA /
  raw RGB; off-screen store.
- **Bidirectional text** (Unicode UAX#9) for RTL languages.
- **Scrollbar indicator** with auto-hide and fade.
- **Visual bell** flash on `BEL`.
- **Dirty-row tracking** and tessellation cache for low-CPU idle frames.
- **Semantic shell marks** (OSC 133): `Cmd+Up/Down` jumps between command
  boundaries.
- **Synchronized Updates** (DCS `?2026`).
- **DECRQSS** and **XTGETTCAP** reply dispatch.
- Test suite covering grid, parser, palette, widget, and PTY helpers.
- `MaxGridDim` and `MaxScrollbackCap` constants; dimension and param bounds
  against pathological input.

### Fixed

- Cursor disappearing at the right margin when `CursorC == Cols`.
- `EraseInLine` and `EraseInDisplay` now propagate current attributes.
- `Tab` no longer divides a negative `CursorC`.
- Truecolor and 256-color SGR parsing bounds-checked to parameter count.
- PTY writes log errors instead of silently dropping them.

### Changed

- `encodeRune` replaced by standard library `utf8.EncodeRune`.
- `Grid.Resize` and `NewGrid` clamp inputs through `clampDim`.

## [0.1.0] - 2026-05-01

### Added

- Initial public release.
- `term.Term` widget bound to a single PTY-backed shell.
- VT parser supporting C0 control bytes, CSI cursor moves,
  erase-in-line, erase-in-display, and SGR for the ANSI 16-color
  palette plus bold / underline / inverse.
- 16-color palette (VS Code Dark+ approximation) with default fg/bg.
- `examples/loon` example window.
