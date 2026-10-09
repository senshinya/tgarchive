package userbot

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func (e *watchEnv) at(m *tg.Message, ago time.Duration) {
	m.Date = int(e.now.Add(-ago).Unix())
}

func TestBackfillArchivesMatchingPostsInRange(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.at(e.tg.reacted(1, 0, false, 10, map[string]int{"🔥": 50}), 30*time.Hour) // before the range
	e.at(e.tg.reacted(2, 0, false, 10, map[string]int{"🔥": 50}), 20*time.Hour)
	e.at(e.tg.reacted(3, 0, false, 10, map[string]int{"🔥": 1}), 10*time.Hour)
	e.at(e.tg.reacted(4, 7, true, 10, map[string]int{"🔥": 12}), 5*time.Hour)
	e.at(e.tg.reacted(5, 7, true, 10, nil), 5*time.Hour)
	for id := 6; id < 6+150; id++ { // more than a page of recent posts below the bar
		e.at(e.tg.reacted(id, 0, false, 1, nil), time.Hour)
	}
	st, err := e.w.Backfill(ctx, e.watch, 24)
	if err != nil || !st.Running || st.Hours != 24 {
		t.Fatalf("Backfill = %+v, %v", st, err)
	}
	if _, err := e.w.Backfill(ctx, e.watch, 24); !errors.Is(err, errBackfillBusy) && e.w.BackfillState(e.watch).Running {
		t.Fatalf("second backfill while running = %v", err)
	}
	eventually(t, "backfill done", func() bool { return !e.w.BackfillState(e.watch).Running })
	b := e.w.BackfillState(e.watch)
	if b.Error != "" || b.Scanned != 154 || b.Archived != 2 || b.FinishedAt == 0 {
		t.Fatalf("state = %+v", b)
	}
	if got := e.archived(t); !equalIDs(tgIDs(got), []int64{2, 4, 5}) {
		t.Fatalf("archived = %v", tgIDs(got))
	}
	if w := e.get(t); w.Hits != 2 || w.ScanHits != 0 {
		t.Fatalf("hits = %d, scan hits = %d (backfill hits are not polled ones)", w.Hits, w.ScanHits)
	}
	// Running it again archives nothing new.
	if _, err := e.w.Backfill(ctx, e.watch, 24); err != nil {
		t.Fatal(err)
	}
	eventually(t, "second backfill done", func() bool { return !e.w.BackfillState(e.watch).Running })
	if b := e.w.BackfillState(e.watch); b.Archived != 0 || len(e.archived(t)) != 3 {
		t.Fatalf("second = %+v", b)
	}
}

func TestBackfillNeedsTheAccount(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.api.ready = false
	if _, err := e.w.Backfill(ctx, e.watch, 1); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Backfill = %v", err)
	}
	if e.w.BackfillState(e.watch) != nil {
		t.Fatal("no state expected")
	}
}

func TestBackfillReportsChannelErrors(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.chanErr = "CHANNEL_PRIVATE"
	if _, err := e.w.Backfill(ctx, e.watch, 1); err != nil {
		t.Fatal(err)
	}
	eventually(t, "backfill done", func() bool { return !e.w.BackfillState(e.watch).Running })
	if b := e.w.BackfillState(e.watch); b.Error == "" {
		t.Fatalf("state = %+v", b)
	}
}

// A backfill passing over posts polling already archived refreshes their counters but keeps the
// original hit: its time is when the post was first archived, and it schedules the later refreshes.
func TestBackfillKeepsTheHitOfArchivedPosts(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx) // init at 5
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20})
	e.w.PollOnce(ctx)
	hitAt := e.now.Unix()
	e.tg.reacted(6, 0, false, 300, map[string]int{"🔥": 40})
	e.now = e.now.Add(3 * time.Hour)
	if _, err := e.w.Backfill(ctx, e.watch, 24); err != nil {
		t.Fatal(err)
	}
	eventually(t, "backfill done", func() bool { return !e.w.BackfillState(e.watch).Running })
	if b := e.w.BackfillState(e.watch); b.Error != "" || b.Archived != 0 {
		t.Fatalf("state = %+v", b)
	}
	got := e.archived(t)
	if len(got) != 1 {
		t.Fatalf("archived = %v", tgIDs(got))
	}
	var ps PostStats
	if err := json.Unmarshal(got[0].Stats, &ps); err != nil {
		t.Fatal(err)
	}
	if ps.Hit == nil || ps.Hit.At != hitAt || len(ps.Hit.Reasons) != 1 || ps.Hit.Reasons[0] != "🔥 20 ≥ 10" {
		t.Fatalf("hit = %+v, want the polled one at %d", ps.Hit, hitAt)
	}
	if ps.Views != 300 || ps.Total != 40 || ps.RefreshedAt != e.now.Unix() {
		t.Fatalf("counters = %+v, want the backfill's", ps)
	}
}

// A backfill is the user asking for posts again: one they deleted from the archive comes back.
// Polling taking the same post in again (here after the watch was re-enabled) leaves it deleted.
func TestBackfillRevivesDeletedPostsPollingDoesNot(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx) // init at 5
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20})
	e.w.PollOnce(ctx)
	got := e.archived(t)
	if len(got) != 1 {
		t.Fatalf("archived = %v", tgIDs(got))
	}
	id := got[0].ID
	if _, _, err := e.st.DeleteMessage(ctx, id, e.now.Unix()); err != nil {
		t.Fatal(err)
	}

	e.st.UpdateWatch(ctx, e.watch, 30, fire10, false, 2)
	e.st.UpdateWatch(ctx, e.watch, 30, fire10, true, 3)
	e.w.PollOnce(ctx)
	if got := e.archived(t); len(got) != 0 {
		t.Fatalf("polling revived %v", tgIDs(got))
	}

	if _, err := e.w.Backfill(ctx, e.watch, 24); err != nil {
		t.Fatal(err)
	}
	eventually(t, "backfill done", func() bool { return !e.w.BackfillState(e.watch).Running })
	if b := e.w.BackfillState(e.watch); b.Error != "" || b.Archived != 1 {
		t.Fatalf("state = %+v", b)
	}
	if got := e.archived(t); len(got) != 1 || got[0].ID != id {
		t.Fatalf("archived after backfill = %+v", got)
	}
}
