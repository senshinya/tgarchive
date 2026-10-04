package userbot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgmock"

	"tgarchive/internal/events"
	"tgarchive/internal/model"
	"tgarchive/internal/receipt"
	"tgarchive/internal/store"
)

type recTransport struct {
	mu    sync.Mutex
	calls []string
}

func (r *recTransport) SetReaction(_ context.Context, _, chatID, msgID int64, emoji string) error {
	r.add(fmt.Sprintf("react %d %d %s", chatID, msgID, emoji))
	return nil
}

func (r *recTransport) Reply(_ context.Context, _, chatID, msgID int64, text string) error {
	r.add(fmt.Sprintf("reply %d %d %s", chatID, msgID, text))
	return nil
}

func (r *recTransport) add(s string) { r.mu.Lock(); r.calls = append(r.calls, s); r.mu.Unlock() }

func (r *recTransport) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.calls
	r.calls = nil
	return out
}

type fakeAPI struct {
	mu     sync.Mutex
	ready  bool
	client *tg.Client
}

func (a *fakeAPI) With(_ context.Context, fn func(*tg.Client) error) error {
	a.mu.Lock()
	ready := a.ready
	a.mu.Unlock()
	if !ready {
		return ErrNotReady
	}
	return fn(a.client)
}

func (a *fakeAPI) WaitReady(context.Context, time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ready
}

// channelTG serves one public channel ("chan", id 500) and its posts.
type channelTG struct {
	*fakeTG
	ch      *tg.Channel
	posts   map[int]*tg.Message
	member  bool    // channel appears in the account's dialogs
	failGet []error // errors returned by successive channels.getMessages calls before succeeding
}

func newChannelTG() *channelTG {
	c := &channelTG{fakeTG: newFakeTG(), posts: map[int]*tg.Message{},
		ch: &tg.Channel{ID: 500, AccessHash: 5005, Title: "Chan", Username: "chan", Photo: &tg.ChatPhotoEmpty{}}}
	c.extra = c.serve
	return c
}

func (c *channelTG) post(id int, text string, group int64) *tg.Message {
	m := &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: 500}, Date: 1700000000 + id, Message: text}
	if group != 0 {
		m.SetGroupedID(group)
	}
	c.posts[id] = m
	return m
}

func (c *channelTG) serve(req bin.Encoder) (bin.Encoder, error) {
	switch r := req.(type) {
	case *tg.ContactsResolveUsernameRequest:
		if r.Username != "chan" {
			return nil, tgerr.New(400, "USERNAME_NOT_OCCUPIED")
		}
		return &tg.ContactsResolvedPeer{Peer: &tg.PeerChannel{ChannelID: 500}, Chats: []tg.ChatClass{c.ch}}, nil
	case *tg.ChannelsGetMessagesRequest:
		if in, ok := r.Channel.(*tg.InputChannel); !ok || in.ChannelID != 500 || in.AccessHash != 5005 {
			return nil, tgerr.New(400, "CHANNEL_INVALID")
		}
		if len(c.failGet) > 0 {
			err := c.failGet[0]
			c.failGet = c.failGet[1:]
			return nil, err
		}
		out := &tg.MessagesChannelMessages{Chats: []tg.ChatClass{c.ch}}
		for _, im := range r.ID {
			id := im.(*tg.InputMessageID).ID
			if p, ok := c.posts[id]; ok {
				out.Messages = append(out.Messages, p)
			} else {
				out.Messages = append(out.Messages, &tg.MessageEmpty{ID: id})
			}
		}
		return out, nil
	case *tg.MessagesGetDialogsRequest:
		out := &tg.MessagesDialogs{}
		if c.member {
			out.Dialogs = []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 500}, TopMessage: 1}}
			out.Messages = []tg.MessageClass{&tg.Message{ID: 1, PeerID: &tg.PeerChannel{ChannelID: 500}, Date: 1}}
			out.Chats = []tg.ChatClass{c.ch}
		}
		return out, nil
	}
	return nil, nil
}

type fetchEnv struct {
	f     *Fetcher
	st    *store.Store
	tr    *recTransport
	tg    *channelTG
	api   *fakeAPI
	bot   int64
	wakes int
}

func newFetchEnv(t *testing.T) *fetchEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bot, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	e := &fetchEnv{st: st, tr: &recTransport{}, tg: newChannelTG(), bot: bot}
	e.api = &fakeAPI{ready: true, client: tg.NewClient(tgmock.Invoker(e.tg.handle))}
	e.f = NewFetcher(e.api, st, receipt.New(st, e.tr), e.tr, events.NewHub(), func() { e.wakes++ }, t.TempDir())
	e.f.Gap, e.f.FloodPad = 0, 0
	return e
}

var me = model.Sender{TgUserID: 42, FirstName: "Shinya"}

func linkMsg(id int64, text string) *model.Message {
	return &model.Message{TgMessageID: id, Source: model.SourceBotUpdate, Kind: model.KindText, Text: text}
}

