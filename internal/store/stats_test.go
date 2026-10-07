package store

import (
	"reflect"
	"testing"

	"tgarchive/internal/model"
)

func TestStats(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	watch := seedWatch(t, s)
	const jan1 = 1704067200 // 2024-01-01 00:00 UTC
	now := int64(jan1 + 10*86400)
	at := func(m *model.Message, date int64) *model.Message { m.Date = date; return m }
	mediaOf := func(msgID int64) []int64 { t.Helper(); return mediaIDs(t, s, msgID) }
	done := func(id, size int64) {
		t.Helper()
		if _, err := s.MarkMediaDone(ctx, id, "p", size); err != nil {
			t.Fatal(err)
		}
	}

	m1 := ingest(t, s, bot, at(photoMsg(1, "bot:a"), jan1-3600))
	ingest(t, s, bot, at(photoMsg(2, "bot:a"), jan1+3600)) // the same photo again
	video := kindMsg(3, model.KindVideo, "video", "bot:v")
	video.Media = append(video.Media, model.Media{DedupeKey: "bot:vt", Kind: "photo", Role: model.RoleThumb})
	m3 := ingest(t, s, bot, at(video, jan1+4*86400+12*3600))
	gone := ingest(t, s, bot, at(kindMsg(4, model.KindDocument, "document", "bot:gone"), jan1))
	ingest(t, s, bot, at(textMsg(5, "old"), now-400*86400))
	done(mediaOf(m1.MessageID)[0], 100)
	vids := mediaOf(m3.MessageID)
	done(vids[0], 1000)
	done(vids[1], 10)
	done(mediaOf(gone.MessageID)[0], 5000)
	if _, _, err := s.DeleteMessage(ctx, gone.MessageID, 1); err != nil {
		t.Fatal(err)
	}

	var chanMsgs []int64
	for _, p := range []*model.Message{at(channelPost(50, "g", "mt:photo:1"), now-86400), at(channelPost(51, "g", "mt:photo:2"), now-86400),
		at(channelPost(52, "", "mt:photo:3"), now-40*86400)} {
		res, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: p, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		chanMsgs = append(chanMsgs, res.MessageID)
	}
	s.MarkMediaFailed(ctx, mediaOf(chanMsgs[0])[0], 3, "x")
	s.MarkMediaTooLarge(ctx, mediaOf(chanMsgs[2])[0])
	s.AddPending(ctx, watch, []Pending{{TgMessageID: 50, GroupedID: 1}, {TgMessageID: 51, GroupedID: 1}, {TgMessageID: 53}}, 53)
	s.AddWatchHit(ctx, watch, true)

	st, err := s.Stats(ctx, 480, now)
	if err != nil {
		t.Fatal(err)
	}
	tot := st.Totals
	if tot.Messages != 7 || tot.PrivateChats != 1 || tot.ChannelChats != 1 || tot.MediaFiles != 6 || tot.MediaBytes != 1110 || tot.DBBytes <= 0 {
		t.Fatalf("totals = %+v", tot)
	}
	wantDaily := []DayCount{{"2023-12-02", 1}, {"2024-01-01", 2}, {"2024-01-05", 1}, {"2024-01-10", 2}}
	if !reflect.DeepEqual(st.Daily, wantDaily) {
		t.Fatalf("daily = %+v", st.Daily)
	}
	wantMonthly := []MonthStat{{"2022-12", 1, 0}, {"2023-12", 1, 0}, {"2024-01", 5, 1110}}
	if !reflect.DeepEqual(st.Monthly, wantMonthly) {
		t.Fatalf("monthly = %+v", st.Monthly)
	}
	if len(st.TopChats) != 2 || st.TopChats[0] != (ChatStat{m1.ChatID, 4, 1110}) || st.TopChats[1].Messages != 3 || st.TopChats[1].MediaBytes != 0 {
		t.Fatalf("top chats = %+v", st.TopChats)
	}
	wantKinds := []KindStat{{"photo", 4, 100}, {"other", 1, 10}, {"video", 1, 1000}}
	if !reflect.DeepEqual(st.MediaKinds, wantKinds) {
		t.Fatalf("kinds = %+v", st.MediaKinds)
	}
	if !reflect.DeepEqual(st.MediaStates, map[string]int64{"done": 3, "pending": 1, "failed": 1, "too_large": 1}) {
		t.Fatalf("states = %+v", st.MediaStates)
	}
	if len(st.Watches) != 1 {
		t.Fatalf("watches = %+v", st.Watches)
	}
	w := st.Watches[0]
	if w.WatchID != watch || w.ChatID == 0 || w.Title != "News" || w.Hits != 1 || w.Scanned != 2 || w.ScanHits != 1 ||
		!reflect.DeepEqual(w.Daily, []DayCount{{"2024-01-10", 1}}) {
		t.Fatalf("watch = %+v", w)
	}

	// Without the offset the photo sent at 23:00 UTC belongs to the last day of 2023.
	st, _ = s.Stats(ctx, 0, now)
	if st.Daily[1] != (DayCount{"2023-12-31", 1}) {
		t.Fatalf("UTC daily = %+v", st.Daily)
	}
}

func TestStatsEmpty(t *testing.T) {
	s := newStore(t)
	st, err := s.Stats(ctx, 0, 1704067200)
	if err != nil || st.Totals.Messages != 0 || st.Daily == nil || st.Monthly == nil || st.TopChats == nil || st.MediaKinds == nil ||
		st.Watches == nil || len(st.MediaStates) != 4 {
		t.Fatalf("empty stats = %+v, %v", st, err)
	}
}

func TestStatsDailyCoversTheHeatmap(t *testing.T) {
	// The heatmap starts on the Monday 52 weeks before this week: up to 52×7+6 = 370 days back.
	s := newStore(t)
	bot := seedBot(t, s, 777)
	now := int64(1704067200 + 10*86400) // 2024-01-11, a Thursday
	m := textMsg(1, "old")
	m.Date = now - 370*86400
	ingest(t, s, bot, m)
	st, err := s.Stats(ctx, 0, now)
	if err != nil || len(st.Daily) != 1 || st.Daily[0].Day != "2023-01-06" {
		t.Fatalf("daily = %+v, %v", st.Daily, err)
	}
}
