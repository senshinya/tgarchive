package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"

	"tgarchive/internal/model"
)

var ErrBadMediaType = errors.New("unknown shared media type")

type SenderView struct {
	TgUserID  int64  `json:"tg_user_id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
	HasAvatar bool   `json:"has_avatar"`
}

type ChatView struct {
	ID            int64        `json:"id"`
	Kind          string       `json:"kind"` // private / channel
	BotID         int64        `json:"bot_id"`
	Sender        SenderView   `json:"sender"`
	Channel       *ChannelView `json:"channel"`
	Watch         *WatchBrief  `json:"watch"`
	LastMessageAt int64        `json:"last_message_at"`
	LastKind      string       `json:"last_kind"`
	LastText      string       `json:"last_text"`
	// LastReadPos is the Pos of the last post read; Unread counts the posts after it (channels
	// only). A post archived late, behind it, is not unread.
	LastReadPos int64 `json:"last_read_pos"`
	Unread      int64 `json:"unread"`
	// FirstUnreadID is the first post after LastReadPos (0: none), where reading resumes.
	FirstUnreadID int64 `json:"first_unread_id"`
}

type ChannelView struct {
	ChannelID int64  `json:"channel_id"`
	Title     string `json:"title"`
	Username  string `json:"username"`
	HasAvatar bool   `json:"has_avatar"`
}

// WatchBrief is what the chat list and header show about a channel conversation's watch.
type WatchBrief struct {
	ID            int64  `json:"id"`
	Enabled       bool   `json:"enabled"`
	Status        string `json:"status"`
	Error         string `json:"error"`
	WindowMinutes int    `json:"window_minutes"`
	Pending       int64  `json:"pending"`
	Hits          int64  `json:"hits"`
}

type MediaView struct {
	ID       int64  `json:"id"`
	Role     string `json:"role"`
	Kind     string `json:"kind"`
	Mime     string `json:"mime"`
	FileName string `json:"file_name"`
	Size     int64  `json:"size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Duration int    `json:"duration"`
	Waveform []byte `json:"waveform,omitempty"`
	State    string `json:"state"`
	Error    string `json:"error"`
	// CompatCodec names the original's video codec when a browser-playable copy exists
	// (served at /media/{id}?compat=1); empty otherwise.
	CompatCodec string `json:"compat_codec,omitempty"`
}

type ReplyView struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
	// Extra carries the quoted comment's author (extra.from); bot chats leave it out.
	Extra json.RawMessage `json:"extra,omitempty"`
}

type MessageView struct {
	ID                 int64           `json:"id"`
	ChatID             int64           `json:"chat_id"`
	TgMessageID        int64           `json:"tg_message_id"`
	Source             string          `json:"source"`
	MediaGroupID       string          `json:"media_group_id"`
	Date               int64           `json:"date"`
	EditDate           int64           `json:"edit_date"`
	Kind               string          `json:"kind"`
	Text               string          `json:"text"`
	Entities           json.RawMessage `json:"entities"`
	ForwardOrigin      json.RawMessage `json:"forward_origin,omitempty"`
	ReplyToTgMessageID int64           `json:"reply_to_tg_message_id"`
	Reply              *ReplyView      `json:"reply,omitempty"`
	OriginChatTitle    string          `json:"origin_chat_title"`
	OriginLink         string          `json:"origin_link"`
	Extra              json.RawMessage `json:"extra,omitempty"`
	Media              []MediaView     `json:"media"`
	Article            *ArticleSummary `json:"article,omitempty"`
	// Stats is the snapshot taken when a watched channel post was archived (channel_watch only).
	Stats json.RawMessage `json:"stats,omitempty"`
	// Favorite is set when the message is a favorite.
	Favorite *FavoriteInfo `json:"favorite"`
	// ThreadRootID is the archived post a comment belongs to; 0 for anything but a comment.
	ThreadRootID int64 `json:"thread_root_id"`
	// Pos orders a conversation, ties broken by id: a channel's posts and comments by their
	// Telegram message id (when they were posted), everything else by id (when it arrived).
	Pos int64 `json:"pos"`
}

