# Terminal Verification

This project verifies terminal-emulator behavior in three layers:

1. Pure unit tests for grid, parser, PTY, and widget helpers.
2. Replay tests that feed realistic escape streams and assert the final screen
   state.
3. Manual compatibility checks in `examples/falcon` for GUI-only behavior.

## Automated Suites

Run the full automated suite:

```bash
go test ./...
go test -race ./...
go vet ./...
```

Run only the replay-style emulator checks:

```bash
go test ./term -run EmulatorReplay
```

Run only the conformance smoke tests (vttest-parity checks):

```bash
go test ./term -run TestConformance
```

## Capability Matrix

| Capability                                                            | Verification                                                      |
| --------------------------------------------------------------------- | ----------------------------------------------------------------- |
| Plain text, CR/LF/BS/TAB, UTF-8 decode                                | `parser_test.go`, `grid_test.go`                                  |
| Cursor movement and erase operations                                  | `parser_test.go`, `emulator_replay_test.go`                       |
| SGR attributes, 16-color, 256-color, truecolor                        | `parser_test.go`, `palette_test.go`                               |
| Scroll regions, insert/delete line/char, IND/RI/NEL                   | `grid_test.go`, `parser_test.go`                                  |
| Alt screen save/restore                                               | `grid_test.go`, `parser_test.go`, `emulator_replay_test.go`       |
| OSC title and OSC 7 working-directory updates                         | `parser_test.go`, `emulator_replay_test.go`                       |
| Device replies (`DA1`, `DA2`, `DECRQSS`, `DECRQM`, `XTGETTCAP`)       | `parser_test.go`, `parser_csi_test.go`, `emulator_replay_test.go` |
| XTWINOPS geometry/state reports (`CSI 11/13/14/15/16/18/19/20/21t`)   | `parser_csi_test.go`, `emulator_replay_test.go`                   |
| Color-scheme notification (mode 2031, `CSI ? 996/997 n`)              | `parser_csi_test.go`, `widget_test.go`                            |
| ConEmu progress (`OSC 9 ; 4 ; …`, scrollbar fill)                     | `parser_osc_test.go`, `widget_progress_draw_test.go`              |
| Bracketed paste, focus reporting, mouse modes, sync output            | `parser_test.go`, `widget_test.go`, `emulator_replay_test.go`     |
| Mouse encodings: X10, urxvt (`?1015`), SGR (`?1006`/`?1016`)          | `widget_mouse_legacy_test.go`, `widget_mouse_test.go`             |
| Grapheme clusters, wide chars, emoji, VS15/16, ZWJ, flags (Mode 2027) | `grapheme_test.go`, `grid_test.go`                                |
| Bidirectional text (UAX#9)                                            | `bidi_test.go`                                                    |
| DECSCA protection, selective erase, rectangular area ops              | `grid_rect_test.go`, `parser_csi_test.go`, `conformance_test.go`  |
| Graphics: Sixel, Kitty (APC), iTerm2 (OSC 1337)                       | `graphics_test.go`, `parser_apc_test.go`, `parser_iterm2_test.go` |
| OSC 1337 `File=` transfers (download), name sanitization              | `parser_iterm2_test.go`, `widget_download_test.go`                |
| Kitty Keyboard Protocol, function/keypad keys                         | `widget_keyboard_test.go`, `parser_csi_test.go`                   |
| Semantic shell marks (OSC 133), search                                | `grid_mark_test.go`, `grid_search_test.go`                        |
| PTY startup and resize plumbing                                       | `pty_test.go`                                                     |
| GUI-only selection, scrolling, clipboard, redraw behavior             | `widget_test.go` plus manual demo runs                            |
| Workspace splits, tabs, persistence, keybinding config                | `term/workspace/*_test.go`                                        |
| Conformance smoke tests (vttest-parity)                               | `conformance_test.go`                                             |
| Fuzzed parser input                                                   | `parser_fuzz_test.go`                                             |

## Manual Checks

Start the demo:

```bash
cd examples/falcon
go run .
```

Exercise these behaviors in the embedded shell:

```bash
printf 'plain\ntext\n'
printf '\x1b[31mred\x1b[0m \x1b[38;5;82mgreen256\x1b[0m \x1b[38;2;255;100;0mtruecolor\x1b[0m\n'
printf 'line1\nline2\nline3\nline4\nline5\n'
vim README.md
less README.md
```

Validate:

| Behavior       | What to check                                                               |
| -------------- | --------------------------------------------------------------------------- |
| Resize         | `stty size` changes after window resize                                     |
| Scrollback     | mouse wheel and PgUp/PgDn move through history                              |
| Selection/copy | drag-select copies trimmed text                                             |
| Paste          | multi-line paste does not auto-execute in bracketed paste mode              |
| Alt screen     | `vim` and `less` restore the main buffer on exit                            |
| Mouse/focus    | mouse-aware apps and focus events do not leak garbage text (not Windows)    |
| Splits/tabs    | Cmd+D / Cmd+Shift+D split, Cmd+T new tab, Cmd+/ overlay                     |
| Persistence    | quit with `--save-workspace`, relaunch with `--workspace`, layout restores  |
| Graphics       | `img2sixel` / `kitten icat` / `imgcat` render inline images                 |
| Copy mode      | `Cmd+Shift+Space`, vim motions, `y` yanks, output frozen while active       |
| Hints          | `Cmd+Shift+U` labels links, key opens; `Cmd+Shift+Y` copies                 |
| Palette/theme  | `Cmd+Shift+P` lists workspace + term actions; `Cmd+Shift+T` previews themes |
| Broadcast      | `Cmd+Shift+I` mirrors typing to every pane in the tab, badge shows          |
| Recording      | `Cmd+Shift+R` records the focused pane, `● REC` pill shows                  |

## Known Omissions

Things go-term deliberately does not implement, recorded here so the gap is a
decision rather than an oversight.

### `modifyOtherKeys` (xterm)

Not implemented. `CSI > 4 ; Pm m` (set), `CSI > 4 m` (reset) and `CSI ? 4 m`
(XTQMODKEYS, query) are parsed as unknown private sequences and discarded. They
are inert, not misread: they share the final byte `m` with SGR, but the `>` /
`?` private marker routes them away from SGR dispatch, so they cannot leak into
text attributes. `TestParser_ModifyOtherKeys_Inert` pins that. The query is
answered with silence — a reply would tell the client the mode was understood.

**Why.** The Kitty Keyboard Protocol is implemented (`CSI > u` push, `CSI < u`
pop, `CSI = u` set, `CSI ? u` query) and supersedes it. KKP disambiguates
strictly more: key release events, left/right modifier distinction, and the
`Ctrl+I` vs `Tab` / `Ctrl+M` vs `Enter` collisions that `modifyOtherKeys` level
2 exists to resolve. Supporting both would mean two encoders for the same
keystrokes and a precedence rule between them.

**What this costs.** An application that probes _only_ `modifyOtherKeys` and
never tries KKP falls back to legacy encoding, so chords that legacy encoding
cannot express — `Ctrl+Shift+<letter>`, most `Ctrl+<digit>` and
`Ctrl+<punctuation>` combinations — arrive as their unmodified or
control-collapsed byte, or not at all. In practice this is a narrow set: clients
that support `modifyOtherKeys` and not KKP. Both are queryable, and an app that
queries KKP first gets full fidelity.

**If you hit this**, the fix is a level 1/2 encoder in `term/widget_keyboard.go`
gated on state set from `parser_csi.go`'s `'>'` branch, plus a precedence rule
making KKP win when both are enabled. File an issue with the application name —
a real client that needs it is the evidence that would change this decision.

### Terminal reports on Windows (ConPTY)

Not a decision so much as a platform constraint, recorded here because it
silently invalidates a whole class of test.

On Windows the child runs under ConPTY, and conhost is itself a terminal
emulator: it maintains its own text buffer and answers DSR/CPR, Device
Attributes and DECRQM from that buffer rather than forwarding the query
upstream. `_CursorPositionReport` in conhost's `adaptDispatch.cpp` reads its own
cursor and, in its words, sends the reply "back into the input channel of the
console." The sequence never reaches our parser, so the CPR handler in
`parser_csi.go` and everything behind it — `settledCol`, grapheme widths — is
unreachable on Windows. What a client reads back describes conhost.

The practical consequence is that **width-probing tools measure conhost, not
go-term**. Anything that brackets a glyph with cursor position reports and diffs
the columns — `ucs-detect` most notably — reports conhost's capabilities.
`ucs-detect` prints U+231A, requires a delta of exactly 2, and otherwise
declares that the terminal does not support wide characters. A conhost whose
width tables predate the emoji in question fails that gate no matter how
correctly the grid renders it. This affects every Windows host terminal equally,
Windows Terminal included; it is not specific to go-term.

`startPTY` mitigates what it can by passing the grapheme glyph-width flag
(`0x08`) to `CreatePseudoConsole`, which conhost turns into
`--textMeasurement graphemes` and which aligns its buffer with the grid's
cluster model. That option arrived around Win11 24H2 / Windows Terminal 1.22.
Older builds mask the bit off and ignore it, so downlevel systems — Windows 10,
whose inbox conhost is still 10.0.19041 — keep the mismatch with no lever
available. Ambiguous width is deliberately left at conhost's default to match
the grid's narrow interpretation.

**Verify go-term is not the cause** before chasing a width bug on Windows: run
the same probe under Windows Terminal on the same machine. An identical result
there is conhost, not this code. To confirm the mechanism directly, check that
no `CSI 6 n` ever appears in a `GOTERM_CAPTURE` tee of the child's output.

### Mouse reporting on Windows (ConPTY)

The same mechanism, in the other direction, and with a harder consequence:
**mouse reporting does not work on older Windows builds.**

Conhost consumes the child's mouse DECSETs instead of forwarding them — it
tracks the modes for its own input translation and passes nothing upstream. The
grid therefore never learns that the child wants mouse, `mouseSnap.shouldReport`
stays false, and the wheel drives local scrollback while clicks drive local
selection. Nothing in the encoders is involved, and no change to them can help.

Measured on 10.0.19045.6466, with a child that sets each mode and a terminal
that logs what arrives:

| mode the child sets | system `conpty.dll` | Windows Terminal's `conpty.dll` |
| ------------------- | ------------------- | ------------------------------- |
| `?1000h`            | swallowed           | forwarded                       |
| `?1003h`            | swallowed           | forwarded                       |
| `?1006h`            | swallowed           | forwarded                       |
| `?1015h`            | swallowed           | swallowed                       |

wezterm ships that second `conpty.dll` (with `OpenConsole.exe`) in its install
directory and loads it in preference to the system one, which is the whole
reason mouse works there on a build where it does not work here. Bundling a
newer ConPTY was considered and rejected: two redistributed Microsoft binaries
in every release, plus a load-order fallback, is not worth the one feature.
`?1015` is dropped even by the newer build, so a Windows child can never select
urxvt encoding regardless.

**Verify mouse on Linux or macOS**, where the child speaks VT directly, or on a
Windows build whose own ConPTY forwards the modes. Before filing a mouse bug on
Windows, confirm the modes actually arrive: a `GOTERM_CAPTURE` tee of the
child's output containing no `CSI ? 1000 h` means the platform ate it.

## External Conformance Tools

This repo does not bundle a full external terminal conformance suite. For
broader compatibility work, use:

- `vttest` for classic VT/xterm behavior
- `tic`, `infocmp`, and `tput` for terminfo validation
- real application checks with `vim`, `less`, `tmux`, `htop`, and shell line
  editing

Treat those as complementary to the Go conformance tests in
`conformance_test.go`. The conformance tests automate the most common vttest
checks as replay-style assertions that run in CI on every push.
