// Package videocover generates small fallback JPEG thumbnails without changing archived videos.
package videocover

import (
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"tgarchive/internal/store"
)

const (
	jobTimeout     = 30 * time.Second
	failureBackoff = time.Hour
)

type Store interface {
	NextVideoCoverJob(context.Context, int64) (*store.VideoCoverJob, error)
	InstallVideoCover(context.Context, store.VideoCoverJob, store.VideoCover) (int64, bool, error)
}

// Worker scans in media-ID order, one process at a time. Failed keys wait at least one hour,
// but the scan cursor moves on so they cannot starve the remaining originals.
type Worker struct {
	st               Store
	ffmpeg, mediaDir string
	onDone           func(int64)
	wake             chan struct{}
	after            int64
	failed           map[string]time.Time
	now              func() time.Time
	timeout          time.Duration
	run              func(context.Context, string, ...string) error
	Idle             time.Duration
}

// NewWorker returns nil when ffmpeg is absent; thumbnail support never prevents app startup.
func NewWorker(st Store, mediaDir string, onDone func(int64)) *Worker {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil
	}
	return newWorker(st, binary, mediaDir, onDone)
}
func newWorker(st Store, binary, mediaDir string, onDone func(int64)) *Worker {
	return &Worker{st: st, ffmpeg: binary, mediaDir: mediaDir, onDone: onDone, wake: make(chan struct{}, 1),
		failed: map[string]time.Time{}, now: time.Now, timeout: jobTimeout, Idle: time.Minute,
		run: func(ctx context.Context, binary string, args ...string) error {
			return exec.CommandContext(ctx, binary, args...).Run()
		}}
}
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if w.Step(ctx) {
			continue
		}
		timer := time.NewTimer(w.Idle)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-w.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// Step attempts at most one original. true advances the current sweep; false waits for a
// download wake or the next periodic sweep. Run and Step are single-worker operations.
func (w *Worker) Step(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	job, err := w.st.NextVideoCoverJob(ctx, w.after)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			log.Printf("video cover: next job: %v", err)
		}
		w.after = 0
		for key, until := range w.failed {
			if !w.now().Before(until) {
				delete(w.failed, key)
			}
		}
		return false
	}
	w.after = job.ID
	now := w.now()
	if until, ok := w.failed[job.DedupeKey]; ok && now.Before(until) {
		return true
	}
	if err := w.process(ctx, *job); err != nil {
		if ctx.Err() != nil {
			return false
		}
		w.failed[job.DedupeKey] = w.now().Add(failureBackoff)
		log.Printf("video cover: media %d: %v", job.ID, err)
	} else {
		delete(w.failed, job.DedupeKey)
	}
	return ctx.Err() == nil
}

func (w *Worker) process(ctx context.Context, job store.VideoCoverJob) error {
	src, err := localFile(w.mediaDir, job.Path)
	if err != nil {
		return err
	}
	rel := store.VideoCoverRel(job.DedupeKey)
	coversDir := filepath.Join(w.mediaDir, "covers")
	if err := os.MkdirAll(coversDir, 0755); err != nil {
		return err
	}
	// Resolve an existing directory too: IsLocal alone cannot reject an escaping symlink.
	coversDir, err = localPath(w.mediaDir, "covers")
	if err != nil {
		return err
	}
	dst := filepath.Join(coversDir, filepath.Base(rel))
	existing, existingErr := localFile(w.mediaDir, rel)
	if cover, err := readJPEG(existing, rel); existingErr == nil && err == nil {
		_, ok, err := w.install(ctx, job, cover)
		if (!ok || err != nil) && job.CoverPath != rel {
			os.Remove(dst)
		}
		return err
	}
	// A private temporary directory gives ffmpeg a non-existent output filename, so it does
	// not need -y or any interactive overwrite prompt. Rename installs only a verified JPEG.
	temporary, err := os.MkdirTemp(coversDir, ".video-cover-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	part := filepath.Join(temporary, "frame.jpg")
	jctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	seek := float64(job.Duration) * 0.1
	if seek < 0 {
		seek = 0
	}
	if seek > 30 {
		seek = 30
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1", "-ss", strconv.FormatFloat(seek, 'f', -1, 64), "-protocol_whitelist", "file", "-i", src,
		"-map", "0:v:0", "-frames:v", "1", "-vf", "scale=640:360:force_original_aspect_ratio=decrease", "-c:v", "mjpeg", "-q:v", "3", "-threads", "1", "-f", "image2", "-update", "1", part}
	if err := w.run(jctx, w.ffmpeg, args...); err != nil {
		return fmt.Errorf("ffmpeg: %w", err)
	}
	if err := jctx.Err(); err != nil {
		return err
	}
	cover, err := readJPEG(part, rel)
	if err != nil {
		return err
	}
	if err := os.Rename(part, dst); err != nil {
		return err
	}
	_, ok, err := w.install(ctx, job, cover)
	if !ok || err != nil {
		os.Remove(dst)
	}
	return err
}
func (w *Worker) install(ctx context.Context, job store.VideoCoverJob, cover store.VideoCover) (int64, bool, error) {
	id, ok, err := w.st.InstallVideoCover(ctx, job, cover)
	if err == nil && ok && w.onDone != nil {
		w.onDone(id)
	}
	return id, ok, err
}
func readJPEG(path, rel string) (store.VideoCover, error) {
	f, err := os.Open(path)
	if err != nil {
		return store.VideoCover{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return store.VideoCover{}, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 10<<20 {
		return store.VideoCover{}, errors.New("invalid JPEG file size")
	}
	config, err := jpeg.DecodeConfig(f)
	if err != nil {
		return store.VideoCover{}, fmt.Errorf("invalid generated JPEG: %w", err)
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 640 || config.Height > 360 {
		return store.VideoCover{}, errors.New("invalid JPEG dimensions")
	}
	// DecodeConfig alone accepts a header whose image data is truncated. Bound dimensions
	// and bytes first, then decode the complete image before marking a thumbnail ready.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return store.VideoCover{}, err
	}
	if _, err := jpeg.Decode(f); err != nil {
		return store.VideoCover{}, fmt.Errorf("incomplete generated JPEG: %w", err)
	}
	return store.VideoCover{Path: rel, Size: info.Size(), Width: config.Width, Height: config.Height}, nil
}
func localPath(dir, rel string) (string, error) {
	if !filepath.IsLocal(rel) {
		return "", errors.New("media path must stay inside media directory")
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return "", errors.New("media symlink escapes media directory")
	}
	return path, nil
}
func localFile(dir, rel string) (string, error) {
	path, err := localPath(dir, rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("media input must be a regular file")
	}
	return path, nil
}
