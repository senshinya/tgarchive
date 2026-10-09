package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"tgarchive/internal/config"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

var bg = context.Background()

type readEnv struct {
	srv      *Server
	h        http.Handler
	st       *store.Store
	hub      *events.Hub
	chat     int64
	photoMsg int64
	media    int64
}

func newReadEnv(t *testing.T) *readEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mediaDir, avatarDir := t.TempDir(), t.TempDir()
	hub := events.NewHub()
	srv := &Server{
		Cfg: &config.Config{RequireForwardAuth: true, AllowedHosts: []string{"example.com"}}, Store: st, Hub: hub,
		Downloader: downloader.New(st, mediaDir, 0, nil),
		Web:        fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "assets/app.js": {Data: []byte("js!")}},
		MediaDir:   mediaDir, AvatarDir: avatarDir, Now: time.Now,
	}
	bot, _ := st.UpsertBot(bg, &store.Bot{TgBotID: 777, Username: "archive_bot", TokenEnc: []byte("SECRET-TOKEN-BYTES"), CreatedAt: 1})
	photo := &model.Message{TgMessageID: 1, Source: model.SourceBotUpdate, Date: 1, Kind: model.KindPhoto, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`),
		Media: []model.Media{{DedupeKey: "bot:p", Kind: "photo", Mime: "image/jpeg", Role: model.RoleMain}}}
	r1, _ := st.Ingest(bg, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42, FirstName: "Alice"}, Msg: photo, Now: 1})
	link := &model.Message{TgMessageID: 2, Source: model.SourceBotUpdate, Date: 2, Kind: model.KindText, Text: "see https://x.dev", RawFormat: model.RawBotAPI,
		Raw: json.RawMessage(`{}`), Entities: []model.Entity{{Type: "url", Offset: 4, Length: 13}}}
	st.Ingest(bg, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42, FirstName: "Alice"}, Msg: link, Now: 2})
	due, _ := st.DueMedia(bg, 0, 10)
	os.MkdirAll(filepath.Join(mediaDir, "1"), 0o755)
	os.WriteFile(filepath.Join(mediaDir, "1", "p.jpg"), []byte("photo-bytes"), 0o644)
	st.MarkMediaDone(bg, due[0].ID, "1/p.jpg", 11)
	return &readEnv{srv: srv, h: srv.Handler(), st: st, hub: hub, chat: r1.ChatID, photoMsg: r1.MessageID, media: due[0].ID}
}

func do(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Remote-User", "shinya")
	if method != "GET" && method != "HEAD" {
		req.Header.Set("Content-Type", "application/json") // as the WebUI sends every write
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestAuth(t *testing.T) {
	e := newReadEnv(t)
	for _, p := range []string{"/api/bots", "/", "/media/1"} {
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 401 {
			t.Fatalf("%s without Remote-User = %d", p, w.Code)
		}
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 {
		t.Fatalf("healthz = %d", w.Code)
	}
	e.srv.Cfg.RequireForwardAuth = false
	w = httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/bots", nil))
	if w.Code != 200 {
		t.Fatalf("auth disabled = %d", w.Code)
	}
}

func TestReadEndpoints(t *testing.T) {
	e := newReadEnv(t)
	w := do(e.h, "GET", "/api/bots", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "SECRET") || !strings.Contains(w.Body.String(), `"username":"archive_bot"`) {
		t.Fatalf("bots = %d %s", w.Code, w.Body)
	}
	var chats []store.ChatView
	json.Unmarshal(do(e.h, "GET", "/api/chats", nil).Body.Bytes(), &chats)
	if len(chats) != 1 || chats[0].Sender.FirstName != "Alice" {
		t.Fatalf("chats = %+v", chats)
	}
	var msgs []store.MessageView
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/chats/%d/messages?limit=10", e.chat), nil).Body.Bytes(), &msgs)
	if len(msgs) != 2 || msgs[0].Media[0].State != "done" {
		t.Fatalf("messages = %+v", msgs)
	}
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/chats/%d/media?type=link", e.chat), nil).Body.Bytes(), &msgs)
	if len(msgs) != 1 || msgs[0].TgMessageID != 2 {
		t.Fatalf("links = %+v", msgs)
	}
	for _, p := range []string{
		fmt.Sprintf("/api/chats/%d/media?type=bogus", e.chat),
		fmt.Sprintf("/api/chats/%d/messages?limit=abc", e.chat),
		"/api/chats/abc/messages",
	} {
		if w := do(e.h, "GET", p, nil); w.Code != 400 || !strings.Contains(w.Body.String(), `"error"`) {
			t.Fatalf("%s = %d %s", p, w.Code, w.Body)
		}
	}
}

func TestBotTimelineEndpoints(t *testing.T) {
	e := newReadEnv(t)
	bots, _ := e.st.ListBots(bg)
	bot := bots[0].ID
	bob := &model.Message{TgMessageID: 1, Source: model.SourceBotUpdate, Date: 3, Kind: model.KindText, Text: "from bob", RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}
	e.st.Ingest(bg, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 7, FirstName: "Bob"}, Msg: bob, Now: 3})
	var msgs []store.MessageView
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/bots/%d/messages?limit=10", bot), nil).Body.Bytes(), &msgs)
	if len(msgs) != 3 || msgs[2].Text != "from bob" || msgs[0].ChatID == msgs[2].ChatID {
		t.Fatalf("bot messages = %+v", msgs)
	}
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/bots/%d/messages?limit=1&before=%d", bot, msgs[2].ID), nil).Body.Bytes(), &msgs)
	if len(msgs) != 1 || msgs[0].TgMessageID != 2 {
		t.Fatalf("bot messages page 2 = %+v", msgs)
	}
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/bots/%d/media?type=media", bot), nil).Body.Bytes(), &msgs)
	if len(msgs) != 1 || msgs[0].Media[0].State != "done" {
		t.Fatalf("bot media = %+v", msgs)
	}
	for _, p := range []string{
		fmt.Sprintf("/api/bots/%d/media?type=bogus", bot),
		fmt.Sprintf("/api/bots/%d/messages?limit=0", bot),
		"/api/bots/abc/messages",
		"/api/bots/abc/media?type=media",
	} {
		if w := do(e.h, "GET", p, nil); w.Code != 400 {
			t.Fatalf("%s = %d %s", p, w.Code, w.Body)
		}
	}
}

func TestServeMedia(t *testing.T) {
	e := newReadEnv(t)
	p := fmt.Sprintf("/media/%d", e.media)
	w := do(e.h, "GET", p, nil)
	if w.Code != 200 || w.Body.String() != "photo-bytes" || w.Header().Get("ETag") == "" || !strings.Contains(w.Header().Get("Cache-Control"), "private") {
		t.Fatalf("media = %d %q %v", w.Code, w.Body, w.Header())
	}
	if w := do(e.h, "GET", p, map[string]string{"Range": "bytes=0-4"}); w.Code != 206 || w.Body.String() != "photo" {
		t.Fatalf("range = %d %q", w.Code, w.Body)
	}
	if w := do(e.h, "GET", p, map[string]string{"If-None-Match": w.Header().Get("ETag")}); w.Code != 304 {
		t.Fatalf("conditional = %d", w.Code)
	}
	if w := do(e.h, "GET", p+"?download=1", nil); !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download header = %q", w.Header().Get("Content-Disposition"))
	}
	os.Remove(filepath.Join(e.srv.MediaDir, "1", "p.jpg"))
	if w := do(e.h, "GET", p, nil); w.Code != 404 {
		t.Fatalf("missing file = %d", w.Code)
	}
	if w := do(e.h, "GET", "/media/99999", nil); w.Code != 404 {
		t.Fatalf("unknown media = %d", w.Code)
	}
}

func TestServeMediaCompat(t *testing.T) {
	e := newReadEnv(t)
	p := fmt.Sprintf("/media/%d?compat=1", e.media)
	if w := do(e.h, "GET", p, nil); w.Code != 404 {
		t.Fatalf("compat before a copy exists = %d", w.Code)
	}
	if err := os.WriteFile(filepath.Join(e.srv.MediaDir, "1", "p.compat.mp4"), []byte("h264-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.srv.Store.SetCompat(context.Background(), e.media, store.CompatDone, "av1", "1/p.compat.mp4", ""); !ok || err != nil {
		t.Fatalf("set compat = %v %v", ok, err)
	}
	w := do(e.h, "GET", p, nil)
	if w.Code != 200 || w.Body.String() != "h264-bytes" || w.Header().Get("Content-Type") != "video/mp4" ||
		w.Header().Get("ETag") == fmt.Sprintf(`"m%d"`, e.media) || w.Header().Get("Content-Disposition") != "" {
		t.Fatalf("compat = %d %q %v", w.Code, w.Body, w.Header())
	}
	if w := do(e.h, "GET", fmt.Sprintf("/media/%d?download=1", e.media), nil); w.Body.String() != "photo-bytes" {
		t.Fatalf("download must stay the original, got %q", w.Body)
	}
}

func TestDeleteAndRetry(t *testing.T) {
	e := newReadEnv(t)
	ch, unsub := e.hub.Subscribe()
	defer unsub()
	if w := do(e.h, "POST", fmt.Sprintf("/api/media/%d/retry", e.media), nil); w.Code != 409 {
		t.Fatalf("retry of done media = %d", w.Code)
	}
	if w := do(e.h, "DELETE", fmt.Sprintf("/api/messages/%d", e.photoMsg), nil); w.Code != 204 {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(e.srv.MediaDir, "1", "p.jpg")); err == nil {
		t.Fatal("orphaned file must be removed")
	}
	if ev := <-ch; ev.Type != "message.deleted" {
		t.Fatalf("event = %+v", ev)
	}
	if w := do(e.h, "DELETE", fmt.Sprintf("/api/messages/%d", e.photoMsg), nil); w.Code != 404 {
		t.Fatalf("second delete = %d", w.Code)
	}
}

func TestSPAFallback(t *testing.T) {
	e := newReadEnv(t)
	if w := do(e.h, "GET", "/chats/5", nil); w.Code != 200 || w.Body.String() != "<html>app</html>" {
		t.Fatalf("spa route = %d %q", w.Code, w.Body)
	}
	if w := do(e.h, "GET", "/assets/app.js", nil); w.Code != 200 || w.Body.String() != "js!" {
		t.Fatalf("asset = %d %q", w.Code, w.Body)
	} else if cc := w.Header().Get("Cache-Control"); cc != "private, max-age=31536000, immutable" {
		t.Fatalf("hashed asset Cache-Control = %q", cc)
	}
	if w := do(e.h, "GET", "/api/nope", nil); w.Code != 404 || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("unknown api = %d %q", w.Code, w.Body)
	}
}

func TestSSE(t *testing.T) {
	e := newReadEnv(t)
	ts := httptest.NewServer(e.h)
	defer ts.Close()
	req, _ := http.NewRequest("GET", ts.URL+"/api/events", nil)
	req.Header.Set("Remote-User", "shinya")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	rd := bufio.NewReader(resp.Body)
	if line, _ := rd.ReadString('\n'); line != "event: ping\n" {
		t.Fatalf("first line = %q, want an immediate ping event", line)
	}
	rd.ReadString('\n') // data
	rd.ReadString('\n') // blank
	go func() {
		time.Sleep(50 * time.Millisecond)
		e.hub.Publish(events.Event{Type: "message.created", Data: map[string]int64{"chat_id": 1, "message_id": 2}})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		line, err := rd.ReadString('\n')
		if err == io.EOF {
			break
		}
		if strings.HasPrefix(line, "event: message.created") {
			data, _ := rd.ReadString('\n')
			if !strings.Contains(data, `"message_id":2`) {
				t.Fatalf("data line = %q", data)
			}
			return
		}
	}
	t.Fatal("event not received")
}

func TestSSEPeriodicPing(t *testing.T) {
	e := newReadEnv(t)
	e.srv.PingEvery = 20 * time.Millisecond
	ts := httptest.NewServer(e.h)
	defer ts.Close()
	req, _ := http.NewRequest("GET", ts.URL+"/api/events", nil)
	req.Header.Set("Remote-User", "shinya")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rd := bufio.NewReader(resp.Body)
	pings := 0
	deadline := time.Now().Add(3 * time.Second)
	for pings < 3 && time.Now().Before(deadline) {
		line, err := rd.ReadString('\n')
		if err != nil {
			break
		}
		if line == "event: ping\n" {
			pings++
		}
	}
	if pings < 3 {
		t.Fatalf("got %d ping events", pings)
	}
}

func TestServeMediaContentSafety(t *testing.T) {
	e := newReadEnv(t)
	const csp = "default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'; sandbox; frame-ancestors 'none'"
	w := do(e.h, "GET", fmt.Sprintf("/media/%d", e.media), nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || w.Header().Get("Content-Disposition") != "" ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Security-Policy") != csp {
		t.Fatalf("jpeg = %d %v", w.Code, w.Header())
	}

	bot, _ := e.st.UpsertBot(bg, &store.Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	doc := &model.Message{TgMessageID: 3, Source: model.SourceBotUpdate, Date: 3, Kind: model.KindDocument, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`),
		Media: []model.Media{{DedupeKey: "bot:html", Kind: "document", Mime: "text/html", FileName: "evil.html", Role: model.RoleMain}}}
	e.st.Ingest(bg, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Msg: doc, Now: 3})
	due, _ := e.st.DueMedia(bg, 0, 10)
	if len(due) != 1 {
		t.Fatalf("due = %+v", due)
	}
	os.WriteFile(filepath.Join(e.srv.MediaDir, "1", "h.html"), []byte("<script>alert(1)</script>"), 0o644)
	e.st.MarkMediaDone(bg, due[0].ID, "1/h.html", 25)
	w = do(e.h, "GET", fmt.Sprintf("/media/%d", due[0].ID), nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/octet-stream" ||
		!strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(w.Header().Get("Content-Disposition"), "evil.html") ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Security-Policy") != csp {
		t.Fatalf("html = %d %v", w.Code, w.Header())
	}
}

