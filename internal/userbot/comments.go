package userbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tgdown "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/convert/mtproto"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

const (
	// commentPages bounds one read of a post's comments: at most this many pages of historyPage.
	commentPages = 10
	// recentCommenters is how many avatars the comment button shows (Web A: 3).
	recentCommenters = 3
	// commentsFullAge is from when on a refresh re-reads a post's whole comment section (the day 3
	// and day 7 checkpoints), picking up edits and reaction changes; earlier ones only add new comments.
	commentsFullAge = 3 * 86400
	// CommentsBackfillKey marks the one-off comment backfill of posts archived before comments were kept.
	CommentsBackfillKey = "comments_backfill"
	// backfillPause separates the posts of the one-off comment backfill.
	backfillPause = 300 * time.Millisecond
	// openCommentsEvery is how often opening a post's comments may refresh them.
	openCommentsEvery = 5 * time.Minute
)

var errNoDiscussion = errors.New("discussion group missing from the replies")

// CommentsInfo is what an archived post's stats say about its comments: present when the post
// takes comments (the count is PostStats.Replies).
type CommentsInfo struct {
	// Recent are the authors of the newest stored comments, newest first (the button's avatars).
	Recent []store.Commenter `json:"recent"`
	// Unreadable: the discussion group cannot be read by the account (a private group it has not
	// joined); the count is shown, the comments are not kept.
	Unreadable bool `json:"unreadable,omitempty"`
}

// commentsSource picks the message of an album (or single post) its comment thread hangs on.
func commentsSource(msgs []*tg.Message) (*tg.Message, tg.MessageReplies, bool) {
	for _, m := range msgs {
		if r, ok := m.GetReplies(); ok && r.Comments && r.ChannelID != 0 {
			return m, r, true
		}
	}
	return nil, tg.MessageReplies{}, false
}

// syncComments stores the comments of archived post rootID (the head of the album msgs) that are
// newer than the newest one kept, or all of them (up to commentPages pages) when full, and returns
// the post's comment info; nil when the post takes no comments. Only a flood wait or a transient
// failure is returned: anything else is logged and leaves the comments as they are.
func (w *Watcher) syncComments(ctx context.Context, api *tg.Client, ch *tg.Channel, rootID int64, msgs []*tg.Message, full bool) (*CommentsInfo, error) {
	src, replies, ok := commentsSource(msgs)
	if !ok {
		return nil, nil
	}
	info := &CommentsInfo{Recent: []store.Commenter{}}
	state, err := w.st.CommentState(ctx, rootID)
	if err != nil {
		return nil, err
	}
	if full || (replies.Replies > 0 && int64(replies.MaxID) > state.MaxTgID) {
		n, err := w.readComments(ctx, api, ch, rootID, src, replies, state, full)
		switch {
		case err == nil:
			if n > 0 {
				w.hub.Publish(events.Event{Type: "comments.updated", Data: map[string]int64{"chat_id": w.chatOf(ctx, rootID), "root_id": rootID}})
				if w.wakeDL != nil {
					w.wakeDL()
				}
			}
		case tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID", "CHANNEL_PUBLIC_GROUP_NA", "MSG_ID_INVALID"):
			info.Unreadable = true
		case errors.Is(err, store.ErrNoWatch):
			// Unwatched while this ran: nothing more is kept.
		default:
			if _, flood := tgerr.AsFloodWait(err); flood || watchTransient(err) {
				return nil, err
			}
			log.Printf("watch: comments of post %d: %v", rootID, err)
		}
	}
	if info.Recent, err = w.st.RecentCommenters(ctx, rootID, recentCommenters); err != nil {
		return nil, err
	}
	return info, nil
}

// chatOf is the conversation of a stored message (0 when it is gone).
func (w *Watcher) chatOf(ctx context.Context, messageID int64) int64 {
	v, err := w.st.GetMessageView(ctx, messageID)
	if err != nil {
		return 0
	}
	return v.ChatID
}

