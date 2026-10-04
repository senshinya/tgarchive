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
