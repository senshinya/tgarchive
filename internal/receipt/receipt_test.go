package receipt

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

var ctx = context.Background()

type rec struct {
	mu    sync.Mutex
	calls []string
}

func (r *rec) SetReaction(_ context.Context, botID, chatID, msgID int64, emoji string) error {
	r.add(fmt.Sprintf("react %d %d %s", chatID, msgID, emoji))
	return nil
}

func (r *rec) Reply(_ context.Context, botID, chatID, msgID int64, text string) error {
	r.add(fmt.Sprintf("reply %d %d %s", chatID, msgID, text))
	return nil
}

func (r *rec) add(s string) { r.mu.Lock(); r.calls = append(r.calls, s); r.mu.Unlock() }

func (r *rec) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.calls
	r.calls = nil
	return out
}

type env struct {
	st  *store.Store
	bot int64
	tr  *rec
	e   *Engine
}

func newEnv(t *testing.T) *env {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bot, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	tr := &rec{}
	return &env{st: st, bot: bot, tr: tr, e: New(st, tr)}
}

func (v *env) ingest(t *testing.T, id int64, source string, keys ...string) (int64, []int64) {
	m := &model.Message{TgMessageID: id, Source: source, Date: id, Kind: model.KindText, Text: "t", RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}
	for _, k := range keys {
		m.Kind = model.KindPhoto
		m.Media = append(m.Media, model.Media{DedupeKey: k, Kind: "photo", Role: model.RoleMain})
	}
	res, err := v.st.Ingest(ctx, store.IngestInput{BotID: v.bot, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: 1})
	if err != nil {
		t.Fatal(err)
	}
	var mids []int64
	for range keys {
		due, _ := v.st.DueMedia(ctx, 0, 100)
		for _, d := range due {
			mids = append(mids, d.ID)
		}
		break
	}
	return res.MessageID, mids
}

func TestTextGoesStraightToDone(t *testing.T) {
	v := newEnv(t)
	id, _ := v.ingest(t, 10, model.SourceBotUpdate)
	v.e.Evaluate(ctx, id)
	v.e.Evaluate(ctx, id)
	if got := v.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👌"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestPendingThenDone(t *testing.T) {
	v := newEnv(t)
	id, mids := v.ingest(t, 10, model.SourceBotUpdate, "bot:a")
	v.e.Evaluate(ctx, id)
	v.e.Evaluate(ctx, id)
	if got := v.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👀"}) {
		t.Fatalf("pending calls = %v", got)
	}
	v.st.MarkMediaDone(ctx, mids[0], "p", 1)
	v.e.MediaSettled(ctx, mids[0])
	if got := v.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👌"}) {
		t.Fatalf("done calls = %v", got)
	}
}

func TestFailureRepliesOnceThenRecovers(t *testing.T) {
	v := newEnv(t)
	id, mids := v.ingest(t, 10, model.SourceBotUpdate, "bot:a")
	v.st.MarkMediaFailed(ctx, mids[0], 4, strings.Repeat("x", 300))
	v.e.MediaSettled(ctx, mids[0])
	v.e.Evaluate(ctx, id)
	got := v.tr.take()
	if len(got) != 2 || got[0] != "react 42 10 👀" || !strings.HasPrefix(got[1], "reply 42 10 ⚠️ 存档失败：") || len([]rune(got[1])) > 230 {
		t.Fatalf("failure calls = %v", got)
	}
	v.st.ResetMedia(ctx, mids[0])
	v.st.MarkMediaDone(ctx, mids[0], "p", 1)
	v.e.MediaSettled(ctx, mids[0])
	if got := v.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👌"}) {
		t.Fatalf("recovery calls = %v", got)
	}
}

func TestTooLargeRepliesAndCompletes(t *testing.T) {
	v := newEnv(t)
	_, mids := v.ingest(t, 10, model.SourceBotUpdate, "bot:a")
	v.st.MarkMediaTooLarge(ctx, mids[0])
	v.e.MediaSettled(ctx, mids[0])
	if got := v.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👌", "reply 42 10 文件超过存档上限，仅保存了消息记录"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestUserbotMessagesIgnored(t *testing.T) {
	v := newEnv(t)
	id, _ := v.ingest(t, 10, model.SourceUserbotFetch)
	v.e.Evaluate(ctx, id)
	if got := v.tr.take(); len(got) != 0 {
		t.Fatalf("calls = %v", got)
	}
}
