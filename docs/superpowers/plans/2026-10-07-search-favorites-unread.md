# 搜索、收藏、频道未读、监听信息、链接预览 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 tgarchive 中加入全文搜索（含任意位置跳转）、收藏与标签、频道未读计数、监听列表运行信息、频道帖子链接预览与不支持类型占位，发布 v0.7.0。

**Architecture:** 后端一个 migration（v7）建 FTS5 trigram 索引（触发器维护）、收藏/标签表、`chats.last_read_id`、`channel_watches.last_polled_at`；store 层新增 Search / 收藏 / 已读 / 监听统计，消息分页加 `after`/`around`；HTTP 暴露新接口与 SSE 事件。前端会话状态支持「中间空洞」（hasNewer），左栏加搜索与收藏入口，新增收藏视图路由。

**Tech Stack:** Go 1.x + modernc.org/sqlite（SQLite 3.53，FTS5 trigram 可用）、gotd/td；Preact + @preact/signals + vitest + SCSS。

**Spec:** `docs/superpowers/specs/2026-10-07-search-favorites-unread-design.md`

## Global Constraints

- 迁移文件 `internal/store/migrations/0007_search_favorites.sql`，`PRAGMA user_version` 7
- Store 只有一个连接：持有 `*sql.Rows` 时不得再发查询；事务内只用 `tx`
- 搜索：最多 5 个词；`limit` 默认 30、最大 100；snippet 前后各 30 rune；`ranges` 用 UTF-16 偏移
- 标签名 1–32 字符、无换行、大小写不敏感去重；每条消息最多 10 个标签
- 未读只给 `kind='channel'` 会话；已读上报同一会话 1s 节流
- 监听停滞阈值：`now - last_polled_at > 3 × poll_seconds`；监听列表每 30s 重拉
- 所有 UI 文案中文；Web A 样式风格；移动端单栏可用
- 提交 trailer：`Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`

## Review Focus

1. 搜索词含 `%`、`_`、`\`、引号 → 按字面量匹配，不报错不全匹配（Task 2 测试覆盖）
2. snippet 截断落在 emoji / 代理对中间 → 按 rune 截断，ranges 按 UTF-16 正确对齐（Task 2 测试覆盖）
3. 跳转目标在相册中间 / 窗口边界 → around 窗口不截断相册，滚动后不重复不缺（Task 3、Task 9 测试覆盖）
4. hasNewer 期间收到新消息 SSE → 不在已载窗口外插入（不制造空洞），回到底部后能看到（Task 9 测试覆盖）
5. 收藏后再删除该消息 → 收藏视图与标签计数同步消失（Task 5 测试覆盖）

---

### Task 1: Migration v7 与搜索索引触发器

**Files:**
- Create: `internal/store/migrations/0007_search_favorites.sql`
- Create: `internal/store/search_test.go`
- Modify: `internal/store/store_test.go`（user_version 断言改 7）

**Interfaces:**
- Produces: 表 `search_fts(body, files, article)`（rowid = messages.id）、`favorites`、`tags`、`message_tags`；列 `chats.last_read_id`、`channel_watches.last_polled_at`

- [ ] **Step 1: 写失败测试** `search_test.go`：
  - `TestSearchIndexFollowsMessages`：Ingest 一条 text 消息 → `SELECT body FROM search_fts WHERE rowid=?` 等于正文；直接 `UPDATE messages SET text='新文本'` → 索引更新；`DeleteMessage` → 索引行消失；硬删 (`DELETE FROM messages`) 同样
  - `TestSearchIndexFiles`：Ingest 带 document 主媒体 `file_name='报告.pdf'` → `files` 列含 `报告.pdf`；photo 的 file_name 不入
  - `TestSearchIndexArticle`：对消息 `SaveArticle`（content `[{"tag":"p","children":["正文 A&B ",{"tag":"a","attrs":{"href":"https://x"},"children":["链接字"]}]}]`）→ `article` 列含 `标题`、`正文 A&B`、`链接字`，不含 `https://x`、`href`
  - `TestMigration7Backfill`：`migrate(ctx, 6)` 建 v6 库，插入消息与文章，再 `migrate(ctx, 0)` → 索引已回填；`chats.last_read_id` = 该会话最大消息 id
