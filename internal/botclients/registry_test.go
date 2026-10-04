package botclients

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
)

const token = "777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestRegistry(t *testing.T) {
	ctx := context.Background()
	f := tgtest.New(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	id, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: box.Seal([]byte(token)), CreatedAt: 1})
	r := New(st, box, f.URL(), nil)

	if err := r.SetReaction(ctx, id, 42, 10, "👌"); err != nil {
		t.Fatal(err)
	}
	if err := r.Reply(ctx, id, 42, 10, "hello"); err != nil {
		t.Fatal(err)
	}
	if c := f.Calls("setMessageReaction"); len(c) != 1 || c[0].Token != token {
		t.Fatalf("calls = %+v", c)
	}
	if c := f.Calls("sendMessage"); len(c) != 1 || c[0].Params["text"] != "hello" {
		t.Fatalf("sendMessage = %+v", c)
	}

	st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: box.Seal([]byte("777:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")), CreatedAt: 1})
	r.Forget(id)
	r.SetReaction(ctx, id, 42, 11, "👌")
	if c := f.Calls("setMessageReaction"); c[1].Token != "777:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB" {
		t.Fatal("Forget must drop the cached client")
	}

	st.RemoveBot(ctx, id)
	r.Forget(id)
	if _, err := r.Get(ctx, id); err == nil {
		t.Fatal("removed bot must not yield a client")
	}
}
