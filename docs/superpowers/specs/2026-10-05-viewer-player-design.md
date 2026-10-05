# 媒体查看器与播放器 · 聊天背景对齐 设计

> 状态：已确认（2026-10-05，方案 B）。上位 spec：`2026-10-04-tgarchive-design.md`、`2026-10-05-live-sync-bot-timeline-downloads-design.md`。

## 1. 目标

1. 查看器的操作手感对标 Telegram 客户端（移动端对标官方 App，PC 对标 Web A / Desktop）：跟手翻页、下拉跟手关闭、双指缩放、自绘视频控件、快捷键、记忆。
2. 聊天背景与 Telegram Web A 默认壁纸逐项一致。

### 成功标准
- 手机：左右滑动画面跟手、松手按距离/速度翻页或回弹；下拉画面跟手缩小、背景变淡后关闭；双指缩放平滑；视频双击左/右侧快退/快进 10 秒、长按 2 倍速
- PC：视频控件含进度条（缓冲 + 悬停时间预览）、音量、倍速、画中画、全屏，鼠标静止自动隐藏；快捷键见 §3.4
- 再次打开同一视频从上次位置继续；音量/静音/倍速跨视频保留
- 系统返回键 / Esc / 关闭按钮行为与现在一致（只关查看器）
- 背景在浅色、深色下与 Web A 默认壁纸肉眼一致
- 主包体积不因播放器增加（查看器按需加载）

### 明确不做
- 消息气泡内联播放（仍点开进查看器）
- 字幕、画质切换、投屏
- 背景随新消息扭动动画（Web A 仅在发送消息时扭动，本应用不发消息）

## 2. 依赖

| 包 | 版本 | 用途 |
|---|---|---|
| `photoswipe` | 5.4.4 | 图库：跟手翻页、下拉关闭、双指/滚轮缩放、双击放大、单击切换界面 |
| `vidstack` | 1.15.6（npm `next` 标签，钉死版本） | 视频播放器 Web Components + 默认布局（控件、手势、快捷键） |

二者均通过动态 `import()` 加载：`MediaViewer` 外壳留在主包，`viewer/Gallery.tsx` 及其依赖成独立 chunk，首次打开查看器时加载（加载中显示转圈）。

## 3. 查看器

### 3.1 结构
- `MediaViewer.tsx`（主包）：读取 `store.viewer`，保留现有「打开时 pushState、popstate 关闭、关闭时 history.back」逻辑与列表分页加载（`toViewerItems`、`VIEWER_PAGE`）；懒加载 `Gallery`
- `Gallery.tsx`（懒加载 chunk）：持有一个 PhotoSwipe 实例（`appendToEl` 为组件容器，不用 lightbox，`dataSource` 为 items 映射）；`items` 增长时更新 `dataSource` 并 `refreshSlideContent`；PhotoSwipe `close` → 调用外壳传入的 `onClose`（走 history.back）；外壳卸载 → `pswp.destroy()`
- `videoSlide.ts`：在 PhotoSwipe `contentLoad` 中为 `type: 'video'` 的项创建 `<media-player>`，`contentDeactivate` 暂停、`contentDestroy` 销毁
- `playerStorage.ts`：Vidstack `MediaStorage` 实现（§3.5）
- `Thumbs.tsx`：缩略图条（§3.6）

### 3.2 图片与翻页（PhotoSwipe）
- 选项：`wheelToZoom: true`、`maxZoomLevel: 4`、`secondaryZoomLevel: 2`、`bgOpacity: 0.9`、`closeOnVerticalDrag: true`、`showHideAnimationType: 'fade'`、`arrowKeys: false`（自管，见 §3.4）、`escKey: false`（自管）、`loop: false`、`preload: [1, 2]`
- 尺寸：用媒体的 `width/height`；缺失时先用 `naturalWidth/Height`（图片加载后 `updateSize`）
- 顶栏：用 `uiRegister` 注册自定义元素替代默认顶栏——发送人/标题、日期、`n / N`、下载链接、关闭；图片额外有缩小/放大（≤600px 隐藏）
- 说明文字：自定义底部元素，RichText，随界面显隐
- GIF/动画：`<video autoplay loop muted playsinline>`，无控件

