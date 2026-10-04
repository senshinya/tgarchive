# tgarchive 计划 2：userbot 链接代取 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 白名单中有 `can_fetch` 权限的发送者把 t.me 消息链接发给任一机器人后，由 userbot（gotd/td MTProto 用户账号）代取原消息（含相册与媒体），存入「发送者 × 该机器人」会话；userbot 的登录（手机号 → 验证码 → 二步验证）全部在 Web 上完成。

**Architecture:** `linkparse` 纯函数识别链接；`collector` 已有的 `LinkHandler` 钩子由 `userbot.Fetcher` 实现：只做幂等入队（`fetch_jobs` 表，按 `(bot_id, sender_id, link_tg_message_id)` 唯一）并回 👀；`Fetcher.Run` 串行消费队列，经 `userbot.Service`（持有 gotd 连接、登录状态机、加密 session）解析 peer、取消息，`convert/mtproto` 转换后走既有 `store.Ingest`，媒体以 `mt:` 前缀进入既有下载队列，由 `userbot.MTSource` 经 `upload.getFile` 下载。回执按 job 聚合：所有代取消息的主媒体落定后在原链接消息上设 👌 或回复失败原因。

**Tech Stack:** Go 1.26、`modernc.org/sqlite` v1.60.1、`github.com/gotd/td` v0.162.0（MTProto；测试用其 `tgmock` 包模拟 RPC）。

**Spec:** `docs/superpowers/specs/2026-10-04-tgarchive-design.md`（§4 数据模型、§5 回执、§6 userbot、§7 HTTP）

**前置：** 计划 1 已合入 `main`。计划 1 遗留事项见 `docs/superpowers/plans/2026-10-04-tgarchive-p1-carryover.md`；其中「TryHandle 必须跨重投幂等」由本计划 Task 6 解决。

**本计划不含**：Preact 前端（计划 3，含 userbot 登录页 UI）、部署（计划 4）。

## Global Constraints

- 依赖只允许 `modernc.org/sqlite v1.60.1` 与 `github.com/gotd/td v0.162.0`（及其传递依赖）；`go.mod` 的 `go` 指令保持不变
- 数据库 `MaxOpenConns(1)`：**持有 `*sql.Rows` 时不得再发起查询；事务内只能用 `tx`**
- userbot 的 `api_id` / `api_hash` 只从 `internal/tgapp` 读取（与 Bot API 共用），不读环境变量；未配置时登录接口返回 409 `请先在设置中填写 api_id / api_hash`
- gotd session 用 `seal.Box`（`TOKEN_ENC_KEY`）加密后存 `userbot.session_enc`；session、验证码、二步验证密码绝不写日志、不出现在 API 响应中；密码不落库
- 只支持一个 userbot 账号（`userbot` 表 `id = 1`）
- 代取队列串行，相邻任务间隔 ≥ 3 秒
- `FLOOD_WAIT_X`：X ≤ 300 秒等待 X+1 秒后重试（同一任务最多重试 3 次）；否则失败，原因 `被限流，请 N 分钟后重试`（N = ⌈X/60⌉）
- MTProto 401 类错误（`auth.IsUnauthorized`）→ userbot 状态 `error`、Bark 推送一次（标题 `tgarchive 代取账号失效`）、丢弃 session 等待重新登录；其间代取请求失败原因 `代取账号未登录`
- 回执作用在**原链接消息**上：入队 👀；全部代取消息的主媒体落定后 👌；代取失败回复 `⚠️ 代取失败：<原因>`；媒体最终失败回复 `⚠️ 存档失败：<原因>`；存在超限媒体时 👌 后回复 `文件超过存档上限，仅保存了消息记录`
- 代取失败原因固定文案：`不支持的链接格式`、`代取账号未登录`、`私有群/频道，代取账号未加入`、`消息不存在或已被删除`、`链接不是频道或超级群消息`、`频道或群组不存在`、`被限流，请 N 分钟后重试`
- 代取消息：`source = userbot_fetch`、`raw_format = mtproto`、`tg_message_id` = 原群消息 ID、`origin_chat_id` = Bot API 形式 `-100<channel_id>`（即 `-(1000000000000 + channel_id)`）、`origin_link` = `https://t.me/<username>/<msg>` 或 `https://t.me/c/<channel_id>/<msg>`
- 代取媒体 `dedupe_key`：照片 `mt:photo:<photo_id>`、照片缩略 `mt:photo:<photo_id>:<size_type>`、文档 `mt:doc:<document_id>`、文档缩略 `mt:doc:<document_id>:thumb`；落盘路径由既有 `downloader.relBase` 决定（`media/mt/<yyyy>/<mm>/<sha1>.<ext>`）
- 只处理不含媒体、`kind = text` 且整条文本（去首尾空白）恰为一个 `t.me` / `telegram.me` 链接的消息；无 `can_fetch` 者的链接按普通消息存档
- 每次 Bot API 回执调用超时 30s（沿用 `receipt` 包）

## Review Focus

1. **同一链接 update 被重投**（`TryHandle` 已接手但推进 offset 失败）→ 只建一个 job、只回一次 👀、不重复回复 → Task 6 测试 `TestTryHandleIdempotent`、`TestUnsupportedLinkRepliesOnce`
2. **进程在代取中途重启** → `fetching` 的 job 重新排队并只完成一次，消息不重复 → Task 2 测试 `TestRequeueFetchingJobs` + Task 6 测试 `TestRunRequeuesInterruptedJob`
3. **媒体重试时 file reference 已过期** → 重新取消息拿新引用后下载成功 → Task 7 测试 `TestMTSourceRefreshesExpiredReference`
4. **相册链接**：不带 `?single` 取回整组且按消息 ID 升序；带 `?single` 只取一条 → Task 6 测试 `TestFetchAlbum`
5. **userbot session 中途失效** → 状态 `error`、Bark 只推一次、后续 job 失败为 `代取账号未登录` 而非死循环 → Task 5 测试 `TestUnauthorizedMarksErrorAndNotifiesOnce` + Task 6 测试 `TestFetchFailures`（`代取账号未登录` 用例）

---

## 文件结构

```
internal/linkparse/linkparse.go          t.me 链接识别与解析（纯函数）
internal/store/migrations/0003_userbot.sql  userbot_peers、fetch_jobs、fetch_job_messages
internal/store/userbot.go                userbot 单行状态、session 密文、私有频道 peer 缓存
internal/store/fetchjobs.go              代取任务队列与 job 回执信息
internal/store/senders.go                （改）GetSender / UpsertSender
internal/store/ingest.go                 （改）代取消息以入库时间刷新会话排序
internal/convert/mtproto/convert.go      gotd tg.Message → model.Message；MediaRef
internal/receipt/receipt.go              （改）EvaluateJob；MediaSettled 同时评估 job
internal/userbot/session.go              加密 session 存储（gotd session.Storage）
internal/userbot/dialer.go               Dialer 接口与 gotd 实现
internal/userbot/service.go              连接生命周期、登录状态机、With/WaitReady
internal/userbot/fetcher.go              LinkHandler 实现 + 串行代取队列
internal/userbot/mtsource.go             downloader.Source（"mt:" 前缀）
internal/httpapi/userbot.go              /api/admin/userbot[...]
internal/httpapi/server.go / settings.go （改）注入 Userbot；保存 api 凭据后 Reload
internal/app/app.go                      （改）装配；e2e 测试
docs/superpowers/specs/2026-10-04-tgarchive-design.md  （改）同步本计划的细化
```

---

### Task 1: linkparse —— t.me 消息链接解析

**Files:**
- Create: `internal/linkparse/linkparse.go`
- Test: `internal/linkparse/linkparse_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `type Link struct { Username string; ChannelID, TopicID, MsgID int64; Single bool }`
  - `var ErrNotLink, ErrUnsupported error`（`ErrUnsupported.Error() == "不支持的链接格式"`）
  - `func Candidate(text string) (string, bool)` —— 文本（去首尾空白）恰为一个 t.me/telegram.me URL 时返回该 URL
  - `func Parse(s string) (Link, error)`

- [ ] **Step 1: 写失败测试**

```go
package linkparse

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	ok := []struct {
		in   string
		want Link
	}{
		{"https://t.me/durov/123", Link{Username: "durov", MsgID: 123}},
		{"t.me/durov/123", Link{Username: "durov", MsgID: 123}},
		{"http://telegram.me/durov/123", Link{Username: "durov", MsgID: 123}},
		{"https://www.t.me/durov/123", Link{Username: "durov", MsgID: 123}},
		{"HTTPS://T.ME/durov/123", Link{Username: "durov", MsgID: 123}},
		{"https://t.me/durov/123?single", Link{Username: "durov", MsgID: 123, Single: true}},
		{"https://t.me/durov/123?single=1&comment=5", Link{Username: "durov", MsgID: 123, Single: true}},
		{"https://t.me/durov/9/123", Link{Username: "durov", TopicID: 9, MsgID: 123}},
		{"https://t.me/s/durov/123", Link{Username: "durov", MsgID: 123}},
		{"https://t.me/c/1234567890/55", Link{ChannelID: 1234567890, MsgID: 55}},
		{"https://t.me/c/1234567890/7/55?single", Link{ChannelID: 1234567890, TopicID: 7, MsgID: 55, Single: true}},
		{"https://t.me/durov/123/", Link{Username: "durov", MsgID: 123}},
	}
	for _, c := range ok {
		got, err := Parse(c.in)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	unsupported := []string{
		"https://t.me/durov", "https://t.me/", "https://t.me/joinchat/AAAA", "https://t.me/+AbCdEf",
		"https://t.me/addstickers/foo", "https://t.me/c/123", "https://t.me/c/abc/5", "https://t.me/durov/0",
		"https://t.me/durov/-1", "https://t.me/durov/x", "https://t.me/ab/5", "https://t.me/durov/1/2/3",
		"https://t.me/share/url/5",
	}
	for _, in := range unsupported {
		if _, err := Parse(in); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Parse(%q) err = %v, want ErrUnsupported", in, err)
		}
	}
	for _, in := range []string{"hello", "https://example.com/durov/1", "https://t.me.evil.com/durov/1", "https://t.me:8443/durov/1", "ftp://t.me/durov/1"} {
		if _, err := Parse(in); !errors.Is(err, ErrNotLink) {
			t.Errorf("Parse(%q) err = %v, want ErrNotLink", in, err)
		}
	}
	if ErrUnsupported.Error() != "不支持的链接格式" {
		t.Fatal("ErrUnsupported text changed")
	}
}

