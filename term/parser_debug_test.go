package term

import (
	"bytes"
	"log"
	"testing"
)

// badKGPParams triggers the parseIntKV/parseUint32KV diagnostics via
// non-numeric values; see TestSplitKGP_BadNumericParamsNoPanic.
const badKGPParams = "f=abc,s=NaN,v=,i=99999999999999999999,q=x;AAAA"

// captureLog redirects the standard logger into a buffer, restoring it
// on cleanup. Test-only silence assertions use this pattern.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

func TestDebugfQuietByDefault(t *testing.T) {
	old := termDebug
	termDebug = false
	defer func() { termDebug = old }()

	buf := captureLog(t)
	splitKGPPayload([]byte(badKGPParams))
	if buf.Len() != 0 {
		t.Errorf("splitKGPPayload emitted %q with gate off; want silence", buf.String())
	}
}

func TestDebugfEmitsWhenEnabled(t *testing.T) {
	old := termDebug
	termDebug = true
	defer func() { termDebug = old }()

	buf := captureLog(t)
	splitKGPPayload([]byte(badKGPParams))
	if !bytes.Contains(buf.Bytes(), []byte("KGP bad")) {
		t.Errorf("splitKGPPayload emitted %q with gate on; want KGP diagnostic", buf.String())
	}
}

func TestParseTermDebugEnv(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"off", false},
		{"no", false},
		{"1", true},
		{"true", true},
		{"yes", true},
		{"anything-else", true},
	} {
		t.Setenv("GOTERM_DEBUG", tc.in)
		if got := parseTermDebugEnv(); got != tc.want {
			t.Errorf("parseTermDebugEnv(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}
