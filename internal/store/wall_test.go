package store

import (
	"errors"
	"strings"
	"testing"

	"tgarchive/internal/model"
)

func kindMsg(id int64, kind model.Kind, mediaKind, key string) *model.Message {
	m := photoMsg(id, key)
	m.Kind = kind
	m.Media[0].Kind = mediaKind
	return m
}

func TestListAllMedia(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	seedWatch(t, s)
	ingest(t, s, bot, photoMsg(1, "bot:p"))
	ingest(t, s, bot, kindMsg(2, model.KindVideo, "video", "bot:v"))
	ingest(t, s, bot, kindMsg(3, model.KindDocument, "document", "bot:d"))
	gone := ingest(t, s, bot, photoMsg(4, "bot:gone")).MessageID
	if _, _, err := s.DeleteMessage(ctx, gone, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(50, "", "mt:photo:1"), Now: 9000}); err != nil {
		t.Fatal(err)
	}
	gifPost := channelPost(51, "", "mt:doc:2")
	gifPost.Kind = model.KindAnimation
	gifPost.Media[0].Kind = "animation"
	if _, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: gifPost, Now: 9001}); err != nil {
		t.Fatal(err)
	}
	ingest(t, s, bot, textMsg(5, "plain"))

	for _, c := range []struct {
		typ, source string
		want        []int64
	}{
		{"all", "all", []int64{51, 50, 2, 1}},
		{"photo", "all", []int64{50, 1}},
		{"video", "all", []int64{51, 2}},
		{"all", "private", []int64{2, 1}},
		{"all", "channel", []int64{51, 50}},
		{"photo", "channel", []int64{50}},
	} {
		got, err := s.ListAllMedia(ctx, c.typ, c.source, 0, 50)
		if err != nil || !eq(ids(got), c.want) {
			t.Fatalf("%s/%s = %v, %v (want %v)", c.typ, c.source, ids(got), err, c.want)
		}
	}
	page, _ := s.ListAllMedia(ctx, "all", "all", 0, 2)
	if !eq(ids(page), []int64{51, 50}) {
		t.Fatalf("first page = %v", ids(page))
	}
	if page[0].Media == nil || len(page[0].Media) == 0 {
		t.Fatalf("hydrated media missing: %+v", page[0])
	}
	if next, _ := s.ListAllMedia(ctx, "all", "all", page[1].ID, 2); !eq(ids(next), []int64{2, 1}) {
		t.Fatalf("second page = %v", ids(next))
	}
	for _, bad := range [][2]string{{"bogus", "all"}, {"all", "bogus"}, {"file", "all"}} {
		if _, err := s.ListAllMedia(ctx, bad[0], bad[1], 0, 50); !errors.Is(err, ErrBadMediaType) {
			t.Fatalf("%v err = %v", bad, err)
		}
	}
}

func TestListAllMediaPlan(t *testing.T) {
	s := newStore(t)
	for _, typ := range []string{"all", "photo", "video"} {
		for _, source := range []string{"all", "private", "channel"} {
			q, args, err := allMediaQuery(typ, source, 10, 50)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := s.db.Query("EXPLAIN QUERY PLAN "+q, args...)
			if err != nil {
				t.Fatal(err)
			}
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				rows.Scan(&id, &parent, &unused, &detail)
				plan = append(plan, detail)
			}
			rows.Close()
			joined := strings.Join(plan, " | ")
			if strings.Contains(joined, "TEMP B-TREE") || !strings.Contains(joined, "SEARCH m USING INTEGER PRIMARY KEY") {
				t.Fatalf("%s/%s plan: %s", typ, source, joined)
			}
		}
	}
}