// lastMessage finds a chat's last message (the preview): the latest post of a channel, the
// latest arrival elsewhere. Each branch is a plain ORDER BY its index answers with one row (an
// ORDER BY CASE could not use one), and only the branch the chat's kind takes is run.
const lastMessage = `CASE WHEN c.kind = 'channel' THEN
		(SELECT m.id FROM messages m WHERE m.chat_id = c.id AND m.thread_root_id = 0 AND m.deleted_at = 0
			ORDER BY m.tg_message_id DESC, m.id DESC LIMIT 1)
	ELSE
		(SELECT m.id FROM messages m WHERE m.chat_id = c.id AND m.thread_root_id = 0 AND m.deleted_at = 0
			ORDER BY m.id DESC LIMIT 1)
	END`

// ListChats lists conversations, most recent first: every bot × sender chat of botID (all bots
// for 0), and with botID 0 also the watched channels' conversations.
func (s *Store) ListChats(ctx context.Context, botID int64) ([]ChatView, error) {
	s = s.reader()
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.kind, COALESCE(c.bot_id, 0), c.last_message_at,
			COALESCE(s.tg_user_id, 0), COALESCE(s.first_name, ''), COALESCE(s.last_name, ''), COALESCE(s.username, ''),
			COALESCE(s.avatar_path, ''),
			COALESCE(c.channel_id, 0), COALESCE(ch.title, ''), COALESCE(ch.username, ''), COALESCE(ch.avatar_path, ''),
			COALESCE(w.id, 0), COALESCE(w.enabled, 0), COALESCE(w.status, ''), COALESCE(w.last_error, ''),
			COALESCE(w.window_minutes, 0), COALESCE(w.hits, 0),
			(SELECT COUNT(*) FROM watch_pending p WHERE p.watch_id = w.id),
			COALESCE(lm.kind, ''), COALESCE(substr(lm.text, 1, 200), ''),
			c.last_read_pos,
			CASE WHEN c.kind = 'channel' THEN
				(SELECT COUNT(*) FROM messages m WHERE m.chat_id = c.id AND m.deleted_at = 0 AND m.thread_root_id = 0 AND m.tg_message_id > c.last_read_pos) ELSE 0 END,
			CASE WHEN c.kind = 'channel' THEN
				COALESCE((SELECT m.id FROM messages m WHERE m.chat_id = c.id AND m.deleted_at = 0 AND m.thread_root_id = 0 AND m.tg_message_id > c.last_read_pos
					ORDER BY m.tg_message_id, m.id LIMIT 1), 0) ELSE 0 END
		FROM chats c
			LEFT JOIN senders s ON s.tg_user_id = c.sender_id
			LEFT JOIN channels ch ON ch.channel_id = c.channel_id
			LEFT JOIN channel_watches w ON w.channel_id = c.channel_id
			LEFT JOIN messages lm ON lm.id = `+lastMessage+`
		WHERE (? = 0 OR c.bot_id = ?)
		ORDER BY c.last_message_at DESC, c.id DESC`, botID, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatView{}
	for rows.Next() {
		var v ChatView
		var avatar, chAvatar string
		var ch ChannelView
		var w WatchBrief
		if err := rows.Scan(&v.ID, &v.Kind, &v.BotID, &v.LastMessageAt, &v.Sender.TgUserID, &v.Sender.FirstName, &v.Sender.LastName,
			&v.Sender.Username, &avatar, &ch.ChannelID, &ch.Title, &ch.Username, &chAvatar,
			&w.ID, &w.Enabled, &w.Status, &w.Error, &w.WindowMinutes, &w.Hits, &w.Pending, &v.LastKind, &v.LastText, &v.LastReadPos, &v.Unread, &v.FirstUnreadID); err != nil {
			return nil, err
		}
		v.Sender.HasAvatar = avatar != ""
		if ch.ChannelID != 0 {
			ch.HasAvatar = chAvatar != ""
			v.Channel = &ch
		}
		if w.ID != 0 {
			v.Watch = &w
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

const msgCols = `id, chat_id, tg_message_id, source, media_group_id, date, edit_date, kind, text, entities_json, forward_origin_json,
	reply_to_tg_message_id, origin_chat_title, origin_link, extra_json, stats_json, thread_root_id`

func scanMessageView(r scanner) (MessageView, error) {
	var v MessageView
	var ents, fwd, extra, stats string
	err := r.Scan(&v.ID, &v.ChatID, &v.TgMessageID, &v.Source, &v.MediaGroupID, &v.Date, &v.EditDate, &v.Kind, &v.Text, &ents, &fwd,
		&v.ReplyToTgMessageID, &v.OriginChatTitle, &v.OriginLink, &extra, &stats, &v.ThreadRootID)
	if ents == "" {
		ents = "[]"
	}
	v.Entities = json.RawMessage(ents)
	v.ForwardOrigin = rawOrNil(fwd)
	v.Extra = rawOrNil(extra)
	v.Stats = rawOrNil(stats)
	v.Pos = v.ID
	if v.Source == model.SourceChannelWatch || v.Source == model.SourceChannelComment {
		v.Pos = v.TgMessageID
	}
	return v, err
}

func rawOrNil(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

func collectViews(rows *sql.Rows, err error) ([]MessageView, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MessageView{}
	for rows.Next() {
		v, err := scanMessageView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// scope selects the messages of one chat, or of every chat of one bot (the merged per-bot timeline).
type scope struct {
	cond string // SQL condition on messages, with one placeholder
	arg  int64
	// key is the column the scope is ordered by, ties broken by id: "id" (arrival) or
	// "tg_message_id" (a channel's posts and comments, in the order they were posted, however
	// late they were archived). MessageView.Pos is that key.
	key string
}

// chatScope is a conversation's timeline: the comments of its posts are not part of it.
func (s *Store) chatScope(ctx context.Context, chatID int64) (scope, error) {
	var kind string
	err := s.db.QueryRowContext(ctx, "SELECT kind FROM chats WHERE id = ?", chatID).Scan(&kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return scope{}, err
	}
	key := "id"
	if kind == "channel" {
		key = "tg_message_id"
	}
	return scope{"chat_id = ? AND thread_root_id = 0", chatID, key}, nil
}

// threadScope is the comments of one archived post. The thread indexes cover comments only
// (thread_root_id != 0), and SQLite uses them only when the query states that condition itself.
func threadScope(rootID int64) scope {
	return scope{"thread_root_id != 0 AND thread_root_id = ?", rootID, "tg_message_id"}
}

func botScope(botID int64) scope {
	return scope{"chat_id IN (SELECT id FROM chats WHERE bot_id = ?)", botID, "id"}
}

// Page picks a conversation page: messages older than Before (the newest page when all are 0),
// newer than After, or a window around Around (that message and older, plus newer). At most one
// is set. All are message ids; "older" and "newer" follow the scope's order.
type Page struct{ Before, After, Around int64 }

func (s *Store) ListMessages(ctx context.Context, chatID, beforeID int64, limit int) ([]MessageView, error) {
	s = s.reader()
	return s.ListMessagesPage(ctx, chatID, Page{Before: beforeID}, limit)
}

// ListBotMessages pages through every chat of one bot as a single timeline.
func (s *Store) ListBotMessages(ctx context.Context, botID, beforeID int64, limit int) ([]MessageView, error) {
	s = s.reader()
	return s.listMessages(ctx, botScope(botID), Page{Before: beforeID}, limit)
}

func (s *Store) ListMessagesPage(ctx context.Context, chatID int64, p Page, limit int) ([]MessageView, error) {
	s = s.reader()
	sc, err := s.chatScope(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return s.listMessages(ctx, sc, p, limit)
}

// ListCommentsPage pages through the comments of archived post rootID, which must be a live
// channel post of chat chatID (ErrNotFound otherwise).
func (s *Store) ListCommentsPage(ctx context.Context, chatID, rootID int64, p Page, limit int) ([]MessageView, error) {
	s = s.reader()
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM messages WHERE id = ? AND chat_id = ? AND source = 'channel_watch' AND deleted_at = 0`,
		rootID, chatID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sc := threadScope(rootID)
	if p.After == rootID {
		// After the post itself: from the first comment (they are numbered by the discussion
		// group, not the channel).
		views, err := s.newer(ctx, sc, cursor{math.MinInt64, math.MinInt64}, limit)
		if err == nil {
			err = s.hydrate(ctx, views)
		}
		return views, err
	}
	return s.listMessages(ctx, sc, p, limit)
}

