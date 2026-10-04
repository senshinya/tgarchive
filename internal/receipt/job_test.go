package receipt

import (
	"encoding/json"
	"reflect"
	"testing"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

func (v *env) job(t *testing.T, linkMsg int64) int64 {
	t.Helper()
	id, _, err := v.st.CreateFetchJob(ctx, &store.FetchJob{BotID: v.bot, SenderID: 42, LinkTgMessageID: linkMsg, Link: "l", State: store.JobQueued, CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (v *env) fetched(t *testing.T, job, tgID int64, keys ...string) (int64, []int64) {
	t.Helper()
	m := &model.Message{TgMessageID: tgID, Source: model.SourceUserbotFetch, OriginChatID: -1000000000500, Date: 1, Kind: model.KindText, Text: "t",
		RawFormat: model.RawMTProto, Raw: json.RawMessage(`{}`)}
	for _, k := range keys {
		m.Kind = model.KindPhoto
		m.Media = append(m.Media, model.Media{DedupeKey: k, Kind: "photo", Role: model.RoleMain})
	}
	res, err := v.st.Ingest(ctx, store.IngestInput{BotID: v.bot, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.st.LinkFetchJobMessage(ctx, job, res.MessageID); err != nil {
		t.Fatal(err)
	}
	var mids []int64
	due, _ := v.st.DueMedia(ctx, 0, 100)
	for _, d := range due {
		mids = append(mids, d.ID)
	}
	return res.MessageID, mids
}

func TestJobSeenThenDone(t *testing.T) {
	v := newEnv(t)
	job := v.job(t, 10)
	v.e.EvaluateJob(ctx, job)
	v.e.EvaluateJob(ctx, job)
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, []string{"react 42 10 👀"}) {
		t.Fatalf("queued = %v", got)
	}
	_, mids := v.fetched(t, job, 500, "mt:photo:1")
	v.st.FinishFetchJob(ctx, job, store.JobFetched, "", 2)
	v.e.EvaluateJob(ctx, job)
	if got := v.tr.(*rec).take(); len(got) != 0 {
		t.Fatalf("fetched with pending media = %v", got)
	}
	v.st.MarkMediaDone(ctx, mids[0], "p", 1)
	v.e.MediaSettled(ctx, mids[0])
	v.e.MediaSettled(ctx, mids[0])
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, []string{"react 42 10 👌"}) {
		t.Fatalf("done = %v", got)
	}
}

func TestJobTextOnlyGoesDone(t *testing.T) {
	v := newEnv(t)
	job := v.job(t, 10)
	v.fetched(t, job, 500)
	v.st.FinishFetchJob(ctx, job, store.JobFetched, "", 2)
	v.e.EvaluateJob(ctx, job)
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, []string{"react 42 10 👌"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestJobFailedRepliesOnce(t *testing.T) {
	v := newEnv(t)
	job := v.job(t, 10)
	v.st.FinishFetchJob(ctx, job, store.JobFailed, "私有群/频道，代取账号未加入", 2)
	v.e.EvaluateJob(ctx, job)
	v.e.EvaluateJob(ctx, job)
	want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 代取失败：私有群/频道，代取账号未加入"}
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v", got)
	}
}

func TestJobMediaFailureUsesArchiveText(t *testing.T) {
	v := newEnv(t)
	job := v.job(t, 10)
	v.e.EvaluateJob(ctx, job)
	_, mids := v.fetched(t, job, 500, "mt:photo:1")
	v.st.FinishFetchJob(ctx, job, store.JobFetched, "", 2)
	v.st.MarkMediaFailed(ctx, mids[0], 4, "network down")
	v.e.MediaSettled(ctx, mids[0])
	want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 存档失败：network down"}
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v", got)
	}
}

func TestUnsupportedJobIgnored(t *testing.T) {
	v := newEnv(t)
	id, _, _ := v.st.CreateFetchJob(ctx, &store.FetchJob{BotID: v.bot, SenderID: 42, LinkTgMessageID: 11, Link: "l", State: store.JobUnsupported, CreatedAt: 1, UpdatedAt: 1})
	v.e.EvaluateJob(ctx, id)
	if got := v.tr.(*rec).take(); len(got) != 0 {
		t.Fatalf("calls = %v", got)
	}
}
