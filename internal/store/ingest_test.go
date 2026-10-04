package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"tgarchive/internal/model"
)

var alice = model.Sender{TgUserID: 42, FirstName: "Alice"}

func photoMsg(id int64, keys ...string) *model.Message {
	m := &model.Message{TgMessageID: id, Source: model.SourceBotUpdate, Date: 1000 + id, Kind: model.KindPhoto,
		Text: "caption", RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}
	for _, k := range keys {
		m.Media = append(m.Media, model.Media{DedupeKey: k, SourceRef: "f-" + k, Kind: "photo", Mime: "image/jpeg", Size: 4, Role: model.RoleMain})
	}
	return m
}

func textMsg(id int64, text string) *model.Message {
	return &model.Message{TgMessageID: id, Source: model.SourceBotUpdate, Date: 1000 + id, Kind: model.KindText,
		Text: text, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}
}

func ingest(t *testing.T, s *Store, bot int64, m *model.Message) *IngestResult {
	t.Helper()
	res, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: alice, Msg: m, Now: 5000})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func mediaIDs(t *testing.T, s *Store, msgID int64) []int64 {
	t.Helper()
	rows, err := s.db.Query("SELECT media_id FROM message_media WHERE message_id = ? ORDER BY position", msgID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids
}

func TestIngestCreatesEverything(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: alice, Msg: photoMsg(1, "bot:a"), Offset: 5, Now: 5000})
	if err != nil || !res.Created || !res.ChatCreated || res.MessageID == 0 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if b, _ := s.GetBot(ctx, bot); b.UpdateOffset != 5 {
		t.Fatalf("offset = %d", b.UpdateOffset)
	}
	res2 := ingest(t, s, bot, textMsg(2, "hi"))
	if res2.ChatCreated || res2.ChatID != res.ChatID {
		t.Fatalf("second message must reuse chat: %+v", res2)
	}
	due, _ := s.DueMedia(ctx, 0, 10)
	if len(due) != 1 || due[0].DedupeKey != "bot:a" || due[0].BotID != bot || due[0].SourceRef != "f-bot:a" || due[0].State != StatePending {
		t.Fatalf("due = %+v", due)
	}
}

func TestIngestEditReplacesMedia(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res := ingest(t, s, bot, photoMsg(1, "bot:a"))
	oldID := mediaIDs(t, s, res.MessageID)[0]
	if ok, err := s.MarkMediaDone(ctx, oldID, "1/a.jpg", 4); !ok || err != nil {
		t.Fatal(ok, err)
	}
	edit := photoMsg(1, "bot:b")
	edit.Text, edit.EditDate = "new caption", 2000
	res2 := ingest(t, s, bot, edit)
	if res2.Created || res2.MessageID != res.MessageID || !reflect.DeepEqual(res2.OrphanPaths, []string{"1/a.jpg"}) {
		t.Fatalf("edit res = %+v", res2)
	}
	if _, err := s.GetMedia(ctx, oldID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replaced media must be deleted, err = %v", err)
	}
	var text string
	var editDate int64
	s.db.QueryRow("SELECT text, edit_date FROM messages WHERE id = ?", res.MessageID).Scan(&text, &editDate)
	if text != "new caption" || editDate != 2000 {
		t.Fatalf("edit not applied: %q %d", text, editDate)
	}
}

