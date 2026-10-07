# 媒体墙与统计面板 设计

日期：2026-10-07　状态：已确认（默认决策随设计一并认可，目标为持续推进至上线）

## 目标

- **媒体墙**：把全部会话里的图片、视频、GIF 按时间铺成一面墙，比在聊天记录里翻图快，点开即进入现有大图浏览器，在全局范围左右翻。
- **统计面板**：一页看清存档规模、活跃度、增长、存储和频道监听效果。

成功标准：两个视图在桌面与手机（≤925px 全屏）可用；媒体墙在 10 万级消息下翻页查询走索引；统计接口一次请求返回全部数据。

## 现状事实（决定设计的约束）

- `store.listMedia(scope, typ, before, limit)` 已支持 chat / bot 范围的共享媒体；加全局范围即可。
- 大图浏览器 `ViewerTarget{chatId}` 通过 `convMedia(api, key, 'media', before)` 翻页，`key` 为会话键。
- `messages` 没有入库时间，只有 Telegram 原始 `date`：热力图与增长按消息日期统计。
- 监听只记 `hits`，没有扫描数：命中率需要新计数，从 v8 迁移起累计。

## 媒体墙

### 后端
- `Store.ListAllMedia(ctx, typ, source, beforeID, limit)`：
  - `typ`：`all`（photo/video/animation）、`photo`、`video`（video/animation）；其他 → `ErrBadMediaType`。
  - `source`：`all`、`private`（chats.kind='private'）、`channel`（chats.kind='channel'）；其他 → `ErrBadMediaType`。
  - 只取 `role='main'`、未删除消息，`ORDER BY id DESC`，`before` 翻页，hydrate 后返回 `[]MessageView`。
  - 查询由 `messages` 主键倒序驱动，媒体条件用 `EXISTS`（不物化整张 IN 子查询），测试里用 `EXPLAIN QUERY PLAN` 断言不出现 `USE TEMP B-TREE FOR ORDER BY`。
- `GET /api/media?type=&source=&before=&limit=`（limit 默认 60，上限 100）。与 `GET /media/{id}`、`POST /api/media/{id}/retry` 不冲突。

### 前端
- 路由 `/media`（`{ name: 'media' }`），左栏头部新增按钮（图片图标，位于收藏按钮旁），行为同收藏：中间栏显示，手机全屏，返回回列表。
- `MediaWall` 组件：
  - 顶部两组切换：类型「全部 / 图片 / 视频」，来源「全部来源 / 私聊 / 频道」。
  - **等高行布局**（justified rows）：纯函数 `justify(items: {w,h}[], width, targetH, gap)` → 每行的项与高度；宽高缺失按 1:1；单项宽高比夹到 [0.5, 3]；最后一行不拉伸（高度用 targetH）。targetH：宽度 < 600 时 120px，否则 180px。
  - 按月份分组（沿用 `monthKey/formatMonth`），每组独立排版，组标题吸顶。
  - 卡片沿用共享媒体：照片下载完用原图否则缩略图、视频用缩略图或首帧、剧透模糊、视频时长 / GIF 角标、下载中 / 不可用状态（不可点）。
  - 滚动到距底 600px 加载下一页（60 条）。容器宽度用 ResizeObserver 取，jsdom 下回退 600。
  - 点击打开浏览器：`ViewerTarget` 新增 `{ wall: { type, source }; seed: ViewerItem[]; mediaId }`；浏览器以墙上已加载项为 seed，向更早翻页时调 `/api/media` 同一筛选条件。标题显示每项来源会话（ViewerItem.chatId 已有）。
  - 实时：`media.updated` 刷新对应卡片，`message.deleted` 移除；新消息不自动插入（重新进入或切换筛选刷新）。
  - 空状态：「暂无媒体」。

## 统计面板

### 后端
`GET /api/stats?tz=<分钟偏移，JS getTimezoneOffset 取反，范围 ±840>` → 

