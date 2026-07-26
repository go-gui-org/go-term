package main

import "testing"

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
	if cfg.Width != windowWidth || cfg.Height != windowHeight {
		t.Errorf("geometry = %dx%d, want %dx%d",
			cfg.Width, cfg.Height, windowWidth, windowHeight)
	}
}

// TestAppCloseWithoutInit covers the teardown path taken when OnInit never
// ran (window creation failed, or --replay short-circuited): run() defers
// a.close() unconditionally, so a nil workspace must not panic.
func TestAppCloseWithoutInit(t *testing.T) {
	a := &app{}
	a.close() // must not panic
}
