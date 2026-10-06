package userbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgmock"

	"tgarchive/internal/events"
	"tgarchive/internal/store"
	"tgarchive/internal/watchcond"
)

// watchTG extends channelTG with what the watcher calls.
type watchTG struct {
	*channelTG
	mu       sync.Mutex
	chanErr  string // error type returned by channels.getChannels
	chanCode int    // its code (default 400)

	manyDialogs int // serve this many dialogs, alternating broadcast channels and groups
	dialogFlood int // FLOOD_WAIT_0 answers before serving dialogs
	dialogCalls int
	historyN    int // getHistory calls
	available   tg.ChatReactionsClass
	emojiDocs   int // getCustomEmojiDocuments calls
}

func newWatchTG() *watchTG {
	c := newChannelTG()
	c.ch.Broadcast = true
	w := &watchTG{channelTG: c, available: &tg.ChatReactionsSome{Reactions: []tg.ReactionClass{
		&tg.ReactionEmoji{Emoticon: "🔥"}, &tg.ReactionEmoji{Emoticon: "👍"}, &tg.ReactionCustomEmoji{DocumentID: 77}}}}
	c.extra = w.serve
	return w
}

func (w *watchTG) serve(req bin.Encoder) (bin.Encoder, error) {
	switch r := req.(type) {
	case *tg.ChannelsGetChannelsRequest:
		if w.chanErr != "" {
			return nil, tgerr.New(max(w.chanCode, 400), w.chanErr)
		}
		return &tg.MessagesChats{Chats: []tg.ChatClass{w.ch}}, nil
	case *tg.MessagesGetHistoryRequest:
		w.historyN++
		var ids []int
		for id := range w.posts {
			if id > r.MinID && (r.OffsetID == 0 || id < r.OffsetID) {
				ids = append(ids, id)
			}
		}
		sort.Sort(sort.Reverse(sort.IntSlice(ids)))
		if len(ids) > r.Limit {
			ids = ids[:r.Limit]
		}
		out := &tg.MessagesChannelMessages{Chats: []tg.ChatClass{w.ch}}
		for _, id := range ids {
			out.Messages = append(out.Messages, w.posts[id])
		}
		return out, nil
	case *tg.ChannelsGetFullChannelRequest:
		full := &tg.ChannelFull{ID: 500, ChatPhoto: &tg.PhotoEmpty{}}
		full.SetAvailableReactions(w.available)
		return &tg.MessagesChatFull{FullChat: full, Chats: []tg.ChatClass{w.ch}}, nil
	case *tg.MessagesGetCustomEmojiDocumentsRequest:
		w.emojiDocs++
		var out []tg.DocumentClass
		for _, id := range r.DocumentID {
			out = append(out, &tg.Document{ID: id, AccessHash: 1, FileReference: []byte{1}, MimeType: "application/x-tgsticker", Size: 10,
				Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeCustomEmoji{Alt: "😀", Stickerset: &tg.InputStickerSetEmpty{}}}})
		}
		return &tg.DocumentClassVector{Elems: out}, nil
	case *tg.MessagesGetAvailableReactionsRequest:
		return &tg.MessagesAvailableReactions{Reactions: []tg.AvailableReaction{
			{Reaction: "❤️", StaticIcon: &tg.DocumentEmpty{}, AppearAnimation: &tg.DocumentEmpty{}, SelectAnimation: &tg.DocumentEmpty{},
				ActivateAnimation: &tg.DocumentEmpty{}, EffectAnimation: &tg.DocumentEmpty{}},
		}}, nil
	case *tg.MessagesGetDialogsRequest:
		if w.manyDialogs == 0 {
			break
		}
		w.dialogCalls++
		if w.dialogFlood > 0 {
			w.dialogFlood--
			return nil, tgerr.New(420, "FLOOD_WAIT_0")
		}
		out := &tg.MessagesDialogsSlice{Count: w.manyDialogs}
		for i := 0; i < w.manyDialogs && len(out.Dialogs) < r.Limit; i++ {
			top := 10000 - i
			if r.OffsetID != 0 && top >= r.OffsetID {
				continue
			}
			id := int64(2000 + i)
			ch := &tg.Channel{ID: id, AccessHash: id * 10, Title: fmt.Sprintf("C%d", i), Broadcast: i%2 == 0, Megagroup: i%2 == 1, Photo: &tg.ChatPhotoEmpty{}}
			out.Dialogs = append(out.Dialogs, &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: id}, TopMessage: top, NotifySettings: tg.PeerNotifySettings{}})
			out.Messages = append(out.Messages, &tg.Message{ID: top, PeerID: &tg.PeerChannel{ChannelID: id}, Date: top})
			out.Chats = append(out.Chats, ch)
		}
		return out, nil
	case *tg.UploadGetFileRequest:
		if r.Offset > 0 {
			return &tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: []byte{}}, nil
		}
		return &tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: []byte("jpeg")}, nil
	case *tg.ContactsSearchRequest:
		other := &tg.Channel{ID: 600, AccessHash: 6006, Title: "Group", Username: "grp", Megagroup: true, Photo: &tg.ChatPhotoEmpty{}}
		return &tg.ContactsFound{Chats: []tg.ChatClass{w.ch, other}}, nil
	}
	return w.channelTG.serve(req)
}

