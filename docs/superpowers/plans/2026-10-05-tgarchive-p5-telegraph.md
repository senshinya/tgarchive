# tgarchive 计划 5：Telegraph 文章离线存档 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 白名单用户发给机器人的消息整条就是一个 Telegraph 文章链接（`telegra.ph` / `graph.org`）时，自动把文章正文与其中全部图片 / 视频离线存档；WebUI 在该消息下显示文章卡片，点开为仿 Telegram Instant View 的全屏阅读页。

**Architecture:** `collector` 在入库前用 `telegraph.Candidate` 识别链接，`store.Ingest` 在**同一事务**里为新消息建 `telegraph_jobs` 任务；`telegraph.Worker` 串行消费队列，经 `telegraph.Client` 调 `getPage`，`Normalize` 清洗节点树后由 `store.SaveArticle` 原子写入 `articles`、`web:` 媒体行（`message_media.role = 'article'`）并把任务置 `fetched`；媒体交给既有下载队列的新数据源 `downloader.WebSource`（拨号时校验地址防 SSRF）。回执沿用 `receipt.Engine.Evaluate`，把任务与文章媒体并入链接消息的 👀/👌 判定。前端新增 `/chat/:id/article/:mid` 路由承载阅读页（系统返回键即 `popstate`），`MediaViewer` 增加「显式列表」模式供文章内图片 / 视频切换。

**Tech Stack:** Go 1.26（模块 `tgarchive`，标准库 `net/http`，`modernc.org/sqlite` v1.60.1）；Preact 11 + TypeScript + Vite 8 + Vitest 5（`web/`），图标 `lucide-preact`。

**Spec:** `docs/superpowers/specs/2026-10-05-telegraph-archive-design.md`（本计划的依据，冲突处以它为准）；上位 spec `docs/superpowers/specs/2026-10-04-tgarchive-design.md`。

**前置：** 计划 1–4 已合入，当前分支 `feat/telegraph`。

**本计划不含**：部署变更（Dockerfile、CI、compose、Caddy 均不动）、版本号发布；移动端真机验收（spec §10 末条 390×844 浅 / 深色与 800px）在发布后按上线验收流程做，见文末「交付后」。

## 裁定（与 spec 的出入）

- **回执里文章媒体失败不阻止 👌，也不回复**：spec §6 写「任务 `fetched` 且文章 media 全部落定 → 👌（与普通媒体规则一致：失败的单张图不阻止 👌）」，但上位 spec §5 的普通媒体规则恰恰是「任一媒体最终 `failed` → 保留 👀 并回复」，括注自相矛盾。按 spec §6 与 §9 的明文行为执行：文章媒体只有 `pending` 会挡住 👌；`failed` / `too_large` 视为已落定，不回复（阅读页显示占位 + 原链接 + 重试）。链接消息自身的主媒体（不会有）仍按原规则。
- **退避在 worker 内原地等待**：spec §3 的 `telegraph_jobs` 没有 `next_attempt_at` 列，无法按时间调度重试。worker 是串行队列，网络错误后按 10s / 60s / 300s 原地 `sleep`（任务保持 `fetching`），`attempts` 每次落库；进程重启时任务回到 `queued`，`attempts` 保留，重启后继续计数（第 4 次失败即 `failed`）。网络整体不通时排队任务本来也会失败，原地等待不额外拖慢。
- **`telegraph_jobs.receipt` 列按 spec 原样建，但不使用**：一条链接消息只有一个 reaction，状态统一记在既有的 `messages.receipt`（none/seen/done/failed），由 `Evaluate` 驱动；另记一份会出现两处状态不一致。
- **任务在 `Ingest` 事务内创建**：spec §2 写「新建消息（`ir.Created`）若 `Candidate(text)` 成立，则创建 Telegraph 任务」。若在 `Ingest` 之后另起事务建任务，进程恰好在两者之间崩溃时 offset 已推进、任务永久丢失。改为 `IngestInput.TelegraphPath`，仅在本事务新建消息时插入任务，语义不变。
- **只认纯文本消息**：「整条消息即链接」限定 `kind = text` 且无媒体（图片说明里的链接不触发），与 userbot 代取的判定一致；仅新消息（非编辑）触发，`Deps.Telegraph` 为 nil 时整个功能关闭（链接按普通文本存档）。
- **编辑不碰文章快照**：既有 `Ingest` 编辑路径会 `DELETE FROM message_media WHERE message_id = ?`，会把文章媒体一并解绑并当孤儿删文件；改为只删 `role != 'article'`。编辑不产生新快照（spec「每次发送都是一份独立快照」）。
- **软删需显式清理**：`DeleteMessage` 是软删，`ON DELETE CASCADE` 不触发，因此在同一事务里显式删除 `articles` 与 `telegraph_jobs`；文章媒体随 `message_media` 解绑后由既有 `collectOrphans` 回收（被其他快照共用的不删）。`PurgeBot` 是硬删，靠外键级联。
- **卡片封面只在下载完成后给出**：`MessageView.article.image_media_id` 仅当封面媒体 `done` 时才有值（`omitempty`），避免卡片出现破图；封面下载完成时的 `media.updated` 会让前端刷新该消息。
- **`ok:false` 的其他错误码**：spec 只举了 `PAGE_NOT_FOUND`（→「文章不存在」）；其他 `ok:false` 同样直接 `failed`，原因为 `Telegraph 错误：<code>`。
- **`web` 数据源的永久失败**：新增 `downloader.Permanent(err)`，`Process` 遇到后立即 `failed`、不排重试。SSRF 拒绝与非 `http(s)`（含重定向到其他 scheme）→「地址不允许」；`4xx`（408、429 除外）→ `HTTP <code>`，同样不重试（spec 未定，重试 404 只会浪费 36 分钟）；`5xx`、超时等仍按既有 1/5/30 分钟重试。
- **web 媒体的扩展名与 MIME**：扩展名先按 `Content-Type` 白名单（jpeg/png/gif/webp/mp4/webm/quicktime），再按 URL 后缀白名单，否则 `.bin`；`media.mime` 不回写，`GET /api/messages/{id}/article` 按扩展名推断 `mime`。`/media/*` 既有的内联白名单不变，SVG/HTML 一律当附件下载。
- **`a.href` 同样补全**：spec §5 只说 `/file/...`，相对链接统一以 `https://telegra.ph/` 解析；`#锚点` 与 `http(s)`/`mailto:`/`tg:` 以外的 scheme 在服务端丢弃 `href`（元素保留为文字），前端再经 `safeHref`。
- **保留路径段**：`file`、`api`、`edit` 之外再加 `embed`、`upload`、`auth`、`js`、`css`、`images`、`img`（均为 telegra.ph 的站点路径）。
- **阅读页用路由承载 history**：spec §8 要求「打开阅读页 push 一条 history 记录，系统返回键关闭」。用路由 `/chat/:chatId/article/:messageId` 实现（可深链，刷新后仍在阅读页）；从卡片打开时 `history.state.fromChat = true`，阅读页的返回按钮据此 `history.back()`，深链进入则 `replace` 回会话。媒体查看器不占 history 记录（既有行为），在阅读页内打开查看器后按系统返回键会同时离开查看器与阅读页。
- **卡片竖线用 2px**：spec §8 写 2px，调研文档 Web A 为 3px，按 spec。
- **`MediaViewer` 的条目改为与来源无关的 `ViewerItem`**（`mediaId/kind/date/text/entities`），`ViewerTarget` 变为「会话」与「显式列表」两种形态的联合类型；会话模式行为不变。

## Global Constraints

- 不新增 Go 或 npm 依赖；`go.mod` / `go.sum`、`web/package.json` / `package-lock.json` 不变。Go 侧网络一律标准库 `net/http`
- 数据库 `MaxOpenConns(1)`：**持有 `*sql.Rows` 时不得再发起查询；事务内只能用 `tx`**
- 链接识别：去首尾空白后不含空白字符；scheme 可省（`http`/`https`）；host 为 `telegra.ph` 或 `graph.org`（大小写不敏感，可带 `www.`）；path 恰好一段、非空、非保留段；返回 URL 解码后的 path，不带前导 `/`
- `getPage`：`GET {TELEGRAPH_API_URL}/getPage/<path>?return_content=true`，默认 `https://api.telegra.ph`，`TELEGRAPH_API_URL` 仅用于测试；单次超时 30s；空闲轮询 30s；网络错误 / 5xx / 非 JSON 按 10s、60s、300s 退避，第 4 次仍失败 → `failed`
- 失败原因固定文案：`文章不存在`、`网络错误`、`地址不允许`；回复文案 `⚠️ 存档失败：<原因>`
- 文章媒体：`media` 行 `dedupe_key = 'web:' || hex(sha256(原始绝对 URL))`、`source_ref` = 该 URL、`kind` 为 `photo`/`video`，经 `message_media(role = 'article')` 关联链接消息；落盘路径由既有 `relBase` 决定（`media/web/<yyyy>/<mm>/<sha1(dedupe_key)>.<ext>`）；受 `MEDIA_MAX_BYTES` 约束（超限 → `too_large`）
- `web` 数据源：`User-Agent: tgarchive/1.0`；最多 5 次重定向；单请求超时 5 分钟；写 `.part` 后 rename，文件 0644；只允许 `http`/`https`；**拨号时**拒绝回环、私网、链路本地、组播、未指定地址与 `100.64.0.0/10`；`Transport.Proxy` 必须为 nil。生产代码中不存在绕过开关，测试只通过 `NewWebSource(max, allow)` / `newApp(..., webDialCheck)` 注入放行函数
- 节点白名单：标签 `a aside b blockquote br code em figcaption figure h3 h4 hr i iframe img li ol p pre s strong u ul video`，属性 `a.href`、`img.src`、`video.src`、`iframe.src`；`img`/`video` 存 `data-media-id` + `data-src`；`iframe` → `{tag:"embed", attrs:{href, src}}`
- 共享媒体、消息自身的 `media`、`/api/chats/{id}/media` 均不含 `role = 'article'`
- 前端：不使用 `innerHTML` / `dangerouslySetInnerHTML`；一切链接经 `safeHref`；文章节点只按白名单生成元素，从不复制存档里的属性
- 阅读页：桌面覆盖中栏、正文列最大宽 732px；≤600px 全屏；标题 26px 粗体，正文 17px 行高 1.6；系统返回键关闭（history）
- 每个 Go 任务结束：`go test ./...` 通过、`gofmt -l cmd internal` 无输出；每个前端任务结束：`cd web && npm test && npm run build` 通过
- 不改 `Dockerfile`、`.github/workflows/`、部署相关文件；不打 tag

## Review Focus

1. **同一条链接的 update 被重投**（入库后、推进 offset 前崩溃，或 Bot API 重发）→ 只有一个任务、一份快照 → Task 2 测试 `TestIngestQueuesTelegraphJob`（重复 `Ingest` 不建第二个任务）
2. **存档完成后用户编辑了这条链接消息** → 文章快照与其媒体文件原样保留，不被当孤儿删掉 → Task 3 测试 `TestEditKeepsArticleMedia`
3. **抓取中途在 WebUI 删除了链接消息** → 不残留 `articles` / 媒体行、不回执、不唤醒下载 → Task 6 测试 `TestWorkerMessageDeletedDuringFetch`；删除后任务即消失 → Task 2 测试 `TestDeleteMessageDropsTelegraphJob`
4. **两份快照引用同一张图**（同一篇文章发两次，或两篇文章共用图床图片）→ 只下载一次；删掉其中一条消息时文件保留，删掉最后一条才清理 → Task 3 测试 `TestArticleMediaSharedAcrossSnapshots`
5. **文章里的图片 URL 指向内网 / 回环地址，或经重定向跳到 `file://`** → 媒体立即 `failed`「地址不允许」，不进入 1/5/30 分钟重试，也不因此阻止 👌 → Task 4 测试 `TestWebSourceRefusesInternalAddresses`、`TestWebSourceRedirects`、`TestWebSourceThroughProcess`；Task 5 测试 `TestArticleSeenUntilMediaSettle`

---

## 文件结构

```
internal/telegraph/candidate.go          链接识别 Candidate（纯函数）
internal/telegraph/node.go               Telegraph Node 的 JSON 编解码
internal/telegraph/normalize.go          节点树规范化、MediaRef、WebKey、AttachMediaIDs、embed 推断
internal/telegraph/client.go             getPage 客户端（Page、APIError）
internal/telegraph/worker.go             串行抓取队列 Worker
internal/store/migrations/0004_telegraph.sql  telegraph_jobs、articles
internal/store/telegraph.go              任务队列读写
internal/store/articles.go               SaveArticle、GetArticle、ArticleSummary、RoleArticle
internal/store/ingest.go                 （改）Ingest 建任务；编辑保留文章媒体；DeleteMessage 清理快照
internal/store/query.go                  （改）MessageView.Article；消息媒体排除 article
internal/store/media.go                  （改）ReceiptInfo.Article
internal/downloader/downloader.go        （改）Permanent 错误
internal/downloader/websource.go         "web:" 数据源 + SSRF 拨号校验
internal/receipt/receipt.go              （改）Evaluate 并入文章任务
internal/config/config.go                （改）TELEGRAPH_API_URL
internal/collector/collector.go          （改）识别链接、唤醒 Telegraph 队列
internal/httpapi/server.go / read.go     （改）GET /api/messages/{id}/article
internal/app/app.go                      （改）装配；e2e
web/src/api/types.ts / client.ts         文章类型与 api.article
web/src/lib/router.ts                    article 路由、routeChatId、fromChat
web/src/state/store.ts                   ViewerItem / ViewerTarget 联合类型
web/src/components/viewer/MediaViewer.tsx  显式列表模式
web/src/components/article/ArticleCard.tsx     气泡内文章卡片
web/src/components/article/ArticleContent.tsx  节点树渲染
web/src/components/article/ArticleReader.tsx   阅读页
web/src/components/article/article.scss
web/src/components/message/MessageBubble.tsx、middle/MiddleColumn.tsx、left/ChatsPanel.tsx、App.tsx  （改）接入
README.md、docs/superpowers/specs/2026-10-04-tgarchive-design.md  （改）文档
```

---

### Task 1: telegraph —— 链接识别与节点树规范化

**Files:**
- Create: `internal/telegraph/candidate.go`、`internal/telegraph/node.go`、`internal/telegraph/normalize.go`
- Test: `internal/telegraph/candidate_test.go`、`internal/telegraph/normalize_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `func Candidate(text string) (path string, ok bool)`
  - `type Node struct { Text, Tag string; Attrs map[string]string; Children []Node }`（实现 `json.Marshaler` / `json.Unmarshaler`；`Tag == ""` 表示文本节点）
  - `const SiteURL = "https://telegra.ph"`；`const KindPhoto = "photo"`、`KindVideo = "video"`
  - `type MediaRef struct { URL, Kind string }`
  - `func WebKey(rawURL string) string` → `"web:" + hex(sha256(rawURL))`
  - `func Normalize(nodes []Node) ([]Node, []MediaRef)`（媒体按首次出现顺序去重）
  - `func AttachMediaIDs(nodes []Node, ids map[string]int64)`（就地给 img/video 写 `data-media-id`）
  - 包内（Task 6 使用）：`func absURL(raw string) (string, bool)`、`func strictURL(raw string) (string, bool)`、`func linkURL(raw string) (string, bool)`

- [ ] **Step 1: 写失败的测试**

`internal/telegraph/candidate_test.go`：

```go
package telegraph

import "testing"

func TestCandidate(t *testing.T) {
	ok := []struct{ in, want string }{
		{"https://telegra.ph/Sample-Page-12-15", "Sample-Page-12-15"},
		{"  telegra.ph/Sample-Page-12-15\n", "Sample-Page-12-15"},
		{"http://telegra.ph/Sample", "Sample"},
		{"HTTPS://TELEGRA.PH/Sample", "Sample"},
		{"https://www.telegra.ph/Sample", "Sample"},
		{"https://graph.org/Sample-10-05", "Sample-10-05"},
		{"https://telegra.ph/Sample/", "Sample"},
		{"https://telegra.ph/Sample?x=1#frag", "Sample"},
		{"https://telegra.ph/%E4%BD%A0%E5%A5%BD-10-05", "你好-10-05"},
	}
	for _, c := range ok {
		got, isOK := Candidate(c.in)
		if !isOK || got != c.want {
			t.Errorf("Candidate(%q) = %q, %v; want %q", c.in, got, isOK, c.want)
		}
	}
	bad := []string{
		"", "hello", "see https://telegra.ph/Sample", "https://telegra.ph/Sample https://telegra.ph/B",
		"https://telegra.ph/Sample x", "https://telegra.ph/", "https://telegra.ph", "https://telegra.ph/a/b",
		"https://telegra.ph/file/abc.jpg", "https://telegra.ph/api", "https://telegra.ph/edit", "https://telegra.ph/FILE",
		"https://telegra.ph/a%2Fb", "https://example.com/Sample", "https://telegra.ph.evil.com/Sample",
		"https://telegra.ph:8443/Sample", "https://user@telegra.ph/Sample", "ftp://telegra.ph/Sample", "https://t.me/durov/1",
	}
	for _, in := range bad {
		if got, isOK := Candidate(in); isOK {
			t.Errorf("Candidate(%q) = %q, true; want false", in, got)
		}
	}
}
```

`internal/telegraph/normalize_test.go`：

```go
package telegraph

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func parse(t *testing.T, s string) []Node {
	t.Helper()
	var nodes []Node
	if err := json.Unmarshal([]byte(s), &nodes); err != nil {
		t.Fatalf("unmarshal %s: %v", s, err)
	}
	return nodes
}

