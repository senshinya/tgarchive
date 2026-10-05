package downloader

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"tgarchive/internal/botapifs"
	"tgarchive/internal/botclients"
	"tgarchive/internal/model"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
)

var ctx = context.Background()

type fakeSource struct {
	mu    sync.Mutex
	errs  []error
	calls int
	hook  func()
}

func (f *fakeSource) Fetch(_ context.Context, m *store.Media, dstBase string) (string, int64, error) {
	f.mu.Lock()
	f.calls++
	var err error
	if len(f.errs) > 0 {
		err, f.errs = f.errs[0], f.errs[1:]
	}
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return "", 0, err
	}
	p := dstBase + ".jpg"
	return p, 4, os.WriteFile(p, []byte("data"), 0o644)
}

type fixture struct {
	st       *store.Store
	bot      int64
	mediaDir string
	settled  []int64
	mu       sync.Mutex
}

func setup(t *testing.T, size int64) (*fixture, *store.IngestResult, *store.Media) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bot, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	msg := &model.Message{TgMessageID: 1, Source: model.SourceBotUpdate, Date: 1, Kind: model.KindPhoto, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`),
		Media: []model.Media{{DedupeKey: "bot:k", SourceRef: "fid", Kind: "photo", Size: size, Role: model.RoleMain}}}
	res, err := st.Ingest(ctx, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Msg: msg, Now: 1})
	if err != nil {
		t.Fatal(err)
	}
	due, _ := st.DueMedia(ctx, 0, 1)
	return &fixture{st: st, bot: bot, mediaDir: t.TempDir()}, res, &due[0]
}

func (f *fixture) newDL(maxBytes int64) *Downloader {
	d := New(f.st, f.mediaDir, maxBytes, func(id int64) { f.mu.Lock(); f.settled = append(f.settled, id); f.mu.Unlock() })
	d.Now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	return d
}

func TestProcessSuccess(t *testing.T) {
	f, _, m := setup(t, 4)
	d := f.newDL(0)
	d.Register("bot", &fakeSource{})
	d.Process(ctx, m)
	got, _ := f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateDone || got.Size != 4 {
		t.Fatalf("media = %+v", got)
	}
	if !regexp.MustCompile(`^\d+/2026/10/[0-9a-f]{40}\.jpg$`).MatchString(got.Path) {
		t.Fatalf("path = %q", got.Path)
	}
	if _, err := os.Stat(filepath.Join(f.mediaDir, got.Path)); err != nil {
		t.Fatal(err)
	}
	if len(f.settled) != 1 || f.settled[0] != m.ID {
		t.Fatalf("settled = %v", f.settled)
	}
}

func TestRetrySchedule(t *testing.T) {
	f, _, m := setup(t, 4)
	d := f.newDL(0)
	boom := errors.New("network down")
	d.Register("bot", &fakeSource{errs: []error{boom, boom, boom, boom}})
	base := d.Now().Unix()
	for i, wantDelay := range []int64{60, 300, 1800} {
		cur, _ := f.st.GetMedia(ctx, m.ID)
		d.Process(ctx, cur)
		got, _ := f.st.GetMedia(ctx, m.ID)
		if got.State != store.StatePending || got.Attempts != i+1 || got.NextAttemptAt != base+wantDelay || got.Error != "network down" {
			t.Fatalf("after failure %d: %+v", i+1, got)
		}
	}
	if len(f.settled) != 0 {
		t.Fatal("retries must not settle")
	}
	cur, _ := f.st.GetMedia(ctx, m.ID)
	d.Process(ctx, cur)
	got, _ := f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateFailed || got.Attempts != 4 || len(f.settled) != 1 {
		t.Fatalf("4th failure: %+v settled=%v", got, f.settled)
	}
}

func TestTooLargeSkipsFetch(t *testing.T) {
	f, _, m := setup(t, 100)
	d := f.newDL(10)
	src := &fakeSource{}
	d.Register("bot", src)
	d.Process(ctx, m)
	got, _ := f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateTooLarge || src.calls != 0 || len(f.settled) != 1 {
		t.Fatalf("media = %+v calls = %d", got, src.calls)
	}
}

func TestDeletedWhileDownloadingRemovesFile(t *testing.T) {
	f, res, m := setup(t, 4)
	d := f.newDL(0)
	d.Register("bot", &fakeSource{hook: func() { f.st.DeleteMessage(ctx, res.MessageID, 1) }})
	d.Process(ctx, m)
	entries, _ := os.ReadDir(filepath.Join(f.mediaDir, "1", "2026", "10"))
	if len(entries) != 0 {
		t.Fatalf("orphaned download left on disk: %v", entries)
	}
}

func TestRunPicksUpWork(t *testing.T) {
	f, _, m := setup(t, 4)
	d := f.newDL(0)
	d.Register("bot", &fakeSource{})
	c, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { d.Run(c); close(done) }()
	d.Wake()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, _ := f.st.GetMedia(ctx, m.ID)
		if got.State == store.StateDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not process pending media")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestBotSource(t *testing.T) {
	fake := tgtest.New(t)
	fake.AddFile("fid", []byte("img!"))
	f, _, m := setup(t, 4)
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	f.st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: box.Seal([]byte("777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")), CreatedAt: 1})
	reg := botclients.New(f.st, box, fake.URL(), nil)
	src := &BotSource{Clients: reg.Get, Mapper: botapifs.Mapper{Remote: fake.RemoteDir, Local: fake.RemoteDir}}
	dstBase := filepath.Join(t.TempDir(), "out")
	p, size, err := src.Fetch(ctx, m, dstBase)
	if err != nil || p != dstBase+".jpg" || size != 4 {
		t.Fatalf("Fetch = %q %d %v", p, size, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "img!" {
		t.Fatalf("content = %q", b)
	}
	matches, _ := filepath.Glob(filepath.Join(fake.RemoteDir, "*", "documents", "fid.jpg"))
	if len(matches) != 0 {
		t.Fatal("bot api cache file must be removed after archiving")
	}
	bad := &BotSource{Clients: reg.Get, Mapper: botapifs.Mapper{Remote: "/nowhere", Local: "/nowhere"}}
	fake.AddFile("fid", []byte("img!"))
	_, _, err = bad.Fetch(ctx, m, dstBase)
	if err == nil {
		t.Fatal("path outside the shared dir must fail")
	}
	if strings.Contains(err.Error(), "777:AAAA") || strings.Contains(err.Error(), "AAAAAAAA") {
		t.Fatalf("Fetch error leaks the bot token: %v", err)
	}
}

func TestFailureErrorIsRedacted(t *testing.T) {
	f, _, m := setup(t, 4)
	d := f.newDL(0)
	tok := "777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	d.Register("bot", &fakeSource{errs: []error{errors.New("open /var/lib/telegram-bot-api/" + tok + "/documents/file_1: no such file")}})
	d.Process(ctx, m)
	got, _ := f.st.GetMedia(ctx, m.ID)
	if strings.Contains(got.Error, "AAAAAAAA") || !strings.Contains(got.Error, "<bot>/documents/file_1") {
		t.Fatalf("stored error = %q", got.Error)
	}
}

func TestRemoveFilesStaysInside(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "keep")
	os.WriteFile(outside, []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("x"), 0o644)
	rel, _ := filepath.Rel(dir, outside)
	RemoveFiles(dir, []string{"a.jpg", rel, "missing.jpg"})
	if _, err := os.Stat(filepath.Join(dir, "a.jpg")); err == nil {
		t.Fatal("a.jpg not removed")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("RemoveFiles escaped mediaDir")
	}
}

// TestWebDownloadsDoNotStarveBotSlots pins 4 slow "web:" media due at once (more than the
// per-source web cap of 2) alongside one "bot:" media in the same batch. Without the cap, Run's
// dispatch loop fills the whole 4-slot pool with blocked web downloads and then blocks
// synchronously trying to hand the bot item a pool slot, so the bot item never gets processed
// while the web items are stuck. With the cap, only 2 web items are dispatched (the rest are
// skipped for this pass, left due for the next one) and the bot item gets a free slot promptly.
func TestWebDownloadsDoNotStarveBotSlots(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bot, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})

	for i := 0; i < 4; i++ {
		msg := &model.Message{TgMessageID: int64(i + 1), Source: model.SourceBotUpdate, Date: 1, Kind: model.KindPhoto, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`),
			Media: []model.Media{{DedupeKey: fmt.Sprintf("web:%d", i), SourceRef: "x", Kind: "photo", Role: model.RoleMain}}}
		if _, err := st.Ingest(ctx, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Msg: msg, Now: 1}); err != nil {
			t.Fatal(err)
		}
	}
	botMsg := &model.Message{TgMessageID: 5, Source: model.SourceBotUpdate, Date: 1, Kind: model.KindPhoto, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`),
		Media: []model.Media{{DedupeKey: "bot:k", SourceRef: "fid", Kind: "photo", Role: model.RoleMain}}}
	if _, err := st.Ingest(ctx, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Msg: botMsg, Now: 1}); err != nil {
		t.Fatal(err)
	}

	due, err := st.DueMedia(ctx, 0, 10)
	if err != nil || len(due) != 5 {
		t.Fatalf("due = %v %v", due, err)
	}
	var botMediaID int64
	for _, m := range due {
		if m.DedupeKey == "bot:k" {
			botMediaID = m.ID
		}
	}
	if botMediaID == 0 {
		t.Fatal("bot media not found among due")
	}

	mediaDir := t.TempDir()
	d := New(st, mediaDir, 0, func(int64) {})
	d.Now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }

	block := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-block:
		default:
			close(block)
		}
	})
	d.Register("web", &fakeSource{hook: func() { <-block }})
	d.Register("bot", &fakeSource{})

	c, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { d.Run(c); close(done) }()
	d.Wake()

	deadline := time.Now().Add(2 * time.Second)
	for {
		got, _ := st.GetMedia(ctx, botMediaID)
		if got.State == store.StateDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bot media was not processed while web downloads were in flight (starved by the shared pool)")
		}
		time.Sleep(20 * time.Millisecond)
	}

	close(block)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not shut down after the blocked web downloads were released")
	}
}

func TestPermanentErrorFailsAtOnce(t *testing.T) {
	f, _, m := setup(t, 4)
	d := f.newDL(0)
	d.Register("bot", &fakeSource{errs: []error{Permanent(errors.New("地址不允许"))}})
	d.Process(ctx, m)
	got, _ := f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateFailed || got.Attempts != 1 || got.Error != "地址不允许" || len(f.settled) != 1 {
		t.Fatalf("media = %+v settled = %v", got, f.settled)
	}
}

// hev1Movie is a minimal MP4 whose only video sample entry is hev1 with VPS/SPS/PPS in hvcC.
func hev1Movie() []byte {
	bx := func(typ string, body ...[]byte) []byte {
		b := bytes.Join(body, nil)
		out := binary.BigEndian.AppendUint32(nil, uint32(8+len(b)))
		return append(append(out, typ...), b...)
	}
	cfg := make([]byte, 23)
	cfg[22] = 3
	for _, typ := range []byte{32, 33, 34} {
		cfg = append(cfg, typ|0x80, 0, 1, 0, 1, 0xAA)
	}
	entry := bx("hev1", make([]byte, 78), bx("hvcC", cfg))
	stsd := bx("stsd", []byte{0, 0, 0, 0, 0, 0, 0, 1}, entry)
	moov := bx("moov", bx("trak", bx("mdia", bx("minf", bx("stbl", stsd)))))
	return bytes.Join([][]byte{bx("ftyp", []byte("isom\x00\x00\x02\x00")), bx("mdat", make([]byte, 16)), moov}, nil)
}

type movieSource struct{}

func (movieSource) Fetch(_ context.Context, _ *store.Media, dstBase string) (string, int64, error) {
	b := hev1Movie()
	p := dstBase + ".mp4"
	return p, int64(len(b)), os.WriteFile(p, b, 0o644)
}

func TestProcessRelabelsHEV1(t *testing.T) {
	f, _, m := setup(t, 4)
	d := f.newDL(0)
	d.Register("bot", movieSource{})
	d.Process(ctx, m)
	got, _ := f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateDone {
		t.Fatalf("media = %+v", got)
	}
	b, err := os.ReadFile(filepath.Join(f.mediaDir, got.Path))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("hev1")) || !bytes.Contains(b, []byte("hvc1")) {
		t.Fatal("downloaded hev1 movie was not relabelled hvc1")
	}
}

// progressSource reports half the file, then waits for release before finishing.
type progressSource struct {
	reported chan struct{}
	release  chan struct{}
}

func (p *progressSource) Fetch(c context.Context, m *store.Media, dstBase string) (string, int64, error) {
	Report(c, 2, 4)
	close(p.reported)
	select {
	case <-p.release:
	case <-c.Done():
		return "", 0, c.Err()
	}
	path := dstBase + ".jpg"
	return path, 4, os.WriteFile(path, []byte("data"), 0o644)
}

func TestRunReportsProgressAndEndsWithEmptyList(t *testing.T) {
	f, _, m := setup(t, 4)
	d := f.newDL(0)
	d.ProgressEvery = 10 * time.Millisecond
	src := &progressSource{reported: make(chan struct{}), release: make(chan struct{})}
	d.Register("bot", src)
	var mu sync.Mutex
	var calls [][]Progress
	d.OnProgress(func(items []Progress, _ int64) {
		mu.Lock()
		calls = append(calls, items)
		mu.Unlock()
	})
	c, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { d.Run(c); close(done) }()
	<-src.reported
	waitFor := func(cond func([][]Progress) bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			ok := cond(calls)
			mu.Unlock()
			if ok {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("progress calls = %+v", calls)
	}
	waitFor(func(cs [][]Progress) bool {
		return len(cs) > 0 && len(cs[len(cs)-1]) == 1 && cs[len(cs)-1][0] == Progress{MediaID: m.ID, Done: 2, Total: 4, StartedAt: d.Now().Unix()}
	})
	close(src.release)
	waitFor(func(cs [][]Progress) bool { return len(cs[len(cs)-1]) == 0 })
	// Once idle, it stays quiet: exactly one empty report.
	mu.Lock()
	n := len(calls)
	mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(calls) != n {
		t.Fatalf("idle downloader kept reporting: %d -> %d", n, len(calls))
	}
	mu.Unlock()
	if len(d.Progress.Snapshot()) != 0 {
		t.Fatal("finished download still tracked")
	}
	cancel()
	<-done
}

func TestProcessUntracksOnFailure(t *testing.T) {
	f, _, m := setup(t, 4)
	d := f.newDL(0)
	d.Register("bot", &fakeSource{errs: []error{errors.New("boom")}})
	d.Process(ctx, m)
	if len(d.Progress.Snapshot()) != 0 {
		t.Fatal("failed download still tracked")
	}
}

// botSourceEnv is a fake Bot API whose getFile is held until released, with a BotSource polling fast.
func botSourceEnv(t *testing.T) (*tgtest.FakeTG, *BotSource, *store.Media, string) {
	t.Helper()
	fake := tgtest.New(t)
	fake.AddFile("fid", []byte("img!"))
	f, _, m := setup(t, 4)
	m.Size = 1000
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	f.st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: box.Seal([]byte("777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")), CreatedAt: 1})
	reg := botclients.New(f.st, box, fake.URL(), nil)
	src := &BotSource{Clients: reg.Get, Mapper: botapifs.Mapper{Remote: fake.RemoteDir, Local: fake.RemoteDir},
		Poll: 5 * time.Millisecond, ClaimTimeout: time.Second}
	temp := filepath.Join(fake.RemoteDir, "777:AAAA", "temp")
	os.MkdirAll(temp, 0o755)
	fake.HoldFiles(true)
	t.Cleanup(func() { fake.HoldFiles(false) }) // runs before the fake server's Close waits on held requests
	return fake, src, m, temp
}

// progressLog collects reports from one Fetch.
type progressLog struct {
	mu   sync.Mutex
	last [2]int64
	n    int
}

func (p *progressLog) ctx() context.Context {
	return WithProgress(ctx, func(done, total int64) {
		p.mu.Lock()
		p.last = [2]int64{done, total}
		p.n++
		p.mu.Unlock()
	})
}

func (p *progressLog) waitFor(t *testing.T, want [2]int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		got := p.last
		p.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t.Fatalf("progress = %v, want %v", p.last, want)
}

func fetchAsync(src *BotSource, c context.Context, m *store.Media, base string) chan error {
	errc := make(chan error, 1)
	go func() {
		_, _, err := src.Fetch(c, m, base)
		errc <- err
	}()
	return errc
}

func TestBotSourceReportsTempFileGrowth(t *testing.T) {
	fake, src, m, temp := botSourceEnv(t)
	stale := filepath.Join(temp, "older")
	os.WriteFile(stale, make([]byte, 900), 0o644) // existed before: never ours
	var log progressLog
	errc := fetchAsync(src, log.ctx(), m, filepath.Join(t.TempDir(), "out"))
	time.Sleep(30 * time.Millisecond)
	part := filepath.Join(temp, "file_1")
	os.WriteFile(part, make([]byte, 100), 0o644)
	log.waitFor(t, [2]int64{100, 1000})
	os.WriteFile(part, make([]byte, 600), 0o644)
	log.waitFor(t, [2]int64{600, 1000})
	fake.HoldFiles(false)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if len(src.claimed) != 0 {
		t.Fatalf("claim not released: %v", src.claimed)
	}
}

func TestBotSourceConcurrentFetchesClaimTheirOwnFiles(t *testing.T) {
	fake, src, m, temp := botSourceEnv(t)
	var a, b progressLog
	errA := fetchAsync(src, a.ctx(), m, filepath.Join(t.TempDir(), "a"))
	time.Sleep(30 * time.Millisecond)
	errB := fetchAsync(src, b.ctx(), m, filepath.Join(t.TempDir(), "b")) // waits at the gate
	time.Sleep(30 * time.Millisecond)
	os.WriteFile(filepath.Join(temp, "file_a"), make([]byte, 10), 0o644)
	a.waitFor(t, [2]int64{10, 1000})
	time.Sleep(30 * time.Millisecond) // B now through the gate, with file_a in its snapshot
	os.WriteFile(filepath.Join(temp, "file_b"), make([]byte, 20), 0o644)
	b.waitFor(t, [2]int64{20, 1000})
	os.WriteFile(filepath.Join(temp, "file_a"), make([]byte, 30), 0o644)
	a.waitFor(t, [2]int64{30, 1000})
	b.waitFor(t, [2]int64{20, 1000})
	fake.HoldFiles(false)
	if err := <-errA; err != nil {
		t.Fatal(err)
	}
	if err := <-errB; err != nil {
		t.Fatal(err)
	}
}

func TestBotSourceGivesUpTheGateWithoutATempFile(t *testing.T) {
	fake, src, m, temp := botSourceEnv(t)
	src.ClaimTimeout = 150 * time.Millisecond
	var a, b progressLog
	errA := fetchAsync(src, a.ctx(), m, filepath.Join(t.TempDir(), "a"))
	time.Sleep(10 * time.Millisecond)
	errB := fetchAsync(src, b.ctx(), m, filepath.Join(t.TempDir(), "b"))
	time.Sleep(220 * time.Millisecond) // A timed out at ~150ms: B holds the gate until ~300ms
	os.WriteFile(filepath.Join(temp, "file_b"), make([]byte, 5), 0o644)
	b.waitFor(t, [2]int64{5, 1000})
	a.mu.Lock()
	if a.n != 0 {
		t.Fatalf("A reported %v after giving up", a.last)
	}
	a.mu.Unlock()
	fake.HoldFiles(false)
	<-errA
	<-errB
}

func TestBotSourceGateHonoursCancellation(t *testing.T) {
	fake, src, m, _ := botSourceEnv(t)
	defer fake.HoldFiles(false)
	errA := fetchAsync(src, ctx, m, filepath.Join(t.TempDir(), "a"))
	time.Sleep(10 * time.Millisecond)
	c, cancel := context.WithCancel(ctx)
	errB := fetchAsync(src, c, m, filepath.Join(t.TempDir(), "b"))
	cancel()
	select {
	case err := <-errB:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Fetch waiting at the gate ignored cancellation")
	}
	fake.HoldFiles(false)
	<-errA
}