- [ ] **Step 2:** `go test ./internal/store -run 'SearchIndex|Migration7'` → FAIL（表不存在）
- [ ] **Step 3: 写 migration**

```sql
CREATE VIRTUAL TABLE search_fts USING fts5(body, files, article, tokenize = 'trigram');

CREATE TABLE favorites (
  id         INTEGER PRIMARY KEY,
  message_id INTEGER NOT NULL UNIQUE REFERENCES messages(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL
);
CREATE TABLE tags (
  id   INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE COLLATE NOCASE
);
CREATE TABLE message_tags (
  message_id INTEGER NOT NULL REFERENCES favorites(message_id) ON DELETE CASCADE,
  tag_id     INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (message_id, tag_id)
);
CREATE INDEX message_tags_tag ON message_tags(tag_id);

ALTER TABLE chats ADD COLUMN last_read_id INTEGER NOT NULL DEFAULT 0;
UPDATE chats SET last_read_id = COALESCE((SELECT MAX(id) FROM messages m WHERE m.chat_id = chats.id), 0);

ALTER TABLE channel_watches ADD COLUMN last_polled_at INTEGER NOT NULL DEFAULT 0;

-- Searchable file names: the main document/audio media of a message.
CREATE VIEW search_files AS
  SELECT mm.message_id, group_concat(md.file_name, ' ') AS names
  FROM message_media mm JOIN media md ON md.id = mm.media_id
  WHERE mm.role = 'main' AND md.kind IN ('document', 'audio') AND md.file_name != ''
  GROUP BY mm.message_id;

-- Searchable article text: title, description and the text nodes of the Telegraph content.
CREATE VIEW search_articles AS
  SELECT a.message_id, a.title || ' ' || a.description || ' ' ||
    COALESCE((SELECT group_concat(value, ' ') FROM json_tree(a.content) WHERE type = 'text' AND typeof(key) = 'integer'), '') AS text
  FROM articles a;

CREATE TRIGGER search_msg_ai AFTER INSERT ON messages WHEN NEW.deleted_at = 0 BEGIN
  INSERT INTO search_fts(rowid, body, files, article) VALUES (NEW.id, NEW.text,
    COALESCE((SELECT names FROM search_files WHERE message_id = NEW.id), ''),
    COALESCE((SELECT text FROM search_articles WHERE message_id = NEW.id), ''));
END;
CREATE TRIGGER search_msg_au AFTER UPDATE OF text, deleted_at ON messages BEGIN
  DELETE FROM search_fts WHERE rowid = OLD.id;
  INSERT INTO search_fts(rowid, body, files, article) SELECT NEW.id, NEW.text,
    COALESCE((SELECT names FROM search_files WHERE message_id = NEW.id), ''),
    COALESCE((SELECT text FROM search_articles WHERE message_id = NEW.id), '')
  WHERE NEW.deleted_at = 0;
END;
CREATE TRIGGER search_msg_ad AFTER DELETE ON messages BEGIN
  DELETE FROM search_fts WHERE rowid = OLD.id;
END;
CREATE TRIGGER search_mm_ai AFTER INSERT ON message_media BEGIN
  UPDATE search_fts SET files = COALESCE((SELECT names FROM search_files WHERE message_id = NEW.message_id), '')
  WHERE rowid = NEW.message_id;
END;
CREATE TRIGGER search_mm_ad AFTER DELETE ON message_media BEGIN
  UPDATE search_fts SET files = COALESCE((SELECT names FROM search_files WHERE message_id = OLD.message_id), '')
  WHERE rowid = OLD.message_id;
END;
CREATE TRIGGER search_art_ai AFTER INSERT ON articles BEGIN
  UPDATE search_fts SET article = COALESCE((SELECT text FROM search_articles WHERE message_id = NEW.message_id), '')
  WHERE rowid = NEW.message_id;
END;
CREATE TRIGGER search_art_au AFTER UPDATE ON articles BEGIN
  UPDATE search_fts SET article = COALESCE((SELECT text FROM search_articles WHERE message_id = NEW.message_id), '')
  WHERE rowid = NEW.message_id;
END;
CREATE TRIGGER search_art_ad AFTER DELETE ON articles BEGIN
  UPDATE search_fts SET article = '' WHERE rowid = OLD.message_id;
END;

INSERT INTO search_fts(rowid, body, files, article)
  SELECT m.id, m.text, COALESCE(f.names, ''), COALESCE(a.text, '')
  FROM messages m LEFT JOIN search_files f ON f.message_id = m.id LEFT JOIN search_articles a ON a.message_id = m.id
  WHERE m.deleted_at = 0;
```
  （`SaveArticle` 若用 `INSERT OR REPLACE`，REPLACE 先删后插会触发 ad + ai，结果一致。）
