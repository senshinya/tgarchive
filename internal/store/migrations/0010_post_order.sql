-- A channel conversation is ordered by its posts' Telegram message ids (the order they were
-- published), not by when they were archived: a backfill or a late hit lands where the post
-- belongs. Comments likewise go by their discussion message ids. The read marker follows: it is
-- the message id of the last post read, and only posts after it are unread.
ALTER TABLE chats ADD COLUMN last_read_pos INTEGER NOT NULL DEFAULT 0;
UPDATE chats SET last_read_pos = COALESCE((SELECT MAX(m.tg_message_id) FROM messages m
  WHERE m.chat_id = chats.id AND m.source = 'channel_watch' AND m.id <= chats.last_read_id), 0)
  WHERE kind = 'channel';
ALTER TABLE chats DROP COLUMN last_read_id;
CREATE INDEX messages_chat_pos ON messages (chat_id, deleted_at, tg_message_id, id);
CREATE INDEX messages_thread_pos ON messages (thread_root_id, tg_message_id, id) WHERE thread_root_id != 0;