func TestCandidate(t *testing.T) {
	for _, in := range []string{"https://t.me/durov/1", "  t.me/durov/1\n", "https://t.me/joinchat/x", "https://t.me/durov"} {
		if _, ok := Candidate(in); !ok {
			t.Errorf("Candidate(%q) = false", in)
		}
	}
	for _, in := range []string{"", "hello", "see https://t.me/durov/1", "https://t.me/durov/1 https://t.me/durov/2", "https://example.com/a/1"} {
		if _, ok := Candidate(in); ok {
			t.Errorf("Candidate(%q) = true", in)
		}
	}
	if got, _ := Candidate("  t.me/durov/1 "); got != "t.me/durov/1" {
		t.Fatalf("Candidate trims to %q", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/linkparse/`
Expected: FAIL（`undefined: Parse` 等）

- [ ] **Step 3: 实现**

```go
// Package linkparse recognises Telegram message links (t.me/...) that the userbot can fetch.
package linkparse

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Link is a parsed message link. Exactly one of Username / ChannelID is set.
type Link struct {
	Username  string // public link: t.me/<username>/...
	ChannelID int64  // private link: t.me/c/<channel_id>/...
	TopicID   int64
	MsgID     int64
	Single    bool // ?single: only this message, not its whole album
}

var (
	ErrNotLink     = errors.New("not a telegram link")
	ErrUnsupported = errors.New("不支持的链接格式")
)

var usernameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)

// reserved are first path segments that look like usernames but are Telegram service paths.
var reserved = map[string]bool{
	"joinchat": true, "addstickers": true, "addemoji": true, "addlist": true, "addtheme": true, "share": true,
	"proxy": true, "socks": true, "boost": true, "login": true, "invoice": true, "giftcode": true,
	"setlanguage": true, "confirmphone": true, "contact": true,
}

// Candidate reports whether text, trimmed, is exactly one t.me / telegram.me URL and returns it.
func Candidate(text string) (string, bool) {
	s := strings.TrimSpace(text)
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return "", false
	}
	if _, _, err := split(s); err != nil {
		return "", false
	}
	return s, true
}

// Parse parses a message link. Non-Telegram input yields ErrNotLink; Telegram URLs that are not
// message links yield ErrUnsupported.
func Parse(s string) (Link, error) {
	segs, q, err := split(strings.TrimSpace(s))
	if err != nil {
		return Link{}, err
	}
	var l Link
	_, l.Single = q["single"]
	if len(segs) > 0 && segs[0] == "s" { // t.me/s/<username>/<msg> is the web preview of the same post
		segs = segs[1:]
	}
	if len(segs) > 0 && segs[0] == "c" {
		if len(segs) != 3 && len(segs) != 4 {
			return Link{}, ErrUnsupported
		}
		id, ok := positive(segs[1])
		if !ok {
			return Link{}, ErrUnsupported
		}
		l.ChannelID, segs = id, segs[2:]
	} else {
		if (len(segs) != 2 && len(segs) != 3) || !usernameRe.MatchString(segs[0]) || reserved[strings.ToLower(segs[0])] {
			return Link{}, ErrUnsupported
		}
		l.Username, segs = segs[0], segs[1:]
	}
	if len(segs) == 2 {
		topic, ok := positive(segs[0])
		if !ok {
			return Link{}, ErrUnsupported
		}
		l.TopicID, segs = topic, segs[1:]
	}
	msg, ok := positive(segs[0])
	if !ok {
		return Link{}, ErrUnsupported
	}
	l.MsgID = msg
	return l, nil
}

func split(s string) ([]string, url.Values, error) {
	rest := s
	switch lower := strings.ToLower(s); {
	case strings.HasPrefix(lower, "https://"):
		rest = s[len("https://"):]
	case strings.HasPrefix(lower, "http://"):
		rest = s[len("http://"):]
	case strings.Contains(lower, "://"):
		return nil, nil, ErrNotLink
	}
	u, err := url.Parse("https://" + rest)
	if err != nil || u.User != nil || u.Port() != "" {
		return nil, nil, ErrNotLink
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host != "t.me" && host != "telegram.me" {
		return nil, nil, ErrNotLink
	}
	p := strings.Trim(u.Path, "/")
	var segs []string
	if p != "" {
		segs = strings.Split(p, "/")
	}
	return segs, u.Query(), nil
}

func positive(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/linkparse/ && gofmt -l internal/linkparse`
Expected: PASS，gofmt 无输出

- [ ] **Step 5: 提交**

```bash
git add internal/linkparse/linkparse.go internal/linkparse/linkparse_test.go
git commit -m "feat(linkparse): parse t.me message links"
```

---

### Task 2: store —— userbot 状态、peer 缓存、代取任务队列

**Files:**
- Create: `internal/store/migrations/0003_userbot.sql`
- Create: `internal/store/userbot.go`
- Create: `internal/store/fetchjobs.go`
- Modify: `internal/store/senders.go`（新增 `GetSender`、`UpsertSender`）
- Modify: `internal/store/ingest.go`（`userbot_fetch` 消息以 `in.Now` 刷新 `chats.last_message_at`）
- Modify: `internal/store/store_test.go`（`TestMigrateIdempotent` 期望 `user_version` 从 2 改为 3）
- Test: `internal/store/userbot_test.go`、`internal/store/fetchjobs_test.go`

**Interfaces:**
- Consumes: 既有 `Store`、`withTx`、`affected`、`ErrNotFound`、`MediaStatus`、`model.Sender`
- Produces:
  - 常量 `UserbotLoggedOut = "logged_out"`、`UserbotReady = "ready"`、`UserbotError = "error"`
  - `type Userbot struct { Phone string; TgUserID int64; Name string; SessionEnc []byte; Status, LastError string; UpdatedAt int64 }`
  - `func (s *Store) GetUserbot(ctx) (*Userbot, error)` —— 行不存在时返回 `&Userbot{Status: UserbotLoggedOut}, nil`
  - `func (s *Store) SaveUserbotSession(ctx, enc []byte, now int64) error`
  - `func (s *Store) SetUserbotAccount(ctx, phone string, tgUserID int64, name string, now int64) error` —— 同时置 `status = ready`、`last_error = ''`
  - `func (s *Store) SetUserbotStatus(ctx, status, lastErr string, now int64) error`
  - `func (s *Store) ClearUserbot(ctx, now int64) error` —— 清空 session/账号，`status = logged_out`
  - `type Peer struct { ChannelID, AccessHash int64; Username, Title string }`
  - `func (s *Store) PutPeers(ctx, peers []Peer, now int64) error`、`func (s *Store) GetPeer(ctx, channelID int64) (*Peer, error)`
  - 常量 `JobQueued = "queued"`、`JobFetching = "fetching"`、`JobFetched = "fetched"`、`JobFailed = "failed"`、`JobUnsupported = "unsupported"`
  - `type FetchJob struct { ID, BotID, SenderID, LinkTgMessageID int64; Link, State, Error, Receipt string; CreatedAt, UpdatedAt int64 }`
  - `func (s *Store) CreateFetchJob(ctx, j *FetchJob) (id int64, created bool, err error)`
  - `func (s *Store) GetFetchJob(ctx, id int64) (*FetchJob, error)`
  - `func (s *Store) ClaimNextFetchJob(ctx, now int64) (*FetchJob, error)` —— 最早的 `queued` 置为 `fetching` 并返回；没有则 `ErrNotFound`
  - `func (s *Store) FinishFetchJob(ctx, id int64, state, errMsg string, now int64) error`
  - `func (s *Store) RequeueFetchingJobs(ctx, now int64) (int64, error)`
  - `func (s *Store) LinkFetchJobMessage(ctx, jobID, messageID int64) error`
  - `func (s *Store) FetchJobsForMessage(ctx, messageID int64) ([]int64, error)`
  - `func (s *Store) SetFetchJobReceipt(ctx, id int64, receipt string) error`
  - `type JobReceiptInfo struct { JobID, BotID, TgChatID, TgMessageID int64; State, Error, Receipt string; Main []MediaStatus }`
  - `func (s *Store) GetJobReceiptInfo(ctx, jobID int64) (*JobReceiptInfo, error)` —— `TgChatID` = sender_id，`TgMessageID` = 链接消息 ID；`Main` 为所有关联且未删除消息的主媒体状态
  - `func (s *Store) GetSender(ctx, tgUserID int64) (model.Sender, error)`、`func (s *Store) UpsertSender(ctx, snd model.Sender, now int64) error`

- [ ] **Step 1: 写迁移**

`internal/store/migrations/0003_userbot.sql`：

```sql
CREATE TABLE userbot_peers (
  channel_id  INTEGER PRIMARY KEY,
  access_hash INTEGER NOT NULL,
  username    TEXT    NOT NULL DEFAULT '',
  title       TEXT    NOT NULL DEFAULT '',
  updated_at  INTEGER NOT NULL
);

CREATE TABLE fetch_jobs (
  id                 INTEGER PRIMARY KEY,
  bot_id             INTEGER NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
  sender_id          INTEGER NOT NULL,
  link_tg_message_id INTEGER NOT NULL,
  link               TEXT    NOT NULL,
  state              TEXT    NOT NULL,
  error              TEXT    NOT NULL DEFAULT '',
  receipt            TEXT    NOT NULL DEFAULT 'none',
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL,
  UNIQUE (bot_id, sender_id, link_tg_message_id)
);
CREATE INDEX fetch_jobs_state ON fetch_jobs (state, id);

CREATE TABLE fetch_job_messages (
  job_id     INTEGER NOT NULL REFERENCES fetch_jobs(id) ON DELETE CASCADE,
  message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  PRIMARY KEY (job_id, message_id)
);
CREATE INDEX fetch_job_messages_message ON fetch_job_messages (message_id);
```

并把 `internal/store/store_test.go` 中 `TestMigrateIdempotent` 的 `v != 2` 改为 `v != 3`。

- [ ] **Step 2: 写失败测试**

`internal/store/userbot_test.go`：

```go
package store

import (
	"errors"
	"testing"

	"tgarchive/internal/model"
)

func TestUserbotRow(t *testing.T) {
	s := newStore(t)
	u, err := s.GetUserbot(ctx)
	if err != nil || u.Status != UserbotLoggedOut || u.SessionEnc != nil {
		t.Fatalf("fresh = %+v, %v", u, err)
	}
	if err := s.SaveUserbotSession(ctx, []byte("enc1"), 10); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserbotAccount(ctx, "+100", 99, "Me", 11); err != nil {
		t.Fatal(err)
	}
	u, _ = s.GetUserbot(ctx)
	if string(u.SessionEnc) != "enc1" || u.Phone != "+100" || u.TgUserID != 99 || u.Name != "Me" || u.Status != UserbotReady || u.UpdatedAt != 11 {
		t.Fatalf("after login = %+v", u)
	}
	if err := s.SetUserbotStatus(ctx, UserbotError, "revoked", 12); err != nil {
		t.Fatal(err)
	}
	u, _ = s.GetUserbot(ctx)
	if u.Status != UserbotError || u.LastError != "revoked" || u.TgUserID != 99 {
		t.Fatalf("after error = %+v", u)
	}
	if err := s.ClearUserbot(ctx, 13); err != nil {
		t.Fatal(err)
	}
	u, _ = s.GetUserbot(ctx)
	if u.SessionEnc != nil || u.Phone != "" || u.TgUserID != 0 || u.Status != UserbotLoggedOut || u.LastError != "" {
		t.Fatalf("after clear = %+v", u)
	}
}

func TestPeers(t *testing.T) {
	s := newStore(t)
	if _, err := s.GetPeer(ctx, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing peer err = %v", err)
	}
	if err := s.PutPeers(ctx, []Peer{{ChannelID: 5, AccessHash: 50, Username: "Chan", Title: "C"}, {ChannelID: 6, AccessHash: 60}}, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPeers(ctx, []Peer{{ChannelID: 5, AccessHash: 51, Title: "C2"}}, 2); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetPeer(ctx, 5)
	if err != nil || p.AccessHash != 51 || p.Title != "C2" || p.Username != "" {
		t.Fatalf("peer 5 = %+v, %v", p, err)
	}
}

func TestSenders(t *testing.T) {
	s := newStore(t)
	if _, err := s.GetSender(ctx, 42); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing sender err = %v", err)
	}
	snd := model.Sender{TgUserID: 42, FirstName: "A", LastName: "B", Username: "ab"}
	if err := s.UpsertSender(ctx, snd, 1); err != nil {
		t.Fatal(err)
	}
	snd.FirstName = "A2"
	if err := s.UpsertSender(ctx, snd, 2); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetSender(ctx, 42); err != nil || got != snd {
		t.Fatalf("sender = %+v, %v", got, err)
	}
}
```

`internal/store/fetchjobs_test.go`：

```go
package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"tgarchive/internal/model"
)

func newJob(bot int64, linkMsg int64) *FetchJob {
	return &FetchJob{BotID: bot, SenderID: 42, LinkTgMessageID: linkMsg, Link: "https://t.me/chan/1", State: JobQueued, CreatedAt: 1, UpdatedAt: 1}
}

func TestCreateFetchJobIsIdempotent(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	id1, created, err := s.CreateFetchJob(ctx, newJob(bot, 10))
	if err != nil || !created {
		t.Fatalf("first = %d %v %v", id1, created, err)
	}
	id2, created, err := s.CreateFetchJob(ctx, newJob(bot, 10))
	if err != nil || created || id2 != id1 {
		t.Fatalf("second = %d %v %v", id2, created, err)
	}
	j, err := s.GetFetchJob(ctx, id1)
	if err != nil || j.State != JobQueued || j.Receipt != ReceiptNone || j.Link != "https://t.me/chan/1" {
		t.Fatalf("job = %+v, %v", j, err)
	}
}

func TestClaimFinishAndRequeue(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	a, _, _ := s.CreateFetchJob(ctx, newJob(bot, 10))
	b, _, _ := s.CreateFetchJob(ctx, newJob(bot, 11))
	j, err := s.ClaimNextFetchJob(ctx, 5)
	if err != nil || j.ID != a || j.State != JobFetching {
		t.Fatalf("claim 1 = %+v, %v", j, err)
	}
	j, _ = s.ClaimNextFetchJob(ctx, 5)
	if j.ID != b {
		t.Fatalf("claim 2 = %+v", j)
	}
	if _, err := s.ClaimNextFetchJob(ctx, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim 3 err = %v", err)
	}
	if err := s.FinishFetchJob(ctx, a, JobFailed, "boom", 6); err != nil {
		t.Fatal(err)
	}
	ja, _ := s.GetFetchJob(ctx, a)
	if ja.State != JobFailed || ja.Error != "boom" || ja.UpdatedAt != 6 {
		t.Fatalf("finished = %+v", ja)
	}
}

func TestRequeueFetchingJobs(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	a, _, _ := s.CreateFetchJob(ctx, newJob(bot, 10))
	s.ClaimNextFetchJob(ctx, 5)
	n, err := s.RequeueFetchingJobs(ctx, 7)
	if err != nil || n != 1 {
		t.Fatalf("requeue = %d, %v", n, err)
	}
	j, err := s.ClaimNextFetchJob(ctx, 8)
	if err != nil || j.ID != a {
		t.Fatalf("reclaim = %+v, %v", j, err)
	}
}

func TestJobReceiptInfo(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	id, _, _ := s.CreateFetchJob(ctx, newJob(bot, 10))
	ingest := func(tgID int64, key string) int64 {
		m := &model.Message{TgMessageID: tgID, Source: model.SourceUserbotFetch, OriginChatID: -1000000000500, Date: 1, Kind: model.KindPhoto,
			RawFormat: model.RawMTProto, Raw: json.RawMessage(`{}`),
			Media: []model.Media{{DedupeKey: key, Kind: "photo", Role: model.RoleMain}, {DedupeKey: key + ":t", Kind: "photo", Role: model.RoleThumb}}}
		r, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: 100})
		if err != nil {
			t.Fatal(err)
		}
		return r.MessageID
	}
	m1, m2 := ingest(1, "mt:photo:1"), ingest(2, "mt:photo:2")
	for _, m := range []int64{m1, m2, m1} {
		if err := s.LinkFetchJobMessage(ctx, id, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.FinishFetchJob(ctx, id, JobFetched, "", 9); err != nil {
		t.Fatal(err)
	}
	info, err := s.GetJobReceiptInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	want := &JobReceiptInfo{JobID: id, BotID: bot, TgChatID: 42, TgMessageID: 10, State: JobFetched, Receipt: ReceiptNone,
		Main: []MediaStatus{{State: StatePending}, {State: StatePending}}}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("info = %+v", info)
	}
	if jobs, _ := s.FetchJobsForMessage(ctx, m2); !reflect.DeepEqual(jobs, []int64{id}) {
		t.Fatalf("jobs for message = %v", jobs)
	}
	if _, _, err := s.DeleteMessage(ctx, m2, 50); err != nil {
		t.Fatal(err)
	}
	info, _ = s.GetJobReceiptInfo(ctx, id)
	if len(info.Main) != 1 {
		t.Fatalf("deleted message still counted: %+v", info.Main)
	}
	if err := s.SetFetchJobReceipt(ctx, id, ReceiptDone); err != nil {
		t.Fatal(err)
	}
	if info, _ = s.GetJobReceiptInfo(ctx, id); info.Receipt != ReceiptDone {
		t.Fatalf("receipt = %q", info.Receipt)
	}
}

func TestFetchedMessageBumpsChatToNow(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	m := &model.Message{TgMessageID: 5, Source: model.SourceUserbotFetch, OriginChatID: -1000000000500, Date: 1000, Kind: model.KindText, Text: "old post",
		RawFormat: model.RawMTProto, Raw: json.RawMessage(`{}`)}
	if _, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: 5000}); err != nil {
		t.Fatal(err)
	}
	chats, err := s.ListChats(ctx, bot)
	if err != nil || len(chats) != 1 || chats[0].LastMessageAt != 5000 {
		t.Fatalf("chats = %+v, %v", chats, err)
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/store/`
Expected: FAIL（未定义的方法 / 常量）

- [ ] **Step 4: 实现**

`internal/store/userbot.go`：

```go
package store

import (
	"context"
	"database/sql"
	"errors"
)

const (
	UserbotLoggedOut = "logged_out"
	UserbotReady     = "ready"
	UserbotError     = "error"
)

type Userbot struct {
	Phone      string
	TgUserID   int64
	Name       string
	SessionEnc []byte
	Status     string
	LastError  string
	UpdatedAt  int64
}

func (s *Store) GetUserbot(ctx context.Context) (*Userbot, error) {
	var u Userbot
	err := s.db.QueryRowContext(ctx, "SELECT phone, tg_user_id, name, session_enc, status, last_error, updated_at FROM userbot WHERE id = 1").
		Scan(&u.Phone, &u.TgUserID, &u.Name, &u.SessionEnc, &u.Status, &u.LastError, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return &Userbot{Status: UserbotLoggedOut}, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) SaveUserbotSession(ctx context.Context, enc []byte, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO userbot (id, session_enc, updated_at) VALUES (1, ?, ?)
		ON CONFLICT (id) DO UPDATE SET session_enc = excluded.session_enc, updated_at = excluded.updated_at`, enc, now)
	return err
}

func (s *Store) SetUserbotAccount(ctx context.Context, phone string, tgUserID int64, name string, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO userbot (id, phone, tg_user_id, name, status, last_error, updated_at) VALUES (1, ?, ?, ?, 'ready', '', ?)
		ON CONFLICT (id) DO UPDATE SET phone = excluded.phone, tg_user_id = excluded.tg_user_id, name = excluded.name,
			status = 'ready', last_error = '', updated_at = excluded.updated_at`, phone, tgUserID, name, now)
	return err
}

func (s *Store) SetUserbotStatus(ctx context.Context, status, lastErr string, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO userbot (id, status, last_error, updated_at) VALUES (1, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET status = excluded.status, last_error = excluded.last_error, updated_at = excluded.updated_at`,
		status, lastErr, now)
	return err
}

func (s *Store) ClearUserbot(ctx context.Context, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO userbot (id, updated_at) VALUES (1, ?)
		ON CONFLICT (id) DO UPDATE SET phone = '', tg_user_id = 0, name = '', session_enc = NULL,
			status = 'logged_out', last_error = '', updated_at = excluded.updated_at`, now)
	return err
}

// Peer caches the access hash of a channel the userbot account can see. Private links (t.me/c/<id>)
// carry no access hash, so it must come from the account's own dialogs.
type Peer struct {
	ChannelID  int64
	AccessHash int64
	Username   string
	Title      string
}

func (s *Store) PutPeers(ctx context.Context, peers []Peer, now int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, p := range peers {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO userbot_peers (channel_id, access_hash, username, title, updated_at) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (channel_id) DO UPDATE SET access_hash = excluded.access_hash, username = excluded.username,
					title = excluded.title, updated_at = excluded.updated_at`,
				p.ChannelID, p.AccessHash, p.Username, p.Title, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) GetPeer(ctx context.Context, channelID int64) (*Peer, error) {
	p := Peer{ChannelID: channelID}
	err := s.db.QueryRowContext(ctx, "SELECT access_hash, username, title FROM userbot_peers WHERE channel_id = ?", channelID).
		Scan(&p.AccessHash, &p.Username, &p.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
```

`internal/store/fetchjobs.go`：

```go
package store

import (
	"context"
	"database/sql"
	"errors"
)

const (
	JobQueued      = "queued"
	JobFetching    = "fetching"
	JobFetched     = "fetched"
	JobFailed      = "failed"
	JobUnsupported = "unsupported" // link not fetchable; the message was archived as text instead
)

type FetchJob struct {
	ID              int64
	BotID           int64
	SenderID        int64
	LinkTgMessageID int64
	Link            string
	State           string
	Error           string
	Receipt         string
	CreatedAt       int64
	UpdatedAt       int64
}

const jobCols = "id, bot_id, sender_id, link_tg_message_id, link, state, error, receipt, created_at, updated_at"

func scanJob(r scanner) (*FetchJob, error) {
	var j FetchJob
	err := r.Scan(&j.ID, &j.BotID, &j.SenderID, &j.LinkTgMessageID, &j.Link, &j.State, &j.Error, &j.Receipt, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// CreateFetchJob inserts j unless a job for the same link message exists; created reports which.
func (s *Store) CreateFetchJob(ctx context.Context, j *FetchJob) (int64, bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO fetch_jobs (bot_id, sender_id, link_tg_message_id, link, state, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (bot_id, sender_id, link_tg_message_id) DO NOTHING RETURNING id`,
		j.BotID, j.SenderID, j.LinkTgMessageID, j.Link, j.State, j.Error, j.CreatedAt, j.UpdatedAt).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	err = s.db.QueryRowContext(ctx, "SELECT id FROM fetch_jobs WHERE bot_id = ? AND sender_id = ? AND link_tg_message_id = ?",
		j.BotID, j.SenderID, j.LinkTgMessageID).Scan(&id)
	return id, false, err
}

func (s *Store) GetFetchJob(ctx context.Context, id int64) (*FetchJob, error) {
	return scanJob(s.db.QueryRowContext(ctx, "SELECT "+jobCols+" FROM fetch_jobs WHERE id = ?", id))
}

func (s *Store) ClaimNextFetchJob(ctx context.Context, now int64) (*FetchJob, error) {
	var job *FetchJob
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		j, err := scanJob(tx.QueryRowContext(ctx, "SELECT "+jobCols+" FROM fetch_jobs WHERE state = 'queued' ORDER BY id LIMIT 1"))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE fetch_jobs SET state = 'fetching', updated_at = ? WHERE id = ?", now, j.ID); err != nil {
			return err
		}
		j.State, j.UpdatedAt = JobFetching, now
		job = j
		return nil
	})
	return job, err
}

func (s *Store) FinishFetchJob(ctx context.Context, id int64, state, errMsg string, now int64) error {
	return affected(s.db.ExecContext(ctx, "UPDATE fetch_jobs SET state = ?, error = ?, updated_at = ? WHERE id = ?", state, errMsg, now, id))
}

// RequeueFetchingJobs puts jobs interrupted by a restart back into the queue.
func (s *Store) RequeueFetchingJobs(ctx context.Context, now int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE fetch_jobs SET state = 'queued', updated_at = ? WHERE state = 'fetching'", now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) LinkFetchJobMessage(ctx context.Context, jobID, messageID int64) error {
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO fetch_job_messages (job_id, message_id) VALUES (?, ?)", jobID, messageID)
	return err
}

