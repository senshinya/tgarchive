package userbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tgdown "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/convert/mtproto"
	"tgarchive/internal/events"
	"tgarchive/internal/linkparse"
	"tgarchive/internal/model"
	"tgarchive/internal/notify"
	"tgarchive/internal/store"
	"tgarchive/internal/watchcond"
)

const (
	PollSettingKey     = "watch_poll_seconds"
	DefaultPollSeconds = 60
	MinPollSeconds     = 30
	MaxPollSeconds     = 600

	historyPage   = 100
	historyPages  = 5
	backfillPages = 10 // a starting watch takes in at most this many pages of recent posts
	testPosts     = 5
)

var (
	errBadCond       = errors.New("条件无效")
	errNotBroadcast  = errors.New("不是频道（只支持广播频道）")
	errInviteLink    = errors.New("私有频道的邀请链接无法直接监听，请先用代取账号加入该频道")
	errBadChannelRef = errors.New("无法识别，请输入 @用户名 或 t.me 链接")
	errUnknownPeer   = errors.New("频道信息已过期，请重新搜索选择")
)

// ChannelInfo is a channel as the picker lists it.
type ChannelInfo struct {
	ChannelID    int64  `json:"channel_id"`
	Title        string `json:"title"`
	Username     string `json:"username"`
	Participants int    `json:"participants"`
}

// ReactionStat is one reaction counter of an archived or previewed post.
type ReactionStat struct {
	Key      string `json:"key"`
	Emoji    string `json:"emoji,omitempty"`
	CustomID string `json:"custom_id,omitempty"`
	MediaID  int64  `json:"media_id,omitempty"`
	Mime     string `json:"mime,omitempty"` // the custom emoji sticker's type (tgs / webm / webp)
	Count    int    `json:"count"`
}

// PostStats is stored as messages.stats_json and returned by the condition test.
type PostStats struct {
	Reactions []ReactionStat `json:"reactions"`
	Total     int            `json:"total"`
	Views     int            `json:"views"`
	Forwards  int            `json:"forwards"`
	Replies   int            `json:"replies"`
	Hit       *HitInfo       `json:"hit,omitempty"`
	// RefreshedAt is when the counters were last re-read after the hit (0: not since).
	RefreshedAt int64 `json:"refreshed_at,omitempty"`
	// Comments is set when the post takes comments (an archived post's; not in condition tests).
	Comments *CommentsInfo `json:"comments,omitempty"`
}

type HitInfo struct {
	At      int64    `json:"at"`
	Reasons []string `json:"reasons"`
}

// TestPost is one recent post judged against a draft condition.
type TestPost struct {
	TgMessageID int64     `json:"tg_message_id"`
	Date        int64     `json:"date"`
	Kind        string    `json:"kind"`
	Text        string    `json:"text"`
	Stats       PostStats `json:"stats"`
	Hit         bool      `json:"hit"`
	Reasons     []string  `json:"reasons"`
}

// AvailableReactions lists what can be reacted with in a channel; All means any emoji.
type AvailableReactions struct {
	All  bool           `json:"all"`
	List []ReactionStat `json:"list"`
}

type TestResult struct {
	Posts     []TestPost         `json:"posts"`
	Reactions AvailableReactions `json:"reactions_available"`
}

type recent struct {
	at    time.Time
	msgs  []*tg.Message
	avail AvailableReactions
	ch    *tg.Channel
	names mtproto.Names
}

// Watcher polls watched channels through the userbot account and archives the posts that meet
// their conditions within their window (spec 2026-10-06-channel-watch §2).
type Watcher struct {
	api       API
	st        *store.Store
	hub       *events.Hub
	notifier  notify.Notifier
	wakeDL    func()
	avatarDir string
	wake      chan struct{}

	Now        func() time.Time
	DialogsTTL time.Duration
	RecentTTL  time.Duration
	Absent     *AbsentChannels // channels a dialogs scan did not find; shared with the Fetcher

	floodUntil atomic.Int64 // unix nanoseconds until which the last FLOOD_WAIT runs (see with)
	mu         sync.Mutex
	dialogsAt  time.Time
	dialogs    []ChannelInfo // last complete scan; nil before the first
	partial    []ChannelInfo // the running scan's results so far
	scanning   bool
	scanErr    string
	recent     map[int64]*recent
	allReacts  []ReactionStat
	photoMiss  map[int64]time.Time // channels whose photo could not be fetched recently
	photoSlots chan struct{}
	backfills  map[int64]*BackfillState // latest manual backfill per watch
	opened     map[int64]time.Time      // when a conversation's posts were last refreshed on open

	// MediaDir is where media files live, for removing the ones a re-read comment no longer uses.
	MediaDir       string
	commentsOpened map[int64]time.Time  // when a post's comments were last refreshed on open
	commenterMiss  map[string]time.Time // commenters whose photo could not be fetched recently
}

func NewWatcher(api API, st *store.Store, hub *events.Hub, n notify.Notifier, wakeDL func(), avatarDir string) *Watcher {
	return &Watcher{api: api, st: st, hub: hub, notifier: n, wakeDL: wakeDL, avatarDir: avatarDir, wake: make(chan struct{}, 1),
		Now: time.Now, DialogsTTL: 30 * time.Minute, RecentTTL: time.Minute, Absent: NewAbsentChannels(),
		recent: map[int64]*recent{}, photoMiss: map[int64]time.Time{}, photoSlots: make(chan struct{}, 2), backfills: map[int64]*BackfillState{},
		opened: map[int64]time.Time{}, commentsOpened: map[int64]time.Time{}, commenterMiss: map[string]time.Time{}}
}

// Wake makes Run poll now (a watch was added or changed).
func (w *Watcher) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// PollInterval reads the configured interval, clamped to its range.
func (w *Watcher) PollInterval(ctx context.Context) time.Duration {
	secs := DefaultPollSeconds
	if v, err := w.st.GetSetting(ctx, PollSettingKey); err == nil {
		if n, err := strconv.Atoi(string(v)); err == nil {
			secs = min(max(n, MinPollSeconds), MaxPollSeconds)
		}
	}
	return time.Duration(secs) * time.Second
}

