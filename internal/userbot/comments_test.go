package userbot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/store"
)

const groupID = 700

// discussion is the channel's discussion group as watchTG serves it: comments per post.
type discussion struct {
	comments   map[int][]*tg.Message // post id → its comments
	users      []tg.UserClass
	chats      []tg.ChatClass
	repliesErr string // error type messages.getReplies answers with
	repliesN   int    // messages.getReplies calls
}

func (d *discussion) group() *tg.Channel {
	return &tg.Channel{ID: groupID, AccessHash: 7007, Title: "Chan Chat", Megagroup: true, Photo: &tg.ChatPhotoEmpty{}}
}

func (w *watchTG) replies(r *tg.MessagesGetRepliesRequest) (*tg.MessagesChannelMessages, error) {
	w.repliesN++
	if w.repliesErr != "" {
		return nil, tgerr.New(400, w.repliesErr)
	}
	var list []*tg.Message
	for _, m := range w.comments[r.MsgID] {
		if m.ID > r.MinID && (r.OffsetID == 0 || m.ID < r.OffsetID) {
			list = append(list, m)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID > list[j].ID })
	if len(list) > r.Limit {
		list = list[:r.Limit]
	}
	out := &tg.MessagesChannelMessages{Users: w.users, Chats: append([]tg.ChatClass{w.ch, w.group()}, w.chats...)}
	for _, m := range list {
		out.Messages = append(out.Messages, m)
	}
	return out, nil
}

// comment adds a comment on post (by user from, 0 for an anonymous admin) and marks the post as
// taking comments, with the counters Telegram keeps.
func (w *watchTG) comment(post, id int, from int64, text string) *tg.Message {
	if w.comments == nil {
		w.comments = map[int][]*tg.Message{}
	}
	c := &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: groupID}, Date: 1700000000 + id, Message: text}
	if from != 0 {
		c.SetFromID(&tg.PeerUser{UserID: from})
	}
	c.SetReplyTo(&tg.MessageReplyHeader{ReplyToMsgID: 1}) // the post's copy in the group
	w.comments[post] = append(w.comments[post], c)
	w.takesComments(post)
	return c
}

func (w *watchTG) takesComments(post int) {
	r := tg.MessageReplies{Comments: true, ChannelID: groupID}
	for _, c := range w.comments[post] {
		r.Replies++
		r.MaxID = max(r.MaxID, c.ID)
	}
	if r.MaxID != 0 {
		r.SetMaxID(r.MaxID)
	}
	w.posts[post].SetReplies(r)
}

func (e *watchEnv) comments(t *testing.T, post int64) []store.MessageView {
	t.Helper()
	root := e.root(t, post)
	out, err := e.st.ListCommentsPage(ctx, e.get(t).ChatID, root, store.Page{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *watchEnv) root(t *testing.T, post int64) int64 {
	t.Helper()
	for _, m := range e.archived(t) {
		if m.TgMessageID == post {
			return m.ID
		}
	}
	t.Fatalf("post %d not archived", post)
	return 0
}

type fromField struct {
	From store.Commenter `json:"from"`
}

func fromOf(t *testing.T, m store.MessageView) store.Commenter {
	t.Helper()
	var f fromField
	if err := json.Unmarshal(m.Extra, &f); err != nil {
		t.Fatal(err)
	}
	return f.From
}

func TestArchiveKeepsComments(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx)
	e.tg.users = []tg.UserClass{
		&tg.User{ID: 11, AccessHash: 111, FirstName: "Ann", Photo: &tg.UserProfilePhoto{PhotoID: 9001}},
		&tg.User{ID: 12, Min: true, FirstName: "Bo", LastName: "Lee"},
	}
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20})
	e.tg.comment(6, 101, 11, "first")
	e.tg.comment(6, 102, 12, "second")
	nested := e.tg.comment(6, 103, 11, "answer")
	nested.SetReplyTo(&tg.MessageReplyHeader{ReplyToMsgID: 102, ReplyToTopID: 1})
	e.tg.comment(6, 104, 0, "from the admins")
	e.tg.comments[6][3].SetReactions(tg.MessageReactions{Results: []tg.ReactionCount{{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 2}}})
	e.w.PollOnce(ctx)

	// The timeline holds the post only.
	if got := tgIDs(e.archived(t)); len(got) != 1 || got[0] != 6 {
		t.Fatalf("timeline = %v", got)
	}
	cs := e.comments(t, 6)
	if got := tgIDs(cs); len(got) != 4 || got[0] != 101 || got[3] != 104 {
		t.Fatalf("comments = %v", got)
	}
	if cs[0].Source != "channel_comment" || cs[0].ReplyToTgMessageID != 0 || cs[0].ThreadRootID != e.root(t, 6) {
		t.Fatalf("comment = %+v", cs[0])
	}
	if cs[2].ReplyToTgMessageID != 102 || cs[2].Reply == nil || cs[2].Reply.Text != "second" {
		t.Fatalf("nested comment = %+v (reply %+v)", cs[2], cs[2].Reply)
	}
	if f := fromOf(t, cs[0]); f != (store.Commenter{Kind: "user", ID: 11, Name: "Ann", Photo: true}) {
		t.Fatalf("from = %+v", f)
	}
	if f := fromOf(t, cs[1]); f != (store.Commenter{Kind: "user", ID: 12, Name: "Bo Lee"}) {
		t.Fatalf("min user = %+v", f)
	}
	if f := fromOf(t, cs[3]); f != (store.Commenter{Kind: "channel", ID: groupID, Name: "Chan Chat"}) {
		t.Fatalf("anonymous admin = %+v", f)
	}
	var cps PostStats
	if err := json.Unmarshal(cs[3].Stats, &cps); err != nil || len(cps.Reactions) != 1 || cps.Reactions[0].Count != 2 {
		t.Fatalf("comment reactions = %s", cs[3].Stats)
	}

	ps := e.stats(t, 6)
	if ps.Replies != 4 || ps.Comments == nil || ps.Comments.Unreadable {
		t.Fatalf("post stats = %+v", ps)
	}
	var recent []string
	for _, c := range ps.Comments.Recent {
		recent = append(recent, c.Name)
	}
	if len(recent) != 3 || recent[0] != "Chan Chat" || recent[1] != "Ann" || recent[2] != "Bo Lee" {
		t.Fatalf("recent = %v", recent)
	}
	if !e.event("comments.updated") {
		t.Fatal("no comments.updated event")
	}
}