func (s *Store) FetchJobsForMessage(ctx context.Context, messageID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT job_id FROM fetch_job_messages WHERE message_id = ? ORDER BY job_id", messageID)
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

func (s *Store) SetFetchJobReceipt(ctx context.Context, id int64, receipt string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE fetch_jobs SET receipt = ? WHERE id = ?", receipt, id)
	return err
}

type JobReceiptInfo struct {
	JobID, BotID, TgChatID, TgMessageID int64
	State, Error, Receipt               string
	Main                                []MediaStatus
}

func (s *Store) GetJobReceiptInfo(ctx context.Context, jobID int64) (*JobReceiptInfo, error) {
	ri := JobReceiptInfo{JobID: jobID}
	err := s.db.QueryRowContext(ctx, "SELECT bot_id, sender_id, link_tg_message_id, state, error, receipt FROM fetch_jobs WHERE id = ?", jobID).
		Scan(&ri.BotID, &ri.TgChatID, &ri.TgMessageID, &ri.State, &ri.Error, &ri.Receipt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT md.state, md.error FROM fetch_job_messages fj
		JOIN messages m ON m.id = fj.message_id AND m.deleted_at = 0
		JOIN message_media mm ON mm.message_id = m.id AND mm.role = 'main'
		JOIN media md ON md.id = mm.media_id
		WHERE fj.job_id = ? ORDER BY m.id, mm.position`, jobID)
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
```

`internal/store/senders.go` 追加（`import` 增加 `database/sql`、`errors`、`tgarchive/internal/model`）：

```go
func (s *Store) GetSender(ctx context.Context, tgUserID int64) (model.Sender, error) {
	snd := model.Sender{TgUserID: tgUserID}
	err := s.db.QueryRowContext(ctx, "SELECT first_name, last_name, username FROM senders WHERE tg_user_id = ?", tgUserID).
		Scan(&snd.FirstName, &snd.LastName, &snd.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Sender{}, ErrNotFound
	}
	return snd, err
}

func (s *Store) UpsertSender(ctx context.Context, snd model.Sender, now int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO senders (tg_user_id, first_name, last_name, username, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (tg_user_id) DO UPDATE SET first_name = excluded.first_name, last_name = excluded.last_name,
			username = excluded.username, updated_at = excluded.updated_at`,
		snd.TgUserID, snd.FirstName, snd.LastName, snd.Username, now)
	return err
}
```

`internal/store/ingest.go`：在 `if res.Created {` 分支里，把刷新会话时间的语句改为按来源取值（代取消息的 `date` 是原帖时间，可能很旧，会话排序应以入库时间为准）：

```go
		if res.Created {
			lastAt := m.Date
			if m.Source == model.SourceUserbotFetch {
				lastAt = in.Now
			}
			if _, err := tx.ExecContext(ctx, "UPDATE chats SET last_message_at = ? WHERE id = ? AND last_message_at < ?", lastAt, res.ChatID, lastAt); err != nil {
				return err
			}
		} else {
```

- [ ] **Step 5: 运行确认通过**

Run: `go test -race ./internal/store/ && gofmt -l internal/store && go vet ./internal/store/`
Expected: PASS，gofmt/vet 无输出

- [ ] **Step 6: 提交**

```bash
git add internal/store/migrations/0003_userbot.sql internal/store/userbot.go internal/store/fetchjobs.go internal/store/senders.go internal/store/ingest.go internal/store/store_test.go internal/store/userbot_test.go internal/store/fetchjobs_test.go
git commit -m "feat(store): userbot state, peer cache and fetch job queue"
```

---
### Task 3: convert/mtproto —— gotd 消息 → 统一模型

**Files:**
- Create: `internal/convert/mtproto/convert.go`
- Test: `internal/convert/mtproto/convert_test.go`
- Modify: `go.mod` / `go.sum`（`go get github.com/gotd/td@v0.162.0`）

**Interfaces:**
- Consumes: `model.Message`、`model.Media`、`model.Entity`、`model.ForwardOrigin`、各 `model.Kind*` / `Role*` / `Source*` / `Raw*` 常量
- Produces:
  - `type Channel struct { ID, AccessHash int64; Title, Username string }`、`func ChannelFrom(c *tg.Channel) Channel`
  - `type Names struct { Users map[int64]*tg.User; Chats map[int64]*tg.Chat; Channels map[int64]*tg.Channel }`、`func NamesFrom(users []tg.UserClass, chats []tg.ChatClass) Names`、`func (n Names) Add(users []tg.UserClass, chats []tg.ChatClass)`
  - `func BotAPIChatID(channelID int64) int64`、`func MessageLink(ch Channel, msgID int) string`
  - `type MediaRef struct { ChannelID, ChannelHash int64; MsgID int; Photo bool; ID, FileHash int64; FileRef []byte; ThumbSize string }`（JSON 存入 `media.source_ref`）、`func (r MediaRef) Location() tg.InputFileLocationClass`
  - `func Convert(m *tg.Message, ch Channel, names Names) (*model.Message, error)`

**语义（与 `convert/botapi` 对齐）：**
- `Media.Kind`：`photo` / `video` / `animation` / `video_note` / `voice` / `audio` / `sticker` / `document`；缩略图一律 `Kind: "photo"`、`Mime: "image/jpeg"`、`Role: thumb`
- 照片：主图取面积最大的 `PhotoSize` / `PhotoSizeProgressive`（后者大小取 `Sizes` 最大值），有更小尺寸时另加缩略（面积最小者）；跳过 stripped / cached / path 尺寸
- 文档判定顺序：sticker → animated → round video → video → voice → audio → document；voice 带 `Waveform`、不加缩略；sticker 不加缩略；视频时长四舍五入为整秒
- `Extra` 键与 `convert/botapi` 相同（`spoiler`、`performer`、`title`、`emoji`、`latitude`、`longitude`、`address`、`phone_number`、`first_name`、`last_name`、`user_id`、`question`、`options`、`total_voter_count`、`is_anonymous`、`poll_type`、`multiple`、`value`），另加代取专有：`author`（超级群发言用户名）、`post_author`（频道署名）
- 实体类型映射到 Bot API 名称（`MessageEntityStrike` → `strikethrough`、`MentionName` → `text_mention`、`TextURL` → `text_link`、`Phone` → `phone_number`、折叠引用 → `expandable_blockquote`）；`BankCard` 与未知类型丢弃
- 无媒体或 `MessageMediaWebPage`：有文本 → `text`，否则 `other`；其余未识别媒体 → `other`

- [ ] **Step 1: 引入依赖**

Run: `go get github.com/gotd/td@v0.162.0`
Expected: `go.mod` 出现 `github.com/gotd/td v0.162.0`

- [ ] **Step 2: 写失败测试**

```go
package mtproto

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/gotd/td/tg"

	"tgarchive/internal/model"
)

var pub = Channel{ID: 500, AccessHash: 5005, Title: "Chan", Username: "chan"}

func ref(t *testing.T, m model.Media) MediaRef {
	t.Helper()
	var r MediaRef
	if err := json.Unmarshal([]byte(m.SourceRef), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestConvertTextAndEntities(t *testing.T) {
	m := &tg.Message{ID: 42, Date: 1700000000, Message: "hello world quote",
		ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 41},
		Entities: []tg.MessageEntityClass{
			&tg.MessageEntityBold{Offset: 0, Length: 5},
			&tg.MessageEntityTextURL{Offset: 6, Length: 5, URL: "https://x.y"},
			&tg.MessageEntityMentionName{Offset: 0, Length: 5, UserID: 9},
			&tg.MessageEntityCustomEmoji{Offset: 12, Length: 2, DocumentID: 123},
			&tg.MessageEntityBlockquote{Offset: 12, Length: 5, Collapsed: true},
			&tg.MessageEntityPre{Offset: 0, Length: 5, Language: "go"},
			&tg.MessageEntityBankCard{Offset: 0, Length: 1},
		},
	}
	m.SetEditDate(1700000100)
	m.SetGroupedID(777)
	got, err := Convert(m, pub, NamesFrom(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != model.SourceUserbotFetch || got.RawFormat != model.RawMTProto || len(got.Raw) == 0 ||
		got.TgMessageID != 42 || got.Date != 1700000000 || got.EditDate != 1700000100 || got.MediaGroupID != "777" ||
		got.ReplyToTgMessageID != 41 || got.Kind != model.KindText || got.Text != "hello world quote" ||
		got.OriginChatID != -1000000000500 || got.OriginChatTitle != "Chan" || got.OriginLink != "https://t.me/chan/42" {
		t.Fatalf("message = %+v", got)
	}
	want := []model.Entity{
		{Type: "bold", Offset: 0, Length: 5},
		{Type: "text_link", Offset: 6, Length: 5, URL: "https://x.y"},
		{Type: "text_mention", Offset: 0, Length: 5, UserID: 9},
		{Type: "custom_emoji", Offset: 12, Length: 2, CustomEmojiID: "123"},
		{Type: "expandable_blockquote", Offset: 12, Length: 5},
		{Type: "pre", Offset: 0, Length: 5, Language: "go"},
	}
	if !reflect.DeepEqual(got.Entities, want) {
		t.Fatalf("entities = %+v", got.Entities)
	}
	priv := Channel{ID: 500, AccessHash: 1, Title: "P"}
	if got, _ := Convert(&tg.Message{ID: 7, Message: "x"}, priv, Names{}); got.OriginLink != "https://t.me/c/500/7" {
		t.Fatalf("private link = %q", got.OriginLink)
	}
}

func TestConvertPhoto(t *testing.T) {
	m := &tg.Message{ID: 42, Media: &tg.MessageMediaPhoto{Spoiler: true, Photo: &tg.Photo{ID: 9, AccessHash: 99, FileReference: []byte{1, 2},
		Sizes: []tg.PhotoSizeClass{
			&tg.PhotoStrippedSize{Type: "i", Bytes: []byte{1}},
			&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 100},
			&tg.PhotoSizeProgressive{Type: "y", W: 1280, H: 960, Sizes: []int{10, 200, 900}},
		}}}}
	got, err := Convert(m, pub, Names{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != model.KindPhoto || len(got.Media) != 2 || string(got.Extra) != `{"spoiler":true}` {
		t.Fatalf("photo = %+v extra=%s", got, got.Extra)
	}
	main, thumb := got.Media[0], got.Media[1]
	if main.DedupeKey != "mt:photo:9" || main.Kind != "photo" || main.Mime != "image/jpeg" || main.Width != 1280 || main.Height != 960 ||
		main.Size != 900 || main.Role != model.RoleMain {
		t.Fatalf("main = %+v", main)
	}
	if thumb.DedupeKey != "mt:photo:9:m" || thumb.Width != 320 || thumb.Role != model.RoleThumb {
		t.Fatalf("thumb = %+v", thumb)
	}
	r := ref(t, main)
	if r != (MediaRef{ChannelID: 500, ChannelHash: 5005, MsgID: 42, Photo: true, ID: 9, FileHash: 99, FileRef: r.FileRef, ThumbSize: "y"}) ||
		!reflect.DeepEqual(r.FileRef, []byte{1, 2}) {
		t.Fatalf("ref = %+v", r)
	}
	loc, ok := r.Location().(*tg.InputPhotoFileLocation)
	if !ok || loc.ID != 9 || loc.AccessHash != 99 || loc.ThumbSize != "y" || !reflect.DeepEqual(loc.FileReference, []byte{1, 2}) {
		t.Fatalf("location = %#v", r.Location())
	}
	if ref(t, thumb).ThumbSize != "m" {
		t.Fatalf("thumb ref = %+v", ref(t, thumb))
	}
}

func doc(id int64, mime string, attrs ...tg.DocumentAttributeClass) *tg.Message {
	return &tg.Message{ID: 42, Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: id, AccessHash: 11, FileReference: []byte{3},
		MimeType: mime, Size: 4096, Attributes: attrs,
		Thumbs: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "m", W: 90, H: 60, Size: 50}}}}}
}

func TestConvertDocuments(t *testing.T) {
	cases := []struct {
		name  string
		msg   *tg.Message
		kind  model.Kind
		mkind string
		check func(t *testing.T, got *model.Message)
	}{
		{"video", doc(5, "video/mp4", &tg.DocumentAttributeVideo{W: 1920, H: 1080, Duration: 12.6}, &tg.DocumentAttributeFilename{FileName: "a.mp4"}),
			model.KindVideo, "video", func(t *testing.T, got *model.Message) {
				m := got.Media[0]
				if m.Width != 1920 || m.Height != 1080 || m.Duration != 13 || m.FileName != "a.mp4" || m.Size != 4096 || m.DedupeKey != "mt:doc:5" {
					t.Fatalf("video media = %+v", m)
				}
				if len(got.Media) != 2 || got.Media[1].DedupeKey != "mt:doc:5:thumb" || ref(t, got.Media[1]).ThumbSize != "m" {
					t.Fatalf("video thumb = %+v", got.Media)
				}
				loc, ok := ref(t, m).Location().(*tg.InputDocumentFileLocation)
				if !ok || loc.ID != 5 || loc.AccessHash != 11 || loc.ThumbSize != "" {
					t.Fatalf("doc location = %#v", ref(t, m).Location())
				}
			}},
		{"round", doc(6, "video/mp4", &tg.DocumentAttributeVideo{RoundMessage: true, W: 240, H: 240, Duration: 3}), model.KindVideoNote, "video_note", nil},
		{"gif", doc(7, "video/mp4", &tg.DocumentAttributeAnimated{}, &tg.DocumentAttributeVideo{W: 320, H: 200, Duration: 2}), model.KindAnimation, "animation", nil},
		{"voice", doc(8, "audio/ogg", &tg.DocumentAttributeAudio{Voice: true, Duration: 4, Waveform: []byte{1, 2, 3}}), model.KindVoice, "voice",
			func(t *testing.T, got *model.Message) {
				if len(got.Media) != 1 || got.Media[0].Duration != 4 || !reflect.DeepEqual(got.Media[0].Waveform, []byte{1, 2, 3}) {
					t.Fatalf("voice = %+v", got.Media)
				}
			}},
		{"audio", doc(9, "audio/mpeg", &tg.DocumentAttributeAudio{Duration: 200, Title: "Song", Performer: "Band"}), model.KindAudio, "audio",
			func(t *testing.T, got *model.Message) {
				if string(got.Extra) != `{"performer":"Band","title":"Song"}` {
					t.Fatalf("audio extra = %s", got.Extra)
				}
			}},
		{"sticker", doc(10, "image/webp", &tg.DocumentAttributeSticker{Alt: "😀", Stickerset: &tg.InputStickerSetEmpty{}}), model.KindSticker, "sticker",
			func(t *testing.T, got *model.Message) {
				if len(got.Media) != 1 || string(got.Extra) != `{"emoji":"😀"}` {
					t.Fatalf("sticker = %+v %s", got.Media, got.Extra)
				}
			}},
		{"file", doc(11, "application/pdf", &tg.DocumentAttributeFilename{FileName: "x.pdf"}), model.KindDocument, "document",
			func(t *testing.T, got *model.Message) {
				if got.Media[0].FileName != "x.pdf" || got.Media[0].Mime != "application/pdf" {
					t.Fatalf("file = %+v", got.Media[0])
				}
			}},
		{"nomime", doc(12, ""), model.KindDocument, "document",
			func(t *testing.T, got *model.Message) {
				if got.Media[0].Mime != "application/octet-stream" {
					t.Fatalf("mime = %q", got.Media[0].Mime)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Convert(c.msg, pub, Names{})
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != c.kind || got.Media[0].Kind != c.mkind || got.Media[0].Role != model.RoleMain {
				t.Fatalf("kind = %s / %s", got.Kind, got.Media[0].Kind)
			}
			if c.check != nil {
				c.check(t, got)
			}
		})
	}
}

func TestConvertForwardAndAuthor(t *testing.T) {
	names := NamesFrom(
		[]tg.UserClass{&tg.User{ID: 9, FirstName: "Ann", LastName: "Lee", Username: "ann"}},
		[]tg.ChatClass{&tg.Channel{ID: 600, Title: "Source", Username: "src"}})
	m := &tg.Message{ID: 1, Message: "x", FromID: &tg.PeerUser{UserID: 9}}
	m.SetFwdFrom(tg.MessageFwdHeader{FromID: &tg.PeerChannel{ChannelID: 600}, ChannelPost: 7, Date: 100, PostAuthor: "ed"})
	got, _ := Convert(m, pub, names)
	want := &model.ForwardOrigin{Type: "channel", Name: "Source", Username: "src", ChatID: -1000000000600, MessageID: 7, Date: 100, Signature: "ed"}
	if !reflect.DeepEqual(got.ForwardOrigin, want) || string(got.Extra) != `{"author":"Ann Lee"}` {
		t.Fatalf("fwd = %+v extra=%s", got.ForwardOrigin, got.Extra)
	}
	h := &tg.Message{ID: 2, Message: "x"}
	h.SetFwdFrom(tg.MessageFwdHeader{FromName: "Hidden", Date: 5})
	got, _ = Convert(h, pub, names)
	if got.ForwardOrigin == nil || got.ForwardOrigin.Type != "hidden_user" || got.ForwardOrigin.Name != "Hidden" {
		t.Fatalf("hidden = %+v", got.ForwardOrigin)
	}
	u := &tg.Message{ID: 3, Message: "x"}
	u.SetFwdFrom(tg.MessageFwdHeader{FromID: &tg.PeerUser{UserID: 9}, Date: 5})
	got, _ = Convert(u, pub, names)
	if f := got.ForwardOrigin; f.Type != "user" || f.Name != "Ann Lee" || f.Username != "ann" || f.UserID != 9 {
		t.Fatalf("user fwd = %+v", f)
	}
}

func TestConvertMisc(t *testing.T) {
	cases := []struct {
		media tg.MessageMediaClass
		text  string
		kind  model.Kind
		extra string
	}{
		{&tg.MessageMediaGeo{Geo: &tg.GeoPoint{Lat: 1.5, Long: 2.5}}, "", model.KindLocation, `{"latitude":1.5,"longitude":2.5}`},
		{&tg.MessageMediaVenue{Geo: &tg.GeoPoint{Lat: 1, Long: 2}, Title: "Cafe", Address: "St"}, "", model.KindVenue,
			`{"address":"St","latitude":1,"longitude":2,"title":"Cafe"}`},
		{&tg.MessageMediaContact{PhoneNumber: "+1", FirstName: "A", UserID: 3}, "", model.KindContact, `{"first_name":"A","phone_number":"+1","user_id":3}`},
		{&tg.MessageMediaDice{Value: 4, Emoticon: "🎲"}, "", model.KindDice, `{"emoji":"🎲","value":4}`},
		{&tg.MessageMediaWebPage{Webpage: &tg.WebPageEmpty{}}, "see link", model.KindText, ``},
		{&tg.MessageMediaUnsupported{}, "", model.KindOther, ``},
		{nil, "", model.KindOther, ``},
		{&tg.MessageMediaPoll{
			Poll: tg.Poll{Question: tg.TextWithEntities{Text: "Q?"}, MultipleChoice: true, Answers: []tg.PollAnswerClass{
				&tg.PollAnswer{Text: tg.TextWithEntities{Text: "a"}, Option: []byte{0}},
				&tg.PollAnswer{Text: tg.TextWithEntities{Text: "b"}, Option: []byte{1}},
			}},
			Results: tg.PollResults{TotalVoters: 3, Results: []tg.PollAnswerVoters{{Option: []byte{1}, Voters: 3}}},
		}, "", model.KindPoll,
			`{"is_anonymous":true,"multiple":true,"options":[{"text":"a","voter_count":0},{"text":"b","voter_count":3}],"poll_type":"regular","question":"Q?","total_voter_count":3}`},
	}
	for i, c := range cases {
		got, err := Convert(&tg.Message{ID: 1, Message: c.text, Media: c.media}, pub, Names{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != c.kind || string(got.Extra) != c.extra || len(got.Media) != 0 {
			t.Fatalf("case %d: kind=%s extra=%s media=%v", i, got.Kind, got.Extra, got.Media)
		}
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/convert/mtproto/`
Expected: FAIL（`undefined: Convert` 等）

- [ ] **Step 4: 实现**

```go
// Package mtproto converts channel / supergroup messages fetched over MTProto (gotd) into model.Message.
package mtproto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"

	"tgarchive/internal/model"
)

// Channel identifies the chat a fetched message came from.
type Channel struct {
	ID         int64 // raw MTProto channel id, as in t.me/c/<id>/...
	AccessHash int64
	Title      string
	Username   string
}

func ChannelFrom(c *tg.Channel) Channel {
	return Channel{ID: c.ID, AccessHash: c.AccessHash, Title: c.Title, Username: c.Username}
}

// Names resolves the peers that forward headers and message authors refer to.
type Names struct {
	Users    map[int64]*tg.User
	Chats    map[int64]*tg.Chat
	Channels map[int64]*tg.Channel
}

func NamesFrom(users []tg.UserClass, chats []tg.ChatClass) Names {
	n := Names{Users: map[int64]*tg.User{}, Chats: map[int64]*tg.Chat{}, Channels: map[int64]*tg.Channel{}}
	n.Add(users, chats)
	return n
}

func (n Names) Add(users []tg.UserClass, chats []tg.ChatClass) {
	for _, u := range users {
		if v, ok := u.(*tg.User); ok {
			n.Users[v.ID] = v
		}
	}
	for _, c := range chats {
		switch v := c.(type) {
		case *tg.Chat:
			n.Chats[v.ID] = v
		case *tg.Channel:
			n.Channels[v.ID] = v
		}
	}
}

// BotAPIChatID returns the Bot API form (-100…) of an MTProto channel id.
func BotAPIChatID(channelID int64) int64 { return -1000000000000 - channelID }

func MessageLink(ch Channel, msgID int) string {
	if ch.Username != "" {
		return fmt.Sprintf("https://t.me/%s/%d", ch.Username, msgID)
	}
	return fmt.Sprintf("https://t.me/c/%d/%d", ch.ID, msgID)
}

// MediaRef is stored as JSON in media.source_ref for "mt:" media. It carries both the file
// location and the message it came from, so an expired file reference can be refreshed.
type MediaRef struct {
	ChannelID   int64  `json:"channel_id"`
	ChannelHash int64  `json:"channel_hash"`
	MsgID       int    `json:"msg_id"`
	Photo       bool   `json:"photo"`
	ID          int64  `json:"id"`
	FileHash    int64  `json:"file_hash"`
	FileRef     []byte `json:"file_ref"`
	ThumbSize   string `json:"thumb_size,omitempty"`
}

func (r MediaRef) Location() tg.InputFileLocationClass {
	if r.Photo {
		return &tg.InputPhotoFileLocation{ID: r.ID, AccessHash: r.FileHash, FileReference: r.FileRef, ThumbSize: r.ThumbSize}
	}
	return &tg.InputDocumentFileLocation{ID: r.ID, AccessHash: r.FileHash, FileReference: r.FileRef, ThumbSize: r.ThumbSize}
}

func Convert(m *tg.Message, ch Channel, names Names) (*model.Message, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	msg := &model.Message{
		TgMessageID:     int64(m.ID),
		Source:          model.SourceUserbotFetch,
		Date:            int64(m.Date),
		Text:            m.Message,
		Entities:        convertEntities(m.Entities),
		OriginChatID:    BotAPIChatID(ch.ID),
		OriginChatTitle: ch.Title,
		OriginLink:      MessageLink(ch, m.ID),
		RawFormat:       model.RawMTProto,
		Raw:             raw,
	}
	if d, ok := m.GetEditDate(); ok {
		msg.EditDate = int64(d)
	}
	if g, ok := m.GetGroupedID(); ok {
		msg.MediaGroupID = strconv.FormatInt(g, 10)
	}
	if h, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
		msg.ReplyToTgMessageID = int64(h.ReplyToMsgID)
	}
	if f, ok := m.GetFwdFrom(); ok {
		msg.ForwardOrigin = convertFwd(f, names)
	}
	extra := map[string]any{}
	if u, ok := m.FromID.(*tg.PeerUser); ok {
		if v := names.Users[u.UserID]; v != nil {
			setIf(extra, "author", strings.TrimSpace(v.FirstName+" "+v.LastName))
		}
	}
	if a, ok := m.GetPostAuthor(); ok {
		setIf(extra, "post_author", a)
	}
	if err := fill(msg, m, ch, extra); err != nil {
		return nil, err
	}
	if len(extra) > 0 {
		b, err := json.Marshal(extra)
		if err != nil {
			return nil, err
		}
		msg.Extra = b
	}
	return msg, nil
}

func fill(msg *model.Message, m *tg.Message, ch Channel, extra map[string]any) error {
	base := MediaRef{ChannelID: ch.ID, ChannelHash: ch.AccessHash, MsgID: m.ID}
	switch md := m.Media.(type) {
	case *tg.MessageMediaPhoto:
		p, ok := md.Photo.(*tg.Photo)
		if !ok {
			msg.Kind = model.KindOther
			return nil
		}
		big, small, has := pickSizes(p.Sizes)
		if !has {
			msg.Kind = model.KindOther
			return nil
		}
		if md.Spoiler {
			extra["spoiler"] = true
		}
		msg.Kind = model.KindPhoto
		r := base
		r.Photo, r.ID, r.FileHash, r.FileRef, r.ThumbSize = true, p.ID, p.AccessHash, p.FileReference, big.Type
		main, err := media(r, fmt.Sprintf("mt:photo:%d", p.ID), "photo", "image/jpeg", big, model.RoleMain)
		if err != nil {
			return err
		}
		msg.Media = append(msg.Media, main)
		if small.Type != big.Type {
			r.ThumbSize = small.Type
			th, err := media(r, fmt.Sprintf("mt:photo:%d:%s", p.ID, small.Type), "photo", "image/jpeg", small, model.RoleThumb)
			if err != nil {
				return err
			}
			msg.Media = append(msg.Media, th)
		}
	case *tg.MessageMediaDocument:
		d, ok := md.Document.(*tg.Document)
		if !ok {
			msg.Kind = model.KindOther
			return nil
		}
		if md.Spoiler {
			extra["spoiler"] = true
		}
		return document(msg, d, base, extra)
	case *tg.MessageMediaGeo:
		return geo(msg, md.Geo, extra)
	case *tg.MessageMediaGeoLive:
		return geo(msg, md.Geo, extra)
	case *tg.MessageMediaVenue:
		if err := geo(msg, md.Geo, extra); err != nil {
			return err
		}
		msg.Kind = model.KindVenue
		setIf(extra, "title", md.Title)
		setIf(extra, "address", md.Address)
	case *tg.MessageMediaContact:
		msg.Kind = model.KindContact
		setIf(extra, "phone_number", md.PhoneNumber)
		setIf(extra, "first_name", md.FirstName)
		setIf(extra, "last_name", md.LastName)
		if md.UserID != 0 {
			extra["user_id"] = md.UserID
		}
	case *tg.MessageMediaPoll:
		p := md.Poll
		msg.Kind = model.KindPoll
		opts := make([]map[string]any, 0, len(p.Answers))
		for _, a := range p.Answers {
			pa, ok := a.(*tg.PollAnswer)
			if !ok {
				continue
			}
			voters := 0
			for _, r := range md.Results.Results {
				if bytes.Equal(r.Option, pa.Option) {
					voters = r.Voters
				}
			}
			opts = append(opts, map[string]any{"text": pa.Text.Text, "voter_count": voters})
		}
		typ := "regular"
		if p.Quiz {
			typ = "quiz"
		}
		extra["question"], extra["options"], extra["total_voter_count"] = p.Question.Text, opts, md.Results.TotalVoters
		extra["is_anonymous"], extra["poll_type"], extra["multiple"] = !p.PublicVoters, typ, p.MultipleChoice
	case *tg.MessageMediaDice:
		msg.Kind = model.KindDice
		extra["emoji"], extra["value"] = md.Emoticon, md.Value
	case nil, *tg.MessageMediaEmpty, *tg.MessageMediaWebPage:
		if msg.Text != "" {
			msg.Kind = model.KindText
		} else {
			msg.Kind = model.KindOther
		}
	default:
		msg.Kind = model.KindOther
	}
	return nil
}

func document(msg *model.Message, d *tg.Document, base MediaRef, extra map[string]any) error {
	var (
		video    *tg.DocumentAttributeVideo
		audio    *tg.DocumentAttributeAudio
		sticker  *tg.DocumentAttributeSticker
		animated bool
		name     string
		sz       size
	)
	for _, a := range d.Attributes {
		switch v := a.(type) {
		case *tg.DocumentAttributeVideo:
			video = v
		case *tg.DocumentAttributeAudio:
			audio = v
		case *tg.DocumentAttributeSticker:
			sticker = v
		case *tg.DocumentAttributeAnimated:
			animated = true
		case *tg.DocumentAttributeFilename:
			name = v.FileName
		case *tg.DocumentAttributeImageSize:
			sz.W, sz.H = v.W, v.H
		}
	}
	kind, mkind, mime, dur := model.KindDocument, "document", d.MimeType, 0
	if video != nil {
		sz.W, sz.H, dur = video.W, video.H, int(math.Round(video.Duration))
	}
	switch {
	case sticker != nil:
		kind, mkind = model.KindSticker, "sticker"
		setIf(extra, "emoji", sticker.Alt)
	case animated:
		kind, mkind, mime = model.KindAnimation, "animation", or(mime, "video/mp4")
	case video != nil && video.RoundMessage:
		kind, mkind, mime = model.KindVideoNote, "video_note", or(mime, "video/mp4")
	case video != nil:
		kind, mkind, mime = model.KindVideo, "video", or(mime, "video/mp4")
	case audio != nil && audio.Voice:
		kind, mkind, mime, dur = model.KindVoice, "voice", or(mime, "audio/ogg"), audio.Duration
	case audio != nil:
		kind, mkind, mime, dur = model.KindAudio, "audio", or(mime, "audio/mpeg"), audio.Duration
		setIf(extra, "performer", audio.Performer)
		setIf(extra, "title", audio.Title)
	}
	msg.Kind = kind
	r := base
	r.ID, r.FileHash, r.FileRef = d.ID, d.AccessHash, d.FileReference
	sz.Bytes = d.Size
	main, err := media(r, fmt.Sprintf("mt:doc:%d", d.ID), mkind, or(mime, "application/octet-stream"), sz, model.RoleMain)
	if err != nil {
		return err
	}
	main.FileName, main.Duration = name, dur
	if kind == model.KindVoice {
		main.Waveform = audio.Waveform
	}
	msg.Media = append(msg.Media, main)
	if kind == model.KindSticker || kind == model.KindVoice {
		return nil
	}
	if big, _, ok := pickSizes(d.Thumbs); ok {
		r.ThumbSize = big.Type
		th, err := media(r, fmt.Sprintf("mt:doc:%d:thumb", d.ID), "photo", "image/jpeg", big, model.RoleThumb)
		if err != nil {
			return err
		}
		msg.Media = append(msg.Media, th)
	}
	return nil
}

func geo(msg *model.Message, g tg.GeoPointClass, extra map[string]any) error {
	p, ok := g.(*tg.GeoPoint)
	if !ok {
		msg.Kind = model.KindOther
		return nil
	}
	msg.Kind = model.KindLocation
	extra["latitude"], extra["longitude"] = p.Lat, p.Long
	return nil
}

type size struct {
	Type  string
	W, H  int
	Bytes int64
}

// pickSizes returns the largest and smallest downloadable photo sizes.
func pickSizes(in []tg.PhotoSizeClass) (big, small size, ok bool) {
	for _, s := range in {
		var c size
		switch v := s.(type) {
		case *tg.PhotoSize:
			c = size{Type: v.Type, W: v.W, H: v.H, Bytes: int64(v.Size)}
		case *tg.PhotoSizeProgressive:
			c = size{Type: v.Type, W: v.W, H: v.H}
			for _, b := range v.Sizes {
				c.Bytes = max(c.Bytes, int64(b))
			}
		default:
			continue
		}
		if !ok || c.W*c.H > big.W*big.H {
			big = c
		}
		if !ok || c.W*c.H < small.W*small.H {
			small = c
		}
		ok = true
	}
	return big, small, ok
}

func media(r MediaRef, key, kind, mime string, sz size, role string) (model.Media, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return model.Media{}, err
	}
	return model.Media{DedupeKey: key, SourceRef: string(b), Kind: kind, Mime: mime, Size: sz.Bytes, Width: sz.W, Height: sz.H, Role: role}, nil
}

func convertEntities(in []tg.MessageEntityClass) []model.Entity {
	out := make([]model.Entity, 0, len(in))
	for _, e := range in {
		me := model.Entity{Offset: e.GetOffset(), Length: e.GetLength()}
		switch v := e.(type) {
		case *tg.MessageEntityMention:
			me.Type = "mention"
		case *tg.MessageEntityHashtag:
			me.Type = "hashtag"
		case *tg.MessageEntityCashtag:
			me.Type = "cashtag"
		case *tg.MessageEntityBotCommand:
			me.Type = "bot_command"
		case *tg.MessageEntityURL:
			me.Type = "url"
		case *tg.MessageEntityEmail:
			me.Type = "email"
		case *tg.MessageEntityPhone:
			me.Type = "phone_number"
		case *tg.MessageEntityBold:
			me.Type = "bold"
		case *tg.MessageEntityItalic:
			me.Type = "italic"
		case *tg.MessageEntityUnderline:
			me.Type = "underline"
		case *tg.MessageEntityStrike:
			me.Type = "strikethrough"
		case *tg.MessageEntitySpoiler:
			me.Type = "spoiler"
		case *tg.MessageEntityCode:
			me.Type = "code"
		case *tg.MessageEntityPre:
			me.Type, me.Language = "pre", v.Language
		case *tg.MessageEntityTextURL:
			me.Type, me.URL = "text_link", v.URL
		case *tg.MessageEntityMentionName:
			me.Type, me.UserID = "text_mention", v.UserID
		case *tg.MessageEntityCustomEmoji:
			me.Type, me.CustomEmojiID = "custom_emoji", strconv.FormatInt(v.DocumentID, 10)
		case *tg.MessageEntityBlockquote:
			me.Type = "blockquote"
			if v.Collapsed {
				me.Type = "expandable_blockquote"
			}
		default:
			continue
		}
		out = append(out, me)
	}
	return out
}

func convertFwd(f tg.MessageFwdHeader, names Names) *model.ForwardOrigin {
	o := &model.ForwardOrigin{Date: int64(f.Date), Signature: f.PostAuthor}
	switch p := f.FromID.(type) {
	case *tg.PeerUser:
		o.Type, o.UserID = "user", p.UserID
		if u := names.Users[p.UserID]; u != nil {
			o.Name, o.Username = strings.TrimSpace(u.FirstName+" "+u.LastName), u.Username
		}
	case *tg.PeerChannel:
		o.Type, o.ChatID, o.MessageID = "channel", BotAPIChatID(p.ChannelID), int64(f.ChannelPost)
		if f.ChannelPost == 0 {
			o.Type = "chat" // anonymous supergroup admin
		}
		if c := names.Channels[p.ChannelID]; c != nil {
			o.Name, o.Username = c.Title, c.Username
		}
	case *tg.PeerChat:
		o.Type, o.ChatID = "chat", -p.ChatID
		if c := names.Chats[p.ChatID]; c != nil {
			o.Name = c.Title
		}
	default:
		o.Type = "hidden_user"
	}
	if o.Name == "" {
		o.Name = f.FromName
	}
	return o
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

> 若 gotd v0.162.0 的某个生成器方法名与上面不同（例如 `GetPostAuthor`、`SetFwdFrom`、`MessageEntityClass.GetOffset`），以 `go doc github.com/gotd/td/tg.<类型>` 查到的实际名称为准，语义不变。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/convert/mtproto/ && gofmt -l internal/convert && go vet ./internal/convert/...`
Expected: PASS，无输出

- [ ] **Step 6: 提交**

```bash
git add go.mod go.sum internal/convert/mtproto/convert.go internal/convert/mtproto/convert_test.go
git commit -m "feat(convert): MTProto channel messages to model"
```

---

### Task 4: receipt —— 代取任务回执

**Files:**
- Modify: `internal/receipt/receipt.go`
- Test: `internal/receipt/job_test.go`

**Interfaces:**
- Consumes: Task 2 的 `GetJobReceiptInfo`、`SetFetchJobReceipt`、`FetchJobsForMessage`、`Job*` 常量
- Produces:
  - 导出常量 `TextFetchFailedPrefix = "⚠️ 代取失败："`
  - `func (e *Engine) EvaluateJob(ctx context.Context, jobID int64)`
  - `MediaSettled` 行为扩展：对每条受影响消息，同时评估其关联的全部 job
  - 既有 `Evaluate` 行为不变（仍只处理 `bot_update` 消息）

**job 回执规则**（作用在 `(bot_id, sender_id, link_tg_message_id)` 上）：

| job 状态 | 动作 |
|---|---|
| `unsupported` | 不处理（Task 6 在入队时直接回复） |
| `queued` / `fetching` | `receipt = none` 时设 👀 → `seen` |
| `failed` | `receipt != failed` 时：若 `none` 先设 👀（只记日志），再回复 `⚠️ 代取失败：<error 截断 200 字>`，回复成功 → `failed` |
| `fetched` | 与消息回执完全相同：有 pending → 确保 👀；有 failed → 回复 `⚠️ 存档失败：…`；否则 👌（有超限再回复超限文案）→ `done` |

- [ ] **Step 1: 写失败测试**

`internal/receipt/job_test.go`：

```go
package receipt

import (
	"encoding/json"
	"reflect"
	"testing"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

func (v *env) job(t *testing.T, linkMsg int64) int64 {
	t.Helper()
	id, _, err := v.st.CreateFetchJob(ctx, &store.FetchJob{BotID: v.bot, SenderID: 42, LinkTgMessageID: linkMsg, Link: "l", State: store.JobQueued, CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (v *env) fetched(t *testing.T, job, tgID int64, keys ...string) (int64, []int64) {
	t.Helper()
	m := &model.Message{TgMessageID: tgID, Source: model.SourceUserbotFetch, OriginChatID: -1000000000500, Date: 1, Kind: model.KindText, Text: "t",
		RawFormat: model.RawMTProto, Raw: json.RawMessage(`{}`)}
	for _, k := range keys {
		m.Kind = model.KindPhoto
		m.Media = append(m.Media, model.Media{DedupeKey: k, Kind: "photo", Role: model.RoleMain})
	}
	res, err := v.st.Ingest(ctx, store.IngestInput{BotID: v.bot, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.st.LinkFetchJobMessage(ctx, job, res.MessageID); err != nil {
		t.Fatal(err)
	}
	var mids []int64
	due, _ := v.st.DueMedia(ctx, 0, 100)
	for _, d := range due {
		mids = append(mids, d.ID)
	}
	return res.MessageID, mids
}

func TestJobSeenThenDone(t *testing.T) {
	v := newEnv(t)
	job := v.job(t, 10)
	v.e.EvaluateJob(ctx, job)
	v.e.EvaluateJob(ctx, job)
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, []string{"react 42 10 👀"}) {
		t.Fatalf("queued = %v", got)
	}
	_, mids := v.fetched(t, job, 500, "mt:photo:1")
	v.st.FinishFetchJob(ctx, job, store.JobFetched, "", 2)
	v.e.EvaluateJob(ctx, job)
	if got := v.tr.(*rec).take(); len(got) != 0 {
		t.Fatalf("fetched with pending media = %v", got)
	}
	v.st.MarkMediaDone(ctx, mids[0], "p", 1)
	v.e.MediaSettled(ctx, mids[0])
	v.e.MediaSettled(ctx, mids[0])
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, []string{"react 42 10 👌"}) {
		t.Fatalf("done = %v", got)
	}
}

func TestJobTextOnlyGoesDone(t *testing.T) {
	v := newEnv(t)
	job := v.job(t, 10)
	v.fetched(t, job, 500)
	v.st.FinishFetchJob(ctx, job, store.JobFetched, "", 2)
	v.e.EvaluateJob(ctx, job)
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, []string{"react 42 10 👌"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestJobFailedRepliesOnce(t *testing.T) {
	v := newEnv(t)
	job := v.job(t, 10)
	v.st.FinishFetchJob(ctx, job, store.JobFailed, "私有群/频道，代取账号未加入", 2)
	v.e.EvaluateJob(ctx, job)
	v.e.EvaluateJob(ctx, job)
	want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 代取失败：私有群/频道，代取账号未加入"}
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v", got)
	}
}

func TestJobMediaFailureUsesArchiveText(t *testing.T) {
	v := newEnv(t)
	job := v.job(t, 10)
	v.e.EvaluateJob(ctx, job)
	_, mids := v.fetched(t, job, 500, "mt:photo:1")
	v.st.FinishFetchJob(ctx, job, store.JobFetched, "", 2)
	v.st.MarkMediaFailed(ctx, mids[0], 4, "network down")
	v.e.MediaSettled(ctx, mids[0])
	want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 存档失败：network down"}
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v", got)
	}
}

func TestUnsupportedJobIgnored(t *testing.T) {
	v := newEnv(t)
	id, _, _ := v.st.CreateFetchJob(ctx, &store.FetchJob{BotID: v.bot, SenderID: 42, LinkTgMessageID: 11, Link: "l", State: store.JobUnsupported, CreatedAt: 1, UpdatedAt: 1})
	v.e.EvaluateJob(ctx, id)
	if got := v.tr.(*rec).take(); len(got) != 0 {
		t.Fatalf("calls = %v", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/receipt/`
Expected: FAIL（`v.e.EvaluateJob undefined`）

- [ ] **Step 3: 实现**

在 `internal/receipt/receipt.go`：

1. 常量区新增 `TextFetchFailedPrefix = "⚠️ 代取失败："`（导出，Task 6 复用）。
2. 把 `Evaluate` 中 `switch { case pending > 0: … default: … }` 这段聚合逻辑抽成方法 `apply`，`Evaluate` 与 `EvaluateJob` 共用：

```go
// apply drives the reaction / reply state machine for one tracked item (a message or a fetch job).
func (e *Engine) apply(ctx context.Context, info *store.ReceiptInfo, main []store.MediaStatus, cur string, save func(string)) {
	var pending, failed, tooLarge int
	firstErr := ""
	for _, m := range main {
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
		if cur == store.ReceiptNone {
			if err := e.react(ctx, info, EmojiSeen); err == nil {
				save(store.ReceiptSeen)
			}
		}
	case failed > 0:
		if cur != store.ReceiptFailed {
			if cur == store.ReceiptNone {
				e.reactLogOnly(ctx, info, EmojiSeen)
			}
			if err := e.reply(ctx, info, textFailedPrefix+truncate(botapifs.RedactPath(firstErr), 200)); err == nil {
				save(store.ReceiptFailed)
			}
		}
	default:
		if cur != store.ReceiptDone {
			if err := e.react(ctx, info, EmojiDone); err == nil {
				if tooLarge > 0 {
					e.replyLogOnly(ctx, info, textTooLarge)
				}
				save(store.ReceiptDone)
			}
		}
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
	e.apply(ctx, info, info.Main, info.Receipt, func(r string) { e.set(ctx, messageID, r) })
}

// EvaluateJob reports a userbot fetch job's progress on the sender's original link message.
func (e *Engine) EvaluateJob(ctx context.Context, jobID int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ji, err := e.st.GetJobReceiptInfo(ctx, jobID)
	if err != nil {
		log.Printf("receipt: info for fetch job %d: %v", jobID, err)
		return
	}
	info := &store.ReceiptInfo{BotID: ji.BotID, TgChatID: ji.TgChatID, TgMessageID: ji.TgMessageID}
	save := func(r string) {
		if err := e.st.SetFetchJobReceipt(ctx, jobID, r); err != nil {
			log.Printf("receipt: save state for fetch job %d: %v", jobID, err)
		}
	}
	switch ji.State {
	case store.JobQueued, store.JobFetching:
		if ji.Receipt == store.ReceiptNone {
			if err := e.react(ctx, info, EmojiSeen); err == nil {
				save(store.ReceiptSeen)
			}
		}
	case store.JobFailed:
		if ji.Receipt != store.ReceiptFailed {
			if ji.Receipt == store.ReceiptNone {
				e.reactLogOnly(ctx, info, EmojiSeen)
			}
			if err := e.reply(ctx, info, TextFetchFailedPrefix+truncate(ji.Error, 200)); err == nil {
				save(store.ReceiptFailed)
			}
		}
	case store.JobFetched:
		e.apply(ctx, info, ji.Main, ji.Receipt, save)
	}
}
```

3. `MediaSettled` 在逐条 `Evaluate` 之后，收集这些消息关联的 job（去重）并逐个 `EvaluateJob`：

```go
func (e *Engine) MediaSettled(ctx context.Context, mediaID int64) {
	ids, err := e.st.MessagesForMedia(ctx, mediaID)
	if err != nil {
		log.Printf("receipt: messages for media %d: %v", mediaID, err)
		return
	}
	jobs := map[int64]bool{}
	var order []int64
	for _, id := range ids {
		e.Evaluate(ctx, id)
		js, err := e.st.FetchJobsForMessage(ctx, id)
		if err != nil {
			log.Printf("receipt: fetch jobs for message %d: %v", id, err)
			continue
		}
		for _, j := range js {
			if !jobs[j] {
				jobs[j] = true
				order = append(order, j)
			}
		}
	}
	for _, j := range order {
		e.EvaluateJob(ctx, j)
	}
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test -race ./internal/receipt/ && gofmt -l internal/receipt && go vet ./internal/receipt/`
Expected: PASS（含既有全部回执测试），无输出

- [ ] **Step 5: 提交**

```bash
git add internal/receipt/receipt.go internal/receipt/job_test.go
git commit -m "feat(receipt): receipts for userbot fetch jobs"
```

---
### Task 5: userbot.Service —— 连接、登录状态机、加密 session

**Files:**
- Create: `internal/userbot/session.go`
- Create: `internal/userbot/dialer.go`
- Create: `internal/userbot/service.go`
- Test: `internal/userbot/service_test.go`、`internal/userbot/fake_test.go`（测试共用的假 MTProto）

**Interfaces:**
- Consumes: Task 2 的 `GetUserbot` / `SaveUserbotSession` / `SetUserbotAccount` / `SetUserbotStatus` / `ClearUserbot`；`tgapp.Store.Load`；`seal.Box`；`notify.Notifier`
- Produces:
  - 状态常量 `StateUnconfigured = "unconfigured"`、`StateConnecting = "connecting"`、`StateLoggedOut = "logged_out"`、`StateCodeSent = "code_sent"`、`StatePasswordNeeded = "password_needed"`、`StateReady = "ready"`、`StateError = "error"`
  - 错误 `ErrNotConfigured`（`请先在设置中填写 api_id / api_hash`）、`ErrNotConnected`（`代取账号尚未连接到 Telegram，请稍后重试`）、`ErrNotReady`（`代取账号未登录`）、`ErrBadState`（`登录步骤已失效，请从输入手机号重新开始`）、`ErrAlreadyLoggedIn`（`已登录，如需更换账号请先登出`）；`type InputError struct{ Msg string }`（用户输入类错误，HTTP 映射 400）
  - `type Info struct { State, Phone, Name string; TgUserID int64; Error string }`（JSON：`state`、`phone`、`name`、`tg_user_id`、`error`）
  - `type CredsLoader interface { Load(ctx) (*tgapp.Credentials, error) }`
  - `type Dialer interface { Dial(ctx, creds tgapp.Credentials, sess session.Storage, fn func(ctx context.Context, api *tg.Client) error) error }`、`type GotdDialer struct{}`
  - `func New(st *store.Store, box *seal.Box, creds CredsLoader, d Dialer, n notify.Notifier) *Service`
  - `func (s *Service) Run(ctx)`、`Reload()`、`Status(ctx) Info`
  - `func (s *Service) SendCode(ctx, phone string) error`、`SignIn(ctx, code string) (state string, err error)`、`Password(ctx, pw string) error`、`Logout(ctx) error`
  - `func (s *Service) With(ctx, fn func(api *tg.Client) error) error` —— 非 ready 返回 `ErrNotReady`；`fn` 返回 401 类错误时标记失效并返回 `ErrNotReady`
  - `func (s *Service) WaitReady(ctx, max time.Duration) bool` —— 仅在 `connecting` 时等待

**行为：**
- `Run` 循环：读凭据（未配置 → `unconfigured`，等 `Reload`）→ `connecting` → `Dial`；连上后 `auth.Status` 判断：已授权 → 写账号、`ready`；未授权且库内 `status = ready` → 视为 session 被吊销（走失效流程）；库内 `error` → `error`；否则 `logged_out`。连接期间收到 `Reload` → 断开重连；`Dial` 报错 → `connecting` + 错误信息，指数退避（1s 起，上限 `MaxBackoff` = 1min）
- 失效流程（`unauthorized`）只在库内状态尚非 `error` 时执行一次：库内 `status = error`、`last_error = 代取账号登录已失效，请重新登录`，内存 `error`，Bark 推 `tgarchive 代取账号失效`，并安排「断开后只丢弃 session」+ `Reload`
- `Logout`：尽力调用 `auth.logOut`（失败只记日志），安排「断开后清空整行（`ClearUserbot`）」+ `Reload`；当前未连接时直接 `ClearUserbot`
- 登录：`SendCode` 允许于 `logged_out` / `code_sent` / `password_needed` / `error`；`SignIn` 仅 `code_sent`；`Password` 仅 `password_needed`；`ready` 时 `SendCode` 返回 `ErrAlreadyLoggedIn`；`unconfigured` → `ErrNotConfigured`；尚无连接 → `ErrNotConnected`
- 错误文案映射（`InputError`）：`PHONE_NUMBER_INVALID` → `手机号格式不正确`；`PHONE_NUMBER_BANNED` → `该手机号已被 Telegram 封禁`；`PHONE_NUMBER_UNOCCUPIED` → `该手机号尚未注册 Telegram`；`PHONE_CODE_INVALID` / `PHONE_CODE_EMPTY` → `验证码错误`；`PHONE_CODE_EXPIRED` → `验证码已过期，请重新获取`；`PASSWORD_HASH_INVALID` → `二步验证密码错误`；`API_ID_INVALID` → `api_id / api_hash 无效`；FLOOD_WAIT → `操作过于频繁，请 N 分钟后重试`；其他错误原样返回（HTTP 502）
- 账号手机号以 `+` 开头保存（`tg.User.Phone` 不带 `+`）

- [ ] **Step 1: 写测试共用的假 MTProto**

`internal/userbot/fake_test.go`：

```go
package userbot

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/session"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgmock"

	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgapp"
)

var ctx = context.Background()

// fakeTG answers MTProto requests. extra, when set, is consulted first; returning (nil, nil)
// falls through to the defaults.
type fakeTG struct {
	mu         sync.Mutex
	authorized bool
	user       *tg.User
	calls      []string
	extra      func(req bin.Encoder) (bin.Encoder, error)
}

func newFakeTG() *fakeTG {
	return &fakeTG{user: &tg.User{ID: 99, FirstName: "Me", LastName: "Self", Phone: "8613800000000", Self: true}}
}

func unauthorizedErr() error {
	return &tgerr.Error{Code: 401, Message: "AUTH_KEY_UNREGISTERED", Type: "AUTH_KEY_UNREGISTERED"}
}

func (f *fakeTG) set(fn func(f *fakeTG)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeTG) called(typ string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == typ {
			n++
		}
	}
	return n
}

func (f *fakeTG) handle(req bin.Encoder) (bin.Encoder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%T", req))
	if f.extra != nil {
		if r, err := f.extra(req); r != nil || err != nil {
			return r, err
		}
	}
	switch r := req.(type) {
	case *tg.UsersGetUsersRequest:
		if !f.authorized {
			return nil, unauthorizedErr()
		}
		return &tg.UserClassVector{Elems: []tg.UserClass{f.user}}, nil
	case *tg.AuthSendCodeRequest:
		return &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeApp{Length: 5}, PhoneCodeHash: "hash-" + r.PhoneNumber}, nil
	case *tg.AuthSignInRequest:
		if r.PhoneCode != "12345" || r.PhoneCodeHash != "hash-"+r.PhoneNumber {
			return nil, &tgerr.Error{Code: 400, Message: "PHONE_CODE_INVALID", Type: "PHONE_CODE_INVALID"}
		}
		f.authorized = true
		return &tg.AuthAuthorization{User: f.user}, nil
	case *tg.AuthLogOutRequest:
		f.authorized = false
		return &tg.AuthLoggedOut{}, nil
	}
	return nil, fmt.Errorf("fakeTG: unexpected %T", req)
}

type fakeDialer struct{ tg *fakeTG }

func (d *fakeDialer) Dial(ctx context.Context, _ tgapp.Credentials, _ session.Storage, fn func(context.Context, *tg.Client) error) error {
	return fn(ctx, tg.NewClient(tgmock.Invoker(d.tg.handle)))
}

type countNotifier struct {
	mu     sync.Mutex
	titles []string
}

func (n *countNotifier) Notify(_ context.Context, title, _ string) {
	n.mu.Lock()
	n.titles = append(n.titles, title)
	n.mu.Unlock()
}

func (n *countNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.titles)
}

type svcEnv struct {
	svc   *Service
	st    *store.Store
	box   *seal.Box
	creds *tgapp.Store
	n     *countNotifier
}

func newSvcEnv(t *testing.T, f *fakeTG, configured bool) *svcEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := seal.New(bytes.Repeat([]byte{2}, 32))
	creds := tgapp.New(st, box)
	if configured {
		if err := creds.Save(ctx, tgapp.Credentials{APIID: 1, APIHash: "0123456789abcdef0123456789abcdef"}, 1); err != nil {
			t.Fatal(err)
		}
	}
	n := &countNotifier{}
	svc := New(st, box, creds, &fakeDialer{tg: f}, n)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.Run(runCtx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &svcEnv{svc: svc, st: st, box: box, creds: creds, n: n}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *svcEnv) waitState(t *testing.T, want string) {
	t.Helper()
	eventually(t, "state "+want, func() bool { return e.svc.Status(ctx).State == want })
}
```

- [ ] **Step 2: 写失败测试**

`internal/userbot/service_test.go`：

```go
package userbot

import (
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/session"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/store"
	"tgarchive/internal/tgapp"
)

func TestUnconfiguredThenReload(t *testing.T) {
	e := newSvcEnv(t, newFakeTG(), false)
	e.waitState(t, StateUnconfigured)
	if err := e.svc.SendCode(ctx, "+100"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("SendCode unconfigured err = %v", err)
	}
	if e.svc.WaitReady(ctx, 0) {
		t.Fatal("WaitReady true while unconfigured")
	}
	e.creds.Save(ctx, tgapp.Credentials{APIID: 1, APIHash: "0123456789abcdef0123456789abcdef"}, 2)
	e.svc.Reload()
	e.waitState(t, StateLoggedOut)
}

func TestLoginWithCode(t *testing.T) {
	f := newFakeTG()
	e := newSvcEnv(t, f, true)
	e.waitState(t, StateLoggedOut)
	if _, err := e.svc.SignIn(ctx, "12345"); !errors.Is(err, ErrBadState) {
		t.Fatalf("SignIn before SendCode err = %v", err)
	}
	if err := e.svc.SendCode(ctx, "+8613800000000"); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Status(ctx).State; got != StateCodeSent {
		t.Fatalf("state = %s", got)
	}
	var ie *InputError
	if _, err := e.svc.SignIn(ctx, "00000"); !errors.As(err, &ie) || ie.Msg != "验证码错误" {
		t.Fatalf("wrong code err = %v", err)
	}
	state, err := e.svc.SignIn(ctx, "12345")
	if err != nil || state != StateReady {
		t.Fatalf("SignIn = %s, %v", state, err)
	}
	info := e.svc.Status(ctx)
	if info.State != StateReady || info.TgUserID != 99 || info.Name != "Me Self" || info.Phone != "+8613800000000" {
		t.Fatalf("info = %+v", info)
	}
	u, _ := e.st.GetUserbot(ctx)
	if u.Status != store.UserbotReady || u.TgUserID != 99 {
		t.Fatalf("db = %+v", u)
	}
	if err := e.svc.SendCode(ctx, "+1"); !errors.Is(err, ErrAlreadyLoggedIn) {
		t.Fatalf("SendCode when ready err = %v", err)
	}
}

func TestLoginNeedsPassword(t *testing.T) {
	f := newFakeTG()
	f.extra = func(req bin.Encoder) (bin.Encoder, error) {
		switch req.(type) {
		case *tg.AuthSignInRequest:
			return nil, &tgerr.Error{Code: 401, Message: "SESSION_PASSWORD_NEEDED", Type: "SESSION_PASSWORD_NEEDED"}
		case *tg.AccountGetPasswordRequest:
			return nil, &tgerr.Error{Code: 400, Message: "PASSWORD_HASH_INVALID", Type: "PASSWORD_HASH_INVALID"}
		}
		return nil, nil
	}
	e := newSvcEnv(t, f, true)
	e.waitState(t, StateLoggedOut)
	if err := e.svc.Password(ctx, "pw"); !errors.Is(err, ErrBadState) {
		t.Fatalf("Password before SignIn err = %v", err)
	}
	e.svc.SendCode(ctx, "+100")
	state, err := e.svc.SignIn(ctx, "12345")
	if err != nil || state != StatePasswordNeeded {
		t.Fatalf("SignIn = %s, %v", state, err)
	}
	var ie *InputError
	if err := e.svc.Password(ctx, "wrong"); !errors.As(err, &ie) || ie.Msg != "二步验证密码错误" {
		t.Fatalf("Password err = %v", err)
	}
	if got := e.svc.Status(ctx).State; got != StatePasswordNeeded {
		t.Fatalf("state after bad password = %s", got)
	}
}

func TestStartsReadyWhenAuthorized(t *testing.T) {
	f := newFakeTG()
	f.authorized = true
	e := newSvcEnv(t, f, true)
	e.waitState(t, StateReady)
	if !e.svc.WaitReady(ctx, 0) {
		t.Fatal("WaitReady false when ready")
	}
	called := false
	if err := e.svc.With(ctx, func(api *tg.Client) error { called = api != nil; return nil }); err != nil || !called {
		t.Fatalf("With = %v, called=%v", err, called)
	}
}

func TestUnauthorizedMarksErrorAndNotifiesOnce(t *testing.T) {
	f := newFakeTG()
	f.authorized = true
	e := newSvcEnv(t, f, true)
	e.waitState(t, StateReady)
	e.st.SaveUserbotSession(ctx, []byte("sealed"), 1)
	f.set(func(f *fakeTG) { f.authorized = false })
	self := func(api *tg.Client) error {
		_, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		return err
	}
	for i := 0; i < 3; i++ {
		if err := e.svc.With(ctx, self); !errors.Is(err, ErrNotReady) {
			t.Fatalf("With #%d err = %v", i, err)
		}
	}
	e.waitState(t, StateError)
	eventually(t, "session dropped", func() bool { u, _ := e.st.GetUserbot(ctx); return u.SessionEnc == nil })
	u, _ := e.st.GetUserbot(ctx)
	if u.Status != store.UserbotError || u.LastError == "" || u.TgUserID != 99 {
		t.Fatalf("db = %+v", u)
	}
	if e.n.count() != 1 {
		t.Fatalf("notifications = %d", e.n.count())
	}
	// Re-login is allowed from the error state.
	if err := e.svc.SendCode(ctx, "+100"); err != nil {
		t.Fatalf("SendCode from error = %v", err)
	}
}

func TestRevokedWhileOfflineDetectedOnConnect(t *testing.T) {
	f := newFakeTG()
	e := newSvcEnv(t, f, false)
	e.waitState(t, StateUnconfigured)
	e.st.SetUserbotAccount(ctx, "+100", 99, "Me", 1)
	e.creds.Save(ctx, tgapp.Credentials{APIID: 1, APIHash: "0123456789abcdef0123456789abcdef"}, 2)
	e.svc.Reload()
	e.waitState(t, StateError)
	if e.n.count() != 1 {
		t.Fatalf("notifications = %d", e.n.count())
	}
}

func TestLogoutClearsAccount(t *testing.T) {
	f := newFakeTG()
	f.authorized = true
	e := newSvcEnv(t, f, true)
	e.waitState(t, StateReady)
	if err := e.svc.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if f.called("*tg.AuthLogOutRequest") != 1 {
		t.Fatal("auth.logOut not called")
	}
	eventually(t, "account cleared", func() bool {
		u, _ := e.st.GetUserbot(ctx)
		return u.Status == store.UserbotLoggedOut && u.TgUserID == 0
	})
	e.waitState(t, StateLoggedOut)
	if e.n.count() != 0 {
		t.Fatalf("logout must not alert, got %d", e.n.count())
	}
}

func TestSessionStoreEncrypts(t *testing.T) {
	e := newSvcEnv(t, newFakeTG(), false)
	ss := &sessionStore{st: e.st, box: e.box, now: e.svc.Now}
	if _, err := ss.LoadSession(ctx); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("empty load err = %v", err)
	}
	if err := ss.StoreSession(ctx, []byte(`{"auth":"secret"}`)); err != nil {
		t.Fatal(err)
	}
	u, _ := e.st.GetUserbot(ctx)
	if len(u.SessionEnc) == 0 || string(u.SessionEnc) == `{"auth":"secret"}` {
		t.Fatalf("session stored in clear: %q", u.SessionEnc)
	}
	got, err := ss.LoadSession(ctx)
	if err != nil || string(got) != `{"auth":"secret"}` {
		t.Fatalf("load = %q, %v", got, err)
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/userbot/`
Expected: FAIL（`undefined: New` 等）

- [ ] **Step 4: 实现**

`internal/userbot/session.go`：

```go
package userbot

import (
	"context"
	"errors"
	"time"

	"github.com/gotd/td/session"

	"tgarchive/internal/seal"
	"tgarchive/internal/store"
)

// sessionStore keeps the gotd session (the account's auth key) sealed with TOKEN_ENC_KEY.
type sessionStore struct {
	st  *store.Store
	box *seal.Box
	now func() time.Time
}

func (s *sessionStore) LoadSession(ctx context.Context) ([]byte, error) {
	u, err := s.st.GetUserbot(ctx)
	if err != nil {
		return nil, err
	}
	if len(u.SessionEnc) == 0 {
		return nil, session.ErrNotFound
	}
	plain, err := s.box.Open(u.SessionEnc)
	if err != nil {
		return nil, errors.New("cannot decrypt userbot session; TOKEN_ENC_KEY changed?")
	}
	return plain, nil
}

func (s *sessionStore) StoreSession(ctx context.Context, data []byte) error {
	return s.st.SaveUserbotSession(ctx, s.box.Seal(data), s.now().Unix())
}
```

`internal/userbot/dialer.go`：

```go
package userbot

import (
	"context"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"tgarchive/internal/tgapp"
)

// Dialer runs fn with a connected MTProto client until fn returns or ctx ends.
type Dialer interface {
	Dial(ctx context.Context, creds tgapp.Credentials, sess session.Storage, fn func(ctx context.Context, api *tg.Client) error) error
}

type GotdDialer struct{}

func (GotdDialer) Dial(ctx context.Context, creds tgapp.Credentials, sess session.Storage, fn func(context.Context, *tg.Client) error) error {
	c := telegram.NewClient(creds.APIID, creds.APIHash, telegram.Options{SessionStorage: sess, NoUpdates: true})
	return c.Run(ctx, func(ctx context.Context) error { return fn(ctx, c.API()) })
}
```

`internal/userbot/service.go`：

```go
// Package userbot runs the MTProto user account that fetches messages bots cannot see
// (forward-protected posts), and its web-driven login.
package userbot

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/notify"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgapp"
)

const (
	StateUnconfigured   = "unconfigured"
	StateConnecting     = "connecting"
	StateLoggedOut      = "logged_out"
	StateCodeSent       = "code_sent"
	StatePasswordNeeded = "password_needed"
	StateReady          = "ready"
	StateError          = "error"
)

const msgRevoked = "代取账号登录已失效，请重新登录"

var (
	ErrNotConfigured   = errors.New("请先在设置中填写 api_id / api_hash")
	ErrNotConnected    = errors.New("代取账号尚未连接到 Telegram，请稍后重试")
	ErrNotReady        = errors.New("代取账号未登录")
	ErrBadState        = errors.New("登录步骤已失效，请从输入手机号重新开始")
	ErrAlreadyLoggedIn = errors.New("已登录，如需更换账号请先登出")
)

// InputError is a login error caused by what the user typed; it is safe to show verbatim.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

type Info struct {
	State    string `json:"state"`
	Phone    string `json:"phone"`
	Name     string `json:"name"`
	TgUserID int64  `json:"tg_user_id"`
	Error    string `json:"error"`
}

type CredsLoader interface {
	Load(ctx context.Context) (*tgapp.Credentials, error)
}

type clearMode int

const (
	clearNone    clearMode = iota
	clearSession           // session revoked: drop the auth key, keep account + error status
	clearAll               // logout: forget everything
)

type Service struct {
	st       *store.Store
	sess     *sessionStore
	creds    CredsLoader
	dialer   Dialer
	notifier notify.Notifier

	Now        func() time.Time
	MaxBackoff time.Duration

	reload chan struct{}

	mu       sync.Mutex
	api      *tg.Client
	auth     *auth.Client
	state    string
	lastErr  string
	phone    string
	codeHash string
	clear    clearMode
}

func New(st *store.Store, box *seal.Box, creds CredsLoader, d Dialer, n notify.Notifier) *Service {
	s := &Service{st: st, creds: creds, dialer: d, notifier: n, Now: time.Now, MaxBackoff: time.Minute,
		reload: make(chan struct{}, 1), state: StateConnecting}
	s.sess = &sessionStore{st: st, box: box, now: func() time.Time { return s.Now() }}
	return s
}

// Reload drops the current connection and reconnects with freshly loaded credentials.
func (s *Service) Reload() {
	select {
	case s.reload <- struct{}{}:
	default:
	}
}

func (s *Service) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		creds, err := s.creds.Load(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, store.ErrNotFound) {
				s.setState(StateUnconfigured, "")
			} else {
				s.setState(StateError, err.Error())
			}
			select {
			case <-ctx.Done():
				return
			case <-s.reload:
			}
			continue
		}
		s.setState(StateConnecting, "")
		connected := false
		err = s.dialer.Dial(ctx, *creds, s.sess, func(cctx context.Context, api *tg.Client) error {
			connected = true
			if err := s.attach(cctx, api, creds); err != nil {
				return err
			}
			select {
			case <-cctx.Done():
				return cctx.Err()
			case <-s.reload:
				return nil
			}
		})
		s.detach(context.WithoutCancel(ctx))
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			backoff = time.Second
			continue
		}
		if connected {
			backoff = time.Second
		}
		log.Printf("userbot: connection: %v (retry in %s)", err, backoff)
		s.setState(StateConnecting, err.Error())
		select {
		case <-ctx.Done():
			return
		case <-s.reload:
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, s.MaxBackoff)
	}
}

func (s *Service) attach(ctx context.Context, api *tg.Client, creds *tgapp.Credentials) error {
	a := auth.NewClient(api, rand.Reader, creds.APIID, creds.APIHash)
	st, err := a.Status(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.api, s.auth = api, a
	s.mu.Unlock()
	if st.Authorized {
		return s.markReady(ctx, st.User, "")
	}
	u, err := s.st.GetUserbot(ctx)
	if err != nil {
		return err
	}
	switch u.Status {
	case store.UserbotReady:
		s.unauthorized(ctx) // the session was revoked while we were offline
	case store.UserbotError:
		s.setState(StateError, u.LastError)
	default:
		s.setState(StateLoggedOut, "")
	}
	return nil
}

// detach forgets the connection and applies any pending session clean-up.
func (s *Service) detach(ctx context.Context) {
	s.mu.Lock()
	s.api, s.auth = nil, nil
	mode := s.clear
	s.clear = clearNone
	s.mu.Unlock()
	var err error
	switch mode {
	case clearSession:
		err = s.st.SaveUserbotSession(ctx, nil, s.Now().Unix())
	case clearAll:
		err = s.st.ClearUserbot(ctx, s.Now().Unix())
	}
	if err != nil {
		log.Printf("userbot: clear session: %v", err)
	}
}

func (s *Service) markReady(ctx context.Context, u *tg.User, phone string) error {
	if u.Phone != "" {
		phone = "+" + strings.TrimPrefix(u.Phone, "+")
	}
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if err := s.st.SetUserbotAccount(ctx, phone, u.ID, name, s.Now().Unix()); err != nil {
		return err
	}
	s.mu.Lock()
	s.state, s.lastErr, s.phone, s.codeHash = StateReady, "", "", ""
	s.mu.Unlock()
	return nil
}

// unauthorized handles a revoked session once: persist the error, alert, drop the auth key, reconnect.
func (s *Service) unauthorized(ctx context.Context) {
	u, err := s.st.GetUserbot(ctx)
	if err == nil && u.Status == store.UserbotError {
		s.setState(StateError, u.LastError)
		return
	}
	if err := s.st.SetUserbotStatus(ctx, store.UserbotError, msgRevoked, s.Now().Unix()); err != nil {
		log.Printf("userbot: save status: %v", err)
	}
	s.mu.Lock()
	s.state, s.lastErr, s.clear = StateError, msgRevoked, clearSession
	s.mu.Unlock()
	s.Reload()
	if s.notifier != nil {
		s.notifier.Notify(ctx, "tgarchive 代取账号失效", msgRevoked)
	}
}

func (s *Service) setState(state, lastErr string) {
	s.mu.Lock()
	s.state, s.lastErr = state, lastErr
	s.mu.Unlock()
}

func (s *Service) Status(ctx context.Context) Info {
	s.mu.Lock()
	info := Info{State: s.state, Error: s.lastErr, Phone: s.phone}
	s.mu.Unlock()
	if u, err := s.st.GetUserbot(ctx); err == nil {
		if u.Phone != "" {
			info.Phone = u.Phone
		}
		info.Name, info.TgUserID = u.Name, u.TgUserID
	}
	return info
}

// With runs fn against the logged-in account. A 401 from Telegram marks the session revoked.
func (s *Service) With(ctx context.Context, fn func(api *tg.Client) error) error {
	s.mu.Lock()
	api, state := s.api, s.state
	s.mu.Unlock()
	if api == nil || state != StateReady {
		return ErrNotReady
	}
	err := fn(api)
	if err != nil && auth.IsUnauthorized(err) {
		s.unauthorized(context.WithoutCancel(ctx))
		return ErrNotReady
	}
	return err
}

// WaitReady waits up to max for an in-progress connection; other states return immediately.
func (s *Service) WaitReady(ctx context.Context, max time.Duration) bool {
	deadline := time.Now().Add(max)
	for {
		s.mu.Lock()
		state := s.state
		s.mu.Unlock()
		if state == StateReady {
			return true
		}
		if state != StateConnecting || !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (s *Service) loginClient() (*auth.Client, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == StateUnconfigured {
		return nil, "", ErrNotConfigured
	}
	if s.auth == nil {
		return nil, "", ErrNotConnected
	}
	return s.auth, s.state, nil
}

func (s *Service) SendCode(ctx context.Context, phone string) error {
	a, state, err := s.loginClient()
	if err != nil {
		return err
	}
	if state == StateReady {
		return ErrAlreadyLoggedIn
	}
	sent, err := a.SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return inputErr(err)
	}
	sc, ok := sent.(*tg.AuthSentCode)
	if !ok {
		return fmt.Errorf("unexpected sent code type %T", sent)
	}
	s.mu.Lock()
	s.phone, s.codeHash, s.state, s.lastErr = phone, sc.PhoneCodeHash, StateCodeSent, ""
	s.mu.Unlock()
	return nil
}

func (s *Service) SignIn(ctx context.Context, code string) (string, error) {
	a, state, err := s.loginClient()
	if err != nil {
		return "", err
	}
	if state != StateCodeSent {
		return "", ErrBadState
	}
	s.mu.Lock()
	phone, hash := s.phone, s.codeHash
	s.mu.Unlock()
	authz, err := a.SignIn(ctx, phone, code, hash)
	if errors.Is(err, auth.ErrPasswordAuthNeeded) {
		s.setState(StatePasswordNeeded, "")
		return StatePasswordNeeded, nil
	}
	if err != nil {
		return "", inputErr(err)
	}
	return StateReady, s.finish(ctx, authz, phone)
}

func (s *Service) Password(ctx context.Context, password string) error {
	a, state, err := s.loginClient()
	if err != nil {
		return err
	}
	if state != StatePasswordNeeded {
		return ErrBadState
	}
	authz, err := a.Password(ctx, password)
	if err != nil {
		return inputErr(err)
	}
	s.mu.Lock()
	phone := s.phone
	s.mu.Unlock()
	return s.finish(ctx, authz, phone)
}

func (s *Service) finish(ctx context.Context, authz *tg.AuthAuthorization, phone string) error {
	u, ok := authz.User.(*tg.User)
	if !ok {
		return fmt.Errorf("unexpected user type %T", authz.User)
	}
	return s.markReady(ctx, u, phone)
}

func (s *Service) Logout(ctx context.Context) error {
	s.mu.Lock()
	api := s.api
	s.mu.Unlock()
	if api != nil {
		if _, err := api.AuthLogOut(ctx); err != nil {
			log.Printf("userbot: auth.logOut: %v", err)
		}
	}
	s.mu.Lock()
	s.state, s.lastErr, s.phone, s.codeHash = StateLoggedOut, "", "", ""
	if api != nil {
		s.clear = clearAll
	}
	s.mu.Unlock()
	if api == nil {
		return s.st.ClearUserbot(ctx, s.Now().Unix())
	}
	s.Reload()
	return nil
}

var inputMsgs = map[string]string{
	"PHONE_NUMBER_INVALID":    "手机号格式不正确",
	"PHONE_NUMBER_BANNED":     "该手机号已被 Telegram 封禁",
	"PHONE_NUMBER_UNOCCUPIED": "该手机号尚未注册 Telegram",
	"PHONE_CODE_INVALID":      "验证码错误",
	"PHONE_CODE_EMPTY":        "验证码错误",
	"PHONE_CODE_EXPIRED":      "验证码已过期，请重新获取",
	"PASSWORD_HASH_INVALID":   "二步验证密码错误",
	"API_ID_INVALID":          "api_id / api_hash 无效",
}

func inputErr(err error) error {
	if d, ok := tgerr.AsFloodWait(err); ok {
		return &InputError{Msg: fmt.Sprintf("操作过于频繁，请 %d 分钟后重试", ceilMinutes(d))}
	}
	if rpc, ok := tgerr.As(err); ok {
		if msg, ok := inputMsgs[rpc.Type]; ok {
			return &InputError{Msg: msg}
		}
	}
	return err
}

func ceilMinutes(d time.Duration) int { return max(1, int(math.Ceil(d.Minutes()))) }
```

- [ ] **Step 5: 运行确认通过**

Run: `go test -race ./internal/userbot/ && gofmt -l internal/userbot && go vet ./internal/userbot/`
Expected: PASS，无输出

- [ ] **Step 6: 提交**

```bash
git add internal/userbot/session.go internal/userbot/dialer.go internal/userbot/service.go internal/userbot/service_test.go internal/userbot/fake_test.go
git commit -m "feat(userbot): MTProto connection, web login state machine, sealed session"
```

---
### Task 6: userbot.Fetcher —— LinkHandler 与串行代取队列

**Files:**
- Create: `internal/userbot/fetcher.go`
- Test: `internal/userbot/fetcher_test.go`

**Interfaces:**
- Consumes:
  - `collector.LinkHandler` 契约：`TryHandle(ctx, botID int64, sender model.Sender, msg *model.Message, canFetch bool) (handled bool, err error)`，必须按 `(botID, msg.TgMessageID)` 幂等（本实现以 `(bot_id, sender_id, link_tg_message_id)` 唯一键保证）
  - Task 1 `linkparse.Candidate` / `Parse` / `ErrUnsupported`；Task 2 job / peer / sender 方法；Task 3 `mtproto.Convert` / `ChannelFrom` / `NamesFrom`；Task 4 `receipt.TextFetchFailedPrefix`、`(*receipt.Engine).EvaluateJob`；Task 5 `ErrNotReady`、`ceilMinutes`
  - 既有 `receipt.Transport`（`botclients.Registry` 实现）、`events.Hub.Publish`、`downloader.RemoveFiles`
- Produces:
  - `type API interface { With(ctx, fn func(api *tg.Client) error) error; WaitReady(ctx, max time.Duration) bool }`（`*Service` 实现）
  - `type JobReceipts interface { EvaluateJob(ctx context.Context, jobID int64) }`
  - `func NewFetcher(api API, st *store.Store, rc JobReceipts, tr receipt.Transport, hub *events.Hub, wake func(), mediaDir string) *Fetcher`；可调字段 `Gap`（默认 3s）、`MaxFlood`（300s）、`FloodPad`（1s）、`ReadyWait`（30s）、`Now`
  - `func (f *Fetcher) TryHandle(...)`（实现 `collector.LinkHandler`）
  - `func (f *Fetcher) Run(ctx)`：启动时 `RequeueFetchingJobs`，之后循环 `RunOnce`，有任务时两次之间睡 `Gap`，无任务时等唤醒或 30s
  - `func (f *Fetcher) RunOnce(ctx) (bool, error)`
  - 包级辅助 `messagesOf(res tg.MessagesMessagesClass) ([]tg.MessageClass, []tg.UserClass, []tg.ChatClass, error)`（Task 7 复用）

**行为：**
- `TryHandle`：`!canFetch`、`msg.Kind != text`、带媒体、或 `linkparse.Candidate` 不成立 → `(false, nil)`。否则先 `UpsertSender`，再：
  - `Parse` 失败 → 建 `unsupported` job（`error = 不支持的链接格式`）；**仅当新建**时直接经 Transport 回复 `⚠️ 代取失败：不支持的链接格式`（30s 超时，失败只记日志）；返回 `(false, nil)`，消息按普通文本存档
  - `Parse` 成功 → 建 `queued` job；新建时 `EvaluateJob`（👀）并唤醒队列；返回 `(true, nil)`
  - 任何存储错误 → `(false, err)`（collector 会重投该 update）
- 处理一个 job：等待账号就绪（`WaitReady(ReadyWait)`，否则失败 `代取账号未登录`）→ 解析 peer → `channels.getMessages` 取链接消息；带 `grouped_id` 且无 `?single` 时再取 `[id-10, id+10]`（跳过 ≤0）中同组者，按 ID 升序 → 逐条 `mtproto.Convert` + `Ingest`（`Offset = 0`，`Sender` 取自 `senders` 表）+ `LinkFetchJobMessage` + 发 `message.created/updated` → `FinishFetchJob(fetched)` → `EvaluateJob` → 唤醒下载器
- peer 解析：公开链接每次 `contacts.resolveUsername`（结果须为 `PeerChannel`，否则 `链接不是频道或超级群消息`；`USERNAME_NOT_OCCUPIED` / `USERNAME_INVALID` → `频道或群组不存在`）；私有链接先查 `userbot_peers`，未命中则遍历 `messages.getDialogs`（gotd `telegram/query/dialogs`，每批 100，找到即停）并把见到的全部非 `min` 频道写入缓存，仍无 → `私有群/频道，代取账号未加入`
- 错误 → 原因：FLOOD_WAIT ≤ `MaxFlood` 且本 job 重试未满 3 次 → 睡 `d + FloodPad` 后重试；否则 `被限流，请 N 分钟后重试`；`ErrNotReady` → `代取账号未登录`；`CHANNEL_PRIVATE` / `CHANNEL_INVALID` / `CHANNEL_PUBLIC_GROUP_NA` → `私有群/频道，代取账号未加入`；链接消息缺失或为 `MessageEmpty`、`MESSAGE_ID_INVALID` → `消息不存在或已被删除`；其他 → `err.Error()`
- `ctx` 取消导致的失败不落 `failed`（job 留在 `fetching`，下次启动重排）

- [ ] **Step 1: 写失败测试**

`internal/userbot/fetcher_test.go`：

```go
package userbot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgmock"

	"tgarchive/internal/events"
	"tgarchive/internal/model"
	"tgarchive/internal/receipt"
	"tgarchive/internal/store"
)

type recTransport struct {
	mu    sync.Mutex
	calls []string
}

func (r *recTransport) SetReaction(_ context.Context, _, chatID, msgID int64, emoji string) error {
	r.add(fmt.Sprintf("react %d %d %s", chatID, msgID, emoji))
	return nil
}

func (r *recTransport) Reply(_ context.Context, _, chatID, msgID int64, text string) error {
	r.add(fmt.Sprintf("reply %d %d %s", chatID, msgID, text))
	return nil
}

func (r *recTransport) add(s string) { r.mu.Lock(); r.calls = append(r.calls, s); r.mu.Unlock() }

func (r *recTransport) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.calls
	r.calls = nil
	return out
}

type fakeAPI struct {
	mu     sync.Mutex
	ready  bool
	client *tg.Client
}

func (a *fakeAPI) With(_ context.Context, fn func(*tg.Client) error) error {
	a.mu.Lock()
	ready := a.ready
	a.mu.Unlock()
	if !ready {
		return ErrNotReady
	}
	return fn(a.client)
}

func (a *fakeAPI) WaitReady(context.Context, time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ready
}

// channelTG serves one public channel ("chan", id 500) and its posts.
type channelTG struct {
	*fakeTG
	ch      *tg.Channel
	posts   map[int]*tg.Message
	member  bool  // channel appears in the account's dialogs
	failGet []error // errors returned by successive channels.getMessages calls before succeeding
}

func newChannelTG() *channelTG {
	c := &channelTG{fakeTG: newFakeTG(), posts: map[int]*tg.Message{},
		ch: &tg.Channel{ID: 500, AccessHash: 5005, Title: "Chan", Username: "chan", Photo: &tg.ChatPhotoEmpty{}}}
	c.extra = c.serve
	return c
}

func (c *channelTG) post(id int, text string, group int64) *tg.Message {
	m := &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: 500}, Date: 1700000000 + id, Message: text}
	if group != 0 {
		m.SetGroupedID(group)
	}
	c.posts[id] = m
	return m
}

func (c *channelTG) serve(req bin.Encoder) (bin.Encoder, error) {
	switch r := req.(type) {
	case *tg.ContactsResolveUsernameRequest:
		if r.Username != "chan" {
			return nil, tgerr.New(400, "USERNAME_NOT_OCCUPIED")
		}
		return &tg.ContactsResolvedPeer{Peer: &tg.PeerChannel{ChannelID: 500}, Chats: []tg.ChatClass{c.ch}}, nil
	case *tg.ChannelsGetMessagesRequest:
		if in, ok := r.Channel.(*tg.InputChannel); !ok || in.ChannelID != 500 || in.AccessHash != 5005 {
			return nil, tgerr.New(400, "CHANNEL_INVALID")
		}
		if len(c.failGet) > 0 {
			err := c.failGet[0]
			c.failGet = c.failGet[1:]
			return nil, err
		}
		out := &tg.MessagesChannelMessages{Chats: []tg.ChatClass{c.ch}}
		for _, im := range r.ID {
			id := im.(*tg.InputMessageID).ID
			if p, ok := c.posts[id]; ok {
				out.Messages = append(out.Messages, p)
			} else {
				out.Messages = append(out.Messages, &tg.MessageEmpty{ID: id})
			}
		}
		return out, nil
	case *tg.MessagesGetDialogsRequest:
		out := &tg.MessagesDialogs{}
		if c.member {
			out.Dialogs = []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 500}, TopMessage: 1}}
			out.Messages = []tg.MessageClass{&tg.Message{ID: 1, PeerID: &tg.PeerChannel{ChannelID: 500}, Date: 1}}
			out.Chats = []tg.ChatClass{c.ch}
		}
		return out, nil
	}
	return nil, nil
}

type fetchEnv struct {
	f     *Fetcher
	st    *store.Store
	tr    *recTransport
	tg    *channelTG
	api   *fakeAPI
	bot   int64
	wakes int
}

func newFetchEnv(t *testing.T) *fetchEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bot, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	e := &fetchEnv{st: st, tr: &recTransport{}, tg: newChannelTG(), bot: bot}
	e.api = &fakeAPI{ready: true, client: tg.NewClient(tgmock.Invoker(e.tg.handle))}
	e.f = NewFetcher(e.api, st, receipt.New(st, e.tr), e.tr, events.NewHub(), func() { e.wakes++ }, t.TempDir())
	e.f.Gap, e.f.FloodPad = 0, 0
	return e
}

