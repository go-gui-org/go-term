# Falcon app icon

| File | Use |
| --- | --- |
| `falcon.icns` | macOS bundle icon. `make app` passes this to `buildapp -icon`. |
| `falcon.ico` | Windows executable/shortcut icon. |
| `falcon.png` | 1024×1024 master, full artwork, full-bleed rounded square. |
| `falcon-small.png` | 1024×1024 master, simplified mark for small sizes. |
| `falcon-dock.png` | 512×512 padded full art, embedded in the binary. Read the next section before touching this one. |

## The bundle icon alone is not enough

Shipping `falcon.icns` in the `.app` and setting `CFBundleIconFile` does
**not** get the icon shown. The go-gui darwin backend calls
`-[NSApplication setApplicationIconImage:]` during window creation, and
falls back to go-gui's own default icon when `gui.WindowCfg.IconPNG` is
empty (`gui/backend/metal/backend.go:495-497` as of go-gui v0.43.0).
That runtime call takes precedence over `CFBundleIconFile` for the running
process, so an unset `IconPNG` means falcon installs the *go-gui* icon
over its own on every launch, from a correctly bundled build.

This is why `icon.go` embeds `falcon-dock.png` and both windows — the live
one and the `--replay` viewer — set `IconPNG` from it. It is not redundant
with the `.icns`; each covers a case the other cannot:

- `.icns` / `CFBundleIconFile` — Finder, the Dock's persistent tile, Get
  Info. Applies only to a bundled build.
- `IconPNG` — the running process's application icon, and the only icon
  that exists at all under `cd examples/falcon && go run .`, where there
  is no bundle.

Symptom if `IconPNG` regresses: the go-gui icon in `⌘Tab` and the Dock
while Finder still shows the falcon. That reads exactly like a stale
LaunchServices icon cache — it isn't, and flushing the cache will not fix
it. `TestWindowCfgWiring` and `TestReplayWindowCfgWiring` fail if either
window drops the field.

Only the darwin backend reads `WindowCfg.IconPNG` today; on Linux and
Windows it is inert but harmless.

## Two marks, one icon

The full artwork (wing + head + `>_` prompt + FALCON wordmark) is
illegible below roughly 48px — the wordmark and the thin feather strokes
collapse into noise. So the icon ships as two marks, and `falcon.icns` /
`falcon.ico` bind them to size buckets:

- **≤64px** — simplified mark: falcon head only, no wing, no wordmark,
  scaled to ~73% of the canvas. Reads as a bird silhouette at 32px and
  stays a distinct shape at 16px.
- **≥128px** — full artwork.

macOS and Windows both pick the closest slot per context, so the Dock and
the Finder icon get the full art while the menu bar, tab bars, and
`⌘Tab` get the legible one.

## Geometry

Both masters are full-bleed 1024×1024 with transparent corners at a
229px radius (22.4% — Apple's squircle proportion). Inside the `.icns`
each slot is padded to the Big Sur convention: artwork at 824px centered
on a 1024px transparent canvas. No drop shadow is baked in; macOS
composites its own.

## Regenerating

The masters are the source of truth. To rebuild the platform files after
editing either PNG, resize each master into an `.iconset` (16, 32, 64
from `falcon-small.png`; 128, 256, 512, 1024 from `falcon.png`, each
padded to 824/1024) and run `iconutil -c icns`. Keep PNGs at `-depth 8`
— a 16-bit master inflates the `.icns` several-fold for no visible gain.

Regenerate `falcon-dock.png` in the same pass — it is `falcon.png` padded
to 824/1024 and scaled to 512, and being a separate file it will silently
keep the old artwork otherwise:

```fish
magick falcon.png -resize 824x824 -background none -gravity center \
    -extent 1024x1024 -resize 512x512 -depth 8 -strip \
    -define png:compression-level=9 falcon-dock.png
```

`TestAppIconDecodes` bounds it at 1024px and 512KB, so an oversized
replacement fails the build rather than quietly bloating every binary.
