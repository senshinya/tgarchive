package store

import (
	"errors"
	"testing"
)

func watchPostIDs(ps []WatchPost) []int64 {
	out := []int64{}
	for _, p := range ps {
		out = append(out, p.TgMessageID)
	}
	return out
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRecentWatchPosts(t *testing.T) {
	s := newStore(t)
	seedWatch(t, s)
	add := func(id int64, group, stats string) int64 {
		t.Helper()
		res, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(id, group, "mt:photo:"+string(rune('a'+id))), Stats: stats, Now: 1})
		if err != nil {
			t.Fatal(err)
		}
		return res.MessageID
	}
	add(1, "", `{"views":1,"hit":{"at":1000,"reasons":[]}}`) // hit too long ago
	add(2, "g", `{"views":2,"hit":{"at":5000,"reasons":[]}}`)
	add(3, "g", `{"views":2,"hit":{"at":5000,"reasons":[]}}`)
	gone := add(4, "", `{"views":4,"hit":{"at":6000,"reasons":[]}}`)
	add(5, "", ``)            // no stats at all
	add(6, "", `{"views":6}`) // no hit recorded
	if _, _, err := s.DeleteMessage(ctx, gone, 7000); err != nil {
		t.Fatal(err)
	}

	got, err := s.RecentWatchPosts(ctx, chanID, 2000)
	if err != nil || !sameIDs(watchPostIDs(got), []int64{2, 3}) {
		t.Fatalf("RecentWatchPosts = %v, %v", watchPostIDs(got), err)
	}
	if got[0].GroupID != "g" || got[0].Stats != `{"views":2,"hit":{"at":5000,"reasons":[]}}` || got[0].ChatID == 0 || got[0].MessageID == 0 {
		t.Fatalf("post = %+v", got[0])
	}
	if got, _ := s.RecentWatchPosts(ctx, 9999, 0); len(got) != 0 {
		t.Fatalf("unknown channel = %v", watchPostIDs(got))
	}

	if ok, err := s.SetPostStats(ctx, got[0].MessageID, got[0].Stats, `{"views":9}`); !ok || err != nil {
		t.Fatalf("SetPostStats = %v %v", ok, err)
	}
	if v, _ := s.GetMessageView(ctx, got[0].MessageID); string(v.Stats) != `{"views":9}` {
		t.Fatalf("stats after SetPostStats = %s", v.Stats)
	}
	// Stats written meanwhile (a new hit re-archived the post) are not overwritten.
	if ok, err := s.SetPostStats(ctx, got[0].MessageID, got[0].Stats, `{"views":10}`); ok || err != nil {
		t.Fatalf("SetPostStats over newer stats = %v %v", ok, err)
	}
	if v, _ := s.GetMessageView(ctx, got[0].MessageID); string(v.Stats) != `{"views":9}` {
		t.Fatalf("newer stats overwritten: %s", v.Stats)
	}
	if ok, err := s.SetPostStats(ctx, 99999, `{}`, `{}`); ok || err != nil {
		t.Fatalf("SetPostStats on a missing message = %v %v", ok, err)
	}
}

func TestChatWatchPostsTakesWholeAlbums(t *testing.T) {
	s := newStore(t)
	seedWatch(t, s)
	var ids []int64
	var chat int64
	for i, g := range []string{"", "g", "g", ""} {
		res, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(int64(i+1), g, "mt:photo:"+string(rune('a'+i))),
			Stats: `{"hit":{"at":1,"reasons":[]}}`, Now: 1})
		if err != nil {
			t.Fatal(err)
		}
		ids, chat = append(ids, res.MessageID), res.ChatID
	}
	channel, got, err := s.ChatWatchPosts(ctx, chat, []int64{ids[1]})
	if err != nil || channel != chanID || !sameIDs(watchPostIDs(got), []int64{2, 3}) {
		t.Fatalf("ChatWatchPosts = %d %v %v", channel, watchPostIDs(got), err)
	}
	if _, got, _ := s.ChatWatchPosts(ctx, chat, []int64{ids[0], ids[3], 99999}); !sameIDs(watchPostIDs(got), []int64{1, 4}) {
		t.Fatalf("singles = %v", watchPostIDs(got))
	}

	bot := seedBot(t, s, 777)
	priv := ingest(t, s, bot, textMsg(1, "hi"))
	if _, _, err := s.ChatWatchPosts(ctx, priv.ChatID, []int64{priv.MessageID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private chat: %v", err)
	}
}
