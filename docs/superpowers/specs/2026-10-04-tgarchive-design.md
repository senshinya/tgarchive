# tgarchive 设计

日期：2026-10-04
状态：待审阅

## 1. 目标与范围

集中管理多个 Telegram 机器人，把白名单用户私聊发给机器人的消息（文本、图片、视频、文件等全部类型）自动存档到服务器，并提供与 Telegram Web A 视觉完全一致的 WebUI 浏览存档。对受保护（禁止转发）的群组/频道消息，支持贴消息链接、由用户账号（userbot）代取存档。

### 使用场景

- 发送者是站长本人和少数信任的人（按机器人白名单）
- 主要用法：把看到的内容转发给机器人收藏；受保护内容改为发送消息链接
- WebUI 仅站长本人使用（Authelia 保护），只读浏览 + 删除存档 + 机器人管理

### 成功标准

- 白名单内发来的各类消息 100% 入库，媒体全部落盘（本地 Bot API `--local` 下载无大小限制；userbot 按账号上限）
- 发送者通过 reaction 即可知道存档状态（👀 处理中 → 👌 完成）
- WebUI 与 Web A 并排截图对比无可辨差异（布局、气泡、配色、图标、动画、深浅色、移动端）
- 进程重启不丢消息、不重复入库

### 明确不做

全文搜索、群组/频道整体存档、通过 WebUI 发消息、多用户权限、编辑历史版本、多个 userbot 账号、批量拉取历史消息。

## 2. 部署形态

| 项 | 值 |
|---|---|
| 域名 | `tg.shinya.click`，CF SaaS + 华为四线解析 |
| 源码 | `git@ssh.git.shinya.click:shinya/tgarchive.git`，本地 `~/Downloads/tgarchive` |
| Stack | `/opt/stacks/tgarchive`，compose `build: .` |
| 数据 | `/opt/app/tgarchive/{db,media,botapi,avatars}` |
| 许可 | GPL-3.0（移植了 Telegram Web A 的样式与组件代码） |

### 容器

单容器 `tgarchive`：自建镜像，接 `web`，监听 8080，挂 `/opt/app/tgarchive` 到 `/data`。

- 应用进程托管 Telegram 官方 `telegram-bot-api` 本地服务器作为子进程（`internal/botapiserver`）：参数 `--local --dir=/data/botapi --temp-dir=/data/botapi-tmp --http-ip-address=127.0.0.1 --http-port=8081`，只监听回环地址，不对外暴露
- 子进程所需的 `api_id` / `api_hash` 在 WebUI 设置页填写，加密存入 `settings` 表；未配置时子进程不启动，状态为 `unconfigured`，添加机器人接口返回 409。保存新凭据后子进程以新凭据重启；子进程异常退出按 1s 起指数退避重启，上限 30s
- 子进程环境不继承应用环境变量，看不到 `TOKEN_ENC_KEY`
- 子进程与应用共用 `/data/botapi`，`getFile` 返回的绝对路径可直接读取
- `BOT_API_MANAGED=false` 时不托管子进程，改连 `BOT_API_URL` 指向的外部 Bot API 服务器（开发与测试用）

### 镜像构建

多阶段 Dockerfile：Node 阶段构建前端 → Go 阶段 `go test ./...` + 构建（`go:embed` 前端产物，`CGO_ENABLED=0`）→ 运行阶段同时包含应用二进制与 `telegram-bot-api` 二进制。本地镜像前缀 `tgarchive` 加入 Cup exclude；`telegram-bot-api` 的版本随镜像构建 pin。

### .env（mode 600，台账存 Vaultwarden）

- `TOKEN_ENC_KEY`（必填）：32 字节 hex，AES-256-GCM 主密钥，加密 bot token、userbot session 与 `settings` 表中的 `api_id` / `api_hash`；缺失或格式错误启动即失败
- `BARK_NOTIFY_FILE=/run/bark/notify.json`（可选，只读挂载 `/opt/app/bark/notify.json`；为空不推送）
- `MEDIA_MAX_BYTES`（可选单文件上限，默认 0 = 不限）
- `BOT_API_MANAGED`（可选，默认 `true`）
- `REQUIRE_FORWARD_AUTH` 默认 `true`（fail-safe），生产不设置

