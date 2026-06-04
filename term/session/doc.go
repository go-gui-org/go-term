// Package session manages terminal pane layouts, split trees, and tab state
// for multi-Term windows. It sits above the term package, wiring *term.Term
// instances together through their public API.
//
// The package provides the data model for panes and split trees used by the
// pane manager in the main application layer. Split tree operations (Add,
// Remove, Find, Walk) are pure data-structure operations with no GUI
// dependencies.
package session
