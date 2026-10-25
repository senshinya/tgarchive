package store

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"tgarchive/internal/model"
)

func libraryVideo(t *testing.T, s *Store, bot, id int64, group, text string) *IngestResult {
	t.Helper()
	m := kindMsg(id, model.KindVideo, "video", fmt.Sprintf("video:%d", id))
	m.MediaGroupID = group
	m.Text = text
	m.Media[0].Mime = "video/mp4"
	m.Media[0].FileName = fmt.Sprintf("片段_%d.mp4", id)
	m.Media[0].Duration = 60
	return ingest(t, s, bot, m)
}

func TestVideoLibraryPostsAndFilters(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	photo := photoMsg(10, "photo:caption")
	photo.MediaGroupID = "album"
	photo.Text = "中文标题\n完整说明"
	first := ingest(t, s, bot, photo)
	a := libraryVideo(t, s, bot, 11, "album", "")
	libraryVideo(t, s, bot, 12, "album", "第二段")
	libraryVideo(t, s, bot, 20, "", "独立视频")
	for _, kind := range []string{"animation", "video_note"} {
		m := kindMsg(30+int64(len(kind)), model.Kind(kind), kind, kind)
		m.Media[0].Mime = "video/mp4"
		ingest(t, s, bot, m)
	}
	doc := kindMsg(50, model.KindDocument, "document", "video:doc")
	doc.Media[0].Mime = "video/webm"
	doc.Text = ""
	doc.Media[0].FileName = "100%_中文.webm"
	ingest(t, s, bot, doc)
	if _, _, err := s.Favorite(ctx, a.MessageID, 123, []string{"电影"}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListVideoLibrary(ctx, VideoLibraryQuery{Limit: 1, Sort: "old"})
	if err != nil || len(page.Items) != 1 || page.TotalItems != 3 || page.TotalVideos != 4 || page.Collections != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	item := page.Items[0]
	if item.ID != first.MessageID || item.Message.ID != a.MessageID || item.Message.Favorite == nil || item.Title != "中文标题" || item.Description != "中文标题\n完整说明\n\n第二段" || len(item.Videos) != 2 || item.Duration != 120 {
		t.Fatalf("item=%+v", item)
	}
	next, err := s.ListVideoLibrary(ctx, VideoLibraryQuery{Limit: 1, Sort: "old", Cursor: page.Next})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == item.ID {
		t.Fatalf("next=%+v %v", next, err)
	}
	for _, q := range []string{"中文标题", "片段_11.mp4", "100%_"} {
		p, e := s.ListVideoLibrary(ctx, VideoLibraryQuery{Q: q})
		if e != nil || len(p.Items) != 1 {
			t.Fatalf("search %q: %+v %v", q, p, e)
		}
	}
	p, e := s.ListVideoLibrary(ctx, VideoLibraryQuery{Q: "100%X"})
	if e != nil || len(p.Items) != 0 {
		t.Fatalf("LIKE escape: %+v %v", p, e)
	}
	tags, _ := s.ListTags(ctx)
	p, e = s.ListVideoLibrary(ctx, VideoLibraryQuery{View: "favorites", TagID: tags[0].ID})
	if e != nil || len(p.Items) != 1 || p.Items[0].ID != item.ID {
		t.Fatalf("favorites %+v %v", p, e)
	}
	got, e := s.GetVideoLibraryItem(ctx, a.MessageID)
	if e != nil || got.ID != first.MessageID {
		t.Fatalf("member detail %+v %v", got, e)
	}
	if _, e = s.ListVideoLibrary(ctx, VideoLibraryQuery{Sort: "new", Cursor: page.Next}); !errors.Is(e, ErrBadVideoLibrary) {
		t.Fatalf("cross-sort cursor=%v", e)
	}
	for _, q := range []VideoLibraryQuery{{Kind: "gif"}, {Kind: "all single"}, {Sort: "new old"}, {Watched: "bad"}, {View: "bad"}, {Sort: "bad"}, {Cursor: "broken"}, {Limit: -1}, {ChatID: -1}} {
		if _, e := s.ListVideoLibrary(ctx, q); !errors.Is(e, ErrBadVideoLibrary) {
			t.Fatalf("bad query %+v: %v", q, e)
		}
	}
}

func TestVideoLibraryProgressMetadataDeletionAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	bot := seedBot(t, s, 777)
	a := libraryVideo(t, s, bot, 1, "g", "默认标题")
	b := libraryVideo(t, s, bot, 2, "g", "")
	item, e := s.GetVideoLibraryItem(ctx, a.MessageID)
	if e != nil {
		t.Fatal(e)
	}
	media := item.Videos[0].Media.ID
	for _, p := range []VideoProgress{{Position: -1}, {Position: 61, Duration: 60}, {Duration: math.Inf(1)}, {Position: math.NaN()}, {Duration: 1e10}} {
		if _, e := s.SaveVideoProgress(ctx, media, p, 100); !errors.Is(e, ErrBadVideoLibrary) {
			t.Fatalf("bad progress %+v: %v", p, e)
		}
	}
	saved, e := s.SaveVideoProgress(ctx, media, VideoProgress{Position: 20, Duration: 60}, 100)
	if e != nil || saved.UpdatedAt != 100 {
		t.Fatalf("save %+v %v", saved, e)
	}
	p, e := s.ListVideoLibrary(ctx, VideoLibraryQuery{View: "continue"})
	if e != nil || len(p.Items) != 1 {
		t.Fatalf("continue %+v %v", p, e)
	}
	title := "改名后的标题"
	cover := item.Videos[1].Media.ID
	got, e := s.UpdateVideoLibraryItem(ctx, b.MessageID, VideoLibraryUpdate{Title: &title, CoverMediaID: &cover})
	if e != nil || got.Title != title || got.CoverMediaID != cover {
		t.Fatalf("update %+v %v", got, e)
	}
	p, e = s.ListVideoLibrary(ctx, VideoLibraryQuery{Q: title})
	if e != nil || len(p.Items) != 1 {
		t.Fatal(p, e)
	}
	bad := int64(9999)
	if _, e := s.UpdateVideoLibraryItem(ctx, a.MessageID, VideoLibraryUpdate{CoverMediaID: &bad}); !errors.Is(e, ErrBadVideoLibrary) {
		t.Fatal(e)
	}
	long := strings.Repeat("中", 201)
	if _, e := s.UpdateVideoLibraryItem(ctx, a.MessageID, VideoLibraryUpdate{Title: &long}); !errors.Is(e, ErrBadVideoLibrary) {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	got, e = s.GetVideoLibraryItem(ctx, a.MessageID)
	if e != nil || got.Title != title || got.Videos[0].Progress.Position != 20 {
		t.Fatalf("reopen %+v %v", got, e)
	}
	if _, _, e := s.DeleteMessage(ctx, a.MessageID, 300); e != nil {
		t.Fatal(e)
	}
	if _, e := s.GetVideoLibraryItem(ctx, a.MessageID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := s.SaveVideoProgress(ctx, media, VideoProgress{}, 400); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	got, e = s.GetVideoLibraryItem(ctx, b.MessageID)
	if e != nil || strings.Contains(got.Description, "默认标题") || got.Title == title {
		t.Fatalf("deleted data leaked %+v %v", got, e)
	}
}

func TestVideoLibraryGroupingIsolationOrderingAndScope(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	a := libraryVideo(t, s, bot, 30, "same", "A")
	libraryVideo(t, s, bot, 10, "same", "A second")
	// A same-numbered group from another source, original channel, or chat is a separate post.
	for i, variant := range []string{"source", "origin", "chat"} {
		m := kindMsg(100+int64(i), model.KindVideo, "video", variant)
		m.MediaGroupID = "same"
		m.Text = variant
		sender := alice
		switch variant {
		case "source":
			m.Source = model.SourceUserbotFetch
		case "origin":
			m.OriginChatID = -100123
		case "chat":
			sender.TgUserID = 43
		}
		if _, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: sender, Msg: m, Now: 1}); err != nil {
			t.Fatal(err)
		}
	}
	comment := libraryVideo(t, s, bot, 200, "same", "secret comment")
	if _, err := s.db.Exec(`UPDATE messages SET thread_root_id=? WHERE id=?`, a.MessageID, comment.MessageID); err != nil {
		t.Fatal(err)
	}
	cm := mediaIDs(t, s, comment.MessageID)[0]
	if _, err := s.SaveVideoProgress(ctx, cm, VideoProgress{}, 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("comment progress %v", err)
	}
	if _, err := s.GetVideoLibraryItem(ctx, comment.MessageID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("comment detail %v", err)
	}
	p, err := s.ListVideoLibrary(ctx, VideoLibraryQuery{})
	if err != nil || p.TotalItems != 4 || p.TotalVideos != 5 || p.Collections != 1 {
		t.Fatalf("isolation %+v %v", p, err)
	}
	item, err := s.GetVideoLibraryItem(ctx, a.MessageID)
	if err != nil || len(item.Videos) != 2 || item.Videos[0].Message.TgMessageID != 10 || strings.Contains(item.Description, "secret") {
		t.Fatalf("order %+v %v", item, err)
	}
	for _, sort := range []string{"new", "old", "published", "duration", "title"} {
		q := VideoLibraryQuery{Sort: sort, Limit: 1}
		seen := map[int64]bool{}
		for i := 0; i < 5; i++ {
			page, e := s.ListVideoLibrary(ctx, q)
			if e != nil {
				t.Fatal(sort, e)
			}
			if len(page.Items) != 1 || seen[page.Items[0].ID] {
				t.Fatalf("%s repeated/missing page %+v", sort, page)
			}
			seen[page.Items[0].ID] = true
			q.Cursor = page.Next
			if q.Cursor == "" {
				break
			}
		}
		if len(seen) != 4 {
			t.Fatalf("%s visited %d", sort, len(seen))
		}
	}
	p, err = s.ListVideoLibrary(ctx, VideoLibraryQuery{ChatID: a.ChatID, Kind: "collection"})
	if err != nil || p.TotalItems != 1 || p.TotalVideos != 2 {
		t.Fatalf("scope %+v %v", p, err)
	}
}