var me = model.Sender{TgUserID: 42, FirstName: "Shinya"}

func linkMsg(id int64, text string) *model.Message {
	return &model.Message{TgMessageID: id, Source: model.SourceBotUpdate, Kind: model.KindText, Text: text}
}

func (e *fetchEnv) submit(t *testing.T, id int64, link string) {
	t.Helper()
	handled, err := e.f.TryHandle(ctx, e.bot, me, linkMsg(id, link), true)
	if err != nil || !handled {
		t.Fatalf("TryHandle(%s) = %v, %v", link, handled, err)
	}
}

func (e *fetchEnv) runOne(t *testing.T) {
	t.Helper()
	did, err := e.f.RunOnce(ctx)
	if err != nil || !did {
		t.Fatalf("RunOnce = %v, %v", did, err)
	}
}

func (e *fetchEnv) messages(t *testing.T) []store.MessageView {
	t.Helper()
	chats, err := e.st.ListChats(ctx, e.bot)
	if err != nil || len(chats) != 1 {
		t.Fatalf("chats = %+v, %v", chats, err)
	}
	msgs, err := e.st.ListMessages(ctx, chats[0].ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestTryHandleIgnoresNonCandidates(t *testing.T) {
	e := newFetchEnv(t)
	photo := linkMsg(1, "https://t.me/chan/1")
	photo.Kind, photo.Media = model.KindPhoto, []model.Media{{DedupeKey: "bot:x", Role: model.RoleMain}}
	cases := []struct {
		msg      *model.Message
		canFetch bool
	}{
		{linkMsg(1, "https://t.me/chan/1"), false},
		{linkMsg(2, "hello"), true},
		{linkMsg(3, "look https://t.me/chan/1"), true},
		{photo, true},
	}
	for i, c := range cases {
		if handled, err := e.f.TryHandle(ctx, e.bot, me, c.msg, c.canFetch); handled || err != nil {
			t.Fatalf("case %d = %v, %v", i, handled, err)
		}
	}
	if _, err := e.st.ClaimNextFetchJob(ctx, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a job was created: %v", err)
	}
	if got := e.tr.take(); len(got) != 0 {
		t.Fatalf("calls = %v", got)
	}
}

func TestTryHandleIdempotent(t *testing.T) {
	e := newFetchEnv(t)
	e.submit(t, 10, "https://t.me/chan/42")
	e.submit(t, 10, "https://t.me/chan/42")
	if got := e.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👀"}) {
		t.Fatalf("calls = %v", got)
	}
	e.runOne(t)
	if did, _ := e.f.RunOnce(ctx); did {
		t.Fatal("duplicate job queued")
	}
	if snd, err := e.st.GetSender(ctx, 42); err != nil || snd.FirstName != "Shinya" {
		t.Fatalf("sender = %+v, %v", snd, err)
	}
}

func TestUnsupportedLinkRepliesOnce(t *testing.T) {
	e := newFetchEnv(t)
	for i := 0; i < 2; i++ {
		handled, err := e.f.TryHandle(ctx, e.bot, me, linkMsg(11, "https://t.me/joinchat/AbC"), true)
		if handled || err != nil {
			t.Fatalf("TryHandle = %v, %v", handled, err)
		}
	}
	if got := e.tr.take(); !reflect.DeepEqual(got, []string{"reply 42 11 ⚠️ 代取失败：不支持的链接格式"}) {
		t.Fatalf("calls = %v", got)
	}
	if did, _ := e.f.RunOnce(ctx); did {
		t.Fatal("unsupported link was queued")
	}
}

func TestFetchPublicLink(t *testing.T) {
	e := newFetchEnv(t)
	p := e.tg.post(42, "caption", 0)
	p.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 9, AccessHash: 99, FileReference: []byte{1},
		Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "y", W: 1280, H: 960, Size: 900}}}}
	e.submit(t, 10, "https://t.me/chan/42")
	e.tr.take()
	e.runOne(t)
	msgs := e.messages(t)
	if len(msgs) != 1 {
		t.Fatalf("messages = %+v", msgs)
	}
	m := msgs[0]
	if m.Source != model.SourceUserbotFetch || m.TgMessageID != 42 || m.OriginLink != "https://t.me/chan/42" ||
		m.OriginChatTitle != "Chan" || m.Text != "caption" || len(m.Media) != 1 || m.Media[0].State != store.StatePending {
		t.Fatalf("message = %+v", m)
	}
	due, _ := e.st.DueMedia(ctx, 1<<40, 10)
	if len(due) != 1 || due[0].DedupeKey != "mt:photo:9" {
		t.Fatalf("due media = %+v", due)
	}
	if e.wakes != 1 {
		t.Fatalf("downloader wakes = %d", e.wakes)
	}
	if got := e.tr.take(); len(got) != 0 { // 👀 already set at enqueue; 👌 waits for the photo
		t.Fatalf("calls = %v", got)
	}
}

