package store

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

const (
	statsDays      = 371 // 53 weeks of daily counts
	statsWatchDays = 30
	statsTopChats  = 10
)

type StatsTotals struct {
	Messages     int64 `json:"messages"`
	PrivateChats int64 `json:"private_chats"`
	ChannelChats int64 `json:"channel_chats"`
	MediaFiles   int64 `json:"media_files"`
	MediaBytes   int64 `json:"media_bytes"` // downloaded files only
	DBBytes      int64 `json:"db_bytes"`
	DiskFree     int64 `json:"disk_free"` // filled in by the HTTP layer
	DiskTotal    int64 `json:"disk_total"`
}

type DayCount struct {
	Day   string `json:"day"` // YYYY-MM-DD in the requested time zone
	Count int64  `json:"count"`
}

type MonthStat struct {
	Month      string `json:"month"` // YYYY-MM
	Messages   int64  `json:"messages"`
	MediaBytes int64  `json:"media_bytes"` // a file counts in the month of its first message
}

type ChatStat struct {
	ChatID     int64 `json:"chat_id"`
	Messages   int64 `json:"messages"`
	MediaBytes int64 `json:"media_bytes"`
}

type KindStat struct {
	Kind  string `json:"kind"` // the main media kind, or "other" (thumbnails, link previews…)
	Count int64  `json:"count"`
	Bytes int64  `json:"bytes"`
}

type WatchStat struct {
	WatchID  int64      `json:"watch_id"`
	ChatID   int64      `json:"chat_id"`
	Title    string     `json:"title"`
	Daily    []DayCount `json:"daily"` // archived posts per day (an album counts once), last 30 days
	Hits     int64      `json:"hits"`
	Scanned  int64      `json:"scanned"`
	ScanHits int64      `json:"scan_hits"`
}

type Stats struct {
	Totals      StatsTotals      `json:"totals"`
	Daily       []DayCount       `json:"daily"`
	Monthly     []MonthStat      `json:"monthly"`
	TopChats    []ChatStat       `json:"top_chats"`
	MediaKinds  []KindStat       `json:"media_kinds"`
	MediaStates map[string]int64 `json:"media_states"`
	Watches     []WatchStat      `json:"watches"`
}

// liveMedia is every media reference of a message that is not deleted.
const liveMedia = `SELECT mm.media_id, mm.role, m.chat_id, m.date FROM message_media mm JOIN messages m ON m.id = mm.message_id
	WHERE m.deleted_at = 0`

