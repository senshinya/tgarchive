// Package collector runs one long-polling worker per bot and archives what they receive.
package collector

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"tgarchive/internal/botclients"
	"tgarchive/internal/convert/botapi"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/model"
	"tgarchive/internal/notify"
	"tgarchive/internal/receipt"
	"tgarchive/internal/store"
	"tgarchive/internal/tgbot"
)

// LinkHandler may consume a message (for example a Telegram post link to fetch) instead of
// archiving it. handled=true advances the update offset; a non-nil err leaves the offset where it
// is, so the worker refetches and re-delivers the same update. Implementations must therefore be
// idempotent per (botID, msg.TgMessageID).
type LinkHandler interface {
	TryHandle(ctx context.Context, botID int64, sender model.Sender, msg *model.Message, canFetch bool) (handled bool, err error)
}

type AvatarRefresher interface {
	RefreshSender(ctx context.Context, botID, userID int64)
}

type Deps struct {
	Store          *store.Store
	Clients        *botclients.Registry
	Downloader     *downloader.Downloader
	Receipts       *receipt.Engine
	Hub            *events.Hub
	Notifier       notify.Notifier
	Links          LinkHandler
	Avatars        AvatarRefresher
	MediaDir       string
	PollTimeoutSec int
	MaxBackoff     time.Duration
	Now            func() time.Time
}

type worker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type Manager struct {
	base    context.Context
	d       Deps
	mu      sync.Mutex
	workers map[int64]*worker
}

func New(base context.Context, d Deps) *Manager {
	if d.PollTimeoutSec == 0 {
		d.PollTimeoutSec = 50
	}
	if d.MaxBackoff == 0 {
		d.MaxBackoff = 60 * time.Second
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Manager{base: base, d: d, workers: map[int64]*worker{}}
}

func (m *Manager) StartAll(ctx context.Context) error {
	bots, err := m.d.Store.ListBots(ctx)
	if err != nil {
		return err
	}
	for _, b := range bots {
		if b.Enabled && b.Status != store.StatusRemoved {
			m.Start(b.ID)
		}
	}
	return nil
}

func (m *Manager) Start(botID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workers[botID]; ok {
		return
	}
	ctx, cancel := context.WithCancel(m.base)
	w := &worker{cancel: cancel, done: make(chan struct{})}
	m.workers[botID] = w
	go func() {
		defer close(w.done)
		defer func() {
			m.mu.Lock()
			if m.workers[botID] == w {
				delete(m.workers, botID)
			}
			m.mu.Unlock()
		}()
		m.run(ctx, botID)
	}()
}

func (m *Manager) Stop(botID int64) {
	m.mu.Lock()
	w := m.workers[botID]
	m.mu.Unlock()
	if w == nil {
		return
	}
	w.cancel()
	<-w.done
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	ids := make([]int64, 0, len(m.workers))
	for id := range m.workers {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Stop(id)
	}
}

func (m *Manager) Running(botID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.workers[botID]
	return ok
}

func (m *Manager) run(ctx context.Context, botID int64) {
	bot, err := m.d.Store.GetBot(ctx, botID)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		m.fail(botID, err)
		return
	}
	if bot.Status == store.StatusRemoved || !bot.Enabled {
		return
	}
	cl, err := m.d.Clients.Get(ctx, botID)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		m.fail(botID, err)
		return
	}
	m.setStatus(botID, store.StatusRunning, "")
	offset := bot.UpdateOffset
	backoff := time.Second
	for ctx.Err() == nil {
		pctx, cancel := context.WithTimeout(ctx, time.Duration(m.d.PollTimeoutSec+20)*time.Second)
		ups, err := cl.GetUpdates(pctx, offset, m.d.PollTimeoutSec)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			var ae *tgbot.APIError
			if errors.As(err, &ae) && (ae.Code == 401 || ae.Code == 409) {
				m.fail(botID, err)
				return
			}
			wait := backoff
			if errors.As(err, &ae) && ae.RetryAfter > 0 {
				wait = time.Duration(ae.RetryAfter) * time.Second
			}
			log.Printf("collector: bot %d poll: %v (retry in %s)", botID, err, wait)
			if !sleep(ctx, wait) {
				return
			}
			backoff = min(backoff*2, m.d.MaxBackoff)
			continue
		}
		backoff = time.Second
		for _, u := range ups {
			if err := m.handle(ctx, botID, u); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("collector: bot %d update %d: %v", botID, u.UpdateID, err)
				sleep(ctx, time.Second)
				break // refetch from the last persisted offset
			}
			offset = u.UpdateID + 1
		}
	}
}

