package store

import (
	"errors"
	"fmt"
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

func TestListBotMessagesNoGapBelowACompletedAlbum(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	alice := model.Sender{TgUserID: 42, FirstName: "Alice"}
	bob := model.Sender{TgUserID: 7, FirstName: "Bob"}
	// Alice's album arrives interleaved with Bob's messages (ids: a1 b1 a2 b2 a3).
	album := func(tg int64, key string) *model.Message {
		m := photoMsg(tg, key)
		m.MediaGroupID = "g"
		return m
	}
	ingestAs(t, s, bot, alice, album(1, "bot:a1"))
	ingestAs(t, s, bot, bob, textMsg(1, "b1"))
	ingestAs(t, s, bot, alice, album(2, "bot:a2"))
	ingestAs(t, s, bot, bob, textMsg(2, "b2"))
	ingestAs(t, s, bot, alice, album(3, "bot:a3"))
	page, _ := s.ListBotMessages(ctx, bot, 0, 1) // cuts inside the album
	if len(page) != 5 {
		var texts []string
		for _, v := range page {
			texts = append(texts, v.Text)
		}
		t.Fatalf("page has %d messages %v: Bob's messages between album parts were skipped", len(page), texts)
	}
	for i := 1; i < len(page); i++ {
		if page[i].ID <= page[i-1].ID {
			t.Fatal("page not ascending")
		}
	}
}

// pagedChat holds messages 1..20 of one chat, with 9, 10 and 11 one album; it returns the chat
// and the stored ids by Telegram id.
func pagedChat(t *testing.T, s *Store, bot int64) (int64, map[int64]int64) {
	t.Helper()
	byTg := map[int64]int64{}
	var chat int64
	for i := int64(1); i <= 20; i++ {
		var m = textMsg(i, "m")
		if i >= 9 && i <= 11 {
			m = photoMsg(i, fmt.Sprintf("bot:%d", i))
			m.MediaGroupID = "g"
		}
		res := ingest(t, s, bot, m)
		chat, byTg[i] = res.ChatID, res.MessageID
	}
	return chat, byTg
}

func TestListMessagesAfterAndAround(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	chat, id := pagedChat(t, s, bot)
	tg := func(vs []MessageView) []int64 {
		rev := map[int64]int64{}
		for k, v := range id {
			rev[v] = k
		}
		out := make([]int64, len(vs))
		for i, v := range vs {
			out[i] = rev[v.ID]
		}
		return out
	}
	cases := []struct {
		name  string
		p     Page
		limit int
		want  []int64
	}{
		{"after", Page{After: id[5]}, 3, []int64{6, 7, 8}},
		{"after completes the album", Page{After: id[7]}, 3, []int64{8, 9, 10, 11}},
		{"after the end", Page{After: id[20]}, 3, []int64{}},
		{"around an album", Page{Around: id[10]}, 4, []int64{9, 10, 11, 12}},
		{"around", Page{Around: id[15]}, 6, []int64{13, 14, 15, 16, 17, 18}},
		{"around the newest", Page{Around: id[20]}, 6, []int64{18, 19, 20}},
		{"before", Page{Before: id[3]}, 5, []int64{1, 2}},
	}
	for _, c := range cases {
		got, err := s.ListMessagesPage(ctx, chat, c.p, c.limit)
		if err != nil || !eq(tg(got), c.want) {
			t.Errorf("%s: %v, %v; want %v", c.name, tg(got), err, c.want)
		}
	}
	if _, _, err := s.DeleteMessage(ctx, id[15], 9000); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ListMessagesPage(ctx, chat, Page{Around: id[15]}, 4); !eq(tg(got), []int64{13, 14, 16, 17}) {
		t.Errorf("around a deleted message: %v", tg(got))
	}
	merged, err := s.ListBotMessagesPage(ctx, bot, Page{Around: id[10]}, 4)
	if err != nil || !eq(tg(merged), []int64{9, 10, 11, 12}) {
		t.Errorf("bot timeline around: %v, %v", tg(merged), err)
	}
}

// A reply quotes a message from the same message id space: a fetched channel post's reply id is
// the source channel's, and a watched post's is its channel's.
func TestReplyQuoteMatchesItsSource(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	fetched := func(id, origin, replyTo int64, text string) *model.Message {
		m := textMsg(id, text)
		m.Source = model.SourceUserbotFetch
		m.OriginChatID = origin
		m.ReplyToTgMessageID = replyTo
		return m
	}
	ingest(t, s, bot, textMsg(349, "unrelated bot message"))
	ingest(t, s, bot, fetched(348, -1002, 0, "other channel's 348"))
	ingest(t, s, bot, fetched(347, -1001, 0, "channel A 347"))
	for _, c := range []struct {
		msg  *model.Message
		want string // "" for no quote
	}{
		{fetched(350, -1001, 349, "re 349"), ""},              // 349 is a bot update, not channel A's
		{fetched(351, -1001, 348, "re 348"), ""},              // 348 is another channel's
		{fetched(352, -1001, 347, "re 347"), "channel A 347"}, // same channel
	} {
		r := ingest(t, s, bot, c.msg)
		v, err := s.GetMessageView(ctx, r.MessageID)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if v.Reply != nil {
			got = v.Reply.Text
		}
		if got != c.want {
			t.Fatalf("%q quotes %q, want %q", c.msg.Text, got, c.want)
		}
	}

	seedWatch(t, s)
	first := channelPost(10, "")
	first.Text = "first post"
	p, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: first, Now: 100})
	if err != nil {
		t.Fatal(err)
	}
	// A comment with the same message id lives in the discussion group's id space.
	if _, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: comment(11, "a comment", "mt:photo:c11", `{}`),
		Now: 101, ThreadRootID: p.MessageID}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id, replyTo int64
		want        string
	}{{12, 10, "first post"}, {13, 11, ""}} {
		post := channelPost(c.id, "")
		post.ReplyToTgMessageID = c.replyTo
		r, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: post, Now: 102})
		if err != nil {
			t.Fatal(err)
		}
		v, err := s.GetMessageView(ctx, r.MessageID)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if v.Reply != nil {
			got = v.Reply.Text
		}
		if got != c.want {
			t.Fatalf("post %d quotes %q, want %q", c.id, got, c.want)
		}
	}
}
