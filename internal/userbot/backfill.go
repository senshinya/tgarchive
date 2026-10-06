package userbot

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/convert/mtproto"
	"tgarchive/internal/store"
	"tgarchive/internal/watchcond"
)

const (
	MaxBackfillHours = 720
	backfillMaxPosts = 5000
	backfillTimeout  = 30 * time.Minute
)

var errBackfillBusy = errors.New("该监听正在回溯，请等它完成")

// BackfillState is the progress of a watch's manual backfill (the latest one, running or not).
type BackfillState struct {
	Running    bool   `json:"running"`
	Hours      int    `json:"hours"`
	Scanned    int    `json:"scanned"`  // posts looked at so far
	Archived   int    `json:"archived"` // newly archived posts (an album counts once)
	Error      string `json:"error"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
}

// BackfillState returns the latest backfill of a watch, nil when none ran since the start.
func (w *Watcher) BackfillState(watchID int64) *BackfillState {
	w.mu.Lock()
	defer w.mu.Unlock()
	if b := w.backfills[watchID]; b != nil {
		cp := *b
		return &cp
	}
	return nil
}

// Backfill starts judging, by their current counts, every post the watch's channel published in
// the last hours, archiving those that meet the condition. It runs in the background.
func (w *Watcher) Backfill(ctx context.Context, watchID int64, hours int) (*BackfillState, error) {
	wv, err := w.st.GetWatch(ctx, watchID)
	if err != nil {
		return nil, err
	}
	cond, err := watchcond.Parse([]byte(wv.Cond))
	if err != nil {
		return nil, errBadCond
	}
	if !w.api.WaitReady(ctx, 0) {
		return nil, ErrNotReady
	}
	w.mu.Lock()
	if b := w.backfills[watchID]; b != nil && b.Running {
		w.mu.Unlock()
		return nil, errBackfillBusy
	}
	st := &BackfillState{Running: true, Hours: hours, StartedAt: w.Now().Unix()}
	w.backfills[watchID] = st
	cp := *st
	w.mu.Unlock()
	w.publish(watchID)
	go w.runBackfill(*wv, cond, hours)
	return &cp, nil
}

func (w *Watcher) updateBackfill(id int64, fn func(*BackfillState)) {
	w.mu.Lock()
	if b := w.backfills[id]; b != nil {
		fn(b)
	}
	w.mu.Unlock()
	w.publish(id)
}

func (w *Watcher) runBackfill(wv store.WatchView, cond *watchcond.Node, hours int) {
	ctx, cancel := context.WithTimeout(context.Background(), backfillTimeout)
	defer cancel()
	err := w.backfill(ctx, wv, cond, hours)
	if err != nil {
		log.Printf("watch %d: backfill: %v", wv.ID, err)
	}
	w.updateBackfill(wv.ID, func(b *BackfillState) {
		b.Running, b.FinishedAt = false, w.Now().Unix()
		if errors.Is(err, store.ErrNoWatch) {
			err = nil
		}
		if err != nil {
			b.Error = watchReason(err)
		}
	})
}

// withFlood runs fn through the account, waiting out FLOOD_WAITs of up to five minutes.
func (w *Watcher) withFlood(ctx context.Context, fn func(api *tg.Client) error) error {
	for {
		err := w.api.With(ctx, fn)
		d, ok := tgerr.AsFloodWait(err)
		if !ok || d > maxDialogWait {
			return err
		}
		if !sleep(ctx, d+time.Second) {
			return ctx.Err()
		}
	}
}

func (w *Watcher) backfill(ctx context.Context, wv store.WatchView, cond *watchcond.Node, hours int) error {
	since := w.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	var ch *tg.Channel
	if err := w.withFlood(ctx, func(api *tg.Client) error {
		var err error
		ch, err = w.channel(ctx, api, wv.ChannelID)
		return err
	}); err != nil {
		return err
	}
	peer := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	names := mtproto.NamesFrom(nil, nil)
	var posts []*tg.Message
	offset := 0
	for len(posts) < backfillMaxPosts {
		var list []tg.MessageClass
		if err := w.withFlood(ctx, func(api *tg.Client) error {
			res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: offset, Limit: historyPage})
			if err != nil {
				return err
			}
			var users []tg.UserClass
			var chats []tg.ChatClass
			list, users, chats, err = messagesOf(res)
			names.Add(users, chats)
			return err
		}); err != nil {
			return err
		}
		older := false
		for _, mc := range list {
			if offset == 0 || mc.GetID() < offset {
				offset = mc.GetID()
			}
			m, ok := mc.(*tg.Message)
			if !ok {
				continue
			}
			if int64(m.Date) < since {
				older = true
				continue
			}
			posts = append(posts, m)
		}
		n := len(posts)
		w.updateBackfill(wv.ID, func(b *BackfillState) { b.Scanned = n })
		if older || len(list) < historyPage {
			break
		}
	}
	// Albums are judged and archived as one, oldest first.
	sort.Slice(posts, func(i, j int) bool { return posts[i].ID < posts[j].ID })
	var groups [][]*tg.Message
	at := map[int64]int{}
	for _, m := range posts {
		if g, ok := m.GetGroupedID(); ok {
			if i, seen := at[g]; seen {
				groups[i] = append(groups[i], m)
				continue
			}
			at[g] = len(groups)
		}
		groups = append(groups, []*tg.Message{m})
	}
	for _, g := range groups {
		convs, err := convertAll(g, ch, names)
		if err != nil {
			return err
		}
		st := statsOf(g, convs)
		if !cond.Eval(st) {
			continue
		}
		var created bool
		if err := w.withFlood(ctx, func(api *tg.Client) error {
			var err error
			created, err = w.archive(ctx, api, wv, ch, g, convs, cond.Explain(st), w.Now().Unix())
			return err
		}); err != nil {
			return err
		}
		if created {
			w.updateBackfill(wv.ID, func(b *BackfillState) { b.Archived++ })
		}
	}
	return nil
}
