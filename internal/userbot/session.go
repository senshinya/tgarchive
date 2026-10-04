package userbot

import (
	"context"
	"errors"
	"time"

	"github.com/gotd/td/session"

	"tgarchive/internal/seal"
	"tgarchive/internal/store"
)

// sessionStore keeps the gotd session (the account's auth key) sealed with TOKEN_ENC_KEY.
type sessionStore struct {
	st  *store.Store
	box *seal.Box
	now func() time.Time
}

func (s *sessionStore) LoadSession(ctx context.Context) ([]byte, error) {
	u, err := s.st.GetUserbot(ctx)
	if err != nil {
		return nil, err
	}
	if len(u.SessionEnc) == 0 {
		return nil, session.ErrNotFound
	}
	plain, err := s.box.Open(u.SessionEnc)
	if err != nil {
		return nil, errors.New("cannot decrypt userbot session; TOKEN_ENC_KEY changed?")
	}
	return plain, nil
}

func (s *sessionStore) StoreSession(ctx context.Context, data []byte) error {
	return s.st.SaveUserbotSession(ctx, s.box.Seal(data), s.now().Unix())
}

// connSession is the session storage handed to one connection. Once a Logout() has happened
// since that connection was dialed (gen is stale), it no longer persists the connection's auth
// key: gotd re-saves the key it loaded during setup, which would otherwise resurrect the session
// Logout just cleared. The check and the write share one critical section with Logout's
// generation bump, so the clear always lands after any write that passed the check.
type connSession struct {
	s   *Service
	gen uint64
}

func (c *connSession) LoadSession(ctx context.Context) ([]byte, error) {
	return c.s.sess.LoadSession(ctx)
}

func (c *connSession) StoreSession(ctx context.Context, data []byte) error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if c.s.logoutGen != c.gen {
		return nil
	}
	return c.s.sess.StoreSession(ctx, data)
}
