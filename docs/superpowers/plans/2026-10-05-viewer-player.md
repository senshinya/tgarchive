# 媒体查看器与播放器 · 聊天背景对齐 实施计划

**Goal:** 实现 spec 并以 v0.4.0 上线。
**Spec:** `docs/superpowers/specs/2026-10-05-viewer-player-design.md`
**执行方式:** 本会话内顺序实现（native），收尾由独立 reviewer 审整个分支。

## Global Constraints
- 前端：Preact + signals；不用 `innerHTML` 拼接用户内容；`localStorage` 读写包 try/catch
- photoswipe 5.4.4、vidstack 1.15.6 精确版本；查看器 chunk 懒加载
- 每个任务先写失败测试；`npm test`、`npm run build`、`go test ./...` 全绿后提交
- 提交信息尾行 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`

## Review Focus
1. 返回键 / Esc / 关闭按钮 / 下拉关闭四条路径都只关查看器，不多退一层历史
2. 拖动视频进度条、音量条不得触发翻页或下拉关闭
3. 翻页离开视频必须暂停，关闭查看器后不得有残留音频
4. 记忆存储在 localStorage 不可用或数据损坏时不抛错
5. 列表分页加载增长时当前项不跳变

## Tasks
### Task 1：聊天背景对齐 Web A
- `lib/wallpaper.ts`：官方静态渐变算法（纯函数，可测）+ canvas 绘制
- `Wallpaper` 组件：canvas + 花纹层；主题切换重绘；替换 `public/pattern.svg` 为官方图案
- 测试：像素函数

### Task 2：依赖与懒加载外壳
- 安装 photoswipe、vidstack；`MediaViewer` 外壳保留 history/分页逻辑，懒加载 `Gallery`
- `ViewerItem` 增加 `thumbId/width/height`

### Task 3：Gallery（PhotoSwipe）
- 实例创建/销毁、dataSource 更新、顶栏/说明自定义 UI、键盘分流、关闭接线

### Task 4：视频页（Vidstack）+ 长按倍速 + 冲突处理 + 记忆存储

### Task 5：缩略图条

### Task 6：验收与发布
- mock 验收（桌面、390 浅/深、800）→ 用户 iPhone 实测 → reviewer → 合并 → tag v0.4.0 → 部署 → 冒烟 → 基础设施文档
