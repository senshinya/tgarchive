package userbot

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type countInvoker struct {
	n    int
	errs []error
}

func (c *countInvoker) Invoke(_ context.Context, _ bin.Encoder, _ bin.Decoder) error {
	c.n++
	if len(c.errs) > 0 {
		err := c.errs[0]
		c.errs = c.errs[1:]
		return err
	}
	return nil
}

func TestFloodRetry(t *testing.T) {
	short := tgerr.New(420, "FLOOD_WAIT_0")
	long := tgerr.New(420, "FLOOD_WAIT_60")
	cases := []struct {
		errs  []error
		calls int
		fails bool
	}{
		{nil, 1, false},
		{[]error{short}, 2, false},
		{[]error{short, short, short}, 3, true}, // gives up after two retries
		{[]error{long}, 1, true},                // too long to wait in place
		{[]error{tgerr.New(400, "CHANNEL_INVALID")}, 1, true},
	}
	for i, c := range cases {
		inv := &countInvoker{errs: c.errs}
		err := FloodRetry(30*time.Second, 2).Handle(inv)(context.Background(), &tg.UsersGetUsersRequest{}, nil)
		if inv.n != c.calls || (err != nil) != c.fails {
			t.Errorf("case %d: calls %d err %v", i, inv.n, err)
		}
	}
}
