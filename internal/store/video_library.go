package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

var ErrBadVideoLibrary = errors.New("invalid video library input")

type VideoProgress struct {
	Position  float64 `json:"position"`
	Duration  float64 `json:"duration"`
	Completed bool    `json:"completed"`
	UpdatedAt int64   `json:"updated_at"`
}
type VideoLibraryVideo struct {
	Message  MessageView   `json:"message"`
	Media    MediaView     `json:"media"`
	Progress VideoProgress `json:"progress"`
}
type VideoLibraryItem struct {
	ID           int64               `json:"id"`
	Message      MessageView         `json:"message"`
	Title        string              `json:"title"`
	Description  string              `json:"description"`
	Date         int64               `json:"date"`
	Added        int64               `json:"added"`
	Duration     float64             `json:"duration"`
	CoverMediaID int64               `json:"cover_media_id"`
	Videos       []VideoLibraryVideo `json:"videos"`
}
type VideoLibraryPage struct {
	Items       []VideoLibraryItem `json:"items"`
	Next        string             `json:"next"`
	TotalItems  int64              `json:"total_items"`
	TotalVideos int64              `json:"total_videos"`
	Collections int64              `json:"collections"`
}
type VideoLibraryQuery struct {
	Q                                 string
	ChatID, TagID                     int64
	Kind, Watched, View, Sort, Cursor string
	Limit                             int
}
type VideoLibraryUpdate struct {
	Title        *string `json:"title"`
	CoverMediaID *int64  `json:"cover_media_id"`
}

// Group each input once. Ordered aggregates retain album captions and the first video,
// while the joins keep counts, favorites, tags and filename search linear in input size.
const videoLibraryCTE = `WITH live AS MATERIALIZED (
 SELECT m.*, MIN(id) OVER (PARTITION BY chat_id,source,origin_chat_id,media_group_id,
 CASE WHEN media_group_id = '' THEN id ELSE 0 END) AS library_id,
 FIRST_VALUE(text) OVER (PARTITION BY chat_id,source,origin_chat_id,media_group_id,
 CASE WHEN media_group_id = '' THEN id ELSE 0 END
 ORDER BY CASE WHEN trim(text,char(10)||char(13)||' '||char(9))!='' THEN 0 ELSE 1 END,tg_message_id,id) AS caption
 FROM messages m WHERE deleted_at = 0 AND thread_root_id = 0
), posts AS (
 SELECT l.library_id AS id,MIN(l.chat_id) AS chat_id,MAX(l.id) AS added,MIN(l.date) AS date,
 COALESCE(group_concat(l.text,char(10)||char(10) ORDER BY l.tg_message_id,l.id)
 FILTER (WHERE trim(l.text,char(10)||char(13)||' '||char(9))!=''),'') AS description,
 trim(MAX(l.caption),char(10)||char(13)||' '||char(9)) AS caption,
 COALESCE(MIN(f.message_id),l.library_id) AS representative,
 COUNT(f.message_id) AS favorite_count,COUNT(mt.message_id) AS tag_count
 FROM live l LEFT JOIN favorites f ON f.message_id=l.id
 LEFT JOIN message_tags mt ON mt.message_id=l.id AND mt.tag_id=@library_tag
 GROUP BY l.library_id
), videos AS MATERIALIZED (
 SELECT l.library_id,l.id AS message_id,l.tg_message_id,mm.position AS media_position,md.id AS media_id,
 md.file_name,md.duration,COALESCE(p.position,0) AS play_position,COALESCE(p.duration,0) AS play_duration,
 COALESCE(p.completed,0) AS completed,COALESCE(p.updated_at,0) AS updated_at,
 ROW_NUMBER() OVER (PARTITION BY l.library_id ORDER BY l.tg_message_id,mm.position,l.id,md.id) AS video_order,
 FIRST_VALUE(md.file_name) OVER (PARTITION BY l.library_id
 ORDER BY CASE WHEN md.file_name!='' THEN 0 ELSE 1 END,l.tg_message_id,mm.position,l.id,md.id) AS first_file_name,
 CASE WHEN md.id=meta.cover_media_id THEN md.id END AS selected_cover
 FROM live l JOIN message_media mm ON mm.message_id=l.id AND mm.role='main'
 JOIN media md ON md.id=mm.media_id LEFT JOIN video_progress p ON p.media_id=md.id
 LEFT JOIN video_library_metadata meta ON meta.message_id=l.library_id
 WHERE md.kind='video' OR (md.kind='document' AND md.mime LIKE 'video/%')
), video_totals AS (
 SELECT library_id,COUNT(*) AS video_count,
 SUM(CASE WHEN duration>0 THEN duration ELSE play_duration END) AS duration,
 SUM(completed) AS completed_count,
 SUM(CASE WHEN completed=1 OR play_position>0 THEN 1 ELSE 0 END) AS started_count,
 MAX(CASE WHEN completed=0 AND play_position>0 THEN updated_at END) AS progress_updated,
 MAX(first_file_name) AS first_file_name,
 COALESCE(MAX(selected_cover),MAX(CASE WHEN video_order=1 THEN media_id END)) AS cover_media_id,
 MAX(CASE WHEN file_name LIKE @library_search ESCAPE '\' THEN 1 ELSE 0 END) AS file_match
 FROM videos GROUP BY library_id
), summary AS (
 SELECT p.*,CASE WHEN COALESCE(meta.title,'')!='' THEN meta.title
 WHEN caption!='' THEN trim(CASE WHEN instr(caption,char(10))>0 THEN substr(caption,1,instr(caption,char(10))-1) ELSE caption END,char(10)||char(13)||' '||char(9))
 ELSE COALESCE(NULLIF(v.first_file_name,''),'未命名视频') END AS title,
 v.cover_media_id,v.video_count,v.duration,v.completed_count,v.started_count,v.progress_updated,v.file_match
 FROM posts p JOIN video_totals v ON v.library_id=p.id
 LEFT JOIN video_library_metadata meta ON meta.message_id=p.id
)`

