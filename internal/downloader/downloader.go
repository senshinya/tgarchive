// Package downloader moves pending media into the archive with retries and dedupe.
package downloader

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tgarchive/internal/botapifs"
	"tgarchive/internal/store"
)

var ErrTooLarge = errors.New("file exceeds the archive size limit")

// permanentError marks a failure that retrying cannot fix (e.g. a forbidden address).
type permanentError struct{ err error }

func (p *permanentError) Error() string { return p.err.Error() }
func (p *permanentError) Unwrap() error { return p.err }

// Permanent wraps err so Process fails the media at once instead of scheduling retries.
func Permanent(err error) error { return &permanentError{err: err} }

type Source interface {
	// Fetch stores the file for m at dstBase plus an extension of the source's choosing.
	Fetch(ctx context.Context, m *store.Media, dstBase string) (path string, size int64, err error)
}

type Downloader struct {
	st        *store.Store
	mediaDir  string
	maxBytes  int64
	sources   map[string]Source
	onSettled func(mediaID int64)

	Concurrency int
	Delays      []time.Duration
	Now         func() time.Time

	wake     chan struct{}
	mu       sync.Mutex
	inflight map[int64]bool
}

func New(st *store.Store, mediaDir string, maxBytes int64, onSettled func(int64)) *Downloader {
	return &Downloader{
		st: st, mediaDir: mediaDir, maxBytes: maxBytes, sources: map[string]Source{}, onSettled: onSettled,
		Concurrency: 4,
		Delays:      []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute},
		Now:         time.Now,
		wake:        make(chan struct{}, 1),
		inflight:    map[int64]bool{},
	}
}

func (d *Downloader) Register(prefix string, s Source) { d.sources[prefix] = s }

func (d *Downloader) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *Downloader) Run(ctx context.Context) {
	sem := make(chan struct{}, d.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		due, err := d.st.DueMedia(ctx, d.Now().Unix(), 32)
		if err != nil && ctx.Err() == nil {
			log.Printf("downloader: list due media: %v", err)
		}
		for i := range due {
			m := due[i]
			if !d.claim(m.ID) {
				continue
			}
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				d.release(m.ID)
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				defer d.release(m.ID)
				d.Process(ctx, &m)
			}()
		}
		select {
		case <-ctx.Done():
			return
		case <-d.wake:
		case <-tick.C:
		}
	}
}

func (d *Downloader) claim(id int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inflight[id] {
		return false
	}
	d.inflight[id] = true
	return true
}

func (d *Downloader) release(id int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.inflight, id)
}

func (d *Downloader) Process(ctx context.Context, m *store.Media) {
	if d.maxBytes > 0 && m.Size > d.maxBytes {
		d.tooLarge(ctx, m)
		return
	}
	prefix, _, _ := strings.Cut(m.DedupeKey, ":")
	src := d.sources[prefix]
	if src == nil {
		d.retry(ctx, m, fmt.Errorf("no download source for %q", prefix))
		return
	}
	abs := filepath.Join(d.mediaDir, d.relBase(m))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		d.retry(ctx, m, err)
		return
	}
	final, size, err := src.Fetch(ctx, m, abs)
	if err != nil {
		if ctx.Err() != nil {
			return // shutting down: leave it pending for the next start
		}
		if errors.Is(err, ErrTooLarge) {
			d.tooLarge(ctx, m)
			return
		}
		var pe *permanentError
		if errors.As(err, &pe) {
			d.fail(ctx, m, m.Attempts+1, err)
			return
		}
		d.retry(ctx, m, err)
		return
	}
	rel, err := filepath.Rel(d.mediaDir, final)
	if err != nil {
		d.retry(ctx, m, err)
		return
	}
	ok, err := d.st.MarkMediaDone(ctx, m.ID, rel, size)
	if err != nil {
		log.Printf("downloader: mark media %d done: %v", m.ID, err)
		return
	}
	if !ok {
		os.Remove(final) // the owning message was deleted while we downloaded
		return
	}
	d.settle(m.ID)
}

func (d *Downloader) retry(ctx context.Context, m *store.Media, cause error) {
	n := m.Attempts + 1
	if n > len(d.Delays) {
		d.fail(ctx, m, n, cause)
		return
	}
	next := d.Now().Add(d.Delays[n-1]).Unix()
	if err := d.st.MarkMediaRetry(ctx, m.ID, n, next, botapifs.RedactPath(cause.Error())); err != nil {
		log.Printf("downloader: schedule retry for media %d: %v", m.ID, err)
	}
}

func (d *Downloader) fail(ctx context.Context, m *store.Media, attempts int, cause error) {
	if err := d.st.MarkMediaFailed(ctx, m.ID, attempts, botapifs.RedactPath(cause.Error())); err != nil {
		log.Printf("downloader: mark media %d failed: %v", m.ID, err)
		return
	}
	d.settle(m.ID)
}

func (d *Downloader) tooLarge(ctx context.Context, m *store.Media) {
	if err := d.st.MarkMediaTooLarge(ctx, m.ID); err != nil {
		log.Printf("downloader: mark media %d too large: %v", m.ID, err)
		return
	}
	d.settle(m.ID)
}

func (d *Downloader) settle(id int64) {
	if d.onSettled != nil {
		d.onSettled(id)
	}
}

// relBase is <bucket>/<yyyy>/<mm>/<sha1(dedupe_key)> where bucket is the bot id for "bot:" keys.
func (d *Downloader) relBase(m *store.Media) string {
	prefix, _, _ := strings.Cut(m.DedupeKey, ":")
	bucket := prefix
	if prefix == "bot" {
		bucket = strconv.FormatInt(m.BotID, 10)
	}
	now := d.Now().UTC()
	sum := sha1.Sum([]byte(m.DedupeKey))
	return filepath.Join(bucket, fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", int(now.Month())), hex.EncodeToString(sum[:]))
}

// RemoveFiles deletes archive files given as paths relative to mediaDir, refusing anything outside it.
func RemoveFiles(mediaDir string, rels []string) {
	for _, rel := range rels {
		p := filepath.Join(mediaDir, rel)
		r, err := filepath.Rel(mediaDir, p)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			log.Printf("downloader: refusing to remove %q outside media dir", rel)
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("downloader: remove %s: %v", rel, err)
		}
	}
}