`api_id` / `api_hash`（my.telegram.org 申请，本地 Bot API 与 userbot 共用）不进 `.env`，在 WebUI 设置页填写。

### Caddy

- 整站 `forward_auth` Authelia；`request_header -Remote-User -Remote-Groups -Remote-Name -Remote-Email` 剥入站伪造头
- `import compress`，但 `/media/*` 不压缩（二进制 + Range）
- `/api/events`（SSE）`flush_interval -1`
- 应用侧在 `REQUIRE_FORWARD_AUTH=true` 时对除 `/healthz` 外的所有请求校验 `Remote-User` 存在，否则 401
- 应用侧 CSRF 兜底：GET/HEAD/OPTIONS 以外的请求，带 `Sec-Fetch-Site` 且不为 `same-origin` 时 403；POST/PUT/PATCH 带请求体而 `Content-Type` 不是 `application/json` 时 403

### 备份

- R2（restic）：备份 `db/`、`avatars/` 与 stack；`media/`、`botapi/`、`botapi-tmp/` 加入 `exclude.txt`
- NAS：rclone 定期拉取 `media/`，方式同音乐库

### 通知

机器人或 userbot 进入 `error` 状态时推送 Bark `docker` 分组（timeSensitive），凭据读 `notify.json`。

## 3. 架构

```
Telegram Bot API                                Telegram MTProto
      ⇅                                               │
┌──────────────────────── tgarchive 容器 ─────────────┼────────────┐
│ telegram-bot-api --local（子进程，127.0.0.1:8081）   │            │
│      │ getUpdates long polling                       ▼            │
│      ▼                                                            │
│ ┌──────────────────────── tgarchive (Go) ──────────────────────┐ │
│ │ botapiserver 托管 telegram-bot-api 子进程（崩溃退避重启）      │ │
│ │ tgapp        api_id / api_hash 加密存取（settings 表）         │ │
│ │ collector    每 bot 一个 worker                                │ │
│ │ userbot      gotd/td 客户端 + 串行任务队列                     │ │
│ │ convert      Bot API / MTProto → 统一内部消息模型               │ │
│ │ downloader   全局下载队列（并发 4）+ 重试状态机                  │ │
│ │ receipt      reaction / 失败回复                               │ │
│ │ store        SQLite（modernc.org/sqlite）+ 迁移                │ │
│ │ api          /api/*、/api/admin/*、/api/events (SSE)、/media/*  │ │
│ │ web          go:embed 的 Preact SPA                            │ │
│ └────────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────┘
```

### Go 包划分

| 包 | 职责 | 依赖 |
|---|---|---|
| `internal/store` | schema、迁移、全部 SQL 读写 | sqlite |
| `internal/seal` | AES-256-GCM 加解密 | — |
| `internal/tgapp` | `api_id` / `api_hash` 校验与加密存取（`settings` 表） | store, seal |
| `internal/botapiserver` | 托管 `telegram-bot-api` 子进程：按凭据启动、换凭据重启、崩溃退避重启、状态上报 | tgapp |
| `internal/tgbot` | 最小 Bot API HTTP 客户端，错误信息中 token 一律打码 | — |
| `internal/model` | 统一消息模型（kind、text、entities、media 元数据、origin） | — |
| `internal/convert/botapi` | Bot API Message → model | model |
| `internal/convert/mtproto` | gotd tg.Message → model | model |
| `internal/collector` | bot worker 生命周期、long polling、白名单、offset | store, convert/botapi, downloader, receipt |
| `internal/userbot` | 登录流程、session 存储、链接解析、代取任务队列 | store, convert/mtproto, downloader, receipt |
| `internal/linkparse` | t.me 消息链接解析（纯函数） | — |
| `internal/downloader` | 媒体下载队列、去重、落盘、引用计数、重试 | store |
| `internal/receipt` | reaction 与失败回复 | — |
| `internal/notify` | Bark 推送 | — |
| `internal/httpapi` | HTTP 路由、鉴权与 CSRF 中间件、SSE、媒体 Range 服务 | store, collector, userbot, tgapp, botapiserver |
| `web/` | Preact SPA 源码 | — |