func TestEditOfDeletedMessageIgnored(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	r := ingest(t, s, bot, photoMsg(1, "bot:a"))
	if _, _, err := s.DeleteMessage(ctx, r.MessageID, 9000); err != nil {
		t.Fatal(err)
	}
	var textBefore string
	s.db.QueryRow("SELECT text FROM messages WHERE id = ?", r.MessageID).Scan(&textBefore)

	edit := photoMsg(1, "bot:b")
	edit.Text, edit.EditDate = "new caption", 2000
	res2, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: alice, Msg: edit, Offset: 9, Now: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Created || res2.MessageID != r.MessageID || res2.OrphanPaths != nil {
		t.Fatalf("edit of deleted message = %+v", res2)
	}
	if due, _ := s.DueMedia(ctx, 0, 10); len(due) != 0 {
		t.Fatalf("no media row for bot:b must be created: %+v", due)
	}
	var text string
	var deletedAt int64
	s.db.QueryRow("SELECT text, deleted_at FROM messages WHERE id = ?", r.MessageID).Scan(&text, &deletedAt)
	if text != textBefore || deletedAt == 0 {
		t.Fatalf("message must be unchanged and still deleted: text=%q deletedAt=%d", text, deletedAt)
	}
	if b, _ := s.GetBot(ctx, bot); b.UpdateOffset != 9 {
		t.Fatalf("offset must still advance: %d", b.UpdateOffset)
	}
}

func TestDedupeAndOrphanCleanup(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	r1 := ingest(t, s, bot, photoMsg(1, "bot:k"))
	r2 := ingest(t, s, bot, photoMsg(2, "bot:k"))
	id1, id2 := mediaIDs(t, s, r1.MessageID)[0], mediaIDs(t, s, r2.MessageID)[0]
	if id1 != id2 {
		t.Fatalf("same dedupe key must share a media row: %d vs %d", id1, id2)
	}
	s.MarkMediaDone(ctx, id1, "1/k.jpg", 4)
	if _, orphans, err := s.DeleteMessage(ctx, r1.MessageID, 9000); err != nil || len(orphans) != 0 {
		t.Fatalf("first delete must keep shared file: %v %v", orphans, err)
	}
	chatID, orphans, err := s.DeleteMessage(ctx, r2.MessageID, 9000)
	if err != nil || chatID != r2.ChatID || !reflect.DeepEqual(orphans, []string{"1/k.jpg"}) {
		t.Fatalf("last delete = %d %v %v", chatID, orphans, err)
	}
	if _, _, err := s.DeleteMessage(ctx, r2.MessageID, 9000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete = %v", err)
	}
}

func TestFailedMediaRequeuedWhenSeenAgain(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	r := ingest(t, s, bot, photoMsg(1, "bot:k"))
	id := mediaIDs(t, s, r.MessageID)[0]
	s.MarkMediaFailed(ctx, id, 4, "boom")
	ingest(t, s, bot, photoMsg(2, "bot:k"))
	m, _ := s.GetMedia(ctx, id)
	if m.State != StatePending || m.Attempts != 0 || m.Error != "" {
		t.Fatalf("media = %+v", m)
	}
}

func TestMediaStateMachine(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	r := ingest(t, s, bot, photoMsg(1, "bot:k"))
	id := mediaIDs(t, s, r.MessageID)[0]
	if err := s.MarkMediaRetry(ctx, id, 1, 160, "net"); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.DueMedia(ctx, 100, 10); len(due) != 0 {
		t.Fatal("media must not be due before next_attempt_at")
	}
	if due, _ := s.DueMedia(ctx, 160, 10); len(due) != 1 || due[0].Attempts != 1 || due[0].Error != "net" {
		t.Fatalf("due = %+v", due)
	}
	if err := s.ResetMedia(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reset of non-failed media = %v", err)
	}
	s.MarkMediaFailed(ctx, id, 4, "gave up")
	if err := s.ResetMedia(ctx, id); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMedia(ctx, id); m.State != StatePending || m.Attempts != 0 {
		t.Fatalf("after reset = %+v", m)
	}
	if err := s.MarkMediaTooLarge(ctx, id); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkMediaDone(ctx, 9999, "x", 1); ok || err != nil {
		t.Fatalf("MarkMediaDone on missing row = %v %v", ok, err)
	}
	ids, _ := s.MessagesForMedia(ctx, id)
	if !reflect.DeepEqual(ids, []int64{r.MessageID}) {
		t.Fatalf("MessagesForMedia = %v", ids)
	}
}

