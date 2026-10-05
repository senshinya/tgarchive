# 切回补数据 · 按 bot 合并时间线 · 下载进度 设计

> 状态：已确认（2026-10-05）。上位 spec：`2026-10-04-tgarchive-design.md`、`2026-10-05-telegraph-archive-design.md`；本文只描述新增部分。

## 1. 目标

1. **切回补数据**：页面在后台被冻结（手机切走、锁屏、休眠）后回到前台，自动发现推送连接已失效并重连、补齐期间漏掉的数据。
2. **按 bot 合并时间线**（可选视图）：左栏可切换「按人 / 按 bot」；按 bot 时每个 bot 一个会话，时间线合并该 bot 下全部发送人的消息，每组消息标出发送人。
3. **下载进度**：消息里每个待下载媒体显示进度圈与「已下 / 总大小」；左栏顶部下载图标显示总进度，点开「下载」面板汇总速度、剩余、排队、进行中与失败项。

### 成功标准
- 手机把页面放后台 ≥5 分钟，期间发消息给 bot；切回后 ≤3 秒内新消息出现，无需手动刷新
- 只有 1 个 bot 时也能切到「按 bot」，合并时间线可滚动加载历史、实时收新消息、打开共享媒体与文章
- 发一个大视频给 bot：气泡与下载面板中进度随时间增长（bot 渠道若实测无法取得进度，退化为「下载中 + 总大小」）
- 下载失败项可在面板中重试；点击条目跳到对应消息

### 明确不做
- 断点续传（重启后仍从头下载）
- 下载暂停 / 取消 / 调整优先级
- 合并时间线中的跨会话回复引用（回复仍只在同一会话内解析）

## 2. 切回补数据

### 服务端
`GET /api/events` 心跳由 SSE 注释改为具名事件：每 25s 发 `event: ping\ndata: {}\n\n`（连接建立时也立即发一次）。

### 客户端（`lib/sse.ts`）
- 订阅 `ping`；任意事件（含 ping）刷新 `lastSeen`
- **看门狗**：每 15s 检查，`now - lastSeen > 60s` → 关闭当前连接、立即重开，重开成功后 `onResync`
- **前台恢复**：`visibilitychange`（变为 visible）、`pageshow`（`persisted`）、`online` 时，若距上次可见/上次事件 > 30s → 同上强制重连 + 补数据
- 原有「浏览器放弃（CLOSED）后每 5s 重试」与「一次断线只弹一次提示」保留

### 补数据范围（`store.resync`）
bot 列表、会话列表、所有已加载的会话（含合并时间线）、下载快照；并向 `onEvent` 订阅者广播一条合成事件 `{type:'resync'}`，文章阅读页据此重新拉取。

## 3. 按 bot 合并时间线

### 服务端
- `GET /api/bots/{id}/messages?before=&limit=`：该 bot 全部会话的未删除消息，按 `id` 倒序分页、返回正序；与会话分页同样不截断相册（补齐最老一条所在相册，限同一会话）
- `GET /api/bots/{id}/media?type=&before=&limit=`：同 `/api/chats/{id}/media`，范围换成该 bot 全部会话
- Store 内部把「会话范围」抽象为 scope（`chat_id = ?` 或 `chat_id IN (SELECT id FROM chats WHERE bot_id = ?)`），两类接口共用查询与 hydrate；hydrate 的回复解析按每条消息自己的 `chat_id`

### 客户端
- **会话键**：`conversations` 仍以数字为键，正数 = 会话 id，负数 = `-botId`（合并时间线）。`api.messages` / `api.chatMedia` 的调用统一经 `convKey` 分派到会话或 bot 接口
- **路由**：新增 `/bot/:id` 与 `/bot/:id/article/:mid`；`routeConvKey(r)` 对 bot 路由返回 `-botId`
- **左栏**：标题栏加「按人 / 按 bot」切换（`localStorage` 键 `tgarchive.listMode`，读写包 try/catch，默认按人）。按 bot 模式隐藏 BotTabs，每个 bot 一行：bot 头像与名字、最后一条消息（「发送人: 预览」）与时间，均由已加载的会话列表在前端算出
- **时间线**：分组在时间间隔之外再按 `chat_id` 切分；按 bot 模式下每组首个气泡上方显示发送人名字（发送人配色），组旁显示发送人头像（Web A 群聊样式）。顶栏显示 bot 头像、名字与「N 位发送人」
- **实时**：`message.*` 事件除更新会话键外，同时更新 `-chat.bot_id` 的时间线（已加载时）；事件涉及未知会话时先重载会话列表再归档
- **查看器 / 共享媒体 / 文章**：以会话键工作；负键走 bot 接口；文章阅读页返回时回到 `/bot/:id`

## 4. 下载进度（服务端）