func videoLibraryScopeCTE(condition string) string {
	return strings.Replace(videoLibraryCTE,
		`FROM messages m WHERE deleted_at = 0 AND thread_root_id = 0`,
		`FROM messages m WHERE deleted_at = 0 AND thread_root_id = 0`+condition, 1)
}
func videoLibraryQueryCTE(q VideoLibraryQuery) (string, []any) {
	args := []any{sql.Named("library_tag", q.TagID), sql.Named("library_search", "%"+likeEscaper.Replace(q.Q)+"%")}
	if q.ChatID > 0 {
		return videoLibraryScopeCTE(" AND m.chat_id=@library_chat"), append(args, sql.Named("library_chat", q.ChatID))
	}
	return videoLibraryCTE, args
}

func normalizeVideoLibraryQuery(q VideoLibraryQuery) (VideoLibraryQuery, error) {
	if q.Kind == "" {
		q.Kind = "all"
	}
	if q.Watched == "" {
		q.Watched = "all"
	}
	if q.View == "" {
		q.View = "all"
	}
	if q.Sort == "" {
		q.Sort = "new"
	}
	if q.Limit == 0 {
		q.Limit = 24
	}
	if q.ChatID < 0 || q.TagID < 0 || q.Limit < 1 || q.Limit > 100 || utf8.RuneCountInString(q.Q) > 500 {
		return q, ErrBadVideoLibrary
	}
	for _, p := range [][2]string{{q.Kind, "all single collection"}, {q.Watched, "all unwatched in_progress completed"}, {q.View, "all continue collections favorites"}, {q.Sort, "new old published duration title"}} {
		if !slices.Contains(strings.Fields(p[1]), p[0]) {
			return q, ErrBadVideoLibrary
		}
	}
	return q, nil
}

type videoLibraryCursor struct {
	Filter string  `json:"f"`
	ID     int64   `json:"i"`
	Number float64 `json:"n"`
	Text   string  `json:"t"`
}