Telegram Bot API 客户端为自写的 `internal/tgbot`（标准库 `net/http`，指向本地服务器）；MTProto 使用 `github.com/gotd/td`。

## 4. 数据模型（SQLite）

时间统一存 Unix 秒（UTC），前端按浏览器时区显示。

```sql
bots(
  id INTEGER PK, tg_bot_id INTEGER UNIQUE, username TEXT, name TEXT,
  avatar_path TEXT, token_enc BLOB, enabled INTEGER,
  status TEXT,            -- running / error / stopped
  last_error TEXT, update_offset INTEGER, created_at INTEGER)

whitelist(
  bot_id INTEGER, tg_user_id INTEGER, note TEXT,
  can_fetch INTEGER DEFAULT 0,   -- 是否允许触发 userbot 代取
  PRIMARY KEY(bot_id, tg_user_id))

rejected(                        -- 最近被拒绝的发送者，管理页一键加白
  bot_id INTEGER, tg_user_id INTEGER, first_name TEXT, username TEXT,
  last_seen_at INTEGER, count INTEGER, PRIMARY KEY(bot_id, tg_user_id))

senders(
  tg_user_id INTEGER PK, first_name TEXT, last_name TEXT, username TEXT,
  avatar_path TEXT, updated_at INTEGER)

chats(                           -- 会话 = 机器人 × 发送者
  id INTEGER PK, bot_id INTEGER, sender_id INTEGER, last_message_at INTEGER,
  UNIQUE(bot_id, sender_id))

messages(
  id INTEGER PK, chat_id INTEGER, tg_message_id INTEGER,
  source TEXT,                   -- bot_update / userbot_fetch
  media_group_id TEXT, date INTEGER, edit_date INTEGER,
  kind TEXT,                     -- text/photo/video/animation/voice/audio/document/
                                 -- sticker/video_note/location/venue/contact/poll/dice/other
  text TEXT, entities_json TEXT,
  forward_origin_json TEXT,      -- Bot API forward_origin 归一化后
  reply_to_tg_message_id INTEGER,
  origin_chat_id INTEGER, origin_chat_title TEXT, origin_link TEXT,  -- userbot 代取
  extra_json TEXT,               -- 位置/联系人/投票等结构化附加数据
  raw_format TEXT,               -- botapi / mtproto
  raw_json TEXT,
  deleted_at INTEGER,
  UNIQUE(chat_id, source, origin_chat_id, tg_message_id))  -- bot_update 的 origin_chat_id 为 0

media(
  id INTEGER PK,
  dedupe_key TEXT,               -- bot:<file_unique_id>；mt:photo:<id>[:<size>]、mt:doc:<id>[:thumb]
  kind TEXT, mime TEXT, file_name TEXT, size INTEGER,
  width INTEGER, height INTEGER, duration INTEGER,
  waveform BLOB,                 -- 语音波形（5-bit packed，与 TG 一致）
  path TEXT, thumb_path TEXT,
  state TEXT,                    -- pending / done / failed / too_large
  attempts INTEGER, next_attempt_at INTEGER, error TEXT,
  UNIQUE(dedupe_key))

message_media(message_id INTEGER, media_id INTEGER, role TEXT,  -- main / thumb
  PRIMARY KEY(message_id, media_id, role))

userbot(
  id INTEGER PK CHECK(id = 1), phone TEXT, tg_user_id INTEGER, name TEXT,
  session_enc BLOB, status TEXT, last_error TEXT, updated_at INTEGER)

settings(                        -- 键值配置；telegram_app = 加密的 {api_id, api_hash}
  key TEXT PK, value BLOB, updated_at INTEGER)

userbot_peers(                   -- 账号可见频道的 access_hash 缓存（私有链接用）
  channel_id INTEGER PK, access_hash INTEGER, username TEXT, title TEXT, updated_at INTEGER)

fetch_jobs(                      -- 代取任务；一条链接消息一个任务
  id INTEGER PK, bot_id INTEGER, sender_id INTEGER, link_tg_message_id INTEGER, link TEXT,
  state TEXT,                    -- queued / fetching / fetched / failed / unsupported
  error TEXT, receipt TEXT, created_at INTEGER, updated_at INTEGER,
  UNIQUE(bot_id, sender_id, link_tg_message_id))

fetch_job_messages(job_id INTEGER, message_id INTEGER, PRIMARY KEY(job_id, message_id))
```

