# 媒体墙与统计面板 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans. Steps use checkbox syntax.

**Goal:** 全局媒体墙（等高行 + 大图浏览器全局翻页）与统计面板（概览、热力图、增长、排行、媒体构成、监听命中率）。

**Architecture:** 后端在 store 加 `ListAllMedia` / `Stats` 与监听扫描计数（迁移 0008），httpapi 加 `GET /api/media`、`GET /api/stats`；前端加 `/media`、`/stats` 两个中间栏视图，浏览器新增 wall 目标。

**Tech Stack:** Go + modernc SQLite；Preact + signals + vitest；SCSS。

**Spec:** docs/superpowers/specs/2026-10-07-media-wall-stats-design.md

## Global Constraints
- 单连接 store：持有 Rows 时不得再查询。
- 前端格式：prettier `--single-quote --print-width 140`，只格式化新文件；改动旧文件手工匹配风格。
- 中文界面文案；手机断点 `(max-width: 925px)`。
- 每个功能 RED→GREEN；提交尾注 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`。

## Review Focus
1. 媒体墙查询在大量消息下的计划（ORDER BY 不走临时 B 树）。
2. 媒体被多条消息引用时统计去重；已删除消息不计入。
3. 时区分桶：tz 偏移跨日边界的消息落在正确的日子。
4. 相册跨两次轮询时 scanned 不重复计数；重复帖（已在 pending）不计数。
5. 浏览器 wall 模式翻页沿用同一筛选；墙上切换筛选后再打开不串数据。

---

### Task 1: store.ListAllMedia
**Files:** `internal/store/query.go`, `internal/store/query_test.go`
- Produces: `func (s *Store) ListAllMedia(ctx, typ, source string, beforeID int64, limit int) ([]MessageView, error)`；类型 all/photo/video，来源 all/private/channel，其余 `ErrBadMediaType`。
- [ ] 测试：私聊照片、频道视频、GIF、文档、已删除照片；断言 all/photo/video × all/private/channel 结果与倒序、before 翻页、坏参数；`EXPLAIN QUERY PLAN` 不含 `TEMP B-TREE`。
- [ ] 跑测试见 FAIL → 实现（`messages m` 主键倒序 + `EXISTS (message_media mm JOIN media md ... role='main' AND md.kind IN (...))` + 来源 `EXISTS chats c kind=?`）→ PASS → 提交。

### Task 2: 监听扫描计数（迁移 0008）
**Files:** `internal/store/migrations/0008_watch_scan.sql`, `internal/store/watches.go`, `internal/store/watches_test.go`, `internal/store/store_test.go`
- Produces: `Watch.Scanned`, `Watch.ScanHits`（json `scanned`, `scan_hits`）；AddPending 累计 scanned；AddWatchHit 累计 scan_hits。
- [ ] 测试：单帖 2 条 → 2；相册 3 帖同 grouped_id → 1；再一批同相册第 4 帖 → 不加；重复插入已 pending 的帖 → 不加；AddWatchHit → hits 与 scan_hits 都 +1；user_version = 8。
- [ ] FAIL → 实现 → PASS → 提交。

### Task 3: store.Stats
**Files:** `internal/store/stats.go`, `internal/store/stats_test.go`
- Produces:
  ```go
  type Stats struct { Totals StatsTotals; Daily []DayCount; Monthly []MonthStat; TopChats []ChatStat; MediaKinds []KindStat; MediaStates map[string]int64; Watches []WatchStat }
  func (s *Store) Stats(ctx context.Context, tzMinutes int, now int64) (*Stats, error)
  ```
  JSON 字段名按 spec；Totals 不含 disk（由 httpapi 填）；`DBBytes` = page_count × page_size。
- [ ] 测试：同一媒体被两条消息引用只算一次；删除消息不计；daily 时区 +480 把 UTC 23:00 归到次日；monthly 增量；top 排序与上限；media_states 四键都在；watch daily 相册算一次，30 天外不计。
- [ ] FAIL → 实现 → PASS → 提交。

### Task 4: HTTP 接口
**Files:** `internal/httpapi/media.go`(新), `internal/httpapi/stats.go`(新), `internal/httpapi/server.go`, 测试 `media_test.go`, `stats_test.go`
- `GET /api/media?type=&source=&before=&limit=`（默认 type=all source=all limit=60，上限 100）；`GET /api/stats?tz=`（±840，默认 0），填 `disk_free/disk_total`（`syscall.Statfs(Cfg.DataDir)`，失败为 0）。
- [ ] 测试参数校验 400、正常 200 结构；FAIL → 实现 → PASS → 提交。

### Task 5: 前端 API、路由与入口
**Files:** `web/src/api/{client,types}.ts`, `web/src/lib/router.ts`, `web/src/App.tsx`, `web/src/components/left/ChatsPanel.tsx`, `web/src/test/fixtures.ts`, 相关测试
- Api 新增 `allMedia(type, source, before, limit)`、`stats(tz)`；类型 `WallType`, `WallSource`, `Stats` 等；Watch 加 `scanned`, `scan_hits`。
- 路由 `media` → `/media`、`stats` → `/stats`；App 中间栏按路由渲染 MediaWall / StatsView，手机不开左栏；左栏头部两个按钮（Images、BarChart3）。
- [ ] 路由与按钮测试 FAIL → 实现 → PASS → 提交。

### Task 6: 等高行布局纯函数
**Files:** `web/src/lib/justify.ts`, `web/src/lib/justify.test.ts`
- Produces: `justify(items: {w:number;h:number}[], width: number, targetH: number, gap: number): { start: number; count: number; height: number }[]`。
- [ ] 测试：缺尺寸按 1:1；极端比例夹取；满行高度使行宽恰等于 width（含 gap，误差 <1px）；最后一行用 targetH；width 0 时每项单独一行。FAIL → 实现 → PASS → 提交。

### Task 7: MediaWall 组件
**Files:** `web/src/components/wall/{MediaWall.tsx,wall.scss,wall.test.tsx}`
- 筛选 Tabs、按月分组、justify 排版、吸顶月份标题、滚动加载、空状态、错误重试、media.updated 刷新、message.deleted 移除、返回按钮（手机）。
- 打开浏览器：`store.viewer.value = { wall: { type, source }, seed: toViewerItems(items), mediaId }`。
- [ ] 测试：首屏加载调用参数；切筛选重载；滚动加载 before；点击设置 viewer 目标；media.updated 替换；删除移除。FAIL → 实现 → PASS → 提交。

### Task 8: 浏览器 wall 模式
**Files:** `web/src/state/store.ts`, `web/src/components/viewer/MediaViewer.tsx`, `MediaViewer.test.tsx`
- ViewerTarget 新增 `{ wall: {type, source}; seed: ViewerItem[]; mediaId: number }`；翻页调 `api.allMedia(type, source, before, VIEWER_PAGE)`；key 含筛选。
- [ ] 测试：wall 目标以 seed 起步，向更早翻页带同一筛选与最早 id。FAIL → 实现 → PASS → 提交。

### Task 9: 统计工具函数
**Files:** `web/src/lib/stats.ts`, `web/src/lib/stats.test.ts`；`formatBytes` 放 `lib/format.ts`
- `formatBytes(n)`、`heatmapGrid(daily, todayISO)`（53 列，周日起，返回列×7 的 `{day,count}|null`）、`quantileLevels(counts)`→`(n)=>0..4`、`cumulative(monthly)`。
- [ ] FAIL → 实现 → PASS → 提交。

### Task 10: StatsView 组件
**Files:** `web/src/components/stats/{StatsView.tsx,stats.scss,stats.test.tsx}`
- 六个区块按 spec；刷新按钮；排行点击跳会话；命中率「—」与「自 v0.8.0 起统计」。
- [ ] 测试：加载调用 tz、渲染概览数字、热力图格数、排行点击导航、命中率显示。FAIL → 实现 → PASS → 提交。

### Task 11: 文档与收尾
- README 加一段；全量 `go test ./...`、`npm test`、`npx tsc --noEmit`、`npm run build`。
