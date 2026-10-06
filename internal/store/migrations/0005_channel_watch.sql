CREATE TABLE channels (
  channel_id  INTEGER PRIMARY KEY,
  title       TEXT    NOT NULL DEFAULT '',
  username    TEXT    NOT NULL DEFAULT '',
  avatar_path TEXT    NOT NULL DEFAULT '',
  updated_at  INTEGER NOT NULL
);

-- chats gains a kind: a bot × sender chat ('private'), or a watched channel ('channel'), which
-- belongs to no bot. Rebuilt the SQLite way (copy, drop, rename) with foreign keys off, so the
-- drop does not cascade into messages.
CREATE TABLE chats_new (
  id              INTEGER PRIMARY KEY,
  kind            TEXT    NOT NULL DEFAULT 'private',
  bot_id          INTEGER REFERENCES bots(id) ON DELETE CASCADE,
  sender_id       INTEGER REFERENCES senders(tg_user_id),
  channel_id      INTEGER UNIQUE REFERENCES channels(channel_id),
  last_message_at INTEGER NOT NULL DEFAULT 0,
  UNIQUE (bot_id, sender_id),
  CHECK ((kind = 'private' AND bot_id IS NOT NULL AND sender_id IS NOT NULL AND channel_id IS NULL)
      OR (kind = 'channel' AND channel_id IS NOT NULL AND bot_id IS NULL AND sender_id IS NULL))
);
INSERT INTO chats_new (id, kind, bot_id, sender_id, last_message_at)
  SELECT id, 'private', bot_id, sender_id, last_message_at FROM chats;
DROP TABLE chats;
ALTER TABLE chats_new RENAME TO chats;

CREATE TABLE channel_watches (
  id             INTEGER PRIMARY KEY,
  channel_id     INTEGER NOT NULL UNIQUE REFERENCES channels(channel_id),
  window_minutes INTEGER NOT NULL,
  cond_json      TEXT    NOT NULL,
  enabled        INTEGER NOT NULL DEFAULT 1,
  status         TEXT    NOT NULL DEFAULT 'ok',
  last_error     TEXT    NOT NULL DEFAULT '',
  last_seen_id   INTEGER NOT NULL DEFAULT 0,
  hits           INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);

CREATE TABLE watch_pending (
  watch_id      INTEGER NOT NULL REFERENCES channel_watches(id) ON DELETE CASCADE,
  tg_message_id INTEGER NOT NULL,
  grouped_id    INTEGER NOT NULL DEFAULT 0,
  date          INTEGER NOT NULL,
  deadline      INTEGER NOT NULL,
  PRIMARY KEY (watch_id, tg_message_id)
);

CREATE TABLE custom_emoji (
  document_id INTEGER PRIMARY KEY,
  media_id    INTEGER NOT NULL REFERENCES media(id)
);

ALTER TABLE messages ADD COLUMN stats_json TEXT NOT NULL DEFAULT '';