func (w *Watcher) Run(ctx context.Context) {
	for ctx.Err() == nil {
		w.PollOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-time.After(w.PollInterval(ctx)):
		}
	}
}

// PollOnce runs one round over every enabled watch; nothing happens while the account is not ready
// or a FLOOD_WAIT runs.
func (w *Watcher) PollOnce(ctx context.Context) {
	if !w.api.WaitReady(ctx, 0) || w.floodLeft() > 0 {
		return
	}
	watches, err := w.st.ListWatches(ctx)
	if err != nil {
		log.Printf("watch: list: %v", err)
		return
	}
	for _, wv := range watches {
		if ctx.Err() != nil {
			return
		}
		if !wv.Enabled {
			continue
		}
		if stop := w.pollWatch(ctx, wv); stop {
			return
		}
	}
}

// pollWatch polls one watch and records the outcome; stop ends the round (flood wait, account gone).
func (w *Watcher) pollWatch(ctx context.Context, wv store.WatchView) (stop bool) {
	cond, err := watchcond.Parse([]byte(wv.Cond))
	if err != nil {
		w.setStatus(ctx, wv, store.WatchError, errBadCond.Error())
		return false
	}
	changed := false
	err = w.with(ctx, func(api *tg.Client) error {
		var err error
		changed, err = w.poll(ctx, api, wv, cond)
		return err
	})
	if d, ok := tgerr.AsFloodWait(err); ok {
		// Rounds are skipped until the wait is over (with): calling again within it only makes
		// Telegram extend it.
		log.Printf("watch %d: flood wait %s", wv.ID, d)
		return true
	}
	if err != nil && watchTransient(err) {
		if ctx.Err() == nil {
			log.Printf("watch %d: %v", wv.ID, err)
		}
		return errors.Is(err, ErrNotReady)
	}
	if err != nil {
		w.setStatus(ctx, wv, store.WatchError, watchReason(err))
	} else {
		w.setStatus(ctx, wv, store.WatchOK, "")
	}
	if changed {
		w.publish(wv.ID)
	}
	return false
}

// with runs fn through the account, recording any FLOOD_WAIT it answers: until that wait is over,
// polling and the other background work leave Telegram alone (floodLeft). The wait is taken as
// the account's, whichever call got it.
func (w *Watcher) with(ctx context.Context, fn func(api *tg.Client) error) error {
	err := w.api.With(ctx, fn)
	if d, ok := tgerr.AsFloodWait(err); ok {
		until := w.Now().Add(d + time.Second).UnixNano()
		for {
			cur := w.floodUntil.Load()
			if cur >= until {
				break
			}
			if w.floodUntil.CompareAndSwap(cur, until) {
				log.Printf("watch: flood wait %s, background calls paused until %s", d, time.Unix(0, until).Format(time.TimeOnly))
				break
			}
		}
	}
	return err
}

// floodLeft is how long the last recorded FLOOD_WAIT still runs; 0 when it is over.
func (w *Watcher) floodLeft() time.Duration {
	return max(time.Duration(w.floodUntil.Load()-w.Now().UnixNano()), 0)
}

// floodErr stands for a FLOOD_WAIT that still runs for d, refusing a call before it is made.
func floodErr(d time.Duration) error {
	return tgerr.New(420, fmt.Sprintf("FLOOD_WAIT_%d", int(math.Ceil(d.Seconds()))))
}

// watchTransient reports errors a later round may not see: the connection going away, or
// Telegram's own server-side failures (5xx, negative codes such as -503 Timeout). Only answers
// about the channel itself put a watch into error.
func watchTransient(err error) bool {
	if e, ok := tgerr.As(err); ok && (e.Code >= 500 || e.Code < 0) {
		return true
	}
	return transient(err)
}

func watchReason(err error) string {
	switch {
	case errors.Is(err, errNotMember), tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID", "CHANNEL_PUBLIC_GROUP_NA"):
		return "无法访问该频道（私有频道需代取账号已加入）"
	case errors.Is(err, errNoChat), tgerr.Is(err, "USERNAME_NOT_OCCUPIED", "USERNAME_INVALID"):
		return "频道不存在"
	}
	return reason(err)
}

func (w *Watcher) setStatus(ctx context.Context, wv store.WatchView, status, msg string) {
	changed, err := w.st.SetWatchStatus(ctx, wv.ID, status, msg, w.Now().Unix())
	if err != nil {
		log.Printf("watch %d: save status: %v", wv.ID, err)
		return
	}
	if !changed {
		return
	}
	w.publish(wv.ID)
	if status == store.WatchError && wv.Status != store.WatchError && w.notifier != nil {
		w.notifier.Notify(ctx, "tgarchive 频道监听出错", fmt.Sprintf("%s：%s", channelName(wv.Channel), msg))
	}
}

func channelName(c store.Channel) string {
	if c.Title != "" {
		return c.Title
	}
	if c.Username != "" {
		return "@" + c.Username
	}
	return strconv.FormatInt(c.ChannelID, 10)
}

func (w *Watcher) publish(watchID int64) {
	w.hub.Publish(events.Event{Type: "watch.updated", Data: map[string]int64{"watch_id": watchID}})
}