func TestFetchTextPostCompletes(t *testing.T) {
	e := newFetchEnv(t)
	e.tg.post(42, "hello", 0)
	e.submit(t, 10, "t.me/chan/42")
	e.runOne(t)
	if got := e.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👀", "react 42 10 👌"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestFetchAlbum(t *testing.T) {
	e := newFetchEnv(t)
	e.tg.post(40, "a", 7)
	e.tg.post(41, "b", 7)
	e.tg.post(42, "c", 7)
	e.tg.post(43, "other album", 8)
	e.tg.post(44, "plain", 0)
	e.submit(t, 10, "https://t.me/chan/41")
	e.runOne(t)
	var ids []int64
	for _, m := range e.messages(t) {
		ids = append(ids, m.TgMessageID)
	}
	if !reflect.DeepEqual(ids, []int64{40, 41, 42}) {
		t.Fatalf("album ids = %v", ids)
	}
	before := e.tg.called("*tg.ChannelsGetMessagesRequest")
	e.submit(t, 11, "https://t.me/chan/41?single")
	e.runOne(t)
	if n := e.tg.called("*tg.ChannelsGetMessagesRequest") - before; n != 1 {
		t.Fatalf("single link made %d getMessages calls", n)
	}
	if n := len(e.messages(t)); n != 3 {
		t.Fatalf("refetch duplicated messages: %d", n)
	}
}