// reacted adds a post with reactions (key → count), views and optionally a photo.
func (w *watchTG) reacted(id int, group int64, photo bool, views int, reactions map[string]int) *tg.Message {
	m := w.post(id, "post "+string(rune('a'+id%26)), group)
	m.SetViews(views)
	var res []tg.ReactionCount
	for k, n := range reactions {
		var r tg.ReactionClass = &tg.ReactionEmoji{Emoticon: k}
		if strings.HasPrefix(k, "custom:") {
			r = &tg.ReactionCustomEmoji{DocumentID: 77}
		}
		res = append(res, tg.ReactionCount{Reaction: r, Count: n})
	}
	if len(res) > 0 {
		m.SetReactions(tg.MessageReactions{Results: res})
	}
	if photo {
		m.SetMedia(&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(id), AccessHash: 1, FileReference: []byte{1},
			Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", W: 100, H: 80, Size: 10}}}})
	}
	return m
}

type watchEnv struct {
	w     *Watcher
	st    *store.Store
	tg    *watchTG
	api   *fakeAPI
	n     *countNotifier
	now   time.Time
	ev    []events.Event
	evMu  sync.Mutex
	watch int64
}

const fire10 = `{"op":"and","items":[{"metric":"reaction","key":"🔥","cmp":"gte","value":10}]}`

func newWatchEnv(t *testing.T, cond string) *watchEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &watchEnv{st: st, tg: newWatchTG(), n: &countNotifier{}, now: time.Unix(1700000000, 0)}
	e.api = &fakeAPI{ready: true, client: tg.NewClient(tgmock.Invoker(e.tg.handle))}
	hub := events.NewHub()
	sub, cancel := hub.Subscribe()
	t.Cleanup(cancel)
	go func() {
		for ev := range sub {
			e.evMu.Lock()
			e.ev = append(e.ev, ev)
			e.evMu.Unlock()
		}
	}()
	e.w = NewWatcher(e.api, st, hub, e.n, nil, t.TempDir())
	e.w.Now = func() time.Time { return e.now }
	e.w.MaxFlood = 0
	st.PutPeers(ctx, []store.Peer{{ChannelID: 500, AccessHash: 5005, Title: "Chan", Username: "chan"}}, 1)
	st.UpsertChannel(ctx, store.Channel{ChannelID: 500, Title: "Chan", Username: "chan"}, 1)
	e.watch, err = st.CreateWatch(ctx, &store.Watch{ChannelID: 500, WindowMinutes: 30, Cond: cond, Enabled: true, CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *watchEnv) get(t *testing.T) *store.WatchView {
	t.Helper()
	w, err := e.st.GetWatch(ctx, e.watch)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func (e *watchEnv) archived(t *testing.T) []store.MessageView {
	t.Helper()
	w := e.get(t)
	msgs, err := e.st.ListMessages(ctx, w.ChatID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func tgIDs(ms []store.MessageView) []int64 {
	out := []int64{}
	for _, m := range ms {
		out = append(out, m.TgMessageID)
	}
	return out
}

func TestWatchStartsAfterLatestPost(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.reacted(5, 0, false, 10, map[string]int{"🔥": 99})
	e.w.PollOnce(ctx)
	if w := e.get(t); w.LastSeenID != 5 || w.Pending != 0 {
		t.Fatalf("after init = %+v", w)
	}
	if got := e.archived(t); len(got) != 0 {
		t.Fatalf("old post archived: %v", tgIDs(got))
	}
}

func TestWatchArchivesHitsWithinWindow(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.reacted(5, 0, false, 10, nil)
	e.w.PollOnce(ctx) // init at 5
	e.tg.reacted(6, 0, false, 100, map[string]int{"🔥": 20, "👍": 3})
	e.tg.reacted(7, 0, false, 100, map[string]int{"🔥": 1})
	e.w.PollOnce(ctx)
	got := e.archived(t)
	if !equalIDs(tgIDs(got), []int64{6}) {
		t.Fatalf("archived = %v", tgIDs(got))
	}
	var ps PostStats
	if err := json.Unmarshal(got[0].Stats, &ps); err != nil {
		t.Fatal(err)
	}
	if ps.Views != 100 || ps.Total != 23 || len(ps.Reactions) != 2 || ps.Reactions[0].Key != "🔥" || ps.Hit == nil ||
		len(ps.Hit.Reasons) != 1 || ps.Hit.Reasons[0] != "🔥 20 ≥ 10" || ps.Hit.At != e.now.Unix() {
		t.Fatalf("stats = %+v", ps)
	}
	if got[0].Source != "channel_watch" || got[0].OriginLink != "https://t.me/chan/6" {
		t.Fatalf("message = %+v", got[0])
	}
	if w := e.get(t); w.Pending != 1 || w.Hits != 1 || w.LastSeenID != 7 {
		t.Fatalf("watch = %+v", w)
	}
	// Post 7 catches up later in its window.
	e.tg.reacted(7, 0, false, 100, map[string]int{"🔥": 12})
	e.now = e.now.Add(10 * time.Minute)
	e.w.PollOnce(ctx)
	if got := e.archived(t); !equalIDs(tgIDs(got), []int64{6, 7}) {
		t.Fatalf("archived = %v", tgIDs(got))
	}
	if w := e.get(t); w.Pending != 0 || w.Hits != 2 {
		t.Fatalf("watch = %+v", w)
	}
}

func TestWatchAlbumJudgedAndArchivedTogether(t *testing.T) {
	e := newWatchEnv(t, `{"op":"and","items":[{"metric":"reaction","key":"🔥","cmp":"gte","value":10},
		{"metric":"type","cmp":"is","value":"photo"}]}`)
	e.tg.reacted(5, 0, false, 10, nil)
	e.w.PollOnce(ctx)
	e.tg.reacted(6, 9, true, 50, map[string]int{"🔥": 15})
	e.tg.reacted(7, 9, true, 50, nil)
	e.tg.reacted(8, 0, false, 50, map[string]int{"🔥": 15}) // text only: fails the type condition
	e.w.PollOnce(ctx)
	got := e.archived(t)
	if !equalIDs(tgIDs(got), []int64{6, 7}) || got[0].MediaGroupID != "9" || len(got[1].Media) == 0 {
		t.Fatalf("archived = %v", tgIDs(got))
	}
	if string(got[0].Stats) != string(got[1].Stats) {
		t.Fatal("album members should carry the same stats")
	}
	if w := e.get(t); w.Hits != 1 || w.Pending != 1 {
		t.Fatalf("watch = %+v", w)
	}
}

func TestWatchDropsExpiredAndDeletedPosts(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.reacted(5, 0, false, 10, nil)
	e.w.PollOnce(ctx)
	e.tg.reacted(6, 0, false, 10, map[string]int{"🔥": 1}) // date = 1700000006
	e.tg.reacted(7, 0, false, 10, map[string]int{"🔥": 1})
	e.tg.reacted(8, 0, false, 10, map[string]int{"🔥": 1})
	e.w.PollOnce(ctx)
	if w := e.get(t); w.Pending != 3 {
		t.Fatalf("pending = %d", w.Pending)
	}
	delete(e.tg.posts, 8)
	// Offline past the deadline: post 7 crossed the threshold meanwhile and still gets archived
	// on this last look; post 6 did not and is dropped; deleted post 8 is dropped.
	e.tg.reacted(7, 0, false, 10, map[string]int{"🔥": 11})
	e.now = e.now.Add(2 * time.Hour)
	e.w.PollOnce(ctx)
	if got := e.archived(t); !equalIDs(tgIDs(got), []int64{7}) {
		t.Fatalf("archived = %v", tgIDs(got))
	}
	if w := e.get(t); w.Pending != 0 || w.Status != store.WatchOK {
		t.Fatalf("watch = %+v", w)
	}
}

func TestWatchReenableDoesNotCatchUp(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.reacted(5, 0, false, 10, nil)
	e.w.PollOnce(ctx)
	e.st.UpdateWatch(ctx, e.watch, 30, fire10, false, 2)
	e.tg.reacted(6, 0, false, 10, map[string]int{"🔥": 99}) // posted while disabled
	e.st.UpdateWatch(ctx, e.watch, 30, fire10, true, 3)
	e.w.PollOnce(ctx)
	e.w.PollOnce(ctx)
	if got := e.archived(t); len(got) != 0 {
		t.Fatalf("archived missed posts: %v", tgIDs(got))
	}
	if w := e.get(t); w.LastSeenID != 6 {
		t.Fatalf("watch = %+v", w)
	}
}

func TestWatchSkipsPostsFirstSeenAfterTheirWindow(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.reacted(5, 0, false, 10, nil)
	e.w.PollOnce(ctx)
	e.tg.reacted(6, 0, false, 10, map[string]int{"🔥": 99}) // published while the account was offline
	e.now = e.now.Add(3 * time.Hour)
	e.w.PollOnce(ctx)
	if got := e.archived(t); len(got) != 0 {
		t.Fatalf("archived = %v", tgIDs(got))
	}
	if w := e.get(t); w.LastSeenID != 6 || w.Pending != 0 {
		t.Fatalf("watch = %+v", w)
	}
}

func TestWatchShortWindowStillJudgesPostsSeenLate(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.st.UpdateWatch(ctx, e.watch, 1, fire10, true, 2)
	e.tg.reacted(5, 0, false, 10, nil)
	e.w.PollOnce(ctx)
	e.tg.reacted(6, 0, false, 10, map[string]int{"🔥": 15}) // date = now + 6s
	e.now = e.now.Add(150 * time.Second)                   // seen 84s after its 1-minute window
	e.w.PollOnce(ctx)
	if got := e.archived(t); !equalIDs(tgIDs(got), []int64{6}) {
		t.Fatalf("archived = %v", tgIDs(got))
	}
}

func TestWatchOnEmptyChannelSeesItsFirstPost(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.w.PollOnce(ctx)
	if w := e.get(t); w.LastSeenID != store.WatchStartEmpty {
		t.Fatalf("watch = %+v", w)
	}
	e.tg.reacted(1, 0, false, 10, map[string]int{"🔥": 20})
	e.w.PollOnce(ctx)
	if got := e.archived(t); !equalIDs(tgIDs(got), []int64{1}) {
		t.Fatalf("archived = %v", tgIDs(got))
	}
}

func TestWatchServerErrorsAreTransient(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.failGet = nil
	e.tg.chanErr = "INTERNAL"
	e.tg.chanCode = 500
	e.w.PollOnce(ctx)
	if w := e.get(t); w.Status != store.WatchOK || e.n.count() != 0 {
		t.Fatalf("watch = %+v, notified %d", w, e.n.count())
	}
}

func TestPickerPhotoDoesNotStoreChannel(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.ch.ID, e.tg.ch.Photo = 500, &tg.ChatPhoto{PhotoID: 9}
	e.st.PutPeers(ctx, []store.Peer{{ChannelID: 500, AccessHash: 5005}}, 1)
	e.st.DeleteWatch(ctx, e.watch, true)
	// Channel 500 is still stored (purge keeps channels); a fresh id stays out of the table.
	if ids, _ := e.st.ChannelIDs(ctx); len(ids) != 0 {
		t.Fatalf("ChannelIDs = %v", ids)
	}
	if _, err := e.w.ChannelPhoto(ctx, 500); err != nil {
		t.Fatal(err)
	}
	if ids, _ := e.st.ChannelIDs(ctx); len(ids) != 0 {
		t.Fatalf("picker photo listed the channel for refresh: %v", ids)
	}
}

func TestWatchPagesThroughManyNewPosts(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.reacted(5, 0, false, 10, nil)
	e.w.PollOnce(ctx)
	for id := 6; id < 6+250; id++ {
		e.tg.reacted(id, 0, false, 1, nil)
	}
	e.w.PollOnce(ctx)
	if w := e.get(t); w.Pending != 250 || w.LastSeenID != 255 {
		t.Fatalf("watch = %+v", w)
	}
}

func TestWatchSkipsWhileAccountNotReady(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.api.ready = false
	e.w.PollOnce(ctx)
	if w := e.get(t); w.LastSeenID != 0 || w.Status != store.WatchOK {
		t.Fatalf("watch = %+v", w)
	}
	if e.tg.historyN != 0 {
		t.Fatal("no Telegram calls expected")
	}
}

func TestWatchErrorNotifiesOnceAndRecovers(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.chanErr = "CHANNEL_PRIVATE"
	e.w.PollOnce(ctx)
	e.w.PollOnce(ctx)
	w := e.get(t)
	if w.Status != store.WatchError || !strings.Contains(w.LastError, "无法访问") || e.n.count() != 1 {
		t.Fatalf("watch = %+v, notified %d", w, e.n.count())
	}
	e.tg.chanErr = ""
	e.tg.reacted(5, 0, false, 1, nil)
	e.w.PollOnce(ctx)
	if w := e.get(t); w.Status != store.WatchOK || w.LastError != "" || w.LastSeenID != 5 {
		t.Fatalf("after recovery = %+v", w)
	}
	e.evMu.Lock()
	n := 0
	for _, ev := range e.ev {
		if ev.Type == "watch.updated" {
			n++
		}
	}
	e.evMu.Unlock()
	if n == 0 {
		t.Fatal("expected watch.updated events")
	}
}

func TestWatchBadConditionIsAnError(t *testing.T) {
	e := newWatchEnv(t, `{"op":"and","items":[]}`)
	e.w.PollOnce(ctx)
	if w := e.get(t); w.Status != store.WatchError || w.LastError != "条件无效" {
		t.Fatalf("watch = %+v", w)
	}
}

func TestWatchDisabledIsSkipped(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.st.UpdateWatch(ctx, e.watch, 30, fire10, false, 2)
	e.tg.reacted(5, 0, false, 1, nil)
	e.w.PollOnce(ctx)
	if w := e.get(t); w.LastSeenID != 0 {
		t.Fatalf("disabled watch polled: %+v", w)
	}
}

func TestWatchTestPreview(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.reacted(1, 0, false, 10, map[string]int{"🔥": 50, "custom:77": 2})
	e.tg.reacted(2, 4, true, 10, nil)
	e.tg.reacted(3, 4, true, 10, map[string]int{"👍": 1})
	for id := 4; id <= 9; id++ {
		e.tg.reacted(id, 0, false, 10, nil)
	}
	cond, _ := watchcond.Parse([]byte(fire10))
	res, err := e.w.Test(ctx, 500, cond)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Posts) != 5 || res.Posts[0].TgMessageID != 9 || res.Posts[4].TgMessageID != 5 {
		t.Fatalf("posts = %+v", res.Posts)
	}
	if res.Reactions.All || len(res.Reactions.List) != 3 || res.Reactions.List[2].Key != "custom:77" || res.Reactions.List[2].MediaID == 0 {
		t.Fatalf("available = %+v", res.Reactions)
	}
	// Older posts: the album 2–3 is one entry, and post 1 hits with its custom emoji resolved.
	e.tg.posts = map[int]*tg.Message{}
	e.tg.reacted(1, 0, false, 10, map[string]int{"🔥": 50, "custom:77": 2})
	e.tg.reacted(2, 4, true, 10, nil)
	e.tg.reacted(3, 4, true, 10, map[string]int{"👍": 1})
	e.w.RecentTTL = 0
	res, _ = e.w.Test(ctx, 500, cond)
	if len(res.Posts) != 2 || res.Posts[0].TgMessageID != 2 || res.Posts[0].Kind != "photo" || res.Posts[0].Stats.Total != 1 {
		t.Fatalf("posts = %+v", res.Posts)
	}
	p := res.Posts[1]
	if !p.Hit || p.Reasons[0] != "🔥 50 ≥ 10" || len(p.Stats.Reactions) != 2 || p.Stats.Reactions[1].MediaID == 0 {
		t.Fatalf("post 1 = %+v", p)
	}
	if e.tg.emojiDocs != 1 {
		t.Fatalf("custom emoji fetched %d times", e.tg.emojiDocs)
	}
	// Any-emoji channels offer Telegram's standard set, variation selectors dropped.
	e.tg.available = &tg.ChatReactionsAll{}
	res, _ = e.w.Test(ctx, 500, nil)
	if !res.Reactions.All || len(res.Reactions.List) != 1 || res.Reactions.List[0].Key != "❤" || res.Posts[1].Hit {
		t.Fatalf("all = %+v", res)
	}
}

func TestWatchResolve(t *testing.T) {
	e := newWatchEnv(t, fire10)
	for _, in := range []string{"@chan", "chan", "t.me/chan", "https://t.me/chan/12", "https://t.me/s/chan"} {
		got, err := e.w.Resolve(ctx, in)
		if err != nil || got.ChannelID != 500 || got.Username != "chan" {
			t.Errorf("Resolve(%s) = %+v, %v", in, got, err)
		}
	}
	for in, want := range map[string]string{
		"https://t.me/+AbCdEf": "邀请链接",
		"t.me/joinchat/AbCdEf": "邀请链接",
		"!!":                   "无法识别",
		"@nobody":              "频道不存在",
	} {
		if _, err := e.w.Resolve(ctx, in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Resolve(%s) = %v, want %q", in, err, want)
		}
	}
	e.tg.ch.Broadcast, e.tg.ch.Flags = false, 0 // encoding only ever sets flag bits
	if _, err := e.w.Resolve(ctx, "@chan"); !errors.Is(err, errNotBroadcast) {
		t.Errorf("group resolved: %v", err)
	}
}

func TestWatchChannelsScanInBackground(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.tg.manyDialogs, e.tg.dialogFlood = 250, 1
	first, err := e.w.Channels(ctx, false)
	if err != nil || !first.Loading {
		t.Fatalf("first = %+v, %v", first, err)
	}
	eventually(t, "scan done", func() bool { l, _ := e.w.Channels(ctx, false); return !l.Loading })
	list, _ := e.w.Channels(ctx, false)
	// Every other dialog is a group, which is left out; flood waits are waited out and resumed.
	if len(list.Channels) != 125 || list.UpdatedAt == 0 || list.Error != "" || list.Channels[0].ChannelID != 2000 {
		t.Fatalf("list = %d channels, %+v", len(list.Channels), list.Error)
	}
	calls := e.tg.dialogCalls
	if calls != 4 { // one flood, then 3 pages of 100
		t.Fatalf("getDialogs calls = %d", calls)
	}
	if l, _ := e.w.Channels(ctx, false); l.Loading || e.tg.dialogCalls != calls {
		t.Fatal("a fresh list should not rescan")
	}
	// The scanned channels are cached as peers, so a watch can be created on them.
	if _, err := e.w.Known(ctx, 2002); err != nil {
		t.Fatalf("Known = %v", err)
	}
	e.api.ready = false
	if l, err := e.w.Channels(ctx, true); err != nil || len(l.Channels) != 125 {
		t.Fatalf("offline with a list = %d, %v", len(l.Channels), err)
	}
}

func TestWatchChannelsOfflineWithoutList(t *testing.T) {
	e := newWatchEnv(t, fire10)
	e.api.ready = false
	if _, err := e.w.Channels(ctx, false); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Channels = %v", err)
	}
}

func TestWatchSearch(t *testing.T) {
	e := newWatchEnv(t, fire10)
	found, err := e.w.Search(ctx, "cha")
	if err != nil || len(found) != 1 || found[0].Username != "chan" {
		t.Fatalf("Search = %+v, %v", found, err)
	}
}

func TestPollIntervalSetting(t *testing.T) {
	e := newWatchEnv(t, fire10)
	if d := e.w.PollInterval(ctx); d != time.Minute {
		t.Fatalf("default = %s", d)
	}
	for v, want := range map[string]time.Duration{"5": 30 * time.Second, "90": 90 * time.Second, "9999": 10 * time.Minute, "x": time.Minute} {
		e.st.PutSetting(ctx, PollSettingKey, []byte(v), 1)
		if d := e.w.PollInterval(ctx); d != want {
			t.Errorf("%s → %s, want %s", v, d, want)
		}
	}
}

func TestChannelPhoto(t *testing.T) {
	e := newWatchEnv(t, fire10)
	if _, err := e.w.ChannelPhoto(ctx, 500); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("no photo: %v", err)
	}
	e.tg.ch.Photo = &tg.ChatPhoto{PhotoID: 9}
	if _, err := e.w.ChannelPhoto(ctx, 500); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a recent miss should be remembered: %v", err)
	}
	e.now = e.now.Add(11 * time.Minute)
	rel, err := e.w.ChannelPhoto(ctx, 500)
	if err != nil || rel != filepath.Join("channels", "500.jpg") {
		t.Fatalf("ChannelPhoto = %q, %v", rel, err)
	}
	b, _ := os.ReadFile(filepath.Join(e.w.avatarDir, rel))
	c, _ := e.st.GetChannel(ctx, 500)
	if string(b) != "jpeg" || c.AvatarPath != rel {
		t.Fatalf("file %q, channel %+v", b, c)
	}
}

func equalIDs(a, b []int64) bool {
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

var _ = context.Background