func dump(t *testing.T, nodes []Node) string {
	t.Helper()
	b, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNodeJSONRoundTrip(t *testing.T) {
	in := `["hi",{"tag":"p","attrs":{"class":"x","n":5},"children":["a",{"tag":"br"}]}]`
	got := dump(t, parse(t, in))
	if want := `["hi",{"tag":"p","attrs":{"class":"x"},"children":["a",{"tag":"br"}]}]`; got != want {
		t.Fatalf("round trip = %s, want %s", got, want)
	}
	var n []Node
	if err := json.Unmarshal([]byte(`[{"attrs":{}}]`), &n); err == nil {
		t.Fatal("element without tag must be rejected")
	}
}

func TestNormalizeTagsAndAttrs(t *testing.T) {
	in := parse(t, `[
		{"tag":"p","attrs":{"class":"lead","style":"x"},"children":["Hello ",{"tag":"strong","children":["world"]}]},
		{"tag":"div","children":[{"tag":"span","children":["unwrapped"]},{"tag":"script","children":["alert(1)"]}]},
		{"tag":"H3","attrs":{"id":"t"},"children":["Title"]},
		{"tag":"a","attrs":{"href":"/Other-Page","target":"_blank"},"children":["rel"]},
		{"tag":"a","attrs":{"href":"javascript:alert(1)"},"children":["js"]},
		{"tag":"a","attrs":{"href":"#anchor"},"children":["anchor"]},
		{"tag":"a","attrs":{"href":"mailto:a@b.c"},"children":["mail"]},
		{"tag":"hr","children":["ignored"]},
		{"tag":"blockquote","children":["q"]},{"tag":"aside","children":["pull"]},
		{"tag":"pre","children":["code"]},{"tag":"ul","children":[{"tag":"li","children":["one"]}]}
	]`)
	got, refs := Normalize(in)
	want := `[{"tag":"p","children":["Hello ",{"tag":"strong","children":["world"]}]},` +
		`"unwrapped","alert(1)",` +
		`{"tag":"h3","children":["Title"]},` +
		`{"tag":"a","attrs":{"href":"https://telegra.ph/Other-Page"},"children":["rel"]},` +
		`{"tag":"a","children":["js"]},{"tag":"a","children":["anchor"]},` +
		`{"tag":"a","attrs":{"href":"mailto:a@b.c"},"children":["mail"]},` +
		`{"tag":"hr"},{"tag":"blockquote","children":["q"]},{"tag":"aside","children":["pull"]},` +
		`{"tag":"pre","children":["code"]},{"tag":"ul","children":[{"tag":"li","children":["one"]}]}]`
	if s := dump(t, got); s != want {
		t.Fatalf("normalized =\n%s\nwant\n%s", s, want)
	}
	if len(refs) != 0 {
		t.Fatalf("refs = %+v", refs)
	}
}

func TestNormalizeMedia(t *testing.T) {
	in := parse(t, `[
		{"tag":"figure","children":[{"tag":"img","attrs":{"src":"/file/abc.jpg","alt":"x"}},{"tag":"figcaption","children":["cap"]}]},
		{"tag":"img","attrs":{"src":"https://cdn.example.com/a.png"}},
		{"tag":"img","attrs":{"src":"/file/abc.jpg"}},
		{"tag":"video","attrs":{"src":"//cdn.example.com/v.mp4","autoplay":"autoplay"}},
		{"tag":"img","attrs":{"src":"data:image/png;base64,AAAA"}},
		{"tag":"img"}
	]`)
	got, refs := Normalize(in)
	want := `[{"tag":"figure","children":[{"tag":"img","attrs":{"data-src":"https://telegra.ph/file/abc.jpg"}},{"tag":"figcaption","children":["cap"]}]},` +
		`{"tag":"img","attrs":{"data-src":"https://cdn.example.com/a.png"}},` +
		`{"tag":"img","attrs":{"data-src":"https://telegra.ph/file/abc.jpg"}},` +
		`{"tag":"video","attrs":{"data-src":"https://cdn.example.com/v.mp4"}}]`
	if s := dump(t, got); s != want {
		t.Fatalf("normalized =\n%s\nwant\n%s", s, want)
	}
	wantRefs := []MediaRef{
		{URL: "https://telegra.ph/file/abc.jpg", Kind: KindPhoto},
		{URL: "https://cdn.example.com/a.png", Kind: KindPhoto},
		{URL: "https://cdn.example.com/v.mp4", Kind: KindVideo},
	}
	if !reflect.DeepEqual(refs, wantRefs) {
		t.Fatalf("refs = %+v", refs)
	}
	AttachMediaIDs(got, map[string]int64{"https://telegra.ph/file/abc.jpg": 7, "https://cdn.example.com/v.mp4": 9})
	s := dump(t, got)
	for _, frag := range []string{
		`{"tag":"img","attrs":{"data-media-id":"7","data-src":"https://telegra.ph/file/abc.jpg"}}`,
		`{"tag":"img","attrs":{"data-src":"https://cdn.example.com/a.png"}}`,
		`{"tag":"video","attrs":{"data-media-id":"9","data-src":"https://cdn.example.com/v.mp4"}}`,
	} {
		if !strings.Contains(s, frag) {
			t.Fatalf("missing %s in %s", frag, s)
		}
	}
}

func TestNormalizeEmbeds(t *testing.T) {
	cases := []struct{ src, href string }{
		{"/embed/youtube?url=https%3A%2F%2Fwww.youtube.com%2Fwatch%3Fv%3DdQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		{"https://www.youtube.com/embed/dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		{"https://player.vimeo.com/video/76979871", "https://vimeo.com/76979871"},
		{"https://platform.twitter.com/embed/Tweet.html?id=20", "https://twitter.com/i/status/20"},
		{"/embed/youtube?url=javascript%3Aalert(1)", "https://telegra.ph/embed/youtube?url=javascript%3Aalert(1)"},
		{"https://maps.example.com/embed?q=1", "https://maps.example.com/embed?q=1"},
	}
	for _, c := range cases {
		in := []Node{{Tag: "figure", Children: []Node{{Tag: "iframe", Attrs: map[string]string{"src": c.src, "width": "640"}}}}}
		got, refs := Normalize(in)
		emb := got[0].Children[0]
		if emb.Tag != "embed" || emb.Attrs["href"] != c.href || len(emb.Attrs) != 2 || len(refs) != 0 {
			t.Errorf("iframe %s -> %+v (refs %v); want href %s", c.src, emb, refs, c.href)
		}
	}
	if got, _ := Normalize([]Node{{Tag: "iframe", Attrs: map[string]string{"src": "javascript:alert(1)"}}}); len(got) != 0 {
		t.Fatalf("javascript iframe kept: %+v", got)
	}
}

func TestNormalizeDepthLimit(t *testing.T) {
	n := Node{Text: "deep"}
	for i := 0; i < 200; i++ {
		n = Node{Tag: "b", Children: []Node{n}}
	}
	got, _ := Normalize([]Node{n})
	if strings.Contains(dump(t, got), "deep") {
		t.Fatal("text below maxDepth must be dropped")
	}
}

func TestWebKey(t *testing.T) {
	k := WebKey("https://telegra.ph/file/abc.jpg")
	if !strings.HasPrefix(k, "web:") || len(k) != 4+64 || k != WebKey("https://telegra.ph/file/abc.jpg") || k == WebKey("https://telegra.ph/file/abd.jpg") {
		t.Fatalf("WebKey = %q", k)
	}
}
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test ./internal/telegraph/`
Expected: FAIL，编译错误 `undefined: Candidate`、`undefined: Node`、`undefined: Normalize` 等

- [ ] **Step 3: 实现**

`internal/telegraph/candidate.go`：

```go
// Package telegraph archives Telegraph (telegra.ph / graph.org) articles: link recognition,
// content normalization, the getPage client and the serial fetch worker.
package telegraph

import (
	"net/url"
	"strings"
	"unicode"
)

// reserved are first path segments of telegra.ph that are site paths, not articles.
var reserved = map[string]bool{
	"file": true, "api": true, "edit": true, "embed": true, "upload": true, "auth": true,
	"js": true, "css": true, "images": true, "img": true,
}

// Candidate reports whether text, trimmed, is exactly one Telegraph article URL and returns the
// article path (one segment, URL-decoded, no leading slash). The scheme may be omitted; host is
// telegra.ph or graph.org (any case, optional www.); query and fragment are ignored.
func Candidate(text string) (string, bool) {
	s := strings.TrimSpace(text)
	if s == "" || strings.IndexFunc(s, unicode.IsSpace) >= 0 {
		return "", false
	}
	rest := s
	switch lower := strings.ToLower(s); {
	case strings.HasPrefix(lower, "https://"):
		rest = s[len("https://"):]
	case strings.HasPrefix(lower, "http://"):
		rest = s[len("http://"):]
	case strings.Contains(lower, "://"):
		return "", false
	}
	u, err := url.Parse("https://" + rest)
	if err != nil || u.User != nil || u.Port() != "" {
		return "", false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host != "telegra.ph" && host != "graph.org" {
		return "", false
	}
	p := strings.Trim(u.Path, "/")
	if p == "" || strings.Contains(p, "/") || reserved[strings.ToLower(p)] {
		return "", false
	}
	return p, true
}
```

`internal/telegraph/node.go`：

```go
package telegraph

import (
	"encoding/json"
	"errors"
)

// Node is a Telegraph content node: a text string (Tag == "") or an element.
// It marshals back to Telegraph's own shape: "text" or {"tag","attrs","children"}.
type Node struct {
	Text     string
	Tag      string
	Attrs    map[string]string
	Children []Node
}

type element struct {
	Tag      string            `json:"tag"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Children []Node            `json:"children,omitempty"`
}

func (n Node) MarshalJSON() ([]byte, error) {
	if n.Tag == "" {
		return json.Marshal(n.Text)
	}
	return json.Marshal(element{Tag: n.Tag, Attrs: n.Attrs, Children: n.Children})
}

func (n *Node) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*n = Node{Text: s}
		return nil
	}
	var e struct {
		Tag      string         `json:"tag"`
		Attrs    map[string]any `json:"attrs"`
		Children []Node         `json:"children"`
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return err
	}
	if e.Tag == "" {
		return errors.New("telegraph: element without tag")
	}
	*n = Node{Tag: e.Tag, Children: e.Children}
	for k, v := range e.Attrs {
		if s, ok := v.(string); ok {
			if n.Attrs == nil {
				n.Attrs = map[string]string{}
			}
			n.Attrs[k] = s
		}
	}
	return nil
}
```

`internal/telegraph/normalize.go`：

```go
package telegraph

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// SiteURL resolves relative URLs found in article content (/file/...).
const SiteURL = "https://telegra.ph"

const (
	KindPhoto = "photo"
	KindVideo = "video"
)

// maxDepth bounds recursion on hostile nesting; deeper elements are dropped.
const maxDepth = 64

// MediaRef is one image or video an article references, in first-appearance order.
type MediaRef struct {
	URL  string // absolute http(s) URL as found in the article
	Kind string // KindPhoto or KindVideo
}

// WebKey is the media dedupe_key for a web URL: "web:" + hex(sha256(url)).
func WebKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return "web:" + hex.EncodeToString(sum[:])
}

var allowedTags = map[string]bool{
	"a": true, "aside": true, "b": true, "blockquote": true, "br": true, "code": true, "em": true,
	"figcaption": true, "figure": true, "h3": true, "h4": true, "hr": true, "i": true, "iframe": true,
	"img": true, "li": true, "ol": true, "p": true, "pre": true, "s": true, "strong": true, "u": true,
	"ul": true, "video": true,
}

var base, _ = url.Parse(SiteURL + "/")

// Normalize cleans a Telegraph node tree for storage (spec §5): unknown tags are unwrapped,
// only a.href / img.src / video.src / iframe.src survive, relative URLs resolve against
// telegra.ph, img/video carry data-src (absolute URL) instead of src, and iframe becomes
// {tag:"embed", attrs:{href, src}}. It also returns the media the tree references, deduplicated.
func Normalize(nodes []Node) ([]Node, []MediaRef) {
	n := &normalizer{seen: map[string]bool{}}
	return n.list(nodes, 0), n.refs
}

type normalizer struct {
	refs []MediaRef
	seen map[string]bool
}

func (n *normalizer) list(in []Node, depth int) []Node {
	out := []Node{}
	for _, c := range in {
		out = append(out, n.node(c, depth)...)
	}
	return out
}

func (n *normalizer) node(in Node, depth int) []Node {
	if in.Tag == "" {
		if in.Text == "" {
			return nil
		}
		return []Node{{Text: in.Text}}
	}
	if depth >= maxDepth {
		return nil
	}
	tag := strings.ToLower(in.Tag)
	if !allowedTags[tag] {
		return n.list(in.Children, depth+1)
	}
	switch tag {
	case "img", "video":
		src, ok := absURL(in.Attrs["src"])
		if !ok {
			return nil
		}
		kind := KindPhoto
		if tag == "video" {
			kind = KindVideo
		}
		if !n.seen[src] {
			n.seen[src] = true
			n.refs = append(n.refs, MediaRef{URL: src, Kind: kind})
		}
		return []Node{{Tag: tag, Attrs: map[string]string{"data-src": src}}}
	case "iframe":
		src, ok := absURL(in.Attrs["src"])
		if !ok {
			return nil
		}
		return []Node{{Tag: "embed", Attrs: map[string]string{"href": embedHref(src), "src": src}}}
	case "br", "hr":
		return []Node{{Tag: tag}}
	}
	out := Node{Tag: tag, Children: n.list(in.Children, depth+1)}
	if tag == "a" {
		if href, ok := linkURL(in.Attrs["href"]); ok {
			out.Attrs = map[string]string{"href": href}
		}
	}
	return []Node{out}
}

// AttachMediaIDs sets data-media-id on every img/video whose data-src has an id in ids.
func AttachMediaIDs(nodes []Node, ids map[string]int64) {
	for i := range nodes {
		nd := &nodes[i]
		if (nd.Tag == "img" || nd.Tag == "video") && nd.Attrs != nil {
			if id, ok := ids[nd.Attrs["data-src"]]; ok {
				nd.Attrs["data-media-id"] = strconv.FormatInt(id, 10)
			}
		}
		AttachMediaIDs(nd.Children, ids)
	}
}

// absURL resolves raw against telegra.ph and accepts only absolute http(s) URLs with a host.
func absURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	r := base.ResolveReference(u)
	if (r.Scheme != "http" && r.Scheme != "https") || r.Host == "" {
		return "", false
	}
	return r.String(), true
}

// strictURL accepts only an already-absolute http(s) URL (no resolution against telegra.ph).
func strictURL(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return u.String(), true
}

// linkURL keeps http(s), mailto: and tg: links; in-page anchors and other schemes are dropped.
func linkURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "mailto", "tg":
		return u.String(), true
	}
	return absURL(raw)
}

var (
	digits    = regexp.MustCompile(`^[0-9]+$`)
	youtubeID = regexp.MustCompile(`^[A-Za-z0-9_-]{6,20}$`)
)

// embedHref guesses the original page of an embed: Telegraph's own /embed/<kind>?url=<page>,
// YouTube embed/<id>, Vimeo video/<id>, Twitter ?id=<id>; anything else keeps src.
func embedHref(src string) string {
	u, err := url.Parse(src)
	if err != nil {
		return src
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch host {
	case "telegra.ph", "graph.org":
		if strings.HasPrefix(u.Path, "/embed/") {
			if inner, ok := strictURL(u.Query().Get("url")); ok {
				return inner
			}
		}
	case "youtube.com", "youtube-nocookie.com":
		if id, ok := strings.CutPrefix(u.Path, "/embed/"); ok && youtubeID.MatchString(id) {
			return "https://www.youtube.com/watch?v=" + id
		}
	case "player.vimeo.com":
		if id, ok := strings.CutPrefix(u.Path, "/video/"); ok && digits.MatchString(id) {
			return "https://vimeo.com/" + id
		}
	case "platform.twitter.com":
		if id := u.Query().Get("id"); digits.MatchString(id) {
			return "https://twitter.com/i/status/" + id
		}
	}
	return src
}
```

- [ ] **Step 4: 运行，确认通过**

Run: `go test ./internal/telegraph/ && gofmt -l internal/telegraph`
Expected: `ok  tgarchive/internal/telegraph`，gofmt 无输出

- [ ] **Step 5: 全量门禁**

Run: `go test ./... && gofmt -l cmd internal`
Expected: 全部 `ok`，gofmt 无输出

- [ ] **Step 6: Commit**

```bash
git add internal/telegraph
git commit -m "feat(telegraph): link recognition and content normalization"
```

---

### Task 2: store —— 迁移 0004 与 Telegraph 任务队列

**Files:**
- Create: `internal/store/migrations/0004_telegraph.sql`、`internal/store/telegraph.go`
- Modify: `internal/store/ingest.go`（`IngestInput` / `IngestResult`、新建分支、`DeleteMessage`）、`internal/store/store_test.go:40`
- Test: `internal/store/telegraph_test.go`

**Interfaces:**
- Consumes: 既有 `store.Ingest`、`withTx`、`affected`、`scanner`、`ErrNotFound`
- Produces:
  - 常量 `TelegraphQueued = "queued"`、`TelegraphFetching = "fetching"`、`TelegraphFetched = "fetched"`、`TelegraphFailed = "failed"`
  - `type TelegraphJob struct { ID, MessageID, ChatID int64; Path, State string; Attempts int; Error string; CreatedAt, UpdatedAt int64 }`
  - `func (s *Store) GetTelegraphJob(ctx, messageID int64) (*TelegraphJob, error)`（消息已删 → `ErrNotFound`）
  - `func (s *Store) ClaimNextTelegraphJob(ctx, now int64) (*TelegraphJob, error)`（空队列 → `ErrNotFound`）
  - `func (s *Store) RequeueFetchingTelegraphJobs(ctx, now int64) (int64, error)`
  - `func (s *Store) SetTelegraphJobAttempts(ctx, id int64, attempts int, errMsg string, now int64) error`
  - `func (s *Store) FinishTelegraphJob(ctx, id int64, state, errMsg string, now int64) error`（任务已不存在 → `ErrNotFound`）
  - `IngestInput.TelegraphPath string`；`IngestResult.TelegraphQueued bool`

- [ ] **Step 1: 写失败的测试**

`internal/store/store_test.go` 的 `TestMigrateIdempotent` 把版本号改为 4：

```go
		if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 4 {
```

`internal/store/telegraph_test.go`：

```go
package store

import (
	"errors"
	"testing"
)

func ingestLink(t *testing.T, s *Store, bot, tgID int64, path string) *IngestResult {
	t.Helper()
	res, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: alice, Msg: textMsg(tgID, "https://telegra.ph/"+path), Now: 5000, TelegraphPath: path})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestIngestQueuesTelegraphJob(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res := ingestLink(t, s, bot, 1, "Sample-10-05")
	if !res.Created || !res.TelegraphQueued {
		t.Fatalf("res = %+v", res)
	}
	j, err := s.GetTelegraphJob(ctx, res.MessageID)
	if err != nil || j.Path != "Sample-10-05" || j.State != TelegraphQueued || j.ChatID != res.ChatID || j.CreatedAt != 5000 {
		t.Fatalf("job = %+v, %v", j, err)
	}
	// Re-delivery / edit of the same message: no second job.
	again := ingestLink(t, s, bot, 1, "Sample-10-05")
	if again.Created || again.TelegraphQueued {
		t.Fatalf("edit res = %+v", again)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM telegraph_jobs").Scan(&n)
	if n != 1 {
		t.Fatalf("jobs = %d", n)
	}
	plain := ingest(t, s, bot, textMsg(2, "hello"))
	if plain.TelegraphQueued {
		t.Fatal("a message without TelegraphPath must not queue")
	}
	if _, err := s.GetTelegraphJob(ctx, plain.MessageID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("job for plain message: %v", err)
	}
}

func TestTelegraphJobQueue(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	a := ingestLink(t, s, bot, 1, "A")
	b := ingestLink(t, s, bot, 2, "B")

	j, err := s.ClaimNextTelegraphJob(ctx, 6000)
	if err != nil || j.MessageID != a.MessageID || j.State != TelegraphFetching || j.ChatID != a.ChatID {
		t.Fatalf("claim = %+v, %v", j, err)
	}
	if err := s.SetTelegraphJobAttempts(ctx, j.ID, 2, "网络错误", 6001); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetTelegraphJob(ctx, a.MessageID); got.Attempts != 2 || got.Error != "网络错误" || got.State != TelegraphFetching || got.UpdatedAt != 6001 {
		t.Fatalf("after attempts = %+v", got)
	}
	// Restart: the interrupted job goes back to the queue and is claimed before B.
	if n, err := s.RequeueFetchingTelegraphJobs(ctx, 7000); err != nil || n != 1 {
		t.Fatalf("requeue = %d, %v", n, err)
	}
	j, _ = s.ClaimNextTelegraphJob(ctx, 7001)
	if j.MessageID != a.MessageID || j.Attempts != 2 {
		t.Fatalf("reclaim = %+v", j)
	}
	if err := s.FinishTelegraphJob(ctx, j.ID, TelegraphFailed, "文章不存在", 7002); err != nil {
		t.Fatal(err)
	}
	j2, _ := s.ClaimNextTelegraphJob(ctx, 7003)
	if j2.MessageID != b.MessageID {
		t.Fatalf("second claim = %+v", j2)
	}
	if _, err := s.ClaimNextTelegraphJob(ctx, 7004); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty queue: %v", err)
	}
	if got, _ := s.GetTelegraphJob(ctx, a.MessageID); got.State != TelegraphFailed || got.Error != "文章不存在" {
		t.Fatalf("finished = %+v", got)
	}
}

func TestDeleteMessageDropsTelegraphJob(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res := ingestLink(t, s, bot, 1, "A")
	j, _ := s.ClaimNextTelegraphJob(ctx, 6000)
	if _, _, err := s.DeleteMessage(ctx, res.MessageID, 6001); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTelegraphJob(ctx, res.MessageID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("job after delete: %v", err)
	}
	if err := s.FinishTelegraphJob(ctx, j.ID, TelegraphFetched, "", 6002); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finish after delete = %v, want ErrNotFound", err)
	}
	if n, _ := s.RequeueFetchingTelegraphJobs(ctx, 6003); n != 0 {
		t.Fatalf("requeued %d deleted jobs", n)
	}
}

func TestPurgeBotCascadesTelegraph(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	ingestLink(t, s, bot, 1, "A")
	if _, err := s.PurgeBot(ctx, bot); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM telegraph_jobs").Scan(&n)
	if n != 0 {
		t.Fatalf("jobs after purge = %d", n)
	}
}
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test ./internal/store/`
Expected: FAIL，编译错误 `unknown field TelegraphPath in struct literal`、`undefined: TelegraphQueued` 等

- [ ] **Step 3: 迁移**

`internal/store/migrations/0004_telegraph.sql`（`articles` 也在此建，Task 3 使用）：

```sql
CREATE TABLE telegraph_jobs (
  id          INTEGER PRIMARY KEY,
  message_id  INTEGER NOT NULL UNIQUE REFERENCES messages(id) ON DELETE CASCADE,
  path        TEXT    NOT NULL,
  state       TEXT    NOT NULL CHECK (state IN ('queued','fetching','fetched','failed')),
  attempts    INTEGER NOT NULL DEFAULT 0,
  error       TEXT    NOT NULL DEFAULT '',
  receipt     TEXT    NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE INDEX telegraph_jobs_state ON telegraph_jobs (state, id);

CREATE TABLE articles (
  message_id     INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
  path           TEXT    NOT NULL,
  url            TEXT    NOT NULL,
  title          TEXT    NOT NULL,
  description    TEXT    NOT NULL DEFAULT '',
  author_name    TEXT    NOT NULL DEFAULT '',
  author_url     TEXT    NOT NULL DEFAULT '',
  image_media_id INTEGER,
  views          INTEGER NOT NULL DEFAULT 0,
  content        TEXT    NOT NULL,
  fetched_at     INTEGER NOT NULL
);
```

- [ ] **Step 4: 任务队列**

`internal/store/telegraph.go`：

```go
package store

import (
	"context"
	"database/sql"
	"errors"
)

const (
	TelegraphQueued   = "queued"
	TelegraphFetching = "fetching"
	TelegraphFetched  = "fetched"
	TelegraphFailed   = "failed"
)

// TelegraphJob archives the Telegraph article a link message points to (one job per message).
type TelegraphJob struct {
	ID        int64
	MessageID int64
	ChatID    int64
	Path      string
	State     string
	Attempts  int
	Error     string
	CreatedAt int64
	UpdatedAt int64
}

const telegraphJobCols = "j.id, j.message_id, m.chat_id, j.path, j.state, j.attempts, j.error, j.created_at, j.updated_at"

func scanTelegraphJob(r scanner) (*TelegraphJob, error) {
	var j TelegraphJob
	err := r.Scan(&j.ID, &j.MessageID, &j.ChatID, &j.Path, &j.State, &j.Attempts, &j.Error, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// GetTelegraphJob returns the job of a non-deleted link message.
func (s *Store) GetTelegraphJob(ctx context.Context, messageID int64) (*TelegraphJob, error) {
	return scanTelegraphJob(s.db.QueryRowContext(ctx, `SELECT `+telegraphJobCols+`
		FROM telegraph_jobs j JOIN messages m ON m.id = j.message_id AND m.deleted_at = 0 WHERE j.message_id = ?`, messageID))
}

// ClaimNextTelegraphJob moves the oldest queued job to 'fetching' and returns it (ErrNotFound when idle).
func (s *Store) ClaimNextTelegraphJob(ctx context.Context, now int64) (*TelegraphJob, error) {
	var job *TelegraphJob
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		j, err := scanTelegraphJob(tx.QueryRowContext(ctx, `SELECT `+telegraphJobCols+`
			FROM telegraph_jobs j JOIN messages m ON m.id = j.message_id AND m.deleted_at = 0
			WHERE j.state = 'queued' ORDER BY j.id LIMIT 1`))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE telegraph_jobs SET state = 'fetching', updated_at = ? WHERE id = ?", now, j.ID); err != nil {
			return err
		}
		j.State, j.UpdatedAt = TelegraphFetching, now
		job = j
		return nil
	})
	return job, err
}

// RequeueFetchingTelegraphJobs puts jobs interrupted by a restart back into the queue.
func (s *Store) RequeueFetchingTelegraphJobs(ctx context.Context, now int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE telegraph_jobs SET state = 'queued', updated_at = ? WHERE state = 'fetching'", now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetTelegraphJobAttempts records a transient failure; the job stays 'fetching'.
func (s *Store) SetTelegraphJobAttempts(ctx context.Context, id int64, attempts int, errMsg string, now int64) error {
	return affected(s.db.ExecContext(ctx, "UPDATE telegraph_jobs SET attempts = ?, error = ?, updated_at = ? WHERE id = ?", attempts, errMsg, now, id))
}

// FinishTelegraphJob sets a final state; ErrNotFound when the job is gone (message deleted).
func (s *Store) FinishTelegraphJob(ctx context.Context, id int64, state, errMsg string, now int64) error {
	return affected(s.db.ExecContext(ctx, "UPDATE telegraph_jobs SET state = ?, error = ?, updated_at = ? WHERE id = ?", state, errMsg, now, id))
}
```

- [ ] **Step 5: `Ingest` 建任务、`DeleteMessage` 清理**

`internal/store/ingest.go`，`IngestInput` 与 `IngestResult` 改为：

```go
type IngestInput struct {
	BotID  int64
	Sender model.Sender
	Msg    *model.Message
	Offset int64 // when > 0, bots.update_offset advances to it in the same transaction
	Now    int64
	// TelegraphPath, when set, queues a Telegraph job for the message in the same transaction,
	// but only if the message is newly created (an edit never queues a second snapshot).
	TelegraphPath string
}

type IngestResult struct {
	MessageID       int64
	ChatID          int64
	ChatCreated     bool
	Created         bool     // false when an existing message was updated (edit)
	OrphanPaths     []string // media files no longer referenced; caller deletes them
	TelegraphQueued bool     // a Telegraph job was created for this message
}
```

在 `Ingest` 的 `case errors.Is(err, sql.ErrNoRows):` 分支里，`res.Created = true` 之后加：

```go
			res.Created = true
			if in.TelegraphPath != "" {
				if _, err := tx.ExecContext(ctx, `INSERT INTO telegraph_jobs (message_id, path, state, created_at, updated_at)
					VALUES (?, ?, 'queued', ?, ?)`, res.MessageID, in.TelegraphPath, in.Now, in.Now); err != nil {
					return err
				}
				res.TelegraphQueued = true
			}
```

`DeleteMessage` 事务内，把原来的

```go
		if _, err := tx.ExecContext(ctx, "DELETE FROM message_media WHERE message_id = ?", id); err != nil {
			return err
		}
```

替换为：

```go
		// Unlinking the media (article media included) lets collectOrphans drop unshared files; the
		// Telegraph snapshot and its job go with the message (the FK cascade only fires on a hard delete).
		for _, q := range []string{
			"DELETE FROM message_media WHERE message_id = ?",
			"DELETE FROM articles WHERE message_id = ?",
			"DELETE FROM telegraph_jobs WHERE message_id = ?",
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
```

- [ ] **Step 6: 运行，确认通过**

Run: `go test ./internal/store/`
Expected: `ok  tgarchive/internal/store`

- [ ] **Step 7: 全量门禁**

Run: `go test ./... && gofmt -l cmd internal`
Expected: 全部 `ok`，gofmt 无输出

- [ ] **Step 8: Commit**

```bash
git add internal/store
git commit -m "feat(store): telegraph_jobs queue, created atomically with the link message"
```

---

### Task 3: store —— 文章快照、文章接口数据与消息卡片摘要

**Files:**
- Create: `internal/store/articles.go`
- Modify: `internal/store/ingest.go`（编辑路径保留文章媒体）、`internal/store/query.go`（`MessageView.Article`、`hydrate`）
- Test: `internal/store/articles_test.go`

**Interfaces:**
- Consumes: Task 2 的 `ClaimNextTelegraphJob`、`TelegraphJob`、`ingestLink`（测试辅助，定义在 `telegraph_test.go`）；既有 `upsertMedia`、`collectOrphans`
- Produces:
  - `const RoleArticle = "article"`
  - `type ArticleMediaInput struct { DedupeKey, URL, Kind string }`
  - `type SaveArticleInput struct { JobID, MessageID int64; Path, URL, Title, Description, AuthorName, AuthorURL, ImageURL string; Views int64; Media []ArticleMediaInput; Render func(ids map[string]int64) (string, error); Now int64 }`
  - `func (s *Store) SaveArticle(ctx, in SaveArticleInput) error`（消息已删 → `ErrNotFound`，整个事务回滚）
  - `type ArticleMediaView struct { ID int64; Kind, State string; Width, Height, Duration int; Mime string }`（JSON：`id kind state width height duration mime`）
  - `type ArticleView struct { URL, Title, Description, AuthorName, AuthorURL string; Views, FetchedAt int64; Content json.RawMessage; Media []ArticleMediaView }`（JSON：`url title description author_name author_url views fetched_at content media`）
  - `func (s *Store) GetArticle(ctx, messageID int64) (*ArticleView, error)`
  - `type ArticleSummary struct { State, Error, Title, Description, AuthorName string; ImageMediaID int64; URL string }`（JSON：`state error title description author_name image_media_id(omitempty) url`）
  - `MessageView.Article *ArticleSummary`（JSON `article`，无任务时省略）

- [ ] **Step 1: 写失败的测试**

`internal/store/articles_test.go`：

```go
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	imgA = "https://telegra.ph/file/a.jpg"
	vidB = "https://cdn.example.com/b.mp4"
)

// saveArticle claims the next job (which must belong to msgID) and stores an article whose
// content lists the media ids it was given.
func saveArticle(t *testing.T, s *Store, msgID int64, media ...ArticleMediaInput) {
	t.Helper()
	j, err := s.ClaimNextTelegraphJob(ctx, 6000)
	if err != nil || j.MessageID != msgID {
		t.Fatalf("claim = %+v, %v", j, err)
	}
	err = s.SaveArticle(ctx, SaveArticleInput{
		JobID: j.ID, MessageID: msgID, Path: j.Path, URL: "https://telegra.ph/" + j.Path, Title: "Title " + j.Path,
		Description: "desc", AuthorName: "Author", AuthorURL: "https://t.me/author", ImageURL: imgA, Views: 12, Media: media,
		Render: func(ids map[string]int64) (string, error) {
			b, err := json.Marshal(ids)
			return fmt.Sprintf(`[{"tag":"p","children":[%q]}]`, string(b)), err
		},
		Now: 6001,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func webMedia(url, kind string) ArticleMediaInput {
	return ArticleMediaInput{DedupeKey: "web:" + url, URL: url, Kind: kind}
}

func TestSaveAndGetArticle(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res := ingestLink(t, s, bot, 1, "Sample")

	v, _ := s.GetMessageView(ctx, res.MessageID)
	if v.Article == nil || v.Article.State != TelegraphQueued || v.Article.URL != "https://telegra.ph/Sample" || v.Article.Title != "" {
		t.Fatalf("queued summary = %+v", v.Article)
	}
	if _, err := s.GetArticle(ctx, res.MessageID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("article before fetch: %v", err)
	}

	saveArticle(t, s, res.MessageID, webMedia(imgA, "photo"), webMedia(vidB, "video"))

	a, err := s.GetArticle(ctx, res.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Title != "Title Sample" || a.AuthorURL != "https://t.me/author" || a.Views != 12 || a.FetchedAt != 6001 || len(a.Media) != 2 {
		t.Fatalf("article = %+v", a)
	}
	if a.Media[0].Kind != "photo" || a.Media[1].Kind != "video" || a.Media[0].State != StatePending {
		t.Fatalf("article media = %+v", a.Media)
	}
	if !strings.Contains(string(a.Content), fmt.Sprintf(`\"%s\":%d`, imgA, a.Media[0].ID)) {
		t.Fatalf("content not rendered with ids: %s", a.Content)
	}
	if j, _ := s.GetTelegraphJob(ctx, res.MessageID); j.State != TelegraphFetched {
		t.Fatalf("job = %+v", j)
	}

	v, _ = s.GetMessageView(ctx, res.MessageID)
	if len(v.Media) != 0 {
		t.Fatalf("article media leaked into the message's own media: %+v", v.Media)
	}
	if v.Article.State != TelegraphFetched || v.Article.Title != "Title Sample" || v.Article.AuthorName != "Author" || v.Article.ImageMediaID != 0 {
		t.Fatalf("summary before cover download = %+v", v.Article)
	}
	if _, err := s.MarkMediaDone(ctx, a.Media[0].ID, "web/2026/10/x.jpg", 3); err != nil {
		t.Fatal(err)
	}
	v, _ = s.GetMessageView(ctx, res.MessageID)
	if v.Article.ImageMediaID != a.Media[0].ID {
		t.Fatalf("summary after cover download = %+v", v.Article)
	}
	a, _ = s.GetArticle(ctx, res.MessageID)
	if a.Media[0].Mime != "image/jpeg" {
		t.Fatalf("mime from extension = %q", a.Media[0].Mime)
	}
	shared, _ := s.ListChatMedia(ctx, res.ChatID, "media", 0, 50)
	if len(shared) != 0 {
		t.Fatalf("article media in shared media: %+v", shared)
	}
	if ids, _ := s.MessagesForMedia(ctx, a.Media[1].ID); len(ids) != 1 || ids[0] != res.MessageID {
		t.Fatalf("MessagesForMedia = %v", ids)
	}
}

func TestArticleMediaSharedAcrossSnapshots(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	r1 := ingestLink(t, s, bot, 1, "A")
	r2 := ingestLink(t, s, bot, 2, "A")
	saveArticle(t, s, r1.MessageID, webMedia(imgA, "photo"))
	saveArticle(t, s, r2.MessageID, webMedia(imgA, "photo"))
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM media").Scan(&n)
	if n != 1 {
		t.Fatalf("media rows = %d, want 1 (deduped)", n)
	}
	a, _ := s.GetArticle(ctx, r1.MessageID)
	s.MarkMediaDone(ctx, a.Media[0].ID, "web/2026/10/a.jpg", 3)

	if _, orphans, err := s.DeleteMessage(ctx, r1.MessageID, 7000); err != nil || len(orphans) != 0 {
		t.Fatalf("delete first snapshot: orphans %v, %v", orphans, err)
	}
	if _, err := s.GetArticle(ctx, r1.MessageID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("article of deleted message: %v", err)
	}
	var arts int
	s.db.QueryRow("SELECT COUNT(*) FROM articles").Scan(&arts)
	if arts != 1 {
		t.Fatalf("articles rows = %d", arts)
	}
	_, orphans, err := s.DeleteMessage(ctx, r2.MessageID, 7001)
	if err != nil || len(orphans) != 1 || orphans[0] != "web/2026/10/a.jpg" {
		t.Fatalf("delete last snapshot: orphans %v, %v", orphans, err)
	}
}

func TestEditKeepsArticleMedia(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res := ingestLink(t, s, bot, 1, "A")
	saveArticle(t, s, res.MessageID, webMedia(imgA, "photo"))
	edited, err := s.Ingest(ctx, IngestInput{BotID: bot, Sender: alice, Msg: textMsg(1, "https://telegra.ph/A "), Now: 8000})
	if err != nil || edited.Created || len(edited.OrphanPaths) != 0 {
		t.Fatalf("edit = %+v, %v", edited, err)
	}
	a, _ := s.GetArticle(ctx, res.MessageID)
	if len(a.Media) != 1 {
		t.Fatalf("article media after edit = %+v", a.Media)
	}
}

func TestSaveArticleAfterDelete(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	res := ingestLink(t, s, bot, 1, "A")
	j, _ := s.ClaimNextTelegraphJob(ctx, 6000)
	s.DeleteMessage(ctx, res.MessageID, 6001)
	err := s.SaveArticle(ctx, SaveArticleInput{JobID: j.ID, MessageID: res.MessageID, Path: "A", Media: []ArticleMediaInput{webMedia(imgA, "photo")},
		Render: func(map[string]int64) (string, error) { return "[]", nil }, Now: 6002})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SaveArticle after delete = %v", err)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM media").Scan(&n)
	if n != 0 {
		t.Fatalf("media rows = %d after a rolled-back save", n)
	}
}
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test ./internal/store/`
Expected: FAIL，编译错误 `undefined: ArticleMediaInput`、`undefined: SaveArticleInput`、`v.Article undefined` 等

- [ ] **Step 3: 快照读写**

`internal/store/articles.go`：

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime"
	"net/url"
	"path/filepath"

	"tgarchive/internal/model"
)

// RoleArticle links a media row to the link message whose Telegraph snapshot references it.
// Such media never show as the message's own media (bubble, shared media, chat media).
const RoleArticle = "article"

type ArticleMediaInput struct {
	DedupeKey string // "web:" + hex(sha256(URL))
	URL       string // absolute source URL; stored as media.source_ref
	Kind      string // photo / video
}

type SaveArticleInput struct {
	JobID       int64
	MessageID   int64
	Path        string
	URL         string
	Title       string
	Description string
	AuthorName  string
	AuthorURL   string
	ImageURL    string // cover (og image); "" for none, otherwise also listed in Media
	Views       int64
	Media       []ArticleMediaInput // in content order, deduplicated by URL
	// Render returns the stored content JSON once every Media URL has its media id.
	Render func(ids map[string]int64) (string, error)
	Now    int64
}

// SaveArticle stores a fetched article in one transaction: one media row per URL (deduped by
// dedupe_key, linked to the message with role 'article'), the articles row (replacing an earlier
// one) and the job moved to 'fetched'. ErrNotFound when the message was deleted meanwhile.
func (s *Store) SaveArticle(ctx context.Context, in SaveArticleInput) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var one int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM messages WHERE id = ? AND deleted_at = 0", in.MessageID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		ids := map[string]int64{}
		for i, m := range in.Media {
			id, err := upsertMedia(ctx, tx, 0, model.Media{DedupeKey: m.DedupeKey, SourceRef: m.URL, Kind: m.Kind})
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO message_media (message_id, media_id, role, position) VALUES (?, ?, ?, ?)",
				in.MessageID, id, RoleArticle, i); err != nil {
				return err
			}
			ids[m.URL] = id
		}
		content, err := in.Render(ids)
		if err != nil {
			return err
		}
		var image any // NULL when there is no cover
		if id, ok := ids[in.ImageURL]; ok && in.ImageURL != "" {
			image = id
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO articles (message_id, path, url, title, description, author_name, author_url, image_media_id, views, content, fetched_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (message_id) DO UPDATE SET path = excluded.path, url = excluded.url, title = excluded.title,
				description = excluded.description, author_name = excluded.author_name, author_url = excluded.author_url,
				image_media_id = excluded.image_media_id, views = excluded.views, content = excluded.content, fetched_at = excluded.fetched_at`,
			in.MessageID, in.Path, in.URL, in.Title, in.Description, in.AuthorName, in.AuthorURL, image, in.Views, content, in.Now); err != nil {
			return err
		}
		return affected(tx.ExecContext(ctx, "UPDATE telegraph_jobs SET state = 'fetched', error = '', updated_at = ? WHERE id = ?", in.Now, in.JobID))
	})
}