func (m *Manager) handle(ctx context.Context, botID int64, u tgbot.Update) error {
	next := u.UpdateID + 1
	raw := u.Message
	if len(raw) == 0 {
		raw = u.EditedMessage
	}
	if len(raw) == 0 {
		return m.d.Store.AdvanceOffset(ctx, botID, next)
	}
	res, err := botapi.Convert(raw)
	if err != nil {
		log.Printf("collector: bot %d skips update %d: %v", botID, u.UpdateID, err)
		return m.d.Store.AdvanceOffset(ctx, botID, next)
	}
	if res.ChatType != "private" {
		return m.d.Store.AdvanceOffset(ctx, botID, next)
	}
	now := m.d.Now().Unix()
	allowed, canFetch, err := m.d.Store.CheckAllowed(ctx, botID, res.Sender.TgUserID)
	if err != nil {
		return err
	}
	if !allowed {
		if err := m.d.Store.RecordRejected(ctx, store.Rejected{
			BotID: botID, TgUserID: res.Sender.TgUserID, FirstName: res.Sender.FirstName, Username: res.Sender.Username, LastSeenAt: now,
		}); err != nil {
			return err
		}
		return m.d.Store.AdvanceOffset(ctx, botID, next)
	}
	if len(u.Message) > 0 && m.d.Links != nil {
		handled, err := m.d.Links.TryHandle(ctx, botID, res.Sender, res.Msg, canFetch)
		if err != nil {
			return fmt.Errorf("link handler: %w", err)
		}
		if handled {
			return m.d.Store.AdvanceOffset(ctx, botID, next)
		}
	}
	ir, err := m.d.Store.Ingest(ctx, store.IngestInput{BotID: botID, Sender: res.Sender, Msg: res.Msg, Offset: next, Now: now})
	if err != nil {
		return err
	}
	downloader.RemoveFiles(m.d.MediaDir, ir.OrphanPaths)
	typ := "message.updated"
	if ir.Created {
		typ = "message.created"
	}
	m.d.Hub.Publish(events.Event{Type: typ, Data: map[string]int64{"chat_id": ir.ChatID, "message_id": ir.MessageID}})
	if ir.ChatCreated && m.d.Avatars != nil {
		go m.d.Avatars.RefreshSender(m.base, botID, res.Sender.TgUserID)
	}
	// Evaluate before waking the downloader so 👀 always precedes 👌.
	m.d.Receipts.Evaluate(ctx, ir.MessageID)
	m.d.Downloader.Wake()
	return nil
}

func (m *Manager) setStatus(botID int64, status, lastErr string) {
	if err := m.d.Store.SetBotStatus(context.Background(), botID, status, lastErr); err != nil {
		log.Printf("collector: set bot %d status: %v", botID, err)
	}
	m.d.Hub.Publish(events.Event{Type: "bot.status", Data: map[string]any{"bot_id": botID, "status": status, "error": lastErr}})
}

func (m *Manager) fail(botID int64, err error) {
	msg := err.Error()
	m.setStatus(botID, store.StatusError, msg)
	if m.d.Notifier == nil {
		return
	}
	name := fmt.Sprintf("bot #%d", botID)
	if b, e := m.d.Store.GetBot(context.Background(), botID); e == nil && b.Username != "" {
		name = "@" + b.Username
	}
	m.d.Notifier.Notify(context.Background(), "tgarchive 机器人异常", name+": "+msg)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
