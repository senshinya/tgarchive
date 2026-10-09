package userbot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/convert/mtproto"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/linkparse"
	"tgarchive/internal/model"
	"tgarchive/internal/receipt"
	"tgarchive/internal/store"
)

// API is what the fetcher and the media source need from Service.
type API interface {
	With(ctx context.Context, fn func(api *tg.Client) error) error
	WaitReady(ctx context.Context, max time.Duration) bool
}

// JobReceipts reports a job's progress on the sender's link message (receipt.Engine).
type JobReceipts interface {
	EvaluateJob(ctx context.Context, jobID int64)
}

var (
	errNotMember  = errors.New("私有群/频道，代取账号未加入")
	errNoMessage  = errors.New("消息不存在或已被删除")
	errNotChannel = errors.New("链接不是频道或超级群消息")
	errNoChat     = errors.New("频道或群组不存在")
	errStop       = errors.New("stop")
)

const replyTimeout = 30 * time.Second

type Fetcher struct {
	api       API
	st        *store.Store
	receipts  JobReceipts
	transport receipt.Transport
	hub       *events.Hub
	wakeDL    func()
	mediaDir  string
	wake      chan struct{}

	Gap       time.Duration // pause between jobs
	MaxFlood  time.Duration // longest FLOOD_WAIT waited out in place
	FloodPad  time.Duration // slack added to each FLOOD_WAIT
	ReadyWait time.Duration // how long a job waits for a connecting account
	RetryWait time.Duration // pause before retrying after a transient connection error
	Now       func() time.Time
	// Absent is updated by every dialogs scan but never consulted: a link is sent when the user
	// wants it, possibly right after joining. Shared with the Watcher.
	Absent *AbsentChannels
}

func NewFetcher(api API, st *store.Store, rc JobReceipts, tr receipt.Transport, hub *events.Hub, wake func(), mediaDir string) *Fetcher {
	return &Fetcher{api: api, st: st, receipts: rc, transport: tr, hub: hub, wakeDL: wake, mediaDir: mediaDir,
		wake: make(chan struct{}, 1), Gap: 3 * time.Second, MaxFlood: 300 * time.Second, FloodPad: time.Second,
		ReadyWait: 30 * time.Second, RetryWait: time.Second, Now: time.Now, Absent: NewAbsentChannels()}
}

// TryHandle implements collector.LinkHandler. It only enqueues; Run does the fetching.
func (f *Fetcher) TryHandle(ctx context.Context, botID int64, sender model.Sender, msg *model.Message, canFetch bool) (bool, error) {
	if !canFetch || msg.Kind != model.KindText || len(msg.Media) > 0 {
		return false, nil
	}
	raw, ok := linkparse.Candidate(msg.Text)
	if !ok {
		return false, nil
	}
	now := f.Now().Unix()
	if err := f.st.UpsertSender(ctx, sender, now); err != nil {
		return false, err
	}
	job := &store.FetchJob{BotID: botID, SenderID: sender.TgUserID, LinkTgMessageID: msg.TgMessageID, Link: raw,
		State: store.JobQueued, CreatedAt: now, UpdatedAt: now}
	if _, err := linkparse.Parse(raw); err != nil {
		job.State, job.Error = store.JobUnsupported, linkparse.ErrUnsupported.Error()
		_, created, err := f.st.CreateFetchJob(ctx, job)
		if err != nil {
			return false, err
		}
		if created {
			rctx, cancel := context.WithTimeout(ctx, replyTimeout)
			defer cancel()
			if err := f.transport.Reply(rctx, botID, sender.TgUserID, msg.TgMessageID, receipt.TextFetchFailedPrefix+job.Error); err != nil {
				log.Printf("userbot: reply on bot %d msg %d: %v", botID, msg.TgMessageID, err)
			}
		}
		return false, nil
	}
	id, created, err := f.st.CreateFetchJob(ctx, job)
	if err != nil {
		return false, err
	}
	if created {
		f.receipts.EvaluateJob(ctx, id)
		select {
		case f.wake <- struct{}{}:
		default:
		}
	}
	return true, nil
}

