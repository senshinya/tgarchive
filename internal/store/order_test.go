package store

import (
	"fmt"
	"path/filepath"
	"testing"
)

func posIDs(views []MessageView) []int64 {
	out := make([]int64, len(views))
	for i, v := range views {
		out[i] = v.TgMessageID
	}
	return out
}

// A channel conversation is in the order its posts were published, whenever each was archived:
// a backfilled or late-hit post lands where it belongs, and paging around it keeps that order.
func TestChannelTimelineFollowsPostOrder(t *testing.T) {
	s := newStore(t)
	seedWatch(t, s)
	ids := map[int64]int64{}
	var chat int64
	for i, tg := range []int64{10, 30, 20, 40, 15} { // archived in this order
		res, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(tg, "", fmt.Sprint("k", tg)), Now: int64(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		chat, ids[tg] = res.ChatID, res.MessageID
	}
	all, err := s.ListMessagesPage(ctx, chat, Page{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInts(posIDs(all), []int64{10, 15, 20, 30, 40}) {
		t.Fatalf("timeline = %v", posIDs(all))
	}
	if all[1].Pos != 15 {
		t.Fatalf("pos = %d", all[1].Pos)
	}
	cases := []struct {
		name string
		p    Page
		lim  int
		want []int64
	}{
		{"newest", Page{}, 2, []int64{30, 40}},
		{"before 20", Page{Before: ids[20]}, 2, []int64{10, 15}},
		{"after 15", Page{After: ids[15]}, 2, []int64{20, 30}},
		{"around 20", Page{Around: ids[20]}, 3, []int64{15, 20, 30}},
		{"before 30 (archived before 20 and 15)", Page{Before: ids[30]}, 10, []int64{10, 15, 20}},
		{"unknown cursor", Page{Before: 99999}, 10, []int64{}},
	}
	for _, c := range cases {
		got, err := s.ListMessagesPage(ctx, chat, c.p, c.lim)
		if err != nil || !equalInts(posIDs(got), c.want) {
			t.Fatalf("%s: %v, %v (want %v)", c.name, posIDs(got), err, c.want)
		}
	}
	media, err := s.ListChatMedia(ctx, chat, "media", ids[30], 10)
	if err != nil || !equalInts(posIDs(media), []int64{20, 15, 10}) {
		t.Fatalf("media before 30 = %v, %v", posIDs(media), err)
	}
	// Read up to 30: the late post 15 lands behind the marker and is not unread; 40 is.
	if err := s.MarkRead(ctx, chat, ids[30]); err != nil {
		t.Fatal(err)
	}
	if n, last := unreadOf(t, s, chat); n != 1 || last != 30 {
		t.Fatalf("unread = %d, last read = %d", n, last)
	}
	if _, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(25, ""), Now: 9}); err != nil {
		t.Fatal(err)
	}
	if n, _ := unreadOf(t, s, chat); n != 1 {
		t.Fatalf("a post archived behind the marker is not unread: %d", n)
	}
	chats, err := s.ListChats(ctx, 0)
	if err != nil || len(chats) != 1 || chats[0].LastKind != "photo" {
		t.Fatalf("chats = %+v, %v", chats, err)
	}
}

// Albums are not cut at a page edge in post order either.
func TestChannelAlbumKeptWholeInPostOrder(t *testing.T) {
	s := newStore(t)
	seedWatch(t, s)
	var chat int64
	ids := map[int64]int64{}
	for i, p := range []struct {
		tg    int64
		group string
	}{{50, ""}, {21, "g"}, {22, "g"}, {23, "g"}, {10, ""}} {
		res, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(p.tg, p.group), Now: int64(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		chat, ids[p.tg] = res.ChatID, res.MessageID
	}
	got, err := s.ListMessagesPage(ctx, chat, Page{}, 2) // 50 and 23: the album's head is pulled in
	if err != nil || !equalInts(posIDs(got), []int64{21, 22, 23, 50}) {
		t.Fatalf("newest = %v, %v", posIDs(got), err)
	}
	got, err = s.ListMessagesPage(ctx, chat, Page{After: ids[10]}, 2)
	if err != nil || !equalInts(posIDs(got), []int64{21, 22, 23}) {
		t.Fatalf("after 10 = %v, %v", posIDs(got), err)
	}
}

// Migration 10 turns the read marker into a post position: the newest post among those archived
// up to the old marker.
func TestMigration10ReadPosition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	old := openAt(t, path, 9)
	seedWatch(t, old)
	var chat int64
	var ids []int64
	for i, tg := range []int64{10, 30, 20} {
		res, err := old.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(tg, ""), Now: int64(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		chat = res.ChatID
		ids = append(ids, res.MessageID)
	}
	if _, err := old.db.Exec("UPDATE chats SET last_read_id = ? WHERE id = ?", ids[1], chat); err != nil { // read 10 and 30
		t.Fatal(err)
	}
	if err := old.migrate(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if n, last := unreadOf(t, old, chat); last != 30 || n != 0 {
		t.Fatalf("last read = %d, unread = %d", last, n)
	}
	old.db.Close()
}

func equalInts(a, b []int64) bool {
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

// Comments go by their discussion group ids, whatever order they were stored in, and "after the
// post" starts at the first of them although the post's own (channel) id is larger.
func TestCommentsFollowTheirOrder(t *testing.T) {
	s := newStore(t)
	seedWatch(t, s)
	post, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(500, ""), Now: 300})
	if err != nil {
		t.Fatal(err)
	}
	for i, tg := range []int64{13, 11, 12} {
		c := comment(tg, "c", fmt.Sprint("mt:photo:", tg), `{"from":{"kind":"user","id":1,"name":"A"}}`)
		if _, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: c, Now: int64(400 + i), ThreadRootID: post.MessageID}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.ListCommentsPage(ctx, post.ChatID, post.MessageID, Page{After: post.MessageID}, 2)
	if err != nil || !equalInts(posIDs(first), []int64{11, 12}) {
		t.Fatalf("from the start = %v, %v", posIDs(first), err)
	}
	rest, err := s.ListCommentsPage(ctx, post.ChatID, post.MessageID, Page{After: first[1].ID}, 2)
	if err != nil || !equalInts(posIDs(rest), []int64{13}) {
		t.Fatalf("after 12 = %v, %v", posIDs(rest), err)
	}
	chats, err := s.ListChats(ctx, 0)
	if err != nil || chats[0].FirstUnreadID != post.MessageID {
		t.Fatalf("first unread = %+v, %v", chats, err)
	}
}
