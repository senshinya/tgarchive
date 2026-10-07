# 全文搜索、收藏与标签、频道未读、监听列表信息、链接预览 设计

> 状态：已确认（2026-10-07）。上位 spec：`2026-10-04-tgarchive-design.md`、`2026-10-06-channel-watch-design.md`。五项合入一个 schema（v7），一次发布 v0.7.0。

## 1. 目标与成功标准

| # | 功能 | 成功标准 |
|---|---|---|
| A | 全文搜索 | 左栏输入任意 1 个及以上字符即可搜到正文、说明文字、文件名、Telegraph 标题与正文中含该子串的消息（中文按子串匹配，大小写不敏感；1–2 个字符的词只对 ASCII 字母不区分大小写）；点结果打开其会话并定位、闪烁该消息，不论它有多早 |
| B | 收藏与标签 | 任意消息可收藏/取消收藏并打标签；收藏视图按收藏时间倒序列出，可按标签筛选，点「定位」跳回原会话 |
| C | 频道未读 | 频道会话在左栏显示未读数角标；打开并看到最新处后清零；换设备一致 |
| D | 监听列表信息 | 监听列表每行可见最后轮询时间、24h/7d 命中数、最近命中时间；轮询停滞标黄 |
| E | 频道帖子渲染补齐 | 带网页预览的频道帖子显示链接预览卡片（含缩略图）；Story、抽奖、付费媒体、账单、游戏显示带类型名的占位 |

### 明确不做
- 搜索语法（引号、排除词、OR）、按发送人/日期/类型筛选、搜索历史
- 机器人会话的未读数；未读的「@提及」「置顶」等
- 收藏夹分组/排序自定义、给未收藏消息打标签
- Bot API 来源消息的链接预览（Bot API 不下发网页预览内容）

## 2. Schema（migration `0007_search_favorites.sql`，user_version 7）

```sql
-- A. 搜索索引：rowid = messages.id；trigram 分词（中文子串、≥3 字符走索引）
CREATE VIRTUAL TABLE search_fts USING fts5(body, files, article, tokenize = 'trigram');
-- B. 收藏与标签
CREATE TABLE favorites (
  id         INTEGER PRIMARY KEY,           -- 分页游标（收藏顺序）
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
-- C. 未读
ALTER TABLE chats ADD COLUMN last_read_id INTEGER NOT NULL DEFAULT 0;
UPDATE chats SET last_read_id = COALESCE((SELECT MAX(id) FROM messages m WHERE m.chat_id = chats.id), 0);
-- D. 轮询时间
ALTER TABLE channel_watches ADD COLUMN last_polled_at INTEGER NOT NULL DEFAULT 0;
```
外加 §3.1 的触发器与存量回填（同一 migration 内）。迁移后已有消息全部视为已读。

## 3. A 全文搜索

### 3.1 索引维护（SQLite 触发器，应用代码不参与）
`search_fts` 每条未删除消息一行，三列：
- `body` = `messages.text`（正文或说明文字）
- `files` = 该消息 `role='main'` 且 `kind IN ('document','audio')` 的 `media.file_name`，空格拼接
- `article` = `articles.title || ' ' || articles.description || ' ' || articles.content`（无文章为空）

触发器：
- `messages` AFTER INSERT（`deleted_at = 0`）→ 插入一行（files/article 由子查询计算，通常为空）
- `messages` AFTER UPDATE OF `text`, `deleted_at` → 删除旧行；`deleted_at = 0` 时按新值重插
- `messages` AFTER DELETE → 删除行
- `message_media` AFTER INSERT / AFTER DELETE → 重算该消息的 `files` 列（仅当该消息有索引行）
- `articles` AFTER INSERT / UPDATE / DELETE → 重算该消息的 `article` 列

`articles.content` 是 Telegraph 节点 JSON（`[{"tag":..,"attrs":{..},"children":[..]}, "文本", ..]`），不能原样入索引（URL、标签名会混入；Go 的 JSON 编码把 `&<>` 转义成 `&` 等，原样索引会搜不到）。`article` 列中的正文部分由触发器用 `json_tree` 只取**数组元素中的字符串**（即文本节点）拼接：
`(SELECT group_concat(value, ' ') FROM json_tree(content) WHERE type = 'text' AND typeof(key) = 'integer')`。
`json_tree` 返回的是解码后的值，转义问题随之消失。

存量回填：migration 末尾 `INSERT INTO search_fts(rowid, body, files, article) SELECT ...` 覆盖全部 `deleted_at = 0` 的消息。