func (e *fetchEnv) submit(t *testing.T, id int64, link string) {
	t.Helper()
	handled, err := e.f.TryHandle(ctx, e.bot, me, linkMsg(id, link), true)
	if err != nil || !handled {
		t.Fatalf("TryHandle(%s) = %v, %v", link, handled, err)
	}
}

func (e *fetchEnv) runOne(t *testing.T) {
	t.Helper()
	did, err := e.f.RunOnce(ctx)
	if err != nil || !did {
		t.Fatalf("RunOnce = %v, %v", did, err)
	}
}

func (e *fetchEnv) messages(t *testing.T) []store.MessageView {
	t.Helper()
	chats, err := e.st.ListChats(ctx, e.bot)
	if err != nil || len(chats) != 1 {
		t.Fatalf("chats = %+v, %v", chats, err)
	}
	msgs, err := e.st.ListMessages(ctx, chats[0].ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestTryHandleIgnoresNonCandidates(t *testing.T) {
	e := newFetchEnv(t)
	photo := linkMsg(1, "https://t.me/chan/1")
	photo.Kind, photo.Media = model.KindPhoto, []model.Media{{DedupeKey: "bot:x", Role: model.RoleMain}}
	cases := []struct {
		msg      *model.Message
		canFetch bool
	}{
		{linkMsg(1, "https://t.me/chan/1"), false},
		{linkMsg(2, "hello"), true},
		{linkMsg(3, "look https://t.me/chan/1"), true},
		{photo, true},
	}
	for i, c := range cases {
		if handled, err := e.f.TryHandle(ctx, e.bot, me, c.msg, c.canFetch); handled || err != nil {
			t.Fatalf("case %d = %v, %v", i, handled, err)
		}
	}
	if _, err := e.st.ClaimNextFetchJob(ctx, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a job was created: %v", err)
	}
	if got := e.tr.take(); len(got) != 0 {
		t.Fatalf("calls = %v", got)
	}
}

func TestTryHandleIdempotent(t *testing.T) {
	e := newFetchEnv(t)
	e.submit(t, 10, "https://t.me/chan/42")
	e.submit(t, 10, "https://t.me/chan/42")
	if got := e.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👀"}) {
		t.Fatalf("calls = %v", got)
	}
	e.runOne(t)
	if did, _ := e.f.RunOnce(ctx); did {
		t.Fatal("duplicate job queued")
	}
	if snd, err := e.st.GetSender(ctx, 42); err != nil || snd.FirstName != "Shinya" {
		t.Fatalf("sender = %+v, %v", snd, err)
	}
}

func TestUnsupportedLinkRepliesOnce(t *testing.T) {
	e := newFetchEnv(t)
	for i := 0; i < 2; i++ {
		handled, err := e.f.TryHandle(ctx, e.bot, me, linkMsg(11, "https://t.me/joinchat/AbC"), true)
		if handled || err != nil {
			t.Fatalf("TryHandle = %v, %v", handled, err)
		}
	}
	if got := e.tr.take(); !reflect.DeepEqual(got, []string{"reply 42 11 ⚠️ 代取失败：不支持的链接格式"}) {
		t.Fatalf("calls = %v", got)
	}
	if did, _ := e.f.RunOnce(ctx); did {
		t.Fatal("unsupported link was queued")
	}
}

func TestFetchPublicLink(t *testing.T) {
	e := newFetchEnv(t)
	p := e.tg.post(42, "caption", 0)
	p.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 9, AccessHash: 99, FileReference: []byte{1},
		Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "y", W: 1280, H: 960, Size: 900}}}}
	e.submit(t, 10, "https://t.me/chan/42")
	e.tr.take()
	e.runOne(t)
	msgs := e.messages(t)
	if len(msgs) != 1 {
		t.Fatalf("messages = %+v", msgs)
	}
	m := msgs[0]
	if m.Source != model.SourceUserbotFetch || m.TgMessageID != 42 || m.OriginLink != "https://t.me/chan/42" ||
		m.OriginChatTitle != "Chan" || m.Text != "caption" || len(m.Media) != 1 || m.Media[0].State != store.StatePending {
		t.Fatalf("message = %+v", m)
	}
	due, _ := e.st.DueMedia(ctx, 1<<40, 10)
	if len(due) != 1 || due[0].DedupeKey != "mt:photo:9" {
		t.Fatalf("due media = %+v", due)
	}
	if e.wakes != 1 {
		t.Fatalf("downloader wakes = %d", e.wakes)
	}
	if got := e.tr.take(); len(got) != 0 { // 👀 already set at enqueue; 👌 waits for the photo
		t.Fatalf("calls = %v", got)
	}
}

