package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"tgarchive/internal/model"
)

const chanID = 1500

func seedWatch(t *testing.T, s *Store) int64 {
	t.Helper()
	if err := s.UpsertChannel(ctx, Channel{ChannelID: chanID, Title: "News", Username: "news"}, 100); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateWatch(ctx, &Watch{ChannelID: chanID, WindowMinutes: 30, Cond: `{"op":"and","items":[]}`, Enabled: true, LastSeenID: 9, CreatedAt: 200})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func channelPost(id int64, group string, keys ...string) *model.Message {
	m := photoMsg(id, keys...)
	m.Source = model.SourceChannelWatch
	m.MediaGroupID = group
	m.OriginChatID = -1000000000000 - chanID
	m.RawFormat = model.RawMTProto
	return m
}

func TestMigrationRebuildKeepsChatsAndMessages(t *testing.T) {
	// Build an at-version-4 database with data in it, then upgrade.
	path := filepath.Join(t.TempDir(), "v4.db")
	s := openAt(t, path, 4)
	bot := seedBot(t, s, 777)
	for _, q := range []string{
		"INSERT INTO senders (tg_user_id, first_name, updated_at) VALUES (42, 'Alice', 1)",
		"INSERT INTO chats (id, bot_id, sender_id, last_message_at) VALUES (3, 1, 42, 10)",
		`INSERT INTO messages (id, chat_id, tg_message_id, source, date, kind, raw_format, raw_json)
			VALUES (5, 3, 1, 'bot_update', 10, 'photo', 'botapi', '{}')`,
		"INSERT INTO media (id, dedupe_key, kind) VALUES (8, 'bot:a', 'photo')",
		"INSERT INTO message_media (message_id, media_id, role) VALUES (5, 8, 'main')",
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	res := &IngestResult{ChatID: 3}
	s.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	views, err := s.ListMessages(ctx, res.ChatID, 0, 10)
	if err != nil || len(views) != 1 || len(views[0].Media) != 1 {
		t.Fatalf("messages after upgrade = %+v, %v", views, err)
	}
	chats, err := s.ListChats(ctx, 0)
	if err != nil || len(chats) != 1 || chats[0].Kind != "private" || chats[0].BotID != bot || chats[0].Sender.TgUserID != 42 ||
		chats[0].Channel != nil || chats[0].Watch != nil {
		t.Fatalf("chats after upgrade = %+v, %v", chats, err)
	}
	// Cascades still point at the rebuilt table: purging the bot removes its chat and messages.
	if _, err := s.PurgeBot(ctx, bot); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n)
	if n != 0 {
		t.Fatalf("messages left after purge: %d", n)
	}
}

// openAt opens a database migrated only up to migration n.
func openAt(t *testing.T, path string, n int) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx, n); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateWatchMakesConversation(t *testing.T) {
	s := newStore(t)
	id := seedWatch(t, s)
	if _, err := s.CreateWatch(ctx, &Watch{ChannelID: chanID, WindowMinutes: 5, Cond: "{}", CreatedAt: 300}); !errors.Is(err, ErrExists) {
		t.Fatalf("second watch: %v", err)
	}
	w, err := s.GetWatch(ctx, id)
	if err != nil || w.Channel.Title != "News" || w.ChatID == 0 || !w.Enabled || w.Status != WatchOK || w.LastSeenID != 9 {
		t.Fatalf("GetWatch = %+v, %v", w, err)
	}
	chats, _ := s.ListChats(ctx, 0)
	if len(chats) != 1 || chats[0].Kind != "channel" || chats[0].Channel == nil || chats[0].Channel.Username != "news" ||
		chats[0].Watch == nil || chats[0].Watch.ID != id || chats[0].Watch.WindowMinutes != 30 || chats[0].LastMessageAt != 200 {
		t.Fatalf("ListChats = %+v", chats)
	}
	// A bot filter never lists channels.
	if chats, _ := s.ListChats(ctx, 1); len(chats) != 0 {
		t.Fatalf("filtered = %+v", chats)
	}
}