### 3.2 查询（`store.Search`）
`GET /api/search?q=&chat=&before=&limit=`
- `q`：去首尾空白后按空白切词，最多 5 个词，每词 ≥1 字符；空查询 400
- 每个词条件：≥3 个字符的词作为带引号的短语交给 `search_fts MATCH`（多个以 AND 连接，`"` 写成 `""`），由 trigram 索引驱动查询并折叠 Unicode 大小写；1–2 个字符的词只能用 `(body LIKE ? OR files LIKE ? OR article LIKE ?)`（`%词%`，转义 `%_\`，`ESCAPE '\'`）在结果上过滤，只折叠 ASCII 大小写；全为短词时为全表扫描（数据量小时可接受）
- `chat`：会话键（正数 = chat id，负数 = `-botId` 合并时间线），省略为全部
- 只返回 `messages.deleted_at = 0`；按 `messages.id` DESC，`before` 为游标，`limit` 默认 30、最大 100
- 每条结果：
```json
{ "message": <MessageView 不含 media 以外的 hydrate 开销照常>, "field": "body|files|article",
  "snippet": "…前后约 40 字…", "ranges": [[start, len], ...] }
```
  `snippet` 在 Go 侧生成：取首个命中字段，定位第一个词首次出现处，前后各 30 个字符（rune）截取并加省略号，换行压成空格；`ranges` 为 snippet 内所有词出现的位置（按 UTF-16 code unit，前端直接 `slice` 用）
- 结果的 `message` 走现有 `hydrate`（带 media，用于缩略图与 kind 标签）

### 3.3 跳转到任意位置（会话支持「中间有空洞」）
后端：`GET /api/chats/{id}/messages` 与 `/api/bots/{id}/messages` 增加参数（三者互斥）：
- `before`（现有）
- `after=X`：`id > X` 升序取 `limit` 条，相册在上边界不截断（补齐最新那条所在组 `id > 上边界` 的全部成员），返回仍按 id 升序
- `around=X`：`id <= X` 取 `limit/2`（向下补齐相册）+ `id > X` 取 `limit/2`（向上补齐相册），合并升序
- `around` 指向已删除或不存在的 id 时照常返回附近窗口

前端 `Conversation` 增加 `hasNewer: boolean`（默认 false）：
- `loadAround(key, id)`：替换 `items` 为窗口；`hasMore = 下半页满`、`hasNewer = 上半页满`
- `loadNewer(key)`：`after = 最新已载 id`，合并；`hasNewer = 本页满`
- 滚动到距底部 < 400px 且 `hasNewer` 时 `loadNewer`
- 「回到底部」按钮：`hasNewer` 时先 `refreshLatest` 以「重来」方式替换为最新页（`hasNewer=false`），再滚到底
- `upsertMessage`：`hasNewer` 时只更新已在窗口内的消息、不追加更新的（避免空洞），`refreshLatest` 的「重叠合并」分支在 `hasNewer` 时改走「重来」
- `jumpTo` 处理：目标已在 `items` → 滚动并闪烁；否则（会话已加载、不在加载中）调用一次 `loadAround`，之后仍不在则放弃（已删除或不属于该会话）。移除 `JUMP_MAX_PAGES` 逐页加载逻辑
- 进入会话的首次加载若已有 `jumpTo` 指向该会话，直接 `loadAround` 而非加载最新页

### 3.4 界面
- 左栏 `left-header` 下方新增搜索框（Web A 样式：圆角、左侧放大镜、右侧清除）。有输入时 300ms 防抖查询，会话列表区替换为结果列表；清空或 Esc 恢复会话列表
- 结果行：会话头像、会话名（频道名 / 发送人名，合并时间线不另显）、日期（`formatListTime`）、`snippet`（命中处 `<mark>` 高亮，主题色）；文件命中前缀「📎」、文章命中前缀「📰」；滚动到底加载下一页；无结果显示「没有找到相关消息」
- 点结果：`jumpTo = {key: chat_id, messageId}` 并 `navigate({name:'chat', chatId})`；移动端（单栏）同时收起搜索态以显示中间栏
- 会话头部新增搜索按钮（放大镜）：打开左栏搜索并限定当前会话，搜索框左侧显示可移除的会话名小标签（chip），移除后变为全局
- 搜索状态（关键词、限定会话）为 store 内 signal，不进路由

## 4. B 收藏与标签