func TestPrivateLinkNeedsMembership(t *testing.T) {
	e := newFetchEnv(t)
	e.tg.post(42, "secret", 0)
	e.submit(t, 10, "https://t.me/c/500/42")
	e.runOne(t)
	want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 代取失败：私有群/频道，代取账号未加入"}
	if got := e.tr.take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v", got)
	}
	e.tg.set(func(*fakeTG) { e.tg.member = true })
	e.submit(t, 11, "https://t.me/c/500/42")
	e.runOne(t)
	if p, err := e.st.GetPeer(ctx, 500); err != nil || p.AccessHash != 5005 {
		t.Fatalf("peer not cached: %+v, %v", p, err)
	}
	dialogs := e.tg.called("*tg.MessagesGetDialogsRequest")
	e.submit(t, 12, "https://t.me/c/500/42")
	e.runOne(t)
	if e.tg.called("*tg.MessagesGetDialogsRequest") != dialogs {
		t.Fatal("cached peer still triggered getDialogs")
	}
	if msgs := e.messages(t); len(msgs) != 1 || msgs[0].OriginLink != "https://t.me/chan/42" {
		t.Fatalf("messages = %+v", msgs)
	}
}

func TestFetchFailures(t *testing.T) {
	cases := []struct {
		link  string
		setup func(e *fetchEnv)
		want  string
	}{
		{"https://t.me/chan/99", nil, "消息不存在或已被删除"},
		{"https://t.me/nochan/1", nil, "频道或群组不存在"},
		{"https://t.me/chan/42", func(e *fetchEnv) { e.tg.failGet = []error{tgerr.New(420, "FLOOD_WAIT_600")} }, "被限流，请 10 分钟后重试"},
		{"https://t.me/chan/42", func(e *fetchEnv) { e.tg.failGet = []error{tgerr.New(400, "CHANNEL_PRIVATE")} }, "私有群/频道，代取账号未加入"},
		{"https://t.me/chan/42", func(e *fetchEnv) { e.api.ready = false }, "代取账号未登录"},
	}
	for _, c := range cases {
		e := newFetchEnv(t)
		e.tg.post(42, "x", 0)
		if c.setup != nil {
			c.setup(e)
		}
		e.submit(t, 10, c.link)
		e.runOne(t)
		want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 代取失败：" + c.want}
		if got := e.tr.take(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: calls = %v", c.link, got)
		}
	}
}