func TestVideoLibraryCompletedResetAndDefaultMetadata(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	r := libraryVideo(t, s, bot, 1, "", "")
	item, err := s.GetVideoLibraryItem(ctx, r.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	media := item.Videos[0].Media.ID
	if item.Title != "片段_1.mp4" {
		t.Fatalf("file fallback %q", item.Title)
	}
	if _, err = s.SaveVideoProgress(ctx, media, VideoProgress{Duration: 100, Completed: true}, 50); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		watched string
		want    int64
	}{{"completed", 1}, {"in_progress", 0}, {"unwatched", 0}} {
		p, e := s.ListVideoLibrary(ctx, VideoLibraryQuery{Watched: c.watched})
		if e != nil || p.TotalItems != c.want {
			t.Fatalf("%s %+v %v", c.watched, p, e)
		}
	}
	if _, err = s.SaveVideoProgress(ctx, media, VideoProgress{}, 51); err != nil {
		t.Fatal(err)
	}
	p, e := s.ListVideoLibrary(ctx, VideoLibraryQuery{Watched: "unwatched"})
	if e != nil || p.TotalItems != 1 {
		t.Fatal(p, e)
	}
	title := "重命名"
	s.UpdateVideoLibraryItem(ctx, item.ID, VideoLibraryUpdate{Title: &title})
	title = ""
	zero := int64(0)
	got, e := s.UpdateVideoLibraryItem(ctx, item.ID, VideoLibraryUpdate{Title: &title, CoverMediaID: &zero})
	if e != nil || got.Title != item.Title || got.CoverMediaID != media {
		t.Fatalf("reset %+v %v", got, e)
	}
}

