package transcode

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const mp4 = "mov,mp4,m4a,3gp,3g2,mj2"

func TestNeeds(t *testing.T) {
	cases := []struct {
		s    Streams
		want bool
	}{
		{Streams{Video: "h264", Audio: "aac", Format: mp4}, false},
		{Streams{Video: "hevc", Audio: "aac", Format: mp4}, false},
		{Streams{Video: "h264", Audio: "", Format: mp4}, false}, // silent clip
		{Streams{Video: "", Audio: "aac", Format: mp4}, false},  // audio only
		{Streams{Video: "vp9", Audio: "opus", Format: "matroska,webm"}, false},
		{Streams{Video: "av1", Audio: "aac", Format: mp4}, true},
		{Streams{Video: "mpeg4", Audio: "mp3", Format: mp4}, true},
		{Streams{Video: "mjpeg", Audio: "pcm_s16le", Format: "avi"}, true},
		{Streams{Video: "h264", Audio: "ac3", Format: mp4}, true},
		{Streams{Video: "h264", Audio: "aac", Format: "avi"}, true},
		{Streams{Video: "h264", Audio: "aac", Format: "mpegts"}, true},
		{Streams{Video: "h264", Audio: "aac", Format: "matroska,webm"}, true}, // MKV: Safari cannot play it
	}
	for _, c := range cases {
		if got := Needs(c.s); got != c.want {
			t.Errorf("Needs(%+v) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestCompatCodec(t *testing.T) {
	// A codec the browser may decode itself is passed on; when audio or the container forced the
	// copy, the name matches no browser check so the copy always plays.
	if got := CompatCodec(Streams{Video: "av1", Audio: "aac", Format: mp4}); got != "av1" {
		t.Fatalf("av1 = %q", got)
	}
	if got := CompatCodec(Streams{Video: "hevc", Audio: "ac3", Format: mp4}); got == "hevc" || !strings.HasPrefix(got, "hevc+ac3") {
		t.Fatalf("hevc+ac3 = %q", got)
	}
}

// TestProbeSkipsCoverArt: only the attached-picture flag marks cover art, so a real Motion-JPEG
// clip is still a video.
func TestProbeSkipsCoverArt(t *testing.T) {
	c := &Converter{FFprobe: "ffprobe", Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`{"streams":[{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}},
			{"codec_type":"audio","codec_name":"aac"},{"codec_type":"video","codec_name":"av1","disposition":{"attached_pic":0}},
			{"codec_type":"audio","codec_name":"mp3"}],"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2"}}`), nil
	}}
	s, err := c.Probe(context.Background(), "x.mp4")
	if err != nil || s != (Streams{Video: "av1", Audio: "aac", Format: mp4}) {
		t.Fatalf("Probe = %+v, %v", s, err)
	}
}

func TestConvertFallsBackToSoftware(t *testing.T) {
	var calls [][]string
	c := &Converter{FFmpeg: "ffmpeg", VAAPIDevice: "/dev/dri/renderD128", Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if slices.Contains(args, "h264_vaapi") {
			return nil, errors.New("no va display")
		}
		return nil, nil
	}}
	if err := c.Convert(context.Background(), "in.mp4", "out.part"); err != nil {
		t.Fatal(err)
	}
	if c.VAAPIDevice != "" {
		t.Fatal("a device that failed where software worked must not be tried again")
	}
	if len(calls) != 2 || !slices.Contains(calls[0], "-vaapi_device") && slices.Contains(calls[0], "0:V:0") || !slices.Contains(calls[1], "libx264") || slices.Contains(calls[1], "-vaapi_device") {
		t.Fatalf("calls = %q", calls)
	}
	last := calls[1]
	if last[len(last)-1] != "out.part" || !slices.Contains(last, "+faststart") || !slices.Contains(last, "0:a:0?") {
		t.Fatalf("software args = %q", last)
	}

	c.VAAPIDevice = ""
	calls = nil
	c.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return nil, errors.New("boom")
	}
	if err := c.Convert(context.Background(), "in.mp4", "out.part"); err == nil || len(calls) != 1 {
		t.Fatalf("software only: err = %v, calls = %d", err, len(calls))
	}
}