### 进度表（`internal/downloader/progress.go`）
- `Tracker`：内存 `map[mediaID]{done, total, startedAt}`，互斥保护；累计字节计数用于测速（5s 滑动窗口）
- `WithProgress(ctx, fn)` / `Report(ctx, done, total)`：数据源通过 ctx 上报，无上报函数时为空操作
- `CountingWriter(ctx, w, total)`：包装写入目标，每次写入上报（上报只是一次加锁赋值，无需节流；推送频率由 1s tick 决定）
- `Process` 开始时登记、结束（任何结果）时注销
- `Run` 内每 1s：若有进行中项，或上一 tick 有而本 tick 无（发一次空表收尾），调用 `onProgress(snapshot)`

### 各数据源
- `web`：`total = Content-Length`（未知为 0），`io.Copy` 目标换成 `CountingWriter`
- `mt`：`total = media.size`，gotd `Stream` 目标换成 `CountingWriter`
- `bot`：本地 Bot API 无进度接口，从其 `temp` 目录推断：
  - 全局认领闸门：调用 `getFile` 前加锁并快照 `<botapi>/*/temp/` 下的文件名；`getFile` 在后台进行，每 500ms 扫描一次，出现的第一个「不在快照中、未被其他下载认领」的文件即归本次下载，认领后放闸
  - `getFile` 返回或 10s 内未出现新文件 → 放闸，本次只有「下载中」无字节进度
  - 认领后每 1s 读该文件大小上报，`total = media.size`；文件消失忽略
  - 依据：TDLib `FileLoaderUtils.cpp` 的 `open_temp_file` 把下载中的文件建在 `get_files_temp_dir` = `<files_dir>/temp/`，完成后 `create_from_temp` 改名移入类型目录；本地 Bot API 的 files_dir 即 `<dir>/<bot token>/`。上线后用真实大文件冒烟确认

### 推送与接口
- SSE `download.progress`：`{items:[{media_id, done, total}], speed}`（bytes/s）
- `GET /api/downloads`：
  ```json
  {
    "active":  [{"media_id","message_id","chat_id","kind","file_name","done","total","started_at"}],
    "queued":  {"count", "bytes"},
    "failed":  [{"media_id","message_id","chat_id","kind","file_name","size","error"}],
    "speed": 0
  }
  ```
  `queued` = `state='pending'` 中不在进行中的项；`failed` 最近 50 条（按 id 倒序）；`message_id/chat_id` 取该媒体关联的任一未删除消息

## 5. 下载进度（客户端）

- `store.downloads`：`{active: Map<mediaId, {done,total}>, speed, summary}`；`summary` 来自 `/api/downloads`，在启动、resync、`media.updated`（1s 去抖）以及 tick 中出现未知 media_id（1s 去抖）时刷新
- **媒体占位**（`MediaStatus`、文件行、文章媒体占位）：进行中 → 进度圈（`total>0` 时为确定进度，否则不确定）+「已下 / 总大小」；pending 但不在进行中 →「排队中」
- **左栏下载按钮**（标题栏右侧）：有进行中项时外圈显示 Σdone/Σtotal；有失败项时右上角红点；无任何任务时不显示
- **下载面板**（左栏覆盖层，与设置同级的视图，窄屏全屏；路由 `/downloads`）：
  - 顶部汇总：速度、剩余（Σ(total−done) + 排队字节）、排队数
  - 「进行中」：缩略图/类型图标、文件名或类型、发送人、进度条、已下/总大小；点击跳到所属消息
  - 「失败」：原因、重试按钮（`/api/media/{id}/retry`）、点击跳转
  - 空态：「没有进行中的下载」

## 6. 测试

- Go：scope 查询（bot 范围分页、相册补齐、跨会话回复解析）、两个新接口、`Tracker`（登记/注销/测速/空表收尾）、`CountingWriter` 节流、web/mt 源上报、bot temp 认领（快照外新文件、他人已认领、超时、getFile 先返回）、`/api/downloads`、ping 事件
- 前端：`sse` 看门狗与前台恢复（假定时器 + 假 EventSource）、resync 广播、路由解析、会话键分派、合并时间线分组与发送人标注、事件双写、左栏模式切换与持久化、进度圈各状态、下载按钮与面板（汇总、跳转、重试、空态）
- 验收（分支构建、mock 服务器）：桌面 + 390×844 浅/深色 + 800px；手机专测后台 → 前台补数据、合并时间线、下载面板

## 7. 错误处理

| 情况 | 行为 |
|---|---|
| `/api/downloads` 失败 | 保留上次 summary，不弹提示（resync 时会重试） |
| bot temp 目录不存在 / 无权限 | 认领机制静默跳过，只报「下载中」 |
| 进度事件中的媒体已不在 summary | 去抖刷新 summary |
| 合并时间线 bot 已删除 | 显示「暂无消息」，左栏不再列出该 bot |
| `localStorage` 不可用 | 列表模式退回默认「按人」 |