func TestChannelIngestAndListing(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	ingest(t, s, bot, textMsg(1, "hi"))
	seedWatch(t, s)
	res, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(50, "g", "mt:photo:1"), Stats: `{"views":5}`, Now: 9000})
	if err != nil || !res.Created || res.ChatCreated {
		t.Fatalf("Ingest = %+v, %v", res, err)
	}
	chats, _ := s.ListChats(ctx, 0)
	if len(chats) != 2 || chats[0].ID != res.ChatID || chats[0].LastMessageAt != 9000 || chats[0].LastKind != "photo" {
		t.Fatalf("ListChats = %+v", chats)
	}
	v, err := s.GetMessageView(ctx, res.MessageID)
	if err != nil || string(v.Stats) != `{"views":5}` || v.Source != model.SourceChannelWatch {
		t.Fatalf("view = %+v, %v", v, err)
	}
	// Re-archiving the same post is an update, not a second message.
	again, _ := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(50, "g", "mt:photo:1"), Now: 9100})
	if again.Created || again.MessageID != res.MessageID {
		t.Fatalf("again = %+v", again)
	}
	// Receipts and avatar refreshes skip the channel conversation instead of failing on NULL columns.
	ri, err := s.GetReceiptInfo(ctx, res.MessageID)
	if err != nil || ri.BotID != 0 || ri.Source != model.SourceChannelWatch {
		t.Fatalf("receipt info = %+v, %v", ri, err)
	}
	if cs, err := s.ChatSenders(ctx); err != nil || len(cs) != 1 {
		t.Fatalf("ChatSenders = %+v, %v", cs, err)
	}
	// The bot's merged timeline does not include the channel.
	if views, _ := s.ListBotMessages(ctx, bot, 0, 10); len(views) != 1 {
		t.Fatalf("bot timeline = %+v", views)
	}
}

func TestPendingLifecycle(t *testing.T) {
	s := newStore(t)
	id := seedWatch(t, s)
	if err := s.AddPending(ctx, id, []Pending{{TgMessageID: 10, Date: 1, Deadline: 61}, {TgMessageID: 11, GroupedID: 7, Date: 2, Deadline: 62}}, 11); err != nil {
		t.Fatal(err)
	}
	// Adding a known post again keeps its deadline; the high-water mark never goes back.
	if err := s.AddPending(ctx, id, []Pending{{TgMessageID: 10, Date: 1, Deadline: 999}}, 5); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.ListPending(ctx, id)
	if len(ps) != 2 || ps[0].Deadline != 61 || ps[1].GroupedID != 7 {
		t.Fatalf("pending = %+v", ps)
	}
	w, _ := s.GetWatch(ctx, id)
	if w.LastSeenID != 11 || w.Pending != 2 {
		t.Fatalf("watch = %+v", w)
	}
	if err := s.DeletePending(ctx, id, []int64{10}); err != nil {
		t.Fatal(err)
	}
	if ps, _ := s.ListPending(ctx, id); len(ps) != 1 {
		t.Fatalf("after delete = %+v", ps)
	}
	if changed, _ := s.SetWatchStatus(ctx, id, WatchError, "gone", 5); !changed {
		t.Fatal("status should change")
	}
	if changed, _ := s.SetWatchStatus(ctx, id, WatchError, "gone", 6); changed {
		t.Fatal("same status should not count as a change")
	}
	// Disabling clears the error.
	if err := s.UpdateWatch(ctx, id, 10, "{}", false, 7); err != nil {
		t.Fatal(err)
	}
	w, _ = s.GetWatch(ctx, id)
	if w.Status != WatchOK || w.LastError != "" || w.Enabled || w.WindowMinutes != 10 {
		t.Fatalf("after disable = %+v", w)
	}
}