// TestConvertWithFFmpeg runs the real tools when they are installed: an MPEG-4 Part 2 clip (which
// no browser plays) comes out as H.264/AAC.
func TestConvertWithFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	c := NewConverter("")
	if c == nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.mp4")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=161x121:rate=10:duration=1",
		"-f", "lavfi", "-i", "sine=duration=1", "-c:v", "mpeg4", "-c:a", "mp2", "-shortest", src).CombinedOutput(); err != nil {
		t.Skipf("cannot make a test clip: %v %s", err, out)
	}
	ctx := context.Background()
	s, err := c.Probe(ctx, src)
	if err != nil || s.Video != "mpeg4" || !Needs(s) {
		t.Fatalf("source = %+v, %v", s, err)
	}
	dst := filepath.Join(dir, "out.part")
	if err := c.Convert(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := c.Probe(ctx, dst)
	if err != nil || got.Video != "h264" || got.Audio != "aac" || Needs(got) {
		t.Fatalf("output = %+v, %v", got, err)
	}
	out, _ := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height,pix_fmt", "-of", "csv=p=0", dst).Output()
	if strings.TrimSpace(string(out)) != "160,120,yuv420p" {
		t.Fatalf("odd frame size must be evened out, got %q", out)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatal(err)
	}
}

// The corruption is synthetic: removing only composition offsets from a normal B-frame MP4
// reproduces the archive's non-monotonic presentation timestamps without private media.
func TestMissingCompositionTimes(t *testing.T) {
	c := NewConverter("")
	if c == nil {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good.mp4")
	if out, err := exec.Command(c.FFmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=30:duration=4",
		"-c:v", "libx264", "-bf", "3", "-g", "60", "-x264-params", "b-adapt=0", "-movflags", "+faststart", good).CombinedOutput(); err != nil {
		t.Fatalf("make fixture: %v %s", err, out)
	}
	if s, err := c.Probe(context.Background(), good); err != nil || Needs(s) {
		t.Fatalf("normal B frames: %+v %v", s, err)
	}
	b, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	var walk func(int, int)
	walk = func(start, end int) {
		for pos := start; pos+8 <= end; {
			n := int(binary.BigEndian.Uint32(b[pos : pos+4]))
			if n < 8 || n > end-pos {
				t.Fatal("invalid generated box")
			}
			switch string(b[pos+4 : pos+8]) {
			case "ctts":
				copy(b[pos+4:pos+8], "free")
				changed++
			case "moov", "trak", "mdia", "minf", "stbl":
				walk(pos+8, pos+n)
			}
			pos += n
		}
	}
	walk(0, len(b))
	if changed != 1 {
		t.Fatalf("ctts boxes = %d", changed)
	}
	bad := filepath.Join(dir, "bad.mp4")
	if err := os.WriteFile(bad, b, 0600); err != nil {
		t.Fatal(err)
	}
	before := sha256.Sum256(b)
	s, err := c.Probe(context.Background(), bad)
	if err != nil || !Needs(s) {
		t.Fatalf("missing ctts must require a copy: %+v %v", s, err)
	}
	if CompatCodec(s) == "h264" {
		t.Fatal("timestamp repair must force compatibility playback")
	}
	dst := filepath.Join(dir, "fixed.mp4")
	if err := c.Convert(context.Background(), bad, dst); err != nil {
		t.Fatal(err)
	}
	if s, err := c.Probe(context.Background(), dst); err != nil || Needs(s) {
		t.Fatalf("repaired: %+v %v", s, err)
	}
	after, err := os.ReadFile(bad)
	if err != nil || sha256.Sum256(after) != before {
		t.Fatal("original changed")
	}
}

func TestProbePresentationTimestamps(t *testing.T) {
	normal := func() []map[string]any {
		f := make([]map[string]any, 60)
		for i := range f {
			f[i] = map[string]any{"pts": i * 100, "best_effort_timestamp": i * 100}
		}
		return f
	}
	for _, tc := range []struct {
		name     string
		change   func([]map[string]any)
		bad, err bool
	}{
		{name: "normal B frames"},
		{name: "duplicates", change: func(f []map[string]any) { f[12]["pts"] = 1100 }},
		{name: "sample boundaries", change: func(f []map[string]any) { f[1]["pts"] = -100; f[59]["pts"] = 0 }},
		{name: "isolated jump", change: func(f []map[string]any) { f[20]["pts"] = 0 }},
		{name: "repeated raw PTS despite repaired best effort", change: func(f []map[string]any) {
			for _, i := range []int{12, 20, 30} {
				f[i]["pts"] = 0
			}
		}, bad: true},
		{name: "missing timestamps", change: func(f []map[string]any) {
			for _, x := range f {
				delete(x, "pts")
			}
		}, err: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := normal()
			if tc.change != nil {
				tc.change(frames)
			}
			calls := 0
			c := &Converter{FFprobe: "ffprobe", Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				calls++
				if slices.Contains(args, "-show_frames") {
					i := slices.Index(args, "-select_streams")
					if i < 0 || args[i+1] != "3" {
						t.Fatalf("selected stream: %q", args)
					}
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 31*time.Second {
						t.Fatal("sample must have a bounded deadline")
					}
					return json.Marshal(map[string]any{"frames": frames})
				}
				return []byte(`{"streams":[{"index":0,"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}},{"index":3,"codec_type":"video","codec_name":"h264","has_b_frames":2}],"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2"}}`), nil
			}}
			got, err := c.Probe(context.Background(), "video.mp4")
			if (err != nil) != tc.err || (!tc.err && Needs(got) != tc.bad) {
				t.Fatalf("Probe=%+v err=%v want bad=%v err=%v", got, err, tc.bad, tc.err)
			}
			if calls != 2 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestTimingProbeFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  []byte
		err  error
	}{
		{"tool failure", nil, errors.New("decode failed")}, {"malformed JSON", []byte("{"), nil}, {"no frames", []byte(`{"frames":[]}`), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Converter{FFprobe: "ffprobe", Run: func(context.Context, string, ...string) ([]byte, error) { return tc.out, tc.err }}
			if _, err := c.invalidPTS(context.Background(), "video.mp4", 0); err == nil {
				t.Fatal("probe failure must be reported")
			}
		})
	}
}

