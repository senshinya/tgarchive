// Package mtproto converts channel / supergroup messages fetched over MTProto (gotd) into model.Message.
package mtproto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"

	"tgarchive/internal/model"
)

// Channel identifies the chat a fetched message came from.
type Channel struct {
	ID         int64 // raw MTProto channel id, as in t.me/c/<id>/...
	AccessHash int64
	Title      string
	Username   string
}

func ChannelFrom(c *tg.Channel) Channel {
	return Channel{ID: c.ID, AccessHash: c.AccessHash, Title: c.Title, Username: c.Username}
}

// Names resolves the peers that forward headers and message authors refer to.
type Names struct {
	Users    map[int64]*tg.User
	Chats    map[int64]*tg.Chat
	Channels map[int64]*tg.Channel
}

func NamesFrom(users []tg.UserClass, chats []tg.ChatClass) Names {
	n := Names{Users: map[int64]*tg.User{}, Chats: map[int64]*tg.Chat{}, Channels: map[int64]*tg.Channel{}}
	n.Add(users, chats)
	return n
}

func (n Names) Add(users []tg.UserClass, chats []tg.ChatClass) {
	for _, u := range users {
		if v, ok := u.(*tg.User); ok {
			n.Users[v.ID] = v
		}
	}
	for _, c := range chats {
		switch v := c.(type) {
		case *tg.Chat:
			n.Chats[v.ID] = v
		case *tg.Channel:
			n.Channels[v.ID] = v
		}
	}
}

// BotAPIChatID returns the Bot API form (-100…) of an MTProto channel id.
func BotAPIChatID(channelID int64) int64 { return -1000000000000 - channelID }

func MessageLink(ch Channel, msgID int) string {
	if ch.Username != "" {
		return fmt.Sprintf("https://t.me/%s/%d", ch.Username, msgID)
	}
	return fmt.Sprintf("https://t.me/c/%d/%d", ch.ID, msgID)
}

// MediaRef is stored as JSON in media.source_ref for "mt:" media. It carries both the file
// location and the message it came from, so an expired file reference can be refreshed.
type MediaRef struct {
	ChannelID   int64  `json:"channel_id"`
	ChannelHash int64  `json:"channel_hash"`
	MsgID       int    `json:"msg_id"`
	Photo       bool   `json:"photo"`
	ID          int64  `json:"id"`
	FileHash    int64  `json:"file_hash"`
	FileRef     []byte `json:"file_ref"`
	ThumbSize   string `json:"thumb_size,omitempty"`
}

func (r MediaRef) Location() tg.InputFileLocationClass {
	if r.Photo {
		return &tg.InputPhotoFileLocation{ID: r.ID, AccessHash: r.FileHash, FileReference: r.FileRef, ThumbSize: r.ThumbSize}
	}
	return &tg.InputDocumentFileLocation{ID: r.ID, AccessHash: r.FileHash, FileReference: r.FileRef, ThumbSize: r.ThumbSize}
}

func Convert(m *tg.Message, ch Channel, names Names) (*model.Message, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	msg := &model.Message{
		TgMessageID:     int64(m.ID),
		Source:          model.SourceUserbotFetch,
		Date:            int64(m.Date),
		Text:            m.Message,
		Entities:        convertEntities(m.Entities),
		OriginChatID:    BotAPIChatID(ch.ID),
		OriginChatTitle: ch.Title,
		OriginLink:      MessageLink(ch, m.ID),
		RawFormat:       model.RawMTProto,
		Raw:             raw,
	}
	if d, ok := m.GetEditDate(); ok {
		msg.EditDate = int64(d)
	}
	if g, ok := m.GetGroupedID(); ok {
		msg.MediaGroupID = strconv.FormatInt(g, 10)
	}
	if h, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
		msg.ReplyToTgMessageID = int64(h.ReplyToMsgID)
	}
	if f, ok := m.GetFwdFrom(); ok {
		msg.ForwardOrigin = convertFwd(f, names)
	}
	extra := map[string]any{}
	if u, ok := m.FromID.(*tg.PeerUser); ok {
		if v := names.Users[u.UserID]; v != nil {
			setIf(extra, "author", strings.TrimSpace(v.FirstName+" "+v.LastName))
		}
	}
	if a, ok := m.GetPostAuthor(); ok {
		setIf(extra, "post_author", a)
	}
	if err := fill(msg, m, ch, extra); err != nil {
		return nil, err
	}
	if len(extra) > 0 {
		b, err := json.Marshal(extra)
		if err != nil {
			return nil, err
		}
		msg.Extra = b
	}
	return msg, nil
}

