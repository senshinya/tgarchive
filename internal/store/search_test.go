package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

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

func hitIDs(hs []SearchHit) []int64 {
	out := make([]int64, len(hs))
	for i, h := range hs {
		out[i] = h.Message.ID
	}
	return out
}

func TestSearch(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	a := ingest(t, s, bot, textMsg(1, "今天天气很好")).MessageID
	b := ingest(t, s, bot, textMsg(2, "Hello World 100%")).MessageID
	c := ingest(t, s, bot, textMsg(3, "a_b 测试")).MessageID
	d := ingest(t, s, bot, textMsg(4, "换行\n第二行")).MessageID
	e := ingestAs(t, s, bot, model.Sender{TgUserID: 43, FirstName: "Bob"}, textMsg(5, "天气预报")).MessageID
	gone := ingest(t, s, bot, textMsg(6, "天气已删")).MessageID
	if _, _, err := s.DeleteMessage(ctx, gone, 9000); err != nil {
		t.Fatal(err)
	}
	chatA := ingest(t, s, bot, textMsg(7, "x")).ChatID
	cases := []struct {
		q    string
		conv int64
		want []int64
	}{
		{"天", 0, []int64{e, a}},
		{"天气", 0, []int64{e, a}},
		{"天气很", 0, []int64{a}},
		{"hello", 0, []int64{b}},
		{"WORLD", 0, []int64{b}},
		{"天气 预报", 0, []int64{e}},
		{"100%", 0, []int64{b}},
		{"%", 0, []int64{b}},
		{"a_b", 0, []int64{c}},
		{"_", 0, []int64{c}},
		{"二行", 0, []int64{d}},
		{"天气", chatA, []int64{a}},
		{"天气", -bot, []int64{e, a}},
		{"没有", 0, []int64{}},
	}
	for _, tc := range cases {
		hs, err := s.Search(ctx, tc.q, tc.conv, 0, 30)
		if err != nil {
			t.Fatalf("%q: %v", tc.q, err)
		}
		if !eq(hitIDs(hs), tc.want) {
			t.Errorf("Search(%q, %d) = %v, want %v", tc.q, tc.conv, hitIDs(hs), tc.want)
		}
	}
	var got []int64
	before := int64(0)
	for {
		hs, err := s.Search(ctx, "天", 0, before, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(hs) == 0 {
			break
		}
		got = append(got, hs[0].Message.ID)
		before = hs[0].Message.ID
	}
	if !eq(got, []int64{e, a}) {
		t.Fatalf("paging = %v", got)
	}
	if _, err := s.Search(ctx, "   ", 0, 0, 30); !errors.Is(err, ErrBadQuery) {
		t.Fatalf("blank query err = %v", err)
	}
}

func TestSearchFieldAndSnippet(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	doc := &model.Message{TgMessageID: 1, Source: model.SourceBotUpdate, Date: 1001, Kind: model.KindDocument, Text: "说明",
		RawFormat: model.RawBotAPI, Raw: []byte(`{}`), Media: []model.Media{
			{DedupeKey: "d1", SourceRef: "f-d1", Kind: "document", FileName: "年度报告.pdf", Role: model.RoleMain}}}
	ingest(t, s, bot, doc)
	link := ingest(t, s, bot, textMsg(2, "link")).MessageID
	if _, err := s.db.Exec(`INSERT INTO articles (message_id, path, url, title, description, content, fetched_at)
		VALUES (?, 'p', 'u', '文章标题', '', '[]', 1)`, link); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("前", 100) + "Needle" + strings.Repeat("后", 100)
	ingest(t, s, bot, textMsg(3, long))

	for q, field := range map[string]string{"报告": "files", "文章标": "article", "needle": "body"} {
		hs, err := s.Search(ctx, q, 0, 0, 30)
		if err != nil || len(hs) != 1 || hs[0].Field != field {
			t.Fatalf("%q: %+v %v", q, hs, err)
		}
	}
	hs, _ := s.Search(ctx, "needle", 0, 0, 30)
	h := hs[0]
	runes := []rune(h.Snippet)
	if runes[0] != '…' || runes[len(runes)-1] != '…' || len(runes) != 30+6+30+2 {
		t.Fatalf("snippet = %q (%d runes)", h.Snippet, len(runes))
	}
	if len(h.Ranges) != 1 || utf16Slice(h.Snippet, h.Ranges[0]) != "Needle" {
		t.Fatalf("ranges = %v", h.Ranges)
	}
	if h.Message.Text != long || h.Message.Media == nil {
		t.Fatalf("message not hydrated: %+v", h.Message)
	}
}

func TestMakeSnippetUTF16(t *testing.T) {
	snip, ranges := makeSnippet("😀😀天气 和 天气", []string{"天气"})
	if snip != "😀😀天气 和 天气" {
		t.Fatalf("snippet = %q", snip)
	}
	if len(ranges) != 2 || ranges[0] != [2]int{4, 2} || ranges[1] != [2]int{9, 2} {
		t.Fatalf("ranges = %v", ranges)
	}
	if s, r := makeSnippet("a  b\n\nc", []string{"b"}); s != "a b c" || r[0] != [2]int{2, 1} {
		t.Fatalf("whitespace = %q %v", s, r)
	}
}

func TestSearchTerms(t *testing.T) {
	if got := SearchTerms("  a  b a "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("dedup = %q", got)
	}
	if got := SearchTerms("1 2 3 4 5 6"); len(got) != 5 {
		t.Fatalf("max 5: %q", got)
	}
	if got := SearchTerms(" \n "); got != nil {
		t.Fatalf("blank = %q", got)
	}
}

// utf16Slice cuts s at a [start, len] range counted in UTF-16 code units, as the WebUI does.
func utf16Slice(s string, r [2]int) string {
	u := utf16.Encode([]rune(s))
	return string(utf16.Decode(u[r[0] : r[0]+r[1]]))
}
