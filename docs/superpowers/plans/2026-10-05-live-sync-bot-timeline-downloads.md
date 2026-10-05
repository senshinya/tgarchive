# 切回补数据 · 按 bot 合并时间线 · 下载进度 实施计划

**Goal:** 实现 spec 中三项功能并以 v0.3.0 上线。
**Spec:** `docs/superpowers/specs/2026-10-05-live-sync-bot-timeline-downloads-design.md`
**执行方式:** 本会话内顺序实现（native），收尾由一个独立 reviewer 审整个分支。

## Global Constraints
- Go：modernc sqlite 单连接，持有 Rows 时不得再发查询
- 前端：Preact + signals；链接经 `safeHref`；不用 `innerHTML`；`localStorage` 读写包 try/catch
- 每个任务 TDD：先写失败测试，再实现；`go test ./...`、`npm test`、`npm run build` 全绿后提交
- 提交信息尾行 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`

## Review Focus
1. 后台冻结后连接「看似 OPEN 实则死亡」——看门狗与前台恢复必须强制重连，而不是等浏览器 onerror
2. 合并时间线收到属于新会话（会话列表里尚无）的消息——必须先重载会话再归档，不能丢
3. bot temp 认领在并发 4 路下载时不得把别人的临时文件认成自己的
4. 进度 tick 停止时必须发一次空表，否则前端进度圈永远停在最后的值
5. 合并时间线的相册补齐与回复解析不得跨会话串数据

## Tasks

### Task 1：心跳事件与切回补数据
- `internal/httpapi/read.go`：建立连接与每 25s 发 `event: ping`；测试读到 ping 事件
- `web/src/lib/sse.ts`：`ping` 进 EVENT 监听；`lastSeen`；15s 看门狗（>60s 强制重连 + resync）；`visibilitychange/pageshow/online`（>30s 强制重连 + resync）；测试用假定时器 + 假 EventSource + 假 document/window 事件
- `web/src/state/store.ts`：`resync` 向 `onEvent` 订阅者广播 `{type:'resync'}`；`ArticleReader` 收到后重拉；测试

### Task 2：bot 范围查询与接口
- `internal/store/query.go`：引入 scope；`ListBotMessages`、`ListBotMedia`；hydrate 回复按每条 `ChatID`
- `internal/httpapi`：`GET /api/bots/{id}/messages`、`GET /api/bots/{id}/media`
- 测试：两会话交错分页、相册补齐限同会话、回复解析、media type 过滤、参数校验

### Task 3：前端合并时间线
- `api/client.ts`：`botMessages`、`botMedia`；store 以会话键分派（负键 = bot）
- `lib/router.ts`：`bot`、`bot-article` 路由，`routeConvKey`
- `lib/grouping.ts`：按 `chat_id` 切组
- 左栏模式切换 + 持久化；bot 行；中栏顶栏；气泡发送人名字与组头像；查看器 / 共享媒体 / 文章以会话键工作
- store 事件双写（会话 + bot 时间线），未知会话先重载会话列表
- 测试覆盖上述各点

### Task 4：下载进度后台
- `internal/downloader/progress.go`：`Tracker`、`WithProgress`、`Report`、`CountingWriter`
- `Process` 登记/注销；`Run` 1s tick 调 `onProgress`（含收尾空表）
- web、mt 源用 `CountingWriter`；bot 源 temp 认领（全局闸门、快照、认领集、10s 超时）
- `internal/store`：`DownloadInfo`（媒体所属消息/会话、排队统计、失败列表）
- `internal/app`：SSE `download.progress`；`GET /api/downloads`
- 测试：Tracker、节流、各源上报、认领各分支、接口

### Task 5：下载进度前端
- `store.downloads`；`download.progress` 处理；summary 刷新策略（启动、resync、media.updated 去抖、未知 id 去抖）
- `ui/ProgressRing`；`MediaStatus`、文件行、文章媒体占位显示进度 / 排队中
- 左栏下载按钮（总进度环、失败红点）；`/downloads` 面板（汇总、进行中、失败重试、跳转、空态）
- 测试覆盖

### Task 6：验收与发布
- mock 服务器加：多发送人数据、模拟下载进度；桌面 + 390×844 浅/深色 + 800px 截图验收；手机实机后台→前台
- 独立 reviewer 审分支 → 修复 → 合并 main → tag v0.3.0 → CI 镜像 → 部署 → 线上冒烟（含 bot temp 进度实测）→ 更新基础设施文档
