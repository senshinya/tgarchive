package store

import (
	"errors"
	"testing"

	"tgarchive/internal/model"
)

func TestUserbotRow(t *testing.T) {
	s := newStore(t)
	u, err := s.GetUserbot(ctx)
	if err != nil || u.Status != UserbotLoggedOut || u.SessionEnc != nil {
		t.Fatalf("fresh = %+v, %v", u, err)
	}
	if err := s.SaveUserbotSession(ctx, []byte("enc1"), 10); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserbotAccount(ctx, "+100", 99, "Me", 11); err != nil {
		t.Fatal(err)
	}
	u, _ = s.GetUserbot(ctx)
	if string(u.SessionEnc) != "enc1" || u.Phone != "+100" || u.TgUserID != 99 || u.Name != "Me" || u.Status != UserbotReady || u.UpdatedAt != 11 {
		t.Fatalf("after login = %+v", u)
	}
	if err := s.SetUserbotStatus(ctx, UserbotError, "revoked", 12); err != nil {
		t.Fatal(err)
	}
	u, _ = s.GetUserbot(ctx)
	if u.Status != UserbotError || u.LastError != "revoked" || u.TgUserID != 99 {
		t.Fatalf("after error = %+v", u)
	}
	if err := s.ClearUserbot(ctx, 13); err != nil {
		t.Fatal(err)
	}
	u, _ = s.GetUserbot(ctx)
	if u.SessionEnc != nil || u.Phone != "" || u.TgUserID != 0 || u.Status != UserbotLoggedOut || u.LastError != "" {
		t.Fatalf("after clear = %+v", u)
	}
}

func TestPeers(t *testing.T) {
	s := newStore(t)
	if _, err := s.GetPeer(ctx, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing peer err = %v", err)
	}
	if err := s.PutPeers(ctx, []Peer{{ChannelID: 5, AccessHash: 50, Username: "Chan", Title: "C"}, {ChannelID: 6, AccessHash: 60}}, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPeers(ctx, []Peer{{ChannelID: 5, AccessHash: 51, Title: "C2"}}, 2); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetPeer(ctx, 5)
	if err != nil || p.AccessHash != 51 || p.Title != "C2" || p.Username != "" {
		t.Fatalf("peer 5 = %+v, %v", p, err)
	}
}

func TestSenders(t *testing.T) {
	s := newStore(t)
	if _, err := s.GetSender(ctx, 42); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing sender err = %v", err)
	}
	snd := model.Sender{TgUserID: 42, FirstName: "A", LastName: "B", Username: "ab"}
	if err := s.UpsertSender(ctx, snd, 1); err != nil {
		t.Fatal(err)
	}
	snd.FirstName = "A2"
	if err := s.UpsertSender(ctx, snd, 2); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetSender(ctx, 42); err != nil || got != snd {
		t.Fatalf("sender = %+v, %v", got, err)
	}
}