说明：

- caption 与 text 统一存入 `text` / `entities_json`（TG 本身渲染上无差别）
- `media` 与消息多对多，按 `dedupe_key` 去重；删除存档时，媒体无其他未删除消息引用才删文件与记录
- `raw_json` 全量保存，供日后补充渲染器；未识别类型 `kind=other`，前端显示「不支持的消息类型」气泡
- 迁移：内嵌 SQL 文件，启动时按 `PRAGMA user_version` 顺序执行；WAL 模式

媒体路径：`media/<bot_id>/<yyyy>/<mm>/<sha1(dedupe_key)>.<ext>`；缩略图同目录 `.thumb.jpg`。头像：`avatars/{bots,senders}/<tg_id>.jpg`。userbot 代取的媒体在 `media/mt/<yyyy>/<mm>/`。代取消息的 `date` 为原帖时间，会话排序（`last_message_at`）取入库时间。

## 5. Bot 采集流程

### Worker

1. `getUpdates`（timeout 50s，`allowed_updates=["message","edited_message"]`）
2. 每个 update 在一个事务内处理完成后持久化 `update_offset = update_id + 1`
3. 非私聊消息忽略；发送者不在白名单 → 写/更新 `rejected`，不回复，丢弃
4. upsert `senders`、`chats`；转换为统一模型写 `messages`；为每个媒体（含视频缩略图）upsert `media` 并关联，新媒体 `state=pending`
5. 新消息（非编辑）先交给 `LinkHandler`（userbot 代取，见 §6）：返回 `handled=true` → 推进 offset，本条消息不作为普通文本存档；返回错误 → 不推进 offset，worker 从已持久化的 offset 重新拉取，同一 update 会再次投递
6. 推 SSE 事件 `message.created`
7. `edited_message`：更新 text/entities/edit_date/媒体，推 `message.updated`

### 下载队列

- 全局并发 4；`getFile` 得到本地绝对路径 → 硬链接到归档路径（同文件系统），失败则拷贝 → 删除 botapi 侧原文件
- 失败重试间隔 1min / 5min / 30min，3 次后 `failed`；WebUI 可手动重试（重置 attempts）
- 本地 Bot API `--local` 模式下载无大小限制；可选 `MEDIA_MAX_BYTES`（默认 0 = 不限）作为磁盘保护，超出 → `too_large`，仅存元数据
- 每日清理 `botapi/` 下 mtime 超过 24h 的残留文件
- 已 `done` 的 `dedupe_key` 再次出现时直接关联，不重复下载

### 回执

| 情况 | 动作 |
|---|---|
| 纯文本入库 | 设 👌 |
| 含媒体入库 | 设 👀；全部媒体 `done` 后改 👌 |
| 任一媒体最终 `failed` | 保留 👀，回复「⚠️ 存档失败：<原因>」 |
| 存在 `too_large` | 其余完成后设 👌，回复「文件超过存档上限，仅保存了消息记录」 |

失败原因中的 bot token（`<bot_id>:<secret>`，本地 Bot API 文件路径含此段）一律替换为 `<bot>` 后再写入 `media.error`、日志与回复。每次 reaction / 回复调用超时 30s。回执失败（如消息已被删）只记日志，不影响存档。