- [ ] **Step 4:** 测试通过；`go test ./internal/store` 全绿（store_test 改 7）
- [ ] **Step 5:** Commit `feat(store): schema v7 with a trigram search index kept by triggers`

### Task 2: Store.Search 与 snippet

**Files:**
- Create: `internal/store/search.go`
- Modify: `internal/store/search_test.go`

**Interfaces:**
- Produces:
```go
type SearchHit struct {
    Message MessageView `json:"message"`
    Field   string      `json:"field"`   // body / files / article
    Snippet string      `json:"snippet"`
    Ranges  [][2]int    `json:"ranges"`  // [start, len] in UTF-16 code units of Snippet
}
var ErrBadQuery = errors.New("bad search query")
func SearchTerms(q string) []string                       // split on whitespace, ≤5 terms, dedup
func (s *Store) Search(ctx context.Context, q string, convKey, beforeID int64, limit int) ([]SearchHit, error)
func makeSnippet(text string, terms []string) (string, [][2]int)
```
`convKey`: 0 全部、>0 chat id、<0 `-botId`（用现有 `chatScope`/`botScope`）。

- [ ] **Step 1: 测试**
  - `TestSearch`：正文分别为 `今天天气很好`、`Hello World 100%`、`a_b 测试`、`换行\n第二行`，另一会话 `天气预报`；
    - `天` → 3 条（1 字符）、`天气` → 2、`天气很` → 1；`hello` 命中 `Hello`（大小写不敏感）
    - `天气 预报` → 1（AND）；`100%` → 1；`%` → 1（只匹配含 `%` 的）；`a_b` → 1、`_` → 1
    - `convKey` 限定会话；合并时间线 `-botId`；`before` 分页（limit 1 逐页取完，id 递减不重复）
    - 软删除的不出现
  - `TestSearchFieldAndSnippet`：只在文件名命中 → `Field=="files"`；只在文章命中 → `"article"`；正文长 200 字命中在中间 → snippet 以 `…` 开头结尾、长度 ≤ 61 rune + 省略号；`ranges` 切出来等于词（大小写按原文）
  - `TestMakeSnippetUTF16`：文本 `😀😀天气` 搜 `天气` → ranges `[[4,2]]`（两个 emoji 各 2 个 UTF-16 单位）
  - `SearchTerms("  a  b a ")` → `[a b]`；6 个词截到 5；空 → nil
- [ ] **Step 2:** 跑测试 FAIL
- [ ] **Step 3: 实现** 要点：
```go
func likeArg(t string) string {
    r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
    return "%" + r.Replace(t) + "%"
}
// SQL: SELECT f.rowid, f.body, f.files, f.article FROM search_fts f JOIN messages m ON m.id = f.rowid
//      WHERE m.deleted_at = 0 AND <scope on m> AND (? = 0 OR m.id < ?)
//        AND (f.body LIKE ? ESCAPE '\' OR f.files LIKE ? ESCAPE '\' OR f.article LIKE ? ESCAPE '\') [AND ... per term]
//      ORDER BY m.id DESC LIMIT ?
```
  先收集 rows（id + 三列）再关闭 rows，然后批量 `SELECT msgCols FROM messages WHERE id IN (...)` + `hydrate`，按原顺序组装。字段选择：按 body→files→article 顺序，取第一个（不区分大小写）包含任意词的字段。`makeSnippet`：`strings.Fields` 压空白后转 `[]rune`，用 `strings.ToLower` 的 rune 切片查找第一个词首次出现位置 p（按 rune），窗口 `[p-30, p+len+30]` 裁到边界，前后被裁则加 `…`；在窗口文本（小写）中找出所有词的全部出现，换算 UTF-16 偏移（`utf16.RuneLen`）。scope 条件需作用在 `m.`：`chatScope`/`botScope` 的 `cond` 用的是裸列名 `chat_id`，在本查询中 JOIN 后 `chat_id` 只存在于 messages，不歧义，可直接用。