// poll fetches new posts into the pending set and judges every pending post.
func (w *Watcher) poll(ctx context.Context, api *tg.Client, wv store.WatchView, cond *watchcond.Node) (changed bool, err error) {
	ch, err := w.channel(ctx, api, wv.ChannelID)
	if err != nil {
		return false, err
	}
	in := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	if ch.Title != wv.Channel.Title || ch.Username != wv.Channel.Username {
		if err := w.st.UpsertChannel(ctx, store.Channel{ChannelID: ch.ID, Title: ch.Title, Username: ch.Username}, w.Now().Unix()); err != nil {
			return false, err
		}
		changed = true
	}
	if wv.LastSeenID == 0 {
		// Starting (created or re-enabled): posts published within the window are taken in
		// as if they had been seen as they came, so each is watched until its own deadline.
		if err := w.start(ctx, api, in, wv); err != nil {
			return true, err
		}
		_, err := w.judge(ctx, api, wv, ch, cond)
		if err == nil {
			err = w.refreshRecent(ctx, api, ch)
		}
		return true, err
	}
	posts, err := newPosts(ctx, api, in, int(max(wv.LastSeenID, 0)))
	if err != nil {
		return changed, err
	}
	if len(posts) > 0 {
		window := int64(wv.WindowMinutes) * 60
		now := w.Now().Unix()
		// Polling only every so often, a post can be first seen just after a short window closed;
		// those still get their one look. Only posts missed by more than a round (the account was
		// offline) are skipped.
		grace := int64((w.PollInterval(ctx) + 2*time.Minute) / time.Second)
		pend := make([]store.Pending, 0, len(posts))
		top := wv.LastSeenID
		for _, m := range posts {
			top = max(top, int64(m.ID))
			// A post first seen after its window (the account was offline) was never observed:
			// it is skipped rather than judged on counts gathered long after.
			if int64(m.Date)+window+grace <= now {
				continue
			}
			g, _ := m.GetGroupedID()
			pend = append(pend, store.Pending{TgMessageID: int64(m.ID), GroupedID: g, Date: int64(m.Date), Deadline: int64(m.Date) + window})
		}
		if err := w.st.AddPending(ctx, wv.ID, pend, top); err != nil {
			return changed, err
		}
		changed = true
	}
	judged, err := w.judge(ctx, api, wv, ch, cond)
	if err == nil {
		err = w.refreshRecent(ctx, api, ch)
	}
	return changed || judged, err
}

func latestID(ctx context.Context, api *tg.Client, peer tg.InputPeerClass) (int64, error) {
	res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, Limit: 1})
	if err != nil {
		return 0, err
	}
	list, _, _, err := messagesOf(res)
	if err != nil {
		return 0, err
	}
	var top int64
	for _, m := range list {
		top = max(top, int64(m.GetID()))
	}
	return top, nil
}

// start takes in the posts published within the window and records the newest post id as the
// watch's starting point (WatchStartEmpty for a channel without posts).
func (w *Watcher) start(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, wv store.WatchView) error {
	window := int64(wv.WindowMinutes) * 60
	since := w.Now().Unix() - window
	var top int64
	var pend []store.Pending
	offset := 0
	for page := 0; page < backfillPages; page++ {
		res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: offset, Limit: historyPage})
		if err != nil {
			return err
		}
		list, _, _, err := messagesOf(res)
		if err != nil {
			return err
		}
		older := false
		for _, mc := range list {
			id := mc.GetID()
			top = max(top, int64(id))
			if offset == 0 || id < offset {
				offset = id
			}
			m, ok := mc.(*tg.Message)
			if !ok {
				continue
			}
			if int64(m.Date) < since {
				older = true
				continue
			}
			g, _ := m.GetGroupedID()
			pend = append(pend, store.Pending{TgMessageID: int64(m.ID), GroupedID: g, Date: int64(m.Date), Deadline: int64(m.Date) + window})
		}
		if older || len(list) < historyPage {
			break
		}
	}
	if top == 0 {
		return w.st.SetWatchStart(ctx, wv.ID, store.WatchStartEmpty)
	}
	if err := w.st.SetWatchStart(ctx, wv.ID, top); err != nil {
		return err
	}
	return w.st.AddPending(ctx, wv.ID, pend, top)
}

// newPosts returns the channel's posts above minID (service messages skipped), oldest first.
func newPosts(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, minID int) ([]*tg.Message, error) {
	var out []*tg.Message
	offset := 0
	for page := 0; page < historyPages; page++ {
		res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, MinID: minID, OffsetID: offset, Limit: historyPage})
		if err != nil {
			return nil, err
		}
		list, _, _, err := messagesOf(res)
		if err != nil {
			return nil, err
		}
		low := 0
		for _, mc := range list {
			id := mc.GetID()
			if low == 0 || id < low {
				low = id
			}
			if m, ok := mc.(*tg.Message); ok && id > minID {
				out = append(out, m)
			}
		}
		if len(list) < historyPage || low <= minID+1 {
			break
		}
		offset = low
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// judge evaluates every pending post (albums as one), archiving hits and dropping posts that
// expired or were deleted. A post past its deadline still gets this last look.
func (w *Watcher) judge(ctx context.Context, api *tg.Client, wv store.WatchView, ch *tg.Channel, cond *watchcond.Node) (bool, error) {
	pend, err := w.st.ListPending(ctx, wv.ID)
	if err != nil || len(pend) == 0 {
		return false, err
	}
	in := &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	names := mtproto.NamesFrom(nil, nil)
	got := map[int]*tg.Message{}
	for i := 0; i < len(pend); i += historyPage {
		var ids []int
		for _, p := range pend[i:min(i+historyPage, len(pend))] {
			ids = append(ids, int(p.TgMessageID))
		}
		part, err := getMessages(ctx, api, in, names, ids...)
		if err != nil {
			return false, err
		}
		for id, m := range part {
			got[id] = m
		}
	}
	type group struct {
		ids      []int64
		msgs     []*tg.Message
		deadline int64
	}
	groups := map[int64]*group{}
	var order []int64
	for _, p := range pend {
		key := -p.TgMessageID
		if p.GroupedID != 0 {
			key = p.GroupedID
		}
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
			order = append(order, key)
		}
		g.ids = append(g.ids, p.TgMessageID)
		g.deadline = max(g.deadline, p.Deadline)
		if m := got[int(p.TgMessageID)]; m != nil {
			g.msgs = append(g.msgs, m)
		}
	}
	now := w.Now().Unix()
	changed := false
	for _, key := range order {
		g := groups[key]
		done := len(g.msgs) == 0 || g.deadline <= now
		if len(g.msgs) > 0 {
			convs, err := convertAll(g.msgs, ch, names)
			if err != nil {
				return changed, err
			}
			st := statsOf(g.msgs, convs)
			if cond.Eval(st) {
				_, err := w.archive(ctx, api, wv, ch, g.msgs, convs, cond.Explain(st), now, true)
				if errors.Is(err, store.ErrNoWatch) {
					return changed, nil // deleted while this poll ran
				}
				if err != nil {
					return changed, err
				}
				done = true
			}
		}
		if done {
			if err := w.st.DeletePending(ctx, wv.ID, g.ids); err != nil {
				return changed, err
			}
			changed = true
		}
	}
	return changed, nil
}

