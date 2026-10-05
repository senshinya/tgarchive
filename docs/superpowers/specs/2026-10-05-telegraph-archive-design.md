# Telegraph 文章离线存档 设计

> 状态：已确认（2026-10-05）。上位 spec：`2026-10-04-tgarchive-design.md`；本文只描述新增部分，与上位 spec 冲突处以本文为准。

## 1. 目标

白名单用户发给机器人的消息**整条就是**一个 Telegraph 文章链接时，自动把文章全部内容离线存档；WebUI 中该消息下方显示文章卡片，点开为仿 Telegram Instant View 的全屏阅读页。

### 成功标准
- 发 `https://telegra.ph/<path>`（或 `graph.org`）→ 链接消息正常入档并收到 👀；文章正文与其中全部图片 / 视频（含外站）落盘后变 👌
- 断网 / 原文删除后，WebUI 仍能完整阅读文章（文字、格式、图片、视频）
- 文章抓取失败（不存在、反复网络错误）→ 机器人回复 `⚠️ 存档失败：<原因>`，链接消息本身仍在档
- 移动端（≤600px）阅读页可用：全屏、可返回（含系统返回键）、图片可放大

### 明确不做
- iframe 嵌入（YouTube / Vimeo / Twitter 等）离线化：只保留标题与原链接
- 文章更新跟踪：每次发送都是一份独立快照，旧快照保留
- 非「整条消息即链接」的场景（夹在文字里、转发消息中的链接）不触发
- 不经过 userbot、不受「代取」开关约束

## 2. 链接识别（`internal/telegraph`）

`Candidate(text string) (path string, ok bool)`：
- 去首尾空白后不得含空白字符
- scheme 可省略，`http`/`https` 均可；host 为 `telegra.ph` 或 `graph.org`（大小写不敏感，可带 `www.`）
- path 恰好一段、非空、不是 `file`/`api`/`edit` 等保留段；忽略 query 与 fragment
- 返回不带前导 `/` 的 path（URL 解码后原样）

识别顺序：现有 t.me 代取的 `LinkHandler` 先判断；未被消费的消息照常 `Ingest`；**新建**消息（`ir.Created`）若 `Candidate(text)` 成立，则创建 Telegraph 任务。

## 3. 数据模型（迁移 `0004_telegraph.sql`）

```sql
CREATE TABLE telegraph_jobs (
  id          INTEGER PRIMARY KEY,
  message_id  INTEGER NOT NULL UNIQUE REFERENCES messages(id) ON DELETE CASCADE,
  path        TEXT NOT NULL,
  state       TEXT NOT NULL CHECK (state IN ('queued','fetching','fetched','failed')),
  attempts    INTEGER NOT NULL DEFAULT 0,
  error       TEXT NOT NULL DEFAULT '',
  receipt     TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE TABLE articles (
  message_id     INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
  path           TEXT NOT NULL,
  url            TEXT NOT NULL,
  title          TEXT NOT NULL,
  description    TEXT NOT NULL DEFAULT '',
  author_name    TEXT NOT NULL DEFAULT '',
  author_url     TEXT NOT NULL DEFAULT '',
  image_media_id INTEGER,               -- 封面（og image）对应的 media 行，可空
  views          INTEGER NOT NULL DEFAULT 0,
  content        TEXT NOT NULL,         -- 规范化后的节点树 JSON（见 §5）
  fetched_at     INTEGER NOT NULL
);
```

文章中的图片 / 视频 / 封面各建一条 `media` 行：`message_id` = 链接消息，`role = 'article'`，`kind` 为 `photo` / `video`，`dedupe_key = 'web:' || hex(sha256(原始绝对 URL))`，路径 `media/web/yyyy/mm/<sha1>.<ext>`，受 `MEDIA_MAX_BYTES` 约束（超限 → `too_large`）。

- 共享媒体、消息气泡的主媒体、`/api/chats/{id}/media` 均**排除** `role = 'article'`
- 删除消息（软删 + 清理孤立媒体）连带删除 `articles`、`telegraph_jobs` 与其 media 文件

## 4. 抓取 worker（`internal/telegraph`）

串行队列，与 userbot `Fetcher` 互不相干：
1. 启动时把 `fetching` 重置为 `queued`；被唤醒或每 30s 轮询一次领取 `queued` 任务
2. `GET {TELEGRAPH_API_URL}/getPage/<path>?return_content=true`（默认 `https://api.telegra.ph`，环境变量仅用于测试），超时 30s
3. 结果：
   - `ok:true` → 规范化节点树（§5）、写 `articles`、建 media 行并交给下载器、任务 `fetched`、发 `message.updated`
   - `ok:false`（如 `PAGE_NOT_FOUND`）→ `failed`，原因「文章不存在」
   - 网络错误 / 5xx / 非 JSON → `attempts+1`，按 10s、60s、300s 退避，第 4 次仍失败 → `failed`，原因「网络错误」
