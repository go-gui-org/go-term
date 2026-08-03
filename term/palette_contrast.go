package term

import (
	"math"

	"github.com/go-gui-org/go-gui/gui"
)

// Minimum contrast: a render-time floor on the ratio between a cell's
// foreground and its background.
//
// The problem it solves is not go-term's palette, it is everyone else's. A
// truecolor SGR is not themeable — `eza` and `starship` emit 24-bit colors
// chosen against a dark background, and on a light theme they arrive exactly
// as sent. Measured on a real Catppuccin Latte session: 77% of the glyph
// pixels sat below a 3:1 contrast ratio, and every one of them came from a
// truecolor sequence rather than from the theme's ANSI table. COLORFGBG
// (pty.go) tells the child which way the terminal is painted; this handles the
// children that do not ask.
//
// Off by default (ratio <= 1 disables). When on, a foreground that fails the
// floor is pushed toward white or black — whichever direction has headroom
// against that cell's background — until it clears. Render-only: the grid
// keeps the color the child sent, so selection copy, search and recording are
// unaffected, exactly like blink and conceal.
//
// The ratio is WCAG 2.x relative luminance, the same definition accessibility
// tooling reports, so a number a user reads elsewhere means the same thing
// here.

// contrastDisabled is the ratio at or below which the clamp is a no-op. 1.0 is
// the ratio of a color against itself, so it is the natural "off".
const contrastDisabled = 1.0

// maxContrast is the ratio of black against white — nothing higher exists, so
// asking for more can only mean "as far as it goes".
const maxContrast = 21.0

// srgbToLinear undoes the sRGB transfer function for one 8-bit channel, per
// WCAG 2.x. Kept as a 256-entry table because the alternative is two math.Pow
// calls per channel per color pair, and this runs while the foreground pass is
// coalescing runs.
var srgbToLinear [256]float64

