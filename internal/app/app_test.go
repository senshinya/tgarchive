package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/session"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgmock"

	"tgarchive/internal/config"
	"tgarchive/internal/store"
	"tgarchive/internal/tgapp"
	"tgarchive/internal/tgtest"
	"tgarchive/internal/userbot"
)

const token = "777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func cfgFor(fake *tgtest.FakeTG, dataDir string) *config.Config {
	return &config.Config{
		DataDir: dataDir, BotAPIURL: fake.URL(), CloudAPIURL: fake.URL(),
		BotAPIDirRemote: fake.RemoteDir, BotAPIDirLocal: fake.RemoteDir,
		TokenEncKey: bytes.Repeat([]byte{7}, 32), RequireForwardAuth: true, PollTimeoutSec: 1,
	}
}

func start(t *testing.T, cfg *config.Config) *App {
	t.Helper()
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	return a
}

func req(t *testing.T, h http.Handler, method, path string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rd)
	r.Header.Set("Remote-User", "shinya")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func emojis(f *tgtest.FakeTG) []string {
	var out []string
	for _, c := range f.Calls("setMessageReaction") {
		out = append(out, c.Params["reaction"].([]any)[0].(map[string]any)["emoji"].(string))
	}
	return out
}

func firstChatMessages(t *testing.T, h http.Handler) []store.MessageView {
	_, b := req(t, h, "GET", "/api/chats", nil)
	var chats []store.ChatView
	json.Unmarshal(b, &chats)
	if len(chats) == 0 {
		return nil
	}
	_, b = req(t, h, "GET", fmt.Sprintf("/api/chats/%d/messages", chats[0].ID), nil)
	var msgs []store.MessageView
	json.Unmarshal(b, &msgs)
	return msgs
}

func addBotAndWhitelist(t *testing.T, h http.Handler, uid int64) int64 {
	code, b := req(t, h, "POST", "/api/admin/bots", map[string]string{"token": token})
	if code != 200 {
		t.Fatalf("add bot = %d %s", code, b)
	}
	var r struct {
		BotID int64 `json:"bot_id"`
	}
	json.Unmarshal(b, &r)
	if code, b := req(t, h, "PUT", fmt.Sprintf("/api/admin/bots/%d/whitelist/%d", r.BotID, uid), map[string]any{"note": "me"}); code != 204 {
		t.Fatalf("whitelist = %d %s", code, b)
	}
	return r.BotID
}

func TestEndToEnd(t *testing.T) {
	fake := tgtest.New(t)
	fake.AddFile("ph", []byte("PHOTO"))
	fake.AddFile("ph-s", []byte("t"))
	a := start(t, cfgFor(fake, t.TempDir()))
	defer a.Close()
	h := a.Handler

	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest("GET", "/api/bots", nil))
	if unauth.Code != 401 {
		t.Fatalf("unauthenticated = %d", unauth.Code)
	}

	addBotAndWhitelist(t, h, 42)
	fake.PushMessage(tgtest.TextMsg(1, 99, "stranger"))
	fake.PushMessage(tgtest.PhotoMsg(2, 42, "ph"))

	eventually(t, "👌", func() bool {
		e := emojis(fake)
		return len(e) > 0 && e[len(e)-1] == "👌"
	})
	msgs := firstChatMessages(t, h)
	if len(msgs) != 1 || msgs[0].Kind != "photo" || msgs[0].Media[0].State != store.StateDone {
		t.Fatalf("messages = %+v", msgs)
	}
	code, body := req(t, h, "GET", fmt.Sprintf("/media/%d", msgs[0].Media[0].ID), nil)
	if code != 200 || string(body) != "PHOTO" {
		t.Fatalf("media = %d %q", code, body)
	}
	if matches, _ := filepath.Glob(filepath.Join(fake.RemoteDir, "*", "documents", "ph.jpg")); len(matches) != 0 {
		t.Fatal("bot api cache file not cleaned up")
	}
	if len(fake.Calls("sendMessage")) != 0 {
		t.Fatal("no replies expected on success or for strangers")
	}
	_, body = req(t, h, "GET", "/api/admin/bots/1/rejected", nil)
	if !strings.Contains(string(body), `"tg_user_id":99`) {
		t.Fatalf("rejected = %s", body)
	}

	fake.PushMessage(tgtest.PhotoMsg(3, 42, "ph")) // same file again: deduped, no second download
	eventually(t, "second 👌", func() bool {
		n := 0
		for _, e := range emojis(fake) {
			if e == "👌" {
				n++
			}
		}
		return n == 2
	})
	gets := 0
	for _, c := range fake.Calls("getFile") {
		if c.Params["file_id"] == "ph" {
			gets++
		}
	}
	if gets != 1 {
		t.Fatalf("deduped file fetched %d times", gets)
	}
}

