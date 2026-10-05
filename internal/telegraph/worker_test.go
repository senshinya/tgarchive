package telegraph

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tgarchive/internal/events"
	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

var ctx = context.Background()

type evalRec struct {
	mu  sync.Mutex
	ids []int64
}

func (r *evalRec) Evaluate(_ context.Context, id int64) {
	r.mu.Lock()
	r.ids = append(r.ids, id)
	r.mu.Unlock()
}
func (r *evalRec) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.ids) }

// fakeAPI answers getPage with the next queued response (the last one repeats).
type fakeAPI struct {
	mu      sync.Mutex
	replies []func(w http.ResponseWriter)
	paths   []string
	hook    func()
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path+"?"+r.URL.RawQuery)
	reply := f.replies[0]
	if len(f.replies) > 1 {
		f.replies = f.replies[1:]
	}
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	reply(w)
}

func (f *fakeAPI) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func jsonReply(body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { w.Header().Set("Content-Type", "application/json"); w.Write([]byte(body)) }
}

func statusReply(code int) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { http.Error(w, "upstream down", code) }
}

const okPage = `{"ok":true,"result":{"path":"Sample-10-05","url":"https://telegra.ph/Sample-10-05","title":"Sample",
	"description":"A sample","author_name":"Anon","author_url":"https://t.me/anon","image_url":"https://telegra.ph/file/cover.jpg","views":7,
	"content":[{"tag":"p","children":["Hello"]},
		{"tag":"figure","children":[{"tag":"img","attrs":{"src":"/file/cover.jpg"}},{"tag":"figcaption","children":["cap"]}]},
		{"tag":"video","attrs":{"src":"https://cdn.example.com/v.mp4"}}]}}`

type wenv struct {
	st    *store.Store
	api   *fakeAPI
	rc    *evalRec
	w     *Worker
	msg   int64
	chat  int64
	evs   <-chan events.Event
	wakes int
}