func TestHostCheck(t *testing.T) {
	e := newReadEnv(t)
	get := func(host string) int {
		req := httptest.NewRequest("GET", "/api/bots", nil)
		req.Host = host
		req.Header.Set("Remote-User", "shinya")
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, req)
		return w.Code
	}
	// A DNS rebinding attack needs a name of its own; addresses and localhost cannot be rebound.
	e.srv.Cfg.AllowedHosts = nil
	for _, h := range []string{"192.168.7.146:8090", "192.168.7.146", "127.0.0.1:8080", "[::1]:8080", "[fe80::1%25eth0]:80", "localhost:5173", "LocalHost"} {
		if c := get(h); c != 200 {
			t.Fatalf("host %s = %d", h, c)
		}
	}
	for _, h := range []string{"evil.example:8090", "example.com", "192.168.7.146.nip.io", "localhost.evil.example", ""} {
		if c := get(h); c != 403 {
			t.Fatalf("host %q = %d, want 403", h, c)
		}
	}
	e.srv.Cfg.AllowedHosts = []string{"tg.example.com"}
	for h, want := range map[string]int{"TG.example.com:443": 200, "tg.example.com.": 200, "tg.example.com": 200, "other.example.com": 403, "10.0.0.1": 200} {
		if c := get(h); c != want {
			t.Fatalf("allowed host %s = %d, want %d", h, c, want)
		}
	}
	e.srv.Cfg.AllowedHosts = []string{"*"}
	if c := get("anything.example"); c != 200 {
		t.Fatalf("* = %d", c)
	}
	// healthz goes through the same check; the container's own healthcheck uses 127.0.0.1.
	e.srv.Cfg.AllowedHosts = nil
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "host not allowed") {
		t.Fatalf("healthz on a foreign name = %d %s", w.Code, w.Body)
	}
}