func TestRestartResumes(t *testing.T) {
	fake := tgtest.New(t)
	fake.AddFile("ph", []byte("PHOTO"))
	fake.AddFile("ph-s", []byte("t"))
	fake.HoldFiles(true)
	dataDir := t.TempDir()

	a1 := start(t, cfgFor(fake, dataDir))
	addBotAndWhitelist(t, a1.Handler, 42)
	fake.PushMessage(tgtest.PhotoMsg(1, 42, "ph"))
	eventually(t, "👀 before restart", func() bool { e := emojis(fake); return len(e) == 1 && e[0] == "👀" })
	eventually(t, "getFile in flight before restart", func() bool {
		for _, c := range fake.Calls("getFile") {
			if c.Params["file_id"] == "ph" {
				return true
			}
		}
		return false
	})
	a1.Close()

	fake.HoldFiles(false)
	n := len(fake.Calls("getUpdates"))
	a2 := start(t, cfgFor(fake, dataDir))
	defer a2.Close()
	eventually(t, "👌 after restart", func() bool { e := emojis(fake); return len(e) == 2 && e[1] == "👌" })
	eventually(t, "poll after restart", func() bool {
		calls := fake.Calls("getUpdates")
		if len(calls) <= n {
			return false
		}
		sawOffsetTwo := false
		for _, c := range calls[n:] {
			off, _ := c.Params["offset"].(float64)
			if off < 2 {
				return false
			}
			if off == 2 {
				sawOffsetTwo = true
			}
		}
		return sawOffsetTwo
	})
	msgs := firstChatMessages(t, a2.Handler)
	if len(msgs) != 1 || msgs[0].Media[0].State != store.StateDone {
		t.Fatalf("messages after restart = %+v", msgs)
	}
	gets := 0
	for _, c := range fake.Calls("getFile") {
		if c.Params["file_id"] == "ph" {
			gets++
		}
	}
	if gets != 2 {
		t.Fatalf("ph fetched %d times, want 2 (once held/cancelled, once after restart)", gets)
	}
}

// mtDialer is an in-process MTProto account that can see one public channel with one photo post.
type mtDialer struct{ photo []byte }

func (d *mtDialer) handle(req bin.Encoder) (bin.Encoder, error) {
	ch := &tg.Channel{ID: 500, AccessHash: 5005, Title: "Chan", Username: "chan", Photo: &tg.ChatPhotoEmpty{}}
	switch r := req.(type) {
	case *tg.UsersGetUsersRequest:
		return &tg.UserClassVector{Elems: []tg.UserClass{&tg.User{ID: 99, FirstName: "Me", Self: true}}}, nil
	case *tg.ContactsResolveUsernameRequest:
		return &tg.ContactsResolvedPeer{Peer: &tg.PeerChannel{ChannelID: 500}, Chats: []tg.ChatClass{ch}}, nil
	case *tg.ChannelsGetMessagesRequest:
		post := &tg.Message{ID: 42, PeerID: &tg.PeerChannel{ChannelID: 500}, Date: 1700000000, Message: "protected",
			Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 9, AccessHash: 99, FileReference: []byte{1},
				Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "y", W: 1280, H: 960, Size: len(d.photo)}}}}}
		return &tg.MessagesChannelMessages{Messages: []tg.MessageClass{post}, Chats: []tg.ChatClass{ch}}, nil
	case *tg.UploadGetFileRequest:
		if r.Offset > 0 {
			return &tg.UploadFile{Type: &tg.StorageFileJpeg{}}, nil
		}
		return &tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: d.photo}, nil
	}
	return nil, fmt.Errorf("mtDialer: unexpected %T", req)
}

func (d *mtDialer) Dial(ctx context.Context, _ tgapp.Credentials, _ session.Storage, fn func(context.Context, *tg.Client) error) error {
	return fn(ctx, tg.NewClient(tgmock.Invoker(d.handle)))
}

