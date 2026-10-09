package store

import (
	"testing"

	"tgarchive/internal/model"
)

// comment is a comment on the watched channel's post, with a photo (mt:<key>) and text.
func comment(id int64, text, key, from string) *model.Message {
	m := photoMsg(id, key)
	m.Source = model.SourceChannelComment
	m.Text = text
	m.OriginChatID = -1000000000700
	m.Extra = []byte(from)
	return m
}

func TestCommentsStayOutOfTheTimeline(t *testing.T) {
	s := newStore(t)
	seedWatch(t, s)
	post, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(1, "", "mt:photo:1"), Stats: `{"views":1}`, Now: 300})
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range []*model.Message{
		comment(11, "第一条评论 hello", "mt:photo:11", `{"from":{"kind":"user","id":1,"name":"A","photo":true}}`),
		comment(12, "第二条评论 hello", "mt:photo:12", `{"from":{"kind":"user","id":2,"name":"B"}}`),
		comment(13, "第三条评论 hello", "mt:photo:13", `{"from":{"kind":"user","id":1,"name":"A","photo":true}}`),
	} {
		if _, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: c, Now: int64(400 + i), ThreadRootID: post.MessageID}); err != nil {
			t.Fatal(err)
		}
	}

	timeline, err := s.ListMessages(ctx, post.ChatID, 0, 50)
	if err != nil || len(timeline) != 1 || timeline[0].ID != post.MessageID {
		t.Fatalf("timeline = %v, %v", timeline, err)
	}
	cs, err := s.ListCommentsPage(ctx, post.ChatID, post.MessageID, Page{}, 2)
	if err != nil || len(cs) != 2 || cs[0].TgMessageID != 12 || cs[1].ThreadRootID != post.MessageID {
		t.Fatalf("newest comments = %+v, %v", cs, err)
	}
	if older, err := s.ListCommentsPage(ctx, post.ChatID, post.MessageID, Page{Before: cs[0].ID}, 2); err != nil || len(older) != 1 || older[0].TgMessageID != 11 {
		t.Fatalf("older comments = %+v, %v", older, err)
	}
	if _, err := s.ListCommentsPage(ctx, post.ChatID, cs[0].ID, Page{}, 2); err != ErrNotFound {
		t.Fatalf("comments of a comment: %v", err)
	}
	if _, err := s.ListCommentsPage(ctx, post.ChatID+1, post.MessageID, Page{}, 2); err != ErrNotFound {
		t.Fatalf("comments of another chat's post: %v", err)
	}

	chats, err := s.ListChats(ctx, 0)
	if err != nil || len(chats) != 1 || chats[0].LastMessageAt != 300 || chats[0].LastText != "caption" || chats[0].Unread != 1 {
		t.Fatalf("chat = %+v, %v", chats, err)
	}
	if hits, err := s.Search(ctx, "评论", 0, 0, 50); err != nil || len(hits) != 0 {
		t.Fatalf("search found comments: %d, %v", len(hits), err)
	}
	if wall, err := s.ListAllMedia(ctx, "all", "all", 0, 50); err != nil || len(wall) != 1 {
		t.Fatalf("wall = %d, %v", len(wall), err)
	}
	if shared, err := s.ListChatMedia(ctx, post.ChatID, "media", 0, 50); err != nil || len(shared) != 1 {
		t.Fatalf("shared media = %d, %v", len(shared), err)
	}
	st, err := s.Stats(ctx, 0, 1000)
	if err != nil || st.Totals.Messages != 1 || st.Totals.MediaFiles != 4 {
		t.Fatalf("stats = %+v, %v", st.Totals, err)
	}

	state, err := s.CommentState(ctx, post.MessageID)
	if err != nil || state != (CommentState{MaxTgID: 13, Count: 3}) {
		t.Fatalf("state = %+v, %v", state, err)
	}
	recent, err := s.RecentCommenters(ctx, post.MessageID, 3)
	if err != nil || len(recent) != 2 || recent[0] != (Commenter{Kind: "user", ID: 1, Name: "A", Photo: true}) || recent[1].ID != 2 {
		t.Fatalf("recent = %+v, %v", recent, err)
	}
}

func TestCommentReplyQuote(t *testing.T) {
	s := newStore(t)
	seedWatch(t, s)
	post, _ := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(1, ""), Now: 100})
	s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: comment(11, "question", "mt:photo:11", `{"from":{"kind":"user","id":1,"name":"A"}}`),
		Now: 200, ThreadRootID: post.MessageID})
	answer := comment(12, "answer", "mt:photo:12", `{"from":{"kind":"user","id":2,"name":"B"}}`)
	answer.ReplyToTgMessageID = 11
	s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: answer, Now: 201, ThreadRootID: post.MessageID})
	cs, err := s.ListCommentsPage(ctx, post.ChatID, post.MessageID, Page{}, 10)
	if err != nil || len(cs) != 2 || cs[1].Reply == nil || cs[1].Reply.Text != "question" || string(cs[1].Reply.Extra) == "" {
		t.Fatalf("reply = %+v, %v", cs, err)
	}
}

func TestCommentPeers(t *testing.T) {
	s := newStore(t)
	if _, err := s.GetCommentPeer(ctx, "user", 1); err != ErrNotFound {
		t.Fatalf("unknown: %v", err)
	}
	if err := s.PutCommentPeer(ctx, "user", 1, 9, `{"access_hash":5}`, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommentPeerSaved(ctx, "user", 1, 9); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCommentPeer(ctx, "user", 1, 10, `{"access_hash":6}`, 20); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetCommentPeer(ctx, "user", 1)
	if err != nil || p.PhotoID != 10 || p.SavedPhotoID != 9 || p.Ref != `{"access_hash":6}` {
		t.Fatalf("peer = %+v, %v", p, err)
	}
	if err := s.SetCommentPeerSaved(ctx, "channel", 1, 9); err != ErrNotFound {
		t.Fatalf("unknown channel: %v", err)
	}
}
