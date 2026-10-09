package userbot

import (
	"strings"
	"testing"
	"time"

	"tgarchive/internal/store"
)

func (e *watchEnv) tgCalls() int {
	f := e.tg.fakeTG
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestWatchFloodWaitHoldsOffUntilItEnds(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx) // init at 5
	e.tg.chanErr, e.tg.chanCode = "FLOOD_WAIT_1800", 420
	floodAt, start := e.now, time.Now()
	e.w.PollOnce(ctx)
	if time.Since(start) > 5*time.Second {
		t.Fatal("a long flood wait blocked the round")
	}
	e.tg.chanErr, e.tg.chanCode = "", 0
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20})
	calls := e.tgCalls()
	for _, after := range []time.Duration{time.Minute, 10 * time.Minute, 29 * time.Minute} {
		e.now = floodAt.Add(after)
		e.w.PollOnce(ctx)
		if n := e.tgCalls(); n != calls {
			t.Fatalf("%s into FLOOD_WAIT_1800: %d Telegram calls", after, n-calls)
		}
	}
	if w := e.get(t); w.Status != store.WatchOK || w.LastSeenID != 5 {
		t.Fatalf("watch during the wait = %+v", w)
	}
	// Telegram answers the account again once the wait is over.
	e.now = floodAt.Add(31 * time.Minute)
	e.w.PollOnce(ctx)
	if got := e.archived(t); !equalIDs(tgIDs(got), []int64{6}) {
		t.Fatalf("archived after the wait = %v", tgIDs(got))
	}
}

func TestWatchFloodWaitHoldsOffPickerScan(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.manyDialogs = 10
	e.tg.chanErr, e.tg.chanCode = "FLOOD_WAIT_1800", 420
	e.w.PollOnce(ctx)
	e.tg.chanErr, e.tg.chanCode = "", 0
	l, err := e.w.Channels(ctx, true)
	if err != nil || l.Loading || !strings.Contains(l.Error, "被限流") {
		t.Fatalf("Channels during the wait = %+v, %v", l, err)
	}
	if n := e.dialogScans(); n != 0 {
		t.Fatalf("getDialogs calls during the wait = %d", n)
	}
	e.now = e.now.Add(31 * time.Minute)
	if l, _ := e.w.Channels(ctx, true); !l.Loading {
		t.Fatalf("Channels after the wait = %+v", l)
	}
	eventually(t, "scan done", func() bool { l, _ := e.w.Channels(ctx, false); return !l.Loading })
	if l, _ := e.w.Channels(ctx, false); len(l.Channels) != 5 || l.Error != "" {
		t.Fatalf("list after the wait = %+v", l)
	}
}

func TestWatchFloodWaitRefusesManualBackfill(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.chanErr, e.tg.chanCode = "FLOOD_WAIT_1800", 420
	e.w.PollOnce(ctx)
	e.tg.chanErr, e.tg.chanCode = "", 0
	calls := e.tgCalls()
	if _, err := e.w.Backfill(ctx, e.watch, 24); err != nil {
		t.Fatal(err)
	}
	eventually(t, "backfill done", func() bool { return !e.w.BackfillState(e.watch).Running })
	if st := e.w.BackfillState(e.watch); !strings.Contains(st.Error, "被限流") {
		t.Fatalf("backfill during the wait = %+v", st)
	}
	if n := e.tgCalls(); n != calls {
		t.Fatalf("backfill made %d Telegram calls during the wait", n-calls)
	}
}
