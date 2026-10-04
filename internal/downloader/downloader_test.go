package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
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
	if _, _, err := bad.Fetch(ctx, m, dstBase); err == nil {
		t.Fatal("path outside the shared dir must fail")
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
