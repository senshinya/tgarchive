// Package receipt tells the sender, via reactions and replies, how archiving went.
package receipt

import (
	"context"
	"log"
	"sync"
	"time"

	"tgarchive/internal/botapifs"
	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

const (
	EmojiSeen = "👀"
	EmojiDone = "👌"

	textFailedPrefix = "⚠️ 存档失败："
	textTooLarge     = "文件超过存档上限，仅保存了消息记录"
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

func (e *Engine) MediaSettled(ctx context.Context, mediaID int64) {
	ids, err := e.st.MessagesForMedia(ctx, mediaID)
	if err != nil {
		log.Printf("receipt: messages for media %d: %v", mediaID, err)
		return
	}
	for _, id := range ids {
		e.Evaluate(ctx, id)
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
	var pending, failed, tooLarge int
	firstErr := ""
	for _, m := range info.Main {
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
		if info.Receipt == store.ReceiptNone {
			if err := e.react(ctx, info, EmojiSeen); err == nil {
				e.set(ctx, messageID, store.ReceiptSeen)
			}
		}
	case failed > 0:
		if info.Receipt != store.ReceiptFailed {
			if info.Receipt == store.ReceiptNone {
				e.reactLogOnly(ctx, info, EmojiSeen)
			}
			if err := e.reply(ctx, info, textFailedPrefix+truncate(botapifs.RedactPath(firstErr), 200)); err == nil {
				e.set(ctx, messageID, store.ReceiptFailed)
			}
		}
	default:
		if info.Receipt != store.ReceiptDone {
			if err := e.react(ctx, info, EmojiDone); err == nil {
				if tooLarge > 0 {
					e.replyLogOnly(ctx, info, textTooLarge)
				}
				e.set(ctx, messageID, store.ReceiptDone)
			}
		}
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
