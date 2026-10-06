package userbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgdown "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/query/dialogs"
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

	historyPage  = 100
	historyPages = 5
	testPosts    = 5
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
	MaxFlood   time.Duration // longest FLOOD_WAIT waited out
	DialogsTTL time.Duration
	RecentTTL  time.Duration

	mu         sync.Mutex
	dialogsAt  time.Time
	dialogs    []ChannelInfo
	recent     map[int64]*recent
	allReacts  []ReactionStat
	photoMiss  map[int64]time.Time // channels whose photo could not be fetched recently
	photoSlots chan struct{}
}

func NewWatcher(api API, st *store.Store, hub *events.Hub, n notify.Notifier, wakeDL func(), avatarDir string) *Watcher {
	return &Watcher{api: api, st: st, hub: hub, notifier: n, wakeDL: wakeDL, avatarDir: avatarDir, wake: make(chan struct{}, 1),
		Now: time.Now, MaxFlood: 300 * time.Second, DialogsTTL: 5 * time.Minute, RecentTTL: time.Minute,
		recent: map[int64]*recent{}, photoMiss: map[int64]time.Time{}, photoSlots: make(chan struct{}, 2)}
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

// PollOnce runs one round over every enabled watch; nothing happens while the account is not ready.
func (w *Watcher) PollOnce(ctx context.Context) {
	if !w.api.WaitReady(ctx, 0) {
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
	err = w.api.With(ctx, func(api *tg.Client) error {
		var err error
		changed, err = w.poll(ctx, api, wv, cond)
		return err
	})
	if d, ok := tgerr.AsFloodWait(err); ok {
		log.Printf("watch %d: flood wait %s", wv.ID, d)
		sleep(ctx, min(d, w.MaxFlood))
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
		// A watch only observes posts published after it was created (or re-enabled).
		top, err := latestID(ctx, api, in)
		if err != nil {
			return changed, err
		}
		if top == 0 {
			top = store.WatchStartEmpty
		}
		return true, w.st.SetWatchStart(ctx, wv.ID, top)
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
				err := w.archive(ctx, api, wv, ch, g.msgs, convs, cond.Explain(st), now)
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

// statsOf measures an album (or single post): counters at their maximum over its messages.
func statsOf(msgs []*tg.Message, convs []*model.Message) watchcond.Stats {
	st := watchcond.Stats{Reactions: map[string]int{}, Kinds: map[string]bool{}}
	var texts []string
	hasMedia := false
	for i, m := range msgs {
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
	sort.SliceStable(ps.Reactions, func(i, j int) bool { return ps.Reactions[i].Count > ps.Reactions[j].Count })
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

func (w *Watcher) archive(ctx context.Context, api *tg.Client, wv store.WatchView, ch *tg.Channel, msgs []*tg.Message,
	convs []*model.Message, reasons []string, now int64) error {
	ps := w.postStats(ctx, api, msgs, statsOf(msgs, convs))
	ps.Hit = &HitInfo{At: now, Reasons: reasons}
	b, err := json.Marshal(ps)
	if err != nil {
		return err
	}
	created := false
	for _, c := range convs {
		c.Source = model.SourceChannelWatch
		ir, err := w.st.Ingest(ctx, store.IngestInput{ChannelID: ch.ID, Msg: c, Stats: string(b), Now: now})
		if err != nil {
			return err
		}
		typ := "message.updated"
		if ir.Created {
			typ, created = "message.created", true
		}
		w.hub.Publish(events.Event{Type: typ, Data: map[string]int64{"chat_id": ir.ChatID, "message_id": ir.MessageID}})
	}
	// A retry after a partial failure re-archives the same post: count it once.
	if created {
		if err := w.st.AddWatchHit(ctx, wv.ID); err != nil {
			return err
		}
	}
	if w.wakeDL != nil {
		w.wakeDL()
	}
	return nil
}

// channel resolves a stored channel id to a full channel (with access hash): from the peer
// cache, else by its username, else by a dialogs scan. A cached hash Telegram rejects is evicted
// and resolved again once.
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
	ch, _, err := resolveChannel(ctx, api, w.st, w.Now, link)
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

// InitialLastSeen returns the newest post id of a channel, where a new watch starts observing.
func (w *Watcher) InitialLastSeen(ctx context.Context, channelID int64) (int64, error) {
	var top int64
	err := w.api.With(ctx, func(api *tg.Client) error {
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

// Channels lists the broadcast channels the account has joined (cached for DialogsTTL).
func (w *Watcher) Channels(ctx context.Context, refresh bool) ([]ChannelInfo, error) {
	w.mu.Lock()
	if !refresh && w.dialogs != nil && w.Now().Sub(w.dialogsAt) < w.DialogsTTL {
		out := w.dialogs
		w.mu.Unlock()
		return out, nil
	}
	w.mu.Unlock()
	var out []ChannelInfo
	err := w.api.With(ctx, func(api *tg.Client) error {
		var seen []*tg.Channel
		out = []ChannelInfo{}
		err := dialogs.NewQueryBuilder(api).GetDialogs().BatchSize(100).ForEach(ctx, func(_ context.Context, e dialogs.Elem) error {
			d, ok := e.Dialog.(*tg.Dialog)
			if !ok {
				return nil
			}
			pc, ok := d.Peer.(*tg.PeerChannel)
			if !ok {
				return nil
			}
			c, ok := e.Entities.Channels()[pc.ChannelID]
			if !ok || c.Min || !c.Broadcast || c.Left {
				return nil
			}
			seen = append(seen, c)
			out = append(out, infoOf(c))
			return nil
		})
		savePeers(ctx, w.st, w.Now, seen...)
		return err
	})
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	w.dialogs, w.dialogsAt = out, w.Now()
	w.mu.Unlock()
	return out, nil
}

// Search finds public broadcast channels by name.
func (w *Watcher) Search(ctx context.Context, q string) ([]ChannelInfo, error) {
	out := []ChannelInfo{}
	err := w.api.With(ctx, func(api *tg.Client) error {
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
	return out, err
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
	err := w.api.With(ctx, func(api *tg.Client) error {
		ch, _, err := resolveChannel(ctx, api, w.st, w.Now, link)
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
	err := w.api.With(ctx, func(api *tg.Client) error {
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
	err := w.api.With(ctx, func(api *tg.Client) error {
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
		w.mu.Lock()
		delete(w.photoMiss, id)
		w.mu.Unlock()
		if _, err := w.channelPhoto(ctx, id, true); err != nil && !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			log.Printf("watch: refresh channel %d: %v", id, err)
		}
	}
}
