package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
)

func TestVideoCoverUnavailableDoesNotPreventStartup(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	a, err := New(context.Background(), cfgFor(tgtest.New(t), t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.vc != nil {
		t.Fatal("cover worker should be disabled without ffmpeg")
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
}

func TestVideoCoverStartupSweepAndLifecycle(t *testing.T) {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	a, err := New(context.Background(), cfgFor(tgtest.New(t), t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.vc == nil {
		t.Fatal("cover worker not connected")
	}
	bot, err := a.st.UpsertBot(context.Background(), &store.Bot{TgBotID: 777, TokenEnc: []byte("unused"), CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	a.st.SetBotEnabled(context.Background(), bot, false)
	m := &model.Message{TgMessageID: 1, Source: model.SourceBotUpdate, Date: 1, Kind: model.KindVideo, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`),
		Media: []model.Media{{DedupeKey: "bot:startup-cover", Kind: "video", Mime: "video/mp4", Role: model.RoleMain}}}
	res, err := a.st.Ingest(context.Background(), store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: 1})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := a.st.GetMessageView(context.Background(), res.MessageID)
	id := v.Media[0].ID
	os.MkdirAll(filepath.Join(a.media, "1"), 0755)
	cmd := exec.Command(binary, "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=320x180:d=1", "-c:v", "mpeg4", "-threads", "1", filepath.Join(a.media, "1/video.mp4"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", output, err)
	}
	a.st.MarkMediaDone(context.Background(), id, "bot:startup-cover", "1/video.mp4", 100)
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		v, err := a.st.GetMessageView(context.Background(), res.MessageID)
		if err != nil {
			t.Fatal(err)
		}
		for _, md := range v.Media {
			if md.Role == "thumb" && md.State == store.StateDone {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("startup sweep did not install a video thumbnail")
}
