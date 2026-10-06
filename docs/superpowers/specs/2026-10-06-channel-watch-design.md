# 频道监听（按 reaction 等条件自动存档）设计

> 状态：已确认（2026-10-06，方案 A：userbot 定时轮询）。上位 spec：`2026-10-04-tgarchive-design.md`、`2026-10-05-live-sync-bot-timeline-downloads-design.md`。

## 1. 目标

由 userbot 账号监听指定频道：每条新帖发布后观察 x 分钟，期间任一时刻满足用户配置的条件（可 AND/OR 嵌套组合）即存档该帖（含媒体）；到期未满足则放弃。每个被监听频道是一个独立会话，在左栏任何模式下都单独成行，不并入任何 bot。

### 成功标准
- 添加监听后，新帖在满足条件的下一个轮询周期内（默认 ≤60s）出现在该频道会话中，媒体按现有下载队列落盘
- 条件支持：指定表情数、reaction 总数、浏览/转发/评论数、比率、内容类型、文本关键词；AND/OR 最多 3 层
- 编辑条件时可对该频道最近 5 条帖子实时试算
- 重启不丢观察中的帖子；userbot 离线期间到期的帖子恢复后再判一次
- 频道无法访问时监听标错误并推 Bark
- 左栏「按人」「按 bot」两种模式下频道会话都单独显示

### 明确不做
- 回溯已有老帖（添加监听只观察之后的新帖）
- 存档后继续更新 reaction 数（只记命中时的快照）
- 自动加入私有频道（邀请链接）
- 监听群组/超级群（只支持广播频道）
- 频道评论区存档

## 2. 获取数据（userbot 轮询）

复用 `userbot.Service.With` 的常驻连接，不开启 MTProto updates。

### Poller（`internal/userbot/watcher.go`）
- 每个周期（全局设置 `watch_poll_seconds`，默认 60，范围 30–600）对每个启用的监听依次执行；userbot 非 `ready` 时整轮跳过
- **拉新帖**：`messages.getHistory(peer, min_id = last_seen_id, limit = 100)`，取 `ID > last_seen_id` 的 `tg.Message`（跳过 service message）；满 100 条时以最小 id 为 `offset_id` 继续，最多 5 页。每条写入 `watch_pending`（`deadline = date + window*60`），推进 `last_seen_id`
- **初始化**：`last_seen_id = 0` 时只取最新一条帖子的 id 作为 `last_seen_id`，不入观察表（不回溯）。创建监听时若 userbot 就绪则同步初始化
- **判定**：取该监听全部观察中的帖子 id，按 100 一批 `channels.getMessages`（一次拿到正文、媒体、reactions、views、forwards、replies）。按相册（`grouped_id`，无则单条）分组计算统计并按条件树求值：
  - 命中 → 整组存档（§4），删除该组观察记录
  - 未命中且组内全部 `deadline ≤ now` → 删除观察记录
  - 返回 `MessageEmpty`（帖子已删）→ 删除观察记录
- **相册统计**：各指标取组内最大值（reaction 按表情取最大），文本为各条拼接，类型为并集
- **频道解析**：复用代取的解析逻辑（`userbot_peers` 缓存 → 用户名解析 → dialogs 扫描，缓存失效时驱逐重解），抽成包内函数供 Fetcher 与 Poller 共用
- **错误**：
  - `FLOOD_WAIT_n`：等待 n 秒（上限 300s）后结束本轮
  - `CHANNEL_PRIVATE` / `CHANNEL_INVALID` / 无法解析：监听 `status = error`、记录原因，首次进入 error 时推 Bark（`docker` 分组）；之后每轮仍重试，恢复后回到 `ok`
  - 连接类错误（未就绪、连接断开）：静默跳过本轮
- 状态或观察数变化时发 SSE `watch.updated {watch_id}`