func TestUserbotFetchEndToEnd(t *testing.T) {
	fake := tgtest.New(t)
	cfg := cfgFor(fake, t.TempDir())
	photo := []byte("protected-photo-bytes")
	a, err := newApp(context.Background(), cfg, &mtDialer{photo: photo}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Handler

	if code, b := req(t, h, "PUT", "/api/admin/telegram-app", map[string]any{"api_id": 1, "api_hash": "0123456789abcdef0123456789abcdef"}); code != 204 {
		t.Fatalf("save app creds = %d %s", code, b)
	}
	eventually(t, "userbot ready", func() bool {
		_, b := req(t, h, "GET", "/api/admin/userbot", nil)
		return strings.Contains(string(b), `"state":"ready"`)
	})
	botID := addBotAndWhitelist(t, h, 42)
	if code, b := req(t, h, "PUT", fmt.Sprintf("/api/admin/bots/%d/whitelist/42", botID), map[string]any{"note": "me", "can_fetch": true}); code != 204 {
		t.Fatalf("grant can_fetch = %d %s", code, b)
	}

	fake.PushMessage(tgtest.TextMsg(1, 42, "https://t.me/chan/42"))
	eventually(t, "👀 then 👌 on the link message", func() bool {
		e := emojis(fake)
		return len(e) == 2 && e[0] == "👀" && e[1] == "👌"
	})
	msgs := firstChatMessages(t, h)
	if len(msgs) != 1 {
		t.Fatalf("messages = %+v", msgs)
	}
	m := msgs[0]
	if m.Source != "userbot_fetch" || m.TgMessageID != 42 || m.Text != "protected" || m.OriginLink != "https://t.me/chan/42" ||
		len(m.Media) != 1 || m.Media[0].State != "done" {
		t.Fatalf("message = %+v", m)
	}
	code, body := req(t, h, "GET", fmt.Sprintf("/media/%d", m.Media[0].ID), nil)
	if code != 200 || string(body) != string(photo) {
		t.Fatalf("media = %d %q", code, body)
	}
	for _, c := range fake.Calls("setMessageReaction") {
		if int64(c.Params["message_id"].(float64)) != 1 {
			t.Fatalf("reaction on wrong message: %+v", c.Params)
		}
	}
}

func TestTelegraphEndToEnd(t *testing.T) {
	fake := tgtest.New(t)
	var base string // the fake Telegraph server's URL, also the host of the article's images
	mux := http.NewServeMux()
	mux.HandleFunc("GET /getPage/Sample-10-05", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"ok":true,"result":{"path":"Sample-10-05","url":"https://telegra.ph/Sample-10-05","title":"Sample",
			"author_name":"Anon","image_url":"%[1]s/cover.jpg","views":1,"content":[{"tag":"p","children":["Hello"]},
			{"tag":"img","attrs":{"src":"%[1]s/cover.jpg"}},{"tag":"img","attrs":{"src":"%[1]s/missing.jpg"}}]}}`, base)
	})
	mux.HandleFunc("GET /getPage/Gone", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error":"PAGE_NOT_FOUND"}`)
	})
	mux.HandleFunc("GET /cover.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("COVER"))
	})
	srv := httptest.NewServer(mux) // /missing.jpg answers 404
	defer srv.Close()
	base = srv.URL

	dataDir := t.TempDir()
	cfg := cfgFor(fake, dataDir)
	cfg.TelegraphAPIURL = srv.URL
	// The fake serves images from 127.0.0.1, which the production dial check refuses.
	a, err := newApp(context.Background(), cfg, userbot.GotdDialer{}, func(net.IP) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Handler
	addBotAndWhitelist(t, h, 42)

	fake.PushMessage(tgtest.TextMsg(1, 42, "https://telegra.ph/Sample-10-05"))
	eventually(t, "👀 then 👌 on the link message", func() bool {
		e := emojis(fake)
		return len(e) == 2 && e[0] == "👀" && e[1] == "👌"
	})
	msgs := firstChatMessages(t, h)
	if len(msgs) != 1 || msgs[0].Text != "https://telegra.ph/Sample-10-05" || len(msgs[0].Media) != 0 {
		t.Fatalf("messages = %+v", msgs)
	}
	art := msgs[0].Article
	if art == nil || art.State != store.TelegraphFetched || art.Title != "Sample" || art.ImageMediaID == 0 {
		t.Fatalf("article summary = %+v", art)
	}
	code, body := req(t, h, "GET", fmt.Sprintf("/api/messages/%d/article", msgs[0].ID), nil)
	var av store.ArticleView
	if code != 200 || json.Unmarshal(body, &av) != nil || len(av.Media) != 2 {
		t.Fatalf("article = %d %s", code, body)
	}
	if av.Media[0].State != store.StateDone || av.Media[1].State != store.StateFailed || av.Media[0].ID != art.ImageMediaID {
		t.Fatalf("article media = %+v", av.Media)
	}
	if !strings.Contains(string(av.Content), fmt.Sprintf(`"data-media-id":"%d"`, av.Media[0].ID)) {
		t.Fatalf("content = %s", av.Content)
	}
	code, body = req(t, h, "GET", fmt.Sprintf("/media/%d", av.Media[0].ID), nil)
	if code != 200 || string(body) != "COVER" {
		t.Fatalf("cover = %d %q", code, body)
	}
	if files, _ := filepath.Glob(filepath.Join(dataDir, "media", "web", "*", "*", "*.jpg")); len(files) != 1 {
		t.Fatalf("archived web files = %v", files)
	}
	if n := len(fake.Calls("sendMessage")); n != 0 {
		t.Fatalf("a failed article image must not reply (%d replies)", n)
	}

	fake.PushMessage(tgtest.TextMsg(2, 42, "telegra.ph/Gone"))
	eventually(t, "failure reply", func() bool {
		for _, c := range fake.Calls("sendMessage") {
			if c.Params["text"] == "⚠️ 存档失败：文章不存在" {
				return true
			}
		}
		return false
	})
	if e := emojis(fake); len(e) != 3 || e[2] != "👀" {
		t.Fatalf("reactions = %v (a failed article keeps 👀)", e)
	}
}