func init() {
	for i := range srgbToLinear {
		v := float64(i) / 255
		if v <= 0.03928 {
			srgbToLinear[i] = v / 12.92
		} else {
			srgbToLinear[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
}

// relLuminance is WCAG relative luminance, 0 (black) to 1 (white).
func relLuminance(c gui.Color) float64 {
	return 0.2126*srgbToLinear[c.R] +
		0.7152*srgbToLinear[c.G] +
		0.0722*srgbToLinear[c.B]
}

// contrastRatio is the WCAG contrast ratio between two colors, 1.0 to 21.0.
func contrastRatio(a, b gui.Color) float64 {
	la, lb := relLuminance(a), relLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// clampContrast returns fg adjusted so it reaches at least ratio against bg,
// or fg unchanged when it already does.
//
// The adjustment is a blend toward white or black rather than a jump to one:
// a red that fails by a little should stay recognizably red, because the
// child's color choice usually carries meaning (a failing test, a modified
// file) that a wholesale replacement would erase. The direction is whichever
// extreme is farther from the *background* — on a light theme that is black,
// on a dark one white — since blending toward the near one would make things
// worse before it made them better.
//
// The blend fraction is found by binary search rather than solved for
// directly: relative luminance is not linear in the blend, so the closed form
// would be per-channel and would still need clamping. Seven iterations of an
// integer search land within 1% of the minimum change that works, and the
// whole call is memoized by contrastMemo below.
func clampContrast(fg, bg gui.Color, ratio float64) gui.Color {
	if ratio <= contrastDisabled {
		return fg
	}
	if ratio > maxContrast {
		ratio = maxContrast
	}
	if contrastRatio(fg, bg) >= ratio {
		return fg
	}
	// Pick the direction by measuring both, not by asking whether the
	// background is "light". The WCAG crossover — where white and black offer
	// equal contrast — is at luminance 0.179, not 0.5, so a mid-tone
	// background (a 50% gray, a muted vim status line) reads as dark by the
	// naive test while black is in fact the better direction by a factor of
	// two. Getting that wrong turns a reachable ratio into an unreachable one
	// and washes the text out to pure white for nothing.
	target := overlayWhite
	if contrastRatio(overlayBlack, bg) > contrastRatio(overlayWhite, bg) {
		target = overlayBlack
	}
	// Invariant: 100% (the extreme) is the best available, so hi always
	// satisfies the test or nothing does — the search returns the extreme in
	// the unreachable case, which is the right best effort.
	lo, hi := 0, 100
	for lo < hi {
		mid := (lo + hi) / 2
		if contrastRatio(mixPct(fg, target, mid), bg) >= ratio {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return mixPct(fg, target, lo)
}

// contrastMemoSize is the number of (fg, bg) pairs the memo holds. A screen of
// text uses a handful of distinct pairs — one per syntax color — so a small
// direct-mapped table hits almost always; 256 entries is ~3 KB per Term and
// leaves room for a pathological rainbow line without thrashing. Kept a power
// of two so the slot fold below is a shift rather than a division.
const (
	contrastMemoBits = 8
	contrastMemoSize = 1 << contrastMemoBits
)

// contrastMemo is a direct-mapped cache of clampContrast results.
//
// A map would allocate and hash on every cell of every frame; this is two
// array indexes and a comparison. Direct-mapped rather than associative
// because a miss costs one clampContrast (microseconds, and only for pairs
// never seen before), so eviction policy does not pay for itself.
//
// Main-thread only: read and written from the foreground pass, which runs
// inside OnDraw under grid.Mu.
//
// Measured (BenchmarkCellRunKey, M-series): 30 ns/cell with the clamp off —
// unchanged from before the feature, the gate is one float comparison — and
// 41 ns/cell with it on, the extra covering bgOf plus the memo hit. At 24×80
// that is ~22 µs a frame, which is why this is opt-in rather than always on.
type contrastMemo struct {
	// keys packs fg and bg into 48 bits with bit 63 set, so a populated slot
	// is never the zero value and no sentinel color is needed.
	keys [contrastMemoSize]uint64
	vals [contrastMemoSize]gui.Color
}

// memoKey packs a color pair into the slot key. Alpha is deliberately dropped:
// every color reaching the foreground pass is opaque.
func memoKey(fg, bg gui.Color) uint64 {
	return 1<<63 |
		uint64(fg.R)<<40 | uint64(fg.G)<<32 | uint64(fg.B)<<24 |
		uint64(bg.R)<<16 | uint64(bg.G)<<8 | uint64(bg.B)
}

// memoSlot folds a key down to a table slot by Fibonacci hashing: one multiply
// and one shift, and every input bit reaches the result.
//
// Masking or XOR-folding the key instead would land on a couple of channels —
// every pair sharing a background, which is the common case, would then be
// distinguished only by the foreground's blue — so two ordinary syntax colors
// could evict each other on every cell.
func memoSlot(k uint64) uint64 {
	return (k * 0x9E3779B97F4A7C15) >> (64 - contrastMemoBits)
}

// lookup returns the clamped foreground for the pair, computing and caching it
// on a miss.
func (m *contrastMemo) lookup(fg, bg gui.Color, ratio float64) gui.Color {
	k := memoKey(fg, bg)
	slot := memoSlot(k)
	if m.keys[slot] == k {
		return m.vals[slot]
	}
	v := clampContrast(fg, bg, ratio)
	m.keys[slot], m.vals[slot] = k, v
	return v
}

// reset drops every cached entry. Called when the ratio changes — the memo is
// keyed by color pair, not by ratio, so entries computed under the old one are
// wrong rather than merely stale.
func (m *contrastMemo) reset() {
	clear(m.keys[:]) // a zero key never matches memoKey, which always sets bit 63
}

// applyMinContrast is the single call site's worth of logic: a fast disabled
// check first, so a Term that never turns this on pays one float comparison
// per cell and nothing else.
func (g *grid) applyMinContrast(fg, bg gui.Color) gui.Color {
	if g.MinContrast <= contrastDisabled {
		return fg
	}
	return g.contrast.lookup(fg, bg, g.MinContrast)
}