func TestFetchTextPostCompletes(t *testing.T) {
	e := newFetchEnv(t)
	e.tg.post(42, "hello", 0)
	e.submit(t, 10, "t.me/chan/42")
	e.runOne(t)
	if got := e.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👀", "react 42 10 👌"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestFetchAlbum(t *testing.T) {
	e := newFetchEnv(t)
	e.tg.post(40, "a", 7)
	e.tg.post(41, "b", 7)
	e.tg.post(42, "c", 7)
	e.tg.post(43, "other album", 8)
	e.tg.post(44, "plain", 0)
	e.submit(t, 10, "https://t.me/chan/41")
	e.runOne(t)
	var ids []int64
	for _, m := range e.messages(t) {
		ids = append(ids, m.TgMessageID)
	}
	if !reflect.DeepEqual(ids, []int64{40, 41, 42}) {
		t.Fatalf("album ids = %v", ids)
	}
	before := e.tg.called("*tg.ChannelsGetMessagesRequest")
	e.submit(t, 11, "https://t.me/chan/41?single")
	e.runOne(t)
	if n := e.tg.called("*tg.ChannelsGetMessagesRequest") - before; n != 1 {
		t.Fatalf("single link made %d getMessages calls", n)
	}
	if n := len(e.messages(t)); n != 3 {
		t.Fatalf("refetch duplicated messages: %d", n)
	}
}

func TestPrivateLinkNeedsMembership(t *testing.T) {
	e := newFetchEnv(t)
	e.tg.post(42, "secret", 0)
	e.submit(t, 10, "https://t.me/c/500/42")
	e.runOne(t)
	want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 代取失败：私有群/频道，代取账号未加入"}
	if got := e.tr.take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v", got)
	}
	e.tg.set(func(*fakeTG) { e.tg.member = true })
	e.submit(t, 11, "https://t.me/c/500/42")
	e.runOne(t)
	if p, err := e.st.GetPeer(ctx, 500); err != nil || p.AccessHash != 5005 {
		t.Fatalf("peer not cached: %+v, %v", p, err)
	}
	dialogs := e.tg.called("*tg.MessagesGetDialogsRequest")
	e.submit(t, 12, "https://t.me/c/500/42")
	e.runOne(t)
	if e.tg.called("*tg.MessagesGetDialogsRequest") != dialogs {
		t.Fatal("cached peer still triggered getDialogs")
	}
	if msgs := e.messages(t); len(msgs) != 1 || msgs[0].OriginLink != "https://t.me/chan/42" {
		t.Fatalf("messages = %+v", msgs)
	}
}

func TestFetchFailures(t *testing.T) {
	cases := []struct {
		link  string
		setup func(e *fetchEnv)
		want  string
	}{
		{"https://t.me/chan/99", nil, "消息不存在或已被删除"},
		{"https://t.me/nochan/1", nil, "频道或群组不存在"},
		{"https://t.me/chan/42", func(e *fetchEnv) { e.tg.failGet = []error{tgerr.New(420, "FLOOD_WAIT_600")} }, "被限流，请 10 分钟后重试"},
		{"https://t.me/chan/42", func(e *fetchEnv) { e.tg.failGet = []error{tgerr.New(400, "CHANNEL_PRIVATE")} }, "私有群/频道，代取账号未加入"},
		{"https://t.me/chan/42", func(e *fetchEnv) { e.api.ready = false }, "代取账号未登录"},
	}
	for _, c := range cases {
		e := newFetchEnv(t)
		e.tg.post(42, "x", 0)
		if c.setup != nil {
			c.setup(e)
		}
		e.submit(t, 10, c.link)
		e.runOne(t)
		want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 代取失败：" + c.want}
		if got := e.tr.take(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: calls = %v", c.link, got)
		}
	}
}

func TestShortFloodWaitRetries(t *testing.T) {
	e := newFetchEnv(t)
	e.tg.post(42, "x", 0)
	e.tg.failGet = []error{tgerr.New(420, "FLOOD_WAIT_1")}
	e.submit(t, 10, "https://t.me/chan/42")
	start := time.Now()
	e.runOne(t)
	if time.Since(start) < time.Second {
		t.Fatal("did not wait out FLOOD_WAIT_1")
	}
	if got := e.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👀", "react 42 10 👌"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestRunRequeuesInterruptedJob(t *testing.T) {
	e := newFetchEnv(t)
	e.tg.post(42, "x", 0)
	e.st.UpsertSender(ctx, me, 1)
	id, _, _ := e.st.CreateFetchJob(ctx, &store.FetchJob{BotID: e.bot, SenderID: 42, LinkTgMessageID: 10, Link: "https://t.me/chan/42",
		State: store.JobQueued, CreatedAt: 1, UpdatedAt: 1})
	e.st.ClaimNextFetchJob(ctx, 1) // simulate a crash mid-fetch
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.f.Run(runCtx); close(done) }()
	defer func() { cancel(); <-done }()
	eventually(t, "job fetched", func() bool {
		j, _ := e.st.GetFetchJob(ctx, id)
		return j.State == store.JobFetched
	})
	if n := len(e.messages(t)); n != 1 {
		t.Fatalf("messages = %d", n)
	}
}