type ArticleMediaView struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Duration int    `json:"duration"`
	Mime     string `json:"mime"`
}

type ArticleView struct {
	URL         string             `json:"url"`
	Title       string             `json:"title"`
	Description string             `json:"description"`
	AuthorName  string             `json:"author_name"`
	AuthorURL   string             `json:"author_url"`
	Views       int64              `json:"views"`
	FetchedAt   int64              `json:"fetched_at"`
	Content     json.RawMessage    `json:"content"`
	Media       []ArticleMediaView `json:"media"`
}

// GetArticle returns the archived article of a non-deleted message (ErrNotFound when none).
func (s *Store) GetArticle(ctx context.Context, messageID int64) (*ArticleView, error) {
	var v ArticleView
	var content string
	err := s.db.QueryRowContext(ctx, `
		SELECT a.url, a.title, a.description, a.author_name, a.author_url, a.views, a.fetched_at, a.content
		FROM articles a JOIN messages m ON m.id = a.message_id AND m.deleted_at = 0 WHERE a.message_id = ?`, messageID,
	).Scan(&v.URL, &v.Title, &v.Description, &v.AuthorName, &v.AuthorURL, &v.Views, &v.FetchedAt, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v.Content = json.RawMessage(content)
	rows, err := s.db.QueryContext(ctx, `
		SELECT md.id, md.kind, md.state, md.width, md.height, md.duration, md.mime, md.path
		FROM message_media mm JOIN media md ON md.id = mm.media_id
		WHERE mm.message_id = ? AND mm.role = 'article' ORDER BY mm.position`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	v.Media = []ArticleMediaView{}
	for rows.Next() {
		var m ArticleMediaView
		var path string
		if err := rows.Scan(&m.ID, &m.Kind, &m.State, &m.Width, &m.Height, &m.Duration, &m.Mime, &path); err != nil {
			return nil, err
		}
		if m.Mime == "" && path != "" {
			m.Mime = mime.TypeByExtension(filepath.Ext(path))
		}
		v.Media = append(v.Media, m)
	}
	return &v, rows.Err()
}

// ArticleSummary is the article card data carried by MessageView (present when a job exists).
type ArticleSummary struct {
	State        string `json:"state"`
	Error        string `json:"error"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	AuthorName   string `json:"author_name"`
	ImageMediaID int64  `json:"image_media_id,omitempty"` // set only once the cover is downloaded
	URL          string `json:"url"`
}

// articleURL is the canonical address of a Telegraph path, used until the article is fetched.
func articleURL(path string) string { return "https://telegra.ph/" + url.PathEscape(path) }
```

- [ ] **Step 4: 编辑时保留文章媒体**

`internal/store/ingest.go` 编辑分支（`default:` 里）把

```go
			if _, err := tx.ExecContext(ctx, "DELETE FROM message_media WHERE message_id = ?", existing); err != nil {
				return err
			}
```

改为：

```go
			// Article media belong to the message's Telegraph snapshot, not to its Telegram content.
			if _, err := tx.ExecContext(ctx, "DELETE FROM message_media WHERE message_id = ? AND role != 'article'", existing); err != nil {
				return err
			}
```

- [ ] **Step 5: 消息视图带卡片摘要、排除文章媒体**

`internal/store/query.go`：

`MessageView` 末尾加字段：

```go
	Media              []MediaView     `json:"media"`
	Article            *ArticleSummary `json:"article,omitempty"`
}
```

`hydrate` 里的媒体查询加 `AND mm.role != 'article'`：

```go
		WHERE mm.message_id IN (`+ph+`) AND mm.role != 'article' ORDER BY mm.message_id, mm.position`, args...)
```

同一函数里，媒体 `rows` 关闭并检查 `rows.Err()` 之后、回复预览循环 `for i := range views {` 之前插入：

```go
	if err := s.hydrateArticles(ctx, views, idx, ph, args); err != nil {
		return err
	}
```

文件末尾新增：

```go
// hydrateArticles attaches the Telegraph card summary to messages that have a job.
func (s *Store) hydrateArticles(ctx context.Context, views []MessageView, idx map[int64]int, ph string, args []any) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT j.message_id, j.state, j.error, j.path, COALESCE(a.url, ''), COALESCE(a.title, ''), COALESCE(a.description, ''),
			COALESCE(a.author_name, ''), COALESCE((SELECT md.id FROM media md WHERE md.id = a.image_media_id AND md.state = 'done'), 0)
		FROM telegraph_jobs j LEFT JOIN articles a ON a.message_id = j.message_id
		WHERE j.message_id IN (`+ph+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var msgID int64
		var path string
		a := &ArticleSummary{}
		if err := rows.Scan(&msgID, &a.State, &a.Error, &path, &a.URL, &a.Title, &a.Description, &a.AuthorName, &a.ImageMediaID); err != nil {
			return err
		}
		if a.URL == "" {
			a.URL = articleURL(path)
		}
		views[idx[msgID]].Article = a
	}
	return rows.Err()
}
```

（`ListChatMedia` 的 `media` / `file` 类型已经只认 `role = 'main'`，无需改动；测试里有断言。）

- [ ] **Step 6: 运行，确认通过**

Run: `go test ./internal/store/`
Expected: `ok  tgarchive/internal/store`

- [ ] **Step 7: 全量门禁**

Run: `go test ./... && gofmt -l cmd internal`
Expected: 全部 `ok`，gofmt 无输出

- [ ] **Step 8: Commit**

```bash
git add internal/store
git commit -m "feat(store): article snapshots with deduped web media, card summary on message views"
```

---

### Task 4: downloader —— `web` 数据源与 SSRF 防护

**Files:**
- Create: `internal/downloader/websource.go`
- Modify: `internal/downloader/downloader.go`（`Permanent`、`Process`、`retry`、新增 `fail`）
- Test: `internal/downloader/websource_test.go`；`internal/downloader/downloader_test.go`（追加一个测试）

**Interfaces:**
- Consumes: 既有 `Downloader.Process`、`Source`、`ErrTooLarge`、`relBase`（`web:` 前缀自动落在 `media/web/...`）
- Produces:
  - `func Permanent(err error) error`（`Process` 遇到即 `failed`，attempts = 已有 + 1）
  - `var ErrAddrNotAllowed = errors.New("地址不允许")`
  - `func PublicIP(ip net.IP) error`（默认拨号校验）
  - `type WebSource struct { MaxBytes int64; Timeout time.Duration }` + `func NewWebSource(maxBytes int64, allow func(net.IP) error) *WebSource`（`allow == nil` → `PublicIP`）；实现 `Source`

- [ ] **Step 1: 写失败的测试**

`internal/downloader/downloader_test.go` 末尾追加：

```go
func TestPermanentErrorFailsAtOnce(t *testing.T) {
	f, _, m := setup(t, 4)
	d := f.newDL(0)
	d.Register("bot", &fakeSource{errs: []error{Permanent(errors.New("地址不允许"))}})
	d.Process(ctx, m)
	got, _ := f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateFailed || got.Attempts != 1 || got.Error != "地址不允许" || len(f.settled) != 1 {
		t.Fatalf("media = %+v settled = %v", got, f.settled)
	}
}
```

`internal/downloader/websource_test.go`：

```go
package downloader

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

func allowAll(net.IP) error { return nil }

func webMedia(url string) *store.Media {
	return &store.Media{ID: 1, DedupeKey: "web:x", SourceRef: url, Kind: "photo"}
}

func isPermanent(err error) bool {
	var pe *permanentError
	return errors.As(err, &pe)
}

func webServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/img", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "tgarchive/1.0" {
			http.Error(w, "bad ua", 400)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNGDATA"))
	})
	mux.HandleFunc("/clip.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("MP4"))
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) { w.Write(make([]byte, 100)) })
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 10; i++ { // chunked: no Content-Length
			w.Write(make([]byte, 10))
			w.(http.Flusher).Flush()
		}
	})
	mux.HandleFunc("/hop/{n}", func(w http.ResponseWriter, r *http.Request) {
		var n int
		fmt.Sscan(r.PathValue("n"), &n)
		if n == 0 {
			http.Redirect(w, r, "/img", http.StatusFound)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/hop/%d", n-1), http.StatusFound)
	})
	mux.HandleFunc("/to-file", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	})
	mux.HandleFunc("/gone", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "gone", 404) })
	mux.HandleFunc("/busy", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "busy", 503) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestWebSourceDownloads(t *testing.T) {
	srv := webServer(t)
	src := NewWebSource(0, allowAll)
	base := filepath.Join(t.TempDir(), "x")
	p, n, err := src.Fetch(ctx, webMedia(srv.URL+"/img"), base)
	if err != nil || p != base+".png" || n != 7 {
		t.Fatalf("Fetch = %q %d %v", p, n, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "PNGDATA" {
		t.Fatalf("content = %q", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v", st.Mode())
	}
	if _, err := os.Stat(p + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part left behind")
	}
	p, _, err = src.Fetch(ctx, webMedia(srv.URL+"/clip.mp4"), base)
	if err != nil || p != base+".mp4" {
		t.Fatalf("extension from URL path: %q %v", p, err)
	}
}

func TestWebSourceRedirects(t *testing.T) {
	srv := webServer(t)
	src := NewWebSource(0, allowAll)
	base := filepath.Join(t.TempDir(), "x")
	if _, _, err := src.Fetch(ctx, webMedia(srv.URL+"/hop/4"), base); err != nil { // 5 redirects
		t.Fatalf("5 redirects: %v", err)
	}
	_, _, err := src.Fetch(ctx, webMedia(srv.URL+"/hop/5"), base) // 6 redirects
	if err == nil || isPermanent(err) {
		t.Fatalf("6 redirects = %v, want a retryable error", err)
	}
	_, _, err = src.Fetch(ctx, webMedia(srv.URL+"/to-file"), base)
	if !isPermanent(err) || !errors.Is(err, ErrAddrNotAllowed) {
		t.Fatalf("redirect to file:// = %v", err)
	}
}

func TestWebSourceSizeLimit(t *testing.T) {
	srv := webServer(t)
	src := NewWebSource(50, allowAll)
	dir := t.TempDir()
	for _, p := range []string{"/big", "/stream"} {
		_, _, err := src.Fetch(ctx, webMedia(srv.URL+p), filepath.Join(dir, "x"))
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("%s: err = %v, want ErrTooLarge", p, err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("partial files left: %v", entries)
	}
}

func TestWebSourceStatus(t *testing.T) {
	srv := webServer(t)
	src := NewWebSource(0, allowAll)
	base := filepath.Join(t.TempDir(), "x")
	if _, _, err := src.Fetch(ctx, webMedia(srv.URL+"/gone"), base); !isPermanent(err) || err.Error() != "HTTP 404" {
		t.Fatalf("404 = %v", err)
	}
	if _, _, err := src.Fetch(ctx, webMedia(srv.URL+"/busy"), base); err == nil || isPermanent(err) {
		t.Fatalf("503 = %v, want retryable", err)
	}
	for _, ref := range []string{"file:///etc/passwd", "ftp://example.com/a.jpg", "not a url", "https:///nohost"} {
		if _, _, err := src.Fetch(ctx, webMedia(ref), base); !isPermanent(err) || !errors.Is(err, ErrAddrNotAllowed) {
			t.Fatalf("%q = %v", ref, err)
		}
	}
}

func TestWebSourceRefusesInternalAddresses(t *testing.T) {
	srv := webServer(t) // listens on 127.0.0.1
	src := NewWebSource(0, nil)
	_, _, err := src.Fetch(ctx, webMedia(srv.URL+"/img"), filepath.Join(t.TempDir(), "x"))
	if !isPermanent(err) || !errors.Is(err, ErrAddrNotAllowed) || err.Error() != "地址不允许" {
		t.Fatalf("loopback = %v", err)
	}
	host := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	if _, _, err := src.Fetch(ctx, webMedia(host+"/img"), filepath.Join(t.TempDir(), "x")); !errors.Is(err, ErrAddrNotAllowed) {
		t.Fatalf("localhost = %v", err)
	}
}

func TestPublicIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "224.0.0.1",
		"0.0.0.0", "100.64.0.1", "100.127.255.255", "::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "ff02::1", "::"} {
		if err := PublicIP(net.ParseIP(s)); !errors.Is(err, ErrAddrNotAllowed) {
			t.Errorf("PublicIP(%s) = %v, want ErrAddrNotAllowed", s, err)
		}
	}
	for _, s := range []string{"1.1.1.1", "149.154.167.99", "100.128.0.1", "100.63.255.255", "2606:4700::1111"} {
		if err := PublicIP(net.ParseIP(s)); err != nil {
			t.Errorf("PublicIP(%s) = %v, want nil", s, err)
		}
	}
}

func TestWebSourceThroughProcess(t *testing.T) {
	srv := webServer(t)
	f, _, _ := setup(t, 0)
	// A second message carrying a web: media row, as SaveArticle would create it.
	f.st.Ingest(ctx, store.IngestInput{BotID: f.bot, Sender: model.Sender{TgUserID: 42}, Msg: &model.Message{TgMessageID: 2,
		Source: model.SourceBotUpdate, Date: 2, Kind: model.KindPhoto, RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`),
		Media: []model.Media{{DedupeKey: "web:abc", SourceRef: srv.URL + "/img", Kind: "photo", Role: model.RoleMain}}}, Now: 2})
	due, _ := f.st.DueMedia(ctx, 0, 10)
	var m *store.Media
	for i := range due {
		if due[i].DedupeKey == "web:abc" {
			m = &due[i]
		}
	}
	d := f.newDL(0)
	d.Register("web", NewWebSource(0, nil)) // production check: 127.0.0.1 is refused, no retries
	d.Process(ctx, m)
	got, _ := f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateFailed || got.Error != "地址不允许" || got.Attempts != 1 {
		t.Fatalf("refused media = %+v", got)
	}
	f.st.ResetMedia(ctx, m.ID)
	d.Register("web", NewWebSource(0, allowAll))
	cur, _ := f.st.GetMedia(ctx, m.ID)
	d.Process(ctx, cur)
	got, _ = f.st.GetMedia(ctx, m.ID)
	if got.State != store.StateDone || !strings.HasPrefix(got.Path, "web/2026/10/") || !strings.HasSuffix(got.Path, ".png") {
		t.Fatalf("downloaded media = %+v", got)
	}
}
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test ./internal/downloader/`
Expected: FAIL，编译错误 `undefined: Permanent`、`undefined: NewWebSource`、`undefined: permanentError` 等

- [ ] **Step 3: 永久失败**

`internal/downloader/downloader.go`，在 `var ErrTooLarge` 之后加：

```go
// permanentError marks a failure that retrying cannot fix (e.g. a forbidden address).
type permanentError struct{ err error }