// readComments pages through a post's comments (newest first, above the newest one kept unless
// full) and stores them oldest first, returning how many were stored.
func (w *Watcher) readComments(ctx context.Context, api *tg.Client, ch *tg.Channel, rootID int64, src *tg.Message,
	replies tg.MessageReplies, state store.CommentState, full bool) (int, error) {
	minID := int(state.MaxTgID)
	if full {
		minID = 0
	}
	names := mtproto.NamesFrom(nil, nil)
	var got []*tg.Message
	offset := 0
	for page := 0; page < commentPages; page++ {
		res, err := api.MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{
			Peer: &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, MsgID: src.ID,
			OffsetID: offset, MinID: minID, Limit: historyPage,
		})
		if err != nil {
			return 0, err
		}
		list, users, chats, err := messagesOf(res)
		if err != nil {
			return 0, err
		}
		names.Add(users, chats)
		low := 0
		for _, mc := range list {
			id := mc.GetID()
			if low == 0 || id < low {
				low = id
			}
			if m, ok := mc.(*tg.Message); ok && id > minID {
				got = append(got, m)
			}
		}
		if len(list) < historyPage || low <= minID+1 {
			break
		}
		offset = low
	}
	if len(got) == 0 {
		return 0, nil
	}
	group := names.Channels[replies.ChannelID]
	if group == nil {
		return 0, errNoDiscussion
	}
	sort.Slice(got, func(i, j int) bool { return got[i].ID < got[j].ID })
	seen := map[string]bool{}
	now := w.Now().Unix()
	stored := 0
	for _, m := range got {
		c, err := mtproto.Convert(m, mtproto.ChannelFrom(group), names)
		if err != nil {
			return stored, err
		}
		c.Source = model.SourceChannelComment
		// A comment answers the thread unless it quotes another comment (then the thread is its top).
		if h, ok := m.ReplyTo.(*tg.MessageReplyHeader); !ok || h.ReplyToTopID == 0 {
			c.ReplyToTgMessageID = 0
		}
		from := w.commenter(ctx, group, m, names, seen)
		if c.Extra, err = withFrom(c.Extra, from); err != nil {
			return stored, err
		}
		ps := w.postStats(ctx, api, []*tg.Message{m}, countersOf([]*tg.Message{m}))
		ps.Views, ps.Forwards, ps.Replies = 0, 0, 0
		b, err := json.Marshal(ps)
		if err != nil {
			return stored, err
		}
		ir, err := w.st.Ingest(ctx, store.IngestInput{ChannelID: ch.ID, Msg: c, Stats: string(b), Now: now, ThreadRootID: rootID})
		if err != nil {
			return stored, err
		}
		if w.MediaDir != "" {
			downloader.RemoveFiles(w.MediaDir, ir.OrphanPaths)
		}
		stored++
	}
	return stored, nil
}

// withFrom adds the author to a converted message's extra.
func withFrom(extra json.RawMessage, from store.Commenter) (json.RawMessage, error) {
	m := map[string]any{}
	if len(extra) > 0 {
		if err := json.Unmarshal(extra, &m); err != nil {
			return nil, err
		}
	}
	m["from"] = from
	return json.Marshal(m)
}

// peerRef is how a commenter is addressed when their photo is fetched: by access hash, or as
// seen in a comment of the discussion group (a "min" peer, whose hash the account does not have).
type peerRef struct {
	AccessHash int64 `json:"access_hash,omitempty"`
	GroupID    int64 `json:"group_id,omitempty"`
	GroupHash  int64 `json:"group_hash,omitempty"`
	MsgID      int   `json:"msg_id,omitempty"`
}

