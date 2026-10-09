package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tgarchive/internal/botapiserver"
	"tgarchive/internal/config"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgapp"
)

func newSettingsEnv(t *testing.T) (http.Handler, *botapiserver.Supervisor) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	sup := botapiserver.New("/nonexistent/telegram-bot-api", t.TempDir(), t.TempDir(), 18082)
	srv := &Server{Cfg: &config.Config{RequireForwardAuth: true, AllowedHosts: []string{"example.com"}}, Store: st, Box: box, TgApp: tgapp.New(st, box), BotAPI: sup, Now: time.Now}
	return srv.Handler(), sup
}

func TestTelegramAppSettings(t *testing.T) {
	h, sup := newSettingsEnv(t)
	w := call(h, "GET", "/api/admin/telegram-app", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":false`) || !strings.Contains(w.Body.String(), `"managed":true`) {
		t.Fatalf("initial = %d %s", w.Code, w.Body)
	}
	if w := call(h, "POST", "/api/admin/bots", map[string]string{"token": goodToken}); w.Code != 409 {
		t.Fatalf("add bot before credentials = %d %s", w.Code, w.Body)
	}
	for _, bad := range []map[string]any{{"api_id": 0, "api_hash": "0123456789abcdef0123456789abcdef"}, {"api_id": 1, "api_hash": "nope"}} {
		if w := call(h, "PUT", "/api/admin/telegram-app", bad); w.Code != 400 {
			t.Fatalf("invalid %v = %d", bad, w.Code)
		}
	}
	const hash = "0123456789abcdef0123456789abcdef"
	if w := call(h, "PUT", "/api/admin/telegram-app", map[string]any{"api_id": 4242, "api_hash": hash}); w.Code != 204 {
		t.Fatalf("save = %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/api/admin/telegram-app", nil)
	var got struct {
		Configured bool `json:"configured"`
		APIID      int  `json:"api_id"`
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if !got.Configured || got.APIID != 4242 || strings.Contains(w.Body.String(), hash) {
		t.Fatalf("after save = %s", w.Body)
	}

	// The supervisor received the credentials: running it now starts (and fails to exec) the binary.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx, nil)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if st, e := sup.Status(); st == botapiserver.StateRestarting && e != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervisor never received the applied credentials")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
