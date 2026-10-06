package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"tgarchive/internal/model"
)

// ErrNoWatch is returned when archiving into a channel that is no longer watched (the watch was
// deleted while a poll was running).
var ErrNoWatch = errors.New("channel not watched")

// ErrExists is returned when creating something that must be unique (a second watch on a channel).
var ErrExists = errors.New("already exists")

const (
	WatchOK    = "ok"
	WatchError = "error"
)

// Channel is a watched channel's display info (the access hash lives in userbot_peers).
type Channel struct {
	ChannelID  int64
	Title      string
	Username   string
	AvatarPath string
}

type Watch struct {
	ID            int64
	ChannelID     int64
	WindowMinutes int
	Cond          string // JSON condition tree (internal/watchcond)
	Enabled       bool
	Status        string
	LastError     string
	LastSeenID    int64
	Hits          int64
	CreatedAt     int64
	UpdatedAt     int64
}

// WatchView is a watch with its channel and live counters, for listings.
type WatchView struct {
	Watch
	Channel Channel
	ChatID  int64 // the channel's conversation; 0 when it has none
	Pending int64
}

// Pending is a channel post under observation.
type Pending struct {
	TgMessageID int64
	GroupedID   int64
	Date        int64
	Deadline    int64
}

// UpsertChannel stores a channel's title and username; the avatar path is kept.
func (s *Store) UpsertChannel(ctx context.Context, c Channel, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO channels (channel_id, title, username, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (channel_id) DO UPDATE SET title = excluded.title, username = excluded.username, updated_at = excluded.updated_at`,
		c.ChannelID, c.Title, c.Username, now)
	return err
}

// RefreshChannelInfo updates a stored channel's title and username; channels not stored (seen
// only in the picker) are left out. Reports whether the channel is stored.
func (s *Store) RefreshChannelInfo(ctx context.Context, c Channel, now int64) (bool, error) {
	err := affected(s.db.ExecContext(ctx, "UPDATE channels SET title = ?, username = ?, updated_at = ? WHERE channel_id = ?",
		c.Title, c.Username, now, c.ChannelID))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) GetChannel(ctx context.Context, id int64) (*Channel, error) {
	c := Channel{ChannelID: id}
	err := s.db.QueryRowContext(ctx, "SELECT title, username, avatar_path FROM channels WHERE channel_id = ?", id).
		Scan(&c.Title, &c.Username, &c.AvatarPath)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

func (s *Store) SetChannelAvatar(ctx context.Context, id int64, path string) error {
	return affected(s.db.ExecContext(ctx, "UPDATE channels SET avatar_path = ? WHERE channel_id = ?", path, id))
}

// ChannelIDs lists the channels that have a conversation or a watch (for the daily info refresh).
func (s *Store) ChannelIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT channel_id FROM channels c WHERE EXISTS (SELECT 1 FROM chats WHERE channel_id = c.channel_id)
		OR EXISTS (SELECT 1 FROM channel_watches WHERE channel_id = c.channel_id) ORDER BY channel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CreateWatch adds a watch on an already stored channel and makes sure the channel has its
// conversation, so it shows up in the chat list right away. ErrExists when the channel is watched.
func (s *Store) CreateWatch(ctx context.Context, w *Watch) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM channel_watches WHERE channel_id = ?", w.ChannelID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrExists
		}
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO channel_watches (channel_id, window_minutes, cond_json, enabled, last_seen_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			w.ChannelID, w.WindowMinutes, w.Cond, w.Enabled, w.LastSeenID, w.CreatedAt, w.CreatedAt).Scan(&id); err != nil {
			return err
		}
		_, err := channelChat(ctx, tx, w.ChannelID, w.CreatedAt)
		return err
	})
	return id, err
}

// channelChat returns the conversation of a channel, creating it (dated now) when missing.
func channelChat(ctx context.Context, tx *sql.Tx, channelID, now int64) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM chats WHERE channel_id = ?", channelID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, "INSERT INTO chats (kind, channel_id, last_message_at) VALUES ('channel', ?, ?) RETURNING id",
			channelID, now).Scan(&id)
	}
	return id, err
}

// UpdateWatch changes a watch's settings. Disabling a watch clears its error; re-enabling one
// starts it over from the channel's newest post (it never catches up on what it missed).
func (s *Store) UpdateWatch(ctx context.Context, id int64, window int, cond string, enabled bool, now int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var was bool
		err := tx.QueryRowContext(ctx, "SELECT enabled FROM channel_watches WHERE id = ?", id).Scan(&was)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE channel_watches SET window_minutes = ?, cond_json = ?, enabled = ?, updated_at = ?,
				status = CASE WHEN ? THEN status ELSE 'ok' END, last_error = CASE WHEN ? THEN last_error ELSE '' END
			WHERE id = ?`, window, cond, enabled, now, enabled, enabled, id); err != nil {
			return err
		}
		if enabled && !was {
			if _, err := tx.ExecContext(ctx, "UPDATE channel_watches SET last_seen_id = 0 WHERE id = ?", id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM watch_pending WHERE watch_id = ?", id); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetWatchStart records where a watch starts observing: the channel's newest post id, or
// WatchStartEmpty for a channel without posts (0 means not started yet).
func (s *Store) SetWatchStart(ctx context.Context, id, lastSeen int64) error {
	return affected(s.db.ExecContext(ctx, "UPDATE channel_watches SET last_seen_id = ? WHERE id = ? AND last_seen_id = 0", lastSeen, id))
}

// WatchStartEmpty marks a started watch on a channel that had no posts yet.
const WatchStartEmpty = -1

const watchCols = `w.id, w.channel_id, w.window_minutes, w.cond_json, w.enabled, w.status, w.last_error, w.last_seen_id, w.hits,
	w.created_at, w.updated_at, c.title, c.username, c.avatar_path, COALESCE((SELECT id FROM chats WHERE channel_id = w.channel_id), 0),
	(SELECT COUNT(*) FROM watch_pending p WHERE p.watch_id = w.id)`

func scanWatch(r scanner) (WatchView, error) {
	var v WatchView
	err := r.Scan(&v.ID, &v.ChannelID, &v.WindowMinutes, &v.Cond, &v.Enabled, &v.Status, &v.LastError, &v.LastSeenID, &v.Hits,
		&v.CreatedAt, &v.UpdatedAt, &v.Channel.Title, &v.Channel.Username, &v.Channel.AvatarPath, &v.ChatID, &v.Pending)
	v.Channel.ChannelID = v.ChannelID
	return v, err
}

func (s *Store) GetWatch(ctx context.Context, id int64) (*WatchView, error) {
	v, err := scanWatch(s.db.QueryRowContext(ctx, `SELECT `+watchCols+` FROM channel_watches w JOIN channels c ON c.channel_id = w.channel_id
		WHERE w.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// ListWatches returns every watch, oldest first.
func (s *Store) ListWatches(ctx context.Context) ([]WatchView, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+watchCols+` FROM channel_watches w JOIN channels c ON c.channel_id = w.channel_id
		ORDER BY w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WatchView{}
	for rows.Next() {
		v, err := scanWatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteWatch removes a watch (its pending posts go with it). With purge, the channel's
// conversation, its messages and the media nothing else uses are deleted too; the returned paths
// are the files to remove and chatID the deleted conversation (0 without purge).
func (s *Store) DeleteWatch(ctx context.Context, id int64, purge bool) (chatID int64, orphans []string, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var channelID int64
		err := tx.QueryRowContext(ctx, "DELETE FROM channel_watches WHERE id = ? RETURNING channel_id", id).Scan(&channelID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !purge {
			return nil
		}
		err = tx.QueryRowContext(ctx, "DELETE FROM chats WHERE channel_id = ? RETURNING id", channelID).Scan(&chatID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		orphans, err = collectOrphans(ctx, tx)
		return err
	})
	return chatID, orphans, err
}

// SetWatchStatus records the outcome of a poll; changed reports whether status or error moved.
func (s *Store) SetWatchStatus(ctx context.Context, id int64, status, lastErr string, now int64) (changed bool, err error) {
	res, err := s.db.ExecContext(ctx, `UPDATE channel_watches SET status = ?, last_error = ?, updated_at = ?
		WHERE id = ? AND (status != ? OR last_error != ?)`, status, lastErr, now, id, status, lastErr)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// AddPending records newly seen posts and advances the watch's high-water mark in one step, so
// a post is never both skipped and unrecorded. Posts already pending are left as they are.
func (s *Store) AddPending(ctx context.Context, watchID int64, posts []Pending, lastSeen int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		// A watch reset to 0 meanwhile (re-enabled while this poll ran) starts over: this
		// poll's posts belong to the old run.
		var cur int64
		if err := tx.QueryRowContext(ctx, "SELECT last_seen_id FROM channel_watches WHERE id = ?", watchID).Scan(&cur); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if cur == 0 {
			return nil
		}
		for _, p := range posts {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO watch_pending (watch_id, tg_message_id, grouped_id, date, deadline)
				VALUES (?, ?, ?, ?, ?)`, watchID, p.TgMessageID, p.GroupedID, p.Date, p.Deadline); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "UPDATE channel_watches SET last_seen_id = ? WHERE id = ? AND last_seen_id < ?", lastSeen, watchID, lastSeen)
		return err
	})
}

func (s *Store) ListPending(ctx context.Context, watchID int64) ([]Pending, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tg_message_id, grouped_id, date, deadline FROM watch_pending
		WHERE watch_id = ? ORDER BY tg_message_id`, watchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pending{}
	for rows.Next() {
		var p Pending
		if err := rows.Scan(&p.TgMessageID, &p.GroupedID, &p.Date, &p.Deadline); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeletePending(ctx context.Context, watchID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	args := []any{watchID}
	for _, id := range ids {
		args = append(args, id)
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	_, err := s.db.ExecContext(ctx, "DELETE FROM watch_pending WHERE watch_id = ? AND tg_message_id IN ("+ph+")", args...)
	return err
}

// AddWatchHit counts one archived post (an album counts once).
func (s *Store) AddWatchHit(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE channel_watches SET hits = hits + 1 WHERE id = ?", id)
	return err
}

// EmojiMedia is the sticker media of a registered custom emoji.
type EmojiMedia struct {
	MediaID int64
	Mime    string
}

// CustomEmojiMedia maps custom emoji document ids to their media rows, for those registered.
func (s *Store) CustomEmojiMedia(ctx context.Context, docIDs []int64) (map[int64]EmojiMedia, error) {
	out := map[int64]EmojiMedia{}
	if len(docIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(docIDs))
	for i, id := range docIDs {
		args[i] = id
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(docIDs)), ",")
	rows, err := s.db.QueryContext(ctx, `SELECT ce.document_id, ce.media_id, m.mime FROM custom_emoji ce JOIN media m ON m.id = ce.media_id
		WHERE ce.document_id IN (`+ph+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d int64
		var e EmojiMedia
		if err := rows.Scan(&d, &e.MediaID, &e.Mime); err != nil {
			return nil, err
		}
		out[d] = e
	}
	return out, rows.Err()
}

// RegisterCustomEmoji stores a custom emoji's sticker as media (downloaded by the queue) and
// returns its media id. Registered media are never collected as orphans.
func (s *Store) RegisterCustomEmoji(ctx context.Context, docID int64, md model.Media) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT media_id FROM custom_emoji WHERE document_id = ?", docID).Scan(&id)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if id, err = upsertMedia(ctx, tx, 0, md); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO custom_emoji (document_id, media_id) VALUES (?, ?)", docID, id)
		return err
	})
	return id, err
}
