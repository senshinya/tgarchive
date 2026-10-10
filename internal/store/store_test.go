package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var ctx = context.Background()

func newStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedBot(t *testing.T, s *Store, tgID int64) int64 {
	t.Helper()
	id, err := s.UpsertBot(ctx, &Bot{TgBotID: tgID, Username: "bot", Name: "Bot", TokenEnc: []byte("enc"), CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	for i := 0; i < 2; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var v int
		if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 11 {
			t.Fatalf("user_version = %d, %v", v, err)
		}
		s.Close()
	}
}

func TestBotLifecycle(t *testing.T) {
	s := newStore(t)
	id := seedBot(t, s, 777)
	b, err := s.GetBot(ctx, id)
	if err != nil || b.TgBotID != 777 || !b.Enabled || b.Status != StatusStopped {
		t.Fatalf("GetBot = %+v, %v", b, err)
	}
	if err := s.AdvanceOffset(ctx, id, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceOffset(ctx, id, 5); err != nil {
		t.Fatal(err)
	}
	b, _ = s.GetBot(ctx, id)
	if b.UpdateOffset != 10 {
		t.Fatalf("offset must only move forward, got %d", b.UpdateOffset)
	}
	if err := s.RemoveBot(ctx, id); err != nil {
		t.Fatal(err)
	}
	b, _ = s.GetBot(ctx, id)
	if b.Status != StatusRemoved || b.Enabled || len(b.TokenEnc) != 0 {
		t.Fatalf("RemoveBot = %+v", b)
	}
	again, err := s.UpsertBot(ctx, &Bot{TgBotID: 777, Username: "bot2", TokenEnc: []byte("new"), CreatedAt: 2})
	if err != nil || again != id {
		t.Fatalf("re-add must reactivate same row: %d, %v", again, err)
	}
	b, _ = s.GetBot(ctx, id)
	if b.Status != StatusStopped || !b.Enabled || string(b.TokenEnc) != "new" || b.Username != "bot2" {
		t.Fatalf("reactivated = %+v", b)
	}
	if _, err := s.GetBot(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing bot err = %v", err)
	}
	if err := s.SetBotEnabled(ctx, 999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetBotEnabled missing = %v", err)
	}
}

func TestWhitelistAndRejected(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	if ok, _, _ := s.CheckAllowed(ctx, bot, 42); ok {
		t.Fatal("empty whitelist must reject")
	}
	for i := 0; i < 2; i++ {
		if err := s.RecordRejected(ctx, Rejected{BotID: bot, TgUserID: 42, FirstName: "Alice", LastSeenAt: int64(100 + i)}); err != nil {
			t.Fatal(err)
		}
	}
	rej, err := s.ListRejected(ctx, bot)
	if err != nil || len(rej) != 1 || rej[0].Count != 2 || rej[0].LastSeenAt != 101 {
		t.Fatalf("ListRejected = %+v, %v", rej, err)
	}
	if err := s.PutWhitelist(ctx, WhitelistEntry{BotID: bot, TgUserID: 42, Note: "me", CanFetch: true}); err != nil {
		t.Fatal(err)
	}
	ok, canFetch, err := s.CheckAllowed(ctx, bot, 42)
	if err != nil || !ok || !canFetch {
		t.Fatalf("CheckAllowed = %v %v %v", ok, canFetch, err)
	}
	if rej, _ := s.ListRejected(ctx, bot); len(rej) != 0 {
		t.Fatal("whitelisting must clear the rejected entry")
	}
	wl, _ := s.ListWhitelist(ctx, bot)
	if len(wl) != 1 || wl[0].Note != "me" {
		t.Fatalf("ListWhitelist = %+v", wl)
	}
	if err := s.DeleteWhitelist(ctx, bot, 42); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWhitelist(ctx, bot, 42); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

// Reads go through a pool of read-only connections: they see every committed write at once, and
// neither wait for nor see a write transaction in progress.
func TestReadPool(t *testing.T) {
	s := newStore(t)
	if s.ro == nil || s.reader() == s {
		t.Fatal("no read pool")
	}
	if _, err := s.ro.db.Exec("INSERT INTO settings (key, value, updated_at) VALUES ('k', x'00', 1)"); err == nil ||
		!strings.Contains(err.Error(), "readonly") {
		t.Fatalf("a write through the read pool: %v", err)
	}
	bot := seedBot(t, s, 777)
	var chat int64
	for i := int64(1); i <= 20; i++ {
		chat = ingest(t, s, bot, textMsg(i, "hi")).ChatID
		if page, err := s.ListMessages(ctx, chat, 0, 50); err != nil || int64(len(page)) != i {
			t.Fatalf("after write %d the read sees %d messages, %v", i, len(page), err)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil) // holds the writer's only connection
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE messages SET text = 'uncommitted'"); err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	chats, err := s.ListChats(rctx, 0)
	if err != nil || len(chats) != 1 || chats[0].LastText != "hi" {
		t.Fatalf("list during a write = %+v, %v", chats, err)
	}
}

// An in-memory database cannot be shared between connections: reads use the writer.
func TestInMemoryReadsShareWriter(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.reader() != s {
		t.Fatal("in-memory store has a separate read pool")
	}
	bot := seedBot(t, s, 777)
	chat := ingest(t, s, bot, textMsg(1, "hi")).ChatID
	if page, err := s.ListMessages(ctx, chat, 0, 50); err != nil || len(page) != 1 {
		t.Fatalf("page = %v, %v", page, err)
	}
}

// A database a newer build migrated past this build's migrations is refused, and left as it is.
func TestOpenRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	s.db.QueryRow("PRAGMA user_version").Scan(&v)
	newer := v + 1
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", newer)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err := Open(path); err == nil || !strings.Contains(err.Error(), "newer") {
		if s != nil {
			s.Close()
		}
		t.Fatalf("open newer = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != newer {
		t.Fatalf("user_version after the refusal = %d, %v", v, err)
	}
}
