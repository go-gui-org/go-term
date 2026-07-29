# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `term`: OSC 133 shell integration now uses the exit status shells report in
  `OSC 133;D;<exit>` (#103). `term.jump-failure` (`Cmd+Shift+E`) scrolls to the
  most recent command that exited non-zero — repeated presses walk back through
  older failures and wrap — and each failure gets a red tick in the scrollbar
  track so one buried in a long build log is findable by eye.
  `term.select-output` (`Cmd+Shift+O`) selects exactly the output region of the
  command under the cursor and enters copy mode with that selection live, ready
  for `y` or `Cmd+C`; on a fresh prompt it selects the previous command's
  output. Both are no-ops on the alt screen, and a command whose shell reported
  no exit status never counts as a failure. See `docs/config.md`.

- User config file covering fonts, theme, terminal settings and Term-level
  keybindings (#94). The existing INI at `~/.config/go-term/config` gains
  `[font]` (`family`, `size`) and `[general]` (`theme`, `scrollback`, `bell`,
  `scrollbar`) sections, and `[keybindings]` entries are now namespaced
  `term.<action>` or `workspace.<command>` — a bare key still means
  `workspace.`, so existing files keep working. `none` unbinds an action so the
  key reaches the child process. Collisions are detected across both
  namespaces, because go-gui's global commands outrank the widget's key
  handling and would otherwise shadow a `term.*` binding silently.
  `Cmd+Shift+,` (`Workspace.ReloadConfig`) re-reads the file and applies it to
  every live pane; a setting removed from the file reverts to the embedder's
  default. Parse errors are logged per line and never wedge the app.
  See `docs/config.md`.
- `term`: live setters for settings that `Cfg` previously fixed at
  construction — `SetTextStyle`, `SetScrollbackRows`, `SetBellMode`,
  `SetScrollbarWidth` — plus `ParseAction` for resolving an action name from a
  config file. `SetTextStyle` clears the runtime font zoom (an absolute size
  that would otherwise outrank the new configured one); `SetScrollbackRows`
  trims stored history immediately when shrinking rather than waiting for
  eviction.

- OSC 1337 `File=` downloads and real argument parsing (#75). The iTerm2
  sequence now carries file transfers (`inline=0`, as sent by `imgcat -d` and
  `it2dl`) in addition to inline images. Transfers are opt-in: embedders set
  `Cfg.OnDownload` to handle the bytes themselves, or `Cfg.DownloadDir` to use
  the built-in writer, which saves with `0600` permissions, suffixes name
  collisions (`report (1).pdf`) instead of overwriting, and reports the saved
  path through the existing notification path. Both unset — the default —
  leaves downloads disabled, so untrusted terminal output cannot create files.
  `falcon` opts in with `~/Downloads`; `workspace.Cfg.DownloadDir` passes the
  choice through.

  The `File=` key list is now parsed rather than substring-matched, so
  `width` and `height` (`N` cells, `Npx`, `N%`, `auto`) and
  `preserveAspectRatio` finally affect inline image size — `imgcat -W 40`
  renders at 40 columns instead of the image's natural size. `name` is
  base64-decoded and sanitized to a bare filename; path separators, traversal
  names, and control bytes cannot escape the download directory.

- Session recording and replay (#74). `falcon --record <file.gtr>` records the
  starting pane, `Cmd+Shift+R` toggles recording on the focused pane (marked by
  a `● REC m:ss` pill), and embedders get `Cfg.RecordPath`,
  `Term.StartRecording`, `StopRecording`, and `Recording`. Recordings capture
  pty output with timing plus grid resizes; keystrokes only when
  `Cfg.RecordInput` is set.

  `falcon --replay <file.gtr>` plays one back through a real `Term` — the
  parser, renderer, scrollback, selection, and search are the production ones,
  so a recording reproduces a rendering bug rather than describing it. Space
  pauses, `+`/`-` change speed, `.` steps a frame, `0` restarts.

  The `.gtr` container (`internal/recfmt`) is a JSON header line followed by
  `kind + delta-µs + length + raw bytes` frames. Payloads are never
  transcoded, so malformed UTF-8 — the byte sequences most worth reporting —
  survives the round trip; asciicast v2, whose events are JSON strings,
  cannot carry it and is therefore an export target rather than the storage
  format. Recording costs no allocations per frame and one write syscall.

  New `gotermrec` tool: `info`, `cat` (raw bytes — the `GOTERM_CAPTURE`
  workflow), `play` (timed playback in any terminal, no GUI), `fixture` (a
  replay fixture via `CaptureFixture`), and `export -cast` (asciicast v2, with
  a per-frame warning wherever bytes had to be replaced).

- DECSCA character protection and the VT420 rectangular area operations
  (#71): DECSCA (`CSI Ps " q`), the selective erases DECSEL (`CSI ? Ps K`),
  DECSED (`CSI ? Ps J`) and DECSERA (`CSI … $ {`) that honor it, plus DECERA
  (`$ z`), DECFRA (`$ x`), DECCARA (`$ r`), DECRARA (`$ t`), DECCRA (`$ v`)
  and the DECSACE extent selector (`CSI Ps * x`). DECRQSS answers `"q` and
  `*x`. Protection follows the DEC rule: only the selective erases skip a
  protected cell — ED/EL/ECH, scrolling and ordinary writes do not. DA1 still
  reports VT100 level, so applications that gate rectangle support on a
  VT420 device attributes reply will not use these.

- ECH (`CSI Ps X`, erase characters) — previously parsed and dropped. TUIs
  that paint split-pane layouts use it to clear a bounded span without
  `EL` wiping the panes sharing the row.
- CHT (`CSI Ps I`) and CBT (`CSI Ps Z`) tab navigation.
- `COLORTERM=truecolor` in the child environment (Unix and Windows). The
  widget renders 24-bit color, but `TERM=xterm-256color` alone only promises
  the palette, so TUI toolkits were quantizing truecolor output.

### Changed

- `term`: the configured font size (`Cfg.TextStyle.Size`, `SetTextStyle`) is
  now clamped to the same 4–72 pt bounds the zoom path already enforced, so no
  caller has to re-derive the limits.

- The OSC 1337 payload cap rose from 4 MiB to 32 MiB of base64 (~24 MiB of
  file data) to leave room for real downloads (#75). A payload that exceeds it
  is now dropped outright instead of silently truncated, and the enlarged
  accumulator is released after each sequence rather than pinned for the
  parser's lifetime.

### Fixed

- `term`: OSC 133 marks, the selection, and graphics origins no longer drift
  off their text when a window resize re-wraps content (#103). All three were
  shifted by the flat scrollback-depth delta, which is wrong as soon as a
  logical line re-wraps into a different number of physical rows — everything
  below it moved by the accumulated difference. They now re-map through the
  re-wrap itself, reusing the same logical-line mapping the cursor already
  used. Visible as prompt jumping landing on the wrong row after resizing a
  window with wrapped output in scrollback.

- Windows: the tail of a session's output is no longer truncated when the
  shell exits. ConPTY's output pipe is fed by conhost from its own process,
  asynchronously, so bytes the child had already produced could still be in
  flight when its process object signalled — and the child-exit path closed
  the read end at exactly that moment, discarding them. The last command's
  output vanished roughly half the time. The console and input pipe still go
  down on child exit, but the output read end now stays open so the reader
  drains to a natural EOF, with a bounded grace period as a backstop.
- IL (`CSI Ps L`) and DL (`CSI Ps M`) no longer move the cursor to column 0.
  xterm, wezterm and tmux all preserve the column; homing it made every
  following write on the row land at the wrong offset. Together with the
  missing ECH and CBT above, this left stale text across the screen in
  cell-diffing TUIs (charmbracelet/crush).
- A synchronized-update frame that completed mid-chunk is now painted
  immediately when the application has already opened the next block but has
  not written into it — the common `ESU BSU` boundary a pty read lands on.
  Previously that frame waited for the next read or the 500 ms watchdog.
  A block that has started writing still suppresses the repaint, so no
  half-drawn frame is shown.

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
- Renamed `examples/demo` to `examples/falcon` (#16); consolidated the
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
- `examples/falcon` example window.