func fill(msg *model.Message, m *tg.Message, ch Channel, extra map[string]any) error {
	base := MediaRef{ChannelID: ch.ID, ChannelHash: ch.AccessHash, MsgID: m.ID}
	switch md := m.Media.(type) {
	case *tg.MessageMediaPhoto:
		p, ok := md.Photo.(*tg.Photo)
		if !ok {
			msg.Kind = model.KindOther
			return nil
		}
		big, small, has := pickSizes(p.Sizes)
		if !has {
			msg.Kind = model.KindOther
			return nil
		}
		if md.Spoiler {
			extra["spoiler"] = true
		}
		msg.Kind = model.KindPhoto
		r := base
		r.Photo, r.ID, r.FileHash, r.FileRef, r.ThumbSize = true, p.ID, p.AccessHash, p.FileReference, big.Type
		main, err := media(r, fmt.Sprintf("mt:photo:%d", p.ID), "photo", "image/jpeg", big, model.RoleMain)
		if err != nil {
			return err
		}
		msg.Media = append(msg.Media, main)
		if small.Type != big.Type {
			r.ThumbSize = small.Type
			th, err := media(r, fmt.Sprintf("mt:photo:%d:%s", p.ID, small.Type), "photo", "image/jpeg", small, model.RoleThumb)
			if err != nil {
				return err
			}
			msg.Media = append(msg.Media, th)
		}
	case *tg.MessageMediaDocument:
		d, ok := md.Document.(*tg.Document)
		if !ok {
			msg.Kind = model.KindOther
			return nil
		}
		if md.Spoiler {
			extra["spoiler"] = true
		}
		return document(msg, d, base, extra)
	case *tg.MessageMediaGeo:
		return geo(msg, md.Geo, extra)
	case *tg.MessageMediaGeoLive:
		return geo(msg, md.Geo, extra)
	case *tg.MessageMediaVenue:
		if err := geo(msg, md.Geo, extra); err != nil {
			return err
		}
		msg.Kind = model.KindVenue
		setIf(extra, "title", md.Title)
		setIf(extra, "address", md.Address)
	case *tg.MessageMediaContact:
		msg.Kind = model.KindContact
		setIf(extra, "phone_number", md.PhoneNumber)
		setIf(extra, "first_name", md.FirstName)
		setIf(extra, "last_name", md.LastName)
		if md.UserID != 0 {
			extra["user_id"] = md.UserID
		}
	case *tg.MessageMediaPoll:
		p := md.Poll
		msg.Kind = model.KindPoll
		opts := make([]map[string]any, 0, len(p.Answers))
		for _, a := range p.Answers {
			pa, ok := a.(*tg.PollAnswer)
			if !ok {
				continue
			}
			voters := 0
			for _, r := range md.Results.Results {
				if bytes.Equal(r.Option, pa.Option) {
					voters = r.Voters
				}
			}
			opts = append(opts, map[string]any{"text": pa.Text.Text, "voter_count": voters})
		}
		typ := "regular"
		if p.Quiz {
			typ = "quiz"
		}
		extra["question"], extra["options"], extra["total_voter_count"] = p.Question.Text, opts, md.Results.TotalVoters
		extra["is_anonymous"], extra["poll_type"], extra["multiple"] = !p.PublicVoters, typ, p.MultipleChoice
	case *tg.MessageMediaDice:
		msg.Kind = model.KindDice
		extra["emoji"], extra["value"] = md.Emoticon, md.Value
	case nil, *tg.MessageMediaEmpty, *tg.MessageMediaWebPage:
		if msg.Text != "" {
			msg.Kind = model.KindText
		} else {
			msg.Kind = model.KindOther
		}
	default:
		msg.Kind = model.KindOther
	}
	return nil
}

