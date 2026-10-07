-- Full-text search: one row per non-deleted message (rowid = messages.id), kept in step by the
-- triggers below. The trigram tokenizer matches substrings, which is what CJK text needs.
CREATE VIRTUAL TABLE search_fts USING fts5(body, files, article, tokenize = 'trigram');

CREATE TABLE favorites (
  id         INTEGER PRIMARY KEY,
  message_id INTEGER NOT NULL UNIQUE REFERENCES messages(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL
);
CREATE TABLE tags (
  id   INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE COLLATE NOCASE
);
CREATE TABLE message_tags (
  message_id INTEGER NOT NULL REFERENCES favorites(message_id) ON DELETE CASCADE,
  tag_id     INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (message_id, tag_id)
);
CREATE INDEX message_tags_tag ON message_tags(tag_id);

ALTER TABLE chats ADD COLUMN last_read_id INTEGER NOT NULL DEFAULT 0;
UPDATE chats SET last_read_id = COALESCE((SELECT MAX(id) FROM messages m WHERE m.chat_id = chats.id), 0);

ALTER TABLE channel_watches ADD COLUMN last_polled_at INTEGER NOT NULL DEFAULT 0;

-- Searchable file names: the main document/audio media of a message.
CREATE VIEW search_files AS
  SELECT mm.message_id, group_concat(md.file_name, ' ') AS names
  FROM message_media mm JOIN media md ON md.id = mm.media_id
  WHERE mm.role = 'main' AND md.kind IN ('document', 'audio') AND md.file_name != ''
  GROUP BY mm.message_id;

-- Searchable article text: title, description and the text nodes of the Telegraph content.
CREATE VIEW search_articles AS
  SELECT a.message_id, a.title || ' ' || a.description || ' ' ||
    COALESCE((SELECT group_concat(value, ' ') FROM json_tree(a.content) WHERE type = 'text' AND typeof(key) = 'integer'), '') AS text
  FROM articles a;

CREATE TRIGGER search_msg_ai AFTER INSERT ON messages WHEN NEW.deleted_at = 0 BEGIN
  INSERT INTO search_fts(rowid, body, files, article) VALUES (NEW.id, NEW.text,
    COALESCE((SELECT names FROM search_files WHERE message_id = NEW.id), ''),
    COALESCE((SELECT text FROM search_articles WHERE message_id = NEW.id), ''));
END;
CREATE TRIGGER search_msg_au AFTER UPDATE OF text, deleted_at ON messages BEGIN
  DELETE FROM search_fts WHERE rowid = OLD.id;
  INSERT INTO search_fts(rowid, body, files, article) SELECT NEW.id, NEW.text,
    COALESCE((SELECT names FROM search_files WHERE message_id = NEW.id), ''),
    COALESCE((SELECT text FROM search_articles WHERE message_id = NEW.id), '')
  WHERE NEW.deleted_at = 0;
END;
CREATE TRIGGER search_msg_ad AFTER DELETE ON messages BEGIN
  DELETE FROM search_fts WHERE rowid = OLD.id;
END;
CREATE TRIGGER search_mm_ai AFTER INSERT ON message_media BEGIN
  UPDATE search_fts SET files = COALESCE((SELECT names FROM search_files WHERE message_id = NEW.message_id), '')
  WHERE rowid = NEW.message_id;
END;
CREATE TRIGGER search_mm_ad AFTER DELETE ON message_media BEGIN
  UPDATE search_fts SET files = COALESCE((SELECT names FROM search_files WHERE message_id = OLD.message_id), '')
  WHERE rowid = OLD.message_id;
END;
CREATE TRIGGER search_art_ai AFTER INSERT ON articles BEGIN
  UPDATE search_fts SET article = COALESCE((SELECT text FROM search_articles WHERE message_id = NEW.message_id), '')
  WHERE rowid = NEW.message_id;
END;
CREATE TRIGGER search_art_au AFTER UPDATE ON articles BEGIN
  UPDATE search_fts SET article = COALESCE((SELECT text FROM search_articles WHERE message_id = NEW.message_id), '')
  WHERE rowid = NEW.message_id;
END;
CREATE TRIGGER search_art_ad AFTER DELETE ON articles BEGIN
  UPDATE search_fts SET article = '' WHERE rowid = OLD.message_id;
END;

INSERT INTO search_fts(rowid, body, files, article)
  SELECT m.id, m.text, COALESCE(f.names, ''), COALESCE(a.text, '')
  FROM messages m LEFT JOIN search_files f ON f.message_id = m.id LEFT JOIN search_articles a ON a.message_id = m.id
  WHERE m.deleted_at = 0;
