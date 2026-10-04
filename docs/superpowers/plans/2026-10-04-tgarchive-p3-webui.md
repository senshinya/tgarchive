# tgarchive 计划 3：Preact WebUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 Preact 实现与 Telegram Web A 视觉一致的只读存档 WebUI（会话列表、全类型消息渲染、共享媒体、媒体查看器、删除与重试、SSE 实时更新、浅/深色、移动端单栏）和管理页（机器人、白名单、最近被拒绝、API 凭据、用户账号登录），构建产物由 Go 二进制 `go:embed` 提供。

**Architecture:** `web/` 是独立的 Vite + TypeScript 工程，`npm run build` 输出到 `web/dist`，沿用现有 `web/web.go` 的 `//go:embed all:dist`。状态集中在 `createStore(api)` 返回的一组 `@preact/signals`（经 `StoreContext` 注入，测试用假 `Api`）；纯逻辑（实体树、相册拼图、分组、波形、格式化、路由、SSE）放 `src/lib/` 并逐一单测；组件按区域分目录（left / middle / message / media / right / viewer / settings / ui）。SSE 事件只带 id，前端用新增的 `GET /api/messages/{id}` 取单条消息增量更新；断线重连后整体 resync。

**Tech Stack:** Preact 11.0.0、@preact/signals 2.11.3、lucide-preact 1.52.0（图标）、lottie-web 5.13.0（`.tgs` 贴纸，懒加载）、Vite 8.3.2（oxc 处理 JSX，无 Babel 插件）、TypeScript 7.0.2、Sass 1.105.1、Vitest 5.0.3 + jsdom 30.1.2 + @testing-library/preact 3.2.4；Go 侧仅标准库改动。

**Spec:** `docs/superpowers/specs/2026-10-04-tgarchive-design.md`（§7 HTTP 接口、§8 WebUI）。视觉数值来源：`.superpowers/research/webA-design.md`（Web A 设计令牌、布局、气泡、配色、深色主题、图标建议）。

**前置：** 计划 2 已合入 `main`（`/api/admin/userbot[...]` 由其 Task 8 提供；接口缺失时用户账号页显示「当前服务未启用用户账号功能」，其余功能不受影响）。在新分支 `feat/p3-webui` 上执行。

**本计划不含**：Dockerfile / compose / Caddy / 上线（计划 4）。

## 裁定（与 spec 的出入）

- **不移植 Web A 代码**：spec §8 写「从 Web A 源码移植 SCSS / 组件 / 图标字体 / 壁纸图案 / 相册算法」，但 telegram-tt 为 GPL-3.0 且图标字体是其专有资产。本计划只按 research 文档中的事实数值（颜色、尺寸、圆角、时长、断点）从零实现；图标用 Lucide（ISC）；壁纸图案是本仓库原创的 `web/public/pattern.svg`；相册拼图按文档描述的行为自行编写。Task 14 同步 spec §8 的「基准」段落。spec §2 的「许可 GPL-3.0（移植了…）」一行不在本计划改动范围内，由站长决定。
- **新增 `GET /api/messages/{id}`，`MessageView` 增加 `chat_id`**（Task 1）：SSE 事件只带 `chat_id` / `message_id` / `message_ids`，前端没有取单条消息的接口就无法增量更新；`media.updated` 不带 `chat_id`，所以消息视图本身要带上。
- **缩略图**：spec §7 的 `/media/:id/thumb` 实际不存在，缩略图是 `role = "thumb"` 的独立 media 记录，前端一律请求 `/media/<thumb 的 id>`（计划 2 Task 9 已同步 spec）。
- **添加机器人「分步进度」**：`POST /api/admin/bots` 在全部步骤完成后一次返回 `steps`，不是流式；前端提交期间显示加载状态与说明，返回后逐步列出结果（含失败步骤）。
- **位置卡片**：不加载第三方地图瓦片（避免把坐标泄露给瓦片服务），显示带图钉的静态卡片，点击在 OpenStreetMap 打开。
- **会话列表不显示未读数**：spec 未要求，后端无未读状态。
- **白名单行只显示备注与 user ID**：白名单接口不返回发送者姓名。
- **userbot 代取消息的「回复」引用**：后端只在 `source = bot_update` 内查找被回复消息，代取消息的回复统一显示「原消息未存档」。

## Global Constraints

- 前端源码全部位于 `web/`；`npm run build` 输出 `web/dist`；`web/web.go` 的 `//go:embed all:dist` 不改；`web/dist/*` 被 git 忽略，只跟踪 `web/dist/.gitkeep`（构建脚本每次重建它，保证 fresh checkout 下 `go build` / `go test ./...` 能编译）
- Node ≥ 22.12（Vite 8 要求）；依赖版本精确锁定（`package.json` 无 `^` / `~`，`web/.npmrc` 设 `save-exact=true`），提交 `web/package-lock.json`；只允许本计划列出的依赖：`preact 11.0.0`、`@preact/signals 2.11.3`、`lucide-preact 1.52.0`、`lottie-web 5.13.0`；dev：`vite 8.3.2`、`vitest 5.0.3`、`jsdom 30.1.2`、`@testing-library/preact 3.2.4`、`typescript 7.0.2`、`sass 1.105.1`
- 不复制 telegram-tt 的任何源码、SCSS、图标字体或图片资产；视觉数值只取自 `.superpowers/research/webA-design.md`；图标只用 `lucide-preact`
- Preact 11 写法：DOM 属性用 `class`（不用 `className`）；`useRef` 必须传初值；`<input type>` 在 TS 下按 `'text'` 断言（Preact 11 的 ARIA 类型按 type 收窄）
- 所有界面文案为简体中文，逐字使用本计划代码中的文案
- 主题只跟随 `prefers-color-scheme`（浅 / 深两套令牌在 `src/styles/tokens.scss`），不提供手动切换，不使用 localStorage
- 断点：≤600px 手机单栏、≤925px 左栏变为覆盖层、≥1276px 右栏固定宽度并挤压中栏
- 应用自身没有登录界面（Caddy forward_auth 在前）；`fetch` 一律 `redirect: 'error'`，401 与网络错误统一提示「网络错误或登录已过期，请刷新页面」
- 写请求：带 JSON 体的请求必须带 `Content-Type: application/json`；无体的 POST / DELETE 不带请求体和 Content-Type（后端 CSRF 兜底规则）
- 消息分页 50 条（`PAGE_SIZE`），共享媒体与查看器分页 100 条（后端上限）
- 测试：Vitest + @testing-library/preact（jsdom），不用 jest-dom；测试文件与源码同目录 `*.test.ts(x)`；组件测试经 `src/test/render.tsx` 的 `renderWithStore` 注入假 `Api`
- 每个任务结束时 `cd web && npm test && npm run build` 必须通过；改动 Go 文件的任务还要 `go test ./...`、`gofmt -l internal` 无输出

## Review Focus

1. **Authelia 会话过期**（fetch 被 302 到登录页、SSE 被关闭）→ 用户看到「网络错误或登录已过期，请刷新页面」，SSE 每 5 秒重连、连上后整体 resync，而不是静默卡死 → Task 3 测试 `maps 401 and network failures to the re-login hint`、Task 4 测试 `opens a new connection when the old one is closed for good`、Task 14 测试 `resyncs after the event stream reconnects`
2. **实时事件的边界**：未打开会话的新消息只刷新列表、`media.updated` 的 `message_ids` 为 `null`、编辑一条比已加载窗口更旧的消息、被删消息再次收到更新（404）→ 不报错、不插出空洞、不残留 → Task 5 测试 `appends created messages…`、`refreshes only loaded messages on media.updated and tolerates null ids`、`does not insert an edited message older than the loaded window`、`removes deleted messages and drops updated ones that 404`
3. **第一页填不满屏幕**（消息很少或屏幕很高时不会触发 scroll 事件）→ 自动继续加载更早消息直到填满或到头；向上翻页时视口不跳 → Task 10 测试 `keeps loading older pages while the content does not fill the viewport`
4. **恶意 / 刁钻文本**：`text_link` 指向 `javascript:`、含 emoji 的 UTF-16 偏移、互相交叉的实体 → 链接被降级为纯文本、格式落在正确字符上 → Task 7 测试 `rejects script and data URLs`、`uses UTF-16 offsets…`、`splits partially overlapping entities…`、`renders safe links and neutralises unsafe ones`
5. **媒体尚未落盘**（`pending` / `failed` / `too_large`）出现在气泡、文件行、共享媒体网格、查看器里 → 各处显示状态而不是坏图，`failed` 可重试，查看器跳过未落盘媒体、目标不可用时自行关闭 → Task 9 测试 `shows pending and too_large placeholders`、`retries failed media through the store`、`shows failure with retry and the too_large note`；Task 12 测试 `closes itself when the target media is not available`

---

## 文件结构

```
internal/store/query.go                 （改）MessageView.chat_id；GetMessageView
internal/httpapi/server.go / read.go    （改）GET /api/messages/{id}
.gitignore                              （改）忽略 web/dist/*，保留 .gitkeep
web/web.go                              （改）包注释
web/package.json / package-lock.json / .npmrc / vite.config.ts / tsconfig.json / index.html
web/scripts/keep-dist.mjs               构建后重建 dist/.gitkeep
web/public/favicon.svg / pattern.svg    图标与原创壁纸图案
web/src/main.tsx / App.tsx / layout.scss  入口、三栏外壳、响应式布局
web/src/styles/                         tokens（浅/深令牌）、global、index
web/src/api/                            types（后端 JSON 形状）、client（fetch 封装、URL 构造）
web/src/lib/                            format、router、sse、entities、grouping、album、waveform（纯逻辑）
web/src/state/store.ts                  signals 状态 + 事件处理 + StoreContext
web/src/test/                           setup、fixtures（假 Api 与数据构造）、render（renderWithStore）
web/src/ui/                             Avatar、Button/IconButton、Switch、Checkbox、InputField、ListItem、Tabs、Modal/ConfirmDialog、ContextMenu、Toast、Spinner
web/src/components/message/             RichText、MessageParts（时间/转发/来源/回复/尾巴）、MessageBubble、MessageList
web/src/components/media/               Photo、Video、Animation、Album、Document、Audio、Voice、Sticker/TgsSticker、VideoNote、Cards（位置/联系人/投票/骰子）、MediaStatus、MessageMedia
web/src/components/middle/              MiddleColumn（顶栏 + 壁纸 + 消息流）
web/src/components/left/                ChatsPanel（机器人切换、会话列表、管理入口）
web/src/components/right/               SharedMedia（媒体 / 文件 / 链接，按月分组）
web/src/components/viewer/              MediaViewer（全屏、左右切换、缩放、下载）
web/src/components/settings/            SettingsPanel、SettingsHome、AddBot、BotSettings、TelegramAppSettings、UserbotSettings
```

---

### Task 1: 后端 —— `GET /api/messages/{id}` 与 `MessageView.chat_id`

**Files:**
- Modify: `internal/store/query.go`（`MessageView` 加 `ChatID`；`msgCols` 与 `scanMessageView` 加 `chat_id`；新增 `GetMessageView`）
- Modify: `internal/httpapi/server.go`（注册路由）
- Modify: `internal/httpapi/read.go`（`getMessage` 处理器）
- Test: `internal/store/query_test.go`、`internal/httpapi/read_test.go`

**Interfaces:**
- Consumes: 既有 `collectViews`、`hydrate`、`ErrNotFound`、`storeErr`、`pathID`
- Produces:
  - `func (s *Store) GetMessageView(ctx context.Context, id int64) (MessageView, error)`：未删除消息（含 media 与 reply 预览）；不存在或已软删 → `ErrNotFound`
  - `MessageView` 新字段 `ChatID int64` → JSON `"chat_id"`，所有返回消息的接口（`/api/chats/{id}/messages`、`/api/chats/{id}/media`、新接口）都带上
  - HTTP `GET /api/messages/{id}`：200 `MessageView`；id 非法 400 `{"error":"bad message id"}`；不存在 / 已删 404 `{"error":"not found"}`

- [ ] **Step 1: 写失败测试**

`internal/store/query_test.go` 文件末尾追加：

```go
func TestGetMessageView(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	first := ingest(t, s, bot, textMsg(1, "hello"))
	reply := textMsg(2, "re")
	reply.ReplyToTgMessageID = 1
	second := ingest(t, s, bot, reply)
	photo := ingest(t, s, bot, photoMsg(3, "bot:p"))

	v, err := s.GetMessageView(ctx, second.MessageID)
	if err != nil || v.ID != second.MessageID || v.ChatID != first.ChatID || v.Text != "re" {
		t.Fatalf("view = %+v, %v", v, err)
	}
	if v.Reply == nil || v.Reply.ID != first.MessageID || v.Reply.Text != "hello" {
		t.Fatalf("reply = %+v", v.Reply)
	}
	if pv, _ := s.GetMessageView(ctx, photo.MessageID); len(pv.Media) != 1 || pv.Media[0].State != StatePending {
		t.Fatalf("media not hydrated: %+v", pv.Media)
	}
	page, _ := s.ListMessages(ctx, first.ChatID, 0, 10)
	if page[0].ChatID != first.ChatID {
		t.Fatalf("list views must carry chat_id: %+v", page[0])
	}
	if _, _, err := s.DeleteMessage(ctx, first.MessageID, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessageView(ctx, first.MessageID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted message err = %v", err)
	}
	if _, err := s.GetMessageView(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing message err = %v", err)
	}
}
```

`internal/httpapi/read_test.go` 文件末尾追加：

```go
func TestGetMessage(t *testing.T) {
	e := newReadEnv(t)
	w := do(e.h, "GET", fmt.Sprintf("/api/messages/%d", e.photoMsg), nil)
	var v store.MessageView
	if err := json.Unmarshal(w.Body.Bytes(), &v); w.Code != 200 || err != nil {
		t.Fatalf("get = %d %s", w.Code, w.Body)
	}
	if v.ID != e.photoMsg || v.ChatID != e.chat || len(v.Media) != 1 || v.Media[0].ID != e.media {
		t.Fatalf("view = %+v", v)
	}
	if !strings.Contains(w.Body.String(), fmt.Sprintf(`"chat_id":%d`, e.chat)) {
		t.Fatalf("chat_id missing from JSON: %s", w.Body)
	}
	if w := do(e.h, "GET", "/api/messages/abc", nil); w.Code != 400 {
		t.Fatalf("bad id = %d", w.Code)
	}
	if w := do(e.h, "DELETE", fmt.Sprintf("/api/messages/%d", e.photoMsg), nil); w.Code != 204 {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := do(e.h, "GET", fmt.Sprintf("/api/messages/%d", e.photoMsg), nil); w.Code != 404 || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("deleted = %d %s", w.Code, w.Body)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/store/ ./internal/httpapi/`
Expected: FAIL（编译错误：`s.GetMessageView undefined`、`v.ChatID undefined`）

- [ ] **Step 3: 实现**

`internal/store/query.go`：

1. `MessageView` 结构体在 `ID` 字段之后插入一行：

```go
	ChatID             int64           `json:"chat_id"`
```

2. `msgCols` 改为（只在开头加 `chat_id`）：

```go
const msgCols = `id, chat_id, tg_message_id, source, media_group_id, date, edit_date, kind, text, entities_json, forward_origin_json,
	reply_to_tg_message_id, origin_chat_title, origin_link, extra_json`
```

3. `scanMessageView` 中的 `r.Scan(` 调用改为（`&v.ID` 之后加 `&v.ChatID`）：

```go
	err := r.Scan(&v.ID, &v.ChatID, &v.TgMessageID, &v.Source, &v.MediaGroupID, &v.Date, &v.EditDate, &v.Kind, &v.Text, &ents, &fwd,
		&v.ReplyToTgMessageID, &v.OriginChatTitle, &v.OriginLink, &extra)
```

4. 在 `func (s *Store) ListChatMedia(` 之前插入：

```go
// GetMessageView returns one non-deleted message with its media and reply preview.
func (s *Store) GetMessageView(ctx context.Context, id int64) (MessageView, error) {
	views, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages WHERE id = ? AND deleted_at = 0`, id))
	if err != nil {
		return MessageView{}, err
	}
	if len(views) == 0 {
		return MessageView{}, ErrNotFound
	}
	if err := s.hydrate(ctx, views[0].ChatID, views); err != nil {
		return MessageView{}, err
	}
	return views[0], nil
}
```

`internal/httpapi/server.go` 的 `Handler()`：在 `mux.HandleFunc("DELETE /api/messages/{id}", s.deleteMessage)` 之前插入

```go
	mux.HandleFunc("GET /api/messages/{id}", s.getMessage)
```

`internal/httpapi/read.go`：在 `func (s *Server) deleteMessage(` 之前插入

```go
func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad message id")
		return
	}
	v, err := s.Store.GetMessageView(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./... && gofmt -l internal && go vet ./internal/store/ ./internal/httpapi/`
Expected: PASS，`gofmt` 与 `vet` 无输出

- [ ] **Step 5: 提交**

```bash
git add internal/store/query.go internal/store/query_test.go internal/httpapi/server.go internal/httpapi/read.go internal/httpapi/read_test.go
git commit -m "feat(httpapi): GET /api/messages/{id}; message views carry chat_id"
```

---

### Task 2: 前端工程脚手架、设计令牌与 embed 衔接

**Files:**
- Create: `web/package.json`、`web/.npmrc`、`web/vite.config.ts`、`web/tsconfig.json`、`web/index.html`、`web/scripts/keep-dist.mjs`
- Create: `web/public/favicon.svg`、`web/public/pattern.svg`
- Create: `web/src/test/setup.ts`、`web/src/styles/tokens.scss`、`web/src/styles/global.scss`、`web/src/styles/index.scss`
- Create: `web/src/App.tsx`、`web/src/main.tsx`（最小外壳，Task 14 整体替换）
- Test: `web/src/App.test.tsx`（Task 14 整体替换）
- Modify: `.gitignore`、`web/web.go`（包注释）
- Delete from git: `web/dist/index.html`（计划 1 的占位页）；Create: `web/dist/.gitkeep`

**Interfaces:**
- Produces: `npm test`（`vitest run`）、`npm run build`（`tsc --noEmit && vite build && node scripts/keep-dist.mjs`）、`npm run dev`（代理 `/api`、`/media`、`/avatars` 到 `http://127.0.0.1:8080`）；全局 CSS 变量（`--color-*`、`--peer-0..6`、`--border-radius-*`、`--shadow-*`、`--layer-transition` 等，见 tokens.scss）；`html.is-apple` / `html.is-ios` 类开关（Task 14 的 main.tsx 设置）
- 后续任务的组件各自 `import './xxx.scss'`，不改 `styles/index.scss`

- [ ] **Step 1: 写工程配置并安装依赖**


`web/package.json`：

```json
{
  "name": "tgarchive-web",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "engines": {
    "node": ">=22.12"
  },
  "scripts": {
    "dev": "vite",
    "build": "tsc --noEmit && vite build && node scripts/keep-dist.mjs",
    "test": "vitest run"
  },
  "dependencies": {
    "@preact/signals": "2.11.3",
    "lottie-web": "5.13.0",
    "lucide-preact": "1.52.0",
    "preact": "11.0.0"
  },
  "devDependencies": {
    "@testing-library/preact": "3.2.4",
    "jsdom": "30.1.2",
    "sass": "1.105.1",
    "typescript": "7.0.2",
    "vite": "8.3.2",
    "vitest": "5.0.3"
  }
}
```

`web/.npmrc`：

```
save-exact=true
```

`web/vite.config.ts`：

```ts
import { defineConfig } from 'vitest/config';

// `npm run dev` proxies the API to a local backend started with REQUIRE_FORWARD_AUTH=false.
const backend = 'http://127.0.0.1:8080';

export default defineConfig({
  oxc: { jsx: { runtime: 'automatic', importSource: 'preact' } },
  build: { outDir: 'dist', emptyOutDir: true, assetsDir: 'assets', target: 'es2022' },
  server: { proxy: { '/api': backend, '/media': backend, '/avatars': backend } },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
    setupFiles: ['src/test/setup.ts'],
  },
});
```

`web/tsconfig.json`：

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "jsx": "react-jsx",
    "jsxImportSource": "preact",
    "strict": true,
    "noEmit": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "skipLibCheck": true,
    "types": ["vite/client"]
  },
  "include": ["src"]
}
```

`web/index.html`：

```html
<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover" />
    <meta name="color-scheme" content="light dark" />
    <meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)" />
    <meta name="theme-color" content="#212121" media="(prefers-color-scheme: dark)" />
    <link rel="icon" href="/favicon.svg" type="image/svg+xml" />
    <title>tgarchive</title>
  </head>
  <body>
    <div id="app"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

`web/scripts/keep-dist.mjs`：

```js
// vite empties dist/ on every build; the tracked placeholder keeps `go:embed all:dist` compiling in fresh checkouts.
import { writeFileSync } from 'node:fs';

writeFileSync(new URL('../dist/.gitkeep', import.meta.url), '');
```

`web/public/favicon.svg`：

```xml
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><circle cx="32" cy="32" r="32" fill="#3390ec"/><path d="M18 24h28v6H18zM20 32h24v14a2 2 0 0 1-2 2H22a2 2 0 0 1-2-2zM28 36h8v3h-8z" fill="#fff"/></svg>
```

`web/public/pattern.svg`（原创壁纸图案，单色描边，作为 CSS mask 使用）：

```xml
<svg xmlns="http://www.w3.org/2000/svg" width="360" height="360" viewBox="0 0 360 360" fill="none" stroke="#000" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><circle cx="40" cy="44" r="14"/><path d="M96 24l8 16 18 3-13 12 3 18-16-8-16 8 3-18-13-12 18-3z"/><path d="M170 40c10-14 30-6 26 10-4 12-26 26-26 26s-22-14-26-26c-4-16 16-24 26-10z"/><rect x="236" y="26" width="34" height="26" rx="6"/><path d="M244 52v10l10-10"/><path d="M314 30v34m0-34l20-6v30"/><circle cx="308" cy="64" r="6"/><circle cx="328" cy="58" r="6"/><path d="M30 120c20-20 40 20 60 0"/><circle cx="140" cy="122" r="22"/><path d="M130 116h0m20 0h0M130 132c6 6 14 6 20 0"/><path d="M206 104l28 28m0-28l-28 28"/><rect x="262" y="104" width="44" height="30" rx="4"/><circle cx="284" cy="119" r="8"/><path d="M330 110l14 24h-28z"/><path d="M28 200l20-14 20 14v24H28z"/><path d="M92 196h40m-40 12h28m-28 12h36"/><circle cx="178" cy="206" r="8"/><circle cx="178" cy="206" r="20"/><path d="M226 188c0 22 36 22 36 0"/><path d="M226 222h36"/><path d="M296 186l24 40m-12-40l24 40"/><path d="M36 268a14 14 0 1 1 28 0c0 10-14 26-14 26s-14-16-14-26z"/><circle cx="50" cy="268" r="4"/><path d="M98 258h44v28H98zm0 0l22 16 22-16"/><path d="M172 290l16-36 16 36m-26-12h20"/><circle cx="252" cy="272" r="18"/><path d="M252 262v10l8 6"/><path d="M300 258c14 0 24 10 24 24m-24-12c7 0 12 5 12 12"/><circle cx="300" cy="282" r="3"/><path d="M60 330h30m-15-15v30"/><path d="M130 320c8-8 24-8 32 0s-8 24-16 24-24-16-16-24z"/><path d="M214 316h40l-6 30h-28z"/><path d="M288 330l12 12 26-26"/></svg>
```

`web/src/test/setup.ts`：

```ts
import { cleanup } from '@testing-library/preact';
import { afterEach } from 'vitest';

afterEach(() => cleanup());

// jsdom does not implement media playback or scrolling into view.
HTMLMediaElement.prototype.play = function play() {
  return Promise.resolve();
};
HTMLMediaElement.prototype.pause = function pause() {};
Element.prototype.scrollIntoView = function scrollIntoView() {};
```

Run: `cd web && npm install`
Expected: 生成 `web/package-lock.json` 与 `web/node_modules/`，无 peer 依赖错误；`npm ls preact` 显示 `preact@11.0.0`

- [ ] **Step 2: 写失败测试**

`web/src/App.test.tsx`：

```tsx
import { render } from '@testing-library/preact';
import { describe, expect, it } from 'vitest';
import { App } from './App';

describe('App shell', () => {
  it('renders the main container', () => {
    const { container } = render(<App />);
    expect(container.querySelector('#Main')).toBeTruthy();
  });
});
```

- [ ] **Step 3: 运行确认失败**

Run: `cd web && npm test`
Expected: FAIL（`Failed to resolve import "./App"`）

- [ ] **Step 4: 实现外壳与令牌**

`web/src/App.tsx`：

```tsx
/** Minimal shell so the toolchain builds end to end; Task 14 replaces it with the three-column app. */
export function App() {
  return (
    <div id="Main">
      <p class="boot">tgarchive</p>
    </div>
  );
}
```

`web/src/main.tsx`：

```tsx
import { render } from 'preact';
import { App } from './App';
import './styles/index.scss';

render(<App />, document.getElementById('app')!);
```


`web/src/styles/tokens.scss`：

```scss
// Design tokens. Values restate the documented Telegram Web A visual facts
// (.superpowers/research/webA-design.md); no Web A source is copied.

@mixin light-theme {
  color-scheme: light;
  --color-primary: #3390ec;
  --color-primary-shade: #2a7fd6;
  --color-primary-tint: rgba(51, 144, 236, 0.08);
  --color-background: #ffffff;
  --color-background-secondary: #f4f4f5;
  --color-background-secondary-accent: #e4e4e5;
  --color-background-selected: #f4f4f5;
  --color-chat-hover: #f4f4f5;
  --color-chat-active: #3390ec;
  --color-item-hover: #f4f4f5;
  --color-item-active: #ededed;
  --color-text: #000000;
  --color-text-secondary: #707579;
  --color-text-meta: #686c72;
  --color-text-green: #4fae4e;
  --color-borders: #dadce0;
  --color-borders-input: #dadce0;
  --color-dividers: #c8c6cc;
  --color-links: #3390ec;
  --color-placeholders: #a2acb4;
  --color-gray: #c4c9cc;
  --color-active: #00c73e;
  --color-code: #4a729a;
  --color-code-bg: rgba(112, 117, 121, 0.08);
  --color-chat-username: #3c7eb0;
  --color-default-shadow: #72727240;
  --color-toast-background: #202020cc;
  --color-interactive-element-hover: rgba(112, 117, 121, 0.08);
  --wallpaper-1: #bdcd8c;
  --wallpaper-2: #8eba89;
  --wallpaper-3: #83b28f;
  --wallpaper-4: #c5d3b0;
  --action-message-bg: #4a8e3a8c;
}

@mixin dark-theme {
  color-scheme: dark;
  --color-primary: #8774e1;
  --color-primary-shade: #7b67db;
  --color-primary-tint: rgba(135, 116, 225, 0.12);
  --color-background: #212121;
  --color-background-secondary: #0f0f0f;
  --color-background-secondary-accent: #191919;
  --color-background-selected: #2c2c2c;
  --color-chat-hover: #2c2c2c;
  --color-chat-active: #766ac8;
  --color-item-hover: #2c2c2c;
  --color-item-active: #292929;
  --color-text: #ffffff;
  --color-text-secondary: #aaaaaa;
  --color-borders: #303030;
  --color-borders-input: #5b5b5a;
  --color-dividers: #3b3b3d;
  --color-links: #8774e1;
  --color-gray: #717579;
  --color-active: #8774e1;
  --color-code: #8774e1;
  --color-code-bg: #00000080;
  --color-chat-username: #e9eef4;
  --color-default-shadow: #1010109c;
  --color-toast-background: #000000cc;
  --color-interactive-element-hover: rgba(170, 170, 170, 0.08);
  --wallpaper-1: #4f5bd5;
  --wallpaper-2: #962fbf;
  --wallpaper-3: #dd6cb9;
  --wallpaper-4: #fec496;
  --action-message-bg: #48576166;
}

:root {
  --font-family: "Roboto", -apple-system, BlinkMacSystemFont, "Apple Color Emoji", "Segoe UI", Oxygen, Ubuntu,
    Cantarell, "Fira Sans", "Droid Sans", "Helvetica Neue", sans-serif;
  --font-family-monospace: "Cascadia Mono", "Roboto Mono", "Droid Sans Mono", "SF Mono", "Menlo", "Ubuntu Mono",
    "Consolas", monospace;
  --font-family-rounded: "Nunito", "Roboto", "Helvetica Neue", sans-serif;
  --font-weight-normal: 400;
  --font-weight-medium: 500;
  --font-weight-semibold: 500;
  --message-text-size: 1rem;

  --color-error: #e53935;
  --color-warning: #fb8c00;
  --color-green: #00c73e;
  --color-white: #ffffff;
  --color-scrollbar: rgba(90, 90, 90, 0.3);
  --color-scrollbar-code: rgba(200, 200, 200, 0.3);
  --color-deleted-account: #9eaab5;

  --peer-0: #d45246;
  --peer-1: #f68136;
  --peer-2: #6c61df;
  --peer-3: #46ba43;
  --peer-4: #5caffa;
  --peer-5: #408acf;
  --peer-6: #d95574;

  --border-radius-button: 28px;
  --border-radius-button-tiny: 18px;
  --border-radius-modal: 32px;
  --border-radius-toast: 16px;
  --border-radius-island: 24px;
  --border-radius-default: 16px;
  --border-radius-default-small: 10px;
  --border-radius-default-tiny: 6px;
  --border-radius-messages: 15px;
  --border-radius-messages-small: 6px;

  --shadow-island: 0 1px 4px 0 #0000000d;
  --shadow-pane: 0 1px 5px -1px rgba(0, 0, 0, 0.21);
  --shadow-chat-list-panel: 0 1px 8px 0 rgba(0, 0, 0, 0.1);
  --shadow-bubble: 0 1px 2px var(--color-default-shadow);

  --layer-transition: 300ms cubic-bezier(0.33, 1, 0.68, 1);
  --slide-transition: 300ms cubic-bezier(0.25, 1, 0.5, 1);
  --select-transition: 200ms ease-out;
  --chat-transform-transition: 0.2s ease-out;

  --column-header-height: 4rem;
  --middle-header-height: 3rem;
  --middle-panel-inline-padding: 1rem;
  --messages-container-width: 47.5rem;
  --right-column-width: 26.5rem;

  @include light-theme;
}

@media (prefers-color-scheme: dark) {
  :root {
    @include dark-theme;
  }
}

html.is-apple {
  --font-family: system-ui, -apple-system, BlinkMacSystemFont, "Roboto", "Apple Color Emoji", "Helvetica Neue",
    sans-serif;
  --font-weight-semibold: 600;
  --color-text-secondary: #8e8e92;
}

@media (prefers-color-scheme: dark) {
  html.is-apple {
    --color-text-secondary: #aaaaaa;
  }
}

html.is-ios {
  --border-radius-messages: 16px;
  --border-radius-messages-small: 8px;
  --message-text-size: 1.0625rem;
}

@media (max-width: 600px) {
  :root {
    --middle-panel-inline-padding: 0.5rem;
  }
}

@media (min-width: 1276px) and (max-width: 1920px) {
  :root {
    --right-column-width: 25vw;
  }
}

@media (min-width: 1921px) {
  :root {
    --messages-container-width: 50vw;
  }
}
```

`web/src/styles/global.scss`：

```scss
*,
*::before,
*::after {
  box-sizing: border-box;
}

html,
body,
#app {
  height: 100%;
  margin: 0;
}

html {
  font-size: 16px;
}

body {
  font-family: var(--font-family);
  font-size: 1rem;
  font-weight: var(--font-weight-normal);
  line-height: 1.5;
  color: var(--color-text);
  background: var(--color-background-secondary);
  overflow: hidden;
  -webkit-font-smoothing: antialiased;
  -webkit-tap-highlight-color: transparent;
}

button {
  font: inherit;
  color: inherit;
  background: none;
  border: none;
  padding: 0;
  cursor: pointer;
}

a {
  color: var(--color-links);
  text-decoration: none;

  &:hover {
    text-decoration: underline;
  }
}

h1,
h2,
h3,
p {
  margin: 0;
}

:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 1px;
}

.custom-scroll {
  overflow-y: auto;
  scrollbar-width: thin;
  scrollbar-color: transparent transparent;
  transition: scrollbar-color 0.3s;

  &:hover,
  &:focus,
  &:focus-within {
    scrollbar-color: var(--color-scrollbar) transparent;
  }

  &::-webkit-scrollbar {
    width: 0.375rem;
    height: 0.375rem;
  }

  &::-webkit-scrollbar-thumb {
    border-radius: 0.375rem;
    background: transparent;
  }

  &:hover::-webkit-scrollbar-thumb {
    background: var(--color-scrollbar);
  }
}

.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}
```

`web/src/styles/index.scss`：

```scss
@use "tokens";
@use "global";
```

`.gitignore` 末尾追加两行：

```
web/dist/*
!web/dist/.gitkeep
```

`web/web.go` 第一行包注释改为：

```go
// Package web embeds the built SPA: web/dist is produced by `npm run build` (only dist/.gitkeep is tracked).
```

移除计划 1 的占位页并跟踪 `.gitkeep`：

```bash
git rm --cached web/dist/index.html
rm -f web/dist/index.html
mkdir -p web/dist && touch web/dist/.gitkeep
```

- [ ] **Step 5: 运行确认通过**

Run: `cd web && npm test && npm run build && ls -a dist && cd .. && go test ./...`
Expected: 1 个测试 PASS；`dist/` 下有 `.gitkeep`、`index.html`、`assets/`、`favicon.svg`、`pattern.svg`；Go 测试全部 PASS

- [ ] **Step 6: 提交**

```bash
git add .gitignore web/web.go web/package.json web/package-lock.json web/.npmrc web/vite.config.ts web/tsconfig.json web/index.html \
  web/scripts web/public web/src web/dist/.gitkeep
git commit -m "feat(web): Vite + Preact scaffold, Web A design tokens, dist embed placeholder"
```

---

### Task 3: API 类型与客户端

**Files:**
- Create: `web/src/api/types.ts`、`web/src/api/client.ts`
- Test: `web/src/api/client.test.ts`

**Interfaces:**
- Produces（`types.ts`）：`Bot`、`Sender`、`Chat`、`MediaState`、`Media`（`waveform?: string` 为 base64）、`Entity`、`ForwardOrigin`、`ReplyRef`、`MessageKind`、`Message`（含 `chat_id`）、`SharedMediaType = 'media' | 'file' | 'link'`、`WhitelistEntry`、`RejectedSender`、`AddBotStep`、`AddBotResult`、`TelegramApp`、`UserbotState`、`UserbotInfo`、`MessageRef`、`ArchiveEvent`（判别联合）、`EVENT_TYPES`
- Produces（`client.ts`）：`PAGE_SIZE = 50`；`class ApiError extends Error { status: number; body: unknown }`；`NETWORK_ERROR`；`interface Api`（21 个方法，签名见代码）；`const api: Api`；`mediaUrl(id: number, download = false): string`；`avatarUrl(kind: 'bots' | 'senders', tgId: number): string`；`errorMessage(err: unknown): string`

- [ ] **Step 1: 写失败测试**

`web/src/api/client.test.ts`：

```ts
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, NETWORK_ERROR, api, avatarUrl, errorMessage, mediaUrl } from './client';

type Call = { url: string; init: RequestInit };

function mockFetch(status: number, body?: unknown): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, init });
      const text = body === undefined ? '' : typeof body === 'string' ? body : JSON.stringify(body);
      return new Response(status === 204 ? null : text, { status });
    }),
  );
  return calls;
}

afterEach(() => vi.unstubAllGlobals());

describe('api client', () => {
  it('builds read URLs with cursor and limit', async () => {
    const calls = mockFetch(200, []);
    await api.messages(7);
    await api.messages(7, 120, 50);
    await api.chatMedia(7, 'file', 33);
    await api.chats();
    expect(calls.map((c) => c.url)).toEqual([
      '/api/chats/7/messages?limit=50',
      '/api/chats/7/messages?before=120&limit=50',
      '/api/chats/7/media?type=file&before=33&limit=50',
      '/api/chats',
    ]);
    expect(calls[0].init.method).toBe('GET');
    expect(calls[0].init.body).toBeUndefined();
  });

  it('sends JSON bodies with the JSON content type the CSRF guard requires', async () => {
    const calls = mockFetch(204);
    await api.putWhitelist(3, 42, '朋友', true);
    expect(calls[0].url).toBe('/api/admin/bots/3/whitelist/42');
    expect(calls[0].init.method).toBe('PUT');
    expect((calls[0].init.headers as Record<string, string>)['Content-Type']).toBe('application/json');
    expect(JSON.parse(String(calls[0].init.body))).toEqual({ note: '朋友', can_fetch: true });
  });

  it('sends bodiless POSTs without a content type', async () => {
    const calls = mockFetch(204);
    await expect(api.retryMedia(9)).resolves.toBeUndefined();
    await api.userbotLogout();
    expect(calls[0].url).toBe('/api/media/9/retry');
    expect(calls[0].init.headers).toBeUndefined();
    expect(calls[1].url).toBe('/api/admin/userbot/logout');
  });

  it('adds purge only when asked', async () => {
    const calls = mockFetch(204);
    await api.deleteBot(5, false);
    await api.deleteBot(5, true);
    expect(calls.map((c) => c.url)).toEqual(['/api/admin/bots/5', '/api/admin/bots/5?purge=1']);
    expect(calls[0].init.method).toBe('DELETE');
  });

  it('turns error JSON into ApiError carrying status, message and body', async () => {
    mockFetch(409, { error: '机器人已存在', bot_id: 4, steps: [{ step: 'getMe', ok: true, detail: '@x' }] });
    const err = await api.addBot('1:abc').catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(409);
    expect(err.message).toBe('机器人已存在');
    expect(err.body.steps).toHaveLength(1);
  });

  it('maps 401 and network failures to the re-login hint', async () => {
    mockFetch(401, { error: 'unauthorized' });
    expect((await api.bots().catch((e) => e)).message).toBe(NETWORK_ERROR);
    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('Failed to fetch'))));
    const err = await api.bots().catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(0);
    expect(errorMessage(err)).toBe(NETWORK_ERROR);
  });

  it('reports non-JSON error bodies by status', async () => {
    mockFetch(405, 'Method Not Allowed');
    expect((await api.bots().catch((e) => e)).message).toBe('HTTP 405');
  });

  it('builds media and avatar URLs', () => {
    expect(mediaUrl(12)).toBe('/media/12');
    expect(mediaUrl(12, true)).toBe('/media/12?download=1');
    expect(avatarUrl('senders', 42)).toBe('/avatars/senders/42');
  });
});
```

- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/api`
Expected: FAIL（`Failed to resolve import "./client"`）

- [ ] **Step 3: 实现**

`web/src/api/types.ts`：

```ts
// JSON shapes served by internal/httpapi. Field names match the Go json tags exactly.

export interface Bot {
  id: number;
  tg_bot_id: number;
  username: string;
  name: string;
  has_avatar: boolean;
  enabled: boolean;
  status: string; // running / error / stopped / removed
  last_error: string;
}

export interface Sender {
  tg_user_id: number;
  first_name: string;
  last_name: string;
  username: string;
  has_avatar: boolean;
}

export interface Chat {
  id: number;
  bot_id: number;
  sender: Sender;
  last_message_at: number;
  last_kind: string;
  last_text: string;
}

export type MediaState = 'pending' | 'done' | 'failed' | 'too_large';

export interface Media {
  id: number;
  role: 'main' | 'thumb';
  kind: string; // photo / video / animation / voice / audio / document / sticker / video_note
  mime: string;
  file_name: string;
  size: number;
  width: number;
  height: number;
  duration: number;
  waveform?: string; // base64 of the 5-bit packed Telegram waveform
  state: MediaState;
  error: string;
}

export interface Entity {
  type: string;
  offset: number; // UTF-16 code units
  length: number;
  url?: string;
  language?: string;
  user_id?: number;
  custom_emoji_id?: string;
}

export interface ForwardOrigin {
  type: 'user' | 'hidden_user' | 'chat' | 'channel';
  name: string;
  username?: string;
  user_id?: number;
  chat_id?: number;
  message_id?: number;
  date: number;
  signature?: string;
}

export interface ReplyRef {
  id: number;
  kind: string;
  text: string;
}

export type MessageKind =
  | 'text'
  | 'photo'
  | 'video'
  | 'animation'
  | 'voice'
  | 'audio'
  | 'document'
  | 'sticker'
  | 'video_note'
  | 'location'
  | 'venue'
  | 'contact'
  | 'poll'
  | 'dice'
  | 'other';

export interface Message {
  id: number;
  chat_id: number;
  tg_message_id: number;
  source: 'bot_update' | 'userbot_fetch';
  media_group_id: string;
  date: number;
  edit_date: number;
  kind: MessageKind;
  text: string;
  entities: Entity[];
  forward_origin?: ForwardOrigin;
  reply_to_tg_message_id: number;
  reply?: ReplyRef;
  origin_chat_title: string;
  origin_link: string;
  extra?: Record<string, unknown>;
  media: Media[];
}

export type SharedMediaType = 'media' | 'file' | 'link';

export interface WhitelistEntry {
  tg_user_id: number;
  note: string;
  can_fetch: boolean;
}

export interface RejectedSender {
  tg_user_id: number;
  first_name: string;
  username: string;
  last_seen_at: number;
  count: number;
}

export interface AddBotStep {
  step: string; // getMe / logOut / start
  ok: boolean;
  detail: string;
}

export interface AddBotResult {
  bot_id?: number;
  error?: string;
  steps: AddBotStep[];
}

export interface TelegramApp {
  configured: boolean;
  api_id: number;
  server: { managed: boolean; state: string; error: string };
}

export type UserbotState =
  | 'unconfigured'
  | 'connecting'
  | 'logged_out'
  | 'code_sent'
  | 'password_needed'
  | 'ready'
  | 'error';

export interface UserbotInfo {
  state: UserbotState;
  phone: string;
  name: string;
  tg_user_id: number;
  error: string;
}

export interface MessageRef {
  chat_id: number;
  message_id: number;
}

export type ArchiveEvent =
  | { type: 'message.created' | 'message.updated' | 'message.deleted'; data: MessageRef }
  | { type: 'media.updated'; data: { media_id: number; message_ids: number[] | null } }
  | { type: 'bot.status'; data: { bot_id: number; status: string; error: string } };

export const EVENT_TYPES = [
  'message.created',
  'message.updated',
  'message.deleted',
  'media.updated',
  'bot.status',
] as const;
```

`web/src/api/client.ts`：

```ts
import type {
  AddBotResult,
  Bot,
  Chat,
  Message,
  RejectedSender,
  SharedMediaType,
  TelegramApp,
  UserbotInfo,
  WhitelistEntry,
} from './types';

export const PAGE_SIZE = 50;

export class ApiError extends Error {
  readonly status: number;
  readonly body: unknown;

  constructor(status: number, message: string, body: unknown) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.body = body;
  }
}

export const NETWORK_ERROR = '网络错误或登录已过期，请刷新页面';

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: 'same-origin', redirect: 'error' };
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  }
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch {
    throw new ApiError(0, NETWORK_ERROR, undefined);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let data: unknown;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }
  if (!res.ok) {
    if (res.status === 401) throw new ApiError(401, NETWORK_ERROR, data);
    const msg =
      data && typeof data === 'object' && 'error' in data ? String((data as { error: unknown }).error) : `HTTP ${res.status}`;
    throw new ApiError(res.status, msg, data);
  }
  return data as T;
}

function qs(params: Record<string, string | number | undefined>): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== 0 && v !== '') p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : '';
}

export interface Api {
  bots(): Promise<Bot[]>;
  chats(): Promise<Chat[]>;
  messages(chatId: number, before?: number, limit?: number): Promise<Message[]>;
  message(id: number): Promise<Message>;
  chatMedia(chatId: number, type: SharedMediaType, before?: number, limit?: number): Promise<Message[]>;
  deleteMessage(id: number): Promise<void>;
  retryMedia(id: number): Promise<void>;
  addBot(token: string): Promise<AddBotResult>;
  setBotEnabled(id: number, enabled: boolean): Promise<Bot>;
  deleteBot(id: number, purge: boolean): Promise<void>;
  whitelist(botId: number): Promise<WhitelistEntry[]>;
  putWhitelist(botId: number, uid: number, note: string, canFetch: boolean): Promise<void>;
  deleteWhitelist(botId: number, uid: number): Promise<void>;
  rejected(botId: number): Promise<RejectedSender[]>;
  telegramApp(): Promise<TelegramApp>;
  saveTelegramApp(apiId: number, apiHash: string): Promise<void>;
  userbot(): Promise<UserbotInfo>;
  userbotPhone(phone: string): Promise<UserbotInfo>;
  userbotCode(code: string): Promise<UserbotInfo>;
  userbotPassword(password: string): Promise<UserbotInfo>;
  userbotLogout(): Promise<void>;
}

export const api: Api = {
  bots: () => request('GET', '/api/bots'),
  chats: () => request('GET', '/api/chats'),
  messages: (chatId, before = 0, limit = PAGE_SIZE) =>
    request('GET', `/api/chats/${chatId}/messages${qs({ before, limit })}`),
  message: (id) => request('GET', `/api/messages/${id}`),
  chatMedia: (chatId, type, before = 0, limit = PAGE_SIZE) =>
    request('GET', `/api/chats/${chatId}/media${qs({ type, before, limit })}`),
  deleteMessage: (id) => request('DELETE', `/api/messages/${id}`),
  retryMedia: (id) => request('POST', `/api/media/${id}/retry`),
  addBot: (token) => request('POST', '/api/admin/bots', { token }),
  setBotEnabled: (id, enabled) => request('PATCH', `/api/admin/bots/${id}`, { enabled }),
  deleteBot: (id, purge) => request('DELETE', `/api/admin/bots/${id}${purge ? '?purge=1' : ''}`),
  whitelist: (botId) => request('GET', `/api/admin/bots/${botId}/whitelist`),
  putWhitelist: (botId, uid, note, canFetch) =>
    request('PUT', `/api/admin/bots/${botId}/whitelist/${uid}`, { note, can_fetch: canFetch }),
  deleteWhitelist: (botId, uid) => request('DELETE', `/api/admin/bots/${botId}/whitelist/${uid}`),
  rejected: (botId) => request('GET', `/api/admin/bots/${botId}/rejected`),
  telegramApp: () => request('GET', '/api/admin/telegram-app'),
  saveTelegramApp: (apiId, apiHash) => request('PUT', '/api/admin/telegram-app', { api_id: apiId, api_hash: apiHash }),
  userbot: () => request('GET', '/api/admin/userbot'),
  userbotPhone: (phone) => request('POST', '/api/admin/userbot/phone', { phone }),
  userbotCode: (code) => request('POST', '/api/admin/userbot/code', { code }),
  userbotPassword: (password) => request('POST', '/api/admin/userbot/password', { password }),
  userbotLogout: () => request('POST', '/api/admin/userbot/logout'),
};

export function mediaUrl(id: number, download = false): string {
  return `/media/${id}${download ? '?download=1' : ''}`;
}

export function avatarUrl(kind: 'bots' | 'senders', tgId: number): string {
  return `/avatars/${kind}/${tgId}`;
}

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return String(err);
}
```


- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/api
git commit -m "feat(web): typed API client for the archive and admin endpoints"
```

---

### Task 4: 格式化、路由与 SSE 连接

**Files:**
- Create: `web/src/lib/format.ts`、`web/src/lib/router.ts`、`web/src/lib/sse.ts`
- Test: `web/src/lib/format.test.ts`、`web/src/lib/router.test.ts`、`web/src/lib/sse.test.ts`

**Interfaces:**
- Consumes: Task 3 的 `Bot`、`Sender`、`ArchiveEvent`、`EVENT_TYPES`
- Produces（`format.ts`）：`daysAgo`、`formatTime(unix)` → `"09:05"`、`dayKey(unix)`、`formatDayLabel(unix, now?)` → `今天 / 昨天 / 9月1日 / 2025年12月31日`、`formatListTime(unix, now?)` → `08:07 / 周一 / 9/27 / 2024/2/29`、`formatFullDate`、`formatMonth` → `2026年1月`、`monthKey`、`formatSize`、`formatDuration`、`kindLabel`、`previewText(kind, text)`、`senderName(sender)`、`botName(bot)`、`initials(name)`、`hashString(s)`、`peerColorIndex(id)`、`peerColor(id)` → `'var(--peer-N)'`、`fileExtension(name, mime)`、`fileColor(ext)`
- Produces（`router.ts`）：`type Route`（`home` / `chat{chatId}` / `settings` / `settings-add-bot` / `settings-bot{botId}` / `settings-telegram-app` / `settings-userbot`）、`parseRoute(pathname)`、`routePath(route)`、`isSettings(route)`、`route: Signal<Route>`、`navigate(route, replace?)`、`startRouter(): () => void`；路径为 `/`、`/chat/:id`、`/settings`、`/settings/bots/new`、`/settings/bots/:id`、`/settings/telegram-app`、`/settings/userbot`
- Produces（`sse.ts`）：`interface EventSourceLike`、`type EventSourceFactory`、`RETRY_MS = 5000`、`connectEvents(onEvent, onResync, factory?): () => void`

- [ ] **Step 1: 写失败测试**

`web/src/lib/format.test.ts`：

```ts
import { describe, expect, it } from 'vitest';
import {
  botName,
  dayKey,
  fileColor,
  fileExtension,
  formatDayLabel,
  formatDuration,
  formatFullDate,
  formatListTime,
  formatMonth,
  formatSize,
  formatTime,
  hashString,
  initials,
  kindLabel,
  monthKey,
  peerColorIndex,
  previewText,
  senderName,
} from './format';

// Build timestamps from local wall-clock parts so the tests pass in any TZ.
const at = (y: number, mo: number, d: number, h = 12, mi = 0) => new Date(y, mo - 1, d, h, mi).getTime() / 1000;
const now = new Date(2026, 9, 4, 15, 30); // 2026-10-04 15:30 local (a Sunday)

describe('dates', () => {
  it('formats time with zero padding', () => {
    expect(formatTime(at(2026, 10, 4, 9, 5))).toBe('09:05');
  });

  it('labels date separators relative to today', () => {
    expect(formatDayLabel(at(2026, 10, 4, 0, 1), now)).toBe('今天');
    expect(formatDayLabel(at(2026, 10, 3, 23, 59), now)).toBe('昨天');
    expect(formatDayLabel(at(2026, 9, 1), now)).toBe('9月1日');
    expect(formatDayLabel(at(2025, 12, 31), now)).toBe('2025年12月31日');
  });

  it('formats the chat list time column', () => {
    expect(formatListTime(at(2026, 10, 4, 8, 7), now)).toBe('08:07');
    expect(formatListTime(at(2026, 9, 28), now)).toBe('周一');
    expect(formatListTime(at(2026, 9, 27), now)).toBe('9/27');
    expect(formatListTime(at(2024, 2, 29), now)).toBe('2024/2/29');
    expect(formatListTime(0, now)).toBe('');
  });

  it('builds day and month keys and labels', () => {
    expect(dayKey(at(2026, 1, 2))).toBe('2026-01-02');
    expect(monthKey(at(2026, 1, 2))).toBe('2026-01');
    expect(formatMonth(at(2026, 1, 2))).toBe('2026年1月');
    expect(formatFullDate(at(2026, 10, 4, 13, 5))).toBe('2026年10月4日 13:05');
  });
});

describe('sizes and durations', () => {
  it('formats byte sizes like Telegram', () => {
    expect(formatSize(0)).toBe('0 B');
    expect(formatSize(512)).toBe('512 B');
    expect(formatSize(1536)).toBe('1.5 KB');
    expect(formatSize(20 * 1024 * 1024)).toBe('20 MB');
    expect(formatSize(1.25 * 1024 ** 3)).toBe('1.3 GB');
  });

  it('formats durations', () => {
    expect(formatDuration(7)).toBe('0:07');
    expect(formatDuration(754)).toBe('12:34');
    expect(formatDuration(3723)).toBe('1:02:03');
    expect(formatDuration(-3)).toBe('0:00');
  });
});

describe('names and labels', () => {
  it('derives sender and bot names with fallbacks', () => {
    expect(senderName({ first_name: 'Alice', last_name: 'Liddell', username: 'al', tg_user_id: 1 })).toBe('Alice Liddell');
    expect(senderName({ first_name: '', last_name: '', username: 'al', tg_user_id: 1 })).toBe('@al');
    expect(senderName({ first_name: '', last_name: '', username: '', tg_user_id: 9 })).toBe('用户 9');
    expect(botName({ name: '', username: 'archive_bot', id: 1 })).toBe('@archive_bot');
  });

  it('takes initials from up to two words, emoji-safe', () => {
    expect(initials('alice liddell')).toBe('AL');
    expect(initials('张三')).toBe('张');
    expect(initials('😀 Smile')).toBe('😀S');
    expect(initials('@bot')).toBe('B');
  });

  it('labels kinds and previews', () => {
    expect(kindLabel('photo')).toBe('照片');
    expect(kindLabel('mystery')).toBe('不支持的消息');
    expect(previewText('photo', '')).toBe('照片');
    expect(previewText('photo', '  hi\n there ')).toBe('hi there');
  });

  it('hashes names to stable non-negative numbers', () => {
    expect(hashString('频道')).toBe(hashString('频道'));
    expect(hashString('a')).not.toBe(hashString('b'));
    expect(hashString('x'.repeat(500))).toBeGreaterThanOrEqual(0);
  });

  it('picks stable peer colors', () => {
    expect(peerColorIndex(42)).toBe(0);
    expect(peerColorIndex(-1001234567890)).toBe(peerColorIndex(1001234567890));
  });

  it('derives file extension and tile color', () => {
    expect(fileExtension('Report.PDF', 'application/pdf')).toBe('pdf');
    expect(fileExtension('', 'application/x-zip')).toBe('zip');
    expect(fileColor('pdf')).toBe('var(--color-error)');
    expect(fileColor('zip')).toBe('var(--color-warning)');
    expect(fileColor('xlsx')).toBe('var(--color-text-green)');
    expect(fileColor('txt')).toBe('var(--color-primary)');
  });
});
```

`web/src/lib/router.test.ts`：

```ts
import { afterEach, describe, expect, it } from 'vitest';
import { isSettings, navigate, parseRoute, route, routePath, startRouter, type Route } from './router';

describe('router', () => {
  afterEach(() => history.replaceState(null, '', '/'));

  it('round-trips every route', () => {
    const all: Route[] = [
      { name: 'home' },
      { name: 'chat', chatId: 12 },
      { name: 'settings' },
      { name: 'settings-add-bot' },
      { name: 'settings-bot', botId: 3 },
      { name: 'settings-telegram-app' },
      { name: 'settings-userbot' },
    ];
    for (const r of all) expect(parseRoute(routePath(r))).toEqual(r);
  });

  it('falls back to home for unknown or malformed paths', () => {
    for (const p of ['/nope', '/chat', '/chat/0', '/chat/abc', '/chat/1/2', '/settings/bots/x', '/settings/zzz']) {
      expect(parseRoute(p)).toEqual({ name: 'home' });
    }
    expect(parseRoute('/chat/7/')).toEqual({ name: 'chat', chatId: 7 });
  });

  it('flags settings routes', () => {
    expect(isSettings({ name: 'settings-bot', botId: 1 })).toBe(true);
    expect(isSettings({ name: 'chat', chatId: 1 })).toBe(false);
  });

  it('navigates with history and follows popstate', () => {
    const stop = startRouter();
    navigate({ name: 'chat', chatId: 5 });
    expect(location.pathname).toBe('/chat/5');
    expect(route.value).toEqual({ name: 'chat', chatId: 5 });
    history.replaceState(null, '', '/settings');
    window.dispatchEvent(new PopStateEvent('popstate'));
    expect(route.value).toEqual({ name: 'settings' });
    stop();
  });
});
```

`web/src/lib/sse.test.ts`：

```ts
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ArchiveEvent } from '../api/types';
import { RETRY_MS, connectEvents, type EventSourceLike } from './sse';

class FakeES implements EventSourceLike {
  static all: FakeES[] = [];
  readyState = 0;
  onopen: ((ev: Event) => unknown) | null = null;
  onerror: ((ev: Event) => unknown) | null = null;
  listeners = new Map<string, (ev: MessageEvent) => void>();
  closed = false;
  constructor(public url: string) {
    FakeES.all.push(this);
  }
  addEventListener(type: string, l: (ev: MessageEvent) => void) {
    this.listeners.set(type, l);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, data: string) {
    this.listeners.get(type)?.(new MessageEvent(type, { data }));
  }
}

afterEach(() => {
  FakeES.all = [];
  vi.useRealTimers();
});

describe('connectEvents', () => {
  it('parses named events and ignores malformed data', () => {
    const got: ArchiveEvent[] = [];
    const stop = connectEvents((e) => got.push(e), () => {}, (u) => new FakeES(u));
    const es = FakeES.all[0];
    expect(es.url).toBe('/api/events');
    es.emit('message.created', '{"chat_id":1,"message_id":2}');
    es.emit('bot.status', 'not json');
    es.emit('media.updated', '{"media_id":3,"message_ids":null}');
    expect(got).toEqual([
      { type: 'message.created', data: { chat_id: 1, message_id: 2 } },
      { type: 'media.updated', data: { media_id: 3, message_ids: null } },
    ]);
    stop();
    expect(es.closed).toBe(true);
  });

  it('resyncs after the browser reconnects on its own', () => {
    const resync = vi.fn();
    connectEvents(() => {}, resync, (u) => new FakeES(u));
    const es = FakeES.all[0];
    es.onopen?.(new Event('open'));
    expect(resync).not.toHaveBeenCalled();
    es.readyState = 0; // CONNECTING: browser retries itself
    es.onerror?.(new Event('error'));
    es.onopen?.(new Event('open'));
    expect(resync).toHaveBeenCalledTimes(1);
    expect(FakeES.all).toHaveLength(1);
  });

  it('opens a new connection when the old one is closed for good', () => {
    vi.useFakeTimers();
    const resync = vi.fn();
    const stop = connectEvents(() => {}, resync, (u) => new FakeES(u));
    const first = FakeES.all[0];
    first.readyState = 2;
    first.onerror?.(new Event('error'));
    expect(first.closed).toBe(true);
    vi.advanceTimersByTime(RETRY_MS);
    expect(FakeES.all).toHaveLength(2);
    FakeES.all[1].onopen?.(new Event('open'));
    expect(resync).toHaveBeenCalledTimes(1);
    stop();
    expect(FakeES.all[1].closed).toBe(true);
  });
});
```


- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/lib`
Expected: FAIL（`Failed to resolve import "./format"` 等）

- [ ] **Step 3: 实现**

`web/src/lib/format.ts`：

```ts
import type { Bot, Sender } from '../api/types';

const pad = (n: number) => String(n).padStart(2, '0');
const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

function toDate(unix: number): Date {
  return new Date(unix * 1000);
}

function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

/** Whole local calendar days between `unix` and `now` (0 = same day). DST-safe via rounding. */
export function daysAgo(unix: number, now: Date = new Date()): number {
  return Math.round((startOfDay(now) - startOfDay(toDate(unix))) / 86_400_000);
}

/** "13:05" */
export function formatTime(unix: number): string {
  const d = toDate(unix);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** Local calendar day key, e.g. "2026-10-04". */
export function dayKey(unix: number): string {
  const d = toDate(unix);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** Date separator pill: 今天 / 昨天 / 10月4日 / 2025年10月4日. */
export function formatDayLabel(unix: number, now: Date = new Date()): string {
  const diff = daysAgo(unix, now);
  if (diff === 0) return '今天';
  if (diff === 1) return '昨天';
  const d = toDate(unix);
  if (d.getFullYear() === now.getFullYear()) return `${d.getMonth() + 1}月${d.getDate()}日`;
  return `${d.getFullYear()}年${d.getMonth() + 1}月${d.getDate()}日`;
}

/** Chat list time column: 13:05 / 周一 / 10/4 / 2025/10/4. */
export function formatListTime(unix: number, now: Date = new Date()): string {
  if (!unix) return '';
  const diff = daysAgo(unix, now);
  const d = toDate(unix);
  if (diff <= 0) return formatTime(unix);
  if (diff < 7) return WEEKDAYS[d.getDay()];
  if (d.getFullYear() === now.getFullYear()) return `${d.getMonth() + 1}/${d.getDate()}`;
  return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()}`;
}

/** "2026年10月4日 13:05" */
export function formatFullDate(unix: number): string {
  const d = toDate(unix);
  return `${d.getFullYear()}年${d.getMonth() + 1}月${d.getDate()}日 ${formatTime(unix)}`;
}

/** Shared-media month heading: "2026年10月". */
export function formatMonth(unix: number): string {
  const d = toDate(unix);
  return `${d.getFullYear()}年${d.getMonth() + 1}月`;
}

/** Month key used to group shared media: "2026-10". */
export function monthKey(unix: number): string {
  const d = toDate(unix);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}`;
}

/** 0 B / 512 B / 1.5 KB / 20 MB / 1.2 GB */
export function formatSize(bytes: number): string {
  if (!bytes || bytes < 0) return '0 B';
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1).replace(/\.0$/, '')} ${units[i]}`;
}

/** 0:07 / 12:34 / 1:02:03 */
export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds || 0));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  return h > 0 ? `${h}:${pad(m)}:${pad(sec)}` : `${m}:${pad(sec)}`;
}

const KIND_LABELS: Record<string, string> = {
  text: '消息',
  photo: '照片',
  video: '视频',
  animation: 'GIF',
  voice: '语音消息',
  audio: '音乐',
  document: '文件',
  sticker: '贴纸',
  video_note: '视频消息',
  location: '位置',
  venue: '地点',
  contact: '联系人',
  poll: '投票',
  dice: '骰子',
  other: '不支持的消息',
};

export function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? KIND_LABELS.other;
}

/** One-line preview used by the chat list and reply quotes. */
export function previewText(kind: string, text: string): string {
  const t = text.replace(/\s+/g, ' ').trim();
  if (t) return t;
  return kind ? kindLabel(kind) : '';
}

export function senderName(s: Pick<Sender, 'first_name' | 'last_name' | 'username' | 'tg_user_id'>): string {
  const full = `${s.first_name} ${s.last_name}`.trim();
  if (full) return full;
  if (s.username) return `@${s.username}`;
  return `用户 ${s.tg_user_id}`;
}

export function botName(b: Pick<Bot, 'name' | 'username' | 'id'>): string {
  return b.name || (b.username ? `@${b.username}` : `机器人 ${b.id}`);
}

/** Up to two initials taken from the first two words; surrogate-pair safe. */
export function initials(name: string): string {
  const words = name.replace(/^@/, '').trim().split(/\s+/).filter(Boolean);
  const chars = words.slice(0, 2).map((w) => Array.from(w)[0] ?? '');
  return chars.join('').toUpperCase();
}

/** Stable non-negative 31-bit hash (djb2) for peers that only have a name. */
export function hashString(s: string): number {
  let h = 5381;
  for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}

/** Stable peer color slot 0..6 (Web A fallback palette indexing). */
export function peerColorIndex(id: number): number {
  return Math.abs(Math.trunc(id)) % 7;
}

export function peerColor(id: number): string {
  return `var(--peer-${peerColorIndex(id)})`;
}

/** Extension shown on the document icon tile, at most 4 characters. */
export function fileExtension(name: string, mime: string): string {
  const m = /\.([A-Za-z0-9]{1,8})$/.exec(name);
  if (m) return m[1].toLowerCase().slice(0, 4);
  const sub = mime.split('/')[1] ?? '';
  return sub.replace(/^x-/, '').slice(0, 4).toLowerCase();
}

/** Icon tile color by extension: red pdf, green spreadsheets/apk, orange archives/slides, else primary. */
export function fileColor(ext: string): string {
  if (['pdf', 'xps'].includes(ext)) return 'var(--color-error)';
  if (['apk', 'xls', 'xlsx', 'ods'].includes(ext)) return 'var(--color-text-green)';
  if (['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz', 'ppt', 'pptx', 'odp'].includes(ext)) return 'var(--color-warning)';
  return 'var(--color-primary)';
}
```

`web/src/lib/router.ts`：

```ts
import { signal } from '@preact/signals';

export type Route =
  | { name: 'home' }
  | { name: 'chat'; chatId: number }
  | { name: 'settings' }
  | { name: 'settings-add-bot' }
  | { name: 'settings-bot'; botId: number }
  | { name: 'settings-telegram-app' }
  | { name: 'settings-userbot' };

export function parseRoute(pathname: string): Route {
  const parts = pathname.split('/').filter(Boolean);
  const id = (s: string | undefined) => (s && /^[1-9][0-9]{0,15}$/.test(s) ? Number(s) : 0);
  if (parts[0] === 'chat' && parts.length === 2 && id(parts[1])) return { name: 'chat', chatId: id(parts[1]) };
  if (parts[0] === 'settings') {
    if (parts.length === 1) return { name: 'settings' };
    if (parts[1] === 'bots' && parts[2] === 'new' && parts.length === 3) return { name: 'settings-add-bot' };
    if (parts[1] === 'bots' && parts.length === 3 && id(parts[2])) return { name: 'settings-bot', botId: id(parts[2]) };
    if (parts[1] === 'telegram-app' && parts.length === 2) return { name: 'settings-telegram-app' };
    if (parts[1] === 'userbot' && parts.length === 2) return { name: 'settings-userbot' };
  }
  return { name: 'home' };
}

export function routePath(r: Route): string {
  switch (r.name) {
    case 'home':
      return '/';
    case 'chat':
      return `/chat/${r.chatId}`;
    case 'settings':
      return '/settings';
    case 'settings-add-bot':
      return '/settings/bots/new';
    case 'settings-bot':
      return `/settings/bots/${r.botId}`;
    case 'settings-telegram-app':
      return '/settings/telegram-app';
    case 'settings-userbot':
      return '/settings/userbot';
  }
}

export function isSettings(r: Route): boolean {
  return r.name.startsWith('settings');
}

export const route = signal<Route>(parseRoute(typeof location === 'undefined' ? '/' : location.pathname));

export function navigate(to: Route, replace = false): void {
  const path = routePath(to);
  if (replace) history.replaceState(null, '', path);
  else history.pushState(null, '', path);
  route.value = to;
}

/** Keeps `route` in sync with browser back/forward. Returns an unsubscribe function. */
export function startRouter(): () => void {
  const onPop = () => {
    route.value = parseRoute(location.pathname);
  };
  window.addEventListener('popstate', onPop);
  onPop();
  return () => window.removeEventListener('popstate', onPop);
}
```

`web/src/lib/sse.ts`：

```ts
import { EVENT_TYPES, type ArchiveEvent } from '../api/types';

export interface EventSourceLike {
  readyState: number;
  onopen: ((ev: Event) => unknown) | null;
  onerror: ((ev: Event) => unknown) | null;
  addEventListener(type: string, listener: (ev: MessageEvent) => void): void;
  close(): void;
}

export type EventSourceFactory = (url: string) => EventSourceLike;

const CLOSED = 2;
export const RETRY_MS = 5000;

/**
 * Subscribes to /api/events. `onResync` runs after every reconnect so the caller can refetch
 * whatever it may have missed. When the browser gives up (readyState CLOSED, e.g. the
 * forward-auth session expired) a new connection is attempted every RETRY_MS.
 */
export function connectEvents(
  onEvent: (ev: ArchiveEvent) => void,
  onResync: () => void,
  factory: EventSourceFactory = (url) => new EventSource(url),
): () => void {
  let es: EventSourceLike | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  let dropped = false;

  const open = () => {
    es = factory('/api/events');
    for (const type of EVENT_TYPES) {
      es.addEventListener(type, (ev) => {
        let data: unknown;
        try {
          data = JSON.parse(ev.data);
        } catch {
          return;
        }
        onEvent({ type, data } as ArchiveEvent);
      });
    }
    es.onopen = () => {
      if (dropped) {
        dropped = false;
        onResync();
      }
    };
    es.onerror = () => {
      dropped = true;
      if (es && es.readyState === CLOSED && !stopped) {
        es.close();
        timer = setTimeout(open, RETRY_MS);
      }
    };
  };

  open();
  return () => {
    stopped = true;
    clearTimeout(timer);
    es?.close();
  };
}
```


- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/lib
git commit -m "feat(web): zh-CN formatters, path router and reconnecting SSE client"
```

---

### Task 5: 状态 store 与实时事件处理

**Files:**
- Create: `web/src/state/store.ts`、`web/src/test/fixtures.ts`
- Test: `web/src/state/store.test.ts`

**Interfaces:**
- Consumes: Task 3 的 `Api`、`ApiError`、`PAGE_SIZE`、`errorMessage`、类型；Task 4 无
- Produces（`store.ts`）：
  - `interface Conversation { items: Message[] /* 按 id 升序 */; hasMore; loading; loaded; error: string }`
  - `interface ViewerTarget { chatId; messageId; mediaId }`、`interface Toast { id; text }`
  - `createStore(api: Api, opts?: { chatsReloadDelay?: number /* 默认 300ms */ })` 返回：signals `bots`、`chats`、`chatsLoaded`、`botFilter`（0 = 全部）、`conversations`、`toast`、`viewer`、`sharedMediaOpen`；computed `botsById`、`visibleChats`；方法 `showToast(text)`、`conv(chatId)`、`loadBots()`、`loadChats()`、`refreshLatest(chatId)`、`loadOlder(chatId)`、`refreshMessage(id, chatHint?)`、`handleEvent(ev)`、`resync()`、`deleteMessage(msg)`（失败抛出；404 视为已删除）、`retryMedia(msg, mediaId)`；字段 `api`
  - `type Store`、`StoreContext`、`useStore()`
- Produces（`test/fixtures.ts`）：`makeBot`、`makeChat`（默认 id 10、bot 1、发送者 Alice/42）、`makeMedia`（默认 id 100、photo、done）、`makeMessage`（默认 id 1、chat 10、text "hello"）、`fakeApi(over?)`（每个方法都是带合法默认返回的 `vi.fn`）

- [ ] **Step 1: 写失败测试**

`web/src/test/fixtures.ts`（测试辅助，先写出来供测试使用）：

```ts
import { vi } from 'vitest';
import type { Api } from '../api/client';
import type { Bot, Chat, Media, Message } from '../api/types';

export function makeBot(over: Partial<Bot> = {}): Bot {
  return {
    id: 1,
    tg_bot_id: 777,
    username: 'archive_bot',
    name: 'Archive',
    has_avatar: false,
    enabled: true,
    status: 'running',
    last_error: '',
    ...over,
  };
}

export function makeChat(over: Partial<Chat> = {}): Chat {
  return {
    id: 10,
    bot_id: 1,
    sender: { tg_user_id: 42, first_name: 'Alice', last_name: '', username: 'alice', has_avatar: false },
    last_message_at: 1_790_000_000,
    last_kind: 'text',
    last_text: 'hello',
    ...over,
  };
}

export function makeMedia(over: Partial<Media> = {}): Media {
  return {
    id: 100,
    role: 'main',
    kind: 'photo',
    mime: 'image/jpeg',
    file_name: '',
    size: 1000,
    width: 800,
    height: 600,
    duration: 0,
    state: 'done',
    error: '',
    ...over,
  };
}

export function makeMessage(over: Partial<Message> = {}): Message {
  return {
    id: 1,
    chat_id: 10,
    tg_message_id: 1,
    source: 'bot_update',
    media_group_id: '',
    date: 1_790_000_000,
    edit_date: 0,
    kind: 'text',
    text: 'hello',
    entities: [],
    reply_to_tg_message_id: 0,
    origin_chat_title: '',
    origin_link: '',
    media: [],
    ...over,
  };
}

/** An Api whose every method is a vi.fn with an empty-but-valid default result. */
export function fakeApi(over: Partial<Api> = {}): Api {
  const base: Api = {
    bots: vi.fn(async () => []),
    chats: vi.fn(async () => []),
    messages: vi.fn(async () => []),
    message: vi.fn(async (id: number) => makeMessage({ id })),
    chatMedia: vi.fn(async () => []),
    deleteMessage: vi.fn(async () => undefined),
    retryMedia: vi.fn(async () => undefined),
    addBot: vi.fn(async () => ({ bot_id: 1, steps: [] })),
    setBotEnabled: vi.fn(async (id: number, enabled: boolean) => makeBot({ id, enabled })),
    deleteBot: vi.fn(async () => undefined),
    whitelist: vi.fn(async () => []),
    putWhitelist: vi.fn(async () => undefined),
    deleteWhitelist: vi.fn(async () => undefined),
    rejected: vi.fn(async () => []),
    telegramApp: vi.fn(async () => ({ configured: true, api_id: 1, server: { managed: true, state: 'running', error: '' } })),
    saveTelegramApp: vi.fn(async () => undefined),
    userbot: vi.fn(async () => ({ state: 'logged_out' as const, phone: '', name: '', tg_user_id: 0, error: '' })),
    userbotPhone: vi.fn(async () => ({ state: 'code_sent' as const, phone: '+1', name: '', tg_user_id: 0, error: '' })),
    userbotCode: vi.fn(async () => ({ state: 'ready' as const, phone: '+1', name: 'Me', tg_user_id: 5, error: '' })),
    userbotPassword: vi.fn(async () => ({ state: 'ready' as const, phone: '+1', name: 'Me', tg_user_id: 5, error: '' })),
    userbotLogout: vi.fn(async () => undefined),
  };
  return { ...base, ...over };
}
```

`web/src/state/store.test.ts`：

```ts
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/client';
import type { Message } from '../api/types';
import { fakeApi, makeBot, makeChat, makeMedia, makeMessage } from '../test/fixtures';
import { createStore } from './store';

const page = (from: number, to: number, chat = 10): Message[] =>
  Array.from({ length: to - from + 1 }, (_, i) => makeMessage({ id: from + i, chat_id: chat }));

const ids = (ms: Message[]) => ms.map((m) => m.id);

describe('store', () => {
  it('filters chats by the selected bot', async () => {
    const api = fakeApi({ chats: vi.fn(async () => [makeChat({ id: 1, bot_id: 1 }), makeChat({ id: 2, bot_id: 2 })]) });
    const s = createStore(api);
    await s.loadChats();
    expect(s.visibleChats.value.map((c) => c.id)).toEqual([1, 2]);
    s.botFilter.value = 2;
    expect(s.visibleChats.value.map((c) => c.id)).toEqual([2]);
  });

  it('loads the latest page and then older pages until exhausted', async () => {
    const messages = vi.fn(async (_chat: number, before = 0) => (before === 0 ? page(51, 100) : before === 51 ? page(31, 50) : []));
    const s = createStore(fakeApi({ messages }));
    await s.refreshLatest(10);
    expect(s.conv(10).hasMore).toBe(true);
    await s.loadOlder(10);
    expect(messages).toHaveBeenLastCalledWith(10, 51, 50);
    expect(ids(s.conv(10).items)).toEqual(ids(page(31, 100)));
    expect(s.conv(10).hasMore).toBe(false);
    await s.loadOlder(10);
    expect(messages).toHaveBeenCalledTimes(2);
  });

  it('merges a refreshed latest page that overlaps and restarts when it does not', async () => {
    let latest = page(51, 100);
    const s = createStore(fakeApi({ messages: vi.fn(async () => latest) }));
    await s.refreshLatest(10);
    latest = page(52, 101);
    await s.refreshLatest(10);
    expect(ids(s.conv(10).items)).toEqual(ids(page(51, 101)));
    latest = page(300, 349);
    await s.refreshLatest(10);
    expect(ids(s.conv(10).items)).toEqual(ids(page(300, 349)));
  });

  it('records a load error on the conversation', async () => {
    const s = createStore(fakeApi({ messages: vi.fn(async () => Promise.reject(new ApiError(500, 'boom', null))) }));
    await s.refreshLatest(10);
    expect(s.conv(10)).toMatchObject({ loading: false, loaded: false, error: 'boom' });
  });

  it('appends created messages to a loaded chat and reloads the chat list', async () => {
    vi.useFakeTimers();
    const api = fakeApi({ messages: vi.fn(async () => page(1, 3)), message: vi.fn(async (id: number) => makeMessage({ id, text: 'new' })) });
    const s = createStore(api, { chatsReloadDelay: 10 });
    await s.refreshLatest(10);
    await s.handleEvent({ type: 'message.created', data: { chat_id: 10, message_id: 4 } });
    await s.handleEvent({ type: 'message.created', data: { chat_id: 99, message_id: 5 } });
    expect(ids(s.conv(10).items)).toEqual([1, 2, 3, 4]);
    expect(api.message).toHaveBeenCalledTimes(1); // chat 99 is not open
    await vi.advanceTimersByTimeAsync(20);
    expect(api.chats).toHaveBeenCalledTimes(1); // debounced
    vi.useRealTimers();
  });

  it('removes deleted messages and drops updated ones that 404', async () => {
    const api = fakeApi({
      messages: vi.fn(async () => page(1, 3)),
      message: vi.fn(async () => Promise.reject(new ApiError(404, 'not found', null))),
    });
    const s = createStore(api, { chatsReloadDelay: 0 });
    await s.refreshLatest(10);
    await s.handleEvent({ type: 'message.deleted', data: { chat_id: 10, message_id: 2 } });
    await s.handleEvent({ type: 'message.updated', data: { chat_id: 10, message_id: 3 } });
    expect(ids(s.conv(10).items)).toEqual([1]);
  });

  it('refreshes only loaded messages on media.updated and tolerates null ids', async () => {
    const api = fakeApi({
      messages: vi.fn(async () => [makeMessage({ id: 1, media: [makeMedia({ id: 7, state: 'pending' })] })]),
      message: vi.fn(async (id: number) => makeMessage({ id, media: [makeMedia({ id: 7, state: 'done' })] })),
    });
    const s = createStore(api);
    await s.refreshLatest(10);
    await s.handleEvent({ type: 'media.updated', data: { media_id: 7, message_ids: [1, 555] } });
    await s.handleEvent({ type: 'media.updated', data: { media_id: 7, message_ids: null } });
    expect(api.message).toHaveBeenCalledTimes(1);
    expect(s.conv(10).items[0].media[0].state).toBe('done');
  });

  it('does not insert an edited message older than the loaded window', async () => {
    const api = fakeApi({ messages: vi.fn(async () => page(51, 100)), message: vi.fn(async (id: number) => makeMessage({ id })) });
    const s = createStore(api);
    await s.refreshLatest(10);
    await s.handleEvent({ type: 'message.updated', data: { chat_id: 10, message_id: 7 } });
    expect(s.conv(10).items[0].id).toBe(51);
  });

  it('applies bot.status and reloads bots it does not know', async () => {
    const api = fakeApi({ bots: vi.fn(async () => [makeBot({ id: 1 })]) });
    const s = createStore(api);
    await s.loadBots();
    await s.handleEvent({ type: 'bot.status', data: { bot_id: 1, status: 'error', error: '401 Unauthorized' } });
    expect(s.bots.value[0]).toMatchObject({ status: 'error', last_error: '401 Unauthorized' });
    await s.handleEvent({ type: 'bot.status', data: { bot_id: 2, status: 'running', error: '' } });
    expect(api.bots).toHaveBeenCalledTimes(2);
  });

  it('deletes messages, treating 404 as already gone, and rethrows other errors', async () => {
    const del = vi.fn(async (id: number) => {
      if (id === 2) throw new ApiError(404, 'not found', null);
      if (id === 3) throw new ApiError(500, 'disk', null);
    });
    const s = createStore(fakeApi({ messages: vi.fn(async () => page(1, 3)), deleteMessage: del }), { chatsReloadDelay: 0 });
    await s.refreshLatest(10);
    await s.deleteMessage(makeMessage({ id: 1 }));
    await s.deleteMessage(makeMessage({ id: 2 }));
    await expect(s.deleteMessage(makeMessage({ id: 3 }))).rejects.toThrow('disk');
    expect(ids(s.conv(10).items)).toEqual([3]);
  });

  it('marks retried media pending everywhere it appears, or toasts and refetches on failure', async () => {
    const failed = makeMedia({ id: 7, state: 'failed', error: 'x' });
    const api = fakeApi({
      messages: vi.fn(async () => [makeMessage({ id: 1, media: [failed] }), makeMessage({ id: 2, media: [failed] })]),
    });
    const s = createStore(api);
    await s.refreshLatest(10);
    await s.retryMedia(s.conv(10).items[0], 7);
    expect(s.conv(10).items.map((m) => m.media[0].state)).toEqual(['pending', 'pending']);

    api.retryMedia = vi.fn(async () => Promise.reject(new ApiError(409, 'media is not in failed state', null)));
    await s.retryMedia(s.conv(10).items[0], 7);
    expect(s.toast.value?.text).toBe('media is not in failed state');
    expect(api.message).toHaveBeenCalledWith(1);
  });

  it('resync reloads bots, chats and every open conversation', async () => {
    const api = fakeApi({ messages: vi.fn(async () => page(1, 2)) });
    const s = createStore(api);
    await s.refreshLatest(10);
    await s.resync();
    expect(api.bots).toHaveBeenCalledTimes(1);
    expect(api.chats).toHaveBeenCalledTimes(1);
    expect(api.messages).toHaveBeenCalledTimes(2);
  });
});
```

- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/state`
Expected: FAIL（`Failed to resolve import "./store"`）

- [ ] **Step 3: 实现**

`web/src/state/store.ts`：

```ts
import { computed, signal } from '@preact/signals';
import { createContext } from 'preact';
import { useContext } from 'preact/hooks';
import { ApiError, PAGE_SIZE, errorMessage, type Api } from '../api/client';
import type { ArchiveEvent, Bot, Chat, Message } from '../api/types';

export interface Conversation {
  items: Message[]; // ascending by id
  hasMore: boolean;
  loading: boolean;
  loaded: boolean;
  error: string;
}

const EMPTY: Conversation = { items: [], hasMore: true, loading: false, loaded: false, error: '' };

export interface ViewerTarget {
  chatId: number;
  messageId: number;
  mediaId: number;
}

export interface Toast {
  id: number;
  text: string;
}

function mergeById(a: Message[], b: Message[]): Message[] {
  const map = new Map<number, Message>();
  for (const m of a) map.set(m.id, m);
  for (const m of b) map.set(m.id, m);
  return [...map.values()].sort((x, y) => x.id - y.id);
}

export function createStore(api: Api, opts: { chatsReloadDelay?: number } = {}) {
  const delay = opts.chatsReloadDelay ?? 300;
  const bots = signal<Bot[]>([]);
  const chats = signal<Chat[]>([]);
  const chatsLoaded = signal(false);
  const botFilter = signal(0); // 0 = 全部
  const conversations = signal<Record<number, Conversation>>({});
  const toast = signal<Toast | null>(null);
  const viewer = signal<ViewerTarget | null>(null); // media viewer target
  const sharedMediaOpen = signal(false); // right column
  let toastSeq = 0;
  let reloadTimer: ReturnType<typeof setTimeout> | undefined;

  const botsById = computed(() => new Map(bots.value.map((b) => [b.id, b])));
  const visibleChats = computed(() =>
    botFilter.value ? chats.value.filter((c) => c.bot_id === botFilter.value) : chats.value,
  );

  function showToast(text: string) {
    toast.value = { id: ++toastSeq, text };
  }

  function conv(chatId: number): Conversation {
    return conversations.value[chatId] ?? EMPTY;
  }

  function setConv(chatId: number, patch: Partial<Conversation>) {
    conversations.value = { ...conversations.value, [chatId]: { ...conv(chatId), ...patch } };
  }

  async function loadBots() {
    try {
      bots.value = await api.bots();
    } catch (e) {
      showToast(errorMessage(e));
    }
  }

  async function loadChats() {
    try {
      chats.value = await api.chats();
      chatsLoaded.value = true;
    } catch (e) {
      showToast(errorMessage(e));
    }
  }

  function scheduleChatsReload() {
    clearTimeout(reloadTimer);
    reloadTimer = setTimeout(() => void loadChats(), delay);
  }

  /** Loads the newest page; merges when it overlaps what we have, otherwise starts over. */
  async function refreshLatest(chatId: number) {
    if (conv(chatId).loading) return;
    setConv(chatId, { loading: true, error: '' });
    try {
      const page = await api.messages(chatId, 0, PAGE_SIZE);
      const c = conv(chatId);
      const newestKnown = c.items.length ? c.items[c.items.length - 1].id : 0;
      const overlaps = c.loaded && page.length > 0 && page[0].id <= newestKnown;
      if (overlaps) setConv(chatId, { items: mergeById(c.items, page), loading: false, loaded: true });
      else setConv(chatId, { items: page, hasMore: page.length >= PAGE_SIZE, loading: false, loaded: true });
    } catch (e) {
      setConv(chatId, { loading: false, error: errorMessage(e) });
    }
  }

  async function loadOlder(chatId: number) {
    const c = conv(chatId);
    if (!c.loaded || c.loading || !c.hasMore) return;
    setConv(chatId, { loading: true, error: '' });
    try {
      const page = await api.messages(chatId, c.items[0]?.id ?? 0, PAGE_SIZE);
      setConv(chatId, { items: mergeById(page, conv(chatId).items), hasMore: page.length >= PAGE_SIZE, loading: false });
    } catch (e) {
      setConv(chatId, { loading: false, error: errorMessage(e) });
    }
  }

  function chatOfMessage(messageId: number): number | undefined {
    for (const [id, c] of Object.entries(conversations.value)) {
      if (c.items.some((m) => m.id === messageId)) return Number(id);
    }
    return undefined;
  }

  function upsertMessage(m: Message) {
    const c = conversations.value[m.chat_id];
    if (!c?.loaded) return;
    const known = c.items.some((x) => x.id === m.id);
    const oldest = c.items[0]?.id ?? 0;
    // Never insert a message older than the loaded window: that would leave a hole.
    if (known || !c.hasMore || m.id > oldest) setConv(m.chat_id, { items: mergeById(c.items, [m]) });
  }

  function removeMessage(chatId: number, messageId: number) {
    const c = conversations.value[chatId];
    if (!c) return;
    setConv(chatId, { items: c.items.filter((m) => m.id !== messageId) });
  }

  async function refreshMessage(messageId: number, chatHint?: number) {
    try {
      upsertMessage(await api.message(messageId));
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        const chatId = chatHint ?? chatOfMessage(messageId);
        if (chatId) removeMessage(chatId, messageId);
      }
    }
  }

  async function handleEvent(ev: ArchiveEvent) {
    switch (ev.type) {
      case 'message.created':
      case 'message.updated':
        scheduleChatsReload();
        if (conv(ev.data.chat_id).loaded) await refreshMessage(ev.data.message_id, ev.data.chat_id);
        return;
      case 'message.deleted':
        removeMessage(ev.data.chat_id, ev.data.message_id);
        scheduleChatsReload();
        return;
      case 'media.updated':
        for (const id of ev.data.message_ids ?? []) {
          if (chatOfMessage(id) !== undefined) await refreshMessage(id);
        }
        return;
      case 'bot.status': {
        const { bot_id, status, error } = ev.data;
        if (!botsById.value.has(bot_id)) {
          await loadBots();
          return;
        }
        bots.value = bots.value.map((b) => (b.id === bot_id ? { ...b, status, last_error: error } : b));
        return;
      }
    }
  }

  async function resync() {
    await Promise.all([loadBots(), loadChats()]);
    const loaded = Object.entries(conversations.value)
      .filter(([, c]) => c.loaded)
      .map(([id]) => Number(id));
    await Promise.all(loaded.map((id) => refreshLatest(id)));
  }

  /** Deletes an archived message; throws on failure so the caller can report it. */
  async function deleteMessage(m: Message) {
    try {
      await api.deleteMessage(m.id);
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 404)) throw e;
    }
    removeMessage(m.chat_id, m.id);
    scheduleChatsReload();
  }

  async function retryMedia(m: Message, mediaId: number) {
    try {
      await api.retryMedia(mediaId);
      const c = conv(m.chat_id);
      setConv(m.chat_id, {
        items: c.items.map((x) =>
          x.media.some((md) => md.id === mediaId)
            ? { ...x, media: x.media.map((md) => (md.id === mediaId ? { ...md, state: 'pending' as const, error: '' } : md)) }
            : x,
        ),
      });
    } catch (e) {
      showToast(errorMessage(e));
      await refreshMessage(m.id, m.chat_id);
    }
  }

  return {
    api,
    bots,
    chats,
    chatsLoaded,
    botFilter,
    conversations,
    toast,
    viewer,
    sharedMediaOpen,
    botsById,
    visibleChats,
    showToast,
    conv,
    loadBots,
    loadChats,
    refreshLatest,
    loadOlder,
    refreshMessage,
    handleEvent,
    resync,
    deleteMessage,
    retryMedia,
  };
}

export type Store = ReturnType<typeof createStore>;

export const StoreContext = createContext<Store | null>(null);

export function useStore(): Store {
  const s = useContext(StoreContext);
  if (!s) throw new Error('StoreContext missing');
  return s;
}
```


- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/state web/src/test/fixtures.ts
git commit -m "feat(web): signal store with paging, live event reducer and resync"
```

---

### Task 6: 基础 UI 组件（Web A 设置页风格）

**Files:**
- Create: `web/src/ui/{Avatar,Spinner,Button,Switch,Checkbox,InputField,ListItem,Tabs,Modal,ContextMenu,Toast}.tsx`、`web/src/ui/ui.scss`、`web/src/test/render.tsx`
- Test: `web/src/ui/ui.test.tsx`

**Interfaces:**
- Consumes: Task 4 的 `initials`、`peerColorIndex`；Task 5 的 `useStore`、`createStore`、`StoreContext`、`fakeApi`
- Produces:
  - `Avatar({ name, peerId, src?, size })`，`size ∈ mini(24) | tiny(32) | small(34) | medium(44) | large(54) | jumbo(120)`；图片加载失败回退为首字母 + 峰色渐变
  - `Spinner({ size?, color? })`（`role="progressbar"`，`aria-label="加载中"`）
  - `Button({ children, variant?: 'primary' | 'secondary' | 'danger', type?, disabled?, loading?, onClick? })`、`IconButton({ label, children, onClick?, class?, disabled? })`
  - `Switch({ checked, label, disabled?, onChange(checked) })`（`role="switch"`）、`Checkbox({ checked, label, onChange })`
  - `InputField({ label, value, onInput(value), error?, type?: 'text' | 'password' | 'tel', inputMode?, autoComplete?, disabled?, autoFocus? })`：浮动标签，有 `error` 时标签显示错误文本
  - `ListItem({ icon?, title, subtitle?, right?, danger?, onClick? })`（有 `onClick` 时渲染为 `<button>`）
  - `Tabs<K>({ items: { key, label }[], active, onChange, class? })`（`role="tablist"` / `tab`）
  - `Modal({ title, onClose, children })`、`ConfirmDialog({ title, text, confirmLabel, danger?, busy?, children?, onConfirm, onClose })`（按钮「取消」/ confirmLabel；Esc 关闭）
  - `ContextMenu({ x, y, items: MenuItem[], onClose })`、`interface MenuItem { label; icon; danger?; onSelect }`
  - `Toast()`（读取 `store.toast`，`TOAST_MS = 4000` 后消失）
  - `renderWithStore(ui, api?)` → `render` 结果 + `{ store, api }`

- [ ] **Step 1: 写失败测试**

`web/src/test/render.tsx`（测试辅助）：

```tsx
import { render } from '@testing-library/preact';
import type { ComponentChildren } from 'preact';
import type { Api } from '../api/client';
import { createStore, StoreContext } from '../state/store';
import { fakeApi } from './fixtures';

/** Renders `ui` inside a StoreContext backed by a fake Api. */
export function renderWithStore(ui: ComponentChildren, api: Api = fakeApi()) {
  const store = createStore(api, { chatsReloadDelay: 0 });
  const result = render(<StoreContext.Provider value={store}>{ui}</StoreContext.Provider>);
  return { ...result, store, api };
}
```

`web/src/ui/ui.test.tsx`：

```tsx
import { act, fireEvent, render, screen } from '@testing-library/preact';
import { describe, expect, it, vi } from 'vitest';
import { renderWithStore } from '../test/render';
import { Avatar } from './Avatar';
import { ContextMenu } from './ContextMenu';
import { InputField } from './InputField';
import { ConfirmDialog } from './Modal';
import { Switch } from './Switch';
import { Tabs } from './Tabs';
import { TOAST_MS, Toast } from './Toast';

describe('Avatar', () => {
  it('shows initials on the peer color without a photo', () => {
    const { container } = render(<Avatar name="Alice Liddell" peerId={44} size="large" />);
    const el = container.querySelector('.Avatar') as HTMLElement;
    expect(el.textContent).toBe('AL');
    expect(el.style.width).toBe('54px');
    expect(el.style.getPropertyValue('--avatar-color')).toBe('var(--peer-2)');
  });

  it('falls back to initials when the photo fails to load', () => {
    const { container } = render(<Avatar name="Bob" peerId={1} size="medium" src="/avatars/senders/1" />);
    const img = container.querySelector('img')!;
    expect(img.getAttribute('src')).toBe('/avatars/senders/1');
    fireEvent.error(img);
    expect(container.querySelector('img')).toBeNull();
    expect(container.textContent).toBe('B');
  });
});

describe('controls', () => {
  it('Switch reports the new state', () => {
    const onChange = vi.fn();
    render(<Switch checked={false} label="启用" onChange={onChange} />);
    fireEvent.click(screen.getByRole('switch', { name: '启用' }));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it('InputField shows the error in place of the label', () => {
    const onInput = vi.fn();
    const { rerender } = render(<InputField label="手机号" value="" onInput={onInput} />);
    fireEvent.input(screen.getByLabelText('手机号'), { target: { value: '+86' } });
    expect(onInput).toHaveBeenCalledWith('+86');
    rerender(<InputField label="手机号" value="+86" onInput={onInput} error="手机号格式不正确" />);
    expect(screen.getByLabelText('手机号格式不正确').getAttribute('aria-invalid')).toBe('true');
  });

  it('Tabs marks the active tab and reports clicks', () => {
    const onChange = vi.fn();
    render(<Tabs items={[{ key: 'a', label: '媒体' }, { key: 'b', label: '文件' }]} active="a" onChange={onChange} />);
    expect(screen.getByRole('tab', { name: '媒体' }).getAttribute('aria-selected')).toBe('true');
    fireEvent.click(screen.getByRole('tab', { name: '文件' }));
    expect(onChange).toHaveBeenCalledWith('b');
  });
});

describe('overlays', () => {
  it('ConfirmDialog confirms, cancels and closes on Escape', () => {
    const onConfirm = vi.fn();
    const onClose = vi.fn();
    render(<ConfirmDialog title="删除存档" text="确定？" confirmLabel="删除" danger onConfirm={onConfirm} onClose={onClose} />);
    fireEvent.click(screen.getByText('删除'));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByText('取消'));
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('ContextMenu runs the chosen item and closes on outside click', () => {
    const onSelect = vi.fn();
    const onClose = vi.fn();
    render(<ContextMenu x={10} y={10} onClose={onClose} items={[{ label: '复制文本', icon: null, onSelect }]} />);
    fireEvent.click(screen.getByRole('menuitem', { name: '复制文本' }));
    expect(onSelect).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.mouseDown(document.body);
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('Toast shows the store message and hides it after a while', async () => {
    vi.useFakeTimers();
    const { store } = renderWithStore(<Toast />);
    act(() => store.showToast('保存失败'));
    expect(screen.getByRole('status').textContent).toBe('保存失败');
    await act(async () => {
      vi.advanceTimersByTime(TOAST_MS);
    });
    expect(screen.queryByRole('status')).toBeNull();
    vi.useRealTimers();
  });
});
```

- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/ui`
Expected: FAIL（`Failed to resolve import "./Avatar"`）

- [ ] **Step 3: 实现**


`web/src/ui/Avatar.tsx`：

```tsx
import { useState } from 'preact/hooks';
import { initials, peerColorIndex } from '../lib/format';
import './ui.scss';

export const AVATAR_SIZES = { mini: 24, tiny: 32, small: 34, medium: 44, large: 54, jumbo: 120 } as const;
export type AvatarSize = keyof typeof AVATAR_SIZES;

interface Props {
  name: string;
  peerId: number;
  src?: string | null;
  size: AvatarSize;
}

export function Avatar({ name, peerId, src, size }: Props) {
  const [failed, setFailed] = useState(false);
  const px = AVATAR_SIZES[size];
  const showImage = Boolean(src) && !failed;
  return (
    <div
      class={`Avatar size-${size}`}
      style={{
        width: `${px}px`,
        height: `${px}px`,
        '--avatar-color': `var(--peer-${peerColorIndex(peerId)})`,
        fontSize: `${Math.max(px / 2 - 4, 8)}px`,
      }}
      aria-hidden="true"
    >
      {showImage ? (
        <img src={src!} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)} />
      ) : (
        <span class="Avatar-initials">{initials(name)}</span>
      )}
    </div>
  );
}
```


`web/src/ui/Spinner.tsx`：

```tsx
import './ui.scss';

export function Spinner({ size = 24, color = 'currentColor' }: { size?: number; color?: string }) {
  return (
    <svg class="Spinner" width={size} height={size} viewBox="0 0 24 24" role="progressbar" aria-label="加载中">
      <circle cx="12" cy="12" r="9" fill="none" stroke={color} stroke-width="2.5" stroke-linecap="round" stroke-dasharray="42 100" />
    </svg>
  );
}
```


`web/src/ui/Button.tsx`：

```tsx
import type { ComponentChildren } from 'preact';
import { Spinner } from './Spinner';
import './ui.scss';

interface Props {
  children: ComponentChildren;
  variant?: 'primary' | 'secondary' | 'danger';
  type?: 'button' | 'submit';
  disabled?: boolean;
  loading?: boolean;
  onClick?: () => void;
}

export function Button({ children, variant = 'primary', type = 'button', disabled, loading, onClick }: Props) {
  return (
    <button type={type} class={`Button ${variant}`} disabled={disabled || loading} onClick={onClick}>
      {loading ? <Spinner size={24} /> : children}
    </button>
  );
}

interface IconButtonProps {
  label: string;
  children: ComponentChildren;
  onClick?: (e: MouseEvent) => void;
  class?: string;
  disabled?: boolean;
}

/** Round 40px translucent icon button used in headers. */
export function IconButton({ label, children, onClick, class: cls = '', disabled }: IconButtonProps) {
  return (
    <button type="button" class={`IconButton ${cls}`} aria-label={label} title={label} onClick={onClick} disabled={disabled}>
      {children}
    </button>
  );
}
```


`web/src/ui/Switch.tsx`：

```tsx
import './ui.scss';

interface Props {
  checked: boolean;
  label: string;
  disabled?: boolean;
  onChange: (checked: boolean) => void;
}

export function Switch({ checked, label, disabled, onChange }: Props) {
  return (
    <label class={`Switch${checked ? ' checked' : ''}`}>
      <input
        type="checkbox"
        role="switch"
        aria-label={label}
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange((e.currentTarget as HTMLInputElement).checked)}
      />
      <span class="Switch-track" aria-hidden="true" />
    </label>
  );
}
```


`web/src/ui/Checkbox.tsx`：

```tsx
import './ui.scss';

interface Props {
  checked: boolean;
  label: string;
  onChange: (checked: boolean) => void;
}

export function Checkbox({ checked, label, onChange }: Props) {
  return (
    <label class="Checkbox">
      <input type="checkbox" checked={checked} onChange={(e) => onChange((e.currentTarget as HTMLInputElement).checked)} />
      <span class="Checkbox-box" aria-hidden="true" />
      <span class="Checkbox-label">{label}</span>
    </label>
  );
}
```


`web/src/ui/InputField.tsx`：

```tsx
import { useId } from 'preact/hooks';
import './ui.scss';

interface Props {
  label: string;
  value: string;
  onInput: (value: string) => void;
  error?: string;
  type?: 'text' | 'password' | 'tel';
  inputMode?: 'text' | 'numeric' | 'tel';
  autoComplete?: string;
  disabled?: boolean;
  autoFocus?: boolean;
}

/** Web A style text field: 48px, 16px radius, floating label that shows the error text. */
export function InputField({ label, value, onInput, error, type = 'text', inputMode, autoComplete = 'off', disabled, autoFocus }: Props) {
  const id = useId();
  return (
    <div class={`InputField${error ? ' error' : ''}${value ? ' touched' : ''}`}>
      <input
        id={id}
        type={type as 'text' /* preact 11 ARIA typing narrows type per role */}
        value={value}
        inputMode={inputMode}
        autoComplete={autoComplete}
        disabled={disabled}
        autoFocus={autoFocus}
        placeholder=" "
        aria-invalid={error ? true : undefined}
        onInput={(e) => onInput((e.currentTarget as HTMLInputElement).value)}
      />
      <label for={id}>{error || label}</label>
    </div>
  );
}
```


`web/src/ui/ListItem.tsx`：

```tsx
import type { ComponentChildren } from 'preact';
import './ui.scss';

interface Props {
  icon?: ComponentChildren;
  title: ComponentChildren;
  subtitle?: ComponentChildren;
  right?: ComponentChildren;
  danger?: boolean;
  onClick?: () => void;
}

/** Settings row sharing the chat-list row geometry (min-height 48px, 16px radius). */
export function ListItem({ icon, title, subtitle, right, danger, onClick }: Props) {
  const body = (
    <>
      {icon && <span class="ListItem-icon">{icon}</span>}
      <span class="ListItem-text">
        <span class="ListItem-title">{title}</span>
        {subtitle && <span class="ListItem-subtitle">{subtitle}</span>}
      </span>
      {right && <span class="ListItem-right">{right}</span>}
    </>
  );
  const cls = `ListItem${danger ? ' danger' : ''}${onClick ? ' interactive' : ''}`;
  return onClick ? (
    <button type="button" class={cls} onClick={onClick}>
      {body}
    </button>
  ) : (
    <div class={cls}>{body}</div>
  );
}
```


`web/src/ui/Tabs.tsx`：

```tsx
import type { ComponentChildren } from 'preact';
import './ui.scss';

export interface TabItem<K extends string | number> {
  key: K;
  label: ComponentChildren;
}

interface Props<K extends string | number> {
  items: TabItem<K>[];
  active: K;
  onChange: (key: K) => void;
  class?: string;
}

export function Tabs<K extends string | number>({ items, active, onChange, class: cls = '' }: Props<K>) {
  return (
    <div class={`Tabs ${cls}`} role="tablist">
      {items.map((t) => (
        <button
          key={t.key}
          type="button"
          role="tab"
          aria-selected={t.key === active}
          class={`Tab${t.key === active ? ' active' : ''}`}
          onClick={() => onChange(t.key)}
        >
          <span class="Tab-label">
            {t.label}
            {t.key === active && <i class="Tab-indicator" aria-hidden="true" />}
          </span>
        </button>
      ))}
    </div>
  );
}
```


`web/src/ui/Modal.tsx`：

```tsx
import type { ComponentChildren } from 'preact';
import { useEffect } from 'preact/hooks';
import './ui.scss';

interface ModalProps {
  title: string;
  onClose: () => void;
  children: ComponentChildren;
}

export function Modal({ title, onClose, children }: ModalProps) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);
  return (
    <div class="Modal" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div class="Modal-dialog" role="dialog" aria-modal="true" aria-label={title}>
        <h3 class="Modal-title">{title}</h3>
        {children}
      </div>
    </div>
  );
}

interface ConfirmProps {
  title: string;
  text: string;
  confirmLabel: string;
  danger?: boolean;
  busy?: boolean;
  children?: ComponentChildren;
  onConfirm: () => void;
  onClose: () => void;
}

export function ConfirmDialog({ title, text, confirmLabel, danger, busy, children, onConfirm, onClose }: ConfirmProps) {
  return (
    <Modal title={title} onClose={onClose}>
      <p class="Modal-text">{text}</p>
      {children}
      <div class="Modal-actions">
        <button type="button" class="Modal-action" onClick={onClose}>
          取消
        </button>
        <button type="button" class={`Modal-action${danger ? ' danger' : ''}`} disabled={busy} onClick={onConfirm}>
          {confirmLabel}
        </button>
      </div>
    </Modal>
  );
}
```


`web/src/ui/ContextMenu.tsx`：

```tsx
import type { ComponentChildren } from 'preact';
import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import './ui.scss';

export interface MenuItem {
  label: string;
  icon: ComponentChildren;
  danger?: boolean;
  onSelect: () => void;
}

interface Props {
  x: number;
  y: number;
  items: MenuItem[];
  onClose: () => void;
}

/** Floating menu at the pointer, kept inside the viewport; closes on outside click, scroll or Escape. */
export function ContextMenu({ x, y, items, onClose }: Props) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ left: x, top: y });

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const w = el.offsetWidth;
    const h = el.offsetHeight;
    setPos({
      left: Math.max(8, Math.min(x, window.innerWidth - w - 8)),
      top: Math.max(8, Math.min(y, window.innerHeight - h - 8)),
    });
  }, [x, y]);

  useEffect(() => {
    const onDown = (e: Event) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('mousedown', onDown, true);
    document.addEventListener('touchstart', onDown, true);
    document.addEventListener('keydown', onKey);
    window.addEventListener('resize', onClose);
    return () => {
      document.removeEventListener('mousedown', onDown, true);
      document.removeEventListener('touchstart', onDown, true);
      document.removeEventListener('keydown', onKey);
      window.removeEventListener('resize', onClose);
    };
  }, [onClose]);

  return (
    <div class="ContextMenu" ref={ref} role="menu" style={{ left: `${pos.left}px`, top: `${pos.top}px` }}>
      {items.map((it) => (
        <button
          key={it.label}
          type="button"
          role="menuitem"
          class={`ContextMenu-item${it.danger ? ' danger' : ''}`}
          onClick={() => {
            onClose();
            it.onSelect();
          }}
        >
          {it.icon}
          <span>{it.label}</span>
        </button>
      ))}
    </div>
  );
}
```


`web/src/ui/Toast.tsx`：

```tsx
import { useEffect } from 'preact/hooks';
import { useStore } from '../state/store';
import './ui.scss';

export const TOAST_MS = 4000;

export function Toast() {
  const { toast } = useStore();
  const current = toast.value;
  useEffect(() => {
    if (!current) return;
    const t = setTimeout(() => {
      if (toast.value?.id === current.id) toast.value = null;
    }, TOAST_MS);
    return () => clearTimeout(t);
  }, [current?.id]);
  if (!current) return null;
  return (
    <div class="Toast" role="status">
      {current.text}
    </div>
  );
}
```


`web/src/ui/ui.scss`：

```scss
.Avatar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  overflow: hidden;
  color: #fff;
  font-family: var(--font-family-rounded);
  font-weight: bold;
  background: linear-gradient(#fff -300%, var(--avatar-color));
  user-select: none;

  img {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }
}

.Spinner {
  animation: spinner-rotate 1s linear infinite;
}

@keyframes spinner-rotate {
  to {
    transform: rotate(360deg);
  }
}

.Button {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 3rem;
  padding: 0.5rem;
  border-radius: var(--border-radius-button);
  font-weight: var(--font-weight-medium);
  text-transform: uppercase;
  transition: background-color 0.2s, color 0.2s, opacity 0.2s;

  &.primary {
    background: var(--color-primary);
    color: #fff;

    &:hover:not(:disabled) {
      background: var(--color-primary-shade);
    }
  }

  &.secondary {
    color: var(--color-primary);

    &:hover:not(:disabled) {
      background: var(--color-primary-tint);
    }
  }

  &.danger {
    color: var(--color-error);

    &:hover:not(:disabled) {
      background: rgba(229, 57, 53, 0.08);
    }
  }

  &:disabled {
    opacity: 0.5;
    cursor: default;
  }
}

.IconButton {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 2.5rem;
  height: 2.5rem;
  border-radius: 50%;
  color: var(--color-text-secondary);
  transition: background-color 0.15s;

  &:hover:not(:disabled) {
    background: var(--color-interactive-element-hover);
  }

  &.translucent-white {
    color: #fff;

    &:hover {
      background: rgba(255, 255, 255, 0.1);
    }
  }
}

.Switch {
  position: relative;
  display: inline-block;
  width: 2.125rem;
  height: 0.875rem;
  cursor: pointer;

  input {
    position: absolute;
    inset: 0;
    opacity: 0;
    margin: 0;
    cursor: pointer;
  }

  .Switch-track {
    position: absolute;
    inset: 0;
    border-radius: 0.5rem;
    background: var(--color-gray);
    transition: background-color 150ms;

    &::after {
      content: "";
      position: absolute;
      top: -0.125rem;
      left: 0;
      width: 1.125rem;
      height: 1.125rem;
      border-radius: 50%;
      border: 2px solid var(--color-gray);
      background: var(--color-background);
      transition: transform 200ms, border-color 150ms;
    }
  }

  &.checked .Switch-track {
    background: var(--color-primary);

    &::after {
      transform: translateX(100%);
      border-color: var(--color-primary);
    }
  }
}

.Checkbox {
  display: flex;
  align-items: center;
  gap: 1rem;
  padding: 0.75rem 1rem;
  cursor: pointer;

  input {
    position: absolute;
    opacity: 0;
  }

  .Checkbox-box {
    flex-shrink: 0;
    width: 1.25rem;
    height: 1.25rem;
    border: 2px solid var(--color-borders-input);
    border-radius: 0.25rem;
    transition: background-color 0.15s, border-color 0.15s;
  }

  input:checked + .Checkbox-box {
    background: var(--color-primary)
      url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='white' stroke-width='3' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='M5 12l5 5L20 7'/%3E%3C/svg%3E")
      center / 0.875rem no-repeat;
    border-color: var(--color-primary);
  }

  input:focus-visible + .Checkbox-box {
    outline: 2px solid var(--color-primary);
    outline-offset: 2px;
  }
}

.InputField {
  position: relative;
  margin-bottom: 1.5rem;

  input {
    display: block;
    width: 100%;
    height: 3rem;
    padding: calc(0.75rem - 1px) calc(1.1875rem - 1px) calc(0.6875rem - 1px);
    border: 1px solid var(--color-borders-input);
    border-radius: var(--border-radius-default);
    background: var(--color-background);
    color: var(--color-text);
    font: inherit;
    font-size: 1rem;
    outline: none;
    transition: border-color 0.15s;
    caret-color: var(--color-primary);

    &:hover {
      border-color: var(--color-primary);
    }

    &:focus {
      border-color: var(--color-primary);
      box-shadow: inset 0 0 0 1px var(--color-primary);
    }
  }

  label {
    position: absolute;
    top: 0.6875rem;
    left: 1rem;
    padding: 0 0.25rem;
    background: var(--color-background);
    color: var(--color-placeholders);
    pointer-events: none;
    transform-origin: left center;
    transition: transform 0.15s ease-out, color 0.15s ease-out;
    white-space: nowrap;
  }

  input:focus + label,
  input:not(:placeholder-shown) + label {
    transform: scale(0.75) translate(-0.5rem, -2.25rem);
    color: var(--color-text-secondary);
  }

  input:focus + label {
    color: var(--color-primary);
  }

  &.error {
    input,
    input:hover,
    input:focus {
      border-color: var(--color-error);
      box-shadow: none;
    }

    label,
    input:focus + label {
      color: var(--color-error);
    }
  }
}

.ListItem {
  display: flex;
  align-items: center;
  width: 100%;
  min-height: 3rem;
  padding: 0.5rem 1rem;
  border-radius: var(--border-radius-default);
  text-align: start;
  color: var(--color-text);
  gap: 2rem;

  &.interactive {
    cursor: pointer;
    transition: background-color 0.15s;

    &:hover {
      background: var(--color-chat-hover);
    }

    &:active {
      background: var(--color-item-active);
    }
  }

  &.danger {
    color: var(--color-error);

    .ListItem-icon {
      color: var(--color-error);
    }
  }

  .ListItem-icon {
    flex-shrink: 0;
    display: flex;
    color: var(--color-text-secondary);
  }

  .ListItem-text {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }

  .ListItem-title {
    font-size: 1rem;
    line-height: 1.5rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .ListItem-subtitle {
    font-size: 0.875rem;
    line-height: 1.125rem;
    color: var(--color-text-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .ListItem-right {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.9375rem;
    color: var(--color-text-secondary);
  }
}

.Tabs {
  display: flex;
  overflow-x: auto;
  scrollbar-width: none;
  gap: 0;

  &::-webkit-scrollbar {
    display: none;
  }
}

.Tab {
  flex: 1 0 auto;
  display: flex;
  justify-content: center;
  padding: 0.625rem 1.125rem;
  border-radius: 6px 6px 0 0;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-secondary);
  white-space: nowrap;
  transition: background-color 0.15s, color 0.15s;

  &:hover:not(.active) {
    background: var(--color-interactive-element-hover);
  }

  &.active {
    color: var(--color-primary);
  }

  .Tab-label {
    position: relative;
    display: flex;
    align-items: center;
    gap: 0.375rem;
  }

  .Tab-indicator {
    position: absolute;
    left: -0.5rem;
    right: -0.5rem;
    bottom: -0.625rem;
    height: 3px;
    border-radius: 3px 3px 0 0;
    background: var(--color-primary);
  }
}

.Modal {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1rem;
  background: rgba(0, 0, 0, 0.25);
  animation: fade-in 0.15s ease-out;
}

.Modal-dialog {
  width: 100%;
  max-width: 22rem;
  padding: 1.25rem 1.5rem 0.75rem;
  border-radius: var(--border-radius-default);
  background: var(--color-background);
  box-shadow: 0 0.25rem 0.5rem 0.125rem var(--color-default-shadow);
  animation: modal-in var(--layer-transition);
}

.Modal-title {
  font-size: 1.25rem;
  font-weight: var(--font-weight-medium);
  margin-bottom: 0.75rem;
}

.Modal-text {
  margin-bottom: 0.5rem;
  line-height: 1.375;
}

.Modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  margin-top: 0.5rem;
}

.Modal-action {
  height: 2.5rem;
  padding: 0 0.75rem;
  border-radius: var(--border-radius-default-small);
  color: var(--color-primary);
  font-weight: var(--font-weight-medium);
  text-transform: uppercase;

  &:hover:not(:disabled) {
    background: var(--color-primary-tint);
  }

  &.danger {
    color: var(--color-error);

    &:hover:not(:disabled) {
      background: rgba(229, 57, 53, 0.08);
    }
  }

  &:disabled {
    opacity: 0.5;
  }
}

@keyframes fade-in {
  from {
    opacity: 0;
  }
}

@keyframes modal-in {
  from {
    transform: scale(0.95);
    opacity: 0;
  }
}

.ContextMenu {
  position: fixed;
  z-index: 60;
  min-width: 13.5rem;
  padding: 0.25rem 0;
  border-radius: var(--border-radius-default);
  background: var(--color-background);
  box-shadow: 0 0.25rem 0.5rem 0.125rem var(--color-default-shadow);
  backdrop-filter: blur(10px);
  animation: menu-in 0.15s ease-out;
  transform-origin: top left;
}

.ContextMenu-item {
  display: flex;
  align-items: center;
  gap: 1.25rem;
  width: calc(100% - 0.5rem);
  margin: 0.125rem 0.25rem;
  padding: 0.3125rem 1rem 0.3125rem 0.75rem;
  border-radius: 0.375rem;
  font-size: 0.875rem;
  font-weight: var(--font-weight-medium);
  line-height: 1.5rem;
  text-align: start;

  svg {
    color: var(--color-text-secondary);
  }

  &:hover {
    background: var(--color-item-hover);
  }

  &.danger,
  &.danger svg {
    color: var(--color-error);
  }
}

@keyframes menu-in {
  from {
    transform: scale(0.85);
    opacity: 0;
  }
}

.Toast {
  position: fixed;
  left: 50%;
  bottom: 1.5rem;
  z-index: 70;
  max-width: min(30rem, calc(100vw - 2rem));
  padding: 0.75rem 1rem;
  border-radius: var(--border-radius-toast);
  background: var(--color-toast-background);
  color: #fff;
  font-size: 0.9375rem;
  transform: translateX(-50%);
  animation: fade-in 0.2s ease-out;
}
```


- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/ui web/src/test/render.tsx
git commit -m "feat(web): Web A style UI kit (avatar, switch, inputs, tabs, dialogs, menu, toast)"
```

---

### Task 7: 文本实体渲染

**Files:**
- Create: `web/src/lib/entities.ts`、`web/src/components/message/RichText.tsx`、`web/src/components/message/RichText.scss`
- Test: `web/src/lib/entities.test.ts`、`web/src/components/message/RichText.test.tsx`

**Interfaces:**
- Consumes: Task 3 的 `Entity`
- Produces:
  - `type RichNode`、`buildTree(text, entities): RichNode[]`（UTF-16 偏移；交叉实体在外层边界处拆分；同一范围块级在外）
  - `safeHref(raw): string | null`（只放行 http / https / tg / mailto / tel；裸域名补 `https://`）
  - `extractLinks(text, entities): string[]`（url + text_link，按出现顺序去重；Task 12 的链接页使用）
  - `RichText({ text, entities })`：bold / italic / underline / strikethrough / spoiler（点击显示）/ code / pre（含语言标签）/ blockquote / expandable_blockquote（三行折叠，「展开」「收起」）/ url / text_link / mention（链到 t.me）/ email / phone_number / text_mention / hashtag / cashtag / bot_command / custom_emoji（显示实体下的 fallback emoji）

- [ ] **Step 1: 写失败测试**

`web/src/lib/entities.test.ts`：

```ts
import { describe, expect, it } from 'vitest';
import type { Entity } from '../api/types';
import { buildTree, extractLinks, safeHref, type RichNode } from './entities';

const e = (type: string, offset: number, length: number, extra: Partial<Entity> = {}): Entity => ({
  type,
  offset,
  length,
  ...extra,
});

// Compact serialisation: entities as type(children), text as-is.
function show(nodes: RichNode[]): string {
  return nodes.map((n) => (n.kind === 'text' ? n.text : `${n.entity.type}(${show(n.children)})`)).join('');
}

describe('buildTree', () => {
  it('returns plain text when there are no entities', () => {
    expect(show(buildTree('hello', []))).toBe('hello');
  });

  it('nests entities that share a range and contained ones', () => {
    const text = 'bold italic tail';
    const tree = buildTree(text, [e('italic', 5, 6), e('bold', 0, 11)]);
    expect(show(tree)).toBe('bold(bold italic(italic)) tail');
  });

  it('splits partially overlapping entities at the outer boundary', () => {
    // bold "abcd", italic "cdef"
    const tree = buildTree('abcdef', [e('bold', 0, 4), e('italic', 2, 4)]);
    expect(show(tree)).toBe('bold(abitalic(cd))italic(ef)');
  });

  it('uses UTF-16 offsets so astral emoji count as two units', () => {
    const text = '👍 ok';
    const tree = buildTree(text, [e('bold', 3, 2)]);
    expect(show(tree)).toBe('👍 bold(ok)');
  });

  it('puts block entities outside inline ones on the same range', () => {
    const tree = buildTree('code', [e('bold', 0, 4), e('pre', 0, 4, { language: 'go' })]);
    expect(show(tree)).toBe('pre(bold(code))');
  });

  it('drops empty, negative and clamps out-of-range entities', () => {
    const tree = buildTree('abc', [e('bold', 1, 0), e('italic', -1, 2), e('underline', 2, 50)]);
    expect(show(tree)).toBe('abunderline(c)');
  });
});

describe('links', () => {
  it('accepts web, tg, mail and tel links and adds https to bare hosts', () => {
    expect(safeHref('https://x.dev/a')).toBe('https://x.dev/a');
    expect(safeHref('x.dev')).toBe('https://x.dev/');
    expect(safeHref('tg://resolve?domain=a')).toBe('tg://resolve?domain=a');
    expect(safeHref('mailto:a@b.c')).toBe('mailto:a@b.c');
  });

  it('rejects script and data URLs', () => {
    expect(safeHref('javascript:alert(1)')).toBeNull();
    expect(safeHref('data:text/html,hi')).toBeNull();
    expect(safeHref('')).toBeNull();
  });

  it('extracts url and text_link targets in order without duplicates', () => {
    const text = 'see x.dev and here and x.dev';
    const links = extractLinks(text, [
      e('url', 23, 5),
      e('text_link', 14, 4, { url: 'https://y.dev' }),
      e('url', 4, 5),
      e('text_link', 0, 3, { url: 'javascript:1' }),
    ]);
    expect(links).toEqual(['https://x.dev/', 'https://y.dev/']);
  });
});
```

`web/src/components/message/RichText.test.tsx`：

```tsx
import { fireEvent, render, screen } from '@testing-library/preact';
import { describe, expect, it } from 'vitest';
import type { Entity } from '../../api/types';
import { RichText } from './RichText';

const e = (type: string, offset: number, length: number, extra: Partial<Entity> = {}): Entity => ({ type, offset, length, ...extra });

function html(text: string, entities: Entity[]) {
  const { container } = render(<RichText text={text} entities={entities} />);
  return container;
}

describe('RichText', () => {
  it('renders the formatting entities', () => {
    const c = html('b i u s c', [e('bold', 0, 1), e('italic', 2, 1), e('underline', 4, 1), e('strikethrough', 6, 1), e('code', 8, 1)]);
    expect(c.querySelector('strong')?.textContent).toBe('b');
    expect(c.querySelector('em')?.textContent).toBe('i');
    expect(c.querySelector('u')?.textContent).toBe('u');
    expect(c.querySelector('del')?.textContent).toBe('s');
    expect(c.querySelector('code.text-entity-code')?.textContent).toBe('c');
  });

  it('renders code blocks with their language', () => {
    const c = html('fmt.Println()', [e('pre', 0, 13, { language: 'go' })]);
    const pre = c.querySelector('pre.text-entity-pre')!;
    expect(pre.getAttribute('data-language')).toBe('go');
    expect(pre.querySelector('.code-language')?.textContent).toBe('go');
    expect(pre.querySelector('code')?.textContent).toBe('fmt.Println()');
  });

  it('renders safe links and neutralises unsafe ones', () => {
    const c = html('x.dev site bad', [e('url', 0, 5), e('text_link', 6, 4, { url: 'https://y.dev' }), e('text_link', 11, 3, { url: 'javascript:alert(1)' })]);
    const links = c.querySelectorAll('a');
    expect(links).toHaveLength(2);
    expect(links[0].getAttribute('href')).toBe('https://x.dev/');
    expect(links[1].getAttribute('href')).toBe('https://y.dev/');
    expect(links[1].getAttribute('rel')).toBe('noopener noreferrer');
    expect(c.querySelector('span.text-entity-link')?.textContent).toBe('bad');
  });

  it('links mentions, emails and phones; styles hashtags without a link', () => {
    const c = html('@alice a@b.co +1 555 #tag $USD /start', [
      e('mention', 0, 6),
      e('email', 7, 6),
      e('phone_number', 14, 6),
      e('hashtag', 21, 4),
      e('cashtag', 26, 4),
      e('bot_command', 31, 6),
    ]);
    const hrefs = [...c.querySelectorAll('a')].map((a) => a.getAttribute('href'));
    expect(hrefs).toEqual(['https://t.me/alice', 'mailto:a@b.co', 'tel:+1555']);
    expect([...c.querySelectorAll('span.text-entity-link')].map((s) => s.textContent)).toEqual(['#tag', '$USD', '/start']);
  });

  it('hides spoilers until clicked', () => {
    const c = html('secret', [e('spoiler', 0, 6)]);
    const sp = screen.getByRole('button', { name: '显示剧透内容' });
    expect(sp.classList.contains('revealed')).toBe(false);
    fireEvent.click(sp);
    expect(c.querySelector('.text-entity-spoiler')?.classList.contains('revealed')).toBe(true);
  });

  it('renders quotes, expandable quotes and custom emoji fallbacks', () => {
    const c = html('quote long 😀', [e('blockquote', 0, 5), e('expandable_blockquote', 6, 4), e('custom_emoji', 11, 2, { custom_emoji_id: '99' })]);
    expect(c.querySelector('blockquote.text-entity-quote:not(.expandable)')?.textContent).toBe('quote');
    fireEvent.click(screen.getByRole('button', { name: '展开引用' }));
    expect(c.querySelector('blockquote.expandable')?.classList.contains('expanded')).toBe(true);
    const emoji = c.querySelector('.custom-emoji')!;
    expect(emoji.textContent).toBe('😀');
    expect(emoji.getAttribute('data-custom-emoji-id')).toBe('99');
  });

  it('keeps newlines in the text for pre-wrap rendering', () => {
    expect(html('a\nb', []).textContent).toBe('a\nb');
  });
});
```


- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/lib/entities src/components/message`
Expected: FAIL（`Failed to resolve import "./entities"`、`"./RichText"`）

- [ ] **Step 3: 实现**

`web/src/lib/entities.ts`：

```ts
import type { Entity } from '../api/types';

export type RichNode =
  | { kind: 'text'; text: string }
  | { kind: 'entity'; entity: Entity; text: string; children: RichNode[] };

// Block-level entities sort outside inline ones when they share a range.
const NESTING_ORDER = ['pre', 'blockquote', 'expandable_blockquote', 'text_link', 'url', 'spoiler'];

function rank(type: string): number {
  const i = NESTING_ORDER.indexOf(type);
  return i === -1 ? NESTING_ORDER.length : i;
}

function byStart(a: Entity, b: Entity): number {
  return a.offset - b.offset || b.length - a.length || rank(a.type) - rank(b.type);
}

/**
 * Turns Telegram text + entities (UTF-16 offsets, which match JS string indexing) into a
 * properly nested tree. Entities that partially overlap are split at the outer boundary.
 */
export function buildTree(text: string, entities: Entity[]): RichNode[] {
  const clean = entities
    .filter((e) => Number.isFinite(e.offset) && Number.isFinite(e.length) && e.length > 0 && e.offset >= 0)
    .map((e) => ({ ...e, length: Math.min(e.length, text.length - e.offset) }))
    .filter((e) => e.length > 0);
  return build(text, 0, text.length, clean.sort(byStart));
}

function build(text: string, start: number, end: number, sorted: Entity[]): RichNode[] {
  const out: RichNode[] = [];
  let pos = start;
  let queue = sorted;
  while (queue.length > 0) {
    const [head, ...others] = queue;
    const headEnd = head.offset + head.length;
    if (head.offset > pos) out.push({ kind: 'text', text: text.slice(pos, head.offset) });
    const inner: Entity[] = [];
    const rest: Entity[] = [];
    for (const e of others) {
      const eEnd = e.offset + e.length;
      if (e.offset >= headEnd) {
        rest.push(e);
      } else if (eEnd <= headEnd) {
        inner.push(e);
      } else {
        inner.push({ ...e, length: headEnd - e.offset });
        rest.push({ ...e, offset: headEnd, length: eEnd - headEnd });
      }
    }
    out.push({
      kind: 'entity',
      entity: head,
      text: text.slice(head.offset, headEnd),
      children: build(text, head.offset, headEnd, inner),
    });
    pos = headEnd;
    queue = rest.sort(byStart);
  }
  if (pos < end) out.push({ kind: 'text', text: text.slice(pos, end) });
  return out;
}

const SAFE_PROTOCOLS = ['http:', 'https:', 'tg:', 'mailto:', 'tel:'];

/** Returns a navigable href or null for anything that is not an allowed scheme (e.g. javascript:). */
export function safeHref(raw: string): string | null {
  const v = raw.trim();
  if (!v) return null;
  const withScheme = /^[a-z][a-z0-9+.-]*:/i.test(v) ? v : `https://${v}`;
  try {
    const u = new URL(withScheme);
    return SAFE_PROTOCOLS.includes(u.protocol) ? u.href : null;
  } catch {
    return null;
  }
}

/** All link targets in a message (url + text_link entities), in order, de-duplicated. */
export function extractLinks(text: string, entities: Entity[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const e of [...entities].sort(byStart)) {
    let href: string | null = null;
    if (e.type === 'url') href = safeHref(text.slice(e.offset, e.offset + e.length));
    else if (e.type === 'text_link' && e.url) href = safeHref(e.url);
    if (href && !seen.has(href)) {
      seen.add(href);
      out.push(href);
    }
  }
  return out;
}
```

`web/src/components/message/RichText.tsx`：

```tsx
import type { ComponentChildren } from 'preact';
import { useMemo, useState } from 'preact/hooks';
import type { Entity } from '../../api/types';
import { buildTree, safeHref, type RichNode } from '../../lib/entities';
import './RichText.scss';

function Spoiler({ children }: { children: ComponentChildren }) {
  const [revealed, setRevealed] = useState(false);
  return (
    <span
      class={`text-entity-spoiler${revealed ? ' revealed' : ''}`}
      role={revealed ? undefined : 'button'}
      aria-label={revealed ? undefined : '显示剧透内容'}
      onClick={(e) => {
        if (!revealed) {
          e.preventDefault();
          e.stopPropagation();
          setRevealed(true);
        }
      }}
    >
      <span class="spoiler-content">{children}</span>
    </span>
  );
}

function ExpandableQuote({ children }: { children: ComponentChildren }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <blockquote class={`text-entity-quote expandable${expanded ? ' expanded' : ''}`}>
      <span class="quote-body">{children}</span>
      <button type="button" class="quote-toggle" aria-label={expanded ? '收起引用' : '展开引用'} onClick={() => setExpanded(!expanded)}>
        {expanded ? '收起' : '展开'}
      </button>
    </blockquote>
  );
}

function Link({ href, children }: { href: string | null; children: ComponentChildren }) {
  if (!href) return <span class="text-entity-link">{children}</span>;
  return (
    <a class="text-entity-link" href={href} target="_blank" rel="noopener noreferrer" onClick={(e) => e.stopPropagation()}>
      {children}
    </a>
  );
}

function renderNode(node: RichNode, key: number): ComponentChildren {
  if (node.kind === 'text') return node.text;
  const kids = node.children.map(renderNode);
  const e = node.entity;
  switch (e.type) {
    case 'bold':
      return <strong key={key}>{kids}</strong>;
    case 'italic':
      return <em key={key}>{kids}</em>;
    case 'underline':
      return <u key={key}>{kids}</u>;
    case 'strikethrough':
      return <del key={key}>{kids}</del>;
    case 'spoiler':
      return <Spoiler key={key}>{kids}</Spoiler>;
    case 'code':
      return (
        <code key={key} class="text-entity-code">
          {kids}
        </code>
      );
    case 'pre':
      return (
        <pre key={key} class="text-entity-pre" data-language={e.language || undefined}>
          {e.language && <span class="code-language">{e.language}</span>}
          <code>{kids}</code>
        </pre>
      );
    case 'blockquote':
      return (
        <blockquote key={key} class="text-entity-quote">
          {kids}
        </blockquote>
      );
    case 'expandable_blockquote':
      return <ExpandableQuote key={key}>{kids}</ExpandableQuote>;
    case 'url':
      return (
        <Link key={key} href={safeHref(node.text)}>
          {kids}
        </Link>
      );
    case 'text_link':
      return (
        <Link key={key} href={safeHref(e.url ?? '')}>
          {kids}
        </Link>
      );
    case 'mention':
      return (
        <Link key={key} href={safeHref(`https://t.me/${node.text.replace(/^@/, '')}`)}>
          {kids}
        </Link>
      );
    case 'email':
      return (
        <Link key={key} href={safeHref(`mailto:${node.text}`)}>
          {kids}
        </Link>
      );
    case 'phone_number':
      return (
        <Link key={key} href={safeHref(`tel:${node.text.replace(/[^0-9+]/g, '')}`)}>
          {kids}
        </Link>
      );
    case 'text_mention':
    case 'hashtag':
    case 'cashtag':
    case 'bot_command':
      return (
        <span key={key} class="text-entity-link">
          {kids}
        </span>
      );
    case 'custom_emoji':
      // The text under a custom emoji entity is its fallback emoji.
      return (
        <span key={key} class="custom-emoji" data-custom-emoji-id={e.custom_emoji_id}>
          {kids}
        </span>
      );
    default:
      return <span key={key}>{kids}</span>;
  }
}

export function RichText({ text, entities }: { text: string; entities: Entity[] }) {
  const nodes = useMemo(() => buildTree(text, entities ?? []), [text, entities]);
  return <>{nodes.map(renderNode)}</>;
}
```

`web/src/components/message/RichText.scss`：

```scss
.text-entity-link {
  color: var(--color-links) !important;
  text-decoration: none;
  cursor: pointer;
  word-break: break-word;

  &:hover {
    text-decoration: underline;
  }
}

span.text-entity-link {
  cursor: default;

  &:hover {
    text-decoration: none;
  }
}

.text-entity-code {
  padding: 1px 2px;
  border-radius: 4px;
  font-family: var(--font-family-monospace);
  font-size: 0.875rem;
  color: var(--color-code);
  background: var(--color-code-bg);
  word-break: break-word;
}

.text-entity-pre {
  position: relative;
  display: block;
  margin: 0.25rem 0;
  padding: 0.5rem;
  border-radius: 4px;
  font-family: var(--font-family-monospace);
  font-size: 0.875rem;
  color: var(--color-code);
  background: var(--color-code-bg);
  white-space: pre;
  overflow-x: auto;
  scrollbar-width: thin;
  scrollbar-color: var(--color-scrollbar-code) transparent;

  code {
    font-family: inherit;
  }

  .code-language {
    display: block;
    margin-bottom: 0.25rem;
    font-family: var(--font-family);
    font-size: 0.75rem;
    font-weight: var(--font-weight-semibold);
    color: var(--accent-color, var(--color-primary));
  }
}

.text-entity-quote {
  position: relative;
  display: block;
  margin: 0.25rem 0;
  padding: 0.125rem 1rem 0.125rem 0.5625rem;
  border-radius: 4px;
  font-size: calc(var(--message-text-size) - 2px);
  background: var(--accent-background-color, var(--color-primary-tint));
  overflow: hidden;

  &::before {
    content: "";
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    width: 3px;
    background: var(--accent-color, var(--color-primary));
  }

  &::after {
    content: "\201D";
    position: absolute;
    top: 0;
    right: 0.25rem;
    font-size: 1rem;
    line-height: 1;
    color: var(--accent-color, var(--color-primary));
  }

  &.expandable {
    .quote-body {
      display: -webkit-box;
      -webkit-line-clamp: 3;
      -webkit-box-orient: vertical;
      overflow: hidden;
    }

    &.expanded .quote-body {
      display: block;
    }

    .quote-toggle {
      display: block;
      margin-top: 0.125rem;
      font-size: 0.75rem;
      color: var(--accent-color, var(--color-primary));
    }
  }
}

.text-entity-spoiler {
  position: relative;
  border-radius: 4px;
  cursor: pointer;
  background-image: radial-gradient(circle, var(--color-text-secondary) 0.6px, transparent 0.8px);
  background-size: 4px 4px;
  animation: spoiler-breathe 1.75s linear infinite;

  .spoiler-content {
    opacity: 0;
    transition: opacity 250ms ease;
  }

  &.revealed {
    background: none;
    animation: none;
    cursor: inherit;

    .spoiler-content {
      opacity: 1;
    }
  }
}

@keyframes spoiler-breathe {
  50% {
    opacity: 0.25;
  }
}

.custom-emoji {
  display: inline-block;
}
```


- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/lib/entities.ts web/src/lib/entities.test.ts web/src/components/message/RichText.tsx web/src/components/message/RichText.scss web/src/components/message/RichText.test.tsx
git commit -m "feat(web): message entity tree and rich text rendering with safe links"
```

---

### Task 8: 消息分组、相册拼图与语音波形

**Files:**
- Create: `web/src/lib/grouping.ts`、`web/src/lib/album.ts`、`web/src/lib/waveform.ts`
- Test: `web/src/lib/grouping.test.ts`、`web/src/lib/album.test.ts`、`web/src/lib/waveform.test.ts`

**Interfaces:**
- Consumes: Task 3 的 `Message`；Task 4 的 `dayKey`、`formatDayLabel`
- Produces:
  - `GROUP_GAP_SECONDS = 600`；`type BubbleContent`（`{ kind: 'message'; key; msg }` | `{ kind: 'album'; key; msgs }`）；`type Bubble = BubbleContent & { first: boolean; last: boolean }`；`type ListEntry`（`{ kind: 'date'; key; label }` | `{ kind: 'group'; key; bubbles }`）；`groupMessages(messages, now?): ListEntry[]`
  - `SIDE_TOP | SIDE_RIGHT | SIDE_BOTTOM | SIDE_LEFT`、`interface Size`、`interface Tile { x; y; w; h; sides }`、`interface AlbumLayout { width; height; tiles }`、`ALBUM_WIDTH = 464`、`layoutAlbum(sizes, width?)`、`fitMedia(size, maxWidth = 464, maxHeight = 512, minWidth = 100): Size`
  - `decodeWaveform(base64?)`、`resample(samples, bars)`、`spikeHeights(values)`、`barCount(duration)`、`SPIKE_WIDTH = 2`、`SPIKE_STEP = 4`、`SPIKE_HEIGHT = 23`

- [ ] **Step 1: 写失败测试**

`web/src/lib/grouping.test.ts`：

```ts
import { describe, expect, it } from 'vitest';
import type { Message } from '../api/types';
import { groupMessages, type ListEntry } from './grouping';

const at = (d: number, h: number, m = 0) => new Date(2026, 9, d, h, m).getTime() / 1000;

function msg(id: number, date: number, extra: Partial<Message> = {}): Message {
  return {
    id,
    chat_id: 1,
    tg_message_id: id,
    source: 'bot_update',
    media_group_id: '',
    date,
    edit_date: 0,
    kind: 'text',
    text: `m${id}`,
    entities: [],
    reply_to_tg_message_id: 0,
    origin_chat_title: '',
    origin_link: '',
    media: [],
    ...extra,
  };
}

function shape(entries: ListEntry[]): string[] {
  return entries.map((e) =>
    e.kind === 'date'
      ? `[${e.label}]`
      : e.bubbles
          .map((b) => {
            const ids = b.kind === 'album' ? `{${b.msgs.map((m) => m.id).join(',')}}` : String(b.msg.id);
            return `${ids}${b.first ? 'F' : ''}${b.last ? 'L' : ''}`;
          })
          .join(' '),
  );
}

const now = new Date(2026, 9, 4, 20, 0);

describe('groupMessages', () => {
  it('inserts date separators and marks first/last bubbles', () => {
    const list = [msg(1, at(3, 10)), msg(2, at(3, 10, 1)), msg(3, at(4, 9)), msg(4, at(4, 9, 2)), msg(5, at(4, 9, 3))];
    expect(shape(groupMessages(list, now))).toEqual(['[昨天]', '1F 2L', '[今天]', '3F 4 5L']);
  });

  it('breaks groups on gaps longer than ten minutes', () => {
    const list = [msg(1, at(4, 9)), msg(2, at(4, 9, 5)), msg(3, at(4, 9, 16))];
    expect(shape(groupMessages(list, now))).toEqual(['[今天]', '1F 2L', '3FL']);
  });

  it('collects adjacent media-group messages into one album bubble', () => {
    const list = [
      msg(1, at(4, 9)),
      msg(2, at(4, 9), { media_group_id: 'g', kind: 'photo' }),
      msg(3, at(4, 9), { media_group_id: 'g', kind: 'photo' }),
      msg(4, at(4, 9), { media_group_id: 'g', kind: 'video' }),
      msg(5, at(4, 9), { media_group_id: 'h', kind: 'photo' }),
      msg(6, at(4, 9, 1)),
    ];
    // A media group of one stays a plain message bubble.
    expect(shape(groupMessages(list, now))).toEqual(['[今天]', '1F {2,3,4} 5 6L']);
  });

  it('sorts by id regardless of input order', () => {
    const list = [msg(2, at(4, 9, 1)), msg(1, at(4, 9))];
    expect(shape(groupMessages(list, now))).toEqual(['[今天]', '1F 2L']);
  });

  it('returns nothing for an empty chat', () => {
    expect(groupMessages([], now)).toEqual([]);
  });
});
```

`web/src/lib/album.test.ts`：

```ts
import { describe, expect, it } from 'vitest';
import { SIDE_BOTTOM, SIDE_LEFT, SIDE_RIGHT, SIDE_TOP, fitMedia, layoutAlbum, type AlbumLayout, type Size } from './album';

const wide: Size = { width: 1600, height: 900 };
const tall: Size = { width: 900, height: 1600 };
const square: Size = { width: 1000, height: 1000 };

function checkInvariants(l: AlbumLayout) {
  for (const t of l.tiles) {
    expect(t.w).toBeGreaterThan(0);
    expect(t.h).toBeGreaterThan(0);
    expect(t.x + t.w).toBeLessThanOrEqual(l.width);
    expect(t.y + t.h).toBeLessThanOrEqual(l.height);
    // Edge flags must match geometry exactly.
    expect(Boolean(t.sides & SIDE_LEFT)).toBe(t.x === 0);
    expect(Boolean(t.sides & SIDE_TOP)).toBe(t.y === 0);
    expect(Boolean(t.sides & SIDE_RIGHT)).toBe(t.x + t.w === l.width);
    expect(Boolean(t.sides & SIDE_BOTTOM)).toBe(t.y + t.h === l.height);
  }
  // No overlaps, and the tiles cover the whole canvas.
  let area = 0;
  l.tiles.forEach((a, i) => {
    area += a.w * a.h;
    l.tiles.slice(i + 1).forEach((b) => {
      const overlap = a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
      expect(overlap).toBe(false);
    });
  });
  expect(area).toBe(l.width * l.height);
}

describe('layoutAlbum', () => {
  it('handles every album size from 1 to 10 with mixed ratios', () => {
    const pool = [wide, tall, square, { width: 3000, height: 500 }, { width: 400, height: 2000 }];
    for (let n = 1; n <= 10; n++) {
      const sizes = Array.from({ length: n }, (_, i) => pool[(i * 3 + n) % pool.length]);
      const l = layoutAlbum(sizes);
      expect(l.tiles).toHaveLength(n);
      expect(l.width).toBe(464);
      checkInvariants(l);
    }
  });

  it('places two similar wide items on top of each other', () => {
    const l = layoutAlbum([wide, wide]);
    expect(l.tiles[0].x).toBe(0);
    expect(l.tiles[1].x).toBe(0);
    expect(l.tiles[1].y).toBe(l.tiles[0].h);
  });

  it('places two portrait items side by side', () => {
    const l = layoutAlbum([tall, tall]);
    expect(l.tiles[0].y).toBe(0);
    expect(l.tiles[1].y).toBe(0);
    expect(l.tiles[1].x).toBe(l.tiles[0].w);
  });

  it('uses a tall left column when the first of three is portrait', () => {
    const l = layoutAlbum([tall, square, square]);
    expect(l.tiles[0]).toMatchObject({ x: 0, y: 0, h: l.height });
    expect(l.tiles[1].x).toBe(l.tiles[0].w);
    expect(l.tiles[2].y).toBe(l.tiles[1].h);
  });

  it('puts a wide first item on top for four', () => {
    const l = layoutAlbum([wide, square, square, square]);
    expect(l.tiles[0]).toMatchObject({ x: 0, y: 0, w: 464 });
    expect(new Set(l.tiles.slice(1).map((t) => t.y)).size).toBe(1);
  });

  it('treats missing dimensions as square', () => {
    const l = layoutAlbum([{ width: 0, height: 0 }, { width: 0, height: 0 }]);
    checkInvariants(l);
    expect(l.tiles[0].w).toBe(232);
  });
});

describe('fitMedia', () => {
  it('fits large images into the bubble', () => {
    expect(fitMedia({ width: 4000, height: 2000 })).toEqual({ width: 464, height: 232 });
    expect(fitMedia({ width: 1000, height: 4000 })).toEqual({ width: 128, height: 512 });
  });

  it('keeps small images at natural size but not below the minimum width', () => {
    expect(fitMedia({ width: 200, height: 100 })).toEqual({ width: 200, height: 100 });
    expect(fitMedia({ width: 20, height: 20 })).toEqual({ width: 100, height: 100 });
  });

  it('falls back to a square for unknown sizes', () => {
    expect(fitMedia({ width: 0, height: 0 })).toEqual({ width: 464, height: 464 });
  });
});
```

`web/src/lib/waveform.test.ts`：

```ts
import { describe, expect, it } from 'vitest';
import { barCount, decodeWaveform, resample, spikeHeights } from './waveform';

function pack(values: number[]): string {
  const bytes = new Uint8Array(Math.ceil((values.length * 5) / 8));
  values.forEach((v, i) => {
    const bit = i * 5;
    const word = (v & 31) << (bit & 7);
    bytes[bit >> 3] |= word & 0xff;
    if (word > 0xff) bytes[(bit >> 3) + 1] |= word >> 8;
  });
  return btoa(String.fromCharCode(...bytes));
}

describe('waveform', () => {
  it('decodes 5-bit little-endian packed samples', () => {
    const values = [0, 31, 7, 16, 1, 30, 12, 5];
    expect(decodeWaveform(pack(values))).toEqual(values);
  });

  it('returns no samples for missing or broken input', () => {
    expect(decodeWaveform(undefined)).toEqual([]);
    expect(decodeWaveform('***')).toEqual([]);
  });

  it('resamples by bucket peak', () => {
    expect(resample([1, 5, 2, 8, 3, 3], 3)).toEqual([5, 8, 3]);
    expect(resample([], 4)).toEqual([0, 0, 0, 0]);
    expect(resample([4], 3)).toEqual([4, 4, 4]);
  });

  it('scales to 23px with a 2px floor', () => {
    expect(spikeHeights([0, 15, 31])).toEqual([2, 11, 23]);
    expect(spikeHeights([0, 0])).toEqual([2, 2]);
  });

  it('chooses 20–50 bars by duration', () => {
    expect(barCount(1)).toBe(20);
    expect(barCount(15)).toBe(30);
    expect(barCount(600)).toBe(50);
  });
});
```


- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/lib/grouping src/lib/album src/lib/waveform`
Expected: FAIL（`Failed to resolve import "./grouping"` 等）

- [ ] **Step 3: 实现**

`web/src/lib/grouping.ts`：

```ts
import type { Message } from '../api/types';
import { dayKey, formatDayLabel } from './format';

/** Consecutive messages further apart than this start a new visual group. */
export const GROUP_GAP_SECONDS = 600;

export type BubbleContent = { kind: 'message'; key: string; msg: Message } | { kind: 'album'; key: string; msgs: Message[] };

export type Bubble = BubbleContent & { first: boolean; last: boolean };

export type ListEntry =
  | { kind: 'date'; key: string; label: string }
  | { kind: 'group'; key: string; bubbles: Bubble[] };

function lastMsg(b: BubbleContent): Message {
  return b.kind === 'message' ? b.msg : b.msgs[b.msgs.length - 1];
}

/**
 * Splits an ascending message list into date separators and sender groups. Messages of one
 * media group that are adjacent become a single album bubble.
 */
export function groupMessages(messages: Message[], now: Date = new Date()): ListEntry[] {
  const sorted = [...messages].sort((a, b) => a.id - b.id);
  const out: ListEntry[] = [];
  let currentDay = '';
  let group: BubbleContent[] = [];

  const flush = () => {
    if (group.length === 0) return;
    const bubbles = group.map((b, i) => ({ ...b, first: i === 0, last: i === group.length - 1 }));
    out.push({ kind: 'group', key: `g${bubbles[0].key}`, bubbles });
    group = [];
  };

  for (const m of sorted) {
    const day = dayKey(m.date);
    if (day !== currentDay) {
      flush();
      currentDay = day;
      out.push({ kind: 'date', key: `d${day}`, label: formatDayLabel(m.date, now) });
    }
    const prev = group[group.length - 1];
    if (prev && m.media_group_id && (prev.kind === 'album' ? prev.msgs[0] : prev.msg).media_group_id === m.media_group_id) {
      if (prev.kind === 'album') prev.msgs.push(m);
      else group[group.length - 1] = { kind: 'album', key: prev.key, msgs: [prev.msg, m] };
      continue;
    }
    if (prev && m.date - lastMsg(prev).date > GROUP_GAP_SECONDS) flush();
    group.push({ kind: 'message', key: String(m.id), msg: m });
  }
  flush();
  return out;
}
```

`web/src/lib/album.ts`：

```ts
// Album mosaic layout written from the documented behaviour of Telegram's grouped layout
// (fixed templates for 2–4 items, row packing for 5+); not a port of any Telegram source.

export const SIDE_TOP = 1;
export const SIDE_RIGHT = 2;
export const SIDE_BOTTOM = 4;
export const SIDE_LEFT = 8;

export interface Size {
  width: number;
  height: number;
}

export interface Tile {
  x: number;
  y: number;
  w: number;
  h: number;
  sides: number;
}

export interface AlbumLayout {
  width: number;
  height: number;
  tiles: Tile[];
}

export const ALBUM_WIDTH = 464;
const MIN_TILE_WIDTH = 100;

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

function ratioOf(s: Size): number {
  return s.width > 0 && s.height > 0 ? s.width / s.height : 1;
}

/** Lays out rows of item indices; every row spans the full width. */
function rowsLayout(rows: number[][], ratios: number[], width: number): AlbumLayout {
  const tiles: Tile[] = new Array(ratios.length);
  let y = 0;
  rows.forEach((row, ri) => {
    const sum = row.reduce((acc, i) => acc + ratios[i], 0);
    const h = Math.max(1, Math.round(width / sum));
    let x = 0;
    row.forEach((idx, ci) => {
      const last = ci === row.length - 1;
      const w = last ? width - x : Math.round(h * ratios[idx]);
      let sides = 0;
      if (ri === 0) sides |= SIDE_TOP;
      if (ri === rows.length - 1) sides |= SIDE_BOTTOM;
      if (ci === 0) sides |= SIDE_LEFT;
      if (last) sides |= SIDE_RIGHT;
      tiles[idx] = { x, y, w, h, sides };
      x += w;
    });
    y += h;
  });
  return { width, height: y, tiles };
}

/** One tall item on the left, the others stacked in a column on the right. */
function leftColumnLayout(ratios: number[], width: number): AlbumLayout | null {
  const [first, ...rest] = ratios;
  const inv = rest.reduce((acc, r) => acc + 1 / r, 0);
  const rightW = Math.round(width / (1 + first * inv));
  const leftW = width - rightW;
  if (rightW < MIN_TILE_WIDTH * 0.6 || leftW < MIN_TILE_WIDTH * 0.6) return null;
  const tiles: Tile[] = [];
  let y = 0;
  const heights = rest.map((r) => Math.max(1, Math.round(rightW / r)));
  heights.forEach((h, i) => {
    let sides = SIDE_RIGHT;
    if (i === 0) sides |= SIDE_TOP;
    if (i === heights.length - 1) sides |= SIDE_BOTTOM;
    tiles.push({ x: leftW, y, w: rightW, h, sides });
    y += h;
  });
  tiles.unshift({ x: 0, y: 0, w: leftW, h: y, sides: SIDE_TOP | SIDE_BOTTOM | SIDE_LEFT });
  return { width, height: y, tiles };
}

/** All ways to split n items into consecutive rows of 2–4 items (n ≤ 10 keeps this tiny). */
function compositions(n: number): number[][] {
  if (n === 0) return [[]];
  const out: number[][] = [];
  for (const k of [3, 2, 4]) {
    if (k > n) continue;
    for (const tail of compositions(n - k)) out.push([k, ...tail]);
  }
  return out;
}

function packedLayout(ratios: number[], width: number): AlbumLayout {
  const avg = ratios.reduce((a, b) => a + b, 0) / ratios.length;
  const cropped = ratios.map((r) => (avg > 1.1 ? clamp(r, 1, 2.75) : clamp(r, 0.6667, 1)));
  let best: { score: number; rows: number[][] } | null = null;
  for (const counts of compositions(cropped.length)) {
    const rows: number[][] = [];
    let i = 0;
    for (const c of counts) {
      rows.push(Array.from({ length: c }, (_, k) => i + k));
      i += c;
    }
    let height = 0;
    let tooNarrow = false;
    for (const row of rows) {
      const h = width / row.reduce((acc, idx) => acc + cropped[idx], 0);
      height += h;
      if (row.some((idx) => h * cropped[idx] < MIN_TILE_WIDTH)) tooNarrow = true;
    }
    const monotonic = counts.every((c, k) => k === 0 || c >= counts[k - 1]) || counts.every((c, k) => k === 0 || c <= counts[k - 1]);
    const score = Math.abs(height - width) + (tooNarrow ? width * 10 : 0) + (monotonic ? 0 : width * 0.2);
    if (!best || score < best.score) best = { score, rows };
  }
  return rowsLayout(best!.rows, cropped, width);
}

/**
 * Computes the mosaic for 2+ media of an album. Coordinates are integers in a canvas of
 * `width` px; render with percentages so the album scales down on narrow screens.
 */
export function layoutAlbum(sizes: Size[], width: number = ALBUM_WIDTH): AlbumLayout {
  const raw = sizes.map(ratioOf);
  const n = raw.length;
  if (n === 0) return { width, height: 0, tiles: [] };
  const r = raw.map((v) => clamp(v, 0.5, 3));
  if (n === 1) return rowsLayout([[0]], r, width);
  if (n === 2) {
    const similarWide = r[0] > 1.2 && r[1] > 1.2 && Math.abs(r[0] - r[1]) / Math.max(r[0], r[1]) < 0.3;
    return similarWide ? rowsLayout([[0], [1]], r, width) : rowsLayout([[0, 1]], r, width);
  }
  if (n === 3) {
    if (r[0] < 0.8) return leftColumnLayout(r, width) ?? rowsLayout([[0], [1, 2]], r, width);
    return rowsLayout([[0], [1, 2]], r, width);
  }
  if (n === 4) {
    if (r[0] >= 1.2) return rowsLayout([[0], [1, 2, 3]], r, width);
    return leftColumnLayout(r, width) ?? rowsLayout([[0, 1], [2, 3]], r, width);
  }
  return packedLayout(raw, width);
}

/** Single photo/video bubble size: fit inside maxWidth × maxHeight, never narrower than minWidth. */
export function fitMedia(size: Size, maxWidth = 464, maxHeight = 512, minWidth = 100): Size {
  const ratio = ratioOf(size);
  let width = Math.min(maxWidth, size.width > 0 ? size.width : maxWidth);
  let height = width / ratio;
  if (height > maxHeight) {
    height = maxHeight;
    width = height * ratio;
  }
  if (width < minWidth) {
    width = minWidth;
    height = Math.min(maxHeight, width / ratio);
  }
  return { width: Math.round(width), height: Math.round(height) };
}
```

`web/src/lib/waveform.ts`：

```ts
// Telegram voice waveforms are 5-bit samples packed little-endian into bytes; the Go API
// serialises the bytes as base64.

export function decodeWaveform(base64: string | undefined): number[] {
  if (!base64) return [];
  let bytes: Uint8Array;
  try {
    bytes = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0));
  } catch {
    return [];
  }
  const count = Math.floor((bytes.length * 8) / 5);
  const out: number[] = [];
  for (let i = 0; i < count; i++) {
    const bit = i * 5;
    const byte = bit >> 3;
    const word = bytes[byte] | ((bytes[byte + 1] ?? 0) << 8);
    out.push((word >> (bit & 7)) & 31);
  }
  return out;
}

/** Resamples to `bars` values by taking the peak of each bucket. */
export function resample(samples: number[], bars: number): number[] {
  if (bars <= 0) return [];
  if (samples.length === 0) return new Array(bars).fill(0);
  const out: number[] = [];
  for (let i = 0; i < bars; i++) {
    const start = Math.floor((i * samples.length) / bars);
    const end = Math.max(start + 1, Math.floor(((i + 1) * samples.length) / bars));
    let peak = 0;
    for (let j = start; j < end && j < samples.length; j++) peak = Math.max(peak, samples[j]);
    out.push(peak);
  }
  return out;
}

export const SPIKE_WIDTH = 2;
export const SPIKE_STEP = 4;
export const SPIKE_HEIGHT = 23;

/** Bar heights in px: proportional to the loudest bar, never below 2px. */
export function spikeHeights(values: number[]): number[] {
  const peak = Math.max(1, ...values);
  return values.map((v) => Math.max(2, Math.round((SPIKE_HEIGHT * v) / peak)));
}

/** Number of bars for a voice message of `duration` seconds (20–50). */
export function barCount(duration: number): number {
  return Math.max(20, Math.min(50, Math.round(duration * 2)));
}
```


- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/lib/grouping.ts web/src/lib/grouping.test.ts web/src/lib/album.ts web/src/lib/album.test.ts web/src/lib/waveform.ts web/src/lib/waveform.test.ts
git commit -m "feat(web): date/sender grouping, album mosaic layout and voice waveform decoding"
```

---

### Task 9: 媒体与卡片组件

**Files:**
- Create: `web/src/components/media/{util.ts,MediaStatus,Photo,Video,Animation,Document,Audio,Voice,TgsSticker,Sticker,VideoNote,Cards,Album,MessageMedia}.tsx`、`web/src/components/media/media.scss`
- Test: `web/src/components/media/media.test.tsx`

**Interfaces:**
- Consumes: Task 3 `mediaUrl`、类型；Task 4 `formatDuration`、`formatSize`、`fileExtension`、`fileColor`、`kindLabel`；Task 5 `useStore().retryMedia`；Task 6 `Avatar`、`Spinner`、`renderWithStore`；Task 8 `fitMedia`、`layoutAlbum`、波形函数
- Produces:
  - `mainMedia(msg)`、`readyThumb(msg)`（只返回已落盘缩略图）、`extraString(msg, key)`、`extraNumber(msg, key)`、`VISUAL_KINDS = ['photo', 'video', 'animation']`
  - `MediaStatus({ msg, media, thumb? })`（`正在下载…` / `下载失败` + 错误 + 「重试」/ `文件超过存档上限` + `仅保存了消息记录`）、`inlineStatus(media): string | null`、`RetryButton({ msg, media })`
  - `Photo({ msg, onOpen, fill? })`、`Video({ msg, onOpen, fill? })`（缩略图 + 播放按钮 `aria-label="播放视频"` + 时长徽标）、`Animation({ msg, onOpen, fill? })`（自动循环静音）：根元素都带 `media-inner` 类；`fill` 为相册格子用
  - `Document({ msg, media? })`（已落盘时整块图标是 `<a href="/media/<id>?download=1" download>`）、`Audio({ msg })`、`Voice({ msg })`、`Sticker({ msg })`、`TgsSticker({ src, fallback })`、`VideoNote({ msg })`、`Location`、`Contact`、`Poll`、`Dice`（均 `{ msg }`）
  - `Album({ msgs, onOpen(msg) })`：全为 photo/video 时拼图（每格 `.Album-tile[data-message-id]`），否则纵向文件列表
  - `MessageMedia({ msg, onOpen(msg) })`：按 `msg.kind` 分派，text / other 返回 `null`

- [ ] **Step 1: 写失败测试**

`web/src/components/media/media.test.tsx`：

```tsx
import { act, fireEvent, screen } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Message } from '../../api/types';
import { fakeApi, makeMedia, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { Album } from './Album';
import { Contact, Dice, Location, Poll } from './Cards';
import { Document } from './Document';
import { MessageMedia } from './MessageMedia';
import { Photo } from './Photo';
import { Sticker } from './Sticker';
import { Video } from './Video';
import { Voice } from './Voice';

afterEach(() => vi.unstubAllGlobals());

const photoMsg = (over: Partial<Message> = {}, state: 'done' | 'pending' | 'failed' | 'too_large' = 'done') =>
  makeMessage({ kind: 'photo', text: '', media: [makeMedia({ id: 100, state, error: state === 'failed' ? 'timeout' : '' })], ...over });

describe('Photo', () => {
  it('shows the archived image and opens the viewer on click', () => {
    const onOpen = vi.fn();
    const { container } = renderWithStore(<Photo msg={photoMsg()} onOpen={onOpen} />);
    const img = container.querySelector('img')!;
    expect(img.getAttribute('src')).toBe('/media/100');
    expect((container.firstChild as HTMLElement).style.width).toBe('464px');
    fireEvent.click(img);
    expect(onOpen).toHaveBeenCalled();
  });

  it('shows pending and too_large placeholders', () => {
    renderWithStore(<Photo msg={photoMsg({}, 'pending')} onOpen={() => {}} />);
    expect(screen.getByText('正在下载…')).toBeTruthy();
    renderWithStore(<Photo msg={photoMsg({}, 'too_large')} onOpen={() => {}} />);
    expect(screen.getByText('文件超过存档上限')).toBeTruthy();
  });

  it('retries failed media through the store', async () => {
    const api = fakeApi();
    renderWithStore(<Photo msg={photoMsg({}, 'failed')} onOpen={() => {}} />, api);
    expect(screen.getByText('timeout')).toBeTruthy();
    await act(async () => {
      fireEvent.click(screen.getByText('重试'));
    });
    expect(api.retryMedia).toHaveBeenCalledWith(100);
  });

  it('blurs spoiler media until revealed', () => {
    const onOpen = vi.fn();
    const { container } = renderWithStore(<Photo msg={photoMsg({ extra: { spoiler: true } })} onOpen={onOpen} />);
    expect(container.querySelector('img')!.className).toBe('media-spoiler-blur');
    fireEvent.click(screen.getByText('显示剧透内容'));
    expect(container.querySelector('img')!.className).toBe('');
    expect(onOpen).not.toHaveBeenCalled();
  });
});

describe('Video', () => {
  it('shows the thumbnail, duration and opens on play', () => {
    const onOpen = vi.fn();
    const msg = makeMessage({
      kind: 'video',
      media: [makeMedia({ id: 5, kind: 'video', duration: 75 }), makeMedia({ id: 6, role: 'thumb' })],
    });
    const { container } = renderWithStore(<Video msg={msg} onOpen={onOpen} />);
    expect(container.querySelector('img')!.getAttribute('src')).toBe('/media/6');
    expect(screen.getByText('1:15')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '播放视频' }));
    expect(onOpen).toHaveBeenCalled();
  });
});

describe('Document', () => {
  const doc = (state: 'done' | 'failed' | 'too_large') =>
    makeMessage({
      kind: 'document',
      media: [makeMedia({ id: 9, kind: 'document', mime: 'application/pdf', file_name: 'report.pdf', size: 1536, state, error: state === 'failed' ? 'bad' : '' })],
    });

  it('links archived files for download with name and size', () => {
    const { container } = renderWithStore(<Document msg={doc('done')} />);
    const a = container.querySelector('a.File-icon')!;
    expect(a.getAttribute('href')).toBe('/media/9?download=1');
    expect(a.getAttribute('download')).toBe('report.pdf');
    expect((a as HTMLElement).style.getPropertyValue('--file-color')).toBe('var(--color-error)');
    expect(screen.getByText('report.pdf')).toBeTruthy();
    expect(screen.getByText('1.5 KB')).toBeTruthy();
    expect(screen.getByText('pdf')).toBeTruthy();
  });

  it('shows failure with retry and the too_large note', () => {
    const { container } = renderWithStore(<Document msg={doc('failed')} />);
    expect(container.querySelector('a')).toBeNull();
    expect(screen.getByText('下载失败：bad')).toBeTruthy();
    expect(screen.getByText('重试')).toBeTruthy();
    renderWithStore(<Document msg={doc('too_large')} />);
    expect(screen.getByText('文件超过存档上限')).toBeTruthy();
  });
});

describe('Voice', () => {
  it('draws one bar per spike and toggles playback', () => {
    const msg = makeMessage({ kind: 'voice', media: [makeMedia({ kind: 'voice', mime: 'audio/ogg', duration: 15, waveform: btoa('ÿ\u0001\u0010') })] });
    const { container } = renderWithStore(<Voice msg={msg} />);
    expect(container.querySelectorAll('rect')).toHaveLength(30);
    expect(screen.getByText('0:15')).toBeTruthy();
    const play = vi.spyOn(HTMLMediaElement.prototype, 'play');
    fireEvent.click(screen.getByRole('button', { name: '播放' }));
    expect(play).toHaveBeenCalled();
    fireEvent.play(container.querySelector('audio')!);
    expect(screen.getByRole('button', { name: '暂停' })).toBeTruthy();
  });
});

describe('Sticker', () => {
  const sticker = (mime: string) =>
    makeMessage({ kind: 'sticker', extra: { emoji: '😺' }, media: [makeMedia({ kind: 'sticker', mime, width: 512, height: 256 })] });

  it('renders webp as an image and webm as a looping video', () => {
    const { container } = renderWithStore(<Sticker msg={sticker('image/webp')} />);
    expect(container.querySelector('img')!.getAttribute('alt')).toBe('😺');
    expect((container.firstChild as HTMLElement).style.getPropertyValue('--sticker-h')).toBe('0.5');
    const v = renderWithStore(<Sticker msg={sticker('video/webm')} />).container.querySelector('video')!;
    expect(v.loop).toBe(true);
    expect(v.muted).toBe(true);
  });

  it('falls back to the emoji when a tgs sticker cannot load', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('offline'))));
    renderWithStore(<Sticker msg={sticker('application/x-tgsticker')} />);
    expect(await screen.findByText('😺')).toBeTruthy();
  });
});

describe('cards', () => {
  it('renders venue with OpenStreetMap link', () => {
    const msg = makeMessage({ kind: 'venue', extra: { latitude: 31.2, longitude: 121.5, title: '外滩', address: '中山东一路' } });
    const { container } = renderWithStore(<Location msg={msg} />);
    expect(container.querySelector('a')!.getAttribute('href')).toBe('https://www.openstreetmap.org/?mlat=31.2&mlon=121.5#map=16/31.2/121.5');
    expect(screen.getByText('外滩')).toBeTruthy();
    expect(screen.getByText('中山东一路')).toBeTruthy();
  });

  it('renders a bare location with coordinates', () => {
    renderWithStore(<Location msg={makeMessage({ kind: 'location', extra: { latitude: 1.5, longitude: 2 } })} />);
    expect(screen.getByText('1.500000, 2.000000')).toBeTruthy();
  });

  it('renders contact, poll and dice', () => {
    renderWithStore(<Contact msg={makeMessage({ kind: 'contact', extra: { first_name: 'Bob', phone_number: '+100' } })} />);
    expect(screen.getByText('Bob')).toBeTruthy();
    expect(screen.getByText('+100')).toBeTruthy();
    renderWithStore(
      <Poll
        msg={makeMessage({
          kind: 'poll',
          extra: { question: '吃什么', options: [{ text: '面', voter_count: 3 }, { text: '饭', voter_count: 1 }], total_voter_count: 4, is_anonymous: true, poll_type: 'regular', multiple: false },
        })}
      />,
    );
    expect(screen.getByText('75%')).toBeTruthy();
    expect(screen.getByText('25%')).toBeTruthy();
    expect(screen.getByText('4 人投票')).toBeTruthy();
    expect(screen.getByText('匿名投票')).toBeTruthy();
    renderWithStore(<Dice msg={makeMessage({ kind: 'dice', extra: { emoji: '🎯', value: 6 } })} />);
    expect(screen.getByText('🎯')).toBeTruthy();
    expect(screen.getByText('点数：6')).toBeTruthy();
  });
});

describe('Album and MessageMedia', () => {
  it('lays out photo albums as positioned tiles', () => {
    const msgs = [1, 2, 3].map((id) => photoMsg({ id, media: [makeMedia({ id: 100 + id, width: 900, height: 1600 })] }));
    const onOpen = vi.fn();
    const { container } = renderWithStore(<Album msgs={msgs} onOpen={onOpen} />);
    const tiles = container.querySelectorAll<HTMLElement>('.Album-tile');
    expect(tiles).toHaveLength(3);
    expect(tiles[0].style.left).toBe('0%');
    expect(tiles[1].getAttribute('data-message-id')).toBe('2');
    fireEvent.click(tiles[2].querySelector('img')!);
    expect(onOpen).toHaveBeenCalledWith(msgs[2]);
  });

  it('stacks document albums as rows', () => {
    const msgs = [1, 2].map((id) => makeMessage({ id, kind: 'document', media: [makeMedia({ id: id + 10, kind: 'document', file_name: `f${id}.txt` })] }));
    const { container } = renderWithStore(<Album msgs={msgs} onOpen={() => {}} />);
    expect(container.querySelectorAll('.Album-list-item')).toHaveLength(2);
    expect(screen.getByText('f2.txt')).toBeTruthy();
  });

  it('renders nothing for text and unsupported kinds', () => {
    const { container } = renderWithStore(<MessageMedia msg={makeMessage({ kind: 'other' })} onOpen={() => {}} />);
    expect(container.innerHTML).toBe('');
  });
});
```


- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/components/media`
Expected: FAIL（`Failed to resolve import "./Album"` 等）

- [ ] **Step 3: 实现**


`web/src/components/media/util.ts`：

```ts
import type { Media, Message } from '../../api/types';

export function mainMedia(msg: Message): Media | undefined {
  return msg.media.find((m) => m.role === 'main');
}

/** The thumbnail, only when it has been downloaded. */
export function readyThumb(msg: Message): Media | undefined {
  return msg.media.find((m) => m.role === 'thumb' && m.state === 'done');
}

export function extraString(msg: Message, key: string): string {
  const v = msg.extra?.[key];
  return typeof v === 'string' ? v : '';
}

export function extraNumber(msg: Message, key: string): number {
  const v = msg.extra?.[key];
  return typeof v === 'number' ? v : 0;
}

/** Kinds that open in the media viewer. */
export const VISUAL_KINDS = ['photo', 'video', 'animation'];
```


`web/src/components/media/MediaStatus.tsx`：

```tsx
import { CircleAlert, FileWarning, RotateCw } from 'lucide-preact';
import { mediaUrl } from '../../api/client';
import type { Media, Message } from '../../api/types';
import { useStore } from '../../state/store';
import { Spinner } from '../../ui/Spinner';
import './media.scss';

interface Props {
  msg: Message;
  media: Media;
  thumb?: Media;
}

/** Placeholder for media that is not downloaded: pending / failed (with retry) / too_large. */
export function MediaStatus({ msg, media, thumb }: Props) {
  const store = useStore();
  return (
    <div class={`MediaStatus state-${media.state}`}>
      {thumb && <img class="MediaStatus-thumb" src={mediaUrl(thumb.id)} alt="" />}
      <div class="MediaStatus-body">
        {media.state === 'pending' && (
          <>
            <Spinner size={32} />
            <span class="MediaStatus-text">正在下载…</span>
          </>
        )}
        {media.state === 'failed' && (
          <>
            <CircleAlert size={32} />
            <span class="MediaStatus-text">下载失败</span>
            {media.error && <span class="MediaStatus-error">{media.error}</span>}
            <button
              type="button"
              class="MediaStatus-retry"
              onClick={(e) => {
                e.stopPropagation();
                void store.retryMedia(msg, media.id);
              }}
            >
              <RotateCw size={16} />
              重试
            </button>
          </>
        )}
        {media.state === 'too_large' && (
          <>
            <FileWarning size={32} />
            <span class="MediaStatus-text">文件超过存档上限</span>
            <span class="MediaStatus-error">仅保存了消息记录</span>
          </>
        )}
      </div>
    </div>
  );
}

/** One-line status used inside file rows; null when the media is downloaded. */
export function inlineStatus(media: Media): string | null {
  switch (media.state) {
    case 'pending':
      return '正在下载…';
    case 'failed':
      return media.error ? `下载失败：${media.error}` : '下载失败';
    case 'too_large':
      return '文件超过存档上限';
    default:
      return null;
  }
}

export function RetryButton({ msg, media }: { msg: Message; media: Media }) {
  const store = useStore();
  return (
    <button
      type="button"
      class="RetryLink"
      onClick={(e) => {
        e.stopPropagation();
        void store.retryMedia(msg, media.id);
      }}
    >
      重试
    </button>
  );
}
```


`web/src/components/media/Photo.tsx`：

```tsx
import { useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { fitMedia } from '../../lib/album';
import { MediaStatus } from './MediaStatus';
import { mainMedia, readyThumb } from './util';
import './media.scss';

interface Props {
  msg: Message;
  onOpen: () => void;
  /** Fill the parent (album tile) instead of sizing itself. */
  fill?: boolean;
}

export function Photo({ msg, onOpen, fill }: Props) {
  const main = mainMedia(msg);
  const [revealed, setRevealed] = useState(false);
  if (!main) return null;
  const spoiler = msg.extra?.spoiler === true && !revealed;
  const size = fitMedia({ width: main.width, height: main.height });
  const style = fill ? undefined : { width: `${size.width}px`, aspectRatio: `${size.width} / ${size.height}` };
  return (
    <div class={`media-inner Photo${fill ? ' fill' : ''}`} style={style}>
      {main.state === 'done' ? (
        <img
          src={mediaUrl(main.id)}
          alt=""
          loading="lazy"
          decoding="async"
          class={spoiler ? 'media-spoiler-blur' : ''}
          onClick={() => (spoiler ? setRevealed(true) : onOpen())}
        />
      ) : (
        <MediaStatus msg={msg} media={main} thumb={readyThumb(msg)} />
      )}
      {spoiler && main.state === 'done' && (
        <button type="button" class="media-spoiler" onClick={() => setRevealed(true)}>
          显示剧透内容
        </button>
      )}
    </div>
  );
}
```


`web/src/components/media/Video.tsx`：

```tsx
import { Play } from 'lucide-preact';
import { useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { fitMedia } from '../../lib/album';
import { formatDuration } from '../../lib/format';
import { MediaStatus } from './MediaStatus';
import { mainMedia, readyThumb } from './util';
import './media.scss';

interface Props {
  msg: Message;
  onOpen: () => void;
  fill?: boolean;
}

/** Video bubble: thumbnail + play button + duration badge; plays in the media viewer. */
export function Video({ msg, onOpen, fill }: Props) {
  const main = mainMedia(msg);
  const [revealed, setRevealed] = useState(false);
  if (!main) return null;
  const thumb = readyThumb(msg);
  const spoiler = msg.extra?.spoiler === true && !revealed;
  const size = fitMedia({ width: main.width, height: main.height });
  const style = fill ? undefined : { width: `${size.width}px`, aspectRatio: `${size.width} / ${size.height}` };
  const done = main.state === 'done';
  return (
    <div class={`media-inner Video${fill ? ' fill' : ''}`} style={style}>
      {thumb ? (
        <img src={mediaUrl(thumb.id)} alt="" loading="lazy" class={spoiler ? 'media-spoiler-blur' : ''} />
      ) : done ? (
        <video src={`${mediaUrl(main.id)}#t=0.1`} preload="metadata" muted playsInline class={spoiler ? 'media-spoiler-blur' : ''} />
      ) : null}
      {done ? (
        <button
          type="button"
          class="media-play"
          aria-label="播放视频"
          onClick={() => (spoiler ? setRevealed(true) : onOpen())}
        >
          <Play size={28} fill="currentColor" />
        </button>
      ) : (
        <MediaStatus msg={msg} media={main} />
      )}
      <span class="MediaBadge">{formatDuration(main.duration)}</span>
    </div>
  );
}
```


`web/src/components/media/Animation.tsx`：

```tsx
import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { fitMedia } from '../../lib/album';
import { MediaStatus } from './MediaStatus';
import { mainMedia, readyThumb } from './util';
import './media.scss';

interface Props {
  msg: Message;
  onOpen: () => void;
  fill?: boolean;
}

/** GIF: loops muted and inline, like Telegram. */
export function Animation({ msg, onOpen, fill }: Props) {
  const main = mainMedia(msg);
  if (!main) return null;
  const size = fitMedia({ width: main.width, height: main.height });
  const style = fill ? undefined : { width: `${size.width}px`, aspectRatio: `${size.width} / ${size.height}` };
  const thumb = readyThumb(msg);
  return (
    <div class={`media-inner Animation${fill ? ' fill' : ''}`} style={style}>
      {main.state === 'done' ? (
        <video
          src={mediaUrl(main.id)}
          poster={thumb ? mediaUrl(thumb.id) : undefined}
          autoplay
          loop
          muted
          playsInline
          disablePictureInPicture
          onClick={onOpen}
        />
      ) : (
        <MediaStatus msg={msg} media={main} thumb={thumb} />
      )}
      <span class="MediaBadge">GIF</span>
    </div>
  );
}
```


`web/src/components/media/Document.tsx`：

```tsx
import { CircleAlert, Download } from 'lucide-preact';
import { mediaUrl } from '../../api/client';
import type { Media, Message } from '../../api/types';
import { fileColor, fileExtension, formatSize, kindLabel } from '../../lib/format';
import { Spinner } from '../../ui/Spinner';
import { RetryButton, inlineStatus } from './MediaStatus';
import { mainMedia, readyThumb } from './util';
import './media.scss';

/** File row: dog-eared extension tile (or thumbnail), name, size; downloads when archived. */
export function Document({ msg, media }: { msg: Message; media?: Media }) {
  const main = media ?? mainMedia(msg);
  if (!main) return null;
  const ext = fileExtension(main.file_name, main.mime);
  const thumb = readyThumb(msg);
  const done = main.state === 'done';
  const status = inlineStatus(main);
  const tile = (
    <>
      {thumb ? <img src={mediaUrl(thumb.id)} alt="" /> : <span class="File-ext">{ext}</span>}
      <span class="File-overlay">
        {main.state === 'pending' && <Spinner size={24} color="#fff" />}
        {(main.state === 'failed' || main.state === 'too_large') && <CircleAlert size={24} />}
        {done && <Download size={24} class="File-download" />}
      </span>
    </>
  );
  return (
    <div class="File">
      {done ? (
        <a
          class={`File-icon${thumb ? ' with-thumb' : ''}`}
          style={{ '--file-color': fileColor(ext) }}
          href={mediaUrl(main.id, true)}
          download={main.file_name || undefined}
          aria-label={`下载 ${main.file_name || kindLabel(msg.kind)}`}
        >
          {tile}
        </a>
      ) : (
        <div class={`File-icon${thumb ? ' with-thumb' : ''} not-ready`} style={{ '--file-color': fileColor(ext) }}>
          {tile}
        </div>
      )}
      <div class="File-info">
        <div class="File-title" title={main.file_name}>
          {main.file_name || kindLabel(msg.kind)}
        </div>
        <div class="File-subtitle">
          {status ? (
            <>
              <span class={main.state === 'pending' ? '' : 'File-error'}>{status}</span>
              {main.size > 0 && <span> · {formatSize(main.size)}</span>}
              {main.state === 'failed' && <RetryButton msg={msg} media={main} />}
            </>
          ) : (
            formatSize(main.size)
          )}
        </div>
      </div>
    </div>
  );
}
```


`web/src/components/media/Audio.tsx`：

```tsx
import { Pause, Play } from 'lucide-preact';
import { useRef, useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { formatDuration } from '../../lib/format';
import { Document } from './Document';
import { extraString, mainMedia } from './util';
import './media.scss';

/** Music file: round play button, title/performer, seekable progress line while playing. */
export function Audio({ msg }: { msg: Message }) {
  const main = mainMedia(msg);
  const ref = useRef<HTMLAudioElement>(null);
  const [playing, setPlaying] = useState(false);
  const [time, setTime] = useState(0);
  if (!main) return null;
  if (main.state !== 'done') return <Document msg={msg} />;
  const title = extraString(msg, 'title') || main.file_name || '音乐';
  const performer = extraString(msg, 'performer');
  const duration = main.duration || ref.current?.duration || 0;
  const toggle = () => {
    const a = ref.current;
    if (!a) return;
    if (a.paused) void a.play();
    else a.pause();
  };
  const seek = (e: MouseEvent) => {
    const a = ref.current;
    const el = e.currentTarget as HTMLElement;
    if (!a || !duration) return;
    const ratio = Math.min(1, Math.max(0, (e.clientX - el.getBoundingClientRect().left) / el.clientWidth));
    a.currentTime = ratio * duration;
  };
  return (
    <div class="Audio">
      <button type="button" class="toggle-play" aria-label={playing ? '暂停' : '播放'} onClick={toggle}>
        {playing ? <Pause size={24} fill="currentColor" /> : <Play size={24} fill="currentColor" />}
      </button>
      <div class="Audio-info">
        <div class="Audio-title">{title}</div>
        {playing || time > 0 ? (
          <>
            <div class="Audio-progress" onClick={seek} role="slider" aria-label="播放进度" aria-valuenow={Math.round(time)}>
              <div class="Audio-progress-fill" style={{ width: `${duration ? (time / duration) * 100 : 0}%` }} />
            </div>
            <div class="Audio-meta">
              {formatDuration(time)} / {formatDuration(duration)}
            </div>
          </>
        ) : (
          <div class="Audio-meta">
            {performer ? `${performer} · ` : ''}
            {formatDuration(duration)}
          </div>
        )}
      </div>
      <audio
        ref={ref}
        src={mediaUrl(main.id)}
        preload="none"
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onEnded={() => {
          setPlaying(false);
          setTime(0);
        }}
        onTimeUpdate={(e) => setTime((e.currentTarget as HTMLAudioElement).currentTime)}
      />
    </div>
  );
}
```


`web/src/components/media/Voice.tsx`：

```tsx
import { Pause, Play } from 'lucide-preact';
import { useMemo, useRef, useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { formatDuration } from '../../lib/format';
import { SPIKE_HEIGHT, SPIKE_STEP, SPIKE_WIDTH, barCount, decodeWaveform, resample, spikeHeights } from '../../lib/waveform';
import { Document } from './Document';
import { mainMedia } from './util';
import './media.scss';

/** Voice message: play button + 5-bit waveform; played part at full accent, the rest at 50%. */
export function Voice({ msg }: { msg: Message }) {
  const main = mainMedia(msg);
  const ref = useRef<HTMLAudioElement>(null);
  const [playing, setPlaying] = useState(false);
  const [time, setTime] = useState(0);
  const bars = useMemo(
    () => spikeHeights(resample(decodeWaveform(main?.waveform), barCount(main?.duration ?? 0))),
    [main?.waveform, main?.duration],
  );
  if (!main) return null;
  if (main.state !== 'done') return <Document msg={msg} />;
  const duration = main.duration || 1;
  const progress = Math.min(1, time / duration);
  const toggle = () => {
    const a = ref.current;
    if (!a) return;
    if (a.paused) void a.play();
    else a.pause();
  };
  const seek = (e: MouseEvent) => {
    const a = ref.current;
    const el = e.currentTarget as SVGElement;
    if (!a) return;
    const width = bars.length * SPIKE_STEP;
    const ratio = Math.min(1, Math.max(0, (e.clientX - el.getBoundingClientRect().left) / width));
    a.currentTime = ratio * duration;
    if (a.paused) void a.play();
  };
  return (
    <div class="Voice">
      <button type="button" class="toggle-play" aria-label={playing ? '暂停' : '播放'} onClick={toggle}>
        {playing ? <Pause size={24} fill="currentColor" /> : <Play size={24} fill="currentColor" />}
      </button>
      <div class="Voice-body">
        <svg
          class="Waveform"
          width={bars.length * SPIKE_STEP}
          height={SPIKE_HEIGHT}
          viewBox={`0 0 ${bars.length * SPIKE_STEP} ${SPIKE_HEIGHT}`}
          onClick={seek}
          role="slider"
          aria-label="播放进度"
          aria-valuenow={Math.round(progress * 100)}
        >
          {bars.map((h, i) => (
            <rect
              key={i}
              x={i * SPIKE_STEP}
              y={SPIKE_HEIGHT - h}
              width={SPIKE_WIDTH}
              height={h}
              rx={1}
              class={(i + 0.5) / bars.length <= progress ? 'played' : ''}
            />
          ))}
        </svg>
        <span class="Voice-time">{formatDuration(playing || time > 0 ? time : main.duration)}</span>
      </div>
      <audio
        ref={ref}
        src={mediaUrl(main.id)}
        preload="none"
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onEnded={() => {
          setPlaying(false);
          setTime(0);
        }}
        onTimeUpdate={(e) => setTime((e.currentTarget as HTMLAudioElement).currentTime)}
      />
    </div>
  );
}
```


`web/src/components/media/TgsSticker.tsx`：

```tsx
import { useEffect, useRef, useState } from 'preact/hooks';

/** Animated .tgs sticker: gzip-compressed Lottie JSON, decompressed natively and played with lottie-web (lazy chunk). */
export function TgsSticker({ src, fallback }: { src: string; fallback: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let destroy: (() => void) | undefined;
    (async () => {
      const res = await fetch(src);
      if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`);
      const json = await new Response(res.body.pipeThrough(new DecompressionStream('gzip'))).json();
      const { default: lottie } = await import('lottie-web/build/player/lottie_light');
      if (cancelled || !ref.current) return;
      const anim = lottie.loadAnimation({ container: ref.current, renderer: 'svg', loop: true, autoplay: true, animationData: json });
      destroy = () => anim.destroy();
    })().catch(() => {
      if (!cancelled) setFailed(true);
    });
    return () => {
      cancelled = true;
      destroy?.();
    };
  }, [src]);

  if (failed) return <span class="Sticker-fallback">{fallback || '贴纸'}</span>;
  return <div class="Sticker-lottie" ref={ref} />;
}
```


`web/src/components/media/Sticker.tsx`：

```tsx
import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { MediaStatus } from './MediaStatus';
import { TgsSticker } from './TgsSticker';
import { extraString, mainMedia } from './util';
import './media.scss';

/** Sticker without a bubble: webp image, webm video or tgs Lottie; 13rem box (11rem on phones). */
export function Sticker({ msg }: { msg: Message }) {
  const main = mainMedia(msg);
  if (!main) return null;
  const w = main.width || 512;
  const h = main.height || 512;
  const scale = Math.max(w, h);
  const style = { '--sticker-w': String(w / scale), '--sticker-h': String(h / scale) };
  const emoji = extraString(msg, 'emoji');
  let body;
  if (main.state !== 'done') body = <MediaStatus msg={msg} media={main} />;
  else if (main.mime === 'application/x-tgsticker') body = <TgsSticker src={mediaUrl(main.id)} fallback={emoji} />;
  else if (main.mime === 'video/webm')
    body = <video src={mediaUrl(main.id)} autoplay loop muted playsInline disablePictureInPicture aria-label={emoji || '贴纸'} />;
  else body = <img src={mediaUrl(main.id)} alt={emoji || '贴纸'} loading="lazy" decoding="async" />;
  return (
    <div class="Sticker" style={style} title={emoji || undefined}>
      {body}
    </div>
  );
}
```


`web/src/components/media/VideoNote.tsx`：

```tsx
import { useRef, useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { formatDuration } from '../../lib/format';
import { MediaStatus } from './MediaStatus';
import { mainMedia, readyThumb } from './util';
import './media.scss';

const SIZE = 240;
const RADIUS = SIZE / 2 - 4;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;

/** Round video message: loops muted; click plays from the start with sound and shows a progress ring. */
export function VideoNote({ msg }: { msg: Message }) {
  const main = mainMedia(msg);
  const ref = useRef<HTMLVideoElement>(null);
  const [withSound, setWithSound] = useState(false);
  const [progress, setProgress] = useState(0);
  if (!main) return null;
  const thumb = readyThumb(msg);
  const toggle = () => {
    const v = ref.current;
    if (!v) return;
    if (withSound) {
      v.muted = true;
      v.loop = true;
      setWithSound(false);
      setProgress(0);
      return;
    }
    v.currentTime = 0;
    v.muted = false;
    v.loop = false;
    setWithSound(true);
    void v.play();
  };
  return (
    <div class="VideoNote" style={{ width: `${SIZE}px`, height: `${SIZE}px` }}>
      {main.state === 'done' ? (
        <video
          ref={ref}
          src={mediaUrl(main.id)}
          poster={thumb ? mediaUrl(thumb.id) : undefined}
          autoplay
          loop
          muted
          playsInline
          disablePictureInPicture
          aria-label={withSound ? '停止播放' : '播放视频消息'}
          onClick={toggle}
          onTimeUpdate={(e) => {
            const v = e.currentTarget as HTMLVideoElement;
            if (withSound && v.duration) setProgress(v.currentTime / v.duration);
          }}
          onEnded={() => {
            const v = ref.current;
            if (v) {
              v.muted = true;
              v.loop = true;
              void v.play();
            }
            setWithSound(false);
            setProgress(0);
          }}
        />
      ) : (
        <MediaStatus msg={msg} media={main} thumb={thumb} />
      )}
      {withSound && (
        <svg class="VideoNote-ring" viewBox={`0 0 ${SIZE} ${SIZE}`} aria-hidden="true">
          <circle
            cx={SIZE / 2}
            cy={SIZE / 2}
            r={RADIUS}
            stroke-dasharray={`${CIRCUMFERENCE * progress} ${CIRCUMFERENCE}`}
          />
        </svg>
      )}
      <span class="MediaBadge">{formatDuration(main.duration)}</span>
    </div>
  );
}
```


`web/src/components/media/Cards.tsx`：

```tsx
import { MapPin } from 'lucide-preact';
import type { Message } from '../../api/types';
import { Avatar } from '../../ui/Avatar';
import { extraNumber, extraString } from './util';
import './media.scss';

/** Location / venue: pin card linking to OpenStreetMap (no third-party tiles are loaded). */
export function Location({ msg }: { msg: Message }) {
  const lat = extraNumber(msg, 'latitude');
  const lon = extraNumber(msg, 'longitude');
  const title = extraString(msg, 'title');
  const address = extraString(msg, 'address');
  const href = `https://www.openstreetmap.org/?mlat=${lat}&mlon=${lon}#map=16/${lat}/${lon}`;
  return (
    <a class="Location" href={href} target="_blank" rel="noopener noreferrer">
      <div class="Location-map">
        <MapPin size={40} class="Location-pin" />
      </div>
      <div class="Location-info">
        <div class="Location-title">{title || '位置'}</div>
        <div class="Location-subtitle">{address || `${lat.toFixed(6)}, ${lon.toFixed(6)}`}</div>
      </div>
    </a>
  );
}

export function Contact({ msg }: { msg: Message }) {
  const name = `${extraString(msg, 'first_name')} ${extraString(msg, 'last_name')}`.trim() || '联系人';
  const phone = extraString(msg, 'phone_number');
  const uid = extraNumber(msg, 'user_id');
  return (
    <div class="Contact">
      <Avatar name={name} peerId={uid || phone.length} size="large" />
      <div class="Contact-info">
        <div class="Contact-name">{name}</div>
        {phone && <div class="Contact-phone">{phone}</div>}
      </div>
    </div>
  );
}

interface PollOption {
  text: string;
  voter_count: number;
}

export function Poll({ msg }: { msg: Message }) {
  const question = extraString(msg, 'question');
  const options = (Array.isArray(msg.extra?.options) ? msg.extra!.options : []) as PollOption[];
  const total = extraNumber(msg, 'total_voter_count');
  const quiz = extraString(msg, 'poll_type') === 'quiz';
  const anonymous = msg.extra?.is_anonymous !== false;
  const multiple = msg.extra?.multiple === true;
  const kind = `${anonymous ? '匿名' : '公开'}${quiz ? '测验' : '投票'}${multiple ? ' · 多选' : ''}`;
  return (
    <div class="Poll">
      <div class="Poll-question">{question}</div>
      <div class="Poll-type">{kind}</div>
      <div class="Poll-options">
        {options.map((o, i) => {
          const pct = total > 0 ? Math.round((o.voter_count / total) * 100) : 0;
          return (
            <div class="Poll-option" key={i}>
              <span class="Poll-percent">{pct}%</span>
              <div class="Poll-option-body">
                <div class="Poll-option-text">{o.text}</div>
                <div class="Poll-bar" style={{ width: `${Math.max(pct, 1)}%` }} />
              </div>
            </div>
          );
        })}
      </div>
      <div class="Poll-total">{total > 0 ? `${total} 人投票` : '暂无投票'}</div>
    </div>
  );
}

export function Dice({ msg }: { msg: Message }) {
  const emoji = extraString(msg, 'emoji') || '🎲';
  const value = extraNumber(msg, 'value');
  return (
    <div class="Dice" title={`点数：${value}`}>
      <span class="Dice-emoji">{emoji}</span>
      <span class="Dice-value">点数：{value}</span>
    </div>
  );
}
```


`web/src/components/media/Album.tsx`：

```tsx
import type { Message } from '../../api/types';
import { layoutAlbum } from '../../lib/album';
import { Audio } from './Audio';
import { Document } from './Document';
import { Photo } from './Photo';
import { Video } from './Video';
import { mainMedia } from './util';
import './media.scss';

interface Props {
  msgs: Message[];
  onOpen: (msg: Message) => void;
}

/** Media group: photo/video mosaic, or a stacked list for document/audio albums. */
export function Album({ msgs, onOpen }: Props) {
  const visual = msgs.every((m) => m.kind === 'photo' || m.kind === 'video');
  if (!visual) {
    return (
      <div class="Album-list">
        {msgs.map((m) => (
          <div class="Album-list-item" key={m.id} data-message-id={m.id}>
            {m.kind === 'audio' ? <Audio msg={m} /> : <Document msg={m} />}
          </div>
        ))}
      </div>
    );
  }
  const layout = layoutAlbum(msgs.map((m) => ({ width: mainMedia(m)?.width ?? 0, height: mainMedia(m)?.height ?? 0 })));
  return (
    <div
      class="media-inner Album"
      style={{ width: `${layout.width}px`, aspectRatio: `${layout.width} / ${layout.height}` }}
    >
      {msgs.map((m, i) => {
        const t = layout.tiles[i];
        return (
          <div
            key={m.id}
            class="Album-tile"
            data-message-id={m.id}
            style={{
              left: `${(t.x / layout.width) * 100}%`,
              top: `${(t.y / layout.height) * 100}%`,
              width: `${(t.w / layout.width) * 100}%`,
              height: `${(t.h / layout.height) * 100}%`,
            }}
          >
            {m.kind === 'video' ? <Video msg={m} fill onOpen={() => onOpen(m)} /> : <Photo msg={m} fill onOpen={() => onOpen(m)} />}
          </div>
        );
      })}
    </div>
  );
}
```


`web/src/components/media/MessageMedia.tsx`：

```tsx
import type { Message } from '../../api/types';
import { Animation } from './Animation';
import { Audio } from './Audio';
import { Contact, Dice, Location, Poll } from './Cards';
import { Document } from './Document';
import { Photo } from './Photo';
import { Sticker } from './Sticker';
import { Video } from './Video';
import { VideoNote } from './VideoNote';
import { Voice } from './Voice';

/** The non-text part of a single message, by kind. Text/other kinds render nothing here. */
export function MessageMedia({ msg, onOpen }: { msg: Message; onOpen: (msg: Message) => void }) {
  const open = () => onOpen(msg);
  switch (msg.kind) {
    case 'photo':
      return <Photo msg={msg} onOpen={open} />;
    case 'video':
      return <Video msg={msg} onOpen={open} />;
    case 'animation':
      return <Animation msg={msg} onOpen={open} />;
    case 'voice':
      return <Voice msg={msg} />;
    case 'audio':
      return <Audio msg={msg} />;
    case 'document':
      return <Document msg={msg} />;
    case 'sticker':
      return <Sticker msg={msg} />;
    case 'video_note':
      return <VideoNote msg={msg} />;
    case 'location':
    case 'venue':
      return <Location msg={msg} />;
    case 'contact':
      return <Contact msg={msg} />;
    case 'poll':
      return <Poll msg={msg} />;
    case 'dice':
      return <Dice msg={msg} />;
    default:
      return null;
  }
}
```


`web/src/components/media/media.scss`：

```scss
.media-inner {
  position: relative;
  max-width: 100%;
  overflow: hidden;
  background: var(--color-background-secondary-accent);

  > img,
  > video {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
    cursor: pointer;
  }

  &.fill {
    width: 100%;
    height: 100%;
  }
}

.media-spoiler-blur {
  filter: blur(20px);
  transform: scale(1.1);
}

.media-spoiler {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  font-size: 0.875rem;
  font-weight: var(--font-weight-medium);
  background: rgba(0, 0, 0, 0.2);
}

.media-play {
  position: absolute;
  top: 50%;
  left: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 3.375rem;
  height: 3.375rem;
  margin: -1.6875rem 0 0 -1.6875rem;
  border-radius: 50%;
  color: #fff;
  background: rgba(0, 0, 0, 0.5);
  transition: background-color 0.15s;

  svg {
    margin-left: 3px;
  }

  &:hover {
    background: rgba(0, 0, 0, 0.6);
  }
}

.MediaBadge {
  position: absolute;
  top: 0.1875rem;
  left: 0.1875rem;
  padding: 0 0.375rem;
  border-radius: 0.75rem;
  font-size: 0.75rem;
  line-height: 1.125rem;
  color: #fff;
  background: rgba(0, 0, 0, 0.25);
  pointer-events: none;
  user-select: none;
}

.MediaStatus {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
  min-height: 8rem;
  background: var(--color-background-secondary-accent);
  color: var(--color-text-secondary);
  overflow: hidden;

  .MediaStatus-thumb {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
    filter: blur(8px);
    opacity: 0.6;
  }

  .MediaStatus-body {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.25rem;
    padding: 0.75rem;
    max-width: 100%;
    text-align: center;
  }

  .MediaStatus-text {
    font-size: 0.875rem;
    font-weight: var(--font-weight-medium);
  }

  .MediaStatus-error {
    font-size: 0.75rem;
    word-break: break-word;
  }

  &.state-failed {
    color: var(--color-error);
  }

  .MediaStatus-retry {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    margin-top: 0.25rem;
    padding: 0.25rem 0.75rem;
    border-radius: var(--border-radius-button-tiny);
    font-size: 0.875rem;
    font-weight: var(--font-weight-medium);
    color: #fff;
    background: var(--color-primary);
  }
}

.RetryLink {
  margin-left: 0.5rem;
  color: var(--color-primary);
  font-weight: var(--font-weight-medium);
}

.File {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  min-width: 13rem;
  padding: 0.25rem 0;

  .File-icon {
    position: relative;
    flex-shrink: 0;
    display: flex;
    align-items: flex-end;
    justify-content: center;
    width: 3.375rem;
    height: 3.375rem;
    padding-bottom: 0.3125rem;
    border-radius: var(--border-radius-messages-small);
    color: #fff;
    background: var(--file-color);
    clip-path: polygon(0 0, calc(100% - 1.125rem) 0, 100% 1.125rem, 100% 100%, 0 100%);
    text-decoration: none;
    overflow: hidden;

    &::after {
      content: "";
      position: absolute;
      top: 0;
      right: 0;
      width: 1.125rem;
      height: 1.125rem;
      background: linear-gradient(to left bottom, transparent 50%, rgba(0, 0, 0, 0.25) 50%);
    }

    &.with-thumb {
      clip-path: none;
      padding: 0;

      &::after {
        display: none;
      }

      img {
        position: absolute;
        inset: 0;
        width: 100%;
        height: 100%;
        object-fit: cover;
      }
    }
  }

  .File-ext {
    position: relative;
    font-size: 1rem;
    font-weight: var(--font-weight-medium);
    line-height: 1.5rem;
    text-transform: lowercase;
  }

  .File-overlay {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    pointer-events: none;
  }

  .File-download {
    opacity: 0;
    transition: opacity 0.15s;
  }

  a.File-icon:hover .File-download {
    opacity: 1;
  }

  a.File-icon:hover .File-ext {
    opacity: 0;
  }

  .not-ready .File-ext {
    opacity: 0;
  }

  .File-info {
    min-width: 0;
    flex: 1;
  }

  .File-title {
    font-weight: var(--font-weight-medium);
    line-height: 1.5rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .File-subtitle {
    font-size: 0.875rem;
    color: var(--color-text-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .File-error {
    color: var(--color-error);
  }
}

.toggle-play {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 3rem;
  height: 3rem;
  border-radius: 50%;
  color: #fff;
  background: var(--color-primary);
  transition: background-color 0.15s;

  &:hover {
    background: var(--color-primary-shade);
  }

  svg {
    margin-left: 0;
  }
}

.Voice,
.Audio {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  min-width: 13rem;
  padding: 0.25rem 0;
}

.Voice-body {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.Waveform {
  display: block;
  cursor: pointer;

  rect {
    fill: var(--color-primary);
    opacity: 0.5;

    &.played {
      opacity: 1;
    }
  }
}

.Voice-time,
.Audio-meta {
  font-size: 0.875rem;
  color: var(--color-text-secondary);
}

.Audio-info {
  flex: 1;
  min-width: 0;
}

.Audio-title {
  font-weight: var(--font-weight-medium);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.Audio-progress {
  position: relative;
  height: 0.25rem;
  margin: 0.375rem 0;
  border-radius: 0.125rem;
  background: var(--color-primary-tint);
  cursor: pointer;
}

.Audio-progress-fill {
  height: 100%;
  border-radius: inherit;
  background: var(--color-primary);
}

.Sticker {
  --sticker-size: 13rem;
  position: relative;
  width: calc(var(--sticker-size) * var(--sticker-w));
  height: calc(var(--sticker-size) * var(--sticker-h));

  img,
  video,
  .Sticker-lottie {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: contain;
  }

  .MediaStatus {
    min-height: 0;
    border-radius: var(--border-radius-messages);
  }

  @media (max-width: 600px) {
    --sticker-size: 11rem;
  }
}

.Sticker-fallback {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
  font-size: 5rem;
}

.VideoNote {
  position: relative;
  max-width: 100%;
  border-radius: 50%;
  overflow: hidden;
  background: var(--color-background-secondary-accent);

  video {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
    border-radius: 50%;
    cursor: pointer;
  }

  .MediaStatus {
    border-radius: 50%;
  }

  .MediaBadge {
    top: auto;
    bottom: 0.75rem;
    left: 50%;
    transform: translateX(-50%);
  }
}

.VideoNote-ring {
  position: absolute;
  inset: 0;
  transform: rotate(-90deg);
  pointer-events: none;

  circle {
    fill: none;
    stroke: #fff;
    stroke-opacity: 0.35;
    stroke-width: 4px;
    stroke-linecap: round;
  }
}

.Location {
  display: block;
  width: 18rem;
  max-width: 100%;
  color: inherit;
  text-decoration: none !important;

  .Location-map {
    display: flex;
    align-items: center;
    justify-content: center;
    aspect-ratio: 4 / 3;
    margin: 0 -0.5rem;
    background:
      linear-gradient(90deg, rgba(0, 0, 0, 0.04) 1px, transparent 1px) 0 0 / 1.5rem 1.5rem,
      linear-gradient(rgba(0, 0, 0, 0.04) 1px, transparent 1px) 0 0 / 1.5rem 1.5rem,
      var(--color-background-secondary-accent);
    animation: fade-in 0.3s;
  }

  .Location-pin {
    color: var(--color-error);
    filter: drop-shadow(0 0 2px var(--color-default-shadow));
  }

  .Location-info {
    padding: 0.3125rem 0 0.25rem;
  }

  .Location-title {
    font-weight: var(--font-weight-medium);
  }

  .Location-subtitle {
    font-size: 0.875rem;
    color: var(--color-text-secondary);
  }
}

.Contact {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  min-width: 13rem;
  padding: 0.5rem 1.5rem 0.5rem 0.125rem;

  .Contact-name {
    font-weight: var(--font-weight-medium);
  }

  .Contact-phone {
    font-size: 0.875rem;
    color: var(--color-text-secondary);
  }
}

.Poll {
  min-width: 15rem;

  .Poll-question {
    font-weight: var(--font-weight-semibold);
    word-break: break-word;
  }

  .Poll-type {
    font-size: 0.875rem;
    color: var(--color-text-secondary);
    margin-bottom: 0.5rem;
  }

  .Poll-options {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .Poll-option {
    display: flex;
    gap: 0.5rem;
  }

  .Poll-percent {
    flex-shrink: 0;
    width: 2.5rem;
    font-size: 0.875rem;
    font-weight: var(--font-weight-medium);
    text-align: end;
  }

  .Poll-option-body {
    flex: 1;
    min-width: 0;
  }

  .Poll-option-text {
    font-size: 0.9375rem;
    word-break: break-word;
  }

  .Poll-bar {
    height: 0.25rem;
    margin-top: 0.25rem;
    border-radius: 0.125rem;
    background: var(--color-primary);
  }

  .Poll-total {
    margin-top: 0.5rem;
    font-size: 0.875rem;
    color: var(--color-text-secondary);
    text-align: center;
  }
}

.Dice {
  display: flex;
  flex-direction: column;
  align-items: center;

  .Dice-emoji {
    font-size: 6rem;
    line-height: 1.2;
  }

  .Dice-value {
    padding: 0 0.5rem;
    border-radius: var(--border-radius-messages);
    font-size: 0.875rem;
    color: #fff;
    background: var(--action-message-bg);
  }
}

.Album {
  position: relative;
  background: none;

  .Album-tile {
    position: absolute;
    overflow: hidden;

    .media-inner {
      border-radius: 0;
    }
  }
}

.Album-list {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
```


- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test && npm run build`
Expected: PASS（`TgsSticker` 的 lottie 动态导入此时不会进入产物，因为 main 还没有引用它；Task 14 后会出现 `lottie_light-*.js` 独立分块）

- [ ] **Step 5: 提交**

```bash
git add web/src/components/media
git commit -m "feat(web): photo/video/GIF/album/file/audio/voice/sticker/round video and card renderers"
```

---

### Task 10: 消息气泡、消息流与中栏

**Files:**
- Create: `web/src/components/message/MessageParts.tsx`、`MessageBubble.tsx`、`MessageList.tsx`、`message.scss`
- Create: `web/src/components/middle/MiddleColumn.tsx`、`web/src/components/middle/middle.scss`
- Test: `web/src/components/message/message.test.tsx`

**Interfaces:**
- Consumes: Task 4 `formatTime`、`formatFullDate`、`hashString`、`peerColor`、`previewText`、`senderName`、`botName`、`navigate`；Task 5 store（`conv`、`refreshLatest`、`loadOlder`、`deleteMessage`、`viewer`、`sharedMediaOpen`、`showToast`）；Task 6 `ContextMenu`、`ConfirmDialog`、`Spinner`、`Avatar`、`IconButton`；Task 7 `RichText`、`safeHref`；Task 8 `groupMessages`、`Bubble`、`fitMedia`、`layoutAlbum`；Task 9 `Album`、`MessageMedia`、`mainMedia`、`extraString`、`VISUAL_KINDS`
- Produces:
  - `MessageMeta({ date, editDate, variant: 'inline' | 'overlay' | 'standalone' })`、`ForwardHeader({ origin })`（「转发自 <名称>」，频道帖子链到 `t.me/<username>/<id>`）、`OriginHeader({ msg })`（「来自 <群名> · 受保护」，链到 `origin_link`，附作者）、`ReplyQuote({ msg, senderName })`（缺失时「原消息未存档」；点击滚动并高亮）、`Appendix()`
  - `MessageBubble({ bubble, sender: { name, peerId }, onMenu(x, y, msg) })`：根 `.Message[data-message-id]`，内容 `.message-content`，类 `has-solid-background` / `media-only` / `has-visual` / `has-text|no-text` / `has-appendix`；贴纸、圆形视频、骰子无气泡（`.no-bubble`）；右键或长按 500ms 触发 `onMenu`
  - `MessageList({ chatId })`：`LOAD_OLDER_THRESHOLD = 400`；右键菜单「复制文本」「下载」「删除存档」；删除二次确认「删除存档」
  - `MiddleColumn({ chatId })`：`chatId = 0` 时显示「选择一个会话开始浏览存档」；否则顶栏（返回按钮仅 ≤925px 显示、头像、发送者名、`机器人 @username`、「共享媒体」按钮切换 `store.sharedMediaOpen`）+ 壁纸 + 消息流

- [ ] **Step 1: 写失败测试**

`web/src/components/message/message.test.tsx`：

```tsx
import { act, fireEvent, screen, waitFor } from '@testing-library/preact';
import { describe, expect, it, vi } from 'vitest';
import type { Message } from '../../api/types';
import type { Bubble } from '../../lib/grouping';
import { fakeApi, makeBot, makeChat, makeMedia, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { MiddleColumn } from '../middle/MiddleColumn';
import { MessageBubble } from './MessageBubble';

const sender = { name: 'Alice', peerId: 42 };
const single = (msg: Message, first = true, last = true): Bubble => ({ kind: 'message', key: String(msg.id), msg, first, last });

function bubble(b: Bubble, onMenu = vi.fn()) {
  return renderWithStore(<MessageBubble bubble={b} sender={sender} onMenu={onMenu} />);
}

describe('MessageBubble', () => {
  it('renders text with time, edited mark and the tail on the last bubble', () => {
    const date = new Date(2026, 9, 4, 9, 5).getTime() / 1000;
    const { container } = bubble(single(makeMessage({ text: 'hi there', date, edit_date: date + 60 })));
    expect(container.querySelector('.text-content')!.textContent).toContain('hi there');
    expect(screen.getByText('09:05')).toBeTruthy();
    expect(screen.getByText('已编辑')).toBeTruthy();
    expect(container.querySelector('.message-content')!.className).toContain('has-appendix');
    expect(container.querySelector('.svg-appendix')).toBeTruthy();
  });

  it('marks group position and omits the tail on non-last bubbles', () => {
    const { container } = bubble(single(makeMessage(), false, false));
    const root = container.querySelector('.Message')!;
    expect(root.classList.contains('first-in-group')).toBe(false);
    expect(root.classList.contains('last-in-group')).toBe(false);
    expect(container.querySelector('.svg-appendix')).toBeNull();
  });

  it('shows the forward header with a channel post link', () => {
    bubble(
      single(
        makeMessage({ forward_origin: { type: 'channel', name: 'News', username: 'news', message_id: 9, chat_id: -1001, date: 1 } }),
      ),
    );
    expect(screen.getByText('转发自')).toBeTruthy();
    expect(screen.getByText('News').closest('a')!.getAttribute('href')).toBe('https://t.me/news/9');
  });

  it('shows the protected-origin header for userbot fetches', () => {
    bubble(
      single(
        makeMessage({
          source: 'userbot_fetch',
          origin_chat_title: '内部群',
          origin_link: 'https://t.me/c/123/45',
          extra: { author: 'Bob' },
        }),
      ),
    );
    expect(screen.getByText('内部群').closest('a')!.getAttribute('href')).toBe('https://t.me/c/123/45');
    expect(screen.getByText('· 受保护')).toBeTruthy();
    expect(screen.getByText('· Bob')).toBeTruthy();
  });

  it('renders reply quotes, including replies whose original is not archived', () => {
    bubble(single(makeMessage({ id: 2, reply_to_tg_message_id: 1, reply: { id: 1, kind: 'photo', text: '' } })));
    expect(screen.getByText('Alice')).toBeTruthy();
    expect(screen.getByText('照片')).toBeTruthy();
    bubble(single(makeMessage({ id: 3, reply_to_tg_message_id: 7 })));
    expect(screen.getByText('原消息未存档')).toBeTruthy();
  });

  it('renders unsupported messages as an italic note', () => {
    bubble(single(makeMessage({ kind: 'other', text: '' })));
    expect(screen.getByText('不支持的消息类型')).toBeTruthy();
  });

  it('renders captionless photos edge to edge with the time over the image and opens the viewer', () => {
    const { container, store } = bubble(single(makeMessage({ id: 5, kind: 'photo', text: '', media: [makeMedia({ id: 50 })] })));
    const content = container.querySelector('.message-content')!;
    expect(content.classList.contains('media-only')).toBe(true);
    expect(content.classList.contains('has-solid-background')).toBe(false);
    expect(container.querySelector('.MessageMeta.overlay')).toBeTruthy();
    fireEvent.click(container.querySelector('img')!);
    expect(store.viewer.value).toEqual({ chatId: 10, messageId: 5, mediaId: 50 });
  });

  it('renders stickers without a bubble', () => {
    const { container } = bubble(single(makeMessage({ kind: 'sticker', text: '', media: [makeMedia({ kind: 'sticker', mime: 'image/webp' })] })));
    expect(container.querySelector('.Message')!.classList.contains('no-bubble')).toBe(true);
    expect(container.querySelector('.has-solid-background')).toBeNull();
  });

  it('renders an album with its caption and reports the right-clicked tile', () => {
    const msgs = [1, 2].map((id) =>
      makeMessage({ id, kind: 'photo', media_group_id: 'g', text: id === 1 ? 'trip' : '', media: [makeMedia({ id: 100 + id })] }),
    );
    const onMenu = vi.fn();
    const { container } = bubble({ kind: 'album', key: '1', msgs, first: true, last: true }, onMenu);
    expect(container.querySelectorAll('.Album-tile')).toHaveLength(2);
    expect(container.querySelector('.text-content')!.textContent).toContain('trip');
    fireEvent.contextMenu(container.querySelectorAll('.Album-tile img')[1]);
    expect(onMenu.mock.calls[0][2].id).toBe(2);
  });
});

describe('MiddleColumn', () => {
  const setup = (messages: Message[]) => {
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1, username: 'archive_bot' })]),
      chats: vi.fn(async () => [makeChat({ id: 10 })]),
      messages: vi.fn(async (_c: number, before = 0) => (before ? [] : messages)),
    });
    return api;
  };

  it('shows an empty hint without a chat', () => {
    renderWithStore(<MiddleColumn chatId={0} />);
    expect(screen.getByText('选择一个会话开始浏览存档')).toBeTruthy();
  });

  it('loads the newest page, shows the header and toggles shared media', async () => {
    const date = new Date(2026, 9, 4, 9, 0).getTime() / 1000;
    const api = setup([makeMessage({ id: 1, text: 'first', date }), makeMessage({ id: 2, text: 'second', date: date + 30 })]);
    const r = renderWithStore(<MiddleColumn chatId={10} />, api);
    await act(async () => {
      await r.store.loadBots();
      await r.store.loadChats();
    });
    expect(api.messages).toHaveBeenCalledWith(10, 0, 50);
    expect(await screen.findByText('second')).toBeTruthy();
    expect(screen.getAllByText('Alice').length).toBeGreaterThan(0);
    expect(screen.getByText('机器人 @archive_bot')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '共享媒体' }));
    expect(r.store.sharedMediaOpen.value).toBe(true);
  });

  it('loads older history when scrolled near the top', async () => {
    const page = Array.from({ length: 50 }, (_, i) => makeMessage({ id: 100 + i, text: `m${i}` }));
    const api = setup(page);
    const { container } = renderWithStore(<MiddleColumn chatId={10} />, api);
    await screen.findByText('m49');
    const list = container.querySelector('.MessageList')!;
    fireEvent.scroll(list);
    await waitFor(() => expect(api.messages).toHaveBeenLastCalledWith(10, 100, 50));
  });

  it('keeps loading older pages while the content does not fill the viewport', async () => {
    const pages: Record<number, Message[]> = {
      0: Array.from({ length: 50 }, (_, i) => makeMessage({ id: 200 + i })),
      200: Array.from({ length: 50 }, (_, i) => makeMessage({ id: 150 + i })),
      150: [],
    };
    const api = fakeApi({ messages: vi.fn(async (_c: number, before = 0) => pages[before] ?? []) });
    renderWithStore(<MiddleColumn chatId={10} />, api);
    await waitFor(() => expect(api.messages).toHaveBeenCalledTimes(3));
    expect(api.messages).toHaveBeenLastCalledWith(10, 150, 50);
  });

  it('deletes a message after confirmation from the context menu', async () => {
    const api = setup([makeMessage({ id: 1, text: 'bye' })]);
    const { container } = renderWithStore(<MiddleColumn chatId={10} />, api);
    await screen.findByText('bye');
    fireEvent.contextMenu(container.querySelector('.message-content')!);
    expect(screen.getByRole('menuitem', { name: '复制文本' })).toBeTruthy();
    fireEvent.click(screen.getByRole('menuitem', { name: '删除存档' }));
    await act(async () => {
      fireEvent.click(screen.getByText('删除'));
    });
    expect(api.deleteMessage).toHaveBeenCalledWith(1);
    await waitFor(() => expect(screen.queryByText('bye')).toBeNull());
  });

  it('offers download only for archived media', async () => {
    const api = setup([makeMessage({ id: 1, kind: 'document', text: '', media: [makeMedia({ id: 9, kind: 'document', file_name: 'a.txt' })] })]);
    const { container } = renderWithStore(<MiddleColumn chatId={10} />, api);
    await screen.findByText('a.txt');
    fireEvent.contextMenu(container.querySelector('.message-content')!);
    expect(screen.getByRole('menuitem', { name: '下载' })).toBeTruthy();
    expect(screen.queryByRole('menuitem', { name: '复制文本' })).toBeNull();
  });
});
```


- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/components/message/message.test.tsx`
Expected: FAIL（`Failed to resolve import "../middle/MiddleColumn"`、`"./MessageBubble"`）

- [ ] **Step 3: 实现**

`web/src/components/message/MessageParts.tsx`：

```tsx
import { Lock } from 'lucide-preact';
import type { ForwardOrigin, Message } from '../../api/types';
import { safeHref } from '../../lib/entities';
import { formatFullDate, formatTime, hashString, peerColor, previewText } from '../../lib/format';
import { extraString } from '../media/util';

export type MetaVariant = 'inline' | 'overlay' | 'standalone';

/** Time + 已编辑; inline floats at the end of text, overlay sits on media, standalone gets its own row. */
export function MessageMeta({ date, editDate, variant }: { date: number; editDate: number; variant: MetaVariant }) {
  const title = editDate > 0 ? `${formatFullDate(date)}\n已编辑：${formatFullDate(editDate)}` : formatFullDate(date);
  return (
    <span class={`MessageMeta ${variant}`} title={title}>
      {editDate > 0 && <span class="message-edited">已编辑</span>}
      <span class="message-time">{formatTime(date)}</span>
    </span>
  );
}

function originPeer(o: ForwardOrigin): number {
  return o.user_id || o.chat_id || hashString(o.name);
}

export function ForwardHeader({ origin }: { origin: ForwardOrigin }) {
  const name = origin.name || '隐藏用户';
  let href: string | null = null;
  if (origin.username) {
    href = safeHref(
      origin.type === 'channel' && origin.message_id
        ? `https://t.me/${origin.username}/${origin.message_id}`
        : `https://t.me/${origin.username}`,
    );
  }
  return (
    <div class="message-title forward-title" style={{ '--accent-color': peerColor(originPeer(origin)) }}>
      <span class="forward-label">转发自 </span>
      {href ? (
        <a class="forward-name" href={href} target="_blank" rel="noopener noreferrer">
          {name}
        </a>
      ) : (
        <span class="forward-name">{name}</span>
      )}
      {origin.signature && <span class="forward-signature">（{origin.signature}）</span>}
    </div>
  );
}

/** Header for userbot-fetched protected content: 来自 <群名> · 受保护, linking to the original post. */
export function OriginHeader({ msg }: { msg: Message }) {
  const title = msg.origin_chat_title || '受保护的频道';
  const href = safeHref(msg.origin_link);
  const author = extraString(msg, 'post_author') || extraString(msg, 'author');
  return (
    <div class="message-title origin-title" style={{ '--accent-color': peerColor(hashString(title)) }}>
      <Lock size={14} class="origin-lock" />
      <span>来自 </span>
      {href ? (
        <a class="origin-name" href={href} target="_blank" rel="noopener noreferrer">
          {title}
        </a>
      ) : (
        <span class="origin-name">{title}</span>
      )}
      <span> · 受保护</span>
      {author && <span class="origin-author"> · {author}</span>}
    </div>
  );
}

/** Reply quote; clicking scrolls to the replied message when it is loaded and flashes it. */
export function ReplyQuote({ msg, senderName }: { msg: Message; senderName: string }) {
  const r = msg.reply;
  const jump = (e: MouseEvent) => {
    e.stopPropagation();
    if (!r) return;
    const el = document.querySelector(`[data-message-id="${r.id}"]`)?.closest('.Message');
    if (!el) return;
    el.scrollIntoView({ block: 'center', behavior: 'smooth' });
    el.classList.remove('highlight');
    void (el as HTMLElement).offsetWidth;
    el.classList.add('highlight');
  };
  return (
    <button type="button" class={`EmbeddedMessage${r ? '' : ' missing'}`} onClick={jump}>
      <span class="embedded-title">{r ? senderName : '回复'}</span>
      <span class="embedded-text">{r ? previewText(r.kind, r.text) : '原消息未存档'}</span>
    </button>
  );
}

/** Bubble tail for the last incoming bubble of a group. */
export function Appendix() {
  return (
    <svg class="svg-appendix" width="9" height="18" viewBox="0 0 9 18" aria-hidden="true">
      <path class="corner" d="M9 0v18H3.2c-1.9 0-2.7-1.3-1.6-2.6C4.3 12.4 8.1 8.2 9 0z" />
    </svg>
  );
}
```

`web/src/components/message/MessageBubble.tsx`：

```tsx
import { useRef } from 'preact/hooks';
import type { Message } from '../../api/types';
import { fitMedia, layoutAlbum } from '../../lib/album';
import { peerColor } from '../../lib/format';
import type { Bubble } from '../../lib/grouping';
import { useStore } from '../../state/store';
import { Album } from '../media/Album';
import { MessageMedia } from '../media/MessageMedia';
import { VISUAL_KINDS, mainMedia } from '../media/util';
import { Appendix, ForwardHeader, MessageMeta, OriginHeader, ReplyQuote } from './MessageParts';
import { RichText } from './RichText';
import './message.scss';

export interface SenderInfo {
  name: string;
  peerId: number;
}

interface Props {
  bubble: Bubble;
  sender: SenderInfo;
  onMenu: (x: number, y: number, msg: Message) => void;
}

const NO_BUBBLE = ['sticker', 'video_note', 'dice'];
const LONG_PRESS_MS = 500;

/** Width the bubble takes when it shows a photo/video/album, so captions wrap to the media. */
function visualWidth(msgs: Message[], album: boolean): number {
  if (album) return layoutAlbum(msgs.map((m) => ({ width: mainMedia(m)?.width ?? 0, height: mainMedia(m)?.height ?? 0 }))).width;
  const main = mainMedia(msgs[0]);
  return fitMedia({ width: main?.width ?? 0, height: main?.height ?? 0 }).width;
}

export function MessageBubble({ bubble, sender, onMenu }: Props) {
  const store = useStore();
  const press = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const album = bubble.kind === 'album';
  const msgs = album ? bubble.msgs : [bubble.msg];
  const head = msgs[0];
  const last = msgs[msgs.length - 1];
  const caption = msgs.find((m) => m.text);
  const noBubble = !album && NO_BUBBLE.includes(head.kind);
  const visual = album ? msgs.every((m) => m.kind === 'photo' || m.kind === 'video') : VISUAL_KINDS.includes(head.kind);
  const hasHeader = Boolean(head.forward_origin) || head.source === 'userbot_fetch';
  const hasReply = head.reply_to_tg_message_id > 0;
  const mediaOnly = visual && !caption && !hasHeader && !hasReply;
  const unsupported = !album && head.kind === 'other';
  const solid = !noBubble && !mediaOnly;
  const editDate = Math.max(...msgs.map((m) => m.edit_date));
  const accent = peerColor(sender.peerId);

  const open = (m: Message) => {
    const md = mainMedia(m);
    if (md && md.state === 'done') store.viewer.value = { chatId: m.chat_id, messageId: m.id, mediaId: md.id };
  };

  const pick = (target: EventTarget | null): Message => {
    const el = (target as HTMLElement | null)?.closest('[data-message-id]');
    const id = Number(el?.getAttribute('data-message-id'));
    return msgs.find((m) => m.id === id) ?? head;
  };

  const classes = ['Message', bubble.first && 'first-in-group', bubble.last && 'last-in-group', noBubble && 'no-bubble']
    .filter(Boolean)
    .join(' ');
  const contentClasses = [
    'message-content',
    solid && 'has-solid-background',
    mediaOnly && 'media-only',
    visual && 'has-visual',
    caption || unsupported ? 'has-text' : 'no-text',
    solid && bubble.last && 'has-appendix',
  ]
    .filter(Boolean)
    .join(' ');

  let meta;
  if (caption || unsupported) meta = null;
  else if (visual || noBubble) meta = <MessageMeta date={last.date} editDate={editDate} variant="overlay" />;
  else meta = <MessageMeta date={last.date} editDate={editDate} variant="standalone" />;

  return (
    <div class={classes} data-message-id={head.id}>
      <div
        class={contentClasses}
        style={{
          '--accent-color': accent,
          '--accent-background-color': `color-mix(in srgb, ${accent} 10%, transparent)`,
          width: visual ? `${visualWidth(msgs, album)}px` : undefined,
        }}
        onContextMenu={(e) => {
          e.preventDefault();
          onMenu(e.clientX, e.clientY, pick(e.target));
        }}
        onTouchStart={(e) => {
          const t = e.touches[0];
          const target = e.target;
          press.current = setTimeout(() => onMenu(t.clientX, t.clientY, pick(target)), LONG_PRESS_MS);
        }}
        onTouchEnd={() => clearTimeout(press.current)}
        onTouchMove={() => clearTimeout(press.current)}
      >
        {head.source === 'userbot_fetch' && <OriginHeader msg={head} />}
        {head.forward_origin && <ForwardHeader origin={head.forward_origin} />}
        {hasReply && <ReplyQuote msg={head} senderName={sender.name} />}
        {album ? <Album msgs={msgs} onOpen={open} /> : <MessageMedia msg={head} onOpen={open} />}
        {(caption || unsupported) && (
          <div class="text-content" dir="auto">
            {caption ? <RichText text={caption.text} entities={caption.entities} /> : <span class="unsupported">不支持的消息类型</span>}
            <MessageMeta date={last.date} editDate={editDate} variant="inline" />
          </div>
        )}
        {meta}
        {solid && bubble.last && <Appendix />}
      </div>
    </div>
  );
}
```

`web/src/components/message/MessageList.tsx`：

```tsx
import { ArrowDown, Copy, Download, Trash2 } from 'lucide-preact';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'preact/hooks';
import { errorMessage, mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { senderName } from '../../lib/format';
import { groupMessages } from '../../lib/grouping';
import { useStore } from '../../state/store';
import { ContextMenu, type MenuItem } from '../../ui/ContextMenu';
import { ConfirmDialog } from '../../ui/Modal';
import { Spinner } from '../../ui/Spinner';
import { mainMedia } from '../media/util';
import { MessageBubble } from './MessageBubble';
import './message.scss';

/** Load older history when the user scrolls within this many px of the top. */
export const LOAD_OLDER_THRESHOLD = 400;
const AT_BOTTOM_PX = 100;
const SHOW_DOWN_PX = 300;

function download(href: string) {
  const a = document.createElement('a');
  a.href = href;
  a.download = '';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

export function MessageList({ chatId }: { chatId: number }) {
  const store = useStore();
  const conv = store.conv(chatId);
  const chat = store.chats.value.find((c) => c.id === chatId);
  const sender = chat ? { name: senderName(chat.sender), peerId: chat.sender.tg_user_id } : { name: '', peerId: chatId };
  const entries = useMemo(() => groupMessages(conv.items), [conv.items]);
  const ref = useRef<HTMLDivElement>(null);
  const snap = useRef({ firstId: 0, lastId: 0, height: 0, top: 0, atBottom: true });
  const [showDown, setShowDown] = useState(false);
  const [menu, setMenu] = useState<{ x: number; y: number; msg: Message } | null>(null);
  const [confirm, setConfirm] = useState<Message | null>(null);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    void store.refreshLatest(chatId);
  }, [chatId]);

  const record = () => {
    const el = ref.current;
    if (!el) return;
    const items = store.conv(chatId).items;
    snap.current = {
      firstId: items[0]?.id ?? 0,
      lastId: items[items.length - 1]?.id ?? 0,
      height: el.scrollHeight,
      top: el.scrollTop,
      atBottom: el.scrollHeight - el.scrollTop - el.clientHeight < AT_BOTTOM_PX,
    };
  };

  // Keep the viewport stable: bottom on first load, anchored when older pages are prepended,
  // following new messages only when the user is already at the bottom.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const first = conv.items[0]?.id ?? 0;
    const last = conv.items[conv.items.length - 1]?.id ?? 0;
    const s = snap.current;
    if (s.lastId === 0) el.scrollTop = el.scrollHeight;
    else if (first < s.firstId && last === s.lastId) el.scrollTop = el.scrollHeight - s.height + s.top;
    else if (last > s.lastId && s.atBottom) el.scrollTop = el.scrollHeight;
    record();
    // A first page shorter than the viewport never fires scroll events: keep filling.
    if (conv.loaded && conv.hasMore && el.scrollHeight - el.clientHeight < LOAD_OLDER_THRESHOLD) void store.loadOlder(chatId);
  }, [conv.items]);

  const onScroll = () => {
    const el = ref.current;
    if (!el) return;
    record();
    setShowDown(el.scrollHeight - el.scrollTop - el.clientHeight > SHOW_DOWN_PX);
    if (el.scrollTop < LOAD_OLDER_THRESHOLD) void store.loadOlder(chatId);
  };

  const menuItems = (msg: Message): MenuItem[] => {
    const items: MenuItem[] = [];
    const caption = msg.text || (msg.media_group_id ? conv.items.find((m) => m.media_group_id === msg.media_group_id && m.text)?.text : '');
    if (caption) {
      items.push({
        label: '复制文本',
        icon: <Copy size={20} />,
        onSelect: () => {
          if (!navigator.clipboard) {
            store.showToast('复制失败');
            return;
          }
          navigator.clipboard.writeText(caption).then(
            () => store.showToast('已复制'),
            () => store.showToast('复制失败'),
          );
        },
      });
    }
    const md = mainMedia(msg);
    if (md && md.state === 'done') {
      items.push({ label: '下载', icon: <Download size={20} />, onSelect: () => download(mediaUrl(md.id, true)) });
    }
    items.push({ label: '删除存档', icon: <Trash2 size={20} />, danger: true, onSelect: () => setConfirm(msg) });
    return items;
  };

  const doDelete = async () => {
    if (!confirm) return;
    setDeleting(true);
    try {
      await store.deleteMessage(confirm);
      setConfirm(null);
    } catch (e) {
      store.showToast(`删除失败：${errorMessage(e)}`);
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div class="MessageList-wrapper">
      <div class="MessageList custom-scroll" ref={ref} onScroll={onScroll}>
        <div class="messages-container">
          {conv.loading && conv.items.length > 0 && (
            <div class="history-loading">
              <Spinner size={28} />
            </div>
          )}
          {conv.error && (
            <div class="history-notice">
              <span>{conv.error}</span>
              <button type="button" onClick={() => void (conv.loaded ? store.loadOlder(chatId) : store.refreshLatest(chatId))}>
                重试
              </button>
            </div>
          )}
          {!conv.loaded && conv.loading && (
            <div class="history-notice">
              <Spinner size={32} />
            </div>
          )}
          {conv.loaded && conv.items.length === 0 && (
            <div class="history-notice">
              <span>暂无消息</span>
            </div>
          )}
          {entries.map((e) =>
            e.kind === 'date' ? (
              <div class="sticky-date" key={e.key}>
                <span>{e.label}</span>
              </div>
            ) : (
              <div class="message-group" key={e.key}>
                {e.bubbles.map((b) => (
                  <MessageBubble key={b.key} bubble={b} sender={sender} onMenu={(x, y, msg) => setMenu({ x, y, msg })} />
                ))}
              </div>
            ),
          )}
        </div>
      </div>
      {showDown && (
        <button
          type="button"
          class="ScrollDown"
          aria-label="回到底部"
          onClick={() => ref.current?.scrollTo({ top: ref.current.scrollHeight, behavior: 'smooth' })}
        >
          <ArrowDown size={24} />
        </button>
      )}
      {menu && <ContextMenu x={menu.x} y={menu.y} items={menuItems(menu.msg)} onClose={() => setMenu(null)} />}
      {confirm && (
        <ConfirmDialog
          title="删除存档"
          text="将从存档中删除这条消息；没有其他消息引用的媒体文件会一并删除。此操作无法撤销。"
          confirmLabel="删除"
          danger
          busy={deleting}
          onConfirm={() => void doDelete()}
          onClose={() => setConfirm(null)}
        />
      )}
    </div>
  );
}
```

`web/src/components/message/message.scss`：

```scss
@mixin meta-colors {
  --color-message-meta: rgba(104, 108, 114, 0.75);
}

.MessageList-wrapper {
  position: relative;
  flex: 1;
  min-height: 0;
}

.MessageList {
  position: absolute;
  inset: 0;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior: contain;
}

.messages-container {
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  min-height: 100%;
  width: 100%;
  max-width: var(--messages-container-width);
  margin: 0 auto;
  padding: calc(var(--middle-header-height) + 1.5rem) var(--middle-panel-inline-padding) 1rem;
}

.history-loading,
.history-notice {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  padding: 0.75rem 0;
  color: #fff;

  span {
    padding: 0.1875rem 0.5rem;
    border-radius: var(--border-radius-messages);
    font-size: calc(var(--message-text-size) - 1px);
    font-weight: var(--font-weight-medium);
    background: var(--action-message-bg);
  }

  button {
    padding: 0.1875rem 0.75rem;
    border-radius: var(--border-radius-messages);
    font-size: 0.875rem;
    font-weight: var(--font-weight-medium);
    color: #fff;
    background: var(--color-primary);
  }
}

.history-notice {
  flex: 1;
}

.sticky-date {
  position: sticky;
  top: calc(var(--middle-header-height) + 1rem);
  z-index: 2;
  display: flex;
  justify-content: center;
  margin: 1rem 0;
  pointer-events: none;

  span {
    display: inline-block;
    padding: 0.1875rem 0.5rem;
    border-radius: var(--border-radius-messages);
    font-size: calc(var(--message-text-size) - 1px);
    font-weight: var(--font-weight-medium);
    line-height: 1.25;
    color: #fff;
    background: var(--action-message-bg);
    user-select: none;
  }
}

.Message {
  @include meta-colors;
  position: relative;
  display: flex;
  align-items: flex-end;
  margin-bottom: 0.375rem;

  &.last-in-group {
    margin-bottom: 0.625rem;
  }

  &.highlight::before {
    content: "";
    position: absolute;
    inset: -0.1875rem -100vmax;
    background: rgba(51, 144, 236, 0.15);
    pointer-events: none;
    animation: message-highlight 1.5s ease-out forwards;
  }
}

@media (prefers-color-scheme: dark) {
  .Message {
    --color-message-meta: rgba(170, 170, 170, 0.75);
  }
}

@keyframes message-highlight {
  0%,
  60% {
    opacity: 1;
  }

  100% {
    opacity: 0;
  }
}

.message-content {
  --max-width: 29rem;
  position: relative;
  max-width: var(--max-width);
  min-width: 0;
  border-radius: var(--border-radius-messages);
  font-size: var(--message-text-size);
  line-height: 1.3125;
  word-break: break-word;
  white-space: pre-wrap;

  @media (max-width: 600px) {
    --max-width: min(29rem, calc(100vw - 6.25rem));
  }

  &.has-solid-background {
    padding: 0.3125rem 0.5rem 0.375rem;
    background: var(--color-background);
    box-shadow: var(--shadow-bubble);
  }

  &.media-only {
    overflow: hidden;
    box-shadow: var(--shadow-bubble);
  }

  &.has-visual {
    > .media-inner {
      width: auto !important;
      max-width: none;
    }
  }

  &.has-solid-background.has-visual > .media-inner {
    margin: 0 -0.5rem 0.3125rem;

    &:first-child {
      margin-top: -0.3125rem;
      border-top-left-radius: inherit;
      border-top-right-radius: inherit;
    }
  }

  &.has-solid-background.has-visual.no-text > .media-inner {
    margin-bottom: -0.375rem;
    border-bottom-left-radius: inherit;
    border-bottom-right-radius: inherit;
  }

  &.has-appendix {
    border-bottom-left-radius: 0;
  }
}

.Message:not(.last-in-group) .message-content {
  border-bottom-left-radius: var(--border-radius-messages-small);
}

.Message:not(.first-in-group) .message-content {
  border-top-left-radius: var(--border-radius-messages-small);
}

.svg-appendix {
  position: absolute;
  bottom: -0.0625rem;
  left: -0.5625rem;
  width: 0.5625rem;
  height: 1.125rem;
  filter: drop-shadow(-1px 1px 1px var(--color-default-shadow));

  .corner {
    fill: var(--color-background);
  }
}

.text-content {
  white-space: pre-wrap;
  overflow-wrap: anywhere;

  .unsupported {
    font-style: italic;
    color: var(--color-text-meta);
  }
}

.MessageMeta {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  height: 1.25rem;
  font-size: 0.75rem;
  line-height: 1;
  white-space: nowrap;
  user-select: none;
  cursor: default;

  &.inline {
    float: right;
    position: relative;
    top: 0.375rem;
    bottom: auto;
    margin-left: 0.5rem;
    margin-right: -0.25rem;
    color: var(--color-message-meta);
  }

  &.standalone {
    display: flex;
    justify-content: flex-end;
    margin: -0.125rem -0.25rem -0.125rem 0;
    color: var(--color-message-meta);
  }

  &.overlay {
    position: absolute;
    right: 0.25rem;
    bottom: 0.25rem;
    padding: 0 0.375rem;
    border-radius: 0.625rem;
    color: #fff;
    background: rgba(0, 0, 0, 0.2);
  }
}

.no-bubble .MessageMeta.overlay {
  background: var(--action-message-bg);
}

.message-title {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  font-size: calc(var(--message-text-size) - 2px);
  font-weight: var(--font-weight-medium);
  line-height: 1.25;
  color: var(--accent-color);
  margin-bottom: 0.125rem;

  a {
    color: inherit;
  }

  .forward-label,
  .forward-signature {
    font-weight: var(--font-weight-normal);
    white-space: pre;
  }

  .origin-lock {
    margin-right: 0.25rem;
  }

  .origin-author {
    font-weight: var(--font-weight-normal);
    color: var(--color-text-secondary);
  }
}

.has-visual > .message-title,
.has-visual > .EmbeddedMessage {
  margin-bottom: 0.375rem;
}

.EmbeddedMessage {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 100%;
  margin: 0.125rem 0 0.25rem;
  padding: 0.1875rem 0.375rem 0.1875rem 0.5625rem;
  border-radius: var(--border-radius-messages-small);
  font-size: calc(var(--message-text-size) - 2px);
  line-height: 1.125rem;
  text-align: start;
  background: var(--accent-background-color);
  overflow: hidden;

  &::before {
    content: "";
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    width: 0.1875rem;
    background: var(--accent-color);
  }

  .embedded-title {
    font-weight: var(--font-weight-medium);
    color: var(--accent-color);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .embedded-text {
    color: var(--color-text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &.missing {
    cursor: default;

    .embedded-text {
      color: var(--color-text-secondary);
    }
  }
}

.ScrollDown {
  position: absolute;
  right: max(1rem, calc((100% - var(--messages-container-width)) / 2 - 3.5rem));
  bottom: 1rem;
  z-index: 3;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 3.5rem;
  height: 3.5rem;
  border-radius: 50%;
  color: var(--color-text-secondary);
  background: var(--color-background);
  box-shadow: 0 1px 2px var(--color-default-shadow);
  animation: fade-in 0.2s ease-out;
}
```

`web/src/components/middle/MiddleColumn.tsx`：

```tsx
import { ArrowLeft, Images } from 'lucide-preact';
import { avatarUrl } from '../../api/client';
import { botName, senderName } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { IconButton } from '../../ui/Button';
import { MessageList } from '../message/MessageList';
import './middle.scss';

function MiddleHeader({ chatId }: { chatId: number }) {
  const store = useStore();
  const chat = store.chats.value.find((c) => c.id === chatId);
  const bot = chat ? store.botsById.value.get(chat.bot_id) : undefined;
  const name = chat ? senderName(chat.sender) : '会话';
  const toggleShared = () => {
    store.sharedMediaOpen.value = !store.sharedMediaOpen.value;
  };
  return (
    <div class="MiddleHeader">
      <IconButton label="返回" class="back-button" onClick={() => navigate({ name: 'home' })}>
        <ArrowLeft size={24} />
      </IconButton>
      <button type="button" class="MiddleHeader-info" onClick={toggleShared}>
        {chat && (
          <Avatar
            name={name}
            peerId={chat.sender.tg_user_id}
            src={chat.sender.has_avatar ? avatarUrl('senders', chat.sender.tg_user_id) : null}
            size="medium"
          />
        )}
        <span class="MiddleHeader-text">
          <span class="MiddleHeader-title">{name}</span>
          {bot && <span class="MiddleHeader-status">机器人 {bot.username ? `@${bot.username}` : botName(bot)}</span>}
        </span>
      </button>
      <IconButton label="共享媒体" onClick={toggleShared}>
        <Images size={24} />
      </IconButton>
    </div>
  );
}

export function MiddleColumn({ chatId }: { chatId: number }) {
  if (!chatId) {
    return (
      <div id="MiddleColumn" class="empty">
        <div class="Wallpaper" aria-hidden="true" />
        <div class="empty-hint">
          <span>选择一个会话开始浏览存档</span>
        </div>
      </div>
    );
  }
  return (
    <div id="MiddleColumn">
      <div class="Wallpaper" aria-hidden="true" />
      <MiddleHeader chatId={chatId} />
      <MessageList key={chatId} chatId={chatId} />
    </div>
  );
}
```

`web/src/components/middle/middle.scss`：

```scss
#MiddleColumn {
  position: relative;
  isolation: isolate;
  flex: 1;
  min-width: 0;
  height: 100%;
  display: flex;
  flex-direction: column;

  &.empty {
    align-items: center;
    justify-content: center;
  }
}

.empty-hint span {
  display: inline-block;
  padding: 0.1875rem 0.75rem;
  border-radius: var(--border-radius-messages);
  font-size: calc(var(--message-text-size) - 1px);
  font-weight: var(--font-weight-medium);
  color: #fff;
  background: var(--action-message-bg);
}

.MiddleHeader {
  position: absolute;
  top: var(--middle-panel-inline-padding);
  left: var(--middle-panel-inline-padding);
  right: var(--middle-panel-inline-padding);
  z-index: 5;
  display: flex;
  align-items: center;
  gap: 0.25rem;
  height: var(--middle-header-height);
  max-width: calc(var(--messages-container-width) - 2rem);
  margin: 0 auto;
  padding: 0 0.25rem 0 0.125rem;
  border-radius: 2rem;
  background: var(--color-background);
  box-shadow: var(--shadow-pane);

  .back-button {
    display: none;
  }

  @media (max-width: 925px) {
    .back-button {
      display: flex;
    }
  }
}

.MiddleHeader-info {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 0.625rem;
  height: 100%;
  padding: 0.125rem;
  text-align: start;
  border-radius: 2rem;

  .Avatar {
    width: 2.75rem !important;
    height: 2.75rem !important;
  }
}

.MiddleHeader-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.MiddleHeader-title {
  font-weight: var(--font-weight-semibold);
  line-height: 1.375rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.MiddleHeader-status {
  font-size: 0.875rem;
  line-height: 1.125rem;
  color: var(--color-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

// Default wallpaper: four-colour corner gradient with a doodle pattern (original artwork in
// public/pattern.svg). Light draws the doodles darker on the gradient; dark cuts the gradient
// out of black through the doodle mask.
.Wallpaper {
  --wallpaper-gradient: radial-gradient(circle at 0% 0%, var(--wallpaper-1) 0%, transparent 70%),
    radial-gradient(circle at 100% 0%, var(--wallpaper-2) 0%, transparent 70%),
    radial-gradient(circle at 100% 100%, var(--wallpaper-3) 0%, transparent 70%),
    radial-gradient(circle at 0% 100%, var(--wallpaper-4) 0%, transparent 70%),
    linear-gradient(135deg, var(--wallpaper-1), var(--wallpaper-3));
  position: absolute;
  inset: 0;
  z-index: -1;
  background: var(--wallpaper-gradient);

  &::after {
    content: "";
    position: absolute;
    inset: 0;
    background: #000;
    opacity: 0.12;
    -webkit-mask: url("/pattern.svg") 0 0 / 360px 360px repeat;
    mask: url("/pattern.svg") 0 0 / 360px 360px repeat;
  }
}

@media (prefers-color-scheme: dark) {
  .Wallpaper {
    background: #000;

    &::after {
      background: var(--wallpaper-gradient);
      opacity: 0.45;
    }
  }
}
```


- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/components/message web/src/components/middle
git commit -m "feat(web): message bubbles, infinite history, context menu delete and chat header"
```

---

### Task 11: 左栏会话列表

**Files:**
- Create: `web/src/components/left/ChatsPanel.tsx`、`web/src/components/left/left.scss`
- Test: `web/src/components/left/left.test.tsx`

**Interfaces:**
- Consumes: Task 3 `avatarUrl`；Task 4 `botName`、`formatListTime`、`previewText`、`senderName`、`navigate`、`route`；Task 5 store（`bots`、`chats`、`chatsLoaded`、`botFilter`、`botsById`、`visibleChats`）；Task 6 `Avatar`、`Spinner`、`Tabs`
- Produces: `ChatsPanel()`：标题栏「tgarchive」；≥2 个机器人时显示机器人切换标签（「全部」+ 每个机器人的头像与名称，已删除且无存档的机器人不显示）；会话行（54px 头像、发送者名、时间、末条摘要；「全部」视图且有多个机器人时摘要前缀 `<机器人名>: `）；选中行 `.selected`；空态「暂无存档」；右下角圆形「管理」按钮进入 `/settings`

- [ ] **Step 1: 写失败测试**

`web/src/components/left/left.test.tsx`：

```tsx
import { act, fireEvent, screen } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { route } from '../../lib/router';
import { fakeApi, makeBot, makeChat } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { ChatsPanel } from './ChatsPanel';

afterEach(() => {
  route.value = { name: 'home' };
  history.replaceState(null, '', '/');
});

async function setup(bots = [makeBot({ id: 1, name: 'Alpha' }), makeBot({ id: 2, name: 'Beta', tg_bot_id: 888 })]) {
  const api = fakeApi({
    bots: vi.fn(async () => bots),
    chats: vi.fn(async () => [
      makeChat({ id: 10, bot_id: 1, last_text: 'hello' }),
      makeChat({ id: 11, bot_id: 2, last_kind: 'photo', last_text: '', sender: { tg_user_id: 7, first_name: 'Bob', last_name: 'Lee', username: '', has_avatar: true } }),
    ]),
  });
  const r = renderWithStore(<ChatsPanel />, api);
  await act(async () => {
    await r.store.loadBots();
    await r.store.loadChats();
  });
  return r;
}

describe('ChatsPanel', () => {
  it('lists chats with previews and bot prefixes in the 全部 view', async () => {
    const { container } = await setup();
    const items = container.querySelectorAll('.ChatItem');
    expect(items).toHaveLength(2);
    expect(items[0].textContent).toContain('Alice');
    expect(items[0].textContent).toContain('Alpha: hello');
    expect(items[1].textContent).toContain('Bob Lee');
    expect(items[1].textContent).toContain('Beta: 照片');
    expect(items[1].querySelector('img')!.getAttribute('src')).toBe('/avatars/senders/7');
  });

  it('filters by bot with the tab bar', async () => {
    const { container } = await setup();
    fireEvent.click(screen.getByRole('tab', { name: 'Beta' }));
    const items = container.querySelectorAll('.ChatItem');
    expect(items).toHaveLength(1);
    expect(items[0].textContent).not.toContain('Beta:');
    fireEvent.click(screen.getByRole('tab', { name: '全部' }));
    expect(container.querySelectorAll('.ChatItem')).toHaveLength(2);
  });

  it('hides the tab bar and prefixes with a single bot', async () => {
    const { container } = await setup([makeBot({ id: 1, name: 'Alpha' })]);
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(container.textContent).not.toContain('Alpha:');
  });

  it('opens a chat and marks it selected', async () => {
    const { container } = await setup();
    fireEvent.click(container.querySelectorAll('.ChatItem')[1]);
    expect(route.value).toEqual({ name: 'chat', chatId: 11 });
    expect(location.pathname).toBe('/chat/11');
    expect(container.querySelectorAll('.ChatItem')[1].classList.contains('selected')).toBe(true);
  });

  it('shows the empty state and opens settings from the gear', async () => {
    const r = renderWithStore(<ChatsPanel />);
    await act(async () => {
      await r.store.loadChats();
    });
    expect(screen.getByText('暂无存档')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '管理' }));
    expect(route.value).toEqual({ name: 'settings' });
  });
});
```


- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/components/left`
Expected: FAIL（`Failed to resolve import "./ChatsPanel"`）

- [ ] **Step 3: 实现**

`web/src/components/left/ChatsPanel.tsx`：

```tsx
import { Settings } from 'lucide-preact';
import { avatarUrl } from '../../api/client';
import type { Chat } from '../../api/types';
import { botName, formatListTime, previewText, senderName } from '../../lib/format';
import { navigate, route } from '../../lib/router';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { Spinner } from '../../ui/Spinner';
import { Tabs } from '../../ui/Tabs';
import './left.scss';

function BotTabs() {
  const store = useStore();
  const bots = store.bots.value.filter((b) => b.status !== 'removed' || store.chats.value.some((c) => c.bot_id === b.id));
  if (bots.length < 2) return null;
  const items = [
    { key: 0, label: '全部' },
    ...bots.map((b) => ({
      key: b.id,
      label: (
        <>
          <Avatar name={botName(b)} peerId={b.tg_bot_id} src={b.has_avatar ? avatarUrl('bots', b.tg_bot_id) : null} size="mini" />
          {botName(b)}
        </>
      ),
    })),
  ];
  return (
    <Tabs
      class="BotTabs"
      items={items}
      active={store.botFilter.value}
      onChange={(k) => {
        store.botFilter.value = k;
      }}
    />
  );
}

function ChatItem({ chat, selected, showBot }: { chat: Chat; selected: boolean; showBot: boolean }) {
  const store = useStore();
  const bot = store.botsById.value.get(chat.bot_id);
  const name = senderName(chat.sender);
  return (
    <button
      type="button"
      class={`ChatItem${selected ? ' selected' : ''}`}
      aria-current={selected ? 'page' : undefined}
      onClick={() => navigate({ name: 'chat', chatId: chat.id })}
    >
      <Avatar
        name={name}
        peerId={chat.sender.tg_user_id}
        src={chat.sender.has_avatar ? avatarUrl('senders', chat.sender.tg_user_id) : null}
        size="large"
      />
      <span class="ChatItem-info">
        <span class="ChatItem-row">
          <span class="ChatItem-title">{name}</span>
          <span class="ChatItem-time">{formatListTime(chat.last_message_at)}</span>
        </span>
        <span class="ChatItem-subtitle">
          {showBot && bot && <span class="sender-name">{botName(bot)}: </span>}
          {previewText(chat.last_kind, chat.last_text)}
        </span>
      </span>
    </button>
  );
}

/** Left column default view: bot switcher, chat list (bot × sender), settings button. */
export function ChatsPanel() {
  const store = useStore();
  const r = route.value;
  const selectedId = r.name === 'chat' ? r.chatId : 0;
  const chats = store.visibleChats.value;
  const showBot = store.botFilter.value === 0 && store.bots.value.length > 1;
  return (
    <div class="ChatsPanel">
      <div class="left-header">
        <h3 class="left-header-title">tgarchive</h3>
      </div>
      <BotTabs />
      <div class="chat-list custom-scroll">
        {!store.chatsLoaded.value && (
          <div class="chat-list-empty">
            <Spinner size={32} />
          </div>
        )}
        {store.chatsLoaded.value && chats.length === 0 && (
          <div class="chat-list-empty">
            <p class="chat-list-empty-title">暂无存档</p>
            <p>白名单用户发给机器人的消息会出现在这里</p>
          </div>
        )}
        {chats.map((c) => (
          <ChatItem key={c.id} chat={c} selected={c.id === selectedId} showBot={showBot} />
        ))}
      </div>
      <button type="button" class="FloatingActionButton" aria-label="管理" title="管理" onClick={() => navigate({ name: 'settings' })}>
        <Settings size={24} />
      </button>
    </div>
  );
}
```

`web/src/components/left/left.scss`：

```scss
.ChatsPanel {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100%;
}

.left-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  height: var(--column-header-height);
  padding: 0.5rem 0.8125rem;
}

.left-header-title {
  margin-left: 1.375rem;
  font-size: 1.25rem;
  font-weight: var(--font-weight-medium);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.BotTabs {
  flex-shrink: 0;
  padding: 0 0.5rem;
  box-shadow: inset 0 -1px 0 var(--color-borders);

  .Avatar {
    font-size: 0.625rem !important;
  }
}

.chat-list {
  flex: 1;
  min-height: 0;
  padding: 0.5rem;
}

.chat-list-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.25rem;
  padding: 2rem 1rem;
  text-align: center;
  font-size: 0.875rem;
  color: var(--color-text-secondary);

  .chat-list-empty-title {
    font-size: 1rem;
    font-weight: var(--font-weight-medium);
    color: var(--color-text);
  }
}

.ChatItem {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  width: 100%;
  min-height: 3rem;
  padding: 0.5625rem 0.5625rem;
  border-radius: var(--border-radius-default);
  text-align: start;
  transition: background-color 0.15s;

  &:hover {
    background: var(--color-chat-hover);
  }

  &.selected {
    @media (min-width: 601px) {
      --color-text: #fff;
      --color-text-secondary: #fff;
      --color-chat-username: #fff;
      color: #fff;
      background: var(--color-chat-active);
    }

    @media (max-width: 600px) {
      background: var(--color-chat-hover);
    }
  }
}

.ChatItem-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.ChatItem-row {
  display: flex;
  align-items: baseline;
  gap: 0.5rem;
}

.ChatItem-title {
  flex: 1;
  min-width: 0;
  font-size: 1rem;
  font-weight: var(--font-weight-semibold);
  line-height: 1.5rem;
  color: var(--color-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ChatItem-time {
  flex-shrink: 0;
  font-size: 0.75rem;
  color: var(--color-text-secondary);
}

.ChatItem-subtitle {
  font-size: 0.875rem;
  line-height: 1.375rem;
  color: var(--color-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;

  .sender-name {
    color: var(--color-chat-username);
  }
}

html.is-apple .ChatItem-subtitle .sender-name {
  color: var(--color-text);
}

.FloatingActionButton {
  position: absolute;
  right: 1.25rem;
  bottom: 1.25rem;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 3.5rem;
  height: 3.5rem;
  border-radius: 50%;
  color: #fff;
  background: var(--color-primary);
  box-shadow: 0 1px 8px 1px rgba(0, 0, 0, 0.12);
  transition: background-color 0.15s, transform 0.2s;

  &:hover {
    background: var(--color-primary-shade);
  }
}
```


- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/components/left
git commit -m "feat(web): chat list grouped by bot × sender with bot switcher"
```

---

### Task 12: 共享媒体抽屉与媒体查看器

**Files:**
- Create: `web/src/components/right/SharedMedia.tsx`、`web/src/components/right/right.scss`
- Create: `web/src/components/viewer/MediaViewer.tsx`、`web/src/components/viewer/viewer.scss`
- Test: `web/src/components/right/shared.test.tsx`

**Interfaces:**
- Consumes: Task 3 `mediaUrl`、`errorMessage`；Task 4 `formatDuration`、`formatMonth`、`monthKey`、`formatFullDate`、`hashString`、`peerColor`、`previewText`、`senderName`；Task 5 store（`api.chatMedia`、`viewer`、`sharedMediaOpen`、`conv`、`chats`）；Task 6 `IconButton`、`Spinner`、`Tabs`；Task 7 `extractLinks`、`RichText`；Task 9 `Document`、`mainMedia`、`readyThumb`、`VISUAL_KINDS`
- Produces:
  - `SHARED_PAGE = 100`；`SharedMedia({ chatId })`：标题「共享媒体」+「关闭」；标签「媒体」「文件」「链接」；按月分组（`2026年10月`）；媒体为 3 列 1px 间隔方格，点击打开查看器；文件用 `Document`；链接行显示站点首字母色块、域名、URL、消息摘要；滚动到底加载下一页；空态「暂无媒体」「暂无文件」「暂无链接」
  - `VIEWER_PAGE = 100`、`VIEWER_MAX_PAGES = 50`、`interface ViewerItem { msg; media }`、`toViewerItems(msgs)`（只取已落盘的 photo / video / animation，按 id 升序）、`MediaViewer()`：读取 `store.viewer`；头部发送者名、日期与「i / n」、「缩小」「放大」（1–4 倍，步长 0.5，滚轮与双击）、「下载」链接、「关闭」；「上一个」「下一个」与方向键；Esc 关闭；有文字时底部显示说明

- [ ] **Step 1: 写失败测试**

`web/src/components/right/shared.test.tsx`：

```tsx
import { act, fireEvent, screen, waitFor } from '@testing-library/preact';
import { describe, expect, it, vi } from 'vitest';
import type { Message } from '../../api/types';
import { fakeApi, makeMedia, makeMessage } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { MediaViewer } from '../viewer/MediaViewer';
import { SharedMedia } from './SharedMedia';

const oct = new Date(2026, 9, 3).getTime() / 1000;
const sep = new Date(2026, 8, 3).getTime() / 1000;

describe('SharedMedia', () => {
  it('groups media by month and opens the viewer from a tile', async () => {
    const api = fakeApi({
      chatMedia: vi.fn(async () => [
        makeMessage({ id: 3, kind: 'photo', date: oct, media: [makeMedia({ id: 30 })] }),
        makeMessage({ id: 2, kind: 'video', date: sep, media: [makeMedia({ id: 20, kind: 'video', duration: 9 }), makeMedia({ id: 21, role: 'thumb' })] }),
      ]),
    });
    const { container, store } = renderWithStore(<SharedMedia chatId={10} />, api);
    expect(await screen.findByText('2026年10月')).toBeTruthy();
    expect(screen.getByText('2026年9月')).toBeTruthy();
    expect(api.chatMedia).toHaveBeenCalledWith(10, 'media', 0, 100);
    const imgs = [...container.querySelectorAll('.SharedMedia-tile img')].map((i) => i.getAttribute('src'));
    expect(imgs).toEqual(['/media/30', '/media/21']);
    expect(screen.getByText('0:09')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '查看照片' }));
    expect(store.viewer.value).toEqual({ chatId: 10, messageId: 3, mediaId: 30 });
  });

  it('switches tabs and lists files and links', async () => {
    const api = fakeApi({
      chatMedia: vi.fn(async (_c: number, type: string) =>
        type === 'file'
          ? [makeMessage({ id: 5, kind: 'document', media: [makeMedia({ id: 50, kind: 'document', file_name: 'a.zip' })] })]
          : type === 'link'
            ? [makeMessage({ id: 6, text: 'see go.dev', entities: [{ type: 'url', offset: 4, length: 6 }] })]
            : [],
      ),
    });
    renderWithStore(<SharedMedia chatId={10} />, api);
    expect(await screen.findByText('暂无媒体')).toBeTruthy();
    fireEvent.click(screen.getByRole('tab', { name: '文件' }));
    expect(await screen.findByText('a.zip')).toBeTruthy();
    fireEvent.click(screen.getByRole('tab', { name: '链接' }));
    expect(await screen.findByText('go.dev')).toBeTruthy();
    expect(screen.getByText('https://go.dev/').closest('a')!.getAttribute('href')).toBe('https://go.dev/');
  });

  it('pages further when scrolled to the end', async () => {
    const full = Array.from({ length: 100 }, (_, i) => makeMessage({ id: 500 - i, kind: 'photo', date: oct, media: [makeMedia({ id: 1000 + i })] }));
    const api = fakeApi({ chatMedia: vi.fn(async (_c: number, _t: string, before = 0) => (before ? [] : full)) });
    const { container } = renderWithStore(<SharedMedia chatId={10} />, api);
    await waitFor(() => expect(container.querySelectorAll('.SharedMedia-tile')).toHaveLength(100));
    fireEvent.scroll(container.querySelector('.SharedMedia-content')!);
    await waitFor(() => expect(api.chatMedia).toHaveBeenLastCalledWith(10, 'media', 401, 100));
  });

  it('closes the panel', async () => {
    const { store } = renderWithStore(<SharedMedia chatId={10} />);
    store.sharedMediaOpen.value = true;
    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    expect(store.sharedMediaOpen.value).toBe(false);
  });
});

describe('MediaViewer', () => {
  const media = (): Message[] => [
    makeMessage({ id: 3, kind: 'photo', text: 'third', media: [makeMedia({ id: 30 })] }),
    makeMessage({ id: 2, kind: 'video', media: [makeMedia({ id: 20, kind: 'video' })] }),
    makeMessage({ id: 1, kind: 'photo', media: [makeMedia({ id: 10, state: 'failed' })] }),
  ];

  it('walks all archived media of the chat and closes with Escape', async () => {
    const api = fakeApi({ chatMedia: vi.fn(async () => media()) });
    const { store, container } = renderWithStore(<MediaViewer />, api);
    act(() => {
      store.viewer.value = { chatId: 10, messageId: 3, mediaId: 30 };
    });
    expect(await screen.findByText(/2 \/ 2/)).toBeTruthy();
    expect(container.querySelector('.MediaViewer-content img')!.getAttribute('src')).toBe('/media/30');
    expect(screen.getByText('third')).toBeTruthy();
    expect(screen.getByRole('link', { name: '下载' }).getAttribute('href')).toBe('/media/30?download=1');
    fireEvent.click(screen.getByRole('button', { name: '上一个' }));
    expect(container.querySelector('.MediaViewer-content video')!.getAttribute('src')).toBe('/media/20');
    expect(screen.queryByRole('button', { name: '上一个' })).toBeNull();
    fireEvent.keyDown(window, { key: 'ArrowRight' });
    expect(container.querySelector('.MediaViewer-content img')!.getAttribute('src')).toBe('/media/30');
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(store.viewer.value).toBeNull();
  });

  it('zooms photos within bounds', async () => {
    const api = fakeApi({ chatMedia: vi.fn(async () => media()) });
    const { store, container } = renderWithStore(<MediaViewer />, api);
    act(() => {
      store.viewer.value = { chatId: 10, messageId: 3, mediaId: 30 };
    });
    await screen.findByText(/2 \/ 2/);
    const img = () => container.querySelector('.MediaViewer-content img') as HTMLElement;
    expect((screen.getByRole('button', { name: '缩小' }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: '放大' }));
    expect(img().style.transform).toContain('scale(1.5)');
    fireEvent.dblClick(img());
    expect(img().style.transform).toContain('scale(1)');
  });

  it('closes itself when the target media is not available', async () => {
    const api = fakeApi({ chatMedia: vi.fn(async () => []) });
    const { store } = renderWithStore(<MediaViewer />, api);
    act(() => {
      store.viewer.value = { chatId: 10, messageId: 9, mediaId: 99 };
    });
    await waitFor(() => expect(store.viewer.value).toBeNull());
  });
});
```


- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/components/right`
Expected: FAIL（`Failed to resolve import "../viewer/MediaViewer"`、`"./SharedMedia"`）

- [ ] **Step 3: 实现**

`web/src/components/right/SharedMedia.tsx`：

```tsx
import { Play, X } from 'lucide-preact';
import { useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { errorMessage, mediaUrl } from '../../api/client';
import type { Message, SharedMediaType } from '../../api/types';
import { extractLinks } from '../../lib/entities';
import { formatDuration, formatMonth, hashString, monthKey, peerColor, previewText } from '../../lib/format';
import { useStore } from '../../state/store';
import { IconButton } from '../../ui/Button';
import { Spinner } from '../../ui/Spinner';
import { Tabs } from '../../ui/Tabs';
import { Document } from '../media/Document';
import { mainMedia, readyThumb } from '../media/util';
import './right.scss';

export const SHARED_PAGE = 100;

const TABS: { key: SharedMediaType; label: string; empty: string }[] = [
  { key: 'media', label: '媒体', empty: '暂无媒体' },
  { key: 'file', label: '文件', empty: '暂无文件' },
  { key: 'link', label: '链接', empty: '暂无链接' },
];

interface ListState {
  items: Message[]; // newest first, as the API returns them
  hasMore: boolean;
  loading: boolean;
  error: string;
}

const INITIAL: ListState = { items: [], hasMore: true, loading: false, error: '' };

function MediaTile({ msg }: { msg: Message }) {
  const store = useStore();
  const main = mainMedia(msg);
  const thumb = readyThumb(msg);
  if (!main) return null;
  const done = main.state === 'done';
  const src = main.kind === 'photo' && done ? mediaUrl(main.id) : thumb ? mediaUrl(thumb.id) : null;
  return (
    <button
      type="button"
      class="SharedMedia-tile"
      disabled={!done}
      aria-label={main.kind === 'photo' ? '查看照片' : '播放视频'}
      onClick={() => {
        store.viewer.value = { chatId: msg.chat_id, messageId: msg.id, mediaId: main.id };
      }}
    >
      {src ? (
        <img src={src} alt="" loading="lazy" decoding="async" />
      ) : done ? (
        <video src={`${mediaUrl(main.id)}#t=0.1`} preload="metadata" muted playsInline />
      ) : (
        <span class="SharedMedia-tile-state">{main.state === 'pending' ? '下载中' : '不可用'}</span>
      )}
      {main.kind !== 'photo' && (
        <span class="MediaBadge">
          <Play size={10} fill="currentColor" /> {main.kind === 'animation' ? 'GIF' : formatDuration(main.duration)}
        </span>
      )}
    </button>
  );
}

function LinkRows({ msg }: { msg: Message }) {
  const links = extractLinks(msg.text, msg.entities);
  return (
    <>
      {links.map((href) => {
        const host = new URL(href).hostname || href;
        return (
          <a class="SharedLink" key={`${msg.id}-${href}`} href={href} target="_blank" rel="noopener noreferrer">
            <span class="SharedLink-icon" style={{ background: peerColor(hashString(host)) }}>
              {Array.from(host.replace(/^www\./, ''))[0]?.toUpperCase()}
            </span>
            <span class="SharedLink-info">
              <span class="SharedLink-title">{host}</span>
              <span class="SharedLink-url">{href}</span>
              <span class="SharedLink-text">{previewText(msg.kind, msg.text)}</span>
            </span>
          </a>
        );
      })}
    </>
  );
}

/** Right column: shared media / files / links of the open chat, grouped by month, paged by message id. */
export function SharedMedia({ chatId }: { chatId: number }) {
  const store = useStore();
  const [tab, setTab] = useState<SharedMediaType>('media');
  const [list, setList] = useState<ListState>(INITIAL);
  const token = useRef(0);
  const stateRef = useRef(list);
  stateRef.current = list;

  const load = async (reset: boolean) => {
    const current = reset ? INITIAL : stateRef.current;
    if (!reset && (current.loading || !current.hasMore)) return;
    const my = ++token.current;
    setList({ ...current, loading: true, error: '' });
    try {
      const before = current.items.length ? current.items[current.items.length - 1].id : 0;
      const page = await store.api.chatMedia(chatId, tab, before, SHARED_PAGE);
      if (my !== token.current) return;
      setList({ items: [...current.items, ...page], hasMore: page.length >= SHARED_PAGE, loading: false, error: '' });
    } catch (e) {
      if (my !== token.current) return;
      setList({ ...current, loading: false, error: errorMessage(e) });
    }
  };

  useEffect(() => {
    void load(true);
  }, [chatId, tab]);

  const groups = useMemo(() => {
    const out: { key: string; label: string; items: Message[] }[] = [];
    for (const m of list.items) {
      const key = monthKey(m.date);
      if (out[out.length - 1]?.key !== key) out.push({ key, label: formatMonth(m.date), items: [] });
      out[out.length - 1].items.push(m);
    }
    return out;
  }, [list.items]);

  const onScroll = (e: Event) => {
    const el = e.currentTarget as HTMLElement;
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 300) void load(false);
  };

  const current = TABS.find((t) => t.key === tab)!;

  return (
    <div class="SharedMedia">
      <div class="right-header">
        <IconButton
          label="关闭"
          onClick={() => {
            store.sharedMediaOpen.value = false;
          }}
        >
          <X size={24} />
        </IconButton>
        <h3 class="right-header-title">共享媒体</h3>
      </div>
      <Tabs class="SharedMedia-tabs" items={TABS.map((t) => ({ key: t.key, label: t.label }))} active={tab} onChange={setTab} />
      <div class="SharedMedia-content custom-scroll" onScroll={onScroll}>
        {groups.map((g) => (
          <section key={g.key} class="SharedMedia-month">
            <h4 class="SharedMedia-month-title">{g.label}</h4>
            {tab === 'media' && (
              <div class="SharedMedia-grid">
                {g.items.map((m) => (
                  <MediaTile key={m.id} msg={m} />
                ))}
              </div>
            )}
            {tab === 'file' && (
              <div class="SharedMedia-files">
                {g.items.map((m) => (
                  <Document key={m.id} msg={m} />
                ))}
              </div>
            )}
            {tab === 'link' && (
              <div class="SharedMedia-links">
                {g.items.map((m) => (
                  <LinkRows key={m.id} msg={m} />
                ))}
              </div>
            )}
          </section>
        ))}
        {list.loading && (
          <div class="SharedMedia-notice">
            <Spinner size={28} />
          </div>
        )}
        {list.error && (
          <div class="SharedMedia-notice">
            <span>{list.error}</span>
            <button type="button" onClick={() => void load(list.items.length === 0)}>
              重试
            </button>
          </div>
        )}
        {!list.loading && !list.error && list.items.length === 0 && <div class="SharedMedia-notice">{current.empty}</div>}
      </div>
    </div>
  );
}
```

`web/src/components/right/right.scss`：

```scss
.SharedMedia {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.right-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 1.375rem;
  height: var(--column-header-height);
  padding: 0.5rem 0.75rem;
}

.right-header-title {
  font-size: 1.25rem;
  font-weight: var(--font-weight-medium);
}

.SharedMedia-tabs {
  flex-shrink: 0;
  padding: 0 0.5rem;
  background: var(--color-background);
  box-shadow: inset 0 -1px 0 var(--color-borders);
}

.SharedMedia-content {
  flex: 1;
  min-height: 0;
  background: var(--color-background);
}

.SharedMedia-month-title {
  margin: 0;
  padding: 0.75rem 1.25rem 0.5rem;
  font-size: 0.875rem;
  font-weight: var(--font-weight-medium);
  color: var(--color-text-secondary);
}

.SharedMedia-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  grid-auto-rows: 1fr;
  gap: 0.0625rem;
}

.SharedMedia-tile {
  position: relative;
  aspect-ratio: 1;
  overflow: hidden;
  background: var(--color-background-secondary-accent);

  img,
  video {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
  }

  .MediaBadge {
    display: flex;
    align-items: center;
    gap: 0.125rem;
  }

  &:disabled {
    cursor: default;
  }
}

.SharedMedia-tile-state {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  font-size: 0.75rem;
  color: var(--color-text-secondary);
}

.SharedMedia-files,
.SharedMedia-links {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
  padding: 0 1.25rem 1.25rem;
}

.SharedLink {
  display: flex;
  gap: 0.75rem;
  color: inherit;
  text-decoration: none !important;
}

.SharedLink-icon {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 3rem;
  height: 3rem;
  border-radius: var(--border-radius-messages-small);
  font-size: 1.5rem;
  font-weight: var(--font-weight-medium);
  color: #fff;
}

.SharedLink-info {
  min-width: 0;
  display: flex;
  flex-direction: column;
  font-size: 0.9375rem;
  line-height: 1.25rem;

  > span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.SharedLink-title {
  font-weight: var(--font-weight-medium);
}

.SharedLink-url {
  color: var(--color-links);
}

.SharedLink-text {
  font-size: 0.875rem;
  color: var(--color-text-secondary);
}

.SharedMedia-notice {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  padding: 1.5rem 1rem;
  color: var(--color-text-secondary);
  font-size: 0.875rem;

  button {
    color: var(--color-primary);
    font-weight: var(--font-weight-medium);
  }
}
```

`web/src/components/viewer/MediaViewer.tsx`：

```tsx
import { ChevronLeft, ChevronRight, Download, X, ZoomIn, ZoomOut } from 'lucide-preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import type { Media, Message } from '../../api/types';
import { formatFullDate, senderName } from '../../lib/format';
import { useStore, type ViewerTarget } from '../../state/store';
import { IconButton } from '../../ui/Button';
import { RichText } from '../message/RichText';
import { VISUAL_KINDS, mainMedia } from '../media/util';
import './viewer.scss';

export const VIEWER_PAGE = 100;
export const VIEWER_MAX_PAGES = 50;
const MIN_ZOOM = 1;
const MAX_ZOOM = 4;
const ZOOM_STEP = 0.5;

export interface ViewerItem {
  msg: Message;
  media: Media;
}

export function toViewerItems(msgs: Message[]): ViewerItem[] {
  const out: ViewerItem[] = [];
  for (const msg of msgs) {
    const media = mainMedia(msg);
    if (media && media.state === 'done' && VISUAL_KINDS.includes(msg.kind)) out.push({ msg, media });
  }
  return out.sort((a, b) => a.msg.id - b.msg.id);
}

function ViewerInner({ target }: { target: ViewerTarget }) {
  const store = useStore();
  const close = () => {
    store.viewer.value = null;
  };
  const seed = toViewerItems(store.conv(target.chatId).items.filter((m) => m.id === target.messageId));
  const [items, setItems] = useState<ViewerItem[]>(seed);
  const [mediaId, setMediaId] = useState(target.mediaId);
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null);

  // Load the whole chat's media (newest first, paged by id) so left/right walks all of it.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const all: Message[] = [];
      let before = 0;
      for (let i = 0; i < VIEWER_MAX_PAGES; i++) {
        const page = await store.api.chatMedia(target.chatId, 'media', before, VIEWER_PAGE);
        all.push(...page);
        if (page.length < VIEWER_PAGE) break;
        before = page[page.length - 1].id;
      }
      if (cancelled) return;
      const list = toViewerItems(all);
      if (list.some((it) => it.media.id === target.mediaId)) setItems(list);
      else if (seed.length === 0) close();
    })().catch(() => {
      if (!cancelled && seed.length === 0) close();
    });
    return () => {
      cancelled = true;
    };
  }, [target.chatId, target.mediaId]);

  const index = items.findIndex((it) => it.media.id === mediaId);
  const item = index >= 0 ? items[index] : undefined;

  const go = (delta: number) => {
    const next = items[index + delta];
    if (!next) return;
    setMediaId(next.media.id);
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
  const chat = store.chats.value.find((c) => c.id === target.chatId);
  const isPhoto = item.msg.kind === 'photo';

  return (
    <div class="MediaViewer" role="dialog" aria-modal="true" aria-label="媒体查看器">
      <div class="MediaViewer-head">
        <div class="MediaViewer-sender">
          <span class="MediaViewer-name">{chat ? senderName(chat.sender) : ''}</span>
          <span class="MediaViewer-date">
            {formatFullDate(item.msg.date)}
            {items.length > 1 && ` · ${index + 1} / ${items.length}`}
          </span>
        </div>
        <div class="MediaViewer-actions">
          {isPhoto && (
            <>
              <IconButton label="缩小" class="translucent-white" disabled={zoom <= MIN_ZOOM} onClick={() => setZoomClamped(zoom - ZOOM_STEP)}>
                <ZoomOut size={24} />
              </IconButton>
              <IconButton label="放大" class="translucent-white" disabled={zoom >= MAX_ZOOM} onClick={() => setZoomClamped(zoom + ZOOM_STEP)}>
                <ZoomIn size={24} />
              </IconButton>
            </>
          )}
          <a class="IconButton translucent-white" href={mediaUrl(item.media.id, true)} download aria-label="下载" title="下载">
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
      >
        {isPhoto ? (
          <img
            key={item.media.id}
            src={mediaUrl(item.media.id)}
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
            key={item.media.id}
            src={mediaUrl(item.media.id)}
            controls={item.msg.kind === 'video'}
            autoplay
            loop={item.msg.kind === 'animation'}
            muted={item.msg.kind === 'animation'}
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
      {item.msg.text && (
        <div class="MediaViewer-caption">
          <RichText text={item.msg.text} entities={item.msg.entities} />
        </div>
      )}
    </div>
  );
}

/** Full-screen viewer over all photos/videos/GIFs of the chat; opened by setting store.viewer. */
export function MediaViewer() {
  const store = useStore();
  const target = store.viewer.value;
  if (!target) return null;
  return <ViewerInner key={`${target.chatId}:${target.mediaId}`} target={target} />;
}
```

`web/src/components/viewer/viewer.scss`：

```scss
.MediaViewer {
  position: fixed;
  inset: 0;
  z-index: 40;
  display: grid;
  grid-template-rows: auto 1fr auto;
  background: rgba(0, 0, 0, 0.9);
  color: #fff;
  animation: fade-in 0.3s cubic-bezier(0.33, 1, 0.68, 1);
}

.MediaViewer-head {
  position: relative;
  z-index: 2;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.5rem 1.25rem;
  background: linear-gradient(to bottom, #000 0%, transparent 100%);

  @media (max-width: 600px) {
    padding: 0.5rem;
  }
}

.MediaViewer-sender {
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.MediaViewer-name {
  font-weight: var(--font-weight-medium);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.MediaViewer-date {
  font-size: 0.875rem;
  opacity: 0.75;
}

.MediaViewer-actions {
  display: flex;
  gap: 0.25rem;

  a.IconButton {
    color: #fff;
  }

  .IconButton:disabled {
    opacity: 0.4;
  }
}

.MediaViewer-content {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 0;
  overflow: hidden;

  img,
  video {
    max-width: 100%;
    max-height: 100%;
    object-fit: contain;
    transition: transform 0.15s ease-out;
    user-select: none;
  }

  img.zoomed {
    cursor: grab;
    transition: none;
  }
}

.MediaViewer-nav {
  position: absolute;
  top: 50%;
  z-index: 2;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 3.5rem;
  height: 3.5rem;
  margin-top: -1.75rem;
  border-radius: 50%;
  color: #fff;
  opacity: 0.7;
  transition: opacity 0.15s, background-color 0.15s;

  &:hover {
    opacity: 1;
    background: rgba(255, 255, 255, 0.1);
  }

  &.prev {
    left: 1rem;
  }

  &.next {
    right: 1rem;
  }
}

.MediaViewer-caption {
  max-height: 30vh;
  overflow-y: auto;
  padding: 1rem 1.25rem 1.25rem;
  text-align: center;
  white-space: pre-wrap;
  background: linear-gradient(to top, #000 0%, transparent 100%);
}
```


- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/components/right web/src/components/viewer
git commit -m "feat(web): shared media drawer by month and full-screen media viewer"
```

---

### Task 13: 管理页（机器人、白名单、API 凭据、用户账号）

**Files:**
- Create: `web/src/components/settings/{common,SettingsHome,AddBot,BotSettings,TelegramAppSettings,UserbotSettings,SettingsPanel}.tsx`、`web/src/components/settings/settings.scss`
- Test: `web/src/components/settings/settings.test.tsx`

**Interfaces:**
- Consumes: Task 3 全部 admin 方法、`ApiError`、`avatarUrl`、`errorMessage`；Task 4 `botName`、`formatListTime`、`navigate`、`Route`；Task 5 store（`api`、`bots`、`loadBots`、`loadChats`、`showToast`）；Task 6 全部 UI 组件
- Produces:
  - `SettingsShell({ title, back: Route, children })`、`Section({ title?, children })`、`Description`、`StatusDot({ status })`、文案表 `BOT_STATUS`、`SERVER_STATE`、`USERBOT_STATE`
  - `SettingsHome()`（`/settings`，标题「管理」）：「机器人」区（每行头像、名称、状态点与状态 / 错误、启停开关 `aria-label="启用 <名称>"`，点击进入详情；「添加机器人」）；「Telegram」区（「API 凭据」`api_id <id> · Bot API <状态>` / `未配置`；「用户账号」状态，接口 404 时「未启用」）
  - `TOKEN_RE`、`AddBot()`（`/settings/bots/new`）：本地校验 token 格式；提交后逐步列出「校验 token」「从云端 Bot API 登出」「开始接收消息」及 detail；成功后「设置白名单」，409 时「查看已有机器人」
  - `BotSettings({ botId })`（`/settings/bots/:id`）：资料卡、「接收消息」开关；「白名单」（备注 / ID、「代取」开关、编辑弹窗、移除、添加表单「用户 ID」「备注（可选）」「允许代取受保护内容」「加入白名单」）；「最近被拒绝」（排除已在白名单者；一键加白，备注取名字或用户名）；「删除机器人」确认框含「同时删除该机器人的全部存档」
  - `TelegramAppSettings()`（`/settings/telegram-app`）：服务器状态；`api_id` / `api_hash` 校验（正整数、32 位十六进制）后 `PUT`，成功提示「已保存」
  - `USERBOT_POLL_MS = 3000`、`UserbotSettings()`（`/settings/userbot`）：风险说明；状态行；`unconfigured` →「填写 API 凭据」；`logged_out` / `error` → 手机号表单「发送验证码」；`code_sent` →「验证码」「下一步」「重新输入手机号」；`password_needed` →「密码」「登录」；`ready` →「登出」（二次确认）；`connecting` 每 3 秒轮询；服务端 400/409/502/503 的错误文本显示在输入框标签上；接口 404 →「当前服务未启用用户账号功能」
  - `SettingsPanel({ route })`：按路由分派

- [ ] **Step 1: 写失败测试**

`web/src/components/settings/settings.test.tsx`：

```tsx
import { act, fireEvent, screen, waitFor } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/client';
import type { UserbotInfo } from '../../api/types';
import { route } from '../../lib/router';
import { fakeApi, makeBot } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { AddBot } from './AddBot';
import { BotSettings } from './BotSettings';
import { SettingsHome } from './SettingsHome';
import { TelegramAppSettings } from './TelegramAppSettings';
import { USERBOT_POLL_MS, UserbotSettings } from './UserbotSettings';

afterEach(() => {
  route.value = { name: 'home' };
  history.replaceState(null, '', '/');
  vi.useRealTimers();
});

const ub = (state: UserbotInfo['state'], over: Partial<UserbotInfo> = {}): UserbotInfo => ({ state, phone: '', name: '', tg_user_id: 0, error: '', ...over });
const type = (label: string | RegExp, value: string) => fireEvent.input(screen.getByLabelText(label), { target: { value } });

describe('SettingsHome', () => {
  it('lists bots with status and toggles them', async () => {
    const api = fakeApi({
      bots: vi.fn(async () => [
        makeBot({ id: 1, name: 'Alpha', status: 'running' }),
        makeBot({ id: 2, name: 'Beta', status: 'error', last_error: 'Unauthorized' }),
        makeBot({ id: 3, name: 'Gone', status: 'removed' }),
      ]),
      setBotEnabled: vi.fn(async (id: number, enabled: boolean) => makeBot({ id, name: 'Alpha', enabled, status: 'stopped' })),
    });
    const { store } = renderWithStore(<SettingsHome />, api);
    expect(await screen.findByText('Alpha')).toBeTruthy();
    expect(screen.getByText('Unauthorized')).toBeTruthy();
    expect(screen.queryByText('Gone')).toBeNull();
    expect(await screen.findByText('api_id 1 · Bot API 运行中')).toBeTruthy();
    expect(screen.getByText('未登录')).toBeTruthy();
    await act(async () => {
      fireEvent.click(screen.getByRole('switch', { name: '启用 Alpha' }));
    });
    expect(api.setBotEnabled).toHaveBeenCalledWith(1, false);
    expect(store.bots.value[0].enabled).toBe(false);
    fireEvent.click(screen.getByText('添加机器人'));
    expect(route.value).toEqual({ name: 'settings-add-bot' });
  });

  it('reports a missing userbot endpoint as 未启用', async () => {
    renderWithStore(<SettingsHome />, fakeApi({ userbot: vi.fn(async () => Promise.reject(new ApiError(404, 'not found', null))) }));
    expect(await screen.findByText('未启用')).toBeTruthy();
  });
});

describe('AddBot', () => {
  const TOKEN = '123456:ABCdefGHIjklMNOpqrSTUvwxYZ0123456789';

  it('rejects malformed tokens without calling the API', async () => {
    const api = fakeApi();
    renderWithStore(<AddBot />, api);
    type('Bot Token', 'nope');
    fireEvent.click(screen.getByText('添加'));
    expect(await screen.findByLabelText('token 格式不正确')).toBeTruthy();
    expect(api.addBot).not.toHaveBeenCalled();
  });

  it('shows each step after adding', async () => {
    const api = fakeApi({
      addBot: vi.fn(async () => ({
        bot_id: 4,
        steps: [
          { step: 'getMe', ok: true, detail: '@new_bot' },
          { step: 'logOut', ok: true, detail: '已从云端登出' },
          { step: 'start', ok: true, detail: '' },
        ],
      })),
    });
    renderWithStore(<AddBot />, api);
    type('Bot Token', ` ${TOKEN} `);
    await act(async () => {
      fireEvent.click(screen.getByText('添加'));
    });
    expect(api.addBot).toHaveBeenCalledWith(TOKEN);
    expect(screen.getByText('校验 token')).toBeTruthy();
    expect(screen.getByText('@new_bot')).toBeTruthy();
    expect(screen.getByText('开始接收消息')).toBeTruthy();
    fireEvent.click(screen.getByText('设置白名单'));
    expect(route.value).toEqual({ name: 'settings-bot', botId: 4 });
  });

  it('shows the failed step and links an existing bot on conflict', async () => {
    const api = fakeApi({
      addBot: vi.fn(async () =>
        Promise.reject(new ApiError(409, '机器人已存在', { error: '机器人已存在', bot_id: 2, steps: [{ step: 'getMe', ok: true, detail: '@dup' }] })),
      ),
    });
    renderWithStore(<AddBot />, api);
    type('Bot Token', TOKEN);
    await act(async () => {
      fireEvent.click(screen.getByText('添加'));
    });
    expect(await screen.findByLabelText('机器人已存在')).toBeTruthy();
    expect(screen.getByText('@dup')).toBeTruthy();
    fireEvent.click(screen.getByText('查看已有机器人'));
    expect(route.value).toEqual({ name: 'settings-bot', botId: 2 });
  });
});

describe('BotSettings', () => {
  const setup = () => {
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1, name: 'Alpha' })]),
      whitelist: vi.fn(async () => [{ tg_user_id: 42, note: '我', can_fetch: false }]),
      rejected: vi.fn(async () => [
        { tg_user_id: 7, first_name: 'Eve', username: 'eve', last_seen_at: 1_790_000_000, count: 3 },
        { tg_user_id: 42, first_name: 'Me', username: '', last_seen_at: 1, count: 1 },
      ]),
    });
    return renderWithStore(<BotSettings botId={1} />, api);
  };

  it('shows whitelist and rejected senders not already listed', async () => {
    setup();
    expect(await screen.findByText('我')).toBeTruthy();
    expect(screen.getByText('Eve')).toBeTruthy();
    expect(screen.getByText(/3 次/)).toBeTruthy();
    expect(screen.queryByText('Me')).toBeNull();
  });

  it('toggles can_fetch and whitelists rejected senders in one click', async () => {
    const { api } = setup();
    await screen.findByText('我');
    await act(async () => {
      fireEvent.click(screen.getByRole('switch', { name: '允许 42 代取' }));
    });
    expect(api.putWhitelist).toHaveBeenCalledWith(1, 42, '我', true);
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '将 7 加入白名单' }));
    });
    expect(api.putWhitelist).toHaveBeenCalledWith(1, 7, 'Eve', false);
  });

  it('validates and adds a whitelist entry', async () => {
    const { api } = setup();
    await screen.findByText('我');
    type('用户 ID', 'abc');
    fireEvent.click(screen.getByText('加入白名单'));
    expect(await screen.findByLabelText('请输入正确的用户 ID')).toBeTruthy();
    type('请输入正确的用户 ID', '1001');
    type('备注（可选）', ' 朋友 ');
    fireEvent.click(screen.getByText('允许代取受保护内容'));
    await act(async () => {
      fireEvent.click(screen.getByText('加入白名单'));
    });
    expect(api.putWhitelist).toHaveBeenCalledWith(1, 1001, '朋友', true);
  });

  it('reports an unknown bot once bots are loaded', async () => {
    renderWithStore(<BotSettings botId={99} />, fakeApi({ bots: vi.fn(async () => []) }));
    expect(await screen.findByText('机器人不存在')).toBeTruthy();
  });

  it('removes entries and deletes the bot with purge', async () => {
    const { api } = setup();
    await screen.findByText('我');
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '移除 42' }));
    });
    expect(api.deleteWhitelist).toHaveBeenCalledWith(1, 42);
    fireEvent.click(screen.getByText('删除机器人'));
    fireEvent.click(screen.getByText('同时删除该机器人的全部存档'));
    await act(async () => {
      fireEvent.click(screen.getByText('删除'));
    });
    expect(api.deleteBot).toHaveBeenCalledWith(1, true);
    await waitFor(() => expect(route.value).toEqual({ name: 'settings' }));
  });
});

describe('TelegramAppSettings', () => {
  it('validates and saves credentials', async () => {
    const api = fakeApi({ telegramApp: vi.fn(async () => ({ configured: true, api_id: 4242, server: { managed: true, state: 'restarting', error: 'exit 1' } })) });
    renderWithStore(<TelegramAppSettings />, api);
    expect(await screen.findByText('重启中：exit 1')).toBeTruthy();
    expect((screen.getByLabelText('api_id') as HTMLInputElement).value).toBe('4242');
    type('api_hash（已保存，修改时重新输入）', 'xyz');
    fireEvent.click(screen.getByText('保存'));
    expect(await screen.findByLabelText('api_hash 必须是 32 位十六进制字符串')).toBeTruthy();
    expect(api.saveTelegramApp).not.toHaveBeenCalled();
    type('api_hash 必须是 32 位十六进制字符串', '0123456789abcdef0123456789ABCDEF');
    await act(async () => {
      fireEvent.click(screen.getByText('保存'));
    });
    expect(api.saveTelegramApp).toHaveBeenCalledWith(4242, '0123456789abcdef0123456789ABCDEF');
  });
});

describe('UserbotSettings', () => {
  it('walks phone → code → password → ready', async () => {
    const api = fakeApi({
      userbot: vi.fn(async () => ub('logged_out')),
      userbotPhone: vi.fn(async () => ub('code_sent', { phone: '+8613800000000' })),
      userbotCode: vi.fn(async () => ub('password_needed', { phone: '+8613800000000' })),
      userbotPassword: vi.fn(async () => ub('ready', { phone: '+8613800000000', name: 'Shinya', tg_user_id: 5 })),
    });
    renderWithStore(<UserbotSettings />, api);
    await screen.findByLabelText('手机号（含国家代码）');
    type('手机号（含国家代码）', '+86 138 0000 0000');
    await act(async () => {
      fireEvent.click(screen.getByText('发送验证码'));
    });
    expect(api.userbotPhone).toHaveBeenCalledWith('+86 138 0000 0000');
    expect(screen.getByText(/\+8613800000000 的 Telegram 客户端/)).toBeTruthy();
    type('验证码', '12345');
    await act(async () => {
      fireEvent.click(screen.getByText('下一步'));
    });
    expect(api.userbotCode).toHaveBeenCalledWith('12345');
    type('密码', 'pw');
    await act(async () => {
      fireEvent.click(screen.getByText('登录'));
    });
    expect(api.userbotPassword).toHaveBeenCalledWith('pw');
    expect(screen.getByText('Shinya · +8613800000000 · ID 5')).toBeTruthy();
    expect(screen.getByText('登出')).toBeTruthy();
  });

  it('shows server-side input errors in the field', async () => {
    const api = fakeApi({
      userbot: vi.fn(async () => ub('code_sent', { phone: '+1' })),
      userbotCode: vi.fn(async () => Promise.reject(new ApiError(400, '验证码错误', null))),
    });
    renderWithStore(<UserbotSettings />, api);
    await screen.findByLabelText('验证码');
    type('验证码', '000');
    await act(async () => {
      fireEvent.click(screen.getByText('下一步'));
    });
    expect(await screen.findByLabelText('验证码错误')).toBeTruthy();
  });

  it('logs out after confirmation', async () => {
    const api = fakeApi({ userbot: vi.fn(async () => ub('ready', { name: 'Me' })) });
    renderWithStore(<UserbotSettings />, api);
    fireEvent.click(await screen.findByText('登出'));
    await act(async () => {
      fireEvent.click(screen.getAllByText('登出')[1]);
    });
    expect(api.userbotLogout).toHaveBeenCalled();
  });

  it('links to API credentials when unconfigured and polls while connecting', async () => {
    renderWithStore(<UserbotSettings />, fakeApi({ userbot: vi.fn(async () => ub('unconfigured')) }));
    fireEvent.click(await screen.findByText('填写 API 凭据'));
    expect(route.value).toEqual({ name: 'settings-telegram-app' });

    vi.useFakeTimers();
    const userbot = vi.fn().mockResolvedValueOnce(ub('connecting')).mockResolvedValue(ub('logged_out'));
    renderWithStore(<UserbotSettings />, fakeApi({ userbot }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(userbot).toHaveBeenCalledTimes(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(USERBOT_POLL_MS);
    });
    expect(userbot).toHaveBeenCalledTimes(2);
    expect(screen.getByText('发送验证码')).toBeTruthy();
  });
});
```


- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/components/settings`
Expected: FAIL（`Failed to resolve import "./AddBot"` 等）

- [ ] **Step 3: 实现**


`web/src/components/settings/common.tsx`：

```tsx
import { ArrowLeft } from 'lucide-preact';
import type { ComponentChildren } from 'preact';
import { navigate, type Route } from '../../lib/router';
import { IconButton } from '../../ui/Button';
import './settings.scss';

export function SettingsShell({ title, back, children }: { title: string; back: Route; children: ComponentChildren }) {
  return (
    <div class="SettingsPanel">
      <div class="left-header settings-header">
        <IconButton label="返回" onClick={() => navigate(back)}>
          <ArrowLeft size={24} />
        </IconButton>
        <h3 class="settings-header-title">{title}</h3>
      </div>
      <div class="settings-content custom-scroll">{children}</div>
    </div>
  );
}

export function Section({ title, children }: { title?: string; children: ComponentChildren }) {
  return (
    <section class="settings-section">
      {title && <h4 class="settings-section-title">{title}</h4>}
      {children}
    </section>
  );
}

export function Description({ children }: { children: ComponentChildren }) {
  return <p class="settings-description">{children}</p>;
}

export function StatusDot({ status }: { status: string }) {
  return <span class={`StatusDot status-${status}`} aria-hidden="true" />;
}

export const BOT_STATUS: Record<string, string> = {
  running: '运行中',
  stopped: '已停止',
  error: '错误',
  removed: '已删除',
};

export const SERVER_STATE: Record<string, string> = {
  running: '运行中',
  restarting: '重启中',
  unconfigured: '未配置',
};

export const USERBOT_STATE: Record<string, string> = {
  unconfigured: '未配置 API 凭据',
  connecting: '连接中…',
  logged_out: '未登录',
  code_sent: '等待验证码',
  password_needed: '等待二步验证密码',
  ready: '已登录',
  error: '错误',
};
```


`web/src/components/settings/SettingsHome.tsx`：

```tsx
import { KeyRound, Plus, Smartphone } from 'lucide-preact';
import { useEffect, useState } from 'preact/hooks';
import { ApiError, avatarUrl, errorMessage } from '../../api/client';
import type { Bot, TelegramApp, UserbotInfo } from '../../api/types';
import { botName } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { ListItem } from '../../ui/ListItem';
import { Switch } from '../../ui/Switch';
import { BOT_STATUS, SERVER_STATE, Section, SettingsShell, StatusDot, USERBOT_STATE } from './common';

function BotRow({ bot }: { bot: Bot }) {
  const store = useStore();
  const [busy, setBusy] = useState(false);
  const toggle = async (enabled: boolean) => {
    setBusy(true);
    try {
      const updated = await store.api.setBotEnabled(bot.id, enabled);
      store.bots.value = store.bots.value.map((b) => (b.id === bot.id ? updated : b));
    } catch (e) {
      store.showToast(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div class="ListItem BotRow">
      <button type="button" class="BotRow-main" onClick={() => navigate({ name: 'settings-bot', botId: bot.id })}>
        <Avatar name={botName(bot)} peerId={bot.tg_bot_id} src={bot.has_avatar ? avatarUrl('bots', bot.tg_bot_id) : null} size="tiny" />
        <span class="ListItem-text">
          <span class="ListItem-title">{botName(bot)}</span>
          <span class={`ListItem-subtitle${bot.status === 'error' ? ' error' : ''}`}>
            <StatusDot status={bot.status} />
            {bot.status === 'error' && bot.last_error ? bot.last_error : `${BOT_STATUS[bot.status] ?? bot.status}${bot.username ? ` · @${bot.username}` : ''}`}
          </span>
        </span>
      </button>
      <Switch checked={bot.enabled} disabled={busy} label={`启用 ${botName(bot)}`} onChange={(v) => void toggle(v)} />
    </div>
  );
}

export function SettingsHome() {
  const store = useStore();
  const [app, setApp] = useState<TelegramApp | null>(null);
  const [userbot, setUserbot] = useState<UserbotInfo | null | 'unavailable'>(null);

  useEffect(() => {
    void store.loadBots();
    store.api.telegramApp().then(setApp, (e) => store.showToast(errorMessage(e)));
    store.api.userbot().then(setUserbot, (e) => {
      if (e instanceof ApiError && e.status === 404) setUserbot('unavailable');
      else store.showToast(errorMessage(e));
    });
  }, []);

  const bots = store.bots.value.filter((b) => b.status !== 'removed');
  let appSubtitle = '加载中…';
  if (app) {
    const server = app.server.managed ? `Bot API ${SERVER_STATE[app.server.state] ?? app.server.state}` : '外部 Bot API';
    appSubtitle = app.configured ? `api_id ${app.api_id} · ${server}` : '未配置';
  }
  let userbotSubtitle = '加载中…';
  if (userbot === 'unavailable') userbotSubtitle = '未启用';
  else if (userbot) userbotSubtitle = userbot.state === 'ready' && userbot.name ? `已登录 · ${userbot.name}` : USERBOT_STATE[userbot.state] ?? userbot.state;

  return (
    <SettingsShell title="管理" back={{ name: 'home' }}>
      <Section title="机器人">
        {bots.map((b) => (
          <BotRow key={b.id} bot={b} />
        ))}
        <ListItem icon={<Plus size={24} />} title="添加机器人" onClick={() => navigate({ name: 'settings-add-bot' })} />
      </Section>
      <Section title="Telegram">
        <ListItem
          icon={<KeyRound size={24} />}
          title="API 凭据"
          subtitle={appSubtitle}
          onClick={() => navigate({ name: 'settings-telegram-app' })}
        />
        <ListItem
          icon={<Smartphone size={24} />}
          title="用户账号"
          subtitle={userbotSubtitle}
          onClick={() => navigate({ name: 'settings-userbot' })}
        />
      </Section>
    </SettingsShell>
  );
}
```


`web/src/components/settings/AddBot.tsx`：

```tsx
import { Check, X } from 'lucide-preact';
import { useState } from 'preact/hooks';
import { ApiError, errorMessage } from '../../api/client';
import type { AddBotResult, AddBotStep } from '../../api/types';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Button } from '../../ui/Button';
import { InputField } from '../../ui/InputField';
import { Description, Section, SettingsShell } from './common';

export const TOKEN_RE = /^\d+:[A-Za-z0-9_-]{30,}$/;

const STEP_LABELS: Record<string, string> = {
  getMe: '校验 token',
  logOut: '从云端 Bot API 登出',
  start: '开始接收消息',
};

function Steps({ steps }: { steps: AddBotStep[] }) {
  return (
    <ol class="AddBot-steps">
      {steps.map((s) => (
        <li key={s.step} class={s.ok ? 'ok' : 'failed'}>
          <span class="AddBot-step-icon">{s.ok ? <Check size={18} /> : <X size={18} />}</span>
          <span class="AddBot-step-text">
            <span class="AddBot-step-label">{STEP_LABELS[s.step] ?? s.step}</span>
            {s.detail && <span class="AddBot-step-detail">{s.detail}</span>}
          </span>
        </li>
      ))}
    </ol>
  );
}

export function AddBot() {
  const store = useStore();
  const [token, setToken] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<AddBotResult | null>(null);
  const [existing, setExisting] = useState(0);

  const submit = async (e: Event) => {
    e.preventDefault();
    const t = token.trim();
    setResult(null);
    setExisting(0);
    if (!TOKEN_RE.test(t)) {
      setError('token 格式不正确');
      return;
    }
    setError('');
    setBusy(true);
    try {
      const res = await store.api.addBot(t);
      setResult(res);
      setToken('');
      await Promise.all([store.loadBots(), store.loadChats()]);
    } catch (err) {
      setError(errorMessage(err));
      if (err instanceof ApiError && err.body && typeof err.body === 'object') {
        const body = err.body as Partial<AddBotResult> & { bot_id?: number };
        if (Array.isArray(body.steps)) setResult({ steps: body.steps, error: body.error });
        if (err.status === 409 && body.bot_id) setExisting(body.bot_id);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <SettingsShell title="添加机器人" back={{ name: 'settings' }}>
      <Section>
        <Description>
          在 @BotFather 创建机器人并复制 token。添加后机器人会从云端 Bot API 登出，改由本服务的本地 Bot API 接收消息。
        </Description>
        <form class="settings-form" onSubmit={(e) => void submit(e)}>
          <InputField label="Bot Token" value={token} onInput={setToken} error={error} autoComplete="off" disabled={busy} />
          <Button type="submit" loading={busy}>
            添加
          </Button>
        </form>
        {busy && <p class="settings-description">正在校验 token、从云端登出并启动，最长约 30 秒…</p>}
        {result && <Steps steps={result.steps} />}
        {result?.bot_id && (
          <Button variant="secondary" onClick={() => navigate({ name: 'settings-bot', botId: result.bot_id! })}>
            设置白名单
          </Button>
        )}
        {existing > 0 && (
          <Button variant="secondary" onClick={() => navigate({ name: 'settings-bot', botId: existing })}>
            查看已有机器人
          </Button>
        )}
      </Section>
    </SettingsShell>
  );
}
```


`web/src/components/settings/BotSettings.tsx`：

```tsx
import { Pencil, Trash2, UserPlus } from 'lucide-preact';
import { useEffect, useState } from 'preact/hooks';
import { avatarUrl, errorMessage } from '../../api/client';
import type { RejectedSender, WhitelistEntry } from '../../api/types';
import { botName, formatListTime } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { Button, IconButton } from '../../ui/Button';
import { Checkbox } from '../../ui/Checkbox';
import { InputField } from '../../ui/InputField';
import { ListItem } from '../../ui/ListItem';
import { ConfirmDialog, Modal } from '../../ui/Modal';
import { Switch } from '../../ui/Switch';
import { BOT_STATUS, Description, Section, SettingsShell, StatusDot } from './common';

const UID_RE = /^[1-9][0-9]{0,15}$/;

function EditEntry({ entry, onSave, onClose }: { entry: WhitelistEntry; onSave: (note: string, canFetch: boolean) => Promise<void>; onClose: () => void }) {
  const [note, setNote] = useState(entry.note);
  const [canFetch, setCanFetch] = useState(entry.can_fetch);
  const [busy, setBusy] = useState(false);
  return (
    <Modal title={`编辑 ${entry.tg_user_id}`} onClose={onClose}>
      <InputField label="备注" value={note} onInput={setNote} />
      <Checkbox label="允许代取受保护内容" checked={canFetch} onChange={setCanFetch} />
      <div class="Modal-actions">
        <button type="button" class="Modal-action" onClick={onClose}>
          取消
        </button>
        <button
          type="button"
          class="Modal-action"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            await onSave(note.trim(), canFetch);
            setBusy(false);
          }}
        >
          保存
        </button>
      </div>
    </Modal>
  );
}

export function BotSettings({ botId }: { botId: number }) {
  const store = useStore();
  const bot = store.bots.value.find((b) => b.id === botId);
  const [whitelist, setWhitelist] = useState<WhitelistEntry[]>([]);
  const [rejected, setRejected] = useState<RejectedSender[]>([]);
  const [uid, setUid] = useState('');
  const [note, setNote] = useState('');
  const [canFetch, setCanFetch] = useState(false);
  const [formError, setFormError] = useState('');
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState<WhitelistEntry | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [purge, setPurge] = useState(false);
  const [checked, setChecked] = useState(false);

  const reload = async () => {
    try {
      const [w, r] = await Promise.all([store.api.whitelist(botId), store.api.rejected(botId)]);
      setWhitelist(w);
      setRejected(r);
    } catch (e) {
      store.showToast(errorMessage(e));
    }
  };

  useEffect(() => {
    void store.loadBots().then(() => setChecked(true));
    void reload();
  }, [botId]);

  const put = async (id: number, n: string, fetch: boolean) => {
    try {
      await store.api.putWhitelist(botId, id, n, fetch);
      await reload();
      return true;
    } catch (e) {
      store.showToast(errorMessage(e));
      return false;
    }
  };

  const add = async (e: Event) => {
    e.preventDefault();
    const v = uid.trim();
    if (!UID_RE.test(v) || !Number.isSafeInteger(Number(v))) {
      setFormError('请输入正确的用户 ID');
      return;
    }
    setFormError('');
    setBusy(true);
    if (await put(Number(v), note.trim(), canFetch)) {
      setUid('');
      setNote('');
      setCanFetch(false);
    }
    setBusy(false);
  };

  const toggleEnabled = async (enabled: boolean) => {
    try {
      const updated = await store.api.setBotEnabled(botId, enabled);
      store.bots.value = store.bots.value.map((b) => (b.id === botId ? updated : b));
    } catch (e) {
      store.showToast(errorMessage(e));
    }
  };

  const remove = async () => {
    setBusy(true);
    try {
      await store.api.deleteBot(botId, purge);
      await Promise.all([store.loadBots(), store.loadChats()]);
      navigate({ name: 'settings' }, true);
    } catch (e) {
      store.showToast(errorMessage(e));
      setBusy(false);
    }
  };

  if (!bot) {
    return (
      <SettingsShell title="机器人" back={{ name: 'settings' }}>
        <Section>
          <Description>{checked ? '机器人不存在' : '加载中…'}</Description>
        </Section>
      </SettingsShell>
    );
  }

  const listed = new Set(whitelist.map((w) => w.tg_user_id));

  return (
    <SettingsShell title={botName(bot)} back={{ name: 'settings' }}>
      <Section>
        <div class="BotProfile">
          <Avatar name={botName(bot)} peerId={bot.tg_bot_id} src={bot.has_avatar ? avatarUrl('bots', bot.tg_bot_id) : null} size="jumbo" />
          <div class="BotProfile-name">{botName(bot)}</div>
          <div class="BotProfile-status">
            <StatusDot status={bot.status} />
            {BOT_STATUS[bot.status] ?? bot.status}
            {bot.username && ` · @${bot.username}`}
          </div>
          {bot.status === 'error' && bot.last_error && <div class="BotProfile-error">{bot.last_error}</div>}
        </div>
        <ListItem
          title="接收消息"
          subtitle="关闭后停止拉取新消息，不会从 Telegram 登出"
          right={<Switch checked={bot.enabled} label="接收消息" onChange={(v) => void toggleEnabled(v)} />}
        />
      </Section>

      <Section title="白名单">
        <Description>只有白名单中的用户发给此机器人的消息才会存档。开启「代取」后，该用户发来的受保护消息链接会由用户账号代取。</Description>
        {whitelist.map((w) => (
          <div class="ListItem WhitelistRow" key={w.tg_user_id}>
            <span class="ListItem-text">
              <span class="ListItem-title">{w.note || `用户 ${w.tg_user_id}`}</span>
              <span class="ListItem-subtitle">ID {w.tg_user_id}</span>
            </span>
            <span class="WhitelistRow-fetch">
              代取
              <Switch checked={w.can_fetch} label={`允许 ${w.tg_user_id} 代取`} onChange={(v) => void put(w.tg_user_id, w.note, v)} />
            </span>
            <IconButton label={`编辑 ${w.tg_user_id}`} onClick={() => setEditing(w)}>
              <Pencil size={20} />
            </IconButton>
            <IconButton
              label={`移除 ${w.tg_user_id}`}
              onClick={async () => {
                try {
                  await store.api.deleteWhitelist(botId, w.tg_user_id);
                  await reload();
                } catch (e) {
                  store.showToast(errorMessage(e));
                }
              }}
            >
              <Trash2 size={20} />
            </IconButton>
          </div>
        ))}
        {whitelist.length === 0 && <Description>白名单为空，所有消息都会被拒绝。</Description>}
        <form class="settings-form" onSubmit={(e) => void add(e)}>
          <InputField label="用户 ID" value={uid} onInput={setUid} inputMode="numeric" error={formError} />
          <InputField label="备注（可选）" value={note} onInput={setNote} />
          <Checkbox label="允许代取受保护内容" checked={canFetch} onChange={setCanFetch} />
          <Button type="submit" loading={busy}>
            加入白名单
          </Button>
        </form>
      </Section>

      <Section title="最近被拒绝">
        {rejected.filter((r) => !listed.has(r.tg_user_id)).length === 0 && <Description>暂无记录</Description>}
        {rejected
          .filter((r) => !listed.has(r.tg_user_id))
          .map((r) => (
            <div class="ListItem RejectedRow" key={r.tg_user_id}>
              <span class="ListItem-text">
                <span class="ListItem-title">{r.first_name || (r.username ? `@${r.username}` : `用户 ${r.tg_user_id}`)}</span>
                <span class="ListItem-subtitle">
                  ID {r.tg_user_id}
                  {r.username && ` · @${r.username}`} · {r.count} 次 · {formatListTime(r.last_seen_at)}
                </span>
              </span>
              <IconButton label={`将 ${r.tg_user_id} 加入白名单`} onClick={() => void put(r.tg_user_id, r.first_name || r.username, false)}>
                <UserPlus size={20} />
              </IconButton>
            </div>
          ))}
      </Section>

      <Section>
        <ListItem icon={<Trash2 size={24} />} title="删除机器人" danger onClick={() => setConfirmDelete(true)} />
      </Section>

      {editing && (
        <EditEntry
          entry={editing}
          onClose={() => setEditing(null)}
          onSave={async (n, f) => {
            if (await put(editing.tg_user_id, n, f)) setEditing(null);
          }}
        />
      )}
      {confirmDelete && (
        <ConfirmDialog
          title="删除机器人"
          text={`停止 ${botName(bot)} 的存档。默认保留已存档的消息。`}
          confirmLabel="删除"
          danger
          busy={busy}
          onConfirm={() => void remove()}
          onClose={() => setConfirmDelete(false)}
        >
          <Checkbox label="同时删除该机器人的全部存档" checked={purge} onChange={setPurge} />
        </ConfirmDialog>
      )}
    </SettingsShell>
  );
}
```


`web/src/components/settings/TelegramAppSettings.tsx`：

```tsx
import { useEffect, useState } from 'preact/hooks';
import { errorMessage } from '../../api/client';
import type { TelegramApp } from '../../api/types';
import { useStore } from '../../state/store';
import { Button } from '../../ui/Button';
import { InputField } from '../../ui/InputField';
import { ListItem } from '../../ui/ListItem';
import { Description, SERVER_STATE, Section, SettingsShell } from './common';

const HASH_RE = /^[0-9a-fA-F]{32}$/;

export function TelegramAppSettings() {
  const store = useStore();
  const [app, setApp] = useState<TelegramApp | null>(null);
  const [apiId, setApiId] = useState('');
  const [apiHash, setApiHash] = useState('');
  const [errors, setErrors] = useState<{ id?: string; hash?: string }>({});
  const [busy, setBusy] = useState(false);

  const load = async () => {
    try {
      const a = await store.api.telegramApp();
      setApp(a);
      if (a.configured) setApiId(String(a.api_id));
    } catch (e) {
      store.showToast(errorMessage(e));
    }
  };

  useEffect(() => {
    void load();
  }, []);

  const save = async (e: Event) => {
    e.preventDefault();
    const next: { id?: string; hash?: string } = {};
    const id = Number(apiId.trim());
    if (!/^[1-9][0-9]{0,9}$/.test(apiId.trim()) || id > 2147483647) next.id = 'api_id 必须是正整数';
    if (!HASH_RE.test(apiHash.trim())) next.hash = 'api_hash 必须是 32 位十六进制字符串';
    setErrors(next);
    if (next.id || next.hash) return;
    setBusy(true);
    try {
      await store.api.saveTelegramApp(id, apiHash.trim());
      setApiHash('');
      store.showToast('已保存');
      await load();
    } catch (err) {
      setErrors({ hash: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  };

  let server = '加载中…';
  if (app) {
    if (!app.server.managed) server = '外部 Bot API（不由本服务托管）';
    else server = app.server.error ? `${SERVER_STATE[app.server.state] ?? app.server.state}：${app.server.error}` : SERVER_STATE[app.server.state] ?? app.server.state;
  }

  return (
    <SettingsShell title="API 凭据" back={{ name: 'settings' }}>
      <Section>
        <Description>
          在 my.telegram.org 申请的 api_id 与 api_hash，本地 Bot API 服务器与用户账号共用。api_hash 加密保存，之后不再显示；保存新凭据会重启 Bot API 服务器。
        </Description>
        <ListItem title="Bot API 服务器" subtitle={server} />
        <form class="settings-form" onSubmit={(e) => void save(e)}>
          <InputField label="api_id" value={apiId} onInput={setApiId} inputMode="numeric" error={errors.id} />
          <InputField
            label={app?.configured ? 'api_hash（已保存，修改时重新输入）' : 'api_hash'}
            value={apiHash}
            onInput={setApiHash}
            type="password"
            autoComplete="off"
            error={errors.hash}
          />
          <Button type="submit" loading={busy}>
            保存
          </Button>
        </form>
      </Section>
    </SettingsShell>
  );
}
```


`web/src/components/settings/UserbotSettings.tsx`：

```tsx
import { useEffect, useRef, useState } from 'preact/hooks';
import { ApiError, errorMessage } from '../../api/client';
import type { UserbotInfo } from '../../api/types';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Button } from '../../ui/Button';
import { InputField } from '../../ui/InputField';
import { ListItem } from '../../ui/ListItem';
import { ConfirmDialog } from '../../ui/Modal';
import { Spinner } from '../../ui/Spinner';
import { Description, Section, SettingsShell, USERBOT_STATE } from './common';

/** While connecting, re-read the state this often. */
export const USERBOT_POLL_MS = 3000;

export function UserbotSettings() {
  const store = useStore();
  const [info, setInfo] = useState<UserbotInfo | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [phone, setPhone] = useState('');
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [editPhone, setEditPhone] = useState(false);
  const [confirmLogout, setConfirmLogout] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const load = async () => {
    try {
      setInfo(await store.api.userbot());
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setUnavailable(true);
      else store.showToast(errorMessage(e));
    }
  };

  useEffect(() => {
    void load();
    return () => clearTimeout(timer.current);
  }, []);

  useEffect(() => {
    clearTimeout(timer.current);
    if (info?.state === 'connecting') timer.current = setTimeout(() => void load(), USERBOT_POLL_MS);
  }, [info]);

  const step = async (call: () => Promise<UserbotInfo>, reset: () => void) => {
    setBusy(true);
    setError('');
    try {
      const next = await call();
      setInfo(next);
      setEditPhone(false);
      reset();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const logout = async () => {
    setBusy(true);
    try {
      await store.api.userbotLogout();
      setConfirmLogout(false);
      await load();
    } catch (e) {
      store.showToast(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const submitPhone = (e: Event) => {
    e.preventDefault();
    void step(() => store.api.userbotPhone(phone), () => setPhone(''));
  };
  const submitCode = (e: Event) => {
    e.preventDefault();
    void step(() => store.api.userbotCode(code), () => setCode(''));
  };
  const submitPassword = (e: Event) => {
    e.preventDefault();
    void step(() => store.api.userbotPassword(password), () => setPassword(''));
  };

  const state = info?.state;
  const showPhone = state === 'logged_out' || state === 'error' || editPhone;

  return (
    <SettingsShell title="用户账号" back={{ name: 'settings' }}>
      <Section>
        <Description>
          用户账号用于代取受保护群组 / 频道的消息（白名单中开启「代取」的用户发送消息链接时）。userbot 违反 Telegram 使用条款，账号存在受限风险；登录状态等同该账号的完整权限。
        </Description>
        {unavailable && <Description>当前服务未启用用户账号功能。</Description>}
        {!info && !unavailable && (
          <div class="settings-loading">
            <Spinner size={28} />
          </div>
        )}
        {info && (
          <ListItem
            title="状态"
            subtitle={
              state === 'ready'
                ? `${info.name || '已登录'}${info.phone ? ` · ${info.phone}` : ''}${info.tg_user_id ? ` · ID ${info.tg_user_id}` : ''}`
                : state === 'error' && info.error
                  ? `错误：${info.error}`
                  : USERBOT_STATE[info.state] ?? info.state
            }
            right={state === 'connecting' ? <Spinner size={20} /> : undefined}
          />
        )}
      </Section>

      {state === 'unconfigured' && (
        <Section>
          <Description>请先在「API 凭据」中填写 api_id 与 api_hash。</Description>
          <div class="settings-form">
            <Button variant="secondary" onClick={() => navigate({ name: 'settings-telegram-app' })}>
              填写 API 凭据
            </Button>
          </div>
        </Section>
      )}

      {showPhone && (
        <Section title="登录">
          <form class="settings-form" onSubmit={submitPhone}>
            <InputField label="手机号（含国家代码）" value={phone} onInput={setPhone} type="tel" inputMode="tel" autoComplete="tel" error={error} />
            <Button type="submit" loading={busy}>
              发送验证码
            </Button>
          </form>
        </Section>
      )}

      {state === 'code_sent' && !editPhone && (
        <Section title="验证码">
          <Description>验证码已发送到 {info?.phone || '该账号'} 的 Telegram 客户端。</Description>
          <form class="settings-form" onSubmit={submitCode}>
            <InputField label="验证码" value={code} onInput={setCode} inputMode="numeric" autoComplete="one-time-code" error={error} />
            <Button type="submit" loading={busy}>
              下一步
            </Button>
            <Button
              variant="secondary"
              onClick={() => {
                setEditPhone(true);
                setError('');
              }}
            >
              重新输入手机号
            </Button>
          </form>
        </Section>
      )}

      {state === 'password_needed' && !editPhone && (
        <Section title="二步验证">
          <Description>该账号开启了二步验证，请输入密码。</Description>
          <form class="settings-form" onSubmit={submitPassword}>
            <InputField label="密码" value={password} onInput={setPassword} type="password" autoComplete="current-password" error={error} />
            <Button type="submit" loading={busy}>
              登录
            </Button>
          </form>
        </Section>
      )}

      {state === 'ready' && (
        <Section>
          <div class="settings-form">
            <Button variant="danger" onClick={() => setConfirmLogout(true)}>
              登出
            </Button>
          </div>
        </Section>
      )}

      {confirmLogout && (
        <ConfirmDialog
          title="登出用户账号"
          text="登出后会清除保存的登录状态，代取功能将不可用，直到重新登录。"
          confirmLabel="登出"
          danger
          busy={busy}
          onConfirm={() => void logout()}
          onClose={() => setConfirmLogout(false)}
        />
      )}
    </SettingsShell>
  );
}
```


`web/src/components/settings/SettingsPanel.tsx`：

```tsx
import type { Route } from '../../lib/router';
import { AddBot } from './AddBot';
import { BotSettings } from './BotSettings';
import { SettingsHome } from './SettingsHome';
import { TelegramAppSettings } from './TelegramAppSettings';
import { UserbotSettings } from './UserbotSettings';

/** Admin pages rendered inside the left column, like Web A's settings. */
export function SettingsPanel({ route }: { route: Route }) {
  switch (route.name) {
    case 'settings-add-bot':
      return <AddBot />;
    case 'settings-bot':
      return <BotSettings key={route.botId} botId={route.botId} />;
    case 'settings-telegram-app':
      return <TelegramAppSettings />;
    case 'settings-userbot':
      return <UserbotSettings />;
    default:
      return <SettingsHome />;
  }
}
```


`web/src/components/settings/settings.scss`：

```scss
.SettingsPanel {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.settings-header {
  gap: 1.375rem;
  padding: 0.5rem 0.75rem;
}

.settings-header-title {
  font-size: 1.25rem;
  font-weight: var(--font-weight-medium);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.settings-content {
  flex: 1;
  min-height: 0;
  background: var(--color-background-secondary);
}

.settings-section {
  padding: 0.5rem 0.5rem 1rem;
  background: var(--color-background);
  border-bottom: 0.625rem solid var(--color-background-secondary);
  box-shadow: inset 0 -1px 0 var(--color-background-secondary-accent);

  &:last-child {
    border-bottom: none;
    box-shadow: none;
  }
}

.settings-section-title {
  margin: 0;
  padding: 0.5rem 1rem;
  padding-inline-start: 1rem;
  font-size: 1rem;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-secondary);
}

.settings-description {
  margin: 0.5rem 1rem;
  font-size: 0.875rem;
  line-height: 1.3125;
  color: var(--color-text-secondary);
}

.settings-form {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 1rem 0.5rem 0;

  .InputField {
    margin-bottom: 1rem;
  }
}

.settings-loading {
  display: flex;
  justify-content: center;
  padding: 1rem;
  color: var(--color-text-secondary);
}

.StatusDot {
  display: inline-block;
  flex-shrink: 0;
  width: 0.5rem;
  height: 0.5rem;
  margin-right: 0.375rem;
  border-radius: 50%;
  vertical-align: middle;
  background: var(--color-gray);

  &.status-running {
    background: var(--color-green);
  }

  &.status-error {
    background: var(--color-error);
  }
}

.ListItem-subtitle.error {
  color: var(--color-error);
}

.BotRow {
  gap: 1rem;
  padding: 0 1rem 0 0;

  .BotRow-main {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 1rem;
    min-height: 3.5rem;
    padding: 0.5rem 0 0.5rem 1rem;
    border-radius: var(--border-radius-default);
    text-align: start;
  }

  &:hover {
    background: var(--color-chat-hover);
  }
}

.WhitelistRow,
.RejectedRow {
  gap: 0.5rem;
  padding-right: 0.5rem;
}

.WhitelistRow-fetch {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-right: 0.5rem;
  font-size: 0.875rem;
  color: var(--color-text-secondary);
}

.BotProfile {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.25rem;
  padding: 1rem 1rem 0.75rem;
  text-align: center;

  .BotProfile-name {
    margin-top: 0.5rem;
    font-size: 1.25rem;
    font-weight: var(--font-weight-medium);
  }

  .BotProfile-status {
    font-size: 0.875rem;
    color: var(--color-text-secondary);
  }

  .BotProfile-error {
    font-size: 0.875rem;
    color: var(--color-error);
    word-break: break-word;
  }
}

.AddBot-steps {
  list-style: none;
  margin: 1rem 0 0.5rem;
  padding: 0 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;

  li {
    display: flex;
    gap: 0.75rem;
  }

  .AddBot-step-icon {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 1.5rem;
    height: 1.5rem;
    border-radius: 50%;
    color: #fff;
  }

  .ok .AddBot-step-icon {
    background: var(--color-green);
  }

  .failed .AddBot-step-icon {
    background: var(--color-error);
  }

  .AddBot-step-text {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .AddBot-step-detail {
    font-size: 0.875rem;
    color: var(--color-text-secondary);
    word-break: break-word;
  }
}
```


- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/components/settings
git commit -m "feat(web): admin pages for bots, whitelist, rejected senders, API credentials and userbot login"
```

---

### Task 14: 应用外壳、响应式三栏、SSE 接线与上线前验证

**Files:**
- Modify（整体替换）: `web/src/App.tsx`、`web/src/main.tsx`、`web/src/App.test.tsx`
- Create: `web/src/layout.scss`
- Modify: `docs/superpowers/specs/2026-10-04-tgarchive-design.md`（§7 表格、§8「基准」段落）

**Interfaces:**
- Consumes: 前面所有任务的组件；Task 4 `startRouter`、`route`、`isSettings`、`connectEvents`、`EventSourceFactory`；Task 5 `createStore`、`StoreContext`、`Store`
- Produces: `App({ store, eventSource? })`：挂载时启动路由、加载机器人与会话、连接 `/api/events`（事件交给 `store.handleEvent`，重连后 `store.resync()`），卸载时关闭；`#Main` 类 `left-column-open`（无打开会话时）/ `right-column-open`（共享媒体打开时）；`#LeftColumn` 内按路由显示 `ChatsPanel` 或 `SettingsPanel`；`#RightColumn` 内 `SharedMedia`；全局 `MediaViewer` 与 `Toast`

- [ ] **Step 1: 写失败测试**

`web/src/App.test.tsx` 整体替换为：

```tsx
import { act, fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App } from './App';
import type { EventSourceLike } from './lib/sse';
import { route } from './lib/router';
import { createStore } from './state/store';
import { fakeApi, makeBot, makeChat, makeMessage } from './test/fixtures';

class FakeES implements EventSourceLike {
  readyState = 1;
  onopen: ((ev: Event) => unknown) | null = null;
  onerror: ((ev: Event) => unknown) | null = null;
  listeners = new Map<string, (ev: MessageEvent) => void>();
  closed = false;
  addEventListener(type: string, l: (ev: MessageEvent) => void) {
    this.listeners.set(type, l);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, data: unknown) {
    this.listeners.get(type)?.(new MessageEvent(type, { data: JSON.stringify(data) }));
  }
}

afterEach(() => {
  history.replaceState(null, '', '/');
  route.value = { name: 'home' };
});

function setup(path = '/') {
  history.replaceState(null, '', path);
  let es!: FakeES;
  const api = fakeApi({
    bots: vi.fn(async () => [makeBot({ id: 1 })]),
    chats: vi.fn(async () => [makeChat({ id: 10 })]),
    messages: vi.fn(async () => [makeMessage({ id: 1, text: 'archived' })]),
    message: vi.fn(async (id: number) => makeMessage({ id, text: 'live!' })),
  });
  const store = createStore(api, { chatsReloadDelay: 0 });
  const utils = render(
    <App
      store={store}
      eventSource={() => {
        es = new FakeES();
        return es;
      }}
    />,
  );
  return { ...utils, api, store, es: () => es };
}

describe('App', () => {
  it('starts on the chat list with the left column open', async () => {
    const { container } = setup('/');
    expect(await screen.findByText('Alice')).toBeTruthy();
    expect(container.querySelector('#Main')!.className).toBe('left-column-open');
    expect(screen.getByText('选择一个会话开始浏览存档')).toBeTruthy();
  });

  it('deep-links into a chat and appends live messages from SSE', async () => {
    const { container, es, api } = setup('/chat/10');
    expect(await screen.findByText('archived')).toBeTruthy();
    expect(container.querySelector('#Main')!.className).toBe('');
    await act(async () => {
      es().emit('message.created', { chat_id: 10, message_id: 2 });
    });
    expect(await screen.findByText('live!')).toBeTruthy();
    expect(api.message).toHaveBeenCalledWith(2);
  });

  it('resyncs after the event stream reconnects', async () => {
    const { es, api } = setup('/');
    await screen.findByText('Alice');
    await act(async () => {
      es().onerror?.(new Event('error'));
      es().onopen?.(new Event('open'));
    });
    expect(api.bots).toHaveBeenCalledTimes(2);
    expect(api.chats).toHaveBeenCalledTimes(2);
  });

  it('opens settings in the left column and the shared media panel on the right', async () => {
    const { container, store } = setup('/chat/10');
    await screen.findByText('archived');
    fireEvent.click(screen.getByRole('button', { name: '共享媒体' }));
    expect(container.querySelector('#Main')!.className).toBe('right-column-open');
    expect(store.sharedMediaOpen.value).toBe(true);
    expect(await screen.findByText('暂无媒体')).toBeTruthy();
    await act(async () => {
      history.pushState(null, '', '/settings');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(await screen.findByText('添加机器人')).toBeTruthy();
    expect(container.querySelector('#RightColumn')!.getAttribute('aria-hidden')).toBe('true');
  });

  it('closes the event stream on unmount', async () => {
    const { unmount, es } = setup('/');
    await screen.findByText('Alice');
    unmount();
    // Effect cleanups may run after the unmount call returns.
    await waitFor(() => expect(es().closed).toBe(true));
  });
});
```

- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/App.test.tsx`
Expected: FAIL（`App` 不接受 `store` / 找不到「Alice」等）

- [ ] **Step 3: 实现**

`web/src/App.tsx` 整体替换为：

```tsx
import { useEffect } from 'preact/hooks';
import { ChatsPanel } from './components/left/ChatsPanel';
import { MiddleColumn } from './components/middle/MiddleColumn';
import { SharedMedia } from './components/right/SharedMedia';
import { SettingsPanel } from './components/settings/SettingsPanel';
import { MediaViewer } from './components/viewer/MediaViewer';
import { isSettings, route, startRouter } from './lib/router';
import { connectEvents, type EventSourceFactory } from './lib/sse';
import { StoreContext, type Store } from './state/store';
import { Toast } from './ui/Toast';
import './layout.scss';

interface Props {
  store: Store;
  /** Injected in tests; defaults to the browser EventSource. */
  eventSource?: EventSourceFactory;
}

export function App({ store, eventSource }: Props) {
  useEffect(() => {
    const stopRouter = startRouter();
    void store.loadBots();
    void store.loadChats();
    const stopEvents = connectEvents(
      (ev) => void store.handleEvent(ev),
      () => void store.resync(),
      eventSource,
    );
    return () => {
      stopRouter();
      stopEvents();
    };
  }, []);

  const r = route.value;
  const chatId = r.name === 'chat' ? r.chatId : 0;
  const rightOpen = chatId > 0 && store.sharedMediaOpen.value;
  const cls = [!chatId && 'left-column-open', rightOpen && 'right-column-open'].filter(Boolean).join(' ');

  return (
    <StoreContext.Provider value={store}>
      <div id="Main" class={cls}>
        <div id="LeftColumn">{isSettings(r) ? <SettingsPanel route={r} /> : <ChatsPanel />}</div>
        <MiddleColumn chatId={chatId} />
        <div id="RightColumn" aria-hidden={!rightOpen}>
          {rightOpen && <SharedMedia chatId={chatId} />}
        </div>
      </div>
      <MediaViewer />
      <Toast />
    </StoreContext.Provider>
  );
}
```

`web/src/main.tsx` 整体替换为：

```tsx
import { render } from 'preact';
import { api } from './api/client';
import { App } from './App';
import { createStore } from './state/store';
import './styles/index.scss';

const ua = navigator.userAgent;
const ios = /iPhone|iPad/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1);
if (/Macintosh|iPhone|iPad/.test(ua)) document.documentElement.classList.add('is-apple');
if (ios) document.documentElement.classList.add('is-ios');

render(<App store={createStore(api)} />, document.getElementById('app')!);
```

`web/src/layout.scss`：

```scss
// Three floating islands (left list, middle chat, right shared media); single column below 926px.

#Main {
  position: relative;
  display: flex;
  height: 100%;
  overflow: hidden;
  background: var(--color-background-secondary);
}

#LeftColumn {
  position: relative;
  z-index: 2;
  flex-shrink: 0;
  width: 33vw;
  min-width: 16rem;
  max-width: 26.5rem;
  margin: 1rem 0 1rem 1rem;
  border-radius: var(--border-radius-island);
  background: var(--color-background);
  box-shadow: var(--shadow-island);
  overflow: hidden;
}

@media (min-width: 926px) {
  #LeftColumn {
    max-width: 40vw;
  }
}

@media (min-width: 1276px) {
  #LeftColumn {
    width: 25vw;
    max-width: 33vw;
  }
}

#MiddleColumn {
  z-index: 1;
}

#RightColumn {
  position: absolute;
  top: 1rem;
  right: 1rem;
  bottom: 1rem;
  z-index: 4;
  width: calc(var(--right-column-width) - 1rem);
  border-radius: var(--border-radius-island);
  background: var(--color-background-secondary);
  box-shadow: var(--shadow-island);
  overflow: hidden;
  transform: translate3d(calc(100% + 1rem), 0, 0);
  transition: transform var(--layer-transition);
}

#Main.right-column-open #RightColumn {
  transform: translate3d(0, 0, 0);
}

@media (min-width: 1276px) {
  #MiddleColumn {
    transition: margin-right var(--layer-transition);
  }

  #Main.right-column-open #MiddleColumn {
    margin-right: var(--right-column-width);
  }
}

@media (max-width: 925px) {
  #LeftColumn {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    z-index: 3;
    width: 26.5rem;
    max-width: calc(100vw - 2rem);
    margin: 1rem;
    transform: translate3d(calc(-100% - 1rem), 0, 0);
    transition: transform var(--layer-transition);
  }

  #Main.left-column-open #LeftColumn {
    transform: translate3d(0, 0, 0);
  }
}

@media (max-width: 600px) {
  #LeftColumn {
    z-index: 2;
    width: 100vw;
    max-width: 100vw;
    margin: 0;
    border-radius: 0;
    box-shadow: none;
    transform: translate3d(-20vw, 0, 0);
  }

  #MiddleColumn {
    position: absolute;
    inset: 0;
    z-index: 3;
    transform: translate3d(0, 0, 0);
    transition: transform var(--layer-transition);
  }

  #Main.left-column-open #MiddleColumn {
    transform: translate3d(100vw, 0, 0);
  }

  #Main.right-column-open #MiddleColumn {
    transform: translate3d(-20vw, 0, 0);
  }

  #RightColumn {
    inset: 0;
    width: 100vw;
    border-radius: 0;
    box-shadow: none;
    transform: translate3d(100vw, 0, 0);
  }
}

@media (prefers-reduced-motion: reduce) {
  #LeftColumn,
  #MiddleColumn,
  #RightColumn {
    transition: none !important;
  }
}
```


- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test && npm run build && ls dist/assets && cd .. && go test ./...`
Expected: 全部 PASS；`dist/assets/` 下有 `index-*.js`、`index-*.css` 与独立的 `lottie_light-*.js` 分块

- [ ] **Step 5: 用真实二进制冒烟**

```bash
go build -o /tmp/tga ./cmd/tgarchive
D=$(mktemp -d)
TOKEN_ENC_KEY=$(openssl rand -hex 32) DATA_DIR=$D BOT_API_MANAGED=false BOT_API_URL=http://127.0.0.1:1 \
  REQUIRE_FORWARD_AUTH=false LISTEN=127.0.0.1:18089 /tmp/tga &
sleep 2
curl -s -o /dev/null -w '%{http_code} %{content_type}\n' http://127.0.0.1:18089/
curl -s http://127.0.0.1:18089/chat/5 | grep -c '<div id="app">'
curl -s -o /dev/null -w '%{http_code} %{content_type}\n' http://127.0.0.1:18089/pattern.svg
curl -s http://127.0.0.1:18089/api/messages/1
kill %1
```

Expected：`200 text/html; charset=utf-8`、`1`、`200 image/svg+xml`、`{"error":"not found"}`

- [ ] **Step 6: 人工视觉核对（与 web.telegram.org/a 同视口并排）**

`cd web && npm run dev`，后端以 Step 5 的方式在 8080 端口运行（`LISTEN=127.0.0.1:8080`）并接入至少一个真实机器人（或在已有数据目录上运行），在 1440×900、800×900、390×844 三个视口、浅 / 深两种系统主题下逐项核对：左栏岛形圆角与阴影、会话行高度与选中蓝（深色紫）、日期胶囊、气泡圆角与尾巴、连续消息圆角收窄、时间与「已编辑」位置、转发 / 来源 / 回复样式、相册拼图、文件图标折角与颜色、语音波形、贴纸无气泡、圆形视频、查看器、共享媒体网格、设置页灰色间隔卡片与开关、窄屏切换动画。发现的差异记入 `docs/superpowers/plans/2026-10-04-tgarchive-p3-carryover.md`（新建），不在本任务内修改。

- [ ] **Step 7: 同步 spec**

对 `docs/superpowers/specs/2026-10-04-tgarchive-design.md` 做以下精确替换：

1. §7 表格中找到这一行：

```
| DELETE | `/api/messages/:id` | 删除存档（软删 + 清理孤立媒体） |
```

在它之前插入：

```
| GET | `/api/messages/:id` | 单条消息（含媒体、回复预览与 `chat_id`），SSE 增量更新用；已删除 404 |
```

2. §8「基准」小节中的这段原文：

```
前端使用 Preact + Vite + SCSS。从 Web A 源码移植：SCSS 变量与主题（浅/深）、组件样式与 DOM/class 结构、图标字体、默认壁纸图案、相册拼图算法、动画曲线与时长、日期/时间格式化规则。移植代码在源文件头注明来源路径与上游 commit。
```

整体替换为：

```
前端使用 Preact + Vite + SCSS，按调研文档（`.superpowers/research/webA-design.md`）记录的视觉数值（配色、尺寸、圆角、动画曲线与时长、断点、日期格式）从零实现，不复制 telegram-tt 的源码、样式或资产；图标使用 Lucide，默认壁纸图案为本仓库原创；相册拼图按 Web A 的行为（2–4 张固定模板、5 张以上按行打包）自行实现。
```

- [ ] **Step 8: 提交**

```bash
git add web/src/App.tsx web/src/App.test.tsx web/src/main.tsx web/src/layout.scss docs/superpowers/specs/2026-10-04-tgarchive-design.md
git commit -m "feat(web): responsive three-column shell with live updates; sync spec"
```

