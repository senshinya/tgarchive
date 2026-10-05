// Package receipt tells the sender, via reactions and replies, how archiving went.
package receipt

import (
	"context"
	"log"
	"slices"
	"sync"
	"time"

	"tgarchive/internal/botapifs"
	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

const (
	EmojiSeen = "👀"
	EmojiDone = "👌"

	textFailedPrefix      = "⚠️ 存档失败："
	TextFetchFailedPrefix = "⚠️ 代取失败："
	textTooLarge          = "文件超过存档上限，仅保存了消息记录"
)

// callTimeout bounds each transport call: Evaluate holds e.mu, so one stuck call would stall
// every downloader worker that settles media.
const callTimeout = 30 * time.Second

type Transport interface {
	SetReaction(ctx context.Context, botID, chatID, msgID int64, emoji string) error
	Reply(ctx context.Context, botID, chatID, msgID int64, text string) error
}

type Engine struct {
	st *store.Store
	tr Transport
	mu sync.Mutex // serializes evaluations so a reply is never sent twice
}

func New(st *store.Store, tr Transport) *Engine { return &Engine{st: st, tr: tr} }

// apply drives the reaction / reply state machine for one tracked item (a message or a fetch job).
func (e *Engine) apply(ctx context.Context, info *store.ReceiptInfo, main []store.MediaStatus, cur string, save func(string)) {
	var pending, failed, tooLarge int
	firstErr := ""
	for _, m := range main {
		switch m.State {
		case store.StatePending:
			pending++
		case store.StateFailed:
			failed++
			if firstErr == "" {
				firstErr = m.Error
			}
		case store.StateTooLarge:
			tooLarge++
		}
	}
	switch {
	case pending > 0:
		if cur == store.ReceiptNone {
			if err := e.react(ctx, info, EmojiSeen); err == nil {
				save(store.ReceiptSeen)
			}
		}
	case failed > 0:
		if cur != store.ReceiptFailed {
			if cur == store.ReceiptNone {
				e.reactLogOnly(ctx, info, EmojiSeen)
			}
			if err := e.reply(ctx, info, textFailedPrefix+truncate(botapifs.RedactPath(firstErr), 200)); err == nil {
				save(store.ReceiptFailed)
			}
		}
	default:
		if cur != store.ReceiptDone {
			if err := e.react(ctx, info, EmojiDone); err == nil {
				if tooLarge > 0 {
					e.replyLogOnly(ctx, info, textTooLarge)
				}
				save(store.ReceiptDone)
			}
		}
	}
}

// failOnce keeps 👀 (setting it if nothing was set yet) and sends the failure reply once.
func (e *Engine) failOnce(ctx context.Context, info *store.ReceiptInfo, cur, text string, save func(string)) {
	if cur == store.ReceiptFailed {
		return
	}
	if cur == store.ReceiptNone {
		e.reactLogOnly(ctx, info, EmojiSeen)
	}
	if err := e.reply(ctx, info, text); err == nil {
		save(store.ReceiptFailed)
	}
}

func (e *Engine) Evaluate(ctx context.Context, messageID int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	info, err := e.st.GetReceiptInfo(ctx, messageID)
	if err != nil {
		log.Printf("receipt: info for message %d: %v", messageID, err)
		return
	}
	if info.Source != model.SourceBotUpdate {
		return
	}
	save := func(r string) { e.set(ctx, messageID, r) }
	main := slices.Clone(info.Main)
	if a := info.Article; a != nil {
		switch a.State {
		case store.TelegraphFailed:
			e.failOnce(ctx, info, info.Receipt, textFailedPrefix+truncate(a.Error, 200), save)
			return
		case store.TelegraphFetched:
			// Article media only hold 👌 back while pending: a failed or oversized image does not
			// turn the archived article into a failure.
			for _, m := range a.Media {
				if m.State == store.StatePending {
					main = append(main, m)
				}
			}
		default: // queued / fetching
			main = append(main, store.MediaStatus{State: store.StatePending})
		}
	}
	e.apply(ctx, info, main, info.Receipt, save)
}

// EvaluateJob reports a userbot fetch job's progress on the sender's original link message.
func (e *Engine) EvaluateJob(ctx context.Context, jobID int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ji, err := e.st.GetJobReceiptInfo(ctx, jobID)
	if err != nil {
		log.Printf("receipt: info for fetch job %d: %v", jobID, err)
		return
	}
	info := &store.ReceiptInfo{BotID: ji.BotID, TgChatID: ji.TgChatID, TgMessageID: ji.TgMessageID}
	save := func(r string) {
		if err := e.st.SetFetchJobReceipt(ctx, jobID, r); err != nil {
			log.Printf("receipt: save state for fetch job %d: %v", jobID, err)
		}
	}
	switch ji.State {
	case store.JobQueued, store.JobFetching:
		if ji.Receipt == store.ReceiptNone {
			if err := e.react(ctx, info, EmojiSeen); err == nil {
				save(store.ReceiptSeen)
			}
		}
	case store.JobFailed:
		e.failOnce(ctx, info, ji.Receipt, TextFetchFailedPrefix+truncate(ji.Error, 200), save)
	case store.JobFetched:
		e.apply(ctx, info, ji.Main, ji.Receipt, save)
	}
}

func (e *Engine) MediaSettled(ctx context.Context, mediaID int64) {
	ids, err := e.st.MessagesForMedia(ctx, mediaID)
	if err != nil {
		log.Printf("receipt: messages for media %d: %v", mediaID, err)
		return
	}
	jobs := map[int64]bool{}
	var order []int64
	for _, id := range ids {
		e.Evaluate(ctx, id)
		js, err := e.st.FetchJobsForMessage(ctx, id)
		if err != nil {
			log.Printf("receipt: fetch jobs for message %d: %v", id, err)
			continue
		}
		for _, j := range js {
			if !jobs[j] {
				jobs[j] = true
				order = append(order, j)
			}
		}
	}
	for _, j := range order {
		e.EvaluateJob(ctx, j)
	}
}

func (e *Engine) setReaction(ctx context.Context, i *store.ReceiptInfo, emoji string) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return e.tr.SetReaction(ctx, i.BotID, i.TgChatID, i.TgMessageID, emoji)
}

