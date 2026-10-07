package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

var (
	// ErrNotFavorite is returned when tagging a message that is not a favorite.
	ErrNotFavorite = errors.New("message is not a favorite")
	// ErrBadTag is returned for a tag name that is empty, too long or multi-line, or too many tags.
	ErrBadTag = errors.New("bad tag")
)

const (
	maxTagRunes = 32
	maxTags     = 10
)

type TagRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// FavoriteInfo is what a message view carries when the message is a favorite.
type FavoriteInfo struct {
	At   int64    `json:"at"`
	Tags []TagRef `json:"tags"`
}

type TagCount struct {
	TagRef
	Count int64 `json:"count"`
}

type FavoriteItem struct {
	FavID   int64       `json:"fav_id"`
	Message MessageView `json:"message"`
}

// NormalizeTags trims tag names and drops blanks and case-insensitive duplicates (the first
// spelling wins). ErrBadTag for a name over 32 characters or with a line break, or over 10 tags.
func NormalizeTags(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if utf8.RuneCountInString(t) > maxTagRunes || strings.ContainsAny(t, "\r\n") {
			return nil, ErrBadTag
		}
		if seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	if len(out) > maxTags {
		return nil, ErrBadTag
	}
	return out, nil
}

// liveChat returns the chat of a non-deleted message.
func liveChat(ctx context.Context, tx *sql.Tx, msgID int64) (int64, error) {
	var chat int64
	err := tx.QueryRowContext(ctx, "SELECT chat_id FROM messages WHERE id = ? AND deleted_at = 0", msgID).Scan(&chat)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return chat, err
}

// Favorite marks a message as a favorite (keeping the original time when it already is one);
// tags, when not nil, replace its tags. It returns the favorite and the message's chat.
func (s *Store) Favorite(ctx context.Context, msgID, now int64, tags []string) (*FavoriteInfo, int64, error) {
	names, err := NormalizeTags(tags)
	if err != nil {
		return nil, 0, err
	}
	var info FavoriteInfo
	var chat int64
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if chat, err = liveChat(ctx, tx, msgID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO favorites (message_id, created_at) VALUES (?, ?) ON CONFLICT (message_id) DO NOTHING",
			msgID, now); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT created_at FROM favorites WHERE message_id = ?", msgID).Scan(&info.At); err != nil {
			return err
		}
		if tags != nil {
			if err := replaceTags(ctx, tx, msgID, names); err != nil {
				return err
			}
		}
		info.Tags, err = tagsOf(ctx, tx, msgID)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	return &info, chat, nil
}

// Unfavorite removes a message from the favorites (and its tags); a message that is not one is
// left as it is. ErrNotFound when the message does not exist.
func (s *Store) Unfavorite(ctx context.Context, msgID int64) (int64, error) {
	var chat int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		if chat, err = liveChat(ctx, tx, msgID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM favorites WHERE message_id = ?", msgID)
		return err
	})
	return chat, err
}

// SetTags replaces a favorite's tags, creating tags as needed (names match case-insensitively).
func (s *Store) SetTags(ctx context.Context, msgID int64, tags []string) ([]TagRef, int64, error) {
	names, err := NormalizeTags(tags)
	if err != nil {
		return nil, 0, err
	}
	var out []TagRef
	var chat int64
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if chat, err = liveChat(ctx, tx, msgID); err != nil {
			return err
		}
		var one int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM favorites WHERE message_id = ?", msgID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFavorite
		} else if err != nil {
			return err
		}
		if err := replaceTags(ctx, tx, msgID, names); err != nil {
			return err
		}
		out, err = tagsOf(ctx, tx, msgID)
		return err
	})
	return out, chat, err
}

func replaceTags(ctx context.Context, tx *sql.Tx, msgID int64, names []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM message_tags WHERE message_id = ?", msgID); err != nil {
		return err
	}
	for _, n := range names {
		if _, err := tx.ExecContext(ctx, "INSERT INTO tags (name) VALUES (?) ON CONFLICT (name) DO NOTHING", n); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO message_tags (message_id, tag_id) SELECT ?, id FROM tags WHERE name = ?",
			msgID, n); err != nil {
			return err
		}
	}
	return nil
}

func tagsOf(ctx context.Context, tx *sql.Tx, msgID int64) ([]TagRef, error) {
	rows, err := tx.QueryContext(ctx, `SELECT t.id, t.name FROM message_tags mt JOIN tags t ON t.id = mt.tag_id
		WHERE mt.message_id = ? ORDER BY t.name`, msgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagRef{}
	for rows.Next() {
		var t TagRef
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListFavorites pages through the favorites, most recently added first; tagID 0 means any tag,
// beforeFavID pages (a FavID from the previous page).
func (s *Store) ListFavorites(ctx context.Context, tagID, beforeFavID int64, limit int) ([]FavoriteItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id, f.message_id FROM favorites f JOIN messages m ON m.id = f.message_id
		WHERE m.deleted_at = 0 AND (? = 0 OR f.id < ?)
			AND (? = 0 OR EXISTS (SELECT 1 FROM message_tags mt WHERE mt.message_id = f.message_id AND mt.tag_id = ?))
		ORDER BY f.id DESC LIMIT ?`, beforeFavID, beforeFavID, tagID, tagID, limit)
	if err != nil {
		return nil, err
	}
	var items []FavoriteItem
	var ids []any
	for rows.Next() {
		var it FavoriteItem
		if err := rows.Scan(&it.FavID, &it.Message.ID); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, it)
		ids = append(ids, it.Message.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []FavoriteItem{}
	if len(items) == 0 {
		return out, nil
	}
	views, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages WHERE id IN (`+
		strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, ids...))
	if err != nil {
		return nil, err
	}
	if err := s.hydrate(ctx, views); err != nil {
		return nil, err
	}
	byID := make(map[int64]MessageView, len(views))
	for _, v := range views {
		byID[v.ID] = v
	}
	for _, it := range items {
		if v, ok := byID[it.Message.ID]; ok {
			it.Message = v
			out = append(out, it)
		}
	}
	return out, nil
}

// ListTags returns every tag by name, with how many live favorites carry it.
func (s *Store) ListTags(ctx context.Context) ([]TagCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.name,
			(SELECT COUNT(*) FROM message_tags mt JOIN messages m ON m.id = mt.message_id WHERE mt.tag_id = t.id AND m.deleted_at = 0)
		FROM tags t ORDER BY t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagCount{}
	for rows.Next() {
		var t TagCount
		if err := rows.Scan(&t.ID, &t.Name, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteTag removes a tag from every favorite and deletes it.
func (s *Store) DeleteTag(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, "DELETE FROM tags WHERE id = ?", id))
}

// hydrateFavorites sets Favorite on the views that are favorites.
func (s *Store) hydrateFavorites(ctx context.Context, views []MessageView, idx map[int64]int, ph string, args []any) error {
	rows, err := s.db.QueryContext(ctx, `SELECT message_id, created_at FROM favorites WHERE message_id IN (`+ph+`)`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, at int64
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			return err
		}
		views[idx[id]].Favorite = &FavoriteInfo{At: at, Tags: []TagRef{}}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT mt.message_id, t.id, t.name FROM message_tags mt JOIN tags t ON t.id = mt.tag_id
		WHERE mt.message_id IN (`+ph+`) ORDER BY t.name`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var t TagRef
		if err := rows.Scan(&id, &t.ID, &t.Name); err != nil {
			return err
		}
		if f := views[idx[id]].Favorite; f != nil {
			f.Tags = append(f.Tags, t)
		}
	}
	return rows.Err()
}
