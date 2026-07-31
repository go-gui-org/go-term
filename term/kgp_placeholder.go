package term

import "unicode/utf8"

// Kitty Graphics Protocol Unicode placeholders.
//
// A placeholder cell is the private-use character U+10EEEE carrying up to
// three combining diacritics from kgpDiacritics (generated):
//
//	first   row within the placement
//	second  column within the placement
//	third   most significant byte of the image id
//
// The image id's low 24 bits come from the cell's foreground color (a palette
// index in 256-color mode, the RGB triple in true-color mode) and the
// placement id from its underline color. Any of the three diacritics may be
// omitted, in which case the missing values are inherited from the placeholder
// cell to the left — that is what lets an application print a whole row as
// `<placeholder><row diacritic>` followed by bare placeholders.
//
// The placeholder plus its diacritics is a single grapheme cluster, so the
// decode reads the interned cluster string, not cell.Ch.

// kgpPlaceholderRune is the private-use character that marks a cell as a
// Kitty Unicode placeholder.
const kgpPlaceholderRune rune = 0x10EEEE

// kgpPlaceholderStr is that rune on its own — the cluster text of a bare
// placeholder cell, the one that inherits everything from its left neighbour.
// A constant so the render scan need not build (or cache-look-up) the string
// for what is the bulk of the cells under a large image.
const kgpPlaceholderStr = string(kgpPlaceholderRune)

// placeholderCell is one decoded placeholder: which image, which placement,
// and which tile of it this cell shows.
type placeholderCell struct {
	imageID     uint32
	placementID uint32
	row         int
	col         int
	// fg/ul are the raw cell colors the values were derived from. The
	// inheritance rules only allow a cell to continue the run to its left when
	// both match, so they have to be carried, not just the decoded ids.
	fg uint32
	ul uint32
	// valid distinguishes "no previous placeholder" from a decoded one; a
	// non-placeholder cell resets the run by zeroing this.
	valid bool
}

// kgpDiacriticValue returns the number diacritic r encodes. The table is
// sorted by code point, so this is a binary search.
func kgpDiacriticValue(r rune) (int, bool) {
	lo, hi := 0, len(kgpDiacritics)-1
	for lo <= hi {
		mid := int(uint(lo+hi) >> 1)
		switch {
		case kgpDiacritics[mid].r < r:
			lo = mid + 1
		case kgpDiacritics[mid].r > r:
			hi = mid - 1
		default:
			return int(kgpDiacritics[mid].val), true
		}
	}
	return 0, false
}

// kgpColorID reads the id a placeholder cell carries in one of its colors.
// Both ids the protocol packs this way — the image id in the foreground, the
// placement id in the underline color — use the same encoding: in 256-color
// mode the palette *index* is the id, not the color it resolves to (which is
// why this reads the packed value rather than going through the theme), and in
// true-color mode the RGB triple is the id. DefaultColor means the application
// set no color, so there is no id to read.
func kgpColorID(c uint32) uint32 {
	switch c & 0xFF000000 {
	case colorPalette:
		return c & 0xFF
	case colorRGB:
		return c & 0x00FFFFFF
	default:
		return 0
	}
}

// kgpImageIDLow24 extracts the low 24 bits of the image id from a cell
// foreground color. Reports false when the color cannot name an image:
// DefaultColor means the application never set one, and id 0 is reserved.
func kgpImageIDLow24(fg uint32) (uint32, bool) {
	id := kgpColorID(fg)
	return id, id != 0
}

// kgpPlacementID extracts the placement id from a cell underline color. Zero
// (and "no underline color set") means the client named no placement, which
// the spec allows: the terminal may then use any virtual placement of the
// image.
func kgpPlacementID(ul uint32) uint32 { return kgpColorID(ul) }

// decodePlaceholder decodes one cell into a placeholderCell. text is the
// cell's full grapheme cluster (base rune plus combining marks) and prev is
// the decode of the cell immediately to its left — zero-valued when that cell
// was not a placeholder, which breaks any inheritance run.
//
// Reports false when the cell is not a placeholder at all, or is one the
// terminal cannot resolve (no image id in the foreground color, or missing
// diacritics with no run to inherit from).
func decodePlaceholder(text string, fg, ul uint32, prev placeholderCell) (placeholderCell, bool) {
	var out placeholderCell

	// Fast reject before touching the rest of the cluster: the overwhelming
	// majority of cells are ordinary text. An empty string decodes to
	// RuneError, which is not the placeholder, so it needs no separate guard.
	base, n := utf8.DecodeRuneInString(text)
	if base != kgpPlaceholderRune {
		return out, false
	}

	var (
		diacritics [3]int
		nDia       int
	)
	for _, r := range text[n:] {
		v, ok := kgpDiacriticValue(r)
		if !ok {
			// A combining mark outside the table means this is not a
			// placeholder cluster — some other text that happens to start with
			// the private-use character.
			return out, false
		}
		if nDia == len(diacritics) {
			// More diacritics than the protocol defines. Bail immediately
			// rather than walking the rest of a cluster that can be as long as
			// the grapheme segmenter allows.
			return out, false
		}
		diacritics[nDia] = v
		nDia++
	}

	idLow, ok := kgpImageIDLow24(fg)
	if !ok {
		return out, false
	}
	out.fg, out.ul = fg, ul
	out.placementID = kgpPlacementID(ul)

	// Same-run test for the inheritance rules: the cell to the left must be a
	// placeholder with identical foreground *and* underline colors. Different
	// colors mean a different image or placement, so nothing carries over.
	sameRun := prev.valid && prev.fg == fg && prev.ul == ul

	switch nDia {
	case 3:
		out.row, out.col = diacritics[0], diacritics[1]
		out.imageID = idLow | uint32(diacritics[2])<<24
	case 2:
		out.row, out.col = diacritics[0], diacritics[1]
		out.imageID = idLow
		// Only the most significant byte is inherited here, and only when the
		// run is otherwise contiguous.
		if sameRun && prev.row == out.row && prev.col+1 == out.col {
			out.imageID = idLow | (prev.imageID & 0xFF000000)
		}
	case 1:
		if !sameRun || prev.row != diacritics[0] {
			return out, false
		}
		out.row = diacritics[0]
		out.col = prev.col + 1
		out.imageID = idLow | (prev.imageID & 0xFF000000)
	default: // 0
		if !sameRun {
			return out, false
		}
		out.row = prev.row
		out.col = prev.col + 1
		out.imageID = idLow | (prev.imageID & 0xFF000000)
	}

	out.valid = true
	return out, true
}

// isPlaceholderCell reports whether c is a Kitty Unicode placeholder. Cheap
// enough for the render and selection hot paths: the base rune is stored
// directly on the cell even for multi-codepoint clusters.
func isPlaceholderCell(c cell) bool { return c.Ch == kgpPlaceholderRune }