func (r peerRef) input(kind string, id int64) tg.InputPeerClass {
	if r.AccessHash == 0 && r.GroupID != 0 {
		group := &tg.InputPeerChannel{ChannelID: r.GroupID, AccessHash: r.GroupHash}
		if kind == "user" {
			return &tg.InputPeerUserFromMessage{Peer: group, MsgID: r.MsgID, UserID: id}
		}
		return &tg.InputPeerChannelFromMessage{Peer: group, MsgID: r.MsgID, ChannelID: id}
	}
	if kind == "user" {
		return &tg.InputPeerUser{UserID: id, AccessHash: r.AccessHash}
	}
	return &tg.InputPeerChannel{ChannelID: id, AccessHash: r.AccessHash}
}

// commenter tells who wrote comment m (the group itself for an anonymous admin) and records how to
// fetch their photo later; seen memoizes the commenters recorded in this read.
func (w *Watcher) commenter(ctx context.Context, group *tg.Channel, m *tg.Message, names mtproto.Names, seen map[string]bool) store.Commenter {
	seenIn := peerRef{GroupID: group.ID, GroupHash: group.AccessHash, MsgID: m.ID}
	var from store.Commenter
	var photoID int64
	ref := seenIn
	switch p := m.FromID.(type) {
	case *tg.PeerUser:
		from = store.Commenter{Kind: "user", ID: p.UserID}
		if u := names.Users[p.UserID]; u != nil {
			from.Name = strings.TrimSpace(u.FirstName + " " + u.LastName)
			if from.Name == "" {
				from.Name = u.Username
			}
			if ph, ok := u.Photo.(*tg.UserProfilePhoto); ok {
				photoID = ph.PhotoID
			}
			if !u.Min && u.AccessHash != 0 {
				ref = peerRef{AccessHash: u.AccessHash}
			}
		}
		if from.Name == "" {
			from.Name = "已注销账号"
		}
	case *tg.PeerChannel:
		from = store.Commenter{Kind: "channel", ID: p.ChannelID}
		if c := names.Channels[p.ChannelID]; c != nil {
			from.Name = c.Title
			if ph, ok := c.Photo.(*tg.ChatPhoto); ok {
				photoID = ph.PhotoID
			}
			if !c.Min && c.AccessHash != 0 {
				ref = peerRef{AccessHash: c.AccessHash}
			}
		}
	default: // an anonymous admin writes as the group
		from = store.Commenter{Kind: "channel", ID: group.ID, Name: group.Title}
		if ph, ok := group.Photo.(*tg.ChatPhoto); ok {
			photoID = ph.PhotoID
		}
		ref = peerRef{AccessHash: group.AccessHash}
	}
	from.Photo = photoID != 0
	key := fmt.Sprintf("%s:%d", from.Kind, from.ID)
	if !seen[key] {
		seen[key] = true
		b, _ := json.Marshal(ref)
		if err := w.st.PutCommentPeer(ctx, from.Kind, from.ID, photoID, string(b), w.Now().Unix()); err != nil {
			log.Printf("watch: commenter %s: %v", key, err)
		}
	}
	return from
}

