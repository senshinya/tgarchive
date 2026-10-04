package userbot

import (
	"context"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"tgarchive/internal/tgapp"
)

// Dialer runs fn with a connected MTProto client until fn returns or ctx ends.
type Dialer interface {
	Dial(ctx context.Context, creds tgapp.Credentials, sess session.Storage, fn func(ctx context.Context, api *tg.Client) error) error
}

type GotdDialer struct{}

func (GotdDialer) Dial(ctx context.Context, creds tgapp.Credentials, sess session.Storage, fn func(context.Context, *tg.Client) error) error {
	c := telegram.NewClient(creds.APIID, creds.APIHash, telegram.Options{SessionStorage: sess, NoUpdates: true})
	return c.Run(ctx, func(ctx context.Context) error { return fn(ctx, c.API()) })
}
