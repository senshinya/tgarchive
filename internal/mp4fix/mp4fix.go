// Package mp4fix makes HEVC MP4s tagged "hev1" playable in Apple's players.
//
// Safari, and every iOS browser (they all use WebKit), refuse HEVC sample entries tagged
// "hev1" but play the same stream tagged "hvc1". The two differ only in where the parameter
// sets may live: hvc1 requires VPS/SPS/PPS in the decoder configuration (hvcC); hev1 also
// allows them in-band. When the hvcC already carries all three, relabelling the entry is
// valid and is all ffmpeg's `-tag:v hvc1` remux does for such files. Only those four bytes
// change; nothing else in the file is touched.
package mp4fix

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxMoov bounds how much of a file is read to inspect its metadata.
const maxMoov = 64 << 20

// visualSampleEntryLen is the fixed part of a VisualSampleEntry after its box header.
const visualSampleEntryLen = 78

// Candidate reports whether a stored file could be an MP4/QuickTime movie worth inspecting.
func Candidate(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4v", ".mov":
		return true
	}
	return false
}

// HEV1ToHVC1 relabels every hev1 sample entry whose hvcC carries VPS, SPS and PPS as hvc1,
// in place. It reports whether the file changed. Files that are not MP4s, or that it cannot
// parse, are left alone and reported unchanged without error; only I/O failures are errors.
func HEV1ToHVC1(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	moovOff, moovLen, ok, err := findTopLevel(f, st.Size(), "moov")
	if err != nil || !ok || moovLen > maxMoov {
		return false, err
	}
	moov := make([]byte, moovLen)
	if _, err := f.ReadAt(moov, moovOff); err != nil {
		return false, err
	}
	var patches []int64 // offsets within moov of "hev1" type fields to rewrite
	walk(moov, 0, int64(len(moov)), &patches)
	for _, p := range patches {
		if _, err := f.WriteAt([]byte("hvc1"), moovOff+p); err != nil {
			return false, err
		}
	}
	return len(patches) > 0, nil
}

// findTopLevel scans the file's top-level boxes for one of type typ.
func findTopLevel(r io.ReaderAt, size int64, typ string) (off, length int64, ok bool, err error) {
	var hdr [16]byte
	for pos := int64(0); pos+8 <= size; {
		if _, err := r.ReadAt(hdr[:8], pos); err != nil {
			return 0, 0, false, err
		}
		n := int64(binary.BigEndian.Uint32(hdr[:4]))
		switch n {
		case 1:
			if pos+16 > size {
				return 0, 0, false, nil
			}
			if _, err := r.ReadAt(hdr[8:16], pos+8); err != nil {
				return 0, 0, false, err
			}
			n = int64(binary.BigEndian.Uint64(hdr[8:16]))
		case 0:
			n = size - pos
		}
		if n < 8 || pos+n > size {
			return 0, 0, false, nil // malformed: not something we should modify
		}
		if string(hdr[4:8]) == typ {
			return pos, n, true, nil
		}
		pos += n
	}
	return 0, 0, false, nil
}

// containers are the boxes on the path moov → trak → mdia → minf → stbl → stsd.
var containers = map[string]bool{"moov": true, "trak": true, "mdia": true, "minf": true, "stbl": true}

// walk visits the boxes in b[start:end], descending into containers and sample descriptions.
func walk(b []byte, start, end int64, patches *[]int64) {
	for pos := start; pos+8 <= end; {
		n, hdr, ok := boxAt(b, pos, end)
		if !ok {
			return
		}
		typ := string(b[pos+4 : pos+8])
		switch {
		case containers[typ]:
			walk(b, pos+hdr, pos+n, patches)
		case typ == "stsd":
			// FullBox: version/flags (4) + entry_count (4), then the sample entries.
			if pos+hdr+8 <= pos+n {
				sampleEntries(b, pos+hdr+8, pos+n, patches)
			}
		}
		pos += n
	}
}

func sampleEntries(b []byte, start, end int64, patches *[]int64) {
	for pos := start; pos+8 <= end; {
		n, hdr, ok := boxAt(b, pos, end)
		if !ok {
			return
		}
		if string(b[pos+4:pos+8]) == "hev1" && hasParameterSets(b, pos+hdr+visualSampleEntryLen, pos+n) {
			*patches = append(*patches, pos+4)
		}
		pos += n
	}
}

// hasParameterSets finds the hvcC among a sample entry's child boxes and reports whether its
// NAL unit arrays include at least one VPS (32), SPS (33) and PPS (34).
func hasParameterSets(b []byte, start, end int64) bool {
	for pos := start; pos+8 <= end; {
		n, hdr, ok := boxAt(b, pos, end)
		if !ok {
			return false
		}
		if string(b[pos+4:pos+8]) == "hvcC" {
			sets, err := nalTypes(b[pos+hdr : pos+n])
			return err == nil && sets[32] && sets[33] && sets[34]
		}
		pos += n
	}
	return false
}

var errShort = errors.New("short hvcC")

// nalTypes returns the NAL unit types present (with at least one unit) in an
// HEVCDecoderConfigurationRecord.
func nalTypes(c []byte) (map[int]bool, error) {
	if len(c) < 23 {
		return nil, errShort
	}
	out := map[int]bool{}
	pos := 23
	for range int(c[22]) {
		if pos+3 > len(c) {
			return nil, errShort
		}
		typ := int(c[pos] & 0x3f)
		count := int(binary.BigEndian.Uint16(c[pos+1 : pos+3]))
		pos += 3
		for range count {
			if pos+2 > len(c) {
				return nil, errShort
			}
			pos += 2 + int(binary.BigEndian.Uint16(c[pos:pos+2]))
			if pos > len(c) {
				return nil, errShort
			}
		}
		if count > 0 {
			out[typ] = true
		}
	}
	return out, nil
}

// boxAt returns the size and header length of the box at pos, bounded by end.
func boxAt(b []byte, pos, end int64) (size, hdr int64, ok bool) {
	size = int64(binary.BigEndian.Uint32(b[pos : pos+4]))
	hdr = 8
	switch size {
	case 1:
		if pos+16 > end {
			return 0, 0, false
		}
		size = int64(binary.BigEndian.Uint64(b[pos+8 : pos+16]))
		hdr = 16
	case 0:
		size = end - pos
	}
	if size < hdr || pos+size > end {
		return 0, 0, false
	}
	return size, hdr, true
}
