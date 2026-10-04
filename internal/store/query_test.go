package store

import (
	"errors"
	"testing"

	"tgarchive/internal/model"
)

func ids(vs []MessageView) []int64 {
	out := []int64{}
	for _, v := range vs {
		out = append(out, v.TgMessageID)
	}
	return out
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListChats(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	bob := model.Sender{TgUserID: 7, FirstName: "Bob"}
	ingest(t, s, bot, textMsg(1, "from alice"))
	if _, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: bob, Msg: textMsg(50, "from bob"), Now: 1}); err != nil {
		t.Fatal(err)
	}
	chats, err := s.ListChats(ctx, 0)
	if err != nil || len(chats) != 2 {
		t.Fatalf("chats = %+v, %v", chats, err)
	}
	if chats[0].Sender.FirstName != "Bob" || chats[0].LastText != "from bob" || chats[0].LastKind != "text" || chats[0].BotID != bot {
		t.Fatalf("newest chat first: %+v", chats[0])
	}
	if other, _ := s.ListChats(ctx, bot+1); len(other) != 0 {
		t.Fatal("bot filter not applied")
	}
}

func TestListMessagesPaging(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	var chat int64
	for i := int64(1); i <= 5; i++ {
		chat = ingest(t, s, bot, textMsg(i, "m")).ChatID
	}
	page, err := s.ListMessages(ctx, chat, 0, 2)
	if err != nil || !eq(ids(page), []int64{4, 5}) {
		t.Fatalf("latest page = %v, %v", ids(page), err)
	}
	older, _ := s.ListMessages(ctx, chat, page[0].ID, 2)
	if !eq(ids(older), []int64{2, 3}) {
		t.Fatalf("older page = %v", ids(older))
	}
	if string(page[0].Entities) != "[]" || page[0].Media == nil {
		t.Fatalf("entities/media must be non-null: %s %v", page[0].Entities, page[0].Media)
	}
}

func TestListMessagesKeepsAlbumWhole(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	chat := ingest(t, s, bot, textMsg(1, "before")).ChatID
	for i, k := range []string{"bot:a", "bot:b", "bot:c"} {
		m := photoMsg(int64(2+i), k)
		m.MediaGroupID = "g"
		ingest(t, s, bot, m)
	}
	page, _ := s.ListMessages(ctx, chat, 0, 2)
	if !eq(ids(page), []int64{2, 3, 4}) {
		t.Fatalf("album split by page boundary: %v", ids(page))
	}
}

func TestHydrateMediaAndReply(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	first := ingest(t, s, bot, textMsg(1, "original"))
	m := photoMsg(2, "bot:main")
	m.Media = append(m.Media, model.Media{DedupeKey: "bot:thumb", SourceRef: "t", Kind: "photo", Role: model.RoleThumb})
	m.ReplyToTgMessageID = 1
	ingest(t, s, bot, m)
	page, _ := s.ListMessages(ctx, first.ChatID, 0, 10)
	got := page[1]
	if got.Reply == nil || got.Reply.ID != first.MessageID || got.Reply.Text != "original" {
		t.Fatalf("reply = %+v", got.Reply)
	}
	if len(got.Media) != 2 || got.Media[0].Role != model.RoleMain || got.Media[1].Role != model.RoleThumb || got.Media[0].State != StatePending {
		t.Fatalf("media = %+v", got.Media)
	}
	s.DeleteMessage(ctx, first.MessageID, 1)
	page, _ = s.ListMessages(ctx, first.ChatID, 0, 10)
	if len(page) != 1 || page[0].Reply != nil {
		t.Fatalf("deleted message must disappear and not be quoted: %+v", page)
	}
}

func TestListChatMedia(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	chat := ingest(t, s, bot, photoMsg(1, "bot:p")).ChatID
	doc := &model.Message{TgMessageID: 2, Source: model.SourceBotUpdate, Date: 2, Kind: model.KindDocument, RawFormat: model.RawBotAPI, Raw: []byte(`{}`),
		Media: []model.Media{{DedupeKey: "bot:d", Kind: "document", Role: model.RoleMain}}}
	ingest(t, s, bot, doc)
	link := textMsg(3, "see https://example.com")
	link.Entities = []model.Entity{{Type: "url", Offset: 4, Length: 19}}
	ingest(t, s, bot, link)
	ingest(t, s, bot, textMsg(4, "plain"))
	for typ, want := range map[string][]int64{"media": {1}, "file": {2}, "link": {3}} {
		got, err := s.ListChatMedia(ctx, chat, typ, 0, 50)
		if err != nil || !eq(ids(got), want) {
			t.Fatalf("%s = %v, %v", typ, ids(got), err)
		}
	}
	if _, err := s.ListChatMedia(ctx, chat, "bogus", 0, 50); !errors.Is(err, ErrBadMediaType) {
		t.Fatalf("bad type err = %v", err)
	}
}
