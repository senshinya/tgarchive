package store

import (
	"context"
	"database/sql"
	"errors"
)

const (
	UserbotLoggedOut = "logged_out"
	UserbotReady     = "ready"
	UserbotError     = "error"
)

type Userbot struct {
	Phone      string
	TgUserID   int64
	Name       string
	SessionEnc []byte
	Status     string
	LastError  string
	UpdatedAt  int64
}

func (s *Store) GetUserbot(ctx context.Context) (*Userbot, error) {
	var u Userbot
	err := s.db.QueryRowContext(ctx, "SELECT phone, tg_user_id, name, session_enc, status, last_error, updated_at FROM userbot WHERE id = 1").
		Scan(&u.Phone, &u.TgUserID, &u.Name, &u.SessionEnc, &u.Status, &u.LastError, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return &Userbot{Status: UserbotLoggedOut}, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) SaveUserbotSession(ctx context.Context, enc []byte, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO userbot (id, session_enc, updated_at) VALUES (1, ?, ?)
		ON CONFLICT (id) DO UPDATE SET session_enc = excluded.session_enc, updated_at = excluded.updated_at`, enc, now)
	return err
}

func (s *Store) SetUserbotAccount(ctx context.Context, phone string, tgUserID int64, name string, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO userbot (id, phone, tg_user_id, name, status, last_error, updated_at) VALUES (1, ?, ?, ?, 'ready', '', ?)
		ON CONFLICT (id) DO UPDATE SET phone = excluded.phone, tg_user_id = excluded.tg_user_id, name = excluded.name,
			status = 'ready', last_error = '', updated_at = excluded.updated_at`, phone, tgUserID, name, now)
	return err
}

func (s *Store) SetUserbotStatus(ctx context.Context, status, lastErr string, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO userbot (id, status, last_error, updated_at) VALUES (1, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET status = excluded.status, last_error = excluded.last_error, updated_at = excluded.updated_at`,
		status, lastErr, now)
	return err
}

func (s *Store) ClearUserbot(ctx context.Context, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO userbot (id, updated_at) VALUES (1, ?)
		ON CONFLICT (id) DO UPDATE SET phone = '', tg_user_id = 0, name = '', session_enc = NULL,
			status = 'logged_out', last_error = '', updated_at = excluded.updated_at`, now)
	return err
}

// Peer caches the access hash of a channel the userbot account can see. Private links (t.me/c/<id>)
// carry no access hash, so it must come from the account's own dialogs.
type Peer struct {
	ChannelID  int64
	AccessHash int64
	Username   string
	Title      string
}

func (s *Store) PutPeers(ctx context.Context, peers []Peer, now int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, p := range peers {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO userbot_peers (channel_id, access_hash, username, title, updated_at) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (channel_id) DO UPDATE SET access_hash = excluded.access_hash, username = excluded.username,
					title = excluded.title, updated_at = excluded.updated_at`,
				p.ChannelID, p.AccessHash, p.Username, p.Title, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) GetPeer(ctx context.Context, channelID int64) (*Peer, error) {
	p := Peer{ChannelID: channelID}
	err := s.db.QueryRowContext(ctx, "SELECT access_hash, username, title FROM userbot_peers WHERE channel_id = ?", channelID).
		Scan(&p.AccessHash, &p.Username, &p.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
