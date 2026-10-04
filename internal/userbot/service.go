// Package userbot runs the MTProto user account that fetches messages bots cannot see
// (forward-protected posts), and its web-driven login.
package userbot

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/notify"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgapp"
)

const (
	StateUnconfigured   = "unconfigured"
	StateConnecting     = "connecting"
	StateLoggedOut      = "logged_out"
	StateCodeSent       = "code_sent"
	StatePasswordNeeded = "password_needed"
	StateReady          = "ready"
	StateError          = "error"
)

const msgRevoked = "代取账号登录已失效，请重新登录"

var (
	ErrNotConfigured   = errors.New("请先在设置中填写 api_id / api_hash")
	ErrNotConnected    = errors.New("代取账号尚未连接到 Telegram，请稍后重试")
	ErrNotReady        = errors.New("代取账号未登录")
	ErrBadState        = errors.New("登录步骤已失效，请从输入手机号重新开始")
	ErrAlreadyLoggedIn = errors.New("已登录，如需更换账号请先登出")
)

// InputError is a login error caused by what the user typed; it is safe to show verbatim.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

type Info struct {
	State    string `json:"state"`
	Phone    string `json:"phone"`
	Name     string `json:"name"`
	TgUserID int64  `json:"tg_user_id"`
	Error    string `json:"error"`
}

type CredsLoader interface {
	Load(ctx context.Context) (*tgapp.Credentials, error)
}

type clearMode int

const (
	clearNone    clearMode = iota
	clearSession           // session revoked: drop the auth key, keep account + error status
	clearAll               // logout: forget everything
)

type Service struct {
	st       *store.Store
	sess     *sessionStore
	creds    CredsLoader
	dialer   Dialer
	notifier notify.Notifier

	Now        func() time.Time
	MaxBackoff time.Duration

	reload chan struct{}

	mu        sync.Mutex
	api       *tg.Client
	auth      *auth.Client
	state     string
	lastErr   string
	phone     string
	codeHash  string
	clear     clearMode
	logoutGen uint64
}

func New(st *store.Store, box *seal.Box, creds CredsLoader, d Dialer, n notify.Notifier) *Service {
	s := &Service{st: st, creds: creds, dialer: d, notifier: n, Now: time.Now, MaxBackoff: time.Minute,
		reload: make(chan struct{}, 1), state: StateConnecting}
	s.sess = &sessionStore{st: st, box: box, now: func() time.Time { return s.Now() }}
	return s
}

// Reload drops the current connection and reconnects with freshly loaded credentials.
func (s *Service) Reload() {
	select {
	case s.reload <- struct{}{}:
	default:
	}
}

func (s *Service) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		creds, err := s.creds.Load(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, store.ErrNotFound) {
				s.setState(StateUnconfigured, "")
			} else {
				s.setState(StateError, err.Error())
			}
			select {
			case <-ctx.Done():
				return
			case <-s.reload:
			}
			continue
		}
		s.setState(StateConnecting, "")
		// gen pins this connection to the logout generation current before dialing: gotd loads
		// (and re-saves) the session inside Dial, so a Logout landing anywhere from here on must
		// be able to tell this connection's work apart from that of a post-logout one.
		s.mu.Lock()
		gen := s.logoutGen
		s.mu.Unlock()
		attached := false
		err = s.dialer.Dial(ctx, *creds, &connSession{s: s, gen: gen}, func(cctx context.Context, api *tg.Client) error {
			if err := s.attach(cctx, api, creds, gen); err != nil {
				return err
			}
			attached = true
			select {
			case <-cctx.Done():
				return cctx.Err()
			case <-s.reload:
				return nil
			}
		})
		s.detach(context.WithoutCancel(ctx))
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			backoff = time.Second
			continue
		}
		// Only reset backoff once attach has actually succeeded; a dial that reaches the
		// callback but fails inside attach() every time (e.g. a persistent, non-transport
		// error) must still back off exponentially instead of hammering Telegram once a
		// second forever.
		if attached {
			backoff = time.Second
		}
		log.Printf("userbot: connection: %v (retry in %s)", err, backoff)
		s.setState(StateConnecting, err.Error())
		select {
		case <-ctx.Done():
			return
		case <-s.reload:
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, s.MaxBackoff)
	}
}