### 3.3 视频（Vidstack）
- `<media-player src title playsinline storage=... key-target=...>` + `<media-provider>` + `<media-video-layout>`；海报用缩略图
- 默认布局提供：播放/暂停、带缓冲与悬停时间预览的进度条、时间、音量（PC）、倍速菜单（0.5–2）、画中画（支持时）、全屏（iOS 走原生）、加载转圈、自动隐藏、`translations` 中文
- 默认手势：鼠标单击播放/暂停、触摸单击切换控件、双击左/右 1/3 快退/快进 10 秒、双击中间全屏
- 长按 2 倍速（自写）：触摸按住 500ms 未移动 → `playbackRate=2` 并显示「2×」提示，松手恢复原倍速
- 与 PhotoSwipe 的冲突：在视频页上 `pointerDown` 起点位于 Vidstack 控件（`media-controls` 内的可交互元素、滑条）时 `preventDefault` 阻止 PhotoSwipe 拖动；视频页禁用 PhotoSwipe 的单击/双击动作（`tapAction`/`doubleTapAction` 过滤），交给 Vidstack
- 翻离视频页暂停；回到前一视频不自动重播（保持暂停）；打开时当前视频自动播放

### 3.4 键盘（自管 keydown，挂 window）
| 键 | 图片页 | 视频页 |
|---|---|---|
| Esc | 关闭 | 退出全屏，否则关闭 |
| ←/→ | 翻页 | 快退/快进 5 秒（Vidstack） |
| Shift+←/→、PageUp/PageDown | 翻页 | 翻页 |
| 空格/K、J/L、↑/↓、M、F、`<` `>`、0–9 | — | Vidstack 快捷键（`keyTarget="document"` 仅当前活动视频） |
| +/- | 缩放 | — |

输入框聚焦时不处理。

### 3.5 记忆（`playerStorage.ts`）
- 全局键 `tgarchive.player`：`{volume, muted, rate}`；按视频键 `tgarchive.player.pos`：`{[mediaId]: {t, at}}`
- 位置仅在 `t ≥ 10s` 且距结尾 > 10s 时保存，否则删除；最多 200 条，超出删最早 `at`
- 所有 localStorage 读写包 try/catch，失败时退化为不记忆

### 3.6 缩略图条（`Thumbs.tsx`）
- 项目数 > 1 时显示；每项 48px 方形（缩略图优先，图片无缩略图用原图，视频无缩略图显示图标），当前项高亮并滚动到可见
- 点击 `pswp.goTo(i)`；PC 常显，触摸设备随 PhotoSwipe UI 显隐（`pswp--ui-visible`）
- 需要 `ViewerItem` 增加 `thumbId?`（由 `toViewerItems` 从 `readyThumb` 填充）与 `width/height`

## 4. 聊天背景（对齐 Web A）

对照 `Ajaxy/telegram-tt`（GPL-3.0，与本项目同许可证）`src/util/wallpaper.ts`、`src/util/gradientBackground.ts`、`src/styles/_patternBackground.module.scss`：

- **花纹**：换用 Web A `src/assets/pattern.svg`（官方 doodle 图案，viewBox 1440×2960），`mask-size: 26.875rem auto`、`mask-repeat: repeat`、`mask-position: center`；原自绘 `public/pattern.svg` 删除
- **渐变**：四色点距离加权（指数 3）渐变，关键点取 `KEY_POINTS[0,2,4,6]`；在 64×64 的 2D canvas 上按官方 `drawStaticGradient` 算法绘制一次，CSS 拉伸铺满（不用 WebGL、不做扭动动画）
- **浅色**：底色 `#bdcd8c`，canvas 渐变 `['#bdcd8c','#8eba89','#83b28f','#c5d3b0']`；花纹层 `background:#000; mix-blend-mode: soft-light; opacity: .375`（强度 50 × 默认系数 75%）
- **深色**：底色 `#000`；canvas 渐变 `['#4f5bd5','#962fbf','#dd6cb9','#fec496']` 被花纹蒙版，`opacity: .375`；花纹 ::after 不显示
- 主题切换（`prefers-color-scheme` 变化）时重绘 canvas
- 只有中栏背景使用；canvas 绘制失败时退回底色

## 5. 测试
- 单元：`playerStorage`（阈值、上限淘汰、损坏数据、localStorage 抛错）、`toViewerItems` 新字段、键盘分流（图片/视频/Shift/输入框）、长按倍速计时、缩略图条（显隐、高亮、点击跳转）、外壳的 history 逻辑（保留现有测试）、渐变像素计算（四角颜色接近官方算法输出）
- PhotoSwipe/Vidstack 在 jsdom 中以桩替代，只验证我们传入的选项与事件接线
- 验收（分支构建 + mock）：桌面、390×844 浅/深色、800px；用户 iPhone 实测手势后再发布

## 6. 错误处理
| 情况 | 行为 |
|---|---|
| 懒加载 chunk 失败 | 提示「查看器加载失败」，关闭查看器 |
| 视频无法播放 | Vidstack 错误态 + 顶栏下载按钮仍可用 |
| 图片无尺寸 | 加载后 `updateSize` |
| WebGL / canvas 不可用 | 背景退回底色 + 花纹 |
