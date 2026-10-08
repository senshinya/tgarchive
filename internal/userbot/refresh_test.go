package userbot

import (
	"encoding/json"
	"testing"
	"time"

	"tgarchive/internal/store"
)

func TestRefreshDue(t *testing.T) {
	const hit = 1_000_000
	m, h, d := int64(60), int64(3600), int64(86400)
	for _, c := range []struct {
		name           string
		refreshed, now int64
		want           bool
	}{
		{"first 10 minutes", 0, hit + 9*m, false},
		{"10 minutes after the hit", 0, hit + 10*m, true},
		{"10 minutes after the last refresh", hit + 50*m, hit + 60*m, true},
		{"too soon in the first 2 hours", hit + 100*m, hit + 109*m, false},
		{"30 minutes from 2 hours", hit + 2*h, hit + 2*h + 29*m, false},
		{"30 minutes from 2 hours, due", hit + 2*h, hit + 2*h + 30*m, true},
		{"2 hours from 6 hours", hit + 6*h, hit + 7*h, false},
		{"2 hours from 6 hours, due", hit + 6*h, hit + 8*h, true},
		{"quiet after a day", hit + 23*h, hit + 2*d, false},
		{"day 3", hit + 23*h, hit + 3*d, true},
		{"day 3 done", hit + 3*d, hit + 5*d, false},
		{"day 7", hit + 3*d, hit + 7*d, true},
		{"day 7 done", hit + 7*d, hit + 7*d + h, false},
		{"after the last checkpoint", hit + 7*d, hit + 30*d, false},
	} {
		if got := refreshDue(hit, c.refreshed, c.now); got != c.want {
			t.Errorf("%s: refreshDue = %v", c.name, got)
		}
	}
}

func (e *watchEnv) stats(t *testing.T, tgID int64) PostStats {
	t.Helper()
	for _, m := range e.archived(t) {
		if m.TgMessageID == tgID {
			var ps PostStats
			if err := json.Unmarshal(m.Stats, &ps); err != nil {
				t.Fatal(err)
			}
			return ps
		}
	}
	t.Fatalf("post %d not archived", tgID)
	return PostStats{}
}

// updates counts the message.updated events seen so far (events arrive asynchronously).
func (e *watchEnv) updates(want int) int {
	deadline := time.Now().Add(time.Second)
	for {
		e.evMu.Lock()
		n := 0
		for _, ev := range e.ev {
			if ev.Type == "message.updated" {
				n++
			}
		}
		e.evMu.Unlock()
		if n >= want || time.Now().After(deadline) {
			return n
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWatchRefreshesArchivedCounters(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx)
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20})
	e.w.PollOnce(ctx)
	hitAt := e.now.Unix()
	base := e.updates(0)

	e.tg.reacted(6, 0, false, 250, map[string]int{"🔥": 40, "👍": 5})
	e.now = e.now.Add(5 * time.Minute)
	e.w.PollOnce(ctx)
	if ps := e.stats(t, 6); ps.Total != 20 {
		t.Fatalf("refreshed too soon: %+v", ps)
	}

	e.now = e.now.Add(5 * time.Minute)
	e.w.PollOnce(ctx)
	ps := e.stats(t, 6)
	if ps.Total != 45 || ps.Views != 250 || len(ps.Reactions) != 2 || ps.Hit == nil || ps.Hit.At != hitAt ||
		ps.RefreshedAt != e.now.Unix() {
		t.Fatalf("after refresh = %+v", ps)
	}
	if n := e.updates(base + 1); n != base+1 {
		t.Fatalf("message.updated events = %d, want %d", n, base+1)
	}

	// Nothing changed on Telegram: the schedule moves on, without an update event.
	e.now = e.now.Add(10 * time.Minute)
	e.w.PollOnce(ctx)
	if ps := e.stats(t, 6); ps.RefreshedAt != e.now.Unix() || ps.Total != 45 {
		t.Fatalf("unchanged refresh = %+v", ps)
	}
	if n := e.updates(base + 2); n != base+1 {
		t.Fatalf("an unchanged refresh published an update (%d events)", n)
	}

	// A post deleted from the channel keeps what was archived.
	delete(e.tg.posts, 6)
	e.now = e.now.Add(10 * time.Minute)
	e.w.PollOnce(ctx)
	if ps := e.stats(t, 6); ps.Total != 45 || ps.Views != 250 {
		t.Fatalf("deleted post = %+v", ps)
	}
}

func TestWatchRefreshStopsAfterAWeek(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx)
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20})
	e.w.PollOnce(ctx)

	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 30})
	e.now = e.now.Add(7*24*time.Hour + time.Hour)
	e.w.PollOnce(ctx)
	if ps := e.stats(t, 6); ps.Total != 30 {
		t.Fatalf("day 7 refresh = %+v", ps)
	}
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 50})
	e.now = e.now.Add(3 * 24 * time.Hour)
	e.w.PollOnce(ctx)
	if ps := e.stats(t, 6); ps.Total != 30 {
		t.Fatalf("refreshed after the last checkpoint: %+v", ps)
	}
}

func TestWatchRefreshesAlbumsTogether(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx)
	e.tg.reacted(6, 9, true, 50, map[string]int{"🔥": 15})
	e.tg.reacted(7, 9, true, 50, nil)
	e.w.PollOnce(ctx)

	e.tg.reacted(7, 9, true, 80, map[string]int{"🔥": 25})
	e.now = e.now.Add(10 * time.Minute)
	e.w.PollOnce(ctx)
	a, b := e.stats(t, 6), e.stats(t, 7)
	if a.Total != 25 || b.Total != 25 || a.Views != 80 || b.Views != 80 {
		t.Fatalf("album = %+v / %+v", a, b)
	}
}

func TestRefreshPostsOnOpen(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx)
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20})
	e.w.PollOnce(ctx)
	msgs := e.archived(t)
	chat, id := msgs[0].ChatID, msgs[0].ID

	// Off the schedule, a minute after the hit.
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 33})
	e.now = e.now.Add(time.Minute)
	if err := e.w.RefreshPosts(ctx, chat, []int64{id}); err != nil {
		t.Fatal(err)
	}
	if ps := e.stats(t, 6); ps.Total != 33 {
		t.Fatalf("on open = %+v", ps)
	}
	// The same chat again within 5 minutes is skipped.
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 44})
	e.now = e.now.Add(4 * time.Minute)
	if err := e.w.RefreshPosts(ctx, chat, []int64{id}); err != nil {
		t.Fatal(err)
	}
	if ps := e.stats(t, 6); ps.Total != 33 {
		t.Fatalf("throttled = %+v", ps)
	}
	e.now = e.now.Add(time.Minute)
	if err := e.w.RefreshPosts(ctx, chat, []int64{id}); err != nil {
		t.Fatal(err)
	}
	if ps := e.stats(t, 6); ps.Total != 44 {
		t.Fatalf("after the throttle = %+v", ps)
	}

	if err := e.w.RefreshPosts(ctx, 99999, []int64{id}); err != store.ErrNotFound {
		t.Fatalf("unknown chat: %v", err)
	}
}
