package userbot

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/session"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgapp"
)

func TestUnconfiguredThenReload(t *testing.T) {
	e := newSvcEnv(t, newFakeTG(), false)
	e.waitState(t, StateUnconfigured)
	if err := e.svc.SendCode(ctx, "+100"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("SendCode unconfigured err = %v", err)
	}
	if e.svc.WaitReady(ctx, 0) {
		t.Fatal("WaitReady true while unconfigured")
	}
	e.creds.Save(ctx, tgapp.Credentials{APIID: 1, APIHash: "0123456789abcdef0123456789abcdef"}, 2)
	e.svc.Reload()
	e.waitState(t, StateLoggedOut)
}

func TestLoginWithCode(t *testing.T) {
	f := newFakeTG()
	e := newSvcEnv(t, f, true)
	e.waitState(t, StateLoggedOut)
	if _, err := e.svc.SignIn(ctx, "12345"); !errors.Is(err, ErrBadState) {
		t.Fatalf("SignIn before SendCode err = %v", err)
	}
	if err := e.svc.SendCode(ctx, "+8613800000000"); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Status(ctx).State; got != StateCodeSent {
		t.Fatalf("state = %s", got)
	}
	var ie *InputError
	if _, err := e.svc.SignIn(ctx, "00000"); !errors.As(err, &ie) || ie.Msg != "验证码错误" {
		t.Fatalf("wrong code err = %v", err)
	}
	state, err := e.svc.SignIn(ctx, "12345")
	if err != nil || state != StateReady {
		t.Fatalf("SignIn = %s, %v", state, err)
	}
	info := e.svc.Status(ctx)
	if info.State != StateReady || info.TgUserID != 99 || info.Name != "Me Self" || info.Phone != "+8613800000000" {
		t.Fatalf("info = %+v", info)
	}
	u, _ := e.st.GetUserbot(ctx)
	if u.Status != store.UserbotReady || u.TgUserID != 99 {
		t.Fatalf("db = %+v", u)
	}
	if err := e.svc.SendCode(ctx, "+1"); !errors.Is(err, ErrAlreadyLoggedIn) {
		t.Fatalf("SendCode when ready err = %v", err)
	}
}

func TestLoginNeedsPassword(t *testing.T) {
	f := newFakeTG()
	f.extra = func(req bin.Encoder) (bin.Encoder, error) {
		switch req.(type) {
		case *tg.AuthSignInRequest:
			return nil, &tgerr.Error{Code: 401, Message: "SESSION_PASSWORD_NEEDED", Type: "SESSION_PASSWORD_NEEDED"}
		case *tg.AccountGetPasswordRequest:
			return nil, &tgerr.Error{Code: 400, Message: "PASSWORD_HASH_INVALID", Type: "PASSWORD_HASH_INVALID"}
		}
		return nil, nil
	}
	e := newSvcEnv(t, f, true)
	e.waitState(t, StateLoggedOut)
	if err := e.svc.Password(ctx, "pw"); !errors.Is(err, ErrBadState) {
		t.Fatalf("Password before SignIn err = %v", err)
	}
	e.svc.SendCode(ctx, "+100")
	state, err := e.svc.SignIn(ctx, "12345")
	if err != nil || state != StatePasswordNeeded {
		t.Fatalf("SignIn = %s, %v", state, err)
	}
	var ie *InputError
	if err := e.svc.Password(ctx, "wrong"); !errors.As(err, &ie) || ie.Msg != "二步验证密码错误" {
		t.Fatalf("Password err = %v", err)
	}
	if got := e.svc.Status(ctx).State; got != StatePasswordNeeded {
		t.Fatalf("state after bad password = %s", got)
	}
}

func TestStartsReadyWhenAuthorized(t *testing.T) {
	f := newFakeTG()
	f.authorized = true
	e := newSvcEnv(t, f, true)
	e.waitState(t, StateReady)
	if !e.svc.WaitReady(ctx, 0) {
		t.Fatal("WaitReady false when ready")
	}
	called := false
	if err := e.svc.With(ctx, func(api *tg.Client) error { called = api != nil; return nil }); err != nil || !called {
		t.Fatalf("With = %v, called=%v", err, called)
	}
}

