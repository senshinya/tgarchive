package collector

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tgarchive/internal/botclients"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/model"
	"tgarchive/internal/receipt"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
)

const token = "777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

var bg = context.Background()

type fakeNotifier struct {
	mu   sync.Mutex
	msgs []string
}

func (n *fakeNotifier) Notify(_ context.Context, title, body string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.msgs = append(n.msgs, title+"|"+body)
}

func (n *fakeNotifier) count() int { n.mu.Lock(); defer n.mu.Unlock(); return len(n.msgs) }

type linkStub struct {
	mu       sync.Mutex
	calls    int
	canFetch bool
	errs     []error      // returned by successive calls before succeeding
	peek     func() int64 // when set, records the persisted offset at each call
	offsets  []int64
}

func (l *linkStub) TryHandle(_ context.Context, _ int64, _ model.Sender, _ *model.Message, canFetch bool) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	l.canFetch = canFetch
	if l.peek != nil {
		l.offsets = append(l.offsets, l.peek())
	}
	if len(l.errs) > 0 {
		err := l.errs[0]
		l.errs = l.errs[1:]
		return false, err
	}
	return true, nil
}

type env struct {
	fake *tgtest.FakeTG
	st   *store.Store
	m    *Manager
	bot  int64
	n    *fakeNotifier
	hub  *events.Hub
}