func convertAll(msgs []*tg.Message, ch *tg.Channel, names mtproto.Names) ([]*model.Message, error) {
	out := make([]*model.Message, 0, len(msgs))
	for _, m := range msgs {
		c, err := mtproto.Convert(m, mtproto.ChannelFrom(ch), names)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// reactionKey is how a reaction is keyed in conditions and stats.
func reactionKey(r tg.ReactionClass) (key, emoji, custom string) {
	switch v := r.(type) {
	case *tg.ReactionEmoji:
		k := watchcond.NormKey(v.Emoticon)
		return k, k, ""
	case *tg.ReactionCustomEmoji:
		id := strconv.FormatInt(v.DocumentID, 10)
		return watchcond.CustomKeyPrefix + id, "", id
	case *tg.ReactionPaid:
		return watchcond.KeyPaid, "⭐", ""
	}
	return "", "", ""
}

// countersOf measures an album (or single post): counters at their maximum over its messages.
func countersOf(msgs []*tg.Message) watchcond.Stats {
	st := watchcond.Stats{Reactions: map[string]int{}, Kinds: map[string]bool{}}
	for _, m := range msgs {
		st.Views = max(st.Views, m.Views)
		st.Forwards = max(st.Forwards, m.Forwards)
		if r, ok := m.GetReplies(); ok {
			st.Replies = max(st.Replies, r.Replies)
		}
		total := 0
		for _, rc := range m.Reactions.Results {
			k, _, _ := reactionKey(rc.Reaction)
			if k == "" {
				continue
			}
			st.Reactions[k] = max(st.Reactions[k], rc.Count)
			total += rc.Count
		}
		st.Total = max(st.Total, total)
	}
	return st
}

// statsOf measures an album (or single post) for its condition: counters, text and media kinds.
func statsOf(msgs []*tg.Message, convs []*model.Message) watchcond.Stats {
	st := countersOf(msgs)
	var texts []string
	hasMedia := false
	for i, m := range msgs {
		if m.Message != "" {
			texts = append(texts, m.Message)
		}
		switch convs[i].Kind {
		case model.KindPhoto:
			st.Kinds["photo"], hasMedia = true, true
		case model.KindVideo, model.KindAnimation, model.KindVideoNote:
			st.Kinds["video"], hasMedia = true, true
		case model.KindDocument, model.KindAudio, model.KindVoice:
			st.Kinds["file"], hasMedia = true, true
		case model.KindText:
		default:
			hasMedia = true
		}
	}
	if !hasMedia {
		st.Kinds["text"] = true
	}
	st.Text = strings.Join(texts, "\n")
	return st
}

// postStats renders an album's counters for storage and display, custom emoji resolved to media.
func (w *Watcher) postStats(ctx context.Context, api *tg.Client, msgs []*tg.Message, st watchcond.Stats) PostStats {
	ps := PostStats{Reactions: []ReactionStat{}, Total: st.Total, Views: st.Views, Forwards: st.Forwards, Replies: st.Replies}
	seen := map[string]bool{}
	var customs []int64
	for _, m := range msgs {
		for _, rc := range m.Reactions.Results {
			k, emoji, custom := reactionKey(rc.Reaction)
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			ps.Reactions = append(ps.Reactions, ReactionStat{Key: k, Emoji: emoji, CustomID: custom, Count: st.Reactions[k]})
			if custom != "" {
				id, _ := strconv.ParseInt(custom, 10, 64)
				customs = append(customs, id)
			}
		}
	}
	// Kept in Telegram's order (paid first, then by count), which clients show as is.
	if len(customs) > 0 {
		media := w.customEmoji(ctx, api, customs)
		for i := range ps.Reactions {
			if r := &ps.Reactions[i]; r.CustomID != "" {
				id, _ := strconv.ParseInt(r.CustomID, 10, 64)
				r.MediaID, r.Mime = media[id].MediaID, media[id].Mime
			}
		}
	}
	return ps
}

// customEmoji maps custom emoji ids to media rows, registering unknown ones (best effort).
func (w *Watcher) customEmoji(ctx context.Context, api *tg.Client, ids []int64) map[int64]store.EmojiMedia {
	known, err := w.st.CustomEmojiMedia(ctx, ids)
	if err != nil {
		log.Printf("watch: custom emoji lookup: %v", err)
		return map[int64]store.EmojiMedia{}
	}
	var missing []int64
	for _, id := range ids {
		if _, ok := known[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return known
	}
	docs, err := api.MessagesGetCustomEmojiDocuments(ctx, missing)
	if err != nil {
		log.Printf("watch: custom emoji documents: %v", err)
		return known
	}
	added := false
	for _, dc := range docs {
		d, ok := dc.(*tg.Document)
		if !ok {
			continue
		}
		md, err := mtproto.CustomEmojiMedia(d)
		if err != nil {
			continue
		}
		id, err := w.st.RegisterCustomEmoji(ctx, d.ID, md)
		if err != nil {
			log.Printf("watch: register custom emoji %d: %v", d.ID, err)
			continue
		}
		known[d.ID] = store.EmojiMedia{MediaID: id, Mime: md.Mime}
		added = true
	}
	if added && w.wakeDL != nil {
		w.wakeDL()
	}
	return known
}

// archive stores an album (or single post) that met the condition; created reports whether it
// was new (a post archived before is only refreshed: it keeps its first hit, whose time schedules
// the later refreshes, and its comments summary). polled says polling found it, which counted
// it as scanned; a backfill hit does not count towards the hit rate.
func (w *Watcher) archive(ctx context.Context, api *tg.Client, wv store.WatchView, ch *tg.Channel, msgs []*tg.Message,
	convs []*model.Message, reasons []string, now int64, polled bool) (created bool, err error) {
	ps := w.postStats(ctx, api, msgs, statsOf(msgs, convs))
	ps.Hit = &HitInfo{At: now, Reasons: reasons}
	ids := make([]int64, len(msgs))
	for i, m := range msgs {
		ids[i] = int64(m.ID)
	}
	prev, err := w.st.WatchPostsByTgID(ctx, ch.ID, ids)
	if err != nil {
		return false, err
	}
	for _, p := range prev {
		var old PostStats
		if json.Unmarshal([]byte(p.Stats), &old) == nil && old.Hit != nil {
			ps.Hit, ps.Comments, ps.RefreshedAt = old.Hit, old.Comments, now
			break
		}
	}
	b, err := json.Marshal(ps)
	if err != nil {
		return false, err
	}
	var posts []store.WatchPost
	for _, c := range convs {
		c.Source = model.SourceChannelWatch
		ir, err := w.st.Ingest(ctx, store.IngestInput{ChannelID: ch.ID, Msg: c, Stats: string(b), Now: now})
		if err != nil {
			return created, err
		}
		posts = append(posts, store.WatchPost{MessageID: ir.MessageID, ChatID: ir.ChatID, TgMessageID: c.TgMessageID, Stats: string(b)})
		typ := "message.updated"
		if ir.Created {
			typ, created = "message.created", true
		}
		w.hub.Publish(events.Event{Type: typ, Data: map[string]int64{"chat_id": ir.ChatID, "message_id": ir.MessageID}})
	}
	w.archiveComments(ctx, api, ch, msgs, posts)
	// A retry after a partial failure re-archives the same post: count it once.
	if created {
		if err := w.st.AddWatchHit(ctx, wv.ID, polled); err != nil {
			return created, err
		}
	}
	if w.wakeDL != nil {
		w.wakeDL()
	}
	return created, nil
}

// archiveComments keeps the comments a just archived post already has. The post stays archived
// whatever happens here: a failure only leaves its comments to the next refresh.
func (w *Watcher) archiveComments(ctx context.Context, api *tg.Client, ch *tg.Channel, msgs []*tg.Message, posts []store.WatchPost) {
	if len(posts) == 0 {
		return
	}
	sort.Slice(posts, func(i, j int) bool { return posts[i].MessageID < posts[j].MessageID })
	info, err := w.syncComments(ctx, api, ch, posts[0].MessageID, msgs, false)
	if err == nil && info != nil {
		err = w.setComments(ctx, posts, info, countersOf(msgs).Replies)
	}
	if err != nil && ctx.Err() == nil {
		log.Printf("watch: comments of channel %d post %d: %v", ch.ID, posts[0].TgMessageID, err)
	}
}

// channel resolves a stored channel id to a full channel (with access hash): from the peer
// cache, else by its username, else by a dialogs scan, unless a recent scan already missed it
// (errNotMember then, see AbsentChannels). A cached hash Telegram rejects is evicted and resolved
// again once.
func (w *Watcher) channel(ctx context.Context, api *tg.Client, id int64) (*tg.Channel, error) {
	ch, err := w.lookup(ctx, api, id)
	if err != nil {
		return nil, err
	}
	full, err := getChannel(ctx, api, ch)
	if err != nil && stalePeerErr(err) {
		if derr := w.st.DeletePeer(ctx, id); derr != nil && !errors.Is(derr, store.ErrNotFound) {
			log.Printf("watch: invalidate stale peer %d: %v", id, derr)
		}
		if ch, err = w.lookup(ctx, api, id); err != nil {
			return nil, err
		}
		full, err = getChannel(ctx, api, ch)
	}
	return full, err
}

func (w *Watcher) lookup(ctx context.Context, api *tg.Client, id int64) (*tg.Channel, error) {
	if p, err := w.st.GetPeer(ctx, id); err == nil {
		return &tg.Channel{ID: p.ChannelID, AccessHash: p.AccessHash, Title: p.Title, Username: p.Username}, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	link := linkparse.Link{ChannelID: id}
	if c, err := w.st.GetChannel(ctx, id); err == nil && c.Username != "" {
		link = linkparse.Link{Username: c.Username}
	}
	if link.Username == "" && w.Absent.has(id, w.Now()) {
		return nil, errNotMember
	}
	ch, _, err := resolveChannel(ctx, api, w.st, w.Now, w.Absent, link)
	if err == nil && ch.ID != id {
		return nil, errNoChat // the username now belongs to another channel
	}
	return ch, err
}

// getChannel re-reads a channel (title, username and photo may have changed).
func getChannel(ctx context.Context, api *tg.Client, ch *tg.Channel) (*tg.Channel, error) {
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}})
	if err != nil {
		return nil, err
	}
	for _, c := range res.GetChats() {
		if v, ok := c.(*tg.Channel); ok && v.ID == ch.ID {
			if v.AccessHash == 0 {
				v.AccessHash = ch.AccessHash
			}
			return v, nil
		}
	}
	return nil, errNoChat
}

// InitialLastSeen returns the newest post id of a channel; creating a watch calls it to check the
// channel can be read.
func (w *Watcher) InitialLastSeen(ctx context.Context, channelID int64) (int64, error) {
	var top int64
	err := w.with(ctx, func(api *tg.Client) error {
		ch, err := w.channel(ctx, api, channelID)
		if err != nil {
			return err
		}
		top, err = latestID(ctx, api, &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash})
		return err
	})
	return top, err
}

