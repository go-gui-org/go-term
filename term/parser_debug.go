package term

import (
	"log"
	"os"
	"strings"
)

// Debug diagnostics for malformed escape sequences, gated on
// GOTERM_DEBUG. Quiet by default: library code must not write to the
// embedder's stderr unconditionally. Set GOTERM_DEBUG=1 (any non-empty
// value except "0"/"false"/"off"/"no") while triaging a rendering bug.
var termDebug = parseTermDebugEnv()

// parseTermDebugEnv interprets the GOTERM_DEBUG value. Unset, empty,
// "0", "false", "off" and "no" disable; anything else enables. This
// mirrors parseLatencyEnv's off set in latency.go.
func parseTermDebugEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GOTERM_DEBUG"))) {
	case "", "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// debugf emits a parser diagnostic only when GOTERM_DEBUG enables it.
// Same log-and-continue shape as the boundary logging elsewhere, but
// quiet unless the embedder asked for triage output.
func debugf(format string, args ...any) {
	if termDebug {
		log.Printf("term: debug: "+format, args...)
	}
}
