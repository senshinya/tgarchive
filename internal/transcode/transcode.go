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
	"log"
	"os"
	"os/exec"
	"strings"
)

// Streams are the container and the codecs of a file's first video (cover art aside) and first
// audio stream, as ffprobe names them ("" when the file has no such stream).
type Streams struct {
	Video  string
	Audio  string
	Format string
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
	if !playableVideo[s.Video] || !playableAudio[s.Audio] {
		return true
	}
	switch {
	case strings.Contains(s.Format, "mp4"): // ffprobe calls the MP4/MOV family "mov,mp4,m4a,3gp,3g2,mj2"
		return false
	case strings.Contains(s.Format, "webm"): // also what it calls MKV, which only WebM codecs make safe
		return !(s.Video == "vp8" || s.Video == "vp9") || !(s.Audio == "" || s.Audio == "opus" || s.Audio == "vorbis")
	}
	return true // AVI, MPEG-TS, FLV, ...
}

// CompatCodec is what the WebUI is told about a video with a copy: its video codec, which the
// browser may still decode natively, unless the audio (or the container) is what forced the copy
// — then a name no browser check knows, so the copy is always played.
func CompatCodec(s Streams) string {
	if !playableVideo[s.Video] {
		return s.Video
	}
	return s.Video + "+" + s.Audio + "@" + s.Format
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
	out, err := c.Run(ctx, c.FFprobe, "-v", "error", "-show_entries",
		"stream=codec_type,codec_name:stream_disposition=attached_pic:format=format_name", "-of", "json", path)
	if err != nil {
		return Streams{}, err
	}
	var r struct {
		Streams []struct {
			CodecType   string `json:"codec_type"`
			CodecName   string `json:"codec_name"`
			Disposition struct {
				AttachedPic int `json:"attached_pic"`
			} `json:"disposition"`
		} `json:"streams"`
		Format struct {
			FormatName string `json:"format_name"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return Streams{}, fmt.Errorf("ffprobe output: %w", err)
	}
	s := Streams{Format: r.Format.FormatName}
	for _, st := range r.Streams {
		switch {
		case st.CodecType == "video" && s.Video == "" && st.Disposition.AttachedPic == 0: // not cover art
			s.Video = st.CodecName
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
	if hwErr != nil {
		// Software managed what the hardware could not: the device is unusable (driver, group,
		// permissions), so stop paying for a failed attempt on every video. A restart retries it.
		log.Printf("transcode: hardware encoding failed, using software from now on: %v", hwErr)
		c.VAAPIDevice = ""
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
	args = append(args, "-i", src, "-map", "0:V:0", "-map", "0:a:0?", "-sn", "-dn") // V: no cover art
	const even = "scale=trunc(iw/2)*2:trunc(ih/2)*2"
	if vaapi {
		args = append(args, "-vf", even+",format=nv12,hwupload", "-c:v", "h264_vaapi", "-qp", "24")
	} else {
		args = append(args, "-vf", even+",format=yuv420p", "-c:v", "libx264", "-preset", "veryfast", "-crf", "23")
	}
	return append(args, "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", "-f", "mp4", dst)
}