func document(msg *model.Message, d *tg.Document, base MediaRef, extra map[string]any) error {
	var (
		video    *tg.DocumentAttributeVideo
		audio    *tg.DocumentAttributeAudio
		sticker  *tg.DocumentAttributeSticker
		animated bool
		name     string
		sz       size
	)
	for _, a := range d.Attributes {
		switch v := a.(type) {
		case *tg.DocumentAttributeVideo:
			video = v
		case *tg.DocumentAttributeAudio:
			audio = v
		case *tg.DocumentAttributeSticker:
			sticker = v
		case *tg.DocumentAttributeAnimated:
			animated = true
		case *tg.DocumentAttributeFilename:
			name = v.FileName
		case *tg.DocumentAttributeImageSize:
			sz.W, sz.H = v.W, v.H
		}
	}
	kind, mkind, mime, dur := model.KindDocument, "document", d.MimeType, 0
	if video != nil {
		sz.W, sz.H, dur = video.W, video.H, int(math.Round(video.Duration))
	}
	switch {
	case sticker != nil:
		kind, mkind = model.KindSticker, "sticker"
		setIf(extra, "emoji", sticker.Alt)
	case animated:
		kind, mkind, mime = model.KindAnimation, "animation", or(mime, "video/mp4")
	case video != nil && video.RoundMessage:
		kind, mkind, mime = model.KindVideoNote, "video_note", or(mime, "video/mp4")
	case video != nil:
		kind, mkind, mime = model.KindVideo, "video", or(mime, "video/mp4")
	case audio != nil && audio.Voice:
		kind, mkind, mime, dur = model.KindVoice, "voice", or(mime, "audio/ogg"), audio.Duration
	case audio != nil:
		kind, mkind, mime, dur = model.KindAudio, "audio", or(mime, "audio/mpeg"), audio.Duration
		setIf(extra, "performer", audio.Performer)
		setIf(extra, "title", audio.Title)
	}
	msg.Kind = kind
	r := base
	r.ID, r.FileHash, r.FileRef = d.ID, d.AccessHash, d.FileReference
	sz.Bytes = d.Size
	main, err := media(r, fmt.Sprintf("mt:doc:%d", d.ID), mkind, or(mime, "application/octet-stream"), sz, model.RoleMain)
	if err != nil {
		return err
	}
	main.FileName, main.Duration = name, dur
	if kind == model.KindVoice {
		main.Waveform = audio.Waveform
	}
	msg.Media = append(msg.Media, main)
	if kind == model.KindSticker || kind == model.KindVoice {
		return nil
	}
	if big, _, ok := pickSizes(d.Thumbs); ok {
		r.ThumbSize = big.Type
		th, err := media(r, fmt.Sprintf("mt:doc:%d:thumb", d.ID), "photo", "image/jpeg", big, model.RoleThumb)
		if err != nil {
			return err
		}
		msg.Media = append(msg.Media, th)
	}
	return nil
}

func geo(msg *model.Message, g tg.GeoPointClass, extra map[string]any) error {
	p, ok := g.(*tg.GeoPoint)
	if !ok {
		msg.Kind = model.KindOther
		return nil
	}
	msg.Kind = model.KindLocation
	extra["latitude"], extra["longitude"] = p.Lat, p.Long
	return nil
}

type size struct {
	Type  string
	W, H  int
	Bytes int64
}