func infoOf(c *tg.Channel) ChannelInfo {
	n, _ := c.GetParticipantsCount()
	return ChannelInfo{ChannelID: c.ID, Title: c.Title, Username: c.Username, Participants: n}
}

// ChannelList is the account's joined broadcast channels as far as the background scan got.
type ChannelList struct {
	Channels  []ChannelInfo `json:"channels"`
	Loading   bool          `json:"loading"`    // a scan is running; Channels may be partial
	UpdatedAt int64         `json:"updated_at"` // when the last complete scan finished; 0 for never
	Error     string        `json:"error"`      // why the last scan stopped early
}

// Channels returns the joined broadcast channels known so far and starts a background scan of
// the account's dialogs when the list is older than DialogsTTL (or refresh is set). Telegram
// throttles messages.getDialogs hard, so the scan pages slowly, waits out every FLOOD_WAIT and
// resumes where it stopped; the picker polls while Loading.
func (w *Watcher) Channels(ctx context.Context, refresh bool) (ChannelList, error) {
	if !w.api.WaitReady(ctx, 0) {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.dialogs == nil {
			return ChannelList{}, ErrNotReady
		}
		return w.channelList(), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	stale := w.dialogs == nil || w.Now().Sub(w.dialogsAt) >= w.DialogsTTL
	if (refresh || stale) && !w.scanning {
		if left := w.floodLeft(); left > 0 {
			w.scanErr = reason(floodErr(left))
		} else {
			w.scanning, w.scanErr = true, ""
			go w.scanDialogs()
		}
	}
	return w.channelList(), nil
}

// channelList snapshots the scan state; w.mu must be held.
func (w *Watcher) channelList() ChannelList {
	list := w.dialogs
	if w.scanning && w.partial != nil {
		list = w.partial
	}
	out := ChannelList{Channels: append([]ChannelInfo{}, list...), Loading: w.scanning, Error: w.scanErr}
	if !w.dialogsAt.IsZero() {
		out.UpdatedAt = w.dialogsAt.Unix()
	}
	return out
}

const (
	dialogsPage   = 100
	maxDialogWait = 5 * time.Minute
)

func (w *Watcher) scanDialogs() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	found, err := w.collectDialogs(ctx)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.scanning, w.partial = false, nil
	if err != nil {
		log.Printf("watch: dialogs scan: %v", err)
		w.scanErr = watchReason(err)
		return
	}
	w.dialogs, w.dialogsAt = found, w.Now()
}