func TestTimingProbeCancellation(t *testing.T) {
	c := NewConverter("")
	if c == nil {
		t.Skip("ffprobe not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.invalidPTS(ctx, filepath.Join(t.TempDir(), "missing.mp4"), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestLocalMediaCommandsRestrictInputProtocols(t *testing.T) {
	requireFileOnly := func(t *testing.T, args []string, inputIndex int) {
		t.Helper()
		index := slices.Index(args, "-protocol_whitelist")
		if index < 0 || index+1 >= len(args) || args[index+1] != "file" || index >= inputIndex {
			t.Errorf("local media input must restrict protocols to file before input: %q", args)
		}
	}
	t.Run("metadata and presentation probes", func(t *testing.T) {
		calls := 0
		c := &Converter{FFprobe: "ffprobe", Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			calls++
			requireFileOnly(t, args, len(args)-1)
			if slices.Contains(args, "-show_frames") {
				return []byte(`{"frames":[{"pts":0},{"pts":1},{"pts":2}]}`), nil
			}
			return []byte(`{"streams":[{"index":0,"codec_type":"video","codec_name":"h264","has_b_frames":1}],"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2"}}`), nil
		}}
		if _, err := c.Probe(context.Background(), "local.mp4"); err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatalf("probe calls=%d", calls)
		}
	})
	for _, hardware := range []bool{false, true} {
		t.Run(fmt.Sprintf("convert hardware=%v", hardware), func(t *testing.T) {
			args := ffmpegArgs("local.mp4", "local.compat.mp4", "/dev/dri/renderD128", hardware)
			input := slices.Index(args, "-i")
			if input < 0 {
				t.Fatal("missing input")
			}
			requireFileOnly(t, args, input)
		})
	}
}
