// Package botclients builds Bot API clients from stored, encrypted tokens.
package botclients

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgbot"
)

type Registry struct {
	st      *store.Store
	box     *seal.Box
	baseURL string
	hc      *http.Client

	mu    sync.Mutex
	cache map[int64]*tgbot.Client
}

func New(st *store.Store, box *seal.Box, baseURL string, hc *http.Client) *Registry {
	return &Registry{st: st, box: box, baseURL: baseURL, hc: hc, cache: map[int64]*tgbot.Client{}}
}

func (r *Registry) Get(ctx context.Context, botID int64) (*tgbot.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cache[botID]; ok {
		return c, nil
	}
	b, err := r.st.GetBot(ctx, botID)
	if err != nil {
		return nil, err
	}
	if len(b.TokenEnc) == 0 {
		return nil, errors.New("bot has no token (removed)")
	}
	tok, err := r.box.Open(b.TokenEnc)
	if err != nil {
		return nil, errors.New("cannot decrypt bot token; TOKEN_ENC_KEY changed?")
	}
	c := tgbot.New(r.baseURL, string(tok), r.hc)
	r.cache[botID] = c
	return c, nil
}

func (r *Registry) Forget(botID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, botID)
}

func (r *Registry) SetReaction(ctx context.Context, botID, chatID, msgID int64, emoji string) error {
	c, err := r.Get(ctx, botID)
	if err != nil {
		return err
	}
	return c.SetMessageReaction(ctx, chatID, msgID, emoji)
}

func (r *Registry) Reply(ctx context.Context, botID, chatID, msgID int64, text string) error {
	c, err := r.Get(ctx, botID)
	if err != nil {
		return err
	}
	return c.SendMessage(ctx, chatID, text, msgID)
}