func TestFramingAndEmbedding(t *testing.T) {
	e := newReadEnv(t)
	media, avatar := fmt.Sprintf("/media/%d", e.media), "/avatars/bots/777"
	os.MkdirAll(filepath.Join(e.srv.AvatarDir, "bots"), 0o755)
	os.WriteFile(filepath.Join(e.srv.AvatarDir, "bots", "777.jpg"), []byte("jpg"), 0o644)
	for _, p := range []string{"/", "/api/bots", "/api/nope", "/healthz", media, avatar, "/media/999999"} {
		w := do(e.h, "GET", p, nil)
		if w.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("%s = %d %v", p, w.Code, w.Header())
		}
		corp := w.Header().Get("Cross-Origin-Resource-Policy")
		if wantCORP := strings.HasPrefix(p, "/media/") || strings.HasPrefix(p, "/avatars/"); wantCORP != (corp == "same-origin") {
			t.Fatalf("%s CORP = %q", p, corp)
		}
	}
	// The SPA page gets nothing beyond frame-ancestors, which would break its scripts and styles.
	if csp := do(e.h, "GET", "/", nil).Header().Get("Content-Security-Policy"); csp != "frame-ancestors 'none'" {
		t.Fatalf("index CSP = %q", csp)
	}
	// Refused requests carry them too.
	req := httptest.NewRequest("GET", "/api/bots", nil)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 401 || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("401 = %d %v", w.Code, w.Header())
	}
}

