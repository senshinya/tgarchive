package mp4fix

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func box(typ string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	out := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(out, uint32(8+len(body)))
	copy(out[4:], typ)
	return append(out, body...)
}

// hvcC builds a decoder configuration record carrying one NAL unit of each given type.
func hvcC(types ...int) []byte {
	c := make([]byte, 23)
	c[22] = byte(len(types))
	for _, t := range types {
		c = append(c, byte(t)|0x80, 0, 1, 0, 2, 0xAA, 0xBB)
	}
	return box("hvcC", c)
}

// movie builds ftyp + mdat + moov with one video track whose sample entry is entryType.
func movie(entryType string, children ...[]byte) []byte {
	entry := box(entryType, make([]byte, visualSampleEntryLen), bytes.Join(children, nil))
	stsd := box("stsd", []byte{0, 0, 0, 0, 0, 0, 0, 1}, entry)
	moov := box("moov", box("trak", box("mdia", box("minf", box("stbl", stsd)))))
	return bytes.Join([][]byte{box("ftyp", []byte("isom\x00\x00\x02\x00")), box("mdat", make([]byte, 64)), moov}, nil)
}

func writeTemp(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "v.mp4")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHEV1ToHVC1(t *testing.T) {
	cases := []struct {
		name    string
		in      []byte
		changed bool
	}{
		{"hev1 with VPS/SPS/PPS", movie("hev1", hvcC(32, 33, 34, 39)), true},
		{"hev1 missing PPS stays", movie("hev1", hvcC(32, 33)), false},
		{"hev1 without hvcC stays", movie("hev1"), false},
		{"already hvc1", movie("hvc1", hvcC(32, 33, 34)), false},
		{"h264", movie("avc1", box("avcC", []byte{1, 2, 3})), false},
		{"not an mp4", []byte("hello, this is not a movie at all"), false},
		{"truncated moov", movie("hev1", hvcC(32, 33, 34))[:120], false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTemp(t, tc.in)
			changed, err := HEV1ToHVC1(p)
			if err != nil {
				t.Fatal(err)
			}
			if changed != tc.changed {
				t.Fatalf("changed = %v, want %v", changed, tc.changed)
			}
			got, _ := os.ReadFile(p)
			want := tc.in
			if tc.changed {
				want = bytes.Replace(tc.in, []byte("hev1"), []byte("hvc1"), 1)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("file contents differ from expected (only the 4-byte tag may change)")
			}
			// Idempotent: a second pass finds nothing to do.
			if again, err := HEV1ToHVC1(p); err != nil || again {
				t.Fatalf("second pass: changed=%v err=%v", again, err)
			}
		})
	}
}

func TestHEV1ToHVC1MissingFile(t *testing.T) {
	if _, err := HEV1ToHVC1(filepath.Join(t.TempDir(), "nope.mp4")); err == nil {
		t.Fatal("want error for a missing file")
	}
}

func TestCandidate(t *testing.T) {
	for p, want := range map[string]bool{"a/b.mp4": true, "x.MOV": true, "y.m4v": true, "z.jpg": false, "w.webm": false, "noext": false} {
		if Candidate(p) != want {
			t.Errorf("Candidate(%q) = %v", p, !want)
		}
	}
}