func videoLibraryFilter(q VideoLibraryQuery) string {
	q.Cursor = ""
	q.Limit = 0
	b, _ := json.Marshal(q)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func videoLibraryWhere(q VideoLibraryQuery) (string, []any) {
	w := []string{"1=1"}
	args := []any{}
	if q.TagID > 0 {
		w = append(w, "tag_count>0")
	}
	switch q.Kind {
	case "single":
		w = append(w, "video_count=1")
	case "collection":
		w = append(w, "video_count>1")
	}
	switch q.Watched {
	case "unwatched":
		w = append(w, "started_count=0")
	case "in_progress":
		w = append(w, "started_count>0 AND completed_count<video_count")
	case "completed":
		w = append(w, "completed_count=video_count")
	}
	switch q.View {
	case "continue":
		w = append(w, "progress_updated IS NOT NULL AND completed_count<video_count")
	case "collections":
		w = append(w, "video_count>1")
	case "favorites":
		w = append(w, "favorite_count>0")
	}
	if q.Q != "" {
		w = append(w, `(s.title LIKE ? ESCAPE '\' OR s.description LIKE ? ESCAPE '\' OR s.file_match=1)`)
		term := "%" + likeEscaper.Replace(q.Q) + "%"
		args = append(args, term, term)
	}
	return strings.Join(w, " AND "), args
}
func videoLibraryOrder(q VideoLibraryQuery) (key, dir, op string) {
	key = "added"
	dir = "DESC"
	op = "<"
	switch q.Sort {
	case "old":
		dir = "ASC"
		op = ">"
	case "published":
		key = "date"
	case "duration":
		key = "duration"
	case "title":
		key = "title"
		dir = "ASC"
		op = ">"
	}
	return
}

func (s *Store) ListVideoLibrary(ctx context.Context, q VideoLibraryQuery) (VideoLibraryPage, error) {
	s = s.reader()
	out := VideoLibraryPage{Items: []VideoLibraryItem{}}
	q, err := normalizeVideoLibraryQuery(q)
	if err != nil {
		return out, err
	}
	var c videoLibraryCursor
	if q.Cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		if e != nil || json.Unmarshal(b, &c) != nil || c.Filter != videoLibraryFilter(q) || c.ID <= 0 || math.IsNaN(c.Number) || math.IsInf(c.Number, 0) {
			return out, ErrBadVideoLibrary
		}
	}
	where, filterArgs := videoLibraryWhere(q)
	cte, args := videoLibraryQueryCTE(q)
	args = append(args, filterArgs...)
	err = s.db.QueryRowContext(ctx, cte+` SELECT COUNT(*),COALESCE(SUM(video_count),0),COALESCE(SUM(CASE WHEN video_count>1 THEN 1 ELSE 0 END),0) FROM summary s WHERE `+where, args...).Scan(&out.TotalItems, &out.TotalVideos, &out.Collections)
	if err != nil {
		return out, err
	}
	key, dir, op := videoLibraryOrder(q)
	if q.Cursor != "" {
		where += " AND (s." + key + ",s.id) " + op + " (?,?)"
		var value any = c.Number
		if key == "title" {
			value = c.Text
		}
		args = append(args, value, c.ID)
	}
	args = append(args, q.Limit+1)
	rows, err := s.db.QueryContext(ctx, cte+` SELECT id,`+key+` FROM summary s WHERE `+where+` ORDER BY `+key+` `+dir+`,id `+dir+` LIMIT ?`, args...)
	if err != nil {
		return out, err
	}
	cursors := []videoLibraryCursor{}
	for rows.Next() {
		cur := videoLibraryCursor{Filter: videoLibraryFilter(q)}
		var e error
		if key == "title" {
			e = rows.Scan(&cur.ID, &cur.Text)
		} else {
			e = rows.Scan(&cur.ID, &cur.Number)
		}
		if e != nil {
			rows.Close()
			return out, e
		}
		cursors = append(cursors, cur)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(cursors) > q.Limit {
		cursors = cursors[:q.Limit]
		b, _ := json.Marshal(cursors[len(cursors)-1])
		out.Next = base64.RawURLEncoding.EncodeToString(b)
	}
	for _, cur := range cursors {
		it, e := s.GetVideoLibraryItem(ctx, cur.ID)
		if errors.Is(e, ErrNotFound) {
			continue
		}
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}

func (s *Store) GetVideoLibraryItem(ctx context.Context, id int64) (VideoLibraryItem, error) {
	s = s.reader()
	out := VideoLibraryItem{Videos: []VideoLibraryVideo{}}
	var representative, chat, origin int64
	var source, group string
	err := s.db.QueryRowContext(ctx, `SELECT chat_id,source,origin_chat_id,media_group_id FROM messages WHERE id=? AND deleted_at=0 AND thread_root_id=0`, id).Scan(&chat, &source, &origin, &group)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	cte := videoLibraryScopeCTE(" AND m.id=@library_item")
	args := []any{sql.Named("library_tag", int64(0)), sql.Named("library_search", ""), sql.Named("library_item", id)}
	if group != "" {
		cte = videoLibraryScopeCTE(" AND m.chat_id=@library_chat AND m.source=@library_source AND m.origin_chat_id=@library_origin AND m.media_group_id=@library_group")
		args = append(args, sql.Named("library_chat", chat), sql.Named("library_source", source), sql.Named("library_origin", origin), sql.Named("library_group", group))
	}
	err = s.db.QueryRowContext(ctx, cte+` SELECT id,representative,title,description,date,added,duration,cover_media_id FROM summary`, args...).Scan(&out.ID, &representative, &out.Title, &out.Description, &out.Date, &out.Added, &out.Duration, &out.CoverMediaID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	// Hydrate only this selected post, never the complete library.
	views, err := collectViews(s.db.QueryContext(ctx, cte+` SELECT `+msgCols+` FROM live ORDER BY tg_message_id,id`, args...))
	if err != nil {
		return out, err
	}
	if err = s.hydrate(ctx, views); err != nil {
		return out, err
	}
	byID := map[int64]MessageView{}
	for _, v := range views {
		byID[v.ID] = v
		if v.ID == representative {
			out.Message = v
		}
	}
	rows, err := s.db.QueryContext(ctx, cte+` SELECT message_id,media_id,play_position,play_duration,completed,updated_at FROM videos ORDER BY tg_message_id,media_position,message_id,media_id`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid, mediaID int64
		var p VideoProgress
		if err = rows.Scan(&mid, &mediaID, &p.Position, &p.Duration, &p.Completed, &p.UpdatedAt); err != nil {
			return out, err
		}
		v, ok := byID[mid]
		if !ok {
			continue
		}
		for _, md := range v.Media {
			if md.ID == mediaID && md.Role == "main" {
				out.Videos = append(out.Videos, VideoLibraryVideo{Message: v, Media: md, Progress: p})
				break
			}
		}
	}
	return out, rows.Err()
}

func (s *Store) SaveVideoProgress(ctx context.Context, mediaID int64, p VideoProgress, now int64) (VideoProgress, error) {
	const maxDuration = 365 * 24 * 60 * 60
	if math.IsNaN(p.Position) || math.IsNaN(p.Duration) || math.IsInf(p.Position, 0) || math.IsInf(p.Duration, 0) || p.Position < 0 || p.Duration < 0 || p.Position > p.Duration || p.Duration > maxDuration {
		return VideoProgress{}, ErrBadVideoLibrary
	}
	p.UpdatedAt = now
	result, err := s.db.ExecContext(ctx, `INSERT INTO video_progress(media_id,position,duration,completed,updated_at)
 SELECT ?,?,?,?,? WHERE EXISTS(SELECT 1 FROM message_media mm JOIN messages m ON m.id=mm.message_id JOIN media md ON md.id=mm.media_id
 WHERE md.id=? AND mm.role='main' AND m.deleted_at=0 AND m.thread_root_id=0 AND (md.kind='video' OR (md.kind='document' AND md.mime LIKE 'video/%')))
 ON CONFLICT(media_id) DO UPDATE SET position=excluded.position,duration=excluded.duration,completed=excluded.completed,updated_at=excluded.updated_at`, mediaID, p.Position, p.Duration, p.Completed, now, mediaID)
	if err == nil {
		err = affected(result, nil)
	}
	return p, err
}

func (s *Store) UpdateVideoLibraryItem(ctx context.Context, id int64, patch VideoLibraryUpdate) (VideoLibraryItem, error) {
	if patch.Title != nil && (!utf8.ValidString(*patch.Title) || utf8.RuneCountInString(*patch.Title) > 200) {
		return VideoLibraryItem{}, ErrBadVideoLibrary
	}
	item, err := s.GetVideoLibraryItem(ctx, id)
	if err != nil {
		return item, err
	}
	if patch.CoverMediaID != nil && *patch.CoverMediaID != 0 {
		found := false
		for _, v := range item.Videos {
			if v.Media.ID == *patch.CoverMediaID {
				found = true
				break
			}
		}
		if !found {
			return item, ErrBadVideoLibrary
		}
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO video_library_metadata(message_id) SELECT id FROM messages WHERE id=? AND deleted_at=0 AND thread_root_id=0 ON CONFLICT DO NOTHING`, item.ID); e != nil {
			return e
		}
		if patch.Title != nil {
			if _, e := tx.ExecContext(ctx, `UPDATE video_library_metadata SET title=? WHERE message_id=?`, strings.TrimSpace(*patch.Title), item.ID); e != nil {
				return e
			}
		}
		if patch.CoverMediaID != nil {
			var cover any
			if *patch.CoverMediaID != 0 {
				cover = *patch.CoverMediaID
			}
			if _, e := tx.ExecContext(ctx, `UPDATE video_library_metadata SET cover_media_id=? WHERE message_id=?`, cover, item.ID); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return item, err
	}
	return s.GetVideoLibraryItem(ctx, id)
}
