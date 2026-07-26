package recfmt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// FuzzReader checks that no recording — corrupt, truncated, or hostile —
// can make the decoder panic or allocate without bound. Recordings travel in
// bug reports, so they are untrusted input by the time they are read.
func FuzzReader(f *testing.F) {
	hdr := []byte("{\"goterm\":1,\"width\":80,\"height\":24}\n")

	frame := func(kind byte, micros uint64, payload []byte) []byte {
		b := []byte{kind}
		b = binary.AppendUvarint(b, micros)
		b = binary.AppendUvarint(b, uint64(len(payload)))
		return append(b, payload...)
	}
	seeds := [][]byte{
		hdr,
		append(bytes.Clone(hdr), frame('o', 1000, []byte("hello"))...),
		append(bytes.Clone(hdr), frame('i', 0, []byte{0xff, 0xfe})...),
		append(bytes.Clone(hdr), 'r', 0x01, 0x18, 0x50),
		append(bytes.Clone(hdr), 'o', 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f),
		append(bytes.Clone(hdr), frame('o', 0, nil)...),
		[]byte("{\"version\":2}\n[0.0,\"o\",\"hi\"]\n"), // asciicast, not ours
		{},
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		rr, err := NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}
		for range 1000 { // bound the loop; a valid file ends on its own
			fr, err := rr.Next()
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					return
				}
				return // any decode error is a legitimate outcome
			}
			// A frame that decoded must respect the declared bound; anything
			// past it means the length check let an allocation through.
			if len(fr.Data) > MaxFrame {
				t.Fatalf("frame payload %d exceeds MaxFrame %d", len(fr.Data), MaxFrame)
			}
		}
	})
}