func (e *watchEnv) event(typ string) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		e.evMu.Lock()
		for _, ev := range e.ev {
			if ev.Type == typ {
				e.evMu.Unlock()
				return true
			}
		}
		e.evMu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestRefreshAddsNewComments(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx)
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20})
	e.tg.comment(6, 101, 0, "one")
	e.w.PollOnce(ctx)
	calls := e.tg.repliesN

	// Nothing new on Telegram: the refresh does not ask.
	e.now = e.now.Add(10 * time.Minute)
	e.w.PollOnce(ctx)
	if e.tg.repliesN != calls {
		t.Fatalf("asked for comments with none new (%d calls)", e.tg.repliesN-calls)
	}

	e.tg.comment(6, 102, 0, "two")
	e.tg.comments[6][0].Message = "one, edited" // an early refresh does not see edits
	e.now = e.now.Add(10 * time.Minute)
	e.w.PollOnce(ctx)
	cs := e.comments(t, 6)
	if got := tgIDs(cs); len(got) != 2 || got[1] != 102 || cs[0].Text != "one" {
		t.Fatalf("after refresh = %v %q", got, cs[0].Text)
	}
	if ps := e.stats(t, 6); ps.Replies != 2 {
		t.Fatalf("replies = %d", ps.Replies)
	}

	// The day 3 checkpoint reads the whole section again.
	e.now = e.now.Add(3 * 24 * time.Hour)
	e.w.PollOnce(ctx)
	if cs := e.comments(t, 6); cs[0].Text != "one, edited" {
		t.Fatalf("day 3 = %q", cs[0].Text)
	}
}

func TestCommentsCap(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx)
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20})
	for i := 1; i <= 1050; i++ {
		e.tg.comment(6, 100+i, 0, "c")
	}
	e.w.PollOnce(ctx)
	root := e.root(t, 6)
	st, err := e.st.CommentState(ctx, root)
	if err != nil || st.Count != commentPages*historyPage || st.MaxTgID != 1150 {
		t.Fatalf("state = %+v, %v", st, err)
	}
}

func TestUnreadableDiscussion(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx)
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20})
	e.tg.comment(6, 101, 0, "hidden")
	e.tg.repliesErr = "CHANNEL_PRIVATE"
	e.w.PollOnce(ctx)
	ps := e.stats(t, 6)
	if ps.Comments == nil || !ps.Comments.Unreadable || ps.Replies != 1 {
		t.Fatalf("stats = %+v", ps)
	}
	if w := e.get(t); w.Status != store.WatchOK {
		t.Fatalf("watch status = %s", w.Status)
	}
	if cs := e.comments(t, 6); len(cs) != 0 {
		t.Fatalf("comments = %v", tgIDs(cs))
	}
}

func TestBackfillComments(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx)
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20})
	e.w.PollOnce(ctx) // archived before comments were kept
	e.tg.comment(6, 101, 0, "late")
	e.now = e.now.Add(30 * 24 * time.Hour) // long past the refreshes

	e.w.BackfillComments(ctx)
	if got := tgIDs(e.comments(t, 6)); len(got) != 1 {
		t.Fatalf("backfilled = %v", got)
	}
	if ps := e.stats(t, 6); ps.Comments == nil || len(ps.Comments.Recent) != 1 || ps.Replies != 1 {
		t.Fatalf("stats = %+v", ps)
	}
	calls := e.tg.repliesN
	e.tg.comment(6, 102, 0, "later")
	e.w.BackfillComments(ctx)
	if e.tg.repliesN != calls {
		t.Fatal("the backfill ran twice")
	}
}

func TestCommenterPhoto(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.oldPost(5)
	e.w.PollOnce(ctx)
	e.tg.users = []tg.UserClass{&tg.User{ID: 11, AccessHash: 111, FirstName: "Ann", Photo: &tg.UserProfilePhoto{PhotoID: 9001}}}
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20})
	e.tg.comment(6, 101, 11, "hi")
	e.w.PollOnce(ctx)

	rel, err := e.w.CommenterPhoto(ctx, "user", 11)
	if err != nil || rel != filepath.Join("users", "11.jpg") {
		t.Fatalf("photo = %q, %v", rel, err)
	}
	if b, err := os.ReadFile(filepath.Join(e.w.avatarDir, rel)); err != nil || string(b) != "jpeg" {
		t.Fatalf("file = %q, %v", b, err)
	}
	n := e.tg.called("*tg.UploadGetFileRequest")
	if _, err := e.w.CommenterPhoto(ctx, "user", 11); err != nil || e.tg.called("*tg.UploadGetFileRequest") != n {
		t.Fatalf("an unchanged photo was fetched again (%v)", err)
	}
	if _, err := e.w.CommenterPhoto(ctx, "user", 12); err != store.ErrNotFound {
		t.Fatalf("unknown commenter: %v", err)
	}
}
