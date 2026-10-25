package store

import (
	"errors"
	"fmt"
	"testing"

	"tgarchive/internal/model"
)

func coverVideo(t *testing.T, s *Store, bot, id int64, key string) (*IngestResult, int64) {
	t.Helper()
	m := kindMsg(id, model.KindVideo, "video", key)
	m.Media[0].Mime = "video/mp4"
	m.Media[0].Duration = 100
	r := ingest(t, s, bot, m)
	md := mediaIDs(t, s, r.MessageID)[0]
	if ok, err := s.MarkMediaDone(ctx, md, key, fmt.Sprintf("1/%d.mp4", md), 100); !ok || err != nil {
		t.Fatal(ok, err)
	}
	return r, md
}
func coverResult(job VideoCoverJob) VideoCover {
	return VideoCover{Path: VideoCoverRel(job.DedupeKey), Size: 500, Width: 640, Height: 360}
}

func TestVideoCoverCandidatesAndCursor(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	first, a := coverVideo(t, s, bot, 1, "bot:a")
	_, b := coverVideo(t, s, bot, 2, "bot:b")
	pending := ingest(t, s, bot, kindMsg(3, model.KindVideo, "video", "bot:pending"))
	_ = pending
	comment, c := coverVideo(t, s, bot, 4, "bot:comment")
	s.db.Exec("UPDATE messages SET thread_root_id=? WHERE id=?", first.MessageID, comment.MessageID)
	article, d := coverVideo(t, s, bot, 5, "bot:article")
	s.db.Exec("UPDATE message_media SET role='article' WHERE message_id=?", article.MessageID)
	for i, kind := range []string{"animation", "video_note"} {
		r, md := coverVideo(t, s, bot, 10+int64(i), kind)
		s.db.Exec("UPDATE media SET kind=? WHERE id=?", kind, md)
		_ = r
	}
	withThumb, e := coverVideo(t, s, bot, 20, "bot:thumb")
	thumb := photoMsg(21, "bot:native-thumb")
	tr := ingest(t, s, bot, thumb)
	tm := mediaIDs(t, s, tr.MessageID)[0]
	s.MarkMediaDone(ctx, tm, "bot:native-thumb", "1/native.jpg", 100)
	s.db.Exec("INSERT INTO message_media(message_id,media_id,role) VALUES(?,?,'thumb')", withThumb.MessageID, tm)
	deleted, f := coverVideo(t, s, bot, 30, "bot:deleted")
	s.DeleteMessage(ctx, deleted.MessageID, 100)
	doc := kindMsg(40, model.KindDocument, "document", "bot:document")
	doc.Media[0].Mime = "video/webm"
	dr := ingest(t, s, bot, doc)
	docID := mediaIDs(t, s, dr.MessageID)[0]
	s.MarkMediaDone(ctx, docID, "bot:document", "1/doc.webm", 100)
	var got []int64
	after := int64(0)
	for {
		job, err := s.NextVideoCoverJob(ctx, after)
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if job.DedupeKey == "" || job.Path == "" {
			t.Fatal(job)
		}
		got = append(got, job.ID)
		after = job.ID
	}
	if !eq(got, []int64{a, b, docID}) {
		t.Fatalf("candidates=%v excluded comment=%d article=%d thumb=%d deleted=%d", got, c, d, e, f)
	}
}