// Stats summarises the archive as of now. Days and months are cut in the time zone tzMinutes
// east of UTC. Messages and files of deleted messages do not count; a file several messages
// share counts once.
func (s *Store) Stats(ctx context.Context, tzMinutes int, now int64) (*Stats, error) {
	off := int64(tzMinutes) * 60
	local := now + off
	today := local - local%86400
	st := &Stats{
		Daily: []DayCount{}, Monthly: []MonthStat{}, TopChats: []ChatStat{}, MediaKinds: []KindStat{}, Watches: []WatchStat{},
		MediaStates: map[string]int64{StateDone: 0, StatePending: 0, StateFailed: 0, StateTooLarge: 0},
	}
	t := &st.Totals
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE deleted_at = 0").Scan(&t.Messages); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(kind = 'private'), 0), COALESCE(SUM(kind = 'channel'), 0) FROM chats`).
		Scan(&t.PrivateChats, &t.ChannelChats); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN state = 'done' THEN size END), 0) FROM media
		WHERE id IN (SELECT media_id FROM (`+liveMedia+`))`).Scan(&t.MediaFiles, &t.MediaBytes); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()").Scan(&t.DBBytes); err != nil {
		return nil, err
	}

	err := s.collect(ctx, func(r *sql.Rows) error {
		var d DayCount
		if err := r.Scan(&d.Day, &d.Count); err != nil {
			return err
		}
		st.Daily = append(st.Daily, d)
		return nil
	}, `SELECT date(date + ?, 'unixepoch') AS d, COUNT(*) FROM messages WHERE deleted_at = 0 AND date + ? >= ?
		GROUP BY d ORDER BY d`, off, off, today-(statsDays-1)*86400)
	if err != nil {
		return nil, err
	}

	months := map[string]*MonthStat{}
	month := func(k string) *MonthStat {
		if months[k] == nil {
			months[k] = &MonthStat{Month: k}
		}
		return months[k]
	}
	err = s.collect(ctx, func(r *sql.Rows) error {
		var k string
		var n int64
		if err := r.Scan(&k, &n); err != nil {
			return err
		}
		month(k).Messages = n
		return nil
	}, `SELECT strftime('%Y-%m', date + ?, 'unixepoch') AS k, COUNT(*) FROM messages WHERE deleted_at = 0 GROUP BY k`, off)
	if err != nil {
		return nil, err
	}
	err = s.collect(ctx, func(r *sql.Rows) error {
		var k string
		var n int64
		if err := r.Scan(&k, &n); err != nil {
			return err
		}
		month(k).MediaBytes = n
		return nil
	}, `SELECT strftime('%Y-%m', f.first + ?, 'unixepoch') AS k, SUM(md.size) FROM media md
		JOIN (SELECT media_id, MIN(date) AS first FROM (`+liveMedia+`) GROUP BY media_id) f ON f.media_id = md.id
		WHERE md.state = 'done' GROUP BY k`, off)
	if err != nil {
		return nil, err
	}
	for _, m := range months {
		st.Monthly = append(st.Monthly, *m)
	}
	sort.Slice(st.Monthly, func(i, j int) bool { return st.Monthly[i].Month < st.Monthly[j].Month })

	err = s.collect(ctx, func(r *sql.Rows) error {
		var c ChatStat
		if err := r.Scan(&c.ChatID, &c.Messages); err != nil {
			return err
		}
		st.TopChats = append(st.TopChats, c)
		return nil
	}, `SELECT chat_id, COUNT(*) AS n FROM messages WHERE deleted_at = 0 GROUP BY chat_id ORDER BY n DESC, chat_id LIMIT ?`, statsTopChats)
	if err != nil {
		return nil, err
	}
	if len(st.TopChats) > 0 {
		args := []any{}
		idx := map[int64]int{}
		for i, c := range st.TopChats {
			args = append(args, c.ChatID)
			idx[c.ChatID] = i
		}
		err = s.collect(ctx, func(r *sql.Rows) error {
			var id, n int64
			if err := r.Scan(&id, &n); err != nil {
				return err
			}
			st.TopChats[idx[id]].MediaBytes = n
			return nil
		}, `SELECT x.chat_id, SUM(md.size) FROM (SELECT DISTINCT chat_id, media_id FROM (`+liveMedia+`) WHERE chat_id IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)) x
			JOIN media md ON md.id = x.media_id WHERE md.state = 'done' GROUP BY x.chat_id`, args...)
		if err != nil {
			return nil, err
		}
	}

	err = s.collect(ctx, func(r *sql.Rows) error {
		var k KindStat
		if err := r.Scan(&k.Kind, &k.Count, &k.Bytes); err != nil {
			return err
		}
		st.MediaKinds = append(st.MediaKinds, k)
		return nil
	}, `SELECT CASE WHEN f.main THEN md.kind ELSE 'other' END AS k, COUNT(*) AS n,
			COALESCE(SUM(CASE WHEN md.state = 'done' THEN md.size END), 0)
		FROM media md JOIN (SELECT media_id, MAX(role = 'main') AS main FROM (`+liveMedia+`) GROUP BY media_id) f ON f.media_id = md.id
		GROUP BY k ORDER BY n DESC, k`)
	if err != nil {
		return nil, err
	}
	err = s.collect(ctx, func(r *sql.Rows) error {
		var k string
		var n int64
		if err := r.Scan(&k, &n); err != nil {
			return err
		}
		st.MediaStates[k] = n
		return nil
	}, `SELECT state, COUNT(*) FROM media WHERE id IN (SELECT media_id FROM (`+liveMedia+`)) GROUP BY state`)
	if err != nil {
		return nil, err
	}

	byChat := map[int64]int{}
	err = s.collect(ctx, func(r *sql.Rows) error {
		w := WatchStat{Daily: []DayCount{}}
		if err := r.Scan(&w.WatchID, &w.ChatID, &w.Title, &w.Hits, &w.Scanned, &w.ScanHits); err != nil {
			return err
		}
		if w.ChatID != 0 {
			byChat[w.ChatID] = len(st.Watches)
		}
		st.Watches = append(st.Watches, w)
		return nil
	}, `SELECT w.id, COALESCE(c.id, 0), ch.title, w.hits, w.scanned, w.scan_hits FROM channel_watches w
		JOIN channels ch ON ch.channel_id = w.channel_id LEFT JOIN chats c ON c.channel_id = w.channel_id ORDER BY w.id`)
	if err != nil {
		return nil, err
	}
	err = s.collect(ctx, func(r *sql.Rows) error {
		var chat int64
		var d DayCount
		if err := r.Scan(&chat, &d.Day, &d.Count); err != nil {
			return err
		}
		if i, ok := byChat[chat]; ok {
			st.Watches[i].Daily = append(st.Watches[i].Daily, d)
		}
		return nil
	}, `SELECT chat_id, date(date + ?, 'unixepoch') AS d,
			COUNT(DISTINCT CASE WHEN media_group_id = '' THEN 'm' || id ELSE 'g' || media_group_id END)
		FROM messages WHERE source = 'channel_watch' AND deleted_at = 0 AND date + ? >= ? GROUP BY chat_id, d ORDER BY d`,
		off, off, today-(statsWatchDays-1)*86400)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// collect runs a query and hands each row to fn, closing the rows before returning.
func (s *Store) collect(ctx context.Context, fn func(*sql.Rows) error, query string, args ...any) error {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
