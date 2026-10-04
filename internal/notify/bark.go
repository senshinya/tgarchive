// Package notify pushes operational alerts to the self-hosted Bark server.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

// userAgent replaces Go's default UA, which Cloudflare's Browser Integrity Check may reject (error 1010).
const userAgent = "tgarchive-notify/1.0"

type Notifier interface {
	Notify(ctx context.Context, title, body string)
}

// Bark reads its endpoint and device keys from File on every call, so key rotation needs no restart.
type Bark struct {
	File string
	HC   *http.Client
}

func (b *Bark) Notify(ctx context.Context, title, body string) {
	if b.File == "" {
		return
	}
	raw, err := os.ReadFile(b.File)
	if err != nil {
		log.Printf("notify: read %s: %v", b.File, err)
		return
	}
	var cfg struct {
		Endpoint   string   `json:"endpoint"`
		DeviceKeys []string `json:"device_keys"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.Endpoint == "" || len(cfg.DeviceKeys) == 0 {
		log.Printf("notify: invalid %s", b.File)
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"title": title, "body": body, "group": "docker", "level": "timeSensitive", "device_keys": cfg.DeviceKeys,
	})
	hc := b.HC
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(payload))
	if err != nil {
		log.Printf("notify: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := hc.Do(req)
	if err != nil {
		log.Printf("notify: push failed: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("notify: push returned http %d", resp.StatusCode)
	}
}
