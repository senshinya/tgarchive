package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"tgarchive/internal/model"
)

func newJob(bot int64, linkMsg int64) *FetchJob {
	return &FetchJob{BotID: bot, SenderID: 42, LinkTgMessageID: linkMsg, Link: "https://t.me/chan/1", State: JobQueued, CreatedAt: 1, UpdatedAt: 1}
}

func TestCreateFetchJobIsIdempotent(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	id1, created, err := s.CreateFetchJob(ctx, newJob(bot, 10))
	if err != nil || !created {
		t.Fatalf("first = %d %v %v", id1, created, err)
	}
	id2, created, err := s.CreateFetchJob(ctx, newJob(bot, 10))
	if err != nil || created || id2 != id1 {
		t.Fatalf("second = %d %v %v", id2, created, err)
	}
	j, err := s.GetFetchJob(ctx, id1)
	if err != nil || j.State != JobQueued || j.Receipt != ReceiptNone || j.Link != "https://t.me/chan/1" {
		t.Fatalf("job = %+v, %v", j, err)
	}
}

func TestClaimFinishAndRequeue(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	a, _, _ := s.CreateFetchJob(ctx, newJob(bot, 10))
	b, _, _ := s.CreateFetchJob(ctx, newJob(bot, 11))
	j, err := s.ClaimNextFetchJob(ctx, 5)
	if err != nil || j.ID != a || j.State != JobFetching {
		t.Fatalf("claim 1 = %+v, %v", j, err)
	}
	j, _ = s.ClaimNextFetchJob(ctx, 5)
	if j.ID != b {
		t.Fatalf("claim 2 = %+v", j)
	}
	if _, err := s.ClaimNextFetchJob(ctx, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim 3 err = %v", err)
	}
	if err := s.FinishFetchJob(ctx, a, JobFailed, "boom", 6); err != nil {
		t.Fatal(err)
	}
	ja, _ := s.GetFetchJob(ctx, a)
	if ja.State != JobFailed || ja.Error != "boom" || ja.UpdatedAt != 6 {
		t.Fatalf("finished = %+v", ja)
	}
}

func TestRequeueFetchingJobs(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	a, _, _ := s.CreateFetchJob(ctx, newJob(bot, 10))
	s.ClaimNextFetchJob(ctx, 5)
	n, err := s.RequeueFetchingJobs(ctx, 7)
	if err != nil || n != 1 {
		t.Fatalf("requeue = %d, %v", n, err)
	}
	j, err := s.ClaimNextFetchJob(ctx, 8)
	if err != nil || j.ID != a {
		t.Fatalf("reclaim = %+v, %v", j, err)
	}
}

func TestJobReceiptInfo(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	id, _, _ := s.CreateFetchJob(ctx, newJob(bot, 10))
	ingest := func(tgID int64, key string) int64 {
		m := &model.Message{TgMessageID: tgID, Source: model.SourceUserbotFetch, OriginChatID: -1000000000500, Date: 1, Kind: model.KindPhoto,
			RawFormat: model.RawMTProto, Raw: json.RawMessage(`{}`),
			Media: []model.Media{{DedupeKey: key, Kind: "photo", Role: model.RoleMain}, {DedupeKey: key + ":t", Kind: "photo", Role: model.RoleThumb}}}
		r, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: 100})
		if err != nil {
			t.Fatal(err)
		}
		return r.MessageID
	}
	m1, m2 := ingest(1, "mt:photo:1"), ingest(2, "mt:photo:2")
	for _, m := range []int64{m1, m2, m1} {
		if err := s.LinkFetchJobMessage(ctx, id, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.FinishFetchJob(ctx, id, JobFetched, "", 9); err != nil {
		t.Fatal(err)
	}
	info, err := s.GetJobReceiptInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	want := &JobReceiptInfo{JobID: id, BotID: bot, TgChatID: 42, TgMessageID: 10, State: JobFetched, Receipt: ReceiptNone,
		Main: []MediaStatus{{State: StatePending}, {State: StatePending}}}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("info = %+v", info)
	}
	if jobs, _ := s.FetchJobsForMessage(ctx, m2); !reflect.DeepEqual(jobs, []int64{id}) {
		t.Fatalf("jobs for message = %v", jobs)
	}
	if _, _, err := s.DeleteMessage(ctx, m2, 50); err != nil {
		t.Fatal(err)
	}
	info, _ = s.GetJobReceiptInfo(ctx, id)
	if len(info.Main) != 1 {
		t.Fatalf("deleted message still counted: %+v", info.Main)
	}
	if err := s.SetFetchJobReceipt(ctx, id, ReceiptDone); err != nil {
		t.Fatal(err)
	}
	if info, _ = s.GetJobReceiptInfo(ctx, id); info.Receipt != ReceiptDone {
		t.Fatalf("receipt = %q", info.Receipt)
	}
}

func TestFetchedMessageBumpsChatToNow(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	m := &model.Message{TgMessageID: 5, Source: model.SourceUserbotFetch, OriginChatID: -1000000000500, Date: 1000, Kind: model.KindText, Text: "old post",
		RawFormat: model.RawMTProto, Raw: json.RawMessage(`{}`)}
	if _, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: 5000}); err != nil {
		t.Fatal(err)
	}
	chats, err := s.ListChats(ctx, bot)
	if err != nil || len(chats) != 1 || chats[0].LastMessageAt != 5000 {
		t.Fatalf("chats = %+v, %v", chats, err)
	}
}
