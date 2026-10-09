package store

// Queries rewritten for speed return what the queries they replaced did.

import (
	"fmt"
	"testing"

	"tgarchive/internal/model"
)

// The chat list's preview is the message the old single query picked with ORDER BY CASE: the
// latest live post of a channel (by Telegram id), the latest live arrival of a bot chat, never a
// comment, nothing for a chat with no live message.
func TestListChatsPreviewMatchesOrderByCase(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	seedWatch(t, s)
	bob := model.Sender{TgUserID: 43, FirstName: "Bob"}
	carol := model.Sender{TgUserID: 44, FirstName: "Carol"}
	for i, tg := range []int64{5, 9, 3} { // Alice's chat: the last to arrive is not the highest Telegram id
		if _, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: alice, Msg: textMsg(tg, fmt.Sprint("alice ", tg)), Now: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	gone, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: alice, Msg: textMsg(11, "deleted"), Now: 9})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DeleteMessage(ctx, gone.MessageID, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: bob, Msg: photoMsg(1, "k:bob"), Now: 1}); err != nil {
		t.Fatal(err)
	}
	only, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: carol, Msg: textMsg(1, "only"), Now: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DeleteMessage(ctx, only.MessageID, 2); err != nil {
		t.Fatal(err)
	}
	var root int64
	for i, tg := range []int64{20, 40, 30} { // archived out of order: 40 is the latest post
		m := channelPost(tg, "", fmt.Sprint("p", tg))
		m.Text = fmt.Sprint("post ", tg)
		res, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: m, Now: int64(10 + i)})
		if err != nil {
			t.Fatal(err)
		}
		root = res.MessageID
	}
	if _, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: comment(9000, "a comment", "c1", `{}`), ThreadRootID: root, Now: 20}); err != nil {
		t.Fatal(err)
	}

	rows, err := s.db.Query(`SELECT c.id,
		COALESCE((SELECT m.kind FROM messages m WHERE m.chat_id = c.id AND m.deleted_at = 0 AND m.thread_root_id = 0
			ORDER BY CASE WHEN c.kind = 'channel' THEN m.tg_message_id ELSE m.id END DESC, m.id DESC LIMIT 1), ''),
		COALESCE((SELECT substr(m.text, 1, 200) FROM messages m WHERE m.chat_id = c.id AND m.deleted_at = 0 AND m.thread_root_id = 0
			ORDER BY CASE WHEN c.kind = 'channel' THEN m.tg_message_id ELSE m.id END DESC, m.id DESC LIMIT 1), '')
		FROM chats c`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64][2]string{}
	for rows.Next() {
		var id int64
		var kind, text string
		if err := rows.Scan(&id, &kind, &text); err != nil {
			t.Fatal(err)
		}
		want[id] = [2]string{kind, text}
	}
	rows.Close()
	chats, err := s.ListChats(ctx, 0)
	if err != nil || len(chats) != 4 || len(want) != 4 {
		t.Fatalf("chats = %+v, %v; want %v", chats, err, want)
	}
	texts := map[string]bool{}
	for _, c := range chats {
		if got := [2]string{c.LastKind, c.LastText}; got != want[c.ID] {
			t.Errorf("chat %d (%s): preview %q, old query %q", c.ID, c.Kind, got, want[c.ID])
		}
		texts[c.LastText] = true
	}
	for _, w := range []string{"alice 3", "caption", "", "post 40"} {
		if !texts[w] {
			t.Errorf("no chat previews %q: %v", w, texts)
		}
	}
}
