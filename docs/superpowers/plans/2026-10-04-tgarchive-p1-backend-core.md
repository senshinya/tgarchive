# tgarchive 计划 1：后端核心 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 tgarchive 的 Go 后端核心：多机器人 long polling 采集、白名单、SQLite 存档、媒体下载与去重、reaction 回执、Bark 告警、只读/管理 HTTP API、SSE，做完即可用 curl 完整验证。

**Architecture:** 单个 Go 二进制。`collector` 为每个机器人跑一个 worker，经本地 Bot API 服务器 long polling；update 转换为统一 `model.Message` 后在一个事务里入库并推进 offset；`downloader` 全局队列按 `dedupe_key` 前缀分派给 `Source`（本计划只有 `bot:`），从共享目录硬链接/拷贝到归档目录；`receipt` 根据媒体状态设 👀/👌 或回复失败原因；`httpapi` 提供 JSON API、媒体 Range 服务、SSE 和 embed 的 SPA 占位页。

**Tech Stack:** Go 1.26（stdlib `net/http` 路由）、`modernc.org/sqlite` v1.60.1（纯 Go，`CGO_ENABLED=0`）。Telegram Bot API 客户端自写（7 个方法），不引入 `go-telegram/bot`。

**Spec:** `docs/superpowers/specs/2026-10-04-tgarchive-design.md`

**本计划不含**（后续计划）：userbot / linkparse / convert/mtproto（计划 2）、Preact 前端（计划 3，本计划只放占位 `web/dist/index.html`）、Dockerfile / compose / Caddy / DNS / 基础设施文档（计划 4）。

## Global Constraints

- Go module 名 `tgarchive`；`go 1.26`；除 `modernc.org/sqlite v1.60.1` 外只用标准库
- 时间一律存 Unix 秒（UTC）
- `TOKEN_ENC_KEY`：64 位 hex（32 字节），缺失或格式错误启动 fail-fast
- `REQUIRE_FORWARD_AUTH` 默认 `true`；为 true 时除 `/healthz` 外所有请求必须带 `Remote-User` 头，否则 401
- `MEDIA_MAX_BYTES` 默认 0 = 不限；非纯数字或负数 fail-fast
- bot token 绝不出现在日志、错误信息、API 响应中
- 媒体路径：`<DATA_DIR>/media/<bot_id>/<yyyy>/<mm>/<sha1(dedupe_key) hex>.<ext>`，库内存相对 `media/` 的路径
- 头像路径：`<DATA_DIR>/avatars/{bots,senders}/<tg_id>.jpg`
- 数据库：`<DATA_DIR>/db/tgarchive.db`，WAL，`foreign_keys=1`，`MaxOpenConns(1)`（**持有 `*sql.Rows` 时不得再发起查询；事务内只能用 `tx`**，否则单连接死锁）
- 媒体下载全局并发 4；失败重试间隔 1min / 5min / 30min，第 4 次失败置 `failed`
- 回执 emoji：收到 `👀`，完成 `👌`；失败回复文案 `⚠️ 存档失败：<原因>`；超限回复 `文件超过存档上限，仅保存了消息记录`
- 只处理 `chat.type == "private"` 的 `message` / `edited_message`
- 机器人 `getUpdates` 返回 401 或 409 → 状态 `error` + Bark，不自动重试；网络错误指数退避，上限 60s
- Bark：读 `BARK_NOTIFY_FILE`（JSON：`endpoint`、`device_keys`），POST `{title, body, group:"docker", level:"timeSensitive", device_keys}`；文件路径为空则不推送

## Review Focus

1. **本地 Bot API 缓存清理误删其状态文件**：`<botapi>/<bot_id:token>/td.binlog` 等顶层文件即使很旧也必须保留，只清理 `<botapi>/<bot>/<分类>/<文件>` 深度的文件 → Task 8 测试 `TestCleanOlderThanKeepsServerState`
2. **畸形或不支持的 update 卡死 worker**：无 `from` 的消息、群聊消息、无法解析的 JSON 必须推进 offset，后续消息照常处理 → Task 12 测试 `TestSkipsUnsupportedUpdates`
3. **同一文件被两条消息引用**：删除其中一条不能删文件，删除最后一条才删 → Task 6 测试 `TestDedupeAndOrphanCleanup`
4. **相册被分页游标切断**：一页最旧的消息属于相册时，同组更旧的消息必须一并返回 → Task 7 测试 `TestListMessagesKeepsAlbumWhole`
5. **进程在入库后、下载完成前重启**：重启后 pending 媒体继续下载、回执补发、消息不重复 → Task 16 测试 `TestRestartResumes`

---

## 文件结构

```
cmd/tgarchive/main.go            入口：读配置、信号、HTTP server
internal/config/                 环境变量解析
internal/seal/                   AES-256-GCM
internal/model/                  统一消息模型（Bot API 与 MTProto 共用）
internal/tgbot/                  Bot API 类型 + 客户端
internal/tgtest/                 测试用假 Bot API 服务器（仅测试 import）
internal/convert/botapi/         Bot API Message → model
internal/store/                  SQLite：迁移、bots、whitelist、ingest、media、查询视图
internal/botapifs/               本地 Bot API 目录路径映射、链接/拷贝、过期清理
internal/botclients/             按 bot 解密 token 并缓存客户端；实现回执 Transport
internal/downloader/             下载队列、重试状态机、BotSource
internal/receipt/                回执引擎
internal/events/                 SSE 事件 Hub
internal/notify/                 Bark
internal/collector/              worker 生命周期与 update 处理
internal/avatars/                头像刷新
internal/httpapi/                HTTP 路由、鉴权、媒体、SSE、管理接口
internal/app/                    依赖装配、维护循环、集成测试
web/                             embed 的前端产物（本计划为占位页）
```

---

### Task 1: 项目骨架与配置

**Files:**
- Create: `go.mod`, `.gitignore`, `internal/config/config.go`, `internal/config/config_test.go`, `web/web.go`, `web/dist/index.html`

**Interfaces:**
- Produces: `config.Config` 结构体（字段见下）；`config.Load(getenv func(string) string) (*config.Config, error)`；`web.FS() fs.FS`

- [ ] **Step 1: 初始化 module**

```bash
cd ~/Downloads/tgarchive
go mod init tgarchive
go get modernc.org/sqlite@v1.60.1
```

`.gitignore`：

```
/tmp/
/tgarchive
web/node_modules/
```

- [ ] **Step 2: 写失败测试** `internal/config/config_test.go`

```go
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
	if c.Listen != ":8080" || c.DataDir != "/data" || c.BotAPIURL != "http://tgarchive-botapi:8081" || c.CloudAPIURL != "https://api.telegram.org" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.BotAPIDirRemote != "/var/lib/telegram-bot-api" || c.BotAPIDirLocal != "/data/botapi" {
		t.Fatalf("unexpected dirs: %+v", c)
	}
	if !c.RequireForwardAuth {
		t.Fatal("RequireForwardAuth must default to true")
	}
	if c.MediaMaxBytes != 0 || c.PollTimeoutSec != 50 || len(c.TokenEncKey) != 32 {
		t.Fatalf("unexpected values: %+v", c)
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
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/config/`
Expected: FAIL（`undefined: Load`）

- [ ] **Step 4: 实现** `internal/config/config.go`

```go
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
	TokenEncKey        []byte
	RequireForwardAuth bool
	MediaMaxBytes      int64
	BarkNotifyFile     string
	PollTimeoutSec     int
}

func Load(getenv func(string) string) (*Config, error) {
	c := &Config{
		Listen:          or(getenv("LISTEN"), ":8080"),
		DataDir:         or(getenv("DATA_DIR"), "/data"),
		BotAPIURL:       or(getenv("BOT_API_URL"), "http://tgarchive-botapi:8081"),
		CloudAPIURL:     or(getenv("CLOUD_API_URL"), "https://api.telegram.org"),
		BotAPIDirRemote: or(getenv("BOT_API_DIR_REMOTE"), "/var/lib/telegram-bot-api"),
		BarkNotifyFile:  getenv("BARK_NOTIFY_FILE"),
		PollTimeoutSec:  50,
	}
	c.BotAPIDirLocal = or(getenv("BOT_API_DIR_LOCAL"), c.DataDir+"/botapi")

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
```

- [ ] **Step 5: 前端占位** `web/web.go` 与 `web/dist/index.html`

```go
// Package web embeds the built SPA. Plan 3 replaces dist/ with the Preact build output.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
```

```html
<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>tgarchive</title></head>
<body>tgarchive backend is running; UI arrives in plan 3.</body></html>
```

- [ ] **Step 6: 运行确认通过**

Run: `go test ./internal/config/ && go vet ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: project skeleton and config loading"
```

---

### Task 2: AES-256-GCM 封装

**Files:**
- Create: `internal/seal/seal.go`, `internal/seal/seal_test.go`

**Interfaces:**
- Produces: `seal.New(key []byte) (*seal.Box, error)`；`(*Box).Seal(plain []byte) []byte`（输出 `nonce || ciphertext`）；`(*Box).Open(data []byte) ([]byte, error)`

- [ ] **Step 1: 写失败测试**

```go
package seal

import (
	"bytes"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestRoundTrip(t *testing.T) {
	b, err := New(key(1))
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := b.Seal([]byte("123:secret")), b.Seal([]byte("123:secret"))
	if bytes.Equal(c1, c2) {
		t.Fatal("nonce must differ between seals")
	}
	got, err := b.Open(c1)
	if err != nil || string(got) != "123:secret" {
		t.Fatalf("open = %q, %v", got, err)
	}
}

func TestOpenRejectsTamperAndWrongKey(t *testing.T) {
	b, _ := New(key(1))
	other, _ := New(key(2))
	c := b.Seal([]byte("x"))
	c[len(c)-1] ^= 1
	if _, err := b.Open(c); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
	if _, err := other.Open(b.Seal([]byte("x"))); err == nil {
		t.Fatal("wrong key must fail")
	}
	if _, err := b.Open([]byte{1, 2}); err == nil {
		t.Fatal("short input must fail")
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/seal/`
Expected: FAIL（`undefined: New`）

- [ ] **Step 3: 实现**

```go
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

type Box struct{ aead cipher.AEAD }

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("seal: key must be 32 bytes")
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Seal(plain []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return b.aead.Seal(nonce, nonce, plain, nil)
}

func (b *Box) Open(data []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(data) < n {
		return nil, errors.New("seal: ciphertext too short")
	}
	return b.aead.Open(nil, data[:n], data[n:], nil)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/seal/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/seal
git commit -m "feat: AES-256-GCM seal box"
```

---

### Task 3: SQLite 存储基础（迁移、bots、白名单）

**Files:**
- Create: `internal/store/store.go`, `internal/store/consts.go`, `internal/store/migrations/0001_init.sql`, `internal/store/bots.go`, `internal/store/whitelist.go`, `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - `store.Open(path string) (*store.Store, error)`，`(*Store).Close() error`，`store.ErrNotFound`
  - 常量：`StatusRunning/StatusError/StatusStopped/StatusRemoved`、`StatePending/StateDone/StateFailed/StateTooLarge`、`ReceiptNone/ReceiptSeen/ReceiptDone/ReceiptFailed`
  - `store.Bot{ID, TgBotID int64; Username, Name, AvatarPath string; TokenEnc []byte; Enabled bool; Status, LastError string; UpdateOffset, CreatedAt int64}`
  - `UpsertBot(ctx, *Bot) (int64, error)`、`GetBot(ctx, id)`、`GetBotByTgID(ctx, tgID)`（均返回 `(*Bot, error)`，不存在为 `ErrNotFound`）、`ListBots(ctx) ([]Bot, error)`、`SetBotStatus(ctx, id, status, lastError string) error`、`SetBotEnabled(ctx, id int64, enabled bool) error`、`SetBotAvatar(ctx, id int64, path string) error`、`AdvanceOffset(ctx, id, offset int64) error`、`RemoveBot(ctx, id int64) error`
  - `store.WhitelistEntry{BotID, TgUserID int64; Note string; CanFetch bool}`、`store.Rejected{BotID, TgUserID int64; FirstName, Username string; LastSeenAt, Count int64}`
  - `CheckAllowed(ctx, botID, userID int64) (allowed, canFetch bool, err error)`、`ListWhitelist(ctx, botID) ([]WhitelistEntry, error)`、`PutWhitelist(ctx, WhitelistEntry) error`、`DeleteWhitelist(ctx, botID, userID int64) error`、`RecordRejected(ctx, Rejected) error`、`ListRejected(ctx, botID int64) ([]Rejected, error)`
  - 包内辅助：`(*Store).withTx(ctx, func(*sql.Tx) error) error`、`scanner` 接口、`affected(sql.Result, error) error`

- [ ] **Step 1: 写迁移** `internal/store/migrations/0001_init.sql`

```sql
CREATE TABLE bots (
  id            INTEGER PRIMARY KEY,
  tg_bot_id     INTEGER NOT NULL UNIQUE,
  username      TEXT    NOT NULL DEFAULT '',
  name          TEXT    NOT NULL DEFAULT '',
  avatar_path   TEXT    NOT NULL DEFAULT '',
  token_enc     BLOB    NOT NULL,
  enabled       INTEGER NOT NULL DEFAULT 1,
  status        TEXT    NOT NULL DEFAULT 'stopped',
  last_error    TEXT    NOT NULL DEFAULT '',
  update_offset INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);

CREATE TABLE whitelist (
  bot_id     INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  tg_user_id INTEGER NOT NULL,
  note       TEXT    NOT NULL DEFAULT '',
  can_fetch  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bot_id, tg_user_id)
);

CREATE TABLE rejected (
  bot_id       INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  tg_user_id   INTEGER NOT NULL,
  first_name   TEXT    NOT NULL DEFAULT '',
  username     TEXT    NOT NULL DEFAULT '',
  last_seen_at INTEGER NOT NULL,
  count        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (bot_id, tg_user_id)
);

CREATE TABLE senders (
  tg_user_id  INTEGER PRIMARY KEY,
  first_name  TEXT    NOT NULL DEFAULT '',
  last_name   TEXT    NOT NULL DEFAULT '',
  username    TEXT    NOT NULL DEFAULT '',
  avatar_path TEXT    NOT NULL DEFAULT '',
  updated_at  INTEGER NOT NULL
);

CREATE TABLE chats (
  id              INTEGER PRIMARY KEY,
  bot_id          INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  sender_id       INTEGER NOT NULL REFERENCES senders(tg_user_id),
  last_message_at INTEGER NOT NULL DEFAULT 0,
  UNIQUE (bot_id, sender_id)
);

CREATE TABLE messages (
  id                     INTEGER PRIMARY KEY,
  chat_id                INTEGER NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  tg_message_id          INTEGER NOT NULL,
  source                 TEXT    NOT NULL,
  media_group_id         TEXT    NOT NULL DEFAULT '',
  date                   INTEGER NOT NULL,
  edit_date              INTEGER NOT NULL DEFAULT 0,
  kind                   TEXT    NOT NULL,
  text                   TEXT    NOT NULL DEFAULT '',
  entities_json          TEXT    NOT NULL DEFAULT '[]',
  forward_origin_json    TEXT    NOT NULL DEFAULT '',
  reply_to_tg_message_id INTEGER NOT NULL DEFAULT 0,
  origin_chat_id         INTEGER NOT NULL DEFAULT 0,
  origin_chat_title      TEXT    NOT NULL DEFAULT '',
  origin_link            TEXT    NOT NULL DEFAULT '',
  extra_json             TEXT    NOT NULL DEFAULT '',
  raw_format             TEXT    NOT NULL,
  raw_json               TEXT    NOT NULL,
  receipt                TEXT    NOT NULL DEFAULT 'none',
  deleted_at             INTEGER NOT NULL DEFAULT 0,
  UNIQUE (chat_id, source, tg_message_id)
);
CREATE INDEX messages_chat_page ON messages(chat_id, deleted_at, id);
CREATE INDEX messages_group ON messages(chat_id, media_group_id);

