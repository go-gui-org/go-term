# go-term

A full-featured, embeddable terminal-emulator widget for the
[`go-gui`](https://github.com/go-gui-org/go-gui) framework. Spawns a real
shell over a PTY, renders through a GPU-accelerated `gui.DrawCanvas`, and
covers the protocol surface expected by modern CLI tools and TUI frameworks.

Targets macOS and Linux.

## Stability

`go-term` is pre-1.0. The public API is deliberately small so embedders have a
narrow, well-defined contract to code against. This section describes what you
can rely on and what may still shift.

### Public API (stable boundary)

| Symbol | Kind | Guarantee |
|---|---|---|
| `Cfg` | struct | Fields are additive. Renames and removals will go through a deprecation cycle (at least one minor version with the old name still accepted). |
| `NamedTheme` | struct | Stable. |
| `Theme` | struct | Stable. |
| `Term` | struct | Opaque handle. All fields are unexported; embedders interact only through methods. |
| `New(w, cfg)` | constructor | Signature stable. |
| `Term.View(w)` | method | Signature stable. The returned `gui.View` tree is an implementation detail. |
| `Term.Close()` | method | Signature stable. Idempotent — safe to call multiple times. |
| `Term.Cwd()` | method | Signature stable. |
| `Term.SetTheme(th)` / `Term.Theme()` | method | Signature stable. |
| `Term.Rows()` / `Term.Cols()` | method | Signature stable. Current grid dimensions. |
| `Term.Write(p)` | method | Signature stable. Inject bytes as if typed. |
| `Term.PID()` / `Term.Alive()` | method | Signature stable. Child process status. |
| `Term.SetFocused(v)` / `Term.HandleWindowEvent(e)` | method | Signature stable. Multi-`Term` embedding (a pane manager routes focus + events). |
| `Shortcuts()` / `ShortcutInfo` | func / struct | Signature stable. Display metadata for help overlays. |
| `MaxGridDim` | constant | Stable. Grid rows/cols are clamped to this value. |
| `MaxScrollbackCap` | constant | Stable. Scrollback rows are capped at this value. |

The pre-built `Theme` variables (`DefaultTheme`, `GruvboxTheme`, `NordTheme`,
`SolarizedDarkTheme`) are stable — their names won't change and their color
values won't shift in ways that break contrast.

### What may change before 1.0

- **`Cfg` fields** — new fields may be added; existing fields will not be
  removed without a deprecation cycle. Defaults for new fields are always
  zero-value-safe (backwards compatible).
- **`Term` method additions** — new methods may appear. Existing method
  signatures are stable.
- **`View` tree internals** — the `gui.View` tree returned by `Term.View`
  may gain new widgets (e.g., tab bar, pane splitter) but the embedder
  only calls `View` and passes the result to `UpdateView`; that contract
  holds.
- **Internal package layout** — new files may appear in `term/` (e.g., a
  pane splitter). Embedders should import only `github.com/go-gui-org/go-term/term`
  and not reach into individual source files.
- **Go version requirement** — the `go` directive in `go.mod` reflects the
  oldest Go release the author tests against. It may advance on minor
  version bumps.

### What is NOT part of the public contract

- **Concurrency details** — the fact that the reader goroutine feeds a parser
  under `Grid.Mu` is an implementation detail. The contract is only that
  `New` starts the shell, `View` renders it, and `Close` tears it down.
- **Render pass structure** — coalesced bg/fg/cursor passes, dirty-row
  tracking, and the tessellation cache are internal optimizations.
- **`gui.DrawCanvas` IDs and versions** — the per-`Term` canvas ID
  (`"term-canvas-N"`) and the draw-version counter are internal.
- **Parser dispatch sites** — `dispatchCSI`, `dispatchOSC`, `dispatchDCS`,
  `dispatchAPC` are internal. Adding a new protocol extension doesn't change
  the public API.
- **File names and code organisation** — the layering invariant (widget →
  parser → grid) matters; the specific file boundaries within a layer do not.

### Versioning

This project follows [Semantic Versioning](https://semver.org/) but with a
pre-1.0 interpretation:

- **Patch (0.x.Y)** — bug fix; no new API surface. Safe to upgrade.
- **Minor (0.X.0)** — new feature; may add `Cfg` fields or `Term` methods,
  but existing signatures are backwards compatible. Read the changelog before
  upgrading.
- **Major (1.0.0)** — first stable release. After 1.0, the public API follows
  standard semver (breaking changes require a major version bump).

### Practical guidance for embedders

- **Pin to a minor version** in your `go.mod` (`v0.X.0`, not `v0.X.Y` or a
  bare commit hash). Patch upgrades are safe; minor upgrades may need a
  one-line config change.
- **Read the changelog** before bumping the minor version.
- **Use `Cfg` zero values** for everything you don't explicitly set. New
  fields added in a minor bump are always zero-value-safe.
- **Stick to the documented methods** — `View`/`Close`/`Cwd`/`SetTheme` cover
  the single-`Term` case; the multi-`Term` embedding methods
  (`SetFocused`/`HandleWindowEvent`/`Rows`/`Cols`/`Write`/`PID`/`Alive`) cover
  pane managers. If you need something none of them provide, open an issue
  rather than depending on internal state. (Or embed `term/workspace`, which
  already wires these together — see below.)

---

## Feature coverage

### Core emulation

| Feature | Notes |
|---|---|
| PTY-backed shell | Spawns `$SHELL`, fallback `/bin/sh`; full `TIOCSWINSZ` resize |
| VT state machine | C0, ESC, CSI, OSC, DCS, APC; xterm-compatible subset |
| 16-color ANSI | Standard foreground / background palette |
| 256-color palette | xterm 6×6×6 cube + 24-step grayscale |
| 24-bit Truecolor | `CSI 38;2;r;g;b m` / `CSI 48;2;r;g;b m` |
| SGR attributes | Bold, Dim, Italic, Underline, Inverse, Strikethrough |
| Extended underlines | Single, double, curly, dotted, dashed; per-cell color (`CSI 58`) |
| Cursor styles | Block, underline, bar — steady or blinking (DECSCUSR); `Cfg.CursorBlink` override |
| Cursor save / restore | DECSC/DECRC (`ESC 7`/`ESC 8`, `CSI s`/`CSI u`); show/hide (`?25`) |
| Scroll regions | DECSTBM (`CSI r`); IND/RI/NEL; `IL`/`DL`/`ICH`/`DCH` |
| Alt screen | DECSET 47 / 1047 / 1049; scrollback suppressed while active |
| Logical line reflow | Wrapped-row tracking; content re-wraps on every resize |
| DEC Special Graphics | `SI`/`SO`, `ESC (0` / `ESC (B` line-drawing charset |
| Tab stops | HTS (`ESC H`), TBC (`CSI g`); defaults to 8-column grid |
| Visual bell | Brief screen flash on `BEL` (`\a`) |

### Input

| Feature | Notes |
|---|---|
| Keyboard | Printable chars, arrows, Enter, Backspace (DEL), Delete, Page Up/Down, Home/End, Ctrl+letter, F1–F12, numeric keypad |
| Alt/Meta keys | ESC-prefix encoding for Alt+key combinations |
| Kitty Keyboard Protocol | `CSI u` push/pop/set/query; key-release events; left/right modifier distinction |
| IME composition | Inline pre-edit string rendered + underlined at the cursor; caret rect reported via `IMESetRect` (CJK, dead keys, emoji picker) |
| Bracketed paste | DECSET 2004; strips embedded `\x1b[201~` markers |
| Focus reporting | DECSET 1004 |

### Mouse

| Feature | Notes |
|---|---|
| Button reporting | `?1000` (click), `?1002` (drag), `?1003` (any-motion) |
| SGR encoding | `?1006`; suppressed while in scrollback |
| SGR-Pixels mode | `?1016`; emits pixel-relative coordinates |
| Mouse wheel | Scrollback navigation; forwarded to PTY in mouse-reporting mode |

### Scrollback & display

| Feature | Notes |
|---|---|
| Scrollback ring | Default 5 000 rows; configurable via `Cfg.ScrollbackRows` |
| Pixel-perfect scrolling | Sub-row `ViewSubPx` offset; momentum scroll with two-phase friction; cancels on trackpad touch |
| Scrollbar indicator | Auto-hides at live viewport; fades after inactivity |
| Text selection | Left-drag; content-relative coordinates survive scroll and resize |
| Clipboard copy/paste | `Cmd+C` / `Cmd+V`; OSC 52 write when explicitly enabled |
| Search | `Cmd+F` literal search; `Ctrl+R` toggles RE2 regex mode; highlights all matches; `Enter`/`Shift+Enter` cycle |
| Semantic shell marks | OSC 133 A/B/C/D; `Cmd+Up/Down` jumps between command boundaries |

### OSC / protocol extensions

| Sequence | Behavior |
|---|---|
| OSC 0 / 1 / 2 | Window title (`Cfg.OnTitle` callback, defaults to `win.SetTitle`) |
| OSC 7 | CWD; exposed via `Term.Cwd()` |
| OSC 8 | Hyperlinks; `Cmd+click` opens in OS default browser |
| OSC 9 / 777 | Desktop notifications; injection-safe dispatch |
| OSC 10 / 11 / 12 | Dynamic foreground / background / cursor-color set and query |
| OSC 52 | Clipboard write (base64, disabled by default via `Cfg.AllowOSC52Write`); read requests silently dropped |
| OSC 133 | Semantic shell integration (prompt / command / output marks) |
| OSC 1337 | iTerm2 inline images |
| DCS sixel | Sixel graphics; 256-register color; RLE; up to 4096×4096 px; 256 retained per grid |
| APC kitty | Kitty Graphics Protocol; chunked base64 transmission; PNG / raw RGBA / raw RGB; off-screen store; delete by ID |
| DECRQSS / XTGETTCAP | Terminfo/SGR capability queries; replied via `onReply` → PTY write. `Smulx`/`Setulc` advertise styled + colored underlines |
| DA1 / DA2 | Primary attributes advertise Sixel (`CSI ?1;2;4c`); secondary identifies as go-term |
| XTVERSION | `CSI > q` → `DCS >\| go-term(ver) ST` |
| XTWINOPS | `CSI 14 t` / `CSI 16 t` pixel-geometry reports (text area / cell size); manipulation ops ignored |
| DECRQM | Reports DEC private-mode state (set / reset / unrecognized) so apps can probe capabilities |
| Synchronized Updates | DCS `?2026` — batches a frame so partial repaints don't flicker |
| Grapheme clustering | Mode 2027 (always on); DECRQM reports it permanently set |

### Internationalization

| Feature | Notes |
|---|---|
| Grapheme clusters | Input segmented into clusters via `uniseg` (Mode 2027, always on; advertised to `ucs-detect`); multi-codepoint clusters stored in a per-grid intern pool |
| East Asian Wide | CJK and wide emoji; cluster width from `uniseg`; correct cursor advance and half-cell erasure |
| Combining / ZWJ / VS15-16 / flags | Combining marks, ZWJ sequences, variation selectors, and regional-indicator flag pairs render as a single cell — not double-advanced |
| Bidirectional text | Unicode BiDi Algorithm (UAX#9); RTL scripts (Hebrew, Arabic) reordered for display |

### Workspace (splits, tabs, persistence)

Native window multiplexing — no `tmux` required — lives in the
`term/workspace` package, a layer *above* `term` that creates and wires
`*term.Term` instances through their public API. The single-shell `term`
widget has no awareness of panes.

| Feature | Notes |
|---|---|
| Split panes | Vertical / horizontal splits over a flex-ratio split tree; keyboard-driven resize |
| Tabs | Tab bar showing each active pane's OSC 0/2 title; create / close / cycle |
| Focus routing | Click or keyboard cycles focus; active pane gets an accent border, others dim |
| Persistence | Full layout (tabs → split trees → per-pane CWD + ratio) saved to versioned JSON, restored on launch |
| Keybindings | Hand-edited INI-style `config` file (`[keybindings]` section); kitty/iTerm2-style defaults, all overridable |
| Config root | `$XDG_CONFIG_HOME/go-term`, else `~/.config/go-term`, else `os.UserConfigDir()/go-term` |

Default bindings (overridable): Cmd+D / Cmd+Shift+D split, Cmd+Shift+W close
pane, Cmd+] / Cmd+[ cycle panes, Cmd+Ctrl+Arrow resize, Cmd+T new tab,
Cmd+Ctrl+W close tab, Cmd+Shift+] / Cmd+Shift+[ cycle tabs, Cmd+/ shortcut
overlay. The bundled `examples/demo` is built on `term/workspace` and accepts
`--workspace <path>` / `--save-workspace <path>` flags.

### Performance

| Mechanism | Effect |
|---|---|
| Dirty-row tracking | `readLoop` skips cache-bust when no cells changed |
| Tessellation cache | `DrawCanvas` ID + version; `OnDraw` skipped by go-gui when version is unchanged |
| Coalesced text runs | Cells with identical SGR batched into single `dc.Text` calls |
| Zero allocs on draw path | `BenchmarkForegroundPass`: 37 µs, 0 allocs — 80×24, Apple M5 |

---

## Requirements

- Go 1.26+
- macOS or Linux
- Sibling working trees of [`go-gui`](https://github.com/go-gui-org/go-gui)
  and [`go-glyph`](https://github.com/go-gui-org/go-glyph) at `../go-gui`
  and `../go-glyph`. Copy `go.work.example` to `go.work` to wire the
  local siblings into the module graph (Go workspace mode).

---

## Quickstart

```bash
git clone https://github.com/go-gui-org/go-term.git
cd go-term/examples/demo
go run .
```

Try `ls --color=always`, `vim`, `htop`, and window resize — then
`stty size` inside the embedded shell to confirm the child process received
the resize signal. Split panes with Cmd+D / Cmd+Shift+D, open tabs with
Cmd+T, and press Cmd+/ for the full shortcut overlay.

---

## Usage

```go
package main

import (
    "log"

    "github.com/go-gui-org/go-gui/gui"
    "github.com/go-gui-org/go-gui/gui/backend"
    "github.com/go-gui-org/go-term/term"
)

func main() {
    var t *term.Term
    w := gui.NewWindow(gui.WindowCfg{
        Title:  "go-term",
        Width:  900,
        Height: 600,
        OnInit: func(w *gui.Window) {
            var err error
            t, err = term.New(w, term.Cfg{})
            if err != nil {
                log.Fatalf("term.New: %v", err)
            }
            w.UpdateView(t.View)
        },
    })
    defer func() {
        if t != nil {
            _ = t.Close()
        }
    }()
    backend.Run(w)
}
```

### Configuration

```go
term.Cfg{
    // Process
    Command: "/bin/bash",            // default: $SHELL, fallback /bin/sh
    Args:    []string{"-l"},          // args for Command (or the default shell)
    Env:     []string{"FOO=bar"},     // appended to os.Environ(); "KEY=" unsets
    Dir:     "/tmp",                  // child working dir (empty = inherit CWD)

    // Display
    ScrollbackRows:  10000,           // default 5000; negative disables history
    CursorBlink:     ptr(true),       // override DECSCUSR blink state
    TextStyle:       gui.TextStyle{}, // override default monospace style
    Themes:          myThemes,        // right-click theme menu; first is default
    BellFlashDuration: 0,             // 0 = 100 ms; negative disables visual bell
    ScrollbarWidth:    0,             // 0 = 4 px; negative hides the scrollbar
    DisableGraphics:   false,         // skip Sixel/Kitty/iTerm2 image decoding

    // Behavior / callbacks
    AllowOSC52Write: true,            // allow shell apps to set the clipboard
    OnTitle:  func(s string) {},      // OSC 0/1/2 title (nil → win.SetTitle)
    OnNotify: func(title, body string) {}, // OSC 9/777 (nil → native notify)
    OnExit:   func() {},              // child process exited
    OnClickFocus: func() {},          // canvas clicked (multi-Term focus)

    // Embedding
    NoWindowHandler: true,            // a pane manager owns window-event dispatch
}
```

`Term.Cwd()` returns the shell's current working directory (updated via
OSC 7, emitted by zsh, bash, and fish with standard shell integration).

---

## Architecture

Three layers; dependencies flow strictly downward. Each layer is split
across multiple files by concern.

```
examples/demo/main.go         gui.NewWindow + workspace.Restore + backend.Run
        │
        ▼
term/workspace/*.go      Pane split tree, tab bar, keybindings, JSON
                         persistence. Wires *term.Term via its public API.
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

term/pty.go              creack/pty wrapper. Spawns $SHELL, resize ioctl.
term/palette.go          256-color ANSI table + RGB resolution.
```

### Concurrency

One PTY reader goroutine is started in `term.New`. `Grid.Mu` is the single
lock — the reader takes it to feed the parser; `OnDraw` takes it to read
cells. After feeding bytes the reader calls `win.QueueCommand(...)` to
schedule a redraw on the main thread. Direct `*gui.Window` access from the
reader goroutine is forbidden.

---

## Testing

```bash
go test ./...
go test -race ./...
go vet ./...
```

Replay-style emulator tests feed realistic byte streams into the parser and
assert final screen state, cursor position, OSC side-effects, and host
replies. See [docs/terminal-verification.md](docs/terminal-verification.md).

Visual verification should complement the automated suite: run `examples/demo`
and exercise resize, redraw, selection, paste, and application compatibility
(`vim`, `htop`, `tmux`).

---

## Out of scope

| Feature | Reason |
|---|---|
| Windows / ConPTY | PTY layer is POSIX-only |
| Disk-backed scrollback | Deferred until real-world memory pressure warrants it |

---

## License

[MIT](LICENSE)
