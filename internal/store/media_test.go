package store

import (
	"reflect"
	"testing"
)

func TestDoneMediaPaths(t *testing.T) {
	s := newStore(t)
	bot, _ := s.UpsertBot(ctx, &Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	first := mediaIDs(t, s, ingest(t, s, bot, photoMsg(1, "bot:a", "bot:b")).MessageID)
	ids := mediaIDs(t, s, ingest(t, s, bot, photoMsg(2, "bot:c")).MessageID)
	if _, err := s.MarkMediaDone(ctx, first[0], "1/2026/10/a.mp4", 4); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkMediaDone(ctx, ids[0], "1/2026/10/c.jpg", 4); err != nil {
		t.Fatal(err)
	}
	got, err := s.DoneMediaPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"1/2026/10/a.mp4", "1/2026/10/c.jpg"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v (pending media must be skipped)", got, want)
	}
}

func TestDueMediaSplitsWebFromTelegram(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	ingest(t, s, bot, photoMsg(1, "web:a", "web:b", "web:c", "bot:a", "user:b"))
	keys := func(ms []Media, err error) []string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range ms {
			out = append(out, m.DedupeKey)
		}
		return out
	}
	if got := keys(s.DueTelegramMedia(ctx, 0, 10)); !reflect.DeepEqual(got, []string{"bot:a", "user:b"}) {
		t.Fatalf("telegram = %v", got)
	}
	if got := keys(s.DueWebMedia(ctx, 0, 2)); !reflect.DeepEqual(got, []string{"web:a", "web:b"}) {
		t.Fatalf("web = %v", got)
	}
}