// attach adopts a fresh connection. gen is the logout generation captured before dialing it.
func (s *Service) attach(ctx context.Context, api *tg.Client, creds *tgapp.Credentials, gen uint64) error {
	a := auth.NewClient(api, rand.Reader, creds.APIID, creds.APIHash)
	st, err := a.Status(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.api, s.auth = api, a
	s.mu.Unlock()
	if st.Authorized {
		err := s.markReady(ctx, gen, st.User, "")
		if errors.Is(err, ErrBadState) {
			// Logout() ran since this connection was dialed: markReady has revoked the stale
			// authorization and armed clearAll for detach(); stay logged out.
			s.setState(StateLoggedOut, "")
			return nil
		}
		return err
	}
	u, err := s.st.GetUserbot(ctx)
	if err != nil {
		return err
	}
	switch u.Status {
	case store.UserbotReady:
		s.unauthorized(ctx, gen) // the session was revoked while we were offline
	case store.UserbotError:
		s.setState(StateError, u.LastError)
	default:
		s.setState(StateLoggedOut, "")
	}
	return nil
}

// detach forgets the connection and applies any pending session clean-up.
func (s *Service) detach(ctx context.Context) {
	s.mu.Lock()
	s.api, s.auth = nil, nil
	mode := s.clear
	s.clear = clearNone
	s.mu.Unlock()
	var err error
	switch mode {
	case clearSession:
		err = s.st.SaveUserbotSession(ctx, nil, s.Now().Unix())
	case clearAll:
		err = s.st.ClearUserbot(ctx, s.Now().Unix())
	}
	if err != nil {
		log.Printf("userbot: clear session: %v", err)
	}
}

// markReady records a successful authorization obtained under logout generation gen. If a
// Logout() has happened since (gen is stale), the authorization is revoked instead and
// ErrBadState is returned: the user asked to be logged out, and Telegram may have re-authorized
// the key anyway (e.g. auth.logOut processed before an in-flight auth.signIn).
func (s *Service) markReady(ctx context.Context, gen uint64, u *tg.User, phone string) error {
	if u.Phone != "" {
		phone = "+" + strings.TrimPrefix(u.Phone, "+")
	} else if phone == "" {
		// The self user may hide its phone; keep the one recorded at login.
		if cur, err := s.st.GetUserbot(ctx); err == nil {
			phone = cur.Phone
		}
	}
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)

	s.mu.Lock()
	if s.logoutGen != gen {
		api := s.api
		if api != nil {
			s.clear = clearAll // a detach() is still coming on this connection
		}
		s.mu.Unlock()
		if api != nil {
			if _, err := api.AuthLogOut(ctx); err != nil {
				log.Printf("userbot: auth.logOut (stale login): %v", err)
			}
			s.Reload()
		}
		return ErrBadState
	}
	// mu is held across the write so a concurrent Logout() is ordered strictly before (gen
	// check above) or after it (its ClearUserbot then wins). No path holds the single DB
	// connection while waiting for mu, so this cannot deadlock.
	defer s.mu.Unlock()
	if err := s.st.SetUserbotAccount(ctx, phone, u.ID, name, s.Now().Unix()); err != nil {
		return err
	}
	// Defense in depth: a fresh successful login must never be undone by a stale pending
	// clear armed by an earlier logout/revocation that never got consumed by a detach()
	// (see Logout's api == nil branch, which is the normal way this gets disarmed).
	s.state, s.lastErr, s.phone, s.codeHash, s.clear = StateReady, "", "", "", clearNone
	return nil
}