CREATE TABLE media (
  id              INTEGER PRIMARY KEY,
  dedupe_key      TEXT    NOT NULL UNIQUE,
  bot_id          INTEGER NOT NULL DEFAULT 0,
  source_ref      TEXT    NOT NULL DEFAULT '',
  kind            TEXT    NOT NULL,
  mime            TEXT    NOT NULL DEFAULT '',
  file_name       TEXT    NOT NULL DEFAULT '',
  size            INTEGER NOT NULL DEFAULT 0,
  width           INTEGER NOT NULL DEFAULT 0,
  height          INTEGER NOT NULL DEFAULT 0,
  duration        INTEGER NOT NULL DEFAULT 0,
  waveform        BLOB,
  path            TEXT    NOT NULL DEFAULT '',
  state           TEXT    NOT NULL DEFAULT 'pending',
  attempts        INTEGER NOT NULL DEFAULT 0,
  next_attempt_at INTEGER NOT NULL DEFAULT 0,
  error           TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX media_due ON media(state, next_attempt_at);

CREATE TABLE message_media (
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  media_id   INTEGER NOT NULL REFERENCES media(id),
  role       TEXT    NOT NULL,
  position   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (message_id, media_id, role)
);
CREATE INDEX message_media_media ON message_media(media_id);

CREATE TABLE userbot (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  phone       TEXT    NOT NULL DEFAULT '',
  tg_user_id  INTEGER NOT NULL DEFAULT 0,
  name        TEXT    NOT NULL DEFAULT '',
  session_enc BLOB,
  status      TEXT    NOT NULL DEFAULT 'logged_out',
  last_error  TEXT    NOT NULL DEFAULT '',
  updated_at  INTEGER NOT NULL DEFAULT 0
);
```

说明（相对 spec 的实现细化，不改变行为）：`media.bot_id` + `media.source_ref`（Bot API `file_id` 只对收到它的机器人有效，下载时需要）；`messages.receipt` 记录回执进度防止重复发送；缩略图不单列 `thumb_path`，而是作为 `role='thumb'` 的独立 media 行，复用去重与下载状态机。

- [ ] **Step 2: 写失败测试** `internal/store/store_test.go`

```go
package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

var ctx = context.Background()

func newStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedBot(t *testing.T, s *Store, tgID int64) int64 {
	t.Helper()
	id, err := s.UpsertBot(ctx, &Bot{TgBotID: tgID, Username: "bot", Name: "Bot", TokenEnc: []byte("enc"), CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	for i := 0; i < 2; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var v int
		if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 1 {
			t.Fatalf("user_version = %d, %v", v, err)
		}
		s.Close()
	}
}

func TestBotLifecycle(t *testing.T) {
	s := newStore(t)
	id := seedBot(t, s, 777)
	b, err := s.GetBot(ctx, id)
	if err != nil || b.TgBotID != 777 || !b.Enabled || b.Status != StatusStopped {
		t.Fatalf("GetBot = %+v, %v", b, err)
	}
	if err := s.AdvanceOffset(ctx, id, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceOffset(ctx, id, 5); err != nil {
		t.Fatal(err)
	}
	b, _ = s.GetBot(ctx, id)
	if b.UpdateOffset != 10 {
		t.Fatalf("offset must only move forward, got %d", b.UpdateOffset)
	}
	if err := s.RemoveBot(ctx, id); err != nil {
		t.Fatal(err)
	}
	b, _ = s.GetBot(ctx, id)
	if b.Status != StatusRemoved || b.Enabled || len(b.TokenEnc) != 0 {
		t.Fatalf("RemoveBot = %+v", b)
	}
	again, err := s.UpsertBot(ctx, &Bot{TgBotID: 777, Username: "bot2", TokenEnc: []byte("new"), CreatedAt: 2})
	if err != nil || again != id {
		t.Fatalf("re-add must reactivate same row: %d, %v", again, err)
	}
	b, _ = s.GetBot(ctx, id)
	if b.Status != StatusStopped || !b.Enabled || string(b.TokenEnc) != "new" || b.Username != "bot2" {
		t.Fatalf("reactivated = %+v", b)
	}
	if _, err := s.GetBot(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing bot err = %v", err)
	}
	if err := s.SetBotEnabled(ctx, 999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetBotEnabled missing = %v", err)
	}
}

func TestWhitelistAndRejected(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	if ok, _, _ := s.CheckAllowed(ctx, bot, 42); ok {
		t.Fatal("empty whitelist must reject")
	}
	for i := 0; i < 2; i++ {
		if err := s.RecordRejected(ctx, Rejected{BotID: bot, TgUserID: 42, FirstName: "Alice", LastSeenAt: int64(100 + i)}); err != nil {
			t.Fatal(err)
		}
	}
	rej, err := s.ListRejected(ctx, bot)
	if err != nil || len(rej) != 1 || rej[0].Count != 2 || rej[0].LastSeenAt != 101 {
		t.Fatalf("ListRejected = %+v, %v", rej, err)
	}
	if err := s.PutWhitelist(ctx, WhitelistEntry{BotID: bot, TgUserID: 42, Note: "me", CanFetch: true}); err != nil {
		t.Fatal(err)
	}
	ok, canFetch, err := s.CheckAllowed(ctx, bot, 42)
	if err != nil || !ok || !canFetch {
		t.Fatalf("CheckAllowed = %v %v %v", ok, canFetch, err)
	}
	if rej, _ := s.ListRejected(ctx, bot); len(rej) != 0 {
		t.Fatal("whitelisting must clear the rejected entry")
	}
	wl, _ := s.ListWhitelist(ctx, bot)
	if len(wl) != 1 || wl[0].Note != "me" {
		t.Fatalf("ListWhitelist = %+v", wl)
	}
	if err := s.DeleteWhitelist(ctx, bot, 42); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWhitelist(ctx, bot, 42); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/store/`
Expected: FAIL（`undefined: Open` 等）

- [ ] **Step 4: 实现** `internal/store/consts.go`

```go
package store

import (
	"database/sql"
	"errors"
)

var ErrNotFound = errors.New("not found")

const (
	StatusRunning = "running"
	StatusError   = "error"
	StatusStopped = "stopped"
	StatusRemoved = "removed"

	StatePending  = "pending"
	StateDone     = "done"
	StateFailed   = "failed"
	StateTooLarge = "too_large"

	ReceiptNone   = "none"
	ReceiptSeen   = "seen"
	ReceiptDone   = "done"
	ReceiptFailed = "failed"
)

type scanner interface{ Scan(dest ...any) error }

// affected turns "0 rows affected" into ErrNotFound.
func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
```

`internal/store/store.go`

```go
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: SQLite has a single writer anyway, and this rules out SQLITE_BUSY.
	// Consequence: never issue a query while holding *sql.Rows, and use only tx inside withTx.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var ver int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for i, name := range names {
		n := i + 1
		if !strings.HasPrefix(name, fmt.Sprintf("%04d_", n)) {
			return fmt.Errorf("migration %s out of sequence", name)
		}
		if n <= ver {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		err = s.withTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("migration %s: %w", name, err)
			}
			_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", n))
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
```

`internal/store/bots.go`

```go
package store

import (
	"context"
	"database/sql"
	"errors"
)

type Bot struct {
	ID, TgBotID      int64
	Username, Name   string
	AvatarPath       string
	TokenEnc         []byte
	Enabled          bool
	Status           string
	LastError        string
	UpdateOffset     int64
	CreatedAt        int64
}

const botCols = "id, tg_bot_id, username, name, avatar_path, token_enc, enabled, status, last_error, update_offset, created_at"

func scanBot(r scanner) (*Bot, error) {
	var b Bot
	err := r.Scan(&b.ID, &b.TgBotID, &b.Username, &b.Name, &b.AvatarPath, &b.TokenEnc, &b.Enabled, &b.Status, &b.LastError, &b.UpdateOffset, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// UpsertBot inserts a bot or reactivates the existing row with the same tg_bot_id.
// Callers must reject duplicates of non-removed bots before calling.
func (s *Store) UpsertBot(ctx context.Context, b *Bot) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO bots (tg_bot_id, username, name, token_enc, enabled, status, last_error, created_at)
		VALUES (?, ?, ?, ?, 1, 'stopped', '', ?)
		ON CONFLICT (tg_bot_id) DO UPDATE SET
			username = excluded.username, name = excluded.name, token_enc = excluded.token_enc,
			enabled = 1, status = 'stopped', last_error = ''
		RETURNING id`, b.TgBotID, b.Username, b.Name, b.TokenEnc, b.CreatedAt).Scan(&id)
	return id, err
}

func (s *Store) GetBot(ctx context.Context, id int64) (*Bot, error) {
	return scanBot(s.db.QueryRowContext(ctx, "SELECT "+botCols+" FROM bots WHERE id = ?", id))
}

func (s *Store) GetBotByTgID(ctx context.Context, tgID int64) (*Bot, error) {
	return scanBot(s.db.QueryRowContext(ctx, "SELECT "+botCols+" FROM bots WHERE tg_bot_id = ?", tgID))
}

func (s *Store) ListBots(ctx context.Context) ([]Bot, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+botCols+" FROM bots ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bot{}
	for rows.Next() {
		b, err := scanBot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (s *Store) SetBotStatus(ctx context.Context, id int64, status, lastError string) error {
	return affected(s.db.ExecContext(ctx, "UPDATE bots SET status = ?, last_error = ? WHERE id = ?", status, lastError, id))
}

func (s *Store) SetBotEnabled(ctx context.Context, id int64, enabled bool) error {
	return affected(s.db.ExecContext(ctx, "UPDATE bots SET enabled = ? WHERE id = ? AND status != 'removed'", enabled, id))
}

func (s *Store) SetBotAvatar(ctx context.Context, id int64, path string) error {
	return affected(s.db.ExecContext(ctx, "UPDATE bots SET avatar_path = ? WHERE id = ?", path, id))
}

func (s *Store) AdvanceOffset(ctx context.Context, id, offset int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE bots SET update_offset = ? WHERE id = ? AND update_offset < ?", offset, id, offset)
	return err
}

// RemoveBot keeps the archive but wipes the token and hides the bot from polling.
func (s *Store) RemoveBot(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx,
		"UPDATE bots SET enabled = 0, status = 'removed', token_enc = x'', last_error = '' WHERE id = ?", id))
}
```

`internal/store/whitelist.go`

```go
package store

import (
	"context"
	"database/sql"
	"errors"
)

type WhitelistEntry struct {
	BotID, TgUserID int64
	Note            string
	CanFetch        bool
}

type Rejected struct {
	BotID, TgUserID     int64
	FirstName, Username string
	LastSeenAt, Count   int64
}

func (s *Store) CheckAllowed(ctx context.Context, botID, userID int64) (bool, bool, error) {
	var canFetch bool
	err := s.db.QueryRowContext(ctx, "SELECT can_fetch FROM whitelist WHERE bot_id = ? AND tg_user_id = ?", botID, userID).Scan(&canFetch)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, canFetch, nil
}

func (s *Store) ListWhitelist(ctx context.Context, botID int64) ([]WhitelistEntry, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT bot_id, tg_user_id, note, can_fetch FROM whitelist WHERE bot_id = ? ORDER BY tg_user_id", botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WhitelistEntry{}
	for rows.Next() {
		var e WhitelistEntry
		if err := rows.Scan(&e.BotID, &e.TgUserID, &e.Note, &e.CanFetch); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) PutWhitelist(ctx context.Context, e WhitelistEntry) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO whitelist (bot_id, tg_user_id, note, can_fetch) VALUES (?, ?, ?, ?)
			ON CONFLICT (bot_id, tg_user_id) DO UPDATE SET note = excluded.note, can_fetch = excluded.can_fetch`,
			e.BotID, e.TgUserID, e.Note, e.CanFetch); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM rejected WHERE bot_id = ? AND tg_user_id = ?", e.BotID, e.TgUserID)
		return err
	})
}

func (s *Store) DeleteWhitelist(ctx context.Context, botID, userID int64) error {
	return affected(s.db.ExecContext(ctx, "DELETE FROM whitelist WHERE bot_id = ? AND tg_user_id = ?", botID, userID))
}

func (s *Store) RecordRejected(ctx context.Context, r Rejected) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rejected (bot_id, tg_user_id, first_name, username, last_seen_at, count) VALUES (?, ?, ?, ?, ?, 1)
		ON CONFLICT (bot_id, tg_user_id) DO UPDATE SET
			first_name = excluded.first_name, username = excluded.username,
			last_seen_at = excluded.last_seen_at, count = rejected.count + 1`,
		r.BotID, r.TgUserID, r.FirstName, r.Username, r.LastSeenAt)
	return err
}

func (s *Store) ListRejected(ctx context.Context, botID int64) ([]Rejected, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT bot_id, tg_user_id, first_name, username, last_seen_at, count
		FROM rejected WHERE bot_id = ? ORDER BY last_seen_at DESC LIMIT 50`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rejected{}
	for rows.Next() {
		var r Rejected
		if err := rows.Scan(&r.BotID, &r.TgUserID, &r.FirstName, &r.Username, &r.LastSeenAt, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/store/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store go.mod go.sum
git commit -m "feat(store): schema, migrations, bots and whitelist"
```

---

### Task 4: 统一消息模型、Bot API 客户端与假服务器

**Files:**
- Create: `internal/model/model.go`, `internal/tgbot/types.go`, `internal/tgbot/client.go`, `internal/tgbot/client_test.go`, `internal/tgtest/fake.go`

**Interfaces:**
- Produces:
  - `model`：`Kind*` 常量；`SourceBotUpdate = "bot_update"`、`SourceUserbotFetch = "userbot_fetch"`；`RawBotAPI = "botapi"`、`RawMTProto = "mtproto"`；`RoleMain = "main"`、`RoleThumb = "thumb"`；类型 `Sender`、`Entity`、`ForwardOrigin`、`Media`、`Message`（字段见代码）
  - `tgbot.New(baseURL, token string, hc *http.Client) *tgbot.Client`；`*tgbot.APIError{Code int; Description string; RetryAfter int}`；方法 `GetMe(ctx) (*User, error)`、`LogOut(ctx) error`、`GetUpdates(ctx, offset int64, timeoutSec int) ([]Update, error)`、`GetFile(ctx, fileID string) (*File, error)`、`SetMessageReaction(ctx, chatID, messageID int64, emoji string) error`、`SendMessage(ctx, chatID int64, text string, replyTo int64) error`、`GetUserProfilePhotos(ctx, userID int64, limit int) (*UserProfilePhotos, error)`
  - `tgbot.Update{UpdateID int64; Message, EditedMessage json.RawMessage}`
  - `tgtest.New(t testing.TB) *tgtest.FakeTG`；`URL() string`；`SetMe(id int64, username string)`；`AddFile(fileID string, content []byte)`；`SetAvatar(userID int64, fileID string, content []byte)`；`PushMessage(json string) int64`；`PushEdited(json string) int64`；`FailUpdates(code int, desc string)`；`RejectToken(token string)`；`Calls(method string) []tgtest.Call`；`LastOffset() int64`；字段 `RemoteDir string`；辅助 `tgtest.TextMsg(id, from int64, text string) string`、`tgtest.PhotoMsg(id, from int64, fileID string) string`

- [ ] **Step 1: 写模型** `internal/model/model.go`

```go
// Package model is the source-independent message shape stored by tgarchive.
// Bot API updates and MTProto messages are both converted into it.
package model

import "encoding/json"

type Kind string

const (
	KindText      Kind = "text"
	KindPhoto     Kind = "photo"
	KindVideo     Kind = "video"
	KindAnimation Kind = "animation"
	KindVoice     Kind = "voice"
	KindAudio     Kind = "audio"
	KindDocument  Kind = "document"
	KindSticker   Kind = "sticker"
	KindVideoNote Kind = "video_note"
	KindLocation  Kind = "location"
	KindVenue     Kind = "venue"
	KindContact   Kind = "contact"
	KindPoll      Kind = "poll"
	KindDice      Kind = "dice"
	KindOther     Kind = "other"
)

const (
	SourceBotUpdate    = "bot_update"
	SourceUserbotFetch = "userbot_fetch"

	RawBotAPI  = "botapi"
	RawMTProto = "mtproto"

	RoleMain  = "main"
	RoleThumb = "thumb"
)

type Sender struct {
	TgUserID  int64
	FirstName string
	LastName  string
	Username  string
}

// Entity offsets and lengths are UTF-16 code units, as Telegram sends them.
type Entity struct {
	Type          string `json:"type"`
	Offset        int    `json:"offset"`
	Length        int    `json:"length"`
	URL           string `json:"url,omitempty"`
	Language      string `json:"language,omitempty"`
	UserID        int64  `json:"user_id,omitempty"`
	CustomEmojiID string `json:"custom_emoji_id,omitempty"`
}

type ForwardOrigin struct {
	Type      string `json:"type"` // user / hidden_user / chat / channel
	Name      string `json:"name"`
	Username  string `json:"username,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	ChatID    int64  `json:"chat_id,omitempty"`
	MessageID int64  `json:"message_id,omitempty"`
	Date      int64  `json:"date"`
	Signature string `json:"signature,omitempty"`
}

type Media struct {
	DedupeKey string // "bot:<file_unique_id>" or "mt:<id>"
	SourceRef string // Bot API file_id, or MTProto location blob
	Kind      string
	Mime      string
	FileName  string
	Size      int64
	Width     int
	Height    int
	Duration  int
	Waveform  []byte
	Role      string
}

type Message struct {
	TgMessageID        int64
	Source             string
	MediaGroupID       string
	Date               int64
	EditDate           int64
	Kind               Kind
	Text               string
	Entities           []Entity
	ForwardOrigin      *ForwardOrigin
	ReplyToTgMessageID int64
	OriginChatID       int64
	OriginChatTitle    string
	OriginLink         string
	Extra              json.RawMessage
	RawFormat          string
	Raw                json.RawMessage
	Media              []Media
}
```

- [ ] **Step 2: 写 Bot API 类型** `internal/tgbot/types.go`

```go
package tgbot

import "encoding/json"

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Username string `json:"username"`
}

type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int64  `json:"file_size"`
}

type Animation struct {
	FileID       string     `json:"file_id"`
	FileUniqueID string     `json:"file_unique_id"`
	Width        int        `json:"width"`
	Height       int        `json:"height"`
	Duration     int        `json:"duration"`
	Thumbnail    *PhotoSize `json:"thumbnail"`
	FileName     string     `json:"file_name"`
	MimeType     string     `json:"mime_type"`
	FileSize     int64      `json:"file_size"`
}

type Video = Animation

type Audio struct {
	FileID       string     `json:"file_id"`
	FileUniqueID string     `json:"file_unique_id"`
	Duration     int        `json:"duration"`
	Performer    string     `json:"performer"`
	Title        string     `json:"title"`
	FileName     string     `json:"file_name"`
	MimeType     string     `json:"mime_type"`
	FileSize     int64      `json:"file_size"`
	Thumbnail    *PhotoSize `json:"thumbnail"`
}

type Document struct {
	FileID       string     `json:"file_id"`
	FileUniqueID string     `json:"file_unique_id"`
	Thumbnail    *PhotoSize `json:"thumbnail"`
	FileName     string     `json:"file_name"`
	MimeType     string     `json:"mime_type"`
	FileSize     int64      `json:"file_size"`
}

type Voice struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
}

type VideoNote struct {
	FileID       string     `json:"file_id"`
	FileUniqueID string     `json:"file_unique_id"`
	Length       int        `json:"length"`
	Duration     int        `json:"duration"`
	Thumbnail    *PhotoSize `json:"thumbnail"`
	FileSize     int64      `json:"file_size"`
}

type Sticker struct {
	FileID       string     `json:"file_id"`
	FileUniqueID string     `json:"file_unique_id"`
	Type         string     `json:"type"`
	Width        int        `json:"width"`
	Height       int        `json:"height"`
	IsAnimated   bool       `json:"is_animated"`
	IsVideo      bool       `json:"is_video"`
	Thumbnail    *PhotoSize `json:"thumbnail"`
	Emoji        string     `json:"emoji"`
	SetName      string     `json:"set_name"`
	FileSize     int64      `json:"file_size"`
}

type MessageEntity struct {
	Type          string `json:"type"`
	Offset        int    `json:"offset"`
	Length        int    `json:"length"`
	URL           string `json:"url"`
	User          *User  `json:"user"`
	Language      string `json:"language"`
	CustomEmojiID string `json:"custom_emoji_id"`
}

type MessageOrigin struct {
	Type            string `json:"type"`
	Date            int64  `json:"date"`
	SenderUser      *User  `json:"sender_user"`
	SenderUserName  string `json:"sender_user_name"`
	SenderChat      *Chat  `json:"sender_chat"`
	Chat            *Chat  `json:"chat"`
	MessageID       int64  `json:"message_id"`
	AuthorSignature string `json:"author_signature"`
}

type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type Venue struct {
	Location Location `json:"location"`
	Title    string   `json:"title"`
	Address  string   `json:"address"`
}

type Contact struct {
	PhoneNumber string `json:"phone_number"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	UserID      int64  `json:"user_id"`
}

type PollOption struct {
	Text       string `json:"text"`
	VoterCount int    `json:"voter_count"`
}

type Poll struct {
	Question              string       `json:"question"`
	Options               []PollOption `json:"options"`
	TotalVoterCount       int          `json:"total_voter_count"`
	IsAnonymous           bool         `json:"is_anonymous"`
	Type                  string       `json:"type"`
	AllowsMultipleAnswers bool         `json:"allows_multiple_answers"`
}

type Dice struct {
	Emoji string `json:"emoji"`
	Value int    `json:"value"`
}

type MessageRef struct {
	MessageID int64 `json:"message_id"`
}

type Message struct {
	MessageID       int64           `json:"message_id"`
	From            *User           `json:"from"`
	Chat            Chat            `json:"chat"`
	Date            int64           `json:"date"`
	EditDate        int64           `json:"edit_date"`
	ForwardOrigin   *MessageOrigin  `json:"forward_origin"`
	ReplyToMessage  *MessageRef     `json:"reply_to_message"`
	MediaGroupID    string          `json:"media_group_id"`
	Text            string          `json:"text"`
	Entities        []MessageEntity `json:"entities"`
	Caption         string          `json:"caption"`
	CaptionEntities []MessageEntity `json:"caption_entities"`
	HasMediaSpoiler bool            `json:"has_media_spoiler"`
	Photo           []PhotoSize     `json:"photo"`
	Animation       *Animation      `json:"animation"`
	Video           *Video          `json:"video"`
	Audio           *Audio          `json:"audio"`
	Document        *Document       `json:"document"`
	Voice           *Voice          `json:"voice"`
	VideoNote       *VideoNote      `json:"video_note"`
	Sticker         *Sticker        `json:"sticker"`
	Location        *Location       `json:"location"`
	Venue           *Venue          `json:"venue"`
	Contact         *Contact        `json:"contact"`
	Poll            *Poll           `json:"poll"`
	Dice            *Dice           `json:"dice"`
}

// Update keeps message payloads raw so they can be archived verbatim.
type Update struct {
	UpdateID      int64           `json:"update_id"`
	Message       json.RawMessage `json:"message"`
	EditedMessage json.RawMessage `json:"edited_message"`
}

type File struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileSize     int64  `json:"file_size"`
	FilePath     string `json:"file_path"`
}

type UserProfilePhotos struct {
	TotalCount int           `json:"total_count"`
	Photos     [][]PhotoSize `json:"photos"`
}
```

- [ ] **Step 3: 写假服务器** `internal/tgtest/fake.go`

```go
// Package tgtest provides an in-process fake of the Telegram Bot API for tests.
package tgtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type Call struct {
	Token  string
	Method string
	Params map[string]any
}

type FakeTG struct {
	Server    *httptest.Server
	RemoteDir string

	mu         sync.Mutex
	me         map[string]any
	updates    []json.RawMessage // update_id = index + 1
	calls      []Call
	files      map[string][]byte
	avatars    map[int64]string
	lastOffset int64
	updErr     *apiErr
	badTokens  map[string]bool
}

type apiErr struct {
	code int
	desc string
}

func New(t testing.TB) *FakeTG {
	f := &FakeTG{
		RemoteDir: t.TempDir(),
		me:        map[string]any{"id": 777, "is_bot": true, "first_name": "Archive", "username": "archive_bot"},
		files:     map[string][]byte{},
		avatars:   map[int64]string{},
		badTokens: map[string]bool{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Server.Close)
	return f
}

func (f *FakeTG) URL() string { return f.Server.URL }

func (f *FakeTG) SetMe(id int64, username string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.me = map[string]any{"id": id, "is_bot": true, "first_name": "Archive", "username": username}
}

func (f *FakeTG) AddFile(fileID string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[fileID] = content
}

func (f *FakeTG) SetAvatar(userID int64, fileID string, content []byte) {
	f.AddFile(fileID, content)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.avatars[userID] = fileID
}

func (f *FakeTG) PushMessage(msg string) int64 { return f.push("message", msg) }
func (f *FakeTG) PushEdited(msg string) int64  { return f.push("edited_message", msg) }

func (f *FakeTG) push(field, msg string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := int64(len(f.updates) + 1)
	f.updates = append(f.updates, json.RawMessage(fmt.Sprintf(`{"update_id":%d,%q:%s}`, id, field, msg)))
	return id
}

func (f *FakeTG) FailUpdates(code int, desc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updErr = &apiErr{code, desc}
}

func (f *FakeTG) RejectToken(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.badTokens[token] = true
}

func (f *FakeTG) Calls(method string) []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Call
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *FakeTG) LastOffset() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastOffset
}

func (f *FakeTG) serve(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/bot")
	i := strings.LastIndex(rest, "/")
	if i < 0 {
		http.NotFound(w, r)
		return
	}
	token, method := rest[:i], rest[i+1:]
	params := map[string]any{}
	_ = json.NewDecoder(r.Body).Decode(&params)

	f.mu.Lock()
	f.calls = append(f.calls, Call{Token: token, Method: method, Params: params})
	bad := f.badTokens[token]
	me := f.me
	f.mu.Unlock()
	if bad {
		fail(w, 401, "Unauthorized")
		return
	}

	switch method {
	case "getMe":
		ok(w, me)
	case "logOut", "setMessageReaction":
		ok(w, true)
	case "sendMessage":
		ok(w, map[string]any{"message_id": 1, "date": 0, "chat": map[string]any{"id": params["chat_id"], "type": "private"}})
	case "getUpdates":
		f.getUpdates(w, r, params)
	case "getFile":
		f.getFile(w, token, params)
	case "getUserProfilePhotos":
		f.mu.Lock()
		fid, has := f.avatars[int64(num(params["user_id"]))]
		f.mu.Unlock()
		if !has {
			ok(w, map[string]any{"total_count": 0, "photos": []any{}})
			return
		}
		ok(w, map[string]any{"total_count": 1, "photos": [][]map[string]any{{
			{"file_id": fid, "file_unique_id": "u" + fid, "width": 160, "height": 160},
			{"file_id": fid, "file_unique_id": "u" + fid, "width": 640, "height": 640},
		}}})
	default:
		fail(w, 404, "Not Found: method not found")
	}
}

func (f *FakeTG) getUpdates(w http.ResponseWriter, r *http.Request, params map[string]any) {
	offset := int64(num(params["offset"]))
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		f.mu.Lock()
		f.lastOffset = offset
		if f.updErr != nil {
			e := *f.updErr
			f.mu.Unlock()
			fail(w, e.code, e.desc)
			return
		}
		out := []json.RawMessage{}
		for i, u := range f.updates {
			if int64(i+1) >= offset {
				out = append(out, u)
			}
		}
		f.mu.Unlock()
		if len(out) > 0 || time.Now().After(deadline) {
			ok(w, out)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (f *FakeTG) getFile(w http.ResponseWriter, token string, params map[string]any) {
	fileID, _ := params["file_id"].(string)
	f.mu.Lock()
	content, has := f.files[fileID]
	f.mu.Unlock()
	if !has {
		fail(w, 400, "Bad Request: invalid file_id")
		return
	}
	dir := filepath.Join(f.RemoteDir, token, "documents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(w, 500, err.Error())
		return
	}
	p := filepath.Join(dir, fileID+".jpg")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		fail(w, 500, err.Error())
		return
	}
	ok(w, map[string]any{"file_id": fileID, "file_unique_id": "u" + fileID, "file_size": len(content), "file_path": p})
}

func ok(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func fail(w http.ResponseWriter, code int, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": code, "description": desc})
}

func num(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

// TextMsg builds a private-chat text message from user `from`.
func TextMsg(id, from int64, text string) string {
	return fmt.Sprintf(`{"message_id":%d,"from":{"id":%d,"is_bot":false,"first_name":"User%d"},"chat":{"id":%d,"type":"private"},"date":%d,"text":%q}`,
		id, from, from, from, 1759500000+id, text)
}

// PhotoMsg builds a private-chat photo message. Callers must AddFile(fileID) and AddFile(fileID+"-s").
func PhotoMsg(id, from int64, fileID string) string {
	return fmt.Sprintf(`{"message_id":%d,"from":{"id":%d,"is_bot":false,"first_name":"User%d"},"chat":{"id":%d,"type":"private"},"date":%d,`+
		`"photo":[{"file_id":"%s-s","file_unique_id":"%s-s","width":90,"height":60,"file_size":2},{"file_id":"%s","file_unique_id":"%s","width":1280,"height":853,"file_size":4}]}`,
		id, from, from, from, 1759500000+id, fileID, fileID, fileID, fileID)
}
```

- [ ] **Step 4: 写失败测试** `internal/tgbot/client_test.go`

```go
package tgbot_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tgarchive/internal/tgbot"
	"tgarchive/internal/tgtest"
)

const token = "777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

var ctx = context.Background()

func TestGetMeAndMethods(t *testing.T) {
	f := tgtest.New(t)
	c := tgbot.New(f.URL(), token, nil)
	me, err := c.GetMe(ctx)
	if err != nil || me.ID != 777 || me.Username != "archive_bot" {
		t.Fatalf("GetMe = %+v, %v", me, err)
	}
	if err := c.SetMessageReaction(ctx, 42, 10, "👀"); err != nil {
		t.Fatal(err)
	}
	call := f.Calls("setMessageReaction")[0]
	if call.Token != token || call.Params["chat_id"].(float64) != 42 || call.Params["message_id"].(float64) != 10 {
		t.Fatalf("reaction call = %+v", call)
	}
	r := call.Params["reaction"].([]any)[0].(map[string]any)
	if r["type"] != "emoji" || r["emoji"] != "👀" {
		t.Fatalf("reaction payload = %+v", r)
	}
	if err := c.SendMessage(ctx, 42, "hi", 10); err != nil {
		t.Fatal(err)
	}
	rp := f.Calls("sendMessage")[0].Params["reply_parameters"].(map[string]any)
	if rp["message_id"].(float64) != 10 || rp["allow_sending_without_reply"] != true {
		t.Fatalf("reply_parameters = %+v", rp)
	}
}

func TestGetUpdatesSendsOffsetAndFilter(t *testing.T) {
	f := tgtest.New(t)
	f.PushMessage(tgtest.TextMsg(1, 42, "a"))
	f.PushMessage(tgtest.TextMsg(2, 42, "b"))
	c := tgbot.New(f.URL(), token, nil)
	ups, err := c.GetUpdates(ctx, 2, 0)
	if err != nil || len(ups) != 1 || ups[0].UpdateID != 2 || len(ups[0].Message) == 0 {
		t.Fatalf("GetUpdates = %+v, %v", ups, err)
	}
	p := f.Calls("getUpdates")[0].Params
	allowed := p["allowed_updates"].([]any)
	if len(allowed) != 2 || allowed[0] != "message" || allowed[1] != "edited_message" {
		t.Fatalf("allowed_updates = %v", allowed)
	}
}

func TestAPIErrorParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`))
	}))
	defer srv.Close()
	_, err := tgbot.New(srv.URL, token, nil).GetMe(ctx)
	var ae *tgbot.APIError
	if !errors.As(err, &ae) || ae.Code != 429 || ae.RetryAfter != 7 {
		t.Fatalf("err = %#v", err)
	}
}

func TestNetworkErrorRedactsToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := tgbot.New(url, token, nil).GetMe(ctx)
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "AAAAAAAA") {
		t.Fatalf("token leaked or no error: %v", err)
	}
}
```

- [ ] **Step 5: 运行确认失败**

Run: `go test ./internal/tgbot/`
Expected: FAIL（`undefined: tgbot.New`）

- [ ] **Step 6: 实现** `internal/tgbot/client.go`

```go
package tgbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type APIError struct {
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram api error %d: %s", e.Code, e.Description)
}

type Client struct {
	baseURL string
	token   string
	hc      *http.Client
}

func New(baseURL, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, hc: hc}
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	if params == nil {
		params = struct{}{}
	}
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return c.redact(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return c.redact(err) // *url.Error embeds the URL, which contains the token
	}
	defer resp.Body.Close()
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  *struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&env); err != nil {
		return fmt.Errorf("telegram %s: http %d: decode response: %w", method, resp.StatusCode, err)
	}
	if !env.OK {
		ae := &APIError{Code: env.ErrorCode, Description: env.Description}
		if ae.Code == 0 {
			ae.Code = resp.StatusCode
		}
		if env.Parameters != nil {
			ae.RetryAfter = env.Parameters.RetryAfter
		}
		return ae
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

func (c *Client) redact(err error) error {
	if c.token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.token, "<redacted>"))
}

func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.call(ctx, "getMe", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (c *Client) LogOut(ctx context.Context) error {
	return c.call(ctx, "logOut", nil, nil)
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var ups []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message", "edited_message"},
	}, &ups)
	return ups, err
}

func (c *Client) GetFile(ctx context.Context, fileID string) (*File, error) {
	var f File
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func (c *Client) SetMessageReaction(ctx context.Context, chatID, messageID int64, emoji string) error {
	return c.call(ctx, "setMessageReaction", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"reaction":   []map[string]string{{"type": "emoji", "emoji": emoji}},
	}, nil)
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, replyTo int64) error {
	params := map[string]any{"chat_id": chatID, "text": text}
	if replyTo != 0 {
		params["reply_parameters"] = map[string]any{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	return c.call(ctx, "sendMessage", params, nil)
}

func (c *Client) GetUserProfilePhotos(ctx context.Context, userID int64, limit int) (*UserProfilePhotos, error) {
	var p UserProfilePhotos
	if err := c.call(ctx, "getUserProfilePhotos", map[string]any{"user_id": userID, "limit": limit}, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
```

- [ ] **Step 7: 运行确认通过**

Run: `go test ./internal/tgbot/ ./internal/tgtest/ && go vet ./...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/model internal/tgbot internal/tgtest
git commit -m "feat: unified message model, Bot API client and fake server"
```

---

### Task 5: Bot API 消息转换

**Files:**
- Create: `internal/convert/botapi/convert.go`, `internal/convert/botapi/convert_test.go`

**Interfaces:**
- Consumes: `tgbot.Message` 等类型；`model.*`
- Produces: `botapi.Convert(raw json.RawMessage) (*botapi.Result, error)`；`botapi.Result{ChatID int64; ChatType string; Sender model.Sender; Msg *model.Message}`；`botapi.ErrNoSender`

规则：

- caption 存在时用 caption/caption_entities，否则 text/entities
- 判定优先级：photo → animation（Bot API 对 GIF 同时给 `document`，必须先判 animation）→ video → video_note → voice → audio → sticker → document → venue（venue 同时含 location，必须先判）→ location → contact → poll → dice → 有文本则 text → 否则 other
- photo：最大尺寸（数组最后一个）为 `main`；数组多于 1 个时第一个为 `thumb`
- video / animation / video_note / audio / document 有 thumbnail 时追加 `thumb`
- sticker 不取缩略图；mime：`is_animated` → `application/x-tgsticker`，`is_video` → `video/webm`，否则 `image/webp`
- 默认 mime：photo/thumb `image/jpeg`，voice `audio/ogg`，video_note `video/mp4`，animation `video/mp4`
- `dedupe_key = "bot:" + file_unique_id`，`SourceRef = file_id`
- `extra`：仅在有内容时写入；键：`spoiler`、`performer`、`title`、`emoji`、`set_name`、`sticker_type`、`latitude`、`longitude`、`address`、`phone_number`、`first_name`、`last_name`、`user_id`、`question`、`options`、`total_voter_count`、`is_anonymous`、`poll_type`、`multiple`、`value`

- [ ] **Step 1: 写失败测试**

```go
package botapi

import (
	"encoding/json"
	"errors"
	"testing"

	"tgarchive/internal/model"
)

const from = `"from":{"id":42,"is_bot":false,"first_name":"Shinya","last_name":"K","username":"shinya"},"chat":{"id":42,"type":"private"},"date":1759500000`

func conv(t *testing.T, body string) *Result {
	t.Helper()
	r, err := Convert(json.RawMessage(`{"message_id":10,` + from + `,` + body + `}`))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func extra(t *testing.T, m *model.Message) map[string]any {
	t.Helper()
	out := map[string]any{}
	if len(m.Extra) > 0 {
		if err := json.Unmarshal(m.Extra, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestTextWithEntities(t *testing.T) {
	r := conv(t, `"text":"hello bold link","entities":[{"type":"bold","offset":6,"length":4},{"type":"text_link","offset":11,"length":4,"url":"https://example.com"}]`)
	m := r.Msg
	if r.ChatID != 42 || r.ChatType != "private" || r.Sender.Username != "shinya" || r.Sender.LastName != "K" {
		t.Fatalf("result = %+v", r)
	}
	if m.Kind != model.KindText || m.Text != "hello bold link" || len(m.Entities) != 2 || m.Entities[1].URL != "https://example.com" {
		t.Fatalf("msg = %+v", m)
	}
	if m.Source != model.SourceBotUpdate || m.RawFormat != model.RawBotAPI || m.TgMessageID != 10 || m.Date != 1759500000 || len(m.Raw) == 0 {
		t.Fatalf("meta = %+v", m)
	}
	if len(m.Media) != 0 || len(m.Extra) != 0 {
		t.Fatal("text message must have no media and no extra")
	}
}

func TestPhotoAlbumWithCaptionAndSpoiler(t *testing.T) {
	r := conv(t, `"media_group_id":"g1","has_media_spoiler":true,"caption":"cap","caption_entities":[{"type":"italic","offset":0,"length":3}],`+
		`"photo":[{"file_id":"s","file_unique_id":"us","width":90,"height":60},{"file_id":"m","file_unique_id":"um","width":320,"height":213},{"file_id":"b","file_unique_id":"ub","width":1280,"height":853,"file_size":1000}]`)
	m := r.Msg
	if m.Kind != model.KindPhoto || m.MediaGroupID != "g1" || m.Text != "cap" || m.Entities[0].Type != "italic" {
		t.Fatalf("msg = %+v", m)
	}
	if len(m.Media) != 2 {
		t.Fatalf("media = %+v", m.Media)
	}
	main, thumb := m.Media[0], m.Media[1]
	if main.Role != model.RoleMain || main.DedupeKey != "bot:ub" || main.SourceRef != "b" || main.Width != 1280 || main.Size != 1000 || main.Mime != "image/jpeg" {
		t.Fatalf("main = %+v", main)
	}
	if thumb.Role != model.RoleThumb || thumb.DedupeKey != "bot:us" {
		t.Fatalf("thumb = %+v", thumb)
	}
	if extra(t, m)["spoiler"] != true {
		t.Fatal("spoiler flag missing")
	}
}

func TestAnimationBeatsDocument(t *testing.T) {
	r := conv(t, `"animation":{"file_id":"a","file_unique_id":"ua","width":320,"height":240,"duration":3,"mime_type":"video/mp4","thumbnail":{"file_id":"t","file_unique_id":"ut","width":90,"height":67}},`+
		`"document":{"file_id":"a","file_unique_id":"ua","mime_type":"video/mp4"}`)
	if r.Msg.Kind != model.KindAnimation || len(r.Msg.Media) != 2 || r.Msg.Media[0].Duration != 3 || r.Msg.Media[1].Role != model.RoleThumb {
		t.Fatalf("msg = %+v", r.Msg)
	}
}

func TestMediaKinds(t *testing.T) {
	cases := []struct {
		name, body string
		kind       model.Kind
		mime       string
		media      int
	}{
		{"video", `"video":{"file_id":"v","file_unique_id":"uv","width":1920,"height":1080,"duration":60,"mime_type":"video/mp4","file_size":30000000,"file_name":"a.mp4"}`, model.KindVideo, "video/mp4", 1},
		{"voice", `"voice":{"file_id":"vo","file_unique_id":"uvo","duration":4}`, model.KindVoice, "audio/ogg", 1},
		{"video_note", `"video_note":{"file_id":"n","file_unique_id":"un","length":384,"duration":5,"thumbnail":{"file_id":"nt","file_unique_id":"unt","width":90,"height":90}}`, model.KindVideoNote, "video/mp4", 2},
		{"document", `"document":{"file_id":"d","file_unique_id":"ud","file_name":"report.pdf","mime_type":"application/pdf","file_size":123}`, model.KindDocument, "application/pdf", 1},
		{"tgs sticker", `"sticker":{"file_id":"st","file_unique_id":"ust","type":"regular","width":512,"height":512,"is_animated":true,"is_video":false,"emoji":"😀","set_name":"Pack"}`, model.KindSticker, "application/x-tgsticker", 1},
		{"webm sticker", `"sticker":{"file_id":"st","file_unique_id":"ust","type":"regular","width":512,"height":512,"is_animated":false,"is_video":true}`, model.KindSticker, "video/webm", 1},
		{"static sticker", `"sticker":{"file_id":"st","file_unique_id":"ust","type":"regular","width":512,"height":512,"is_animated":false,"is_video":false}`, model.KindSticker, "image/webp", 1},
		{"audio", `"audio":{"file_id":"au","file_unique_id":"uau","duration":200,"performer":"P","title":"T","mime_type":"audio/mpeg"}`, model.KindAudio, "audio/mpeg", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := conv(t, c.body)
			if r.Msg.Kind != c.kind || len(r.Msg.Media) != c.media || r.Msg.Media[0].Mime != c.mime || r.Msg.Media[0].Role != model.RoleMain {
				t.Fatalf("msg = %+v", r.Msg)
			}
		})
	}
}

func TestStructuredKinds(t *testing.T) {
	r := conv(t, `"venue":{"location":{"latitude":35.6,"longitude":139.7},"title":"Tower","address":"Tokyo"},"location":{"latitude":35.6,"longitude":139.7}`)
	if r.Msg.Kind != model.KindVenue || extra(t, r.Msg)["title"] != "Tower" || extra(t, r.Msg)["latitude"] != 35.6 {
		t.Fatalf("venue = %+v", r.Msg)
	}
	r = conv(t, `"location":{"latitude":1.5,"longitude":2.5}`)
	if r.Msg.Kind != model.KindLocation || extra(t, r.Msg)["longitude"] != 2.5 {
		t.Fatalf("location = %+v", r.Msg)
	}
	r = conv(t, `"contact":{"phone_number":"+100","first_name":"Bob","user_id":5}`)
	if r.Msg.Kind != model.KindContact || extra(t, r.Msg)["phone_number"] != "+100" {
		t.Fatalf("contact = %+v", r.Msg)
	}
	r = conv(t, `"poll":{"id":"p","question":"Q?","options":[{"text":"a","voter_count":1},{"text":"b","voter_count":2}],"total_voter_count":3,"is_anonymous":true,"type":"regular","allows_multiple_answers":false}`)
	if r.Msg.Kind != model.KindPoll || extra(t, r.Msg)["question"] != "Q?" || len(extra(t, r.Msg)["options"].([]any)) != 2 {
		t.Fatalf("poll = %+v", r.Msg)
	}
	r = conv(t, `"dice":{"emoji":"🎲","value":6}`)
	if r.Msg.Kind != model.KindDice || extra(t, r.Msg)["value"] != float64(6) {
		t.Fatalf("dice = %+v", r.Msg)
	}
	r = conv(t, `"story":{"chat":{"id":1,"type":"channel"},"id":5}`)
	if r.Msg.Kind != model.KindOther {
		t.Fatalf("unknown kind = %s", r.Msg.Kind)
	}
}

func TestForwardAndReply(t *testing.T) {
	r := conv(t, `"text":"x","reply_to_message":{"message_id":7},"forward_origin":{"type":"channel","date":1759000000,"chat":{"id":-1001,"type":"channel","title":"News","username":"news"},"message_id":99,"author_signature":"ed"}`)
	o := r.Msg.ForwardOrigin
	if r.Msg.ReplyToTgMessageID != 7 || o == nil || o.Type != "channel" || o.Name != "News" || o.Username != "news" || o.ChatID != -1001 || o.MessageID != 99 || o.Signature != "ed" {
		t.Fatalf("msg = %+v origin = %+v", r.Msg, o)
	}
	r = conv(t, `"text":"x","forward_origin":{"type":"user","date":1,"sender_user":{"id":9,"is_bot":false,"first_name":"Ann","last_name":"Lee"}}`)
	if o := r.Msg.ForwardOrigin; o.Name != "Ann Lee" || o.UserID != 9 {
		t.Fatalf("user origin = %+v", o)
	}
	r = conv(t, `"text":"x","forward_origin":{"type":"hidden_user","date":1,"sender_user_name":"Ghost"}`)
	if o := r.Msg.ForwardOrigin; o.Name != "Ghost" {
		t.Fatalf("hidden origin = %+v", o)
	}
}

func TestGroupChatAndNoSender(t *testing.T) {
	r, err := Convert(json.RawMessage(`{"message_id":1,"from":{"id":42,"is_bot":false,"first_name":"A"},"chat":{"id":-5,"type":"group","title":"G"},"date":1,"text":"x"}`))
	if err != nil || r.ChatType != "group" {
		t.Fatalf("group = %+v, %v", r, err)
	}
	if _, err := Convert(json.RawMessage(`{"message_id":1,"chat":{"id":-100,"type":"channel"},"date":1,"text":"x"}`)); !errors.Is(err, ErrNoSender) {
		t.Fatalf("no sender err = %v", err)
	}
	if _, err := Convert(json.RawMessage(`{not json`)); err == nil {
		t.Fatal("malformed json must fail")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/convert/botapi/`
Expected: FAIL（`undefined: Convert`）

- [ ] **Step 3: 实现** `internal/convert/botapi/convert.go`

```go
// Package botapi converts Bot API messages into model.Message.
package botapi

import (
	"encoding/json"
	"errors"
	"strings"

	"tgarchive/internal/model"
	"tgarchive/internal/tgbot"
)

var ErrNoSender = errors.New("message has no sender")

type Result struct {
	ChatID   int64
	ChatType string
	Sender   model.Sender
	Msg      *model.Message
}

func Convert(raw json.RawMessage) (*Result, error) {
	var m tgbot.Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.From == nil {
		return nil, ErrNoSender
	}
	msg := &model.Message{
		TgMessageID:  m.MessageID,
		Source:       model.SourceBotUpdate,
		MediaGroupID: m.MediaGroupID,
		Date:         m.Date,
		EditDate:     m.EditDate,
		RawFormat:    model.RawBotAPI,
		Raw:          append(json.RawMessage(nil), raw...),
	}
	if m.ReplyToMessage != nil {
		msg.ReplyToTgMessageID = m.ReplyToMessage.MessageID
	}
	msg.ForwardOrigin = convertOrigin(m.ForwardOrigin)
	if m.Caption != "" {
		msg.Text, msg.Entities = m.Caption, convertEntities(m.CaptionEntities)
	} else {
		msg.Text, msg.Entities = m.Text, convertEntities(m.Entities)
	}

	extra := map[string]any{}
	if m.HasMediaSpoiler {
		extra["spoiler"] = true
	}
	fill(msg, &m, extra)
	if len(extra) > 0 {
		b, err := json.Marshal(extra)
		if err != nil {
			return nil, err
		}
		msg.Extra = b
	}
	return &Result{
		ChatID:   m.Chat.ID,
		ChatType: m.Chat.Type,
		Sender: model.Sender{
			TgUserID:  m.From.ID,
			FirstName: m.From.FirstName,
			LastName:  m.From.LastName,
			Username:  m.From.Username,
		},
		Msg: msg,
	}, nil
}

func fill(msg *model.Message, m *tgbot.Message, extra map[string]any) {
	switch {
	case len(m.Photo) > 0:
		msg.Kind = model.KindPhoto
		big := m.Photo[len(m.Photo)-1]
		msg.Media = append(msg.Media, photo(big, model.RoleMain))
		if len(m.Photo) > 1 {
			msg.Media = append(msg.Media, photo(m.Photo[0], model.RoleThumb))
		}
	case m.Animation != nil:
		a := m.Animation
		msg.Kind = model.KindAnimation
		msg.Media = append(msg.Media, file(a.FileUniqueID, a.FileID, "animation", or(a.MimeType, "video/mp4"), a.FileName, a.FileSize, a.Width, a.Height, a.Duration))
		msg.Media = append(msg.Media, thumb(a.Thumbnail)...)
	case m.Video != nil:
		v := m.Video
		msg.Kind = model.KindVideo
		msg.Media = append(msg.Media, file(v.FileUniqueID, v.FileID, "video", or(v.MimeType, "video/mp4"), v.FileName, v.FileSize, v.Width, v.Height, v.Duration))
		msg.Media = append(msg.Media, thumb(v.Thumbnail)...)
	case m.VideoNote != nil:
		v := m.VideoNote
		msg.Kind = model.KindVideoNote
		msg.Media = append(msg.Media, file(v.FileUniqueID, v.FileID, "video_note", "video/mp4", "", v.FileSize, v.Length, v.Length, v.Duration))
		msg.Media = append(msg.Media, thumb(v.Thumbnail)...)
	case m.Voice != nil:
		v := m.Voice
		msg.Kind = model.KindVoice
		msg.Media = append(msg.Media, file(v.FileUniqueID, v.FileID, "voice", or(v.MimeType, "audio/ogg"), "", v.FileSize, 0, 0, v.Duration))
	case m.Audio != nil:
		a := m.Audio
		msg.Kind = model.KindAudio
		msg.Media = append(msg.Media, file(a.FileUniqueID, a.FileID, "audio", or(a.MimeType, "audio/mpeg"), a.FileName, a.FileSize, 0, 0, a.Duration))
		msg.Media = append(msg.Media, thumb(a.Thumbnail)...)
		setIf(extra, "performer", a.Performer)
		setIf(extra, "title", a.Title)
	case m.Sticker != nil:
		s := m.Sticker
		msg.Kind = model.KindSticker
		mime := "image/webp"
		if s.IsAnimated {
			mime = "application/x-tgsticker"
		} else if s.IsVideo {
			mime = "video/webm"
		}
		msg.Media = append(msg.Media, file(s.FileUniqueID, s.FileID, "sticker", mime, "", s.FileSize, s.Width, s.Height, 0))
		setIf(extra, "emoji", s.Emoji)
		setIf(extra, "set_name", s.SetName)
		setIf(extra, "sticker_type", s.Type)
	case m.Document != nil:
		d := m.Document
		msg.Kind = model.KindDocument
		msg.Media = append(msg.Media, file(d.FileUniqueID, d.FileID, "document", or(d.MimeType, "application/octet-stream"), d.FileName, d.FileSize, 0, 0, 0))
		msg.Media = append(msg.Media, thumb(d.Thumbnail)...)
	case m.Venue != nil:
		v := m.Venue
		msg.Kind = model.KindVenue
		extra["latitude"], extra["longitude"] = v.Location.Latitude, v.Location.Longitude
		setIf(extra, "title", v.Title)
		setIf(extra, "address", v.Address)
	case m.Location != nil:
		msg.Kind = model.KindLocation
		extra["latitude"], extra["longitude"] = m.Location.Latitude, m.Location.Longitude
	case m.Contact != nil:
		c := m.Contact
		msg.Kind = model.KindContact
		setIf(extra, "phone_number", c.PhoneNumber)
		setIf(extra, "first_name", c.FirstName)
		setIf(extra, "last_name", c.LastName)
		if c.UserID != 0 {
			extra["user_id"] = c.UserID
		}
	case m.Poll != nil:
		p := m.Poll
		msg.Kind = model.KindPoll
		opts := make([]map[string]any, 0, len(p.Options))
		for _, o := range p.Options {
			opts = append(opts, map[string]any{"text": o.Text, "voter_count": o.VoterCount})
		}
		extra["question"], extra["options"], extra["total_voter_count"] = p.Question, opts, p.TotalVoterCount
		extra["is_anonymous"], extra["poll_type"], extra["multiple"] = p.IsAnonymous, p.Type, p.AllowsMultipleAnswers
	case m.Dice != nil:
		msg.Kind = model.KindDice
		extra["emoji"], extra["value"] = m.Dice.Emoji, m.Dice.Value
	case msg.Text != "":
		msg.Kind = model.KindText
	default:
		msg.Kind = model.KindOther
	}
}

func file(uniq, fileID, kind, mime, name string, size int64, w, h, dur int) model.Media {
	return model.Media{
		DedupeKey: "bot:" + uniq, SourceRef: fileID, Kind: kind, Mime: mime, FileName: name,
		Size: size, Width: w, Height: h, Duration: dur, Role: model.RoleMain,
	}
}

func photo(p tgbot.PhotoSize, role string) model.Media {
	m := file(p.FileUniqueID, p.FileID, "photo", "image/jpeg", "", p.FileSize, p.Width, p.Height, 0)
	m.Role = role
	return m
}

func thumb(p *tgbot.PhotoSize) []model.Media {
	if p == nil {
		return nil
	}
	return []model.Media{photo(*p, model.RoleThumb)}
}

func convertEntities(in []tgbot.MessageEntity) []model.Entity {
	out := make([]model.Entity, 0, len(in))
	for _, e := range in {
		me := model.Entity{Type: e.Type, Offset: e.Offset, Length: e.Length, URL: e.URL, Language: e.Language, CustomEmojiID: e.CustomEmojiID}
		if e.User != nil {
			me.UserID = e.User.ID
		}
		out = append(out, me)
	}
	return out
}

func convertOrigin(o *tgbot.MessageOrigin) *model.ForwardOrigin {
	if o == nil {
		return nil
	}
	f := &model.ForwardOrigin{Type: o.Type, Date: o.Date, Signature: o.AuthorSignature}
	switch o.Type {
	case "user":
		if u := o.SenderUser; u != nil {
			f.Name = strings.TrimSpace(u.FirstName + " " + u.LastName)
			f.Username, f.UserID = u.Username, u.ID
		}
	case "hidden_user":
		f.Name = o.SenderUserName
	case "chat":
		if c := o.SenderChat; c != nil {
			f.Name, f.Username, f.ChatID = c.Title, c.Username, c.ID
		}
	case "channel":
		if c := o.Chat; c != nil {
			f.Name, f.Username, f.ChatID = c.Title, c.Username, c.ID
		}
		f.MessageID = o.MessageID
	}
	return f
}

func setIf(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/convert/botapi/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/convert
git commit -m "feat(convert): Bot API message to unified model"
```

---

### Task 6: 入库事务、媒体状态机、删除与回执信息

**Files:**
- Create: `internal/store/ingest.go`, `internal/store/media.go`, `internal/store/senders.go`, `internal/store/ingest_test.go`

**Interfaces:**
- Consumes: `model.Message`、`model.Sender`；Task 3 的 `withTx`、`affected`、常量
- Produces:
  - `store.IngestInput{BotID int64; Sender model.Sender; Msg *model.Message; Offset int64; Now int64}`（`Offset > 0` 时在同一事务内推进 `bots.update_offset`）
  - `store.IngestResult{MessageID, ChatID int64; ChatCreated, Created bool; OrphanPaths []string}`
  - `(*Store).Ingest(ctx, IngestInput) (*IngestResult, error)`
  - `(*Store).DeleteMessage(ctx, id, now int64) (chatID int64, orphanPaths []string, err error)`
  - `(*Store).PurgeBot(ctx, id int64) ([]string, error)`
  - `store.Media{ID int64; DedupeKey string; BotID int64; SourceRef, Kind, Mime, FileName string; Size int64; Width, Height, Duration int; Waveform []byte; Path, State string; Attempts int; NextAttemptAt int64; Error string}`
  - `GetMedia(ctx, id) (*Media, error)`、`DueMedia(ctx, now int64, limit int) ([]Media, error)`、`MarkMediaDone(ctx, id int64, path string, size int64) (bool, error)`、`MarkMediaRetry(ctx, id int64, attempts int, nextAt int64, errMsg string) error`、`MarkMediaFailed(ctx, id int64, attempts int, errMsg string) error`、`MarkMediaTooLarge(ctx, id int64) error`、`ResetMedia(ctx, id int64) error`（仅 failed → pending，否则 `ErrNotFound`）、`MessagesForMedia(ctx, mediaID int64) ([]int64, error)`
  - `store.ReceiptInfo{MessageID, BotID, TgChatID, TgMessageID int64; Source, Receipt string; Main []MediaStatus}`、`store.MediaStatus{State, Error string}`、`GetReceiptInfo(ctx, messageID int64) (*ReceiptInfo, error)`、`SetReceipt(ctx, messageID int64, receipt string) error`
  - `SetSenderAvatar(ctx, tgUserID int64, path string) error`、`store.ChatSender{BotID, TgUserID int64}`、`ChatSenders(ctx) ([]ChatSender, error)`（每个发送者取一个其所在的非 removed 机器人）

- [ ] **Step 1: 写失败测试** `internal/store/ingest_test.go`

```go
package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"tgarchive/internal/model"
)

var alice = model.Sender{TgUserID: 42, FirstName: "Alice"}

func photoMsg(id int64, keys ...string) *model.Message {
	m := &model.Message{TgMessageID: id, Source: model.SourceBotUpdate, Date: 1000 + id, Kind: model.KindPhoto,
		Text: "caption", RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}
	for _, k := range keys {
		m.Media = append(m.Media, model.Media{DedupeKey: k, SourceRef: "f-" + k, Kind: "photo", Mime: "image/jpeg", Size: 4, Role: model.RoleMain})
	}
	return m
}

func textMsg(id int64, text string) *model.Message {
	return &model.Message{TgMessageID: id, Source: model.SourceBotUpdate, Date: 1000 + id, Kind: model.KindText,
		Text: text, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}
}

func ingest(t *testing.T, s *Store, bot int64, m *model.Message) *IngestResult {
	t.Helper()
	res, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: alice, Msg: m, Now: 5000})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func mediaIDs(t *testing.T, s *Store, msgID int64) []int64 {
	t.Helper()
	rows, err := s.db.Query("SELECT media_id FROM message_media WHERE message_id = ? ORDER BY position", msgID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids
}

func TestIngestCreatesEverything(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: alice, Msg: photoMsg(1, "bot:a"), Offset: 5, Now: 5000})
	if err != nil || !res.Created || !res.ChatCreated || res.MessageID == 0 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if b, _ := s.GetBot(ctx, bot); b.UpdateOffset != 5 {
		t.Fatalf("offset = %d", b.UpdateOffset)
	}
	res2 := ingest(t, s, bot, textMsg(2, "hi"))
	if res2.ChatCreated || res2.ChatID != res.ChatID {
		t.Fatalf("second message must reuse chat: %+v", res2)
	}
	due, _ := s.DueMedia(ctx, 0, 10)
	if len(due) != 1 || due[0].DedupeKey != "bot:a" || due[0].BotID != bot || due[0].SourceRef != "f-bot:a" || due[0].State != StatePending {
		t.Fatalf("due = %+v", due)
	}
}

func TestIngestEditReplacesMedia(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res := ingest(t, s, bot, photoMsg(1, "bot:a"))
	oldID := mediaIDs(t, s, res.MessageID)[0]
	if ok, err := s.MarkMediaDone(ctx, oldID, "1/a.jpg", 4); !ok || err != nil {
		t.Fatal(ok, err)
	}
	edit := photoMsg(1, "bot:b")
	edit.Text, edit.EditDate = "new caption", 2000
	res2 := ingest(t, s, bot, edit)
	if res2.Created || res2.MessageID != res.MessageID || !reflect.DeepEqual(res2.OrphanPaths, []string{"1/a.jpg"}) {
		t.Fatalf("edit res = %+v", res2)
	}
	if _, err := s.GetMedia(ctx, oldID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replaced media must be deleted, err = %v", err)
	}
	var text string
	var editDate int64
	s.db.QueryRow("SELECT text, edit_date FROM messages WHERE id = ?", res.MessageID).Scan(&text, &editDate)
	if text != "new caption" || editDate != 2000 {
		t.Fatalf("edit not applied: %q %d", text, editDate)
	}
}

func TestDedupeAndOrphanCleanup(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	r1 := ingest(t, s, bot, photoMsg(1, "bot:k"))
	r2 := ingest(t, s, bot, photoMsg(2, "bot:k"))
	id1, id2 := mediaIDs(t, s, r1.MessageID)[0], mediaIDs(t, s, r2.MessageID)[0]
	if id1 != id2 {
		t.Fatalf("same dedupe key must share a media row: %d vs %d", id1, id2)
	}
	s.MarkMediaDone(ctx, id1, "1/k.jpg", 4)
	if _, orphans, err := s.DeleteMessage(ctx, r1.MessageID, 9000); err != nil || len(orphans) != 0 {
		t.Fatalf("first delete must keep shared file: %v %v", orphans, err)
	}
	chatID, orphans, err := s.DeleteMessage(ctx, r2.MessageID, 9000)
	if err != nil || chatID != r2.ChatID || !reflect.DeepEqual(orphans, []string{"1/k.jpg"}) {
		t.Fatalf("last delete = %d %v %v", chatID, orphans, err)
	}
	if _, _, err := s.DeleteMessage(ctx, r2.MessageID, 9000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete = %v", err)
	}
}

func TestFailedMediaRequeuedWhenSeenAgain(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	r := ingest(t, s, bot, photoMsg(1, "bot:k"))
	id := mediaIDs(t, s, r.MessageID)[0]
	s.MarkMediaFailed(ctx, id, 4, "boom")
	ingest(t, s, bot, photoMsg(2, "bot:k"))
	m, _ := s.GetMedia(ctx, id)
	if m.State != StatePending || m.Attempts != 0 || m.Error != "" {
		t.Fatalf("media = %+v", m)
	}
}

func TestMediaStateMachine(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	r := ingest(t, s, bot, photoMsg(1, "bot:k"))
	id := mediaIDs(t, s, r.MessageID)[0]
	if err := s.MarkMediaRetry(ctx, id, 1, 160, "net"); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.DueMedia(ctx, 100, 10); len(due) != 0 {
		t.Fatal("media must not be due before next_attempt_at")
	}
	if due, _ := s.DueMedia(ctx, 160, 10); len(due) != 1 || due[0].Attempts != 1 || due[0].Error != "net" {
		t.Fatalf("due = %+v", due)
	}
	if err := s.ResetMedia(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reset of non-failed media = %v", err)
	}
	s.MarkMediaFailed(ctx, id, 4, "gave up")
	if err := s.ResetMedia(ctx, id); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMedia(ctx, id); m.State != StatePending || m.Attempts != 0 {
		t.Fatalf("after reset = %+v", m)
	}
	if err := s.MarkMediaTooLarge(ctx, id); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkMediaDone(ctx, 9999, "x", 1); ok || err != nil {
		t.Fatalf("MarkMediaDone on missing row = %v %v", ok, err)
	}
	ids, _ := s.MessagesForMedia(ctx, id)
	if !reflect.DeepEqual(ids, []int64{r.MessageID}) {
		t.Fatalf("MessagesForMedia = %v", ids)
	}
}

func TestReceiptInfo(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	m := photoMsg(7, "bot:main")
	m.Media = append(m.Media, model.Media{DedupeKey: "bot:thumb", SourceRef: "t", Kind: "photo", Role: model.RoleThumb})
	r := ingest(t, s, bot, m)
	info, err := s.GetReceiptInfo(ctx, r.MessageID)
	if err != nil || info.BotID != bot || info.TgChatID != 42 || info.TgMessageID != 7 || info.Receipt != ReceiptNone || info.Source != model.SourceBotUpdate {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if len(info.Main) != 1 || info.Main[0].State != StatePending {
		t.Fatalf("only main media count: %+v", info.Main)
	}
	s.SetReceipt(ctx, r.MessageID, ReceiptSeen)
	if info, _ := s.GetReceiptInfo(ctx, r.MessageID); info.Receipt != ReceiptSeen {
		t.Fatal("SetReceipt not applied")
	}
}

func TestPurgeAndRemoveBot(t *testing.T) {
	s := newStore(t)
	keep := seedBot(t, s, 1)
	purge := seedBot(t, s, 2)
	ingest(t, s, keep, textMsg(1, "kept"))
	r := ingest(t, s, purge, photoMsg(1, "bot:p"))
	s.MarkMediaDone(ctx, mediaIDs(t, s, r.MessageID)[0], "2/p.jpg", 4)
	if err := s.RemoveBot(ctx, keep); err != nil {
		t.Fatal(err)
	}
	paths, err := s.PurgeBot(ctx, purge)
	if err != nil || !reflect.DeepEqual(paths, []string{"2/p.jpg"}) {
		t.Fatalf("purge = %v %v", paths, err)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n)
	if n != 1 {
		t.Fatalf("removed bot must keep its archive, purged must not: %d messages", n)
	}
	if _, err := s.PurgeBot(ctx, purge); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second purge = %v", err)
	}
}

func TestChatSendersAndAvatar(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	ingest(t, s, bot, textMsg(1, "x"))
	cs, err := s.ChatSenders(ctx)
	if err != nil || len(cs) != 1 || cs[0].BotID != bot || cs[0].TgUserID != 42 {
		t.Fatalf("ChatSenders = %+v %v", cs, err)
	}
	if err := s.SetSenderAvatar(ctx, 42, "senders/42.jpg"); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/store/`
Expected: FAIL（`undefined: IngestInput` 等）

- [ ] **Step 3: 实现** `internal/store/ingest.go`

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"tgarchive/internal/model"
)

type IngestInput struct {
	BotID  int64
	Sender model.Sender
	Msg    *model.Message
	Offset int64 // when > 0, bots.update_offset advances to it in the same transaction
	Now    int64
}

type IngestResult struct {
	MessageID   int64
	ChatID      int64
	ChatCreated bool
	Created     bool     // false when an existing message was updated (edit)
	OrphanPaths []string // media files no longer referenced; caller deletes them
}

func (s *Store) Ingest(ctx context.Context, in IngestInput) (*IngestResult, error) {
	res := &IngestResult{}
	m := in.Msg
	ents := m.Entities
	if ents == nil {
		ents = []model.Entity{}
	}
	entJSON, err := json.Marshal(ents)
	if err != nil {
		return nil, err
	}
	fwd := ""
	if m.ForwardOrigin != nil {
		b, err := json.Marshal(m.ForwardOrigin)
		if err != nil {
			return nil, err
		}
		fwd = string(b)
	}

	err = s.withTx(ctx, func(tx *sql.Tx) error {
		snd := in.Sender
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO senders (tg_user_id, first_name, last_name, username, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (tg_user_id) DO UPDATE SET first_name = excluded.first_name, last_name = excluded.last_name,
				username = excluded.username, updated_at = excluded.updated_at`,
			snd.TgUserID, snd.FirstName, snd.LastName, snd.Username, in.Now); err != nil {
			return err
		}

		err := tx.QueryRowContext(ctx, "SELECT id FROM chats WHERE bot_id = ? AND sender_id = ?", in.BotID, snd.TgUserID).Scan(&res.ChatID)
		if errors.Is(err, sql.ErrNoRows) {
			if err := tx.QueryRowContext(ctx, "INSERT INTO chats (bot_id, sender_id) VALUES (?, ?) RETURNING id", in.BotID, snd.TgUserID).Scan(&res.ChatID); err != nil {
				return err
			}
			res.ChatCreated = true
		} else if err != nil {
			return err
		}

		var existing int64
		err = tx.QueryRowContext(ctx, "SELECT id FROM messages WHERE chat_id = ? AND source = ? AND tg_message_id = ?",
			res.ChatID, m.Source, m.TgMessageID).Scan(&existing)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO messages (chat_id, tg_message_id, source, media_group_id, date, edit_date, kind, text, entities_json,
					forward_origin_json, reply_to_tg_message_id, origin_chat_id, origin_chat_title, origin_link, extra_json, raw_format, raw_json)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
				res.ChatID, m.TgMessageID, m.Source, m.MediaGroupID, m.Date, m.EditDate, string(m.Kind), m.Text, string(entJSON),
				fwd, m.ReplyToTgMessageID, m.OriginChatID, m.OriginChatTitle, m.OriginLink, string(m.Extra), m.RawFormat, string(m.Raw),
			).Scan(&res.MessageID); err != nil {
				return err
			}
			res.Created = true
		case err != nil:
			return err
		default:
			res.MessageID = existing
			if _, err := tx.ExecContext(ctx, `
				UPDATE messages SET edit_date = ?, kind = ?, text = ?, entities_json = ?, extra_json = ?, raw_json = ? WHERE id = ?`,
				m.EditDate, string(m.Kind), m.Text, string(entJSON), string(m.Extra), string(m.Raw), existing); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM message_media WHERE message_id = ?", existing); err != nil {
				return err
			}
		}

		for i, md := range m.Media {
			mediaID, err := upsertMedia(ctx, tx, in.BotID, md)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO message_media (message_id, media_id, role, position) VALUES (?, ?, ?, ?)",
				res.MessageID, mediaID, md.Role, i); err != nil {
				return err
			}
		}

		if res.Created {
			if _, err := tx.ExecContext(ctx, "UPDATE chats SET last_message_at = ? WHERE id = ? AND last_message_at < ?", m.Date, res.ChatID, m.Date); err != nil {
				return err
			}
		} else {
			paths, err := collectOrphans(ctx, tx)
			if err != nil {
				return err
			}
			res.OrphanPaths = paths
		}

		if in.Offset > 0 {
			if _, err := tx.ExecContext(ctx, "UPDATE bots SET update_offset = ? WHERE id = ? AND update_offset < ?", in.Offset, in.BotID, in.Offset); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// upsertMedia dedupes by dedupe_key. A failed row seen again is requeued; a not-yet-done row
// takes the newest bot_id/source_ref because Bot API file_ids are only valid for the receiving bot.
func upsertMedia(ctx context.Context, tx *sql.Tx, botID int64, md model.Media) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO media (dedupe_key, bot_id, source_ref, kind, mime, file_name, size, width, height, duration, waveform)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (dedupe_key) DO UPDATE SET
			bot_id          = CASE WHEN media.state = 'done' THEN media.bot_id ELSE excluded.bot_id END,
			source_ref      = CASE WHEN media.state = 'done' THEN media.source_ref ELSE excluded.source_ref END,
			attempts        = CASE WHEN media.state = 'failed' THEN 0 ELSE media.attempts END,
			next_attempt_at = CASE WHEN media.state = 'failed' THEN 0 ELSE media.next_attempt_at END,
			error           = CASE WHEN media.state = 'failed' THEN '' ELSE media.error END,
			state           = CASE WHEN media.state = 'failed' THEN 'pending' ELSE media.state END
		RETURNING id`,
		md.DedupeKey, botID, md.SourceRef, md.Kind, md.Mime, md.FileName, md.Size, md.Width, md.Height, md.Duration, md.Waveform).Scan(&id)
	return id, err
}

// collectOrphans deletes media rows no message links to and returns their stored paths.
func collectOrphans(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id, path FROM media WHERE NOT EXISTS (SELECT 1 FROM message_media mm WHERE mm.media_id = media.id)")
	if err != nil {
		return nil, err
	}
	var ids []int64
	paths := []string{}
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		if p != "" {
			paths = append(paths, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, "DELETE FROM media WHERE id = ?", id); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

// DeleteMessage soft-deletes a message and drops its media links; unreferenced media are removed.
func (s *Store) DeleteMessage(ctx context.Context, id, now int64) (int64, []string, error) {
	var chatID int64
	var orphans []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT chat_id FROM messages WHERE id = ? AND deleted_at = 0", id).Scan(&chatID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE messages SET deleted_at = ? WHERE id = ?", now, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM message_media WHERE message_id = ?", id); err != nil {
			return err
		}
		orphans, err = collectOrphans(ctx, tx)
		return err
	})
	return chatID, orphans, err
}

// PurgeBot deletes a bot with all its chats, messages and now-unreferenced media.
func (s *Store) PurgeBot(ctx context.Context, id int64) ([]string, error) {
	var orphans []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := affected(tx.ExecContext(ctx, "DELETE FROM bots WHERE id = ?", id)); err != nil {
			return err
		}
		var err error
		orphans, err = collectOrphans(ctx, tx)
		return err
	})
	return orphans, err
}
```

`internal/store/media.go`

```go
package store

import (
	"context"
	"database/sql"
	"errors"
)

type Media struct {
	ID            int64
	DedupeKey     string
	BotID         int64
	SourceRef     string
	Kind          string
	Mime          string
	FileName      string
	Size          int64
	Width         int
	Height        int
	Duration      int
	Waveform      []byte
	Path          string
	State         string
	Attempts      int
	NextAttemptAt int64
	Error         string
}

const mediaCols = "id, dedupe_key, bot_id, source_ref, kind, mime, file_name, size, width, height, duration, waveform, path, state, attempts, next_attempt_at, error"

func scanMedia(r scanner) (*Media, error) {
	var m Media
	err := r.Scan(&m.ID, &m.DedupeKey, &m.BotID, &m.SourceRef, &m.Kind, &m.Mime, &m.FileName, &m.Size, &m.Width, &m.Height,
		&m.Duration, &m.Waveform, &m.Path, &m.State, &m.Attempts, &m.NextAttemptAt, &m.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) GetMedia(ctx context.Context, id int64) (*Media, error) {
	return scanMedia(s.db.QueryRowContext(ctx, "SELECT "+mediaCols+" FROM media WHERE id = ?", id))
}

func (s *Store) DueMedia(ctx context.Context, now int64, limit int) ([]Media, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+mediaCols+" FROM media WHERE state = 'pending' AND next_attempt_at <= ? ORDER BY id LIMIT ?", now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Media{}
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// MarkMediaDone returns false when the row no longer exists (deleted while downloading).
func (s *Store) MarkMediaDone(ctx context.Context, id int64, path string, size int64) (bool, error) {
	err := affected(s.db.ExecContext(ctx, "UPDATE media SET state = 'done', path = ?, size = ?, error = '' WHERE id = ?", path, size, id))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) MarkMediaRetry(ctx context.Context, id int64, attempts int, nextAt int64, errMsg string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE media SET attempts = ?, next_attempt_at = ?, error = ? WHERE id = ?", attempts, nextAt, errMsg, id)
	return err
}

func (s *Store) MarkMediaFailed(ctx context.Context, id int64, attempts int, errMsg string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE media SET state = 'failed', attempts = ?, error = ? WHERE id = ?", attempts, errMsg, id)
	return err
}

func (s *Store) MarkMediaTooLarge(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE media SET state = 'too_large', error = '' WHERE id = ?", id)
	return err
}

func (s *Store) ResetMedia(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx,
		"UPDATE media SET state = 'pending', attempts = 0, next_attempt_at = 0, error = '' WHERE id = ? AND state = 'failed'", id))
}

func (s *Store) MessagesForMedia(ctx context.Context, mediaID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT mm.message_id FROM message_media mm JOIN messages m ON m.id = mm.message_id
		WHERE mm.media_id = ? AND m.deleted_at = 0 ORDER BY mm.message_id`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type MediaStatus struct{ State, Error string }

type ReceiptInfo struct {
	MessageID, BotID, TgChatID, TgMessageID int64
	Source, Receipt                         string
	Main                                    []MediaStatus
}

func (s *Store) GetReceiptInfo(ctx context.Context, messageID int64) (*ReceiptInfo, error) {
	var ri ReceiptInfo
	err := s.db.QueryRowContext(ctx, `
		SELECT m.id, c.bot_id, c.sender_id, m.tg_message_id, m.source, m.receipt
		FROM messages m JOIN chats c ON c.id = m.chat_id WHERE m.id = ? AND m.deleted_at = 0`, messageID,
	).Scan(&ri.MessageID, &ri.BotID, &ri.TgChatID, &ri.TgMessageID, &ri.Source, &ri.Receipt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT md.state, md.error FROM message_media mm JOIN media md ON md.id = mm.media_id
		WHERE mm.message_id = ? AND mm.role = 'main' ORDER BY mm.position`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ms MediaStatus
		if err := rows.Scan(&ms.State, &ms.Error); err != nil {
			return nil, err
		}
		ri.Main = append(ri.Main, ms)
	}
	return &ri, rows.Err()
}

func (s *Store) SetReceipt(ctx context.Context, messageID int64, receipt string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE messages SET receipt = ? WHERE id = ?", receipt, messageID)
	return err
}
```

`internal/store/senders.go`

```go
package store

import "context"

type ChatSender struct{ BotID, TgUserID int64 }

func (s *Store) SetSenderAvatar(ctx context.Context, tgUserID int64, path string) error {
	return affected(s.db.ExecContext(ctx, "UPDATE senders SET avatar_path = ? WHERE tg_user_id = ?", path, tgUserID))
}

// ChatSenders returns one (bot, sender) pair per sender, using a bot that can still make API calls.
func (s *Store) ChatSenders(ctx context.Context) ([]ChatSender, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT MIN(c.bot_id), c.sender_id FROM chats c JOIN bots b ON b.id = c.bot_id
		WHERE b.status != 'removed' GROUP BY c.sender_id ORDER BY c.sender_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatSender{}
	for rows.Next() {
		var cs ChatSender
		if err := rows.Scan(&cs.BotID, &cs.TgUserID); err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/store/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat(store): ingest transaction, media state machine, deletion"
```

---

### Task 7: 查询视图（会话、消息分页、共享媒体）

**Files:**
- Create: `internal/store/query.go`, `internal/store/query_test.go`

**Interfaces:**
- Consumes: Task 6 的表与 `Ingest`
- Produces（JSON tag 即前端 API 字段名，计划 3 依赖）：
  - `store.SenderView{TgUserID int64 "tg_user_id"; FirstName "first_name"; LastName "last_name"; Username "username"; HasAvatar bool "has_avatar"}`
  - `store.ChatView{ID "id"; BotID "bot_id"; Sender SenderView "sender"; LastMessageAt "last_message_at"; LastKind "last_kind"; LastText "last_text"}`
  - `store.MediaView{ID "id"; Role "role"; Kind "kind"; Mime "mime"; FileName "file_name"; Size "size"; Width "width"; Height "height"; Duration "duration"; Waveform []byte "waveform,omitempty"; State "state"; Error "error"}`
  - `store.ReplyView{ID "id"; Kind "kind"; Text "text"}`
  - `store.MessageView{ID "id"; TgMessageID "tg_message_id"; Source "source"; MediaGroupID "media_group_id"; Date "date"; EditDate "edit_date"; Kind "kind"; Text "text"; Entities json.RawMessage "entities"; ForwardOrigin json.RawMessage "forward_origin,omitempty"; ReplyToTgMessageID "reply_to_tg_message_id"; Reply *ReplyView "reply,omitempty"; OriginChatTitle "origin_chat_title"; OriginLink "origin_link"; Extra json.RawMessage "extra,omitempty"; Media []MediaView "media"}`
  - `ListChats(ctx, botID int64) ([]ChatView, error)`（`botID == 0` 表示全部）
  - `ListMessages(ctx, chatID, beforeID int64, limit int) ([]MessageView, error)`：按 id 升序返回；`beforeID == 0` 取最新一页
  - `ListChatMedia(ctx, chatID int64, typ string, beforeID int64, limit int) ([]MessageView, error)`：`typ ∈ {media, file, link}`，按 id 降序；未知类型返回 `store.ErrBadMediaType`

- [ ] **Step 1: 写失败测试** `internal/store/query_test.go`

```go
package store

import (
	"errors"
	"testing"

	"tgarchive/internal/model"
)

func ids(vs []MessageView) []int64 {
	out := []int64{}
	for _, v := range vs {
		out = append(out, v.TgMessageID)
	}
	return out
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListChats(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	bob := model.Sender{TgUserID: 7, FirstName: "Bob"}
	ingest(t, s, bot, textMsg(1, "from alice"))
	if _, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: bob, Msg: textMsg(50, "from bob"), Now: 1}); err != nil {
		t.Fatal(err)
	}
	chats, err := s.ListChats(ctx, 0)
	if err != nil || len(chats) != 2 {
		t.Fatalf("chats = %+v, %v", chats, err)
	}
	if chats[0].Sender.FirstName != "Bob" || chats[0].LastText != "from bob" || chats[0].LastKind != "text" || chats[0].BotID != bot {
		t.Fatalf("newest chat first: %+v", chats[0])
	}
	if other, _ := s.ListChats(ctx, bot+1); len(other) != 0 {
		t.Fatal("bot filter not applied")
	}
}

func TestListMessagesPaging(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	var chat int64
	for i := int64(1); i <= 5; i++ {
		chat = ingest(t, s, bot, textMsg(i, "m")).ChatID
	}
	page, err := s.ListMessages(ctx, chat, 0, 2)
	if err != nil || !eq(ids(page), []int64{4, 5}) {
		t.Fatalf("latest page = %v, %v", ids(page), err)
	}
	older, _ := s.ListMessages(ctx, chat, page[0].ID, 2)
	if !eq(ids(older), []int64{2, 3}) {
		t.Fatalf("older page = %v", ids(older))
	}
	if string(page[0].Entities) != "[]" || page[0].Media == nil {
		t.Fatalf("entities/media must be non-null: %s %v", page[0].Entities, page[0].Media)
	}
}

func TestListMessagesKeepsAlbumWhole(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	chat := ingest(t, s, bot, textMsg(1, "before")).ChatID
	for i, k := range []string{"bot:a", "bot:b", "bot:c"} {
		m := photoMsg(int64(2+i), k)
		m.MediaGroupID = "g"
		ingest(t, s, bot, m)
	}
	page, _ := s.ListMessages(ctx, chat, 0, 2)
	if !eq(ids(page), []int64{2, 3, 4}) {
		t.Fatalf("album split by page boundary: %v", ids(page))
	}
}

func TestHydrateMediaAndReply(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	first := ingest(t, s, bot, textMsg(1, "original"))
	m := photoMsg(2, "bot:main")
	m.Media = append(m.Media, model.Media{DedupeKey: "bot:thumb", SourceRef: "t", Kind: "photo", Role: model.RoleThumb})
	m.ReplyToTgMessageID = 1
	ingest(t, s, bot, m)
	page, _ := s.ListMessages(ctx, first.ChatID, 0, 10)
	got := page[1]
	if got.Reply == nil || got.Reply.ID != first.MessageID || got.Reply.Text != "original" {
		t.Fatalf("reply = %+v", got.Reply)
	}
	if len(got.Media) != 2 || got.Media[0].Role != model.RoleMain || got.Media[1].Role != model.RoleThumb || got.Media[0].State != StatePending {
		t.Fatalf("media = %+v", got.Media)
	}
	s.DeleteMessage(ctx, first.MessageID, 1)
	page, _ = s.ListMessages(ctx, first.ChatID, 0, 10)
	if len(page) != 1 || page[0].Reply != nil {
		t.Fatalf("deleted message must disappear and not be quoted: %+v", page)
	}
}

func TestListChatMedia(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	chat := ingest(t, s, bot, photoMsg(1, "bot:p")).ChatID
	doc := &model.Message{TgMessageID: 2, Source: model.SourceBotUpdate, Date: 2, Kind: model.KindDocument, RawFormat: model.RawBotAPI, Raw: []byte(`{}`),
		Media: []model.Media{{DedupeKey: "bot:d", Kind: "document", Role: model.RoleMain}}}
	ingest(t, s, bot, doc)
	link := textMsg(3, "see https://example.com")
	link.Entities = []model.Entity{{Type: "url", Offset: 4, Length: 19}}
	ingest(t, s, bot, link)
	ingest(t, s, bot, textMsg(4, "plain"))
	for typ, want := range map[string][]int64{"media": {1}, "file": {2}, "link": {3}} {
		got, err := s.ListChatMedia(ctx, chat, typ, 0, 50)
		if err != nil || !eq(ids(got), want) {
			t.Fatalf("%s = %v, %v", typ, ids(got), err)
		}
	}
	if _, err := s.ListChatMedia(ctx, chat, "bogus", 0, 50); !errors.Is(err, ErrBadMediaType) {
		t.Fatalf("bad type err = %v", err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/store/ -run 'TestList|TestHydrate'`
Expected: FAIL（`undefined: ListChats` 等）

- [ ] **Step 3: 实现** `internal/store/query.go`

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"tgarchive/internal/model"
)

var ErrBadMediaType = errors.New("unknown shared media type")

type SenderView struct {
	TgUserID  int64  `json:"tg_user_id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
	HasAvatar bool   `json:"has_avatar"`
}

type ChatView struct {
	ID            int64      `json:"id"`
	BotID         int64      `json:"bot_id"`
	Sender        SenderView `json:"sender"`
	LastMessageAt int64      `json:"last_message_at"`
	LastKind      string     `json:"last_kind"`
	LastText      string     `json:"last_text"`
}

type MediaView struct {
	ID       int64  `json:"id"`
	Role     string `json:"role"`
	Kind     string `json:"kind"`
	Mime     string `json:"mime"`
	FileName string `json:"file_name"`
	Size     int64  `json:"size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Duration int    `json:"duration"`
	Waveform []byte `json:"waveform,omitempty"`
	State    string `json:"state"`
	Error    string `json:"error"`
}

type ReplyView struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type MessageView struct {
	ID                 int64           `json:"id"`
	TgMessageID        int64           `json:"tg_message_id"`
	Source             string          `json:"source"`
	MediaGroupID       string          `json:"media_group_id"`
	Date               int64           `json:"date"`
	EditDate           int64           `json:"edit_date"`
	Kind               string          `json:"kind"`
	Text               string          `json:"text"`
	Entities           json.RawMessage `json:"entities"`
	ForwardOrigin      json.RawMessage `json:"forward_origin,omitempty"`
	ReplyToTgMessageID int64           `json:"reply_to_tg_message_id"`
	Reply              *ReplyView      `json:"reply,omitempty"`
	OriginChatTitle    string          `json:"origin_chat_title"`
	OriginLink         string          `json:"origin_link"`
	Extra              json.RawMessage `json:"extra,omitempty"`
	Media              []MediaView     `json:"media"`
}

func (s *Store) ListChats(ctx context.Context, botID int64) ([]ChatView, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.bot_id, c.last_message_at, s.tg_user_id, s.first_name, s.last_name, s.username, s.avatar_path,
			COALESCE((SELECT m.kind FROM messages m WHERE m.chat_id = c.id AND m.deleted_at = 0 ORDER BY m.id DESC LIMIT 1), ''),
			COALESCE((SELECT substr(m.text, 1, 200) FROM messages m WHERE m.chat_id = c.id AND m.deleted_at = 0 ORDER BY m.id DESC LIMIT 1), '')
		FROM chats c JOIN senders s ON s.tg_user_id = c.sender_id
		WHERE (? = 0 OR c.bot_id = ?)
		ORDER BY c.last_message_at DESC, c.id DESC`, botID, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatView{}
	for rows.Next() {
		var v ChatView
		var avatar string
		if err := rows.Scan(&v.ID, &v.BotID, &v.LastMessageAt, &v.Sender.TgUserID, &v.Sender.FirstName, &v.Sender.LastName,
			&v.Sender.Username, &avatar, &v.LastKind, &v.LastText); err != nil {
			return nil, err
		}
		v.Sender.HasAvatar = avatar != ""
		out = append(out, v)
	}
	return out, rows.Err()
}

const msgCols = `id, tg_message_id, source, media_group_id, date, edit_date, kind, text, entities_json, forward_origin_json,
	reply_to_tg_message_id, origin_chat_title, origin_link, extra_json`

func scanMessageView(r scanner) (MessageView, error) {
	var v MessageView
	var ents, fwd, extra string
	err := r.Scan(&v.ID, &v.TgMessageID, &v.Source, &v.MediaGroupID, &v.Date, &v.EditDate, &v.Kind, &v.Text, &ents, &fwd,
		&v.ReplyToTgMessageID, &v.OriginChatTitle, &v.OriginLink, &extra)
	if ents == "" {
		ents = "[]"
	}
	v.Entities = json.RawMessage(ents)
	v.ForwardOrigin = rawOrNil(fwd)
	v.Extra = rawOrNil(extra)
	return v, err
}

func rawOrNil(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

func collectViews(rows *sql.Rows, err error) ([]MessageView, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MessageView{}
	for rows.Next() {
		v, err := scanMessageView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) ListMessages(ctx context.Context, chatID, beforeID int64, limit int) ([]MessageView, error) {
	views, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages
		WHERE chat_id = ? AND deleted_at = 0 AND (? = 0 OR id < ?) ORDER BY id DESC LIMIT ?`, chatID, beforeID, beforeID, limit))
	if err != nil {
		return nil, err
	}
	// Never cut an album at the page boundary: pull in the rest of the oldest message's group.
	if n := len(views); n > 0 && views[n-1].MediaGroupID != "" {
		oldest := views[n-1]
		more, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages
			WHERE chat_id = ? AND deleted_at = 0 AND media_group_id = ? AND id < ? ORDER BY id DESC`, chatID, oldest.MediaGroupID, oldest.ID))
		if err != nil {
			return nil, err
		}
		views = append(views, more...)
	}
	slices.Reverse(views)
	if err := s.hydrate(ctx, chatID, views); err != nil {
		return nil, err
	}
	return views, nil
}

func (s *Store) ListChatMedia(ctx context.Context, chatID int64, typ string, beforeID int64, limit int) ([]MessageView, error) {
	var cond string
	switch typ {
	case "media":
		cond = `id IN (SELECT mm.message_id FROM message_media mm JOIN media md ON md.id = mm.media_id
			WHERE mm.role = 'main' AND md.kind IN ('photo', 'video', 'animation'))`
	case "file":
		cond = `id IN (SELECT mm.message_id FROM message_media mm JOIN media md ON md.id = mm.media_id
			WHERE mm.role = 'main' AND md.kind IN ('document', 'audio'))`
	case "link":
		cond = `(entities_json LIKE '%"type":"url"%' OR entities_json LIKE '%"type":"text_link"%')`
	default:
		return nil, ErrBadMediaType
	}
	views, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages
		WHERE chat_id = ? AND deleted_at = 0 AND (? = 0 OR id < ?) AND `+cond+` ORDER BY id DESC LIMIT ?`,
		chatID, beforeID, beforeID, limit))
	if err != nil {
		return nil, err
	}
	if err := s.hydrate(ctx, chatID, views); err != nil {
		return nil, err
	}
	return views, nil
}

func (s *Store) hydrate(ctx context.Context, chatID int64, views []MessageView) error {
	if len(views) == 0 {
		return nil
	}
	idx := make(map[int64]int, len(views))
	args := make([]any, 0, len(views))
	for i, v := range views {
		idx[v.ID] = i
		args = append(args, v.ID)
		views[i].Media = []MediaView{}
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(views)), ",")
	rows, err := s.db.QueryContext(ctx, `
		SELECT mm.message_id, mm.role, md.id, md.kind, md.mime, md.file_name, md.size, md.width, md.height, md.duration,
			md.waveform, md.state, md.error
		FROM message_media mm JOIN media md ON md.id = mm.media_id
		WHERE mm.message_id IN (`+ph+`) ORDER BY mm.message_id, mm.position`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var msgID int64
		var mv MediaView
		if err := rows.Scan(&msgID, &mv.Role, &mv.ID, &mv.Kind, &mv.Mime, &mv.FileName, &mv.Size, &mv.Width, &mv.Height,
			&mv.Duration, &mv.Waveform, &mv.State, &mv.Error); err != nil {
			rows.Close()
			return err
		}
		i := idx[msgID]
		views[i].Media = append(views[i].Media, mv)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range views {
		r := views[i].ReplyToTgMessageID
		if r == 0 {
			continue
		}
		var rv ReplyView
		err := s.db.QueryRowContext(ctx, `SELECT id, kind, substr(text, 1, 200) FROM messages
			WHERE chat_id = ? AND source = ? AND tg_message_id = ? AND deleted_at = 0`, chatID, model.SourceBotUpdate, r).Scan(&rv.ID, &rv.Kind, &rv.Text)
		if err == nil {
			views[i].Reply = &rv
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/store/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat(store): chat, message and shared-media views"
```

---

### Task 8: Bot API 目录映射与客户端注册表

**Files:**
- Create: `internal/botapifs/botapifs.go`, `internal/botapifs/botapifs_test.go`, `internal/botclients/registry.go`, `internal/botclients/registry_test.go`

**Interfaces:**
- Consumes: `store.Store.GetBot`、`seal.Box.Open`、`tgbot.New`
- Produces:
  - `botapifs.Mapper{Remote, Local string}`；`(Mapper).Map(remote string) (string, error)`；`(Mapper).CleanOlderThan(age time.Duration, now time.Time) (int, error)`；`botapifs.LinkOrCopy(src, dst string) error`
  - `botclients.New(st *store.Store, box *seal.Box, baseURL string, hc *http.Client) *botclients.Registry`；`(*Registry).Get(ctx, botID int64) (*tgbot.Client, error)`；`(*Registry).Forget(botID int64)`；`(*Registry).SetReaction(ctx, botID, chatID, msgID int64, emoji string) error`；`(*Registry).Reply(ctx, botID, chatID, msgID int64, text string) error`

本地 Bot API 服务器的工作目录结构是 `<root>/<bot_id>:<token>/` 下放自身状态（`td.binlog` 等），下载的文件在 `<root>/<bot_id>:<token>/<分类>/<文件>`。清理只能碰深度 ≥ 3 的文件。

- [ ] **Step 1: 写失败测试** `internal/botapifs/botapifs_test.go`

```go
package botapifs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, p string, mtime time.Time) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(p, mtime, mtime)
}

func TestMap(t *testing.T) {
	m := Mapper{Remote: "/var/lib/telegram-bot-api", Local: "/data/botapi"}
	got, err := m.Map("/var/lib/telegram-bot-api/1:tok/photos/file_0.jpg")
	if err != nil || got != "/data/botapi/1:tok/photos/file_0.jpg" {
		t.Fatalf("Map = %q, %v", got, err)
	}
	for _, bad := range []string{"/etc/passwd", "/var/lib/telegram-bot-api/../x", "/var/lib/telegram-bot-api", "relative/x"} {
		if _, err := m.Map(bad); err == nil {
			t.Fatalf("Map(%q) must fail", bad)
		}
	}
}

func TestCleanOlderThanKeepsServerState(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	write(t, filepath.Join(root, "1:tok", "td.binlog"), old)
	write(t, filepath.Join(root, "1:tok", "photos", "old.jpg"), old)
	write(t, filepath.Join(root, "1:tok", "photos", "new.jpg"), now)
	n, err := Mapper{Local: root}.CleanOlderThan(24*time.Hour, now)
	if err != nil || n != 1 {
		t.Fatalf("removed %d, %v", n, err)
	}
	for p, want := range map[string]bool{"1:tok/td.binlog": true, "1:tok/photos/old.jpg": false, "1:tok/photos/new.jpg": true} {
		_, err := os.Stat(filepath.Join(root, p))
		if (err == nil) != want {
			t.Fatalf("%s exists=%v, want %v", p, err == nil, want)
		}
	}
	if n, err := (Mapper{Local: filepath.Join(root, "missing")}).CleanOlderThan(time.Hour, now); n != 0 || err != nil {
		t.Fatalf("missing dir = %d %v", n, err)
	}
}

func TestLinkOrCopy(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a"), filepath.Join(dir, "sub", "b")
	os.WriteFile(src, []byte("data"), 0o644)
	os.MkdirAll(filepath.Dir(dst), 0o755)
	os.WriteFile(dst, []byte("stale"), 0o644)
	if err := LinkOrCopy(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "data" {
		t.Fatalf("dst = %q", b)
	}
}
```

- [ ] **Step 2: 写失败测试** `internal/botclients/registry_test.go`

```go
package botclients

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
)

const token = "777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestRegistry(t *testing.T) {
	ctx := context.Background()
	f := tgtest.New(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	id, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: box.Seal([]byte(token)), CreatedAt: 1})
	r := New(st, box, f.URL(), nil)

	if err := r.SetReaction(ctx, id, 42, 10, "👌"); err != nil {
		t.Fatal(err)
	}
	if err := r.Reply(ctx, id, 42, 10, "hello"); err != nil {
		t.Fatal(err)
	}
	if c := f.Calls("setMessageReaction"); len(c) != 1 || c[0].Token != token {
		t.Fatalf("calls = %+v", c)
	}
	if c := f.Calls("sendMessage"); len(c) != 1 || c[0].Params["text"] != "hello" {
		t.Fatalf("sendMessage = %+v", c)
	}

	st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: box.Seal([]byte("777:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")), CreatedAt: 1})
	r.Forget(id)
	r.SetReaction(ctx, id, 42, 11, "👌")
	if c := f.Calls("setMessageReaction"); c[1].Token != "777:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB" {
		t.Fatal("Forget must drop the cached client")
	}

	st.RemoveBot(ctx, id)
	r.Forget(id)
	if _, err := r.Get(ctx, id); err == nil {
		t.Fatal("removed bot must not yield a client")
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/botapifs/ ./internal/botclients/`
Expected: FAIL（未定义）

- [ ] **Step 4: 实现** `internal/botapifs/botapifs.go`

```go
// Package botapifs handles the directory shared with the local Bot API server.
package botapifs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Mapper translates paths returned by getFile (inside the Bot API container)
// into paths inside this container.
type Mapper struct {
	Remote string
	Local  string
}

func (m Mapper) Map(remote string) (string, error) {
	if !filepath.IsAbs(remote) {
		return "", fmt.Errorf("bot api file path %q is not absolute", remote)
	}
	rel, err := filepath.Rel(m.Remote, filepath.Clean(remote))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("bot api file path %q is outside %s", remote, m.Remote)
	}
	return filepath.Join(m.Local, rel), nil
}

// CleanOlderThan removes downloaded files older than age. Only files at depth >= 3
// (<bot>/<category>/<file>) are touched; the server's own state such as <bot>/td.binlog is kept.
func (m Mapper) CleanOlderThan(age time.Duration, now time.Time) (int, error) {
	if _, err := os.Stat(m.Local); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	n := 0
	err := filepath.WalkDir(m.Local, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(m.Local, p)
		if err != nil || len(strings.Split(rel, string(filepath.Separator))) < 3 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if now.Sub(info.ModTime()) > age && os.Remove(p) == nil {
			n++
		}
		return nil
	})
	return n, err
}

// LinkOrCopy places src at dst, replacing dst. A hard link is tried first (same filesystem),
// falling back to copy-then-rename so dst is never observed half-written.
func LinkOrCopy(src, dst string) error {
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
```

`internal/botclients/registry.go`

```go
// Package botclients builds Bot API clients from stored, encrypted tokens.
package botclients

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgbot"
)

type Registry struct {
	st      *store.Store
	box     *seal.Box
	baseURL string
	hc      *http.Client

	mu    sync.Mutex
	cache map[int64]*tgbot.Client
}

func New(st *store.Store, box *seal.Box, baseURL string, hc *http.Client) *Registry {
	return &Registry{st: st, box: box, baseURL: baseURL, hc: hc, cache: map[int64]*tgbot.Client{}}
}

func (r *Registry) Get(ctx context.Context, botID int64) (*tgbot.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cache[botID]; ok {
		return c, nil
	}
	b, err := r.st.GetBot(ctx, botID)
	if err != nil {
		return nil, err
	}
	if len(b.TokenEnc) == 0 {
		return nil, errors.New("bot has no token (removed)")
	}
	tok, err := r.box.Open(b.TokenEnc)
	if err != nil {
		return nil, errors.New("cannot decrypt bot token; TOKEN_ENC_KEY changed?")
	}
	c := tgbot.New(r.baseURL, string(tok), r.hc)
	r.cache[botID] = c
	return c, nil
}

func (r *Registry) Forget(botID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, botID)
}

func (r *Registry) SetReaction(ctx context.Context, botID, chatID, msgID int64, emoji string) error {
	c, err := r.Get(ctx, botID)
	if err != nil {
		return err
	}
	return c.SetMessageReaction(ctx, chatID, msgID, emoji)
}

func (r *Registry) Reply(ctx context.Context, botID, chatID, msgID int64, text string) error {
	c, err := r.Get(ctx, botID)
	if err != nil {
		return err
	}
	return c.SendMessage(ctx, chatID, text, msgID)
}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/botapifs/ ./internal/botclients/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/botapifs internal/botclients
git commit -m "feat: bot api dir mapping/cleanup and client registry"
```

---

### Task 9: 下载队列与 BotSource

**Files:**
- Create: `internal/downloader/downloader.go`, `internal/downloader/botsource.go`, `internal/downloader/downloader_test.go`

**Interfaces:**
- Consumes: `store.Media`、`DueMedia`、`MarkMedia*`；`botapifs.Mapper`、`botapifs.LinkOrCopy`；`tgbot.Client.GetFile`
- Produces:
  - `downloader.ErrTooLarge`
  - `downloader.Source` 接口：`Fetch(ctx, m *store.Media, dstBase string) (path string, size int64, err error)`（`dstBase` 为不带扩展名的绝对路径，Source 自行决定扩展名）
  - `downloader.New(st *store.Store, mediaDir string, maxBytes int64, onSettled func(mediaID int64)) *downloader.Downloader`；可调字段 `Concurrency int`（默认 4）、`Delays []time.Duration`（默认 1m/5m/30m）、`Now func() time.Time`
  - `(*Downloader).Register(prefix string, s Source)`、`Wake()`、`Run(ctx)`（阻塞至 ctx 结束）、`Process(ctx, m *store.Media)`
  - `downloader.RemoveFiles(mediaDir string, rels []string)`
  - `downloader.BotSource{Clients func(ctx context.Context, botID int64) (*tgbot.Client, error); Mapper botapifs.Mapper}`
- `onSettled` 在媒体进入终态（done / failed / too_large）时调用；重试排期不调用

- [ ] **Step 1: 写失败测试** `internal/downloader/downloader_test.go`

```go
package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"tgarchive/internal/botapifs"
	"tgarchive/internal/botclients"
	"tgarchive/internal/model"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
)

var ctx = context.Background()

type fakeSource struct {
	mu    sync.Mutex
	errs  []error
	calls int
	hook  func()
}

func (f *fakeSource) Fetch(_ context.Context, m *store.Media, dstBase string) (string, int64, error) {
	f.mu.Lock()
	f.calls++
	var err error
	if len(f.errs) > 0 {
		err, f.errs = f.errs[0], f.errs[1:]
	}
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return "", 0, err
	}
	p := dstBase + ".jpg"
	return p, 4, os.WriteFile(p, []byte("data"), 0o644)
}

type fixture struct {
	st       *store.Store
	bot      int64
	mediaDir string
	settled  []int64
	mu       sync.Mutex
}

func setup(t *testing.T, size int64) (*fixture, *store.IngestResult, *store.Media) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bot, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	msg := &model.Message{TgMessageID: 1, Source: model.SourceBotUpdate, Date: 1, Kind: model.KindPhoto, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`),
		Media: []model.Media{{DedupeKey: "bot:k", SourceRef: "fid", Kind: "photo", Size: size, Role: model.RoleMain}}}
	res, err := st.Ingest(ctx, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Msg: msg, Now: 1})
	if err != nil {
		t.Fatal(err)
	}
	due, _ := st.DueMedia(ctx, 0, 1)
	return &fixture{st: st, bot: bot, mediaDir: t.TempDir()}, res, &due[0]
}

func (f *fixture) newDL(maxBytes int64) *Downloader {
	d := New(f.st, f.mediaDir, maxBytes, func(id int64) { f.mu.Lock(); f.settled = append(f.settled, id); f.mu.Unlock() })
	d.Now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	return d
}

func TestProcessSuccess(t *testing.T) {
	f, _, m := setup(t, 4)
	d := f.newDL(0)
	d.Register("bot", &fakeSource{})
	d.Process(ctx, m)
	got, _ := f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateDone || got.Size != 4 {
		t.Fatalf("media = %+v", got)
	}
	if !regexp.MustCompile(`^\d+/2026/10/[0-9a-f]{40}\.jpg$`).MatchString(got.Path) {
		t.Fatalf("path = %q", got.Path)
	}
	if _, err := os.Stat(filepath.Join(f.mediaDir, got.Path)); err != nil {
		t.Fatal(err)
	}
	if len(f.settled) != 1 || f.settled[0] != m.ID {
		t.Fatalf("settled = %v", f.settled)
	}
}

func TestRetrySchedule(t *testing.T) {
	f, _, m := setup(t, 4)
	d := f.newDL(0)
	boom := errors.New("network down")
	d.Register("bot", &fakeSource{errs: []error{boom, boom, boom, boom}})
	base := d.Now().Unix()
	for i, wantDelay := range []int64{60, 300, 1800} {
		cur, _ := f.st.GetMedia(ctx, m.ID)
		d.Process(ctx, cur)
		got, _ := f.st.GetMedia(ctx, m.ID)
		if got.State != store.StatePending || got.Attempts != i+1 || got.NextAttemptAt != base+wantDelay || got.Error != "network down" {
			t.Fatalf("after failure %d: %+v", i+1, got)
		}
	}
	if len(f.settled) != 0 {
		t.Fatal("retries must not settle")
	}
	cur, _ := f.st.GetMedia(ctx, m.ID)
	d.Process(ctx, cur)
	got, _ := f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateFailed || got.Attempts != 4 || len(f.settled) != 1 {
		t.Fatalf("4th failure: %+v settled=%v", got, f.settled)
	}
}

func TestTooLargeSkipsFetch(t *testing.T) {
	f, _, m := setup(t, 100)
	d := f.newDL(10)
	src := &fakeSource{}
	d.Register("bot", src)
	d.Process(ctx, m)
	got, _ := f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateTooLarge || src.calls != 0 || len(f.settled) != 1 {
		t.Fatalf("media = %+v calls = %d", got, src.calls)
	}
}

func TestDeletedWhileDownloadingRemovesFile(t *testing.T) {
	f, res, m := setup(t, 4)
	d := f.newDL(0)
	d.Register("bot", &fakeSource{hook: func() { f.st.DeleteMessage(ctx, res.MessageID, 1) }})
	d.Process(ctx, m)
	entries, _ := os.ReadDir(filepath.Join(f.mediaDir, "1", "2026", "10"))
	if len(entries) != 0 {
		t.Fatalf("orphaned download left on disk: %v", entries)
	}
}

func TestRunPicksUpWork(t *testing.T) {
	f, _, m := setup(t, 4)
	d := f.newDL(0)
	d.Register("bot", &fakeSource{})
	c, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { d.Run(c); close(done) }()
	d.Wake()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, _ := f.st.GetMedia(ctx, m.ID)
		if got.State == store.StateDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not process pending media")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestBotSource(t *testing.T) {
	fake := tgtest.New(t)
	fake.AddFile("fid", []byte("img!"))
	f, _, m := setup(t, 4)
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	f.st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: box.Seal([]byte("777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")), CreatedAt: 1})
	reg := botclients.New(f.st, box, fake.URL(), nil)
	src := &BotSource{Clients: reg.Get, Mapper: botapifs.Mapper{Remote: fake.RemoteDir, Local: fake.RemoteDir}}
	dstBase := filepath.Join(t.TempDir(), "out")
	p, size, err := src.Fetch(ctx, m, dstBase)
	if err != nil || p != dstBase+".jpg" || size != 4 {
		t.Fatalf("Fetch = %q %d %v", p, size, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "img!" {
		t.Fatalf("content = %q", b)
	}
	matches, _ := filepath.Glob(filepath.Join(fake.RemoteDir, "*", "documents", "fid.jpg"))
	if len(matches) != 0 {
		t.Fatal("bot api cache file must be removed after archiving")
	}
	bad := &BotSource{Clients: reg.Get, Mapper: botapifs.Mapper{Remote: "/nowhere", Local: "/nowhere"}}
	if _, _, err := bad.Fetch(ctx, m, dstBase); err == nil {
		t.Fatal("path outside the shared dir must fail")
	}
}

func TestRemoveFilesStaysInside(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "keep")
	os.WriteFile(outside, []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("x"), 0o644)
	rel, _ := filepath.Rel(dir, outside)
	RemoveFiles(dir, []string{"a.jpg", rel, "missing.jpg"})
	if _, err := os.Stat(filepath.Join(dir, "a.jpg")); err == nil {
		t.Fatal("a.jpg not removed")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("RemoveFiles escaped mediaDir")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/downloader/`
Expected: FAIL（未定义）

- [ ] **Step 3: 实现** `internal/downloader/downloader.go`

```go
// Package downloader moves pending media into the archive with retries and dedupe.
package downloader

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tgarchive/internal/store"
)

var ErrTooLarge = errors.New("file exceeds the archive size limit")

type Source interface {
	// Fetch stores the file for m at dstBase plus an extension of the source's choosing.
	Fetch(ctx context.Context, m *store.Media, dstBase string) (path string, size int64, err error)
}

type Downloader struct {
	st        *store.Store
	mediaDir  string
	maxBytes  int64
	sources   map[string]Source
	onSettled func(mediaID int64)

	Concurrency int
	Delays      []time.Duration
	Now         func() time.Time

	wake     chan struct{}
	mu       sync.Mutex
	inflight map[int64]bool
}

func New(st *store.Store, mediaDir string, maxBytes int64, onSettled func(int64)) *Downloader {
	return &Downloader{
		st: st, mediaDir: mediaDir, maxBytes: maxBytes, sources: map[string]Source{}, onSettled: onSettled,
		Concurrency: 4,
		Delays:      []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute},
		Now:         time.Now,
		wake:        make(chan struct{}, 1),
		inflight:    map[int64]bool{},
	}
}

