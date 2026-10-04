// Package app wires all components together.
package app

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tgarchive/internal/avatars"
	"tgarchive/internal/botapifs"
	"tgarchive/internal/botclients"
	"tgarchive/internal/collector"
	"tgarchive/internal/config"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/httpapi"
	"tgarchive/internal/notify"
	"tgarchive/internal/receipt"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/web"
)

type App struct {
	Handler http.Handler

	ctx    context.Context
	cancel context.CancelFunc
	st     *store.Store
	mgr    *collector.Manager
	dl     *downloader.Downloader
	av     *avatars.Refresher
	mapper botapifs.Mapper
	wg     sync.WaitGroup
}

func New(parent context.Context, cfg *config.Config) (*App, error) {
	mediaDir := filepath.Join(cfg.DataDir, "media")
	avatarDir := filepath.Join(cfg.DataDir, "avatars")
	for _, d := range []string{mediaDir, avatarDir, filepath.Join(cfg.DataDir, "db")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "db", "tgarchive.db"))
	if err != nil {
		return nil, err
	}
	box, err := seal.New(cfg.TokenEncKey)
	if err != nil {
		st.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	hc := &http.Client{} // no global timeout: long polls and large getFile calls use per-call contexts
	clients := botclients.New(st, box, cfg.BotAPIURL, hc)
	mapper := botapifs.Mapper{Remote: cfg.BotAPIDirRemote, Local: cfg.BotAPIDirLocal}
	hub := events.NewHub()
	rc := receipt.New(st, clients)
	dl := downloader.New(st, mediaDir, cfg.MediaMaxBytes, func(mediaID int64) {
		rc.MediaSettled(ctx, mediaID)
		ids, _ := st.MessagesForMedia(ctx, mediaID)
		hub.Publish(events.Event{Type: "media.updated", Data: map[string]any{"media_id": mediaID, "message_ids": ids}})
	})
	dl.Register("bot", &downloader.BotSource{Clients: clients.Get, Mapper: mapper})
	av := &avatars.Refresher{Store: st, Clients: clients, Mapper: mapper, Dir: avatarDir}
	mgr := collector.New(ctx, collector.Deps{
		Store: st, Clients: clients, Downloader: dl, Receipts: rc, Hub: hub,
		Notifier: &notify.Bark{File: cfg.BarkNotifyFile}, Avatars: av,
		MediaDir: mediaDir, PollTimeoutSec: cfg.PollTimeoutSec,
	})
	srv := &httpapi.Server{
		Cfg: cfg, Store: st, Box: box, Clients: clients, Manager: mgr, Downloader: dl, Hub: hub, Avatars: av,
		Web: web.FS(), MediaDir: mediaDir, AvatarDir: avatarDir, HTTP: hc, Now: time.Now,
	}
	return &App{Handler: srv.Handler(), ctx: ctx, cancel: cancel, st: st, mgr: mgr, dl: dl, av: av, mapper: mapper}, nil
}

func (a *App) Start() error {
	a.wg.Add(2)
	go func() { defer a.wg.Done(); a.dl.Run(a.ctx) }()
	go func() { defer a.wg.Done(); a.maintain() }()
	return a.mgr.StartAll(a.ctx)
}

func (a *App) Close() {
	a.cancel()
	a.mgr.StopAll()
	a.wg.Wait()
	a.st.Close()
}

func (a *App) maintain() {
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-t.C:
		}
		if n, err := a.mapper.CleanOlderThan(24*time.Hour, time.Now()); err != nil {
			log.Printf("maintenance: clean bot api cache: %v", err)
		} else if n > 0 {
			log.Printf("maintenance: removed %d stale bot api cache files", n)
		}
		a.av.RefreshAll(a.ctx)
		t.Reset(24 * time.Hour)
	}
}
