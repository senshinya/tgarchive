package downloader

import (
	"context"
	"os"
	"path/filepath"
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
// downloads, though, TDLib writes the partial file under <dir>/<bot>/temp/. So each Fetch
// watches that directory: the first file to appear there that existed neither before its
// getFile nor belongs to another Fetch is taken to be its download, and its growing size is
// reported as progress. Only one Fetch at a time may be looking for its file (the claim gate),
// so concurrent downloads cannot take each other's; a Fetch that finds nothing within
// ClaimTimeout gives up the gate and reports no bytes.
type BotSource struct {
	Clients func(ctx context.Context, botID int64) (*tgbot.Client, error)
	Mapper  botapifs.Mapper

	// Poll is how often the temp directory is scanned and the claimed file measured.
	Poll time.Duration
	// ClaimTimeout bounds how long a Fetch holds the claim gate looking for its file.
	ClaimTimeout time.Duration

	initOnce sync.Once
	gate     chan struct{} // holds a token while one Fetch is looking for its temp file
	mu       sync.Mutex
	claimed  map[string]bool
}

const (
	defaultBotPoll         = 500 * time.Millisecond
	defaultBotClaimTimeout = 10 * time.Second
)

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
	poll, timeout := b.Poll, b.ClaimTimeout
	if poll <= 0 {
		poll = defaultBotPoll
	}
	if timeout <= 0 {
		timeout = defaultBotClaimTimeout
	}
	b.initOnce.Do(func() {
		b.gate = make(chan struct{}, 1)
		b.claimed = map[string]bool{}
	})
	select {
	case b.gate <- struct{}{}:
	case <-ctx.Done():
		return getFileResult{err: ctx.Err()}
	}
	gated := true
	release := func() {
		if gated {
			gated = false
			<-b.gate
		}
	}
	defer release()
	before := b.tempFiles()

	done := make(chan getFileResult, 1)
	go func() {
		f, err := cl.GetFile(ctx, m.SourceRef)
		done <- getFileResult{f, err}
	}()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	giveUp := time.After(timeout)
	mine := ""
	defer func() {
		if mine != "" {
			b.mu.Lock()
			delete(b.claimed, mine)
			b.mu.Unlock()
		}
	}()
	for {
		select {
		case r := <-done:
			return r
		case <-giveUp:
			release()
			giveUp = nil
		case <-tick.C:
			if mine == "" && gated {
				mine = b.claim(before)
				if mine != "" {
					release()
				}
			}
			if mine != "" {
				if st, err := os.Stat(mine); err == nil {
					Report(ctx, st.Size(), m.Size)
				}
			}
		}
	}
}

// tempFiles lists the partial downloads currently in every bot's temp directory.
func (b *BotSource) tempFiles() map[string]bool {
	out := map[string]bool{}
	if b.Mapper.Local == "" {
		return out
	}
	matches, _ := filepath.Glob(filepath.Join(b.Mapper.Local, "*", "temp", "*"))
	for _, p := range matches {
		out[p] = true
	}
	return out
}

// claim takes the first temp file that is neither in before nor claimed by another Fetch.
func (b *BotSource) claim(before map[string]bool) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var fresh []string
	for p := range b.tempFiles() {
		if !before[p] && !b.claimed[p] {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) == 0 {
		return ""
	}
	// Several at once means downloads started by someone else (another process, a retry in the
	// Bot API server): attributing one would be a guess, so report nothing.
	if len(fresh) > 1 {
		return ""
	}
	b.claimed[fresh[0]] = true
	return fresh[0]
}