### 统计（`internal/watchcond`，纯函数）
```go
type Stats struct {
  Reactions map[string]int // key：emoji 原文 / "custom:<document_id>" / "paid"
  Total, Views, Forwards, Replies int
  Kinds map[string]bool    // photo / video / file / text
  Text string
}
```
- `Total` = 全部 reaction 计数之和（含自定义表情、付费星星）
- 类型：photo = 有图片；video = 视频 / GIF / 圆视频；file = 文件 / 音频 / 语音；text = 整组无任何媒体

## 3. 条件

### JSON（`channel_watches.cond_json`）
```jsonc
{"op": "and", "items": [ /* 节点 */ ]}                       // 组：op = and | or
{"metric": "reaction", "key": "🔥", "cmp": "gte", "value": 10}
{"metric": "total" | "views" | "forwards" | "replies", "cmp": "gte" | "lte", "value": 100}
{"metric": "ratio", "num": "🔥" | "total", "den": "total" | "views", "cmp": "gte" | "lte", "value": 60}  // value 为百分比
{"metric": "type", "cmp": "is" | "not", "value": "photo" | "video" | "file" | "text"}
{"metric": "text", "cmp": "contains" | "not_contains", "value": "关键词"}
```
- 校验：根必须是组；组非空；最多 3 层组；叶子总数 ≤ 30；数值为非负整数（ratio 0–100）；关键词 1–100 字符；未知字段/取值拒绝（400，中文原因）
- 求值：比率分母为 0 → 该叶子为假；文本比较不区分大小写（Unicode 小写）
- `Explain(node, stats)` 返回命中叶子的可读说明（如 `🔥 23 ≥ 10`、`浏览 8.1k ≥ 5000`、`🔥 占比 64% ≥ 60%`），存档时写入消息

## 4. 存储

迁移 `0005_channel_watch.sql`。迁移执行器改为：每个迁移前 `PRAGMA foreign_keys = OFF`，事务内执行并在提交前 `PRAGMA foreign_key_check` 无结果，结束后恢复 `ON`（SQLite 官方重建表流程；单连接保证 pragma 作用于同一连接）。

```sql
channels(channel_id INTEGER PK, title TEXT, username TEXT, avatar_path TEXT, updated_at INTEGER)

-- chats 重建：kind 区分私聊与频道；私聊 bot_id/sender_id 必填，频道 channel_id 必填且唯一
chats(id INTEGER PK, kind TEXT NOT NULL DEFAULT 'private',
  bot_id INTEGER REFERENCES bots(id) ON DELETE CASCADE,
  sender_id INTEGER REFERENCES senders(tg_user_id),
  channel_id INTEGER UNIQUE REFERENCES channels(channel_id),
  last_message_at INTEGER NOT NULL DEFAULT 0,
  UNIQUE(bot_id, sender_id),
  CHECK ((kind = 'private' AND bot_id IS NOT NULL AND sender_id IS NOT NULL AND channel_id IS NULL)
      OR (kind = 'channel' AND channel_id IS NOT NULL AND bot_id IS NULL AND sender_id IS NULL)))

channel_watches(id INTEGER PK, channel_id INTEGER NOT NULL UNIQUE REFERENCES channels(channel_id),
  window_minutes INTEGER NOT NULL, cond_json TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL DEFAULT 'ok', last_error TEXT NOT NULL DEFAULT '',
  last_seen_id INTEGER NOT NULL DEFAULT 0, hits INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)

watch_pending(watch_id INTEGER NOT NULL REFERENCES channel_watches(id) ON DELETE CASCADE,
  tg_message_id INTEGER NOT NULL, grouped_id INTEGER NOT NULL DEFAULT 0,
  date INTEGER NOT NULL, deadline INTEGER NOT NULL, PRIMARY KEY(watch_id, tg_message_id))

custom_emoji(document_id INTEGER PK, media_id INTEGER NOT NULL REFERENCES media(id))

ALTER TABLE messages ADD COLUMN stats_json TEXT NOT NULL DEFAULT '';
```
- 窗口 1–1440 分钟
- 存档消息：`source = 'channel_watch'`，`origin_chat_id/title/link` 同代取；`last_message_at` 取存档时间
- `stats_json`：`{reactions:[{key, emoji?, custom_id?, media_id?, count}], total, views, forwards, replies, hit:{at, reasons:[...]}}`，相册每条相同
- 读取私聊相关的旧查询（会话列表、发送人头像刷新、回执）对 NULL 列使用 LEFT JOIN / COALESCE；回执只处理 `bot_update` 与 `userbot_fetch`
- `collectOrphans` 保留被 `custom_emoji` 引用的媒体
- **自定义表情**：存档或预览遇到未知 `custom:<id>` 时调 `messages.getCustomEmojiDocuments`，按贴纸转为 `media`（`dedupe_key = mt:doc:<id>`）并登记 `custom_emoji`，走现有下载队列
- **频道头像**：`/avatars/channels/{channel_id}`；文件不存在且 userbot 就绪、频道在 peers 缓存中时，按需以 `InputPeerPhotoFileLocation`（小图）下载到 `avatars/channels/<id>.jpg`；无头像 404。频道信息（标题、用户名、头像）在创建监听时与每日维护时刷新
- 删除监听：`DELETE /api/admin/watches/{id}` 只删监听（会话与存档保留，会话显示「未监听」）；`?purge=1` 同时删除该频道会话及其消息与无引用媒体