// pickSizes returns the largest and smallest downloadable photo sizes.
func pickSizes(in []tg.PhotoSizeClass) (big, small size, ok bool) {
	for _, s := range in {
		var c size
		switch v := s.(type) {
		case *tg.PhotoSize:
			c = size{Type: v.Type, W: v.W, H: v.H, Bytes: int64(v.Size)}
		case *tg.PhotoSizeProgressive:
			c = size{Type: v.Type, W: v.W, H: v.H}
			for _, b := range v.Sizes {
				c.Bytes = max(c.Bytes, int64(b))
			}
		default:
			continue
		}
		if !ok || c.W*c.H > big.W*big.H {
			big = c
		}
		if !ok || c.W*c.H < small.W*small.H {
			small = c
		}
		ok = true
	}
	return big, small, ok
}

func media(r MediaRef, key, kind, mime string, sz size, role string) (model.Media, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return model.Media{}, err
	}
	return model.Media{DedupeKey: key, SourceRef: string(b), Kind: kind, Mime: mime, Size: sz.Bytes, Width: sz.W, Height: sz.H, Role: role}, nil
}

func convertEntities(in []tg.MessageEntityClass) []model.Entity {
	out := make([]model.Entity, 0, len(in))
	for _, e := range in {
		me := model.Entity{Offset: e.GetOffset(), Length: e.GetLength()}
		switch v := e.(type) {
		case *tg.MessageEntityMention:
			me.Type = "mention"
		case *tg.MessageEntityHashtag:
			me.Type = "hashtag"
		case *tg.MessageEntityCashtag:
			me.Type = "cashtag"
		case *tg.MessageEntityBotCommand:
			me.Type = "bot_command"
		case *tg.MessageEntityURL:
			me.Type = "url"
		case *tg.MessageEntityEmail:
			me.Type = "email"
		case *tg.MessageEntityPhone:
			me.Type = "phone_number"
		case *tg.MessageEntityBold:
			me.Type = "bold"
		case *tg.MessageEntityItalic:
			me.Type = "italic"
		case *tg.MessageEntityUnderline:
			me.Type = "underline"
		case *tg.MessageEntityStrike:
			me.Type = "strikethrough"
		case *tg.MessageEntitySpoiler:
			me.Type = "spoiler"
		case *tg.MessageEntityCode:
			me.Type = "code"
		case *tg.MessageEntityPre:
			me.Type, me.Language = "pre", v.Language
		case *tg.MessageEntityTextURL:
			me.Type, me.URL = "text_link", v.URL
		case *tg.MessageEntityMentionName:
			me.Type, me.UserID = "text_mention", v.UserID
		case *tg.MessageEntityCustomEmoji:
			me.Type, me.CustomEmojiID = "custom_emoji", strconv.FormatInt(v.DocumentID, 10)
		case *tg.MessageEntityBlockquote:
			me.Type = "blockquote"
			if v.Collapsed {
				me.Type = "expandable_blockquote"
			}
		default:
			continue
		}
		out = append(out, me)
	}
	return out
}

func convertFwd(f tg.MessageFwdHeader, names Names) *model.ForwardOrigin {
	o := &model.ForwardOrigin{Date: int64(f.Date), Signature: f.PostAuthor}
	switch p := f.FromID.(type) {
	case *tg.PeerUser:
		o.Type, o.UserID = "user", p.UserID
		if u := names.Users[p.UserID]; u != nil {
			o.Name, o.Username = strings.TrimSpace(u.FirstName+" "+u.LastName), u.Username
		}
	case *tg.PeerChannel:
		o.Type, o.ChatID, o.MessageID = "channel", BotAPIChatID(p.ChannelID), int64(f.ChannelPost)
		if f.ChannelPost == 0 {
			o.Type = "chat" // anonymous supergroup admin
		}
		if c := names.Channels[p.ChannelID]; c != nil {
			o.Name, o.Username = c.Title, c.Username
		}
	case *tg.PeerChat:
		o.Type, o.ChatID = "chat", -p.ChatID
		if c := names.Chats[p.ChatID]; c != nil {
			o.Name = c.Title
		}
	default:
		o.Type = "hidden_user"
	}
	if o.Name == "" {
		o.Name = f.FromName
	}
	return o
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
