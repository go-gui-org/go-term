//go:build !windows

package main

// defaultFontFamily is the JetBrains Mono Nerd Font (Mono variant) family
// name as reported by go-glyph's pure-Go go-text discovery on macOS/Linux.
// go-text reads the abbreviated "NF"/"NFM" family from the font's name table
// (not the spelled-out "Nerd Font"), so the request must use that form to
// match; this mirrors font_windows.go's "JetBrainsMono NFM".
const defaultFontFamily = "JetBrainsMono NFM"
