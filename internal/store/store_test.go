package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
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
		if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 3 {
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
