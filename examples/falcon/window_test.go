package main

import (
	"bytes"
	"image/png"
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// assertSharedWindowCfg checks the fields every falcon window must set the
// same way, whatever it is showing. Both window builders are separate
// literals, so these are asserted per-builder rather than once.
func assertSharedWindowCfg(t *testing.T, cfg gui.WindowCfg) {
	t.Helper()
	if cfg.Width != windowWidth || cfg.Height != windowHeight {
		t.Errorf("geometry = %dx%d, want %dx%d",
			cfg.Width, cfg.Height, windowWidth, windowHeight)
	}
	// An empty IconPNG is not cosmetic-only: the macOS backend then
	// installs go-gui's default icon as the application icon, which
	// overrides the bundle's .icns in the Dock and app switcher, and
	// the Linux/Windows backends fall back to go-gui's default window
	// icon in the taskbar.
	if len(cfg.IconPNG) == 0 {
		t.Error("IconPNG is empty: go-gui's default icon would override the bundle icon")
	}
	// WMClass names the window for X11 window managers: grouping and
	// .desktop-file matching (StartupWMClass). Empty WM_CLASS falls
	// back to the WM's default icon for apps without a .desktop file,
	// and a class that disagrees with the app name splits the window
	// off from the rest of the app's grouping.
	if cfg.WMClass != appName {
		t.Errorf("WMClass = %q, want %q", cfg.WMClass, appName)
	}
}

// TestWindowCfgWiring guards the callbacks the live window depends on.
// Dropping OnCloseRequest is the dangerous regression: the window would
// still open and run, but Cmd+Q would bypass the quit confirmation *and*
// the workspace save, losing the user's layout silently. A missing OnInit
// yields a permanently blank window.
func TestWindowCfgWiring(t *testing.T) {
	a := &app{}
	cfg := a.windowCfg()

	if cfg.OnInit == nil {
		t.Error("OnInit is nil: the window would never build a workspace")
	}
	if cfg.OnCloseRequest == nil {
		t.Error("OnCloseRequest is nil: quit would skip confirm and save")
	}
	if cfg.Title == "" {
		t.Error("Title is empty")
	}
	assertSharedWindowCfg(t, cfg)
}

// TestReplayWindowCfgWiring covers the --replay viewer's window. It is a
// separate WindowCfg literal from windowCfg(), so it regresses
// independently — the icon and the recording name in the title are both
// easy to drop when editing runReplay.
func TestReplayWindowCfgWiring(t *testing.T) {
	called := false
	cfg := replayWindowCfg("/tmp/session.gtr", func(*gui.Window) { called = true })

	if cfg.OnInit == nil {
		t.Fatal("OnInit is nil: the replay window would stay blank")
	}
	cfg.OnInit(nil)
	if !called {
		t.Error("OnInit is not the callback that was passed in")
	}
	// The base name identifies which recording is playing; a full path
	// would overflow the title bar, and no path at all makes concurrent
	// replay windows indistinguishable.
	if !strings.Contains(cfg.Title, "session.gtr") {
		t.Errorf("Title = %q, want it to name the recording", cfg.Title)
	}
	if strings.Contains(cfg.Title, "/tmp") {
		t.Errorf("Title = %q, want the base name and not the full path", cfg.Title)
	}
	assertSharedWindowCfg(t, cfg)
}

// TestAppIconDecodes catches an embed that resolved to the wrong file or a
// truncated asset — the backend silently ignores data NSImage can't decode,
// so a bad asset looks identical to the icon never being set at all. Full
// Decode rather than DecodeConfig: a truncated PNG keeps a valid header, so
// only decoding the pixel data proves the whole asset made it in.
func TestAppIconDecodes(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(appIconPNG))
	if err != nil {
		t.Fatalf("appIconPNG does not decode as PNG: %v", err)
	}
	// Square is the shape every platform expects for an app icon; a
	// non-square asset means the wrong file (e.g. the 1408x768 mockup)
	// got embedded.
	b := img.Bounds()
	if b.Dx() != b.Dy() {
		t.Errorf("icon is %dx%d, want square", b.Dx(), b.Dy())
	}
	// Bound the asset. It is linked into every falcon binary and decoded
	// on every launch, so an oversized swap costs binary size and startup
	// work with no visible benefit — 1024 is the largest slot macOS asks
	// for. These are deliberately loose: they catch a mistake (embedding a
	// print-resolution master), not a few KB of drift.
	const (
		maxDim   = 1024
		maxBytes = 512 << 10
	)
	if b.Dx() > maxDim {
		t.Errorf("icon is %dpx, want <= %d", b.Dx(), maxDim)
	}
	if len(appIconPNG) > maxBytes {
		t.Errorf("icon is %d bytes, want <= %d", len(appIconPNG), maxBytes)
	}
}

// TestAppCloseWithoutInit covers the teardown path taken when OnInit never
// ran (window creation failed, or --replay short-circuited): run() defers
// a.close() unconditionally, so a nil workspace must not panic.
func TestAppCloseWithoutInit(t *testing.T) {
	a := &app{}
	a.close() // must not panic
}