func TestCrossSiteWritesRejected(t *testing.T) {
	e := newReadEnv(t)
	send := func(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Remote-User", "shinya")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, req)
		return w
	}
	del := fmt.Sprintf("/api/messages/%d", e.photoMsg)
	retry := fmt.Sprintf("/api/media/%d/retry", e.media)
	for name, w := range map[string]*httptest.ResponseRecorder{
		"cross-site delete": send("DELETE", del, "", map[string]string{"Sec-Fetch-Site": "cross-site"}),
		"same-site delete":  send("DELETE", del, "", map[string]string{"Sec-Fetch-Site": "same-site"}),
		"form post":         send("POST", retry, "a=1", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}),
		"text post":         send("POST", retry, "{}", map[string]string{"Content-Type": "text/plain"}),
		"post no type":      send("POST", retry, "{}", nil),
		// Without Sec-Fetch-Site (an untrusted plain-HTTP origin), an empty no-cors POST or a
		// fieldless form must not get through for lack of a body.
		"empty post":      send("POST", retry, "", nil),
		"empty form post": send("POST", retry, "", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}),
		"delete no type":  send("DELETE", del, "", nil),
		"foreign origin":  send("POST", retry, "{}", map[string]string{"Content-Type": "application/json", "Origin": "http://evil.example"}),
		"other port":      send("POST", retry, "{}", map[string]string{"Content-Type": "application/json", "Origin": "http://example.com:8081"}),
		"null origin":     send("POST", retry, "{}", map[string]string{"Content-Type": "application/json", "Origin": "null"}),
		"bad origin":      send("POST", retry, "{}", map[string]string{"Content-Type": "application/json", "Origin": "::"}),
	} {
		if w.Code != 403 || !strings.Contains(w.Body.String(), `"error":"cross-site request rejected"`) {
			t.Fatalf("%s = %d %s", name, w.Code, w.Body)
		}
	}
	if w := send("GET", "/api/bots", "", map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != 200 {
		t.Fatalf("cross-site GET = %d", w.Code)
	}
	if w := send("POST", retry, "{}", map[string]string{"Sec-Fetch-Site": "same-origin", "Content-Type": "application/json; charset=utf-8"}); w.Code != 409 {
		t.Fatalf("same-origin JSON post = %d %s", w.Code, w.Body)
	}
	// httptest requests are for example.com; the browser's own Origin matches Host.
	if w := send("POST", retry, "{}", map[string]string{"Content-Type": "application/json", "Origin": "http://EXAMPLE.com"}); w.Code != 409 {
		t.Fatalf("same-origin post with Origin = %d %s", w.Code, w.Body)
	}
	if w := send("DELETE", del, "", map[string]string{"Sec-Fetch-Site": "same-origin", "Content-Type": "application/json"}); w.Code != 204 {
		t.Fatalf("same-origin delete = %d %s", w.Code, w.Body)
	}
}

