// Command genthemes converts the Ghostty-format theme files shipped by
// mbadolato/iTerm2-Color-Schemes into go-term's compact bundled-theme table
// (term/themes_bundled.txt) and the user-facing name list (docs/themes.md).
//
// It reads a local clone; it never touches the network, so a build can never
// depend on GitHub being reachable. The generated .txt is the vendored
// artifact — the upstream sources are not committed. Pass the upstream commit
// SHA so the header records exactly what the table was generated from.
//
// Usage:
//
//	go run ./term/genthemes \
//	    -src /path/to/iTerm2-Color-Schemes/ghostty \
//	    -sha 875a82f0fdc773ae45099ce683a11c56bb0f8b3d
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Ghostty theme files are flat `key = value` text. Only these keys matter;
// cursor-color, selection-background and friends are read by Ghostty but have
// no equivalent in term.Theme, which is deliberately just ANSI + fg/bg.
const (
	keyPalette    = "palette"
	keyBackground = "background"
	keyForeground = "foreground"
)

// theme is the generator's intermediate form: 16 ANSI slots plus fg/bg, each
// stored as a 6-digit lowercase hex string, and a presence bit per slot so an
// incomplete file can be rejected rather than silently emitted with black
// holes in its palette.
type theme struct {
	name string
	ansi [16]string
	seen [16]bool
	fg   string
	bg   string
}

// complete reports whether every one of the 18 colors term.Theme needs was
// present in the source file.
func (t *theme) complete() bool {
	for _, ok := range t.seen {
		if !ok {
			return false
		}
	}
	return t.fg != "" && t.bg != ""
}

// hex emits the 108-character payload: ANSI 0–15, then fg, then bg.
func (t *theme) hex() string {
	var sb strings.Builder
	sb.Grow(18 * 6)
	for _, c := range t.ansi {
		sb.WriteString(c)
	}
	sb.WriteString(t.fg)
	sb.WriteString(t.bg)
	return sb.String()
}

func main() {
	src := flag.String("src", "", "path to the iTerm2-Color-Schemes `ghostty` directory")
	sha := flag.String("sha", "", "upstream commit SHA, recorded in the output header")
	out := flag.String("out", "term/themes_bundled.txt", "generated table path")
	docs := flag.String("docs", "docs/themes.md", "generated theme list path")
	flag.Parse()

	if *src == "" || *sha == "" {
		flag.Usage()
		log.Fatal("genthemes: -src and -sha are required")
	}

	themes, skipped, err := parseDir(*src)
	if err != nil {
		log.Fatalf("genthemes: %v", err)
	}
	if len(themes) == 0 {
		log.Fatalf("genthemes: no usable themes found in %s", *src)
	}

	// Sort case-insensitively so the browser's list order matches the file
	// order and no runtime sort is needed after decoding.
	sort.Slice(themes, func(i, j int) bool {
		a, b := themes[i].name, themes[j].name
		if ai, bi := strings.ToLower(a), strings.ToLower(b); ai != bi {
			return ai < bi
		}
		return a < b
	})

	if err := writeTable(*out, *sha, themes); err != nil {
		log.Fatalf("genthemes: %v", err)
	}
	if err := writeDocs(*docs, *sha, themes); err != nil {
		log.Fatalf("genthemes: %v", err)
	}
	fmt.Printf("genthemes: wrote %d themes to %s (%d skipped as incomplete)\n",
		len(themes), *out, skipped)
}

// parseDir reads every file in dir as a Ghostty theme, returning the complete
// ones plus a count of those rejected for missing colors.
func parseDir(dir string) (themes []*theme, skipped int, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		t, err := parseFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, 0, err
		}
		if !t.complete() || !usableName(t.name) {
			skipped++
			continue
		}
		themes = append(themes, t)
	}
	return themes, skipped, nil
}

// usableName rejects names the table format cannot carry. The name is a
// filename from an upstream clone, so it is not trusted to be well-behaved:
// the table is one theme per line with a TAB between name and payload, and a
// name containing either delimiter would produce a line the decoder either
// drops or, worse, reads as a different theme. Leading/trailing space is
// rejected too — it would be invisible in docs/themes.md and unmatchable from
// a config file.
func usableName(name string) bool {
	return name != "" && name == strings.TrimSpace(name) &&
		!strings.ContainsAny(name, "\t\n\r")
}