### 生命周期

- 添加：`getMe`（对云端 api.telegram.org）校验 token → 云端 `logOut`（已登出视为成功）→ 存库 → 启动 worker；接口逐步返回结果，前端分步展示
- 401 / 409 Conflict → 停止 worker，`status=error`，记录 `last_error`，Bark 推送，不自动重试
- 网络错误 → 指数退避，最大 60s
- 启停：仅控制 worker，不 logOut；切换后推 `bot.status`（`stopped`），重新启用的 worker 运行后再推 `running`
- 删除：停止 worker，可选同时删除该机器人全部存档（默认保留）
- 头像：机器人与发送者头像每日刷新一次（`getUserProfilePhotos` 取最新一张）

## 6. 受保护内容：userbot 链接代取

### 登录

userbot 使用的 `api_id` / `api_hash` 从 `internal/tgapp`（`settings` 表）读取，与本地 Bot API 共用，不读环境变量；未配置时拒绝登录。

管理页「用户账号」：手机号 → 验证码 → 二步验证密码（如有）。gotd session 序列化后用 `TOKEN_ENC_KEY` 加密存 `userbot.session_enc`。只支持一个账号；可登出（调用 `auth.logOut` 并清除 session）。

### 链接格式（`internal/linkparse`）

- `https://t.me/<username>/<msg>`
- `https://t.me/c/<channel_id>/<msg>`
- `https://t.me/c/<channel_id>/<topic>/<msg>`、`https://t.me/<username>/<topic>/<msg>`
- 以上均可带 `?single`，`telegram.me` 与无 scheme 形式等价
- 其他 t.me 链接 → 回复「⚠️ 代取失败：不支持的链接格式」，并按普通消息存档（只回复一次）

### 代取流程

`LinkHandler.TryHandle(ctx, botID, sender, msg, canFetch) (handled bool, err error)`：不是可代取的链接或发送者无 `can_fetch` → `(false, nil)`，按普通消息存档；已接手 → `(true, nil)`；暂时无法接手 → 返回错误，该 update 会被重新投递。实现必须按 `(botID, msg.TgMessageID)` 幂等，重复投递不得重复入队或重复回复。

1. 写入 `fetch_jobs`（唯一键保证重投不重复入队）并在原链接消息设 👀，由串行队列处理；进程重启时 `fetching` 的任务重新排队
2. 解析 peer：
   - 公开链接（username）→ `contacts.resolveUsername` 直接取得 access_hash，**无需加入**（等同客户端预览）
   - 私有链接（`c/<id>`）→ 链接只含数字 ID，access_hash 只能从账号自己的对话中取得，**必须是成员**：查 `userbot_peers` 缓存，缺失则遍历一次 `messages.getDialogs`（找到即停）并缓存见到的频道后再查；仍无 → 失败「私有群/频道，代取账号未加入」
3. `channels.getMessages`（私聊/普通群链接不适用，链接格式只覆盖频道与超级群）取消息；若带 `grouped_id` 且未指定 `?single`，再取 `[msg-10, msg+10]`，筛出同组消息
4. 经 `convert/mtproto` 转换，`source=userbot_fetch`，写入「发送者 × 该机器人」会话，`origin_chat_id/title/link` 填写，`tg_message_id` 为原群消息 ID
5. 媒体经 `upload.getFile` 分块流式写入归档路径，复用下载队列的状态机与去重（`dedupe_key` 见 §4）；file reference 过期时重取原消息刷新后重试
6. 全部代取消息的主媒体落定后设 👌；代取失败回复「⚠️ 代取失败：<原因>」；媒体最终失败回复「⚠️ 存档失败：<原因>」

### 限流与失效

- 队列串行，相邻任务间隔 ≥3 秒
- `FLOOD_WAIT_X`：X ≤ 300 秒则等待后重试，否则失败并回复「被限流，请 N 分钟后重试」
- `AUTH_KEY_UNREGISTERED` / `SESSION_REVOKED` → `userbot.status=error`，Bark 推送，待重新登录；期间链接请求回复「代取账号未登录」
- 无 `can_fetch` 权限者发链接 → 按普通文本消息存档

