package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
}

// ListChats lists conversations, most recent first: every bot × sender chat of botID (all bots
// for 0), and with botID 0 also the watched channels' conversations.
func (s *Store) ListChats(ctx context.Context, botID int64) ([]ChatView, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.kind, COALESCE(c.bot_id, 0), c.last_message_at,
			COALESCE(s.tg_user_id, 0), COALESCE(s.first_name, ''), COALESCE(s.last_name, ''), COALESCE(s.username, ''),
			COALESCE(s.avatar_path, ''),
			COALESCE(c.channel_id, 0), COALESCE(ch.title, ''), COALESCE(ch.username, ''), COALESCE(ch.avatar_path, ''),
			COALESCE(w.id, 0), COALESCE(w.enabled, 0), COALESCE(w.status, ''), COALESCE(w.last_error, ''),
			COALESCE(w.window_minutes, 0), COALESCE(w.hits, 0),
			(SELECT COUNT(*) FROM watch_pending p WHERE p.watch_id = w.id),
			COALESCE((SELECT m.kind FROM messages m WHERE m.chat_id = c.id AND m.deleted_at = 0 ORDER BY m.id DESC LIMIT 1), ''),
			COALESCE((SELECT substr(m.text, 1, 200) FROM messages m WHERE m.chat_id = c.id AND m.deleted_at = 0 ORDER BY m.id DESC LIMIT 1), '')
		FROM chats c
			LEFT JOIN senders s ON s.tg_user_id = c.sender_id
			LEFT JOIN channels ch ON ch.channel_id = c.channel_id
			LEFT JOIN channel_watches w ON w.channel_id = c.channel_id
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
			&w.ID, &w.Enabled, &w.Status, &w.Error, &w.WindowMinutes, &w.Hits, &w.Pending, &v.LastKind, &v.LastText); err != nil {
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
	reply_to_tg_message_id, origin_chat_title, origin_link, extra_json, stats_json`

func scanMessageView(r scanner) (MessageView, error) {
	var v MessageView
	var ents, fwd, extra, stats string
	err := r.Scan(&v.ID, &v.ChatID, &v.TgMessageID, &v.Source, &v.MediaGroupID, &v.Date, &v.EditDate, &v.Kind, &v.Text, &ents, &fwd,
		&v.ReplyToTgMessageID, &v.OriginChatTitle, &v.OriginLink, &extra, &stats)
	if ents == "" {
		ents = "[]"
	}
	v.Entities = json.RawMessage(ents)
	v.ForwardOrigin = rawOrNil(fwd)
	v.Extra = rawOrNil(extra)
	v.Stats = rawOrNil(stats)
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
}

func chatScope(chatID int64) scope { return scope{"chat_id = ?", chatID} }

func botScope(botID int64) scope {
	return scope{"chat_id IN (SELECT id FROM chats WHERE bot_id = ?)", botID}
}

func (s *Store) ListMessages(ctx context.Context, chatID, beforeID int64, limit int) ([]MessageView, error) {
	return s.listMessages(ctx, chatScope(chatID), beforeID, limit)
}

// ListBotMessages pages through every chat of one bot as a single timeline.
func (s *Store) ListBotMessages(ctx context.Context, botID, beforeID int64, limit int) ([]MessageView, error) {
	return s.listMessages(ctx, botScope(botID), beforeID, limit)
}

func (s *Store) listMessages(ctx context.Context, sc scope, beforeID int64, limit int) ([]MessageView, error) {
	views, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages
		WHERE `+sc.cond+` AND deleted_at = 0 AND (? = 0 OR id < ?) ORDER BY id DESC LIMIT ?`, sc.arg, beforeID, beforeID, limit))
	if err != nil {
		return nil, err
	}
	// Never cut an album at the page boundary: extend the page down to the lowest id of the
	// oldest message's group (an album lives in one chat). Everything in scope above that id is
	// included too, so a merged timeline whose albums interleave with other senders' messages
	// has no gap for the next page (which starts below this page's oldest id) to skip over.
	if n := len(views); n > 0 && views[n-1].MediaGroupID != "" {
		oldest := views[n-1]
		var low sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `SELECT MIN(id) FROM messages
			WHERE chat_id = ? AND deleted_at = 0 AND media_group_id = ? AND id < ?`, oldest.ChatID, oldest.MediaGroupID, oldest.ID).Scan(&low); err != nil {
			return nil, err
		}
		if low.Valid {
			more, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages
				WHERE `+sc.cond+` AND deleted_at = 0 AND id >= ? AND id < ? ORDER BY id DESC`, sc.arg, low.Int64, oldest.ID))
			if err != nil {
				return nil, err
			}
			views = append(views, more...)
		}
	}
	slices.Reverse(views)
	if err := s.hydrate(ctx, views); err != nil {
		return nil, err
	}
	return views, nil
}

// GetMessageView returns one non-deleted message with its media and reply preview.
func (s *Store) GetMessageView(ctx context.Context, id int64) (MessageView, error) {
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
	return s.listMedia(ctx, chatScope(chatID), typ, beforeID, limit)
}

// ListBotMedia is ListChatMedia over every chat of one bot.
func (s *Store) ListBotMedia(ctx context.Context, botID int64, typ string, beforeID int64, limit int) ([]MessageView, error) {
	return s.listMedia(ctx, botScope(botID), typ, beforeID, limit)
}

func (s *Store) listMedia(ctx context.Context, sc scope, typ string, beforeID int64, limit int) ([]MessageView, error) {
	var cond string
	switch typ {
	case "media":
		cond = `id IN (SELECT mm.message_id FROM message_media mm JOIN media md ON md.id = mm.media_id
			WHERE mm.role = 'main' AND md.kind IN ('photo', 'video', 'animation'))`
	case "file":
		cond = `id IN (SELECT mm.message_id FROM message_media mm JOIN media md ON md.id = mm.media_id
			WHERE mm.role = 'main' AND md.kind IN ('document', 'audio'))`
	case "link":
		cond = `(entities_json LIKE '%"type":"url"%' OR entities_json LIKE '%"type":"text_link"%')`
	default:
		return nil, ErrBadMediaType
	}
	views, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages
		WHERE `+sc.cond+` AND deleted_at = 0 AND (? = 0 OR id < ?) AND `+cond+` ORDER BY id DESC LIMIT ?`,
		sc.arg, beforeID, beforeID, limit))
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
	for i := range views {
		r := views[i].ReplyToTgMessageID
		if r == 0 {
			continue
		}
		var rv ReplyView
		err := s.db.QueryRowContext(ctx, `SELECT id, kind, substr(text, 1, 200) FROM messages
			WHERE chat_id = ? AND source = ? AND tg_message_id = ? AND deleted_at = 0`, views[i].ChatID, model.SourceBotUpdate, r).Scan(&rv.ID, &rv.Kind, &rv.Text)
		if err == nil {
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