func TestDeleteWatch(t *testing.T) {
	s := newStore(t)
	id := seedWatch(t, s)
	res, _ := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(50, "", "mt:photo:1"), Now: 9000})
	emoji, err := s.RegisterCustomEmoji(ctx, 77, model.Media{DedupeKey: "mt:doc:77", SourceRef: "{}", Kind: "sticker", Mime: "image/webp"})
	if err != nil || emoji == 0 {
		t.Fatal(err)
	}
	if again, _ := s.RegisterCustomEmoji(ctx, 77, model.Media{DedupeKey: "mt:doc:77"}); again != emoji {
		t.Fatalf("re-register = %d", again)
	}
	s.AddPending(ctx, id, []Pending{{TgMessageID: 60, Date: 1, Deadline: 2}}, 60)

	// Stopping keeps the conversation and its archive.
	chatID, orphans, err := s.DeleteWatch(ctx, id, false)
	if err != nil || chatID != 0 || len(orphans) != 0 {
		t.Fatalf("stop = %d %v %v", chatID, orphans, err)
	}
	chats, _ := s.ListChats(ctx, 0)
	if len(chats) != 1 || chats[0].Watch != nil {
		t.Fatalf("chats after stop = %+v", chats)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM watch_pending").Scan(&n)
	if n != 0 {
		t.Fatalf("pending left: %d", n)
	}
	if _, _, err := s.DeleteWatch(ctx, id, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}

	// Purging a new watch on the same channel removes the conversation, its messages and media,
	// but not the custom emoji.
	id2, err := s.CreateWatch(ctx, &Watch{ChannelID: chanID, WindowMinutes: 5, Cond: "{}", CreatedAt: 300})
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec("UPDATE media SET path = 'p/1.jpg' WHERE dedupe_key = 'mt:photo:1'")
	chatID, orphans, err = s.DeleteWatch(ctx, id2, true)
	if err != nil || chatID != res.ChatID || len(orphans) != 1 || orphans[0] != "p/1.jpg" {
		t.Fatalf("purge = %d %v %v", chatID, orphans, err)
	}
	if chats, _ := s.ListChats(ctx, 0); len(chats) != 0 {
		t.Fatalf("chats after purge = %+v", chats)
	}
	got, _ := s.CustomEmojiMedia(ctx, []int64{77, 78})
	if len(got) != 1 || got[77].MediaID != emoji || got[77].Mime != "image/webp" {
		t.Fatalf("custom emoji = %v", got)
	}
	s.db.QueryRow("SELECT COUNT(*) FROM media").Scan(&n)
	if n != 1 {
		t.Fatalf("media left = %d", n)
	}
}

func TestStatsJSONRoundTrip(t *testing.T) {
	s := newStore(t)
	seedWatch(t, s)
	stats, _ := json.Marshal(map[string]any{"views": 10})
	res, _ := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(1, ""), Stats: string(stats), Now: 1})
	views, _ := s.ListMessages(ctx, res.ChatID, 0, 10)
	if len(views) != 1 || string(views[0].Stats) != `{"views":10}` {
		t.Fatalf("views = %+v", views)
	}
}

func TestReenableStartsOver(t *testing.T) {
	s := newStore(t)
	id := seedWatch(t, s)
	s.AddPending(ctx, id, []Pending{{TgMessageID: 10, Date: 1, Deadline: 2}}, 10)
	s.UpdateWatch(ctx, id, 30, "{}", true, 3) // still enabled: nothing reset
	if w, _ := s.GetWatch(ctx, id); w.LastSeenID != 10 || w.Pending != 1 {
		t.Fatalf("enabled → enabled = %+v", w)
	}
	s.UpdateWatch(ctx, id, 30, "{}", false, 4)
	s.UpdateWatch(ctx, id, 30, "{}", true, 5)
	if w, _ := s.GetWatch(ctx, id); w.LastSeenID != 0 || w.Pending != 0 {
		t.Fatalf("re-enabled = %+v", w)
	}
	if err := s.SetWatchStart(ctx, id, WatchStartEmpty); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWatchStart(ctx, id, 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a started watch keeps its start: %v", err)
	}
}

func TestChannelIngestNeedsAWatch(t *testing.T) {
	s := newStore(t)
	id := seedWatch(t, s)
	res, _ := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(1, ""), Stats: `{"views":1}`, Now: 1})
	// Re-archiving refreshes the stats snapshot.
	s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(1, ""), Stats: `{"views":2}`, Now: 2})
	if v, _ := s.GetMessageView(ctx, res.MessageID); string(v.Stats) != `{"views":2}` {
		t.Fatalf("stats = %s", v.Stats)
	}
	s.DeleteWatch(ctx, id, true)
	if _, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(2, ""), Now: 3}); !errors.Is(err, ErrNoWatch) {
		t.Fatalf("ingest after purge: %v", err)
	}
	if chats, _ := s.ListChats(ctx, 0); len(chats) != 0 {
		t.Fatalf("purged conversation came back: %+v", chats)
	}
}