4. 下载器新数据源 `web`（`dedupe_key` 前缀 `web:`）：HTTP GET 原始 URL，`User-Agent: tgarchive/1.0`，最多 5 次重定向，单请求超时 5 min，流式写 `.part` 后 rename；**SSRF 防护**：拨号时解析出的地址若为回环、私网、链路本地、组播、未指定地址或 `100.64.0.0/10`，拒绝（媒体 `failed`，原因「地址不允许」）；只允许 `http`/`https`

## 5. 节点树规范化

Telegraph Node = 字符串 | `{tag, attrs?, children?}`。存储前：
- 只保留标签：`a aside b blockquote br code em figcaption figure h3 h4 hr i iframe img li ol p pre s strong u ul video`；其他标签展开为其子节点
- 只保留属性：`a.href`、`img.src`、`video.src`、`iframe.src`
- 相对 URL（`/file/...`）以 `https://telegra.ph` 补全
- `img`/`video` 的 `src` 改写为 `data-media-id`（media 行 id），同时保留 `data-src`（原始绝对 URL，下载失败时 UI 显示占位 + 原链接）
- `iframe` 改写为 `{tag:"embed", attrs:{href:<原始页面 URL 推断>, src}}`：YouTube `embed/<id>` → `https://www.youtube.com/watch?v=<id>`，Vimeo、Twitter 同理，未知来源保留 `src`

## 6. 回执

链接消息的回执纳入现有 `receipt.Engine`：
- 任务 `queued`/`fetching`，或任务 `fetched` 但文章 media 尚有 `pending` → 👀
- 任务 `fetched` 且文章 media 全部落定（`done`/`failed`/`too_large`）→ 👌（与普通媒体规则一致：失败的单张图不阻止 👌）
- 任务 `failed` → 回复 `⚠️ 存档失败：<原因>`，保持 👀 不变为 👌

## 7. HTTP 接口

- `MessageView` 增加可选字段 `article`：`{state: "queued"|"fetching"|"fetched"|"failed", error, title, description, author_name, image_media_id, url}`（无任务时省略）
- `GET /api/messages/{id}/article` → `{url, title, description, author_name, author_url, views, fetched_at, content, media: [{id, kind, state, width, height, duration, mime}]}`；无文章 404
- SSE：任务状态变化发 `message.updated`；文章媒体下载完成沿用 `media.updated`

## 8. WebUI

- **文章卡片**：链接消息气泡内，文本下方；样式仿 Telegram 链接预览（左侧 2px 强调色竖线、站点名「Telegraph」、标题加粗、描述两行截断、封面图在下方圆角）。`queued`/`fetching` 显示「正在存档文章…」；`failed` 显示错误与原链接
- **阅读页**：点卡片打开；全屏覆盖（桌面在中栏区域上覆盖，居中正文列最大宽 732px；≤600px 全屏）。顶栏：返回、标题、「在 Telegraph 打开」原链接按钮。正文排版仿 Instant View：标题 26px 粗体、作者与日期、正文 17px 行高 1.6、h3/h4、引用（左竖线）、`aside` 大号引语、列表、`pre` 等宽、`figure` 图注居中灰色、`hr`、embed 卡片（图标 + 来源 + 原链接）
- 图片点击打开现有媒体查看器，查看器内可在**本文**的图片 / 视频之间切换
- 打开阅读页 push 一条 history 记录，系统返回键关闭阅读页
- 链接一律经 `safeHref`；不使用 `innerHTML`

## 9. 错误处理汇总

| 情况 | 行为 |
|---|---|
| 文章不存在 | 任务 `failed`「文章不存在」，回复 ⚠️ |
| 网络 / 5xx 连续 4 次 | 任务 `failed`「网络错误」，回复 ⚠️ |
| 单张图片下载失败 | 该 media `failed`，阅读页显示占位 + 原链接 + 重试按钮（复用 `/api/media/{id}/retry`） |
| 图片超过 `MEDIA_MAX_BYTES` | `too_large`，占位 |
| 目标地址为私网等 | media `failed`「地址不允许」，不重试 |
| 服务重启 | `fetching` 任务回到 `queued` |

## 10. 测试

- `Candidate`：各种合法 / 非法 URL 表
- 规范化：标签白名单、属性白名单、相对 URL、iframe→embed、媒体改写
- worker：假 Telegraph 服务端覆盖成功、`PAGE_NOT_FOUND`、5xx 重试到失败、重启复位
- `web` 数据源：成功、重定向、超限、SSRF 拒绝（对 127.0.0.1 测试服务器须被拒，测试中通过可注入的拨号校验放行）
- 回执：👀 → 👌、失败回复
- 端到端（`internal/app`）：发链接 → 档案中有链接消息、`articles` 行、媒体落盘、👌
- 前端：卡片各状态、阅读页渲染每种节点、不安全链接降级、查看器在文章媒体间切换、返回键关闭
- 移动端验收：390×844 浅 / 深色、800px，阅读页与卡片
