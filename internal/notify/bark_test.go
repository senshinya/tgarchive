package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBarkPosts(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"code":200}`))
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "notify.json")
	os.WriteFile(cfg, []byte(`{"endpoint":"`+srv.URL+`/push","device_keys":["k1","k2"]}`), 0o600)
	(&Bark{File: cfg}).Notify(context.Background(), "title", "body")
	if got["title"] != "title" || got["body"] != "body" || got["group"] != "docker" || got["level"] != "timeSensitive" {
		t.Fatalf("payload = %v", got)
	}
	if keys := got["device_keys"].([]any); len(keys) != 2 {
		t.Fatalf("device_keys = %v", keys)
	}
}

func TestBarkDisabledWithoutFile(t *testing.T) {
	(&Bark{}).Notify(context.Background(), "t", "b") // must be a no-op
	(&Bark{File: "/nonexistent/notify.json"}).Notify(context.Background(), "t", "b")
}

func TestBarkSendsNonDefaultUserAgent(t *testing.T) {
	// bark.shinya.click sits behind Cloudflare, whose Browser Integrity Check rejects default client UAs.
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.UserAgent()
		w.Write([]byte(`{"code":200}`))
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "notify.json")
	os.WriteFile(cfg, []byte(`{"endpoint":"`+srv.URL+`/push","device_keys":["k1"]}`), 0o600)
	(&Bark{File: cfg}).Notify(context.Background(), "t", "b")
	if ua != "tgarchive-notify/1.0" {
		t.Fatalf("User-Agent = %q", ua)
	}
}