func TestUnauthorizedMarksErrorAndNotifiesOnce(t *testing.T) {
	f := newFakeTG()
	f.authorized = true
	e := newSvcEnv(t, f, true)
	e.waitState(t, StateReady)
	e.st.SaveUserbotSession(ctx, []byte("sealed"), 1)
	f.set(func(f *fakeTG) { f.authorized = false })
	self := func(api *tg.Client) error {
		_, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		return err
	}
	for i := 0; i < 3; i++ {
		if err := e.svc.With(ctx, self); !errors.Is(err, ErrNotReady) {
			t.Fatalf("With #%d err = %v", i, err)
		}
	}
	e.waitState(t, StateError)
	eventually(t, "session dropped", func() bool { u, _ := e.st.GetUserbot(ctx); return u.SessionEnc == nil })
	u, _ := e.st.GetUserbot(ctx)
	if u.Status != store.UserbotError || u.LastError == "" || u.TgUserID != 99 {
		t.Fatalf("db = %+v", u)
	}
	if e.n.count() != 1 {
		t.Fatalf("notifications = %d", e.n.count())
	}
	// Dropping the session kicks off a reconnect (detach -> re-dial -> attach); SendCode needs
	// loginClient() to see a fresh auth.Client again, which only happens once that reconnect's
	// attach() has run. Racing it straight after "session dropped" would intermittently observe
	// the brief in-between window where s.auth is nil and get ErrNotConnected.
	eventually(t, "reconnected", func() bool {
		_, state, err := e.svc.loginClient()
		return err == nil && state == StateError
	})
	// Re-login is allowed from the error state.
	if err := e.svc.SendCode(ctx, "+100"); err != nil {
		t.Fatalf("SendCode from error = %v", err)
	}
}

func TestRevokedWhileOfflineDetectedOnConnect(t *testing.T) {
	f := newFakeTG()
	e := newSvcEnv(t, f, false)
	e.waitState(t, StateUnconfigured)
	e.st.SetUserbotAccount(ctx, "+100", 99, "Me", 1)
	e.creds.Save(ctx, tgapp.Credentials{APIID: 1, APIHash: "0123456789abcdef0123456789abcdef"}, 2)
	e.svc.Reload()
	e.waitState(t, StateError)
	// unauthorized() publishes state before it calls Notify (both on the Run goroutine), so the
	// state flip can be observed here a moment before the notification lands; wait for it rather
	// than asserting the count immediately.
	eventually(t, "notified", func() bool { return e.n.count() == 1 })
}

func TestLogoutClearsAccount(t *testing.T) {
	f := newFakeTG()
	f.authorized = true
	e := newSvcEnv(t, f, true)
	e.waitState(t, StateReady)
	if err := e.svc.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if f.called("*tg.AuthLogOutRequest") != 1 {
		t.Fatal("auth.logOut not called")
	}
	eventually(t, "account cleared", func() bool {
		u, _ := e.st.GetUserbot(ctx)
		return u.Status == store.UserbotLoggedOut && u.TgUserID == 0
	})
	e.waitState(t, StateLoggedOut)
	if e.n.count() != 0 {
		t.Fatalf("logout must not alert, got %d", e.n.count())
	}
}