// parseFile reads one Ghostty theme file. The theme's display name is the
// filename — upstream has no in-file name field, and the filenames are already
// the names Ghostty shows.
func parseFile(path string) (*theme, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	// Read-only: nothing to flush, so the close error carries no information.
	defer func() { _ = f.Close() }()

	t := &theme{name: filepath.Base(path)}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		switch key {
		case keyBackground:
			t.bg = normHex(val)
		case keyForeground:
			t.fg = normHex(val)
		case keyPalette:
			// `palette = N=#rrggbb` — the value itself is another pair.
			idxStr, colStr, ok := strings.Cut(val, "=")
			if !ok {
				continue
			}
			i, err := strconv.Atoi(strings.TrimSpace(idxStr))
			if err != nil || i < 0 || i > 15 {
				continue // 256-color overrides are not themeable in term.Theme
			}
			if c := normHex(strings.TrimSpace(colStr)); c != "" {
				t.ansi[i], t.seen[i] = c, true
			}
		}
	}
	return t, sc.Err()
}

// normHex reduces `#rrggbb`, `rrggbb` or `#rgb` to six lowercase hex digits,
// returning "" for anything it does not recognise so the caller can treat the
// theme as incomplete rather than emitting garbage.
func normHex(s string) string {
	s = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if len(s) == 3 {
		// Expand #rgb to #rrggbb the way CSS does.
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return ""
	}
	for i := range len(s) {
		if !isHexDigit(s[i]) {
			return ""
		}
	}
	return s
}

func isHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f'
}

// writeTable emits the embedded data file: a comment header carrying the
// upstream attribution and provenance, then one `name<TAB>hex` line per theme.
func writeTable(path, sha string, themes []*theme) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, `# Bundled terminal color themes for go-term. GENERATED FILE — DO NOT EDIT.
#
# Generated by term/genthemes from the Ghostty-format schemes in
# https://github.com/mbadolato/iTerm2-Color-Schemes at commit
# %s
#
# Those schemes are distributed under the MIT License,
# Copyright (c) 2011 to Present Mark Badolato. See docs/themes.md.
#
# Format: one theme per line, "<name>\t<108 hex digits>". The payload is 18
# RGB triples in order: ANSI 0-15, then default foreground, then default
# background. Lines are sorted case-insensitively by name, which is the order
# BundledThemes returns and the theme browser displays.
#
# To regenerate:
#   go run ./term/genthemes -src <clone>/ghostty -sha <commit>
`, sha)
	for _, t := range themes {
		fmt.Fprintf(&sb, "%s\t%s\n", t.name, t.hex())
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

// writeDocs emits the user-facing name list. docs/config.md points here rather
// than enumerating 600 names inline.
func writeDocs(path, sha string, themes []*theme) error {
	var sb strings.Builder
	sb.WriteString("# Bundled themes\n\n")
	sb.WriteString("<!-- GENERATED FILE — DO NOT EDIT. See term/genthemes. -->\n\n")
	fmt.Fprintf(&sb, "go-term ships %d color themes. Press `Cmd+Shift+T` to browse them\n"+
		"with a live preview and filter — that is the intended way to pick one.\n\n", len(themes))
	sb.WriteString("Any name below is also valid for the `theme` key in `[general]`\n" +
		"(see [config.md](config.md)); matching is case-insensitive.\n\n")
	sb.WriteString("## Attribution\n\n")
	sb.WriteString("These themes are generated from the Ghostty-format schemes in\n" +
		"[mbadolato/iTerm2-Color-Schemes](https://github.com/mbadolato/iTerm2-Color-Schemes),\n")
	fmt.Fprintf(&sb, "commit `%s`, distributed under the MIT License,\n"+
		"Copyright (c) 2011 to Present Mark Badolato.\n\n", sha)
	fmt.Fprintf(&sb, "## Names (%d)\n\n", len(themes))
	for _, t := range themes {
		fmt.Fprintf(&sb, "- %s\n", t.name)
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}
