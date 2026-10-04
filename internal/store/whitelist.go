package store

import (
	"context"
	"database/sql"
	"errors"
)

type WhitelistEntry struct {
	BotID, TgUserID int64
	Note            string
	CanFetch        bool
}

type Rejected struct {
	BotID, TgUserID     int64
	FirstName, Username string
	LastSeenAt, Count   int64
}

func (s *Store) CheckAllowed(ctx context.Context, botID, userID int64) (bool, bool, error) {
	var canFetch bool
	err := s.db.QueryRowContext(ctx, "SELECT can_fetch FROM whitelist WHERE bot_id = ? AND tg_user_id = ?", botID, userID).Scan(&canFetch)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, canFetch, nil
}

func (s *Store) ListWhitelist(ctx context.Context, botID int64) ([]WhitelistEntry, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT bot_id, tg_user_id, note, can_fetch FROM whitelist WHERE bot_id = ? ORDER BY tg_user_id", botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WhitelistEntry{}
	for rows.Next() {
		var e WhitelistEntry
		if err := rows.Scan(&e.BotID, &e.TgUserID, &e.Note, &e.CanFetch); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) PutWhitelist(ctx context.Context, e WhitelistEntry) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO whitelist (bot_id, tg_user_id, note, can_fetch) VALUES (?, ?, ?, ?)
			ON CONFLICT (bot_id, tg_user_id) DO UPDATE SET note = excluded.note, can_fetch = excluded.can_fetch`,
			e.BotID, e.TgUserID, e.Note, e.CanFetch); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM rejected WHERE bot_id = ? AND tg_user_id = ?", e.BotID, e.TgUserID)
		return err
	})
}

func (s *Store) DeleteWhitelist(ctx context.Context, botID, userID int64) error {
	return affected(s.db.ExecContext(ctx, "DELETE FROM whitelist WHERE bot_id = ? AND tg_user_id = ?", botID, userID))
}

func (s *Store) RecordRejected(ctx context.Context, r Rejected) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rejected (bot_id, tg_user_id, first_name, username, last_seen_at, count) VALUES (?, ?, ?, ?, ?, 1)
		ON CONFLICT (bot_id, tg_user_id) DO UPDATE SET
			first_name = excluded.first_name, username = excluded.username,
			last_seen_at = excluded.last_seen_at, count = rejected.count + 1`,
		r.BotID, r.TgUserID, r.FirstName, r.Username, r.LastSeenAt)
	return err
}

func (s *Store) ListRejected(ctx context.Context, botID int64) ([]Rejected, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT bot_id, tg_user_id, first_name, username, last_seen_at, count
		FROM rejected WHERE bot_id = ? ORDER BY last_seen_at DESC LIMIT 50`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rejected{}
	for rows.Next() {
		var r Rejected
		if err := rows.Scan(&r.BotID, &r.TgUserID, &r.FirstName, &r.Username, &r.LastSeenAt, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
