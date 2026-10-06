// Package transcode makes browser-playable H.264/AAC copies of archived videos whose codecs some
// browser cannot play (AV1 on older iPhones, MPEG-4 Part 2 everywhere, ...). The original is kept
// for download; the copy is only what the WebUI plays.
package transcode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Streams are the codecs of a file's first video and first audio stream, as ffprobe names them
// ("" when the file has no such stream).
type Streams struct {
	Video string
	Audio string
}

// playableVideo and playableAudio are the codecs left as they are. HEVC and VP9 do not play in
// every browser, but where they do the original is better than a re-encode, so only codecs that
// are known not to play (or not on common devices) get a copy.
var (
	playableVideo = map[string]bool{"h264": true, "hevc": true, "vp8": true, "vp9": true}
	playableAudio = map[string]bool{"": true, "aac": true, "mp3": true, "opus": true, "vorbis": true, "flac": true}
)

// Needs reports whether a file with these streams needs a browser-playable copy.
func Needs(s Streams) bool {
	if s.Video == "" {
		return false // audio only, or unreadable: nothing a video copy would fix
	}
	return !playableVideo[s.Video] || !playableAudio[s.Audio]
}

// Runner runs a command and returns its stdout; stderr goes into the error.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		return nil, fmt.Errorf("%s: %w: %s", name, err, msg)
	}
	return stdout.Bytes(), nil
}

// Converter probes and converts files with ffprobe / ffmpeg.
type Converter struct {
	FFmpeg  string
	FFprobe string
	// VAAPIDevice is the render node for hardware H.264 encoding; empty, or a device that does
	// not exist, means software (libx264) only.
	VAAPIDevice string
	Run         Runner
}

// NewConverter finds ffmpeg and ffprobe on PATH; it returns nil when either is missing.
func NewConverter(vaapiDevice string) *Converter {
	ff, err1 := exec.LookPath("ffmpeg")
	fp, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		return nil
	}
	if vaapiDevice != "" {
		if _, err := os.Stat(vaapiDevice); err != nil {
			vaapiDevice = ""
		}
	}
	return &Converter{FFmpeg: ff, FFprobe: fp, VAAPIDevice: vaapiDevice, Run: execRunner}
}

// Probe reads the codecs of path's first video and first audio stream.
func (c *Converter) Probe(ctx context.Context, path string) (Streams, error) {
	out, err := c.Run(ctx, c.FFprobe, "-v", "error", "-show_entries", "stream=codec_type,codec_name", "-of", "json", path)
	if err != nil {
		return Streams{}, err
	}
	var r struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return Streams{}, fmt.Errorf("ffprobe output: %w", err)
	}
	var s Streams
	for _, st := range r.Streams {
		switch {
		case st.CodecType == "video" && s.Video == "" && st.CodecName != "mjpeg" && st.CodecName != "png":
			s.Video = st.CodecName // skip cover art, which ffprobe lists as a video stream
		case st.CodecType == "audio" && s.Audio == "":
			s.Audio = st.CodecName
		}
	}
	return s, nil
}

// Convert writes an H.264/AAC MP4 of src to dst, with the index up front so it plays while
// loading. It tries the VAAPI encoder first when one is configured and falls back to libx264.
func (c *Converter) Convert(ctx context.Context, src, dst string) error {
	var hwErr error
	if c.VAAPIDevice != "" {
		if hwErr = c.convert(ctx, src, dst, true); hwErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	err := c.convert(ctx, src, dst, false)
	if err != nil && hwErr != nil {
		return errors.Join(err, fmt.Errorf("vaapi: %w", hwErr))
	}
	return err
}

func (c *Converter) convert(ctx context.Context, src, dst string, vaapi bool) error {
	_, err := c.Run(ctx, c.FFmpeg, ffmpegArgs(src, dst, c.VAAPIDevice, vaapi)...)
	if err != nil {
		os.Remove(dst)
	}
	return err
}

// ffmpegArgs builds the conversion: first video and (optional) first audio stream, even frame
// size (H.264 4:2:0 needs it), 8-bit 4:2:0 output every browser decodes.
func ffmpegArgs(src, dst, device string, vaapi bool) []string {
	args := []string{"-v", "error", "-nostdin", "-y"}
	if vaapi {
		args = append(args, "-vaapi_device", device)
	}
	args = append(args, "-i", src, "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn")
	const even = "scale=trunc(iw/2)*2:trunc(ih/2)*2"
	if vaapi {
		args = append(args, "-vf", even+",format=nv12,hwupload", "-c:v", "h264_vaapi", "-qp", "24")
	} else {
		args = append(args, "-vf", even+",format=yuv420p", "-c:v", "libx264", "-preset", "veryfast", "-crf", "23")
	}
	return append(args, "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", "-f", "mp4", dst)
}
