# tgarchive 计划 4：打包、CI 与上线 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 tgarchive 推到公开仓库 `github.com/senshinya/tgarchive`，由 GitHub Actions 在 amd64 / arm64 原生 runner 上构建并发布 `ghcr.io/senshinya/tgarchive` 多架构镜像，然后按本站基础设施约定部署到 `tg.shinya.click`（Caddy + Authelia + CF SaaS + 华为四线 + 备份 + NAS 媒体拉取 + 文档）。

**Architecture:** 仓库侧：多阶段 Dockerfile（Node 测试并构建前端 → Go `go test ./...` 并以 `CGO_ENABLED=0` 交叉编译 → `alpine:3.24` 运行阶段，从 `aiogram/telegram-bot-api:10.3` 复制官方二进制并用 alpine 仓库补齐其动态库）；CI 每个架构一个原生 runner 按 digest 推送，再由 merge 作业合成多架构清单；应用侧补三处上线必需的小改动（归档文件 0644、Bark 请求带 UA、Linux 下子进程 Pdeathsig）。服务器侧是一份逐条可执行的 runbook：DNS → stack → Caddy → 备份 / Cup / Glance → OpenList 只读存储 + NAS 拉取 → 文档 → 用户首次使用与验收。

**Tech Stack:** Docker Buildx、GitHub Actions（`ubuntu-24.04` / `ubuntu-24.04-arm`）、GHCR；Go 1.26、Node 24；服务器侧 Docker Compose、Caddy、Authelia、Cloudflare SaaS API、华为云 DNS API、restic、OpenList、rclone。

**Spec:** `docs/superpowers/specs/2026-10-04-tgarchive-design.md`（§2 部署形态、§12 基础设施文档变更；§11 验收）。基础设施约定：`/Users/shinya/Documents/server/` 下的 `CLAUDE.md`、`deploy.md`、`dns.md`、`backup.md`、`sso.md`、`inspection.md`、`gotchas.md`（Ignis / blog-comment 段）、`bark.md`。

**前置：** 计划 3 已合入 `main`（`web/` 工程、`web/package-lock.json`、`web/.npmrc`、`web/dist/.gitkeep` 均已存在）。在 `main` 上新建分支 `feat/p4-deploy` 执行 Task 1–5；Task 6 合并发布；Task 7–13 是服务器 runbook。

## 裁定（与 spec 的出入）

- **源码与镜像来源改为 GitHub + GHCR**（站长明确指示，覆盖 spec §2 的「Forgejo 源码 + compose `build: .`」）：仓库 `git@github.com:senshinya/tgarchive.git`（公开），镜像 `ghcr.io/senshinya/tgarchive`，compose 用 `image:` 固定 semver tag。Task 5 同步 spec §2 与 §12。
- **Cup 不加 exclude**（覆盖 spec §2 / §12 的「本地镜像前缀 `tgarchive` 加入 Cup exclude」）：镜像不再是本地构建，按 CLAUDE.md 的 Cup 条目只有本地镜像前缀进 exclude；Ignis（`ghcr.io/senshinya/ignis`）、blog-comment（`ghcr.io/senshinya/blog-comment`）这类自有 Actions 镜像都由 Cup 正常按 semver 监控，tgarchive 照此办理。
- **运行阶段基础镜像用 `alpine:3.24.2`，只从 aiogram 镜像复制二进制**，不直接以 aiogram 镜像为底：
  - 已用 registry API 实查 `aiogram/telegram-bot-api:10.3`（index `sha256:50a9ed1f…`）：amd64 / arm64 两个变体都是 `alpine-minirootfs-3.21.8` + `apk add openssl libstdc++`，二进制 `/usr/local/bin/telegram-bot-api`（Bot API 10.3），ELF 解释器为 musl（`/lib/ld-musl-{x86_64,aarch64}.so.1`），NEEDED = `libssl.so.3 libcrypto.so.3 libz.so.1 libstdc++.so.6 libgcc_s.so.1 libc.musl-*.so.1`，导入的符号版本只有 `OPENSSL_3.0.0` 与 `GCC_3.0`
  - Alpine 3.21 于 **2026-11-01 EOL**（不到一个月），以它为底等于上线即用停更系统；3.24 支持到 2028-06-01，其仓库提供同 ABI 的 `libssl3`/`libcrypto3` 3.5.9、`libstdc++`/`libgcc` 15.2、`zlib` 1.3.2、musl 1.2.6（OpenSSL 3.x 与 libstdc++.so.6 均向后兼容，符号版本要求满足）
  - aiogram 镜像的 entrypoint 会 `chown` 工作目录并以 `--username` 降权，不适合由本应用托管的子进程；只取二进制最干净
  - 运行阶段 `RUN telegram-bot-api --version` 在构建时验证动态库齐全
- **Go 构建镜像用 `golang:1.26.8-alpine3.24`，不用当前最新的 1.27.1**：`go.mod` 声明 `go 1.26.1`，开发机为 go1.26.1，计划 1–3 的测试都在 1.26 上验证；1.26.8 是该线最新补丁。升到 1.27 属于工具链升级，应改 `go.mod` 并单独验证，不在上线计划内。
- **Node 用 `node:24.21.0-alpine3.24`（24 LTS），不用 26.10.0**：Node 26 当前仍是 Current，10 月下旬才进 LTS；Vite 8 要求 ≥ 22.12，24 满足。
- **以 UID/GID 10001:10001 运行（非 root）**，不用 1000：宿主 UID 1000 是 Forgejo passthrough 的 `git` 用户（disaster-recovery.md），给它授权读 `notify.json` 没有必要；1001 是 OpenList / Navidrome。
- **`notify.json` 用 POSIX ACL 授权**：`/opt/app/bark/notify.json` 是 root 600，非 root 容器读不到。复制一份会产生第三份设备 key 配置（bark.md 要求 VPS 与 LAN 两份同步），所以用 `setfacl -m u:10001:r`。该文件是单文件 bind mount，修改必须保 inode（`cat tmp > notify.json`），这样 ACL 也随之保留——写进 gotchas.md 与 bark.md。
- **新增三处应用侧改动**（spec 未写，但上线必需）：
  - Task 1：归档文件统一 0644。TDLib 下载的文件是 0600，硬链接后共用 inode；不放宽的话 OpenList（UID 1001）读不到 media，NAS 拉取会静默漏文件。
  - Task 2：Bark 请求带 `User-Agent: tgarchive-notify/1.0`。dns.md / bark.md 记录了 CF Browser Integrity Check 拦截默认 UA（1010），`bark.shinya.click` 走 CF。
  - Task 4：Linux 下子进程 `Pdeathsig=SIGTERM`，并让启动子进程的 goroutine 锁定 OS 线程直到子进程退出（Pdeathsig 绑定的是 fork 它的**线程**而非进程；Go 运行时会回收「锁定后未解锁就退出」的 goroutine 所在线程，不钉住的话子进程可能被误杀）。
- **NAS 媒体拉取走 OpenList + `drive-dav`**：backup.md 的音乐库方法是 rclone 经 `drive-dav.shinya.click`（OpenList WebDAV，灰云 + 代理节点，实测比 22 端口快约 100 倍）拉取，凭据只在 NAS。tgarchive 没有 WebDAV，因此在 OpenList 加一个只读本机存储 `/tgarchive-media`（OpenList compose 增加 `:ro` 挂载），NAS 复用现有 remote `vps-dav`。SFTP 受限账号方案被否：22 端口实测 120 KB/s 且 chroot 要求整条路径 root 属主。
- **`/media/*` 与 `/api/events` 都不压缩**：spec 只写了 `/media/*`。SSE 走 `encode` 时每个事件都要 flush 压缩流，与 CLAUDE.md §4 对 `cpa-api` SSE 不压缩的理由相同。
- **`SQLITE_SKIP` 必须包含 `media/*`**：用户可把 `.db` / `.sqlite` 当文档发给机器人，文件以原扩展名落在 `/opt/app/tgarchive/media/<bot>/<yyyy>/<mm>/`（距 `/opt/app` 深度 6，正好在备份脚本 SQLite 扫描的 `-maxdepth 6` 内），只加 `exclude.txt` 挡不住 staging（backup.md）。
- **许可仍为 GPL-3.0**：spec 写的理由「移植了 Telegram Web A 的样式与组件代码」已被计划 3 推翻（不再复制 telegram-tt）。依赖（gotd/td MIT、modernc sqlite BSD-3、preact MIT、lucide ISC、lottie-web MIT；镜像内 telegram-bot-api BSL-1.0、OpenSSL Apache-2.0）均与 GPL-3.0 兼容，没有必须改许可的理由，也没有必须用 GPL 的理由。按 spec 保留 GPL-3.0，镜像标签写 `GPL-3.0-only`；Task 5 删掉 spec 里过时的理由括注。

## Global Constraints

- Go 模块 `tgarchive`，`go.mod` 的 `go 1.26.1` 不改
- 基础镜像一律 `tag@digest`：
  - `node:24.21.0-alpine3.24@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1`
  - `golang:1.26.8-alpine3.24@sha256:8ac98ca534ac3f51e1f420a1dd2c15e74c75cfa0f23f3ad27eb5d7236c349a0c`
  - `aiogram/telegram-bot-api:10.3@sha256:50a9ed1f229930add49fd3aad5fa4119f269e40a9b364e6bd03c4d754fb9d950`
  - `alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6`
- Actions 一律精确版本：`actions/checkout@v7.0.1`、`docker/setup-buildx-action@v4.4.1`、`docker/login-action@v4.6.0`、`docker/metadata-action@v6.2.0`、`docker/build-push-action@v7.4.0`、`actions/upload-artifact@v7.0.1`、`actions/download-artifact@v8.0.1`；不用 `docker/setup-qemu-action`
- 平台 `linux/amd64`（`ubuntu-24.04`）+ `linux/arm64`（`ubuntu-24.04-arm`），各自原生构建，merge 作业合并清单
- 镜像 tag：推送 `v*` tag → `X.Y.Z`；推送 `main` → `main` 与 `sha-<7 位>`；`pull_request` / `workflow_dispatch` 只构建不推送；不产出 `latest`
- 部署只钉 semver tag，首个版本 `0.1.0`（git tag `v0.1.0`）
- 容器以 UID/GID `10001:10001` 运行；`/data` = `/opt/app/tgarchive`
- compose：`init: true`、`stop_grace_period: 30s`、只接外部网络 `web`、不暴露宿主端口；健康检查由镜像的 `HEALTHCHECK` 提供（`GET /healthz`），compose 不覆盖
- 机器人 token、`api_id` / `api_hash`、userbot 登录只在 WebUI 设置；`.env` 只有 `TOKEN_ENC_KEY`
- `TOKEN_ENC_KEY` 只在服务器上用 `openssl rand -hex 32` 生成，不得打印到 controller 的输出、日志或任何仓库文件
- 服务器命令一律经 `/usr/bin/ssh root@152.53.243.102` 执行；单文件 bind mount（Caddyfile、glance.yml、notify.json）只用保 inode 写法 `cat tmp > file`
- 回滚材料放 `/opt/app/maintenance-<YYYYMMDD>-updates/`（目录 700、文件 600，已被 `exclude.txt` 的 glob 排除）
- NAS（192.168.7.146，用户 `shinya`）与 LAN 机由站长操作：本计划只给出命令，controller 不登录 NAS；LAN `192.168.7.2` 的 cfst 步骤 controller 可尝试，连不上就交给站长在自己终端执行
- 基础设施文档用中文，只陈述当前状态，不写实施经过
- Go 改动后 `go test ./...` 通过、`gofmt -l cmd internal` 无输出；改动 Linux 专属文件后 `GOOS=linux go vet ./internal/botapiserver/` 通过

## Review Focus

1. **用户把 `.db` / `.sqlite` 文件当文档发给机器人** → 该文件落在 `media/` 深度 6 处，若备份脚本的 SQLite 扫描不跳过它，会被 `.backup` 进 staging 再进 R2，违背「media 不进 R2」；期望被 `SQLITE_SKIP` 跳过 → Task 10 Step 3 的 `SQLITE_SKIP` 功能核验（前三行 `yes`）+ Task 13 Step 6 的 `restic ls` 核验
2. **本地 Bot API 服务器下载的文件是 0600**，硬链接进归档后 OpenList（UID 1001）读不到，NAS 每晚 `rc=0` 却漏拉；期望归档文件一律 0644 → Task 1 测试 `TestLinkOrCopyMakesArchiveWorldReadable`，Task 13 Step 3 线上 `find -perm` 核验
3. **应用进程被 SIGKILL / OOM / 崩溃而 telegram-bot-api 子进程存活**，孤儿占着 127.0.0.1:8081 与 binlog，重启后新子进程起不来；期望子进程随父进程收到 SIGTERM，且不会因 Go 运行时回收线程被误杀 → Task 4 测试 `TestSetPdeathsig`、`TestChildTerminatedWhenParentDies`、`TestChildSurvivesOSThreadChurn`
4. **有人用编辑器「原子保存」改了 `notify.json`**，容器还钉在旧 inode 上、新文件也没有 ACL，机器人进入 error 时 Bark 静默不推；期望容器内可读且与宿主一致 → Task 8 Step 5 的可读性与内容一致性核验、Task 12 写入 gotchas.md / bark.md 的保 inode 规则
5. **Caddy 指令顺序**：`forward_auth` 默认排在 `request_header` 之前，若剥头写在 `route` 外，登录后 Authelia 写入的 `Remote-User` 被删、应用全站 401；反之若不剥头，客户端自带 `Remote-User` 可直通应用 → Task 9 Step 4 的 adapt 顺序核验、Task 9 Step 6 伪造头 302、Task 8 Step 4 内网缺头 401，Task 13 Step 2 用户登录后能正常加载

---

## 文件结构

```
仓库（/Users/shinya/Downloads/tgarchive）
internal/botapifs/botapifs.go            （改）归档文件 0644
internal/botapifs/botapifs_test.go       （改）权限测试
internal/notify/bark.go                  （改）User-Agent
internal/notify/bark_test.go             （改）UA 测试
internal/botapiserver/supervisor.go      （改）锁线程启动/等待子进程、调用 setPdeathsig
internal/botapiserver/supervisor_linux.go       Pdeathsig=SIGTERM
internal/botapiserver/supervisor_other.go       非 Linux 空实现
internal/botapiserver/supervisor_test.go （改）TestMain 增加假父进程与 SIGTERM 记录
internal/botapiserver/supervisor_linux_test.go  Linux 专属测试
Dockerfile / .dockerignore / LICENSE
.github/workflows/docker.yml
README.md
docs/superpowers/specs/2026-10-04-tgarchive-design.md （改）§2、§12

服务器（152.53.243.102）
/opt/stacks/tgarchive/{compose.yaml,.env}
/opt/app/tgarchive/{db,media,avatars,botapi,botapi-tmp}
/opt/stacks/caddy/Caddyfile               （改）tg.shinya.click 站点块
/opt/app/glance/glance.yml                （改）探活、书签、releases
/opt/app/backup/exclude.txt               （改）3 行
/usr/local/bin/backup-daily.sh            （改）SQLITE_SKIP 3 项
/opt/stacks/openlist/compose.yaml         （改）只读挂载 media
/opt/app/bark/notify.json                 （ACL）u:10001:r

基础设施文档（/Users/shinya/Documents/server）
CLAUDE.md、dns.md、backup.md、gotchas.md、current-state.md、sso.md、bark.md、inspection.md、disaster-recovery.md
```