func (d *Downloader) Register(prefix string, s Source) { d.sources[prefix] = s }

func (d *Downloader) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *Downloader) Run(ctx context.Context) {
	sem := make(chan struct{}, d.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		due, err := d.st.DueMedia(ctx, d.Now().Unix(), 32)
		if err != nil && ctx.Err() == nil {
			log.Printf("downloader: list due media: %v", err)
		}
		for i := range due {
			m := due[i]
			if !d.claim(m.ID) {
				continue
			}
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				d.release(m.ID)
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				defer d.release(m.ID)
				d.Process(ctx, &m)
			}()
		}
		select {
		case <-ctx.Done():
			return
		case <-d.wake:
		case <-tick.C:
		}
	}
}

func (d *Downloader) claim(id int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inflight[id] {
		return false
	}
	d.inflight[id] = true
	return true
}

func (d *Downloader) release(id int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.inflight, id)
}

func (d *Downloader) Process(ctx context.Context, m *store.Media) {
	if d.maxBytes > 0 && m.Size > d.maxBytes {
		d.tooLarge(ctx, m)
		return
	}
	prefix, _, _ := strings.Cut(m.DedupeKey, ":")
	src := d.sources[prefix]
	if src == nil {
		d.retry(ctx, m, fmt.Errorf("no download source for %q", prefix))
		return
	}
	abs := filepath.Join(d.mediaDir, d.relBase(m))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		d.retry(ctx, m, err)
		return
	}
	final, size, err := src.Fetch(ctx, m, abs)
	if err != nil {
		if ctx.Err() != nil {
			return // shutting down: leave it pending for the next start
		}
		if errors.Is(err, ErrTooLarge) {
			d.tooLarge(ctx, m)
			return
		}
		d.retry(ctx, m, err)
		return
	}
	rel, err := filepath.Rel(d.mediaDir, final)
	if err != nil {
		d.retry(ctx, m, err)
		return
	}
	ok, err := d.st.MarkMediaDone(ctx, m.ID, rel, size)
	if err != nil {
		log.Printf("downloader: mark media %d done: %v", m.ID, err)
		return
	}
	if !ok {
		os.Remove(final) // the owning message was deleted while we downloaded
		return
	}
	d.settle(m.ID)
}

