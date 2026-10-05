package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

func TestGetArticle(t *testing.T) {
	e := newReadEnv(t)
	res, err := e.st.Ingest(bg, store.IngestInput{BotID: 1, Sender: model.Sender{TgUserID: 42, FirstName: "Alice"}, Now: 3, TelegraphPath: "Sample",
		Msg: &model.Message{TgMessageID: 3, Source: model.SourceBotUpdate, Date: 3, Kind: model.KindText, Text: "https://telegra.ph/Sample",
			RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if w := do(e.h, "GET", fmt.Sprintf("/api/messages/%d/article", res.MessageID), nil); w.Code != 404 {
		t.Fatalf("article before fetch = %d", w.Code)
	}
	var msg store.MessageView
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/messages/%d", res.MessageID), nil).Body.Bytes(), &msg)
	if msg.Article == nil || msg.Article.State != "queued" || msg.Article.URL != "https://telegra.ph/Sample" {
		t.Fatalf("message article = %+v", msg.Article)
	}

	j, _ := e.st.ClaimNextTelegraphJob(bg, 4)
	err = e.st.SaveArticle(bg, store.SaveArticleInput{JobID: j.ID, MessageID: res.MessageID, Path: "Sample", URL: "https://telegra.ph/Sample",
		Title: "Sample", AuthorName: "Anon", Views: 3, Now: 5,
		Media:  []store.ArticleMediaInput{{DedupeKey: "web:1", URL: "https://telegra.ph/file/1.jpg", Kind: "photo"}},
		Render: func(ids map[string]int64) (string, error) { return `[{"tag":"p","children":["hi"]}]`, nil }})
	if err != nil {
		t.Fatal(err)
	}
	w := do(e.h, "GET", fmt.Sprintf("/api/messages/%d/article", res.MessageID), nil)
	var got struct {
		Content json.RawMessage
		Media   []map[string]any
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("article = %d %s", w.Code, w.Body)
	}
	var raw map[string]any
	json.Unmarshal(w.Body.Bytes(), &raw)
	for _, k := range []string{"url", "title", "description", "author_name", "author_url", "views", "fetched_at", "content", "media"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("article JSON lacks %q: %s", k, w.Body)
		}
	}
	if string(got.Content) != `[{"tag":"p","children":["hi"]}]` || len(got.Media) != 1 || got.Media[0]["kind"] != "photo" || got.Media[0]["state"] != "pending" {
		t.Fatalf("article = %s", w.Body)
	}
	for _, k := range []string{"id", "kind", "state", "width", "height", "duration", "mime"} {
		if _, ok := got.Media[0][k]; !ok {
			t.Fatalf("article media JSON lacks %q: %s", k, w.Body)
		}
	}
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/messages/%d", res.MessageID), nil).Body.Bytes(), &msg)
	if msg.Article.State != "fetched" || msg.Article.Title != "Sample" || len(msg.Media) != 0 {
		t.Fatalf("message after fetch = %+v", msg)
	}

	if w := do(e.h, "GET", fmt.Sprintf("/api/messages/%d/article", e.photoMsg), nil); w.Code != 404 {
		t.Fatalf("article of a photo message = %d", w.Code)
	}
	if w := do(e.h, "GET", "/api/messages/abc/article", nil); w.Code != 400 {
		t.Fatalf("bad id = %d", w.Code)
	}
	if w := do(e.h, "DELETE", fmt.Sprintf("/api/messages/%d", res.MessageID), nil); w.Code != 204 {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := do(e.h, "GET", fmt.Sprintf("/api/messages/%d/article", res.MessageID), nil); w.Code != 404 {
		t.Fatalf("article after delete = %d", w.Code)
	}
}
