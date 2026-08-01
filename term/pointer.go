package term

// pointer.go — OSC 22 mouse cursor shape.
//
// Grid-layer only: the enum and the name table live here so the parser never
// reaches into go-gui. The mapping onto window cursor calls is in
// widget_mouse.go, which is the layer allowed to know go-gui exists.

// pointerShape is the mouse cursor an application requested via OSC 22.
// Named for the pointer to keep it distinct from cursorShape, which is the
// DECSCUSR *text* cursor.
type pointerShape uint8

const (
	pointerDefault pointerShape = iota // no request — the widget decides
	pointerIBeam
	pointerPointingHand
	pointerCrosshair
	pointerArrow // an explicit request for the plain arrow, not "unset"
	pointerAll
	pointerNS
	pointerEW
	pointerNESW
	pointerNWSE
	pointerNotAllowed
)

// pointerNames maps OSC 22 payloads to shapes. Applications send either X11
// cursor-font names (xterm's vocabulary, what the sequence was defined
// against) or CSS names (what newer TUIs assume), so both are accepted for
// every shape that has them.
//
// Deliberately not exhaustive over the X11 cursor font: go-gui exposes ten
// shapes, and mapping the other ~70 X11 names onto them would be guesswork
// that renders as a wrong cursor rather than an obviously ignored one.
var pointerNames = map[string]pointerShape{
	"text":              pointerIBeam,
	"xterm":             pointerIBeam,
	"ibeam":             pointerIBeam,
	"pointer":           pointerPointingHand,
	"hand":              pointerPointingHand,
	"hand1":             pointerPointingHand,
	"hand2":             pointerPointingHand,
	"pointing_hand":     pointerPointingHand,
	"crosshair":         pointerCrosshair,
	"cross":             pointerCrosshair,
	"tcross":            pointerCrosshair,
	"default":           pointerArrow,
	"arrow":             pointerArrow,
	"left_ptr":          pointerArrow,
	"fleur":             pointerAll,
	"move":              pointerAll,
	"all-scroll":        pointerAll,
	"all_scroll":        pointerAll,
	"ns-resize":         pointerNS,
	"n-resize":          pointerNS,
	"s-resize":          pointerNS,
	"row-resize":        pointerNS,
	"sb_v_double_arrow": pointerNS,
	"ew-resize":         pointerEW,
	"e-resize":          pointerEW,
	"w-resize":          pointerEW,
	"col-resize":        pointerEW,
	"sb_h_double_arrow": pointerEW,
	"nesw-resize":       pointerNESW,
	"ne-resize":         pointerNESW,
	"sw-resize":         pointerNESW,
	"nwse-resize":       pointerNWSE,
	"nw-resize":         pointerNWSE,
	"se-resize":         pointerNWSE,
	"not-allowed":       pointerNotAllowed,
	"no-drop":           pointerNotAllowed,
	"x_cursor":          pointerNotAllowed,
	"pirate":            pointerNotAllowed,
}

// maxPointerNameLen is the length of the longest key in pointerNames
// ("sb_v_double_arrow"). OSC payloads run up to maxOSCBytes, and asciiLower
// would copy the whole 4 KB before a map lookup that cannot possibly match —
// so reject on length first and keep a junk payload allocation-free.
const maxPointerNameLen = 17

// parsePointerShape resolves an OSC 22 payload. An empty payload clears the
// request (xterm's documented "reset to default"). An unrecognized name
// returns ok=false and the caller leaves the current shape alone: an app
// asking for an exotic X11 cursor should not have its previous, honored
// request silently revoked.
func parsePointerShape(name string) (pointerShape, bool) {
	if name == "" {
		return pointerDefault, true
	}
	if len(name) > maxPointerNameLen {
		return pointerDefault, false
	}
	s, ok := pointerNames[asciiLower(name)]
	return s, ok
}

// asciiLower lowercases ASCII, returning the input unchanged — and allocating
// nothing — when it is already lowercase, which is the case for every name a
// real application sends. OSC 22 names are ASCII by definition, so this avoids
// pulling strings.ToLower's Unicode path into a sequence that arrives on every
// mouse-mode change some TUIs make.
func asciiLower(s string) string {
	needs := false
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			needs = true
			break
		}
	}
	if !needs {
		return s
	}
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
