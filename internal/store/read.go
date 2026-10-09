package store

import "context"

// MarkRead records that a channel conversation has been read up to message messageID (up to its
// post, in the order posts were published); it never moves back. Other chats have no unread count.
func (s *Store) MarkRead(ctx context.Context, chatID, messageID int64) error {
	return affected(s.db.ExecContext(ctx, `UPDATE chats SET last_read_pos = MAX(last_read_pos,
		COALESCE((SELECT tg_message_id FROM messages WHERE id = ? AND chat_id = chats.id AND source = 'channel_watch'), 0))
		WHERE id = ?`, messageID, chatID))
}