func (s *Store) ListBotMessagesPage(ctx context.Context, botID int64, p Page, limit int) ([]MessageView, error) {
	s = s.reader()
	return s.listMessages(ctx, botScope(botID), p, limit)
}

// cursor is a position in a scope's order: (key, id).
type cursor struct{ key, id int64 }

// at returns the position of message id in sc's order; ok is false when there is no such message.
func (s *Store) at(ctx context.Context, sc scope, id int64) (c cursor, ok bool, err error) {
	if sc.key == "id" {
		return cursor{id, id}, true, nil
	}
	err = s.db.QueryRowContext(ctx, "SELECT "+sc.key+" FROM messages WHERE id = ?", id).Scan(&c.key)
	if errors.Is(err, sql.ErrNoRows) {
		return cursor{}, false, nil
	}
	return cursor{c.key, id}, err == nil, err
}

// listMessages returns one page in ascending order. Albums are never cut at a page edge.
func (s *Store) listMessages(ctx context.Context, sc scope, p Page, limit int) ([]MessageView, error) {
	ref := max(p.After, p.Around, p.Before)
	var c cursor
	if ref > 0 {
		var ok bool
		var err error
		if c, ok, err = s.at(ctx, sc, ref); err != nil || !ok {
			return []MessageView{}, err
		}
	}
	var views []MessageView
	var err error
	switch {
	case p.After > 0:
		views, err = s.newer(ctx, sc, c, limit)
	case p.Around > 0:
		var newer []MessageView
		// The older half (rounded up) holds the target itself.
		if views, err = s.older(ctx, sc, &cursor{c.key, c.id + 1}, limit-limit/2); err == nil {
			newer, err = s.newer(ctx, sc, c, limit/2)
			views = append(views, newer...)
		}
	case p.Before > 0:
		views, err = s.older(ctx, sc, &c, limit)
	default:
		views, err = s.older(ctx, sc, nil, limit)
	}
	if err != nil {
		return nil, err
	}
	if err := s.hydrate(ctx, views); err != nil {
		return nil, err
	}
	return views, nil
}

