// Package app wires all components together.
package app

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tgarchive/internal/avatars"
	"tgarchive/internal/botapifs"
	"tgarchive/internal/botapiserver"
	"tgarchive/internal/botclients"
	"tgarchive/internal/collector"
	"tgarchive/internal/config"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/httpapi"
	"tgarchive/internal/mp4fix"
	"tgarchive/internal/notify"
	"tgarchive/internal/receipt"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/telegraph"
	"tgarchive/internal/tgapp"
	"tgarchive/internal/userbot"
	"tgarchive/web"
)

type App struct {
	Handler http.Handler

	ctx     context.Context
	cancel  context.CancelFunc
	st      *store.Store
	mgr     *collector.Manager
	dl      *downloader.Downloader
	av      *avatars.Refresher
	mapper  botapifs.Mapper
	tg      *tgapp.Store
	sup     *botapiserver.Supervisor
	ub      *userbot.Service
	fetcher *userbot.Fetcher
	tw      *telegraph.Worker
	media   string
	wg      sync.WaitGroup
}

func New(parent context.Context, cfg *config.Config) (*App, error) {
	return newApp(parent, cfg, userbot.GotdDialer{}, nil)
}

// newApp takes the userbot dialer and the web media dial check (nil = downloader.PublicIP) so
// tests can run against in-process fakes; production always uses New.
func newApp(parent context.Context, cfg *config.Config, dialer userbot.Dialer, webDialCheck func(net.IP) error) (*App, error) {
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
	tg := tgapp.New(st, box)
	notifier := &notify.Bark{File: cfg.BarkNotifyFile}
	rc := receipt.New(st, clients)
	dl := downloader.New(st, mediaDir, cfg.MediaMaxBytes, func(mediaID int64) {
		rc.MediaSettled(ctx, mediaID)
		ids, _ := st.MessagesForMedia(ctx, mediaID)
		hub.Publish(events.Event{Type: "media.updated", Data: map[string]any{"media_id": mediaID, "message_ids": ids}})
	})
	dl.Register("bot", &downloader.BotSource{Clients: clients.Get, Mapper: mapper})
	ub := userbot.New(st, box, tg, dialer, notifier)
	dl.Register("mt", &userbot.MTSource{API: ub})
	dl.Register("web", downloader.NewWebSource(cfg.MediaMaxBytes, webDialCheck))
	fetcher := userbot.NewFetcher(ub, st, rc, clients, hub, dl.Wake, mediaDir)
	tw := telegraph.NewWorker(st, telegraph.NewClient(cfg.TelegraphAPIURL), rc, hub, dl.Wake)
	av := &avatars.Refresher{Store: st, Clients: clients, Mapper: mapper, Dir: avatarDir}
	mgr := collector.New(ctx, collector.Deps{
		Store: st, Clients: clients, Downloader: dl, Receipts: rc, Hub: hub,
		Notifier: notifier, Avatars: av, Links: fetcher, Telegraph: tw,
		MediaDir: mediaDir, PollTimeoutSec: cfg.PollTimeoutSec,
	})
	var sup *botapiserver.Supervisor
	if cfg.ManageBotAPI {
		sup = botapiserver.New(cfg.BotAPIBinary, cfg.BotAPIDirLocal, filepath.Join(cfg.DataDir, "botapi-tmp"), 8081)
	}
	srv := &httpapi.Server{
		Cfg: cfg, Store: st, Box: box, Clients: clients, Manager: mgr, Downloader: dl, Hub: hub, Avatars: av,
		TgApp: tg, BotAPI: sup, Userbot: ub,
		Web: web.FS(), MediaDir: mediaDir, AvatarDir: avatarDir, HTTP: hc, Now: time.Now,
	}
	return &App{Handler: srv.Handler(), ctx: ctx, cancel: cancel, st: st, mgr: mgr, dl: dl, av: av, mapper: mapper, tg: tg, sup: sup,
		ub: ub, fetcher: fetcher, tw: tw, media: mediaDir}, nil
}

func (a *App) Start() error {
	a.wg.Add(2)
	go func() { defer a.wg.Done(); a.dl.Run(a.ctx) }()
	go func() { defer a.wg.Done(); a.maintain() }()
	a.wg.Add(3)
	go func() { defer a.wg.Done(); a.ub.Run(a.ctx) }()
	go func() { defer a.wg.Done(); a.fetcher.Run(a.ctx) }()
	go func() { defer a.wg.Done(); a.tw.Run(a.ctx) }()
	if a.sup != nil {
		creds, err := a.tg.Load(a.ctx)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		a.wg.Add(1)
		go func() { defer a.wg.Done(); a.sup.Run(a.ctx, creds) }()
	}
	return a.mgr.StartAll(a.ctx)
}

func (a *App) Close() {
	a.cancel()
	a.mgr.StopAll()
	a.wg.Wait()
	a.st.Close()
}

// fixHEVCTags relabels already-archived hev1 videos as hvc1 so Safari / iOS can play them
// (new downloads are fixed by the downloader). Idempotent: it only reads each movie's metadata
// once a file has been fixed.
func (a *App) fixHEVCTags() {
	paths, err := a.st.DoneMediaPaths(a.ctx)
	if err != nil {
		log.Printf("maintenance: list media for hevc tag fix: %v", err)
		return
	}
	n := 0
	for _, rel := range paths {
		if a.ctx.Err() != nil {
			return
		}
		if !mp4fix.Candidate(rel) {
			continue
		}
		changed, err := mp4fix.HEV1ToHVC1(filepath.Join(a.media, rel))
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.Printf("maintenance: hevc tag fix %s: %v", rel, err)
			}
			continue
		}
		if changed {
			n++
		}
	}
	if n > 0 {
		log.Printf("maintenance: relabelled %d hev1 videos as hvc1", n)
	}
}

func (a *App) maintain() {
	a.fixHEVCTags()
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
