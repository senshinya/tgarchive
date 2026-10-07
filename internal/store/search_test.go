package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"tgarchive/internal/model"
)

// indexed returns the search row of message id (ok false when it has none).
func indexed(t *testing.T, s *Store, id int64) (body, files, article string, ok bool) {
	t.Helper()
	err := s.db.QueryRow("SELECT body, files, article FROM search_fts WHERE rowid = ?", id).Scan(&body, &files, &article)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return body, files, article, true
}

func TestSearchIndexFollowsMessages(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	a := ingest(t, s, bot, textMsg(1, "今天天气很好")).MessageID
	if body, _, _, ok := indexed(t, s, a); !ok || body != "今天天气很好" {
		t.Fatalf("after insert: %q %v", body, ok)
	}
	if _, err := s.db.Exec("UPDATE messages SET text = '新文本' WHERE id = ?", a); err != nil {
		t.Fatal(err)
	}
	if body, _, _, _ := indexed(t, s, a); body != "新文本" {
		t.Fatalf("after edit: %q", body)
	}
	if _, _, err := s.DeleteMessage(ctx, a, 9000); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := indexed(t, s, a); ok {
		t.Fatal("a deleted message must leave the index")
	}
	b := ingest(t, s, bot, textMsg(2, "second")).MessageID
	if _, err := s.db.Exec("DELETE FROM messages WHERE id = ?", b); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := indexed(t, s, b); ok {
		t.Fatal("a hard-deleted message must leave the index")
	}
}

func TestSearchIndexFiles(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	doc := &model.Message{TgMessageID: 1, Source: model.SourceBotUpdate, Date: 1001, Kind: model.KindDocument,
		RawFormat: model.RawBotAPI, Raw: []byte(`{}`), Media: []model.Media{
			{DedupeKey: "d1", SourceRef: "f-d1", Kind: "document", FileName: "报告.pdf", Role: model.RoleMain}}}
	id := ingest(t, s, bot, doc).MessageID
	if _, files, _, ok := indexed(t, s, id); !ok || files != "报告.pdf" {
		t.Fatalf("files = %q %v", files, ok)
	}
	p := photoMsg(2, "p1")
	p.Media[0].FileName = "photo.jpg"
	pid := ingest(t, s, bot, p).MessageID
	if _, files, _, _ := indexed(t, s, pid); files != "" {
		t.Fatalf("photo file names are not searchable, got %q", files)
	}
}

func TestSearchIndexArticle(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	id := ingest(t, s, bot, textMsg(1, "link")).MessageID
	if _, err := s.db.Exec(`INSERT INTO articles (message_id, path, url, title, description, content, fetched_at)
		VALUES (?, 'p', 'u', '标题', '描述', ?, 1)`, id,
		`[{"tag":"p","children":["正文 A&B ",{"tag":"a","attrs":{"href":"https://x"},"children":["链接字"]}]}]`); err != nil {
		t.Fatal(err)
	}
	_, _, article, _ := indexed(t, s, id)
	for _, want := range []string{"标题", "描述", "正文 A&B", "链接字"} {
		if !strings.Contains(article, want) {
			t.Fatalf("article %q lacks %q", article, want)
		}
	}
	for _, not := range []string{"https://x", "href", `"tag"`} {
		if strings.Contains(article, not) {
			t.Fatalf("article %q must not contain %q", article, not)
		}
	}
	if _, err := s.db.Exec("DELETE FROM articles WHERE message_id = ?", id); err != nil {
		t.Fatal(err)
	}
	if _, _, article, _ := indexed(t, s, id); article != "" {
		t.Fatalf("after delete: %q", article)
	}
}

func TestMigration7Backfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	old := openAt(t, path, 6)
	bot := seedBot(t, old, 777)
	a := ingest(t, old, bot, textMsg(1, "旧消息")).MessageID
	b := ingest(t, old, bot, textMsg(2, "另一条")).MessageID
	if _, _, err := old.DeleteMessage(ctx, b, 9000); err != nil {
		t.Fatal(err)
	}
	if err := old.migrate(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if body, _, _, ok := indexed(t, old, a); !ok || body != "旧消息" {
		t.Fatalf("backfill: %q %v", body, ok)
	}
	if _, _, _, ok := indexed(t, old, b); ok {
		t.Fatal("deleted messages are not backfilled")
	}
	var lastRead int64
	if err := old.db.QueryRow("SELECT last_read_id FROM chats").Scan(&lastRead); err != nil || lastRead != b {
		t.Fatalf("last_read_id = %d, %v (want the newest message %d)", lastRead, err, b)
	}
	old.db.Close()
}
