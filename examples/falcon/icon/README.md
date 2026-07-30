# Falcon app icon

| File | Use |
| --- | --- |
| `falcon.icns` | macOS bundle icon. `make app` passes this to `buildapp -icon`. |
| `falcon.ico` | Windows executable/shortcut icon. |
| `falcon.png` | 1024×1024 master, full artwork, full-bleed rounded square. |
| `falcon-small.png` | 1024×1024 master, simplified mark for small sizes. |

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