- [ ] **Step 4:** 测试通过
- [ ] **Step 5:** Commit `feat(store): substring search over text, file names and articles`

### Task 3: 消息分页 after / around

**Files:**
- Modify: `internal/store/query.go`（`listMessages` 拆分）
- Modify: `internal/httpapi/read.go`（`listMessages`、`listBotMessages` 解析 `after`/`around`）
- Test: `internal/store/query_test.go`、`internal/httpapi/read_test.go`

**Interfaces:**
- Produces:
```go
type Page struct{ Before, After, Around int64 } // at most one non-zero
func (s *Store) ListMessages(ctx context.Context, chatID int64, p Page, limit int) ([]MessageView, error)
func (s *Store) ListBotMessages(ctx context.Context, botID int64, p Page, limit int) ([]MessageView, error)
```
  （改签名，更新所有调用点与测试；HTTP 三参数同时给两个以上 → 400）

- [ ] **Step 1: 测试**（query_test）：一会话 1..20 条，其中 9,10,11 为同一相册
  - `After: 5, limit 3` → 6,7,8；`After: 7, limit 3` → 8,9,10 + 补齐 11
  - `Around: 10, limit 4` → 下半 `id<=10` 取 2 条为 9,10 → 9 在相册中，向下补齐无更低成员；上半 `id>10` 取 2 → 11,12；结果 9..12 升序
  - `Around: 15, limit 6` → 13,14,15,16,17,18
  - `Around` 指向已删除 id 照常返回附近
  - 合并时间线（botScope）同样适用
  - HTTP：`?after=1&around=2` → 400；`?around=10` 返回升序
- [ ] **Step 2:** FAIL
- [ ] **Step 3: 实现**：把现有 before 逻辑抽成 `older(ctx, sc, beforeIncl bool, id, limit)`（`id < X` 或 `id <= X`，DESC，相册向下补齐，返回升序）；新增 `newer(ctx, sc, afterID, limit)`（`id > X` ASC，若最后一条有 `media_group_id`，补齐同 chat 同 group `id > last` 的成员以及 scope 内 `last < id <= maxGroupID` 的全部消息；升序）。`Around`：`older(incl, X, limit/2)` + `newer(X, limit - limit/2)`。最后统一 `hydrate`。
- [ ] **Step 4:** `go test ./internal/store ./internal/httpapi` 绿
- [ ] **Step 5:** Commit `feat: page conversations after or around a message`

### Task 4: HTTP 搜索接口

**Files:**
- Create: `internal/httpapi/search.go`、`internal/httpapi/search_test.go`
- Modify: `internal/httpapi/server.go`（注册路由）

**Interfaces:**
- Consumes: `Store.Search`, `SearchTerms`
- Produces: `GET /api/search?q=&chat=&before=&limit=` → `{"items": SearchHit[], "next": <last id or 0>}`（满页时 next = 最后一条 message.id）

- [ ] **Step 1: 测试**：缺 q / 全空白 → 400；`chat=-1`（bot 1 合并）可用；limit 101 → 400；正常返回 items 与 next
- [ ] **Step 2:** FAIL → **Step 3:** 实现（`queryInt` 解析 chat 范围 `-(1<<62)..1<<62`）→ **Step 4:** PASS
- [ ] **Step 5:** Commit `feat(api): GET /api/search`

### Task 5: 收藏与标签（store + HTTP）

