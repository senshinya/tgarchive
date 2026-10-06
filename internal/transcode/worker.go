package transcode

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tgarchive/internal/store"
)

// Store is what the worker needs from the archive.
type Store interface {
	NextCompatJob(ctx context.Context) (*store.CompatJob, error)
	SetCompat(ctx context.Context, id int64, state, codec, path, errMsg string) (bool, error)
	ResetFailedCompat(ctx context.Context) error
}

// Media probes and converts files; *Converter is the real one.
type Media interface {
	Probe(ctx context.Context, path string) (Streams, error)
	Convert(ctx context.Context, src, dst string) error
}

// jobTimeout bounds one conversion, so a pathological file cannot stall the queue for good.
const jobTimeout = 2 * time.Hour

// Worker checks each downloaded video once, one at a time, newest first, and converts those that
// need a browser-playable copy. Existing videos are worked through after a start.
type Worker struct {
	st       Store
	media    Media
	mediaDir string
	onDone   func(mediaID int64)
	wake     chan struct{}
	// Idle is how long Run sleeps when there is nothing to check and nobody wakes it.
	Idle time.Duration
}

// NewWorker returns a worker that stores copies next to the originals under mediaDir and calls
// onDone after a copy is ready.
func NewWorker(st Store, media Media, mediaDir string, onDone func(int64)) *Worker {
	return &Worker{st: st, media: media, mediaDir: mediaDir, onDone: onDone, wake: make(chan struct{}, 1), Idle: 5 * time.Minute}
}

// Wake makes Run look for work now (e.g. after a download finished).
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) Run(ctx context.Context) {
	if err := w.st.ResetFailedCompat(ctx); err != nil && ctx.Err() == nil {
		log.Printf("transcode: reset failed conversions: %v", err)
	}
	for {
		if w.Step(ctx) {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-time.After(w.Idle):
		}
	}
}

// Step handles one unchecked video and reports whether Run should go straight on to the next.
func (w *Worker) Step(ctx context.Context) bool {
	job, err := w.st.NextCompatJob(ctx)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			log.Printf("transcode: next job: %v", err)
		}
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	src := filepath.Join(w.mediaDir, job.Path)
	s, err := w.media.Probe(ctx, src)
	if err != nil {
		if ctx.Err() != nil {
			return false // shutting down: leave it unchecked
		}
		_, err := w.set(ctx, job.ID, store.CompatFailed, "", "", err.Error())
		return err == nil
	}
	if !Needs(s) {
		_, err := w.set(ctx, job.ID, store.CompatNone, s.Video, "", "")
		return err == nil
	}
	rel := strings.TrimSuffix(job.Path, filepath.Ext(job.Path)) + ".compat.mp4"
	dst := filepath.Join(w.mediaDir, rel)
	part := dst + ".part"
	start := time.Now()
	jctx, cancel := context.WithTimeout(ctx, jobTimeout)
	err = w.media.Convert(jctx, src, part)
	cancel()
	if err != nil {
		os.Remove(part)
		if ctx.Err() != nil {
			return false
		}
		log.Printf("transcode: media %d (%s/%s): %v", job.ID, s.Video, s.Audio, err)
		_, err := w.set(ctx, job.ID, store.CompatFailed, s.Video, "", err.Error())
		return err == nil
	}
	if err := os.Rename(part, dst); err != nil {
		os.Remove(part)
		_, err := w.set(ctx, job.ID, store.CompatFailed, s.Video, "", err.Error())
		return err == nil
	}
	ok, err := w.set(ctx, job.ID, store.CompatDone, s.Video, rel, "")
	if !ok {
		os.Remove(dst) // the media was deleted while we converted it, or the record failed
		return err == nil
	}
	log.Printf("transcode: media %d %s/%s -> h264/aac in %s", job.ID, s.Video, s.Audio, time.Since(start).Round(time.Second))
	if w.onDone != nil {
		w.onDone(job.ID)
	}
	return true
}

// set records an outcome; ok is false when the media is gone. A database error is logged and
// returned so Step backs off instead of retrying the same job in a tight loop.
func (w *Worker) set(ctx context.Context, id int64, state, codec, path, errMsg string) (ok bool, err error) {
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	ok, err = w.st.SetCompat(ctx, id, state, codec, path, errMsg)
	if err != nil && ctx.Err() == nil {
		log.Printf("transcode: record media %d: %v", id, err)
	}
	return ok, err
}
