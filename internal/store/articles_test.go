package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	imgA = "https://telegra.ph/file/a.jpg"
	vidB = "https://cdn.example.com/b.mp4"
)

// saveArticle claims the next job (which must belong to msgID) and stores an article whose
// content lists the media ids it was given.
func saveArticle(t *testing.T, s *Store, msgID int64, media ...ArticleMediaInput) {
	t.Helper()
	j, err := s.ClaimNextTelegraphJob(ctx, 6000)
	if err != nil || j.MessageID != msgID {
		t.Fatalf("claim = %+v, %v", j, err)
	}
	err = s.SaveArticle(ctx, SaveArticleInput{
		JobID: j.ID, MessageID: msgID, Path: j.Path, URL: "https://telegra.ph/" + j.Path, Title: "Title " + j.Path,
		Description: "desc", AuthorName: "Author", AuthorURL: "https://t.me/author", ImageURL: imgA, Views: 12, Media: media,
		Render: func(ids map[string]int64) (string, error) {
			b, err := json.Marshal(ids)
			return fmt.Sprintf(`[{"tag":"p","children":[%q]}]`, string(b)), err
		},
		Now: 6001,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func webMedia(url, kind string) ArticleMediaInput {
	return ArticleMediaInput{DedupeKey: "web:" + url, URL: url, Kind: kind}
}

func TestSaveAndGetArticle(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res := ingestLink(t, s, bot, 1, "Sample")

	v, _ := s.GetMessageView(ctx, res.MessageID)
	if v.Article == nil || v.Article.State != TelegraphQueued || v.Article.URL != "https://telegra.ph/Sample" || v.Article.Title != "" {
		t.Fatalf("queued summary = %+v", v.Article)
	}
	if _, err := s.GetArticle(ctx, res.MessageID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("article before fetch: %v", err)
	}

	saveArticle(t, s, res.MessageID, webMedia(imgA, "photo"), webMedia(vidB, "video"))

	a, err := s.GetArticle(ctx, res.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Title != "Title Sample" || a.AuthorURL != "https://t.me/author" || a.Views != 12 || a.FetchedAt != 6001 || len(a.Media) != 2 {
		t.Fatalf("article = %+v", a)
	}
	if a.Media[0].Kind != "photo" || a.Media[1].Kind != "video" || a.Media[0].State != StatePending {
		t.Fatalf("article media = %+v", a.Media)
	}
	if !strings.Contains(string(a.Content), fmt.Sprintf(`\"%s\":%d`, imgA, a.Media[0].ID)) {
		t.Fatalf("content not rendered with ids: %s", a.Content)
	}
	if j, _ := s.GetTelegraphJob(ctx, res.MessageID); j.State != TelegraphFetched {
		t.Fatalf("job = %+v", j)
	}

	v, _ = s.GetMessageView(ctx, res.MessageID)
	if len(v.Media) != 0 {
		t.Fatalf("article media leaked into the message's own media: %+v", v.Media)
	}
	if v.Article.State != TelegraphFetched || v.Article.Title != "Title Sample" || v.Article.AuthorName != "Author" || v.Article.ImageMediaID != 0 {
		t.Fatalf("summary before cover download = %+v", v.Article)
	}
	if _, err := s.MarkMediaDone(ctx, a.Media[0].ID, "web/2026/10/x.jpg", 3); err != nil {
		t.Fatal(err)
	}
	v, _ = s.GetMessageView(ctx, res.MessageID)
	if v.Article.ImageMediaID != a.Media[0].ID {
		t.Fatalf("summary after cover download = %+v", v.Article)
	}
	a, _ = s.GetArticle(ctx, res.MessageID)
	if a.Media[0].Mime != "image/jpeg" {
		t.Fatalf("mime from extension = %q", a.Media[0].Mime)
	}
	shared, _ := s.ListChatMedia(ctx, res.ChatID, "media", 0, 50)
	if len(shared) != 0 {
		t.Fatalf("article media in shared media: %+v", shared)
	}
	if ids, _ := s.MessagesForMedia(ctx, a.Media[1].ID); len(ids) != 1 || ids[0] != res.MessageID {
		t.Fatalf("MessagesForMedia = %v", ids)
	}
}

func TestArticleMediaSharedAcrossSnapshots(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	r1 := ingestLink(t, s, bot, 1, "A")
	r2 := ingestLink(t, s, bot, 2, "A")
	saveArticle(t, s, r1.MessageID, webMedia(imgA, "photo"))
	saveArticle(t, s, r2.MessageID, webMedia(imgA, "photo"))
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM media").Scan(&n)
	if n != 1 {
		t.Fatalf("media rows = %d, want 1 (deduped)", n)
	}
	a, _ := s.GetArticle(ctx, r1.MessageID)
	s.MarkMediaDone(ctx, a.Media[0].ID, "web/2026/10/a.jpg", 3)

	if _, orphans, err := s.DeleteMessage(ctx, r1.MessageID, 7000); err != nil || len(orphans) != 0 {
		t.Fatalf("delete first snapshot: orphans %v, %v", orphans, err)
	}
	if _, err := s.GetArticle(ctx, r1.MessageID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("article of deleted message: %v", err)
	}
	var arts int
	s.db.QueryRow("SELECT COUNT(*) FROM articles").Scan(&arts)
	if arts != 1 {
		t.Fatalf("articles rows = %d", arts)
	}
	_, orphans, err := s.DeleteMessage(ctx, r2.MessageID, 7001)
	if err != nil || len(orphans) != 1 || orphans[0] != "web/2026/10/a.jpg" {
		t.Fatalf("delete last snapshot: orphans %v, %v", orphans, err)
	}
}

func TestEditKeepsArticleMedia(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res := ingestLink(t, s, bot, 1, "A")
	saveArticle(t, s, res.MessageID, webMedia(imgA, "photo"))
	edited, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: alice, Msg: textMsg(1, "https://telegra.ph/A "), Now: 8000})
	if err != nil || edited.Created || len(edited.OrphanPaths) != 0 {
		t.Fatalf("edit = %+v, %v", edited, err)
	}
	a, _ := s.GetArticle(ctx, res.MessageID)
	if len(a.Media) != 1 {
		t.Fatalf("article media after edit = %+v", a.Media)
	}
}

func TestSaveArticleAfterDelete(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res := ingestLink(t, s, bot, 1, "A")
	j, _ := s.ClaimNextTelegraphJob(ctx, 6000)
	s.DeleteMessage(ctx, res.MessageID, 6001)
	err := s.SaveArticle(ctx, SaveArticleInput{JobID: j.ID, MessageID: res.MessageID, Path: "A", Media: []ArticleMediaInput{webMedia(imgA, "photo")},
		Render: func(map[string]int64) (string, error) { return "[]", nil }, Now: 6002})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SaveArticle after delete = %v", err)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM media").Scan(&n)
	if n != 0 {
		t.Fatalf("media rows = %d after a rolled-back save", n)
	}
}