func (d *Downloader) retry(ctx context.Context, m *store.Media, cause error) {
	n := m.Attempts + 1
	if n > len(d.Delays) {
		if err := d.st.MarkMediaFailed(ctx, m.ID, n, cause.Error()); err != nil {
			log.Printf("downloader: mark media %d failed: %v", m.ID, err)
			return
		}
		d.settle(m.ID)
		return
	}
	next := d.Now().Add(d.Delays[n-1]).Unix()
	if err := d.st.MarkMediaRetry(ctx, m.ID, n, next, cause.Error()); err != nil {
		log.Printf("downloader: schedule retry for media %d: %v", m.ID, err)
	}
}

func (d *Downloader) tooLarge(ctx context.Context, m *store.Media) {
	if err := d.st.MarkMediaTooLarge(ctx, m.ID); err != nil {
		log.Printf("downloader: mark media %d too large: %v", m.ID, err)
		return
	}
	d.settle(m.ID)
}

func (d *Downloader) settle(id int64) {
	if d.onSettled != nil {
		d.onSettled(id)
	}
}

// relBase is <bucket>/<yyyy>/<mm>/<sha1(dedupe_key)> where bucket is the bot id for "bot:" keys.
func (d *Downloader) relBase(m *store.Media) string {
	prefix, _, _ := strings.Cut(m.DedupeKey, ":")
	bucket := prefix
	if prefix == "bot" {
		bucket = strconv.FormatInt(m.BotID, 10)
	}
	now := d.Now().UTC()
	sum := sha1.Sum([]byte(m.DedupeKey))
	return filepath.Join(bucket, fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", int(now.Month())), hex.EncodeToString(sum[:]))
}

// RemoveFiles deletes archive files given as paths relative to mediaDir, refusing anything outside it.
func RemoveFiles(mediaDir string, rels []string) {
	for _, rel := range rels {
		p := filepath.Join(mediaDir, rel)
		r, err := filepath.Rel(mediaDir, p)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			log.Printf("downloader: refusing to remove %q outside media dir", rel)
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("downloader: remove %s: %v", rel, err)
		}
	}
}
```

`internal/downloader/botsource.go`

```go
package downloader

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"tgarchive/internal/botapifs"
	"tgarchive/internal/store"
	"tgarchive/internal/tgbot"
)