// unauthorized handles a revoked session once: persist the error, alert, drop the auth key, reconnect.
//
// The dedupe check-and-set happens under mu in one critical section, so two concurrent 401s
// (e.g. two in-flight With() calls) can't both pass the check before either sets the in-memory
// state: only the first to acquire the lock proceeds past it. The DB write below is still the
// persisted source of truth for status/last_error observed across reconnects and restarts.
//
// gen is the logout generation the failing call ran under: a 401 caused by our own Logout()
// (which revokes the key mid-call) is not a revocation and must not overwrite logged_out.
func (s *Service) unauthorized(ctx context.Context, gen uint64) {
	s.mu.Lock()
	if s.logoutGen != gen || s.state == StateError {
		s.mu.Unlock()
		return
	}
	s.state, s.lastErr, s.clear = StateError, msgRevoked, clearSession
	s.mu.Unlock()

	if err := s.st.SetUserbotStatus(ctx, store.UserbotError, msgRevoked, s.Now().Unix()); err != nil {
		log.Printf("userbot: save status: %v", err)
	}
	s.Reload()
	if s.notifier != nil {
		s.notifier.Notify(ctx, "tgarchive 代取账号失效", msgRevoked)
	}
}

func (s *Service) setState(state, lastErr string) {
	s.mu.Lock()
	s.state, s.lastErr = state, lastErr
	s.mu.Unlock()
}

func (s *Service) Status(ctx context.Context) Info {
	s.mu.Lock()
	info := Info{State: s.state, Error: s.lastErr, Phone: s.phone}
	s.mu.Unlock()
	if u, err := s.st.GetUserbot(ctx); err == nil {
		if u.Phone != "" {
			info.Phone = u.Phone
		}
		info.Name, info.TgUserID = u.Name, u.TgUserID
	}
	return info
}

// With runs fn against the logged-in account. A 401 from Telegram marks the session revoked.
func (s *Service) With(ctx context.Context, fn func(api *tg.Client) error) error {
	s.mu.Lock()
	api, state, gen := s.api, s.state, s.logoutGen
	s.mu.Unlock()
	if api == nil || state != StateReady {
		return ErrNotReady
	}
	err := fn(api)
	if err != nil && auth.IsUnauthorized(err) {
		s.unauthorized(context.WithoutCancel(ctx), gen)
		return ErrNotReady
	}
	return err
}