func TestVideoLibraryTitleTrimsLeadingBlankLines(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	r := libraryVideo(t, s, bot, 1, "", " \n\t中文首行\r\n第二行")
	got, e := s.GetVideoLibraryItem(ctx, r.MessageID)
	if e != nil || got.Title != "中文首行" {
		t.Fatalf("title=%q %v", got.Title, e)
	}
}

func TestVideoLibraryPlanHasNoPerPostCorrelatedScans(t *testing.T) {
	s := newStore(t)
	for _, chat := range []int64{0, 1} {
		q := VideoLibraryQuery{View: "favorites", TagID: 1, Q: "中文", ChatID: chat}
		where, filterArgs := videoLibraryWhere(q)
		cte, args := videoLibraryQueryCTE(q)
		args = append(args, filterArgs...)
		rows, err := s.db.Query("EXPLAIN QUERY PLAN "+cte+" SELECT id FROM summary s WHERE "+where, args...)
		if err != nil {
			t.Fatal(err)
		}
		searchedChat := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if strings.Contains(detail, "CORRELATED") {
				t.Errorf("library aggregation must join/group once, found %s", detail)
			}
			if strings.Contains(detail, "SEARCH m USING") && strings.Contains(detail, "chat_id=?") {
				searchedChat = true
			}
			if chat > 0 && strings.HasPrefix(detail, "SCAN m ") {
				t.Errorf("chat filter must constrain input before aggregation: %s", detail)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if chat > 0 && !searchedChat {
			t.Error("chat scope must use an indexed message search")
		}
	}
}

func BenchmarkVideoLibrary10000(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "library.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	statements := []string{
		`INSERT INTO bots(id,tg_bot_id,token_enc,created_at) VALUES(1,777,x'00',1)`,
		`INSERT INTO senders(tg_user_id,updated_at) VALUES(42,1),(43,1)`,
		`INSERT INTO chats(id,bot_id,sender_id) VALUES(1,1,42),(2,1,43)`,
		`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000)
   INSERT INTO messages(id,chat_id,tg_message_id,source,date,kind,text,raw_format,raw_json)
   SELECT x,CASE WHEN x<=5000 THEN 1 ELSE 2 END,x,'bot_update',x,'video','中文视频 '||x,'botapi','{}' FROM n`,
		`INSERT INTO media(id,dedupe_key,kind,mime,file_name,duration) SELECT id,'benchmark:'||id,'video','video/mp4','video_'||id||'.mp4',60 FROM messages`,
		`INSERT INTO message_media(message_id,media_id,role) SELECT id,id,'main' FROM messages`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			b.Fatal(err)
		}
	}
	for _, scope := range []struct {
		name  string
		chat  int64
		total int64
	}{{"all", 0, 10000}, {"chat", 1, 5000}} {
		q, _ := normalizeVideoLibraryQuery(VideoLibraryQuery{ChatID: scope.chat})
		cte, args := videoLibraryQueryCTE(q)
		b.Run(scope.name+"/count", func(b *testing.B) {
			for b.Loop() {
				var total int64
				if err := s.db.QueryRow(cte+` SELECT COUNT(*) FROM summary`, args...).Scan(&total); err != nil || total != scope.total {
					b.Fatal(total, err)
				}
			}
		})
		b.Run(scope.name+"/page_query", func(b *testing.B) {
			for b.Loop() {
				rows, err := s.db.Query(cte+` SELECT id,added FROM summary ORDER BY added DESC,id DESC LIMIT 25`, args...)
				if err != nil {
					b.Fatal(err)
				}
				n := 0
				for rows.Next() {
					var id, added int64
					if err := rows.Scan(&id, &added); err != nil {
						b.Fatal(err)
					}
					n++
				}
				rows.Close()
				if err := rows.Err(); err != nil || n != 25 {
					b.Fatal(n, err)
				}
			}
		})
		b.Run(scope.name+"/first_page", func(b *testing.B) {
			for b.Loop() {
				page, err := s.ListVideoLibrary(ctx, q)
				if err != nil || len(page.Items) != 24 || page.TotalItems != scope.total {
					b.Fatal(len(page.Items), page.TotalItems, err)
				}
			}
		})
	}
}