func (f *Fetcher) Run(ctx context.Context) {
	for ctx.Err() == nil {
		// The queue is serial, so no job is legitimately 'fetching' between iterations: this
		// requeues jobs interrupted by a restart and any whose FinishFetchJob failed.
		if n, err := f.st.RequeueFetchingJobs(ctx, f.Now().Unix()); err != nil {
			if ctx.Err() == nil {
				log.Printf("userbot: requeue interrupted jobs: %v", err)
			}
		} else if n > 0 {
			log.Printf("userbot: requeued %d interrupted fetch jobs", n)
		}
		did, err := f.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("userbot: fetch queue: %v", err)
		}
		if did {
			if !sleep(ctx, f.Gap) {
				return
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
		case <-time.After(30 * time.Second):
		}
	}
}

// RunOnce processes the oldest queued job and reports whether there was one.
func (f *Fetcher) RunOnce(ctx context.Context) (bool, error) {
	job, err := f.st.ClaimNextFetchJob(ctx, f.Now().Unix())
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	f.process(ctx, job)
	return true, nil
}

func (f *Fetcher) process(ctx context.Context, job *store.FetchJob) {
	link, err := linkparse.Parse(job.Link)
	if err != nil {
		f.finish(ctx, job, store.JobFailed, linkparse.ErrUnsupported.Error())
		return
	}
	for attempt := 0; ; attempt++ {
		if !f.api.WaitReady(ctx, f.ReadyWait) {
			if ctx.Err() == nil {
				f.finish(ctx, job, store.JobFailed, ErrNotReady.Error())
			}
			return
		}
		got, err := f.fetch(ctx, link)
		fetchErr := err
		if err == nil {
			err = f.archive(ctx, job, got)
		}
		if ctx.Err() != nil {
			return // left as 'fetching'; requeued on the next start
		}
		if err == nil {
			f.finish(ctx, job, store.JobFetched, "")
			if f.wakeDL != nil {
				f.wakeDL()
			}
			return
		}
		if d, ok := tgerr.AsFloodWait(err); ok && d <= f.MaxFlood && attempt < 3 {
			log.Printf("userbot: job %d: flood wait %s", job.ID, d)
			if !sleep(ctx, d+f.FloodPad) {
				return
			}
			continue
		}
		if fetchErr != nil && transient(fetchErr) && attempt < 3 {
			// The connection dropped or was reloaded between WaitReady and With: wait for
			// the account again rather than failing the job.
			log.Printf("userbot: job %d: transient: %v", job.ID, fetchErr)
			if !sleep(ctx, f.RetryWait) {
				return
			}
			continue
		}
		f.finish(ctx, job, store.JobFailed, reason(err))
		return
	}
}

func (f *Fetcher) finish(ctx context.Context, job *store.FetchJob, state, msg string) {
	if err := f.st.FinishFetchJob(ctx, job.ID, state, msg, f.Now().Unix()); err != nil {
		log.Printf("userbot: finish job %d: %v", job.ID, err)
		return
	}
	f.receipts.EvaluateJob(ctx, job.ID)
}

type fetched struct {
	msgs  []*tg.Message
	ch    mtproto.Channel
	names mtproto.Names
}

func (f *Fetcher) fetch(ctx context.Context, link linkparse.Link) (*fetched, error) {
	var out *fetched
	err := f.api.With(ctx, func(api *tg.Client) error {
		ch, cached, err := f.resolve(ctx, api, link)
		if err != nil {
			return err
		}
		list, names, err := f.fetchMessages(ctx, api, ch, link)
		if err != nil && cached && stalePeerErr(err) {
			// The cached access_hash is rejected outright (rather than just failing to find the
			// message): most likely the userbot account switched or rejoined since it was cached.
			// Evict it and re-resolve once via a fresh dialogs scan before giving up.
			if derr := f.st.DeletePeer(ctx, link.ChannelID); derr != nil && !errors.Is(derr, store.ErrNotFound) {
				log.Printf("userbot: invalidate stale peer %d: %v", link.ChannelID, derr)
			}
			var ch2 *tg.Channel
			if ch2, _, err = f.resolve(ctx, api, link); err == nil {
				ch = ch2
				list, names, err = f.fetchMessages(ctx, api, ch, link)
			}
		}
		if err != nil {
			return err
		}
		out = &fetched{msgs: list, ch: mtproto.ChannelFrom(ch), names: names}
		return nil
	})
	return out, err
}