func newWorkerEnv(t *testing.T, replies ...func(http.ResponseWriter)) *wenv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bot, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	res, err := st.Ingest(ctx, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Now: 1, TelegraphPath: "Sample-10-05",
		Msg: &model.Message{TgMessageID: 5, Source: model.SourceBotUpdate, Date: 1, Kind: model.KindText,
			Text: "https://telegra.ph/Sample-10-05", RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{replies: replies}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	hub := events.NewHub()
	evs, unsub := hub.Subscribe()
	t.Cleanup(unsub)
	e := &wenv{st: st, api: api, rc: &evalRec{}, msg: res.MessageID, chat: res.ChatID, evs: evs}
	e.w = NewWorker(st, NewClient(srv.URL), e.rc, hub, func() { e.wakes++ })
	e.w.Delays = []time.Duration{0, 0, 0}
	return e
}

func (e *wenv) updates() int {
	n := 0
	for {
		select {
		case ev := <-e.evs:
			d := ev.Data.(map[string]int64)
			if ev.Type == "message.updated" && d["message_id"] == e.msg && d["chat_id"] == e.chat {
				n++
			}
		default:
			return n
		}
	}
}

func TestWorkerArchivesArticle(t *testing.T) {
	e := newWorkerEnv(t, jsonReply(okPage))
	did, err := e.w.RunOnce(ctx)
	if !did || err != nil {
		t.Fatalf("RunOnce = %v, %v", did, err)
	}
	if got := e.api.calls(); len(got) != 1 || got[0] != "/getPage/Sample-10-05?return_content=true" {
		t.Fatalf("requests = %v", got)
	}
	j, _ := e.st.GetTelegraphJob(ctx, e.msg)
	if j.State != store.TelegraphFetched {
		t.Fatalf("job = %+v", j)
	}
	a, err := e.st.GetArticle(ctx, e.msg)
	if err != nil {
		t.Fatal(err)
	}
	if a.Title != "Sample" || a.URL != "https://telegra.ph/Sample-10-05" || a.AuthorURL != "https://t.me/anon" || a.Views != 7 {
		t.Fatalf("article = %+v", a)
	}
	if len(a.Media) != 2 || a.Media[0].Kind != "photo" || a.Media[1].Kind != "video" {
		t.Fatalf("media = %+v (the cover is the first image, stored once)", a.Media)
	}
	for _, frag := range []string{`"data-src":"https://telegra.ph/file/cover.jpg"`, `"data-media-id":"`, `{"tag":"figcaption","children":["cap"]}`} {
		if !strings.Contains(string(a.Content), frag) {
			t.Fatalf("content missing %s: %s", frag, a.Content)
		}
	}
	v, _ := e.st.GetMessageView(ctx, e.msg)
	if v.Article.State != store.TelegraphFetched || v.Article.Description != "A sample" {
		t.Fatalf("summary = %+v", v.Article)
	}
	due, _ := e.st.DueMedia(ctx, 1<<40, 10)
	if len(due) != 2 || !strings.HasPrefix(due[0].DedupeKey, "web:") || due[0].SourceRef != "https://telegra.ph/file/cover.jpg" {
		t.Fatalf("due media = %+v", due)
	}
	if e.updates() != 2 || e.rc.count() != 1 || e.wakes != 1 {
		t.Fatalf("updates/evaluations/wakes = %d/%d/%d", e.updates(), e.rc.count(), e.wakes)
	}
}

func TestWorkerPageNotFound(t *testing.T) {
	e := newWorkerEnv(t, jsonReply(`{"ok":false,"error":"PAGE_NOT_FOUND"}`))
	e.w.RunOnce(ctx)
	j, _ := e.st.GetTelegraphJob(ctx, e.msg)
	if j.State != store.TelegraphFailed || j.Error != ReasonNotFound || len(e.api.calls()) != 1 {
		t.Fatalf("job = %+v, requests = %d", j, len(e.api.calls()))
	}
	if e.rc.count() != 1 || e.wakes != 0 {
		t.Fatalf("evaluations = %d, wakes = %d", e.rc.count(), e.wakes)
	}
}

func TestWorkerRetriesThenFails(t *testing.T) {
	e := newWorkerEnv(t, statusReply(502), jsonReply("<html>busy</html>"), statusReply(500))
	e.w.RunOnce(ctx)
	j, _ := e.st.GetTelegraphJob(ctx, e.msg)
	if j.State != store.TelegraphFailed || j.Error != ReasonNetwork || len(e.api.calls()) != 4 || j.Attempts != 3 {
		t.Fatalf("job = %+v, requests = %d", j, len(e.api.calls()))
	}
}

func TestWorkerRecoversFromTransientErrors(t *testing.T) {
	e := newWorkerEnv(t, statusReply(503), jsonReply("not json"), jsonReply(okPage))
	e.w.RunOnce(ctx)
	j, _ := e.st.GetTelegraphJob(ctx, e.msg)
	if j.State != store.TelegraphFetched || j.Attempts != 2 || len(e.api.calls()) != 3 {
		t.Fatalf("job = %+v, requests = %d", j, len(e.api.calls()))
	}
}

func TestWorkerAttemptsSurviveRestart(t *testing.T) {
	e := newWorkerEnv(t, statusReply(500))
	j, _ := e.st.ClaimNextTelegraphJob(ctx, 2)
	e.st.SetTelegraphJobAttempts(ctx, j.ID, 3, ReasonNetwork, 2) // three failures before the restart
	c, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { e.w.Run(c); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, _ := e.st.GetTelegraphJob(ctx, e.msg)
		if got.State == store.TelegraphFailed {
			if len(e.api.calls()) != 1 {
				t.Fatalf("requests after restart = %d, want 1 (4th attempt)", len(e.api.calls()))
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("interrupted job not resumed: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkerWake(t *testing.T) {
	e := newWorkerEnv(t, jsonReply(okPage))
	e.w.Poll = time.Hour
	j, _ := e.st.ClaimNextTelegraphJob(ctx, 2)
	e.st.FinishTelegraphJob(ctx, j.ID, store.TelegraphFetched, "", 2) // queue now empty
	c, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { e.w.Run(c); close(done) }()
	defer func() { cancel(); <-done }()
	time.Sleep(50 * time.Millisecond) // let Run go idle
	bot, _ := e.st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	res, _ := e.st.Ingest(ctx, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Now: 3, TelegraphPath: "Other",
		Msg: &model.Message{TgMessageID: 6, Source: model.SourceBotUpdate, Date: 3, Kind: model.KindText, Text: "telegra.ph/Other",
			RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}})
	e.w.Wake()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if got, _ := e.st.GetTelegraphJob(ctx, res.MessageID); got.State == store.TelegraphFetched {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Wake did not start the queued job")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkerMessageDeletedDuringFetch(t *testing.T) {
	e := newWorkerEnv(t, jsonReply(okPage))
	e.api.hook = func() { e.st.DeleteMessage(ctx, e.msg, 9) }
	if did, err := e.w.RunOnce(ctx); !did || err != nil {
		t.Fatalf("RunOnce = %v, %v", did, err)
	}
	if _, err := e.st.GetArticle(ctx, e.msg); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("article = %v", err)
	}
	if due, _ := e.st.DueMedia(ctx, 1<<40, 10); len(due) != 0 {
		t.Fatalf("media created for a deleted message: %+v", due)
	}
	if e.rc.count() != 0 || e.wakes != 0 {
		t.Fatalf("evaluations = %d, wakes = %d", e.rc.count(), e.wakes)
	}
}