// BotSource fetches "bot:" media through the local Bot API server (--local mode),
// which writes the file into the shared directory and returns its absolute path.
type BotSource struct {
	Clients func(ctx context.Context, botID int64) (*tgbot.Client, error)
	Mapper  botapifs.Mapper
}

func (b *BotSource) Fetch(ctx context.Context, m *store.Media, dstBase string) (string, int64, error) {
	cl, err := b.Clients(ctx, m.BotID)
	if err != nil {
		return "", 0, err
	}
	f, err := cl.GetFile(ctx, m.SourceRef)
	if err != nil {
		return "", 0, err
	}
	local, err := b.Mapper.Map(f.FilePath)
	if err != nil {
		return "", 0, err
	}
	dst := dstBase + strings.ToLower(filepath.Ext(local))
	if err := botapifs.LinkOrCopy(local, dst); err != nil {
		return "", 0, err
	}
	st, err := os.Stat(dst)
	if err != nil {
		return "", 0, err
	}
	_ = os.Remove(local)
	return dst, st.Size(), nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test -race ./internal/downloader/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/downloader
git commit -m "feat(downloader): queue, retry state machine, bot source"
```

---

### Task 10: 回执引擎

**Files:**
- Create: `internal/receipt/receipt.go`, `internal/receipt/receipt_test.go`

**Interfaces:**
- Consumes: `store.GetReceiptInfo`、`store.SetReceipt`、`store.MessagesForMedia`
- Produces:
  - `receipt.Transport` 接口：`SetReaction(ctx, botID, chatID, msgID int64, emoji string) error`、`Reply(ctx, botID, chatID, msgID int64, text string) error`（`*botclients.Registry` 已实现）
  - `receipt.New(st *store.Store, tr Transport) *receipt.Engine`；`(*Engine).Evaluate(ctx, messageID int64)`；`(*Engine).MediaSettled(ctx, mediaID int64)`
  - 常量 `receipt.EmojiSeen = "👀"`、`receipt.EmojiDone = "👌"`

状态转移（只看 `role=main` 的媒体；仅 `source=bot_update`）：

| 主媒体状态 | 当前 receipt | 动作 | 新 receipt |
|---|---|---|---|
| 存在 pending | none | 👀 | seen |
| 存在 pending | 其他 | 无 | 不变 |
| 无 pending，存在 failed | ≠ failed | （若 none 先 👀）回复 `⚠️ 存档失败：<首个错误，截断 200 字符>` | failed |
| 全部 done/too_large（含无媒体） | ≠ done | 👌；存在 too_large 时回复 `文件超过存档上限，仅保存了消息记录` | done |

- [ ] **Step 1: 写失败测试**

```go
package receipt

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

var ctx = context.Background()

type rec struct {
	mu    sync.Mutex
	calls []string
}

func (r *rec) SetReaction(_ context.Context, botID, chatID, msgID int64, emoji string) error {
	r.add(fmt.Sprintf("react %d %d %s", chatID, msgID, emoji))
	return nil
}

func (r *rec) Reply(_ context.Context, botID, chatID, msgID int64, text string) error {
	r.add(fmt.Sprintf("reply %d %d %s", chatID, msgID, text))
	return nil
}

func (r *rec) add(s string) { r.mu.Lock(); r.calls = append(r.calls, s); r.mu.Unlock() }

func (r *rec) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.calls
	r.calls = nil
	return out
}