func TestChannelInfoOnlyForStoredChannels(t *testing.T) {
	s := newStore(t)
	if ok, err := s.RefreshChannelInfo(ctx, Channel{ChannelID: 9, Title: "x"}, 1); ok || err != nil {
		t.Fatalf("unknown channel = %v %v", ok, err)
	}
	seedWatch(t, s)
	s.UpsertChannel(ctx, Channel{ChannelID: 77, Title: "orphan"}, 1)
	if ok, _ := s.RefreshChannelInfo(ctx, Channel{ChannelID: chanID, Title: "Renamed"}, 2); !ok {
		t.Fatal("stored channel not refreshed")
	}
	if c, _ := s.GetChannel(ctx, chanID); c.Title != "Renamed" {
		t.Fatalf("channel = %+v", c)
	}
	if ids, _ := s.ChannelIDs(ctx); len(ids) != 1 || ids[0] != chanID {
		t.Fatalf("ChannelIDs = %v", ids)
	}
}

func TestAddPendingAfterResetIsDropped(t *testing.T) {
	s := newStore(t)
	id := seedWatch(t, s)
	// A poll read the watch, then it was disabled and re-enabled (reset to 0) before the poll saved.
	s.UpdateWatch(ctx, id, 30, "{}", false, 2)
	s.UpdateWatch(ctx, id, 30, "{}", true, 3)
	if err := s.AddPending(ctx, id, []Pending{{TgMessageID: 20, Date: 1, Deadline: 2}}, 20); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.GetWatch(ctx, id); w.LastSeenID != 0 || w.Pending != 0 {
		t.Fatalf("stale poll undid the reset: %+v", w)
	}
}

func TestUpdateWatchMovesPendingDeadlines(t *testing.T) {
	s := newStore(t)
	id := seedWatch(t, s)
	s.AddPending(ctx, id, []Pending{{TgMessageID: 10, Date: 1000, Deadline: 1000 + 30*60}}, 10)
	s.UpdateWatch(ctx, id, 360, "{}", true, 2)
	if ps, _ := s.ListPending(ctx, id); len(ps) != 1 || ps[0].Deadline != 1000+360*60 {
		t.Fatalf("pending = %+v", ps)
	}
}

func TestWatchActivity(t *testing.T) {
	s := newStore(t)
	id := seedWatch(t, s)
	const now = 10 * 86400
	post := func(tgID int64, group string, date int64) {
		m := channelPost(tgID, group)
		m.Date = date
		if _, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: m, Now: date}); err != nil {
			t.Fatal(err)
		}
	}
	post(1, "g", now-3600) // one album of three counts once
	post(2, "g", now-3600)
	post(3, "g", now-3500)
	post(4, "", now-2*86400)  // within 7d only
	post(5, "", now-8*86400)  // older than both
	act, err := s.WatchActivity(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if a := act[id]; a.Hits24h != 1 || a.Hits7d != 2 || a.LastHitAt != now-3500 {
		t.Fatalf("activity = %+v", a)
	}
	if w, _ := s.GetWatch(ctx, id); w.LastPolledAt != 0 {
		t.Fatalf("never polled: %d", w.LastPolledAt)
	}
	if _, err := s.SetWatchStatus(ctx, id, WatchOK, "", 500); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.GetWatch(ctx, id); w.LastPolledAt != 500 {
		t.Fatalf("a poll with no status change still records its time: %d", w.LastPolledAt)
	}
	if changed, _ := s.SetWatchStatus(ctx, id, WatchError, "x", 600); !changed {
		t.Fatal("status change must be reported")
	}
	if w, _ := s.GetWatch(ctx, id); w.LastPolledAt != 600 {
		t.Fatalf("last_polled_at = %d", w.LastPolledAt)
	}
}