**Files:**
- Create: `internal/store/favorites.go`、`internal/store/favorites_test.go`
- Create: `internal/httpapi/favorites.go`、`internal/httpapi/favorites_test.go`
- Modify: `internal/store/query.go`（MessageView.Favorite + hydrate）、`internal/store/ingest.go`（DeleteMessage 删 favorites）、`internal/httpapi/server.go`

**Interfaces:**
- Produces:
```go
type TagRef struct{ ID int64 `json:"id"`; Name string `json:"name"` }
type FavoriteInfo struct{ At int64 `json:"at"`; Tags []TagRef `json:"tags"` }
// MessageView gains: Favorite *FavoriteInfo `json:"favorite"`
type TagCount struct{ TagRef; Count int64 `json:"count"` }
type FavoriteItem struct{ FavID int64 `json:"fav_id"`; Message MessageView `json:"message"` }
var ErrNotFavorite = errors.New("message is not a favorite")
var ErrBadTag = errors.New("bad tag")
func NormalizeTags(in []string) ([]string, error) // trim, 1..32 runes, no \n\r, dedup case-insensitively (first spelling wins), ≤10
func (s *Store) Favorite(ctx context.Context, msgID, now int64, tags []string) (*FavoriteInfo, int64 /*chatID*/, error)
func (s *Store) Unfavorite(ctx context.Context, msgID int64) (chatID int64, err error) // ErrNotFound only when message missing
func (s *Store) SetTags(ctx context.Context, msgID int64, tags []string) ([]TagRef, int64, error)
func (s *Store) ListFavorites(ctx context.Context, tagID, beforeFavID int64, limit int) ([]FavoriteItem, error)
func (s *Store) ListTags(ctx context.Context) ([]TagCount, error)
func (s *Store) DeleteTag(ctx context.Context, id int64) error
```
- `Favorite`：消息不存在/已删 → ErrNotFound；已收藏保留原 `created_at`，`tags` 非 nil 时整体替换
- `SetTags`：未收藏 → ErrNotFavorite（HTTP 409）
- HTTP 路由：`PUT/DELETE /api/messages/{id}/favorite`、`PUT /api/messages/{id}/tags`、`GET /api/favorites`（返回 `{items, next}`，next = 满页时最后 fav_id）、`GET /api/tags`、`DELETE /api/tags/{id}`；变更后发 `message.updated {chat_id,message_id}` 与 `favorites.updated`
- [ ] **Step 1: 测试**：
  - 收藏幂等（两次 at 不变）；带标签收藏；`SetTags` 替换、规范化（`" 旅行 "`、`"旅行"`、`"TRAVEL"`、`"travel"` → `旅行`、`TRAVEL` 两个）；33 字符 → ErrBadTag；11 个 → ErrBadTag；含换行 → ErrBadTag
  - 未收藏 SetTags → ErrNotFavorite；Unfavorite 清掉 message_tags；标签行保留
  - `ListTags` 计数；`DeleteTag` 后消息仍收藏、无该标签
  - `ListFavorites` 按收藏顺序倒序、`tagID` 过滤、`before` 分页；Message.Favorite 带 tags
  - `DeleteMessage` 一条已收藏消息 → favorites 与 message_tags 行消失、ListTags 计数减少
  - `GetMessageView`/`ListMessages` 的 `favorite` 字段：未收藏 null
  - HTTP：409、400（坏标签）、404（无此消息）、SSE 事件发出
- [ ] **Step 2:** FAIL → **Step 3:** 实现（hydrate 末尾按 id IN 批量查 `favorites` 与 `message_tags JOIN tags`，先收集再赋值，注意单连接规则；DeleteMessage 事务里加 `DELETE FROM favorites WHERE message_id = ?`）→ **Step 4:** PASS
- [ ] **Step 5:** Commit `feat: favorites and tags`

### Task 6: 频道未读（store + HTTP）

**Files:**
- Modify: `internal/store/query.go`（ChatView.Unread、ListChats 子查询）
- Create: `internal/store/read.go`（MarkRead）
- Modify: `internal/httpapi/read.go` / `server.go`（`POST /api/chats/{id}/read`）
- Test: `internal/store/query_test.go`、`internal/httpapi/read_test.go`

