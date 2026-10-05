package store

import (
	"context"
	"strings"
)

// DownloadItem is a media row as the downloads panel lists it, with the newest live message
// using it (to jump to).
type DownloadItem struct {
	MediaID   int64  `json:"media_id"`
	MessageID int64  `json:"message_id"`
	ChatID    int64  `json:"chat_id"`
	Kind      string `json:"kind"`
	FileName  string `json:"file_name"`
	Size      int64  `json:"size"`
	Error     string `json:"error,omitempty"`
}

func idArgs(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

// DownloadItems describes the given media; media no live message uses are left out.
func (s *Store) DownloadItems(ctx context.Context, ids []int64) (map[int64]DownloadItem, error) {
	out := map[int64]DownloadItem{}
	if len(ids) == 0 {
		return out, nil
	}
	ph, args := idArgs(ids)
	rows, err := s.db.QueryContext(ctx, `
		SELECT md.id, md.kind, md.file_name, md.size, md.error, m.id, m.chat_id
		FROM media md JOIN message_media mm ON mm.media_id = md.id JOIN messages m ON m.id = mm.message_id AND m.deleted_at = 0
		WHERE md.id IN (`+ph+`) ORDER BY m.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it DownloadItem
		if err := rows.Scan(&it.MediaID, &it.Kind, &it.FileName, &it.Size, &it.Error, &it.MessageID, &it.ChatID); err != nil {
			return nil, err
		}
		out[it.MediaID] = it // ascending by message id: the newest message wins
	}
	return out, rows.Err()
}

// QueueStats counts pending media other than those in exclude (the ones downloading now).
func (s *Store) QueueStats(ctx context.Context, exclude []int64) (count, bytes int64, err error) {
	q := `SELECT COUNT(*), COALESCE(SUM(size), 0) FROM media WHERE state = ?`
	args := []any{StatePending}
	if len(exclude) > 0 {
		ph, ex := idArgs(exclude)
		q += ` AND id NOT IN (` + ph + `)`
		args = append(args, ex...)
	}
	err = s.db.QueryRowContext(ctx, q, args...).Scan(&count, &bytes)
	return count, bytes, err
}

// FailedDownloads lists the most recently created failed media still used by a live message.
func (s *Store) FailedDownloads(ctx context.Context, limit int) ([]DownloadItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM media WHERE state = ? ORDER BY id DESC LIMIT ?`, StateFailed, limit)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items, err := s.DownloadItems(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := []DownloadItem{}
	for _, id := range ids {
		if it, ok := items[id]; ok {
			out = append(out, it)
		}
	}
	return out, nil
}
