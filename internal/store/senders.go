package store

import (
	"context"
	"database/sql"
	"errors"

	"tgarchive/internal/model"
)

type ChatSender struct{ BotID, TgUserID int64 }

func (s *Store) SetSenderAvatar(ctx context.Context, tgUserID int64, path string) error {
	return affected(s.db.ExecContext(ctx, "UPDATE senders SET avatar_path = ? WHERE tg_user_id = ?", path, tgUserID))
}

// ChatSenders returns one (bot, sender) pair per sender, using a bot that can still make API calls.
func (s *Store) ChatSenders(ctx context.Context) ([]ChatSender, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT MIN(c.bot_id), c.sender_id FROM chats c JOIN bots b ON b.id = c.bot_id
		WHERE b.status != 'removed' GROUP BY c.sender_id ORDER BY c.sender_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatSender{}
	for rows.Next() {
		var cs ChatSender
		if err := rows.Scan(&cs.BotID, &cs.TgUserID); err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

func (s *Store) GetSender(ctx context.Context, tgUserID int64) (model.Sender, error) {
	snd := model.Sender{TgUserID: tgUserID}
	err := s.db.QueryRowContext(ctx, "SELECT first_name, last_name, username FROM senders WHERE tg_user_id = ?", tgUserID).
		Scan(&snd.FirstName, &snd.LastName, &snd.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Sender{}, ErrNotFound
	}
	return snd, err
}

func (s *Store) UpsertSender(ctx context.Context, snd model.Sender, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO senders (tg_user_id, first_name, last_name, username, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (tg_user_id) DO UPDATE SET first_name = excluded.first_name, last_name = excluded.last_name,
			username = excluded.username, updated_at = excluded.updated_at`,
		snd.TgUserID, snd.FirstName, snd.LastName, snd.Username, now)
	return err
}