func (p *permanentError) Error() string { return p.err.Error() }
func (p *permanentError) Unwrap() error { return p.err }

// Permanent wraps err so Process fails the media at once instead of scheduling retries.
func Permanent(err error) error { return &permanentError{err: err} }
```

`Process` 里 `src.Fetch` 出错的分支改为：

```go
	if err != nil {
		if ctx.Err() != nil {
			return // shutting down: leave it pending for the next start
		}
		if errors.Is(err, ErrTooLarge) {
			d.tooLarge(ctx, m)
			return
		}
		var pe *permanentError
		if errors.As(err, &pe) {
			d.fail(ctx, m, m.Attempts+1, err)
			return
		}
		d.retry(ctx, m, err)
		return
	}
```

`retry` 的最终失败分支改为调用新的 `fail`：

```go
func (d *Downloader) retry(ctx context.Context, m *store.Media, cause error) {
	n := m.Attempts + 1
	if n > len(d.Delays) {
		d.fail(ctx, m, n, cause)
		return
	}
	next := d.Now().Add(d.Delays[n-1]).Unix()
	if err := d.st.MarkMediaRetry(ctx, m.ID, n, next, botapifs.RedactPath(cause.Error())); err != nil {
		log.Printf("downloader: schedule retry for media %d: %v", m.ID, err)
	}
}

func (d *Downloader) fail(ctx context.Context, m *store.Media, attempts int, cause error) {
	if err := d.st.MarkMediaFailed(ctx, m.ID, attempts, botapifs.RedactPath(cause.Error())); err != nil {
		log.Printf("downloader: mark media %d failed: %v", m.ID, err)
		return
	}
	d.settle(m.ID)
}
```

- [ ] **Step 4: `web` 数据源**

`internal/downloader/websource.go`：

```go
package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"tgarchive/internal/store"
)

// ErrAddrNotAllowed is the failure reason for URLs that are not http(s) or that resolve to a
// loopback, private, link-local, multicast, unspecified or CGNAT (100.64.0.0/10) address.
var ErrAddrNotAllowed = errors.New("地址不允许")

const (
	webUserAgent    = "tgarchive/1.0"
	webMaxRedirects = 5
	webTimeout      = 5 * time.Minute
)

var cgnat = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// PublicIP is the default dial check of WebSource: nil for publicly routable unicast addresses.
func PublicIP(ip net.IP) error {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip) {
		return ErrAddrNotAllowed
	}
	return nil
}

// WebSource downloads "web:" media (Telegraph article images and videos) over plain HTTP(S).
// The address check runs on every connection after DNS resolution, so redirects and DNS
// rebinding cannot reach an internal address.
type WebSource struct {
	MaxBytes int64         // 0 = unlimited; larger bodies fail with ErrTooLarge
	Timeout  time.Duration // whole request, default 5 min
	client   *http.Client
}

// NewWebSource builds the source. allow checks each dialed IP; nil means PublicIP. Only tests
// pass anything else (to reach an httptest server on 127.0.0.1).
func NewWebSource(maxBytes int64, allow func(net.IP) error) *WebSource {
	if allow == nil {
		allow = PublicIP
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return ErrAddrNotAllowed
			}
			return allow(ip)
		},
	}
	tr := &http.Transport{
		Proxy:                 nil, // a proxy would make the dial check see the proxy's address instead
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: time.Minute,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
	}
	client := &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > webMaxRedirects {
				return fmt.Errorf("超过 %d 次重定向", webMaxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return ErrAddrNotAllowed
			}
			return nil
		},
	}
	return &WebSource{MaxBytes: maxBytes, Timeout: webTimeout, client: client}
}

func (w *WebSource) Fetch(ctx context.Context, m *store.Media, dstBase string) (string, int64, error) {
	u, err := url.Parse(m.SourceRef)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", 0, Permanent(ErrAddrNotAllowed)
	}
	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", 0, Permanent(err)
	}
	req.Header.Set("User-Agent", webUserAgent)
	resp, err := w.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrAddrNotAllowed) {
			return "", 0, Permanent(ErrAddrNotAllowed)
		}
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("HTTP %d", resp.StatusCode)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests {
			return "", 0, Permanent(err)
		}
		return "", 0, err
	}
	if w.MaxBytes > 0 && resp.ContentLength > w.MaxBytes {
		return "", 0, ErrTooLarge
	}
	dst := dstBase + webExt(resp.Header.Get("Content-Type"), u.Path)
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, err
	}
	var body io.Reader = resp.Body
	if w.MaxBytes > 0 {
		body = io.LimitReader(resp.Body, w.MaxBytes+1)
	}
	n, err := io.Copy(f, body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && w.MaxBytes > 0 && n > w.MaxBytes {
		err = ErrTooLarge
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644) // archived files are world-readable (NAS pull), whatever the umask
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		return "", 0, err
	}
	return dst, n, nil
}

var webMimeExt = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
	"video/mp4": ".mp4", "video/webm": ".webm", "video/quicktime": ".mov",
}

var webPathExt = map[string]string{
	".jpg": ".jpg", ".jpeg": ".jpg", ".png": ".png", ".gif": ".gif", ".webp": ".webp",
	".mp4": ".mp4", ".webm": ".webm", ".mov": ".mov",
}

// webExt picks the stored extension from the response type, then the URL path; anything else
// is ".bin", which /media serves as an opaque download.
func webExt(contentType, urlPath string) string {
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		if e, ok := webMimeExt[strings.ToLower(mt)]; ok {
			return e
		}
	}
	if e, ok := webPathExt[strings.ToLower(path.Ext(urlPath))]; ok {
		return e
	}
	return ".bin"
}
```

- [ ] **Step 5: 运行，确认通过**

Run: `go test ./internal/downloader/`
Expected: `ok  tgarchive/internal/downloader`（`TestWebSourceRefusesInternalAddresses` 证明默认校验拒绝 127.0.0.1 与 `localhost`）

- [ ] **Step 6: 全量门禁**

Run: `go test ./... && gofmt -l cmd internal`
Expected: 全部 `ok`，gofmt 无输出

- [ ] **Step 7: Commit**

```bash
git add internal/downloader
git commit -m "feat(downloader): web source with dial-time SSRF check; permanent failures skip retries"
```

---

### Task 5: receipt —— 链接消息回执并入文章任务

**Files:**
- Modify: `internal/store/media.go`（`ArticleReceipt`、`ReceiptInfo.Article`、`GetReceiptInfo`、新增 `mediaStatuses`）、`internal/receipt/receipt.go`（`Evaluate`、新增 `failOnce`、`EvaluateJob` 复用）
- Test: `internal/receipt/article_test.go`

**Interfaces:**
- Consumes: Task 2 `Ingest(TelegraphPath)`、`ClaimNextTelegraphJob`、`FinishTelegraphJob`；Task 3 `SaveArticle`、`GetArticle`、`RoleArticle`
- Produces:
  - `type ArticleReceipt struct { State, Error string; Media []MediaStatus }`；`ReceiptInfo.Article *ArticleReceipt`（无任务为 nil）
  - `Engine.Evaluate(ctx, messageID)` 新规则：任务 `queued`/`fetching` → 👀；`fetched` → 文章媒体仍有 `pending` 时 👀，否则 👌（失败 / 超限不回复）；`failed` → 保持 👀 并回复一次 `⚠️ 存档失败：<原因>`
  - 既有 `MediaSettled` 不变：它经 `MessagesForMedia`（不分 role）找到链接消息再 `Evaluate`

- [ ] **Step 1: 写失败的测试**

`internal/receipt/article_test.go`：

```go
package receipt

import (
	"encoding/json"
	"reflect"
	"testing"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

// link ingests a Telegraph link message (tg message 10) with a queued job.
func (v *env) link(t *testing.T) int64 {
	t.Helper()
	m := &model.Message{TgMessageID: 10, Source: model.SourceBotUpdate, Date: 10, Kind: model.KindText, Text: "https://telegra.ph/A",
		RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}
	res, err := v.st.Ingest(ctx, store.IngestInput{BotID: v.bot, Sender: model.Sender{TgUserID: 42}, Msg: m, Now: 1, TelegraphPath: "A"})
	if err != nil || !res.TelegraphQueued {
		t.Fatalf("ingest = %+v, %v", res, err)
	}
	return res.MessageID
}

// fetch claims the job and saves an article referencing urls; it returns their media ids.
func (v *env) fetch(t *testing.T, msgID int64, urls ...string) []int64 {
	t.Helper()
	j, err := v.st.ClaimNextTelegraphJob(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	var media []store.ArticleMediaInput
	for _, u := range urls {
		media = append(media, store.ArticleMediaInput{DedupeKey: "web:" + u, URL: u, Kind: "photo"})
	}
	if err := v.st.SaveArticle(ctx, store.SaveArticleInput{JobID: j.ID, MessageID: msgID, Path: "A", URL: "https://telegra.ph/A", Title: "A",
		Media: media, Render: func(map[string]int64) (string, error) { return "[]", nil }, Now: 3}); err != nil {
		t.Fatal(err)
	}
	a, _ := v.st.GetArticle(ctx, msgID)
	var ids []int64
	for _, m := range a.Media {
		ids = append(ids, m.ID)
	}
	return ids
}

func TestArticleSeenUntilMediaSettle(t *testing.T) {
	v := newEnv(t)
	id := v.link(t)
	v.e.Evaluate(ctx, id)
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, []string{"react 42 10 👀"}) {
		t.Fatalf("queued calls = %v", got)
	}
	mids := v.fetch(t, id, "https://x/1.jpg", "https://x/2.jpg")
	v.e.Evaluate(ctx, id)
	if got := v.tr.(*rec).take(); len(got) != 0 {
		t.Fatalf("fetched with pending media: calls = %v", got)
	}
	v.st.MarkMediaDone(ctx, mids[0], "web/a.jpg", 1)
	v.e.MediaSettled(ctx, mids[0])
	if got := v.tr.(*rec).take(); len(got) != 0 {
		t.Fatalf("one media still pending: calls = %v", got)
	}
	v.st.MarkMediaFailed(ctx, mids[1], 1, "地址不允许")
	v.e.MediaSettled(ctx, mids[1])
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, []string{"react 42 10 👌"}) {
		t.Fatalf("settled calls = %v (a failed article image must not block 👌 or reply)", got)
	}
}

