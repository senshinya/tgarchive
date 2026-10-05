package downloader

import (
	"context"
	"io"
	"sort"
	"sync"
	"time"
)

// Progress is one in-flight download. Total is 0 while the size is unknown.
type Progress struct {
	MediaID   int64 `json:"media_id"`
	Done      int64 `json:"done"`
	Total     int64 `json:"total"`
	StartedAt int64 `json:"started_at"`
}

// speedWindow is how far back Tracker looks when it measures throughput.
const speedWindow = 5 * time.Second

type speedSample struct {
	at    time.Time
	bytes int64
}

// Tracker holds the byte progress of every download in flight, in memory only.
type Tracker struct {
	mu      sync.Mutex
	items   map[int64]*Progress
	bytes   int64 // every byte reported since start, across all downloads
	samples []speedSample
}

func NewTracker() *Tracker { return &Tracker{items: map[int64]*Progress{}} }

func (t *Tracker) start(id, total int64, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.items[id] = &Progress{MediaID: id, Total: total, StartedAt: now.Unix()}
}

// update records that download id has done bytes so far, out of total (0 keeps the known total).
func (t *Tracker) update(id, done, total int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := t.items[id]
	if p == nil {
		return
	}
	if done > p.Done {
		t.bytes += done - p.Done
	}
	p.Done = done
	if total > 0 {
		p.Total = total
	}
}

func (t *Tracker) finish(id int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.items, id)
}

// Snapshot returns the downloads in flight, oldest first.
func (t *Tracker) Snapshot() []Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked()
}

func (t *Tracker) snapshotLocked() []Progress {
	out := make([]Progress, 0, len(t.items))
	for _, p := range t.items {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt < out[j].StartedAt
		}
		return out[i].MediaID < out[j].MediaID
	})
	return out
}

// Tick samples throughput at now and returns the downloads in flight with the current speed in
// bytes per second, averaged over the last speedWindow.
func (t *Tracker) Tick(now time.Time) ([]Progress, int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.samples = append(t.samples, speedSample{now, t.bytes})
	cut := 0
	for cut < len(t.samples)-1 && now.Sub(t.samples[cut].at) > speedWindow {
		cut++
	}
	t.samples = t.samples[cut:]
	return t.snapshotLocked(), t.speedLocked()
}

// Speed is the throughput measured by the latest Tick.
func (t *Tracker) Speed() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.speedLocked()
}

func (t *Tracker) speedLocked() int64 {
	if len(t.samples) < 2 {
		return 0
	}
	first, last := t.samples[0], t.samples[len(t.samples)-1]
	dt := last.at.Sub(first.at).Seconds()
	if dt <= 0 {
		return 0
	}
	return int64(float64(last.bytes-first.bytes) / dt)
}

type progressKey struct{}

// ProgressFunc receives a download's bytes done so far and its total (0 when unknown).
type ProgressFunc func(done, total int64)

// WithProgress returns a context through which a Source reports its download's progress.
func WithProgress(ctx context.Context, fn ProgressFunc) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

// Report passes a download's progress to the function in ctx, if any.
func Report(ctx context.Context, done, total int64) {
	if fn, ok := ctx.Value(progressKey{}).(ProgressFunc); ok {
		fn(done, total)
	}
}

type countingWriter struct {
	ctx   context.Context
	w     io.Writer
	n     int64
	total int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	Report(c.ctx, c.n, c.total)
	return n, err
}

// CountingWriter wraps w so every write reports the bytes written so far through ctx.
func CountingWriter(ctx context.Context, w io.Writer, total int64) io.Writer {
	return &countingWriter{ctx: ctx, w: w, total: total}
}
