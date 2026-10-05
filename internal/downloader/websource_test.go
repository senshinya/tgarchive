package downloader

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

func allowAll(net.IP) error { return nil }

func webMedia(url string) *store.Media {
	return &store.Media{ID: 1, DedupeKey: "web:x", SourceRef: url, Kind: "photo"}
}

func isPermanent(err error) bool {
	var pe *permanentError
	return errors.As(err, &pe)
}

func webServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/img", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "tgarchive/1.0" {
			http.Error(w, "bad ua", 400)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNGDATA"))
	})
	mux.HandleFunc("/clip.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("MP4"))
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) { w.Write(make([]byte, 100)) })
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 10; i++ { // chunked: no Content-Length
			w.Write(make([]byte, 10))
			w.(http.Flusher).Flush()
		}
	})
	mux.HandleFunc("/hop/{n}", func(w http.ResponseWriter, r *http.Request) {
		var n int
		fmt.Sscan(r.PathValue("n"), &n)
		if n == 0 {
			http.Redirect(w, r, "/img", http.StatusFound)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/hop/%d", n-1), http.StatusFound)
	})
	mux.HandleFunc("/to-file", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	})
	mux.HandleFunc("/gone", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "gone", 404) })
	mux.HandleFunc("/busy", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "busy", 503) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestWebSourceDownloads(t *testing.T) {
	srv := webServer(t)
	src := NewWebSource(0, allowAll)
	base := filepath.Join(t.TempDir(), "x")
	var last [2]int64
	pc := WithProgress(ctx, func(done, total int64) { last = [2]int64{done, total} })
	p, n, err := src.Fetch(pc, webMedia(srv.URL+"/img"), base)
	if err != nil || p != base+".png" || n != 7 {
		t.Fatalf("Fetch = %q %d %v", p, n, err)
	}
	if last != [2]int64{7, 7} {
		t.Fatalf("progress = %v, want 7 of 7 (Content-Length)", last)
	}
	if b, _ := os.ReadFile(p); string(b) != "PNGDATA" {
		t.Fatalf("content = %q", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v", st.Mode())
	}
	if _, err := os.Stat(p + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part left behind")
	}
	p, _, err = src.Fetch(ctx, webMedia(srv.URL+"/clip.mp4"), base)
	if err != nil || p != base+".mp4" {
		t.Fatalf("extension from URL path: %q %v", p, err)
	}
}

func TestWebSourceRedirects(t *testing.T) {
	srv := webServer(t)
	src := NewWebSource(0, allowAll)
	base := filepath.Join(t.TempDir(), "x")
	if _, _, err := src.Fetch(ctx, webMedia(srv.URL+"/hop/4"), base); err != nil { // 5 redirects
		t.Fatalf("5 redirects: %v", err)
	}
	_, _, err := src.Fetch(ctx, webMedia(srv.URL+"/hop/5"), base) // 6 redirects
	if err == nil || isPermanent(err) {
		t.Fatalf("6 redirects = %v, want a retryable error", err)
	}
	_, _, err = src.Fetch(ctx, webMedia(srv.URL+"/to-file"), base)
	if !isPermanent(err) || !errors.Is(err, ErrAddrNotAllowed) {
		t.Fatalf("redirect to file:// = %v", err)
	}
}

func TestWebSourceSizeLimit(t *testing.T) {
	srv := webServer(t)
	src := NewWebSource(50, allowAll)
	dir := t.TempDir()
	for _, p := range []string{"/big", "/stream"} {
		_, _, err := src.Fetch(ctx, webMedia(srv.URL+p), filepath.Join(dir, "x"))
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("%s: err = %v, want ErrTooLarge", p, err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("partial files left: %v", entries)
	}
}

// TestWebSourceDefaultCapWhenUnset is the production case (MEDIA_MAX_BYTES unset, so
// NewWebSource is called with maxBytes=0): article authors are untrusted, so WebSource must
// still enforce its own default cap instead of streaming an unbounded body. The test overrides
// the unexported default (DefaultWebMaxBytes is 2 GiB in production, far too large to stream
// here) so the oversize body in the test server actually exceeds it.
func TestWebSourceDefaultCapWhenUnset(t *testing.T) {
	srv := webServer(t)
	src := NewWebSource(0, allowAll)
	src.defaultMaxBytes = 50
	dir := t.TempDir()
	for _, p := range []string{"/big", "/stream"} {
		_, _, err := src.Fetch(ctx, webMedia(srv.URL+p), filepath.Join(dir, "x"))
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("%s: err = %v, want ErrTooLarge", p, err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("partial files left: %v", entries)
	}
	// Below the overridden default: still downloads normally.
	p, n, err := src.Fetch(ctx, webMedia(srv.URL+"/img"), filepath.Join(dir, "y"))
	if err != nil || n != 7 {
		t.Fatalf("Fetch under cap = %q %d %v", p, n, err)
	}
}

func TestDefaultWebMaxBytesIs2GiB(t *testing.T) {
	if DefaultWebMaxBytes != 2<<30 {
		t.Fatalf("DefaultWebMaxBytes = %d, want 2 GiB", DefaultWebMaxBytes)
	}
	src := NewWebSource(0, allowAll)
	if src.defaultMaxBytes != DefaultWebMaxBytes {
		t.Fatalf("defaultMaxBytes = %d, want %d", src.defaultMaxBytes, DefaultWebMaxBytes)
	}
}

func TestWebSourceStatus(t *testing.T) {
	srv := webServer(t)
	src := NewWebSource(0, allowAll)
	base := filepath.Join(t.TempDir(), "x")
	if _, _, err := src.Fetch(ctx, webMedia(srv.URL+"/gone"), base); !isPermanent(err) || err.Error() != "HTTP 404" {
		t.Fatalf("404 = %v", err)
	}
	if _, _, err := src.Fetch(ctx, webMedia(srv.URL+"/busy"), base); err == nil || isPermanent(err) {
		t.Fatalf("503 = %v, want retryable", err)
	}
	for _, ref := range []string{"file:///etc/passwd", "ftp://example.com/a.jpg", "not a url", "https:///nohost"} {
		if _, _, err := src.Fetch(ctx, webMedia(ref), base); !isPermanent(err) || !errors.Is(err, ErrAddrNotAllowed) {
			t.Fatalf("%q = %v", ref, err)
		}
	}
}

func TestWebSourceRefusesInternalAddresses(t *testing.T) {
	srv := webServer(t) // listens on 127.0.0.1
	src := NewWebSource(0, nil)
	_, _, err := src.Fetch(ctx, webMedia(srv.URL+"/img"), filepath.Join(t.TempDir(), "x"))
	if !isPermanent(err) || !errors.Is(err, ErrAddrNotAllowed) || err.Error() != "地址不允许" {
		t.Fatalf("loopback = %v", err)
	}
	host := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	if _, _, err := src.Fetch(ctx, webMedia(host+"/img"), filepath.Join(t.TempDir(), "x")); !errors.Is(err, ErrAddrNotAllowed) {
		t.Fatalf("localhost = %v", err)
	}
}

func TestPublicIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "224.0.0.1",
		"0.0.0.0", "100.64.0.1", "100.127.255.255", "::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "ff02::1", "::",
		"64:ff9b::a9fe:a9fe", "64:ff9b:1::7f00:1", "2002:7f00:1::1",
		"0.1.2.3", "0.255.255.255", "192.0.0.0", "192.0.0.255", "198.18.0.0", "198.19.255.255", "240.0.0.1", "255.255.255.255"} {
		if err := PublicIP(net.ParseIP(s)); !errors.Is(err, ErrAddrNotAllowed) {
			t.Errorf("PublicIP(%s) = %v, want ErrAddrNotAllowed", s, err)
		}
	}
	for _, s := range []string{"1.1.1.1", "149.154.167.99", "100.128.0.1", "100.63.255.255", "2606:4700::1111",
		"192.0.1.0", "198.17.255.255", "198.20.0.0"} {
		if err := PublicIP(net.ParseIP(s)); err != nil {
			t.Errorf("PublicIP(%s) = %v, want nil", s, err)
		}
	}
}

func TestWebSourceThroughProcess(t *testing.T) {
	srv := webServer(t)
	f, _, _ := setup(t, 0)
	// A second message carrying a web: media row, as SaveArticle would create it.
	f.st.Ingest(ctx, store.IngestInput{BotID: f.bot, Sender: model.Sender{TgUserID: 42}, Msg: &model.Message{TgMessageID: 2,
		Source: model.SourceBotUpdate, Date: 2, Kind: model.KindPhoto, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`),
		Media: []model.Media{{DedupeKey: "web:abc", SourceRef: srv.URL + "/img", Kind: "photo", Role: model.RoleMain}}}, Now: 2})
	due, _ := f.st.DueMedia(ctx, 0, 10)
	var m *store.Media
	for i := range due {
		if due[i].DedupeKey == "web:abc" {
			m = &due[i]
		}
	}
	d := f.newDL(0)
	d.Register("web", NewWebSource(0, nil)) // production check: 127.0.0.1 is refused, no retries
	d.Process(ctx, m)
	got, _ := f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateFailed || got.Error != "地址不允许" || got.Attempts != 1 {
		t.Fatalf("refused media = %+v", got)
	}
	f.st.ResetMedia(ctx, m.ID)
	d.Register("web", NewWebSource(0, allowAll))
	cur, _ := f.st.GetMedia(ctx, m.ID)
	d.Process(ctx, cur)
	got, _ = f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateDone || !strings.HasPrefix(got.Path, "web/2026/10/") || !strings.HasSuffix(got.Path, ".png") {
		t.Fatalf("downloaded media = %+v", got)
	}
}