---

### Task 1: 归档文件统一 0644

**Files:**
- Modify: `internal/botapifs/botapifs.go`（`linkOrCopy`）
- Test: `internal/botapifs/botapifs_test.go`

**Interfaces:**
- Consumes: 现有 `LinkOrCopy(src, dst string) error`
- Produces: `LinkOrCopy` 的结果文件权限恒为 `0o644`（硬链接路径与拷贝路径一致）；新增包内常量 `archiveMode fs.FileMode = 0o644`

- [ ] **Step 1: 建分支**

```bash
cd /Users/shinya/Downloads/tgarchive
git checkout main && git pull --ff-only 2>/dev/null; git checkout -b feat/p4-deploy
```

- [ ] **Step 2: 写失败测试**

在 `internal/botapifs/botapifs_test.go` 的 `TestLinkOrCopy` 之后追加：

```go
func TestLinkOrCopyMakesArchiveWorldReadable(t *testing.T) {
	// The local Bot API server (TDLib) creates downloads 0600, and a hard link shares that inode.
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LinkOrCopy(src, dst); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o644 {
		t.Fatalf("archived file mode = %o, want 644", got)
	}
}
```

- [ ] **Step 3: 运行，确认失败**

Run: `go test ./internal/botapifs/ -run TestLinkOrCopyMakesArchiveWorldReadable -v`
Expected: FAIL，`archived file mode = 600, want 644`

- [ ] **Step 4: 实现**

`internal/botapifs/botapifs.go` 中，把 `LinkOrCopy` 的注释与 `linkOrCopy` 整体替换为：

```go
// archiveMode is the mode of every archived file. The local Bot API server creates downloads 0600
// and a hard link shares that inode, so it is widened here: the NAS pull reads media/ through
// OpenList, which runs as a different UID.
const archiveMode fs.FileMode = 0o644

// LinkOrCopy places src at dst, replacing dst. A hard link is tried first (same filesystem),
// falling back to copy-then-rename so dst is never observed half-written. dst ends up archiveMode.
func LinkOrCopy(src, dst string) error {
	return redactErr(linkOrCopy(src, dst))
}

func linkOrCopy(src, dst string) error {
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Link(src, dst); err == nil {
		return os.Chmod(dst, archiveMode)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, archiveMode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Chmod(archiveMode); err != nil { // OpenFile's mode is filtered by the umask
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
```

- [ ] **Step 5: 运行全部测试**

Run: `go test ./... && gofmt -l cmd internal`
Expected: 全部 PASS，gofmt 无输出

- [ ] **Step 6: 提交**

```bash
git add internal/botapifs/botapifs.go internal/botapifs/botapifs_test.go
git commit -m "fix(botapifs): archived files are 0644 so the NAS pull can read hard-linked downloads"
```

---

### Task 2: Bark 请求带非默认 User-Agent

**Files:**
- Modify: `internal/notify/bark.go`
- Test: `internal/notify/bark_test.go`

**Interfaces:**
- Consumes: 现有 `(*Bark).Notify(ctx, title, body)`
- Produces: 包内常量 `userAgent = "tgarchive-notify/1.0"`；每个推送请求带该 UA

- [ ] **Step 1: 写失败测试**

在 `internal/notify/bark_test.go` 末尾追加：

```go
func TestBarkSendsNonDefaultUserAgent(t *testing.T) {
	// bark.shinya.click sits behind Cloudflare, whose Browser Integrity Check rejects default client UAs.
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.UserAgent()
		w.Write([]byte(`{"code":200}`))
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "notify.json")
	os.WriteFile(cfg, []byte(`{"endpoint":"`+srv.URL+`/push","device_keys":["k1"]}`), 0o600)
	(&Bark{File: cfg}).Notify(context.Background(), "t", "b")
	if ua != "tgarchive-notify/1.0" {
		t.Fatalf("User-Agent = %q", ua)
	}
}
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test ./internal/notify/ -run TestBarkSendsNonDefaultUserAgent -v`
Expected: FAIL，`User-Agent = "Go-http-client/1.1"`

- [ ] **Step 3: 实现**

`internal/notify/bark.go`：在 `type Notifier interface` 之前加入

```go
// userAgent replaces Go's default UA, which Cloudflare's Browser Integrity Check may reject (error 1010).
const userAgent = "tgarchive-notify/1.0"
```

并把

```go
	req.Header.Set("Content-Type", "application/json")
```

替换为

```go
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
```

- [ ] **Step 4: 运行全部测试**

Run: `go test ./... && gofmt -l cmd internal`
Expected: 全部 PASS，gofmt 无输出

- [ ] **Step 5: 提交**

```bash
git add internal/notify/bark.go internal/notify/bark_test.go
git commit -m "fix(notify): send a non-default User-Agent to Bark behind Cloudflare"
```

---

### Task 3: 镜像、CI 与首次推送 GitHub

**Files:**
- Create: `Dockerfile`、`.dockerignore`、`LICENSE`、`.github/workflows/docker.yml`

**Interfaces:**
- Consumes: 计划 3 的 `web/package.json`（`npm test`、`npm run build`）、`web/package-lock.json`、`web/.npmrc`；`web/web.go` 的 `//go:embed all:dist`；`cmd/tgarchive`
- Produces:
  - 镜像：入口 `/usr/local/bin/tgarchive`，`/usr/local/bin/telegram-bot-api`（10.3），`USER 10001:10001`，`WORKDIR /data`，`EXPOSE 8080`，`HEALTHCHECK` 请求 `http://127.0.0.1:8080/healthz`
  - 工作流 `Docker`（`.github/workflows/docker.yml`）：作业 `build`（矩阵 `linux/amd64`@`ubuntu-24.04`、`linux/arm64`@`ubuntu-24.04-arm`）与 `merge`；PR 上只构建
  - GitHub 远端 `origin` = `git@github.com:senshinya/tgarchive.git`，分支 `main` 与 `feat/p4-deploy` 已推送，PR 已开

- [ ] **Step 1: 写 `LICENSE`（GPL-3.0 原文）并校验**

```bash
curl -fsSL https://www.gnu.org/licenses/gpl-3.0.txt -o LICENSE
shasum -a 256 LICENSE
```

Expected: `3972dc9744f6499f0f9b2dbf76696f2ae7ad8af9b23dde66d6af86c9dfb36986  LICENSE`（674 行，首行为 `GNU GENERAL PUBLIC LICENSE`）。校验和不符就删除文件重下，不要手改。

- [ ] **Step 2: 写 `.dockerignore`**

```
.git
.github
.superpowers
docs
*.md
.env
/tgarchive
**/.DS_Store
web/node_modules
web/dist
```

`web/dist` 必须忽略：前端产物只能来自 Node 阶段，本地残留的旧构建不能混进镜像。

- [ ] **Step 3: 写 `Dockerfile`**

```dockerfile
# tgarchive 镜像：前端（Node）→ 测试与编译（Go）→ 运行（Alpine + 官方 telegram-bot-api）。
# 基础镜像一律 tag@digest；升级时 tag 与 digest 一起改。

# ---- 前端：产物与目标架构无关，固定在构建机架构上跑 ----
FROM --platform=$BUILDPLATFORM node:24.21.0-alpine3.24@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm test && npm run build

# ---- Go：在构建机架构上跑测试，再交叉编译到目标架构（CI 中两者相同）----
FROM --platform=$BUILDPLATFORM golang:1.26.8-alpine3.24@sha256:8ac98ca534ac3f51e1f420a1dd2c15e74c75cfa0f23f3ad27eb5d7236c349a0c AS build
ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web/web.go ./web/web.go
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go test ./...
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w" -o /out/tgarchive ./cmd/tgarchive

# ---- 官方 Bot API 服务器：只取目标架构的二进制 ----
FROM aiogram/telegram-bot-api:10.3@sha256:50a9ed1f229930add49fd3aad5fa4119f269e40a9b364e6bd03c4d754fb9d950 AS botapi

# ---- 运行 ----
FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
# telegram-bot-api 的 ELF NEEDED：libssl.so.3 libcrypto.so.3 libz.so.1 libstdc++.so.6 libgcc_s.so.1（+ musl）
RUN apk add --no-cache libssl3 libcrypto3 zlib libstdc++ libgcc ca-certificates tzdata \
 && addgroup -S -g 10001 tgarchive \
 && adduser -S -D -H -u 10001 -G tgarchive -h /data -s /sbin/nologin tgarchive \
 && mkdir -p /data \
 && chown 10001:10001 /data
COPY --from=botapi /usr/local/bin/telegram-bot-api /usr/local/bin/telegram-bot-api
COPY --from=build /out/tgarchive /usr/local/bin/tgarchive
# 缺运行库时在这里失败，而不是上线后子进程起不来
RUN telegram-bot-api --version
LABEL org.opencontainers.image.source="https://github.com/senshinya/tgarchive" \
      org.opencontainers.image.licenses="GPL-3.0-only"
USER 10001:10001
WORKDIR /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/tgarchive"]
```

不声明 `VOLUME`：声明了会在没挂载时产生匿名卷，CLAUDE.md §3 记录匿名卷不在备份范围。

- [ ] **Step 4: 写 `.github/workflows/docker.yml`**

```yaml
# 构建 ghcr.io/senshinya/tgarchive 多架构镜像。
# amd64 与 arm64 各在原生 runner 上构建（不用 QEMU），按 digest 推送后由 merge 作业合并成一个清单。
#   推送 v* tag → X.Y.Z；推送 main → main 与 sha-<7 位>；PR 与手动运行只构建不推送。
# Dockerfile 内会跑前端测试与 go test ./...，任一测试失败都会让对应架构的构建失败。

name: Docker

on:
  push:
    branches: [main]
    tags: ['v*']
  pull_request:
  workflow_dispatch:

permissions:
  contents: read

env:
  IMAGE: ghcr.io/senshinya/tgarchive
  PUSH: ${{ github.event_name == 'push' }}

concurrency:
  group: docker-${{ github.ref }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}

jobs:
  build:
    strategy:
      fail-fast: false
      matrix:
        include:
          - platform: linux/amd64
            runner: ubuntu-24.04
          - platform: linux/arm64
            runner: ubuntu-24.04-arm
    runs-on: ${{ matrix.runner }}
    permissions:
      contents: read
      packages: write
    steps:
      - name: Platform pair
        run: |
          platform='${{ matrix.platform }}'
          echo "PLATFORM_PAIR=${platform//\//-}" >> "$GITHUB_ENV"

      - uses: actions/checkout@v7.0.1

      - uses: docker/setup-buildx-action@v4.4.1

      - if: env.PUSH == 'true'
        uses: docker/login-action@v4.6.0
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - id: meta
        uses: docker/metadata-action@v6.2.0
        with:
          images: ${{ env.IMAGE }}

      - id: build
        uses: docker/build-push-action@v7.4.0
        with:
          context: .
          platforms: ${{ matrix.platform }}
          labels: ${{ steps.meta.outputs.labels }}
          outputs: type=image,name=${{ env.IMAGE }},push-by-digest=true,name-canonical=true,push=${{ env.PUSH }}
          cache-from: type=gha,scope=${{ env.PLATFORM_PAIR }}
          cache-to: type=gha,scope=${{ env.PLATFORM_PAIR }},mode=max

      - if: env.PUSH == 'true'
        name: Export digest
        run: |
          mkdir -p "$RUNNER_TEMP/digests"
          digest='${{ steps.build.outputs.digest }}'
          touch "$RUNNER_TEMP/digests/${digest#sha256:}"

      - if: env.PUSH == 'true'
        uses: actions/upload-artifact@v7.0.1
        with:
          name: digests-${{ env.PLATFORM_PAIR }}
          path: ${{ runner.temp }}/digests/*
          if-no-files-found: error
          retention-days: 1

  merge:
    if: github.event_name == 'push'
    needs: build
    runs-on: ubuntu-24.04
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/download-artifact@v8.0.1
        with:
          path: ${{ runner.temp }}/digests
          pattern: digests-*
          merge-multiple: true

      - uses: docker/setup-buildx-action@v4.4.1

      - uses: docker/login-action@v4.6.0
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - id: meta
        uses: docker/metadata-action@v6.2.0
        with:
          images: ${{ env.IMAGE }}
          tags: |
            type=semver,pattern={{version}}
            type=ref,event=branch
            type=sha,prefix=sha-,enable=${{ github.ref == 'refs/heads/main' }}

      - name: Create multi-arch manifest
        working-directory: ${{ runner.temp }}/digests
        run: |
          docker buildx imagetools create \
            $(jq -cr '.tags | map("-t " + .) | join(" ")' <<< "$DOCKER_METADATA_OUTPUT_JSON") \
            $(printf '${{ env.IMAGE }}@sha256:%s ' *)

      - name: Inspect
        run: docker buildx imagetools inspect '${{ env.IMAGE }}:${{ steps.meta.outputs.version }}'
```

- [ ] **Step 5: 本地自检**

本机没有容器运行时，镜像构建由 CI 验证；本地只确认 Dockerfile 依赖的构建命令本身可用：

Run: `go test ./... && (cd web && npm ci && npm test && npm run build) && git status --short`
Expected: Go 与前端测试全部 PASS；`git status` 只列出本任务新增的 4 个文件（`web/dist` 内容被 `.gitignore` 忽略）

- [ ] **Step 6: 提交**

```bash
git add Dockerfile .dockerignore LICENSE .github/workflows/docker.yml
git commit -m "build: multi-stage image with telegram-bot-api, native multi-arch CI to GHCR, GPL-3.0"
```

- [ ] **Step 7: 推送到 GitHub 并开 PR**

```bash
git remote add origin git@github.com:senshinya/tgarchive.git
git push -u origin main
git push -u origin feat/p4-deploy
gh pr create --repo senshinya/tgarchive --base main --head feat/p4-deploy \
  --title "Packaging, CI and deployment" \
  --body $'Multi-stage Docker image (web build + tests, go test, alpine runtime with the official telegram-bot-api 10.3), native amd64/arm64 GitHub Actions builds merged into one GHCR manifest, and the small app fixes needed for deployment.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)'
```

若 `git push` 报 `Permission denied (publickey)`：`gh auth setup-git && git remote set-url origin https://github.com/senshinya/tgarchive.git` 后重推。`main` 推上去时还没有工作流文件，不会触发构建；PR 触发 `pull_request` 事件。

