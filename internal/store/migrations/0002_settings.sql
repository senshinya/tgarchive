CREATE TABLE settings (
  key        TEXT    PRIMARY KEY,
  value      BLOB    NOT NULL,
  updated_at INTEGER NOT NULL
);