func TestGetMessage(t *testing.T) {
	e := newReadEnv(t)
	w := do(e.h, "GET", fmt.Sprintf("/api/messages/%d", e.photoMsg), nil)
	var v store.MessageView
	if err := json.Unmarshal(w.Body.Bytes(), &v); w.Code != 200 || err != nil {
		t.Fatalf("get = %d %s", w.Code, w.Body)
	}
	if v.ID != e.photoMsg || v.ChatID != e.chat || len(v.Media) != 1 || v.Media[0].ID != e.media {
		t.Fatalf("view = %+v", v)
	}
	if !strings.Contains(w.Body.String(), fmt.Sprintf(`"chat_id":%d`, e.chat)) {
		t.Fatalf("chat_id missing from JSON: %s", w.Body)
	}
	if w := do(e.h, "GET", "/api/messages/abc", nil); w.Code != 400 {
		t.Fatalf("bad id = %d", w.Code)
	}
	if w := do(e.h, "DELETE", fmt.Sprintf("/api/messages/%d", e.photoMsg), nil); w.Code != 204 {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := do(e.h, "GET", fmt.Sprintf("/api/messages/%d", e.photoMsg), nil); w.Code != 404 || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("deleted = %d %s", w.Code, w.Body)
	}
}

// heldSource reports half of a 10-byte file, then blocks until its context ends.
type heldSource struct{ started chan struct{} }

