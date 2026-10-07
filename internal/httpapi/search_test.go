package httpapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

func TestSearchEndpoint(t *testing.T) {
	e := newReadEnv(t)
	bots, _ := e.st.ListBots(bg)
	var page struct {
		Items []struct {
			Message struct {
				ID int64 `json:"id"`
			} `json:"message"`
			Field   string   `json:"field"`
			Snippet string   `json:"snippet"`
			Ranges  [][2]int `json:"ranges"`
		} `json:"items"`
		Next int64 `json:"next"`
	}
	for _, p := range []string{"/api/search?q=x.dev", fmt.Sprintf("/api/search?q=X.DEV&chat=%d", e.chat), fmt.Sprintf("/api/search?q=x.dev&chat=-%d", bots[0].ID)} {
		w := do(e.h, "GET", p, nil)
		if w.Code != 200 {
			t.Fatalf("%s = %d %s", p, w.Code, w.Body)
		}
		json.Unmarshal(w.Body.Bytes(), &page)
		if len(page.Items) != 1 || page.Items[0].Field != "body" || page.Items[0].Snippet != "see https://x.dev" ||
			len(page.Items[0].Ranges) != 1 || page.Next != 0 {
			t.Fatalf("%s = %s", p, w.Body)
		}
	}
	if w := do(e.h, "GET", "/api/search?q="+url.QueryEscape("x.dev")+"&chat=99999", nil); w.Code != 200 || w.Body.String() != "{\"items\":[],\"next\":0}\n" {
		t.Fatalf("other chat = %d %q", w.Code, w.Body)
	}
	e.st.Ingest(bg, ingestText(bots[0].ID, 3, "see x.dev again"))
	json.Unmarshal(do(e.h, "GET", "/api/search?q=x.dev&limit=1", nil).Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Next != page.Items[0].Message.ID {
		t.Fatalf("full page must carry next: %+v", page)
	}
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/search?q=x.dev&limit=1&before=%d", page.Next), nil).Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Snippet != "see https://x.dev" {
		t.Fatalf("second page = %+v", page)
	}
	for _, p := range []string{"/api/search", "/api/search?q=%20%20", "/api/search?q=a&limit=101", "/api/search?q=a&chat=x", "/api/search?q=a&before=-1"} {
		if w := do(e.h, "GET", p, nil); w.Code != 400 {
			t.Fatalf("%s = %d", p, w.Code)
		}
	}
}

func ingestText(bot, tgID int64, text string) store.IngestInput {
	return store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42, FirstName: "Alice"}, Now: tgID,
		Msg: &model.Message{TgMessageID: tgID, Source: model.SourceBotUpdate, Date: tgID, Kind: model.KindText, Text: text,
			RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}}
}
