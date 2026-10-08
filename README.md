# tgarchive

集中管理多个 Telegram 机器人，把白名单用户私聊发给机器人的消息（文本与全部媒体）存档到服务器，用仿 Telegram Web A 的只读 WebUI 浏览；受保护群组 / 频道的内容可以贴消息链接，由用户账号（userbot）代取。设计见 [`docs/superpowers/specs/2026-10-04-tgarchive-design.md`](docs/superpowers/specs/2026-10-04-tgarchive-design.md)。

消息整条就是一个 Telegraph 文章链接（`telegra.ph` / `graph.org`）时，文章正文与其中的图片、视频会离线存档，WebUI 在该消息下显示文章卡片，点开为仿 Instant View 的阅读页。设计见 [`docs/superpowers/specs/2026-10-05-telegraph-archive-design.md`](docs/superpowers/specs/2026-10-05-telegraph-archive-design.md)。文章图片从原站直接下载，容器需要能访问外网。

存档可全文搜索（正文、说明文字、文件名与 Telegraph 正文，中文按子串匹配，可限定单个会话，结果可跳到任意早的消息）；消息可收藏并打标签，在「收藏」视图按标签筛选；监听频道的会话在左栏显示未读数，打开时停在第一条未读消息（上方有「以下为新消息」分隔线），滚到底才算已读。设计见 [`docs/superpowers/specs/2026-10-07-search-favorites-unread-design.md`](docs/superpowers/specs/2026-10-07-search-favorites-unread-design.md)。

「媒体墙」把所有会话的图片、视频与 GIF 按月份排成等高行，可按类型和来源（私聊 / 频道）筛选，点开即在大图浏览器里翻遍整面墙；「统计」汇总消息、媒体与磁盘占用，并给出一年活跃热力图、累计增长、会话排行、媒体构成与各频道监听的命中趋势和命中率（命中率自 v0.8.0 起统计）。设计见 [`docs/superpowers/specs/2026-10-07-media-wall-stats-design.md`](docs/superpowers/specs/2026-10-07-media-wall-stats-design.md)。

监听命中存档的帖子会继续刷新 reactions、浏览、转发与评论数：命中后 2 小时内每 10 分钟、6 小时内每 30 分钟、24 小时内每 2 小时，之后在第 3 天和第 7 天各刷新一次；打开频道会话时也会刷新当前显示的帖子（同一会话 5 分钟内最多一次）。

单个 Go 二进制内嵌 Preact 前端，并以子进程托管官方 [telegram-bot-api](https://github.com/tdlib/telegram-bot-api) 本地服务器（`--local`，只监听 127.0.0.1:8081）。

## 开发

依赖：Go 1.26+、Node ≥ 22.12。

```bash
cd web && npm ci && npm test && npm run build && cd ..   # 产物进 web/dist，供 go:embed
go test ./...
```

本机运行（不经认证网关；不托管子进程，连一个已有的 Bot API 服务器）：

```bash
TOKEN_ENC_KEY=$(openssl rand -hex 32) DATA_DIR=./tmp REQUIRE_FORWARD_AUTH=false \
  BOT_API_MANAGED=false BOT_API_URL=http://127.0.0.1:8081 go run ./cmd/tgarchive
cd web && npm run dev   # Vite 开发服务器，/api、/media、/avatars 代理到 127.0.0.1:8080
```

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `TOKEN_ENC_KEY` | 必填 | 64 位 hex（32 字节），AES-256-GCM 主密钥，加密 bot token、userbot session 与 `api_id` / `api_hash`；丢失后这些都无法解密 |
| `LISTEN` | `:8080` | HTTP 监听地址 |
| `DATA_DIR` | `/data` | 数据根目录（`db/`、`media/`、`avatars/`、`botapi/`、`botapi-tmp/`） |
| `REQUIRE_FORWARD_AUTH` | `true` | 除 `/healthz` 外要求请求带 `Remote-User`，否则 401 |
| `BARK_NOTIFY_FILE` | 空 | Bark 配置（`endpoint` + `device_keys`），机器人或 userbot 出错时推送；空则不推 |
| `MEDIA_MAX_BYTES` | `0` | 单文件存档上限，0 为不限；Telegraph 文章的网页媒体此时仍有 2 GiB 的默认上限（作者不可信） |
| `BOT_API_MANAGED` | `true` | 是否托管 `telegram-bot-api` 子进程 |
| `BOT_API_BINARY` | `telegram-bot-api` | 子进程可执行文件 |
| `BOT_API_URL` | `http://127.0.0.1:8081` | Bot API 服务器地址 |
| `BOT_API_DIR_LOCAL` / `BOT_API_DIR_REMOTE` | `$DATA_DIR/botapi` | 本容器内 / Bot API 服务器内的同一目录（外部服务器时用于路径映射） |
| `CLOUD_API_URL` | `https://api.telegram.org` | 云端 Bot API，只用于添加机器人时的 `getMe` 与 `logOut` |
| `TELEGRAPH_API_URL` | `https://api.telegra.ph` | Telegraph API 地址，仅测试时指向假服务器 |
| `TRANSCODE` | `true` | 为浏览器播不了的视频（AV1、MPEG-4 Part 2、非常见音频等）在后台生成 H.264/AAC 兼容版，原文件保留供下载；镜像自带 ffmpeg |
| `TRANSCODE_VAAPI_DEVICE` | `/dev/dri/renderD128` | Intel 核显的 render 节点，用于硬件编码；设备不存在或编码失败时用 libx264 软件编码 |

机器人 token、`api_id` / `api_hash` 与 userbot 登录都在 WebUI「管理」页设置并加密入库，不经过环境变量。

## 镜像与发布

镜像 `ghcr.io/senshinya/tgarchive`，GitHub Actions 在 amd64 / arm64 原生 runner 上构建，Dockerfile 内会跑前端测试与 `go test ./...`：

- 推送 `vX.Y.Z` tag → `X.Y.Z`
- 推送 `main` → `main`、`sha-<7 位>`
- PR 与手动运行只构建不推送

发布：

```bash
git tag -a v0.1.1 -m v0.1.1 && git push origin v0.1.1
gh release create v0.1.1 --verify-tag --generate-notes
```

## 部署

```yaml
services:
  tgarchive:
    image: ghcr.io/senshinya/tgarchive:0.1.0
    restart: unless-stopped
    init: true               # docker-init 回收子进程
    stop_grace_period: 30s
    env_file: .env           # TOKEN_ENC_KEY
    volumes:
      - ./data:/data         # chown -R 10001:10001
```

- 容器以 UID/GID 10001 运行，镜像自带健康检查 `GET /healthz`
- 硬件转码：compose 加 `devices: [/dev/dri:/dev/dri]`，并用 `group_add` 加入宿主 render 节点所属的组（`stat -c %g /dev/dri/renderD128`）
- 前面必须有反代做认证并写入 `Remote-User`，且先剥掉客户端自带的 `Remote-*` 头；SSE 路径 `/api/events` 不要缓冲
- 首次使用：管理 → API 凭据填 [my.telegram.org](https://my.telegram.org) 的 `api_id` / `api_hash` → 添加机器人并设置白名单 →（可选）用户账号登录

## 许可

GPL-3.0，见 [LICENSE](LICENSE)。镜像内的 telegram-bot-api 以 BSL-1.0 发布。
