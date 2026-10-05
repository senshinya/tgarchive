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