type env struct {
	st  *store.Store
	bot int64
	tr  *rec
	e   *Engine
}

func newEnv(t *testing.T) *env {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bot, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	tr := &rec{}
	return &env{st: st, bot: bot, tr: tr, e: New(st, tr)}
}

func (v *env) ingest(t *testing.T, id int64, source string, keys ...string) (int64, []int64) {
	m := &model.Message{TgMessageID: id, Source: source, Date: id, Kind: model.KindText, Text: "t", RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}
	for _, k := range keys {
		m.Kind = model.KindPhoto
		m.Media = append(m.Media, model.Media{DedupeKey: k, Kind: "photo", Role: model.RoleMain})
	}
	res, err := v.st.Ingest(ctx, store.IngestInput{BotID: v.bot, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: 1})
	if err != nil {
		t.Fatal(err)
	}
	var mids []int64
	for range keys {
		due, _ := v.st.DueMedia(ctx, 0, 100)
		for _, d := range due {
			mids = append(mids, d.ID)
		}
		break
	}
	return res.MessageID, mids
}

func TestTextGoesStraightToDone(t *testing.T) {
	v := newEnv(t)
	id, _ := v.ingest(t, 10, model.SourceBotUpdate)
	v.e.Evaluate(ctx, id)
	v.e.Evaluate(ctx, id)
	if got := v.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👌"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestPendingThenDone(t *testing.T) {
	v := newEnv(t)
	id, mids := v.ingest(t, 10, model.SourceBotUpdate, "bot:a")
	v.e.Evaluate(ctx, id)
	v.e.Evaluate(ctx, id)
	if got := v.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👀"}) {
		t.Fatalf("pending calls = %v", got)
	}
	v.st.MarkMediaDone(ctx, mids[0], "p", 1)
	v.e.MediaSettled(ctx, mids[0])
	if got := v.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👌"}) {
		t.Fatalf("done calls = %v", got)
	}
}

func TestFailureRepliesOnceThenRecovers(t *testing.T) {
	v := newEnv(t)
	id, mids := v.ingest(t, 10, model.SourceBotUpdate, "bot:a")
	v.st.MarkMediaFailed(ctx, mids[0], 4, strings.Repeat("x", 300))
	v.e.MediaSettled(ctx, mids[0])
	v.e.Evaluate(ctx, id)
	got := v.tr.take()
	if len(got) != 2 || got[0] != "react 42 10 👀" || !strings.HasPrefix(got[1], "reply 42 10 ⚠️ 存档失败：") || len([]rune(got[1])) > 230 {
		t.Fatalf("failure calls = %v", got)
	}
	v.st.ResetMedia(ctx, mids[0])
	v.st.MarkMediaDone(ctx, mids[0], "p", 1)
	v.e.MediaSettled(ctx, mids[0])
	if got := v.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👌"}) {
		t.Fatalf("recovery calls = %v", got)
	}
}

