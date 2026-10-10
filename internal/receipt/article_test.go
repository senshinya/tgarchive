package receipt

import (
	"encoding/json"
	"reflect"
	"testing"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

// link ingests a Telegraph link message (tg message 10) with a queued job.
func (v *env) link(t *testing.T) int64 {
	t.Helper()
	m := &model.Message{TgMessageID: 10, Source: model.SourceBotUpdate, Date: 10, Kind: model.KindText, Text: "https://telegra.ph/A",
		RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}
	res, err := v.st.Ingest(ctx, store.IngestInput{BotID: v.bot, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: 1, TelegraphPath: "A"})
	if err != nil || !res.TelegraphQueued {
		t.Fatalf("ingest = %+v, %v", res, err)
	}
	return res.MessageID
}

// fetch claims the job and saves an article referencing urls; it returns their media ids.
func (v *env) fetch(t *testing.T, msgID int64, urls ...string) []int64 {
	t.Helper()
	j, err := v.st.ClaimNextTelegraphJob(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	var media []store.ArticleMediaInput
	for _, u := range urls {
		media = append(media, store.ArticleMediaInput{DedupeKey: "web:" + u, URL: u, Kind: "photo"})
	}
	if err := v.st.SaveArticle(ctx, store.SaveArticleInput{JobID: j.ID, MessageID: msgID, Path: "A", URL: "https://telegra.ph/A", Title: "A",
		Media: media, Render: func(map[string]int64) (string, error) { return "[]", nil }, Now: 3}); err != nil {
		t.Fatal(err)
	}
	a, _ := v.st.GetArticle(ctx, msgID)
	var ids []int64
	for _, m := range a.Media {
		ids = append(ids, m.ID)
	}
	return ids
}

func TestArticleSeenUntilMediaSettle(t *testing.T) {
	v := newEnv(t)
	id := v.link(t)
	v.e.Evaluate(ctx, id)
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, []string{"react 42 10 👀"}) {
		t.Fatalf("queued calls = %v", got)
	}
	mids := v.fetch(t, id, "https://x/1.jpg", "https://x/2.jpg")
	v.e.Evaluate(ctx, id)
	if got := v.tr.(*rec).take(); len(got) != 0 {
		t.Fatalf("fetched with pending media: calls = %v", got)
	}
	v.st.MarkMediaDone(ctx, mids[0], v.key(t, mids[0]), "web/a.jpg", 1)
	v.e.MediaSettled(ctx, mids[0])
	if got := v.tr.(*rec).take(); len(got) != 0 {
		t.Fatalf("one media still pending: calls = %v", got)
	}
	v.st.MarkMediaFailed(ctx, mids[1], v.key(t, mids[1]), 1, "地址不允许")
	v.e.MediaSettled(ctx, mids[1])
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, []string{"react 42 10 👌"}) {
		t.Fatalf("settled calls = %v (a failed article image must not block 👌 or reply)", got)
	}
}

func TestArticleWithoutMediaIsDoneOnFetch(t *testing.T) {
	v := newEnv(t)
	id := v.link(t)
	v.e.Evaluate(ctx, id)
	v.fetch(t, id)
	v.e.Evaluate(ctx, id)
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, []string{"react 42 10 👀", "react 42 10 👌"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestArticleFailureRepliesOnce(t *testing.T) {
	v := newEnv(t)
	id := v.link(t)
	v.e.Evaluate(ctx, id)
	j, _ := v.st.ClaimNextTelegraphJob(ctx, 2)
	v.st.FinishTelegraphJob(ctx, j.ID, store.TelegraphFailed, "文章不存在", 3)
	v.e.Evaluate(ctx, id)
	v.e.Evaluate(ctx, id)
	want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 存档失败：文章不存在"}
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestArticleFailureWithoutPriorReaction(t *testing.T) {
	v := newEnv(t)
	id := v.link(t)
	j, _ := v.st.ClaimNextTelegraphJob(ctx, 2)
	v.st.FinishTelegraphJob(ctx, j.ID, store.TelegraphFailed, "网络错误", 3)
	v.e.Evaluate(ctx, id)
	want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 存档失败：网络错误"}
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}
