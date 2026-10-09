package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

// CommentState is what the comments of an archived post look like locally.
type CommentState struct {
	MaxTgID int64 // the newest stored comment's id in the discussion group; 0 when none
	Count   int64
}

// CommentState reports the stored comments of archived post rootID.
func (s *Store) CommentState(ctx context.Context, rootID int64) (CommentState, error) {
	var st CommentState
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(tg_message_id), 0), COUNT(*) FROM messages
		WHERE thread_root_id != 0 AND thread_root_id = ? AND deleted_at = 0`, rootID).Scan(&st.MaxTgID, &st.Count)
	return st, err
}

// Commenter is who wrote a comment, as stored in its extra.from.
type Commenter struct {
	Kind  string `json:"kind"` // user / channel
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Photo bool   `json:"photo,omitempty"`
}

// RecentCommenters returns the authors of the newest comments of archived post rootID, newest
// first, each once, at most n.
func (s *Store) RecentCommenters(ctx context.Context, rootID int64, n int) ([]Commenter, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT extra_json FROM messages WHERE thread_root_id != 0 AND thread_root_id = ? AND deleted_at = 0
		ORDER BY tg_message_id DESC LIMIT 200`, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Commenter{}
	seen := map[string]bool{}
	for rows.Next() && len(out) < n {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var extra struct {
			From *Commenter `json:"from"`
		}
		if json.Unmarshal([]byte(raw), &extra) != nil || extra.From == nil || extra.From.ID == 0 {
			continue
		}
		key := extra.From.Kind + ":" + strconv.FormatInt(extra.From.ID, 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, *extra.From)
	}
	return out, rows.Err()
}

// CommentPeer is a commenter as last seen: their current photo, how to address them, and the
// photo their stored avatar came from.
type CommentPeer struct {
	Kind         string
	ID           int64
	PhotoID      int64
	Ref          string
	SavedPhotoID int64
}

// PutCommentPeer records a commenter's current photo and reference (the saved avatar is kept).
func (s *Store) PutCommentPeer(ctx context.Context, kind string, id, photoID int64, ref string, now int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO comment_peers (kind, peer_id, photo_id, ref_json, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (kind, peer_id) DO UPDATE SET photo_id = excluded.photo_id, ref_json = excluded.ref_json, updated_at = excluded.updated_at`,
		kind, id, photoID, ref, now)
	return err
}

// GetCommentPeer returns a commenter; ErrNotFound when never seen.
func (s *Store) GetCommentPeer(ctx context.Context, kind string, id int64) (CommentPeer, error) {
	p := CommentPeer{Kind: kind, ID: id}
	err := s.db.QueryRowContext(ctx, "SELECT photo_id, ref_json, saved_photo_id FROM comment_peers WHERE kind = ? AND peer_id = ?", kind, id).
		Scan(&p.PhotoID, &p.Ref, &p.SavedPhotoID)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// SetCommentPeerSaved records the photo a commenter's stored avatar was taken from.
func (s *Store) SetCommentPeerSaved(ctx context.Context, kind string, id, photoID int64) error {
	return affected(s.db.ExecContext(ctx, "UPDATE comment_peers SET saved_photo_id = ? WHERE kind = ? AND peer_id = ?", photoID, kind, id))
}

// ArchivedWatchPosts lists the live archived posts of a channel's conversation, oldest first (the
// one-off comment backfill walks them all).
func (s *Store) ArchivedWatchPosts(ctx context.Context, channelID int64) ([]WatchPost, error) {
	return collectWatchPosts(s.db.QueryContext(ctx, `SELECT `+watchPostCols+` FROM messages m JOIN chats c ON c.id = m.chat_id
		WHERE c.channel_id = ? AND m.deleted_at = 0 AND m.source = 'channel_watch' ORDER BY m.id`, channelID))
}