**Interfaces:**
- Produces: `ChatView.Unread int64 json:"unread"`、`ChatView.LastReadID int64 json:"last_read_id"`；`func (s *Store) MarkRead(ctx, chatID, messageID int64) error`（`UPDATE chats SET last_read_id = MAX(last_read_id, ?)`，无此会话 ErrNotFound）；SSE `chat.read {chat_id}`
- [ ] **Step 1: 测试**：频道会话 3 条新消息 unread=3；MarkRead(2nd) → 1；MarkRead 较小 id 不回退；私聊 unread 恒 0；软删除不计；HTTP body `{message_id}` 缺失 → 400，未知会话 → 404，成功 204 并发事件
- [ ] **Step 2–4:** FAIL → 实现 → PASS
- [ ] **Step 5:** Commit `feat: unread counts for channel conversations`

### Task 7: 监听运行信息

**Files:**
- Modify: `internal/store/watches.go`（`SetWatchStatus` 写 `last_polled_at`；`WatchView` 增字段；`watchCols` 增子查询）
- Modify: `internal/httpapi/watches.go`（JSON 输出 `last_polled_at`、`hits_24h`、`hits_7d`、`last_hit_at`、`poll_seconds`）
- Test: `internal/store/watches_test.go`、`internal/httpapi/watches_test.go`

**Interfaces:**
- Produces: `WatchView{LastPolledAt, Hits24h, Hits7d, LastHitAt int64}`；`ListWatches(ctx, now int64)` 与 `GetWatch(ctx, id, now int64)` 增加 now 参数（统计窗口基准，测试可控）；更新所有调用点
- 统计 SQL（针对 `w` 的频道会话 `c.id`）：
```sql
(SELECT COUNT(DISTINCT CASE WHEN m.media_group_id = '' THEN 'm' || m.id ELSE 'g' || m.media_group_id END)
   FROM messages m JOIN chats c2 ON c2.id = m.chat_id
   WHERE c2.channel_id = w.channel_id AND m.source = 'channel_watch' AND m.deleted_at = 0 AND m.date > ? - 86400)
```
  7d 同理 `604800`；`last_hit_at` = `COALESCE(MAX(m.date), 0)`
- [ ] **Step 1: 测试**：相册 3 条算 1；一条 2 天前只进 7d；8 天前都不进；`last_hit_at` 最大 date；`SetWatchStatus` 后 `last_polled_at = now`（状态未变也写）；HTTP 输出含 `poll_seconds`
- [ ] **Step 2–4**
- [ ] **Step 5:** Commit `feat(watch): last poll time and recent hit counts`

### Task 8: mtproto 链接预览与不支持类型

**Files:**
- Modify: `internal/convert/mtproto/convert.go`
- Modify: `internal/model/model.go`（`RoleLinkPreview = "link_preview"`）
- Test: `internal/convert/mtproto/convert_test.go`

**Interfaces:**
- Produces: `extra.link_preview = {url, display_url, site_name, title, description}`；媒体 role `link_preview`（photo，dedupe `mt:photo:<id>`，只出大图一张）；`extra.unsupported ∈ story|giveaway|paid_media|invoice|game`
- [ ] **Step 1: 测试**：`MessageMediaWebPage{Webpage: &tg.WebPage{URL, DisplayURL, SiteName, Title, Description, Photo: &tg.Photo{...sizes}}}` 且有正文 → kind text、extra.link_preview 各字段、Media 含一条 role link_preview 的 photo；`WebPageEmpty` → 无 link_preview；`MessageMediaStory` 无正文 → kind other + unsupported story；`MessageMediaGiveaway`、`MessageMediaGiveawayResults`、`MessageMediaPaidMedia`、`MessageMediaInvoice`、`MessageMediaGame` 各自值；有正文的 story → kind text + unsupported
- [ ] **Step 2–4**（照片尺寸复用 `pickSizes`，构造 `MediaRef{Photo:true,...}` 同 MessageMediaPhoto 分支）
- [ ] **Step 5:** Commit `feat(convert): link previews and placeholders for unsupported channel posts`

### Task 9: 前端会话 around / newer / jump

