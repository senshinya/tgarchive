package httpapi

import (
	"bytes"
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

	"tgarchive/internal/config"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgapp"
	"tgarchive/internal/userbot"
)

type fakeUserbot struct {
	mu      sync.Mutex
	state   string
	phone   string
	code    string
	pw      string
	reloads int
	logouts int
	err     error
}

func (f *fakeUserbot) Status(context.Context) userbot.Info {
	f.mu.Lock()
	defer f.mu.Unlock()
	return userbot.Info{State: f.state, Phone: f.phone}
}

func (f *fakeUserbot) SendCode(_ context.Context, phone string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.phone, f.state = phone, userbot.StateCodeSent
	return nil
}

func (f *fakeUserbot) SignIn(_ context.Context, code string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.code, f.state = code, userbot.StatePasswordNeeded
	return f.state, nil
}

func (f *fakeUserbot) Password(_ context.Context, pw string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pw, f.state = pw, userbot.StateReady
	return nil
}

func (f *fakeUserbot) Logout(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logouts++
	f.state = userbot.StateLoggedOut
	return nil
}

func (f *fakeUserbot) Reload() {
	f.mu.Lock()
	f.reloads++
	f.mu.Unlock()
}

func newUserbotEnv(t *testing.T) (http.Handler, *fakeUserbot) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	fu := &fakeUserbot{state: userbot.StateLoggedOut}
	srv := &Server{Cfg: &config.Config{RequireForwardAuth: true, AllowedHosts: []string{"example.com"}}, Store: st, Box: box, TgApp: tgapp.New(st, box), Userbot: fu, Now: time.Now}
	return srv.Handler(), fu
}

func state(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var info userbot.Info
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatalf("body %s: %v", w.Body, err)
	}
	return info.State
}

func TestUserbotLoginFlow(t *testing.T) {
	h, fu := newUserbotEnv(t)
	if w := call(h, "GET", "/api/admin/userbot", nil); w.Code != 200 || state(t, w) != userbot.StateLoggedOut {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{"abc", "+12", "+86 138 0000 0000 0000 0000 1"} {
		if w := call(h, "POST", "/api/admin/userbot/phone", map[string]string{"phone": bad}); w.Code != 400 || !strings.Contains(w.Body.String(), "手机号格式不正确") {
			t.Fatalf("phone %q = %d %s", bad, w.Code, w.Body)
		}
	}
	w := call(h, "POST", "/api/admin/userbot/phone", map[string]string{"phone": " 86 138-0000-0000 "})
	if w.Code != 200 || state(t, w) != userbot.StateCodeSent || fu.phone != "+8613800000000" {
		t.Fatalf("phone = %d %s (got %q)", w.Code, w.Body, fu.phone)
	}
	if w := call(h, "POST", "/api/admin/userbot/code", map[string]string{"code": "12a"}); w.Code != 400 {
		t.Fatalf("bad code = %d", w.Code)
	}
	w = call(h, "POST", "/api/admin/userbot/code", map[string]string{"code": " 12345 "})
	if w.Code != 200 || state(t, w) != userbot.StatePasswordNeeded || fu.code != "12345" {
		t.Fatalf("code = %d %s", w.Code, w.Body)
	}
	if w := call(h, "POST", "/api/admin/userbot/password", map[string]string{"password": ""}); w.Code != 400 {
		t.Fatalf("empty password = %d", w.Code)
	}
	w = call(h, "POST", "/api/admin/userbot/password", map[string]string{"password": "p w"})
	if w.Code != 200 || state(t, w) != userbot.StateReady || fu.pw != "p w" {
		t.Fatalf("password = %d %s", w.Code, w.Body)
	}
	if w := call(h, "POST", "/api/admin/userbot/logout", nil); w.Code != 204 || fu.logouts != 1 {
		t.Fatalf("logout = %d", w.Code)
	}
}

func TestUserbotErrorMapping(t *testing.T) {
	h, fu := newUserbotEnv(t)
	cases := []struct {
		err  error
		code int
		msg  string
	}{
		{&userbot.InputError{Msg: "验证码错误"}, 400, "验证码错误"},
		{userbot.ErrNotConfigured, 409, "api_id"},
		{userbot.ErrBadState, 409, "登录步骤"},
		{userbot.ErrAlreadyLoggedIn, 409, "已登录"},
		{userbot.ErrNotConnected, 503, "尚未连接"},
		{errors.New("rpc error code 500: INTERNAL"), 502, "INTERNAL"},
	}
	for _, c := range cases {
		fu.err = c.err
		w := call(h, "POST", "/api/admin/userbot/phone", map[string]string{"phone": "+8613800000000"})
		if w.Code != c.code || !strings.Contains(w.Body.String(), c.msg) {
			t.Fatalf("%v → %d %s", c.err, w.Code, w.Body)
		}
	}
}

func TestUserbotBodyLimit(t *testing.T) {
	h, _ := newUserbotEnv(t)
	big := map[string]string{"password": strings.Repeat("x", 8192)}
	if w := call(h, "POST", "/api/admin/userbot/password", big); w.Code != 400 {
		t.Fatalf("oversized body = %d", w.Code)
	}
}

func TestTelegramAppSaveReloadsUserbot(t *testing.T) {
	h, fu := newUserbotEnv(t)
	w := call(h, "PUT", "/api/admin/telegram-app", map[string]any{"api_id": 4242, "api_hash": "0123456789abcdef0123456789abcdef"})
	if w.Code != 204 || fu.reloads != 1 {
		t.Fatalf("save = %d, reloads = %d", w.Code, fu.reloads)
	}
}
