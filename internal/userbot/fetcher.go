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
	Now       func() time.Time
}

func NewFetcher(api API, st *store.Store, rc JobReceipts, tr receipt.Transport, hub *events.Hub, wake func(), mediaDir string) *Fetcher {
	return &Fetcher{api: api, st: st, receipts: rc, transport: tr, hub: hub, wakeDL: wake, mediaDir: mediaDir,
		wake: make(chan struct{}, 1), Gap: 3 * time.Second, MaxFlood: 300 * time.Second, FloodPad: time.Second,
		ReadyWait: 30 * time.Second, Now: time.Now}
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
	if n, err := f.st.RequeueFetchingJobs(ctx, f.Now().Unix()); err != nil {
		log.Printf("userbot: requeue interrupted jobs: %v", err)
	} else if n > 0 {
		log.Printf("userbot: requeued %d interrupted fetch jobs", n)
	}
	for ctx.Err() == nil {
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
		ch, err := f.resolve(ctx, api, link)
		if err != nil {
			return err
		}
		in := &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
		names := mtproto.NamesFrom(nil, nil)
		id := int(link.MsgID)
		got, err := getMessages(ctx, api, in, names, id)
		if err != nil {
			return err
		}
		first, ok := got[id]
		if !ok {
			return errNoMessage
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
				return err
			}
			for _, m := range around {
				if mg, ok := m.GetGroupedID(); ok && mg == g {
					list = append(list, m)
				}
			}
			sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		}
		out = &fetched{msgs: list, ch: mtproto.ChannelFrom(ch), names: names}
		return nil
	})
	return out, err
}

func (f *Fetcher) resolve(ctx context.Context, api *tg.Client, link linkparse.Link) (*tg.Channel, error) {
	if link.Username != "" {
		r, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: link.Username})
		if err != nil {
			if tgerr.Is(err, "USERNAME_NOT_OCCUPIED", "USERNAME_INVALID") {
				return nil, errNoChat
			}
			return nil, err
		}
		pc, ok := r.Peer.(*tg.PeerChannel)
		if !ok {
			return nil, errNotChannel
		}
		for _, c := range r.Chats {
			if ch, ok := c.(*tg.Channel); ok && ch.ID == pc.ChannelID {
				f.savePeers(ctx, ch)
				return ch, nil
			}
		}
		return nil, errNotChannel
	}
	p, err := f.st.GetPeer(ctx, link.ChannelID)
	if err == nil {
		return &tg.Channel{ID: p.ChannelID, AccessHash: p.AccessHash, Title: p.Title, Username: p.Username}, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
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
		return nil, err
	}
	all := make([]*tg.Channel, 0, len(seen))
	for _, c := range seen {
		all = append(all, c)
	}
	f.savePeers(ctx, all...)
	if c := seen[link.ChannelID]; c != nil {
		return c, nil
	}
	return nil, errNotMember
}

func (f *Fetcher) savePeers(ctx context.Context, chs ...*tg.Channel) {
	peers := make([]store.Peer, 0, len(chs))
	for _, c := range chs {
		if !c.Min {
			peers = append(peers, store.Peer{ChannelID: c.ID, AccessHash: c.AccessHash, Username: c.Username, Title: c.Title})
		}
	}
	if len(peers) == 0 {
		return
	}
	if err := f.st.PutPeers(ctx, peers, f.Now().Unix()); err != nil {
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