- [ ] **Step 8: 等 CI 并核对两个架构都真正跑了测试**

```bash
gh pr checks --repo senshinya/tgarchive --watch
RUN=$(gh run list --repo senshinya/tgarchive --branch feat/p4-deploy --workflow Docker --limit 1 --json databaseId --jq '.[0].databaseId')
gh run view "$RUN" --repo senshinya/tgarchive --log | grep -E 'tgarchive/internal/botapiserver|Test Files|telegram-bot-api --version|10\.3' | head -20
```

Expected：两个 `build` 作业成功、`merge` 作业被跳过；日志中两个架构各出现一次 `ok  	tgarchive/internal/botapiserver`、vitest 的 `Test Files … passed`，以及 `telegram-bot-api --version` 输出含 `10.3`。若 `npm ci` 报找不到 `@rolldown/binding-linux-*-musl`，说明 lockfile 缺 musl 可选依赖：在 `web/` 用 `npm install --package-lock-only --os=linux --libc=musl --cpu=arm64` 与 `--cpu=x64` 各跑一次补全 lockfile、提交后重推。

---

### Task 4: Linux 下子进程 Pdeathsig 与线程钉定

**Files:**
- Create: `internal/botapiserver/supervisor_linux.go`、`internal/botapiserver/supervisor_other.go`、`internal/botapiserver/supervisor_linux_test.go`
- Modify: `internal/botapiserver/supervisor.go`（`start`）、`internal/botapiserver/supervisor_test.go`（`TestMain`）

**Interfaces:**
- Consumes: 现有 `New`、`(*Supervisor).Run/Apply/Status`、测试辅助 `newSup`、`lines`、`eventually`、常量 `hash1`
- Produces: 包内 `setPdeathsig(cmd *exec.Cmd)`（Linux 设 `SysProcAttr.Pdeathsig = SIGTERM`，其他系统空实现）；`start` 在专用、锁定的 OS 线程上 `cmd.Start` 并 `cmd.Wait`；测试辅助 `runFakeParent()`、`appendLine(path, line string)`，假子进程在设置了 `FAKE_TERM_RECORD` 时写 `start <pid>` 与 `term`

Linux 专属测试只能在 Linux 上运行；本机（macOS）只做 `GOOS=linux go vet` 编译检查，RED / GREEN 以 PR 上的 CI 结果为准（Dockerfile 的 `go test ./...` 会在两个架构上跑它们）。

- [ ] **Step 1: 加桩，让新测试能编译**

`internal/botapiserver/supervisor_linux.go`：

```go
//go:build linux

package botapiserver

import "os/exec"

func setPdeathsig(cmd *exec.Cmd) {}
```

`internal/botapiserver/supervisor_other.go`：

```go
//go:build !linux

package botapiserver

import "os/exec"

// setPdeathsig is Linux-only; elsewhere the child is stopped only by Supervisor.stop.
func setPdeathsig(cmd *exec.Cmd) {}
```

`internal/botapiserver/supervisor.go` 的 `start` 中，把

```go
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
```

替换为

```go
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	setPdeathsig(cmd)
	if err := cmd.Start(); err != nil {
```

- [ ] **Step 2: 写失败测试**

`internal/botapiserver/supervisor_test.go`：把整个 `TestMain` 替换为下面的版本，并在其后加入 `appendLine` 与 `runFakeParent`：

```go
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_PARENT") == "1" {
		runFakeParent()
	}
	if os.Getenv("FAKE_BOTAPI") == "1" {
		f, _ := os.OpenFile(os.Getenv("FAKE_RECORD"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		fmt.Fprintf(f, "%s %s %s token=%q\n", os.Getenv("TELEGRAM_API_ID"), os.Getenv("TELEGRAM_API_HASH"), strings.Join(os.Args[1:], " "), os.Getenv("TOKEN_ENC_KEY"))
		f.Close()
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)
		markTerm := func() {}
		if p := os.Getenv("FAKE_TERM_RECORD"); p != "" {
			appendLine(p, fmt.Sprintf("start %d", os.Getpid()))
			markTerm = func() { appendLine(p, "term") }
		}
		if ms, err := strconv.Atoi(os.Getenv("FAKE_EXIT_AFTER_MS")); err == nil {
			select {
			case <-sig:
				markTerm()
				os.Exit(0)
			case <-time.After(time.Duration(ms) * time.Millisecond):
				os.Exit(1)
			}
		}
		<-sig
		markTerm()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	fmt.Fprintln(f, line)
	f.Close()
}

// runFakeParent stands in for the tgarchive process in the parent-death test: it supervises a fake
// child and then idles until the test SIGKILLs it.
func runFakeParent() {
	dir := os.Getenv("FAKE_DIR")
	s := New(os.Args[0], filepath.Join(dir, "botapi"), filepath.Join(dir, "tmp"), 18082)
	s.Env = []string{
		"FAKE_BOTAPI=1",
		"FAKE_RECORD=" + os.Getenv("FAKE_RECORD"),
		"FAKE_TERM_RECORD=" + os.Getenv("FAKE_TERM_RECORD"),
	}
	go s.Run(context.Background(), &tgapp.Credentials{APIID: 1, APIHash: hash1})
	time.Sleep(time.Hour)
	os.Exit(0)
}
```

`internal/botapiserver/supervisor_linux_test.go`：

```go
//go:build linux

package botapiserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestSetPdeathsig(t *testing.T) {
	cmd := exec.Command("true")
	setPdeathsig(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Pdeathsig != syscall.SIGTERM {
		t.Fatalf("SysProcAttr = %+v, want Pdeathsig SIGTERM", cmd.SysProcAttr)
	}
}

// A SIGKILLed or OOM-killed tgarchive must not leave telegram-bot-api holding port 8081.
func TestChildTerminatedWhenParentDies(t *testing.T) {
	dir := t.TempDir()
	term := filepath.Join(dir, "term.txt")
	parent := exec.Command(os.Args[0])
	parent.Env = append(os.Environ(),
		"FAKE_PARENT=1",
		"FAKE_DIR="+dir,
		"FAKE_RECORD="+filepath.Join(dir, "record.txt"),
		"FAKE_TERM_RECORD="+term,
	)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { parent.Process.Kill() }) // no-op once the test has killed it
	childPID := 0
	eventually(t, "child started", func() bool {
		for _, l := range lines(term) {
			if n, ok := strings.CutPrefix(l, "start "); ok {
				childPID, _ = strconv.Atoi(n)
				return true
			}
		}
		return false
	})
	t.Cleanup(func() { syscall.Kill(childPID, syscall.SIGKILL) })

	parent.Process.Kill()
	parent.Wait()
	eventually(t, "child got SIGTERM after its parent died", func() bool {
		return slices.Contains(lines(term), "term")
	})
}
```

- [ ] **Step 3: 本机编译检查并推送，确认 CI 失败**

```bash
go test ./internal/botapiserver/ && GOOS=linux go vet ./internal/botapiserver/ && gofmt -l internal
git add internal/botapiserver
git commit -m "test(botapiserver): child must die with its parent on Linux"
git push
gh pr checks --repo senshinya/tgarchive --watch
RUN=$(gh run list --repo senshinya/tgarchive --branch feat/p4-deploy --workflow Docker --limit 1 --json databaseId --jq '.[0].databaseId')
gh run view "$RUN" --repo senshinya/tgarchive --log-failed | grep -E -- '--- FAIL|SysProcAttr|timed out' | head
```

Expected：本机三条命令无错误（macOS 上 Linux 测试不编译进来）；CI 两个 `build` 作业都在 `go test ./...` 失败，日志含 `--- FAIL: TestSetPdeathsig`（`want Pdeathsig SIGTERM`）与 `--- FAIL: TestChildTerminatedWhenParentDies`（`timed out waiting for child got SIGTERM after its parent died`）

- [ ] **Step 4: 实现 Pdeathsig**

`internal/botapiserver/supervisor_linux.go` 整体替换为：

```go
//go:build linux

package botapiserver

import (
	"os/exec"
	"syscall"
)

// setPdeathsig makes the kernel send SIGTERM to telegram-bot-api when the OS thread that forked it
// dies — in practice when tgarchive is SIGKILLed, OOM-killed or crashes — so the child never outlives
// the app while holding port 8081 and its binlog. start keeps that thread alive for the child's lifetime.
func setPdeathsig(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
```

- [ ] **Step 5: 推送，确认 CI 通过**

```bash
GOOS=linux go vet ./internal/botapiserver/ && go test ./... && gofmt -l cmd internal
git add internal/botapiserver/supervisor_linux.go
git commit -m "feat(botapiserver): SIGTERM telegram-bot-api when tgarchive dies (Pdeathsig)"
git push
gh pr checks --repo senshinya/tgarchive --watch
```

Expected：本机无错误；CI 两个 `build` 作业成功

- [ ] **Step 6: 写线程回收测试**

Pdeathsig 绑定的是 fork 子进程的线程：Go 运行时会终止「锁定 OS 线程后不解锁就退出」的 goroutine 所在线程，如果那恰好是当初 fork 子进程的线程，子进程会被误发 SIGTERM。在 `internal/botapiserver/supervisor_linux_test.go` 末尾追加（并在 import 中加入 `"context"`、`"runtime"`、`"sync"`、`"time"`、`"tgarchive/internal/tgapp"`）：

```go
// The runtime terminates the OS thread of a goroutine that exits while locked to it. If that is the
// thread which forked the child, Pdeathsig fires and the healthy child is killed.
func TestChildSurvivesOSThreadChurn(t *testing.T) {
	s, record := newSup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx, &tgapp.Credentials{APIID: 1, APIHash: hash1})
	eventually(t, "running", func() bool {
		st, _ := s.Status()
		return st == StateRunning && len(lines(record)) == 1
	})
	for i := 0; i < 200; i++ {
		var wg sync.WaitGroup
		for j := 0; j < 8; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runtime.LockOSThread() // exits still locked: the runtime retires this OS thread
			}()
		}
		wg.Wait()
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(lines(record)); n != 1 {
		t.Fatalf("child was restarted %d time(s) during OS thread churn", n-1)
	}
}
```

- [ ] **Step 7: 推送，确认 CI 失败**

```bash
GOOS=linux go vet ./internal/botapiserver/ && gofmt -l internal
git add internal/botapiserver/supervisor_linux_test.go
git commit -m "test(botapiserver): child survives OS thread churn"
git push
gh pr checks --repo senshinya/tgarchive --watch
RUN=$(gh run list --repo senshinya/tgarchive --branch feat/p4-deploy --workflow Docker --limit 1 --json databaseId --jq '.[0].databaseId')
gh run view "$RUN" --repo senshinya/tgarchive --log-failed | grep -E -- '--- FAIL|restarted' | head
```

Expected：至少一个架构 FAIL，日志含 `--- FAIL: TestChildSurvivesOSThreadChurn` 与 `child was restarted`。该失败依赖调度，属概率性；若两个架构都意外通过，用 `gh run rerun "$RUN" --repo senshinya/tgarchive` 重跑一次，仍通过则在 PR 评论里记录「RED 未复现」后继续——GREEN 之后它是确定性通过的。

- [ ] **Step 8: 实现线程钉定**

`internal/botapiserver/supervisor.go`：import 增加 `"runtime"`；把 `start` 中从 `setPdeathsig(cmd)` 到函数末尾整体替换为：

```go
	setPdeathsig(cmd)
	// Pdeathsig follows the forking OS thread, not the process. Start and reap the child on one locked
	// thread that does nothing else, so the runtime can never retire that thread under a live child.
	started := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := cmd.Start(); err != nil {
			started <- err
			return
		}
		started <- nil
		done <- cmd.Wait()
	}()
	if err := <-started; err != nil {
		return nil, nil, err
	}
	return cmd, done, nil
}
```

（被替换掉的是原来的 `if err := cmd.Start(); err != nil { return nil, nil, err }`、`done := make(chan error, 1)`、`go func() { done <- cmd.Wait() }()` 与 `return cmd, done, nil`。）

- [ ] **Step 9: 推送，确认 CI 通过**

```bash
GOOS=linux go vet ./internal/botapiserver/ && go test ./... && gofmt -l cmd internal
git add internal/botapiserver/supervisor.go
git commit -m "fix(botapiserver): start and reap telegram-bot-api on a locked OS thread"
git push
gh pr checks --repo senshinya/tgarchive --watch
```

Expected：本机无错误；CI 两个 `build` 作业成功

---

### Task 5: README 与 spec 同步

**Files:**
- Create: `README.md`
- Modify: `docs/superpowers/specs/2026-10-04-tgarchive-design.md`（§2 整节、§12 整节）

**Interfaces:**
- Consumes: `internal/config/config.go` 的环境变量与默认值；Task 3 的镜像 tag 规则
- Produces: 仓库首页说明；spec §2 / §12 与本计划一致

- [ ] **Step 1: 写 `README.md`**

````markdown
# tgarchive

