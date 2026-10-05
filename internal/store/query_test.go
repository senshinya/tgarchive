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

func TestGetMessageView(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	first := ingest(t, s, bot, textMsg(1, "hello"))
	reply := textMsg(2, "re")
	reply.ReplyToTgMessageID = 1
	second := ingest(t, s, bot, reply)
	photo := ingest(t, s, bot, photoMsg(3, "bot:p"))

	v, err := s.GetMessageView(ctx, second.MessageID)
	if err != nil || v.ID != second.MessageID || v.ChatID != first.ChatID || v.Text != "re" {
		t.Fatalf("view = %+v, %v", v, err)
	}
	if v.Reply == nil || v.Reply.ID != first.MessageID || v.Reply.Text != "hello" {
		t.Fatalf("reply = %+v", v.Reply)
	}
	if pv, _ := s.GetMessageView(ctx, photo.MessageID); len(pv.Media) != 1 || pv.Media[0].State != StatePending {
		t.Fatalf("media not hydrated: %+v", pv.Media)
	}
	page, _ := s.ListMessages(ctx, first.ChatID, 0, 10)
	if page[0].ChatID != first.ChatID {
		t.Fatalf("list views must carry chat_id: %+v", page[0])
	}
	if _, _, err := s.DeleteMessage(ctx, first.MessageID, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessageView(ctx, first.MessageID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted message err = %v", err)
	}
	if _, err := s.GetMessageView(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing message err = %v", err)
	}
}

// ingestAs archives msg in the chat of bot × sender.
func ingestAs(t *testing.T, s *Store, bot int64, sender model.Sender, msg *model.Message) *IngestResult {
	t.Helper()
	r, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: sender, Msg: msg, Now: msg.Date})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func chatIDs(vs []MessageView) []int64 {
	out := []int64{}
	for _, v := range vs {
		out = append(out, v.ChatID)
	}
	return out
}

func TestListBotMessagesMergesSenders(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	other := seedBot(t, s, 888)
	alice := model.Sender{TgUserID: 42, FirstName: "Alice"}
	bob := model.Sender{TgUserID: 7, FirstName: "Bob"}
	a := ingestAs(t, s, bot, alice, textMsg(1, "a1")).ChatID
	b := ingestAs(t, s, bot, bob, textMsg(1, "b1")).ChatID
	ingestAs(t, s, bot, alice, textMsg(2, "a2"))
	ingestAs(t, s, other, alice, textMsg(1, "elsewhere"))
	ingestAs(t, s, bot, bob, textMsg(2, "b2"))

	page, err := s.ListBotMessages(ctx, bot, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !eq(chatIDs(page), []int64{b, a, b}) {
		t.Fatalf("latest page chats = %v (alice %d bob %d)", chatIDs(page), a, b)
	}
	older, _ := s.ListBotMessages(ctx, bot, page[0].ID, 3)
	if len(older) != 1 || older[0].ChatID != a || older[0].Text != "a1" {
		t.Fatalf("older page = %+v", older)
	}
	if none, _ := s.ListBotMessages(ctx, bot+100, 0, 10); len(none) != 0 {
		t.Fatalf("unknown bot = %+v", none)
	}
}

func TestListBotMessagesAlbumStaysInItsChat(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	alice := model.Sender{TgUserID: 42, FirstName: "Alice"}
	bob := model.Sender{TgUserID: 7, FirstName: "Bob"}
	// Bob's message shares a media_group_id string with Alice's album; it must not be pulled in.
	stray := photoMsg(1, "bot:stray")
	stray.MediaGroupID = "g"
	ingestAs(t, s, bot, bob, stray)
	for i, k := range []string{"bot:a", "bot:b", "bot:c"} {
		m := photoMsg(int64(1+i), k)
		m.MediaGroupID = "g"
		ingestAs(t, s, bot, alice, m)
	}
	page, _ := s.ListBotMessages(ctx, bot, 0, 2)
	if len(page) != 3 {
		t.Fatalf("album not completed or stray pulled in: %d messages", len(page))
	}
	for _, v := range page {
		if v.Media[0].Kind != "photo" || v.ChatID != page[0].ChatID {
			t.Fatalf("page mixes chats: %v", chatIDs(page))
		}
	}
}

func TestListBotMessagesResolvesRepliesPerChat(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	alice := model.Sender{TgUserID: 42, FirstName: "Alice"}
	bob := model.Sender{TgUserID: 7, FirstName: "Bob"}
	ingestAs(t, s, bot, alice, textMsg(1, "alice original"))
	bobFirst := ingestAs(t, s, bot, bob, textMsg(1, "bob original"))
	reply := textMsg(2, "re")
	reply.ReplyToTgMessageID = 1
	ingestAs(t, s, bot, bob, reply)
	page, _ := s.ListBotMessages(ctx, bot, 0, 10)
	last := page[len(page)-1]
	if last.Reply == nil || last.Reply.ID != bobFirst.MessageID || last.Reply.Text != "bob original" {
		t.Fatalf("reply = %+v", last.Reply)
	}
}

func TestListBotMedia(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	alice := model.Sender{TgUserID: 42, FirstName: "Alice"}
	bob := model.Sender{TgUserID: 7, FirstName: "Bob"}
	ingestAs(t, s, bot, alice, photoMsg(1, "bot:p1"))
	ingestAs(t, s, bot, bob, textMsg(1, "plain"))
	ingestAs(t, s, bot, bob, photoMsg(2, "bot:p2"))
	got, err := s.ListBotMedia(ctx, bot, "media", 0, 50)
	if err != nil || len(got) != 2 || got[0].Media[0].State != StatePending {
		t.Fatalf("bot media = %+v, %v", got, err)
	}
	if got[0].ChatID == got[1].ChatID {
		t.Fatal("expected media from both senders")
	}
	if _, err := s.ListBotMedia(ctx, bot, "bogus", 0, 50); !errors.Is(err, ErrBadMediaType) {
		t.Fatalf("bad type err = %v", err)
	}
}