### 风险

userbot 违反 Telegram 使用条款，存在账号受限风险。session 等同该账号完整登录权限，与 `TOKEN_ENC_KEY` 同为最高敏感级。代取内容仅用于个人存档。

## 7. HTTP 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/bots` | 机器人列表（不含 token） |
| GET | `/api/chats?bot_id=` | 会话列表，按 `last_message_at` 倒序 |
| GET | `/api/chats/:id/messages?before=&limit=50` | 游标分页，含媒体与 sender |
| GET | `/api/chats/:id/media?type=media\|file\|link&before=` | 共享媒体 |
| DELETE | `/api/messages/:id` | 删除存档（软删 + 清理孤立媒体） |
| POST | `/api/media/:id/retry` | 重试失败媒体 |
| GET | `/api/events` | SSE：`message.created/updated/deleted`、`media.updated`、`bot.status` |
| GET | `/media/:id` | Range + ETag，`Cache-Control: private, max-age=31536000, immutable`；见下方媒体安全头 |
| GET/POST/PATCH/DELETE | `/api/admin/bots[...]` | 添加（分步结果）、启停、删除 |
| GET/PUT/DELETE | `/api/admin/bots/:id/whitelist[...]` | 白名单与 `can_fetch` |
| GET | `/api/admin/bots/:id/rejected` | 最近被拒绝的发送者 |
| GET | `/api/admin/telegram-app` | `{configured, api_id, server: {managed, state, error}}`，不返回 `api_hash` |
| PUT | `/api/admin/telegram-app` | 保存 `{api_id, api_hash}`（加密入 `settings`），托管模式下以新凭据重启 Bot API 子进程；成功 204，校验失败 400 |
| GET | `/api/admin/userbot` | `{state, phone, name, tg_user_id, error}`；state ∈ unconfigured / connecting / logged_out / code_sent / password_needed / ready / error |
| POST | `/api/admin/userbot/{phone,code,password,logout}` | 登录流程：`{phone}` → `{code}` → `{password}`（如需）；返回最新状态；输入错误 400、步骤错误或未配置 409、未连接 503；logout 204 |

媒体安全头：`/media/*` 一律带 `X-Content-Type-Options: nosniff` 与 `Content-Security-Policy: default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'; sandbox`。仅 `image/jpeg`、`image/png`、`image/gif`、`image/webp`、`video/*`、`audio/*`、`application/x-tgsticker` 内联返回；其余类型（含 SVG、HTML、XHTML、PDF、`text/*`）以 `application/octet-stream` + `Content-Disposition: attachment` 返回。

## 8. WebUI

### 基准

**以 Telegram Web A（`Ajaxy/telegram-tt`，web.telegram.org/a）为唯一像素基准。** 前端使用 Preact + Vite + SCSS。从 Web A 源码移植：SCSS 变量与主题（浅/深）、组件样式与 DOM/class 结构、图标字体、默认壁纸图案、相册拼图算法、动画曲线与时长、日期/时间格式化规则。移植代码在源文件头注明来源路径与上游 commit。

### 布局

- 左栏：顶部机器人切换（头像行 + 「全部」）；会话列表（发送者头像、名字、末条摘要、时间），按最近消息排序；底部齿轮进入管理页
- 右栏：顶栏（发送者名 + 所属机器人，右侧共享媒体按钮）；消息流（默认图案壁纸，全部为接收方左侧气泡，日期分隔条，转发标题，回复引用，时间与「已编辑」）
- 右侧抽屉：共享媒体（媒体 / 文件 / 链接），按月分组
- 移动端：单栏切换，行为同 Web A 窄屏

### 消息渲染

