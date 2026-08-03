# Falcon app icon

| File | Use |
| --- | --- |
| `falcon.svg` | **Source of truth** for the full artwork. Everything below except `falcon-small.png` is rendered from it. |
| `falcon.icns` | macOS bundle icon. `make app` passes this to `buildapp -icon`. |
| `falcon.ico` | Windows executable/shortcut icon. |
| `falcon.png` | 1024×1024 render of `falcon.svg`, full-bleed rounded square. |
| `falcon-small.png` | 1024×1024 raster master, simplified mark for small sizes. Not derived from the SVG — see "Two marks, one icon". |
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

The full artwork (wing + head + `>_` prompt) carries three separate
elements and thin feather strokes that collapse into noise below roughly
48px. So the icon ships as two marks, and `falcon.icns` / `falcon.ico`
bind them to size buckets:

- **≤64px** — simplified mark: falcon head only, no wing, scaled to ~73%
  of the canvas. Reads as a bird silhouette at 32px and stays a distinct
  shape at 16px.
- **≥128px** — full artwork.

macOS and Windows both pick the closest slot per context, so the Dock and
the Finder icon get the full art while the menu bar, tab bars, and
`⌘Tab` get the legible one.

`falcon-small.png` is its own drawing, not the `#head` group of
`falcon.svg` scaled up. The two heads are close but not identical — the
simplified mark has a longer, cleaner crest and drops the throat detail
that only reads above 128px. Rendering `#head` at 73% instead produces a
visibly busier mark with a detached feather shard, so don't "simplify"
this by deriving it. If the small mark ever needs editing it wants its
own vectorization pass.

Consequence: the two masters carry independently produced backgrounds.
`falcon.svg`'s is the reconstructed gradient described below, roughly
2.7% off `falcon-small.png`'s. That gap only shows if a 64px and a 128px
icon sit side by side, which no macOS or Windows surface does.

## Geometry

Both masters are full-bleed 1024×1024 with transparent corners at a
222px radius (21.7%). That number is measured, not chosen: fitting the
original artwork's sub-pixel alpha silhouette gives a plain rounded rect
at r=222 with 0.41px rms. It is **not** an Apple-style continuous-curvature
squircle — the best-fitting superellipse (n=5.30) is 4.19px rms, ten times
worse. Don't "correct" this to a squircle; that moves away from the
artwork. Inside the `.icns`
each slot is padded to the Big Sur convention: artwork at 824px centered
on a 1024px transparent canvas. No drop shadow is baked in; macOS
composites its own.

## Inside falcon.svg

Three groups — `#wing`, `#head`, `#prompt` — inside one `#art` group, over
a `#background` group. The `#art` transform is the only thing positioning
the mark; edit that one attribute to move or resize the whole thing.

The background is a **four-corner bilinear gradient**, not the single
`linearGradient` it looks like. SVG has no bilinear gradient, so it is two
vertical gradients — left edge and right edge — cross-faded horizontally
by a mask. The four corner colors in `<defs>` are the only place
background color lives; changing one is a one-line edit.

That reconstruction exists because the vectorizer flattened the original
raster's gradient to a single navy fill and left the blue→purple
transition behind as three thin edge slivers. Corner values are
least-squares fitted to the pre-vector raster over 206796 masked
background samples (bilinear residual 5.74/255; a plain linear fit was
6.24 and radial 11.67, so the bilinear form is not decoration). Rendered
error against the original background is about 7.5/255.

**Trap when refitting.** Separating background from artwork by
thresholding max-channel brightness needs a threshold above ~63%. The
background's top-right purple reaches 53% max-channel, so the obvious
45% threshold classifies the purple corner *as artwork* and excludes it
from the fit — which silently drags the fitted top-right corner toward
blue and drains the purple out of the icon. The max-channel histogram has
a clean valley at 181/255; anything in 160–180 separates correctly.

`#wingLower`, `#wingMid`, `#throatUpper` and `#throatLower` are gradients
refitted the same way, per path, against the raster inside each path's
eroded mask — the vectorizer had flattened all four. One flat fill
remains by choice: a 12-interior-pixel shard in `#head`, too thin to fit
and invisible at every shipped size.

## Regenerating

`falcon.svg` is the source of truth for everything except
`falcon-small.png`. Rendering needs `rsvg-convert` (`brew install
librsvg`). Keep PNGs at `-depth 8` — a 16-bit master inflates the `.icns`
several-fold for no visible gain.

```fish
# 1024 full-bleed master
rsvg-convert -w 1024 -h 1024 -o falcon.png falcon.svg
magick falcon.png -depth 8 -strip -define png:compression-level=9 falcon.png

# falcon-dock.png — padded to 824/1024, scaled to 512. A separate file, so
# it silently keeps the old artwork if you skip this.
rsvg-convert -w 824 -h 824 -o /tmp/a.png falcon.svg
magick /tmp/a.png -background none -gravity center -extent 1024x1024 \
    -resize 512x512 -depth 8 -strip -define png:compression-level=9 \
    falcon-dock.png
```

For the `.iconset`, render each slot from the SVG at its *art* size
(`slot × 824/1024`) and then `-extent` to the slot size — rendering the
vector at the target size is sharper than downscaling the 1024 PNG. Slots
16/32/64 come from `falcon-small.png` instead, resized the same way. Then
`iconutil -c icns -o falcon.icns falcon.iconset`.

`falcon.ico` uses the same split: 16/32/48/64 from `falcon-small.png`,
128/256 rendered from the SVG, combined with `magick <pngs> falcon.ico`.

`TestAppIconDecodes` bounds `falcon-dock.png` at 1024px and 512KB, so an
oversized replacement fails the build rather than quietly bloating every
binary.
