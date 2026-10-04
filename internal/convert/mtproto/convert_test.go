package mtproto

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/gotd/td/tg"

	"tgarchive/internal/model"
)

var pub = Channel{ID: 500, AccessHash: 5005, Title: "Chan", Username: "chan"}

func ref(t *testing.T, m model.Media) MediaRef {
	t.Helper()
	var r MediaRef
	if err := json.Unmarshal([]byte(m.SourceRef), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestConvertTextAndEntities(t *testing.T) {
	m := &tg.Message{ID: 42, Date: 1700000000, Message: "hello world quote",
		ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 41},
		Entities: []tg.MessageEntityClass{
			&tg.MessageEntityBold{Offset: 0, Length: 5},
			&tg.MessageEntityTextURL{Offset: 6, Length: 5, URL: "https://x.y"},
			&tg.MessageEntityMentionName{Offset: 0, Length: 5, UserID: 9},
			&tg.MessageEntityCustomEmoji{Offset: 12, Length: 2, DocumentID: 123},
			&tg.MessageEntityBlockquote{Offset: 12, Length: 5, Collapsed: true},
			&tg.MessageEntityPre{Offset: 0, Length: 5, Language: "go"},
			&tg.MessageEntityBankCard{Offset: 0, Length: 1},
		},
	}
	m.SetEditDate(1700000100)
	m.SetGroupedID(777)
	got, err := Convert(m, pub, NamesFrom(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != model.SourceUserbotFetch || got.RawFormat != model.RawMTProto || len(got.Raw) == 0 ||
		got.TgMessageID != 42 || got.Date != 1700000000 || got.EditDate != 1700000100 || got.MediaGroupID != "777" ||
		got.ReplyToTgMessageID != 41 || got.Kind != model.KindText || got.Text != "hello world quote" ||
		got.OriginChatID != -1000000000500 || got.OriginChatTitle != "Chan" || got.OriginLink != "https://t.me/chan/42" {
		t.Fatalf("message = %+v", got)
	}
	want := []model.Entity{
		{Type: "bold", Offset: 0, Length: 5},
		{Type: "text_link", Offset: 6, Length: 5, URL: "https://x.y"},
		{Type: "text_mention", Offset: 0, Length: 5, UserID: 9},
		{Type: "custom_emoji", Offset: 12, Length: 2, CustomEmojiID: "123"},
		{Type: "expandable_blockquote", Offset: 12, Length: 5},
		{Type: "pre", Offset: 0, Length: 5, Language: "go"},
	}
	if !reflect.DeepEqual(got.Entities, want) {
		t.Fatalf("entities = %+v", got.Entities)
	}
	priv := Channel{ID: 500, AccessHash: 1, Title: "P"}
	if got, _ := Convert(&tg.Message{ID: 7, Message: "x"}, priv, Names{}); got.OriginLink != "https://t.me/c/500/7" {
		t.Fatalf("private link = %q", got.OriginLink)
	}
}

func TestConvertPhoto(t *testing.T) {
	m := &tg.Message{ID: 42, Media: &tg.MessageMediaPhoto{Spoiler: true, Photo: &tg.Photo{ID: 9, AccessHash: 99, FileReference: []byte{1, 2},
		Sizes: []tg.PhotoSizeClass{
			&tg.PhotoStrippedSize{Type: "i", Bytes: []byte{1}},
			&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 100},
			&tg.PhotoSizeProgressive{Type: "y", W: 1280, H: 960, Sizes: []int{10, 200, 900}},
		}}}}
	got, err := Convert(m, pub, Names{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != model.KindPhoto || len(got.Media) != 2 || string(got.Extra) != `{"spoiler":true}` {
		t.Fatalf("photo = %+v extra=%s", got, got.Extra)
	}
	main, thumb := got.Media[0], got.Media[1]
	if main.DedupeKey != "mt:photo:9" || main.Kind != "photo" || main.Mime != "image/jpeg" || main.Width != 1280 || main.Height != 960 ||
		main.Size != 900 || main.Role != model.RoleMain {
		t.Fatalf("main = %+v", main)
	}
	if thumb.DedupeKey != "mt:photo:9:m" || thumb.Width != 320 || thumb.Role != model.RoleThumb {
		t.Fatalf("thumb = %+v", thumb)
	}
	r := ref(t, main)
	if !reflect.DeepEqual(r, MediaRef{ChannelID: 500, ChannelHash: 5005, MsgID: 42, Photo: true, ID: 9, FileHash: 99, FileRef: r.FileRef, ThumbSize: "y"}) ||
		!reflect.DeepEqual(r.FileRef, []byte{1, 2}) {
		t.Fatalf("ref = %+v", r)
	}
	loc, ok := r.Location().(*tg.InputPhotoFileLocation)
	if !ok || loc.ID != 9 || loc.AccessHash != 99 || loc.ThumbSize != "y" || !reflect.DeepEqual(loc.FileReference, []byte{1, 2}) {
		t.Fatalf("location = %#v", r.Location())
	}
	if ref(t, thumb).ThumbSize != "m" {
		t.Fatalf("thumb ref = %+v", ref(t, thumb))
	}
}

func doc(id int64, mime string, attrs ...tg.DocumentAttributeClass) *tg.Message {
	return &tg.Message{ID: 42, Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: id, AccessHash: 11, FileReference: []byte{3},
		MimeType: mime, Size: 4096, Attributes: attrs,
		Thumbs: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "m", W: 90, H: 60, Size: 50}}}}}
}