### 4.1 接口
| 方法 | 路径 | 说明 |
|---|---|---|
| PUT | `/api/messages/{id}/favorite` | 收藏（幂等），body 可选 `{tags: string[]}`；返回 `{favorited_at, tags}` |
| DELETE | `/api/messages/{id}/favorite` | 取消收藏（连带清标签关联），幂等 204 |
| PUT | `/api/messages/{id}/tags` | `{tags: string[]}` 整体替换；未收藏的消息 409；返回 `{tags}` |
| GET | `/api/favorites?tag=&before=&limit=` | 按 `favorites.id` DESC 分页，`before` 为 favorites.id 游标；`tag` 为标签 id 过滤 |
| GET | `/api/tags` | `[{id, name, count}]`，按名称排序；count 为使用次数 |
| DELETE | `/api/tags/{id}` | 删除标签（解除所有关联，不动收藏） |

- 标签名：去首尾空白，1–32 个字符，不能含换行；同名（大小写不敏感）复用；一条消息最多 10 个标签；不再被引用的标签保留（可手动删除）
- `MessageView` 增加 `favorite: {at, tags: [{id,name}]} | null`（hydrate 时一次查询批量带出）
- `GET /api/favorites` 返回 `{items: [{fav_id, message: MessageView}], next: fav_id|0}`
- 消息被删除（软删）：`DeleteMessage` 同事务删除其 `favorites` 行（级联清标签关联）；硬删由外键级联
- 变更后发 SSE `message.updated {chat_id, message_id}`（复用现有刷新路径）与 `favorites.updated`

### 4.2 界面
- 消息菜单增加「收藏」/「取消收藏」与「标签…」（收藏后可用）。相册中按住哪张就是哪条
- 已收藏消息在时间元信息（`MessageMeta`）前显示实心小星标
- 「标签…」打开对话框：已选标签 chip（可移除）+ 输入框（回车添加，下方按前缀列出已有标签供点选）+ 保存
- 左栏头部增加收藏按钮（Bookmark 图标），进入路由 `/favorites`（`{name:'favorites'}`），中间栏显示收藏视图：
  - 头部：返回、标题「收藏」、条目数
  - 标签筛选条：「全部」+ 各标签（带计数），单选；长按/右键标签可删除标签（确认）
  - 列表：每条一个卡片——上方一行来源（会话头像 + 会话名 + 原消息日期 + 「定位」按钮），下方原样渲染该消息气泡（复用 `MessageBubble`，相册中的单条按单图渲染），再下方该消息的标签 chip
  - 卡片菜单与会话中一致（取消收藏、标签…、复制、下载），不提供删除存档
  - 点「定位」：`jumpTo` + 导航到该会话
  - 空状态：「还没有收藏的消息」+ 说明「在消息上右键或长按即可收藏」
  - 下拉到底加载下一页；收到 `favorites.updated` 时重载当前页
- 左栏在 `/favorites` 时收藏按钮处于选中态；移动端收藏视图占满屏，返回回到会话列表

## 5. C 频道未读

- `ChatView` 增加 `unread`：`kind='channel'` 时为 `COUNT(*) WHERE chat_id = c.id AND deleted_at = 0 AND id > c.last_read_id`，私聊恒为 0
- `POST /api/chats/{id}/read {message_id}`：`last_read_id = MAX(last_read_id, message_id)`，204；发 SSE `chat.read {chat_id}` 让其他设备刷新列表
- 前端：频道会话打开后，当列表处于底部（距底 < 100px）、`hasNewer=false` 且最新已载消息 id > 该会话 `last_read_id` 时，上报最新 id（同一会话 1s 节流）；本地立即把该会话 `unread` 置 0
- 左栏 `ChannelItem` 时间下方显示圆形未读角标（Web A `.ChatBadge` 样式：主题色底、白字、≥1000 显示 `999+`）；所选会话不显示角标
- 「按 bot」模式下频道行同样显示

## 6. D 监听列表信息

- `SetWatchStatus` 每次调用（每轮非瞬时错误的轮询结束）同时写 `last_polled_at = now`；只在状态变化时返回 changed（不额外推 SSE）
- `WatchView` / `GET /api/admin/watches` 增加：
  - `last_polled_at`
  - `hits_24h`、`hits_7d`：该频道会话内 `source='channel_watch'` 且 `deleted_at=0` 的消息，按相册去重（`COUNT(DISTINCT CASE WHEN media_group_id='' THEN 'm'||id ELSE 'g'||media_group_id END)`），按帖子 `date` 落在区间内计
  - `last_hit_at`：上述消息的 `MAX(date)`，无则 0
  - `poll_seconds`：当前轮询间隔（便于前端判断停滞）
- 列表行副标题改两行：
  - 第 1 行：现有状态文字 + ` · {相对时间}轮询`（从未轮询为「尚未轮询」）
  - 第 2 行：`24h 命中 {n} · 7d 命中 {n} · 最近 {相对时间}`（无命中为「尚无命中」）