// older returns up to limit messages before c (nil: the newest), ascending.
func (s *Store) older(ctx context.Context, sc scope, c *cursor, limit int) ([]MessageView, error) {
	k := sc.key
	q := `SELECT ` + msgCols + ` FROM messages WHERE ` + sc.cond + ` AND deleted_at = 0`
	args := []any{sc.arg}
	if c != nil {
		q += ` AND (` + k + `, id) < (?, ?)`
		args = append(args, c.key, c.id)
	}
	views, err := collectViews(s.db.QueryContext(ctx, q+` ORDER BY `+k+` DESC, id DESC LIMIT ?`, append(args, limit)...))
	if err != nil {
		return nil, err
	}
	// Never cut an album at the page boundary: extend the page down to the first message of the
	// oldest message's group (an album lives in one chat). Everything in scope from there on is
	// included too, so a merged timeline whose albums interleave with other senders' messages
	// has no gap for the next page (which starts before this page's oldest message) to skip over.
	if n := len(views); n > 0 && views[n-1].MediaGroupID != "" {
		oldest := views[n-1]
		var low cursor
		err := s.db.QueryRowContext(ctx, `SELECT `+k+`, id FROM messages
			WHERE chat_id = ? AND deleted_at = 0 AND media_group_id = ? AND (`+k+`, id) < (?, ?) ORDER BY `+k+`, id LIMIT 1`,
			oldest.ChatID, oldest.MediaGroupID, oldest.Pos, oldest.ID).Scan(&low.key, &low.id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			more, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages
				WHERE `+sc.cond+` AND deleted_at = 0 AND (`+k+`, id) >= (?, ?) AND (`+k+`, id) < (?, ?) ORDER BY `+k+` DESC, id DESC`,
				sc.arg, low.key, low.id, oldest.Pos, oldest.ID))
			if err != nil {
				return nil, err
			}
			views = append(views, more...)
		}
	}
	slices.Reverse(views)
	return views, nil
}

// newer returns up to limit messages after c, ascending; like older, it completes an album the
// page edge would cut, along with everything in scope up to the album's last message.
func (s *Store) newer(ctx context.Context, sc scope, c cursor, limit int) ([]MessageView, error) {
	k := sc.key
	views, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages
		WHERE `+sc.cond+` AND deleted_at = 0 AND (`+k+`, id) > (?, ?) ORDER BY `+k+`, id LIMIT ?`, sc.arg, c.key, c.id, limit))
	if err != nil {
		return nil, err
	}
	if n := len(views); n > 0 && views[n-1].MediaGroupID != "" {
		newest := views[n-1]
		var high cursor
		err := s.db.QueryRowContext(ctx, `SELECT `+k+`, id FROM messages
			WHERE chat_id = ? AND deleted_at = 0 AND media_group_id = ? AND (`+k+`, id) > (?, ?) ORDER BY `+k+` DESC, id DESC LIMIT 1`,
			newest.ChatID, newest.MediaGroupID, newest.Pos, newest.ID).Scan(&high.key, &high.id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			more, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages
				WHERE `+sc.cond+` AND deleted_at = 0 AND (`+k+`, id) > (?, ?) AND (`+k+`, id) <= (?, ?) ORDER BY `+k+`, id`,
				sc.arg, newest.Pos, newest.ID, high.key, high.id))
			if err != nil {
				return nil, err
			}
			views = append(views, more...)
		}
	}
	return views, nil
}

// GetMessageView returns one non-deleted message with its media and reply preview.
func (s *Store) GetMessageView(ctx context.Context, id int64) (MessageView, error) {
	s = s.reader()
	views, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages WHERE id = ? AND deleted_at = 0`, id))
	if err != nil {
		return MessageView{}, err
	}
	if len(views) == 0 {
		return MessageView{}, ErrNotFound
	}
	if err := s.hydrate(ctx, views); err != nil {
		return MessageView{}, err
	}
	return views[0], nil
}

