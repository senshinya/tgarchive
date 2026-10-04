package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
)

type Config struct {
	Listen             string
	DataDir            string
	BotAPIURL          string // 本地 Bot API 服务器
	CloudAPIURL        string // 官方云端，仅用于 logOut
	BotAPIDirRemote    string // getFile 返回路径的前缀（Bot API 容器内）
	BotAPIDirLocal     string // 同一目录在本容器内的路径
	ManageBotAPI       bool   // 本进程是否托管 telegram-bot-api 子进程
	BotAPIBinary       string // telegram-bot-api 可执行文件路径/名称
	TokenEncKey        []byte
	RequireForwardAuth bool
	MediaMaxBytes      int64
	BarkNotifyFile     string
	PollTimeoutSec     int
}

func Load(getenv func(string) string) (*Config, error) {
	c := &Config{
		Listen:         or(getenv("LISTEN"), ":8080"),
		DataDir:        or(getenv("DATA_DIR"), "/data"),
		BotAPIURL:      or(getenv("BOT_API_URL"), "http://127.0.0.1:8081"),
		CloudAPIURL:    or(getenv("CLOUD_API_URL"), "https://api.telegram.org"),
		BarkNotifyFile: getenv("BARK_NOTIFY_FILE"),
		PollTimeoutSec: 50,
	}
	c.BotAPIDirLocal = or(getenv("BOT_API_DIR_LOCAL"), c.DataDir+"/botapi")
	c.BotAPIDirRemote = or(getenv("BOT_API_DIR_REMOTE"), c.BotAPIDirLocal)
	c.BotAPIBinary = or(getenv("BOT_API_BINARY"), "telegram-bot-api")
	switch v := getenv("BOT_API_MANAGED"); v {
	case "", "true":
		c.ManageBotAPI = true
	case "false":
		c.ManageBotAPI = false
	default:
		return nil, fmt.Errorf("BOT_API_MANAGED must be true or false, got %q", v)
	}

	keyHex := getenv("TOKEN_ENC_KEY")
	if keyHex == "" {
		return nil, errors.New("TOKEN_ENC_KEY is required")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return nil, errors.New("TOKEN_ENC_KEY must be 64 hex characters (32 bytes)")
	}
	c.TokenEncKey = key

	switch v := getenv("REQUIRE_FORWARD_AUTH"); v {
	case "", "true":
		c.RequireForwardAuth = true
	case "false":
		c.RequireForwardAuth = false
	default:
		return nil, fmt.Errorf("REQUIRE_FORWARD_AUTH must be true or false, got %q", v)
	}

	if v := getenv("MEDIA_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("MEDIA_MAX_BYTES must be a non-negative integer, got %q", v)
		}
		c.MediaMaxBytes = n
	}
	return c, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
