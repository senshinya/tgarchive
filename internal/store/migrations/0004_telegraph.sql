CREATE TABLE telegraph_jobs (
  id          INTEGER PRIMARY KEY,
  message_id  INTEGER NOT NULL UNIQUE REFERENCES messages(id) ON DELETE CASCADE,
  path        TEXT    NOT NULL,
  state       TEXT    NOT NULL CHECK (state IN ('queued','fetching','fetched','failed')),
  attempts    INTEGER NOT NULL DEFAULT 0,
  error       TEXT    NOT NULL DEFAULT '',
  receipt     TEXT    NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE INDEX telegraph_jobs_state ON telegraph_jobs (state, id);

CREATE TABLE articles (
  message_id     INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
  path           TEXT    NOT NULL,
  url            TEXT    NOT NULL,
  title          TEXT    NOT NULL,
  description    TEXT    NOT NULL DEFAULT '',
  author_name    TEXT    NOT NULL DEFAULT '',
  author_url     TEXT    NOT NULL DEFAULT '',
  image_media_id INTEGER,
  views          INTEGER NOT NULL DEFAULT 0,
  content        TEXT    NOT NULL,
  fetched_at     INTEGER NOT NULL
);