func TestInstallVideoCoverSharedReferencesAndNativeThumbWins(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	first, id := coverVideo(t, s, bot, 1, "bot:shared")
	second, _ := coverVideo(t, s, bot, 2, "bot:shared")
	job, err := s.NextVideoCoverJob(ctx, 0)
	if err != nil || job.ID != id {
		t.Fatal(job, err)
	}
	thumb := photoMsg(3, "bot:arrived-thumb")
	r := ingest(t, s, bot, thumb)
	native := mediaIDs(t, s, r.MessageID)[0]
	s.MarkMediaDone(ctx, native, "bot:arrived-thumb", "1/native.jpg", 300)
	s.db.Exec("INSERT INTO message_media(message_id,media_id,role) VALUES(?,?,'thumb')", first.MessageID, native)
	cover, ok, err := s.InstallVideoCover(ctx, *job, coverResult(*job))
	if err != nil || !ok || cover == 0 {
		t.Fatalf("install=%d %v %v", cover, ok, err)
	}
	for _, c := range []struct{ message, thumb int64 }{{first.MessageID, native}, {second.MessageID, cover}} {
		var got int64
		var count int
		s.db.QueryRow("SELECT COUNT(*),MAX(media_id) FROM message_media WHERE message_id=? AND role='thumb'", c.message).Scan(&count, &got)
		if count != 1 || got != c.thumb {
			t.Fatalf("message=%d thumbs=%d/%d", c.message, count, got)
		}
	}
	md, err := s.GetMedia(ctx, cover)
	if err != nil || md.Kind != "photo" || md.Mime != "image/jpeg" || md.State != StateDone || md.Path != VideoCoverRel(job.DedupeKey) {
		t.Fatal(md, err)
	}
	// A newly archived reference reuses the generated row and deterministic path.
	third, _ := coverVideo(t, s, bot, 4, "bot:shared")
	next, err := s.NextVideoCoverJob(ctx, 0)
	if err != nil || next.CoverPath != md.Path {
		t.Fatal(next, err)
	}
	reused, ok, err := s.InstallVideoCover(ctx, *next, coverResult(*next))
	if err != nil || !ok || reused != cover {
		t.Fatalf("reuse=%d %v %v", reused, ok, err)
	}
	v, _ := s.GetMessageView(ctx, third.MessageID)
	found := false
	for _, m := range v.Media {
		if m.ID == cover && m.Role == "thumb" {
			found = true
		}
	}
	if !found {
		t.Fatal(v)
	}
	if _, err := s.NextVideoCoverJob(ctx, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestInstallVideoCoverRejectsChangedIdentity(t *testing.T) {
	for _, change := range []string{"reused_id", "changed_path", "deleted", "comment", "native_thumb"} {
		t.Run(change, func(t *testing.T) {
			s := newStore(t)
			bot := seedBot(t, s, 777)
			r, id := coverVideo(t, s, bot, 1, "bot:old")
			job, err := s.NextVideoCoverJob(ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "reused_id":
				s.DeleteMessage(ctx, r.MessageID, 100)
				_, newID := coverVideo(t, s, bot, 2, "bot:replacement")
				if id != newID {
					t.Fatalf("fixture did not reuse media ID %d/%d", id, newID)
				}
			case "changed_path":
				s.MarkMediaDone(ctx, id, "bot:old", "other.mp4", 100)
			case "deleted":
				s.DeleteMessage(ctx, r.MessageID, 100)
			case "comment":
				s.db.Exec("UPDATE messages SET thread_root_id=999 WHERE id=?", r.MessageID)
			case "native_thumb":
				thumb := photoMsg(2, "bot:thumb")
				tr := ingest(t, s, bot, thumb)
				md := mediaIDs(t, s, tr.MessageID)[0]
				s.MarkMediaDone(ctx, md, "bot:thumb", "t.jpg", 100)
				s.db.Exec("INSERT INTO message_media(message_id,media_id,role) VALUES(?,?,'thumb')", r.MessageID, md)
			}
			cover, ok, err := s.InstallVideoCover(ctx, *job, coverResult(*job))
			if err != nil || ok || cover != 0 {
				t.Fatalf("install stale=%d %v %v", cover, ok, err)
			}
			var n int
			s.db.QueryRow("SELECT COUNT(*) FROM media WHERE dedupe_key=?", "cover:"+job.DedupeKey).Scan(&n)
			if n != 0 {
				t.Fatalf("orphan cover row retained: %d", n)
			}
		})
	}
}

func TestGeneratedVideoCoverLeavesNativeThumbnailPriority(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	r, _ := coverVideo(t, s, bot, 1, "bot:video")
	native := ingest(t, s, bot, photoMsg(2, "bot:pending-thumb"))
	nativeID := mediaIDs(t, s, native.MessageID)[0]
	if _, err := s.db.Exec("INSERT INTO message_media(message_id,media_id,role,position) VALUES(?,?,'thumb',1)", r.MessageID, nativeID); err != nil {
		t.Fatal(err)
	}
	job, err := s.NextVideoCoverJob(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	generated, ok, err := s.InstallVideoCover(ctx, *job, coverResult(*job))
	if err != nil || !ok {
		t.Fatal(generated, ok, err)
	}
	s.MarkMediaDone(ctx, nativeID, "bot:pending-thumb", "native.jpg", 100)
	view, err := s.GetMessageView(ctx, r.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	for _, md := range view.Media {
		if md.Role == "thumb" && md.State == StateDone {
			if md.ID != nativeID {
				t.Fatalf("generated cover %d took priority over native thumb %d", md.ID, nativeID)
			}
			return
		}
	}
	t.Fatal("no ready thumbnail")
}
