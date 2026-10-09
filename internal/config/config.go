package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
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
	AllowedHosts       []string // 除 IP 字面量与 localhost 外可用的 Host（小写、无端口）；["*"] 不检查
	MediaMaxBytes      int64
	BarkNotifyFile     string
	PollTimeoutSec     int
	TelegraphAPIURL    string // Telegraph API; only tests point it elsewhere
	Transcode          bool   // 为浏览器播不了的视频生成 H.264 兼容版
	VAAPIDevice        string // 硬件编码用的 render 节点；不存在时用软件编码
}

func Load(getenv func(string) string) (*Config, error) {
	c := &Config{
		Listen:          or(getenv("LISTEN"), ":8080"),
		DataDir:         or(getenv("DATA_DIR"), "/data"),
		BotAPIURL:       or(getenv("BOT_API_URL"), "http://127.0.0.1:8081"),
		CloudAPIURL:     or(getenv("CLOUD_API_URL"), "https://api.telegram.org"),
		BarkNotifyFile:  getenv("BARK_NOTIFY_FILE"),
		PollTimeoutSec:  50,
		TelegraphAPIURL: or(getenv("TELEGRAPH_API_URL"), "https://api.telegra.ph"),
		VAAPIDevice:     or(getenv("TRANSCODE_VAAPI_DEVICE"), "/dev/dri/renderD128"),
	}
	switch v := getenv("TRANSCODE"); v {
	case "", "true":
		c.Transcode = true
	case "false":
		c.Transcode = false
	default:
		return nil, fmt.Errorf("TRANSCODE must be true or false, got %q", v)
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

	hosts, err := parseHosts(getenv("ALLOWED_HOSTS"))
	if err != nil {
		return nil, err
	}
	c.AllowedHosts = hosts

	if v := getenv("MEDIA_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("MEDIA_MAX_BYTES must be a non-negative integer, got %q", v)
		}
		c.MediaMaxBytes = n
	}
	return c, nil
}

// parseHosts reads ALLOWED_HOSTS: comma-separated host names without scheme or port, or "*" alone.
func parseHosts(v string) ([]string, error) {
	var out []string
	for _, h := range strings.Split(v, ",") {
		h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
		if h == "" {
			continue
		}
		if h != "*" && strings.ContainsAny(h, ":/*[] ") {
			return nil, fmt.Errorf("ALLOWED_HOSTS takes host names without scheme or port, got %q", h)
		}
		out = append(out, h)
	}
	if len(out) > 1 && slices.Contains(out, "*") {
		return nil, errors.New(`ALLOWED_HOSTS is either "*" or a list of host names`)
	}
	return out, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
