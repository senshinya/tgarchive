package userbot

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"tgarchive/internal/linkparse"
	"tgarchive/internal/store"
)

// privateWatchEnv watches channel 500 as a private channel (no username to resolve it by) that
// the account has lost: Telegram rejects it and it is not among the account's dialogs.
func privateWatchEnv(t *testing.T) *watchEnv {
	t.Helper()
	e := newWatchEnv(t, fire10)
	e.st.UpsertChannel(ctx, store.Channel{ChannelID: 500, Title: "Chan"}, 2)
	e.st.PutPeers(ctx, []store.Peer{{ChannelID: 500, AccessHash: 5005, Title: "Chan"}}, 2)
	e.tg.chanErr = "CHANNEL_PRIVATE"
	return e
}

func (e *watchEnv) dialogScans() int { return e.tg.called("*tg.MessagesGetDialogsRequest") }

func TestWatchLostPrivateChannelScansDialogsOnce(t *testing.T) {
	e := privateWatchEnv(t)
	for i := 0; i < 3; i++ {
		e.w.PollOnce(ctx)
	}
	if n := e.dialogScans(); n != 1 {
		t.Fatalf("getDialogs calls over 3 rounds = %d, want 1", n)
	}
	w := e.get(t)
	if w.Status != store.WatchError || !strings.Contains(w.LastError, "无法访问") || e.n.count() != 1 {
		t.Fatalf("watch = %+v, notified %d", w, e.n.count())
	}
	// The daily channel refresh leaves it alone too.
	e.w.RefreshChannels(ctx)
	if n := e.dialogScans(); n != 1 {
		t.Fatalf("RefreshChannels scanned dialogs again (%d calls)", n)
	}
	// Once the entry has expired the channel is looked for again, once.
	e.now = e.now.Add(31 * time.Minute)
	e.w.PollOnce(ctx)
	e.w.PollOnce(ctx)
	if n := e.dialogScans(); n != 2 {
		t.Fatalf("getDialogs calls after expiry = %d, want 2", n)
	}
	if w := e.get(t); w.Status != store.WatchError || !strings.Contains(w.LastError, "无法访问") {
		t.Fatalf("watch after expiry = %+v", w)
	}
}

func TestWatchRejoinedPrivateChannelFoundByScan(t *testing.T) {
	e := privateWatchEnv(t)
	e.w.PollOnce(ctx)
	// Rejoined: a link sent to the fetcher (sharing the record) scans despite the entry and finds it.
	e.tg.chanErr = ""
	e.tg.set(func(*fakeTG) { e.tg.member = true })
	f := NewFetcher(e.api, e.st, nil, nil, nil, nil, t.TempDir())
	f.Now = e.w.Now
	f.Absent = e.w.Absent
	e.tg.post(42, "x", 0)
	if _, err := f.fetch(ctx, linkparse.Link{ChannelID: 500, MsgID: 42}); err != nil {
		t.Fatalf("fetch = %v", err)
	}
	if e.w.Absent.has(500, e.now) {
		t.Fatal("a scan that found the channel kept it as absent")
	}
	e.w.PollOnce(ctx)
	if w := e.get(t); w.Status != store.WatchOK {
		t.Fatalf("watch after rejoin = %+v", w)
	}
}

func TestFetcherScansDespiteAbsentChannel(t *testing.T) {
	e := newFetchEnv(t)
	e.tg.post(42, "secret", 0)
	e.f.Absent.add(500, e.f.Now())
	e.submit(t, 10, "https://t.me/c/500/42")
	e.runOne(t)
	if n := e.tg.called("*tg.MessagesGetDialogsRequest"); n != 1 {
		t.Fatalf("getDialogs calls = %d, want 1", n)
	}
	want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 代取失败：私有群/频道，代取账号未加入"}
	if got := e.tr.take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v", got)
	}
	if !e.f.Absent.has(500, e.f.Now()) {
		t.Fatal("a scan that did not find the channel should record it")
	}
	e.tg.set(func(*fakeTG) { e.tg.member = true })
	e.submit(t, 11, "https://t.me/c/500/42")
	e.runOne(t)
	if got := e.tr.take(); !reflect.DeepEqual(got, []string{"react 42 11 👀", "react 42 11 👌"}) {
		t.Fatalf("calls = %v", got)
	}
	if e.f.Absent.has(500, e.f.Now()) {
		t.Fatal("a scan that found the channel kept it as absent")
	}
}

func TestAbsentChannelsExpireAndStayBounded(t *testing.T) {
	a := NewAbsentChannels()
	t0 := time.Unix(1700000000, 0)
	a.add(1, t0)
	if !a.has(1, t0.Add(a.TTL-time.Second)) || a.has(1, t0.Add(a.TTL)) {
		t.Fatal("entry should last exactly TTL")
	}
	for i := int64(0); i < absentMax+10; i++ {
		a.add(100+i, t0.Add(time.Duration(i)*time.Millisecond))
	}
	if n := a.size(); n != absentMax {
		t.Fatalf("size = %d, want %d", n, absentMax)
	}
	if a.has(100, t0) || !a.has(100+absentMax+9, t0) {
		t.Fatal("the oldest entries should go first")
	}
	// Adding sweeps expired entries.
	a.add(1, t0.Add(a.TTL+time.Hour))
	if n := a.size(); n != 1 {
		t.Fatalf("size after expiry = %d, want 1", n)
	}
	a.remove(1)
	if a.size() != 0 {
		t.Fatal("remove kept the entry")
	}
}

func TestPickerScanClearsAbsentChannels(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.manyDialogs = 10
	e.w.Absent.add(2002, e.now)
	e.w.Channels(ctx, true)
	eventually(t, "scan done", func() bool { l, _ := e.w.Channels(ctx, false); return !l.Loading })
	if e.w.Absent.has(2002, e.now) {
		t.Fatal("a channel the picker scan found is still taken as absent")
	}
}
