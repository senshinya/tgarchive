// Package botapi converts Bot API messages into model.Message.
package botapi

import (
	"encoding/json"
	"errors"
	"strings"

	"tgarchive/internal/model"
	"tgarchive/internal/tgbot"
)

var ErrNoSender = errors.New("message has no sender")

type Result struct {
	ChatID   int64
	ChatType string
	Sender   model.Sender
	Msg      *model.Message
}

func Convert(raw json.RawMessage) (*Result, error) {
	var m tgbot.Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.From == nil {
		return nil, ErrNoSender
	}
	msg := &model.Message{
		TgMessageID:  m.MessageID,
		Source:       model.SourceBotUpdate,
		MediaGroupID: m.MediaGroupID,
		Date:         m.Date,
		EditDate:     m.EditDate,
		RawFormat:    model.RawBotAPI,
		Raw:          append(json.RawMessage(nil), raw...),
	}
	if m.ReplyToMessage != nil {
		msg.ReplyToTgMessageID = m.ReplyToMessage.MessageID
	}
	msg.ForwardOrigin = convertOrigin(m.ForwardOrigin)
	if m.Caption != "" {
		msg.Text, msg.Entities = m.Caption, convertEntities(m.CaptionEntities)
	} else {
		msg.Text, msg.Entities = m.Text, convertEntities(m.Entities)
	}

	extra := map[string]any{}
	if m.HasMediaSpoiler {
		extra["spoiler"] = true
	}
	fill(msg, &m, extra)
	if len(extra) > 0 {
		b, err := json.Marshal(extra)
		if err != nil {
			return nil, err
		}
		msg.Extra = b
	}
	return &Result{
		ChatID:   m.Chat.ID,
		ChatType: m.Chat.Type,
		Sender: model.Sender{
			TgUserID:  m.From.ID,
			FirstName: m.From.FirstName,
			LastName:  m.From.LastName,
			Username:  m.From.Username,
		},
		Msg: msg,
	}, nil
}

func fill(msg *model.Message, m *tgbot.Message, extra map[string]any) {
	switch {
	case len(m.Photo) > 0:
		msg.Kind = model.KindPhoto
		big := m.Photo[len(m.Photo)-1]
		msg.Media = append(msg.Media, photo(big, model.RoleMain))
		if len(m.Photo) > 1 {
			msg.Media = append(msg.Media, photo(m.Photo[0], model.RoleThumb))
		}
	case m.Animation != nil:
		a := m.Animation
		msg.Kind = model.KindAnimation
		msg.Media = append(msg.Media, file(a.FileUniqueID, a.FileID, "animation", or(a.MimeType, "video/mp4"), a.FileName, a.FileSize, a.Width, a.Height, a.Duration))
		msg.Media = append(msg.Media, thumb(a.Thumbnail)...)
	case m.Video != nil:
		v := m.Video
		msg.Kind = model.KindVideo
		msg.Media = append(msg.Media, file(v.FileUniqueID, v.FileID, "video", or(v.MimeType, "video/mp4"), v.FileName, v.FileSize, v.Width, v.Height, v.Duration))
		msg.Media = append(msg.Media, thumb(v.Thumbnail)...)
	case m.VideoNote != nil:
		v := m.VideoNote
		msg.Kind = model.KindVideoNote
		msg.Media = append(msg.Media, file(v.FileUniqueID, v.FileID, "video_note", "video/mp4", "", v.FileSize, v.Length, v.Length, v.Duration))
		msg.Media = append(msg.Media, thumb(v.Thumbnail)...)
	case m.Voice != nil:
		v := m.Voice
		msg.Kind = model.KindVoice
		msg.Media = append(msg.Media, file(v.FileUniqueID, v.FileID, "voice", or(v.MimeType, "audio/ogg"), "", v.FileSize, 0, 0, v.Duration))
	case m.Audio != nil:
		a := m.Audio
		msg.Kind = model.KindAudio
		msg.Media = append(msg.Media, file(a.FileUniqueID, a.FileID, "audio", or(a.MimeType, "audio/mpeg"), a.FileName, a.FileSize, 0, 0, a.Duration))
		msg.Media = append(msg.Media, thumb(a.Thumbnail)...)
		setIf(extra, "performer", a.Performer)
		setIf(extra, "title", a.Title)
	case m.Sticker != nil:
		s := m.Sticker
		msg.Kind = model.KindSticker
		mime := "image/webp"
		if s.IsAnimated {
			mime = "application/x-tgsticker"
		} else if s.IsVideo {
			mime = "video/webm"
		}
		msg.Media = append(msg.Media, file(s.FileUniqueID, s.FileID, "sticker", mime, "", s.FileSize, s.Width, s.Height, 0))
		setIf(extra, "emoji", s.Emoji)
		setIf(extra, "set_name", s.SetName)
		setIf(extra, "sticker_type", s.Type)
	case m.Document != nil:
		d := m.Document
		msg.Kind = model.KindDocument
		msg.Media = append(msg.Media, file(d.FileUniqueID, d.FileID, "document", or(d.MimeType, "application/octet-stream"), d.FileName, d.FileSize, 0, 0, 0))
		msg.Media = append(msg.Media, thumb(d.Thumbnail)...)
	case m.Venue != nil:
		v := m.Venue
		msg.Kind = model.KindVenue
		extra["latitude"], extra["longitude"] = v.Location.Latitude, v.Location.Longitude
		setIf(extra, "title", v.Title)
		setIf(extra, "address", v.Address)
	case m.Location != nil:
		msg.Kind = model.KindLocation
		extra["latitude"], extra["longitude"] = m.Location.Latitude, m.Location.Longitude
	case m.Contact != nil:
		c := m.Contact
		msg.Kind = model.KindContact
		setIf(extra, "phone_number", c.PhoneNumber)
		setIf(extra, "first_name", c.FirstName)
		setIf(extra, "last_name", c.LastName)
		if c.UserID != 0 {
			extra["user_id"] = c.UserID
		}
	case m.Poll != nil:
		p := m.Poll
		msg.Kind = model.KindPoll
		opts := make([]map[string]any, 0, len(p.Options))
		for _, o := range p.Options {
			opts = append(opts, map[string]any{"text": o.Text, "voter_count": o.VoterCount})
		}
		extra["question"], extra["options"], extra["total_voter_count"] = p.Question, opts, p.TotalVoterCount
		extra["is_anonymous"], extra["poll_type"], extra["multiple"] = p.IsAnonymous, p.Type, p.AllowsMultipleAnswers
	case m.Dice != nil:
		msg.Kind = model.KindDice
		extra["emoji"], extra["value"] = m.Dice.Emoji, m.Dice.Value
	case msg.Text != "":
		msg.Kind = model.KindText
	default:
		msg.Kind = model.KindOther
	}
}