func (s *Store) ListChatMedia(ctx context.Context, chatID int64, typ string, beforeID int64, limit int) ([]MessageView, error) {
	s = s.reader()
	sc, err := s.chatScope(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return s.listMedia(ctx, sc, typ, beforeID, limit)
}

// ListBotMedia is ListChatMedia over every chat of one bot.
func (s *Store) ListBotMedia(ctx context.Context, botID int64, typ string, beforeID int64, limit int) ([]MessageView, error) {
	s = s.reader()
	return s.listMedia(ctx, botScope(botID), typ, beforeID, limit)
}

func (s *Store) listMedia(ctx context.Context, sc scope, typ string, beforeID int64, limit int) ([]MessageView, error) {
	// Media are checked per message as the page is walked: an IN (subquery) would first gather
	// every message of the archive that has such media.
	var cond string
	switch typ {
	case "media":
		cond = `EXISTS (SELECT 1 FROM message_media mm JOIN media md ON md.id = mm.media_id
			WHERE mm.message_id = messages.id AND mm.role = 'main' AND md.kind IN ('photo', 'video', 'animation'))`
	case "file":
		cond = `EXISTS (SELECT 1 FROM message_media mm JOIN media md ON md.id = mm.media_id
			WHERE mm.message_id = messages.id AND mm.role = 'main' AND md.kind IN ('document', 'audio'))`
	case "link":
		cond = `(entities_json LIKE '%"type":"url"%' OR entities_json LIKE '%"type":"text_link"%')`
	default:
		return nil, ErrBadMediaType
	}
	q := `SELECT ` + msgCols + ` FROM messages WHERE ` + sc.cond + ` AND deleted_at = 0 AND ` + cond
	args := []any{sc.arg}
	if beforeID > 0 {
		c, ok, err := s.at(ctx, sc, beforeID)
		if err != nil || !ok {
			return []MessageView{}, err
		}
		q += ` AND (` + sc.key + `, id) < (?, ?)`
		args = append(args, c.key, c.id)
	}
	views, err := collectViews(s.db.QueryContext(ctx, q+` ORDER BY `+sc.key+` DESC, id DESC LIMIT ?`, append(args, limit)...))
	if err != nil {
		return nil, err
	}
	if err := s.hydrate(ctx, views); err != nil {
		return nil, err
	}
	return views, nil
}

func (s *Store) hydrate(ctx context.Context, views []MessageView) error {
	if len(views) == 0 {
		return nil
	}
	idx := make(map[int64]int, len(views))
	args := make([]any, 0, len(views))
	for i, v := range views {
		idx[v.ID] = i
		args = append(args, v.ID)
		views[i].Media = []MediaView{}
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(views)), ",")
	rows, err := s.db.QueryContext(ctx, `
		SELECT mm.message_id, mm.role, md.id, md.kind, md.mime, md.file_name, md.size, md.width, md.height, md.duration,
			md.waveform, md.state, md.error, CASE WHEN md.compat_state = 'done' THEN md.compat_codec ELSE '' END
		FROM message_media mm JOIN media md ON md.id = mm.media_id
		WHERE mm.message_id IN (`+ph+`) AND mm.role != 'article' ORDER BY mm.message_id, mm.position`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var msgID int64
		var mv MediaView
		if err := rows.Scan(&msgID, &mv.Role, &mv.ID, &mv.Kind, &mv.Mime, &mv.FileName, &mv.Size, &mv.Width, &mv.Height,
			&mv.Duration, &mv.Waveform, &mv.State, &mv.Error, &mv.CompatCodec); err != nil {
			rows.Close()
			return err
		}
		i := idx[msgID]
		views[i].Media = append(views[i].Media, mv)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if err := s.hydrateArticles(ctx, views, idx, ph, args); err != nil {
		return err
	}
	if err := s.hydrateFavorites(ctx, views, idx, ph, args); err != nil {
		return err
	}
	for i := range views {
		r := views[i].ReplyToTgMessageID
		if r == 0 {
			continue
		}
		// A bot chat's replies quote its own updates; a comment quotes another comment.
		src := model.SourceBotUpdate
		if views[i].Source == model.SourceChannelComment {
			src = model.SourceChannelComment
		}
		var rv ReplyView
		var extra string
		err := s.db.QueryRowContext(ctx, `SELECT id, kind, substr(text, 1, 200), extra_json FROM messages
			WHERE chat_id = ? AND source = ? AND tg_message_id = ? AND deleted_at = 0`, views[i].ChatID, src, r).Scan(&rv.ID, &rv.Kind, &rv.Text, &extra)
		if err == nil {
			if src == model.SourceChannelComment {
				rv.Extra = rawOrNil(extra)
			}
			views[i].Reply = &rv
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

// hydrateArticles attaches the Telegraph card summary to messages that have a job.
func (s *Store) hydrateArticles(ctx context.Context, views []MessageView, idx map[int64]int, ph string, args []any) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT j.message_id, j.state, j.error, j.path, COALESCE(a.url, ''), COALESCE(a.title, ''), COALESCE(a.description, ''),
			COALESCE(a.author_name, ''), COALESCE((SELECT md.id FROM media md WHERE md.id = a.image_media_id AND md.state = 'done'), 0)
		FROM telegraph_jobs j LEFT JOIN articles a ON a.message_id = j.message_id
		WHERE j.message_id IN (`+ph+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var msgID int64
		var path string
		a := &ArticleSummary{}
		if err := rows.Scan(&msgID, &a.State, &a.Error, &path, &a.URL, &a.Title, &a.Description, &a.AuthorName, &a.ImageMediaID); err != nil {
			return err
		}
		if a.URL == "" {
			a.URL = articleURL(path)
		}
		views[idx[msgID]].Article = a
	}
	return rows.Err()
}
