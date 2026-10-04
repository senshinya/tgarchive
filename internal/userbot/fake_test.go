package userbot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/session"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgmock"

	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgapp"
)

var ctx = context.Background()

// fakeTG answers MTProto requests. extra, when set, is consulted first; returning (nil, nil)
// falls through to the defaults.
type fakeTG struct {
	mu         sync.Mutex
	authorized bool
	user       *tg.User
	calls      []string
	extra      func(req bin.Encoder) (bin.Encoder, error)
	// before, when set, runs ahead of every request without f.mu held, so it may itself drive
	// the Service (including RPCs that come back through handle).
	before func(req bin.Encoder)
}

func newFakeTG() *fakeTG {
	return &fakeTG{user: &tg.User{ID: 99, FirstName: "Me", LastName: "Self", Phone: "8613800000000", Self: true}}
}

func unauthorizedErr() error {
	return &tgerr.Error{Code: 401, Message: "AUTH_KEY_UNREGISTERED", Type: "AUTH_KEY_UNREGISTERED"}
}

func (f *fakeTG) set(fn func(f *fakeTG)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeTG) called(typ string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == typ {
			n++
		}
	}
	return n
}

func (f *fakeTG) handle(req bin.Encoder) (bin.Encoder, error) {
	f.mu.Lock()
	before := f.before
	f.mu.Unlock()
	if before != nil {
		before(req)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%T", req))
	if f.extra != nil {
		if r, err := f.extra(req); r != nil || err != nil {
			return r, err
		}
	}
	switch r := req.(type) {
	case *tg.UsersGetUsersRequest:
		if !f.authorized {
			return nil, unauthorizedErr()
		}
		return &tg.UserClassVector{Elems: []tg.UserClass{f.user}}, nil
	case *tg.AuthSendCodeRequest:
		return &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeApp{Length: 5}, PhoneCodeHash: "hash-" + r.PhoneNumber}, nil
	case *tg.AuthSignInRequest:
		if r.PhoneCode != "12345" || r.PhoneCodeHash != "hash-"+r.PhoneNumber {
			return nil, &tgerr.Error{Code: 400, Message: "PHONE_CODE_INVALID", Type: "PHONE_CODE_INVALID"}
		}
		f.authorized = true
		return &tg.AuthAuthorization{User: f.user}, nil
	case *tg.AuthLogOutRequest:
		f.authorized = false
		return &tg.AuthLoggedOut{}, nil
	}
	return nil, fmt.Errorf("fakeTG: unexpected %T", req)
}

type fakeDialer struct{ tg *fakeTG }

func (d *fakeDialer) Dial(ctx context.Context, _ tgapp.Credentials, _ session.Storage, fn func(context.Context, *tg.Client) error) error {
	return fn(ctx, tg.NewClient(tgmock.Invoker(d.tg.handle)))
}

// keyedDialer models auth keys the way gotd does: a connection with no stored session starts a
// brand-new, unauthorized key (and persists it); one with a stored session reuses it, keeping
// whatever authorization that key has. hold, when set, is called once on the first Dial after the
// session has been loaded and before it is re-saved — the window gotd's own session load/save
// happens in during connection setup.
type keyedDialer struct {
	tg   *fakeTG
	mu   sync.Mutex
	hold func()
}

func (d *keyedDialer) Dial(ctx context.Context, _ tgapp.Credentials, sess session.Storage, fn func(context.Context, *tg.Client) error) error {
	data, err := sess.LoadSession(ctx)
	if err != nil && !errors.Is(err, session.ErrNotFound) {
		return err
	}
	d.mu.Lock()
	hold := d.hold
	d.hold = nil
	d.mu.Unlock()
	if hold != nil {
		hold()
	}
	if len(data) == 0 {
		d.tg.set(func(f *fakeTG) { f.authorized = false })
		data = []byte("fresh-key")
	}
	if err := sess.StoreSession(ctx, data); err != nil {
		return err
	}
	return fn(ctx, tg.NewClient(tgmock.Invoker(d.tg.handle)))
}

// newSvcWith runs a Service over its own store with the given dialer (configured credentials).
func newSvcWith(t *testing.T, d Dialer) *svcEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := seal.New(bytes.Repeat([]byte{2}, 32))
	creds := tgapp.New(st, box)
	if err := creds.Save(ctx, tgapp.Credentials{APIID: 1, APIHash: "0123456789abcdef0123456789abcdef"}, 1); err != nil {
		t.Fatal(err)
	}
	n := &countNotifier{}
	return &svcEnv{svc: New(st, box, creds, d, n), st: st, box: box, creds: creds, n: n}
}

// start runs the service loop until the test ends.
func (e *svcEnv) start(t *testing.T) {
	t.Helper()
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.Run(runCtx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

type countNotifier struct {
	mu     sync.Mutex
	titles []string
}

func (n *countNotifier) Notify(_ context.Context, title, _ string) {
	n.mu.Lock()
	n.titles = append(n.titles, title)
	n.mu.Unlock()
}

func (n *countNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.titles)
}

type svcEnv struct {
	svc   *Service
	st    *store.Store
	box   *seal.Box
	creds *tgapp.Store
	n     *countNotifier
}

func newSvcEnv(t *testing.T, f *fakeTG, configured bool) *svcEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := seal.New(bytes.Repeat([]byte{2}, 32))
	creds := tgapp.New(st, box)
	if configured {
		if err := creds.Save(ctx, tgapp.Credentials{APIID: 1, APIHash: "0123456789abcdef0123456789abcdef"}, 1); err != nil {
			t.Fatal(err)
		}
	}
	n := &countNotifier{}
	svc := New(st, box, creds, &fakeDialer{tg: f}, n)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.Run(runCtx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &svcEnv{svc: svc, st: st, box: box, creds: creds, n: n}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *svcEnv) waitState(t *testing.T, want string) {
	t.Helper()
	eventually(t, "state "+want, func() bool { return e.svc.Status(ctx).State == want })
}
