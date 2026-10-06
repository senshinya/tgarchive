package transcode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestNeeds(t *testing.T) {
	cases := []struct {
		s    Streams
		want bool
	}{
		{Streams{"h264", "aac"}, false},
		{Streams{"hevc", "aac"}, false},
		{Streams{"vp9", "opus"}, false},
		{Streams{"h264", ""}, false}, // silent clip
		{Streams{"", "aac"}, false},  // audio only
		{Streams{"av1", "aac"}, true},
		{Streams{"mpeg4", "mp3"}, true},
		{Streams{"msmpeg4v3", "mp3"}, true},
		{Streams{"h264", "ac3"}, true},
		{Streams{"h264", "pcm_s16le"}, true},
	}
	for _, c := range cases {
		if got := Needs(c.s); got != c.want {
			t.Errorf("Needs(%+v) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestProbeSkipsCoverArt(t *testing.T) {
	c := &Converter{FFprobe: "ffprobe", Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`{"streams":[{"codec_type":"video","codec_name":"mjpeg"},{"codec_type":"audio","codec_name":"aac"},
			{"codec_type":"video","codec_name":"av1"},{"codec_type":"audio","codec_name":"mp3"}]}`), nil
	}}
	s, err := c.Probe(context.Background(), "x.mp4")
	if err != nil || s != (Streams{"av1", "aac"}) {
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
	if len(calls) != 2 || !slices.Contains(calls[0], "-vaapi_device") || !slices.Contains(calls[1], "libx264") || slices.Contains(calls[1], "-vaapi_device") {
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
	if err != nil || got != (Streams{"h264", "aac"}) {
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