func TestArticleWithoutMediaIsDoneOnFetch(t *testing.T) {
	v := newEnv(t)
	id := v.link(t)
	v.e.Evaluate(ctx, id)
	v.fetch(t, id)
	v.e.Evaluate(ctx, id)
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, []string{"react 42 10 👀", "react 42 10 👌"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestArticleFailureRepliesOnce(t *testing.T) {
	v := newEnv(t)
	id := v.link(t)
	v.e.Evaluate(ctx, id)
	j, _ := v.st.ClaimNextTelegraphJob(ctx, 2)
	v.st.FinishTelegraphJob(ctx, j.ID, store.TelegraphFailed, "文章不存在", 3)
	v.e.Evaluate(ctx, id)
	v.e.Evaluate(ctx, id)
	want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 存档失败：文章不存在"}
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestArticleFailureWithoutPriorReaction(t *testing.T) {
	v := newEnv(t)
	id := v.link(t)
	j, _ := v.st.ClaimNextTelegraphJob(ctx, 2)
	v.st.FinishTelegraphJob(ctx, j.ID, store.TelegraphFailed, "网络错误", 3)
	v.e.Evaluate(ctx, id)
	want := []string{"react 42 10 👀", "reply 42 10 ⚠️ 存档失败：网络错误"}
	if got := v.tr.(*rec).take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test ./internal/receipt/`
Expected: FAIL：`TestArticleSeenUntilMediaSettle` 首次 `Evaluate` 得到 `react 42 10 👌`（文本消息直接完成），`TestArticleFailureRepliesOnce` 等同理

- [ ] **Step 3: 回执信息带文章任务**

`internal/store/media.go`：import 增加 `"tgarchive/internal/model"`；把 `type MediaStatus` 到 `GetReceiptInfo` 结束替换为：

```go
type MediaStatus struct{ State, Error string }

// ArticleReceipt is the Telegraph job of a link message and the states of its article media.
type ArticleReceipt struct {
	State, Error string
	Media        []MediaStatus
}

type ReceiptInfo struct {
	MessageID, BotID, TgChatID, TgMessageID int64
	Source, Receipt                         string
	Main                                    []MediaStatus
	Article                                 *ArticleReceipt // nil when the message has no Telegraph job
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
	if ri.Main, err = s.mediaStatuses(ctx, messageID, model.RoleMain); err != nil {
		return nil, err
	}
	var a ArticleReceipt
	err = s.db.QueryRowContext(ctx, "SELECT state, error FROM telegraph_jobs WHERE message_id = ?", messageID).Scan(&a.State, &a.Error)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return &ri, nil
	case err != nil:
		return nil, err
	}
	if a.Media, err = s.mediaStatuses(ctx, messageID, RoleArticle); err != nil {
		return nil, err
	}
	ri.Article = &a
	return &ri, nil
}

// mediaStatuses lists the states of a message's media with the given role, in position order.
func (s *Store) mediaStatuses(ctx context.Context, messageID int64, role string) ([]MediaStatus, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT md.state, md.error FROM message_media mm JOIN media md ON md.id = mm.media_id
		WHERE mm.message_id = ? AND mm.role = ? ORDER BY mm.position`, messageID, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaStatus
	for rows.Next() {
		var ms MediaStatus
		if err := rows.Scan(&ms.State, &ms.Error); err != nil {
			return nil, err
		}
		out = append(out, ms)
	}
	return out, rows.Err()
}
```

（`mediaStatuses` 在返回前关闭 `rows`，之后才发下一条查询，符合单连接约束。）

- [ ] **Step 4: `Evaluate` 并入文章任务**

`internal/receipt/receipt.go`：import 增加 `"slices"`；`Evaluate` 末尾的

```go
	e.apply(ctx, info, info.Main, info.Receipt, func(r string) { e.set(ctx, messageID, r) })
}
```

替换为：

```go
	save := func(r string) { e.set(ctx, messageID, r) }
	main := slices.Clone(info.Main)
	if a := info.Article; a != nil {
		switch a.State {
		case store.TelegraphFailed:
			e.failOnce(ctx, info, info.Receipt, textFailedPrefix+truncate(a.Error, 200), save)
			return
		case store.TelegraphFetched:
			// Article media only hold 👌 back while pending: a failed or oversized image does not
			// turn the archived article into a failure.
			for _, m := range a.Media {
				if m.State == store.StatePending {
					main = append(main, m)
				}
			}
		default: // queued / fetching
			main = append(main, store.MediaStatus{State: store.StatePending})
		}
	}
	e.apply(ctx, info, main, info.Receipt, save)
}

// failOnce keeps 👀 (setting it if nothing was set yet) and sends the failure reply once.
func (e *Engine) failOnce(ctx context.Context, info *store.ReceiptInfo, cur, text string, save func(string)) {
	if cur == store.ReceiptFailed {
		return
	}
	if cur == store.ReceiptNone {
		e.reactLogOnly(ctx, info, EmojiSeen)
	}
	if err := e.reply(ctx, info, text); err == nil {
		save(store.ReceiptFailed)
	}
}
```

`EvaluateJob` 的 `case store.JobFailed:` 分支改为复用它（行为不变，既有 `job_test.go` 覆盖）：

```go
	case store.JobFailed:
		e.failOnce(ctx, info, ji.Receipt, TextFetchFailedPrefix+truncate(ji.Error, 200), save)
```

- [ ] **Step 5: 运行，确认通过**

Run: `go test ./internal/receipt/ ./internal/store/`
Expected: 均 `ok`

- [ ] **Step 6: 全量门禁**

Run: `go test ./... && gofmt -l cmd internal`
Expected: 全部 `ok`，gofmt 无输出

- [ ] **Step 7: Commit**

```bash
git add internal/store/media.go internal/receipt
git commit -m "feat(receipt): Telegraph link messages wait for the article and reply on failure"
```

---

### Task 6: telegraph —— getPage 客户端与串行抓取 Worker

**Files:**
- Create: `internal/telegraph/client.go`、`internal/telegraph/worker.go`
- Test: `internal/telegraph/worker_test.go`

**Interfaces:**
- Consumes: Task 1 `Node`、`Normalize`、`AttachMediaIDs`、`WebKey`、`absURL`、`strictURL`、`linkURL`、`SiteURL`；Task 2 任务队列方法；Task 3 `SaveArticle`、`ArticleMediaInput`；`events.Hub`
- Produces:
  - `const DefaultAPIURL = "https://api.telegra.ph"`
  - `type Page struct { Path, URL, Title, Description, AuthorName, AuthorURL, ImageURL string; Content []Node; Views int64 }`
  - `type APIError struct{ Code string }`（`ok:false`）
  - `type Client struct { BaseURL string; HTTP *http.Client }`；`func NewClient(baseURL string) *Client`（30s 超时）；`func (c *Client) GetPage(ctx, path string) (*Page, error)`
  - `type Receipts interface { Evaluate(ctx context.Context, messageID int64) }`（`*receipt.Engine` 满足）
  - `const ReasonNotFound = "文章不存在"`、`ReasonNetwork = "网络错误"`
  - `type Worker struct { Delays []time.Duration; Poll time.Duration; Now func() time.Time; ... }`
  - `func NewWorker(st *store.Store, api *Client, rc Receipts, hub *events.Hub, wakeDL func()) *Worker`
  - `func (w *Worker) Wake()`、`func (w *Worker) Run(ctx)`、`func (w *Worker) RunOnce(ctx) (bool, error)`
  - 事件：领取任务、完成、失败时各发一次 `message.updated` `{chat_id, message_id}`

- [ ] **Step 1: 写失败的测试**

`internal/telegraph/worker_test.go`：

```go
package telegraph

import (
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

	"tgarchive/internal/events"
	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

var ctx = context.Background()

type evalRec struct {
	mu  sync.Mutex
	ids []int64
}

func (r *evalRec) Evaluate(_ context.Context, id int64) {
	r.mu.Lock()
	r.ids = append(r.ids, id)
	r.mu.Unlock()
}
func (r *evalRec) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.ids) }

// fakeAPI answers getPage with the next queued response (the last one repeats).
type fakeAPI struct {
	mu      sync.Mutex
	replies []func(w http.ResponseWriter)
	paths   []string
	hook    func()
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path+"?"+r.URL.RawQuery)
	reply := f.replies[0]
	if len(f.replies) > 1 {
		f.replies = f.replies[1:]
	}
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	reply(w)
}

func (f *fakeAPI) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func jsonReply(body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { w.Header().Set("Content-Type", "application/json"); w.Write([]byte(body)) }
}

func statusReply(code int) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { http.Error(w, "upstream down", code) }
}

const okPage = `{"ok":true,"result":{"path":"Sample-10-05","url":"https://telegra.ph/Sample-10-05","title":"Sample",
	"description":"A sample","author_name":"Anon","author_url":"https://t.me/anon","image_url":"https://telegra.ph/file/cover.jpg","views":7,
	"content":[{"tag":"p","children":["Hello"]},
		{"tag":"figure","children":[{"tag":"img","attrs":{"src":"/file/cover.jpg"}},{"tag":"figcaption","children":["cap"]}]},
		{"tag":"video","attrs":{"src":"https://cdn.example.com/v.mp4"}}]}}`

type wenv struct {
	st    *store.Store
	api   *fakeAPI
	rc    *evalRec
	w     *Worker
	msg   int64
	chat  int64
	evs   <-chan events.Event
	wakes int
}

func newWorkerEnv(t *testing.T, replies ...func(http.ResponseWriter)) *wenv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bot, _ := st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	res, err := st.Ingest(ctx, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Now: 1, TelegraphPath: "Sample-10-05",
		Msg: &model.Message{TgMessageID: 5, Source: model.SourceBotUpdate, Date: 1, Kind: model.KindText,
			Text: "https://telegra.ph/Sample-10-05", RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{replies: replies}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	hub := events.NewHub()
	evs, unsub := hub.Subscribe()
	t.Cleanup(unsub)
	e := &wenv{st: st, api: api, rc: &evalRec{}, msg: res.MessageID, chat: res.ChatID, evs: evs}
	e.w = NewWorker(st, NewClient(srv.URL), e.rc, hub, func() { e.wakes++ })
	e.w.Delays = []time.Duration{0, 0, 0}
	return e
}

func (e *wenv) updates() int {
	n := 0
	for {
		select {
		case ev := <-e.evs:
			d := ev.Data.(map[string]int64)
			if ev.Type == "message.updated" && d["message_id"] == e.msg && d["chat_id"] == e.chat {
				n++
			}
		default:
			return n
		}
	}
}

func TestWorkerArchivesArticle(t *testing.T) {
	e := newWorkerEnv(t, jsonReply(okPage))
	did, err := e.w.RunOnce(ctx)
	if !did || err != nil {
		t.Fatalf("RunOnce = %v, %v", did, err)
	}
	if got := e.api.calls(); len(got) != 1 || got[0] != "/getPage/Sample-10-05?return_content=true" {
		t.Fatalf("requests = %v", got)
	}
	j, _ := e.st.GetTelegraphJob(ctx, e.msg)
	if j.State != store.TelegraphFetched {
		t.Fatalf("job = %+v", j)
	}
	a, err := e.st.GetArticle(ctx, e.msg)
	if err != nil {
		t.Fatal(err)
	}
	if a.Title != "Sample" || a.URL != "https://telegra.ph/Sample-10-05" || a.AuthorURL != "https://t.me/anon" || a.Views != 7 {
		t.Fatalf("article = %+v", a)
	}
	if len(a.Media) != 2 || a.Media[0].Kind != "photo" || a.Media[1].Kind != "video" {
		t.Fatalf("media = %+v (the cover is the first image, stored once)", a.Media)
	}
	for _, frag := range []string{`"data-src":"https://telegra.ph/file/cover.jpg"`, `"data-media-id":"`, `{"tag":"figcaption","children":["cap"]}`} {
		if !strings.Contains(string(a.Content), frag) {
			t.Fatalf("content missing %s: %s", frag, a.Content)
		}
	}
	v, _ := e.st.GetMessageView(ctx, e.msg)
	if v.Article.State != store.TelegraphFetched || v.Article.Description != "A sample" {
		t.Fatalf("summary = %+v", v.Article)
	}
	due, _ := e.st.DueMedia(ctx, 1<<40, 10)
	if len(due) != 2 || !strings.HasPrefix(due[0].DedupeKey, "web:") || due[0].SourceRef != "https://telegra.ph/file/cover.jpg" {
		t.Fatalf("due media = %+v", due)
	}
	if e.updates() != 2 || e.rc.count() != 1 || e.wakes != 1 {
		t.Fatalf("updates/evaluations/wakes = %d/%d/%d", e.updates(), e.rc.count(), e.wakes)
	}
}

func TestWorkerPageNotFound(t *testing.T) {
	e := newWorkerEnv(t, jsonReply(`{"ok":false,"error":"PAGE_NOT_FOUND"}`))
	e.w.RunOnce(ctx)
	j, _ := e.st.GetTelegraphJob(ctx, e.msg)
	if j.State != store.TelegraphFailed || j.Error != ReasonNotFound || len(e.api.calls()) != 1 {
		t.Fatalf("job = %+v, requests = %d", j, len(e.api.calls()))
	}
	if e.rc.count() != 1 || e.wakes != 0 {
		t.Fatalf("evaluations = %d, wakes = %d", e.rc.count(), e.wakes)
	}
}

func TestWorkerRetriesThenFails(t *testing.T) {
	e := newWorkerEnv(t, statusReply(502), jsonReply("<html>busy</html>"), statusReply(500))
	e.w.RunOnce(ctx)
	j, _ := e.st.GetTelegraphJob(ctx, e.msg)
	if j.State != store.TelegraphFailed || j.Error != ReasonNetwork || len(e.api.calls()) != 4 || j.Attempts != 3 {
		t.Fatalf("job = %+v, requests = %d", j, len(e.api.calls()))
	}
}

func TestWorkerRecoversFromTransientErrors(t *testing.T) {
	e := newWorkerEnv(t, statusReply(503), jsonReply("not json"), jsonReply(okPage))
	e.w.RunOnce(ctx)
	j, _ := e.st.GetTelegraphJob(ctx, e.msg)
	if j.State != store.TelegraphFetched || j.Attempts != 2 || len(e.api.calls()) != 3 {
		t.Fatalf("job = %+v, requests = %d", j, len(e.api.calls()))
	}
}

func TestWorkerAttemptsSurviveRestart(t *testing.T) {
	e := newWorkerEnv(t, statusReply(500))
	j, _ := e.st.ClaimNextTelegraphJob(ctx, 2)
	e.st.SetTelegraphJobAttempts(ctx, j.ID, 3, ReasonNetwork, 2) // three failures before the restart
	c, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { e.w.Run(c); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, _ := e.st.GetTelegraphJob(ctx, e.msg)
		if got.State == store.TelegraphFailed {
			if len(e.api.calls()) != 1 {
				t.Fatalf("requests after restart = %d, want 1 (4th attempt)", len(e.api.calls()))
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("interrupted job not resumed: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkerWake(t *testing.T) {
	e := newWorkerEnv(t, jsonReply(okPage))
	e.w.Poll = time.Hour
	j, _ := e.st.ClaimNextTelegraphJob(ctx, 2)
	e.st.FinishTelegraphJob(ctx, j.ID, store.TelegraphFetched, "", 2) // queue now empty
	c, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { e.w.Run(c); close(done) }()
	defer func() { cancel(); <-done }()
	time.Sleep(50 * time.Millisecond) // let Run go idle
	bot, _ := e.st.UpsertBot(ctx, &store.Bot{TgBotID: 777, TokenEnc: []byte("x"), CreatedAt: 1})
	res, _ := e.st.Ingest(ctx, store.IngestInput{BotID: bot, Sender: model.Sender{TgUserID: 42}, Now: 3, TelegraphPath: "Other",
		Msg: &model.Message{TgMessageID: 6, Source: model.SourceBotUpdate, Date: 3, Kind: model.KindText, Text: "telegra.ph/Other",
			RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}})
	e.w.Wake()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if got, _ := e.st.GetTelegraphJob(ctx, res.MessageID); got.State == store.TelegraphFetched {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Wake did not start the queued job")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkerMessageDeletedDuringFetch(t *testing.T) {
	e := newWorkerEnv(t, jsonReply(okPage))
	e.api.hook = func() { e.st.DeleteMessage(ctx, e.msg, 9) }
	if did, err := e.w.RunOnce(ctx); !did || err != nil {
		t.Fatalf("RunOnce = %v, %v", did, err)
	}
	if _, err := e.st.GetArticle(ctx, e.msg); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("article = %v", err)
	}
	if due, _ := e.st.DueMedia(ctx, 1<<40, 10); len(due) != 0 {
		t.Fatalf("media created for a deleted message: %+v", due)
	}
	if e.rc.count() != 0 || e.wakes != 0 {
		t.Fatalf("evaluations = %d, wakes = %d", e.rc.count(), e.wakes)
	}
}
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test ./internal/telegraph/`
Expected: FAIL，编译错误 `undefined: NewWorker`、`undefined: NewClient`、`undefined: ReasonNotFound` 等

- [ ] **Step 3: 客户端**

`internal/telegraph/client.go`：

```go
package telegraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultAPIURL is the Telegraph API; TELEGRAPH_API_URL overrides it for tests only.
const DefaultAPIURL = "https://api.telegra.ph"

const maxPageBytes = 16 << 20

// Page is the part of a Telegraph getPage result tgarchive keeps.
type Page struct {
	Path        string `json:"path"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	AuthorName  string `json:"author_name"`
	AuthorURL   string `json:"author_url"`
	ImageURL    string `json:"image_url"`
	Content     []Node `json:"content"`
	Views       int64  `json:"views"`
}

// APIError is Telegraph answering ok:false (e.g. PAGE_NOT_FOUND). It is final: never retried.
type APIError struct{ Code string }

func (e *APIError) Error() string { return "telegraph: " + e.Code }

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient talks to baseURL with a 30s per-request timeout.
func NewClient(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// GetPage fetches an article with its content. Network errors, 5xx and non-JSON answers come
// back as plain errors (retryable); ok:false comes back as *APIError.
func (c *Client) GetPage(ctx context.Context, path string) (*Page, error) {
	u := c.BaseURL + "/getPage/" + url.PathEscape(path) + "?return_content=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tgarchive/1.0")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("telegraph: HTTP %d", resp.StatusCode)
	}
	var r struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Result *Page  `json:"result"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("telegraph: bad response (HTTP %d): %w", resp.StatusCode, err)
	}
	if !r.OK {
		return nil, &APIError{Code: r.Error}
	}
	if r.Result == nil {
		return nil, errors.New("telegraph: ok without result")
	}
	return r.Result, nil
}
```

- [ ] **Step 4: Worker**

`internal/telegraph/worker.go`：

```go
package telegraph

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"slices"
	"time"

	"tgarchive/internal/events"
	"tgarchive/internal/store"
)

// Receipts reports the link message's archiving state to the sender (receipt.Engine).
type Receipts interface {
	Evaluate(ctx context.Context, messageID int64)
}

const (
	ReasonNotFound = "文章不存在"
	ReasonNetwork  = "网络错误"
)

// Worker fetches queued Telegraph jobs one at a time; it is independent of the userbot queue.
type Worker struct {
	st       *store.Store
	api      *Client
	receipts Receipts
	hub      *events.Hub
	wakeDL   func()
	wake     chan struct{}

	Delays []time.Duration // waits after the 1st..3rd transient failure; one more failure fails the job
	Poll   time.Duration   // idle re-check interval
	Now    func() time.Time
}

func NewWorker(st *store.Store, api *Client, rc Receipts, hub *events.Hub, wakeDL func()) *Worker {
	return &Worker{st: st, api: api, receipts: rc, hub: hub, wakeDL: wakeDL, wake: make(chan struct{}, 1),
		Delays: []time.Duration{10 * time.Second, 60 * time.Second, 300 * time.Second}, Poll: 30 * time.Second, Now: time.Now}
}

// Wake makes an idle Run look for work now (the collector calls it after queueing a job).
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) Run(ctx context.Context) {
	if n, err := w.st.RequeueFetchingTelegraphJobs(ctx, w.Now().Unix()); err != nil {
		if ctx.Err() == nil {
			log.Printf("telegraph: requeue interrupted jobs: %v", err)
		}
	} else if n > 0 {
		log.Printf("telegraph: requeued %d interrupted jobs", n)
	}
	for ctx.Err() == nil {
		did, err := w.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("telegraph: queue: %v", err)
		}
		if did {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-time.After(w.Poll):
		}
	}
}

// RunOnce processes the oldest queued job and reports whether there was one.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	job, err := w.st.ClaimNextTelegraphJob(ctx, w.Now().Unix())
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	w.publish(job)
	w.process(ctx, job)
	return true, nil
}

func (w *Worker) process(ctx context.Context, job *store.TelegraphJob) {
	attempts := job.Attempts
	for {
		page, err := w.api.GetPage(ctx, job.Path)
		if ctx.Err() != nil {
			return // left 'fetching'; Run requeues it on the next start
		}
		if err == nil {
			err = w.save(ctx, job, page)
			if errors.Is(err, store.ErrNotFound) {
				return // the link message was deleted meanwhile
			}
			if err != nil {
				log.Printf("telegraph: job %d: save: %v", job.ID, err)
				w.finish(ctx, job, store.TelegraphFailed, err.Error())
				return
			}
			w.publish(job)
			// Evaluate before waking the downloader so 👀 always precedes 👌.
			w.receipts.Evaluate(ctx, job.MessageID)
			if w.wakeDL != nil {
				w.wakeDL()
			}
			return
		}
		var ae *APIError
		if errors.As(err, &ae) {
			reason := ReasonNotFound
			if ae.Code != "PAGE_NOT_FOUND" {
				reason = "Telegraph 错误：" + ae.Code
			}
			w.finish(ctx, job, store.TelegraphFailed, reason)
			return
		}
		attempts++
		log.Printf("telegraph: job %d (%s) attempt %d: %v", job.ID, job.Path, attempts, err)
		if attempts > len(w.Delays) {
			w.finish(ctx, job, store.TelegraphFailed, ReasonNetwork)
			return
		}
		if err := w.st.SetTelegraphJobAttempts(ctx, job.ID, attempts, ReasonNetwork, w.Now().Unix()); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return
			}
			log.Printf("telegraph: job %d: record attempt: %v", job.ID, err)
		}
		if !sleep(ctx, w.Delays[attempts-1]) {
			return
		}
	}
}

func (w *Worker) save(ctx context.Context, job *store.TelegraphJob, p *Page) error {
	nodes, refs := Normalize(p.Content)
	image := ""
	if u, ok := absURL(p.ImageURL); ok {
		image = u
		if !slices.ContainsFunc(refs, func(r MediaRef) bool { return r.URL == u }) {
			refs = append(refs, MediaRef{URL: u, Kind: KindPhoto})
		}
	}
	media := make([]store.ArticleMediaInput, 0, len(refs))
	for _, r := range refs {
		media = append(media, store.ArticleMediaInput{DedupeKey: WebKey(r.URL), URL: r.URL, Kind: r.Kind})
	}
	pageURL, ok := strictURL(p.URL)
	if !ok {
		pageURL = SiteURL + "/" + url.PathEscape(job.Path)
	}
	authorURL, _ := linkURL(p.AuthorURL)
	title := p.Title
	if title == "" {
		title = job.Path
	}
	return w.st.SaveArticle(ctx, store.SaveArticleInput{
		JobID: job.ID, MessageID: job.MessageID, Path: job.Path, URL: pageURL, Title: title, Description: p.Description,
		AuthorName: p.AuthorName, AuthorURL: authorURL, ImageURL: image, Views: p.Views, Media: media,
		Render: func(ids map[string]int64) (string, error) {
			AttachMediaIDs(nodes, ids)
			b, err := json.Marshal(nodes)
			return string(b), err
		},
		Now: w.Now().Unix(),
	})
}

func (w *Worker) finish(ctx context.Context, job *store.TelegraphJob, state, reason string) {
	if err := w.st.FinishTelegraphJob(ctx, job.ID, state, reason, w.Now().Unix()); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Printf("telegraph: finish job %d: %v", job.ID, err)
		}
		return
	}
	w.publish(job)
	w.receipts.Evaluate(ctx, job.MessageID)
}