## 5. HTTP 接口（`/api/admin/*`，同现有鉴权与 CSRF）

| 接口 | 说明 |
|---|---|
| `GET /api/admin/channels?refresh=1` | userbot 已加入的广播频道 `[{channel_id, title, username, participants, watched}]`，内存缓存 5 分钟，`refresh=1` 绕过 |
| `GET /api/admin/channels/search?q=` | `contacts.search` 公开广播频道（≤20 条），q 为 2–64 字符 |
| `POST /api/admin/channels/resolve` | `{input}`：`@name` / `name` / `t.me/name` / `t.me/name/123` / `t.me/c/<id>/<msg>`，返回频道 |
| `POST /api/admin/watches/test` | `{channel_id, cond}`：频道最近 5 条帖子 `[{tg_message_id, date, kind, text, stats, hit, reasons}]` + `{reactions_available: {all, list}}`；帖子与可用反应按频道缓存 60s；cond 为空时 hit 为 false |
| `GET /api/admin/watches` | 全部监听（含频道信息、观察中数量、已存数、状态） |
| `POST /api/admin/watches` | `{channel_id, window_minutes, cond, enabled}`；频道已有监听 → 409 |
| `PUT /api/admin/watches/{id}` | 同上字段（不可改频道） |
| `DELETE /api/admin/watches/{id}?purge=1` | §4 |
| `GET/PUT /api/admin/watch-settings` | `{poll_seconds}` |

以上接口在 userbot 未就绪时（涉及 Telegram 调用的）返回 409「代取账号未登录」。所有渠道返回的频道都写入 `userbot_peers`，后续按 id 访问。

`GET /api/chats` 每项增加 `kind`、`channel`（`{channel_id, title, username, has_avatar}`）与 `watch`（`{id, enabled, status, error, window_minutes, pending, hits}`，无监听时为 null）；私聊项 `channel`、`watch` 为 null，频道项 `bot_id = 0`、`sender` 为空对象。

## 6. 前端

### 路由
- `/settings/watches`：监听列表 + 轮询间隔
- `/settings/watches/new`：选频道 → 条件编辑
- `/settings/watches/:id`：条件编辑

### 选频道（`ChannelPicker`）
- 打开即拉已加入频道列表，搜索框本地过滤（名称、用户名，不区分大小写）；右上角刷新
- 无本地匹配且输入 ≥2 字符：停顿 500ms 后搜索公开频道，单独一组「公开频道」
- 输入形如链接或 `@name` 时显示「解析 xxx」一项
- 每行：头像（`/avatars/channels/:id`，失败回退首字母）、标题、`@用户名`、订阅数；已监听的标「已监听」，点击进入其编辑页
- 私有邀请链接提示「请先用代取账号加入该频道」

