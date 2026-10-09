-- A channel's timeline is its posts, but messages_chat_pos holds its comments too, ten or more for
-- every post, numbered after them: reading the newest page walked past every comment first.
-- messages_posts puts a conversation's posts (thread_root_id = 0) apart from its comments, live
-- ones apart from deleted, each in timeline order. It is a full index, not a partial one: SQLite
-- takes a partial index for a cheap full scan when a database has statistics, and would then
-- answer a search by scanning every message. messages_chat_pos stays for looking a message up by
-- its Telegram id.
CREATE INDEX messages_posts ON messages (chat_id, thread_root_id, deleted_at, tg_message_id, id);

-- messages_thread_pos (thread_root_id, tg_message_id, id) answers everything messages_thread did.
DROP INDEX messages_thread;
