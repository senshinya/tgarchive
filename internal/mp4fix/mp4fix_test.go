package mp4fix

import (
	"bytes"
	"encoding/binary"
	"math"
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

// largeBox builds a box header using the 64-bit largesize field, followed by payload.
func largeBox(typ string, largesize uint64, payload ...[]byte) []byte {
	out := make([]byte, 16)
	binary.BigEndian.PutUint32(out, 1)
	copy(out[4:], typ)
	binary.BigEndian.PutUint64(out[8:], largesize)
	return append(out, bytes.Join(payload, nil)...)
}

// Box sizes are untrusted: a largesize near MaxInt64 must not wrap the bounds checks into
// passing and send the parser out of range. Any such file is just left alone.
func TestHEV1ToHVC1HugeBoxSizes(t *testing.T) {
	const huge = uint64(math.MaxInt64 - 4)
	cases := map[string][]byte{
		"child in moov":        box("moov", largeBox("free", huge)),
		"child above MaxInt64": box("moov", largeBox("free", math.MaxUint64)),
		"entry in stsd": box("moov", box("trak", box("mdia", box("minf", box("stbl",
			box("stsd", []byte{0, 0, 0, 0, 0, 0, 0, 1}, largeBox("hev1", huge))))))),
		"box in sample entry": box("moov", box("trak", box("mdia", box("minf", box("stbl",
			box("stsd", []byte{0, 0, 0, 0, 0, 0, 0, 1},
				box("hev1", make([]byte, visualSampleEntryLen), largeBox("hvcC", huge)))))))),
		"top level after ftyp":     bytes.Join([][]byte{box("ftyp", []byte("isom")), largeBox("mdat", huge), box("moov")}, nil),
		"top level above MaxInt64": bytes.Join([][]byte{box("ftyp", []byte("isom")), largeBox("mdat", math.MaxUint64), box("moov")}, nil),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeTemp(t, in)
			changed, err := HEV1ToHVC1(p)
			if err != nil || changed {
				t.Fatalf("changed=%v err=%v, want unchanged without error", changed, err)
			}
			if got, _ := os.ReadFile(p); !bytes.Equal(got, in) {
				t.Fatal("malformed file was modified")
			}
		})
	}
}

// FuzzHEV1ToHVC1 feeds arbitrary bytes through the parser. It must never fail on a file it can
// read (a recovered panic surfaces as an error), and may only ever turn "hev1" into "hvc1".
func FuzzHEV1ToHVC1(f *testing.F) {
	f.Add(movie("hev1", hvcC(32, 33, 34, 39)))
	f.Add(movie("hev1", hvcC(32, 33)))
	f.Add(movie("avc1", box("avcC", []byte{1, 2, 3})))
	f.Add(movie("hev1", hvcC(32, 33, 34))[:120])
	f.Add(box("moov", largeBox("free", math.MaxInt64-4)))
	f.Add(bytes.Join([][]byte{box("ftyp", []byte("isom")), largeBox("mdat", math.MaxUint64), box("moov")}, nil))
	f.Add([]byte("hello, this is not a movie at all"))
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, in []byte) {
		p := filepath.Join(dir, "v.mp4")
		if err := os.WriteFile(p, in, 0o644); err != nil {
			t.Fatal(err)
		}
		changed, err := HEV1ToHVC1(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(in) {
			t.Fatalf("length changed: %d -> %d", len(in), len(got))
		}
		if diff := !bytes.Equal(got, in); diff != changed {
			t.Fatalf("changed=%v but contents differ=%v", changed, diff)
		}
		// Undoing every relabel must give back the input exactly.
		if changed && !bytes.Equal(bytes.ReplaceAll(got, []byte("hvc1"), []byte("hev1")), bytes.ReplaceAll(in, []byte("hvc1"), []byte("hev1"))) {
			t.Fatal("bytes other than hev1 tags changed")
		}
	})
}