func TestShortFloodWaitRetries(t *testing.T) {
	e := newFetchEnv(t)
	e.tg.post(42, "x", 0)
	e.tg.failGet = []error{tgerr.New(420, "FLOOD_WAIT_1")}
	e.submit(t, 10, "https://t.me/chan/42")
	start := time.Now()
	e.runOne(t)
	if time.Since(start) < time.Second {
		t.Fatal("did not wait out FLOOD_WAIT_1")
	}
	if got := e.tr.take(); !reflect.DeepEqual(got, []string{"react 42 10 👀", "react 42 10 👌"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestRunRequeuesInterruptedJob(t *testing.T) {
	e := newFetchEnv(t)
	e.tg.post(42, "x", 0)
	e.st.UpsertSender(ctx, me, 1)
	id, _, _ := e.st.CreateFetchJob(ctx, &store.FetchJob{BotID: e.bot, SenderID: 42, LinkTgMessageID: 10, Link: "https://t.me/chan/42",
		State: store.JobQueued, CreatedAt: 1, UpdatedAt: 1})
	e.st.ClaimNextFetchJob(ctx, 1) // simulate a crash mid-fetch
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.f.Run(runCtx); close(done) }()
	defer func() { cancel(); <-done }()
	eventually(t, "job fetched", func() bool {
		j, _ := e.st.GetFetchJob(ctx, id)
		return j.State == store.JobFetched
	})
	if n := len(e.messages(t)); n != 1 {
		t.Fatalf("messages = %d", n)
	}
}

```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/userbot/ -run 'Fetch|TryHandle|Unsupported|Private|Flood|Requeue'`
Expected: FAIL（`undefined: NewFetcher`）

- [ ] **Step 3: 实现**

`internal/userbot/fetcher.go`：

```go
package userbot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/convert/mtproto"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/linkparse"
	"tgarchive/internal/model"
	"tgarchive/internal/receipt"
	"tgarchive/internal/store"
)

// API is what the fetcher and the media source need from Service.
type API interface {
	With(ctx context.Context, fn func(api *tg.Client) error) error
	WaitReady(ctx context.Context, max time.Duration) bool
}

// JobReceipts reports a job's progress on the sender's link message (receipt.Engine).
type JobReceipts interface {
	EvaluateJob(ctx context.Context, jobID int64)
}

var (
	errNotMember  = errors.New("私有群/频道，代取账号未加入")
	errNoMessage  = errors.New("消息不存在或已被删除")
	errNotChannel = errors.New("链接不是频道或超级群消息")
	errNoChat     = errors.New("频道或群组不存在")
	errStop       = errors.New("stop")
)

const replyTimeout = 30 * time.Second

type Fetcher struct {
	api       API
	st        *store.Store
	receipts  JobReceipts
	transport receipt.Transport
	hub       *events.Hub
	wakeDL    func()
	mediaDir  string
	wake      chan struct{}

	Gap       time.Duration // pause between jobs
	MaxFlood  time.Duration // longest FLOOD_WAIT waited out in place
	FloodPad  time.Duration // slack added to each FLOOD_WAIT
	ReadyWait time.Duration // how long a job waits for a connecting account
	Now       func() time.Time
}

func NewFetcher(api API, st *store.Store, rc JobReceipts, tr receipt.Transport, hub *events.Hub, wake func(), mediaDir string) *Fetcher {
	return &Fetcher{api: api, st: st, receipts: rc, transport: tr, hub: hub, wakeDL: wake, mediaDir: mediaDir,
		wake: make(chan struct{}, 1), Gap: 3 * time.Second, MaxFlood: 300 * time.Second, FloodPad: time.Second,
		ReadyWait: 30 * time.Second, Now: time.Now}
}

// TryHandle implements collector.LinkHandler. It only enqueues; Run does the fetching.
func (f *Fetcher) TryHandle(ctx context.Context, botID int64, sender model.Sender, msg *model.Message, canFetch bool) (bool, error) {
	if !canFetch || msg.Kind != model.KindText || len(msg.Media) > 0 {
		return false, nil
	}
	raw, ok := linkparse.Candidate(msg.Text)
	if !ok {
		return false, nil
	}
	now := f.Now().Unix()
	if err := f.st.UpsertSender(ctx, sender, now); err != nil {
		return false, err
	}
	job := &store.FetchJob{BotID: botID, SenderID: sender.TgUserID, LinkTgMessageID: msg.TgMessageID, Link: raw,
		State: store.JobQueued, CreatedAt: now, UpdatedAt: now}
	if _, err := linkparse.Parse(raw); err != nil {
		job.State, job.Error = store.JobUnsupported, linkparse.ErrUnsupported.Error()
		_, created, err := f.st.CreateFetchJob(ctx, job)
		if err != nil {
			return false, err
		}
		if created {
			rctx, cancel := context.WithTimeout(ctx, replyTimeout)
			defer cancel()
			if err := f.transport.Reply(rctx, botID, sender.TgUserID, msg.TgMessageID, receipt.TextFetchFailedPrefix+job.Error); err != nil {
				log.Printf("userbot: reply on bot %d msg %d: %v", botID, msg.TgMessageID, err)
			}
		}
		return false, nil
	}
	id, created, err := f.st.CreateFetchJob(ctx, job)
	if err != nil {
		return false, err
	}
	if created {
		f.receipts.EvaluateJob(ctx, id)
		select {
		case f.wake <- struct{}{}:
		default:
		}
	}
	return true, nil
}

func (f *Fetcher) Run(ctx context.Context) {
	if n, err := f.st.RequeueFetchingJobs(ctx, f.Now().Unix()); err != nil {
		log.Printf("userbot: requeue interrupted jobs: %v", err)
	} else if n > 0 {
		log.Printf("userbot: requeued %d interrupted fetch jobs", n)
	}
	for ctx.Err() == nil {
		did, err := f.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("userbot: fetch queue: %v", err)
		}
		if did {
			if !sleep(ctx, f.Gap) {
				return
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
		case <-time.After(30 * time.Second):
		}
	}
}

// RunOnce processes the oldest queued job and reports whether there was one.
func (f *Fetcher) RunOnce(ctx context.Context) (bool, error) {
	job, err := f.st.ClaimNextFetchJob(ctx, f.Now().Unix())
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	f.process(ctx, job)
	return true, nil
}

func (f *Fetcher) process(ctx context.Context, job *store.FetchJob) {
	link, err := linkparse.Parse(job.Link)
	if err != nil {
		f.finish(ctx, job, store.JobFailed, linkparse.ErrUnsupported.Error())
		return
	}
	for attempt := 0; ; attempt++ {
		if !f.api.WaitReady(ctx, f.ReadyWait) {
			if ctx.Err() == nil {
				f.finish(ctx, job, store.JobFailed, ErrNotReady.Error())
			}
			return
		}
		got, err := f.fetch(ctx, link)
		if err == nil {
			err = f.archive(ctx, job, got)
		}
		if ctx.Err() != nil {
			return // left as 'fetching'; requeued on the next start
		}
		if err == nil {
			f.finish(ctx, job, store.JobFetched, "")
			if f.wakeDL != nil {
				f.wakeDL()
			}
			return
		}
		if d, ok := tgerr.AsFloodWait(err); ok && d <= f.MaxFlood && attempt < 3 {
			log.Printf("userbot: job %d: flood wait %s", job.ID, d)
			if !sleep(ctx, d+f.FloodPad) {
				return
			}
			continue
		}
		f.finish(ctx, job, store.JobFailed, reason(err))
		return
	}
}

func (f *Fetcher) finish(ctx context.Context, job *store.FetchJob, state, msg string) {
	if err := f.st.FinishFetchJob(ctx, job.ID, state, msg, f.Now().Unix()); err != nil {
		log.Printf("userbot: finish job %d: %v", job.ID, err)
		return
	}
	f.receipts.EvaluateJob(ctx, job.ID)
}

type fetched struct {
	msgs  []*tg.Message
	ch    mtproto.Channel
	names mtproto.Names
}

func (f *Fetcher) fetch(ctx context.Context, link linkparse.Link) (*fetched, error) {
	var out *fetched
	err := f.api.With(ctx, func(api *tg.Client) error {
		ch, err := f.resolve(ctx, api, link)
		if err != nil {
			return err
		}
		in := &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
		names := mtproto.NamesFrom(nil, nil)
		id := int(link.MsgID)
		got, err := getMessages(ctx, api, in, names, id)
		if err != nil {
			return err
		}
		first, ok := got[id]
		if !ok {
			return errNoMessage
		}
		list := []*tg.Message{first}
		if g, ok := first.GetGroupedID(); ok && !link.Single {
			var ids []int
			for i := id - 10; i <= id+10; i++ {
				if i > 0 && i != id {
					ids = append(ids, i)
				}
			}
			around, err := getMessages(ctx, api, in, names, ids...)
			if err != nil {
				return err
			}
			for _, m := range around {
				if mg, ok := m.GetGroupedID(); ok && mg == g {
					list = append(list, m)
				}
			}
			sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		}
		out = &fetched{msgs: list, ch: mtproto.ChannelFrom(ch), names: names}
		return nil
	})
	return out, err
}

func (f *Fetcher) resolve(ctx context.Context, api *tg.Client, link linkparse.Link) (*tg.Channel, error) {
	if link.Username != "" {
		r, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: link.Username})
		if err != nil {
			if tgerr.Is(err, "USERNAME_NOT_OCCUPIED", "USERNAME_INVALID") {
				return nil, errNoChat
			}
			return nil, err
		}
		pc, ok := r.Peer.(*tg.PeerChannel)
		if !ok {
			return nil, errNotChannel
		}
		for _, c := range r.Chats {
			if ch, ok := c.(*tg.Channel); ok && ch.ID == pc.ChannelID {
				f.savePeers(ctx, ch)
				return ch, nil
			}
		}
		return nil, errNotChannel
	}
	p, err := f.st.GetPeer(ctx, link.ChannelID)
	if err == nil {
		return &tg.Channel{ID: p.ChannelID, AccessHash: p.AccessHash, Title: p.Title, Username: p.Username}, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	seen := map[int64]*tg.Channel{}
	err = dialogs.NewQueryBuilder(api).GetDialogs().BatchSize(100).ForEach(ctx, func(_ context.Context, e dialogs.Elem) error {
		for id, c := range e.Entities.Channels() {
			if !c.Min {
				seen[id] = c
			}
		}
		if seen[link.ChannelID] != nil {
			return errStop
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return nil, err
	}
	all := make([]*tg.Channel, 0, len(seen))
	for _, c := range seen {
		all = append(all, c)
	}
	f.savePeers(ctx, all...)
	if c := seen[link.ChannelID]; c != nil {
		return c, nil
	}
	return nil, errNotMember
}

func (f *Fetcher) savePeers(ctx context.Context, chs ...*tg.Channel) {
	peers := make([]store.Peer, 0, len(chs))
	for _, c := range chs {
		if !c.Min {
			peers = append(peers, store.Peer{ChannelID: c.ID, AccessHash: c.AccessHash, Username: c.Username, Title: c.Title})
		}
	}
	if len(peers) == 0 {
		return
	}
	if err := f.st.PutPeers(ctx, peers, f.Now().Unix()); err != nil {
		log.Printf("userbot: cache peers: %v", err)
	}
}

func (f *Fetcher) archive(ctx context.Context, job *store.FetchJob, got *fetched) error {
	sender, err := f.st.GetSender(ctx, job.SenderID)
	if err != nil {
		return err
	}
	now := f.Now().Unix()
	for _, m := range got.msgs {
		msg, err := mtproto.Convert(m, got.ch, got.names)
		if err != nil {
			return err
		}
		ir, err := f.st.Ingest(ctx, store.IngestInput{BotID: job.BotID, Sender: sender, Msg: msg, Now: now})
		if err != nil {
			return err
		}
		downloader.RemoveFiles(f.mediaDir, ir.OrphanPaths)
		if err := f.st.LinkFetchJobMessage(ctx, job.ID, ir.MessageID); err != nil {
			return err
		}
		typ := "message.updated"
		if ir.Created {
			typ = "message.created"
		}
		f.hub.Publish(events.Event{Type: typ, Data: map[string]int64{"chat_id": ir.ChatID, "message_id": ir.MessageID}})
	}
	return nil
}

func getMessages(ctx context.Context, api *tg.Client, in tg.InputChannelClass, names mtproto.Names, ids ...int) (map[int]*tg.Message, error) {
	req := &tg.ChannelsGetMessagesRequest{Channel: in}
	for _, id := range ids {
		req.ID = append(req.ID, &tg.InputMessageID{ID: id})
	}
	res, err := api.ChannelsGetMessages(ctx, req)
	if err != nil {
		return nil, err
	}
	list, users, chats, err := messagesOf(res)
	if err != nil {
		return nil, err
	}
	names.Add(users, chats)
	out := map[int]*tg.Message{}
	for _, mc := range list {
		if m, ok := mc.(*tg.Message); ok {
			out[m.ID] = m
		}
	}
	return out, nil
}

func messagesOf(res tg.MessagesMessagesClass) ([]tg.MessageClass, []tg.UserClass, []tg.ChatClass, error) {
	switch r := res.(type) {
	case *tg.MessagesChannelMessages:
		return r.Messages, r.Users, r.Chats, nil
	case *tg.MessagesMessages:
		return r.Messages, r.Users, r.Chats, nil
	case *tg.MessagesMessagesSlice:
		return r.Messages, r.Users, r.Chats, nil
	}
	return nil, nil, nil, fmt.Errorf("unexpected messages result %T", res)
}

func reason(err error) string {
	if d, ok := tgerr.AsFloodWait(err); ok {
		return fmt.Sprintf("被限流，请 %d 分钟后重试", ceilMinutes(d))
	}
	switch {
	case errors.Is(err, ErrNotReady):
		return ErrNotReady.Error()
	case errors.Is(err, errNotMember), tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID", "CHANNEL_PUBLIC_GROUP_NA"):
		return errNotMember.Error()
	case errors.Is(err, errNoMessage), tgerr.Is(err, "MESSAGE_ID_INVALID"):
		return errNoMessage.Error()
	}
	return err.Error()
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
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

> gotd 的 dialogs 迭代器若要求测试夹具（`MessagesDialogs`）补充字段才能工作，只改 `fetcher_test.go` 的夹具，不改用其他生产实现。

- [ ] **Step 4: 运行确认通过**

Run: `go test -race ./internal/userbot/ && gofmt -l internal/userbot && go vet ./internal/userbot/`
Expected: PASS，无输出

- [ ] **Step 5: 提交**

```bash
git add internal/userbot/fetcher.go internal/userbot/fetcher_test.go
git commit -m "feat(userbot): idempotent link handler and serial fetch queue"
```

---

### Task 7: userbot.MTSource —— "mt:" 媒体下载

**Files:**
- Create: `internal/userbot/mtsource.go`
- Test: `internal/userbot/mtsource_test.go`

**Interfaces:**
- Consumes: `downloader.Source` 契约（`Fetch(ctx, m *store.Media, dstBase string) (path string, size int64, err error)`；文件写到 `dstBase + 扩展名`）；Task 3 `mtproto.MediaRef` / `Convert`；Task 6 `API`、`messagesOf`、`errNoMessage`
- Produces: `type MTSource struct { API API }`、`func (s *MTSource) Fetch(...)`、`func extFor(m *store.Media) string`

**行为：**
- 解出 `MediaRef` → 经 `API.With` 用 gotd `telegram/downloader` 流式写入 `<dst>.part`，成功后 rename 为 `<dst>`；任何失败删除 `.part`
- 下载报 `FILE_REFERENCE_EXPIRED` / `FILE_REFERENCE_INVALID` → `channels.getMessages` 重取原消息，`Convert` 后按同一 `dedupe_key` 找到新 `MediaRef`，再下载一次；原消息已不存在 → `消息不存在或已被删除`
- 其他错误原样返回，交给下载队列的重试状态机（1/5/30 分钟）
- 扩展名：文件名扩展（小写，须匹配 `^\.[a-z0-9]{1,10}$`）优先，否则按 mime 表，兜底 `.bin`

- [ ] **Step 1: 写失败测试**

`internal/userbot/mtsource_test.go`：

```go
package userbot

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgmock"

	"tgarchive/internal/convert/mtproto"
	"tgarchive/internal/store"
)

func photoPost(fileRef []byte) *tg.Message {
	return &tg.Message{ID: 42, PeerID: &tg.PeerChannel{ChannelID: 500}, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{
		ID: 9, AccessHash: 99, FileReference: fileRef, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "y", W: 10, H: 10, Size: 5}}}}}
}

func mtMedia(t *testing.T, fileRef []byte) *store.Media {
	t.Helper()
	conv, err := mtproto.Convert(photoPost(fileRef), mtproto.Channel{ID: 500, AccessHash: 5005, Username: "chan"}, mtproto.Names{})
	if err != nil {
		t.Fatal(err)
	}
	md := conv.Media[0]
	return &store.Media{ID: 1, DedupeKey: md.DedupeKey, SourceRef: md.SourceRef, Kind: md.Kind, Mime: md.Mime}
}

func mtEnv(t *testing.T, ready bool, h func(req bin.Encoder) (bin.Encoder, error)) *MTSource {
	f := newFakeTG()
	f.extra = h
	return &MTSource{API: &fakeAPI{ready: ready, client: tg.NewClient(tgmock.Invoker(f.handle))}}
}

func TestMTSourceDownloads(t *testing.T) {
	data := []byte("jpeg-bytes")
	src := mtEnv(t, true, func(req bin.Encoder) (bin.Encoder, error) {
		if r, ok := req.(*tg.UploadGetFileRequest); ok {
			loc := r.Location.(*tg.InputPhotoFileLocation)
			if loc.ID != 9 || loc.ThumbSize != "y" || !reflect.DeepEqual(loc.FileReference, []byte{1}) {
				t.Errorf("location = %+v", loc)
			}
			if r.Offset > 0 {
				return &tg.UploadFile{Type: &tg.StorageFileJpeg{}}, nil
			}
			return &tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: data}, nil
		}
		return nil, nil
	})
	dir := t.TempDir()
	path, size, err := src.Fetch(ctx, mtMedia(t, []byte{1}), filepath.Join(dir, "abc"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if path != filepath.Join(dir, "abc.jpg") || size != int64(len(data)) || string(got) != string(data) {
		t.Fatalf("path=%s size=%d got=%q", path, size, got)
	}
	if _, err := os.Stat(path + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(".part left behind")
	}
}

func TestMTSourceRefreshesExpiredReference(t *testing.T) {
	data := []byte("fresh")
	refetched := false
	src := mtEnv(t, true, func(req bin.Encoder) (bin.Encoder, error) {
		switch r := req.(type) {
		case *tg.UploadGetFileRequest:
			loc := r.Location.(*tg.InputPhotoFileLocation)
			if reflect.DeepEqual(loc.FileReference, []byte{1}) {
				return nil, tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			}
			if r.Offset > 0 {
				return &tg.UploadFile{Type: &tg.StorageFileJpeg{}}, nil
			}
			return &tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: data}, nil
		case *tg.ChannelsGetMessagesRequest:
			in := r.Channel.(*tg.InputChannel)
			if in.ChannelID != 500 || in.AccessHash != 5005 || r.ID[0].(*tg.InputMessageID).ID != 42 {
				t.Errorf("refetch request = %+v", r)
			}
			refetched = true
			return &tg.MessagesChannelMessages{Messages: []tg.MessageClass{photoPost([]byte{2})}}, nil
		}
		return nil, nil
	})
	path, _, err := src.Fetch(ctx, mtMedia(t, []byte{1}), filepath.Join(t.TempDir(), "abc"))
	if err != nil || !refetched {
		t.Fatalf("Fetch = %v, refetched=%v", err, refetched)
	}
	if got, _ := os.ReadFile(path); string(got) != "fresh" {
		t.Fatalf("content = %q", got)
	}
}

func TestMTSourceFailures(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := mtEnv(t, false, nil).Fetch(ctx, mtMedia(t, []byte{1}), filepath.Join(dir, "a")); !errors.Is(err, ErrNotReady) {
		t.Fatalf("not ready err = %v", err)
	}
	gone := mtEnv(t, true, func(req bin.Encoder) (bin.Encoder, error) {
		switch req.(type) {
		case *tg.UploadGetFileRequest:
			return nil, tgerr.New(400, "FILE_REFERENCE_EXPIRED")
		case *tg.ChannelsGetMessagesRequest:
			return &tg.MessagesChannelMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 42}}}, nil
		}
		return nil, nil
	})
	if _, _, err := gone.Fetch(ctx, mtMedia(t, []byte{1}), filepath.Join(dir, "b")); !errors.Is(err, errNoMessage) {
		t.Fatalf("deleted post err = %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("files left behind: %v", entries)
	}
	bad := &store.Media{DedupeKey: "mt:doc:1", SourceRef: "{not json"}
	if _, _, err := mtEnv(t, true, nil).Fetch(ctx, bad, filepath.Join(dir, "c")); err == nil {
		t.Fatal("bad source ref accepted")
	}
}

func TestExtFor(t *testing.T) {
	cases := map[string]*store.Media{
		".jpg":  {Mime: "image/jpeg"},
		".pdf":  {FileName: "Report.PDF", Mime: "application/octet-stream"},
		".mp4":  {FileName: "", Mime: "video/mp4"},
		".tgs":  {Mime: "application/x-tgsticker"},
		".bin":  {FileName: "weird.name with space", Mime: "application/x-unknown"},
		".ogg":  {Mime: "audio/ogg"},
		".webm": {FileName: "clip.webm", Mime: "video/webm"},
	}
	for want, m := range cases {
		if got := extFor(m); got != want {
			t.Errorf("extFor(%+v) = %s, want %s", m, got, want)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/userbot/ -run 'MTSource|ExtFor'`
Expected: FAIL（`undefined: MTSource`）

- [ ] **Step 3: 实现**

`internal/userbot/mtsource.go`：

```go
package userbot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	tgdown "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/convert/mtproto"
	"tgarchive/internal/store"
)

// MTSource downloads "mt:" media through the userbot account.
type MTSource struct {
	API API
}

func (s *MTSource) Fetch(ctx context.Context, m *store.Media, dstBase string) (string, int64, error) {
	var ref mtproto.MediaRef
	if err := json.Unmarshal([]byte(m.SourceRef), &ref); err != nil {
		return "", 0, fmt.Errorf("bad source ref for media %d: %w", m.ID, err)
	}
	dst := dstBase + extFor(m)
	tmp := dst + ".part"
	err := s.API.With(ctx, func(api *tg.Client) error {
		err := download(ctx, api, ref.Location(), tmp)
		if !tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID") {
			return err
		}
		fresh, err := refreshRef(ctx, api, ref, m.DedupeKey)
		if err != nil {
			return err
		}
		return download(ctx, api, fresh.Location(), tmp)
	})
	if err != nil {
		os.Remove(tmp)
		return "", 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", 0, err
	}
	st, err := os.Stat(dst)
	if err != nil {
		return "", 0, err
	}
	return dst, st.Size(), nil
}

func download(ctx context.Context, api *tg.Client, loc tg.InputFileLocationClass, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, err = tgdown.NewDownloader().Download(api, loc).Stream(ctx, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// refreshRef re-reads the source message to obtain a current file reference for dedupeKey.
func refreshRef(ctx context.Context, api *tg.Client, ref mtproto.MediaRef, dedupeKey string) (mtproto.MediaRef, error) {
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: ref.ChannelID, AccessHash: ref.ChannelHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: ref.MsgID}},
	})
	if err != nil {
		return mtproto.MediaRef{}, err
	}
	list, _, _, err := messagesOf(res)
	if err != nil {
		return mtproto.MediaRef{}, err
	}
	for _, mc := range list {
		m, ok := mc.(*tg.Message)
		if !ok || m.ID != ref.MsgID {
			continue
		}
		conv, err := mtproto.Convert(m, mtproto.Channel{ID: ref.ChannelID, AccessHash: ref.ChannelHash}, mtproto.Names{})
		if err != nil {
			return mtproto.MediaRef{}, err
		}
		for _, md := range conv.Media {
			if md.DedupeKey == dedupeKey {
				var fresh mtproto.MediaRef
				err := json.Unmarshal([]byte(md.SourceRef), &fresh)
				return fresh, err
			}
		}
	}
	return mtproto.MediaRef{}, errNoMessage
}

var safeExt = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)

var mimeExt = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
	"video/mp4": ".mp4", "video/webm": ".webm", "video/quicktime": ".mov",
	"audio/ogg": ".ogg", "audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/x-m4a": ".m4a", "audio/flac": ".flac",
	"application/x-tgsticker": ".tgs", "application/pdf": ".pdf", "application/zip": ".zip",
}

