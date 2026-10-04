// Package avatars keeps local copies of bot and sender profile photos.
package avatars

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"tgarchive/internal/botapifs"
	"tgarchive/internal/botclients"
	"tgarchive/internal/store"
)

type Refresher struct {
	Store   *store.Store
	Clients *botclients.Registry
	Mapper  botapifs.Mapper
	Dir     string
}

func (r *Refresher) RefreshBot(ctx context.Context, botID int64) {
	b, err := r.Store.GetBot(ctx, botID)
	if err != nil {
		log.Printf("avatars: bot %d: %v", botID, err)
		return
	}
	rel := filepath.Join("bots", fmt.Sprintf("%d.jpg", b.TgBotID))
	if r.fetch(ctx, botID, b.TgBotID, rel) {
		if err := r.Store.SetBotAvatar(ctx, botID, rel); err != nil {
			log.Printf("avatars: save bot %d avatar: %v", botID, err)
		}
	}
}

func (r *Refresher) RefreshSender(ctx context.Context, botID, userID int64) {
	rel := filepath.Join("senders", fmt.Sprintf("%d.jpg", userID))
	if r.fetch(ctx, botID, userID, rel) {
		if err := r.Store.SetSenderAvatar(ctx, userID, rel); err != nil {
			log.Printf("avatars: save sender %d avatar: %v", userID, err)
		}
	}
}

func (r *Refresher) RefreshAll(ctx context.Context) {
	bots, err := r.Store.ListBots(ctx)
	if err != nil {
		log.Printf("avatars: list bots: %v", err)
		return
	}
	for _, b := range bots {
		if b.Enabled && b.Status != store.StatusRemoved {
			r.RefreshBot(ctx, b.ID)
		}
	}
	senders, err := r.Store.ChatSenders(ctx)
	if err != nil {
		log.Printf("avatars: list senders: %v", err)
		return
	}
	for _, cs := range senders {
		r.RefreshSender(ctx, cs.BotID, cs.TgUserID)
	}
}

func (r *Refresher) fetch(ctx context.Context, botID, userID int64, rel string) bool {
	cl, err := r.Clients.Get(ctx, botID)
	if err != nil {
		log.Printf("avatars: client for bot %d: %v", botID, err)
		return false
	}
	ph, err := cl.GetUserProfilePhotos(ctx, userID, 1)
	if err != nil {
		log.Printf("avatars: profile photos of %d: %v", userID, cl.Redact(err))
		return false
	}
	if len(ph.Photos) == 0 || len(ph.Photos[0]) == 0 {
		return false
	}
	pick := ph.Photos[0][0]
	for _, s := range ph.Photos[0] {
		if s.Width <= 640 && s.Width >= pick.Width {
			pick = s
		}
	}
	f, err := cl.GetFile(ctx, pick.FileID)
	if err != nil {
		log.Printf("avatars: getFile for %d: %v", userID, cl.Redact(err))
		return false
	}
	local, err := r.Mapper.Map(f.FilePath)
	if err != nil {
		log.Printf("avatars: %s", botapifs.RedactPath(cl.Redact(err).Error()))
		return false
	}
	dst := filepath.Join(r.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		log.Printf("avatars: %s", botapifs.RedactPath(err.Error()))
		return false
	}
	if err := botapifs.LinkOrCopy(local, dst); err != nil {
		log.Printf("avatars: store %s: %s", rel, botapifs.RedactPath(cl.Redact(err).Error()))
		return false
	}
	_ = os.Remove(local)
	return true
}
