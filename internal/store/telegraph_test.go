package store

import (
	"errors"
	"testing"
)

func ingestLink(t *testing.T, s *Store, bot, tgID int64, path string) *IngestResult {
	t.Helper()
	res, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: alice, Msg: textMsg(tgID, "https://telegra.ph/"+path), Now: 5000, TelegraphPath: path})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestIngestQueuesTelegraphJob(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res := ingestLink(t, s, bot, 1, "Sample-10-05")
	if !res.Created || !res.TelegraphQueued {
		t.Fatalf("res = %+v", res)
	}
	j, err := s.GetTelegraphJob(ctx, res.MessageID)
	if err != nil || j.Path != "Sample-10-05" || j.State != TelegraphQueued || j.ChatID != res.ChatID || j.CreatedAt != 5000 {
		t.Fatalf("job = %+v, %v", j, err)
	}
	// Re-delivery / edit of the same message: no second job.
	again := ingestLink(t, s, bot, 1, "Sample-10-05")
	if again.Created || again.TelegraphQueued {
		t.Fatalf("edit res = %+v", again)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM telegraph_jobs").Scan(&n)
	if n != 1 {
		t.Fatalf("jobs = %d", n)
	}
	plain := ingest(t, s, bot, textMsg(2, "hello"))
	if plain.TelegraphQueued {
		t.Fatal("a message without TelegraphPath must not queue")
	}
	if _, err := s.GetTelegraphJob(ctx, plain.MessageID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("job for plain message: %v", err)
	}
}

func TestTelegraphJobQueue(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	a := ingestLink(t, s, bot, 1, "A")
	b := ingestLink(t, s, bot, 2, "B")

	j, err := s.ClaimNextTelegraphJob(ctx, 6000)
	if err != nil || j.MessageID != a.MessageID || j.State != TelegraphFetching || j.ChatID != a.ChatID {
		t.Fatalf("claim = %+v, %v", j, err)
	}
	if err := s.SetTelegraphJobAttempts(ctx, j.ID, 2, "网络错误", 6001); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetTelegraphJob(ctx, a.MessageID); got.Attempts != 2 || got.Error != "网络错误" || got.State != TelegraphFetching || got.UpdatedAt != 6001 {
		t.Fatalf("after attempts = %+v", got)
	}
	// Restart: the interrupted job goes back to the queue and is claimed before B.
	if n, err := s.RequeueFetchingTelegraphJobs(ctx, 7000); err != nil || n != 1 {
		t.Fatalf("requeue = %d, %v", n, err)
	}
	j, _ = s.ClaimNextTelegraphJob(ctx, 7001)
	if j.MessageID != a.MessageID || j.Attempts != 2 {
		t.Fatalf("reclaim = %+v", j)
	}
	if err := s.FinishTelegraphJob(ctx, j.ID, TelegraphFailed, "文章不存在", 7002); err != nil {
		t.Fatal(err)
	}
	j2, _ := s.ClaimNextTelegraphJob(ctx, 7003)
	if j2.MessageID != b.MessageID {
		t.Fatalf("second claim = %+v", j2)
	}
	if _, err := s.ClaimNextTelegraphJob(ctx, 7004); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty queue: %v", err)
	}
	if got, _ := s.GetTelegraphJob(ctx, a.MessageID); got.State != TelegraphFailed || got.Error != "文章不存在" {
		t.Fatalf("finished = %+v", got)
	}
}

func TestDeleteMessageDropsTelegraphJob(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res := ingestLink(t, s, bot, 1, "A")
	j, _ := s.ClaimNextTelegraphJob(ctx, 6000)
	if _, _, err := s.DeleteMessage(ctx, res.MessageID, 6001); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTelegraphJob(ctx, res.MessageID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("job after delete: %v", err)
	}
	if err := s.FinishTelegraphJob(ctx, j.ID, TelegraphFetched, "", 6002); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finish after delete = %v, want ErrNotFound", err)
	}
	if n, _ := s.RequeueFetchingTelegraphJobs(ctx, 6003); n != 0 {
		t.Fatalf("requeued %d deleted jobs", n)
	}
}

func TestPurgeBotCascadesTelegraph(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	ingestLink(t, s, bot, 1, "A")
	if _, err := s.PurgeBot(ctx, bot); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM telegraph_jobs").Scan(&n)
	if n != 0 {
		t.Fatalf("jobs after purge = %d", n)
	}
}
