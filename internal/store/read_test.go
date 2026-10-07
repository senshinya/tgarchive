package store

import (
	"errors"
	"testing"
)

func unreadOf(t *testing.T, s *Store, chatID int64) (unread, lastRead int64) {
	t.Helper()
	chats, err := s.ListChats(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats {
		if c.ID == chatID {
			return c.Unread, c.LastReadID
		}
	}
	t.Fatalf("chat %d not listed", chatID)
	return 0, 0
}

func TestChannelUnread(t *testing.T) {
	s := newStore(t)
	seedWatch(t, s)
	var ids []int64
	var chat int64
	for i := int64(1); i <= 3; i++ {
		res, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(i, ""), Now: i})
		if err != nil {
			t.Fatal(err)
		}
		chat = res.ChatID
		ids = append(ids, res.MessageID)
	}
	if n, _ := unreadOf(t, s, chat); n != 3 {
		t.Fatalf("unread = %d", n)
	}
	if err := s.MarkRead(ctx, chat, ids[1]); err != nil {
		t.Fatal(err)
	}
	if n, last := unreadOf(t, s, chat); n != 1 || last != ids[1] {
		t.Fatalf("after reading 2: %d, %d", n, last)
	}
	if err := s.MarkRead(ctx, chat, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, last := unreadOf(t, s, chat); last != ids[1] {
		t.Fatalf("reading an older message must not go back: %d", last)
	}
	if _, _, err := s.DeleteMessage(ctx, ids[2], 9000); err != nil {
		t.Fatal(err)
	}
	if n, _ := unreadOf(t, s, chat); n != 0 {
		t.Fatalf("deleted messages are not unread: %d", n)
	}
	if err := s.MarkRead(ctx, 9999, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown chat: %v", err)
	}

	bot := seedBot(t, s, 777)
	private := ingest(t, s, bot, textMsg(1, "hi")).ChatID
	if n, _ := unreadOf(t, s, private); n != 0 {
		t.Fatalf("private chats have no unread count: %d", n)
	}
}