```json
{
  "totals": { "messages": 0, "private_chats": 0, "channel_chats": 0, "media_files": 0, "media_bytes": 0,
              "db_bytes": 0, "disk_free": 0, "disk_total": 0 },
  "daily":  [{ "day": "2026-10-07", "count": 0 }],          // 最近 371 天（53 周），只列非零天
  "monthly":[{ "month": "2026-10", "messages": 0, "media_bytes": 0 }], // 全部月份，按月增量，前端累加
  "top_chats": [{ "chat_id": 0, "messages": 0, "media_bytes": 0 }],  // 前 10
  "media_kinds": [{ "kind": "photo", "count": 0, "bytes": 0 }],
  "media_states": { "done": 0, "pending": 0, "failed": 0, "too_large": 0 },
  "watches": [{ "watch_id": 0, "chat_id": 0, "title": "", "daily": [{ "day": "", "count": 0 }],
                "hits": 0, "scanned": 0, "scan_hits": 0 }]
}
```

- 计数口径：消息只算未删除；媒体文件按 `media` 行去重（被多条消息引用只算一次），只算被未删除消息引用的；`media_bytes` 只算 `state='done'`；`media_states` 按 `media.state` 的四个取值（done / pending / failed / too_large）计数。
- 日期分桶：`date(m.date + tz*60, 'unixepoch')`。
- 监听 daily：最近 30 天，`source='channel_watch'`，相册算一次（同 WatchActivity）。
- `db_bytes`：数据库页数 × 页大小（不含尚未检查点的 WAL）；`disk_free/disk_total`：数据目录所在文件系统（`syscall.Statfs`），取不到时为 0。
- 实时计算，无缓存。

### 监听扫描计数
- 迁移 0008：`channel_watches` 加 `scanned INTEGER NOT NULL DEFAULT 0`、`scan_hits INTEGER NOT NULL DEFAULT 0`。
- `AddPending` 在同一事务里按新插入的帖子累加 `scanned`：单帖各算 1，相册（grouped_id≠0）在本批内去重后、且该 grouped_id 此前不在 pending 中时算 1。
- `AddWatchHit(id, polled)`：轮询命中 `scan_hits + 1`，回填命中只加 `hits`（回填的帖子没有计入 scanned）。迁移时 scanned 以当时 pending 中的帖子数（相册算一次）起算。界面命中率封顶 100%。命中率 = scan_hits / scanned（scanned=0 时显示「—」），界面注明「自 v0.8.0 起统计」。

### 前端
- 路由 `/stats`，左栏头部按钮（图表图标）。中间栏显示，手机全屏。
- 区块（卡片式，大气留白）：
  1. 概览：消息、私聊 / 频道会话、媒体文件与占用、数据库大小、磁盘剩余（带占用条）。
  2. 活跃热力图：53 列 × 7 行 CSS grid，5 级色阶按分位数（非零天的 25/50/75 分位），`title` 与点击显示「YYYY-MM-DD · N 条」；窄屏横向滚动且初始滚到最右。
  3. 增长：SVG 折线（累计消息）+ 面积（累计媒体占用，右轴），x 轴按月。
  4. 会话排行：前 10 横向条形，点击跳转会话。
  5. 媒体构成：按类型数量与大小的条形；下载状态四个数字（完成 / 下载中 / 失败 / 过大）。
  6. 频道监听：每个监听一行，30 天迷你柱状图 + 总命中 + 命中率。
- 头部刷新按钮；加载中 Spinner；失败显示错误与重试。
- 工具函数（纯、可测）：`formatBytes`、`heatmapGrid(daily, today)`、`quantileLevels`、`cumulative(monthly)`。

## 不做
- 媒体墙按单个会话筛选（会话内共享媒体已覆盖）、批量操作、统计导出、统计缓存、按入库时间统计。

## 测试
- Go：ListAllMedia 类型 / 来源 / 翻页 / 删除 / 错误参数 / 查询计划；Stats 各口径（去重、删除排除、时区分桶、相册算一次）；扫描计数（单帖、相册跨批、重复帖不重复计）；迁移 v7→v8；HTTP 参数校验。
- Web：路由；justify 纯函数；MediaWall 加载 / 筛选 / 翻页 / 打开浏览器 / media.updated / 删除；浏览器 wall 模式翻页；统计工具函数；StatsView 渲染与跳转。