// CommenterPhoto stores a commenter's small profile photo at avatars/users/<id>.jpg (channels:
// avatars/channels/<id>.jpg) unless the one stored is of their current photo, returning its path
// relative to the avatar directory. store.ErrNotFound when the commenter is unknown or has no
// photo; a failed download is not retried for ten minutes.
func (w *Watcher) CommenterPhoto(ctx context.Context, kind string, id int64) (string, error) {
	dir := "users"
	if kind == "channel" {
		dir = "channels"
	}
	rel := filepath.Join(dir, fmt.Sprintf("%d.jpg", id))
	dst := filepath.Join(w.avatarDir, rel)
	p, err := w.st.GetCommentPeer(ctx, kind, id)
	if err != nil {
		return "", err
	}
	if p.PhotoID == 0 {
		return "", store.ErrNotFound
	}
	if _, serr := os.Stat(dst); serr == nil && p.SavedPhotoID == p.PhotoID {
		return rel, nil
	}
	key := kind + ":" + strconv.FormatInt(id, 10)
	w.mu.Lock()
	t, missed := w.commenterMiss[key]
	w.mu.Unlock()
	if missed && w.Now().Sub(t) < 10*time.Minute {
		return "", store.ErrNotFound
	}
	var ref peerRef
	if err := json.Unmarshal([]byte(p.Ref), &ref); err != nil {
		return "", err
	}
	// Shares the channel photos' two download slots: avatars must not flood the account.
	select {
	case w.photoSlots <- struct{}{}:
		defer func() { <-w.photoSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	err = w.with(ctx, func(api *tg.Client) error {
		return savePhoto(ctx, api, &tg.InputPeerPhotoFileLocation{Peer: ref.input(kind, id), PhotoID: p.PhotoID}, dst)
	})
	if err == nil {
		err = w.st.SetCommentPeerSaved(ctx, kind, id, p.PhotoID)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		w.commenterMiss[key] = w.Now()
		return "", err
	}
	delete(w.commenterMiss, key)
	return rel, nil
}

// savePhoto downloads loc to dst through a temporary file.
func savePhoto(ctx context.Context, api *tg.Client, loc tg.InputFileLocationClass, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = tgdown.NewDownloader().Download(api, loc).Stream(ctx, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// RefreshComments refreshes an archived post (its counters and new comments) when its comments
// are opened, at most once every 5 minutes per post. store.ErrNotFound when rootID is not an
// archived post of the channel conversation chatID.
func (w *Watcher) RefreshComments(ctx context.Context, chatID, rootID int64) error {
	channel, posts, err := w.st.ChatWatchPosts(ctx, chatID, []int64{rootID})
	if err != nil {
		return err
	}
	if len(posts) == 0 {
		return store.ErrNotFound
	}
	if w.floodLeft() > 0 {
		return nil // the comments kept are shown; refreshed on an opening after the wait
	}
	now := w.Now()
	w.mu.Lock()
	if at, ok := w.commentsOpened[rootID]; ok && now.Sub(at) < openCommentsEvery {
		w.mu.Unlock()
		return nil
	}
	w.commentsOpened[rootID] = now
	w.mu.Unlock()
	if !w.api.WaitReady(ctx, 0) {
		return ErrNotReady
	}
	return w.with(ctx, func(api *tg.Client) error {
		ch, err := w.channel(ctx, api, channel)
		if err != nil {
			return err
		}
		return w.refreshPosts(ctx, api, ch, posts, true)
	})
}

// BackfillComments keeps, once, the comments of every post archived before comments were kept
// (posts still being refreshed get theirs from the refresh as well). It waits for the account,
// rides out flood waits and transient failures, skips channels it cannot read, and marks itself
// done only after a complete pass.
func (w *Watcher) BackfillComments(ctx context.Context) {
	if v, err := w.st.GetSetting(ctx, CommentsBackfillKey); err == nil && string(v) == "done" {
		return
	}
	for ctx.Err() == nil {
		if !w.api.WaitReady(ctx, 0) {
			sleep(ctx, time.Minute)
			continue
		}
		if left := w.floodLeft(); left > 0 && !sleep(ctx, left) {
			return
		}
		err := w.backfillComments(ctx)
		if err == nil {
			if err := w.st.PutSetting(ctx, CommentsBackfillKey, []byte("done"), w.Now().Unix()); err != nil {
				log.Printf("watch: comment backfill: %v", err)
			}
			log.Printf("watch: comment backfill done")
			return
		}
		if d, ok := tgerr.AsFloodWait(err); ok {
			sleep(ctx, d+time.Second)
			continue
		}
		if ctx.Err() == nil {
			log.Printf("watch: comment backfill: %v (retrying)", err)
		}
		sleep(ctx, time.Minute)
	}
}

// backfillComments runs one pass over every watched channel's archived posts, skipping posts
// whose comments are already up to date.
func (w *Watcher) backfillComments(ctx context.Context) error {
	watches, err := w.st.ListWatches(ctx)
	if err != nil {
		return err
	}
	for _, wv := range watches {
		posts, err := w.st.ArchivedWatchPosts(ctx, wv.ChannelID)
		if err != nil {
			return err
		}
		if len(posts) == 0 {
			continue
		}
		err = w.with(ctx, func(api *tg.Client) error {
			ch, err := w.channel(ctx, api, wv.ChannelID)
			if err != nil {
				return err
			}
			return w.backfillChannel(ctx, api, ch, posts)
		})
		if _, flood := tgerr.AsFloodWait(err); err != nil && (flood || watchTransient(err)) {
			return err
		}
		if err != nil {
			log.Printf("watch: comment backfill of channel %d: %v", wv.ChannelID, err)
		}
	}
	return nil
}

// backfillChannel reads a channel's archived posts a page at a time and stores the comments of
// each, updating only the comment fields of their stats (the counters stay as last refreshed).
func (w *Watcher) backfillChannel(ctx context.Context, api *tg.Client, ch *tg.Channel, posts []store.WatchPost) error {
	groups, order := groupPosts(posts)
	in := &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	names := mtproto.NamesFrom(nil, nil)
	for i := 0; i < len(order); {
		// A page of whole albums.
		var ids []int
		j := i
		for ; j < len(order) && (len(ids) == 0 || len(ids)+len(groups[order[j]]) <= historyPage); j++ {
			for _, p := range groups[order[j]] {
				ids = append(ids, int(p.TgMessageID))
			}
		}
		got, err := getMessages(ctx, api, in, names, ids...)
		if err != nil {
			return err
		}
		for _, key := range order[i:j] {
			g := groups[key]
			var msgs []*tg.Message
			for _, p := range g {
				if m := got[int(p.TgMessageID)]; m != nil {
					msgs = append(msgs, m)
				}
			}
			if len(msgs) == 0 {
				continue
			}
			// Paced: a first run reads every archived post's comments.
			if !sleep(ctx, backfillPause) {
				return ctx.Err()
			}
			info, err := w.syncComments(ctx, api, ch, g[0].MessageID, msgs, false)
			if err != nil {
				return err
			}
			if info == nil {
				continue
			}
			replies := countersOf(msgs).Replies
			if err := w.setComments(ctx, g, info, replies); err != nil {
				return err
			}
		}
		i = j
		if !sleep(ctx, time.Second) {
			return ctx.Err()
		}
	}
	return nil
}

// groupPosts splits archived posts into albums (single posts alone), in order; each album's
// first post is its head.
func groupPosts(posts []store.WatchPost) (map[string][]store.WatchPost, []string) {
	groups := map[string][]store.WatchPost{}
	var order []string
	for _, p := range posts {
		key := p.GroupID
		if key == "" {
			key = fmt.Sprintf("#%d", p.TgMessageID)
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], p)
	}
	return groups, order
}

// setComments writes an album's comment info and count into its stats, leaving the rest as is,
// and publishes message.updated where they changed.
func (w *Watcher) setComments(ctx context.Context, posts []store.WatchPost, info *CommentsInfo, replies int) error {
	for _, p := range posts {
		var ps PostStats
		if json.Unmarshal([]byte(p.Stats), &ps) != nil {
			continue
		}
		before, _ := json.Marshal(ps)
		ps.Comments, ps.Replies = info, replies
		after, err := json.Marshal(ps)
		if err != nil {
			return err
		}
		if string(before) == string(after) {
			continue
		}
		ok, err := w.st.SetPostStats(ctx, p.MessageID, p.Stats, string(after))
		if err != nil {
			return err
		}
		if ok {
			w.hub.Publish(events.Event{Type: "message.updated", Data: map[string]int64{"chat_id": p.ChatID, "message_id": p.MessageID}})
		}
	}
	return nil
}
