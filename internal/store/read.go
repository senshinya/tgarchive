package store

import "context"

// MarkRead records that a conversation has been read up to messageID; it never moves back.
func (s *Store) MarkRead(ctx context.Context, chatID, messageID int64) error {
	return affected(s.db.ExecContext(ctx, "UPDATE chats SET last_read_id = MAX(last_read_id, ?) WHERE id = ?", messageID, chatID))
}
