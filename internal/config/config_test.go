package config

import (
	"strings"
	"testing"
)

var validKey = strings.Repeat("ab", 32)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(map[string]string{"TOKEN_ENC_KEY": validKey}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.DataDir != "/data" || c.BotAPIURL != "http://127.0.0.1:8081" || c.CloudAPIURL != "https://api.telegram.org" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.BotAPIDirRemote != "/data/botapi" || c.BotAPIDirLocal != "/data/botapi" {
		t.Fatalf("unexpected dirs: %+v", c)
	}
	if !c.RequireForwardAuth {
		t.Fatal("RequireForwardAuth must default to true")
	}
	if c.MediaMaxBytes != 0 || c.PollTimeoutSec != 50 || len(c.TokenEncKey) != 32 {
		t.Fatalf("unexpected values: %+v", c)
	}
	if !c.ManageBotAPI || c.BotAPIBinary != "telegram-bot-api" {
		t.Fatalf("unexpected bot api management defaults: %+v", c)
	}
}

func TestLoadOverrides(t *testing.T) {
	c, err := Load(env(map[string]string{
		"TOKEN_ENC_KEY":        validKey,
		"REQUIRE_FORWARD_AUTH": "false",
		"MEDIA_MAX_BYTES":      "1048576",
		"DATA_DIR":             "/srv",
		"BARK_NOTIFY_FILE":     "/run/bark/notify.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.RequireForwardAuth || c.MediaMaxBytes != 1048576 || c.BotAPIDirLocal != "/srv/botapi" || c.BarkNotifyFile != "/run/bark/notify.json" {
		t.Fatalf("overrides not applied: %+v", c)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]map[string]string{
		"missing key":        {},
		"short key":          {"TOKEN_ENC_KEY": "abcd"},
		"non-hex key":        {"TOKEN_ENC_KEY": strings.Repeat("zz", 32)},
		"bad forward auth":   {"TOKEN_ENC_KEY": validKey, "REQUIRE_FORWARD_AUTH": "yes"},
		"bad managed":        {"TOKEN_ENC_KEY": validKey, "BOT_API_MANAGED": "yes"},
		"quoted max bytes":   {"TOKEN_ENC_KEY": validKey, "MEDIA_MAX_BYTES": `"100"`},
		"negative max bytes": {"TOKEN_ENC_KEY": validKey, "MEDIA_MAX_BYTES": "-1"},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(m)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
