CREATE TABLE video_progress (
  media_id INTEGER PRIMARY KEY REFERENCES media(id) ON DELETE CASCADE,
  position REAL NOT NULL DEFAULT 0,
  duration REAL NOT NULL DEFAULT 0,
  completed INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL
);
CREATE TABLE video_library_metadata (
  message_id INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  cover_media_id INTEGER REFERENCES media(id) ON DELETE SET NULL
);
