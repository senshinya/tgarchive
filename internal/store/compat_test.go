package store

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"tgarchive/internal/model"
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
		if _, err := s.MarkMediaDone(ctx, id, mediaKey(t, s, id), "1/f", 4); err != nil {
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
	if err := s.PrepareCompatJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if j, err := s.NextCompatJob(ctx); err != nil || j.ID != older {
		t.Fatalf("a failed conversion is retried after a reset, got %+v %v", j, err)
	}
	if ok, err := s.SetCompat(ctx, 9999, CompatNone, "h264", "", ""); ok || err != nil {
		t.Fatalf("unknown media = %v %v", ok, err)
	}
}

func TestOrphanedVideoTakesItsCopyAlong(t *testing.T) {
	s := newStore(t)
	bot, _ := s.UpsertBot(ctx, &Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	m := photoMsg(1, "bot:v")
	m.Kind, m.Media[0].Kind, m.Media[0].Mime = "video", "video", "video/mp4"
	msgID := ingest(t, s, bot, m).MessageID
	mid := mediaIDs(t, s, msgID)[0]
	if _, err := s.MarkMediaDone(ctx, mid, mediaKey(t, s, mid), "1/2026/10/v.mov", 4); err != nil {
		t.Fatal(err)
	}
	_, orphans, err := s.DeleteMessage(ctx, msgID, 9000)
	if err != nil {
		t.Fatal(err)
	}
	// Derived from the original's path, so a copy a crash left unrecorded goes too.
	want := []string{"1/2026/10/v.mov", "1/2026/10/v.compat.mp4", "1/2026/10/v.compat.mp4.part"}
	if !reflect.DeepEqual(orphans, want) {
		t.Fatalf("orphans = %v, want %v", orphans, want)
	}
}

func TestCompatTimingRecheckOnce(t *testing.T) {
	s := newStore(t)
	bot, _ := s.UpsertBot(ctx, &Bot{TgBotID: 778, TokenEnc: []byte("x"), CreatedAt: 1})
	add := func(n int64, state, codec, path string) int64 {
		m := photoMsg(n, fmt.Sprintf("bot:timing:%d", n))
		m.Kind, m.Media[0].Kind, m.Media[0].Mime = "video", "video", "video/mp4"
		msg := ingest(t, s, bot, m).MessageID
		id := mediaIDs(t, s, msg)[0]
		if _, err := s.MarkMediaDone(ctx, id, mediaKey(t, s, id), fmt.Sprintf("1/%d.mp4", n), 4); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetCompat(ctx, id, state, codec, path, ""); err != nil {
			t.Fatal(err)
		}
		return id
	}
	h264 := add(1, CompatNone, "h264", "")
	hevc := add(2, CompatNone, "hevc", "")
	ready := add(3, CompatDone, "h264+aac@mp4", "1/3.compat.mp4")
	failed := add(4, CompatFailed, "mpeg4", "")
	article := add(5, CompatNone, "h264", "")
	if _, err := s.db.ExecContext(ctx, `UPDATE message_media SET role='article' WHERE media_id=?`, article); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_timing_marker BEFORE INSERT ON settings WHEN NEW.key='compat_timing_v1' BEGIN SELECT RAISE(FAIL,'forced marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareCompatJobs(ctx); err == nil {
		t.Fatal("expected preparation failure")
	}
	if m, err := s.GetMedia(ctx, failed); err != nil || m.CompatState != CompatFailed {
		t.Fatalf("failure reset committed without marker: %+v %v", m, err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_timing_marker`); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareCompatJobs(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{h264: CompatUnchecked, hevc: CompatNone, ready: CompatDone, failed: CompatUnchecked, article: CompatNone} {
		m, err := s.GetMedia(ctx, id)
		if err != nil || m.CompatState != want {
			t.Fatalf("media %d: state=%q, err=%v, want %q", id, m.CompatState, err, want)
		}
	}
	if _, err := s.SetCompat(ctx, h264, CompatNone, "h264", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareCompatJobs(ctx); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetMedia(ctx, h264)
	if err != nil || m.CompatState != CompatNone {
		t.Fatalf("rechecked media reset on next startup: %+v %v", m, err)
	}
}

func TestCompatIncludesVideoDocumentsWithoutChangingScope(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	var want []int64
	for i, c := range []struct {
		kind, mime, role string
		comment, want    bool
	}{
		{"document", "video/mp4", "main", false, true},
		{"document", "application/pdf", "main", false, false},
		{"document", "video/webm", "article", false, false},
		{"video", "video/mp4", "article", false, false},
		{"document", "video/mp4", "main", true, true},
		{"video", "video/mp4", "main", true, true},
		{"animation", "video/mp4", "main", false, true},
		{"video_note", "video/mp4", "main", false, true},
	} {
		m := photoMsg(int64(i+1), fmt.Sprintf("bot:compat-scope:%d", i))
		m.Kind = model.Kind(c.kind)
		m.Media[0].Kind = c.kind
		m.Media[0].Mime = c.mime
		r := ingest(t, s, bot, m)
		id := mediaIDs(t, s, r.MessageID)[0]
		if c.role != "main" {
			if _, err := s.db.Exec("UPDATE message_media SET role=? WHERE media_id=?", c.role, id); err != nil {
				t.Fatal(err)
			}
		}
		if c.comment {
			if _, err := s.db.Exec("UPDATE messages SET thread_root_id=999 WHERE id=?", r.MessageID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.MarkMediaDone(ctx, id, mediaKey(t, s, id), fmt.Sprintf("1/%d.mp4", id), 100); err != nil {
			t.Fatal(err)
		}
		if c.want {
			want = append([]int64{id}, want...)
		}
	}
	var got []int64
	for {
		j, err := s.NextCompatJob(ctx)
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, j.ID)
		if _, err := s.SetCompat(ctx, j.ID, CompatNone, "h264", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compat jobs=%v want %v", got, want)
	}
}

func TestCompatTimingRechecksVideoDocumentsOnly(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	for i, c := range []struct{ mime, role, want string }{
		{"video/mp4", "main", CompatUnchecked},
		{"application/pdf", "main", CompatNone},
		{"video/mp4", "article", CompatNone},
	} {
		m := photoMsg(int64(i+1), fmt.Sprintf("bot:document-timing:%d", i))
		m.Kind = model.KindDocument
		m.Media[0].Kind = "document"
		m.Media[0].Mime = c.mime
		r := ingest(t, s, bot, m)
		id := mediaIDs(t, s, r.MessageID)[0]
		s.MarkMediaDone(ctx, id, mediaKey(t, s, id), fmt.Sprintf("1/%d.mp4", id), 100)
		s.SetCompat(ctx, id, CompatNone, "h264", "", "")
		if c.role != "main" {
			if _, err := s.db.Exec("UPDATE message_media SET role=? WHERE media_id=?", c.role, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.PrepareCompatJobs(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{1: CompatUnchecked, 2: CompatNone, 3: CompatNone} {
		md, err := s.GetMedia(ctx, id)
		if err != nil || md.CompatState != want {
			t.Fatalf("media%d=%+v %v want %s", id, md, err, want)
		}
	}
}

func TestOrphanedVideoDocumentTakesItsCopyAlong(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	m := photoMsg(1, "bot:video-document")
	m.Kind = model.KindDocument
	m.Media[0].Kind = "document"
	m.Media[0].Mime = "video/mp4"
	r := ingest(t, s, bot, m)
	id := mediaIDs(t, s, r.MessageID)[0]
	s.MarkMediaDone(ctx, id, mediaKey(t, s, id), "1/document.mp4", 100)
	_, paths, err := s.DeleteMessage(ctx, r.MessageID, 1000)
	want := []string{"1/document.mp4", "1/document.compat.mp4", "1/document.compat.mp4.part"}
	if err != nil || !reflect.DeepEqual(paths, want) {
		t.Fatalf("orphan paths=%v %v want %v", paths, err, want)
	}
}