// collectDialogs pages through messages.getDialogs, publishing partial results as it goes.
func (w *Watcher) collectDialogs(ctx context.Context) ([]ChannelInfo, error) {
	out := []ChannelInfo{}
	seen := map[int64]bool{}
	req := &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: dialogsPage}
	for page := 0; ; page++ {
		var res tg.MessagesDialogsClass
		err := w.with(ctx, func(api *tg.Client) error {
			var err error
			res, err = api.MessagesGetDialogs(ctx, req)
			return err
		})
		if d, ok := tgerr.AsFloodWait(err); ok && d <= maxDialogWait {
			log.Printf("watch: dialogs page %d: flood wait %s", page, d)
			if !sleep(ctx, d+time.Second) {
				return nil, ctx.Err()
			}
			page--
			continue
		}
		if err != nil {
			return nil, err
		}
		var dlgs []tg.DialogClass
		var msgs []tg.MessageClass
		var chats []tg.ChatClass
		var users []tg.UserClass
		last := true
		switch r := res.(type) {
		case *tg.MessagesDialogs:
			dlgs, msgs, chats, users = r.Dialogs, r.Messages, r.Chats, r.Users
		case *tg.MessagesDialogsSlice:
			dlgs, msgs, chats, users = r.Dialogs, r.Messages, r.Chats, r.Users
			last = len(r.Dialogs) < dialogsPage
		default:
			return out, nil
		}
		channels := map[int64]*tg.Channel{}
		for _, c := range chats {
			if ch, ok := c.(*tg.Channel); ok {
				channels[ch.ID] = ch
			}
		}
		var keep []*tg.Channel
		var ids []int64
		for _, dc := range dlgs {
			d, ok := dc.(*tg.Dialog)
			if !ok {
				continue
			}
			pc, ok := d.Peer.(*tg.PeerChannel)
			if !ok {
				continue
			}
			c := channels[pc.ChannelID]
			if c == nil || c.Min || !c.Broadcast || c.Left || seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			keep = append(keep, c)
			ids = append(ids, c.ID)
			out = append(out, infoOf(c))
		}
		savePeers(ctx, w.st, w.Now, keep...)
		w.Absent.remove(ids...)
		w.mu.Lock()
		w.partial = append([]ChannelInfo{}, out...)
		w.mu.Unlock()
		if last || len(dlgs) == 0 {
			return out, nil
		}
		next, ok := dialogOffset(dlgs[len(dlgs)-1], msgs, channels, users)
		if !ok {
			return out, nil
		}
		next.Limit = dialogsPage
		req = next
	}
}

// dialogOffset builds the request for the page after the dialog d (Telegram pages dialogs by
// the date and id of each dialog's top message, and its peer).
func dialogOffset(dc tg.DialogClass, msgs []tg.MessageClass, channels map[int64]*tg.Channel, users []tg.UserClass) (*tg.MessagesGetDialogsRequest, bool) {
	d, ok := dc.(*tg.Dialog)
	if !ok {
		return nil, false
	}
	req := &tg.MessagesGetDialogsRequest{OffsetID: d.TopMessage}
	for _, mc := range msgs {
		if mc.GetID() != d.TopMessage {
			continue
		}
		var peer tg.PeerClass
		var date int
		switch m := mc.(type) {
		case *tg.Message:
			peer, date = m.PeerID, m.Date
		case *tg.MessageService:
			peer, date = m.PeerID, m.Date
		default:
			continue
		}
		if samePeer(peer, d.Peer) {
			req.OffsetDate = date
			break
		}
	}
	switch p := d.Peer.(type) {
	case *tg.PeerUser:
		var hash int64
		for _, uc := range users {
			if u, ok := uc.(*tg.User); ok && u.ID == p.UserID {
				hash = u.AccessHash
			}
		}
		req.OffsetPeer = &tg.InputPeerUser{UserID: p.UserID, AccessHash: hash}
	case *tg.PeerChat:
		req.OffsetPeer = &tg.InputPeerChat{ChatID: p.ChatID}
	case *tg.PeerChannel:
		c := channels[p.ChannelID]
		if c == nil {
			return nil, false
		}
		req.OffsetPeer = &tg.InputPeerChannel{ChannelID: c.ID, AccessHash: c.AccessHash}
	default:
		return nil, false
	}
	return req, true
}

func samePeer(a, b tg.PeerClass) bool {
	switch x := a.(type) {
	case *tg.PeerUser:
		y, ok := b.(*tg.PeerUser)
		return ok && x.UserID == y.UserID
	case *tg.PeerChat:
		y, ok := b.(*tg.PeerChat)
		return ok && x.ChatID == y.ChatID
	case *tg.PeerChannel:
		y, ok := b.(*tg.PeerChannel)
		return ok && x.ChannelID == y.ChannelID
	}
	return false
}

