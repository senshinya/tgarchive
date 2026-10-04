package botapi

import (
	"encoding/json"
	"errors"
	"testing"

	"tgarchive/internal/model"
)

const from = `"from":{"id":42,"is_bot":false,"first_name":"Shinya","last_name":"K","username":"shinya"},"chat":{"id":42,"type":"private"},"date":1759500000`

func conv(t *testing.T, body string) *Result {
	t.Helper()
	r, err := Convert(json.RawMessage(`{"message_id":10,` + from + `,` + body + `}`))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func extra(t *testing.T, m *model.Message) map[string]any {
	t.Helper()
	out := map[string]any{}
	if len(m.Extra) > 0 {
		if err := json.Unmarshal(m.Extra, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestTextWithEntities(t *testing.T) {
	r := conv(t, `"text":"hello bold link","entities":[{"type":"bold","offset":6,"length":4},{"type":"text_link","offset":11,"length":4,"url":"https://example.com"}]`)
	m := r.Msg
	if r.ChatID != 42 || r.ChatType != "private" || r.Sender.Username != "shinya" || r.Sender.LastName != "K" {
		t.Fatalf("result = %+v", r)
	}
	if m.Kind != model.KindText || m.Text != "hello bold link" || len(m.Entities) != 2 || m.Entities[1].URL != "https://example.com" {
		t.Fatalf("msg = %+v", m)
	}
	if m.Source != model.SourceBotUpdate || m.RawFormat != model.RawBotAPI || m.TgMessageID != 10 || m.Date != 1759500000 || len(m.Raw) == 0 {
		t.Fatalf("meta = %+v", m)
	}
	if len(m.Media) != 0 || len(m.Extra) != 0 {
		t.Fatal("text message must have no media and no extra")
	}
}

func TestPhotoAlbumWithCaptionAndSpoiler(t *testing.T) {
	r := conv(t, `"media_group_id":"g1","has_media_spoiler":true,"caption":"cap","caption_entities":[{"type":"italic","offset":0,"length":3}],`+
		`"photo":[{"file_id":"s","file_unique_id":"us","width":90,"height":60},{"file_id":"m","file_unique_id":"um","width":320,"height":213},{"file_id":"b","file_unique_id":"ub","width":1280,"height":853,"file_size":1000}]`)
	m := r.Msg
	if m.Kind != model.KindPhoto || m.MediaGroupID != "g1" || m.Text != "cap" || m.Entities[0].Type != "italic" {
		t.Fatalf("msg = %+v", m)
	}
	if len(m.Media) != 2 {
		t.Fatalf("media = %+v", m.Media)
	}
	main, thumb := m.Media[0], m.Media[1]
	if main.Role != model.RoleMain || main.DedupeKey != "bot:ub" || main.SourceRef != "b" || main.Width != 1280 || main.Size != 1000 || main.Mime != "image/jpeg" {
		t.Fatalf("main = %+v", main)
	}
	if thumb.Role != model.RoleThumb || thumb.DedupeKey != "bot:us" {
		t.Fatalf("thumb = %+v", thumb)
	}
	if extra(t, m)["spoiler"] != true {
		t.Fatal("spoiler flag missing")
	}
}

func TestAnimationBeatsDocument(t *testing.T) {
	r := conv(t, `"animation":{"file_id":"a","file_unique_id":"ua","width":320,"height":240,"duration":3,"mime_type":"video/mp4","thumbnail":{"file_id":"t","file_unique_id":"ut","width":90,"height":67}},`+
		`"document":{"file_id":"a","file_unique_id":"ua","mime_type":"video/mp4"}`)
	if r.Msg.Kind != model.KindAnimation || len(r.Msg.Media) != 2 || r.Msg.Media[0].Duration != 3 || r.Msg.Media[1].Role != model.RoleThumb {
		t.Fatalf("msg = %+v", r.Msg)
	}
}

func TestMediaKinds(t *testing.T) {
	cases := []struct {
		name, body string
		kind       model.Kind
		mime       string
		media      int
	}{
		{"video", `"video":{"file_id":"v","file_unique_id":"uv","width":1920,"height":1080,"duration":60,"mime_type":"video/mp4","file_size":30000000,"file_name":"a.mp4"}`, model.KindVideo, "video/mp4", 1},
		{"voice", `"voice":{"file_id":"vo","file_unique_id":"uvo","duration":4}`, model.KindVoice, "audio/ogg", 1},
		{"video_note", `"video_note":{"file_id":"n","file_unique_id":"un","length":384,"duration":5,"thumbnail":{"file_id":"nt","file_unique_id":"unt","width":90,"height":90}}`, model.KindVideoNote, "video/mp4", 2},
		{"document", `"document":{"file_id":"d","file_unique_id":"ud","file_name":"report.pdf","mime_type":"application/pdf","file_size":123}`, model.KindDocument, "application/pdf", 1},
		{"tgs sticker", `"sticker":{"file_id":"st","file_unique_id":"ust","type":"regular","width":512,"height":512,"is_animated":true,"is_video":false,"emoji":"😀","set_name":"Pack"}`, model.KindSticker, "application/x-tgsticker", 1},
		{"webm sticker", `"sticker":{"file_id":"st","file_unique_id":"ust","type":"regular","width":512,"height":512,"is_animated":false,"is_video":true}`, model.KindSticker, "video/webm", 1},
		{"static sticker", `"sticker":{"file_id":"st","file_unique_id":"ust","type":"regular","width":512,"height":512,"is_animated":false,"is_video":false}`, model.KindSticker, "image/webp", 1},
		{"audio", `"audio":{"file_id":"au","file_unique_id":"uau","duration":200,"performer":"P","title":"T","mime_type":"audio/mpeg"}`, model.KindAudio, "audio/mpeg", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := conv(t, c.body)
			if r.Msg.Kind != c.kind || len(r.Msg.Media) != c.media || r.Msg.Media[0].Mime != c.mime || r.Msg.Media[0].Role != model.RoleMain {
				t.Fatalf("msg = %+v", r.Msg)
			}
		})
	}
}

func TestStructuredKinds(t *testing.T) {
	r := conv(t, `"venue":{"location":{"latitude":35.6,"longitude":139.7},"title":"Tower","address":"Tokyo"},"location":{"latitude":35.6,"longitude":139.7}`)
	if r.Msg.Kind != model.KindVenue || extra(t, r.Msg)["title"] != "Tower" || extra(t, r.Msg)["latitude"] != 35.6 {
		t.Fatalf("venue = %+v", r.Msg)
	}
	r = conv(t, `"location":{"latitude":1.5,"longitude":2.5}`)
	if r.Msg.Kind != model.KindLocation || extra(t, r.Msg)["longitude"] != 2.5 {
		t.Fatalf("location = %+v", r.Msg)
	}
	r = conv(t, `"contact":{"phone_number":"+100","first_name":"Bob","user_id":5}`)
	if r.Msg.Kind != model.KindContact || extra(t, r.Msg)["phone_number"] != "+100" {
		t.Fatalf("contact = %+v", r.Msg)
	}
	r = conv(t, `"poll":{"id":"p","question":"Q?","options":[{"text":"a","voter_count":1},{"text":"b","voter_count":2}],"total_voter_count":3,"is_anonymous":true,"type":"regular","allows_multiple_answers":false}`)
	if r.Msg.Kind != model.KindPoll || extra(t, r.Msg)["question"] != "Q?" || len(extra(t, r.Msg)["options"].([]any)) != 2 {
		t.Fatalf("poll = %+v", r.Msg)
	}
	r = conv(t, `"dice":{"emoji":"🎲","value":6}`)
	if r.Msg.Kind != model.KindDice || extra(t, r.Msg)["value"] != float64(6) {
		t.Fatalf("dice = %+v", r.Msg)
	}
	r = conv(t, `"story":{"chat":{"id":1,"type":"channel"},"id":5}`)
	if r.Msg.Kind != model.KindOther {
		t.Fatalf("unknown kind = %s", r.Msg.Kind)
	}
}

func TestForwardAndReply(t *testing.T) {
	r := conv(t, `"text":"x","reply_to_message":{"message_id":7},"forward_origin":{"type":"channel","date":1759000000,"chat":{"id":-1001,"type":"channel","title":"News","username":"news"},"message_id":99,"author_signature":"ed"}`)
	o := r.Msg.ForwardOrigin
	if r.Msg.ReplyToTgMessageID != 7 || o == nil || o.Type != "channel" || o.Name != "News" || o.Username != "news" || o.ChatID != -1001 || o.MessageID != 99 || o.Signature != "ed" {
		t.Fatalf("msg = %+v origin = %+v", r.Msg, o)
	}
	r = conv(t, `"text":"x","forward_origin":{"type":"user","date":1,"sender_user":{"id":9,"is_bot":false,"first_name":"Ann","last_name":"Lee"}}`)
	if o := r.Msg.ForwardOrigin; o.Name != "Ann Lee" || o.UserID != 9 {
		t.Fatalf("user origin = %+v", o)
	}
	r = conv(t, `"text":"x","forward_origin":{"type":"hidden_user","date":1,"sender_user_name":"Ghost"}`)
	if o := r.Msg.ForwardOrigin; o.Name != "Ghost" {
		t.Fatalf("hidden origin = %+v", o)
	}
}

func TestGroupChatAndNoSender(t *testing.T) {
	r, err := Convert(json.RawMessage(`{"message_id":1,"from":{"id":42,"is_bot":false,"first_name":"A"},"chat":{"id":-5,"type":"group","title":"G"},"date":1,"text":"x"}`))
	if err != nil || r.ChatType != "group" {
		t.Fatalf("group = %+v, %v", r, err)
	}
	if _, err := Convert(json.RawMessage(`{"message_id":1,"chat":{"id":-100,"type":"channel"},"date":1,"text":"x"}`)); !errors.Is(err, ErrNoSender) {
		t.Fatalf("no sender err = %v", err)
	}
	if _, err := Convert(json.RawMessage(`{not json`)); err == nil {
		t.Fatal("malformed json must fail")
	}
}
