package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tgarchive/internal/app"
	"tgarchive/internal/config"
)

func main() {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, cfg)
	if err != nil {
		log.Fatalf("init: %v", err)
	}
	if err := a.Start(); err != nil {
		log.Fatalf("start: %v", err)
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx }, // ends SSE streams on shutdown
	}
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		stop() // a second signal now hits the default handler and force-exits
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		close(shutdownDone)
	}()
	log.Printf("tgarchive listening on %s", cfg.Listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-shutdownDone // let in-flight handlers finish draining before tearing down the app
	a.Close()
}