// fetchMessages fetches the linked message (plus its album siblings, unless ?single) from an
// already-resolved channel.
func (f *Fetcher) fetchMessages(ctx context.Context, api *tg.Client, ch *tg.Channel, link linkparse.Link) ([]*tg.Message, mtproto.Names, error) {
	in := &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	names := mtproto.NamesFrom(nil, nil)
	id := int(link.MsgID)
	got, err := getMessages(ctx, api, in, names, id)
	if err != nil {
		return nil, names, err
	}
	first, ok := got[id]
	if !ok {
		return nil, names, errNoMessage
	}
	list := []*tg.Message{first}
	if g, ok := first.GetGroupedID(); ok && !link.Single {
		var ids []int
		for i := id - 10; i <= id+10; i++ {
			if i > 0 && i != id {
				ids = append(ids, i)
			}
		}
		around, err := getMessages(ctx, api, in, names, ids...)
		if err != nil {
			return nil, names, err
		}
		for _, m := range around {
			if mg, ok := m.GetGroupedID(); ok && mg == g {
				list = append(list, m)
			}
		}
		sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	}
	return list, names, nil
}

// resolve finds the channel a link refers to. cached reports whether ch came straight from the
// userbot_peers cache (as opposed to a fresh username resolve or dialogs scan): only a cached hit
// might be stale, so only it is worth invalidating and retrying on CHANNEL_INVALID/CHANNEL_PRIVATE.
func (f *Fetcher) resolve(ctx context.Context, api *tg.Client, link linkparse.Link) (ch *tg.Channel, cached bool, err error) {
	return resolveChannel(ctx, api, f.st, f.Now, f.Absent, link)
}

