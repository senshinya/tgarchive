package store

// Queries rewritten for speed return what the queries they replaced did.

import (
	"database/sql"
	"fmt"
	"reflect"
	"slices"
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

// collectOrphans given every media row finds what the old whole-table sweep found, across
// several batches, whatever order and repeats the candidates come in; given some, only those.
func TestCollectOrphansMatchesFullSweep(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	msg := ingest(t, s, bot, textMsg(1, "holder")).MessageID
	var all []int64
	for i := range 1200 {
		var id int64
		kind := []string{"photo", "video", "document"}[i%3]
		if err := s.db.QueryRow(`INSERT INTO media (dedupe_key, kind, path) VALUES (?, ?, ?) RETURNING id`,
			fmt.Sprint("k", i), kind, fmt.Sprintf("p/%d", i)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		switch i % 5 {
		case 0:
			s.db.Exec("INSERT INTO message_media (message_id, media_id, role, position) VALUES (?, ?, 'main', ?)", msg, id, i)
		case 1:
			s.db.Exec("INSERT INTO custom_emoji (document_id, media_id) VALUES (?, ?)", i, id)
		case 2:
			s.db.Exec("UPDATE media SET path = '' WHERE id = ?", id)
		}
		all = append(all, id)
	}
	var want []string
	rows, err := s.db.Query(`SELECT kind, path FROM media WHERE NOT EXISTS (SELECT 1 FROM message_media mm WHERE mm.media_id = media.id)
		AND NOT EXISTS (SELECT 1 FROM custom_emoji ce WHERE ce.media_id = media.id)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind, p string
		rows.Scan(&kind, &p)
		if p == "" {
			continue
		}
		want = append(want, p)
		if kind == "video" {
			want = append(want, CompatRel(p), CompatPartRel(p))
		}
	}
	rows.Close()

	collect := func(cand []int64) []string {
		var got []string
		err := s.withTx(ctx, func(tx *sql.Tx) error {
			var err error
			got, err = collectOrphans(ctx, tx, cand)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := collect(all[:4]); !reflect.DeepEqual(got, []string{"p/3"}) {
		t.Fatalf("some = %v", got)
	}
	want = want[1:] // p/3 is gone
	shuffled := append(slices.Clone(all), all[:100]...)
	slices.Reverse(shuffled)
	if got := collect(shuffled); !reflect.DeepEqual(got, want) {
		t.Fatalf("all: %d paths, full sweep %d", len(got), len(want))
	}
	var left int
	s.db.QueryRow("SELECT COUNT(*) FROM media").Scan(&left)
	if left != 1200/5*2 {
		t.Fatalf("%d media left", left)
	}
}
