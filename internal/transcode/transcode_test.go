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

const mp4 = "mov,mp4,m4a,3gp,3g2,mj2"

func TestNeeds(t *testing.T) {
	cases := []struct {
		s    Streams
		want bool
	}{
		{Streams{"h264", "aac", mp4}, false},
		{Streams{"hevc", "aac", mp4}, false},
		{Streams{"h264", "", mp4}, false}, // silent clip
		{Streams{"", "aac", mp4}, false},  // audio only
		{Streams{"vp9", "opus", "matroska,webm"}, false},
		{Streams{"av1", "aac", mp4}, true},
		{Streams{"mpeg4", "mp3", mp4}, true},
		{Streams{"mjpeg", "pcm_s16le", "avi"}, true},
		{Streams{"h264", "ac3", mp4}, true},
		{Streams{"h264", "aac", "avi"}, true},
		{Streams{"h264", "aac", "mpegts"}, true},
		{Streams{"h264", "aac", "matroska,webm"}, true}, // MKV: Safari cannot play it
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
	if got := CompatCodec(Streams{"av1", "aac", mp4}); got != "av1" {
		t.Fatalf("av1 = %q", got)
	}
	if got := CompatCodec(Streams{"hevc", "ac3", mp4}); got == "hevc" || !strings.HasPrefix(got, "hevc+ac3") {
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
	if err != nil || s != (Streams{"av1", "aac", mp4}) {
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