func (w *Worker) publish(job *store.TelegraphJob) {
	w.hub.Publish(events.Event{Type: "message.updated", Data: map[string]int64{"chat_id": job.ChatID, "message_id": job.MessageID}})
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

- [ ] **Step 5: 运行，确认通过（含竞态检测）**

Run: `go test -race ./internal/telegraph/`
Expected: `ok  tgarchive/internal/telegraph`

- [ ] **Step 6: 全量门禁**

Run: `go test ./... && gofmt -l cmd internal`
Expected: 全部 `ok`，gofmt 无输出

- [ ] **Step 7: Commit**

```bash
git add internal/telegraph
git commit -m "feat(telegraph): getPage client and serial fetch worker with backoff"
```

---

### Task 7: 装配 —— 配置、collector、HTTP 接口、app 与端到端测试

**Files:**
- Modify: `internal/config/config.go`、`internal/config/config_test.go`、`internal/collector/collector.go`、`internal/collector/collector_test.go`、`internal/httpapi/server.go`、`internal/httpapi/read.go`、`internal/app/app.go`、`internal/app/app_test.go`
- Test: `internal/httpapi/article_test.go`（新建）

**Interfaces:**
- Consumes: Task 1 `telegraph.Candidate`；Task 2 `IngestInput.TelegraphPath` / `IngestResult.TelegraphQueued`；Task 3 `GetArticle`、`ArticleView`、`MessageView.Article`；Task 4 `NewWebSource`；Task 6 `NewWorker`、`NewClient`
- Produces:
  - `config.Config.TelegraphAPIURL`（env `TELEGRAPH_API_URL`，默认 `https://api.telegra.ph`）
  - `collector.TelegraphQueue interface{ Wake() }`；`collector.Deps.Telegraph`
  - `GET /api/messages/{id}/article` → 200 `store.ArticleView`；无文章 / 消息已删 404；非法 id 400
  - `app.newApp(parent, cfg, dialer userbot.Dialer, webDialCheck func(net.IP) error)`（`New` 传 nil）

- [ ] **Step 1: 写失败的测试**

`internal/config/config_test.go`：`TestLoadDefaults` 末尾加

```go
	if c.TelegraphAPIURL != "https://api.telegra.ph" {
		t.Fatalf("TelegraphAPIURL = %q", c.TelegraphAPIURL)
	}
```

`TestLoadOverrides` 的 env 里加 `"TELEGRAPH_API_URL":    "http://127.0.0.1:9999",`，断言改为：

```go
	if c.RequireForwardAuth || c.MediaMaxBytes != 1048576 || c.BotAPIDirLocal != "/srv/botapi" || c.BarkNotifyFile != "/run/bark/notify.json" ||
		c.TelegraphAPIURL != "http://127.0.0.1:9999" {
```

`internal/collector/collector_test.go` 末尾追加：

```go
type wakeCounter struct {
	mu sync.Mutex
	n  int
}

func (w *wakeCounter) Wake()      { w.mu.Lock(); w.n++; w.mu.Unlock() }
func (w *wakeCounter) count() int { w.mu.Lock(); defer w.mu.Unlock(); return w.n }

func TestTelegraphLinkQueuesJob(t *testing.T) {
	e := setup(t, nil)
	tq := &wakeCounter{}
	e.m.d.Telegraph = tq
	e.st.PutWhitelist(bg, store.WhitelistEntry{BotID: e.bot, TgUserID: 42})
	e.fake.PushMessage(tgtest.TextMsg(1, 42, " https://telegra.ph/Sample-10-05 "))
	e.fake.PushMessage(tgtest.TextMsg(2, 42, "see https://telegra.ph/Sample-10-05"))
	e.m.Start(e.bot)
	eventually(t, "both archived", func() bool { return len(e.messages(t)) == 2 && e.offset() == 3 })
	msgs := e.messages(t)
	j, err := e.st.GetTelegraphJob(bg, msgs[0].ID)
	if err != nil || j.Path != "Sample-10-05" || j.State != store.TelegraphQueued {
		t.Fatalf("job = %+v, %v", j, err)
	}
	if _, err := e.st.GetTelegraphJob(bg, msgs[1].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("link inside text queued a job: %v", err)
	}
	if tq.count() != 1 {
		t.Fatalf("telegraph wakes = %d", tq.count())
	}
	eventually(t, "reactions", func() bool { return len(e.reactions()) == 2 })
	if r := e.reactions(); r[0] != "👀" || r[1] != "👌" {
		t.Fatalf("reactions = %v (the queued link waits with 👀)", r)
	}
}

func TestTelegraphDisabledArchivesPlainText(t *testing.T) {
	e := setup(t, nil) // no Telegraph queue
	e.st.PutWhitelist(bg, store.WhitelistEntry{BotID: e.bot, TgUserID: 42})
	e.fake.PushMessage(tgtest.TextMsg(1, 42, "https://telegra.ph/Sample-10-05"))
	e.m.Start(e.bot)
	eventually(t, "👌", func() bool { r := e.reactions(); return len(r) == 1 && r[0] == "👌" })
	if _, err := e.st.GetTelegraphJob(bg, e.messages(t)[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("job without a queue: %v", err)
	}
}
```

`internal/httpapi/article_test.go`：

```go
package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"

	"tgarchive/internal/model"
	"tgarchive/internal/store"
)

func TestGetArticle(t *testing.T) {
	e := newReadEnv(t)
	res, err := e.st.Ingest(bg, store.IngestInput{BotID: 1, Sender: model.Sender{TgUserID: 42, FirstName: "Alice"}, Now: 3, TelegraphPath: "Sample",
		Msg: &model.Message{TgMessageID: 3, Source: model.SourceBotUpdate, Date: 3, Kind: model.KindText, Text: "https://telegra.ph/Sample",
			RawFormat: model.RawBotAPI, Raw: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if w := do(e.h, "GET", fmt.Sprintf("/api/messages/%d/article", res.MessageID), nil); w.Code != 404 {
		t.Fatalf("article before fetch = %d", w.Code)
	}
	var msg store.MessageView
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/messages/%d", res.MessageID), nil).Body.Bytes(), &msg)
	if msg.Article == nil || msg.Article.State != "queued" || msg.Article.URL != "https://telegra.ph/Sample" {
		t.Fatalf("message article = %+v", msg.Article)
	}

	j, _ := e.st.ClaimNextTelegraphJob(bg, 4)
	err = e.st.SaveArticle(bg, store.SaveArticleInput{JobID: j.ID, MessageID: res.MessageID, Path: "Sample", URL: "https://telegra.ph/Sample",
		Title: "Sample", AuthorName: "Anon", Views: 3, Now: 5,
		Media:  []store.ArticleMediaInput{{DedupeKey: "web:1", URL: "https://telegra.ph/file/1.jpg", Kind: "photo"}},
		Render: func(ids map[string]int64) (string, error) { return `[{"tag":"p","children":["hi"]}]`, nil }})
	if err != nil {
		t.Fatal(err)
	}
	w := do(e.h, "GET", fmt.Sprintf("/api/messages/%d/article", res.MessageID), nil)
	var got struct {
		Content json.RawMessage
		Media   []map[string]any
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("article = %d %s", w.Code, w.Body)
	}
	var raw map[string]any
	json.Unmarshal(w.Body.Bytes(), &raw)
	for _, k := range []string{"url", "title", "description", "author_name", "author_url", "views", "fetched_at", "content", "media"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("article JSON lacks %q: %s", k, w.Body)
		}
	}
	if string(got.Content) != `[{"tag":"p","children":["hi"]}]` || len(got.Media) != 1 || got.Media[0]["kind"] != "photo" || got.Media[0]["state"] != "pending" {
		t.Fatalf("article = %s", w.Body)
	}
	for _, k := range []string{"id", "kind", "state", "width", "height", "duration", "mime"} {
		if _, ok := got.Media[0][k]; !ok {
			t.Fatalf("article media JSON lacks %q: %s", k, w.Body)
		}
	}
	json.Unmarshal(do(e.h, "GET", fmt.Sprintf("/api/messages/%d", res.MessageID), nil).Body.Bytes(), &msg)
	if msg.Article.State != "fetched" || msg.Article.Title != "Sample" || len(msg.Media) != 0 {
		t.Fatalf("message after fetch = %+v", msg)
	}

	if w := do(e.h, "GET", fmt.Sprintf("/api/messages/%d/article", e.photoMsg), nil); w.Code != 404 {
		t.Fatalf("article of a photo message = %d", w.Code)
	}
	if w := do(e.h, "GET", "/api/messages/abc/article", nil); w.Code != 400 {
		t.Fatalf("bad id = %d", w.Code)
	}
	if w := do(e.h, "DELETE", fmt.Sprintf("/api/messages/%d", res.MessageID), nil); w.Code != 204 {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := do(e.h, "GET", fmt.Sprintf("/api/messages/%d/article", res.MessageID), nil); w.Code != 404 {
		t.Fatalf("article after delete = %d", w.Code)
	}
}
```

`internal/app/app_test.go`：import 增加 `"net"` 与 `"tgarchive/internal/userbot"`；`TestUserbotFetchEndToEnd` 里的 `newApp(context.Background(), cfg, &mtDialer{photo: photo})` 改为 `newApp(context.Background(), cfg, &mtDialer{photo: photo}, nil)`；文件末尾追加：

```go
func TestTelegraphEndToEnd(t *testing.T) {
	fake := tgtest.New(t)
	var base string // the fake Telegraph server's URL, also the host of the article's images
	mux := http.NewServeMux()
	mux.HandleFunc("GET /getPage/Sample-10-05", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"ok":true,"result":{"path":"Sample-10-05","url":"https://telegra.ph/Sample-10-05","title":"Sample",
			"author_name":"Anon","image_url":"%[1]s/cover.jpg","views":1,"content":[{"tag":"p","children":["Hello"]},
			{"tag":"img","attrs":{"src":"%[1]s/cover.jpg"}},{"tag":"img","attrs":{"src":"%[1]s/missing.jpg"}}]}}`, base)
	})
	mux.HandleFunc("GET /getPage/Gone", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error":"PAGE_NOT_FOUND"}`)
	})
	mux.HandleFunc("GET /cover.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("COVER"))
	})
	srv := httptest.NewServer(mux) // /missing.jpg answers 404
	defer srv.Close()
	base = srv.URL

	dataDir := t.TempDir()
	cfg := cfgFor(fake, dataDir)
	cfg.TelegraphAPIURL = srv.URL
	// The fake serves images from 127.0.0.1, which the production dial check refuses.
	a, err := newApp(context.Background(), cfg, userbot.GotdDialer{}, func(net.IP) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Handler
	addBotAndWhitelist(t, h, 42)

	fake.PushMessage(tgtest.TextMsg(1, 42, "https://telegra.ph/Sample-10-05"))
	eventually(t, "👀 then 👌 on the link message", func() bool {
		e := emojis(fake)
		return len(e) == 2 && e[0] == "👀" && e[1] == "👌"
	})
	msgs := firstChatMessages(t, h)
	if len(msgs) != 1 || msgs[0].Text != "https://telegra.ph/Sample-10-05" || len(msgs[0].Media) != 0 {
		t.Fatalf("messages = %+v", msgs)
	}
	art := msgs[0].Article
	if art == nil || art.State != store.TelegraphFetched || art.Title != "Sample" || art.ImageMediaID == 0 {
		t.Fatalf("article summary = %+v", art)
	}
	code, body := req(t, h, "GET", fmt.Sprintf("/api/messages/%d/article", msgs[0].ID), nil)
	var av store.ArticleView
	if code != 200 || json.Unmarshal(body, &av) != nil || len(av.Media) != 2 {
		t.Fatalf("article = %d %s", code, body)
	}
	if av.Media[0].State != store.StateDone || av.Media[1].State != store.StateFailed || av.Media[0].ID != art.ImageMediaID {
		t.Fatalf("article media = %+v", av.Media)
	}
	if !strings.Contains(string(av.Content), fmt.Sprintf(`"data-media-id":"%d"`, av.Media[0].ID)) {
		t.Fatalf("content = %s", av.Content)
	}
	code, body = req(t, h, "GET", fmt.Sprintf("/media/%d", av.Media[0].ID), nil)
	if code != 200 || string(body) != "COVER" {
		t.Fatalf("cover = %d %q", code, body)
	}
	if files, _ := filepath.Glob(filepath.Join(dataDir, "media", "web", "*", "*", "*.jpg")); len(files) != 1 {
		t.Fatalf("archived web files = %v", files)
	}
	if n := len(fake.Calls("sendMessage")); n != 0 {
		t.Fatalf("a failed article image must not reply (%d replies)", n)
	}

	fake.PushMessage(tgtest.TextMsg(2, 42, "telegra.ph/Gone"))
	eventually(t, "failure reply", func() bool {
		for _, c := range fake.Calls("sendMessage") {
			if c.Params["text"] == "⚠️ 存档失败：文章不存在" {
				return true
			}
		}
		return false
	})
	if e := emojis(fake); len(e) != 3 || e[2] != "👀" {
		t.Fatalf("reactions = %v (a failed article keeps 👀)", e)
	}
}
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test ./internal/config/ ./internal/collector/ ./internal/httpapi/ ./internal/app/`
Expected: FAIL，编译错误 `c.TelegraphAPIURL undefined`、`e.m.d.Telegraph undefined`、`too many arguments in call to newApp`；`TestGetArticle` 得到 404 的 `GET /api/` 兜底

- [ ] **Step 3: 配置**

`internal/config/config.go`：`Config` 末尾加字段，`Load` 的字面量加默认值：

```go
	PollTimeoutSec     int
	TelegraphAPIURL    string // Telegraph API; only tests point it elsewhere
}
```

```go
		BarkNotifyFile:  getenv("BARK_NOTIFY_FILE"),
		PollTimeoutSec:  50,
		TelegraphAPIURL: or(getenv("TELEGRAPH_API_URL"), "https://api.telegra.ph"),
	}
```

（加字段后 `gofmt -w internal/config/config.go` 会重新对齐字面量。）

- [ ] **Step 4: collector 识别链接**

`internal/collector/collector.go`：import 增加 `"tgarchive/internal/telegraph"`；在 `LinkHandler` 之后加接口，`Deps` 加字段：

```go
// TelegraphQueue is woken after a message queued a Telegraph job (telegraph.Worker). When nil,
// Telegraph links are archived as plain text.
type TelegraphQueue interface {
	Wake()
}
```

```go
	Links          LinkHandler
	Telegraph      TelegraphQueue
```

`handle` 里把 `Ingest` 调用替换为：

```go
	// A new text message that is exactly one Telegraph link also queues the article snapshot,
	// in the same transaction as the message and the offset.
	tpath := ""
	if len(u.Message) > 0 && m.d.Telegraph != nil && res.Msg.Kind == model.KindText && len(res.Msg.Media) == 0 {
		tpath, _ = telegraph.Candidate(res.Msg.Text)
	}
	ir, err := m.d.Store.Ingest(ctx, store.IngestInput{BotID: botID, Sender: res.Sender, Msg: res.Msg, Offset: next, Now: now, TelegraphPath: tpath})
```

`handle` 结尾改为：

```go
	// Evaluate before waking the downloader so 👀 always precedes 👌.
	m.d.Receipts.Evaluate(ctx, ir.MessageID)
	m.d.Downloader.Wake()
	if ir.TelegraphQueued {
		m.d.Telegraph.Wake()
	}
	return nil
}
```

（t.me 链接仍先交给 `LinkHandler`；被它消费的消息不会走到这里。）

- [ ] **Step 5: HTTP 接口**

`internal/httpapi/server.go` 路由表在 `GET /api/messages/{id}` 之后加：

```go
	mux.HandleFunc("GET /api/messages/{id}/article", s.getArticle)
```

`internal/httpapi/read.go` 在 `deleteMessage` 之前加：

```go
func (s *Server) getArticle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad message id")
		return
	}
	a, err := s.Store.GetArticle(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}
```

- [ ] **Step 6: app 装配**

`internal/app/app.go`：import 增加 `"net"` 与 `"tgarchive/internal/telegraph"`；`App` 结构体加 `tw *telegraph.Worker`；`New` / `newApp` 改为：

```go
func New(parent context.Context, cfg *config.Config) (*App, error) {
	return newApp(parent, cfg, userbot.GotdDialer{}, nil)
}

// newApp takes the userbot dialer and the web media dial check (nil = downloader.PublicIP) so
// tests can run against in-process fakes; production always uses New.
func newApp(parent context.Context, cfg *config.Config, dialer userbot.Dialer, webDialCheck func(net.IP) error) (*App, error) {
```

注册 `mt` 数据源之后：

```go
	dl.Register("mt", &userbot.MTSource{API: ub})
	dl.Register("web", downloader.NewWebSource(cfg.MediaMaxBytes, webDialCheck))
	fetcher := userbot.NewFetcher(ub, st, rc, clients, hub, dl.Wake, mediaDir)
	tw := telegraph.NewWorker(st, telegraph.NewClient(cfg.TelegraphAPIURL), rc, hub, dl.Wake)
```

`collector.Deps` 字面量加 `Telegraph: tw`：

```go
		Notifier: notifier, Avatars: av, Links: fetcher, Telegraph: tw,
```

返回值加 `tw: tw`：

```go
		ub: ub, fetcher: fetcher, tw: tw}, nil
```

`Start` 里启动 worker：

```go
	a.wg.Add(3)
	go func() { defer a.wg.Done(); a.ub.Run(a.ctx) }()
	go func() { defer a.wg.Done(); a.fetcher.Run(a.ctx) }()
	go func() { defer a.wg.Done(); a.tw.Run(a.ctx) }()
```

- [ ] **Step 7: 运行，确认通过**

Run: `go test -race ./internal/config/ ./internal/collector/ ./internal/httpapi/ ./internal/app/`
Expected: 均 `ok`；`TestTelegraphEndToEnd` 覆盖 发链接 → 👀 → 文章入库 → 封面落盘 → 👌，缺图不回复，`PAGE_NOT_FOUND` 回复 `⚠️ 存档失败：文章不存在`

- [ ] **Step 8: 全量门禁**

Run: `go vet ./... && go test ./... && gofmt -l cmd internal`
Expected: 全部 `ok`，gofmt 无输出

- [ ] **Step 9: Commit**

```bash
git add internal/config internal/collector internal/httpapi internal/app
git commit -m "feat: wire Telegraph archiving: collector, worker, web source, GET /api/messages/{id}/article"
```

---

### Task 8: web —— 文章类型、路由与媒体查看器的显式列表模式

**Files:**
- Modify: `web/src/api/types.ts`、`web/src/api/client.ts`、`web/src/api/client.test.ts`、`web/src/test/fixtures.ts`、`web/src/lib/router.ts`、`web/src/lib/router.test.ts`、`web/src/state/store.ts`、`web/src/components/viewer/MediaViewer.tsx`、`web/src/components/viewer/MediaViewer.test.tsx`

**Interfaces:**
- Consumes: Task 7 的 JSON（`MessageView.article`、`GET /api/messages/{id}/article`）
- Produces:
  - 类型 `ArticleState`、`ArticleSummary`、`ArticleNode`、`ArticleElement`、`ArticleMedia`、`Article`；`Message.article?: ArticleSummary`
  - `Api.article(messageId: number): Promise<Article>`
  - 测试夹具 `makeArticle(over)`、`makeArticleMedia(over)`；`fakeApi().article` 默认返回 `makeArticle()`
  - 路由 `{ name: 'article'; chatId: number; messageId: number }` ↔ `/chat/:chatId/article/:messageId`；`routeChatId(r: Route): number`；`NavigateOptions.fromChat`（`history.state` 写 `fromChat: true`）
  - `ViewerItem { mediaId: number; kind: string; date: number; text: string; entities: Entity[] }`
  - `ViewerTarget = { chatId; messageId; mediaId } | { list: ViewerItem[]; mediaId: number; title: string }`
  - `toViewerItems(msgs: Message[]): ViewerItem[]`（按消息 id 升序）

- [ ] **Step 1: 写失败的测试**

`web/src/api/client.test.ts` 第一个用例 `builds read URLs with cursor and limit` 改为额外请求文章：

```ts
    await api.chats();
    await api.article(55);
    expect(calls.map((c) => c.url)).toEqual([
      '/api/chats/7/messages?limit=50',
      '/api/chats/7/messages?before=120&limit=50',
      '/api/chats/7/media?type=file&before=33&limit=50',
      '/api/chats',
      '/api/messages/55/article',
    ]);
```

`web/src/lib/router.test.ts`：import 加 `routeChatId`；`round-trips every route` 的列表在 `chat` 之后加 `{ name: 'article', chatId: 12, messageId: 345 },`；`falls back to home` 的路径列表改为：

```ts
    for (const p of [
      '/nope',
      '/chat',
      '/chat/0',
      '/chat/abc',
      '/chat/1/2',
      '/settings/bots/x',
      '/settings/zzz',
      '/chat/1/article',
      '/chat/1/article/0',
      '/chat/1/articles/2',
      '/chat/1/article/2/3',
    ]) {
```

`describe('router')` 末尾加：

```ts
  it('maps routes to the chat they show', () => {
    expect(routeChatId({ name: 'chat', chatId: 4 })).toBe(4);
    expect(routeChatId({ name: 'article', chatId: 4, messageId: 9 })).toBe(4);
    expect(routeChatId({ name: 'settings' })).toBe(0);
  });

  it('marks an article opened from its chat with fromChat', () => {
    navigate({ name: 'article', chatId: 5, messageId: 6 }, { fromChat: true });
    expect(location.pathname).toBe('/chat/5/article/6');
    expect(history.state).toEqual({ fromList: false, fromChat: true });
  });
```

`web/src/components/viewer/MediaViewer.test.tsx` 末尾追加：

```tsx
describe('MediaViewer with an explicit list', () => {
  const list = [
    { mediaId: 201, kind: 'photo', date: 1_790_000_000, text: '', entities: [] },
    { mediaId: 202, kind: 'video', date: 1_790_000_000, text: '', entities: [] },
    { mediaId: 203, kind: 'photo', date: 1_790_000_000, text: '', entities: [] },
  ];

  it('walks only the given items, titled by the list, without loading chat media', async () => {
    const api = fakeApi();
    const r = renderWithStore(<MediaViewer />, api);
    act(() => {
      r.store.viewer.value = { list, mediaId: 202, title: 'Sample' };
    });
    await screen.findByRole('dialog', { name: '媒体查看器' });
    expect(screen.getByText('Sample')).toBeTruthy();
    expect(screen.getByText(/2 \/ 3/)).toBeTruthy();
    expect(r.container.querySelector('.MediaViewer-content video')!.getAttribute('src')).toBe('/media/202');
    fireEvent.click(screen.getByRole('button', { name: '下一个' }));
    expect(r.container.querySelector('.MediaViewer-content img')!.getAttribute('src')).toBe('/media/203');
    expect(screen.queryByRole('button', { name: '下一个' })).toBeNull();
    fireEvent.keyDown(window, { key: 'ArrowLeft' });
    fireEvent.keyDown(window, { key: 'ArrowLeft' });
    expect(r.container.querySelector('.MediaViewer-content img')!.getAttribute('src')).toBe('/media/201');
    await act(async () => {}); // flush effects: no chat-media request in list mode
    expect(api.chatMedia).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(r.store.viewer.value).toBeNull();
  });
});
```

- [ ] **Step 2: 运行，确认失败**

Run: `cd web && npx vitest run src/api src/lib/router.test.ts src/components/viewer`
Expected: FAIL：`api.article is not a function`、`routeChatId is not a function`、列表模式用例等不到「媒体查看器」对话框（旧实现把列表目标当会话处理，查不到媒体后自行关闭）

- [ ] **Step 3: 类型与客户端**

`web/src/api/types.ts`：`Message` 末尾加字段并在其后新增类型：

```ts
  media: Media[];
  /** Present when the message is a Telegraph link with an archiving job. */
  article?: ArticleSummary;
}

export type ArticleState = 'queued' | 'fetching' | 'fetched' | 'failed';

/** Card data for a Telegraph link message (GET /api/messages/:id carries it). */
export interface ArticleSummary {
  state: ArticleState;
  error: string;
  title: string;
  description: string;
  author_name: string;
  /** Cover image; only set once it has been downloaded. */
  image_media_id?: number;
  url: string;
}

/** Normalized Telegraph node: text, or an element. img/video carry data-media-id + data-src;
 * iframes arrive as {tag: 'embed', attrs: {href, src}}. */
export type ArticleNode = string | ArticleElement;

export interface ArticleElement {
  tag: string;
  attrs?: Record<string, string>;
  children?: ArticleNode[];
}

export interface ArticleMedia {
  id: number;
  kind: string; // photo / video
  state: MediaState;
  width: number;
  height: number;
  duration: number;
  mime: string;
}

/** GET /api/messages/:id/article */
export interface Article {
  url: string;
  title: string;
  description: string;
  author_name: string;
  author_url: string;
  views: number;
  fetched_at: number;
  content: ArticleNode[];
  media: ArticleMedia[];
}
```

`web/src/api/client.ts`：类型 import 加 `Article`；`Api` 接口在 `message(id: number): Promise<Message>;` 之后加 `article(messageId: number): Promise<Article>;`；实现里在 `message:` 之后加：

```ts
  article: (messageId) => request('GET', `/api/messages/${messageId}/article`),
```

`web/src/test/fixtures.ts`：类型 import 改为 `import type { Article, ArticleMedia, Bot, Chat, Media, Message } from '../api/types';`；在 `fakeApi` 之前加：

```ts
export function makeArticleMedia(over: Partial<ArticleMedia> = {}): ArticleMedia {
  return { id: 200, kind: 'photo', state: 'done', width: 0, height: 0, duration: 0, mime: 'image/jpeg', ...over };
}

export function makeArticle(over: Partial<Article> = {}): Article {
  return {
    url: 'https://telegra.ph/Sample-10-05',
    title: 'Sample',
    description: 'A sample article',
    author_name: 'Anon',
    author_url: 'https://t.me/anon',
    views: 7,
    fetched_at: 1_790_000_000,
    content: [{ tag: 'p', children: ['Hello'] }],
    media: [],
    ...over,
  };
}
```

`fakeApi` 的 `base` 里 `message:` 之后加：

```ts
    article: vi.fn(async () => makeArticle()),
```

- [ ] **Step 4: 路由**

`web/src/lib/router.ts`：

`Route` 联合类型在 `chat` 之后加 `| { name: 'article'; chatId: number; messageId: number }`。

`parseRoute` 在 `chat` 判断之后加：

```ts
  if (parts[0] === 'chat' && parts.length === 4 && parts[2] === 'article' && id(parts[1]) && id(parts[3])) {
    return { name: 'article', chatId: id(parts[1]), messageId: id(parts[3]) };
  }
```

`routePath` 在 `case 'chat':` 之后加：

```ts
    case 'article':
      return `/chat/${r.chatId}/article/${r.messageId}`;
```

`NavigateOptions` 加字段，并新增 `routeChatId`、改写 `navigate` 的 state：

```ts
  fromList?: boolean;
  /** Marks an article reader opened from its chat, so closing it can use `history.back()` (the
   * system back button and the reader's own back button then do the same thing). */
  fromChat?: boolean;
}

/** The chat a route shows (the article reader overlays its chat); 0 for none. */
export function routeChatId(r: Route): number {
  return r.name === 'chat' || r.name === 'article' ? r.chatId : 0;
}

export function navigate(to: Route, opts: NavigateOptions = {}): void {
  const path = routePath(to);
  const state: { fromList: boolean; fromChat?: boolean } = { fromList: !!opts.fromList };
  if (opts.fromChat) state.fromChat = true;
  if (opts.replace) history.replaceState(state, '', path);
  else history.pushState(state, '', path);
  route.value = to;
}
```

（未传 `fromChat` 时 state 仍是 `{ fromList }`，既有断言不变。）

- [ ] **Step 5: 查看器目标**

`web/src/state/store.ts`：类型 import 加 `Entity`；把 `interface ViewerTarget` 替换为：

```ts
/** One photo/video/GIF the media viewer can show. */
export interface ViewerItem {
  mediaId: number;
  kind: string; // photo / video / animation
  date: number;
  text: string; // caption, '' for none
  entities: Entity[];
}