func (h heldSource) Fetch(c context.Context, _ *store.Media, _ string) (string, int64, error) {
	downloader.Report(c, 5, 10)
	close(h.started)
	<-c.Done()
	return "", 0, c.Err()
}

func TestDownloadsEndpoint(t *testing.T) {
	e := newReadEnv(t)
	bots, _ := e.st.ListBots(bg)
	ingestPhoto := func(tgID int64, key string) *store.IngestResult {
		m := &model.Message{TgMessageID: tgID, Source: model.SourceBotUpdate, Date: tgID, Kind: model.KindPhoto, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`),
			Media: []model.Media{{DedupeKey: key, Kind: "photo", Mime: "image/jpeg", Size: 10, Role: model.RoleMain}}}
		r, err := e.st.Ingest(bg, store.IngestInput{BotID: bots[0].ID, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: tgID})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	active := ingestPhoto(10, "bot:active")
	ingestPhoto(11, "bot:queued")
	failed := ingestPhoto(12, "bot:failed")
	due, _ := e.st.DueMedia(bg, 1<<40, 10)
	byKey := map[string]store.Media{}
	for _, m := range due {
		byKey[m.DedupeKey] = m
	}
	e.st.MarkMediaFailed(bg, byKey["bot:failed"].ID, 3, "HTTP 500")

	src := heldSource{started: make(chan struct{})}
	e.srv.Downloader.Register("bot", src)
	c, cancel := context.WithCancel(bg)
	done := make(chan struct{})
	m := byKey["bot:active"]
	go func() { e.srv.Downloader.Process(c, &m); close(done) }()
	<-src.started
	defer func() { cancel(); <-done }()

	w := do(e.h, "GET", "/api/downloads", nil)
	var got struct {
		Active []struct {
			MediaID   int64  `json:"media_id"`
			MessageID int64  `json:"message_id"`
			ChatID    int64  `json:"chat_id"`
			Done      int64  `json:"done"`
			Total     int64  `json:"total"`
			Kind      string `json:"kind"`
		} `json:"active"`
		Queued struct{ Count, Bytes int64 } `json:"queued"`
		Failed []struct {
			MessageID int64  `json:"message_id"`
			Error     string `json:"error"`
		} `json:"failed"`
		Speed *int64 `json:"speed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
		t.Fatalf("downloads = %d %s", w.Code, w.Body)
	}
	if len(got.Active) != 1 || got.Active[0].MediaID != m.ID || got.Active[0].MessageID != active.MessageID ||
		got.Active[0].ChatID != active.ChatID || got.Active[0].Done != 5 || got.Active[0].Total != 10 || got.Active[0].Kind != "photo" {
		t.Fatalf("active = %+v", got.Active)
	}
	if got.Queued.Count != 1 || got.Queued.Bytes != 10 {
		t.Fatalf("queued = %+v", got.Queued)
	}
	if len(got.Failed) != 1 || got.Failed[0].MessageID != failed.MessageID || got.Failed[0].Error != "HTTP 500" {
		t.Fatalf("failed = %+v", got.Failed)
	}
	if got.Speed == nil {
		t.Fatal("speed missing")
	}
}

func TestMessagesAfterAndAround(t *testing.T) {
	e := newReadEnv(t)
	bots, _ := e.st.ListBots(bg)
	var all []store.MessageView
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/chats/%d/messages", e.chat), nil).Body.Bytes(), &all)
	first, second := all[0].ID, all[1].ID
	for _, base := range []string{fmt.Sprintf("/api/chats/%d/messages", e.chat), fmt.Sprintf("/api/bots/%d/messages", bots[0].ID)} {
		var msgs []store.MessageView
		json.Unmarshal(do(e.h, "GET", fmt.Sprintf("%s?after=%d", base, first), nil).Body.Bytes(), &msgs)
		if len(msgs) != 1 || msgs[0].ID != second {
			t.Fatalf("%s after = %+v", base, msgs)
		}
		json.Unmarshal(do(e.h, "GET", fmt.Sprintf("%s?around=%d&limit=4", base, second), nil).Body.Bytes(), &msgs)
		if len(msgs) != 2 || msgs[0].ID != first || msgs[1].ID != second {
			t.Fatalf("%s around = %+v", base, msgs)
		}
		for _, q := range []string{"?after=1&around=2", "?before=1&after=2", "?around=x"} {
			if w := do(e.h, "GET", base+q, nil); w.Code != 400 {
				t.Fatalf("%s%s = %d", base, q, w.Code)
			}
		}
	}
}

func TestMarkRead(t *testing.T) {
	e := newReadEnv(t)
	ch, unsub := e.hub.Subscribe()
	defer unsub()
	path := fmt.Sprintf("/api/chats/%d/read", e.chat)
	if w := call(e.h, "POST", path, map[string]any{"message_id": e.photoMsg}); w.Code != 204 {
		t.Fatalf("read = %d %s", w.Code, w.Body)
	}
	if ev := <-ch; ev.Type != "chat.read" {
		t.Fatalf("event = %+v", ev)
	}
	var chats []store.ChatView
	json.Unmarshal(do(e.h, "GET", "/api/chats", nil).Body.Bytes(), &chats)
	if len(chats) != 1 || chats[0].LastReadPos != 0 || chats[0].Unread != 0 {
		t.Fatalf("chats = %+v (a private chat keeps no read position)", chats)
	}
	for _, c := range []struct {
		path string
		body any
		code int
	}{
		{path, map[string]any{}, 400},
		{path, map[string]any{"message_id": -1}, 400},
		{path, "x", 400},
		{"/api/chats/9999/read", map[string]any{"message_id": 1}, 404},
		{"/api/chats/abc/read", map[string]any{"message_id": 1}, 400},
	} {
		if w := call(e.h, "POST", c.path, c.body); w.Code != c.code {
			t.Fatalf("%s %v = %d, want %d", c.path, c.body, w.Code, c.code)
		}
	}
}

func TestListComments(t *testing.T) {
	e := newReadEnv(t)
	if err := e.st.UpsertChannel(bg, store.Channel{ChannelID: 500, Title: "News"}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.CreateWatch(bg, &store.Watch{ChannelID: 500, WindowMinutes: 30, Cond: "{}", Enabled: true, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	msg := func(id int64, src string) *model.Message {
		return &model.Message{TgMessageID: id, Source: src, Date: id, Kind: model.KindText, Text: fmt.Sprint("m", id), RawFormat: model.RawMTProto, Raw: json.RawMessage(`{}`)}
	}
	post, err := e.st.Ingest(bg, store.IngestInput{ChannelID: 500, Msg: msg(1, model.SourceChannelWatch), Now: 5})
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(10); i < 13; i++ {
		if _, err := e.st.Ingest(bg, store.IngestInput{ChannelID: 500, Msg: msg(i, model.SourceChannelComment), Now: 6, ThreadRootID: post.MessageID}); err != nil {
			t.Fatal(err)
		}
	}
	path := fmt.Sprintf("/api/chats/%d/posts/%d/comments?limit=2", post.ChatID, post.MessageID)
	w := do(e.h, "GET", path, nil)
	var got []store.MessageView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got) != 2 || got[1].Text != "m12" || got[1].ThreadRootID != post.MessageID {
		t.Fatalf("comments = %d %s", w.Code, w.Body)
	}
	if w := do(e.h, "GET", fmt.Sprintf("/api/chats/%d/messages", post.ChatID), nil); !strings.Contains(w.Body.String(), `"m1"`) || strings.Contains(w.Body.String(), `"m10"`) {
		t.Fatalf("timeline = %s", w.Body)
	}
	if w := do(e.h, "GET", fmt.Sprintf("/api/chats/%d/posts/%d/comments", post.ChatID, got[0].ID), nil); w.Code != 404 {
		t.Fatalf("comments of a comment = %d", w.Code)
	}
	if w := do(e.h, "GET", fmt.Sprintf("/api/chats/%d/posts/%d/comments?before=1&after=1", post.ChatID, post.MessageID), nil); w.Code != 400 {
		t.Fatalf("two cursors = %d", w.Code)
	}
}
