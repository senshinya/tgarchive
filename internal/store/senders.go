package store

import "context"

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