### 条件编辑（`WatchEditor` + `CondEditor`）
- 顶部频道信息；窗口分钟数；启用开关
- 条件组：组头 AND/OR 切换（「全部满足 / 任一满足」）；行 `[指标][运算符][值]` + 删除；组底「+ 条件」「+ 条件组」（第 3 层不显示「+ 条件组」）；非根组可删除
- 表情选择：频道可用反应列表（`all` 时额外允许输入任意 emoji），自定义表情显示图片
- 试算：条件变化 400ms 后调 `/watches/test`，最近 5 条帖子逐条显示 ✓/✗、摘要文字与各项数值
- 保存：前端先做与后端相同的结构校验，失败就地标红；成功后进入该频道会话

### 左栏
- 标题栏加「监听频道」按钮（`Radio` 图标）→ `/settings/watches/new`
- 按人模式：「全部」含频道会话按时间混排；标签栏在「有 ≥2 个 bot」或「有频道会话」时显示，末尾「频道」标签只看频道；选具体 bot 时不显示频道
- 按 bot 模式：bot 行与频道行按时间混排
- 频道行：频道头像 + 名称，标题前小喇叭图标；预览为最后存档帖；监听 error 时时间位置显示红色感叹号

### 会话
- 顶栏：频道头像、名称；副标题「监听中 · 窗口 N 分钟 · 观察中 M 条」/「已停用」/「出错：原因」/「未监听」；右侧「监听设置」按钮
- 气泡（`source = channel_watch`）：无发送人头像与标题；正文后底部一行 reaction 标签（emoji + 数量，自定义表情图片，⭐ 为付费）；meta 区显示浏览数（眼睛图标）、作者签名、时间；命中标记（火花图标），点击浮层显示命中说明与时间
- 共享媒体、查看器、文章、删除、实时更新与现有会话一致
- SSE `watch.updated` → 重载会话列表

### 设置
- 管理页「Telegram」节下新增「频道监听」入口（副标题：N 个监听 / 有错误时显示错误数）
- 列表页：每行频道头像、名称、状态（观察中 M · 已存 K / 错误原因），启用开关；点击进编辑；轮询间隔输入（秒）
- 编辑页底部「删除监听」：弹窗二选一「仅停止监听」/「同时删除已存档的帖子」

## 7. 测试
- Go：迁移（旧库升级后私聊会话与消息完好、FK 完整）、`watchcond`（各指标、AND/OR 嵌套、比率分母 0、校验各拒绝分支、Explain 文案）、Poller（初始化不回溯、拉新分页、命中存档整组、到期丢弃、帖子删除、userbot 未就绪跳过、离线后到期补判、CHANNEL_PRIVATE 进 error 且只推一次、恢复）、频道会话 Ingest、`ListChats` 混合、删除监听两种方式、custom emoji 登记与孤儿保留、各接口参数校验
- 前端：路由、条件编辑器（增删、切换、深度限制、校验）、试算渲染、频道选择（过滤、公开搜索去抖、解析项）、左栏两种模式与频道标签、频道顶栏各状态、频道气泡 reaction/浏览/命中、`watch.updated`
- 验收（分支构建 + mock）：桌面、390×844 浅/深色、800px；手机实测后再发布

## 8. 错误处理

| 情况 | 行为 |
|---|---|
| userbot 未登录 | 轮询暂停；设置页监听列表顶部提示；相关接口 409 |
| 频道不可访问 | 监听 error + Bark（仅首次），持续重试 |
| FLOOD_WAIT | 等待后结束本轮 |
| 存档时媒体下载失败 | 与现有媒体一致（重试状态机 + 下载面板） |
| 自定义表情获取失败 | 显示 `❔` 占位，不影响存档 |
| 条件 JSON 损坏（库内） | 该监听 error「条件无效」，不判定 |
