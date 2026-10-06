# 频道监听 实施计划

**Goal:** 实现 spec 中频道监听并以 v0.5.0 上线。
**Spec:** `docs/superpowers/specs/2026-10-06-channel-watch-design.md`
**执行方式:** 本会话内顺序实现（native），收尾由一个独立 reviewer 审整个分支。

## Global Constraints
- Go：modernc sqlite 单连接，持有 Rows 时不得再发查询
- 前端：Preact + signals；链接经 `safeHref`；不用 `innerHTML`；`localStorage` 读写包 try/catch
- 每个任务先写失败测试再实现；`go test ./...`、`npm test`、`npm run build` 全绿后提交
- 提交信息尾行 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`

## Review Focus
1. 0005 迁移重建 chats：旧库升级不得因 ON DELETE CASCADE 丢消息；FK 检查必须通过
2. 私聊相关旧查询遇到 bot_id/sender_id 为 NULL 的频道会话不得报错（会话列表、头像刷新、回执、合并时间线）
3. Poller 不回溯老帖；相册整组判定与整组存档；同一帖子不会重复存档
4. userbot 离线/重连期间：到期帖子补判一次后丢弃，监听不进入 error
5. 删除监听「仅停止」不得删存档；purge 必须清理无引用媒体但保留自定义表情媒体

## Tasks
1. 迁移执行器（FK off + check）与 0005；store：channels、chats kind、watches CRUD、pending、频道 Ingest、ListChats 扩展、custom_emoji、collectOrphans、回执/头像查询兼容；测试
2. `internal/watchcond`：解析/校验/求值/Explain；测试
3. userbot：解析逻辑抽取共享；Poller（拉新、判定、存档、错误、事件、Bark）；自定义表情登记；频道头像下载；频道列表/搜索/解析/试算服务；测试（tgmock 假 TG）
4. httpapi + app 接线：channels、watches、watch-settings 接口，ListChats 字段，avatars channels，维护刷新；测试
5. 前端数据层：types、client、router、store（频道会话、频道标签、watch.updated）；测试
6. 前端界面：左栏、顶栏、频道气泡、设置（列表、选频道、条件编辑、试算、删除）；测试
7. 验收与发布：mock 补数据 → 桌面/手机截图验收 → 独立 reviewer → 修复 → 合并 → tag v0.5.0 → CI → 部署 → 冒烟 → 基础设施文档