func TestConvertDocuments(t *testing.T) {
	cases := []struct {
		name  string
		msg   *tg.Message
		kind  model.Kind
		mkind string
		check func(t *testing.T, got *model.Message)
	}{
		{"video", doc(5, "video/mp4", &tg.DocumentAttributeVideo{W: 1920, H: 1080, Duration: 12.6}, &tg.DocumentAttributeFilename{FileName: "a.mp4"}),
			model.KindVideo, "video", func(t *testing.T, got *model.Message) {
				m := got.Media[0]
				if m.Width != 1920 || m.Height != 1080 || m.Duration != 13 || m.FileName != "a.mp4" || m.Size != 4096 || m.DedupeKey != "mt:doc:5" {
					t.Fatalf("video media = %+v", m)
				}
				if len(got.Media) != 2 || got.Media[1].DedupeKey != "mt:doc:5:thumb" || ref(t, got.Media[1]).ThumbSize != "m" {
					t.Fatalf("video thumb = %+v", got.Media)
				}
				loc, ok := ref(t, m).Location().(*tg.InputDocumentFileLocation)
				if !ok || loc.ID != 5 || loc.AccessHash != 11 || loc.ThumbSize != "" {
					t.Fatalf("doc location = %#v", ref(t, m).Location())
				}
			}},
		{"round", doc(6, "video/mp4", &tg.DocumentAttributeVideo{RoundMessage: true, W: 240, H: 240, Duration: 3}), model.KindVideoNote, "video_note", nil},
		{"gif", doc(7, "video/mp4", &tg.DocumentAttributeAnimated{}, &tg.DocumentAttributeVideo{W: 320, H: 200, Duration: 2}), model.KindAnimation, "animation", nil},
		{"voice", doc(8, "audio/ogg", &tg.DocumentAttributeAudio{Voice: true, Duration: 4, Waveform: []byte{1, 2, 3}}), model.KindVoice, "voice",
			func(t *testing.T, got *model.Message) {
				if len(got.Media) != 1 || got.Media[0].Duration != 4 || !reflect.DeepEqual(got.Media[0].Waveform, []byte{1, 2, 3}) {
					t.Fatalf("voice = %+v", got.Media)
				}
			}},
		{"audio", doc(9, "audio/mpeg", &tg.DocumentAttributeAudio{Duration: 200, Title: "Song", Performer: "Band"}), model.KindAudio, "audio",
			func(t *testing.T, got *model.Message) {
				if string(got.Extra) != `{"performer":"Band","title":"Song"}` {
					t.Fatalf("audio extra = %s", got.Extra)
				}
			}},
		{"sticker", doc(10, "image/webp", &tg.DocumentAttributeSticker{Alt: "😀", Stickerset: &tg.InputStickerSetEmpty{}}), model.KindSticker, "sticker",
			func(t *testing.T, got *model.Message) {
				if len(got.Media) != 1 || string(got.Extra) != `{"emoji":"😀"}` {
					t.Fatalf("sticker = %+v %s", got.Media, got.Extra)
				}
			}},
		{"file", doc(11, "application/pdf", &tg.DocumentAttributeFilename{FileName: "x.pdf"}), model.KindDocument, "document",
			func(t *testing.T, got *model.Message) {
				if got.Media[0].FileName != "x.pdf" || got.Media[0].Mime != "application/pdf" {
					t.Fatalf("file = %+v", got.Media[0])
				}
			}},
		{"nomime", doc(12, ""), model.KindDocument, "document",
			func(t *testing.T, got *model.Message) {
				if got.Media[0].Mime != "application/octet-stream" {
					t.Fatalf("mime = %q", got.Media[0].Mime)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Convert(c.msg, pub, Names{})
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != c.kind || got.Media[0].Kind != c.mkind || got.Media[0].Role != model.RoleMain {
				t.Fatalf("kind = %s / %s", got.Kind, got.Media[0].Kind)
			}
			if c.check != nil {
				c.check(t, got)
			}
		})
	}
}

func TestConvertForwardAndAuthor(t *testing.T) {
	names := NamesFrom(
		[]tg.UserClass{&tg.User{ID: 9, FirstName: "Ann", LastName: "Lee", Username: "ann"}},
		[]tg.ChatClass{&tg.Channel{ID: 600, Title: "Source", Username: "src"}})
	m := &tg.Message{ID: 1, Message: "x", FromID: &tg.PeerUser{UserID: 9}}
	m.SetFwdFrom(tg.MessageFwdHeader{FromID: &tg.PeerChannel{ChannelID: 600}, ChannelPost: 7, Date: 100, PostAuthor: "ed"})
	got, _ := Convert(m, pub, names)
	want := &model.ForwardOrigin{Type: "channel", Name: "Source", Username: "src", ChatID: -1000000000600, MessageID: 7, Date: 100, Signature: "ed"}
	if !reflect.DeepEqual(got.ForwardOrigin, want) || string(got.Extra) != `{"author":"Ann Lee"}` {
		t.Fatalf("fwd = %+v extra=%s", got.ForwardOrigin, got.Extra)
	}
	h := &tg.Message{ID: 2, Message: "x"}
	h.SetFwdFrom(tg.MessageFwdHeader{FromName: "Hidden", Date: 5})
	got, _ = Convert(h, pub, names)
	if got.ForwardOrigin == nil || got.ForwardOrigin.Type != "hidden_user" || got.ForwardOrigin.Name != "Hidden" {
		t.Fatalf("hidden = %+v", got.ForwardOrigin)
	}
	u := &tg.Message{ID: 3, Message: "x"}
	u.SetFwdFrom(tg.MessageFwdHeader{FromID: &tg.PeerUser{UserID: 9}, Date: 5})
	got, _ = Convert(u, pub, names)
	if f := got.ForwardOrigin; f.Type != "user" || f.Name != "Ann Lee" || f.Username != "ann" || f.UserID != 9 {
		t.Fatalf("user fwd = %+v", f)
	}
}

func TestConvertMisc(t *testing.T) {
	cases := []struct {
		media tg.MessageMediaClass
		text  string
		kind  model.Kind
		extra string
	}{
		{&tg.MessageMediaGeo{Geo: &tg.GeoPoint{Lat: 1.5, Long: 2.5}}, "", model.KindLocation, `{"latitude":1.5,"longitude":2.5}`},
		{&tg.MessageMediaVenue{Geo: &tg.GeoPoint{Lat: 1, Long: 2}, Title: "Cafe", Address: "St"}, "", model.KindVenue,
			`{"address":"St","latitude":1,"longitude":2,"title":"Cafe"}`},
		{&tg.MessageMediaContact{PhoneNumber: "+1", FirstName: "A", UserID: 3}, "", model.KindContact, `{"first_name":"A","phone_number":"+1","user_id":3}`},
		{&tg.MessageMediaDice{Value: 4, Emoticon: "🎲"}, "", model.KindDice, `{"emoji":"🎲","value":4}`},
		{&tg.MessageMediaWebPage{Webpage: &tg.WebPageEmpty{}}, "see link", model.KindText, ``},
		{&tg.MessageMediaUnsupported{}, "", model.KindOther, ``},
		{nil, "", model.KindOther, ``},
		{&tg.MessageMediaPoll{
			Poll: tg.Poll{Question: tg.TextWithEntities{Text: "Q?"}, MultipleChoice: true, Answers: []tg.PollAnswerClass{
				&tg.PollAnswer{Text: tg.TextWithEntities{Text: "a"}, Option: []byte{0}},
				&tg.PollAnswer{Text: tg.TextWithEntities{Text: "b"}, Option: []byte{1}},
			}},
			Results: tg.PollResults{TotalVoters: 3, Results: []tg.PollAnswerVoters{{Option: []byte{1}, Voters: 3}}},
		}, "", model.KindPoll,
			`{"is_anonymous":true,"multiple":true,"options":[{"text":"a","voter_count":0},{"text":"b","voter_count":3}],"poll_type":"regular","question":"Q?","total_voter_count":3}`},
	}
	for i, c := range cases {
		got, err := Convert(&tg.Message{ID: 1, Message: c.text, Media: c.media}, pub, Names{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != c.kind || string(got.Extra) != c.extra || len(got.Media) != 0 {
			t.Fatalf("case %d: kind=%s extra=%s media=%v", i, got.Kind, got.Extra, got.Media)
		}
	}
}
