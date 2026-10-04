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
	ID            int64      `json:"id"`
	BotID         int64      `json:"bot_id"`
	Sender        SenderView `json:"sender"`
	LastMessageAt int64      `json:"last_message_at"`
	LastKind      string     `json:"last_kind"`
	LastText      string     `json:"last_text"`
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
}

type ReplyView struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type MessageView struct {
	ID                 int64           `json:"id"`
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
}

func (s *Store) ListChats(ctx context.Context, botID int64) ([]ChatView, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.bot_id, c.last_message_at, s.tg_user_id, s.first_name, s.last_name, s.username, s.avatar_path,
			COALESCE((SELECT m.kind FROM messages m WHERE m.chat_id = c.id AND m.deleted_at = 0 ORDER BY m.id DESC LIMIT 1), ''),
			COALESCE((SELECT substr(m.text, 1, 200) FROM messages m WHERE m.chat_id = c.id AND m.deleted_at = 0 ORDER BY m.id DESC LIMIT 1), '')
		FROM chats c JOIN senders s ON s.tg_user_id = c.sender_id
		WHERE (? = 0 OR c.bot_id = ?)
		ORDER BY c.last_message_at DESC, c.id DESC`, botID, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatView{}
	for rows.Next() {
		var v ChatView
		var avatar string
		if err := rows.Scan(&v.ID, &v.BotID, &v.LastMessageAt, &v.Sender.TgUserID, &v.Sender.FirstName, &v.Sender.LastName,
			&v.Sender.Username, &avatar, &v.LastKind, &v.LastText); err != nil {
			return nil, err
		}
		v.Sender.HasAvatar = avatar != ""
		out = append(out, v)
	}
	return out, rows.Err()
}

const msgCols = `id, tg_message_id, source, media_group_id, date, edit_date, kind, text, entities_json, forward_origin_json,
	reply_to_tg_message_id, origin_chat_title, origin_link, extra_json`

func scanMessageView(r scanner) (MessageView, error) {
	var v MessageView
	var ents, fwd, extra string
	err := r.Scan(&v.ID, &v.TgMessageID, &v.Source, &v.MediaGroupID, &v.Date, &v.EditDate, &v.Kind, &v.Text, &ents, &fwd,
		&v.ReplyToTgMessageID, &v.OriginChatTitle, &v.OriginLink, &extra)
	if ents == "" {
		ents = "[]"
	}
	v.Entities = json.RawMessage(ents)
	v.ForwardOrigin = rawOrNil(fwd)
	v.Extra = rawOrNil(extra)
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

func (s *Store) ListMessages(ctx context.Context, chatID, beforeID int64, limit int) ([]MessageView, error) {
	views, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages
		WHERE chat_id = ? AND deleted_at = 0 AND (? = 0 OR id < ?) ORDER BY id DESC LIMIT ?`, chatID, beforeID, beforeID, limit))
	if err != nil {
		return nil, err
	}
	// Never cut an album at the page boundary: pull in the rest of the oldest message's group.
	if n := len(views); n > 0 && views[n-1].MediaGroupID != "" {
		oldest := views[n-1]
		more, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages
			WHERE chat_id = ? AND deleted_at = 0 AND media_group_id = ? AND id < ? ORDER BY id DESC`, chatID, oldest.MediaGroupID, oldest.ID))
		if err != nil {
			return nil, err
		}
		views = append(views, more...)
	}
	slices.Reverse(views)
	if err := s.hydrate(ctx, chatID, views); err != nil {
		return nil, err
	}
	return views, nil
}

func (s *Store) ListChatMedia(ctx context.Context, chatID int64, typ string, beforeID int64, limit int) ([]MessageView, error) {
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
		WHERE chat_id = ? AND deleted_at = 0 AND (? = 0 OR id < ?) AND `+cond+` ORDER BY id DESC LIMIT ?`,
		chatID, beforeID, beforeID, limit))
	if err != nil {
		return nil, err
	}
	if err := s.hydrate(ctx, chatID, views); err != nil {
		return nil, err
	}
	return views, nil
}

func (s *Store) hydrate(ctx context.Context, chatID int64, views []MessageView) error {
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
			md.waveform, md.state, md.error
		FROM message_media mm JOIN media md ON md.id = mm.media_id
		WHERE mm.message_id IN (`+ph+`) ORDER BY mm.message_id, mm.position`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var msgID int64
		var mv MediaView
		if err := rows.Scan(&msgID, &mv.Role, &mv.ID, &mv.Kind, &mv.Mime, &mv.FileName, &mv.Size, &mv.Width, &mv.Height,
			&mv.Duration, &mv.Waveform, &mv.State, &mv.Error); err != nil {
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
	for i := range views {
		r := views[i].ReplyToTgMessageID
		if r == 0 {
			continue
		}
		var rv ReplyView
		err := s.db.QueryRowContext(ctx, `SELECT id, kind, substr(text, 1, 200) FROM messages
			WHERE chat_id = ? AND source = ? AND tg_message_id = ? AND deleted_at = 0`, chatID, model.SourceBotUpdate, r).Scan(&rv.ID, &rv.Kind, &rv.Text)
		if err == nil {
			views[i].Reply = &rv
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}