func extFor(m *store.Media) string {
	if e := strings.ToLower(filepath.Ext(m.FileName)); safeExt.MatchString(e) {
		return e
	}
	if e, ok := mimeExt[m.Mime]; ok {
		return e
	}
	return ".bin"
}
```

> gotd 的 `Stream` 以「返回字节数小于请求的 limit」判定结束；测试夹具对 `Offset > 0` 返回空块以兼容按块读取的实现。若实测只请求一次，删掉夹具中的 `Offset > 0` 分支即可，不改实现。

- [ ] **Step 4: 运行确认通过**

Run: `go test -race ./internal/userbot/ && gofmt -l internal/userbot && go vet ./internal/userbot/`
Expected: PASS，无输出

- [ ] **Step 5: 提交**

```bash
git add internal/userbot/mtsource.go internal/userbot/mtsource_test.go
git commit -m "feat(userbot): download mt: media with file-reference refresh"
```

---
### Task 8: httpapi —— userbot 登录接口

**Files:**
- Create: `internal/httpapi/userbot.go`
- Modify: `internal/httpapi/server.go`（`Server` 新字段 `Userbot UserbotService`；`Handler()` 中在 `s.settingsRoutes(mux)` 之后调用 `s.userbotRoutes(mux)`）
- Modify: `internal/httpapi/settings.go`（`putTelegramApp` 保存成功后 `if s.Userbot != nil { s.Userbot.Reload() }`，位于 `s.BotAPI.Apply(c)` 之后）
- Test: `internal/httpapi/userbot_test.go`

**Interfaces:**
- Consumes: Task 5 的 `userbot.Info`、`userbot.InputError`、`ErrNotConfigured` / `ErrBadState` / `ErrAlreadyLoggedIn` / `ErrNotConnected`
- Produces（HTTP）：

| 方法 | 路径 | 请求体 | 成功 |
|---|---|---|---|
| GET | `/api/admin/userbot` | — | 200 `Info` |
| POST | `/api/admin/userbot/phone` | `{"phone"}` | 200 `Info` |
| POST | `/api/admin/userbot/code` | `{"code"}` | 200 `Info` |
| POST | `/api/admin/userbot/password` | `{"password"}` | 200 `Info` |
| POST | `/api/admin/userbot/logout` | — | 204 |

- 错误映射：`*userbot.InputError` → 400（`error` = `Msg`）；`ErrNotConfigured` / `ErrBadState` / `ErrAlreadyLoggedIn` → 409；`ErrNotConnected` → 503；其他 → 502（`error` = `err.Error()`）
- 请求体上限 4 KiB（`http.MaxBytesReader`），超限或非法 JSON → 400 `invalid json`
- 手机号：去掉空格、`-`、`(`、`)` 后须匹配 `^\+?[0-9]{5,20}$`，否则 400 `手机号格式不正确`；不带 `+` 时补上
- 验证码：去首尾空白后须匹配 `^[0-9]{3,8}$`，否则 400 `验证码格式不正确`
- 密码：1–256 字节，否则 400 `请输入二步验证密码`；密码不进日志
- 每个登录调用使用 `r.Context()` 派生的 30s 超时

- [ ] **Step 1: 写失败测试**

`internal/httpapi/userbot_test.go`：

```go
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tgarchive/internal/config"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgapp"
	"tgarchive/internal/userbot"
)

type fakeUserbot struct {
	mu      sync.Mutex
	state   string
	phone   string
	code    string
	pw      string
	reloads int
	logouts int
	err     error
}

func (f *fakeUserbot) Status(context.Context) userbot.Info {
	f.mu.Lock()
	defer f.mu.Unlock()
	return userbot.Info{State: f.state, Phone: f.phone}
}

func (f *fakeUserbot) SendCode(_ context.Context, phone string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.phone, f.state = phone, userbot.StateCodeSent
	return nil
}

func (f *fakeUserbot) SignIn(_ context.Context, code string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.code, f.state = code, userbot.StatePasswordNeeded
	return f.state, nil
}

func (f *fakeUserbot) Password(_ context.Context, pw string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pw, f.state = pw, userbot.StateReady
	return nil
}

func (f *fakeUserbot) Logout(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logouts++
	f.state = userbot.StateLoggedOut
	return nil
}

func (f *fakeUserbot) Reload() {
	f.mu.Lock()
	f.reloads++
	f.mu.Unlock()
}

func newUserbotEnv(t *testing.T) (http.Handler, *fakeUserbot) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	fu := &fakeUserbot{state: userbot.StateLoggedOut}
	srv := &Server{Cfg: &config.Config{RequireForwardAuth: true}, Store: st, Box: box, TgApp: tgapp.New(st, box), Userbot: fu, Now: time.Now}
	return srv.Handler(), fu
}

func state(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var info userbot.Info
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatalf("body %s: %v", w.Body, err)
	}
	return info.State
}

func TestUserbotLoginFlow(t *testing.T) {
	h, fu := newUserbotEnv(t)
	if w := call(h, "GET", "/api/admin/userbot", nil); w.Code != 200 || state(t, w) != userbot.StateLoggedOut {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{"abc", "+12", "+86 138 0000 0000 0000 0000 1"} {
		if w := call(h, "POST", "/api/admin/userbot/phone", map[string]string{"phone": bad}); w.Code != 400 || !strings.Contains(w.Body.String(), "手机号格式不正确") {
			t.Fatalf("phone %q = %d %s", bad, w.Code, w.Body)
		}
	}
	w := call(h, "POST", "/api/admin/userbot/phone", map[string]string{"phone": " 86 138-0000-0000 "})
	if w.Code != 200 || state(t, w) != userbot.StateCodeSent || fu.phone != "+8613800000000" {
		t.Fatalf("phone = %d %s (got %q)", w.Code, w.Body, fu.phone)
	}
	if w := call(h, "POST", "/api/admin/userbot/code", map[string]string{"code": "12a"}); w.Code != 400 {
		t.Fatalf("bad code = %d", w.Code)
	}
	w = call(h, "POST", "/api/admin/userbot/code", map[string]string{"code": " 12345 "})
	if w.Code != 200 || state(t, w) != userbot.StatePasswordNeeded || fu.code != "12345" {
		t.Fatalf("code = %d %s", w.Code, w.Body)
	}
	if w := call(h, "POST", "/api/admin/userbot/password", map[string]string{"password": ""}); w.Code != 400 {
		t.Fatalf("empty password = %d", w.Code)
	}
	w = call(h, "POST", "/api/admin/userbot/password", map[string]string{"password": "p w"})
	if w.Code != 200 || state(t, w) != userbot.StateReady || fu.pw != "p w" {
		t.Fatalf("password = %d %s", w.Code, w.Body)
	}
	if w := call(h, "POST", "/api/admin/userbot/logout", nil); w.Code != 204 || fu.logouts != 1 {
		t.Fatalf("logout = %d", w.Code)
	}
}

func TestUserbotErrorMapping(t *testing.T) {
	h, fu := newUserbotEnv(t)
	cases := []struct {
		err  error
		code int
		msg  string
	}{
		{&userbot.InputError{Msg: "验证码错误"}, 400, "验证码错误"},
		{userbot.ErrNotConfigured, 409, "api_id"},
		{userbot.ErrBadState, 409, "登录步骤"},
		{userbot.ErrAlreadyLoggedIn, 409, "已登录"},
		{userbot.ErrNotConnected, 503, "尚未连接"},
		{errors.New("rpc error code 500: INTERNAL"), 502, "INTERNAL"},
	}
	for _, c := range cases {
		fu.err = c.err
		w := call(h, "POST", "/api/admin/userbot/phone", map[string]string{"phone": "+8613800000000"})
		if w.Code != c.code || !strings.Contains(w.Body.String(), c.msg) {
			t.Fatalf("%v → %d %s", c.err, w.Code, w.Body)
		}
	}
}

func TestUserbotBodyLimit(t *testing.T) {
	h, _ := newUserbotEnv(t)
	big := map[string]string{"password": strings.Repeat("x", 8192)}
	if w := call(h, "POST", "/api/admin/userbot/password", big); w.Code != 400 {
		t.Fatalf("oversized body = %d", w.Code)
	}
}

func TestTelegramAppSaveReloadsUserbot(t *testing.T) {
	h, fu := newUserbotEnv(t)
	w := call(h, "PUT", "/api/admin/telegram-app", map[string]any{"api_id": 4242, "api_hash": "0123456789abcdef0123456789abcdef"})
	if w.Code != 204 || fu.reloads != 1 {
		t.Fatalf("save = %d, reloads = %d", w.Code, fu.reloads)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/httpapi/ -run Userbot`
Expected: FAIL（`unknown field Userbot`）

- [ ] **Step 3: 实现**

`internal/httpapi/server.go`：`Server` 结构体在 `BotAPI` 字段后加

```go
	Userbot    UserbotService           // userbot login; nil disables /api/admin/userbot
```

并在 `Handler()` 中 `s.settingsRoutes(mux)` 之后加 `s.userbotRoutes(mux)`。

`internal/httpapi/settings.go`：`putTelegramApp` 中

```go
	if s.BotAPI != nil {
		s.BotAPI.Apply(c)
	}
	if s.Userbot != nil {
		s.Userbot.Reload()
	}
	w.WriteHeader(http.StatusNoContent)
```

`internal/httpapi/userbot.go`：

```go
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"tgarchive/internal/userbot"
)

type UserbotService interface {
	Status(ctx context.Context) userbot.Info
	SendCode(ctx context.Context, phone string) error
	SignIn(ctx context.Context, code string) (string, error)
	Password(ctx context.Context, password string) error
	Logout(ctx context.Context) error
	Reload()
}

var (
	phoneRe = regexp.MustCompile(`^\+?[0-9]{5,20}$`)
	codeRe  = regexp.MustCompile(`^[0-9]{3,8}$`)
)

const loginTimeout = 30 * time.Second

func (s *Server) userbotRoutes(mux *http.ServeMux) {
	if s.Userbot == nil {
		return
	}
	mux.HandleFunc("GET /api/admin/userbot", s.userbotStatus)
	mux.HandleFunc("POST /api/admin/userbot/phone", s.userbotPhone)
	mux.HandleFunc("POST /api/admin/userbot/code", s.userbotCode)
	mux.HandleFunc("POST /api/admin/userbot/password", s.userbotPassword)
	mux.HandleFunc("POST /api/admin/userbot/logout", s.userbotLogout)
}

func (s *Server) userbotStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Userbot.Status(r.Context()))
}

func (s *Server) userbotPhone(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone string `json:"phone"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	phone := strings.Map(func(c rune) rune {
		switch c {
		case ' ', '-', '(', ')':
			return -1
		}
		return c
	}, req.Phone)
	if !phoneRe.MatchString(phone) {
		writeErr(w, http.StatusBadRequest, "手机号格式不正确")
		return
	}
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}
	ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
	defer cancel()
	if err := s.Userbot.SendCode(ctx, phone); err != nil {
		userbotErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.Userbot.Status(r.Context()))
}

func (s *Server) userbotCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	code := strings.TrimSpace(req.Code)
	if !codeRe.MatchString(code) {
		writeErr(w, http.StatusBadRequest, "验证码格式不正确")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
	defer cancel()
	if _, err := s.Userbot.SignIn(ctx, code); err != nil {
		userbotErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.Userbot.Status(r.Context()))
}

func (s *Server) userbotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if req.Password == "" || len(req.Password) > 256 {
		writeErr(w, http.StatusBadRequest, "请输入二步验证密码")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
	defer cancel()
	if err := s.Userbot.Password(ctx, req.Password); err != nil {
		userbotErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.Userbot.Status(r.Context()))
}

func (s *Server) userbotLogout(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
	defer cancel()
	if err := s.Userbot.Logout(ctx); err != nil {
		userbotErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeSmall(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

func userbotErr(w http.ResponseWriter, err error) {
	var ie *userbot.InputError
	switch {
	case errors.As(err, &ie):
		writeErr(w, http.StatusBadRequest, ie.Msg)
	case errors.Is(err, userbot.ErrNotConfigured), errors.Is(err, userbot.ErrBadState), errors.Is(err, userbot.ErrAlreadyLoggedIn):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, userbot.ErrNotConnected):
		writeErr(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeErr(w, http.StatusBadGateway, err.Error())
	}
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test -race ./internal/httpapi/ && gofmt -l internal/httpapi && go vet ./internal/httpapi/`
Expected: PASS（含既有测试），无输出

- [ ] **Step 5: 提交**

```bash
git add internal/httpapi/userbot.go internal/httpapi/userbot_test.go internal/httpapi/server.go internal/httpapi/settings.go
git commit -m "feat(httpapi): userbot login endpoints; reload userbot on credential change"
```

---

### Task 9: 装配、端到端测试与 spec 同步

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`（新增 `TestUserbotFetchEndToEnd` 与假 MTProto Dialer）
- Modify: `docs/superpowers/specs/2026-10-04-tgarchive-design.md`

**Interfaces:**
- Consumes: Task 4–8 全部产出；既有 `collector.Deps.Links`、`downloader.Register`、`httpapi.Server`
- Produces: `func New(parent context.Context, cfg *config.Config) (*App, error)` 签名不变；包内 `newApp(parent, cfg, dialer userbot.Dialer)` 供测试注入

- [ ] **Step 1: 写失败的端到端测试**

在 `internal/app/app_test.go` 追加（`import` 增加 `github.com/gotd/td/bin`、`github.com/gotd/td/session`、`github.com/gotd/td/tg`、`github.com/gotd/td/tgmock`、`tgarchive/internal/tgapp`）：

```go
// mtDialer is an in-process MTProto account that can see one public channel with one photo post.
type mtDialer struct{ photo []byte }

func (d *mtDialer) handle(req bin.Encoder) (bin.Encoder, error) {
	ch := &tg.Channel{ID: 500, AccessHash: 5005, Title: "Chan", Username: "chan", Photo: &tg.ChatPhotoEmpty{}}
	switch r := req.(type) {
	case *tg.UsersGetUsersRequest:
		return &tg.UserClassVector{Elems: []tg.UserClass{&tg.User{ID: 99, FirstName: "Me", Self: true}}}, nil
	case *tg.ContactsResolveUsernameRequest:
		return &tg.ContactsResolvedPeer{Peer: &tg.PeerChannel{ChannelID: 500}, Chats: []tg.ChatClass{ch}}, nil
	case *tg.ChannelsGetMessagesRequest:
		post := &tg.Message{ID: 42, PeerID: &tg.PeerChannel{ChannelID: 500}, Date: 1700000000, Message: "protected",
			Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 9, AccessHash: 99, FileReference: []byte{1},
				Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "y", W: 1280, H: 960, Size: len(d.photo)}}}}}
		return &tg.MessagesChannelMessages{Messages: []tg.MessageClass{post}, Chats: []tg.ChatClass{ch}}, nil
	case *tg.UploadGetFileRequest:
		if r.Offset > 0 {
			return &tg.UploadFile{Type: &tg.StorageFileJpeg{}}, nil
		}
		return &tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: d.photo}, nil
	}
	return nil, fmt.Errorf("mtDialer: unexpected %T", req)
}

func (d *mtDialer) Dial(ctx context.Context, _ tgapp.Credentials, _ session.Storage, fn func(context.Context, *tg.Client) error) error {
	return fn(ctx, tg.NewClient(tgmock.Invoker(d.handle)))
}

func TestUserbotFetchEndToEnd(t *testing.T) {
	fake := tgtest.New(t)
	cfg := cfgFor(fake, t.TempDir())
	photo := []byte("protected-photo-bytes")
	a, err := newApp(context.Background(), cfg, &mtDialer{photo: photo})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Handler

	if code, b := req(t, h, "PUT", "/api/admin/telegram-app", map[string]any{"api_id": 1, "api_hash": "0123456789abcdef0123456789abcdef"}); code != 204 {
		t.Fatalf("save app creds = %d %s", code, b)
	}
	eventually(t, "userbot ready", func() bool {
		_, b := req(t, h, "GET", "/api/admin/userbot", nil)
		return strings.Contains(string(b), `"state":"ready"`)
	})
	botID := addBotAndWhitelist(t, h, 42)
	if code, b := req(t, h, "PUT", fmt.Sprintf("/api/admin/bots/%d/whitelist/42", botID), map[string]any{"note": "me", "can_fetch": true}); code != 204 {
		t.Fatalf("grant can_fetch = %d %s", code, b)
	}

	fake.PushMessage(tgtest.TextMsg(1, 42, "https://t.me/chan/42"))
	eventually(t, "👀 then 👌 on the link message", func() bool {
		e := emojis(fake)
		return len(e) == 2 && e[0] == "👀" && e[1] == "👌"
	})
	msgs := firstChatMessages(t, h)
	if len(msgs) != 1 {
		t.Fatalf("messages = %+v", msgs)
	}
	m := msgs[0]
	if m.Source != "userbot_fetch" || m.TgMessageID != 42 || m.Text != "protected" || m.OriginLink != "https://t.me/chan/42" ||
		len(m.Media) != 1 || m.Media[0].State != "done" {
		t.Fatalf("message = %+v", m)
	}
	code, body := req(t, h, "GET", fmt.Sprintf("/media/%d", m.Media[0].ID), nil)
	if code != 200 || string(body) != string(photo) {
		t.Fatalf("media = %d %q", code, body)
	}
	for _, c := range fake.Calls("setMessageReaction") {
		if int64(c.Params["message_id"].(float64)) != 1 {
			t.Fatalf("reaction on wrong message: %+v", c.Params)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/app/ -run UserbotFetch`
Expected: FAIL（`undefined: newApp`）

- [ ] **Step 3: 装配**

`internal/app/app.go`：

1. `import` 增加 `tgarchive/internal/userbot`。
2. `App` 结构体增加字段 `ub *userbot.Service` 与 `fetcher *userbot.Fetcher`。
3. 把 `func New(parent context.Context, cfg *config.Config) (*App, error) {` 的函数体移入 `func newApp(parent context.Context, cfg *config.Config, dialer userbot.Dialer) (*App, error)`，`New` 改为 `return newApp(parent, cfg, userbot.GotdDialer{})`。
4. 在 `newApp` 中：
   - 把 `tg := tgapp.New(st, box)` 移到 `rc := receipt.New(st, clients)` 之前，并新建 `notifier := &notify.Bark{File: cfg.BarkNotifyFile}`；
   - `dl.Register("bot", ...)` 之后加：

```go
	ub := userbot.New(st, box, tg, dialer, notifier)
	dl.Register("mt", &userbot.MTSource{API: ub})
	fetcher := userbot.NewFetcher(ub, st, rc, clients, hub, dl.Wake, mediaDir)
```

   - `collector.Deps` 中 `Notifier: notifier`，并加 `Links: fetcher`；
   - `httpapi.Server` 加 `Userbot: ub`；
   - 返回的 `&App{...}` 加 `ub: ub, fetcher: fetcher`。
5. `Start()` 中 `a.wg.Add(2)` 那两行之后加：

```go
	a.wg.Add(2)
	go func() { defer a.wg.Done(); a.ub.Run(a.ctx) }()
	go func() { defer a.wg.Done(); a.fetcher.Run(a.ctx) }()
```

（`Close()` 不变：`cancel` 后 `wg.Wait` 会等两者退出。）

- [ ] **Step 4: 同步 spec**

对 `docs/superpowers/specs/2026-10-04-tgarchive-design.md` 做以下替换（逐条精确替换原文）：

1. `dedupe_key TEXT,               -- bot:<file_unique_id> 或 mt:<photo/document id>` →
   `dedupe_key TEXT,               -- bot:<file_unique_id>；mt:photo:<id>[:<size>]、mt:doc:<id>[:thumb]`
2. 在 §4 SQL 代码块中 `settings(` 表定义之后、代码块结束之前追加：

```sql

userbot_peers(                   -- 账号可见频道的 access_hash 缓存（私有链接用）
  channel_id INTEGER PK, access_hash INTEGER, username TEXT, title TEXT, updated_at INTEGER)

fetch_jobs(                      -- 代取任务；一条链接消息一个任务
  id INTEGER PK, bot_id INTEGER, sender_id INTEGER, link_tg_message_id INTEGER, link TEXT,
  state TEXT,                    -- queued / fetching / fetched / failed / unsupported
  error TEXT, receipt TEXT, created_at INTEGER, updated_at INTEGER,
  UNIQUE(bot_id, sender_id, link_tg_message_id))

fetch_job_messages(job_id INTEGER, message_id INTEGER, PRIMARY KEY(job_id, message_id))
```

3. `媒体路径：` 那一行末尾追加：`userbot 代取的媒体在 \`media/mt/<yyyy>/<mm>/\`。代取消息的 \`date\` 为原帖时间，会话排序（\`last_message_at\`）取入库时间。`
4. `- 其他格式 → 回复「不支持的链接格式」` →
   `- 其他 t.me 链接 → 回复「⚠️ 代取失败：不支持的链接格式」，并按普通消息存档（只回复一次）`
5. `1. 原链接消息设 👀，任务入 userbot 串行队列` →
   `1. 写入 \`fetch_jobs\`（唯一键保证重投不重复入队）并在原链接消息设 👀，由串行队列处理；进程重启时 \`fetching\` 的任务重新排队`
6. 私有链接一行中 `查 gotd peer 存储，缺失则调用一次 \`messages.getDialogs\` 刷新后再查` →
   `查 \`userbot_peers\` 缓存，缺失则遍历一次 \`messages.getDialogs\`（找到即停）并缓存见到的频道后再查`
7. `5. 媒体经 \`upload.getFile\` 分块流式写入归档路径，单文件上限按账号（普通 2GB / Premium 4GB），复用下载队列的状态机与去重（\`dedupe_key = mt:<id>\`）` →
   `5. 媒体经 \`upload.getFile\` 分块流式写入归档路径，复用下载队列的状态机与去重（\`dedupe_key\` 见 §4）；file reference 过期时重取原消息刷新后重试`
8. `6. 完成设 👌；失败回复原因` →
   `6. 全部代取消息的主媒体落定后设 👌；代取失败回复「⚠️ 代取失败：<原因>」，媒体最终失败回复「⚠️ 存档失败：<原因>」`
9. §7 表格：`| GET | \`/media/:id\`、\`/media/:id/thumb\` |` → `| GET | \`/media/:id\` |`（缩略图本就是独立的 media 记录）
10. §7 表格：`| POST | \`/api/admin/userbot/{phone,code,password,logout}\` | userbot 登录流程 |` 替换为两行：

```
| GET | `/api/admin/userbot` | `{state, phone, name, tg_user_id, error}`；state ∈ unconfigured / connecting / logged_out / code_sent / password_needed / ready / error |
| POST | `/api/admin/userbot/{phone,code,password,logout}` | 登录流程：`{phone}` → `{code}` → `{password}`（如需）；返回最新状态；输入错误 400、步骤错误或未配置 409、未连接 503；logout 204 |
```

- [ ] **Step 5: 全量验证**

Run: `go test -race ./... && gofmt -l . && go vet ./... && go build ./...`
Expected: 全部 PASS，gofmt / vet 无输出

- [ ] **Step 6: 提交**

```bash
git add internal/app/app.go internal/app/app_test.go docs/superpowers/specs/2026-10-04-tgarchive-design.md
git commit -m "feat(app): wire userbot fetching end to end; sync spec"
```