**Files:**
- Modify: `web/src/api/client.ts`（`messages`/`botMessages` 接受 `{before?, after?, around?}`；`convMessages(api, key, page, limit)`）
- Modify: `web/src/state/store.ts`（`Conversation.hasNewer`、`loadAround`、`loadNewer`、`upsertMessage`、`refreshLatest(key, {reset})`）
- Modify: `web/src/components/message/MessageList.tsx`（jump 走 around；底部加载更新；回到底部按钮）
- Test: `web/src/state/store.test.ts`、`web/src/components/message/message.test.tsx`、`web/src/api/client.test.ts`

**Interfaces:**
- Produces:
```ts
export interface PageParams { before?: number; after?: number; around?: number }
export function convMessages(api: Api, key: number, page?: PageParams, limit?: number): Promise<Message[]>
store.loadAround(key: number, messageId: number): Promise<void>
store.loadNewer(key: number): Promise<void>
store.refreshLatest(key: number, opts?: { reset?: boolean }): Promise<void>
```
- [ ] **Step 1: 测试**：
  - `loadAround`：fake api 返回窗口 → items 替换、`hasMore`/`hasNewer` 按半页满判断
  - `loadNewer` 合并并更新 hasNewer
  - hasNewer 时 `message.created`（新 id 大于窗口）不插入；窗口内已有消息的 `message.updated` 仍更新
  - `refreshLatest(key,{reset:true})` 替换并 hasNewer=false；hasNewer 时普通 refreshLatest 也走替换
  - MessageList：jumpTo 指向不在 items 的消息 → 调用 around（spy `api.messages` 参数含 around）并最终带 highlight；找不到 → jumpTo 清空；首次打开带 jumpTo 时不先加载最新页
  - 回到底部按钮在 hasNewer 时触发 reset 刷新
- [ ] **Step 2–4**
- [ ] **Step 5:** Commit `feat(web): jump anywhere in a conversation`

### Task 10: 前端搜索

**Files:**
- Create: `web/src/components/left/SearchBox.tsx`、`web/src/components/left/SearchResults.tsx`、`web/src/lib/search.ts`（highlight 切片纯函数）
- Modify: `web/src/api/client.ts`、`web/src/api/types.ts`（`SearchHit`、`SearchPage`）、`web/src/state/store.ts`（`search = signal<{query:string; scope:number}>`、`openSearch(scope?)`）、`ChatsPanel.tsx`、`MiddleColumn.tsx`（头部搜索按钮）、`left.scss`
- Test: `web/src/components/left/left.test.tsx`、`web/src/lib/search.test.ts`

**Interfaces:**
- Produces: `api.search(q, scope?, before?): Promise<{items: SearchHit[]; next: number}>`；`highlightParts(snippet, ranges): {text: string; mark: boolean}[]`
- [ ] **Step 1: 测试**：`highlightParts` 拆分正确（含相邻、越界忽略）；输入后 300ms 内不请求、之后请求一次；结果行显示会话名、`<mark>`；点结果设置 jumpTo 并 navigate；Esc 清空恢复列表；会话头搜索按钮 → 搜索框带 chip，移除 chip → scope 0；无结果文案；滚动到底加载 next
- [ ] **Step 2–4**
- [ ] **Step 5:** Commit `feat(web): message search`

### Task 11: 前端收藏与标签

**Files:**
- Create: `web/src/components/favorites/FavoritesView.tsx`、`TagDialog.tsx`、`favorites.scss`
- Modify: `web/src/lib/router.ts`（`{name:'favorites'}` ↔ `/favorites`）、`web/src/App.tsx`（中间栏渲染收藏视图、移动端单栏逻辑）、`api/client.ts`、`api/types.ts`（`Message.favorite`、`Tag`、`FavoriteItem`）、`MessageList.tsx`（菜单项）、`MessageParts.tsx`（星标）、`ChatsPanel.tsx`（收藏按钮）、`state/store.ts`（`toggleFavorite(m)`、`setTags(m, tags)`、`favorites.updated` 事件分发）
- Test: `web/src/components/favorites/favorites.test.tsx`、`message.test.tsx`、`router` 测试