// TestLogoutDuringConnectDoesNotResurrect exercises the race where Logout() lands while an
// in-flight attach() is still inside auth.Status (s.api/s.auth are only set once that RPC
// returns), so Logout sees "not connected" and takes the direct-clear branch. Before the fix,
// attach() would then see a stale Authorized=true answer and call markReady, silently undoing
// the logout. The fix is a logout generation counter: attach() must notice logoutGen advanced
// while it was waiting on Status and refuse to resurrect the account.
func TestLogoutDuringConnectDoesNotResurrect(t *testing.T) {
	f := newFakeTG()
	f.authorized = true // the underlying session is already authorized when this connect lands

	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, _ := seal.New(bytes.Repeat([]byte{2}, 32))
	creds := tgapp.New(st, box)
	if err := creds.Save(ctx, tgapp.Credentials{APIID: 1, APIHash: "0123456789abcdef0123456789abcdef"}, 1); err != nil {
		t.Fatal(err)
	}
	svc := New(st, box, creds, &fakeDialer{tg: f}, &countNotifier{})

	logoutDone := make(chan struct{})
	var raced bool
	f.extra = func(req bin.Encoder) (bin.Encoder, error) {
		if _, ok := req.(*tg.UsersGetUsersRequest); !ok || raced {
			return nil, nil
		}
		raced = true
		// Logout() races with the in-flight Status() call: attach() hasn't returned from
		// a.Status yet, so s.api/s.auth are still nil and Logout takes the "not connected"
		// branch (no auth.logOut RPC, direct ClearUserbot).
		if err := svc.Logout(ctx); err != nil {
			t.Errorf("Logout during connect = %v", err)
		}
		close(logoutDone)
		// From here on, treat the account as genuinely logged out: a real reconnect would
		// start a brand-new MTProto session once clearAll wipes session_enc, which this fake
		// (unlike GotdDialer) doesn't otherwise model. This call's answer is still the stale
		// "authorized" one computed before the race, matching the window being tested.
		f.authorized = false
		return &tg.UserClassVector{Elems: []tg.UserClass{f.user}}, nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.Run(runCtx); close(done) }()
	defer func() { cancel(); <-done }()

	<-logoutDone
	eventually(t, "state logged_out", func() bool { return svc.Status(ctx).State == StateLoggedOut })
	eventually(t, "account cleared", func() bool {
		u, _ := st.GetUserbot(ctx)
		return u.Status == store.UserbotLoggedOut && u.TgUserID == 0 && u.SessionEnc == nil
	})
	// Give any wrongly-queued markReady a moment to land, then confirm it never does.
	time.Sleep(50 * time.Millisecond)
	if got := svc.Status(ctx).State; got != StateLoggedOut {
		t.Fatalf("state after race = %s (logout was undone)", got)
	}
	u, _ := st.GetUserbot(ctx)
	if u.Status != store.UserbotLoggedOut || u.TgUserID != 0 {
		t.Fatalf("db after race = %+v (logout was undone)", u)
	}
}

