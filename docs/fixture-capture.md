# Capturing Replay Fixtures from Real Terminal Sessions

Replay fixtures are JSON files in `term/testdata/` that let the test
suite verify parser behaviour without a live PTY. Each fixture contains
base64-encoded input bytes and expected grid output.

## Quick Start: Go-Generated Fixtures

For programmatic fixture capture, edit and run the `TestCaptureFixture`
helper in `term/fixture_test.go`:

```go
func TestCaptureFixture(t *testing.T) {
    name := "my_fixture"
    rows := 24
    cols := 80
    input := []byte("hello\x1b[2;3HX\x1b[K")
    // ...
}
```

```bash
go test -run TestCaptureFixture -count=1 ./term
```

## Capturing from a Real Terminal with `script`

The `script` command (available on macOS and Linux) records an entire
terminal session to a file. Use it to capture real program output:

```bash
# Start recording. The shell prompt appears normally.
script fixture.script

# Run the commands you want to capture:
ls --color=auto
vim -c 'set columns=40' -c 'q!' somefile
printf '\e[31mred text\e[0m\n'
# ... any terminal output you want as a fixture ...

# Exit the recording shell.
exit
```

This produces two files:
- `fixture.script` — raw terminal bytes (what we want)
- `fixture.script.time` — timing metadata (discard)

## Converting a Script Typescript to a Fixture

Use the `script2fixture` Go helper to convert a typescript file into a
replay fixture:

```bash
go run ./term/script2fixture \
  -name cursor_moves \
  -script fixture.script \
  -rows 24 -cols 80 \
  -out term/testdata/
```

The helper:
1. Reads the typescript file
2. Feeds it through a fresh `Parser` + `Grid`
3. Captures the final grid state, cursor position, title, and CWD
4. Writes a `.json` fixture to `term/testdata/`

After generating the fixture:
```bash
# Verify it replays correctly.
go test -run TestEmulatorReplayFixtures -count=1 ./term
```

### Manual Conversion (Shell One-Liner)

If you prefer not to use the Go helper, convert manually:

```fish
# Fish shell
set input_b64 (base64 < fixture.script | string collect)

# Inspect the typescript bytes to understand the expected output,
# then write a JSON fixture by hand:
cat > term/testdata/my_fixture.json <<EOF
{
  "name": "my_fixture",
  "rows": 24,
  "cols": 80,
  "input_b64": "$input_b64",
  "want_lines": ["expected line 1", "..."],
  "want_row": 0,
  "want_col": 0
}
EOF
```

## Determining Expected Output

The hardest part of manual fixture creation is knowing what the grid
*should* look like after feeding the input. Two approaches:

1. **Feed-and-inspect**: temporarily modify `TestCaptureFixture` to
   use your recorded bytes, run it, and copy the `want_lines` it prints.
2. **Use `script2fixture`**: it does this automatically.

## Fixture JSON Format

```json
{
  "name": "shell_prompt",
  "rows": 24,
  "cols": 80,
  "input_b64": "aGVsbG8=",
  "want_lines": ["expected content per row..."],
  "want_row": 0,
  "want_col": 4,
  "want_title": "optional window title",
  "want_cwd": "optional working directory"
}
```

- `input_b64` — base64-encoded terminal bytes (survives text-editor round-trips)
- `want_lines` — one string per grid row, each exactly `cols` characters
- `want_row`, `want_col` — final cursor position (0-indexed)
- `want_title` — window title after parsing (omit if unchanged)
- `want_cwd` — working directory after parsing (omit if unchanged)

## Limitations

- **No timing**: fixtures are pure byte streams, not interactive sessions.
  The parser doesn't model time, so replay is always instantaneous.
- **No resize events**: the grid size is fixed per fixture. Test resize
  behaviour with multiple fixtures at different sizes.
- **No PTY behaviour**: the fixture path feeds bytes directly into the
  parser, bypassing the PTY reader goroutine. PTY-specific behaviour
  (SIGWINCH, process lifecycle) must be tested separately.
