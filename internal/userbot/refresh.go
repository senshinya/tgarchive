package userbot

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/convert/mtproto"
	"tgarchive/internal/events"
	"tgarchive/internal/store"
)

const (
	// refreshHorizon bounds which archived posts the poll looks at: past the day 7 checkpoint
	// (with a day's slack for an account that was offline then), a post is no longer refreshed.
	refreshHorizon = 8 * 24 * 3600
	// openRefreshEvery is how often opening a conversation may refresh its posts.
	openRefreshEvery = 5 * time.Minute
)

// refreshDue reports whether an archived post hit at hitAt and last refreshed at refreshed
// (0: never) is due for a refresh. Reactions grow fastest right after a post, so the gaps widen:
// every 10 minutes for the first 2 hours, every 30 minutes until 6 hours, every 2 hours until a
// day, then once on day 3 and once on day 7.
func refreshDue(hitAt, refreshed, now int64) bool {
	const m, h, d = 60, 3600, 86400
	last := max(refreshed, hitAt)
	age := now - hitAt
	switch {
	case age >= 7*d:
		return last < hitAt+7*d
	case age >= 3*d:
		return last < hitAt+3*d
	case age >= d:
		return false
	case age >= 6*h:
		return now-last >= 2*h
	case age >= 2*h:
		return now-last >= 30*m
	}
	return now-last >= 10*m
}

// refreshRecent refreshes the channel's archived posts that are due. Only a flood wait or a
// transient failure is returned (the poll handles those); anything else is logged, so a refresh
// never puts the watch into error.
func (w *Watcher) refreshRecent(ctx context.Context, api *tg.Client, ch *tg.Channel) error {
	posts, err := w.st.RecentWatchPosts(ctx, ch.ID, w.Now().Unix()-refreshHorizon)
	if err == nil {
		err = w.refreshPosts(ctx, api, ch, posts, false)
	}
	if err == nil {
		return nil
	}
	if _, ok := tgerr.AsFloodWait(err); ok || watchTransient(err) {
		return err
	}
	log.Printf("watch: refresh channel %d: %v", ch.ID, err)
	return nil
}

// RefreshPosts refreshes the counters of the archived watch posts among ids (a conversation being
// opened), at most once every 5 minutes per conversation. store.ErrNotFound when chatID is not a
// channel conversation.
func (w *Watcher) RefreshPosts(ctx context.Context, chatID int64, ids []int64) error {
	channel, posts, err := w.st.ChatWatchPosts(ctx, chatID, ids)
	if err != nil || len(posts) == 0 {
		return err
	}
	if w.floodLeft() > 0 {
		return nil // the counters kept are shown; refreshed on an opening after the wait
	}
	now := w.Now()
	w.mu.Lock()
	if at, ok := w.opened[chatID]; ok && now.Sub(at) < openRefreshEvery {
		w.mu.Unlock()
		return nil
	}
	w.opened[chatID] = now
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

// refreshPosts re-reads archived posts from the channel and stores their current counters and new
// comments, keeping the hit, and publishes message.updated for those whose counters changed. Albums are
// refreshed whole; unless force, only the ones refreshDue says are due. A post gone from the
// channel keeps what was archived.
func (w *Watcher) refreshPosts(ctx context.Context, api *tg.Client, ch *tg.Channel, posts []store.WatchPost, force bool) error {
	now := w.Now().Unix()
	type group struct {
		posts []store.WatchPost
		old   PostStats
		due   bool
	}
	groups := map[string]*group{}
	var order []string
	for _, p := range posts {
		var ps PostStats
		if json.Unmarshal([]byte(p.Stats), &ps) != nil || ps.Hit == nil {
			continue
		}
		key := p.GroupID
		if key == "" {
			key = "#" + strconv.FormatInt(p.TgMessageID, 10)
		}
		g := groups[key]
		if g == nil {
			g = &group{old: ps}
			groups[key] = g
			order = append(order, key)
		}
		g.posts = append(g.posts, p)
		g.due = g.due || force || refreshDue(ps.Hit.At, ps.RefreshedAt, now)
	}
	var ids []int
	for _, key := range order {
		if g := groups[key]; g.due {
			for _, p := range g.posts {
				ids = append(ids, int(p.TgMessageID))
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	in := &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	names := mtproto.NamesFrom(nil, nil)
	got := map[int]*tg.Message{}
	for i := 0; i < len(ids); i += historyPage {
		part, err := getMessages(ctx, api, in, names, ids[i:min(i+historyPage, len(ids))]...)
		if err != nil {
			return err
		}
		for id, m := range part {
			got[id] = m
		}
	}
	for _, key := range order {
		g := groups[key]
		if !g.due {
			continue
		}
		var msgs []*tg.Message
		for _, p := range g.posts {
			if m := got[int(p.TgMessageID)]; m != nil {
				msgs = append(msgs, m)
			}
		}
		ps, changed := g.old, false
		if len(msgs) > 0 {
			ps = w.postStats(ctx, api, msgs, countersOf(msgs))
			ps.Hit = g.old.Hit
			full := !force && now-g.old.Hit.At >= commentsFullAge
			info, err := w.syncComments(ctx, api, ch, g.posts[0].MessageID, msgs, full)
			if err != nil {
				return err
			}
			ps.Comments = info
			changed = !sameCounters(g.old, ps)
		}
		ps.RefreshedAt = now
		b, err := json.Marshal(ps)
		if err != nil {
			return err
		}
		for _, p := range g.posts {
			ok, err := w.st.SetPostStats(ctx, p.MessageID, p.Stats, string(b))
			if err != nil {
				return err
			}
			if ok && changed {
				w.hub.Publish(events.Event{Type: "message.updated", Data: map[string]int64{"chat_id": p.ChatID, "message_id": p.MessageID}})
			}
		}
	}
	return nil
}

// sameCounters reports whether two stats show the same numbers (refresh times aside).
func sameCounters(a, b PostStats) bool {
	a.RefreshedAt, b.RefreshedAt = 0, 0
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