// TestLogoutWhileDisconnectedDoesNotWipeFutureLogin reproduces a round-1-fix regression: Logout()
// called while nothing was connected armed s.clear = clearAll (so an in-flight attach() wouldn't
// resurrect a stale "authorized" answer) but never disarmed it itself, relying on some future
// detach() to consume it. If no detach() happens before a completely unrelated future login
// succeeds, the stale clearAll sits pending and wipes that fresh account and session the next
// time any disconnect (or shutdown) reaches detach() — logging the user back out and discarding
// their session even though they never asked to log out again.
func TestLogoutWhileDisconnectedDoesNotWipeFutureLogin(t *testing.T) {
	f := newFakeTG()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, _ := seal.New(bytes.Repeat([]byte{2}, 32))
	creds := tgapp.New(st, box)
	if err := creds.Save(ctx, tgapp.Credentials{APIID: 1, APIHash: "0123456789abcdef0123456789abcdef"}, 1); err != nil {
		t.Fatal(err)
	}
	svc := New(st, box, creds, &fakeDialer{tg: f}, &countNotifier{})

	// Logout while genuinely disconnected: Run() hasn't even started yet, so s.api is nil and
	// Logout takes the direct-clear branch.
	if err := svc.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	// Drain the Reload() that Logout() just queued. Run() hasn't started, so nothing has
	// consumed it yet; left in place, the very first connect cycle would immediately pick it
	// up and disconnect/reconnect once on its own, which happens to run a detach() early (before
	// the fresh login below) and would accidentally disarm the stale clearAll by coincidence —
	// masking the bug this test exists to catch, regardless of whether the fix is applied.
	select {
	case <-svc.reload:
	default:
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.Run(runCtx); close(done) }()
	// Wait for the connect cycle's attach() to actually finish (loginClient() needs s.auth
	// set), not merely for the in-memory state to read "logged_out" — Logout() itself already
	// set that same state directly before Run() even started, so checking state alone would be
	// satisfied instantly and wouldn't actually wait for attach().
	eventually(t, "connected and logged_out", func() bool {
		_, state, err := svc.loginClient()
		return err == nil && state == StateLoggedOut
	})

	// A fresh, unrelated login completes normally.
	if err := svc.SendCode(ctx, "+100"); err != nil {
		t.Fatalf("SendCode = %v", err)
	}
	state, err := svc.SignIn(ctx, "12345")
	if err != nil || state != StateReady {
		t.Fatalf("SignIn = %s, %v", state, err)
	}
	eventually(t, "state ready", func() bool { return svc.Status(ctx).State == StateReady })
	// A live connection would have gotd persist the account's auth key via session.Storage as a
	// side effect; this fake dialer ignores session.Storage entirely, so seed it directly
	// through the same sessionStore the service uses, to also exercise "session still present".
	if err := svc.sess.StoreSession(ctx, []byte("auth-key-bytes")); err != nil {
		t.Fatal(err)
	}

	// Force a disconnect: shut the service down. detach() runs unconditionally right after Dial
	// returns no matter why, and on shutdown Run() returns immediately afterward with no
	// healing reconnect — if the stale clearAll from the earlier Logout is still armed, this is
	// exactly where it wipes an account and session that have no business being touched.
	cancel()
	<-done

	if got := svc.Status(ctx).State; got != StateReady {
		t.Fatalf("state after shutdown = %s, want %s (a stale clearAll must not fire)", got, StateReady)
	}
	u, err := st.GetUserbot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u.Status != store.UserbotReady || u.TgUserID != 99 || len(u.SessionEnc) == 0 {
		t.Fatalf("account/session wiped by a stale clearAll from the earlier Logout: %+v", u)
	}
}

// TestConcurrentUnauthorizedAlertsOnce exercises several concurrent 401s all triggering
// unauthorized() at once. The check-and-set for "already handled" must happen under the same
// lock acquisition as the read, or two callers can both pass the check before either sets the
// in-memory state and both alert.
func TestConcurrentUnauthorizedAlertsOnce(t *testing.T) {
	f := newFakeTG()
	f.authorized = true
	e := newSvcEnv(t, f, true)
	e.waitState(t, StateReady)
	f.set(func(f *fakeTG) { f.authorized = false })
	self := func(api *tg.Client) error {
		_, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		return err
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.svc.With(ctx, self)
		}()
	}
	wg.Wait()
	e.waitState(t, StateError)
	eventually(t, "notified", func() bool { return e.n.count() >= 1 })
	// Give any duplicate alert from a losing concurrent caller a chance to land.
	time.Sleep(50 * time.Millisecond)
	if n := e.n.count(); n != 1 {
		t.Fatalf("notifications = %d, want exactly 1", n)
	}
}

func TestSessionStoreEncrypts(t *testing.T) {
	e := newSvcEnv(t, newFakeTG(), false)
	ss := &sessionStore{st: e.st, box: e.box, now: e.svc.Now}
	if _, err := ss.LoadSession(ctx); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("empty load err = %v", err)
	}
	if err := ss.StoreSession(ctx, []byte(`{"auth":"secret"}`)); err != nil {
		t.Fatal(err)
	}
	u, _ := e.st.GetUserbot(ctx)
	if len(u.SessionEnc) == 0 || string(u.SessionEnc) == `{"auth":"secret"}` {
		t.Fatalf("session stored in clear: %q", u.SessionEnc)
	}
	got, err := ss.LoadSession(ctx)
	if err != nil || string(got) != `{"auth":"secret"}` {
		t.Fatalf("load = %q, %v", got, err)
	}
}
