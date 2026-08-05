package main

import _ "embed"

// appIconPNG is the icon handed to gui.WindowCfg.IconPNG.
//
// This is not redundant with the .app bundle's falcon.icns. The macOS
// backend calls -[NSApplication setApplicationIconImage:] on startup, and
// falls back to go-gui's own default icon when IconPNG is empty — that
// runtime call wins over CFBundleIconFile for the running process, so
// leaving this unset shows the go-gui icon in the Dock and app switcher
// even from a correctly bundled build. On Linux (X11) and Windows the
// backend publishes IconPNG as the taskbar/alt-tab window icon; without
// it the window manager or shell falls back to its default icon, which
// is why a command-line launch would otherwise show the wrong artwork.
//
// 512x512 with the artwork inset to 824/1024 of the canvas, matching the
// padding of the .icns slots so the Dock tile lines up with its neighbors.
//
//go:embed icon/falcon-dock.png
var appIconPNG []byte