// Search finds public broadcast channels by name.
func (w *Watcher) Search(ctx context.Context, q string) ([]ChannelInfo, error) {
	out := []ChannelInfo{}
	err := w.with(ctx, func(api *tg.Client) error {
		res, err := api.ContactsSearch(ctx, &tg.ContactsSearchRequest{Q: q, Limit: 20})
		if err != nil {
			return err
		}
		var found []*tg.Channel
		for _, cc := range res.Chats {
			if c, ok := cc.(*tg.Channel); ok && c.Broadcast && !c.Min && c.Username != "" {
				found = append(found, c)
				out = append(out, infoOf(c))
			}
		}
		savePeers(ctx, w.st, w.Now, found...)
		return nil
	})
	if err != nil {
		return nil, userErr(err)
	}
	return out, nil
}

// userErr turns a Telegram error into a user-facing one, keeping ErrNotReady recognisable.
func userErr(err error) error {
	if errors.Is(err, ErrNotReady) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return errors.New(watchReason(err))
}

var (
	bareName    = regexp.MustCompile(`^@?([A-Za-z][A-Za-z0-9_]{3,31})$`)
	channelLink = regexp.MustCompile(`^(?:https?://)?(?:t\.me|telegram\.me)/(?:s/)?([A-Za-z][A-Za-z0-9_]{3,31})/?(?:\?.*)?$`)
	inviteLink  = regexp.MustCompile(`^(?:https?://)?(?:t\.me|telegram\.me)/(?:\+|joinchat/)`)
)

// Resolve turns what the user typed (@name, name, t.me/name, a post link) into a channel.
func (w *Watcher) Resolve(ctx context.Context, input string) (*ChannelInfo, error) {
	input = strings.TrimSpace(input)
	var link linkparse.Link
	switch {
	case inviteLink.MatchString(input):
		return nil, errInviteLink
	case bareName.MatchString(input):
		link.Username = bareName.FindStringSubmatch(input)[1]
	case channelLink.MatchString(input):
		link.Username = channelLink.FindStringSubmatch(input)[1]
	default:
		l, err := linkparse.Parse(input)
		if err != nil {
			return nil, errBadChannelRef
		}
		link = l
	}
	var info *ChannelInfo
	err := w.with(ctx, func(api *tg.Client) error {
		ch, _, err := resolveChannel(ctx, api, w.st, w.Now, w.Absent, link)
		if err != nil {
			return err
		}
		if !ch.Broadcast {
			if full, err := getChannel(ctx, api, ch); err == nil {
				ch = full
			}
		}
		if !ch.Broadcast {
			return errNotBroadcast
		}
		i := infoOf(ch)
		info = &i
		return nil
	})
	if err != nil {
		if errors.Is(err, errNotBroadcast) || errors.Is(err, ErrNotReady) {
			return nil, err
		}
		if errors.Is(err, errNotChannel) {
			return nil, errNotBroadcast
		}
		return nil, errors.New(watchReason(err))
	}
	return info, nil
}

// Known returns a channel the picker has seen (it is in the peer cache), for creating a watch.
func (w *Watcher) Known(ctx context.Context, id int64) (*store.Channel, error) {
	p, err := w.st.GetPeer(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		if c, err := w.st.GetChannel(ctx, id); err == nil {
			return c, nil
		}
		return nil, errUnknownPeer
	}
	if err != nil {
		return nil, err
	}
	return &store.Channel{ChannelID: id, Title: p.Title, Username: p.Username}, nil
}

// Test judges a channel's latest posts against a draft condition (nil: no condition yet).
func (w *Watcher) Test(ctx context.Context, channelID int64, cond *watchcond.Node) (*TestResult, error) {
	res := &TestResult{Posts: []TestPost{}}
	err := w.with(ctx, func(api *tg.Client) error {
		r, err := w.recentPosts(ctx, api, channelID)
		if err != nil {
			return err
		}
		res.Reactions = r.avail
		// Albums are judged as one post: group the newest messages, newest group first.
		var groups [][]*tg.Message
		for _, m := range r.msgs {
			g, ok := m.GetGroupedID()
			if n := len(groups); ok && n > 0 {
				if pg, _ := groups[n-1][0].GetGroupedID(); pg == g {
					groups[n-1] = append(groups[n-1], m)
					continue
				}
			}
			groups = append(groups, []*tg.Message{m})
		}
		for _, g := range groups {
			if len(res.Posts) == testPosts {
				break
			}
			sort.Slice(g, func(i, j int) bool { return g[i].ID < g[j].ID })
			convs, err := convertAll(g, r.ch, r.names)
			if err != nil {
				return err
			}
			st := statsOf(g, convs)
			tp := TestPost{TgMessageID: int64(g[0].ID), Date: int64(g[0].Date), Kind: string(convs[0].Kind),
				Text: truncateRunes(st.Text, 200), Stats: w.postStats(ctx, api, g, st), Reasons: []string{}}
			if cond != nil && cond.Eval(st) {
				tp.Hit, tp.Reasons = true, cond.Explain(st)
			}
			res.Posts = append(res.Posts, tp)
		}
		return nil
	})
	if err != nil && !errors.Is(err, ErrNotReady) {
		return nil, errors.New(watchReason(err))
	}
	return res, err
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// recentPosts returns a channel's newest messages and its allowed reactions (cached RecentTTL).
func (w *Watcher) recentPosts(ctx context.Context, api *tg.Client, id int64) (*recent, error) {
	w.mu.Lock()
	r := w.recent[id]
	w.mu.Unlock()
	if r != nil && w.Now().Sub(r.at) < w.RecentTTL {
		return r, nil
	}
	ch, err := w.channel(ctx, api, id)
	if err != nil {
		return nil, err
	}
	res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer: &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, Limit: 20})
	if err != nil {
		return nil, err
	}
	list, users, chats, err := messagesOf(res)
	if err != nil {
		return nil, err
	}
	r = &recent{at: w.Now(), ch: ch, names: mtproto.NamesFrom(users, chats)}
	for _, mc := range list {
		if m, ok := mc.(*tg.Message); ok {
			r.msgs = append(r.msgs, m)
		}
	}
	if r.avail, err = w.available(ctx, api, ch); err != nil {
		return nil, err
	}
	w.mu.Lock()
	w.recent[id] = r
	w.mu.Unlock()
	return r, nil
}