func TestReceiptInfo(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	m := photoMsg(7, "bot:main")
	m.Media = append(m.Media, model.Media{DedupeKey: "bot:thumb", SourceRef: "t", Kind: "photo", Role: model.RoleThumb})
	r := ingest(t, s, bot, m)
	info, err := s.GetReceiptInfo(ctx, r.MessageID)
	if err != nil || info.BotID != bot || info.TgChatID != 42 || info.TgMessageID != 7 || info.Receipt != ReceiptNone || info.Source != model.SourceBotUpdate {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if len(info.Main) != 1 || info.Main[0].State != StatePending {
		t.Fatalf("only main media count: %+v", info.Main)
	}
	s.SetReceipt(ctx, r.MessageID, ReceiptSeen)
	if info, _ := s.GetReceiptInfo(ctx, r.MessageID); info.Receipt != ReceiptSeen {
		t.Fatal("SetReceipt not applied")
	}
}

func TestPurgeAndRemoveBot(t *testing.T) {
	s := newStore(t)
	keep := seedBot(t, s, 1)
	purge := seedBot(t, s, 2)
	ingest(t, s, keep, textMsg(1, "kept"))
	r := ingest(t, s, purge, photoMsg(1, "bot:p"))
	s.MarkMediaDone(ctx, mediaIDs(t, s, r.MessageID)[0], "2/p.jpg", 4)
	if err := s.RemoveBot(ctx, keep); err != nil {
		t.Fatal(err)
	}
	paths, err := s.PurgeBot(ctx, purge)
	if err != nil || !reflect.DeepEqual(paths, []string{"2/p.jpg"}) {
		t.Fatalf("purge = %v %v", paths, err)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n)
	if n != 1 {
		t.Fatalf("removed bot must keep its archive, purged must not: %d messages", n)
	}
	if _, err := s.PurgeBot(ctx, purge); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second purge = %v", err)
	}
}

func TestChatSendersAndAvatar(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	ingest(t, s, bot, textMsg(1, "x"))
	cs, err := s.ChatSenders(ctx)
	if err != nil || len(cs) != 1 || cs[0].BotID != bot || cs[0].TgUserID != 42 {
		t.Fatalf("ChatSenders = %+v %v", cs, err)
	}
	if err := s.SetSenderAvatar(ctx, 42, "senders/42.jpg"); err != nil {
		t.Fatal(err)
	}
}

func TestUserbotFetchesKeyedByOriginChat(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	fetched := func(origin int64, key, text string) *model.Message {
		m := photoMsg(7, key)
		m.Source = model.SourceUserbotFetch
		m.OriginChatID = origin
		m.Text = text
		return m
	}
	a := ingest(t, s, bot, fetched(-1001, "user:a", "from A"))
	b := ingest(t, s, bot, fetched(-1002, "user:b", "from B"))
	if !a.Created || !b.Created || a.MessageID == b.MessageID {
		t.Fatalf("a = %+v, b = %+v", a, b)
	}
	// Re-fetching A's message (an edit) must update A only.
	again := ingest(t, s, bot, fetched(-1001, "user:a2", "A edited"))
	if again.Created || again.MessageID != a.MessageID {
		t.Fatalf("refetch = %+v", again)
	}
	if ids := mediaIDs(t, s, b.MessageID); len(ids) != 1 {
		t.Fatalf("B media after editing A = %v", ids)
	}
	var textB string
	s.db.QueryRow("SELECT text FROM messages WHERE id = ?", b.MessageID).Scan(&textB)
	if textB != "from B" {
		t.Fatalf("B text = %q", textB)
	}
	if _, _, err := s.DeleteMessage(ctx, a.MessageID, 9); err != nil {
		t.Fatal(err)
	}
	if ids := mediaIDs(t, s, b.MessageID); len(ids) != 1 {
		t.Fatalf("B media after deleting A = %v", ids)
	}
}
