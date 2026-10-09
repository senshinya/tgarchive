-- Comments of archived channel posts, read from the channel's discussion group. They live in the
-- channel's conversation but outside its timeline: thread_root_id is the archived post (the head
-- of its album) they belong to, 0 for everything else.
ALTER TABLE messages ADD COLUMN thread_root_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX messages_thread ON messages (thread_root_id, id) WHERE thread_root_id != 0;

-- Commenters, for fetching their profile photo when it is first shown: the photo they have now,
-- how to address them (ref_json: an access hash, or the comment they were seen in), and the photo
-- the stored avatar (avatars/users/<id>.jpg or channels/<id>.jpg) was taken from.
CREATE TABLE comment_peers (
  kind           TEXT    NOT NULL, -- user / channel
  peer_id        INTEGER NOT NULL,
  photo_id       INTEGER NOT NULL DEFAULT 0,
  ref_json       TEXT    NOT NULL DEFAULT '',
  saved_photo_id INTEGER NOT NULL DEFAULT 0,
  updated_at     INTEGER NOT NULL,
  PRIMARY KEY (kind, peer_id)
);
