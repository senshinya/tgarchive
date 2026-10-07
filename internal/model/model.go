// Package model is the source-independent message shape stored by tgarchive.
// Bot API updates and MTProto messages are both converted into it.
package model

import "encoding/json"

type Kind string

const (
	KindText      Kind = "text"
	KindPhoto     Kind = "photo"
	KindVideo     Kind = "video"
	KindAnimation Kind = "animation"
	KindVoice     Kind = "voice"
	KindAudio     Kind = "audio"
	KindDocument  Kind = "document"
	KindSticker   Kind = "sticker"
	KindVideoNote Kind = "video_note"
	KindLocation  Kind = "location"
	KindVenue     Kind = "venue"
	KindContact   Kind = "contact"
	KindPoll      Kind = "poll"
	KindDice      Kind = "dice"
	KindOther     Kind = "other"
)

const (
	SourceBotUpdate    = "bot_update"
	SourceUserbotFetch = "userbot_fetch"
	SourceChannelWatch = "channel_watch"

	RawBotAPI  = "botapi"
	RawMTProto = "mtproto"

	RoleMain  = "main"
	RoleThumb = "thumb"
	// RoleLinkPreview is the photo of a message's link preview card.
	RoleLinkPreview = "link_preview"
)

type Sender struct {
	TgUserID  int64
	FirstName string
	LastName  string
	Username  string
}

// Entity offsets and lengths are UTF-16 code units, as Telegram sends them.
type Entity struct {
	Type          string `json:"type"`
	Offset        int    `json:"offset"`
	Length        int    `json:"length"`
	URL           string `json:"url,omitempty"`
	Language      string `json:"language,omitempty"`
	UserID        int64  `json:"user_id,omitempty"`
	CustomEmojiID string `json:"custom_emoji_id,omitempty"`
}

type ForwardOrigin struct {
	Type      string `json:"type"` // user / hidden_user / chat / channel
	Name      string `json:"name"`
	Username  string `json:"username,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	ChatID    int64  `json:"chat_id,omitempty"`
	MessageID int64  `json:"message_id,omitempty"`
	Date      int64  `json:"date"`
	Signature string `json:"signature,omitempty"`
}

type Media struct {
	DedupeKey string // "bot:<file_unique_id>" or "mt:<id>"
	SourceRef string // Bot API file_id, or MTProto location blob
	Kind      string
	Mime      string
	FileName  string
	Size      int64
	Width     int
	Height    int
	Duration  int
	Waveform  []byte
	Role      string
}

type Message struct {
	TgMessageID        int64
	Source             string
	MediaGroupID       string
	Date               int64
	EditDate           int64
	Kind               Kind
	Text               string
	Entities           []Entity
	ForwardOrigin      *ForwardOrigin
	ReplyToTgMessageID int64
	OriginChatID       int64
	OriginChatTitle    string
	OriginLink         string
	Extra              json.RawMessage
	RawFormat          string
	Raw                json.RawMessage
	Media              []Media
}