// WaitReady waits up to max for an in-progress connection; other states return immediately.
func (s *Service) WaitReady(ctx context.Context, max time.Duration) bool {
	deadline := time.Now().Add(max)
	for {
		s.mu.Lock()
		state := s.state
		s.mu.Unlock()
		if state == StateReady {
			return true
		}
		if state != StateConnecting || !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// loginClient returns the connection's auth client, the current state and the logout generation
// the login step runs under.
func (s *Service) loginClient() (*auth.Client, string, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == StateUnconfigured {
		return nil, "", 0, ErrNotConfigured
	}
	if s.auth == nil {
		return nil, "", 0, ErrNotConnected
	}
	return s.auth, s.state, s.logoutGen, nil
}

func (s *Service) SendCode(ctx context.Context, phone string) error {
	a, state, _, err := s.loginClient()
	if err != nil {
		return err
	}
	if state == StateReady {
		return ErrAlreadyLoggedIn
	}
	sent, err := a.SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return inputErr(err)
	}
	sc, ok := sent.(*tg.AuthSentCode)
	if !ok {
		return fmt.Errorf("unexpected sent code type %T", sent)
	}
	s.mu.Lock()
	s.phone, s.codeHash, s.state, s.lastErr = phone, sc.PhoneCodeHash, StateCodeSent, ""
	s.mu.Unlock()
	return nil
}

func (s *Service) SignIn(ctx context.Context, code string) (string, error) {
	a, state, gen, err := s.loginClient()
	if err != nil {
		return "", err
	}
	if state != StateCodeSent {
		return "", ErrBadState
	}
	s.mu.Lock()
	phone, hash := s.phone, s.codeHash
	s.mu.Unlock()
	authz, err := a.SignIn(ctx, phone, code, hash)
	if errors.Is(err, auth.ErrPasswordAuthNeeded) {
		s.setState(StatePasswordNeeded, "")
		return StatePasswordNeeded, nil
	}
	if err != nil {
		return "", inputErr(err)
	}
	return StateReady, s.finish(ctx, gen, authz, phone)
}

func (s *Service) Password(ctx context.Context, password string) error {
	a, state, gen, err := s.loginClient()
	if err != nil {
		return err
	}
	if state != StatePasswordNeeded {
		return ErrBadState
	}
	authz, err := a.Password(ctx, password)
	if err != nil {
		return inputErr(err)
	}
	s.mu.Lock()
	phone := s.phone
	s.mu.Unlock()
	return s.finish(ctx, gen, authz, phone)
}

func (s *Service) finish(ctx context.Context, gen uint64, authz *tg.AuthAuthorization, phone string) error {
	u, ok := authz.User.(*tg.User)
	if !ok {
		return fmt.Errorf("unexpected user type %T", authz.User)
	}
	return s.markReady(ctx, gen, u, phone)
}

func (s *Service) Logout(ctx context.Context) error {
	s.mu.Lock()
	api := s.api
	s.logoutGen++
	s.clear = clearAll
	s.state, s.lastErr, s.phone, s.codeHash = StateLoggedOut, "", "", ""
	s.mu.Unlock()
	if api != nil {
		if _, err := api.AuthLogOut(ctx); err != nil {
			log.Printf("userbot: auth.logOut: %v", err)
		}
	}
	// Always nudge the connection loop so a pending clearAll (armed above) gets applied by
	// detach() even if nothing was connected yet (e.g. a connection is mid-setup right now:
	// its markReady will see logoutGen advance and revoke a stale "authorized" answer).
	// With nothing connected, the store is cleared first: a loop woken from its backoff by
	// this Reload must not load the old session before it is gone.
	if api != nil {
		s.Reload()
	}
	if api == nil {
		// Nothing was connected, so no detach() is coming to consume the clearAll armed above
		// on our behalf. Clear directly, then disarm the flag ourselves — but only if still
		// nothing is connected: a connection that attached meanwhile may have armed clearAll
		// for its own (stale-login) clean-up, which its detach() must still apply. Left armed
		// with nothing attached, it would instead wipe an unrelated *future* login the next
		// time any disconnect (or shutdown) reaches detach().
		err := s.st.ClearUserbot(ctx, s.Now().Unix())
		s.mu.Lock()
		if s.api == nil {
			s.clear = clearNone
		}
		s.mu.Unlock()
		s.Reload()
		return err
	}
	return nil
}

var inputMsgs = map[string]string{
	"PHONE_NUMBER_INVALID":    "手机号格式不正确",
	"PHONE_NUMBER_BANNED":     "该手机号已被 Telegram 封禁",
	"PHONE_NUMBER_UNOCCUPIED": "该手机号尚未注册 Telegram",
	"PHONE_CODE_INVALID":      "验证码错误",
	"PHONE_CODE_EMPTY":        "验证码错误",
	"PHONE_CODE_EXPIRED":      "验证码已过期，请重新获取",
	"PASSWORD_HASH_INVALID":   "二步验证密码错误",
	"API_ID_INVALID":          "api_id / api_hash 无效",
}

func inputErr(err error) error {
	if d, ok := tgerr.AsFloodWait(err); ok {
		return &InputError{Msg: fmt.Sprintf("操作过于频繁，请 %d 分钟后重试", ceilMinutes(d))}
	}
	if rpc, ok := tgerr.As(err); ok {
		if msg, ok := inputMsgs[rpc.Type]; ok {
			return &InputError{Msg: msg}
		}
	}
	return err
}

func ceilMinutes(d time.Duration) int { return max(1, int(math.Ceil(d.Minutes()))) }