func TestTooLargeRepliesAndCompletes(t *testing.T) {
	v := newEnv(t)
	_, mids := v.ingest(t, 10, model.SourceBotUpdate, "bot:a")
	v.st.MarkMediaTooLarge(ctx, mids[0])
	v.e.MediaSettled(ctx, mids[0])
	if got := v.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👌", "reply 42 10 文件超过存档上限，仅保存了消息记录"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestUserbotMessagesIgnored(t *testing.T) {
	v := newEnv(t)
	id, _ := v.ingest(t, 10, model.SourceUserbotFetch)
	v.e.Evaluate(ctx, id)
	if got := v.tr.take(); len(got) != 0 {
		t.Fatalf("calls = %v", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/receipt/`
Expected: FAIL（未定义）

- [ ] **Step 3: 实现**

```go
// Package receipt tells the sender, via reactions and replies, how archiving went.
package receipt

import (
	"context"
	"log"
	"sync"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

const (
	EmojiSeen = "👀"
	EmojiDone = "👌"

	textFailedPrefix = "⚠️ 存档失败："
	textTooLarge     = "文件超过存档上限，仅保存了消息记录"
)

type Transport interface {
	SetReaction(ctx context.Context, botID, chatID, msgID int64, emoji string) error
	Reply(ctx context.Context, botID, chatID, msgID int64, text string) error
}

type Engine struct {
	st *store.Store
	tr Transport
	mu sync.Mutex // serializes evaluations so a reply is never sent twice
}

func New(st *store.Store, tr Transport) *Engine { return &Engine{st: st, tr: tr} }

func (e *Engine) MediaSettled(ctx context.Context, mediaID int64) {
	ids, err := e.st.MessagesForMedia(ctx, mediaID)
	if err != nil {
		log.Printf("receipt: messages for media %d: %v", mediaID, err)
		return
	}
	for _, id := range ids {
		e.Evaluate(ctx, id)
	}
}

func (e *Engine) Evaluate(ctx context.Context, messageID int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	info, err := e.st.GetReceiptInfo(ctx, messageID)
	if err != nil {
		log.Printf("receipt: info for message %d: %v", messageID, err)
		return
	}
	if info.Source != model.SourceBotUpdate {
		return
	}
	var pending, failed, tooLarge int
	firstErr := ""
	for _, m := range info.Main {
		switch m.State {
		case store.StatePending:
			pending++
		case store.StateFailed:
			failed++
			if firstErr == "" {
				firstErr = m.Error
			}
		case store.StateTooLarge:
			tooLarge++
		}
	}
	switch {
	case pending > 0:
		if info.Receipt == store.ReceiptNone {
			e.react(ctx, info, EmojiSeen)
			e.set(ctx, messageID, store.ReceiptSeen)
		}
	case failed > 0:
		if info.Receipt != store.ReceiptFailed {
			if info.Receipt == store.ReceiptNone {
				e.react(ctx, info, EmojiSeen)
			}
			e.reply(ctx, info, textFailedPrefix+truncate(firstErr, 200))
			e.set(ctx, messageID, store.ReceiptFailed)
		}
	default:
		if info.Receipt != store.ReceiptDone {
			e.react(ctx, info, EmojiDone)
			if tooLarge > 0 {
				e.reply(ctx, info, textTooLarge)
			}
			e.set(ctx, messageID, store.ReceiptDone)
		}
	}
}

func (e *Engine) react(ctx context.Context, i *store.ReceiptInfo, emoji string) {
	if err := e.tr.SetReaction(ctx, i.BotID, i.TgChatID, i.TgMessageID, emoji); err != nil {
		log.Printf("receipt: react %s on bot %d msg %d: %v", emoji, i.BotID, i.TgMessageID, err)
	}
}

func (e *Engine) reply(ctx context.Context, i *store.ReceiptInfo, text string) {
	if err := e.tr.Reply(ctx, i.BotID, i.TgChatID, i.TgMessageID, text); err != nil {
		log.Printf("receipt: reply on bot %d msg %d: %v", i.BotID, i.TgMessageID, err)
	}
}

func (e *Engine) set(ctx context.Context, id int64, receipt string) {
	if err := e.st.SetReceipt(ctx, id, receipt); err != nil {
		log.Printf("receipt: save state for message %d: %v", id, err)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/receipt/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/receipt
git commit -m "feat(receipt): reaction and failure-reply engine"
```

---

### Task 11: SSE 事件 Hub 与 Bark 通知

**Files:**
- Create: `internal/events/hub.go`, `internal/events/hub_test.go`, `internal/notify/bark.go`, `internal/notify/bark_test.go`

**Interfaces:**
- Produces:
  - `events.Event{Type string "type"; Data any "data"}`；`events.NewHub() *events.Hub`；`(*Hub).Subscribe() (<-chan Event, func())`；`(*Hub).Publish(Event)`（非阻塞，订阅者缓冲 64，满则丢弃该订阅者的这条事件）
  - 事件类型（计划 3 依赖）：`message.created` / `message.updated` / `message.deleted`（data：`{"chat_id","message_id"}`）、`media.updated`（data：`{"media_id","message_ids"}`）、`bot.status`（data：`{"bot_id","status","error"}`）
  - `notify.Notifier` 接口：`Notify(ctx, title, body string)`；`notify.Bark{File string; HC *http.Client}` 实现它

- [ ] **Step 1: 写失败测试** `internal/events/hub_test.go`

```go
package events

import (
	"testing"
	"time"
)

func TestPublishSubscribe(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	h.Publish(Event{Type: "message.created", Data: 1})
	select {
	case e := <-ch:
		if e.Type != "message.created" {
			t.Fatalf("event = %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	cancel()
	h.Publish(Event{Type: "after"}) // must not panic or block after unsubscribe
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	h := NewHub()
	_, cancel := h.Subscribe()
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			h.Publish(Event{Type: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
}
```

`internal/notify/bark_test.go`

```go
package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBarkPosts(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"code":200}`))
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "notify.json")
	os.WriteFile(cfg, []byte(`{"endpoint":"`+srv.URL+`/push","device_keys":["k1","k2"]}`), 0o600)
	(&Bark{File: cfg}).Notify(context.Background(), "title", "body")
	if got["title"] != "title" || got["body"] != "body" || got["group"] != "docker" || got["level"] != "timeSensitive" {
		t.Fatalf("payload = %v", got)
	}
	if keys := got["device_keys"].([]any); len(keys) != 2 {
		t.Fatalf("device_keys = %v", keys)
	}
}

func TestBarkDisabledWithoutFile(t *testing.T) {
	(&Bark{}).Notify(context.Background(), "t", "b") // must be a no-op
	(&Bark{File: "/nonexistent/notify.json"}).Notify(context.Background(), "t", "b")
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/events/ ./internal/notify/`
Expected: FAIL（未定义）

- [ ] **Step 3: 实现** `internal/events/hub.go`

```go
// Package events fans out archive changes to SSE subscribers.
package events

import "sync"

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan Event]struct{}{}} }

func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
		})
	}
}

func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default: // slow subscriber: drop rather than stall the collector
		}
	}
}
```

`internal/notify/bark.go`

```go
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
```

- [ ] **Step 4: 运行确认通过**

Run: `go test -race ./internal/events/ ./internal/notify/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/events internal/notify
git commit -m "feat: SSE event hub and Bark notifier"
```

---

### Task 12: 采集 worker 与 Manager

**Files:**
- Create: `internal/collector/collector.go`, `internal/collector/collector_test.go`

**Interfaces:**
- Consumes: `botclients.Registry.Get`、`tgbot.Client.GetUpdates`、`tgbot.APIError`、`botapi.Convert`、`store.CheckAllowed/RecordRejected/AdvanceOffset/Ingest/GetBot/ListBots/SetBotStatus`、`downloader.Downloader.Wake`、`downloader.RemoveFiles`、`receipt.Engine.Evaluate`、`events.Hub.Publish`、`notify.Notifier`
- Produces:
  - `collector.LinkHandler` 接口：`TryHandle(ctx, botID int64, sender model.Sender, msg *model.Message, canFetch bool) bool`（计划 2 实现；返回 true 表示消息已被代取流程消费，不再作为普通消息入库）
  - `collector.AvatarRefresher` 接口：`RefreshSender(ctx, botID, userID int64)`
  - `collector.Deps{Store; Clients *botclients.Registry; Downloader *downloader.Downloader; Receipts *receipt.Engine; Hub *events.Hub; Notifier notify.Notifier; Links LinkHandler; Avatars AvatarRefresher; MediaDir string; PollTimeoutSec int; MaxBackoff time.Duration; Now func() time.Time}`
  - `collector.New(base context.Context, d Deps) *collector.Manager`；`(*Manager).StartAll(ctx) error`、`Start(botID int64)`（幂等）、`Stop(botID int64)`（取消并等待退出）、`StopAll()`、`Running(botID int64) bool`

行为：worker 启动时状态置 `running`；`getUpdates` 返回 401/409 → `error` + `bot.status` 事件 + Bark（标题 `tgarchive 机器人异常`，正文 `@<username>: <错误>`），worker 退出；其他错误按 `RetryAfter` 或指数退避（1s 起，上限 `MaxBackoff`，默认 60s）。单条 update 处理出错（存储错误）时中断本批，1 秒后从已持久化的 offset 重新拉取。

- [ ] **Step 1: 写失败测试** `internal/collector/collector_test.go`

```go
package collector

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tgarchive/internal/botclients"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/model"
	"tgarchive/internal/receipt"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
)

const token = "777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

var bg = context.Background()

type fakeNotifier struct {
	mu   sync.Mutex
	msgs []string
}

func (n *fakeNotifier) Notify(_ context.Context, title, body string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.msgs = append(n.msgs, title+"|"+body)
}

func (n *fakeNotifier) count() int { n.mu.Lock(); defer n.mu.Unlock(); return len(n.msgs) }

type linkStub struct {
	mu       sync.Mutex
	calls    int
	canFetch bool
}

func (l *linkStub) TryHandle(_ context.Context, _ int64, _ model.Sender, _ *model.Message, canFetch bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	l.canFetch = canFetch
	return true
}

type env struct {
	fake *tgtest.FakeTG
	st   *store.Store
	m    *Manager
	bot  int64
	n    *fakeNotifier
	hub  *events.Hub
}

func setup(t *testing.T, links LinkHandler) *env {
	t.Helper()
	fake := tgtest.New(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	bot, _ := st.UpsertBot(bg, &store.Bot{TgBotID: 777, Username: "archive_bot", TokenEnc: box.Seal([]byte(token)), CreatedAt: 1})
	reg := botclients.New(st, box, fake.URL(), nil)
	hub := events.NewHub()
	n := &fakeNotifier{}
	ctx, cancel := context.WithCancel(bg)
	m := New(ctx, Deps{
		Store: st, Clients: reg, Downloader: downloader.New(st, t.TempDir(), 0, nil), Receipts: receipt.New(st, reg),
		Hub: hub, Notifier: n, Links: links, MediaDir: t.TempDir(), PollTimeoutSec: 1,
	})
	t.Cleanup(func() { m.StopAll(); cancel(); st.Close() })
	return &env{fake: fake, st: st, m: m, bot: bot, n: n, hub: hub}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (e *env) reactions() []string {
	var out []string
	for _, c := range e.fake.Calls("setMessageReaction") {
		r := c.Params["reaction"].([]any)[0].(map[string]any)
		out = append(out, r["emoji"].(string))
	}
	return out
}

func (e *env) offset() int64 {
	b, _ := e.st.GetBot(bg, e.bot)
	return b.UpdateOffset
}

func (e *env) messages(t *testing.T) []store.MessageView {
	chats, _ := e.st.ListChats(bg, 0)
	if len(chats) == 0 {
		return nil
	}
	msgs, err := e.st.ListMessages(bg, chats[0].ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestArchivesWhitelistedText(t *testing.T) {
	e := setup(t, nil)
	e.st.PutWhitelist(bg, store.WhitelistEntry{BotID: e.bot, TgUserID: 42})
	ch, unsub := e.hub.Subscribe()
	defer unsub()
	e.fake.PushMessage(tgtest.TextMsg(1, 42, "hello"))
	e.m.Start(e.bot)
	eventually(t, "👌 reaction", func() bool { r := e.reactions(); return len(r) == 1 && r[0] == "👌" })
	if msgs := e.messages(t); len(msgs) != 1 || msgs[0].Text != "hello" {
		t.Fatalf("messages = %+v", msgs)
	}
	if e.offset() != 2 {
		t.Fatalf("offset = %d", e.offset())
	}
	if b, _ := e.st.GetBot(bg, e.bot); b.Status != store.StatusRunning || !e.m.Running(e.bot) {
		t.Fatalf("status = %s", b.Status)
	}
	got := false
	for !got {
		select {
		case ev := <-ch:
			got = ev.Type == "message.created"
		case <-time.After(2 * time.Second):
			t.Fatal("no message.created event")
		}
	}
}

func TestRejectsStrangers(t *testing.T) {
	e := setup(t, nil)
	e.fake.PushMessage(tgtest.TextMsg(1, 99, "let me in"))
	e.m.Start(e.bot)
	eventually(t, "offset advance", func() bool { return e.offset() == 2 })
	rej, _ := e.st.ListRejected(bg, e.bot)
	if len(rej) != 1 || rej[0].TgUserID != 99 || rej[0].FirstName != "User99" {
		t.Fatalf("rejected = %+v", rej)
	}
	if len(e.messages(t)) != 0 || len(e.reactions()) != 0 || len(e.fake.Calls("sendMessage")) != 0 {
		t.Fatal("strangers must be ignored silently")
	}
}

func TestSkipsUnsupportedUpdates(t *testing.T) {
	e := setup(t, nil)
	e.st.PutWhitelist(bg, store.WhitelistEntry{BotID: e.bot, TgUserID: 42})
	e.fake.PushMessage(`{"message_id":1,"from":{"id":42,"is_bot":false,"first_name":"A"},"chat":{"id":-5,"type":"group","title":"G"},"date":1,"text":"group"}`)
	e.fake.PushMessage(`{"message_id":2,"chat":{"id":-100,"type":"channel"},"date":1,"text":"no sender"}`)
	e.fake.PushMessage(`{"message_id":"not-a-number"}`)
	e.fake.PushMessage(tgtest.TextMsg(4, 42, "valid"))
	e.m.Start(e.bot)
	eventually(t, "valid message archived", func() bool { return len(e.reactions()) == 1 })
	msgs := e.messages(t)
	if len(msgs) != 1 || msgs[0].Text != "valid" || e.offset() != 5 {
		t.Fatalf("messages = %+v offset = %d", msgs, e.offset())
	}
}

func TestEditedMessageUpdates(t *testing.T) {
	e := setup(t, nil)
	e.st.PutWhitelist(bg, store.WhitelistEntry{BotID: e.bot, TgUserID: 42})
	e.fake.PushMessage(tgtest.TextMsg(1, 42, "first"))
	e.m.Start(e.bot)
	eventually(t, "first archived", func() bool { return len(e.messages(t)) == 1 })
	edited := strings.Replace(tgtest.TextMsg(1, 42, "second"), `"date":`, `"edit_date":1759600000,"date":`, 1)
	e.fake.PushEdited(edited)
	eventually(t, "edit applied", func() bool {
		m := e.messages(t)
		return len(m) == 1 && m[0].Text == "second" && m[0].EditDate == 1759600000
	})
}

func TestFatalErrorStopsWorker(t *testing.T) {
	e := setup(t, nil)
	e.fake.FailUpdates(401, "Unauthorized")
	e.m.Start(e.bot)
	eventually(t, "error status", func() bool {
		b, _ := e.st.GetBot(bg, e.bot)
		return b.Status == store.StatusError && strings.Contains(b.LastError, "Unauthorized")
	})
	eventually(t, "worker exit", func() bool { return !e.m.Running(e.bot) })
	if e.n.count() != 1 || !strings.Contains(e.n.msgs[0], "@archive_bot") {
		t.Fatalf("notifications = %v", e.n.msgs)
	}
}

func TestLinkHandlerConsumes(t *testing.T) {
	stub := &linkStub{}
	e := setup(t, stub)
	e.st.PutWhitelist(bg, store.WhitelistEntry{BotID: e.bot, TgUserID: 42, CanFetch: true})
	e.fake.PushMessage(tgtest.TextMsg(1, 42, "https://t.me/c/123/456"))
	e.m.Start(e.bot)
	eventually(t, "offset advance", func() bool { return e.offset() == 2 })
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.calls != 1 || !stub.canFetch || len(e.messages(t)) != 0 {
		t.Fatalf("calls=%d canFetch=%v", stub.calls, stub.canFetch)
	}
}

func TestStartAllAndStop(t *testing.T) {
	e := setup(t, nil)
	if err := e.m.StartAll(bg); err != nil {
		t.Fatal(err)
	}
	eventually(t, "running", func() bool { return e.m.Running(e.bot) })
	e.m.Stop(e.bot)
	if e.m.Running(e.bot) {
		t.Fatal("Stop must wait for the worker to exit")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/collector/`
Expected: FAIL（未定义）

- [ ] **Step 3: 实现** `internal/collector/collector.go`

```go
// Package collector runs one long-polling worker per bot and archives what they receive.
package collector

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"tgarchive/internal/botclients"
	"tgarchive/internal/convert/botapi"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/model"
	"tgarchive/internal/notify"
	"tgarchive/internal/receipt"
	"tgarchive/internal/store"
	"tgarchive/internal/tgbot"
)

type LinkHandler interface {
	TryHandle(ctx context.Context, botID int64, sender model.Sender, msg *model.Message, canFetch bool) bool
}

type AvatarRefresher interface {
	RefreshSender(ctx context.Context, botID, userID int64)
}

type Deps struct {
	Store          *store.Store
	Clients        *botclients.Registry
	Downloader     *downloader.Downloader
	Receipts       *receipt.Engine
	Hub            *events.Hub
	Notifier       notify.Notifier
	Links          LinkHandler
	Avatars        AvatarRefresher
	MediaDir       string
	PollTimeoutSec int
	MaxBackoff     time.Duration
	Now            func() time.Time
}

type worker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type Manager struct {
	base    context.Context
	d       Deps
	mu      sync.Mutex
	workers map[int64]*worker
}

func New(base context.Context, d Deps) *Manager {
	if d.PollTimeoutSec == 0 {
		d.PollTimeoutSec = 50
	}
	if d.MaxBackoff == 0 {
		d.MaxBackoff = 60 * time.Second
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Manager{base: base, d: d, workers: map[int64]*worker{}}
}

func (m *Manager) StartAll(ctx context.Context) error {
	bots, err := m.d.Store.ListBots(ctx)
	if err != nil {
		return err
	}
	for _, b := range bots {
		if b.Enabled && b.Status != store.StatusRemoved {
			m.Start(b.ID)
		}
	}
	return nil
}

func (m *Manager) Start(botID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workers[botID]; ok {
		return
	}
	ctx, cancel := context.WithCancel(m.base)
	w := &worker{cancel: cancel, done: make(chan struct{})}
	m.workers[botID] = w
	go func() {
		defer close(w.done)
		defer func() {
			m.mu.Lock()
			if m.workers[botID] == w {
				delete(m.workers, botID)
			}
			m.mu.Unlock()
		}()
		m.run(ctx, botID)
	}()
}

func (m *Manager) Stop(botID int64) {
	m.mu.Lock()
	w := m.workers[botID]
	m.mu.Unlock()
	if w == nil {
		return
	}
	w.cancel()
	<-w.done
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	ids := make([]int64, 0, len(m.workers))
	for id := range m.workers {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Stop(id)
	}
}

func (m *Manager) Running(botID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.workers[botID]
	return ok
}

func (m *Manager) run(ctx context.Context, botID int64) {
	cl, err := m.d.Clients.Get(ctx, botID)
	if err != nil {
		m.fail(botID, err)
		return
	}
	bot, err := m.d.Store.GetBot(ctx, botID)
	if err != nil {
		m.fail(botID, err)
		return
	}
	m.setStatus(botID, store.StatusRunning, "")
	offset := bot.UpdateOffset
	backoff := time.Second
	for ctx.Err() == nil {
		pctx, cancel := context.WithTimeout(ctx, time.Duration(m.d.PollTimeoutSec+20)*time.Second)
		ups, err := cl.GetUpdates(pctx, offset, m.d.PollTimeoutSec)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			var ae *tgbot.APIError
			if errors.As(err, &ae) && (ae.Code == 401 || ae.Code == 409) {
				m.fail(botID, err)
				return
			}
			wait := backoff
			if errors.As(err, &ae) && ae.RetryAfter > 0 {
				wait = time.Duration(ae.RetryAfter) * time.Second
			}
			log.Printf("collector: bot %d poll: %v (retry in %s)", botID, err, wait)
			if !sleep(ctx, wait) {
				return
			}
			backoff = min(backoff*2, m.d.MaxBackoff)
			continue
		}
		backoff = time.Second
		for _, u := range ups {
			if err := m.handle(ctx, botID, u); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("collector: bot %d update %d: %v", botID, u.UpdateID, err)
				sleep(ctx, time.Second)
				break // refetch from the last persisted offset
			}
			offset = u.UpdateID + 1
		}
	}
}

func (m *Manager) handle(ctx context.Context, botID int64, u tgbot.Update) error {
	next := u.UpdateID + 1
	raw := u.Message
	if len(raw) == 0 {
		raw = u.EditedMessage
	}
	if len(raw) == 0 {
		return m.d.Store.AdvanceOffset(ctx, botID, next)
	}
	res, err := botapi.Convert(raw)
	if err != nil {
		log.Printf("collector: bot %d skips update %d: %v", botID, u.UpdateID, err)
		return m.d.Store.AdvanceOffset(ctx, botID, next)
	}
	if res.ChatType != "private" {
		return m.d.Store.AdvanceOffset(ctx, botID, next)
	}
	now := m.d.Now().Unix()
	allowed, canFetch, err := m.d.Store.CheckAllowed(ctx, botID, res.Sender.TgUserID)
	if err != nil {
		return err
	}
	if !allowed {
		if err := m.d.Store.RecordRejected(ctx, store.Rejected{
			BotID: botID, TgUserID: res.Sender.TgUserID, FirstName: res.Sender.FirstName, Username: res.Sender.Username, LastSeenAt: now,
		}); err != nil {
			return err
		}
		return m.d.Store.AdvanceOffset(ctx, botID, next)
	}
	if len(u.Message) > 0 && m.d.Links != nil && m.d.Links.TryHandle(ctx, botID, res.Sender, res.Msg, canFetch) {
		return m.d.Store.AdvanceOffset(ctx, botID, next)
	}
	ir, err := m.d.Store.Ingest(ctx, store.IngestInput{BotID: botID, Sender: res.Sender, Msg: res.Msg, Offset: next, Now: now})
	if err != nil {
		return err
	}
	downloader.RemoveFiles(m.d.MediaDir, ir.OrphanPaths)
	typ := "message.updated"
	if ir.Created {
		typ = "message.created"
	}
	m.d.Hub.Publish(events.Event{Type: typ, Data: map[string]int64{"chat_id": ir.ChatID, "message_id": ir.MessageID}})
	if ir.ChatCreated && m.d.Avatars != nil {
		go m.d.Avatars.RefreshSender(m.base, botID, res.Sender.TgUserID)
	}
	// Evaluate before waking the downloader so 👀 always precedes 👌.
	m.d.Receipts.Evaluate(ctx, ir.MessageID)
	m.d.Downloader.Wake()
	return nil
}

func (m *Manager) setStatus(botID int64, status, lastErr string) {
	if err := m.d.Store.SetBotStatus(context.Background(), botID, status, lastErr); err != nil {
		log.Printf("collector: set bot %d status: %v", botID, err)
	}
	m.d.Hub.Publish(events.Event{Type: "bot.status", Data: map[string]any{"bot_id": botID, "status": status, "error": lastErr}})
}

func (m *Manager) fail(botID int64, err error) {
	msg := err.Error()
	m.setStatus(botID, store.StatusError, msg)
	if m.d.Notifier == nil {
		return
	}
	name := fmt.Sprintf("bot #%d", botID)
	if b, e := m.d.Store.GetBot(context.Background(), botID); e == nil && b.Username != "" {
		name = "@" + b.Username
	}
	m.d.Notifier.Notify(context.Background(), "tgarchive 机器人异常", name+": "+msg)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test -race ./internal/collector/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/collector
git commit -m "feat(collector): per-bot long-polling workers"
```

---

### Task 13: 头像刷新

**Files:**
- Create: `internal/avatars/avatars.go`, `internal/avatars/avatars_test.go`

**Interfaces:**
- Consumes: `botclients.Registry.Get`、`tgbot.Client.GetUserProfilePhotos/GetFile`、`botapifs.Mapper`、`botapifs.LinkOrCopy`、`store.GetBot/ListBots/ChatSenders/SetBotAvatar/SetSenderAvatar`
- Produces: `avatars.Refresher{Store *store.Store; Clients *botclients.Registry; Mapper botapifs.Mapper; Dir string}`；`RefreshBot(ctx, botID int64)`、`RefreshSender(ctx, botID, userID int64)`（满足 `collector.AvatarRefresher`）、`RefreshAll(ctx)`；文件写到 `<Dir>/bots/<tg_bot_id>.jpg`、`<Dir>/senders/<tg_user_id>.jpg`，库内存 `bots/<id>.jpg` / `senders/<id>.jpg`

选尺寸规则：取第一张头像中宽度 ≤ 640 的最大尺寸。错误只记日志。

- [ ] **Step 1: 写失败测试**

```go
package avatars

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tgarchive/internal/botapifs"
	"tgarchive/internal/botclients"
	"tgarchive/internal/model"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
)

func TestRefresh(t *testing.T) {
	ctx := context.Background()
	fake := tgtest.New(t)
	fake.SetAvatar(42, "av42", []byte("face"))
	st, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	bot, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: box.Seal([]byte("777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")), CreatedAt: 1})
	st.Ingest(ctx, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Now: 1,
		Msg: &model.Message{TgMessageID: 1, Source: model.SourceBotUpdate, Kind: model.KindText, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}})
	dir := t.TempDir()
	r := &Refresher{Store: st, Clients: botclients.New(st, box, fake.URL(), nil), Mapper: botapifs.Mapper{Remote: fake.RemoteDir, Local: fake.RemoteDir}, Dir: dir}

	r.RefreshAll(ctx)

	if b, err := os.ReadFile(filepath.Join(dir, "senders", "42.jpg")); err != nil || string(b) != "face" {
		t.Fatalf("sender avatar = %q, %v", b, err)
	}
	chats, _ := st.ListChats(ctx, 0)
	if !chats[0].Sender.HasAvatar {
		t.Fatal("sender avatar path not stored")
	}
	if _, err := os.Stat(filepath.Join(dir, "bots", "777.jpg")); err == nil {
		t.Fatal("bot without profile photo must not get an avatar file")
	}
	if b, _ := st.GetBot(ctx, bot); b.AvatarPath != "" {
		t.Fatal("bot avatar path must stay empty")
	}
	call := fake.Calls("getFile")[0]
	if call.Params["file_id"] != "av42" {
		t.Fatalf("picked file = %v", call.Params["file_id"])
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/avatars/`
Expected: FAIL（未定义）

- [ ] **Step 3: 实现**

```go
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
		log.Printf("avatars: profile photos of %d: %v", userID, err)
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
		log.Printf("avatars: getFile for %d: %v", userID, err)
		return false
	}
	local, err := r.Mapper.Map(f.FilePath)
	if err != nil {
		log.Printf("avatars: %v", err)
		return false
	}
	dst := filepath.Join(r.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		log.Printf("avatars: %v", err)
		return false
	}
	if err := botapifs.LinkOrCopy(local, dst); err != nil {
		log.Printf("avatars: store %s: %v", rel, err)
		return false
	}
	_ = os.Remove(local)
	return true
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/avatars/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/avatars
git commit -m "feat(avatars): refresh bot and sender profile photos"
```

---

### Task 14: HTTP 只读接口、媒体服务、SSE、鉴权、SPA

**Files:**
- Create: `internal/httpapi/server.go`, `internal/httpapi/read.go`, `internal/httpapi/read_test.go`

**Interfaces:**
- Consumes: `store.*View` 与查询方法、`store.DeleteMessage/ResetMedia/GetMedia`、`downloader.RemoveFiles`、`downloader.Downloader.Wake`、`events.Hub`
- Produces:
  - `httpapi.Server{Cfg *config.Config; Store *store.Store; Box *seal.Box; Clients *botclients.Registry; Manager *collector.Manager; Downloader *downloader.Downloader; Hub *events.Hub; Avatars *avatars.Refresher; Web fs.FS; MediaDir, AvatarDir string; HTTP *http.Client; Now func() time.Time}`；`(*Server).Handler() http.Handler`
  - API（计划 3 依赖）：
    - `GET /healthz` → `ok`（免鉴权）
    - `GET /api/bots` → `[]BotView{id, tg_bot_id, username, name, has_avatar, enabled, status, last_error}`
    - `GET /api/chats?bot_id=` → `[]ChatView`
    - `GET /api/chats/{id}/messages?before=&limit=` → `[]MessageView`（升序，limit 默认 50，范围 1–100）
    - `GET /api/chats/{id}/media?type=media|file|link&before=&limit=` → `[]MessageView`（降序）
    - `DELETE /api/messages/{id}` → 204；发 `message.deleted`
    - `POST /api/media/{id}/retry` → 204（非 failed 返回 409）
    - `GET /api/events` → SSE（`event: <type>\ndata: <json data>\n\n`，每 25s 注释心跳）
    - `GET /media/{id}[?download=1]` → 文件，支持 Range/ETag（`"m<id>"`），`Cache-Control: private, max-age=31536000, immutable`
    - `GET /avatars/{kind}/{id}` → `kind ∈ {bots, senders}`，`Cache-Control: private, max-age=3600`
    - 其他 `GET /api/...` → 404 JSON；其他 GET → SPA（静态文件存在则返回，否则 `index.html`）
  - 错误体统一 `{"error": "<message>"}`

- [ ] **Step 1: 写失败测试** `internal/httpapi/read_test.go`

```go
package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"tgarchive/internal/config"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

var bg = context.Background()

type readEnv struct {
	srv      *Server
	h        http.Handler
	st       *store.Store
	hub      *events.Hub
	chat     int64
	photoMsg int64
	media    int64
}

func newReadEnv(t *testing.T) *readEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mediaDir, avatarDir := t.TempDir(), t.TempDir()
	hub := events.NewHub()
	srv := &Server{
		Cfg: &config.Config{RequireForwardAuth: true}, Store: st, Hub: hub,
		Downloader: downloader.New(st, mediaDir, 0, nil),
		Web:        fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "assets/app.js": {Data: []byte("js!")}},
		MediaDir:   mediaDir, AvatarDir: avatarDir, Now: time.Now,
	}
	bot, _ := st.UpsertBot(bg, &store.Bot{TgBotID: 777, Username: "archive_bot", TokenEnc: []byte("SECRET-TOKEN-BYTES"), CreatedAt: 1})
	photo := &model.Message{TgMessageID: 1, Source: model.SourceBotUpdate, Date: 1, Kind: model.KindPhoto, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`),
		Media: []model.Media{{DedupeKey: "bot:p", Kind: "photo", Mime: "image/jpeg", Role: model.RoleMain}}}
	r1, _ := st.Ingest(bg, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42, FirstName: "Alice"}, Msg: photo, Now: 1})
	link := &model.Message{TgMessageID: 2, Source: model.SourceBotUpdate, Date: 2, Kind: model.KindText, Text: "see https://x.dev", RawFormat: model.RawBotAPI,
		Raw: json.RawMessage(`{}`), Entities: []model.Entity{{Type: "url", Offset: 4, Length: 13}}}
	st.Ingest(bg, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42, FirstName: "Alice"}, Msg: link, Now: 2})
	due, _ := st.DueMedia(bg, 0, 10)
	os.MkdirAll(filepath.Join(mediaDir, "1"), 0o755)
	os.WriteFile(filepath.Join(mediaDir, "1", "p.jpg"), []byte("photo-bytes"), 0o644)
	st.MarkMediaDone(bg, due[0].ID, "1/p.jpg", 11)
	return &readEnv{srv: srv, h: srv.Handler(), st: st, hub: hub, chat: r1.ChatID, photoMsg: r1.MessageID, media: due[0].ID}
}

func do(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Remote-User", "shinya")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestAuth(t *testing.T) {
	e := newReadEnv(t)
	for _, p := range []string{"/api/bots", "/", "/media/1"} {
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 401 {
			t.Fatalf("%s without Remote-User = %d", p, w.Code)
		}
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 {
		t.Fatalf("healthz = %d", w.Code)
	}
	e.srv.Cfg.RequireForwardAuth = false
	w = httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/bots", nil))
	if w.Code != 200 {
		t.Fatalf("auth disabled = %d", w.Code)
	}
}

func TestReadEndpoints(t *testing.T) {
	e := newReadEnv(t)
	w := do(e.h, "GET", "/api/bots", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "SECRET") || !strings.Contains(w.Body.String(), `"username":"archive_bot"`) {
		t.Fatalf("bots = %d %s", w.Code, w.Body)
	}
	var chats []store.ChatView
	json.Unmarshal(do(e.h, "GET", "/api/chats", nil).Body.Bytes(), &chats)
	if len(chats) != 1 || chats[0].Sender.FirstName != "Alice" {
		t.Fatalf("chats = %+v", chats)
	}
	var msgs []store.MessageView
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/chats/%d/messages?limit=10", e.chat), nil).Body.Bytes(), &msgs)
	if len(msgs) != 2 || msgs[0].Media[0].State != "done" {
		t.Fatalf("messages = %+v", msgs)
	}
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/chats/%d/media?type=link", e.chat), nil).Body.Bytes(), &msgs)
	if len(msgs) != 1 || msgs[0].TgMessageID != 2 {
		t.Fatalf("links = %+v", msgs)
	}
	for _, p := range []string{
		fmt.Sprintf("/api/chats/%d/media?type=bogus", e.chat),
		fmt.Sprintf("/api/chats/%d/messages?limit=abc", e.chat),
		"/api/chats/abc/messages",
	} {
		if w := do(e.h, "GET", p, nil); w.Code != 400 || !strings.Contains(w.Body.String(), `"error"`) {
			t.Fatalf("%s = %d %s", p, w.Code, w.Body)
		}
	}
}

func TestServeMedia(t *testing.T) {
	e := newReadEnv(t)
	p := fmt.Sprintf("/media/%d", e.media)
	w := do(e.h, "GET", p, nil)
	if w.Code != 200 || w.Body.String() != "photo-bytes" || w.Header().Get("ETag") == "" || !strings.Contains(w.Header().Get("Cache-Control"), "private") {
		t.Fatalf("media = %d %q %v", w.Code, w.Body, w.Header())
	}
	if w := do(e.h, "GET", p, map[string]string{"Range": "bytes=0-4"}); w.Code != 206 || w.Body.String() != "photo" {
		t.Fatalf("range = %d %q", w.Code, w.Body)
	}
	if w := do(e.h, "GET", p, map[string]string{"If-None-Match": w.Header().Get("ETag")}); w.Code != 304 {
		t.Fatalf("conditional = %d", w.Code)
	}
	if w := do(e.h, "GET", p+"?download=1", nil); !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download header = %q", w.Header().Get("Content-Disposition"))
	}
	os.Remove(filepath.Join(e.srv.MediaDir, "1", "p.jpg"))
	if w := do(e.h, "GET", p, nil); w.Code != 404 {
		t.Fatalf("missing file = %d", w.Code)
	}
	if w := do(e.h, "GET", "/media/99999", nil); w.Code != 404 {
		t.Fatalf("unknown media = %d", w.Code)
	}
}

func TestDeleteAndRetry(t *testing.T) {
	e := newReadEnv(t)
	ch, unsub := e.hub.Subscribe()
	defer unsub()
	if w := do(e.h, "POST", fmt.Sprintf("/api/media/%d/retry", e.media), nil); w.Code != 409 {
		t.Fatalf("retry of done media = %d", w.Code)
	}
	if w := do(e.h, "DELETE", fmt.Sprintf("/api/messages/%d", e.photoMsg), nil); w.Code != 204 {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(e.srv.MediaDir, "1", "p.jpg")); err == nil {
		t.Fatal("orphaned file must be removed")
	}
	if ev := <-ch; ev.Type != "message.deleted" {
		t.Fatalf("event = %+v", ev)
	}
	if w := do(e.h, "DELETE", fmt.Sprintf("/api/messages/%d", e.photoMsg), nil); w.Code != 404 {
		t.Fatalf("second delete = %d", w.Code)
	}
}

func TestSPAFallback(t *testing.T) {
	e := newReadEnv(t)
	if w := do(e.h, "GET", "/chats/5", nil); w.Code != 200 || w.Body.String() != "<html>app</html>" {
		t.Fatalf("spa route = %d %q", w.Code, w.Body)
	}
	if w := do(e.h, "GET", "/assets/app.js", nil); w.Code != 200 || w.Body.String() != "js!" {
		t.Fatalf("asset = %d %q", w.Code, w.Body)
	}
	if w := do(e.h, "GET", "/api/nope", nil); w.Code != 404 || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("unknown api = %d %q", w.Code, w.Body)
	}
}

func TestSSE(t *testing.T) {
	e := newReadEnv(t)
	ts := httptest.NewServer(e.h)
	defer ts.Close()
	req, _ := http.NewRequest("GET", ts.URL+"/api/events", nil)
	req.Header.Set("Remote-User", "shinya")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	rd := bufio.NewReader(resp.Body)
	rd.ReadString('\n') // ": ok"
	go func() {
		time.Sleep(50 * time.Millisecond)
		e.hub.Publish(events.Event{Type: "message.created", Data: map[string]int64{"chat_id": 1, "message_id": 2}})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		line, err := rd.ReadString('\n')
		if err == io.EOF {
			break
		}
		if strings.HasPrefix(line, "event: message.created") {
			data, _ := rd.ReadString('\n')
			if !strings.Contains(data, `"message_id":2`) {
				t.Fatalf("data line = %q", data)
			}
			return
		}
	}
	t.Fatal("event not received")
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/httpapi/`
Expected: FAIL（未定义）

- [ ] **Step 3: 实现** `internal/httpapi/server.go`

```go
// Package httpapi exposes the archive and bot administration over HTTP.
package httpapi

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"tgarchive/internal/avatars"
	"tgarchive/internal/botclients"
	"tgarchive/internal/collector"
	"tgarchive/internal/config"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
)

type Server struct {
	Cfg        *config.Config
	Store      *store.Store
	Box        *seal.Box
	Clients    *botclients.Registry
	Manager    *collector.Manager
	Downloader *downloader.Downloader
	Hub        *events.Hub
	Avatars    *avatars.Refresher
	Web        fs.FS
	MediaDir   string
	AvatarDir  string
	HTTP       *http.Client
	Now        func() time.Time
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/bots", s.listBots)
	mux.HandleFunc("GET /api/chats", s.listChats)
	mux.HandleFunc("GET /api/chats/{id}/messages", s.listMessages)
	mux.HandleFunc("GET /api/chats/{id}/media", s.listChatMedia)
	mux.HandleFunc("DELETE /api/messages/{id}", s.deleteMessage)
	mux.HandleFunc("POST /api/media/{id}/retry", s.retryMedia)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /media/{id}", s.serveMedia)
	mux.HandleFunc("GET /avatars/{kind}/{id}", s.serveAvatar)
	s.adminRoutes(mux)
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) { writeErr(w, http.StatusNotFound, "not found") })
	mux.Handle("GET /", s.spa())
	return s.auth(mux)
}

// auth is the in-app backstop behind Caddy forward_auth (which strips client-sent Remote-* headers).
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Cfg.RequireForwardAuth && r.URL.Path != "/healthz" && r.Header.Get("Remote-User") == "" {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.Web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(s.Web, p); err == nil && !st.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(s.Web, "index.html")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "ui not built")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// storeErr maps store errors to HTTP codes.
func storeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrBadMediaType):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

func queryInt(r *http.Request, name string, def, lo, hi int64) (int64, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < lo || n > hi {
		return 0, false
	}
	return n, true
}
```

`internal/httpapi/read.go`

```go
package httpapi

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/store"
)