- entities 全量：粗体、斜体、下划线、删除线、剧透、行内代码、代码块（含语言）、链接、text_link、提及、hashtag、cashtag、引用、可展开引用、自定义表情（显示 fallback emoji）
- 图片/视频按比例；视频缩略图 + 时长；GIF 自动循环静音播放
- 媒体查看器：全屏、左右切换（会话内全部媒体）、缩放、下载
- 相册：Web A 拼图算法
- 语音：波形 + 播放器；video_note：圆形播放器
- 文件：图标、名称、大小、下载
- 贴纸：webp 直接显示；`.tgs` 用 lottie；`.webm` 用 video
- 位置/场所、联系人、投票、骰子：对应卡片
- userbot 代取：转发样式标题「来自 <群名> · 受保护」，附原链接
- 媒体状态 `pending` / `failed` / `too_large`：占位 + 状态说明，`failed` 带重试按钮

### 交互

- 向上无限滚动，50 条/页，打开会话定位到最新
- 右键菜单：复制文本、下载、删除存档（二次确认）
- SSE 实时追加/更新
- 主题跟随系统

### 管理页（样式仿 Web A 设置页）

- 机器人：列表 + 状态灯 + 启停 + 添加（分步进度）+ 删除
- 白名单：user ID + 备注 + `can_fetch` 开关；「最近被拒绝」一键加白
- 用户账号：登录状态、登录流程、登出

## 9. 错误处理汇总

| 场景 | 处理 |
|---|---|
| Bot 401 / 409 | worker 停止，status=error，Bark |
| 网络错误 | 指数退避 ≤60s |
| 媒体下载失败 | 3 次重试后 failed，回复发送者，WebUI 可重试 |
| 媒体超过 `MEDIA_MAX_BYTES` | too_large，仅元数据 |
| userbot session 失效 | status=error，Bark，链接请求回复未登录 |
| FLOOD_WAIT | ≤5min 等待，否则失败并告知 |
| 回执失败 | 仅记日志 |
| `TOKEN_ENC_KEY` 缺失/格式错 | 启动 fail-fast |
| `REQUIRE_FORWARD_AUTH=true` 且无 `Remote-User` | 401 |

## 10. 测试

- **Go 单元测试**（构建时执行）：每种消息类型的 Bot API fixture 转换；MTProto fixture 转换；linkparse 全格式与非法输入；白名单与 `can_fetch` 分流；offset 推进与崩溃恢复；去重与引用计数删除；重试状态机；迁移；加解密
- **集成测试**：假 Bot API HTTP 服务器回放录制的 update 序列，覆盖入库 → 下载 → 清理 botapi 缓存 → reaction 全链路；userbot 用接口注入假客户端覆盖代取流程
- **前端单元测试**：entities 渲染、相册拼图（移植 Web A 用例）、日期分隔逻辑

## 11. 验收

1. 两个真实机器人、两个白名单账号，发送：全格式文本、单图、相册、>20MB 视频、语音、圆形视频、文件、贴纸（静态/tgs/webm）、转发频道消息、回复消息 → 回执 👀→👌，落盘正确
2. 非白名单账号发送 → 无回复，出现在「最近被拒绝」
3. 受保护群中对文本、单图、相册、>20MB 视频、文件贴链接 → 代取成功；无 `can_fetch` 账号贴链接 → 仅存为文本
4. 与 Web A 同视口并排截图：会话列表、各类气泡、媒体查看器、共享媒体、深/浅主题、移动端
5. 删除存档后孤立媒体文件被清理；重启容器后不丢不重
6. 吊销 bot token → error + Bark；userbot 在官方客户端踢掉 session → error + Bark
7. 未经 Authelia 直接请求源站带伪造 `Remote-User` → 401

## 12. 基础设施文档变更（上线时）

- CLAUDE.md 服务清单新增 `tg.shinya.click` 行，补凭据指针（`.env` 三项、userbot session 敏感级）
- gotchas.md：`logOut` 与 10 分钟切换限制、409 Conflict、my.telegram.org 申请报错、userbot 风险与破窗（session 失效重登）
- backup.md：`media/`、`botapi/` exclude 与 NAS 拉取
- Cup exclude 增加 `tgarchive` 前缀