func setup(t *testing.T, links LinkHandler) *env {
	t.Helper()
	fake := tgtest.New(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	bot, _ := st.UpsertBot(bg, &store.Bot{TgBotID: 777, Username: "archive_bot", TokenEnc: box.Seal([]byte(token)), CreatedAt: 1})
	reg := botclients.New(st, box, fake.URL(), nil)
	hub := events.NewHub()
	n := &fakeNotifier{}
	ctx, cancel := context.WithCancel(bg)
	m := New(ctx, Deps{
		Store: st, Clients: reg, Downloader: downloader.New(st, t.TempDir(), 0, nil), Receipts: receipt.New(st, reg),
		Hub: hub, Notifier: n, Links: links, MediaDir: t.TempDir(), PollTimeoutSec: 1,
	})
	t.Cleanup(func() { m.StopAll(); cancel(); st.Close() })
	return &env{fake: fake, st: st, m: m, bot: bot, n: n, hub: hub}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (e *env) reactions() []string {
	var out []string
	for _, c := range e.fake.Calls("setMessageReaction") {
		r := c.Params["reaction"].([]any)[0].(map[string]any)
		out = append(out, r["emoji"].(string))
	}
	return out
}

func (e *env) offset() int64 {
	b, _ := e.st.GetBot(bg, e.bot)
	return b.UpdateOffset
}

func (e *env) messages(t *testing.T) []store.MessageView {
	chats, _ := e.st.ListChats(bg, 0)
	if len(chats) == 0 {
		return nil
	}
	msgs, err := e.st.ListMessages(bg, chats[0].ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestArchivesWhitelistedText(t *testing.T) {
	e := setup(t, nil)
	e.st.PutWhitelist(bg, store.WhitelistEntry{BotID: e.bot, TgUserID: 42})
	ch, unsub := e.hub.Subscribe()
	defer unsub()
	e.fake.PushMessage(tgtest.TextMsg(1, 42, "hello"))
	e.m.Start(e.bot)
	eventually(t, "👌 reaction", func() bool { r := e.reactions(); return len(r) == 1 && r[0] == "👌" })
	if msgs := e.messages(t); len(msgs) != 1 || msgs[0].Text != "hello" {
		t.Fatalf("messages = %+v", msgs)
	}
	if e.offset() != 2 {
		t.Fatalf("offset = %d", e.offset())
	}
	if b, _ := e.st.GetBot(bg, e.bot); b.Status != store.StatusRunning || !e.m.Running(e.bot) {
		t.Fatalf("status = %s", b.Status)
	}
	got := false
	for !got {
		select {
		case ev := <-ch:
			got = ev.Type == "message.created"
		case <-time.After(2 * time.Second):
			t.Fatal("no message.created event")
		}
	}
}

func TestRejectsStrangers(t *testing.T) {
	e := setup(t, nil)
	e.fake.PushMessage(tgtest.TextMsg(1, 99, "let me in"))
	e.m.Start(e.bot)
	eventually(t, "offset advance", func() bool { return e.offset() == 2 })
	rej, _ := e.st.ListRejected(bg, e.bot)
	if len(rej) != 1 || rej[0].TgUserID != 99 || rej[0].FirstName != "User99" {
		t.Fatalf("rejected = %+v", rej)
	}
	if len(e.messages(t)) != 0 || len(e.reactions()) != 0 || len(e.fake.Calls("sendMessage")) != 0 {
		t.Fatal("strangers must be ignored silently")
	}
}

func TestSkipsUnsupportedUpdates(t *testing.T) {
	e := setup(t, nil)
	e.st.PutWhitelist(bg, store.WhitelistEntry{BotID: e.bot, TgUserID: 42})
	e.fake.PushMessage(`{"message_id":1,"from":{"id":42,"is_bot":false,"first_name":"A"},"chat":{"id":-5,"type":"group","title":"G"},"date":1,"text":"group"}`)
	e.fake.PushMessage(`{"message_id":2,"chat":{"id":-100,"type":"channel"},"date":1,"text":"no sender"}`)
	e.fake.PushMessage(`{"message_id":"not-a-number"}`)
	e.fake.PushMessage(tgtest.TextMsg(4, 42, "valid"))
	e.m.Start(e.bot)
	eventually(t, "valid message archived", func() bool { return len(e.reactions()) == 1 })
	msgs := e.messages(t)
	if len(msgs) != 1 || msgs[0].Text != "valid" || e.offset() != 5 {
		t.Fatalf("messages = %+v offset = %d", msgs, e.offset())
	}
}

func TestEditedMessageUpdates(t *testing.T) {
	e := setup(t, nil)
	e.st.PutWhitelist(bg, store.WhitelistEntry{BotID: e.bot, TgUserID: 42})
	e.fake.PushMessage(tgtest.TextMsg(1, 42, "first"))
	e.m.Start(e.bot)
	eventually(t, "first archived", func() bool { return len(e.messages(t)) == 1 })
	edited := strings.Replace(tgtest.TextMsg(1, 42, "second"), `"date":`, `"edit_date":1759600000,"date":`, 1)
	e.fake.PushEdited(edited)
	eventually(t, "edit applied", func() bool {
		m := e.messages(t)
		return len(m) == 1 && m[0].Text == "second" && m[0].EditDate == 1759600000
	})
}

func TestFatalErrorStopsWorker(t *testing.T) {
	e := setup(t, nil)
	e.fake.FailUpdates(401, "Unauthorized")
	e.m.Start(e.bot)
	eventually(t, "error status", func() bool {
		b, _ := e.st.GetBot(bg, e.bot)
		return b.Status == store.StatusError && strings.Contains(b.LastError, "Unauthorized")
	})
	eventually(t, "worker exit", func() bool { return !e.m.Running(e.bot) })
	if e.n.count() != 1 || !strings.Contains(e.n.msgs[0], "@archive_bot") {
		t.Fatalf("notifications = %v", e.n.msgs)
	}
}

func TestLinkHandlerConsumes(t *testing.T) {
	stub := &linkStub{}
	e := setup(t, stub)
	e.st.PutWhitelist(bg, store.WhitelistEntry{BotID: e.bot, TgUserID: 42, CanFetch: true})
	e.fake.PushMessage(tgtest.TextMsg(1, 42, "https://t.me/c/123/456"))
	e.m.Start(e.bot)
	eventually(t, "offset advance", func() bool { return e.offset() == 2 })
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.calls != 1 || !stub.canFetch || len(e.messages(t)) != 0 {
		t.Fatalf("calls=%d canFetch=%v", stub.calls, stub.canFetch)
	}
}

func TestLinkHandlerErrorRetries(t *testing.T) {
	stub := &linkStub{errs: []error{errors.New("userbot busy")}}
	e := setup(t, stub)
	stub.peek = e.offset
	e.st.PutWhitelist(bg, store.WhitelistEntry{BotID: e.bot, TgUserID: 42, CanFetch: true})
	e.fake.PushMessage(tgtest.TextMsg(1, 42, "https://t.me/c/123/456"))
	e.m.Start(e.bot)
	eventually(t, "offset advance", func() bool { return e.offset() == 2 })
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.calls != 2 || stub.offsets[0] != 0 || stub.offsets[1] != 0 || len(e.messages(t)) != 0 {
		t.Fatalf("calls=%d offsets=%v", stub.calls, stub.offsets)
	}
}

func TestStartAllAndStop(t *testing.T) {
	e := setup(t, nil)
	if err := e.m.StartAll(bg); err != nil {
		t.Fatal(err)
	}
	eventually(t, "running", func() bool { return e.m.Running(e.bot) })
	e.m.Stop(e.bot)
	if e.m.Running(e.bot) {
		t.Fatal("Stop must wait for the worker to exit")
	}
}

// Regression: cancelling the worker ctx during startup (Store.GetBot / Clients.Get) must not be
// mistaken for a fatal error. Looped to land on the startup race window.
func TestStopDuringStartupNoFalseAlert(t *testing.T) {
	e := setup(t, nil)
	for i := 0; i < 20; i++ {
		e.m.Start(e.bot)
		e.m.Stop(e.bot)
	}
	if e.n.count() != 0 {
		t.Fatalf("notifications = %v", e.n.msgs)
	}
	if b, _ := e.st.GetBot(bg, e.bot); b.Status == store.StatusError {
		t.Fatalf("status = %s, last_error = %q", b.Status, b.LastError)
	}
}

func TestRemovedBotWorkerExitsQuietly(t *testing.T) {
	e := setup(t, nil)
	if err := e.st.RemoveBot(bg, e.bot); err != nil {
		t.Fatal(err)
	}
	e.m.Start(e.bot)
	eventually(t, "worker exit", func() bool { return !e.m.Running(e.bot) })
	if b, _ := e.st.GetBot(bg, e.bot); b.Status != store.StatusRemoved {
		t.Fatalf("status = %s", b.Status)
	}
	if e.n.count() != 0 {
		t.Fatalf("notifications = %v", e.n.msgs)
	}
	if len(e.fake.Calls("getUpdates")) != 0 {
		t.Fatal("removed bot must never reach the Bot API client")
	}
}

func TestDisabledBotWorkerExitsQuietly(t *testing.T) {
	e := setup(t, nil)
	if err := e.st.SetBotEnabled(bg, e.bot, false); err != nil {
		t.Fatal(err)
	}
	e.m.Start(e.bot)
	eventually(t, "worker exit", func() bool { return !e.m.Running(e.bot) })
	if b, _ := e.st.GetBot(bg, e.bot); b.Status == store.StatusError {
		t.Fatalf("status = %s", b.Status)
	}
	if e.n.count() != 0 {
		t.Fatalf("notifications = %v", e.n.msgs)
	}
}