func file(uniq, fileID, kind, mime, name string, size int64, w, h, dur int) model.Media {
	return model.Media{
		DedupeKey: "bot:" + uniq, SourceRef: fileID, Kind: kind, Mime: mime, FileName: name,
		Size: size, Width: w, Height: h, Duration: dur, Role: model.RoleMain,
	}
}

func photo(p tgbot.PhotoSize, role string) model.Media {
	m := file(p.FileUniqueID, p.FileID, "photo", "image/jpeg", "", p.FileSize, p.Width, p.Height, 0)
	m.Role = role
	return m
}

func thumb(p *tgbot.PhotoSize) []model.Media {
	if p == nil {
		return nil
	}
	return []model.Media{photo(*p, model.RoleThumb)}
}

func convertEntities(in []tgbot.MessageEntity) []model.Entity {
	out := make([]model.Entity, 0, len(in))
	for _, e := range in {
		me := model.Entity{Type: e.Type, Offset: e.Offset, Length: e.Length, URL: e.URL, Language: e.Language, CustomEmojiID: e.CustomEmojiID}
		if e.User != nil {
			me.UserID = e.User.ID
		}
		out = append(out, me)
	}
	return out
}

func convertOrigin(o *tgbot.MessageOrigin) *model.ForwardOrigin {
	if o == nil {
		return nil
	}
	f := &model.ForwardOrigin{Type: o.Type, Date: o.Date, Signature: o.AuthorSignature}
	switch o.Type {
	case "user":
		if u := o.SenderUser; u != nil {
			f.Name = strings.TrimSpace(u.FirstName + " " + u.LastName)
			f.Username, f.UserID = u.Username, u.ID
		}
	case "hidden_user":
		f.Name = o.SenderUserName
	case "chat":
		if c := o.SenderChat; c != nil {
			f.Name, f.Username, f.ChatID = c.Title, c.Username, c.ID
		}
	case "channel":
		if c := o.Chat; c != nil {
			f.Name, f.Username, f.ChatID = c.Title, c.Username, c.ID
		}
		f.MessageID = o.MessageID
	}
	return f
}

func setIf(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
