package avatars

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tgarchive/internal/botapifs"
	"tgarchive/internal/botclients"
	"tgarchive/internal/model"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
)

func TestRefresh(t *testing.T) {
	ctx := context.Background()
	fake := tgtest.New(t)
	fake.SetAvatar(42, "av42", []byte("face"))
	st, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	bot, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: box.Seal([]byte("777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")), CreatedAt: 1})
	st.Ingest(ctx, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Now: 1,
		Msg: &model.Message{TgMessageID: 1, Source: model.SourceBotUpdate, Kind: model.KindText, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}})
	dir := t.TempDir()
	r := &Refresher{Store: st, Clients: botclients.New(st, box, fake.URL(), nil), Mapper: botapifs.Mapper{Remote: fake.RemoteDir, Local: fake.RemoteDir}, Dir: dir}

	r.RefreshAll(ctx)

	if b, err := os.ReadFile(filepath.Join(dir, "senders", "42.jpg")); err != nil || string(b) != "face" {
		t.Fatalf("sender avatar = %q, %v", b, err)
	}
	chats, _ := st.ListChats(ctx, 0)
	if !chats[0].Sender.HasAvatar {
		t.Fatal("sender avatar path not stored")
	}
	if _, err := os.Stat(filepath.Join(dir, "bots", "777.jpg")); err == nil {
		t.Fatal("bot without profile photo must not get an avatar file")
	}
	if b, _ := st.GetBot(ctx, bot); b.AvatarPath != "" {
		t.Fatal("bot avatar path must stay empty")
	}
	call := fake.Calls("getFile")[0]
	if call.Params["file_id"] != "av42" {
		t.Fatalf("picked file = %v", call.Params["file_id"])
	}
}
