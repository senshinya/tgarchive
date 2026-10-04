package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tgarchive/internal/config"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
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