**Interfaces:**
- Produces: `api.favorite(id, tags?)`、`api.unfavorite(id)`、`api.setTags(id, tags)`、`api.favorites(tag?, before?)`、`api.tags()`、`api.deleteTag(id)`
- [ ] **Step 1: 测试**：菜单「收藏」调用 api 并 toast「已收藏」；已收藏显示「取消收藏」「标签…」；星标渲染；TagDialog 添加/去重/移除/保存调用 setTags；FavoritesView 列表渲染来源行与气泡、标签筛选重新请求、「定位」设置 jumpTo 并导航、空状态文案；router `/favorites`
- [ ] **Step 2–4**
- [ ] **Step 5:** Commit `feat(web): favorites with tags`

### Task 12: 前端未读角标与已读上报

**Files:**
- Modify: `web/src/api/types.ts`（`Chat.unread`、`Chat.last_read_id`）、`api/client.ts`（`markRead`）、`state/store.ts`（`markRead(chatId, id)` 1s 节流 + 本地清零；`chat.read` 事件 → `scheduleChatsReload`）、`ChatsPanel.tsx`（角标）、`MessageList.tsx`（底部且 !hasNewer 时上报）、`left.scss`
- Test: `left.test.tsx`、`store.test.ts`、`message.test.tsx`
- [ ] **Step 1: 测试**：unread>0 显示角标、1000 → `999+`、选中会话不显示；滚到底触发一次 markRead（最新 id）、1s 内多次只发一次、私聊不发；`chat.read` 事件重载会话
- [ ] **Step 2–4**
- [ ] **Step 5:** Commit `feat(web): unread badges for channels`

### Task 13: 前端监听列表行

**Files:**
- Modify: `web/src/api/types.ts`（Watch 增字段）、`web/src/components/watch/WatchList.tsx`、`watch.scss`、`web/src/lib/format.ts`（`formatAgo(ts, now)`）
- Test: `watch.test.tsx`、`format` 测试
- [ ] **Step 1: 测试**：`formatAgo` 刚刚/分钟/小时/天；行两行文本；停滞（启用且超 3×poll）显示「轮询停滞」黄色；停用不判停滞；`last_polled_at=0` 显示「尚未轮询」；无命中「尚无命中」；30s 定时重拉（fake timers）
- [ ] **Step 2–4**
- [ ] **Step 5:** Commit `feat(web): watch list shows polling and recent hits`

### Task 14: 前端链接预览卡片与占位

**Files:**
- Create: `web/src/components/message/LinkPreview.tsx`
- Modify: `MessageBubble.tsx`、`message.scss`、`web/src/components/media/util.ts`（`linkPreviewMedia(msg)`）
- Test: `message.test.tsx`
- [ ] **Step 1: 测试**：extra.link_preview → 卡片含站点名/标题/描述、链接 `target=_blank`、`safeHref` 过滤 `javascript:`；有 link_preview 媒体 done → img src `/media/{id}`；pending → 状态占位；`unsupported: story` 且 other → 「动态 · 请在 Telegram 中查看」，有 origin_link 时为链接；未知 other 仍「不支持的消息类型」；有正文 + unsupported 时正文下方显示该提示
- [ ] **Step 2–4**
- [ ] **Step 5:** Commit `feat(web): link preview cards and named placeholders`

### Task 15: 收尾、整体评审、发布

- [ ] README：搜索、收藏、未读说明（功能列表一节）
- [ ] `go vet ./... && go test ./...`、`cd web && npm test && npm run build`（tsc + lint）全绿
- [ ] 整分支 review（最强模型 subagent），修复确认项
- [ ] 合并前：push 分支 → CI 构建 `sha-*` 镜像成功 → NAS 部署分支镜像 → 按 spec §10 验收（含手机）
- [ ] 合并 main、annotated tag `v0.7.0`、CI 成功后 `gh release create --verify-tag --generate-notes`、NAS 切 `0.7.0`、smoke
- [ ] 基础设施文档（CLAUDE.md、current-state.md、gotchas.md）镜像版本与 schema 改为 0.7.0 / v7