func (e *Engine) sendReply(ctx context.Context, i *store.ReceiptInfo, text string) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return e.tr.Reply(ctx, i.BotID, i.TgChatID, i.TgMessageID, text)
}

func (e *Engine) react(ctx context.Context, i *store.ReceiptInfo, emoji string) error {
	if err := e.setReaction(ctx, i, emoji); err != nil {
		log.Printf("receipt: react %s on bot %d msg %d: %v", emoji, i.BotID, i.TgMessageID, err)
		return err
	}
	return nil
}

func (e *Engine) reactLogOnly(ctx context.Context, i *store.ReceiptInfo, emoji string) {
	if err := e.setReaction(ctx, i, emoji); err != nil {
		log.Printf("receipt: react %s on bot %d msg %d: %v", emoji, i.BotID, i.TgMessageID, err)
	}
}

func (e *Engine) reply(ctx context.Context, i *store.ReceiptInfo, text string) error {
	if err := e.sendReply(ctx, i, text); err != nil {
		log.Printf("receipt: reply on bot %d msg %d: %v", i.BotID, i.TgMessageID, err)
		return err
	}
	return nil
}

func (e *Engine) replyLogOnly(ctx context.Context, i *store.ReceiptInfo, text string) {
	if err := e.sendReply(ctx, i, text); err != nil {
		log.Printf("receipt: reply on bot %d msg %d: %v", i.BotID, i.TgMessageID, err)
	}
}

func (e *Engine) set(ctx context.Context, id int64, receipt string) {
	if err := e.st.SetReceipt(ctx, id, receipt); err != nil {
		log.Printf("receipt: save state for message %d: %v", id, err)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
