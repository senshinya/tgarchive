package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// WatchPost is an archived channel post whose counters are refreshed after its hit.
type WatchPost struct {
	MessageID   int64
	ChatID      int64
	TgMessageID int64
	GroupID     string // media_group_id: album members are refreshed together
	Stats       string
}

const watchPostCols = "m.id, m.chat_id, m.tg_message_id, m.media_group_id, m.stats_json"

// hitAt reads the hit time out of stats_json (NULL when there is none, or no valid JSON).
const hitAt = "CASE WHEN json_valid(m.stats_json) THEN json_extract(m.stats_json, '$.hit.at') END"

func collectWatchPosts(rows *sql.Rows, err error) ([]WatchPost, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WatchPost{}
	for rows.Next() {
		var p WatchPost
		if err := rows.Scan(&p.MessageID, &p.ChatID, &p.TgMessageID, &p.GroupID, &p.Stats); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RecentWatchPosts lists a watched channel's live archived posts hit at or after since, oldest first.
func (s *Store) RecentWatchPosts(ctx context.Context, channelID, since int64) ([]WatchPost, error) {
	return collectWatchPosts(s.db.QueryContext(ctx, `SELECT `+watchPostCols+` FROM messages m JOIN chats c ON c.id = m.chat_id
		WHERE c.channel_id = ? AND m.deleted_at = 0 AND m.source = 'channel_watch' AND `+hitAt+` >= ?
		ORDER BY m.id`, channelID, since))
}

// ChatWatchPosts returns the live archived watch posts among ids in a channel chat, with whole
// albums (an album's counters are its members' maximum), and the chat's channel. ErrNotFound when
// the chat is not a channel chat.
func (s *Store) ChatWatchPosts(ctx context.Context, chatID int64, ids []int64) (int64, []WatchPost, error) {
	var channel sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT channel_id FROM chats WHERE id = ?", chatID).Scan(&channel)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !channel.Valid {
		return 0, nil, ErrNotFound
	}
	if err != nil {
		return 0, nil, err
	}
	if len(ids) == 0 {
		return channel.Int64, []WatchPost{}, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{chatID}
	for range 2 {
		for _, id := range ids {
			args = append(args, id)
		}
	}
	args = append(args, chatID)
	posts, err := collectWatchPosts(s.db.QueryContext(ctx, `SELECT `+watchPostCols+` FROM messages m
		WHERE m.chat_id = ? AND m.deleted_at = 0 AND m.source = 'channel_watch' AND (m.id IN (`+ph+`)
			OR m.media_group_id != '' AND m.media_group_id IN (SELECT media_group_id FROM messages WHERE id IN (`+ph+`) AND chat_id = ?))
		ORDER BY m.id`, args...))
	return channel.Int64, posts, err
}

// SetPostStats replaces a message's stats_json if it is still old, reporting whether it did: stats
// written since old was read (a new hit) are newer than a refresh based on old.
func (s *Store) SetPostStats(ctx context.Context, messageID int64, old, stats string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE messages SET stats_json = ? WHERE id = ? AND stats_json = ?", stats, messageID, old)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
