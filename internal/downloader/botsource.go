package downloader

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tgarchive/internal/botapifs"
	"tgarchive/internal/store"
	"tgarchive/internal/tgbot"
)

// BotSource fetches "bot:" media through the local Bot API server (--local mode),
// which writes the file into the shared directory and returns its absolute path.
//
// getFile blocks until the Bot API server has the whole file and offers no progress. While it
// downloads, though, TDLib writes the partial file under <dir>/<bot>/temp/ and renames it away
// when done. So each Fetch, while its getFile runs, waits in a queue for a temp file: every file
// that appears there after a Fetch started waiting goes to the longest-waiting Fetch that has
// none yet, and its growing size is reported as that Fetch's progress. getFile itself is never
// held back; a Fetch whose file never shows up just reports no bytes.
type BotSource struct {
	Clients func(ctx context.Context, botID int64) (*tgbot.Client, error)
	Mapper  botapifs.Mapper

	// Poll is how often the temp directories are scanned and claimed files measured.
	Poll time.Duration

	mu      sync.Mutex
	waiting []*tempWaiter   // fetches in progress, oldest first
	seen    map[string]bool // temp files already handed out or present before anyone waited
}

const defaultBotPoll = 500 * time.Millisecond

type tempWaiter struct {
	file string // the claimed temp file; "" until one is assigned
	size int64  // the expected size of the download, 0 when unknown
}

type getFileResult struct {
	f   *tgbot.File
	err error
}

func (b *BotSource) Fetch(ctx context.Context, m *store.Media, dstBase string) (string, int64, error) {
	cl, err := b.Clients(ctx, m.BotID)
	if err != nil {
		return "", 0, err
	}
	res := b.getFileWatching(ctx, cl, m)
	if res.err != nil {
		if ctx.Err() != nil {
			return "", 0, ctx.Err()
		}
		return "", 0, cl.Redact(res.err)
	}
	local, err := b.Mapper.Map(res.f.FilePath)
	if err != nil {
		return "", 0, cl.Redact(err)
	}
	dst := dstBase + strings.ToLower(filepath.Ext(local))
	if err := botapifs.LinkOrCopy(local, dst); err != nil {
		return "", 0, cl.Redact(err)
	}
	st, err := os.Stat(dst)
	if err != nil {
		return "", 0, cl.Redact(err)
	}
	_ = os.Remove(local)
	return dst, st.Size(), nil
}

// getFileWatching runs getFile while reporting the size of its partial file in the temp dir.
func (b *BotSource) getFileWatching(ctx context.Context, cl *tgbot.Client, m *store.Media) getFileResult {
	done := make(chan getFileResult, 1)
	getFile := func() {
		f, err := cl.GetFile(ctx, m.SourceRef)
		done <- getFileResult{f, err}
	}
	if b.Mapper.Local == "" {
		getFile()
		return <-done
	}
	poll := b.Poll
	if poll <= 0 {
		poll = defaultBotPoll
	}
	// Queue up before getFile starts, so its temp file cannot appear before we wait for it.
	w := b.enqueue(m.Size)
	defer b.dequeue(w)
	go getFile()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case r := <-done:
			return r
		case <-tick.C:
			if file := b.assigned(w); file != "" {
				if st, err := os.Stat(file); err == nil {
					done := st.Size()
					if m.Size > 0 {
						done = min(done, m.Size)
					}
					Report(ctx, done, m.Size)
				}
			}
		}
	}
}

// enqueue adds a waiter. Files already in the temp dirs that no earlier waiter can take
// belong to downloads nobody here is waiting for, so they are marked seen.
func (b *BotSource) enqueue(size int64) *tempWaiter {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.assignLocked()
	for p := range b.tempFiles() {
		b.seen[p] = true
	}
	w := &tempWaiter{size: size}
	b.waiting = append(b.waiting, w)
	return w
}

func (b *BotSource) dequeue(w *tempWaiter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, x := range b.waiting {
		if x == w {
			b.waiting = append(b.waiting[:i], b.waiting[i+1:]...)
			break
		}
	}
}

// assigned hands out new temp files and returns the one w holds, if any. A file that has grown
// past w's expected size was not w's after all: w gives it up and waits for another.
func (b *BotSource) assigned(w *tempWaiter) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.assignLocked()
	if w.file != "" && w.size > 0 && fileSize(w.file) > w.size {
		w.file = ""
		b.assignLocked()
	}
	return w.file
}

// assignLocked gives each temp file not seen before, oldest first, to the longest-waiting
// fetch without one; files nobody is waiting for are just marked seen.
func (b *BotSource) assignLocked() {
	if b.seen == nil {
		b.seen = map[string]bool{}
	}
	files := b.tempFiles()
	for p := range b.seen {
		if !files[p] {
			delete(b.seen, p) // renamed away or removed: forget it
		}
	}
	var fresh []string
	for p := range files {
		if !b.seen[p] {
			fresh = append(fresh, p)
		}
	}
	sort.Slice(fresh, func(i, j int) bool { return modTime(fresh[i]).Before(modTime(fresh[j])) })
	for _, p := range fresh {
		b.seen[p] = true
		size := fileSize(p)
		for _, w := range b.waiting {
			if w.file == "" && (w.size == 0 || size <= w.size) {
				w.file = p
				break
			}
		}
	}
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

func modTime(p string) time.Time {
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// tempFiles lists the partial downloads currently in every bot's temp directory.
func (b *BotSource) tempFiles() map[string]bool {
	out := map[string]bool{}
	matches, _ := filepath.Glob(filepath.Join(b.Mapper.Local, "*", "temp", "*"))
	for _, p := range matches {
		out[p] = true
	}
	return out
}
