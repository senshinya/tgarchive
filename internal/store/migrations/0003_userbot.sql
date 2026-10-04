CREATE TABLE userbot_peers (
  channel_id  INTEGER PRIMARY KEY,
  access_hash INTEGER NOT NULL,
  username    TEXT    NOT NULL DEFAULT '',
  title       TEXT    NOT NULL DEFAULT '',
  updated_at  INTEGER NOT NULL
);

CREATE TABLE fetch_jobs (
  id                 INTEGER PRIMARY KEY,
  bot_id             INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  sender_id          INTEGER NOT NULL,
  link_tg_message_id INTEGER NOT NULL,
  link               TEXT    NOT NULL,
  state              TEXT    NOT NULL,
  error              TEXT    NOT NULL DEFAULT '',
  receipt            TEXT    NOT NULL DEFAULT 'none',
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL,
  UNIQUE (bot_id, sender_id, link_tg_message_id)
);
CREATE INDEX fetch_jobs_state ON fetch_jobs (state, id);

CREATE TABLE fetch_job_messages (
  job_id     INTEGER NOT NULL REFERENCES fetch_jobs(id) ON DELETE CASCADE,
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  PRIMARY KEY (job_id, message_id)
);
CREATE INDEX fetch_job_messages_message ON fetch_job_messages (message_id);