func (w *Watcher) available(ctx context.Context, api *tg.Client, ch *tg.Channel) (AvailableReactions, error) {
	out := AvailableReactions{List: []ReactionStat{}}
	full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash})
	if err != nil {
		return out, err
	}
	cf, ok := full.FullChat.(*tg.ChannelFull)
	if !ok {
		return out, nil
	}
	ar, _ := cf.GetAvailableReactions()
	switch v := ar.(type) {
	case *tg.ChatReactionsAll:
		out.All = true
		out.List = w.allReactions(ctx, api)
	case *tg.ChatReactionsSome:
		var customs []int64
		for _, r := range v.Reactions {
			k, emoji, custom := reactionKey(r)
			if k == "" {
				continue
			}
			out.List = append(out.List, ReactionStat{Key: k, Emoji: emoji, CustomID: custom})
			if custom != "" {
				id, _ := strconv.ParseInt(custom, 10, 64)
				customs = append(customs, id)
			}
		}
		if len(customs) > 0 {
			media := w.customEmoji(ctx, api, customs)
			for i := range out.List {
				if r := &out.List[i]; r.CustomID != "" {
					id, _ := strconv.ParseInt(r.CustomID, 10, 64)
					r.MediaID, r.Mime = media[id].MediaID, media[id].Mime
				}
			}
		}
	}
	return out, nil
}

// allReactions is Telegram's standard emoji reaction set (fetched once).
func (w *Watcher) allReactions(ctx context.Context, api *tg.Client) []ReactionStat {
	w.mu.Lock()
	cached := w.allReacts
	w.mu.Unlock()
	if cached != nil {
		return cached
	}
	res, err := api.MessagesGetAvailableReactions(ctx, 0)
	if err != nil {
		log.Printf("watch: available reactions: %v", err)
		return []ReactionStat{}
	}
	list := []ReactionStat{}
	if v, ok := res.(*tg.MessagesAvailableReactions); ok {
		for _, r := range v.Reactions {
			if !r.Inactive {
				k := watchcond.NormKey(r.Reaction)
				list = append(list, ReactionStat{Key: k, Emoji: k})
			}
		}
	}
	w.mu.Lock()
	w.allReacts = list
	w.mu.Unlock()
	return list
}

// ChannelPhoto stores a channel's small profile photo at avatars/channels/<id>.jpg (removing a
// stale one when the channel has no photo) and records it on the channel when it is stored
// (picker-only channels just get the file).
// Failures are remembered for ten minutes so a missing photo is not refetched on every request.
func (w *Watcher) ChannelPhoto(ctx context.Context, id int64) (string, error) {
	return w.channelPhoto(ctx, id, false)
}

// channelPhoto is ChannelPhoto; force refetches a photo already on disk (daily refresh).
func (w *Watcher) channelPhoto(ctx context.Context, id int64, force bool) (string, error) {
	w.mu.Lock()
	if t, ok := w.photoMiss[id]; ok && w.Now().Sub(t) < 10*time.Minute {
		w.mu.Unlock()
		return "", store.ErrNotFound
	}
	w.mu.Unlock()
	// At most two photo fetches at a time: a picker full of channels must not flood the account
	// the poller and the fetcher share.
	select {
	case w.photoSlots <- struct{}{}:
		defer func() { <-w.photoSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	rel := filepath.Join("channels", fmt.Sprintf("%d.jpg", id))
	dst := filepath.Join(w.avatarDir, rel)
	if _, err := os.Stat(dst); err == nil && !force {
		// Already on disk (from the picker, or a request that waited in front of this one).
		if serr := w.st.SetChannelAvatar(ctx, id, rel); serr != nil && !errors.Is(serr, store.ErrNotFound) {
			log.Printf("watch: save channel %d avatar: %v", id, serr)
		}
		return rel, nil
	}
	has := false
	err := w.with(ctx, func(api *tg.Client) error {
		ch, err := w.channel(ctx, api, id)
		if err != nil {
			return err
		}
		if _, err := w.st.RefreshChannelInfo(ctx, store.Channel{ChannelID: ch.ID, Title: ch.Title, Username: ch.Username}, w.Now().Unix()); err != nil {
			return err
		}
		photo, ok := ch.Photo.(*tg.ChatPhoto)
		if !ok {
			os.Remove(dst)
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		tmp := dst + ".part"
		f, err := os.Create(tmp)
		if err != nil {
			return err
		}
		loc := &tg.InputPeerPhotoFileLocation{Peer: &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, PhotoID: photo.PhotoID}
		_, err = tgdown.NewDownloader().Download(api, loc).Stream(ctx, f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Rename(tmp, dst)
		}
		if err != nil {
			os.Remove(tmp)
			return err
		}
		has = true
		return nil
	})
	if err == nil {
		path := ""
		if has {
			path = rel
		}
		if serr := w.st.SetChannelAvatar(ctx, id, path); serr != nil && !errors.Is(serr, store.ErrNotFound) {
			log.Printf("watch: save channel %d avatar: %v", id, serr)
		}
	}
	if err != nil || !has {
		w.mu.Lock()
		w.photoMiss[id] = w.Now()
		w.mu.Unlock()
		if err == nil {
			err = store.ErrNotFound
		}
		return "", err
	}
	w.mu.Lock()
	delete(w.photoMiss, id)
	w.mu.Unlock()
	return rel, nil
}

// RefreshChannels re-reads every stored channel's info and photo (daily maintenance).
func (w *Watcher) RefreshChannels(ctx context.Context) {
	ids, err := w.st.ChannelIDs(ctx)
	if err != nil {
		log.Printf("watch: list channels: %v", err)
		return
	}
	for _, id := range ids {
		if left := w.floodLeft(); left > 0 && !sleep(ctx, left) {
			return
		}
		w.mu.Lock()
		delete(w.photoMiss, id)
		w.mu.Unlock()
		if _, err := w.channelPhoto(ctx, id, true); err != nil && !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			log.Printf("watch: refresh channel %d: %v", id, err)
		}
	}
}