type BotView struct {
	ID        int64  `json:"id"`
	TgBotID   int64  `json:"tg_bot_id"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	HasAvatar bool   `json:"has_avatar"`
	Enabled   bool   `json:"enabled"`
	Status    string `json:"status"`
	LastError string `json:"last_error"`
}

func botView(b *store.Bot) BotView {
	return BotView{ID: b.ID, TgBotID: b.TgBotID, Username: b.Username, Name: b.Name, HasAvatar: b.AvatarPath != "",
		Enabled: b.Enabled, Status: b.Status, LastError: b.LastError}
}

func (s *Server) listBots(w http.ResponseWriter, r *http.Request) {
	bots, err := s.Store.ListBots(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	out := make([]BotView, 0, len(bots))
	for i := range bots {
		out = append(out, botView(&bots[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listChats(w http.ResponseWriter, r *http.Request) {
	botID, ok := queryInt(r, "bot_id", 0, 0, 1<<62)
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad bot_id")
		return
	}
	chats, err := s.Store.ListChats(r.Context(), botID)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, chats)
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	chatID, ok := pathID(r, "id")
	before, ok2 := queryInt(r, "before", 0, 0, 1<<62)
	limit, ok3 := queryInt(r, "limit", 50, 1, 100)
	if !ok || !ok2 || !ok3 {
		writeErr(w, http.StatusBadRequest, "bad chat id, before or limit")
		return
	}
	msgs, err := s.Store.ListMessages(r.Context(), chatID, before, int(limit))
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) listChatMedia(w http.ResponseWriter, r *http.Request) {
	chatID, ok := pathID(r, "id")
	before, ok2 := queryInt(r, "before", 0, 0, 1<<62)
	limit, ok3 := queryInt(r, "limit", 50, 1, 100)
	if !ok || !ok2 || !ok3 {
		writeErr(w, http.StatusBadRequest, "bad chat id, before or limit")
		return
	}
	msgs, err := s.Store.ListChatMedia(r.Context(), chatID, r.URL.Query().Get("type"), before, int(limit))
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad message id")
		return
	}
	chatID, orphans, err := s.Store.DeleteMessage(r.Context(), id, s.Now().Unix())
	if err != nil {
		storeErr(w, err)
		return
	}
	downloader.RemoveFiles(s.MediaDir, orphans)
	s.Hub.Publish(events.Event{Type: "message.deleted", Data: map[string]int64{"chat_id": chatID, "message_id": id}})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) retryMedia(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad media id")
		return
	}
	if err := s.Store.ResetMedia(r.Context(), id); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusConflict, "media is not in failed state")
			return
		}
		storeErr(w, err)
		return
	}
	s.Downloader.Wake()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, cancel := s.Hub.Subscribe()
	defer cancel()
	fmt.Fprint(w, ": ok\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			data, err := jsonMarshal(e.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad media id")
		return
	}
	m, err := s.Store.GetMedia(r.Context(), id)
	if err != nil || m.State != store.StateDone {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	p, ok := within(s.MediaDir, m.Path)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	ct := m.Mime
	if ct == "" {
		ct = mime.TypeByExtension(filepath.Ext(p))
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("ETag", fmt.Sprintf(`"m%d"`, m.ID))
	if r.URL.Query().Get("download") == "1" {
		name := m.FileName
		if name == "" {
			name = filepath.Base(p)
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func (s *Server) serveAvatar(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	id, ok := pathID(r, "id")
	if !ok || (kind != "bots" && kind != "senders") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	p := filepath.Join(s.AvatarDir, kind, fmt.Sprintf("%d.jpg", id))
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// within joins rel onto base and refuses results that escape base.
func within(base, rel string) (string, bool) {
	p := filepath.Join(base, rel)
	r, err := filepath.Rel(base, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return p, true
}
```

在 `server.go` 末尾追加（供 SSE 使用，单独函数便于以后替换编码）：

```go
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
```

- [ ] **Step 4: 写占位** `internal/httpapi/admin.go`（Task 15 填充，先保证编译）

```go
package httpapi

import "net/http"

func (s *Server) adminRoutes(mux *http.ServeMux) {}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test -race ./internal/httpapi/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/httpapi
git commit -m "feat(httpapi): read API, media serving, SSE, auth backstop, SPA"
```

---

### Task 15: 管理接口（机器人增删启停、白名单、被拒列表）

**Files:**
- Modify: `internal/httpapi/admin.go`（替换 Task 14 的占位）
- Create: `internal/httpapi/admin_test.go`

**Interfaces:**
- Consumes: `tgbot.New`、`store.UpsertBot/GetBotByTgID/GetBot/SetBotEnabled/SetBotStatus/RemoveBot/PurgeBot/PutWhitelist/ListWhitelist/DeleteWhitelist/ListRejected`、`collector.Manager.Start/Stop/Running`、`botclients.Registry.Forget`、`avatars.Refresher.RefreshBot`
- Produces（计划 3 依赖）：
  - `POST /api/admin/bots` body `{"token"}` → 200 `{"bot_id", "steps":[{"step","ok","detail"}]}`；token 格式错 400；`getMe` 失败 400（带 steps）；已存在且未 removed 409（带 steps）。步骤依次为 `getMe`（经本地 Bot API）、`logOut`（经云端，失败不致命，`ok` 恒为 true，`detail` 说明结果）、`start`
  - `PATCH /api/admin/bots/{id}` body `{"enabled": bool}` → 200 `BotView`
  - `DELETE /api/admin/bots/{id}?purge=1` → 204（无 `purge` 时仅 RemoveBot 保留存档）
  - `GET /api/admin/bots/{id}/whitelist` → `[]{"tg_user_id","note","can_fetch"}`
  - `PUT /api/admin/bots/{id}/whitelist/{uid}` body `{"note","can_fetch"}` → 204
  - `DELETE /api/admin/bots/{id}/whitelist/{uid}` → 204
  - `GET /api/admin/bots/{id}/rejected` → `[]{"tg_user_id","first_name","username","last_seen_at","count"}`

- [ ] **Step 1: 写失败测试** `internal/httpapi/admin_test.go`

```go
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tgarchive/internal/botclients"
	"tgarchive/internal/collector"
	"tgarchive/internal/config"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/receipt"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
)

const goodToken = "777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

type adminEnv struct {
	h    http.Handler
	st   *store.Store
	fake *tgtest.FakeTG
	mgr  *collector.Manager
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	fake := tgtest.New(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	reg := botclients.New(st, box, fake.URL(), nil)
	hub := events.NewHub()
	mediaDir := t.TempDir()
	dl := downloader.New(st, mediaDir, 0, nil)
	ctx, cancel := context.WithCancel(context.Background())
	mgr := collector.New(ctx, collector.Deps{Store: st, Clients: reg, Downloader: dl, Receipts: receipt.New(st, reg), Hub: hub, MediaDir: mediaDir, PollTimeoutSec: 1})
	t.Cleanup(func() { mgr.StopAll(); cancel(); st.Close() })
	srv := &Server{
		Cfg:   &config.Config{RequireForwardAuth: true, BotAPIURL: fake.URL(), CloudAPIURL: fake.URL()},
		Store: st, Box: box, Clients: reg, Manager: mgr, Downloader: dl, Hub: hub,
		MediaDir: mediaDir, AvatarDir: t.TempDir(), Now: time.Now,
	}
	return &adminEnv{h: srv.Handler(), st: st, fake: fake, mgr: mgr}
}

func call(h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Remote-User", "shinya")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

type addResp struct {
	BotID int64  `json:"bot_id"`
	Error string `json:"error"`
	Steps []struct {
		Step   string `json:"step"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	} `json:"steps"`
}

func waitStatus(t *testing.T, st *store.Store, id int64, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := st.GetBot(context.Background(), id)
		if b != nil && b.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("bot %d status = %v, want %s", id, b, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAddBot(t *testing.T) {
	e := newAdminEnv(t)
	if w := call(e.h, "POST", "/api/admin/bots", map[string]string{"token": "nope"}); w.Code != 400 {
		t.Fatalf("bad format = %d", w.Code)
	}
	e.fake.RejectToken("888:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	w := call(e.h, "POST", "/api/admin/bots", map[string]string{"token": "888:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"})
	var r addResp
	json.Unmarshal(w.Body.Bytes(), &r)
	if w.Code != 400 || len(r.Steps) != 1 || r.Steps[0].Step != "getMe" || r.Steps[0].OK {
		t.Fatalf("rejected token = %d %s", w.Code, w.Body)
	}

	w = call(e.h, "POST", "/api/admin/bots", map[string]string{"token": " " + goodToken + "\n"})
	r = addResp{}
	json.Unmarshal(w.Body.Bytes(), &r)
	if w.Code != 200 || r.BotID == 0 || len(r.Steps) != 3 || r.Steps[0].Detail != "@archive_bot" {
		t.Fatalf("add = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), goodToken) {
		t.Fatal("token echoed in response")
	}
	if len(e.fake.Calls("logOut")) != 1 {
		t.Fatal("logOut must be called on the cloud API")
	}
	waitStatus(t, e.st, r.BotID, store.StatusRunning)

	w = call(e.h, "POST", "/api/admin/bots", map[string]string{"token": goodToken})
	if w.Code != 409 {
		t.Fatalf("duplicate = %d %s", w.Code, w.Body)
	}
}

func TestToggleAndDeleteBot(t *testing.T) {
	e := newAdminEnv(t)
	var r addResp
	json.Unmarshal(call(e.h, "POST", "/api/admin/bots", map[string]string{"token": goodToken}).Body.Bytes(), &r)
	waitStatus(t, e.st, r.BotID, store.StatusRunning)
	p := fmt.Sprintf("/api/admin/bots/%d", r.BotID)

	w := call(e.h, "PATCH", p, map[string]bool{"enabled": false})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"stopped"`) || e.mgr.Running(r.BotID) {
		t.Fatalf("disable = %d %s running=%v", w.Code, w.Body, e.mgr.Running(r.BotID))
	}
	if w := call(e.h, "PATCH", p, map[string]bool{"enabled": true}); w.Code != 200 {
		t.Fatalf("enable = %d", w.Code)
	}
	waitStatus(t, e.st, r.BotID, store.StatusRunning)

	if w := call(e.h, "DELETE", p, nil); w.Code != 204 {
		t.Fatalf("remove = %d", w.Code)
	}
	waitStatus(t, e.st, r.BotID, store.StatusRemoved)
	if w := call(e.h, "PATCH", p, map[string]bool{"enabled": true}); w.Code != 404 {
		t.Fatalf("enabling a removed bot = %d", w.Code)
	}

	w = call(e.h, "POST", "/api/admin/bots", map[string]string{"token": goodToken})
	r2 := addResp{}
	json.Unmarshal(w.Body.Bytes(), &r2)
	if w.Code != 200 || r2.BotID != r.BotID {
		t.Fatalf("re-add of removed bot = %d %s", w.Code, w.Body)
	}
	if w := call(e.h, "DELETE", p+"?purge=1", nil); w.Code != 204 {
		t.Fatalf("purge = %d", w.Code)
	}
	if _, err := e.st.GetBot(context.Background(), r.BotID); err == nil {
		t.Fatal("purged bot still exists")
	}
	if w := call(e.h, "DELETE", p+"?purge=1", nil); w.Code != 404 {
		t.Fatalf("purge missing = %d", w.Code)
	}
}

func TestWhitelistAndRejected(t *testing.T) {
	e := newAdminEnv(t)
	var r addResp
	json.Unmarshal(call(e.h, "POST", "/api/admin/bots", map[string]string{"token": goodToken}).Body.Bytes(), &r)
	base := fmt.Sprintf("/api/admin/bots/%d", r.BotID)

	e.fake.PushMessage(tgtest.TextMsg(1, 99, "hi"))
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(call(e.h, "GET", base+"/rejected", nil).Body.String(), `"tg_user_id":99`) {
		if time.Now().After(deadline) {
			t.Fatal("rejected sender not listed")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if w := call(e.h, "PUT", base+"/whitelist/99", map[string]any{"note": "friend", "can_fetch": true}); w.Code != 204 {
		t.Fatalf("put = %d %s", w.Code, w.Body)
	}
	w := call(e.h, "GET", base+"/whitelist", nil)
	if !strings.Contains(w.Body.String(), `"tg_user_id":99`) || !strings.Contains(w.Body.String(), `"can_fetch":true`) {
		t.Fatalf("whitelist = %s", w.Body)
	}
	if strings.Contains(call(e.h, "GET", base+"/rejected", nil).Body.String(), `"tg_user_id":99`) {
		t.Fatal("whitelisted user must leave the rejected list")
	}
	if w := call(e.h, "DELETE", base+"/whitelist/99", nil); w.Code != 204 {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := call(e.h, "DELETE", base+"/whitelist/99", nil); w.Code != 404 {
		t.Fatalf("delete missing = %d", w.Code)
	}
	if w := call(e.h, "PUT", "/api/admin/bots/9999/whitelist/1", map[string]any{}); w.Code != 404 {
		t.Fatalf("put on missing bot = %d", w.Code)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/httpapi/ -run 'TestAddBot|TestToggle|TestWhitelist'`
Expected: FAIL（404 / 405，路由未注册）

- [ ] **Step 3: 实现** `internal/httpapi/admin.go`

```go
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"tgarchive/internal/downloader"
	"tgarchive/internal/store"
	"tgarchive/internal/tgbot"
)

var tokenRe = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]{30,}$`)

type step struct {
	Step   string `json:"step"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/bots", s.addBot)
	mux.HandleFunc("PATCH /api/admin/bots/{id}", s.patchBot)
	mux.HandleFunc("DELETE /api/admin/bots/{id}", s.deleteBot)
	mux.HandleFunc("GET /api/admin/bots/{id}/whitelist", s.listWhitelist)
	mux.HandleFunc("PUT /api/admin/bots/{id}/whitelist/{uid}", s.putWhitelist)
	mux.HandleFunc("DELETE /api/admin/bots/{id}/whitelist/{uid}", s.deleteWhitelist)
	mux.HandleFunc("GET /api/admin/bots/{id}/rejected", s.listRejected)
}

func (s *Server) addBot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	token := strings.TrimSpace(req.Token)
	if !tokenRe.MatchString(token) {
		writeErr(w, http.StatusBadRequest, "token 格式不正确")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	steps := []step{}

	me, err := tgbot.New(s.Cfg.BotAPIURL, token, s.HTTP).GetMe(ctx)
	if err != nil {
		steps = append(steps, step{"getMe", false, err.Error()})
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "token 校验失败", "steps": steps})
		return
	}
	steps = append(steps, step{"getMe", true, "@" + me.Username})

	if existing, err := s.Store.GetBotByTgID(ctx, me.ID); err == nil && existing.Status != store.StatusRemoved {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "机器人已存在", "bot_id": existing.ID, "steps": steps})
		return
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		storeErr(w, err)
		return
	}

	if err := tgbot.New(s.Cfg.CloudAPIURL, token, s.HTTP).LogOut(ctx); err != nil {
		steps = append(steps, step{"logOut", true, "云端未登出（可能已登出）：" + err.Error()})
	} else {
		steps = append(steps, step{"logOut", true, "已从云端登出"})
	}

	id, err := s.Store.UpsertBot(ctx, &store.Bot{
		TgBotID: me.ID, Username: me.Username, Name: me.FirstName,
		TokenEnc: s.Box.Seal([]byte(token)), CreatedAt: s.Now().Unix(),
	})
	if err != nil {
		storeErr(w, err)
		return
	}
	s.Clients.Forget(id)
	s.Manager.Start(id)
	steps = append(steps, step{"start", true, ""})
	if s.Avatars != nil {
		go s.Avatars.RefreshBot(context.WithoutCancel(r.Context()), id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"bot_id": id, "steps": steps})
}

func (s *Server) patchBot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !ok || json.NewDecoder(r.Body).Decode(&req) != nil || req.Enabled == nil {
		writeErr(w, http.StatusBadRequest, `body must be {"enabled": bool}`)
		return
	}
	ctx := r.Context()
	if err := s.Store.SetBotEnabled(ctx, id, *req.Enabled); err != nil {
		storeErr(w, err)
		return
	}
	s.Manager.Stop(id) // also restarts cleanly when re-enabling a bot in error state
	if err := s.Store.SetBotStatus(ctx, id, store.StatusStopped, ""); err != nil {
		storeErr(w, err)
		return
	}
	if *req.Enabled {
		s.Clients.Forget(id)
		s.Manager.Start(id)
	}
	b, err := s.Store.GetBot(ctx, id)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, botView(b))
}

func (s *Server) deleteBot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad bot id")
		return
	}
	s.Manager.Stop(id)
	s.Clients.Forget(id)
	if r.URL.Query().Get("purge") == "1" {
		paths, err := s.Store.PurgeBot(r.Context(), id)
		if err != nil {
			storeErr(w, err)
			return
		}
		downloader.RemoveFiles(s.MediaDir, paths)
	} else if err := s.Store.RemoveBot(r.Context(), id); err != nil {
		storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) requireBot(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad bot id")
		return 0, false
	}
	if _, err := s.Store.GetBot(r.Context(), id); err != nil {
		storeErr(w, err)
		return 0, false
	}
	return id, true
}

func (s *Server) listWhitelist(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireBot(w, r)
	if !ok {
		return
	}
	entries, err := s.Store.ListWhitelist(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	type view struct {
		TgUserID int64  `json:"tg_user_id"`
		Note     string `json:"note"`
		CanFetch bool   `json:"can_fetch"`
	}
	out := make([]view, 0, len(entries))
	for _, e := range entries {
		out = append(out, view{e.TgUserID, e.Note, e.CanFetch})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putWhitelist(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireBot(w, r)
	if !ok {
		return
	}
	uid, err := strconv.ParseInt(r.PathValue("uid"), 10, 64)
	var req struct {
		Note     string `json:"note"`
		CanFetch bool   `json:"can_fetch"`
	}
	if err != nil || uid <= 0 || json.NewDecoder(r.Body).Decode(&req) != nil {
		writeErr(w, http.StatusBadRequest, "bad user id or body")
		return
	}
	if err := s.Store.PutWhitelist(r.Context(), store.WhitelistEntry{BotID: id, TgUserID: uid, Note: req.Note, CanFetch: req.CanFetch}); err != nil {
		storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteWhitelist(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireBot(w, r)
	if !ok {
		return
	}
	uid, err := strconv.ParseInt(r.PathValue("uid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad user id")
		return
	}
	if err := s.Store.DeleteWhitelist(r.Context(), id, uid); err != nil {
		storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRejected(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireBot(w, r)
	if !ok {
		return
	}
	rej, err := s.Store.ListRejected(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	type view struct {
		TgUserID   int64  `json:"tg_user_id"`
		FirstName  string `json:"first_name"`
		Username   string `json:"username"`
		LastSeenAt int64  `json:"last_seen_at"`
		Count      int64  `json:"count"`
	}
	out := make([]view, 0, len(rej))
	for _, x := range rej {
		out = append(out, view{x.TgUserID, x.FirstName, x.Username, x.LastSeenAt, x.Count})
	}
	writeJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test -race ./internal/httpapi/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi
git commit -m "feat(httpapi): bot, whitelist and rejected-sender admin API"
```

---

### Task 16: 装配、入口与端到端测试

**Files:**
- Create: `internal/app/app.go`, `internal/app/app_test.go`, `cmd/tgarchive/main.go`
- Modify: `internal/tgtest/fake.go`（增加 `HoldFiles`）

**Interfaces:**
- Consumes: 前面所有包
- Produces: `app.New(parent context.Context, cfg *config.Config) (*app.App, error)`；`(*App).Handler http.Handler`（字段）；`(*App).Start() error`；`(*App).Close()`；`tgtest.(*FakeTG).HoldFiles(on bool)`

`Start` 启动下载循环、全部启用的 worker、维护循环（启动 1 分钟后首次执行，之后每 24 小时：清理 Bot API 缓存目录中超过 24 小时的文件、刷新全部头像）。`Close` 取消上下文、停止全部 worker、等待下载循环退出、关闭数据库。

- [ ] **Step 1: 修改假服务器** `internal/tgtest/fake.go`

在 `FakeTG` 结构体中增加字段：

```go
	holdFiles  bool
```

增加方法：

```go
// HoldFiles makes getFile block until released or the request is cancelled,
// simulating a download in progress.
func (f *FakeTG) HoldFiles(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holdFiles = on
}
```

把 `serve` 中的 `case "getFile": f.getFile(w, token, params)` 改为 `case "getFile": f.getFile(w, r, token, params)`，并把 `getFile` 改为：

```go
func (f *FakeTG) getFile(w http.ResponseWriter, r *http.Request, token string, params map[string]any) {
	for {
		f.mu.Lock()
		hold := f.holdFiles
		f.mu.Unlock()
		if !hold {
			break
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	fileID, _ := params["file_id"].(string)
	f.mu.Lock()
	content, has := f.files[fileID]
	f.mu.Unlock()
	if !has {
		fail(w, 400, "Bad Request: invalid file_id")
		return
	}
	dir := filepath.Join(f.RemoteDir, token, "documents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(w, 500, err.Error())
		return
	}
	p := filepath.Join(dir, fileID+".jpg")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		fail(w, 500, err.Error())
		return
	}
	ok(w, map[string]any{"file_id": fileID, "file_unique_id": "u" + fileID, "file_size": len(content), "file_path": p})
}
```

- [ ] **Step 2: 写失败测试** `internal/app/app_test.go`

```go
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tgarchive/internal/config"
	"tgarchive/internal/store"
	"tgarchive/internal/tgtest"
)

const token = "777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func cfgFor(fake *tgtest.FakeTG, dataDir string) *config.Config {
	return &config.Config{
		DataDir: dataDir, BotAPIURL: fake.URL(), CloudAPIURL: fake.URL(),
		BotAPIDirRemote: fake.RemoteDir, BotAPIDirLocal: fake.RemoteDir,
		TokenEncKey: bytes.Repeat([]byte{7}, 32), RequireForwardAuth: true, PollTimeoutSec: 1,
	}
}

func start(t *testing.T, cfg *config.Config) *App {
	t.Helper()
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	return a
}

func req(t *testing.T, h http.Handler, method, path string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rd)
	r.Header.Set("Remote-User", "shinya")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func emojis(f *tgtest.FakeTG) []string {
	var out []string
	for _, c := range f.Calls("setMessageReaction") {
		out = append(out, c.Params["reaction"].([]any)[0].(map[string]any)["emoji"].(string))
	}
	return out
}

func firstChatMessages(t *testing.T, h http.Handler) []store.MessageView {
	_, b := req(t, h, "GET", "/api/chats", nil)
	var chats []store.ChatView
	json.Unmarshal(b, &chats)
	if len(chats) == 0 {
		return nil
	}
	_, b = req(t, h, "GET", fmt.Sprintf("/api/chats/%d/messages", chats[0].ID), nil)
	var msgs []store.MessageView
	json.Unmarshal(b, &msgs)
	return msgs
}

func addBotAndWhitelist(t *testing.T, h http.Handler, uid int64) int64 {
	code, b := req(t, h, "POST", "/api/admin/bots", map[string]string{"token": token})
	if code != 200 {
		t.Fatalf("add bot = %d %s", code, b)
	}
	var r struct {
		BotID int64 `json:"bot_id"`
	}
	json.Unmarshal(b, &r)
	if code, b := req(t, h, "PUT", fmt.Sprintf("/api/admin/bots/%d/whitelist/%d", r.BotID, uid), map[string]any{"note": "me"}); code != 204 {
		t.Fatalf("whitelist = %d %s", code, b)
	}
	return r.BotID
}

func TestEndToEnd(t *testing.T) {
	fake := tgtest.New(t)
	fake.AddFile("ph", []byte("PHOTO"))
	fake.AddFile("ph-s", []byte("t"))
	a := start(t, cfgFor(fake, t.TempDir()))
	defer a.Close()
	h := a.Handler

	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest("GET", "/api/bots", nil))
	if unauth.Code != 401 {
		t.Fatalf("unauthenticated = %d", unauth.Code)
	}

	addBotAndWhitelist(t, h, 42)
	fake.PushMessage(tgtest.TextMsg(1, 99, "stranger"))
	fake.PushMessage(tgtest.PhotoMsg(2, 42, "ph"))

	eventually(t, "👌", func() bool {
		e := emojis(fake)
		return len(e) > 0 && e[len(e)-1] == "👌"
	})
	msgs := firstChatMessages(t, h)
	if len(msgs) != 1 || msgs[0].Kind != "photo" || msgs[0].Media[0].State != store.StateDone {
		t.Fatalf("messages = %+v", msgs)
	}
	code, body := req(t, h, "GET", fmt.Sprintf("/media/%d", msgs[0].Media[0].ID), nil)
	if code != 200 || string(body) != "PHOTO" {
		t.Fatalf("media = %d %q", code, body)
	}
	if matches, _ := filepath.Glob(filepath.Join(fake.RemoteDir, "*", "documents", "ph.jpg")); len(matches) != 0 {
		t.Fatal("bot api cache file not cleaned up")
	}
	if len(fake.Calls("sendMessage")) != 0 {
		t.Fatal("no replies expected on success or for strangers")
	}
	_, body = req(t, h, "GET", "/api/admin/bots/1/rejected", nil)
	if !strings.Contains(string(body), `"tg_user_id":99`) {
		t.Fatalf("rejected = %s", body)
	}

	fake.PushMessage(tgtest.PhotoMsg(3, 42, "ph")) // same file again: deduped, no second download
	eventually(t, "second 👌", func() bool {
		n := 0
		for _, e := range emojis(fake) {
			if e == "👌" {
				n++
			}
		}
		return n == 2
	})
	gets := 0
	for _, c := range fake.Calls("getFile") {
		if c.Params["file_id"] == "ph" {
			gets++
		}
	}
	if gets != 1 {
		t.Fatalf("deduped file fetched %d times", gets)
	}
}

func TestRestartResumes(t *testing.T) {
	fake := tgtest.New(t)
	fake.AddFile("ph", []byte("PHOTO"))
	fake.AddFile("ph-s", []byte("t"))
	fake.HoldFiles(true)
	dataDir := t.TempDir()

	a1 := start(t, cfgFor(fake, dataDir))
	addBotAndWhitelist(t, a1.Handler, 42)
	fake.PushMessage(tgtest.PhotoMsg(1, 42, "ph"))
	eventually(t, "👀 before restart", func() bool { e := emojis(fake); return len(e) == 1 && e[0] == "👀" })
	a1.Close()

	fake.HoldFiles(false)
	a2 := start(t, cfgFor(fake, dataDir))
	defer a2.Close()
	eventually(t, "👌 after restart", func() bool { e := emojis(fake); return len(e) == 2 && e[1] == "👌" })
	eventually(t, "poll after restart", func() bool { return fake.LastOffset() == 2 })
	msgs := firstChatMessages(t, a2.Handler)
	if len(msgs) != 1 || msgs[0].Media[0].State != store.StateDone {
		t.Fatalf("messages after restart = %+v", msgs)
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/app/`
Expected: FAIL（`undefined: New`）

- [ ] **Step 4: 实现** `internal/app/app.go`

```go
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
```

`cmd/tgarchive/main.go`

```go
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
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Printf("tgarchive listening on %s", cfg.Listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	a.Close()
}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test -race ./... && go vet ./...`
Expected: 全部 PASS

- [ ] **Step 6: 本地冒烟**

```bash
CGO_ENABLED=0 go build -o tgarchive ./cmd/tgarchive
mkdir -p tmp
TOKEN_ENC_KEY=$(openssl rand -hex 32) DATA_DIR=./tmp LISTEN=127.0.0.1:18080 REQUIRE_FORWARD_AUTH=false ./tgarchive &
sleep 1
curl -fsS 127.0.0.1:18080/healthz          # 期望：ok
curl -fsS 127.0.0.1:18080/api/bots          # 期望：[]
curl -fsS 127.0.0.1:18080/ | head -c 80     # 期望：占位页 HTML
kill %1
rm -rf tmp tgarchive
```

Expected: 三条请求均成功，进程收到 SIGTERM 后正常退出。

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: app wiring, entrypoint and end-to-end tests"
```

---

## 后续计划（不在本计划内）

- **计划 2：userbot 代取**——`internal/linkparse`、`internal/convert/mtproto`、`internal/userbot`（gotd 登录流程、session 加密存 `userbot` 表、串行队列、FLOOD_WAIT），实现 `collector.LinkHandler` 与 `downloader.Source`（前缀 `mt`），新增 `/api/admin/userbot/*`
- **计划 3：WebUI**——先读 `Ajaxy/telegram-tt` 源码定位要移植的 SCSS/组件/算法，Preact + Vite 构建到 `web/dist`；bot 语音无 waveform，前端用 WebAudio 解码计算
- **计划 4：部署**——Dockerfile（node → go → distroless）、compose（`tgarchive` + `tgarchive-botapi`，`tgarchive-internal` 网络，`/opt/app/tgarchive` 挂到 `/data`、`/opt/app/tgarchive/botapi` 挂到 Bot API 容器的 `/var/lib/telegram-bot-api`）、Caddy、DNS、Cup exclude、备份 exclude 与 NAS 拉取、基础设施文档
