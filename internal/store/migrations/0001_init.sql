CREATE TABLE bots (
  id            INTEGER PRIMARY KEY,
  tg_bot_id     INTEGER NOT NULL UNIQUE,
  username      TEXT    NOT NULL DEFAULT '',
  name          TEXT    NOT NULL DEFAULT '',
  avatar_path   TEXT    NOT NULL DEFAULT '',
  token_enc     BLOB    NOT NULL,
  enabled       INTEGER NOT NULL DEFAULT 1,
  status        TEXT    NOT NULL DEFAULT 'stopped',
  last_error    TEXT    NOT NULL DEFAULT '',
  update_offset INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);

CREATE TABLE whitelist (
  bot_id     INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  tg_user_id INTEGER NOT NULL,
  note       TEXT    NOT NULL DEFAULT '',
  can_fetch  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bot_id, tg_user_id)
);

CREATE TABLE rejected (
  bot_id       INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  tg_user_id   INTEGER NOT NULL,
  first_name   TEXT    NOT NULL DEFAULT '',
  username     TEXT    NOT NULL DEFAULT '',
  last_seen_at INTEGER NOT NULL,
  count        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bot_id, tg_user_id)
);

CREATE TABLE senders (
  tg_user_id  INTEGER PRIMARY KEY,
  first_name  TEXT    NOT NULL DEFAULT '',
  last_name   TEXT    NOT NULL DEFAULT '',
  username    TEXT    NOT NULL DEFAULT '',
  avatar_path TEXT    NOT NULL DEFAULT '',
  updated_at  INTEGER NOT NULL
);

CREATE TABLE chats (
  id              INTEGER PRIMARY KEY,
  bot_id          INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  sender_id       INTEGER NOT NULL REFERENCES senders(tg_user_id),
  last_message_at INTEGER NOT NULL DEFAULT 0,
  UNIQUE (bot_id, sender_id)
);

CREATE TABLE messages (
  id                     INTEGER PRIMARY KEY,
  chat_id                INTEGER NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  tg_message_id          INTEGER NOT NULL,
  source                 TEXT    NOT NULL,
  media_group_id         TEXT    NOT NULL DEFAULT '',
  date                   INTEGER NOT NULL,
  edit_date              INTEGER NOT NULL DEFAULT 0,
  kind                   TEXT    NOT NULL,
  text                   TEXT    NOT NULL DEFAULT '',
  entities_json          TEXT    NOT NULL DEFAULT '[]',
  forward_origin_json    TEXT    NOT NULL DEFAULT '',
  reply_to_tg_message_id INTEGER NOT NULL DEFAULT 0,
  origin_chat_id         INTEGER NOT NULL DEFAULT 0,
  origin_chat_title      TEXT    NOT NULL DEFAULT '',
  origin_link            TEXT    NOT NULL DEFAULT '',
  extra_json             TEXT    NOT NULL DEFAULT '',
  raw_format             TEXT    NOT NULL,
  raw_json               TEXT    NOT NULL,
  receipt                TEXT    NOT NULL DEFAULT 'none',
  deleted_at             INTEGER NOT NULL DEFAULT 0,
  UNIQUE (chat_id, source, tg_message_id)
);
CREATE INDEX messages_chat_page ON messages(chat_id, deleted_at, id);
CREATE INDEX messages_group ON messages(chat_id, media_group_id);

CREATE TABLE media (
  id              INTEGER PRIMARY KEY,
  dedupe_key      TEXT    NOT NULL UNIQUE,
  bot_id          INTEGER NOT NULL DEFAULT 0,
  source_ref      TEXT    NOT NULL DEFAULT '',
  kind            TEXT    NOT NULL,
  mime            TEXT    NOT NULL DEFAULT '',
  file_name       TEXT    NOT NULL DEFAULT '',
  size            INTEGER NOT NULL DEFAULT 0,
  width           INTEGER NOT NULL DEFAULT 0,
  height          INTEGER NOT NULL DEFAULT 0,
  duration        INTEGER NOT NULL DEFAULT 0,
  waveform        BLOB,
  path            TEXT    NOT NULL DEFAULT '',
  state           TEXT    NOT NULL DEFAULT 'pending',
  attempts        INTEGER NOT NULL DEFAULT 0,
  next_attempt_at INTEGER NOT NULL DEFAULT 0,
  error           TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX media_due ON media(state, next_attempt_at);

CREATE TABLE message_media (
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  media_id   INTEGER NOT NULL REFERENCES media(id),
  role       TEXT    NOT NULL,
  position   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (message_id, media_id, role)
);
CREATE INDEX message_media_media ON message_media(media_id);

CREATE TABLE userbot (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  phone       TEXT    NOT NULL DEFAULT '',
  tg_user_id  INTEGER NOT NULL DEFAULT 0,
  name        TEXT    NOT NULL DEFAULT '',
  session_enc BLOB,
  status      TEXT    NOT NULL DEFAULT 'logged_out',
  last_error  TEXT    NOT NULL DEFAULT '',
  updated_at  INTEGER NOT NULL DEFAULT 0
);