/** Opens the viewer on a chat's media (walking the whole chat), or on an explicit list such as
 * the media of one archived article (walking only that list). */
export type ViewerTarget =
  | { chatId: number; messageId: number; mediaId: number }
  | { list: ViewerItem[]; mediaId: number; title: string };
```

- [ ] **Step 6: 查看器显式列表模式**

`web/src/components/viewer/MediaViewer.tsx` 整个替换为：

```tsx
import { ChevronLeft, ChevronRight, Download, X, ZoomIn, ZoomOut } from 'lucide-preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { errorMessage, mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { formatFullDate, senderName } from '../../lib/format';
import { useStore, type ViewerItem, type ViewerTarget } from '../../state/store';
import { IconButton } from '../../ui/Button';
import { RichText } from '../message/RichText';
import { VISUAL_KINDS, mainMedia } from '../media/util';
import './viewer.scss';

export const VIEWER_PAGE = 100;
export const VIEWER_MAX_PAGES = 50;
const MIN_ZOOM = 1;
const MAX_ZOOM = 4;
const ZOOM_STEP = 0.5;
const SWIPE_H_THRESHOLD = 50; // px; horizontal drag past this (and past the vertical delta) navigates
const SWIPE_V_THRESHOLD = 80; // px; downward drag past this closes the viewer

export function toViewerItems(msgs: Message[]): ViewerItem[] {
  const out: { id: number; item: ViewerItem }[] = [];
  for (const msg of msgs) {
    const media = mainMedia(msg);
    if (media && media.state === 'done' && VISUAL_KINDS.includes(msg.kind)) {
      out.push({ id: msg.id, item: { mediaId: media.id, kind: msg.kind, date: msg.date, text: msg.text, entities: msg.entities } });
    }
  }
  return out.sort((a, b) => a.id - b.id).map((x) => x.item);
}

