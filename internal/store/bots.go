package store

import (
	"context"
	"database/sql"
	"errors"
)

type Bot struct {
	ID, TgBotID    int64
	Username, Name string
	AvatarPath     string
	TokenEnc       []byte
	Enabled        bool
	Status         string
	LastError      string
	UpdateOffset   int64
	CreatedAt      int64
}

const botCols = "id, tg_bot_id, username, name, avatar_path, token_enc, enabled, status, last_error, update_offset, created_at"

func scanBot(r scanner) (*Bot, error) {
	var b Bot
	err := r.Scan(&b.ID, &b.TgBotID, &b.Username, &b.Name, &b.AvatarPath, &b.TokenEnc, &b.Enabled, &b.Status, &b.LastError, &b.UpdateOffset, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// UpsertBot inserts a bot or reactivates the existing row with the same tg_bot_id.
// Callers must reject duplicates of non-removed bots before calling.
func (s *Store) UpsertBot(ctx context.Context, b *Bot) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO bots (tg_bot_id, username, name, token_enc, enabled, status, last_error, created_at)
		VALUES (?, ?, ?, ?, 1, 'stopped', '', ?)
		ON CONFLICT (tg_bot_id) DO UPDATE SET
			username = excluded.username, name = excluded.name, token_enc = excluded.token_enc,
			enabled = 1, status = 'stopped', last_error = ''
		RETURNING id`, b.TgBotID, b.Username, b.Name, b.TokenEnc, b.CreatedAt).Scan(&id)
	return id, err
}

func (s *Store) GetBot(ctx context.Context, id int64) (*Bot, error) {
	return scanBot(s.db.QueryRowContext(ctx, "SELECT "+botCols+" FROM bots WHERE id = ?", id))
}

func (s *Store) GetBotByTgID(ctx context.Context, tgID int64) (*Bot, error) {
	return scanBot(s.db.QueryRowContext(ctx, "SELECT "+botCols+" FROM bots WHERE tg_bot_id = ?", tgID))
}

func (s *Store) ListBots(ctx context.Context) ([]Bot, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+botCols+" FROM bots ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bot{}
	for rows.Next() {
		b, err := scanBot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (s *Store) SetBotStatus(ctx context.Context, id int64, status, lastError string) error {
	return affected(s.db.ExecContext(ctx, "UPDATE bots SET status = ?, last_error = ? WHERE id = ?", status, lastError, id))
}

func (s *Store) SetBotEnabled(ctx context.Context, id int64, enabled bool) error {
	return affected(s.db.ExecContext(ctx, "UPDATE bots SET enabled = ? WHERE id = ? AND status != 'removed'", enabled, id))
}

func (s *Store) SetBotAvatar(ctx context.Context, id int64, path string) error {
	return affected(s.db.ExecContext(ctx, "UPDATE bots SET avatar_path = ? WHERE id = ?", path, id))
}

func (s *Store) AdvanceOffset(ctx context.Context, id, offset int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE bots SET update_offset = ? WHERE id = ? AND update_offset < ?", offset, id, offset)
	return err
}

// RemoveBot keeps the archive but wipes the token and hides the bot from polling.
func (s *Store) RemoveBot(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx,
		"UPDATE bots SET enabled = 0, status = 'removed', token_enc = x'', last_error = '' WHERE id = ?", id))
}
