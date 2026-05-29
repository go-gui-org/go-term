# go-term

A full-featured, embeddable terminal-emulator widget for the
[`go-gui`](https://github.com/mike-ward/go-gui) framework. Spawns a real
shell over a PTY, renders through a GPU-accelerated `gui.DrawCanvas`, and
covers the protocol surface expected by modern CLI tools and TUI frameworks.

Targets macOS and Linux.

## Status

Pre-1.0. The public API (`Cfg`, `Term`, `New`, `View`, `Close`) is small on
purpose and may still shift. See [CHANGELOG.md](CHANGELOG.md).

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
| DECRQSS / XTGETTCAP | Replied via `onReply` → PTY write |
| Synchronized Updates | DCS `?2026` |

### Internationalization

| Feature | Notes |
|---|---|
| East Asian Wide | CJK and wide emoji via `uniseg`; correct cursor advance and half-cell erasure |
| Combining / ZWJ | Zero-width codepoints dropped (not double-advanced) |

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
- Sibling working trees of [`go-gui`](https://github.com/mike-ward/go-gui)
  and [`go-glyph`](https://github.com/mike-ward/go-glyph) at `../go-gui`
  and `../go-glyph` (referenced via `replace` directives in `go.mod`)

---

## Quickstart

```bash
git clone https://github.com/mike-ward/go-term.git
cd go-term/examples/demo
go run .
```

Try `ls --color=always`, `vim`, `htop`, and window resize — then
`stty size` inside the embedded shell to confirm the child process received
the resize signal.

---

## Usage

```go
package main

import (
    "log"

    "github.com/mike-ward/go-gui/gui"
    "github.com/mike-ward/go-gui/gui/backend"
    "github.com/mike-ward/go-term/term"
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
    ScrollbackRows: 10000,          // default 5000
    CursorBlink:    ptr(true),      // override DECSCUSR blink state
    AllowOSC52Write: true,           // allow shell apps to set clipboard
    OnTitle:        func(s string) { /* window title change */ },
}
```

`Term.Cwd()` returns the shell's current working directory (updated via
OSC 7, emitted by zsh, bash, and fish with standard shell integration).

---

## Architecture

Three layers; dependencies flow strictly downward. Each layer is split
across multiple files by concern.

```
examples/demo/main.go         gui.NewWindow + term.New + backend.Run
        │
        ▼
term/widget.go           Term struct, New, View, Close; reader goroutine.
term/widget_draw.go      OnDraw: bg/fg/graphics/cursor render passes.
term/widget_keyboard.go  onChar, onKeyDown, onKeyUp; KKP encoding.
term/widget_mouse.go     Mouse button/motion/wheel; SGR encoding.
term/widget_clipboard.go Cmd+C/V; opt-in OSC 52 clipboard write.
term/widget_scroll.go    Scrollbar, momentum scroll, ViewSubPx math.
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
term/grid_edit.go        Erase, insert/delete lines/chars.
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
| IME composition / dead keys | Requires platform input-method integration |
| Windows / ConPTY | PTY layer is POSIX-only |
| Font ligatures | Requires a full text shaper (HarfBuzz / go-text) |
| Native splits and tabs | Layout manager above `Term`; planned, not started |

---

## License

[MIT](LICENSE)
