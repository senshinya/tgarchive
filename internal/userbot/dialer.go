package userbot

import (
	"context"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/tgapp"
)

// Dialer runs fn with a connected MTProto client until fn returns or ctx ends.
type Dialer interface {
	Dial(ctx context.Context, creds tgapp.Credentials, sess session.Storage, fn func(ctx context.Context, api *tg.Client) error) error
}

type GotdDialer struct{}

func (GotdDialer) Dial(ctx context.Context, creds tgapp.Credentials, sess session.Storage, fn func(context.Context, *tg.Client) error) error {
	c := telegram.NewClient(creds.APIID, creds.APIHash, telegram.Options{SessionStorage: sess, NoUpdates: true,
		Middlewares: []telegram.Middleware{FloodRetry(shortFlood, 2)}})
	return c.Run(ctx, func(ctx context.Context) error { return fn(ctx, c.API()) })
}

// shortFlood is the longest FLOOD_WAIT a call waits out in place. Paging through dialogs (the
// channel picker) runs into short waits routinely; longer ones are left to the callers.
const shortFlood = 30 * time.Second

// FloodRetry retries a call up to tries times when Telegram answers FLOOD_WAIT of at most max
// (plus a second of slack), unless the call's context ends first.
func FloodRetry(max time.Duration, tries int) telegram.Middleware {
	return telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			for i := 0; ; i++ {
				err := next.Invoke(ctx, input, output)
				d, ok := tgerr.AsFloodWait(err)
				if !ok || d > max || i >= tries {
					return err
				}
				t := time.NewTimer(d + time.Second)
				select {
				case <-ctx.Done():
					t.Stop()
					return err
				case <-t.C:
				}
			}
		}
	})
}