function ViewerInner({ target }: { target: ViewerTarget }) {
  const store = useStore();
  const close = () => {
    store.viewer.value = null;
  };
  const listed = 'list' in target ? target : null;
  const inChat = 'list' in target ? null : target;
  const chatId = inChat?.chatId ?? 0;
  const seed = listed ? listed.list : toViewerItems(store.conv(chatId).items.filter((m) => m.id === inChat?.messageId));
  const [items, setItems] = useState<ViewerItem[]>(seed);
  const [mediaId, setMediaId] = useState(target.mediaId);
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null);
  const swipeStart = useRef<{ x: number; y: number } | null>(null);

  // Load the whole chat's media (newest first, paged by id) so left/right walks all of it. An
  // explicit list is already complete.
  useEffect(() => {
    if (listed) return;
    let cancelled = false;
    (async () => {
      const all: Message[] = [];
      let before = 0;
      for (let i = 0; i < VIEWER_MAX_PAGES; i++) {
        const page = await store.api.chatMedia(chatId, 'media', before, VIEWER_PAGE);
        all.push(...page);
        if (page.length < VIEWER_PAGE) break;
        before = page[page.length - 1].id;
      }
      if (cancelled) return;
      const list = toViewerItems(all);
      if (list.some((it) => it.mediaId === target.mediaId)) setItems(list);
      else if (seed.length === 0) close();
    })().catch((err) => {
      if (cancelled) return;
      if (seed.length > 0) store.showToast(errorMessage(err));
      else close();
    });
    return () => {
      cancelled = true;
    };
  }, [chatId, target.mediaId]);

  const index = items.findIndex((it) => it.mediaId === mediaId);
  const item = index >= 0 ? items[index] : undefined;

  const go = (delta: number) => {
    const next = items[index + delta];
    if (!next) return;
    setMediaId(next.mediaId);
    setZoom(1);
    setPan({ x: 0, y: 0 });
  };

  const setZoomClamped = (z: number) => {
    const v = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z));
    setZoom(v);
    if (v === 1) setPan({ x: 0, y: 0 });
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close();
      else if (e.key === 'ArrowLeft') go(-1);
      else if (e.key === 'ArrowRight') go(1);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  if (!item) return null;
  const chat = store.chats.value.find((c) => c.id === chatId);
  const title = listed ? listed.title : chat ? senderName(chat.sender) : '';
  const isPhoto = item.kind === 'photo';

  return (
    <div class="MediaViewer" role="dialog" aria-modal="true" aria-label="媒体查看器">
      <div class="MediaViewer-head">
        <div class="MediaViewer-sender">
          <span class="MediaViewer-name">{title}</span>
          <span class="MediaViewer-date">
            {formatFullDate(item.date)}
            {items.length > 1 && ` · ${index + 1} / ${items.length}`}
          </span>
        </div>
        <div class="MediaViewer-actions">
          {isPhoto && (
            <>
              <IconButton label="缩小" class="translucent-white zoom-btn" disabled={zoom <= MIN_ZOOM} onClick={() => setZoomClamped(zoom - ZOOM_STEP)}>
                <ZoomOut size={24} />
              </IconButton>
              <IconButton label="放大" class="translucent-white zoom-btn" disabled={zoom >= MAX_ZOOM} onClick={() => setZoomClamped(zoom + ZOOM_STEP)}>
                <ZoomIn size={24} />
              </IconButton>
            </>
          )}
          <a class="IconButton translucent-white" href={mediaUrl(item.mediaId, true)} download aria-label="下载" title="下载">
            <Download size={24} />
          </a>
          <IconButton label="关闭" class="translucent-white" onClick={close}>
            <X size={24} />
          </IconButton>
        </div>
      </div>
      <div
        class="MediaViewer-content"
        onClick={(e) => {
          if (e.target === e.currentTarget) close();
        }}
        onWheel={(e) => {
          if (!isPhoto) return;
          e.preventDefault();
          setZoomClamped(zoom + (e.deltaY < 0 ? ZOOM_STEP : -ZOOM_STEP));
        }}
        onPointerDown={(e) => {
          // zoom > 1 is the pan-drag's territory (handled on the <img> itself below); leave it alone.
          if (zoom > 1) return;
          // A drag on a video's native controls (scrubbing) is not a swipe.
          if ((e.target as HTMLElement).tagName === 'VIDEO') return;
          swipeStart.current = { x: e.clientX, y: e.clientY };
        }}
        onPointerUp={(e) => {
          const start = swipeStart.current;
          swipeStart.current = null;
          if (!start || zoom > 1) return;
          const dx = e.clientX - start.x;
          const dy = e.clientY - start.y;
          if (Math.abs(dx) > SWIPE_H_THRESHOLD && Math.abs(dx) > Math.abs(dy)) go(dx < 0 ? 1 : -1); // swipe left = next
          else if (dy > SWIPE_V_THRESHOLD && dy > Math.abs(dx)) close();
        }}
      >
        {isPhoto ? (
          <img
            key={item.mediaId}
            src={mediaUrl(item.mediaId)}
            alt=""
            draggable={false}
            class={zoom > 1 ? 'zoomed' : ''}
            style={{ transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})` }}
            onDblClick={() => setZoomClamped(zoom > 1 ? 1 : 2)}
            onPointerDown={(e) => {
              if (zoom <= 1) return;
              (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
              drag.current = { x: e.clientX, y: e.clientY, px: pan.x, py: pan.y };
            }}
            onPointerMove={(e) => {
              const d = drag.current;
              if (d) setPan({ x: d.px + e.clientX - d.x, y: d.py + e.clientY - d.y });
            }}
            onPointerUp={() => {
              drag.current = null;
            }}
          />
        ) : (
          <video
            key={item.mediaId}
            src={mediaUrl(item.mediaId)}
            controls={item.kind === 'video'}
            autoplay
            loop={item.kind === 'animation'}
            muted={item.kind === 'animation'}
            playsInline
          />
        )}
      </div>
      {index > 0 && (
        <button type="button" class="MediaViewer-nav prev" aria-label="上一个" onClick={() => go(-1)}>
          <ChevronLeft size={36} />
        </button>
      )}
      {index < items.length - 1 && (
        <button type="button" class="MediaViewer-nav next" aria-label="下一个" onClick={() => go(1)}>
          <ChevronRight size={36} />
        </button>
      )}
      {item.text && (
        <div class="MediaViewer-caption">
          <RichText text={item.text} entities={item.entities} />
        </div>
      )}
    </div>
  );
}

/** Full-screen viewer over all photos/videos/GIFs of the chat, or over an explicit list (an
 * article's media); opened by setting store.viewer. */
export function MediaViewer() {
  const store = useStore();
  const target = store.viewer.value;
  if (!target) return null;
  const key = 'list' in target ? `list:${target.mediaId}` : `${target.chatId}:${target.mediaId}`;
  return <ViewerInner key={key} target={target} />;
}
```

- [ ] **Step 7: 运行，确认通过**

Run: `cd web && npm test`
Expected: 全部通过（含既有 `shared.test.tsx` 的会话模式查看器用例）

- [ ] **Step 8: 构建门禁**

Run: `cd web && npm run build`
Expected: `tsc --noEmit` 无错误，`vite build` 成功

- [ ] **Step 9: Commit**

```bash
git add web/src
git commit -m "feat(web): article types and route; media viewer walks an explicit list"
```

---

### Task 9: web —— 文章卡片与 Instant View 阅读页

**Files:**
- Create: `web/src/components/article/ArticleCard.tsx`、`web/src/components/article/ArticleContent.tsx`、`web/src/components/article/ArticleReader.tsx`、`web/src/components/article/article.scss`
- Modify: `web/src/components/message/MessageBubble.tsx`、`web/src/components/middle/MiddleColumn.tsx`、`web/src/App.tsx`、`web/src/components/left/ChatsPanel.tsx`
- Test: `web/src/components/article/article.test.tsx`

**Interfaces:**
- Consumes: Task 8 的类型、`api.article`、`makeArticle`/`makeArticleMedia`、`navigate(..., { fromChat })`、`routeChatId`、`ViewerItem` / 列表型 `ViewerTarget`；既有 `safeHref`、`mediaUrl`、`api.retryMedia`、`IconButton`、`Spinner`、`formatFullDate`
- Produces:
  - `ArticleCard({ msg })`：`queued`/`fetching` →「正在存档文章…」；`failed` →「存档失败：<原因>」+ 原链接；`fetched` → 可点按钮（站点名 Telegraph、标题、两行描述、封面），点击 `navigate({ name: 'article', chatId, messageId }, { fromChat: true })`
  - `ArticleContent({ nodes, media, onOpenMedia, onRetry })`：白名单渲染，不复制属性
  - `ArticleReader({ chatId, messageId })`：`role="dialog"`、`aria-label="文章"`
  - `MiddleColumn({ chatId, articleId? })`

- [ ] **Step 1: 写失败的测试**

`web/src/components/article/article.test.tsx`：

```tsx
import { act, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ArticleNode, ArticleSummary, Message } from '../../api/types';
import { App } from '../../App';
import type { EventSourceLike } from '../../lib/sse';
import { route } from '../../lib/router';
import { createStore } from '../../state/store';
import { fakeApi, makeArticle, makeArticleMedia, makeBot, makeChat, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { MessageBubble } from '../message/MessageBubble';
import { ArticleCard } from './ArticleCard';
import { ArticleReader } from './ArticleReader';

afterEach(() => {
  history.replaceState(null, '', '/');
  route.value = { name: 'home' };
});

function summary(over: Partial<ArticleSummary> = {}): ArticleSummary {
  return { state: 'fetched', error: '', title: 'Sample', description: 'A sample article', author_name: 'Anon', url: 'https://telegra.ph/Sample-10-05', ...over };
}

function linkMsg(article: ArticleSummary): Message {
  return makeMessage({ id: 1, chat_id: 10, text: 'https://telegra.ph/Sample-10-05', article });
}

describe('ArticleCard', () => {
  it('shows progress while queued or fetching', () => {
    renderWithStore(<ArticleCard msg={linkMsg(summary({ state: 'queued', title: '' }))} />);
    expect(screen.getByText('Telegraph')).toBeTruthy();
    expect(screen.getByText('正在存档文章…')).toBeTruthy();
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('shows the error and the original link when archiving failed', () => {
    renderWithStore(<ArticleCard msg={linkMsg(summary({ state: 'failed', error: '文章不存在', title: '' }))} />);
    expect(screen.getByText('存档失败：文章不存在')).toBeTruthy();
    expect(screen.getByRole('link').getAttribute('href')).toBe('https://telegra.ph/Sample-10-05');
  });

  it('drops an unsafe original link', () => {
    const { container } = renderWithStore(<ArticleCard msg={linkMsg(summary({ state: 'failed', error: 'x', url: 'javascript:alert(1)' }))} />);
    expect(container.querySelector('a')).toBeNull();
  });

  it('shows title, description and cover once fetched, and opens the reader with a history entry', () => {
    history.replaceState({ fromList: true }, '', '/chat/10');
    const { container } = renderWithStore(<ArticleCard msg={linkMsg(summary({ image_media_id: 7 }))} />);
    expect(screen.getByText('Sample')).toBeTruthy();
    expect(screen.getByText('A sample article')).toBeTruthy();
    expect(container.querySelector('.ArticleCard-image')!.getAttribute('src')).toBe('/media/7');
    const before = history.length;
    fireEvent.click(screen.getByRole('button'));
    expect(location.pathname).toBe('/chat/10/article/1');
    expect(history.length).toBe(before + 1);
    expect(history.state).toEqual({ fromList: false, fromChat: true });
    expect(route.value).toEqual({ name: 'article', chatId: 10, messageId: 1 });
  });

  it('renders inside the message bubble under the text', () => {
    const msg = linkMsg(summary());
    const { container } = renderWithStore(
      <MessageBubble bubble={{ kind: 'message', key: '1', msg, first: true, last: true }} sender={{ name: 'Alice', peerId: 42 }} onMenu={vi.fn()} />,
    );
    const text = container.querySelector('.text-content')!;
    expect(text.querySelector('.ArticleCard')).toBeTruthy();
    expect(text.textContent!.startsWith('https://telegra.ph/Sample-10-05')).toBe(true);
  });
});

const everyNode: ArticleNode[] = [
  { tag: 'p', children: ['Plain ', { tag: 'strong', children: ['bold'] }, ' ', { tag: 'b', children: ['b'] }, ' ', { tag: 'em', children: ['em'] }, ' ', { tag: 'i', children: ['i'] }, ' ', { tag: 'u', children: ['u'] }, ' ', { tag: 's', children: ['s'] }, ' ', { tag: 'code', children: ['code'] }, { tag: 'br' }, 'after break'] },
  { tag: 'h3', children: ['Heading 3'] },
  { tag: 'h4', children: ['Heading 4'] },
  { tag: 'blockquote', children: ['A quote'] },
  { tag: 'aside', children: ['A pull quote'] },
  { tag: 'ul', children: [{ tag: 'li', children: ['bullet'] }] },
  { tag: 'ol', children: [{ tag: 'li', children: ['numbered'] }] },
  { tag: 'pre', children: ['let x = 1;'] },
  { tag: 'hr' },
  { tag: 'p', children: [{ tag: 'a', attrs: { href: 'https://example.com/x' }, children: ['safe link'] }, ' ', { tag: 'a', attrs: { href: 'javascript:alert(1)' }, children: ['bad link'] }] },
  { tag: 'figure', children: [{ tag: 'img', attrs: { 'data-media-id': '201', 'data-src': 'https://telegra.ph/file/a.jpg' } }, { tag: 'figcaption', children: ['A caption'] }] },
  { tag: 'video', attrs: { 'data-media-id': '202', 'data-src': 'https://cdn.example.com/v.mp4' } },
  { tag: 'img', attrs: { 'data-media-id': '203', 'data-src': 'https://telegra.ph/file/c.jpg' } },
  { tag: 'img', attrs: { 'data-media-id': '204', 'data-src': 'https://telegra.ph/file/d.jpg' } },
  { tag: 'img', attrs: { 'data-src': 'javascript:alert(1)' } },
  { tag: 'figure', children: [{ tag: 'embed', attrs: { href: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ', src: 'https://telegra.ph/embed/youtube?url=x' } }] },
  { tag: 'script', children: ['unwrapped text'] },
];

const everyMedia = [
  makeArticleMedia({ id: 201, kind: 'photo', state: 'done' }),
  makeArticleMedia({ id: 202, kind: 'video', state: 'done', mime: 'video/mp4' }),
  makeArticleMedia({ id: 203, kind: 'photo', state: 'failed' }),
  makeArticleMedia({ id: 204, kind: 'photo', state: 'pending' }),
];

async function openReader(over: Parameters<typeof makeArticle>[0] = {}) {
  const api = fakeApi({ article: vi.fn(async () => makeArticle({ content: everyNode, media: everyMedia, ...over })) });
  const r = renderWithStore(<ArticleReader chatId={10} messageId={1} />, api);
  await screen.findByRole('heading', { level: 1, name: 'Sample' });
  return r;
}

describe('ArticleReader', () => {
  it('renders every node type without raw HTML', async () => {
    const { container, api } = await openReader();
    expect(api.article).toHaveBeenCalledWith(1);
    const body = container.querySelector('.ArticleReader-body')!;
    for (const sel of ['p strong', 'p em', 'p u', 'p s', 'p code', 'p br', 'h3', 'h4', 'blockquote', 'aside', 'ul li', 'ol li', 'pre', 'hr', 'figure figcaption']) {
      expect(body.querySelector(sel), sel).toBeTruthy();
    }
    expect(body.querySelectorAll('strong')).toHaveLength(2); // <b> renders as <strong>
    expect(body.querySelector('script')).toBeNull();
    expect(body.textContent).toContain('unwrapped text');
    expect(screen.getByText('safe link').closest('a')!.getAttribute('href')).toBe('https://example.com/x');
    expect(screen.getByText('bad link').closest('a')).toBeNull();
    expect(screen.getByText('Anon').closest('a')!.getAttribute('href')).toBe('https://t.me/anon');
    expect(screen.getByRole('link', { name: '在 Telegraph 打开' }).getAttribute('href')).toBe('https://telegra.ph/Sample-10-05');
  });

  it('shows done media, placeholders with the original link, and an embed card', async () => {
    const { container } = await openReader();
    expect(container.querySelector('.ArticleMedia-photo img')!.getAttribute('src')).toBe('/media/201');
    expect(container.querySelector('video.ArticleMedia')!.getAttribute('src')).toBe('/media/202');
    const placeholders = container.querySelectorAll('.ArticleMedia-placeholder');
    expect(placeholders).toHaveLength(3);
    expect(placeholders[0].textContent).toContain('下载失败');
    expect(placeholders[0].querySelector('a')!.getAttribute('href')).toBe('https://telegra.ph/file/c.jpg');
    expect(placeholders[1].textContent).toContain('正在下载…');
    expect(placeholders[1].querySelector('button')).toBeNull();
    expect(placeholders[2].querySelector('a')).toBeNull(); // javascript: original link dropped
    const embed = container.querySelector('a.ArticleEmbed')!;
    expect(embed.getAttribute('href')).toBe('https://www.youtube.com/watch?v=dQw4w9WgXcQ');
    expect(embed.textContent).toContain('YouTube');
  });

  it('retries a failed image through the media retry endpoint', async () => {
    const { container, api } = await openReader();
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await waitFor(() => expect(api.retryMedia).toHaveBeenCalledWith(203));
    await waitFor(() => expect(container.querySelectorAll('.ArticleMedia-placeholder')[0].textContent).toContain('正在下载…'));
  });

  it('opens the media viewer on this article’s photos and videos only', async () => {
    const { store } = await openReader();
    fireEvent.click(screen.getByRole('button', { name: '查看图片' }));
    const t = store.viewer.value;
    expect(t && 'list' in t ? { ids: t.list.map((i) => i.mediaId), kinds: t.list.map((i) => i.kind), mediaId: t.mediaId, title: t.title } : t).toEqual({
      ids: [201, 202],
      kinds: ['photo', 'video'],
      mediaId: 201,
      title: 'Sample',
    });
  });

  it('shows the load error', async () => {
    const api = fakeApi({ article: vi.fn(async () => Promise.reject(new Error('not found'))) });
    renderWithStore(<ArticleReader chatId={10} messageId={1} />, api);
    expect(await screen.findByText('not found')).toBeTruthy();
  });

  it('back button goes back through history when opened from the chat, else replaces with the chat', async () => {
    const back = vi.spyOn(history, 'back').mockImplementation(() => {});
    history.pushState({ fromList: false, fromChat: true }, '', '/chat/10/article/1');
    await openReader();
    fireEvent.click(screen.getByRole('button', { name: '返回' }));
    expect(back).toHaveBeenCalledTimes(1);
    back.mockRestore();
  });

  it('back button from a deep link replaces the entry with the chat', async () => {
    history.replaceState(null, '', '/chat/10/article/1');
    const before = history.length;
    await openReader();
    fireEvent.click(screen.getByRole('button', { name: '返回' }));
    expect(location.pathname).toBe('/chat/10');
    expect(history.length).toBe(before);
  });
});

class FakeES implements EventSourceLike {
  readyState = 1;
  onopen: ((ev: Event) => unknown) | null = null;
  onerror: ((ev: Event) => unknown) | null = null;
  addEventListener() {}
  close() {}
}

describe('ArticleReader in the app', () => {
  it('opens from the card and closes on the system back button (popstate)', async () => {
    history.replaceState({ fromList: true }, '', '/chat/10');
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1 })]),
      chats: vi.fn(async () => [makeChat({ id: 10 })]),
      messages: vi.fn(async () => [linkMsg(summary())]),
    });
    const store = createStore(api, { chatsReloadDelay: 0 });
    const { container } = render(<App store={store} eventSource={() => new FakeES()} />);
    fireEvent.click(await screen.findByRole('button', { name: /Sample/ }));
    expect(await screen.findByRole('dialog', { name: '文章' })).toBeTruthy();
    expect(container.querySelector('#MiddleColumn .ArticleReader')).toBeTruthy();
    await act(async () => {
      history.replaceState({ fromList: true }, '', '/chat/10'); // what the browser restores on back
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(screen.queryByRole('dialog', { name: '文章' })).toBeNull();
    expect(screen.getByText('https://telegra.ph/Sample-10-05')).toBeTruthy(); // still in the chat
  });

  it('deep-links straight into the reader', async () => {
    history.replaceState(null, '', '/chat/10/article/1');
    const api = fakeApi({ chats: vi.fn(async () => [makeChat({ id: 10 })]) });
    const store = createStore(api, { chatsReloadDelay: 0 });
    render(<App store={store} eventSource={() => new FakeES()} />);
    expect(await screen.findByRole('heading', { level: 1, name: 'Sample' })).toBeTruthy();
  });
});

describe('article.scss', () => {
  const css = readFileSync(join(process.cwd(), 'src/components/article/article.scss'), 'utf-8');
  it('covers its whole column (full screen at <=600px, where the middle column is) above the chat header', () => {
    const start = css.indexOf('.ArticleReader {');
    const body = css.slice(start, css.indexOf('}', start));
    expect(body).toMatch(/position:\s*absolute;/);
    expect(body).toMatch(/inset:\s*0;/);
    expect(body).toMatch(/z-index:\s*10;/);
  });
  it('limits the reading column to 732px', () => {
    expect(css).toMatch(/\.ArticleReader-body \{\s*max-width:\s*732px;/);
  });
});
```

- [ ] **Step 2: 运行，确认失败**

Run: `cd web && npx vitest run src/components/article`
Expected: FAIL，`Failed to resolve import "./ArticleCard"`

- [ ] **Step 3: 卡片**

`web/src/components/article/ArticleCard.tsx`：

```tsx
import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { safeHref } from '../../lib/entities';
import { navigate } from '../../lib/router';
import './article.scss';

/** Link-preview style card under a Telegraph link message; opens the reader once archived. */
export function ArticleCard({ msg }: { msg: Message }) {
  const a = msg.article;
  if (!a) return null;
  if (a.state === 'fetched') {
    return (
      <button
        type="button"
        class="ArticleCard"
        onClick={() => navigate({ name: 'article', chatId: msg.chat_id, messageId: msg.id }, { fromChat: true })}
      >
        <span class="ArticleCard-site">Telegraph</span>
        {a.title && <span class="ArticleCard-title">{a.title}</span>}
        {a.description && <span class="ArticleCard-description">{a.description}</span>}
        {a.image_media_id ? (
          <img class="ArticleCard-image" src={mediaUrl(a.image_media_id)} alt="" loading="lazy" decoding="async" />
        ) : null}
      </button>
    );
  }
  const href = safeHref(a.url);
  return (
    <div class="ArticleCard">
      <span class="ArticleCard-site">Telegraph</span>
      {a.state === 'failed' ? (
        <>
          <span class="ArticleCard-status error">存档失败：{a.error || '未知原因'}</span>
          {href && (
            <a class="ArticleCard-link" href={href} target="_blank" rel="noopener noreferrer">
              {href}
            </a>
          )}
        </>
      ) : (
        <span class="ArticleCard-status">正在存档文章…</span>
      )}
    </div>
  );
}
```

- [ ] **Step 4: 正文渲染**

`web/src/components/article/ArticleContent.tsx`：

```tsx
import { ExternalLink } from 'lucide-preact';
import { Fragment, h, type ComponentChild } from 'preact';
import { mediaUrl } from '../../api/client';
import type { ArticleElement, ArticleMedia, ArticleNode } from '../../api/types';
import { safeHref } from '../../lib/entities';

/** Telegraph tags rendered as plain elements (b/i map to their semantic twins). Anything not
 * listed here and not handled below is unwrapped to its children: no attributes are ever copied
 * from the archive, and nothing goes through innerHTML. */
const BLOCKS: Record<string, string> = {
  p: 'p',
  h3: 'h3',
  h4: 'h4',
  blockquote: 'blockquote',
  aside: 'aside',
  ul: 'ul',
  ol: 'ol',
  li: 'li',
  pre: 'pre',
  code: 'code',
  b: 'strong',
  strong: 'strong',
  i: 'em',
  em: 'em',
  u: 'u',
  s: 's',
  figure: 'figure',
  figcaption: 'figcaption',
};

export interface ArticleContentProps {
  nodes: ArticleNode[];
  media: Map<number, ArticleMedia>;
  onOpenMedia: (mediaId: number) => void;
  onRetry: (mediaId: number) => void;
}

export function ArticleContent(props: ArticleContentProps) {
  return <>{props.nodes.map((n, i) => renderNode(n, i, props))}</>;
}

function renderNode(n: ArticleNode, key: number, ctx: ArticleContentProps): ComponentChild {
  if (typeof n === 'string') return n;
  const kids = (n.children ?? []).map((c, i) => renderNode(c, i, ctx));
  switch (n.tag) {
    case 'br':
      return <br key={key} />;
    case 'hr':
      return <hr key={key} />;
    case 'a': {
      const href = safeHref(n.attrs?.href ?? '');
      return href ? (
        <a key={key} href={href} target="_blank" rel="noopener noreferrer">
          {kids}
        </a>
      ) : (
        <span key={key}>{kids}</span>
      );
    }
    case 'img':
    case 'video':
      return <ArticleMediaBlock key={key} node={n} ctx={ctx} />;
    case 'embed':
      return <EmbedCard key={key} node={n} />;
  }
  const tag = BLOCKS[n.tag];
  return tag ? h(tag, { key }, kids) : <Fragment key={key}>{kids}</Fragment>;
}

function ArticleMediaBlock({ node, ctx }: { node: ArticleElement; ctx: ArticleContentProps }) {
  const id = Number(node.attrs?.['data-media-id'] ?? 0);
  const m = ctx.media.get(id);
  if (m && m.state === 'done') {
    if (m.kind === 'video') {
      return <video class="ArticleMedia" src={mediaUrl(id)} controls playsInline preload="metadata" />;
    }
    return (
      <button type="button" class="ArticleMedia ArticleMedia-photo" aria-label="查看图片" onClick={() => ctx.onOpenMedia(id)}>
        <img src={mediaUrl(id)} alt="" loading="lazy" decoding="async" />
      </button>
    );
  }
  const original = safeHref(node.attrs?.['data-src'] ?? '');
  const label = !m || m.state === 'pending' ? '正在下载…' : m.state === 'too_large' ? '文件超过存档上限' : '下载失败';
  return (
    <div class="ArticleMedia-placeholder">
      <span>{label}</span>
      {m?.state === 'failed' && (
        <button type="button" class="ArticleMedia-retry" onClick={() => ctx.onRetry(id)}>
          重试
        </button>
      )}
      {original && (
        <a href={original} target="_blank" rel="noopener noreferrer">
          原链接
        </a>
      )}
    </div>
  );
}

const EMBED_SOURCES: Record<string, string> = {
  'youtube.com': 'YouTube',
  'youtu.be': 'YouTube',
  'vimeo.com': 'Vimeo',
  'twitter.com': 'Twitter',
  'x.com': 'Twitter',
};

function EmbedCard({ node }: { node: ArticleElement }) {
  const href = safeHref(node.attrs?.href ?? '') ?? safeHref(node.attrs?.src ?? '');
  if (!href) return null;
  const host = new URL(href).hostname.replace(/^www\./, '');
  return (
    <a class="ArticleEmbed" href={href} target="_blank" rel="noopener noreferrer">
      <ExternalLink size={20} class="ArticleEmbed-icon" />
      <span class="ArticleEmbed-text">
        <span class="ArticleEmbed-source">{EMBED_SOURCES[host] ?? host}</span>
        <span class="ArticleEmbed-url">{href}</span>
      </span>
    </a>
  );
}
```

- [ ] **Step 5: 阅读页**

`web/src/components/article/ArticleReader.tsx`：

```tsx
import { ArrowLeft } from 'lucide-preact';
import { useEffect, useState } from 'preact/hooks';
import { errorMessage } from '../../api/client';
import type { Article } from '../../api/types';
import { safeHref } from '../../lib/entities';
import { formatFullDate } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore, type ViewerItem } from '../../state/store';
import { IconButton } from '../../ui/Button';
import { Spinner } from '../../ui/Spinner';
import { ArticleContent } from './ArticleContent';
import './article.scss';

/** Instant View style reader for an archived Telegraph article, opened by the article route. */
export function ArticleReader({ chatId, messageId }: { chatId: number; messageId: number }) {
  const store = useStore();
  const [article, setArticle] = useState<Article | null>(null);
  const [error, setError] = useState('');
  // The message object is replaced whenever SSE reports a change to it or its article media,
  // so depending on it reloads the article (media states) while the reader is open.
  const msg = store.conv(chatId).items.find((m) => m.id === messageId);

  useEffect(() => {
    let cancelled = false;
    store.api.article(messageId).then(
      (a) => {
        if (cancelled) return;
        setArticle(a);
        setError('');
      },
      (e) => {
        if (!cancelled) setError(errorMessage(e));
      },
    );
    return () => {
      cancelled = true;
    };
  }, [messageId, msg]);

  // Opened from the chat: go back through history, so the system back button and this one agree.
  // Deep link: there is nothing of ours to go back to, so replace the entry with the chat.
  const close = () => {
    const state = history.state as { fromChat?: boolean } | null;
    if (state?.fromChat) history.back();
    else navigate({ name: 'chat', chatId }, { replace: true });
  };

  const media = new Map((article?.media ?? []).map((m) => [m.id, m]));
  const viewable: ViewerItem[] = (article?.media ?? [])
    .filter((m) => m.state === 'done' && (m.kind === 'photo' || m.kind === 'video'))
    .map((m) => ({ mediaId: m.id, kind: m.kind, date: article!.fetched_at, text: '', entities: [] }));
  const openMedia = (mediaId: number) => {
    if (article) store.viewer.value = { list: viewable, mediaId, title: article.title };
  };
  const retry = async (mediaId: number) => {
    try {
      await store.api.retryMedia(mediaId);
      setArticle((a) => a && { ...a, media: a.media.map((m) => (m.id === mediaId ? { ...m, state: 'pending' as const } : m)) });
    } catch (e) {
      store.showToast(errorMessage(e));
    }
  };

  const title = article?.title || msg?.article?.title || '文章';
  const original = safeHref(article?.url ?? msg?.article?.url ?? '');
  const authorHref = article?.author_url ? safeHref(article.author_url) : null;

  return (
    <div class="ArticleReader" role="dialog" aria-modal="true" aria-label="文章">
      <div class="ArticleReader-header">
        <IconButton label="返回" onClick={close}>
          <ArrowLeft size={24} />
        </IconButton>
        <span class="ArticleReader-title">{title}</span>
        {original && (
          <a class="ArticleReader-open" href={original} target="_blank" rel="noopener noreferrer">
            在 Telegraph 打开
          </a>
        )}
      </div>
      <div class="ArticleReader-scroll">
        {article ? (
          <article class="ArticleReader-body">
            <h1 class="ArticleReader-heading">{article.title}</h1>
            <div class="ArticleReader-byline">
              {article.author_name &&
                (authorHref ? (
                  <a href={authorHref} target="_blank" rel="noopener noreferrer">
                    {article.author_name}
                  </a>
                ) : (
                  <span>{article.author_name}</span>
                ))}
              <span>存档于 {formatFullDate(article.fetched_at)}</span>
            </div>
            <ArticleContent nodes={article.content} media={media} onOpenMedia={openMedia} onRetry={retry} />
          </article>
        ) : (
          <div class="ArticleReader-state">{error || <Spinner />}</div>
        )}
      </div>
    </div>
  );
}
```

- [ ] **Step 6: 样式**

`web/src/components/article/article.scss`：

```scss
// Telegraph article card (link-preview look, webA-design.md §5.9) and the Instant View style
// reader. Sizes for the reader come from the Telegraph spec §8.

.ArticleCard {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: 29rem;
  margin: 0.375rem 0 0.25rem;
  padding: 0.1875rem 0.5rem 0.25rem 0.625rem;
  border-radius: 0.25rem;
  font-size: calc(var(--message-text-size) - 1px);
  line-height: 1.125rem;
  text-align: start;
  white-space: normal;
  color: var(--color-text);
  background: var(--accent-background-color, var(--color-primary-tint));

  &::before {
    content: '';
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    width: 2px;
    border-radius: 2px 0 0 2px;
    background: var(--accent-color, var(--color-primary));
  }
}

button.ArticleCard {
  cursor: pointer;
}

.ArticleCard-site {
  font-weight: var(--font-weight-semibold);
  color: var(--accent-color, var(--color-primary));
}

.ArticleCard-title {
  margin-top: 0.125rem;
  font-weight: var(--font-weight-semibold);
}

.ArticleCard-description {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
}

.ArticleCard-image {
  display: block;
  width: 100%;
  max-height: 20rem;
  margin: 0.25rem 0 0.1875rem;
  border-radius: var(--border-radius-messages-small);
  object-fit: cover;
}

.ArticleCard-status {
  color: var(--color-text-secondary);

  &.error {
    color: var(--color-error);
  }
}

.ArticleCard-link {
  color: var(--color-links);
  overflow-wrap: anywhere;
}

// Covers the middle column on desktop; the middle column itself is full screen at <=600px
// (layout.scss), so the reader is too.
.ArticleReader {
  position: absolute;
  inset: 0;
  z-index: 10; // above MiddleHeader (5)
  display: flex;
  flex-direction: column;
  background: var(--color-background);
  animation: fade-in 0.2s ease-out;
}

.ArticleReader-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  height: 3.5rem;
  padding: 0 0.75rem 0 0.5rem;
  border-bottom: 1px solid var(--color-borders);
}

.ArticleReader-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 1rem;
  font-weight: var(--font-weight-semibold);
}

.ArticleReader-open {
  flex-shrink: 0;
  padding: 0.375rem 0.75rem;
  border-radius: var(--border-radius-button-tiny);
  font-size: 0.875rem;
  font-weight: var(--font-weight-medium);
  color: var(--color-primary);
  text-decoration: none;

  &:hover {
    background: var(--color-primary-tint);
  }
}

.ArticleReader-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
}

.ArticleReader-state {
  display: flex;
  justify-content: center;
  padding: 3rem 1rem;
  color: var(--color-text-secondary);
}

.ArticleReader-body {
  max-width: 732px;
  margin: 0 auto;
  padding: 1.5rem 1.5rem 3rem;
  font-size: 17px;
  line-height: 1.6;
  color: var(--color-text);
  overflow-wrap: anywhere;

  @media (max-width: 600px) {
    padding: 1rem 1rem 2.5rem;
  }

  p {
    margin: 0 0 1rem;
  }

  h3 {
    margin: 1.75rem 0 0.75rem;
    font-size: 22px;
    font-weight: var(--font-weight-semibold);
    line-height: 1.3;
  }

  h4 {
    margin: 1.5rem 0 0.5rem;
    font-size: 19px;
    font-weight: var(--font-weight-semibold);
    line-height: 1.3;
  }

  a {
    color: var(--color-links);
    text-decoration: none;

    &:hover {
      text-decoration: underline;
    }
  }

  blockquote {
    margin: 1rem 0;
    padding: 0.125rem 0 0.125rem 1rem;
    border-left: 3px solid var(--color-primary);
    font-style: italic;
  }

  aside {
    margin: 1.5rem 0;
    font-size: 22px;
    font-style: italic;
    line-height: 1.45;
    text-align: center;
  }

  ul,
  ol {
    margin: 0 0 1rem;
    padding-left: 1.5rem;
  }

  li {
    margin-bottom: 0.25rem;
  }

  pre {
    margin: 0 0 1rem;
    padding: 0.75rem 1rem;
    border-radius: 0.5rem;
    font-family: var(--font-family-monospace);
    font-size: 0.875rem;
    line-height: 1.5;
    white-space: pre;
    overflow-x: auto;
    background: var(--color-code-bg);
  }

  code {
    font-family: var(--font-family-monospace);
    font-size: 0.9em;
  }

  figure {
    margin: 1.25rem 0;
  }

  figcaption {
    margin-top: 0.5rem;
    font-size: 14px;
    line-height: 1.4;
    text-align: center;
    color: var(--color-text-secondary);
  }

  hr {
    width: 30%;
    margin: 2rem auto;
    border: none;
    border-top: 1px solid var(--color-dividers);
  }
}

.ArticleReader-heading {
  margin: 0 0 0.5rem;
  font-size: 26px;
  font-weight: 700;
  line-height: 1.25;
}

.ArticleReader-byline {
  display: flex;
  flex-wrap: wrap;
  gap: 0 0.75rem;
  margin-bottom: 1.5rem;
  font-size: 15px;
  color: var(--color-text-secondary);
}

.ArticleMedia {
  display: block;
  width: 100%;
  max-height: 80vh;
  margin: 0 auto 1rem;
  border-radius: 0.5rem;
  background: var(--color-background-secondary);
}

.ArticleMedia-photo {
  padding: 0;
  cursor: zoom-in;
  overflow: hidden;

  img {
    display: block;
    width: 100%;
    height: auto;
    max-height: 80vh;
    object-fit: contain;
  }
}

.ArticleMedia-placeholder {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 0.5rem 1rem;
  min-height: 8rem;
  margin: 0 0 1rem;
  padding: 1rem;
  border-radius: 0.5rem;
  font-size: 15px;
  color: var(--color-text-secondary);
  background: var(--color-background-secondary);
}

.ArticleMedia-retry {
  padding: 0.25rem 0.75rem;
  border-radius: var(--border-radius-button-tiny);
  font-size: 0.875rem;
  font-weight: var(--font-weight-medium);
  color: #fff;
  background: var(--color-primary);
}

.ArticleEmbed {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin: 0 0 1rem;
  padding: 0.75rem 1rem;
  border: 1px solid var(--color-borders);
  border-radius: 0.5rem;
  text-decoration: none !important;
  color: var(--color-text) !important;

  &:hover {
    background: var(--color-interactive-element-hover);
  }
}

.ArticleEmbed-icon {
  flex-shrink: 0;
  color: var(--color-primary);
}

.ArticleEmbed-text {
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.ArticleEmbed-source {
  font-weight: var(--font-weight-semibold);
}

.ArticleEmbed-url {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 14px;
  color: var(--color-text-secondary);
}
```

（`fade-in` 关键帧已在 `ui/ui.scss` 全局定义。≤600px 时 `#MiddleColumn` 本身就是 `position: absolute; inset: 0` 的全屏层（`layout.scss`），阅读页 `inset: 0` 即全屏；不要改成 `position: fixed`——`#MiddleColumn` 带 `transform`，fixed 会相对它定位，反而容易出错。）

- [ ] **Step 7: 接入气泡、中栏与路由**

`web/src/components/message/MessageBubble.tsx`：import 加 `import { ArticleCard } from '../article/ArticleCard';`；`.text-content` 里 `RichText` 之后、`MessageMeta` 之前加一行：

```tsx
            {caption ? <RichText text={caption.text} entities={caption.entities} /> : <span class="unsupported">不支持的消息类型</span>}
            {!album && head.article && <ArticleCard msg={head} />}
            <MessageMeta date={last.date} editDate={editDate} variant="inline" />
```

`web/src/components/middle/MiddleColumn.tsx`：import 加 `import { ArticleReader } from '../article/ArticleReader';`；组件改为：

```tsx
/** The chat; with articleId, the article reader on top of it. */
export function MiddleColumn({ chatId, articleId = 0 }: { chatId: number; articleId?: number }) {
```

并在 `<MessageList key={chatId} chatId={chatId} />` 之后加：

```tsx
      {articleId > 0 && <ArticleReader key={articleId} chatId={chatId} messageId={articleId} />}
```

`web/src/App.tsx`：router import 加 `routeChatId`；

```tsx
  const chatId = routeChatId(r);
  const articleId = r.name === 'article' ? r.messageId : 0;
```

```tsx
        <MiddleColumn chatId={chatId} articleId={articleId} />
```

（`chatId` 在阅读页时仍是所在会话，因此共享媒体面板、左栏状态、`lastChatId` 都不变；`store.viewer` 随路由变化关闭的既有 effect 保持不动。）

`web/src/components/left/ChatsPanel.tsx`：router import 改为 `import { navigate, route, routeChatId } from '../../lib/router';`，选中项改为：

```tsx
  const selectedId = routeChatId(r);
```

- [ ] **Step 8: 运行，确认通过**

Run: `cd web && npm test`
Expected: 全部通过

- [ ] **Step 9: 构建门禁**

Run: `cd web && npm run build`
Expected: 成功

- [ ] **Step 10: Commit**

```bash
git add web/src
git commit -m "feat(web): Telegraph article card and Instant View style reader"
```

---

### Task 10: 文档 —— README 与上位 spec 接口表

**Files:**
- Modify: `README.md`、`docs/superpowers/specs/2026-10-04-tgarchive-design.md`

**Interfaces:**
- Consumes: Task 7 的接口与环境变量
- Produces: 无代码

- [ ] **Step 1: README**

开头介绍段落之后（「单个 Go 二进制……」那段之前）加一段：

```markdown
消息整条就是一个 Telegraph 文章链接（`telegra.ph` / `graph.org`）时，文章正文与其中的图片、视频会离线存档，WebUI 在该消息下显示文章卡片，点开为仿 Instant View 的阅读页。设计见 [`docs/superpowers/specs/2026-10-05-telegraph-archive-design.md`](docs/superpowers/specs/2026-10-05-telegraph-archive-design.md)。文章图片从原站直接下载，容器需要能访问外网。
```

「环境变量」表末尾加一行：

```markdown
| `TELEGRAPH_API_URL` | `https://api.telegra.ph` | Telegraph API 地址，仅测试时指向假服务器 |
```

- [ ] **Step 2: 上位 spec §7 接口表**

`docs/superpowers/specs/2026-10-04-tgarchive-design.md` §7 表格，在 `GET /api/messages/:id` 一行之后加：

```markdown
| GET | `/api/messages/:id/article` | 该链接消息存档的 Telegraph 文章（正文节点树与文章媒体状态）；无文章或消息已删 404。见 `2026-10-05-telegraph-archive-design.md` §7 |
```

- [ ] **Step 3: 确认没有动部署文件**

Run: `git diff --name-only main... -- Dockerfile .github .dockerignore`
Expected: 无输出

- [ ] **Step 4: 最终门禁**

Run: `go vet ./... && go test ./... && gofmt -l cmd internal && (cd web && npm test && npm run build)`
Expected: 全部通过，gofmt 无输出

- [ ] **Step 5: Commit**

```bash
git add README.md docs/superpowers/specs/2026-10-04-tgarchive-design.md
git commit -m "docs: Telegraph archiving in README; GET /api/messages/:id/article in the API table"
```

---

## 交付后

- 发布与部署按计划 4 的流程另行进行（本计划不打 tag、不改 compose）。
- 上线后验收（spec §1 成功标准与 §10 末条）：发一条 `https://telegra.ph/<path>` → 👀 → 👌；断开原站后阅读页仍完整；发不存在的文章 → `⚠️ 存档失败：文章不存在`；手机 390×844 浅 / 深色与 800px 宽下检查卡片、阅读页全屏、系统返回键关闭、图片点开可在本文媒体间左右切换。
