package store

import (
	"errors"
	"testing"
)

func TestCompatJobs(t *testing.T) {
	s := newStore(t)
	bot, _ := s.UpsertBot(ctx, &Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	msgOf := map[int64]int64{}
	vid := func(id int64, key string) int64 {
		m := photoMsg(id, key)
		m.Kind, m.Media[0].Kind, m.Media[0].Mime = "video", "video", "video/mp4"
		msgID := ingest(t, s, bot, m).MessageID
		mid := mediaIDs(t, s, msgID)[0]
		msgOf[mid] = msgID
		return mid
	}
	older, newer := vid(1, "bot:v1"), vid(2, "bot:v2")
	photo := mediaIDs(t, s, ingest(t, s, bot, photoMsg(3, "bot:p")).MessageID)[0]
	if _, err := s.NextCompatJob(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending videos must wait for their download, got %v", err)
	}
	for _, id := range []int64{older, newer, photo} {
		if _, err := s.MarkMediaDone(ctx, id, "1/f", 4); err != nil {
			t.Fatal(err)
		}
	}
	j, err := s.NextCompatJob(ctx)
	if err != nil || j.ID != newer {
		t.Fatalf("next = %+v %v, want the newest video %d", j, err, newer)
	}
	if ok, err := s.SetCompat(ctx, newer, CompatDone, "av1", "1/f.compat.mp4", ""); !ok || err != nil {
		t.Fatalf("set = %v %v", ok, err)
	}
	if ok, err := s.SetCompat(ctx, older, CompatFailed, "mpeg4", "", "boom"); !ok || err != nil {
		t.Fatalf("set = %v %v", ok, err)
	}
	if _, err := s.NextCompatJob(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("photos are never jobs, got %v", err)
	}
	m, _ := s.GetMedia(ctx, newer)
	if m.CompatState != CompatDone || m.CompatCodec != "av1" || m.CompatPath != "1/f.compat.mp4" {
		t.Fatalf("media = %+v", m)
	}
	for id, want := range map[int64]string{newer: "av1", older: ""} {
		v, err := s.GetMessageView(ctx, msgOf[id])
		if err != nil || v.Media[0].CompatCodec != want {
			t.Fatalf("view of media %d: compat_codec = %q %v, want %q (only a ready copy is announced)", id, v.Media[0].CompatCodec, err, want)
		}
	}
	if err := s.ResetFailedCompat(ctx); err != nil {
		t.Fatal(err)
	}
	if j, err := s.NextCompatJob(ctx); err != nil || j.ID != older {
		t.Fatalf("a failed conversion is retried after a reset, got %+v %v", j, err)
	}
	if ok, err := s.SetCompat(ctx, 9999, CompatNone, "h264", "", ""); ok || err != nil {
		t.Fatalf("unknown media = %v %v", ok, err)
	}
}