- 启用且 `now - last_polled_at > 3 × poll_seconds`（且 last_polled_at > 0 或监听已创建超过 3 个周期）时第 1 行改为黄色「轮询停滞 · {相对时间}轮询」，状态点为警告色
- 监听列表页每 30s 重新拉取一次，使相对时间与停滞判断保持新鲜
- 相对时间：`刚刚`（<1 分钟）/`N 分钟前`/`N 小时前`/`N 天前`

## 7. E 频道帖子渲染补齐

### 7.1 链接预览（mtproto 来源：频道监听、userbot 代取）
- `MessageMediaWebPage` 且 `Webpage` 为 `*tg.WebPage`：`extra.link_preview = {url, display_url, site_name, title, description}`（空字段省略）；若有 `Photo` 则按照片同样的选尺寸逻辑产出一个 `role = 'link_preview'` 的媒体（dedupe key `mt:photo:<id>`，与普通照片同 key 规则，可复用下载/刷新 file reference 流程）
- `WebPageEmpty` / `WebPagePending` / `WebPageNotModified`：不产出卡片
- 消息 `kind` 不变（有正文为 text）
- 前端：`text-content` 内、正文之后显示卡片（Web A `.WebPage` 样式：左侧主题色竖线、站点名主题色加粗、标题加粗、描述最多 4 行、缩略图在下方按宽度适配；点卡片新开链接）。有 `link_preview` 媒体且已下载时显示缩略图，未下载时显示状态占位（复用 `MediaStatus`）
- `hydrate` 照常带出 `role='link_preview'` 媒体；共享媒体「图片」只看 `role='main'`，不受影响

### 7.2 不支持类型的占位
- mtproto 转换：`MessageMediaStory`→`extra.unsupported='story'`，`MessageMediaGiveaway`/`MessageMediaGiveawayResults`→`'giveaway'`，`MessageMediaPaidMedia`→`'paid_media'`，`MessageMediaInvoice`→`'invoice'`，`MessageMediaGame`→`'game'`；`kind` 有正文时为 text，否则 other
- 前端：`other` 气泡与带 `unsupported` 的消息显示「{类型名} · 请在 Telegram 中查看」（动态 / 抽奖 / 付费媒体 / 账单 / 游戏），有 `origin_link` 时类型名为链接；未知仍为「不支持的消息类型」

## 8. SSE 事件新增
| 事件 | data | 前端处理 |
|---|---|---|
| `chat.read` | `{chat_id}` | 重载会话列表 |
| `favorites.updated` | `null` | 收藏视图重载；标签列表重载 |

## 9. 测试
- Go：
  - migration 7：从 v6 数据库升级后回填索引、`last_read_id` = 各会话最大 id
  - 触发器：插入/改文本/软删/硬删/补媒体文件名/保存与删除文章后索引同步
  - `Search`：中文 1/2/3+ 字符、英文大小写、多词 AND、`%`/`_` 字面量、会话与合并时间线范围、游标分页、snippet 与 ranges（含 emoji 的 UTF-16 偏移）
  - `listMessages` 的 `after`/`around`：相册不被截断、边界与空结果
  - 收藏/标签：幂等、标签规范化与上限、未收藏打标签 409、软删除级联、标签计数、筛选分页
  - 未读：计数、只增不减、私聊恒 0
  - 监听统计：相册去重、24h/7d 边界、`last_polled_at` 写入
  - mtproto 转换：WebPage 产出 extra 与 link_preview 媒体；各不支持类型的 `unsupported`
  - HTTP：上述接口的参数校验与状态码
- 前端（vitest）：搜索框防抖与结果渲染高亮、限定会话 chip；`loadAround`/`loadNewer`/hasNewer 下的 upsert；jumpTo 走 around；收藏菜单、标签对话框、收藏视图筛选；未读角标与上报节流；监听行两行文本与停滞判断；链接预览卡片与占位文字

## 10. 验收（分支构建部署到 NAS 后、打 tag 前）
1. 搜「大」类单字、两字、长串中文、英文大小写不同的词，结果正确高亮；点一条很早的结果能定位
2. 会话内搜索 chip 生效；移除后变全局
3. 收藏 3 条（含相册中一张），打标签、按标签筛选、定位、取消收藏；删除一条已收藏消息后收藏视图同步消失
4. 频道新命中后左栏出现角标，打开滚到底清零；另一设备同步清零
5. 监听列表两行信息正确；停止 NAS 容器外的 userbot 不现实，改为人工把轮询间隔调大观察「停滞」逻辑（或单测覆盖）
6. 找一条带链接预览的频道帖子（可用回溯拉取）显示卡片与缩略图
7. 手机端（iPhone）：搜索框、结果点击跳转、收藏视图、长按菜单收藏、角标显示
