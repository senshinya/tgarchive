package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

func TestVideoLibraryEndpoints(t *testing.T) {
	e := newReadEnv(t)
	e.srv.Now = func() time.Time { return time.Unix(12345, 0) }
	bots, _ := e.st.ListBots(bg)
	m := &model.Message{TgMessageID: 10, Source: model.SourceBotUpdate, Date: 10, Kind: model.KindVideo, Text: "中文测试", RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`), Media: []model.Media{{DedupeKey: "library:video", Kind: "video", Mime: "video/mp4", Role: model.RoleMain}}}
	res, err := e.st.Ingest(bg, store.IngestInput{BotID: bots[0].ID, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: 10})
	if err != nil {
		t.Fatal(err)
	}
	w := call(e.h, "GET", "/api/video-library", nil)
	var page store.VideoLibraryPage
	json.Unmarshal(w.Body.Bytes(), &page)
	if w.Code != 200 || len(page.Items) != 1 || page.TotalVideos != 1 {
		t.Fatalf("list=%d %s", w.Code, w.Body)
	}
	item := page.Items[0]
	path := fmt.Sprintf("/api/video-library/%d", res.MessageID)
	progress := fmt.Sprintf("/api/video-library/media/%d/progress", item.Videos[0].Media.ID)
	w = call(e.h, "PUT", progress, map[string]any{"position": 25.5, "duration": 100, "completed": false})
	var p store.VideoProgress
	json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 200 || p.Position != 25.5 || p.UpdatedAt != 12345 {
		t.Fatalf("progress=%d %s", w.Code, w.Body)
	}
	if w = call(e.h, "PATCH", path, map[string]any{"title": "新的标题", "cover_media_id": item.Videos[0].Media.ID}); w.Code != 200 || !strings.Contains(w.Body.String(), "新的标题") {
		t.Fatalf("patch=%d %s", w.Code, w.Body)
	}
	for _, q := range []string{"limit=0", "chat_id=-1", "tag_id=x", "kind=animation", "watched=x", "view=x", "sort=x", "cursor=x"} {
		if w = call(e.h, "GET", "/api/video-library?"+q, nil); w.Code != 400 {
			t.Fatalf("%s=%d %s", q, w.Code, w.Body)
		}
	}
	for _, body := range []any{map[string]any{"position": -1, "duration": 100}, map[string]any{"position": 101, "duration": 100}, "not-json", map[string]any{"position": 1e12, "duration": 1e12}, nil} {
		if w = call(e.h, "PUT", progress, body); w.Code != 400 {
			t.Fatalf("body=%+v => %d %s", body, w.Code, w.Body)
		}
	}
	for _, body := range []any{map[string]any{"title": strings.Repeat("字", 201)}, map[string]any{"cover_media_id": e.media}, "bad"} {
		if w = call(e.h, "PATCH", path, body); w.Code != 400 {
			t.Fatalf("patch %+v=%d %s", body, w.Code, w.Body)
		}
	}
	for _, bad := range []string{"/api/video-library/99999", "/api/video-library/" + fmt.Sprint(e.photoMsg)} {
		if w = call(e.h, "GET", bad, nil); w.Code != 404 {
			t.Fatalf("%s=%d", bad, w.Code)
		}
	}
	if w = call(e.h, "PUT", fmt.Sprintf("/api/video-library/media/%d/progress", e.media), map[string]any{}); w.Code != 404 {
		t.Fatalf("photo progress=%d %s", w.Code, w.Body)
	}
	// Existing Host/auth/Origin middleware protects every new write route.
	for _, c := range []struct {
		header, value string
		code          int
	}{{"Origin", "https://evil.test", 403}, {"Content-Type", "text/plain", 403}, {"Remote-User", "", 401}, {"Host", "evil.test", 403}} {
		req := httptest.NewRequest("PUT", progress, strings.NewReader(`{"position":1,"duration":100}`))
		req.Header.Set("Remote-User", "shinya")
		req.Header.Set("Content-Type", "application/json")
		if c.header == "Host" {
			req.Host = c.value
		} else {
			req.Header.Set(c.header, c.value)
		}
		rw := httptest.NewRecorder()
		e.h.ServeHTTP(rw, req)
		if rw.Code != c.code {
			t.Fatalf("%s=%d %s", c.header, rw.Code, rw.Body)
		}
	}
	if w = call(e.h, "DELETE", fmt.Sprintf("/api/messages/%d", res.MessageID), nil); w.Code != 200 && w.Code != 204 {
		t.Fatalf("delete=%d %s", w.Code, w.Body)
	}
	if w = call(e.h, "GET", path, nil); w.Code != 404 {
		t.Fatalf("deleted detail=%d %s", w.Code, w.Body)
	}
	if w = call(e.h, "PUT", progress, map[string]any{}); w.Code != 404 {
		t.Fatalf("deleted progress=%d %s", w.Code, w.Body)
	}
}
