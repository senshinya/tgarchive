package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tgarchive/internal/botclients"
	"tgarchive/internal/collector"
	"tgarchive/internal/config"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/receipt"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
)

const goodToken = "777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

type adminEnv struct {
	h    http.Handler
	st   *store.Store
	fake *tgtest.FakeTG
	mgr  *collector.Manager
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	fake := tgtest.New(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	reg := botclients.New(st, box, fake.URL(), nil)
	hub := events.NewHub()
	mediaDir := t.TempDir()
	dl := downloader.New(st, mediaDir, 0, nil)
	ctx, cancel := context.WithCancel(context.Background())
	mgr := collector.New(ctx, collector.Deps{Store: st, Clients: reg, Downloader: dl, Receipts: receipt.New(st, reg), Hub: hub, MediaDir: mediaDir, PollTimeoutSec: 1})
	t.Cleanup(func() { mgr.StopAll(); cancel(); st.Close() })
	srv := &Server{
		Cfg:   &config.Config{RequireForwardAuth: true, BotAPIURL: fake.URL(), CloudAPIURL: fake.URL()},
		Store: st, Box: box, Clients: reg, Manager: mgr, Downloader: dl, Hub: hub,
		MediaDir: mediaDir, AvatarDir: t.TempDir(), Now: time.Now,
	}
	return &adminEnv{h: srv.Handler(), st: st, fake: fake, mgr: mgr}
}

func call(h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Remote-User", "shinya")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

type addResp struct {
	BotID int64  `json:"bot_id"`
	Error string `json:"error"`
	Steps []struct {
		Step   string `json:"step"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	} `json:"steps"`
}

func waitStatus(t *testing.T, st *store.Store, id int64, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := st.GetBot(context.Background(), id)
		if b != nil && b.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("bot %d status = %v, want %s", id, b, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAddBot(t *testing.T) {
	e := newAdminEnv(t)
	if w := call(e.h, "POST", "/api/admin/bots", map[string]string{"token": "nope"}); w.Code != 400 {
		t.Fatalf("bad format = %d", w.Code)
	}
	e.fake.RejectToken("888:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	w := call(e.h, "POST", "/api/admin/bots", map[string]string{"token": "888:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"})
	var r addResp
	json.Unmarshal(w.Body.Bytes(), &r)
	if w.Code != 400 || len(r.Steps) != 1 || r.Steps[0].Step != "getMe" || r.Steps[0].OK {
		t.Fatalf("rejected token = %d %s", w.Code, w.Body)
	}

	w = call(e.h, "POST", "/api/admin/bots", map[string]string{"token": " " + goodToken + "\n"})
	r = addResp{}
	json.Unmarshal(w.Body.Bytes(), &r)
	if w.Code != 200 || r.BotID == 0 || len(r.Steps) != 3 || r.Steps[0].Detail != "@archive_bot" {
		t.Fatalf("add = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), goodToken) {
		t.Fatal("token echoed in response")
	}
	if len(e.fake.Calls("logOut")) != 1 {
		t.Fatal("logOut must be called on the cloud API")
	}
	waitStatus(t, e.st, r.BotID, store.StatusRunning)

	w = call(e.h, "POST", "/api/admin/bots", map[string]string{"token": goodToken})
	if w.Code != 409 {
		t.Fatalf("duplicate = %d %s", w.Code, w.Body)
	}
}

func TestToggleAndDeleteBot(t *testing.T) {
	e := newAdminEnv(t)
	var r addResp
	json.Unmarshal(call(e.h, "POST", "/api/admin/bots", map[string]string{"token": goodToken}).Body.Bytes(), &r)
	waitStatus(t, e.st, r.BotID, store.StatusRunning)
	p := fmt.Sprintf("/api/admin/bots/%d", r.BotID)

	w := call(e.h, "PATCH", p, map[string]bool{"enabled": false})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"stopped"`) || e.mgr.Running(r.BotID) {
		t.Fatalf("disable = %d %s running=%v", w.Code, w.Body, e.mgr.Running(r.BotID))
	}
	if w := call(e.h, "PATCH", p, map[string]bool{"enabled": true}); w.Code != 200 {
		t.Fatalf("enable = %d", w.Code)
	}
	waitStatus(t, e.st, r.BotID, store.StatusRunning)

	if w := call(e.h, "DELETE", p, nil); w.Code != 204 {
		t.Fatalf("remove = %d", w.Code)
	}
	waitStatus(t, e.st, r.BotID, store.StatusRemoved)
	if w := call(e.h, "PATCH", p, map[string]bool{"enabled": true}); w.Code != 404 {
		t.Fatalf("enabling a removed bot = %d", w.Code)
	}

	w = call(e.h, "POST", "/api/admin/bots", map[string]string{"token": goodToken})
	r2 := addResp{}
	json.Unmarshal(w.Body.Bytes(), &r2)
	if w.Code != 200 || r2.BotID != r.BotID {
		t.Fatalf("re-add of removed bot = %d %s", w.Code, w.Body)
	}
	if w := call(e.h, "DELETE", p+"?purge=1", nil); w.Code != 204 {
		t.Fatalf("purge = %d", w.Code)
	}
	if _, err := e.st.GetBot(context.Background(), r.BotID); err == nil {
		t.Fatal("purged bot still exists")
	}
	if w := call(e.h, "DELETE", p+"?purge=1", nil); w.Code != 404 {
		t.Fatalf("purge missing = %d", w.Code)
	}
}

func TestWhitelistAndRejected(t *testing.T) {
	e := newAdminEnv(t)
	var r addResp
	json.Unmarshal(call(e.h, "POST", "/api/admin/bots", map[string]string{"token": goodToken}).Body.Bytes(), &r)
	base := fmt.Sprintf("/api/admin/bots/%d", r.BotID)

	e.fake.PushMessage(tgtest.TextMsg(1, 99, "hi"))
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(call(e.h, "GET", base+"/rejected", nil).Body.String(), `"tg_user_id":99`) {
		if time.Now().After(deadline) {
			t.Fatal("rejected sender not listed")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if w := call(e.h, "PUT", base+"/whitelist/99", map[string]any{"note": "friend", "can_fetch": true}); w.Code != 204 {
		t.Fatalf("put = %d %s", w.Code, w.Body)
	}
	w := call(e.h, "GET", base+"/whitelist", nil)
	if !strings.Contains(w.Body.String(), `"tg_user_id":99`) || !strings.Contains(w.Body.String(), `"can_fetch":true`) {
		t.Fatalf("whitelist = %s", w.Body)
	}
	if strings.Contains(call(e.h, "GET", base+"/rejected", nil).Body.String(), `"tg_user_id":99`) {
		t.Fatal("whitelisted user must leave the rejected list")
	}
	if w := call(e.h, "DELETE", base+"/whitelist/99", nil); w.Code != 204 {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := call(e.h, "DELETE", base+"/whitelist/99", nil); w.Code != 404 {
		t.Fatalf("delete missing = %d", w.Code)
	}
	if w := call(e.h, "PUT", "/api/admin/bots/9999/whitelist/1", map[string]any{}); w.Code != 404 {
		t.Fatalf("put on missing bot = %d", w.Code)
	}
}
