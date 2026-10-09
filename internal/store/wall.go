package store

import (
	"context"
	"strings"
)

// wallKinds maps a media wall type to the main-media kinds it shows.
var wallKinds = map[string]string{
	"all":   "'photo', 'video', 'animation'",
	"photo": "'photo'",
	"video": "'video', 'animation'",
}

// allMediaQuery builds the media wall page: messages of every chat (or only private chats, or
// only channels) whose main media is of the type, newest first. The scan walks messages by id
// downwards, so a page stops as soon as it has limit rows.
func allMediaQuery(typ, source string, beforeID int64, limit int) (string, []any, error) {
	kinds, ok := wallKinds[typ]
	if !ok {
		return "", nil, ErrBadMediaType
	}
	where := []string{"m.deleted_at = 0", "m.thread_root_id = 0", `EXISTS (SELECT 1 FROM message_media mm JOIN media md ON md.id = mm.media_id
		WHERE mm.message_id = m.id AND mm.role = 'main' AND md.kind IN (` + kinds + `))`}
	var args []any
	switch source {
	case "all":
	case "private", "channel":
		where = append(where, "EXISTS (SELECT 1 FROM chats c WHERE c.id = m.chat_id AND c.kind = ?)")
		args = append(args, source)
	default:
		return "", nil, ErrBadMediaType
	}
	if beforeID > 0 {
		where = append(where, "m.id < ?")
		args = append(args, beforeID)
	}
	args = append(args, limit)
	return `SELECT ` + msgCols + ` FROM messages m WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY m.id DESC LIMIT ?`, args, nil
}

// ListAllMedia pages through the media wall: photos ("photo"), videos and GIFs ("video") or
// both ("all"), from every chat ("all"), private chats or channels; beforeID pages.
func (s *Store) ListAllMedia(ctx context.Context, typ, source string, beforeID int64, limit int) ([]MessageView, error) {
	s = s.reader()
	q, args, err := allMediaQuery(typ, source, beforeID, limit)
	if err != nil {
		return nil, err
	}
	views, err := collectViews(s.db.QueryContext(ctx, q, args...))
	if err != nil {
		return nil, err
	}
	if err := s.hydrate(ctx, views); err != nil {
		return nil, err
	}
	return views, nil
}