// resolveChannel finds the channel a link refers to: by username, else from the userbot_peers
// cache, else by scanning the account's dialogs (caching every channel seen). A scan clears the
// channels it saw from absent and records the linked one there when the whole scan missed it;
// whether to scan at all is the caller's call.
func resolveChannel(ctx context.Context, api *tg.Client, st *store.Store, now func() time.Time, absent *AbsentChannels,
	link linkparse.Link) (ch *tg.Channel, cached bool, err error) {
	if link.Username != "" {
		r, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: link.Username})
		if err != nil {
			if tgerr.Is(err, "USERNAME_NOT_OCCUPIED", "USERNAME_INVALID") {
				return nil, false, errNoChat
			}
			return nil, false, err
		}
		pc, ok := r.Peer.(*tg.PeerChannel)
		if !ok {
			return nil, false, errNotChannel
		}
		for _, c := range r.Chats {
			if ch, ok := c.(*tg.Channel); ok && ch.ID == pc.ChannelID {
				savePeers(ctx, st, now, ch)
				return ch, false, nil
			}
		}
		return nil, false, errNotChannel
	}
	p, err := st.GetPeer(ctx, link.ChannelID)
	if err == nil {
		return &tg.Channel{ID: p.ChannelID, AccessHash: p.AccessHash, Title: p.Title, Username: p.Username}, true, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, false, err
	}
	seen := map[int64]*tg.Channel{}
	err = dialogs.NewQueryBuilder(api).GetDialogs().BatchSize(100).ForEach(ctx, func(_ context.Context, e dialogs.Elem) error {
		for id, c := range e.Entities.Channels() {
			if !c.Min {
				seen[id] = c
			}
		}
		if seen[link.ChannelID] != nil {
			return errStop
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return nil, false, err
	}
	all := make([]*tg.Channel, 0, len(seen))
	ids := make([]int64, 0, len(seen))
	for id, c := range seen {
		all = append(all, c)
		ids = append(ids, id)
	}
	savePeers(ctx, st, now, all...)
	absent.remove(ids...)
	if c := seen[link.ChannelID]; c != nil {
		return c, false, nil
	}
	absent.add(link.ChannelID, now())
	return nil, false, errNotMember
}

// transient reports whether a fetch error is the connection going away under the call (the
// account not ready, or a non-RPC error from gotd such as a closed engine) rather than an
// answer from Telegram or a definitive outcome of our own.
func transient(err error) bool {
	if errors.Is(err, ErrNotReady) {
		return true
	}
	if _, ok := tgerr.As(err); ok {
		return false
	}
	for _, e := range []error{errNotMember, errNoMessage, errNotChannel, errNoChat} {
		if errors.Is(err, e) {
			return false
		}
	}
	return true
}

// stalePeerErr reports whether err is Telegram rejecting a channel reference outright, the
// signature of a cached access_hash that no longer matches (e.g. after an account switch/rejoin),
// as opposed to the channel merely being gone or the message missing.
func stalePeerErr(err error) bool {
	return tgerr.Is(err, "CHANNEL_INVALID", "CHANNEL_PRIVATE")
}

func (f *Fetcher) savePeers(ctx context.Context, chs ...*tg.Channel) {
	savePeers(ctx, f.st, f.Now, chs...)
}

// savePeers caches the access hashes of channels (min constructors carry none worth keeping).
func savePeers(ctx context.Context, st *store.Store, now func() time.Time, chs ...*tg.Channel) {
	peers := make([]store.Peer, 0, len(chs))
	for _, c := range chs {
		if !c.Min {
			peers = append(peers, store.Peer{ChannelID: c.ID, AccessHash: c.AccessHash, Username: c.Username, Title: c.Title})
		}
	}
	if len(peers) == 0 {
		return
	}
	if err := st.PutPeers(ctx, peers, now().Unix()); err != nil {
		log.Printf("userbot: cache peers: %v", err)
	}
}

func (f *Fetcher) archive(ctx context.Context, job *store.FetchJob, got *fetched) error {
	sender, err := f.st.GetSender(ctx, job.SenderID)
	if err != nil {
		return err
	}
	now := f.Now().Unix()
	for _, m := range got.msgs {
		msg, err := mtproto.Convert(m, got.ch, got.names)
		if err != nil {
			return err
		}
		ir, err := f.st.Ingest(ctx, store.IngestInput{BotID: job.BotID, Sender: sender, Msg: msg, Now: now})
		if err != nil {
			return err
		}
		downloader.RemoveFiles(f.mediaDir, ir.OrphanPaths)
		if err := f.st.LinkFetchJobMessage(ctx, job.ID, ir.MessageID); err != nil {
			return err
		}
		typ := "message.updated"
		if ir.Created {
			typ = "message.created"
		}
		f.hub.Publish(events.Event{Type: typ, Data: map[string]int64{"chat_id": ir.ChatID, "message_id": ir.MessageID}})
	}
	return nil
}

func getMessages(ctx context.Context, api *tg.Client, in tg.InputChannelClass, names mtproto.Names, ids ...int) (map[int]*tg.Message, error) {
	req := &tg.ChannelsGetMessagesRequest{Channel: in}
	for _, id := range ids {
		req.ID = append(req.ID, &tg.InputMessageID{ID: id})
	}
	res, err := api.ChannelsGetMessages(ctx, req)
	if err != nil {
		return nil, err
	}
	list, users, chats, err := messagesOf(res)
	if err != nil {
		return nil, err
	}
	names.Add(users, chats)
	out := map[int]*tg.Message{}
	for _, mc := range list {
		if m, ok := mc.(*tg.Message); ok {
			out[m.ID] = m
		}
	}
	return out, nil
}

func messagesOf(res tg.MessagesMessagesClass) ([]tg.MessageClass, []tg.UserClass, []tg.ChatClass, error) {
	switch r := res.(type) {
	case *tg.MessagesChannelMessages:
		return r.Messages, r.Users, r.Chats, nil
	case *tg.MessagesMessages:
		return r.Messages, r.Users, r.Chats, nil
	case *tg.MessagesMessagesSlice:
		return r.Messages, r.Users, r.Chats, nil
	}
	return nil, nil, nil, fmt.Errorf("unexpected messages result %T", res)
}

func reason(err error) string {
	if d, ok := tgerr.AsFloodWait(err); ok {
		return fmt.Sprintf("被限流，请 %d 分钟后重试", ceilMinutes(d))
	}
	switch {
	case errors.Is(err, ErrNotReady):
		return ErrNotReady.Error()
	case errors.Is(err, errNotMember), tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID", "CHANNEL_PUBLIC_GROUP_NA"):
		return errNotMember.Error()
	case errors.Is(err, errNoMessage), tgerr.Is(err, "MESSAGE_ID_INVALID"):
		return errNoMessage.Error()
	}
	return err.Error()
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
