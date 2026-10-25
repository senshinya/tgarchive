# 视频库实现计划

**目标：** 将已确认的视频库与帖子合集示意接入 TGArchive，并持久化观看进度。

**架构：** SQLite 在服务端聚合完整帖子、筛选与分页；Preact 专用页面消费有界 API；复用 Vidstack、鉴权和现有收藏标签。后台线程仅负责缺失视频封面的生成。

**技术栈：** Go 1.26、SQLite、Preact、TypeScript、SCSS、Vidstack、ffmpeg。

## 1. 存储与 HTTP

- [x] 新增 `internal/store/video_library_test.go`，先验证分组、混合相册、分页、检索、收藏、进度和坏输入。
- [x] 新增迁移 `0012_video_library.sql`，观看进度和库级标题/封面关联；已有媒体/收藏不迁移复制。
- [x] 新增 `internal/store/video_library.go`：`ListVideoLibrary`、`GetVideoLibraryItem`、`SaveVideoProgress`、`UpdateVideoLibraryItem`。
- [x] 新增 `internal/httpapi/video_library.go` 与集成测试；在 `server.go` 注册 `/api/video-library`、`/{id}`、`/media/{id}/progress`。所有写请求沿用既有中间件。
- [x] `env -u GOROOT go test ./internal/store ./internal/httpapi` 通过后审查契约。

## 2. 前端与播放

- [x] `api/types.ts`、`api/client.ts`、测试 fixture 加入视频库 API 契约。
- [x] `lib/router.ts` 增加 `/videos`、`/videos/{id}`、`/videos/{id}/play/{mediaId}`，保持 URL 参数与返回历史。
- [x] `components/library/` 按列表、详情、播放器、卡片、进度存储分文件；先写行为测试，再实现。
- [x] `App.tsx` 专用视频库布局，`ChatsPanel.tsx` 增加入口。使用现有主题 token 与组件；不复制独立示意页的假播放。
- [x] 使用现有 `createVideoSlide` 的播放器与浏览器设置，通过可选 storage 和 ended 回调扩展复用。
- [x] 运行 `npm test` 与 `npm run build`，验证路由、收藏、队列和服务端进度。

## 3. 封面

- [x] 新增 `internal/videocover/` 工作线程与命令测试；对普通视频和 video/* 文件缺失缩略图生成 JPEG，命令有超时/限线程/原子安装。
- [x] `internal/app/app.go` 接入启动、下载完成唤醒和关闭；生成后发布 media.updated。
- [x] 列表只使用缩略图或占位，详情可选合集中的其他视频为封面。

## 4. 集成与交付

- [x] 更新 README 当前功能与 API 使用说明，保存测试证据。
- [x] `env -u GOROOT go test ./...`、前端全测、production build、`git diff --check`。
- [x] 使用隔离模拟数据启动真实应用，验收桌面/手机、两套主题、搜索/合集/播放/返回。
- [x] 规格与质量复核，修正问题后提交功能分支保留本地功能分支供审查。正式 release 和 NAS 部署遵循现有维护约束。