集中管理多个 Telegram 机器人，把白名单用户私聊发给机器人的消息（文本与全部媒体）存档到服务器，用仿 Telegram Web A 的只读 WebUI 浏览；受保护群组 / 频道的内容可以贴消息链接，由用户账号（userbot）代取。设计见 [`docs/superpowers/specs/2026-10-04-tgarchive-design.md`](docs/superpowers/specs/2026-10-04-tgarchive-design.md)。

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
| `MEDIA_MAX_BYTES` | `0` | 单文件存档上限，0 为不限 |
| `BOT_API_MANAGED` | `true` | 是否托管 `telegram-bot-api` 子进程 |
| `BOT_API_BINARY` | `telegram-bot-api` | 子进程可执行文件 |
| `BOT_API_URL` | `http://127.0.0.1:8081` | Bot API 服务器地址 |
| `BOT_API_DIR_LOCAL` / `BOT_API_DIR_REMOTE` | `$DATA_DIR/botapi` | 本容器内 / Bot API 服务器内的同一目录（外部服务器时用于路径映射） |
| `CLOUD_API_URL` | `https://api.telegram.org` | 云端 Bot API，只用于添加机器人时的 `getMe` 与 `logOut` |

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
- 前面必须有反代做认证并写入 `Remote-User`，且先剥掉客户端自带的 `Remote-*` 头；SSE 路径 `/api/events` 不要缓冲
- 首次使用：管理 → API 凭据填 [my.telegram.org](https://my.telegram.org) 的 `api_id` / `api_hash` → 添加机器人并设置白名单 →（可选）用户账号登录

## 许可

GPL-3.0，见 [LICENSE](LICENSE)。镜像内的 telegram-bot-api 以 BSL-1.0 发布。
````

- [ ] **Step 2: 同步 spec §2**

把 spec 中从 `## 2. 部署形态` 开始、到 `## 3. 架构` 之前（不含）的整节替换为下面的文本：

```markdown
## 2. 部署形态

| 项 | 值 |
|---|---|
| 域名 | `tg.shinya.click`，CF SaaS + 华为四线解析 |
| 源码 | GitHub 公开仓库 [senshinya/tgarchive](https://github.com/senshinya/tgarchive)（`git@github.com:senshinya/tgarchive.git`，默认分支 `main`），本地 `~/Downloads/tgarchive` |
| 镜像 | `ghcr.io/senshinya/tgarchive`（公开），GitHub Actions 构建 `linux/amd64` + `linux/arm64` |
| Stack | `/opt/stacks/tgarchive`，compose 固定镜像的 semver tag（如 `0.1.0`），不用 `main` / `sha-*` |
| 数据 | `/opt/app/tgarchive/{db,media,avatars,botapi,botapi-tmp}`，属主 10001:10001 |
| 许可 | GPL-3.0 |

### 容器

单容器 `tgarchive`：接 `web`，监听 8080，挂 `/opt/app/tgarchive` 到 `/data`，以 UID/GID 10001:10001（非 root）运行。compose 设 `init: true`（docker-init 作为 PID 1 回收僵尸进程）与 `stop_grace_period: 30s`（应用关停 HTTP ≤10s、停子进程 ≤10s）；镜像内置健康检查 `GET /healthz`。

- 应用进程托管 Telegram 官方 `telegram-bot-api` 本地服务器作为子进程（`internal/botapiserver`）：参数 `--local --dir=/data/botapi --temp-dir=/data/botapi-tmp --http-ip-address=127.0.0.1 --http-port=8081`，只监听回环地址，不对外暴露
- 子进程所需的 `api_id` / `api_hash` 在 WebUI 设置页填写，加密存入 `settings` 表；未配置时子进程不启动，状态为 `unconfigured`，添加机器人接口返回 409。保存新凭据后子进程以新凭据重启；子进程异常退出按 1s 起指数退避重启，上限 30s
- Linux 下子进程设置 `Pdeathsig=SIGTERM`，并在一个锁定的 OS 线程上启动与回收：应用被 SIGKILL、OOM 或崩溃时内核立即向子进程发 SIGTERM，不会留下占用 8081 端口与 binlog 的孤儿
- 子进程环境不继承应用环境变量，看不到 `TOKEN_ENC_KEY`
- 子进程与应用共用 `/data/botapi`，`getFile` 返回的绝对路径可直接读取；归档文件统一设为 0644（TDLib 下载的文件为 0600，硬链接共用 inode）
- `BOT_API_MANAGED=false` 时不托管子进程，改连 `BOT_API_URL` 指向的外部 Bot API 服务器（开发与测试用）

### 镜像构建

多阶段 Dockerfile：Node 阶段 `npm ci` + `npm test` + 构建前端 → Go 阶段 `go test ./...` + 构建（`go:embed` 前端产物，`CGO_ENABLED=0`）→ 运行阶段 `alpine:3.24`，包含应用二进制与从 `aiogram/telegram-bot-api` 镜像复制的官方 `telegram-bot-api` 二进制（10.3），其动态库（OpenSSL 3、zlib、libstdc++、libgcc）由 alpine 自身仓库安装。全部基础镜像以 tag@digest 固定。

GitHub Actions（`.github/workflows/docker.yml`）：amd64 在 `ubuntu-24.04`、arm64 在 `ubuntu-24.04-arm` 原生构建，不用 QEMU；各自按 digest 推送，再由 merge 作业合并成多架构清单。推送 `v*` tag → 镜像 tag `X.Y.Z`；推送 `main` → `main` 与 `sha-<7 位>`；PR 与手动运行只构建不推送。发布版本同时创建 GitHub Release。

服务器上的镜像由 Cup 按 semver 正常监控，与 Ignis、blog-comment 等自有 Actions 镜像一致，不加入 Cup exclude。

### .env（mode 600，台账存 Vaultwarden）

只有一项：`TOKEN_ENC_KEY`（必填）：32 字节 hex，AES-256-GCM 主密钥，加密 bot token、userbot session 与 `settings` 表中的 `api_id` / `api_hash`；缺失或格式错误启动即失败。

compose `environment` 中的非机密配置：

- `TZ=Asia/Shanghai`
- `BARK_NOTIFY_FILE=/run/bark/notify.json`：单文件只读挂载 `/opt/app/bark/notify.json`；该文件为 root 600，以 POSIX ACL 授予 UID 10001 只读
- `MEDIA_MAX_BYTES`、`BOT_API_MANAGED`、`REQUIRE_FORWARD_AUTH` 等其余变量生产不设置，取默认值

机器人 token、`api_id` / `api_hash`（my.telegram.org 申请，本地 Bot API 与 userbot 共用）与 userbot 登录全部在 WebUI 完成，加密入库，不经过环境变量。

### Caddy

站点块整体包在一个 `route` 里以固定执行顺序（Caddy 默认把 `forward_auth` 排在 `request_header` 之前，写在 `route` 外会把认证写入的头一并删掉）：

1. `request_header -Remote-User` / `-Remote-Groups` / `-Remote-Name` / `-Remote-Email` 剥入站伪造头
2. `import authelia`：整站 `forward_auth`
3. `/media/*`：直接反代，不压缩（二进制 + Range）
4. `/api/events`（SSE）：`flush_interval -1`，不压缩
5. 其余路径：`import compress` 后反代

- 应用侧在 `REQUIRE_FORWARD_AUTH=true` 时对除 `/healthz` 外的所有请求校验 `Remote-User` 存在，否则 401
- 应用侧 CSRF 兜底：GET/HEAD/OPTIONS 以外的请求，带 `Sec-Fetch-Site` 且不为 `same-origin` 时 403；POST/PUT/PATCH 带请求体而 `Content-Type` 不是 `application/json` 时 403

### 备份

- R2（restic）：备份 `db/`（SQLite stage）、`avatars/` 与 stack；`media/`、`botapi/`、`botapi-tmp/` 加入 `exclude.txt`，并同时加入备份脚本的 `SQLITE_SKIP`（用户可能把 `.db` / `.sqlite` 当文档存档，它们落在 SQLite 扫描深度内）
- NAS：OpenList 以只读本机存储 `/tgarchive-media` 暴露 `media/`，NAS 用 rclone 经 `drive-dav` 每日拉取（`copy`），方式同音乐库

### 通知

机器人或 userbot 进入 `error` 状态时推送 Bark `docker` 分组（timeSensitive），凭据读 `notify.json`；请求带 `User-Agent: tgarchive-notify/1.0`，避开 CF Browser Integrity Check 对默认 UA 的拦截。

```

- [ ] **Step 3: 同步 spec §12**

把 `## 12. 基础设施文档变更（上线时）` 整节（到文件末尾）替换为：

```markdown
## 12. 基础设施文档变更（上线时）

- CLAUDE.md：服务清单新增 `tg.shinya.click` 行；凭据指针补 `.env` 的 `TOKEN_ENC_KEY`、userbot session 敏感级与 `notify.json` ACL；OpenList 补只读存储 `/tgarchive-media`；容器数、域名数与 `import compress` 引用数
- gotchas.md：新增 TGArchive 段（镜像与升级、子进程与停机、Caddy 顺序、`notify.json` ACL 与 inode、`logOut` 与 10 分钟切换限制、409 Conflict、my.telegram.org 申请报错、userbot 风险与破窗、`TOKEN_ENC_KEY`）
- backup.md：`media/`、`botapi/`、`botapi-tmp/` 的 exclude 与 `SQLITE_SKIP`，NAS 拉取
- dns.md、current-state.md：SaaS 子域清单与计数
- sso.md、bark.md、inspection.md、disaster-recovery.md：接入方式、通知调用方、NAS 心跳与对账、恢复速查
- Cup 不加 exclude：`ghcr.io/senshinya/tgarchive` 按 semver 监控
```

- [ ] **Step 4: 核对**

Run: `grep -n "ssh.git.shinya.click\|build: \.\|加入 Cup exclude\|移植了" docs/superpowers/specs/2026-10-04-tgarchive-design.md`
Expected: 无输出

- [ ] **Step 5: 提交并确认 CI**

```bash
git add README.md docs/superpowers/specs/2026-10-04-tgarchive-design.md
git commit -m "docs: README; spec deployment section follows GitHub + GHCR"
git push
gh pr checks --repo senshinya/tgarchive --watch
```

Expected: CI 两个 `build` 作业成功

---

### Task 6: 合并、发布 v0.1.0 并核对镜像

**Files:** 无（git 与 GitHub 操作）

**Interfaces:**
- Consumes: Task 1–5 的分支
- Produces: `ghcr.io/senshinya/tgarchive:0.1.0`（`linux/amd64` + `linux/arm64`，匿名可拉取）、`:main`、`:sha-<7 位>`；GitHub Release `v0.1.0`

- [ ] **Step 1: 合并到 main 并推送**

```bash
git checkout main
git merge --no-ff feat/p4-deploy -m "Merge plan 4: packaging, CI and deployment"
git push origin main
gh run watch --repo senshinya/tgarchive $(gh run list --repo senshinya/tgarchive --branch main --workflow Docker --limit 1 --json databaseId --jq '.[0].databaseId') --exit-status
```

Expected: `build`（两个架构）与 `merge` 全部成功；PR 自动显示为 merged

- [ ] **Step 2: 打 tag 并发布**

```bash
git tag -a v0.1.0 -m v0.1.0
git push origin v0.1.0
sleep 15
gh run watch --repo senshinya/tgarchive $(gh run list --repo senshinya/tgarchive --workflow Docker --event push --limit 1 --json databaseId,headBranch --jq '.[] | select(.headBranch=="v0.1.0") | .databaseId') --exit-status
gh release create v0.1.0 --repo senshinya/tgarchive --verify-tag --generate-notes --title v0.1.0
```

Expected: tag 构建的 `merge` 作业 `Inspect` 步骤列出 `ghcr.io/senshinya/tgarchive:0.1.0` 含 `linux/amd64` 与 `linux/arm64`

- [ ] **Step 3: 匿名核对（服务器拉取不登录）**

```bash
T=$(curl -s "https://ghcr.io/token?scope=repository:senshinya/tgarchive:pull" | jq -r .token)
curl -s -H "Authorization: Bearer $T" https://ghcr.io/v2/senshinya/tgarchive/tags/list | jq -c .tags
curl -s -H "Authorization: Bearer $T" -H "Accept: application/vnd.oci.image.index.v1+json" \
  https://ghcr.io/v2/senshinya/tgarchive/manifests/0.1.0 | jq -r '.manifests[] | "\(.platform.os)/\(.platform.architecture)"'
```

Expected：tags 含 `0.1.0`、`main`、`sha-<7 位>`；平台列出 `linux/amd64`、`linux/arm64`（另有 `unknown/unknown` 是 provenance 证明，正常）。若返回 401 / `DENIED`，说明包是私有的：请站长在 GitHub → 头像 → Your profile → Packages → tgarchive → Package settings → Danger Zone → Change visibility 改为 Public，然后重跑本步骤。

---

### Task 7: DNS（CF SaaS Custom Hostname + 华为四线）

在**本机** macOS 执行（凭据在本机），流程逐字按 deploy.md Step 4a 的 i～v，`<name>` = `tg`。顺序约束：本任务完成后才能做 Task 8（compose up）与 Task 9（Caddy reload），Step 4a-vi 放在 Task 9。

**Files:** 无（云端 DNS / CF 状态；LAN `192.168.7.2:/opt/app/cfst-direct/update-cf-ips.py`）

**Interfaces:**
- Produces: `tg.shinya.click` 的 CF Custom Hostname（`ssl.method=txt`），华为 recordsets：`_cf-custom-hostname.tg` TXT、`_acme-challenge.tg` CNAME（DCV 委托）、`tg` A × 4 线；cfst `SAAS` 列表含 `"tg"`

- [ ] **Step 1: 预检（token 有效、名字未被占用）**

```bash
bash <<'EOF'
set -euo pipefail
cd /Users/shinya/Documents/server
TOKEN=$(cat .cf-saas-token); ZONE=0c26b19f9047669baac1968747cdc35f; HZONE=ff8080829e039233019e07c8152b3eaf
curl -s -H "Authorization: Bearer $TOKEN" https://api.cloudflare.com/client/v4/user/tokens/verify | jq -r .result.status
curl -s -H "Authorization: Bearer $TOKEN" "https://api.cloudflare.com/client/v4/zones/$ZONE/custom_hostnames?hostname=tg.shinya.click" | jq -c '.result | length'
HC_REGION=ap-southeast-3 python3 .huawei-tool.py GET "/v2.1/zones/$HZONE/recordsets?limit=500&name=tg.shinya.click" \
  | jq -r '.recordsets[] | select(.name | test("(^|\\.)tg\\.shinya\\.click\\.$")) | "\(.name)\t\(.type)\t\(.line)"'
EOF
```

Expected：`active`、`0`、第三条无输出。任一不符就停下报告，不要覆盖已有记录。

- [ ] **Step 2: 建 Custom Hostname、ownership TXT、DCV 委托 CNAME 与四线 A 记录**

```bash
bash <<'EOF'
set -euo pipefail
cd /Users/shinya/Documents/server
TOKEN=$(cat .cf-saas-token); ZONE=0c26b19f9047669baac1968747cdc35f; HZONE=ff8080829e039233019e07c8152b3eaf
hw() { HC_REGION=ap-southeast-3 python3 .huawei-tool.py "$@"; }

# i) CF SaaS Custom Hostname
R=$(curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  "https://api.cloudflare.com/client/v4/zones/$ZONE/custom_hostnames" \
  -d '{"hostname":"tg.shinya.click","ssl":{"method":"txt","type":"dv","bundle_method":"ubiquitous","wildcard":false}}')
echo "$R" | jq -c '{success, id:.result.id, ownership:.result.ownership_verification}'
OWN_NAME=$(echo "$R" | jq -r .result.ownership_verification.name)
OWN_VALUE=$(echo "$R" | jq -r .result.ownership_verification.value)
test "$OWN_NAME" = "_cf-custom-hostname.tg.shinya.click"

# ii) ownership TXT（静态、永久保留）
hw POST /v2.1/zones/$HZONE/recordsets \
  "{\"name\":\"$OWN_NAME.\",\"type\":\"TXT\",\"ttl\":60,\"records\":[\"$OWN_VALUE\"],\"line\":\"default_view\"}" \
  | jq -c '{id,name,type,status}'

# iii) DCV 委托 CNAME（不要写静态 _acme-challenge TXT）
hw POST /v2.1/zones/$HZONE/recordsets \
  '{"name":"_acme-challenge.tg.shinya.click.","type":"CNAME","ttl":300,"records":["tg.shinya.click.3bfe5f4dc007bedf.dcv.cloudflare.com."],"line":"default_view"}' \
  | jq -c '{id,name,type,status}'

# iv) 四线 A：三线 IP 抄 bonsai 的当前值（cfst 每 6h 接管重刷）
IPS=$(hw GET "/v2.1/zones/$HZONE/recordsets?limit=500&type=A" \
  | jq -r '.recordsets[] | select(.name=="bonsai.shinya.click.") | "\(.line):\(.records[0])"')
echo "$IPS"
for line in Dianxin Liantong Yidong; do echo "$IPS" | grep -q "^$line:" ; done
for line_ip in "default_view:172.64.53.102" $(echo "$IPS" | grep -E '^(Dianxin|Liantong|Yidong):'); do
  line=${line_ip%%:*}; ip=${line_ip#*:}
  hw POST /v2.1/zones/$HZONE/recordsets \
    "{\"name\":\"tg.shinya.click.\",\"type\":\"A\",\"ttl\":300,\"records\":[\"$ip\"],\"line\":\"$line\"}" \
    | jq -c '{id,name,line,records,status}'
done
EOF
```

Expected：`success: true`；之后 6 条 recordset 各返回一个 `id`，`status` 为 `PENDING_CREATE` 或 `ACTIVE`。记录格式与 deploy.md Step 4a 完全一致（TXT 值不额外加引号）。

- [ ] **Step 3: 核对解析**

```bash
sleep 60
dig +short A tg.shinya.click @ns1.huaweicloud-dns.org
dig +short CNAME _acme-challenge.tg.shinya.click @1.1.1.1
dig +short TXT _cf-custom-hostname.tg.shinya.click @1.1.1.1
```

Expected：A 返回一个地址（境外视角为 `172.64.53.102`，国内运营商视角为对应线路的 Top IP）；CNAME 为 `tg.shinya.click.3bfe5f4dc007bedf.dcv.cloudflare.com.`；TXT 为 Step 2 的 ownership 值

- [ ] **Step 4: 把 `tg` 加进 cfst 的 SAAS 列表（LAN 192.168.7.2）**

```bash
/usr/bin/ssh shinya@192.168.7.2 \
  'python3 - <<EOF
import re
p="/opt/app/cfst-direct/update-cf-ips.py"; s=open(p).read()
m=re.search(r"^SAAS = \[(.*?)\]", s, re.M)
if "\"tg\"" not in m.group(1):
    s=s[:m.start(1)]+m.group(1).rstrip()+",\"tg\""+s[m.end(1):]; open(p,"w").write(s); print("added")
EOF
docker restart cfst-direct'
/usr/bin/ssh shinya@192.168.7.2 'grep -c "\"tg\"" /opt/app/cfst-direct/update-cf-ips.py'
```

Expected：`added`、`cfst-direct`、`1`。若 ssh 超时或 `No route to host`（本机进程可能没有局域网权限），把上面两条命令原样交给站长在自己的终端执行，等他回报 `added` 与 `1` 后再继续。

---

### Task 8: 服务器 stack

**Files（服务器）:**
- Create: `/opt/stacks/tgarchive/compose.yaml`、`/opt/stacks/tgarchive/.env`、`/opt/app/tgarchive/{db,media,avatars,botapi,botapi-tmp}`
- Modify: `/opt/app/bark/notify.json`（只加 ACL）

**Interfaces:**
- Consumes: Task 6 的 `ghcr.io/senshinya/tgarchive:0.1.0`；Task 7 的 DNS
- Produces: 容器 `tgarchive`（网络 `web`，`tgarchive:8080`，healthy，UID 10001），Bot API 状态 `unconfigured`

- [ ] **Step 1: 目录、属主与 `.env`**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
set -euo pipefail
if [ -e /opt/stacks/tgarchive ] || [ -e /opt/app/tgarchive ]; then echo 'tgarchive dirs already exist' >&2; exit 1; fi
mkdir -p /opt/stacks/tgarchive /opt/app/tgarchive/{db,media,avatars,botapi,botapi-tmp}
chown -R 10001:10001 /opt/app/tgarchive
chmod 750 /opt/app/tgarchive
chmod 700 /opt/app/tgarchive/{db,botapi,botapi-tmp}
chmod 755 /opt/app/tgarchive/{media,avatars}
( umask 077; printf 'TOKEN_ENC_KEY=%s\n' "$(openssl rand -hex 32)" > /opt/stacks/tgarchive/.env )
stat -c '%a %U:%G %n' /opt/stacks/tgarchive/.env /opt/app/tgarchive /opt/app/tgarchive/*
grep -c '^TOKEN_ENC_KEY=[0-9a-f]\{64\}$' /opt/stacks/tgarchive/.env
EOF
```

Expected：`.env` 为 `600 root:root`；`/opt/app/tgarchive` 及子目录属主为数字 `10001:10001`（宿主无此用户，`stat` 显示 `UNKNOWN` 也正常）；最后一行 `1`。`media` 755 是为了 Task 11 的 OpenList（UID 1001）只读挂载能读；`db`、`botapi*` 700（`botapi/` 的子目录名就是 bot token）。

- [ ] **Step 2: `notify.json` 授予 UID 10001 只读**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
set -euo pipefail
command -v setfacl >/dev/null || apt-get install -y acl
setfacl -m u:10001:r /opt/app/bark/notify.json
getfacl -p /opt/app/bark/notify.json
EOF
```

Expected：输出含 `user::rw-`、`user:10001:r--`、`group::---`、`other::---`。若 `setfacl` 报 `Operation not supported`，停下报告（根分区未启用 ACL）。

- [ ] **Step 3: 写 compose 并启动**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
set -euo pipefail
cat > /opt/stacks/tgarchive/compose.yaml <<'YAML'
services:
  tgarchive:
    image: ghcr.io/senshinya/tgarchive:0.1.0
    container_name: tgarchive
    restart: unless-stopped
    init: true
    stop_grace_period: 30s
    env_file: .env
    environment:
      TZ: Asia/Shanghai
      BARK_NOTIFY_FILE: /run/bark/notify.json
    volumes:
      - /opt/app/tgarchive:/data
      - /opt/app/bark/notify.json:/run/bark/notify.json:ro
    networks:
      - web

networks:
  web:
    external: true
YAML
cd /opt/stacks/tgarchive
docker compose config -q
docker compose pull
docker compose up -d
for i in $(seq 1 30); do
  s=$(docker inspect -f '{{.State.Health.Status}}' tgarchive); echo "$s"
  [ "$s" = healthy ] && break; sleep 2
done
EOF
```

Expected：最后一行 `healthy`

- [ ] **Step 4: 进程、日志与内网健康检查**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
docker exec tgarchive id
docker exec tgarchive telegram-bot-api --version
docker exec tgarchive ps -o pid,user,args
docker logs --tail 50 tgarchive
docker exec caddy wget -qO- http://tgarchive:8080/healthz; echo
docker exec caddy sh -c 'wget -S -q -O /dev/null http://tgarchive:8080/api/bots 2>&1 | head -1'
docker exec caddy sh -c 'wget -S -q -O /dev/null --header "Remote-User: shinya" http://tgarchive:8080/api/admin/telegram-app 2>&1 | head -1'
docker exec caddy wget -qO- --header "Remote-User: shinya" http://tgarchive:8080/api/admin/telegram-app; echo
EOF
```

Expected：
- `uid=10001(tgarchive) gid=10001(tgarchive)`；版本输出含 `10.3`
- `ps`：PID 1 为 `/sbin/docker-init -- /usr/local/bin/tgarchive`，其子进程 `/usr/local/bin/tgarchive` 属 `tgarchive`；尚无 `telegram-bot-api` 进程（未配置凭据）
- 日志含 `tgarchive listening on :8080`，无 `config:`、`init:`、`permission denied` 字样
- `ok`；`HTTP/1.1 401 Unauthorized`（缺 `Remote-User`）；`HTTP/1.1 200 OK`
- JSON 中 `"configured":false`，`server.state` 为 `unconfigured`

- [ ] **Step 5: `notify.json` 在容器内可读且与宿主一致；Bark 不拦截应用 UA**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
docker exec tgarchive sh -c 'test -r /run/bark/notify.json && echo readable'
[ "$(docker exec tgarchive sha256sum /run/bark/notify.json | cut -d" " -f1)" = "$(sha256sum /opt/app/bark/notify.json | cut -d" " -f1)" ] && echo same-content
curl -s -A 'tgarchive-notify/1.0' -o /dev/null -w '%{http_code}\n' https://bark.shinya.click/ping
EOF
```

Expected：`readable`、`same-content`、`200`

- [ ] **Step 6: TOKEN_ENC_KEY 入台账（站长操作）**

请站长在自己的终端运行 `/usr/bin/ssh root@152.53.243.102 cat /opt/stacks/tgarchive/.env`，把 `TOKEN_ENC_KEY` 存入 Vaultwarden（条目名 `tgarchive TOKEN_ENC_KEY`）。controller 不读取、不转述该值。

---

### Task 9: Caddy 站点块与公网验收

**Files（服务器）:**
- Modify: `/opt/stacks/caddy/Caddyfile`（单文件 bind mount，保 inode）

**Interfaces:**
- Consumes: Caddyfile 已有的 `(authelia)`、`(compress)` snippet；Task 8 的 `tgarchive:8080`
- Produces: `https://tg.shinya.click` 经 Authelia 保护；回滚材料 `/opt/app/maintenance-<YYYYMMDD>-updates/Caddyfile.before-tgarchive`

- [ ] **Step 1: 生成候选配置并在容器内验证**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
set -euo pipefail
cd /opt/stacks/caddy
if grep -q '^tg\.shinya\.click' Caddyfile; then echo 'tg block already present' >&2; exit 1; fi
grep -q '^(authelia)' Caddyfile
grep -q '^(compress)' Caddyfile
M=/opt/app/maintenance-$(date +%Y%m%d)-updates
install -d -m 700 "$M"
install -m 600 Caddyfile "$M/Caddyfile.before-tgarchive"
cp Caddyfile /tmp/Caddyfile.tg
cat >> /tmp/Caddyfile.tg <<'CADDY'

tg.shinya.click {
    # route 保证书写顺序：先剥入站 Remote-*，再 forward_auth 写入可信值
    route {
        request_header -Remote-User
        request_header -Remote-Groups
        request_header -Remote-Name
        request_header -Remote-Email
        import authelia
        handle /media/* {
            # 二进制 + Range，不压缩
            reverse_proxy tgarchive:8080
        }
        handle /api/events {
            # SSE：不压缩、不缓冲
            reverse_proxy tgarchive:8080 {
                flush_interval -1
            }
        }
        handle {
            import compress
            reverse_proxy tgarchive:8080
        }
    }
}
CADDY
docker cp /tmp/Caddyfile.tg caddy:/tmp/Caddyfile.tg
docker exec caddy caddy validate --config /tmp/Caddyfile.tg --adapter caddyfile
EOF
```

Expected：`Valid configuration`

- [ ] **Step 2: 保 inode 写入并热加载**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
set -euo pipefail
cd /opt/stacks/caddy
cat /tmp/Caddyfile.tg > Caddyfile
docker exec caddy grep -c '^tg\.shinya\.click' /etc/caddy/Caddyfile
docker exec -w /etc/caddy caddy caddy reload
rm -f /tmp/Caddyfile.tg
EOF
```

Expected：`1`（容器内看到新内容，说明 inode 未变）；reload 无报错

- [ ] **Step 3: 源站证书**

```bash
sleep 60
/usr/bin/ssh root@152.53.243.102 "docker logs --since 5m caddy 2>&1 | grep 'tg.shinya.click' | grep -E 'certificate obtained|error' | tail -5"
```

Expected：出现 `certificate obtained successfully` 且无 `error`。若出现 ACME 失败并卡锁，按 dns.md「Caddy reload 比 Huawei DNS 写入快会卡 ACME 锁」处理（先确认解析，再 `docker restart caddy`）。

- [ ] **Step 4: 处理链顺序核验（Review Focus 5）**

```bash
/usr/bin/ssh root@152.53.243.102 \
  "docker exec caddy caddy adapt --config /etc/caddy/Caddyfile --adapter caddyfile 2>/dev/null \
   | jq -c '[.apps.http.servers[].routes[] | select(tostring|test(\"tg.shinya.click\")) | .. | objects | select(has(\"handler\")) | [.handler, (.upstreams[0].dial? // \"\")]]'"
```

Expected：数组里第一个 `reverse_proxy` 的 dial 是 `authelia:9091`，且它之前恰有 4 个 `headers`；之后依次出现三个 dial 为 `tgarchive:8080` 的 `reverse_proxy`，其中最后一个之前有一个 `encode`

- [ ] **Step 5: CF SaaS 证书状态（deploy.md 4a-vi，本机）**

```bash
TOKEN=$(cat /Users/shinya/Documents/server/.cf-saas-token)
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://api.cloudflare.com/client/v4/zones/0c26b19f9047669baac1968747cdc35f/custom_hostnames?hostname=tg.shinya.click" \
  | jq -c '.result[0]|{status, ssl:.ssl.status, errors:.ssl.validation_errors}'
```

Expected：`{"status":"active","ssl":"active","errors":null}`（签发通常 1～3 分钟，未 active 时每分钟重查，10 分钟仍 pending 按 dns.md「CF Custom Hostname 使用 DCV 委托验证」排查）

- [ ] **Step 6: 公网与伪造头验收（本机）**

```bash
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' https://tg.shinya.click/
curl -s -o /dev/null -w '%{http_code}\n' -H 'Remote-User: shinya' https://tg.shinya.click/api/bots
curl -s -o /dev/null -w '%{http_code}\n' https://tg.shinya.click/healthz
/usr/bin/ssh root@152.53.243.102 \
  "curl -sk -o /dev/null -w '%{http_code}\n' --resolve tg.shinya.click:443:127.0.0.1 -H 'Remote-User: shinya' https://tg.shinya.click/api/bots"
/usr/bin/ssh root@152.53.243.102 \
  "curl -sk -D- -o /dev/null --resolve tg.shinya.click:443:127.0.0.1 -H 'Accept-Encoding: gzip, zstd' https://tg.shinya.click/ | grep -iE '^(HTTP|location|content-encoding)'"
```

Expected：
- `302 https://auth.shinya.click/?rd=https%3A%2F%2Ftg.shinya.click%2F…`
- 带伪造 `Remote-User` 经 CF：`302`（被 Authelia 拦下，没有直达应用）
- `/healthz` 经公网也是 `302`（整站受保护；健康检查走内网）
- 源站直连带伪造头：`302`
- 源站首页：`HTTP/2 302` 与指向 `auth.shinya.click` 的 `location`

应用层「无 Authelia 带伪造头 → 401」由 Task 8 Step 4 的内网请求覆盖（spec §11 第 7 条）。

---

### Task 10: Cup、Glance 与 R2 备份范围

**Files（服务器）:**
- Modify: `/opt/app/glance/glance.yml`（单文件 bind mount）、`/opt/app/backup/exclude.txt`、`/usr/local/bin/backup-daily.sh`

**Interfaces:**
- Produces: Glance 探活 `http://tgarchive:8080/healthz`、书签 `https://tg.shinya.click`（Apps 组）、releases `senshinya/tgarchive`；restic 排除 3 个目录；`SQLITE_SKIP` 3 项

- [ ] **Step 1: Cup 正常监控（不加 exclude）**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
grep -c tgarchive /opt/app/cup/cup.json
B=$(docker exec caddy wget -qO- http://cup:8000/api/v3/json | jq -r .last_updated)
docker exec caddy wget -qO- http://cup:8000/api/v3/refresh >/dev/null
for i in $(seq 1 30); do
  J=$(docker exec caddy wget -qO- http://cup:8000/api/v3/json)
  [ "$(echo "$J" | jq -r .last_updated)" != "$B" ] && break; sleep 10
done
echo "$J" | jq -c .metrics
echo "$J" | grep -o 'ghcr.io/senshinya/tgarchive:[^"]*' | sort -u
EOF
```

Expected：第一行 `0`（cup.json 不含 tgarchive）；metrics 中 `unknown` 为 0、`monitored_images` 比部署前多 1；最后一行 `ghcr.io/senshinya/tgarchive:0.1.0`

- [ ] **Step 2: Glance 探活、书签与 releases**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
set -euo pipefail
M=/opt/app/maintenance-$(date +%Y%m%d)-updates
install -d -m 700 "$M"
install -m 600 /opt/app/glance/glance.yml "$M/glance.yml.before-tgarchive"
python3 - <<'PY'
import difflib, re, sys
p = "/opt/app/glance/glance.yml"
src = open(p).read()
if "tgarchive" in src:
    sys.exit("glance.yml already mentions tgarchive")
lines = src.split("\n")

def ind(s):
    return len(s) - len(s.lstrip(" "))

def list_end(first, item_ind):
    i = first
    while i < len(lines):
        l = lines[i]
        if l.strip() and (ind(l) < item_ind or (ind(l) == item_ind and not l.lstrip().startswith("- "))):
            break
        i += 1
    while i > first and not lines[i - 1].strip():
        i -= 1
    return i

def append_item(key_re, item, start=0):
    for i in range(start, len(lines)):
        if re.match(key_re, lines[i]):
            j = i + 1
            while j < len(lines) and not lines[j].strip():
                j += 1
            if j >= len(lines) or not lines[j].lstrip().startswith("- "):
                sys.exit(f"unexpected list layout after line {i + 1}")
            k = ind(lines[j])
            item_lines = item(lines[j]) if callable(item) else item
            end = list_end(j, k)
            lines[end:end] = [" " * k + l for l in item_lines]
            return i
    sys.exit(f"anchor not found: {key_re}")

# monitor 探活：走内网健康端点（gotchas.md Glance：forward_auth 后的服务探内网名）
append_item(r"^\s*sites:\s*$", ["- title: TGArchive", "  url: http://tgarchive:8080/healthz"])
# 书签：Apps 组
g = next((i for i, l in enumerate(lines) if re.match(r"^\s*- title: Apps\s*$", l)), None)
if g is None:
    sys.exit("bookmark group 'Apps' not found")
append_item(r"^\s*links:\s*$", ["- title: TGArchive", "  url: https://tg.shinya.click"], start=g)
# releases：沿用现有条目格式
def release_item(first):
    if re.match(r"^\s*- [\w.:-]+/[\w.-]+\s*$", first):
        return ["- senshinya/tgarchive"]
    if re.match(r"^\s*- repository:", first):
        return ["- repository: senshinya/tgarchive"]
    sys.exit(f"unknown releases item format: {first!r}")
append_item(r"^\s*repositories:\s*$", release_item)

out = "\n".join(lines)
sys.stdout.writelines(difflib.unified_diff(src.splitlines(True), out.splitlines(True), "glance.yml", "glance.new.yml"))
open("/tmp/glance.new.yml", "w").write(out)
PY
docker run --rm -v /tmp/glance.new.yml:/app/config/glance.yml:ro \
  "$(docker inspect glance --format '{{.Config.Image}}')" config:validate
EOF
```

Expected：diff 只新增 3 处（`sites` 列表末尾一条、Apps 组 `links` 末尾一条、`repositories` 末尾一条），缩进与相邻条目一致；`config:validate` 通过。脚本以 `anchor not found` / `unexpected list layout` 退出时停下，把 glance.yml 相关片段贴出来人工确定插入位置，不要猜。

确认 diff 无误后写入并重启：

```bash
/usr/bin/ssh root@152.53.243.102 'cat /tmp/glance.new.yml > /opt/app/glance/glance.yml && rm /tmp/glance.new.yml && docker restart glance && sleep 5 && docker exec glance grep -ci tgarchive /app/config/glance.yml'
```

Expected：`glance`、`4`（两个 `TGArchive` 标题、内网探活 URL、`senshinya/tgarchive`）

- [ ] **Step 3: R2 排除与 `SQLITE_SKIP`（Review Focus 1）**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
set -euo pipefail
M=/opt/app/maintenance-$(date +%Y%m%d)-updates
install -d -m 700 "$M"
install -m 600 /opt/app/backup/exclude.txt "$M/exclude.txt.before-tgarchive"
install -m 600 /usr/local/bin/backup-daily.sh "$M/backup-daily.sh.before-tgarchive"
if grep -q tgarchive /opt/app/backup/exclude.txt; then echo 'exclude.txt already mentions tgarchive' >&2; exit 1; fi
printf '%s\n' /opt/app/tgarchive/media /opt/app/tgarchive/botapi /opt/app/tgarchive/botapi-tmp >> /opt/app/backup/exclude.txt
python3 - <<'PY'
import re, sys
p = "/usr/local/bin/backup-daily.sh"
s = open(p).read()
if "/opt/app/tgarchive/" in s:
    sys.exit("SQLITE_SKIP already mentions tgarchive")
m = re.search(r"^SQLITE_SKIP=\(", s, re.M)
if not m:
    sys.exit("SQLITE_SKIP=( not found")
add = '\n  "/opt/app/tgarchive/media/*"\n  "/opt/app/tgarchive/botapi/*"\n  "/opt/app/tgarchive/botapi-tmp/*"'
s = s[:m.end()] + add + s[m.end():]
open(p, "w").write(s)  # 原地截断写入：保留 inode 与执行权限
PY
bash -n /usr/local/bin/backup-daily.sh && echo syntax-ok
tail -3 /opt/app/backup/exclude.txt
# 功能核验：取出数组，按脚本的 case 语义逐条匹配
ARR=$(python3 -c 'import re;s=open("/usr/local/bin/backup-daily.sh").read();print(re.search(r"^SQLITE_SKIP=\(.*?\)", s, re.S|re.M).group(0))')
bash -c "$ARR"'
for f in /opt/app/tgarchive/media/3/2026/10/0a1b2c.sqlite /opt/app/tgarchive/botapi/123:abc/td.db /opt/app/tgarchive/botapi-tmp/x.db /opt/app/tgarchive/db/tgarchive.db; do
  hit=no; for p in "${SQLITE_SKIP[@]}"; do case "$f" in $p) hit=yes;; esac; done; echo "$hit $f"
done'
EOF
```

Expected：`syntax-ok`；exclude.txt 末三行为三个目录；核验输出前三行 `yes …`、第四行 `no /opt/app/tgarchive/db/tgarchive.db`（主库必须继续被 stage）。脚本原有条目若是未加引号的写法，先 `grep -n -A12 '^SQLITE_SKIP=(' /usr/local/bin/backup-daily.sh` 看清格式，确认加引号的新条目在该脚本的匹配方式下同样命中（以上核验输出为准）。

---

### Task 11: NAS 媒体拉取（OpenList 只读存储 + rclone）

**Files（服务器）:**
- Modify: `/opt/stacks/openlist/compose.yaml`（新增一条 `:ro` 挂载）

**Files（NAS，站长执行）:**
- Create: `~/.local/share/vps-tgarchive-backup/sync.sh`、用户 crontab 一行、`/vol1/1000/tgarchive-media/`

**Interfaces:**
- Consumes: OpenList 管理员 `shinya`（即 NAS 上 rclone remote `vps-dav` 使用的本地账号）；Task 1 的 0644 归档文件
- Produces: OpenList 存储 `/tgarchive-media`（只读）；WebDAV 路径 `https://drive-dav.shinya.click/dav/tgarchive-media/`；NAS 每日 05:15 拉取到 `/vol1/1000/tgarchive-media`

- [ ] **Step 1: OpenList 只读挂载 media**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
set -euo pipefail
cd /opt/stacks/openlist
M=/opt/app/maintenance-$(date +%Y%m%d)-updates
install -d -m 700 "$M"
install -m 600 compose.yaml "$M/openlist-compose.yaml.before-tgarchive"
python3 - <<'PY'
import re, sys
p = "/opt/stacks/openlist/compose.yaml"
lines = open(p).read().split("\n")
if any("/opt/app/tgarchive/media" in l for l in lines):
    sys.exit("already mounted")
idx = [i for i, l in enumerate(lines) if re.match(r"""^\s*-\s*["']?/opt/app/openlist/drive:""", l)]
if len(idx) != 1:
    sys.exit(f"expected exactly one /opt/app/openlist/drive volume line, found {len(idx)}")
i = idx[0]
pad = lines[i][: len(lines[i]) - len(lines[i].lstrip())]
lines.insert(i + 1, pad + "- /opt/app/tgarchive/media:/opt/tgarchive-media:ro")
open("/tmp/openlist-compose.yaml", "w").write("\n".join(lines))
PY
diff compose.yaml /tmp/openlist-compose.yaml || true
docker compose -f /tmp/openlist-compose.yaml config -q
cat /tmp/openlist-compose.yaml > compose.yaml && rm /tmp/openlist-compose.yaml
docker compose up -d
sleep 5
C=$(docker compose ps --format '{{.Name}}' | head -1)
docker exec "$C" sh -c 'id; ls -ld /opt/tgarchive-media; touch /opt/tgarchive-media/x 2>&1 | head -1'
EOF
```

Expected：diff 只多一行挂载；`id` 显示 uid=1001；目录可列出；`touch` 报 `Read-only file system`

- [ ] **Step 2: 站长在 OpenList 建存储**

请站长登录 https://drive.shinya.click → 管理 → 存储 → 添加：驱动选「本机存储」，挂载路径 `/tgarchive-media`，根文件夹路径 `/opt/tgarchive-media`，缩略图关闭，其余保持默认 → 保存，状态显示「工作中」。

- [ ] **Step 3: 匿名访问必须被拒（本机）**

```bash
curl -s -o /dev/null -w '%{http_code}\n' -X PROPFIND -H 'Depth: 1' https://drive-dav.shinya.click/dav/tgarchive-media/
curl -s -X POST https://drive.shinya.click/api/fs/list -H 'Content-Type: application/json' \
  -d '{"path":"/tgarchive-media","page":1,"per_page":1}' | jq -c '{code,message}'
```

Expected：`401`；JSON 的 `code` 不是 `200`（访客无权访问）。任一为可访问，立即请站长在 OpenList 删除该存储并报告，不继续后续步骤。

- [ ] **Step 4: 站长在 NAS 上安装拉取脚本与定时任务**

请站长 `ssh shinya@192.168.7.146` 后依次执行：

```bash
command -v rclone && rclone lsd vps-dav: | grep tgarchive-media
mkdir -p ~/.local/share/vps-tgarchive-backup /vol1/1000/tgarchive-media
cat > ~/.local/share/vps-tgarchive-backup/sync.sh <<'EOF'
#!/bin/bash
# tgarchive 媒体副本：VPS OpenList /tgarchive-media（drive-dav WebDAV）→ /vol1/1000/tgarchive-media
# copy 而非 sync：VPS 侧删除不传导到这份副本。
set -euo pipefail
export PATH="$HOME/.local/bin:/usr/local/bin:/usr/bin:/bin"
DIR="$HOME/.local/share/vps-tgarchive-backup"
LOG="$DIR/sync.log"
DEST=/vol1/1000/tgarchive-media
echo "===== $(TZ=Asia/Shanghai date '+%F %T') start" >> "$LOG"
if rclone copy vps-dav:tgarchive-media "$DEST" \
    --exclude '*.part' \
    --transfers 4 --checkers 8 --no-unicode-normalization \
    --retries 10 --low-level-retries 20 --timeout 5m \
    --log-level INFO --log-file "$LOG"; then rc=0; else rc=$?; fi
echo "===== $(TZ=Asia/Shanghai date '+%F %T') done rc=$rc" >> "$LOG"
tail -n 5000 "$LOG" > "$LOG.tmp" && mv "$LOG.tmp" "$LOG"
exit "$rc"
EOF
chmod 700 ~/.local/share/vps-tgarchive-backup/sync.sh
crontab -l 2>/dev/null | grep -q vps-tgarchive-backup/sync.sh || \
  (crontab -l 2>/dev/null; echo '15 5 * * * /usr/bin/flock -n /tmp/vps-tgarchive-sync.lock $HOME/.local/share/vps-tgarchive-backup/sync.sh') | crontab -
crontab -l | grep vps-tgarchive
/usr/bin/flock -n /tmp/vps-tgarchive-sync.lock ~/.local/share/vps-tgarchive-backup/sync.sh; echo "rc=$?"
grep -E '^===== ' ~/.local/share/vps-tgarchive-backup/sync.log | tail -2
```

Expected：第一行打印 rclone 路径且列出 `tgarchive-media`；crontab 含新行；手动运行 `rc=0`；日志末两行为 `start` 与 `done rc=0`。此时媒体目录可能还是空的，两端对账放在 Task 13 Step 5。

---

### Task 12: 基础设施文档

在本机编辑 `/Users/shinya/Documents/server/` 下的文档（该目录不是 git 仓库，直接改文件；用 Edit 工具做精确替换，不用 `sed -i`，`AGENTS.md` 是指向 `CLAUDE.md` 的符号链接）。全部中文，只写现状。计数类数值先现场测量，与下列期望值不符时写测量值并在汇报中说明。

下面每处修改都写成「原文」与「改为」两个代码块：原文必须在文件中唯一匹配；「插入」类修改给出锚点行的开头与要插入的完整内容。

**Files:**
- Modify: `CLAUDE.md`、`dns.md`、`backup.md`、`gotchas.md`、`current-state.md`、`sso.md`、`bark.md`、`inspection.md`、`disaster-recovery.md`

**Interfaces:**
- Consumes: Task 6–11 的实际结果
- Produces: 文档与现场一致

- [ ] **Step 1: 测量计数**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
echo "containers $(docker ps -q | wc -l)"
echo "caddy-hosts $(grep -oE '^[a-z0-9.-]+\.shinya\.click' /opt/stacks/caddy/Caddyfile | sort -u | wc -l)"
echo "import-compress $(grep -c 'import compress' /opt/stacks/caddy/Caddyfile)"
docker exec caddy wget -qO- http://cup:8000/api/v3/json | jq -c .metrics
EOF
bash -c 'cd /Users/shinya/Documents/server
TOKEN=$(cat .cf-saas-token)
echo "custom-hostnames $(curl -s -H "Authorization: Bearer $TOKEN" "https://api.cloudflare.com/client/v4/zones/0c26b19f9047669baac1968747cdc35f/custom_hostnames?per_page=100" | jq ".result | length")"
echo "recordsets $(HC_REGION=ap-southeast-3 python3 .huawei-tool.py GET "/v2.1/zones/ff8080829e039233019e07c8152b3eaf/recordsets?limit=500" | jq ".recordsets | length")"'
```

期望：containers 42、caddy-hosts 31、import-compress 27、custom-hostnames 30、recordsets 207；Cup `monitored_images` 为 34（current-state 记录的 33 加 1）。

- [ ] **Step 2: CLAUDE.md**

(1) §0 路由表

原文：

```
Komari、Danmu、blog-comment、TREK、Linkding、Ignis） | gotchas.md |
```

改为：

```
Komari、Danmu、blog-comment、TREK、Linkding、Ignis、TGArchive） | gotchas.md |
```

(2) §2 服务表：在以 `| https://keep.shinya.click |` 开头的行**之前**插入一行：

```
| https://tg.shinya.click | TGArchive Telegram 机器人消息存档，`ghcr.io/senshinya/tgarchive:0.1.0`，容器 `tgarchive`，8080；源码 [senshinya/tgarchive](https://github.com/senshinya/tgarchive)（公开，`v*` tag 由 GitHub Actions 在 amd64/arm64 原生 runner 构建多架构镜像，Cup 按 semver 监控）。容器内以子进程托管官方 `telegram-bot-api` 10.3（`--local`，仅监听 127.0.0.1:8081），Linux Pdeathsig 保证不留孤儿；compose `init: true`、`stop_grace_period: 30s`。机器人 token、`api_id`/`api_hash` 与 userbot 登录只在 WebUI 设置并加密入库，不进环境变量。Caddy 整站 Authelia `forward_auth`，在同一 `route` 内先剥入站 `Remote-*` 再鉴权；`/media/*` 与 `/api/events` 不压缩，SSE `flush_interval -1`；应用对缺 `Remote-User` 的请求返回 401（`/healthz` 除外），探活 `http://tgarchive:8080/healthz`。以 UID/GID 10001:10001 运行，数据属主相同；`notify.json` 以 ACL 授 10001 只读。media、botapi 不进 R2，媒体由 NAS 经 OpenList `/tgarchive-media` + drive-dav 拉取。约束与破窗见 gotchas.md。 | `/opt/stacks/tgarchive`（数据 `/opt/app/tgarchive/{db,media,avatars,botapi,botapi-tmp}`） |
```

(3) §2 OpenList 行

原文：

```
容器固定 UID 1001，PUID/PGID 不生效；bind mount 属主为 1001:1001。 | `/opt/stacks/openlist` |
```

改为：

```
容器固定 UID 1001，PUID/PGID 不生效；bind mount 属主为 1001:1001。另有只读本机存储 `/tgarchive-media`（挂载 tgarchive 媒体，供 NAS 拉取）。 | `/opt/stacks/openlist` |
```

(4) §2 计数

原文：

```
现役服务共 41 个运行容器、34 个公网 Web 域名。
```

改为：

```
现役服务共 42 个运行容器、35 个公网 Web 域名。
```

(5) §4 compress 引用数

原文：

```
**26 个站点块通过 `import compress` 引用**
```

改为：

```
**27 个站点块通过 `import compress` 引用**
```

(6) §4：在「故意不加的 4 个」表格最后一行（以 `| \`memos\` |` 开头）之后空一行，插入：

```
`tg` 站点块只在 `handle` 兜底分支里 `import compress`：`/media/*`（二进制 + Range）与 `/api/events`（SSE）两条路径刻意不压缩。
```

(7) §5 OpenList 条目

原文：

```
单一 local 存储 `/网盘` → `/opt/app/openlist/drive`（1:1 不改名）。
```

改为：

```
local 存储 `/网盘` → `/opt/app/openlist/drive`（1:1 不改名）；另有只读本机存储 `/tgarchive-media` → 容器内 `/opt/tgarchive-media`（宿主 `/opt/app/tgarchive/media`，compose `:ro` 挂载），NAS 用同一账号经 WebDAV 拉取。
```

(8) §5：在以 `- \`/opt/stacks/bonsai/.env\`` 开头的条目**之前**插入：

```
- `/opt/stacks/tgarchive/.env` — 只有 `TOKEN_ENC_KEY`（64 位 hex，AES-256-GCM 主密钥，加密库内 bot token、userbot session 与 `api_id`/`api_hash`；**丢失或更换 = 以上全部无法解密**，存档消息与媒体为明文不受影响；台账存 Vaultwarden）。机器人 token、`api_id`/`api_hash` 与 userbot 登录只在 WebUI 设置。`/opt/app/tgarchive/db/tgarchive.db` 里的 userbot session 等同该 Telegram 账号的完整登录权限，与 `TOKEN_ENC_KEY` 同为最高敏感级。`/opt/app/tgarchive/botapi/` 是本地 Bot API 服务器状态，子目录名即 bot token（mode 700），不进 R2。`/opt/app/bark/notify.json` 以 POSIX ACL 授 UID 10001 只读，单文件挂载进 tgarchive，修改须保 inode。
```

- [ ] **Step 3: dns.md**

(1) 原文：

```
Huawei DNS 共 201 个 recordsets，CF SaaS 有 29 个 Custom Hostnames
```

改为：

```
Huawei DNS 共 207 个 recordsets，CF SaaS 有 30 个 Custom Hostnames
```

(2) 原文：

```
Caddy 提供 30 个源站 HTTPS host：27 个由 Caddy 回源的 SaaS 子域
```

改为：

```
Caddy 提供 31 个源站 HTTPS host：28 个由 Caddy 回源的 SaaS 子域
```

(3) 原文：

```
services / trek / umami
```

改为：

```
services / tg / trek / umami
```

(4) 原文：

```
现役 29 个 SaaS 子域对应 87 条 ISP 记录
```

改为：

```
现役 30 个 SaaS 子域对应 90 条 ISP 记录
```

- [ ] **Step 4: backup.md**

(1) 原文：

```
Bonsai SVG、Navidrome cache、TREK 升级前快照
```

改为：

```
Bonsai SVG、Navidrome cache、tgarchive 的 media / botapi / botapi-tmp、TREK 升级前快照
```

(2) 原文：

```
forgejo-runner 四个 CI 缓存目录、Karakeep `data.preimport-*` |
```

改为：

```
forgejo-runner 四个 CI 缓存目录、Karakeep `data.preimport-*`、tgarchive 的 `media/*`、`botapi/*`、`botapi-tmp/*`（用户可能把 `.db`/`.sqlite` 当文档存档，它们落在扫描深度内） |
```

(3) 在 `## 新应用是否需要改备份脚本` 这一行**之前**插入整节（末尾留一个空行）：

```markdown
## tgarchive 媒体：不进 R2，改由 NAS 只读拉取

`/opt/app/tgarchive/media`（Telegram 存档的图片、视频与文件）以及 `botapi/`、`botapi-tmp/`（本地 Bot API 服务器状态与临时下载）都在 `exclude.txt` 中，同时写进 `SQLITE_SKIP`：用户可能把 `.db`/`.sqlite` 当文档发给机器人，它们以原扩展名落在 `media/<bot>/<yyyy>/<mm>/`，深度在 SQLite 扫描范围内，只加 `exclude.txt` 挡不住 staging。R2 只备份 `db/tgarchive.db`（SQLite stage）、`avatars/` 与 stack。`botapi/` 可再生：丢失后本地 Bot API 服务器按 token 重新初始化，只丢失当时还在其内部队列、尚未被应用取走的更新。

媒体副本由 NAS 拉取，传输路径与音乐库相同（`drive-dav` 灰云 + mihomo 代理节点 + OpenList 本地账号），凭据只在 NAS：

| 项 | 值 |
|---|---|
| VPS 暴露方式 | OpenList 只读本机存储 `/tgarchive-media`，根文件夹 `/opt/tgarchive-media`（OpenList compose 以 `:ro` 挂载 `/opt/app/tgarchive/media`）；匿名访问返回 401 |
| 脚本 | NAS `~/.local/share/vps-tgarchive-backup/sync.sh`（700） |
| 定时 | 用户 crontab `15 5 * * *`（北京时间 05:15，错开 04:30 的音乐同步），`flock -n /tmp/vps-tgarchive-sync.lock` |
| 凭据 | 复用 `~/.config/rclone/rclone.conf` 的 remote `vps-dav` |
| 目标目录 | `/vol1/1000/tgarchive-media` |
| 日志 | 同目录 `sync.log`，截到最近 5000 行，以 `===== <时间> start` / `===== <时间> done rc=<码>` 分隔 |
| 关键 flag | `--exclude '*.part' --transfers 4 --checkers 8 --no-unicode-normalization --retries 10 --low-level-retries 20 --timeout 5m` |

- **`copy` 不是 `sync`**：WebUI 删除存档后 VPS 侧文件被清理，NAS 副本保留；需要彻底删除时在 NAS 手删。
- 媒体文件名为 `sha1(dedupe_key).<ext>`，只写一次、不改写，两端对账比较文件数与字节总和即可（inspection.md W8）。
- 应用把归档文件统一设为 0644：本地 Bot API 服务器下载的文件是 0600，硬链接后共用 inode，不放宽的话 OpenList（UID 1001）读不到，NAS 会以 `rc=0` 静默漏拉。
- 媒体目录在 OpenList 中只读，网页端能看到但不能写入。
```

- [ ] **Step 5: gotchas.md**

文件末尾追加整节（与上一节之间空一行）：

````markdown
## TGArchive

`tg.shinya.click`，`/opt/stacks/tgarchive`，镜像 `ghcr.io/senshinya/tgarchive`。

### 镜像与升级

- 只跟随 GitHub 上的 `v*` 发布：compose 固定 semver tag，`main` / `sha-*` tag 只用于测试，不部署。Cup 按 semver 监控，不在 exclude 中。
- 升级：改 compose 的 tag → `docker compose pull && docker compose up -d` → 等 healthy。应用启动时按 `PRAGMA user_version` 自动迁移 SQLite，旧二进制读不了新 schema；有迁移的版本先 `sqlite3 /opt/app/tgarchive/db/tgarchive.db ".backup /opt/app/maintenance-<日期>-updates/tgarchive.db"`，回滚时停容器、恢复该文件、删除 WAL/SHM、换回旧 tag。
- 镜像内 `telegram-bot-api` 的版本随镜像固定（当前 10.3）；升级 Bot API 只能发新的 tgarchive 版本。

### 子进程与停机

- 应用以子进程托管 `telegram-bot-api`，只监听容器内 127.0.0.1:8081。Linux 下子进程设置 `Pdeathsig=SIGTERM`；compose `init: true` 由 docker-init 回收僵尸进程，`stop_grace_period: 30s` 覆盖应用关停 HTTP（≤10s）与停子进程（≤10s）。
- API 凭据未配置时子进程不启动，`/api/admin/telegram-app` 的 `server.state` 为 `unconfigured`；保存凭据后为 `running`。

### Caddy 指令顺序

站点块整体包在 `route` 里：先 `request_header -Remote-*`，再 `import authelia`，再按路径分流。Caddy 默认把 `forward_auth` 排在 `request_header` 之前，顶层写法会把认证写入的 `Remote-User` 删掉，登录后全站 401。`/media/*`（Range）与 `/api/events`（SSE）不压缩，其余 `import compress`。改动后用 adapt 核对：第一个 `reverse_proxy` 的 dial 为 `authelia:9091`，且其前恰有 4 个 `headers`。

```bash
docker exec caddy caddy adapt --config /etc/caddy/Caddyfile --adapter caddyfile 2>/dev/null \
  | jq -c '[.apps.http.servers[].routes[] | select(tostring|test("tg.shinya.click")) | .. | objects | select(has("handler")) | [.handler, (.upstreams[0].dial? // "")]]'
```

前端 `/assets/*` 带扩展名，CF 会按默认规则缓存，未登录访客可能从边缘取到前端脚本；脚本即公开仓库的构建产物，不含数据。`/media/*`、`/avatars/*` 与 API 均返回 `private` / `no-cache`，不进 CF 缓存。

### Bark 凭据

`/opt/app/bark/notify.json`（root 600）以 POSIX ACL `u:10001:r` 授权，并单文件挂载到容器 `/run/bark/notify.json`。编辑器的原子保存会换 inode 并丢掉 ACL：容器仍读旧内容或读不到，机器人出错时 Bark 静默不推（只在容器日志留 `notify: read …`）。修改只用 `cat tmp > notify.json`，改完核对：

```bash
getfacl -p /opt/app/bark/notify.json | grep 'user:10001:r--'
docker exec tgarchive sha256sum /run/bark/notify.json; sha256sum /opt/app/bark/notify.json
```

ACL 丢失时重跑 `setfacl -m u:10001:r /opt/app/bark/notify.json`；inode 已变时 `docker restart tgarchive`。推送请求带 `User-Agent: tgarchive-notify/1.0`，不经 `send.py`，所以不进 `delivery.jsonl`。

### 本地 Bot API 与机器人状态

- 添加机器人时应用先对云端 `api.telegram.org` 调 `logOut`；此后 10 分钟内该 bot 无法再登录云端 Bot API，本地服务器可立即使用。迁回云端：先在 WebUI 删除该机器人，再对本地服务器调用 `logOut`（`docker exec tgarchive wget -qO- "http://127.0.0.1:8081/bot<token>/logOut"`），距上次云端 `logOut` 满 10 分钟后即可在云端使用。
- 409 Conflict：`terminated by other getUpdates request` 表示另有进程在用同一 token 拉取（旧部署、开发实例或别的程序）；`can't use getUpdates method while webhook is active` 表示 token 设置了 webhook，先 `deleteWebhook`。这两种情况及 401（token 在 BotFather 被 revoke）都会让 worker 停止、状态 `error` 并推 Bark，不自动重试；处理完原因后在管理页重新启用该机器人。
- `botapi/` 下的子目录名就是 bot token，目录 700，不进 R2；丢失后本地服务器按 token 重新初始化。

### my.telegram.org

`api_id` / `api_hash` 在 https://my.telegram.org → API development tools 申请，本地 Bot API 与 userbot 共用，在管理 → API 凭据填写。创建应用时页面只弹「ERROR」是常见现象，多与访问网络或表单内容有关：换用常用网络与浏览器、关闭广告拦截，App title / Short name 只用英文字母和数字后重试。

### userbot 风险与破窗

- userbot 违反 Telegram 使用条款，账号存在受限风险；session 等同该账号完整登录权限，加密存在 `db/tgarchive.db`。
- 官方客户端踢掉会话或 session 失效（`AUTH_KEY_UNREGISTERED` / `SESSION_REVOKED`）时状态变为 `error` 并推 Bark，在管理 → 用户账号重新登录即可；期间链接请求回复「代取账号未登录」。
- 怀疑 session 或 `TOKEN_ENC_KEY` 泄露：先在手机 Telegram → 设置 → 设备中终止该会话，再在 BotFather revoke 各机器人 token。
- `TOKEN_ENC_KEY` 丢失或更换后，库内 bot token、userbot session、`api_id`/`api_hash` 都无法解密；存档消息与媒体是明文，不受影响。需重新填写 API 凭据、重新配置机器人并重新登录 userbot。
````

- [ ] **Step 6: current-state.md**

(1) 原文：

```
TREK 为 2026-09-24 验证状态。
```

改为（`<执行日期>` 用执行当天的 `YYYY-MM-DD`）：

```
TREK 为 2026-09-24 验证状态；TGArchive 为 <执行日期> 验证状态。
```

(2) 原文：

```
41 个容器运行正常
```

改为：

```
42 个容器运行正常
```

(3) 原文：

```
| 公网 Web | 34 个业务域名，其中 30 个由源站 Caddy 提供 HTTPS
```

改为：

```
| 公网 Web | 35 个业务域名，其中 31 个由源站 Caddy 提供 HTTPS
```

(4) 原文：

```
| DNS | Huawei 201 个 recordsets，CF SaaS 29 个 Custom Hostnames，LAN SAAS 清单 29 项 |
```

改为：

```
| DNS | Huawei 207 个 recordsets，CF SaaS 30 个 Custom Hostnames，LAN SAAS 清单 30 项 |
```

(5) 原文：

```
| Cup | 33 个监控镜像
```

改为（数字取 Step 1 的 `monitored_images`）：

```
| Cup | 34 个监控镜像
```

(6) 在以 `| Ignis |` 开头的行**之前**插入：

```
| TGArchive | 0.1.0（senshinya/tgarchive），telegram-bot-api 10.3；容器 healthy，UID 10001；CF SaaS 与源站证书 active；匿名与伪造 `Remote-User` 经 CF 与源站均跳转 Authelia，内网缺 `Remote-User` 返回 401、`/healthz` 正常；`notify.json` ACL 生效，容器内可读且与宿主一致；OpenList 只读存储 `/tgarchive-media` 匿名 401；首次使用（API 凭据、添加机器人、userbot 登录）与 NAS 首轮拉取尚待用户验收 |
```

(7) 在以 `- **音乐库不进 R2**` 开头的条目**之后**插入：

```
- **tgarchive 媒体不进 R2**，`media/`、`botapi/`、`botapi-tmp/` 同时在 `exclude.txt` 与 `SQLITE_SKIP` 中；媒体副本由 NAS 每日 05:15 CST 经 OpenList `/tgarchive-media` + `drive-dav` 拉到 `/vol1/1000/tgarchive-media`（`copy` 非 `sync`）。机制见 [backup.md](backup.md)。
```

- [ ] **Step 7: sso.md**

在以 `| Navidrome |` 开头的行**之后**插入：

```
| TGArchive | Caddy `forward_auth`（整站）+ 应用侧 `Remote-User` 兜底 | 同一 `route` 内先剥入站 `Remote-*` 再 `import authelia`；应用在 `REQUIRE_FORWARD_AUTH=true`（默认）时对缺 `Remote-User` 的请求返回 401（`/healthz` 除外）。无用户系统、不创建 OIDC client；Glance 探活走内网 `http://tgarchive:8080/healthz` |
```

- [ ] **Step 8: bark.md**

(1) 原文：

```
| `/opt/app/bark/notify.json` | VPS 通知 endpoint 与 `device_keys` 数组；600 |
```

改为：

```
| `/opt/app/bark/notify.json` | VPS 通知 endpoint 与 `device_keys` 数组；600，另以 ACL 授 UID 10001（tgarchive）只读；单文件挂载进 tgarchive，修改须保 inode（`cat tmp > notify.json`） |
```

(2) 在以 `| LAN \`/opt/app/tailscale/relay-switch.sh\`` 开头的行**之后**插入：

```
| tgarchive（机器人或 userbot 进入 error） | docker | — | timeSensitive；应用直接 POST Bark，不经 `send.py`，不写 `delivery.jsonl` |
```

- [ ] **Step 9: inspection.md**

(1) 原文：

```
| **NAS 音乐备份（无通道）** | **没有任何被动告警**——cron 跑挂、rclone 失败、NAS 离线都不会有人知道 | D11 每日核验心跳（cron 在、昨晚跑过、rc=0）+ W7 每周两端对账（`rc=0` 证明不了完整） |
```

改为：

```
| **NAS 音乐与 tgarchive 媒体备份（无通道）** | **没有任何被动告警**——cron 跑挂、rclone 失败、NAS 离线都不会有人知道 | D11 每日核验两条心跳（cron 在、昨晚跑过、rc=0）+ W7 / W8 每周两端对账（`rc=0` 证明不了完整） |
```

(2) 原文：

```
DAG 失败也推 timeSensitive（2026-08-11 起 `handler_on.failure`） |
```

改为：

```
DAG 失败也推 timeSensitive（2026-08-11 起 `handler_on.failure`）；tgarchive 机器人或 userbot 进入 error 推 timeSensitive（不进 delivery.jsonl） |
```

(3) 在 `## 每周段（周一或「深度巡检」时加做）` 这一行**之前**插入：

````markdown
tgarchive 媒体拉取是同一类链路（每天 **北京 05:15**），按同样的判据核验：

```bash
/usr/bin/ssh shinya@192.168.7.146 '
  crontab -l | grep -q "vps-tgarchive-backup/sync.sh" && echo "cron: OK" || echo "cron: ⚠️ 条目丢失"
  flock -n /tmp/vps-tgarchive-sync.lock true 2>/dev/null && echo "flock: 空闲" || echo "flock: ⚠️ 仍被持有"
  grep -E "^===== " ~/.local/share/vps-tgarchive-backup/sync.log | tail -4
  date +"now:  %FT%T%:z"'
```

````

(4) 文件末尾追加：

````markdown
### W8 tgarchive 媒体两端对账（本机执行，需家庭局域网）

媒体文件名为 `sha1(dedupe_key).<ext>`，只写一次不改写，对账只比文件数与字节总和（排除 `.part`）：

```bash
cat > /tmp/tga-count.py <<'PYEOF'
import os
d = next(p for p in ('/opt/app/tgarchive/media', '/vol1/1000/tgarchive-media') if os.path.isdir(p))
t = n = 0
for r, _, fs in os.walk(d):
    for f in fs:
        if f.endswith('.part'):
            continue
        t += os.path.getsize(os.path.join(r, f)); n += 1
print(f'{n} 文件, {t} 字节')
PYEOF
/usr/bin/ssh root@152.53.243.102  python3 - < /tmp/tga-count.py
/usr/bin/ssh shinya@192.168.7.146 python3 - < /tmp/tga-count.py
```

NAS 的数 **≥** VPS 的数为健康：`copy` 不传导删除，WebUI 删过存档后 NAS 会多出文件。NAS 少于 VPS 时，先看缺的是否为最近一次 05:15 之后才入库的；更早的也缺，查 `sync.log` 的 rclone 报错，并在 VPS 跑 `find /opt/app/tgarchive/media -type f ! -perm -o+r | wc -l`（应为 0）。SSH 连不上 = 不在家庭局域网，记「跳过」。
````

- [ ] **Step 10: disaster-recovery.md**

在以 `| Bonsai |` 开头的行**之后**插入：

```
| TGArchive | `/opt/app/tgarchive/db/tgarchive.db` + `avatars/` + stack `.env` 的 `TOKEN_ENC_KEY`；媒体副本在 NAS `/vol1/1000/tgarchive-media` | SQLite stage + 同批密钥；媒体从 NAS 拷回 `media/`；`botapi/` 可再生；属主 10001:10001；重建 `notify.json` 的 ACL（`setfacl -m u:10001:r`） |
```

- [ ] **Step 11: 核对**

```bash
cd /Users/shinya/Documents/server
for f in CLAUDE.md dns.md backup.md gotchas.md current-state.md sso.md bark.md inspection.md disaster-recovery.md; do
  printf '%-22s %s\n' "$f" "$(grep -ciE 'tg\.shinya\.click|tgarchive|/ tg /' "$f")"
done
test -L AGENTS.md && readlink AGENTS.md
```

Expected：每个文件计数 ≥ 1；`AGENTS.md` 仍是指向 `CLAUDE.md` 的符号链接（若被替换成普通文件：`rm AGENTS.md && ln -s CLAUDE.md AGENTS.md`）

---

### Task 13: 首次使用与上线验收

**Files:** 无（用户在 WebUI 操作；controller 核验服务器状态）；验收结果回写 `current-state.md` 的 TGArchive 行

**Interfaces:**
- Consumes: 上述全部
- Produces: 机器人在线、userbot 就绪、备份与 NAS 链路被真实数据验证

- [ ] **Step 1: 站长：填 API 凭据**

浏览器打开 https://tg.shinya.click → Authelia 登录 → 应看到空会话列表 → 左下齿轮「管理」→「API 凭据」：填 my.telegram.org 的 `api_id` / `api_hash` → 保存，状态显示 Bot API「运行中」。

controller 核验：

```bash
/usr/bin/ssh root@152.53.243.102 'docker exec tgarchive ps -o pid,user,args | grep telegram-bot-api; docker exec tgarchive netstat -ltn | grep 8081; docker logs --since 5m tgarchive 2>&1 | tail -20'
```

Expected：`telegram-bot-api --local --dir=/data/botapi --temp-dir=/data/botapi-tmp --http-port=8081 --http-ip-address=127.0.0.1` 以 `tgarchive` 用户运行；`127.0.0.1:8081` 在 LISTEN；日志无反复重启

- [ ] **Step 2: 站长：添加机器人并收发（Review Focus 5 的真实登录路径）**

「管理」→「添加机器人」：粘贴 BotFather token → 「校验 token」「从云端 Bot API 登出」「开始接收消息」三步全部成功 → 白名单加自己的 user ID（开启「允许代取受保护内容」）。用手机给机器人依次发：一段带格式的文本、一张图片、一个 >20MB 的视频、一个小的 `.sqlite` 文件（`sqlite3 /tmp/t.sqlite 'create table t(x)'` 生成，作为文档发送）。期望：每条消息先出现 👀、媒体落盘后变 👌；WebUI 实时出现并能播放 / 下载。

- [ ] **Step 3: controller 核验落盘、权限与 botapi 清理（Review Focus 2）**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
find /opt/app/tgarchive/media -type f -printf '%m %u %s %p\n' | head -20
echo "not-world-readable: $(find /opt/app/tgarchive/media -type f ! -perm -o+r | wc -l)"
find /opt/app/tgarchive/botapi -mindepth 3 -type f | wc -l
docker logs --since 10m tgarchive 2>&1 | grep -iE 'error|failed' | tail -10
EOF
```

Expected：媒体文件均为 `644`，属主显示 `10001`（宿主无此用户名）；`not-world-readable: 0`；`botapi` 下第三层残留文件数为 0 或只剩正在下载的；日志无错误

- [ ] **Step 4: 站长：userbot 登录与代取（可选但建议）**

「管理」→「用户账号」：手机号 → 验证码 →（如有）二步验证密码 → 状态「已登录」。给机器人发一条受保护频道的消息链接，期望 👀 → 👌，WebUI 出现「来自 <群名> · 受保护」的消息。

- [ ] **Step 5: NAS 首轮拉取与对账（站长在 NAS，controller 在 VPS）**

站长在 NAS 执行 `/usr/bin/flock -n /tmp/vps-tgarchive-sync.lock ~/.local/share/vps-tgarchive-backup/sync.sh; echo rc=$?`，然后按 inspection.md W8 两端计数。Expected：`rc=0`，两端文件数与字节总和相同。

- [ ] **Step 6: 备份范围实测（Review Focus 1）**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
docker exec dagu dagu start backup-daily
for i in $(seq 1 60); do
  line=$(docker exec dagu dagu history backup-daily 2>/dev/null | sed -n 2p)
  echo "$line"
  echo "$line" | grep -qE 'Succeeded|Failed' && ! echo "$line" | grep -q Running && break
  sleep 30
done
source /opt/app/backup/r2.env
restic --no-lock snapshots --latest 1 --json | jq -r '.[0].time'
echo "excluded-dirs-in-snapshot: $(restic --no-lock ls latest | grep -cE '^/opt/app/tgarchive/(media|botapi|botapi-tmp)(/|$)')"
restic --no-lock ls latest | grep -E 'tgarchive' | grep -vE '^/opt/app/tgarchive(/|$)'
restic --no-lock ls latest | grep -cE '^/opt/app/tgarchive/db/tgarchive\.db$'
EOF
```

Expected：history 行显示刚才这次运行 `Succeeded`；最新快照时间是几分钟前（不是更早的日备）；`excluded-dirs-in-snapshot: 0`；第三条只列出 stage 中的 `tgarchive.db` 副本，**不得**出现 Step 2 发送的 `.sqlite` 文件（`<sha1>.sqlite`）；最后一行 `1`。快照时间若仍是旧的，说明 `dagu start` 未阻塞且本轮尚未结束，继续等到 history 出现新的 STARTED 时间再重跑后四条。

- [ ] **Step 7: 重启与停机时长**

```bash
/usr/bin/ssh root@152.53.243.102 'bash -s' <<'EOF'
cd /opt/stacks/tgarchive
time docker compose stop
docker inspect -f '{{.State.ExitCode}}' tgarchive
docker compose up -d
for i in $(seq 1 30); do s=$(docker inspect -f '{{.State.Health.Status}}' tgarchive); [ "$s" = healthy ] && break; sleep 2; done; echo "$s"
sleep 5; docker logs --since 1m tgarchive 2>&1 | grep -iE 'address already in use|botapi' | tail -5
EOF
```

Expected：`stop` 耗时远小于 30s；退出码 `0`；重启后 `healthy`；无 `address already in use`

- [ ] **Step 8: 回写验收结果**

把 `current-state.md` TGArchive 行末尾的「首次使用（API 凭据、添加机器人、userbot 登录）与 NAS 首轮拉取尚待用户验收」替换为实际结果，例如全部通过时写：「API 凭据、机器人收发（文本 / 图片 / >20MB 视频 / 文件）与 👀→👌 回执、userbot 登录与受保护链接代取均已验收；归档文件 0644；R2 快照不含 media / botapi，SQLite stage 只含主库；NAS 首轮拉取两端文件数与字节一致；停机 <30s、重启无端口冲突」。未完成的项目逐项保留「尚待用户验收」。spec §11 的其余条目（Web A 并排截图、吊销 token 触发 Bark 等）由站长按需验收，不在本计划内。
