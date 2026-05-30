# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/).

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
- `examples/demo` example window.
