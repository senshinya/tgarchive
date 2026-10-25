import { ArrowLeft, Bookmark, CircleCheck, Download, Film, Info, Layers, Locate, Pencil, Play, Tags } from 'lucide-preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { errorMessage, mediaUrl } from '../../api/client';
import type { LibraryVideo, VideoLibraryItem } from '../../api/types';
import { formatDuration, formatFullDate, formatSize } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Spinner } from '../../ui/Spinner';
import { TagDialog } from '../favorites/TagDialog';
import { coverDimensions, coverVideo, libraryReturnRoute, resumeVideo, videoTitle } from './library';
import { itemSource, LibraryLink, VideoCover } from './LibraryCard';
import { LibraryError, invalidateLibraryCache } from './LibraryHome';

export function LibraryDetail({ itemId }: { itemId: number }) {
  const store = useStore();
  const [item, setItem] = useState<VideoLibraryItem | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [tagging, setTagging] = useState(false);
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState('');
  const [cover, setCover] = useState(0);
  const alive = useRef(true);
  const generation = useRef(0);
  const load = async () => {
    const mine = ++generation.current;
    try {
      const it = await store.api.videoLibraryItem(itemId);
      if (alive.current && mine === generation.current) {
        setItem(it);
        setError('');
      }
    } catch (e) {
      if (alive.current && mine === generation.current) setError(errorMessage(e));
    }
  };
  useEffect(() => {
    alive.current = true;
    void load();
    const off = store.onEvent((ev) => {
      if (['resync', 'media.updated', 'message.deleted', 'message.updated', 'favorites.updated', 'video-library.updated'].includes(ev.type))
        void load();
    });
    return () => {
      alive.current = false;
      generation.current++;
      off();
    };
  }, [itemId]);
  const act = async (fn: () => Promise<unknown>) => {
    if (busy) return;
    setBusy(true);
    try {
      await fn();
      invalidateLibraryCache();
      await load();
    } catch (e) {
      store.showToast(errorMessage(e));
    } finally {
      if (alive.current) setBusy(false);
    }
  };
  const mark = (v: LibraryVideo) =>
    act(() =>
      store.api.saveVideoProgress(v.media.id, {
        position: v.progress.completed ? 0 : v.media.duration || v.progress.duration,
        duration: v.media.duration || v.progress.duration,
        completed: !v.progress.completed,
      }),
    );
  const coverSize = coverDimensions(item ? coverVideo(item) : undefined);
  return (
    <div class="LibraryScroll custom-scroll">
      <div class="LibraryContent">
        <LibraryLink class="LibraryBack" to={libraryReturnRoute()}>
          <ArrowLeft size={18} />
          返回视频库
        </LibraryLink>
        {error ? (
          <LibraryError text={error} retry={() => void load()} />
        ) : !item ? (
          <div class="LibraryNotice">
            <Spinner size={30} />
          </div>
        ) : (
          <>
            <section class="LibraryDetail-summary">
              <div class="LibraryDetail-cover" style={{ '--cover-ratio': coverSize.width / coverSize.height }}>
                <VideoCover video={coverVideo(item)} />
                <span class="LibraryCover-badge">
                  {item.videos.length > 1 ? (
                    <>
                      <Layers size={13} />
                      {item.videos.length} 个视频
                    </>
                  ) : (
                    formatDuration(item.duration)
                  )}
                </span>
              </div>
              <div class="LibraryDetail-info">
                <span class="LibraryEyebrow">
                  {item.videos.length > 1 ? <Layers size={15} /> : <Film size={15} />}
                  {item.videos.length > 1 ? '帖子合集' : '单视频'}
                </span>
                <h1>{item.title}</h1>
                <div class="LibraryDetail-meta">
                  <span>{itemSource(item, store.chats.value)}</span>
                  <span>·</span>
                  <span>{formatFullDate(item.date)}</span>
                  <span>·</span>
                  <span>{formatDuration(item.duration)}</span>
                </div>
                <p class="LibraryDescription">{item.description}</p>
                <div class="LibraryActions">
                  {item.videos.some((v) => v.media.state === 'done') && (
                    <LibraryLink
                      class="LibraryButton primary"
                      to={{
                        name: 'video-play',
                        itemId: item.id,
                        mediaId: resumeVideo(item).media.id,
                      }}
                    >
                      <Play size={17} fill="currentColor" />
                      {item.videos.some((v) => !v.progress.completed && v.progress.position > 0)
                        ? '继续观看'
                        : item.videos.length > 1
                          ? '播放合集'
                          : '播放视频'}
                    </LibraryLink>
                  )}
                  <button
                    class="LibraryButton secondary"
                    disabled={busy}
                    aria-pressed={!!item.message.favorite}
                    onClick={() =>
                      void act(() => (item.message.favorite ? store.api.unfavorite(item.message.id) : store.api.favorite(item.message.id)))
                    }
                  >
                    <Bookmark size={17} fill={item.message.favorite ? 'currentColor' : 'none'} />
                    {item.message.favorite ? '已收藏' : '收藏'}
                  </button>
                  <button
                    class="LibraryButton secondary"
                    onClick={() => {
                      store.jumpTo.value = {
                        key: item.message.chat_id,
                        messageId: item.message.id,
                      };
                      navigate({ name: 'chat', chatId: item.message.chat_id });
                    }}
                  >
                    <Locate size={17} />
                    原帖
                  </button>
                  <button
                    class="LibraryIconButton"
                    aria-label="编辑标题与封面"
                    onClick={() => {
                      setTitle(item.title);
                      setCover(item.cover_media_id);
                      setEditing(!editing);
                    }}
                  >
                    <Pencil size={18} />
                  </button>
                </div>
                <p class="LibraryDetail-status">
                  已看 {item.videos.filter((v) => v.progress.completed).length} / {item.videos.length} 个视频
                </p>
                {item.message.favorite && (
                  <div class="LibraryTags">
                    {item.message.favorite.tags.map((t) => (
                      <span key={t.id}>{t.name}</span>
                    ))}
                    <button class="LibraryButton secondary" onClick={() => setTagging(true)}>
                      <Tags size={15} />
                      编辑标签
                    </button>
                  </div>
                )}
              </div>
            </section>
            {editing && (
              <form
                class="LibraryEdit"
                onSubmit={(e) => {
                  e.preventDefault();
                  void act(async () => {
                    await store.api.updateVideoLibraryItem(item.id, {
                      title: title.trim(),
                      cover_media_id: cover,
                    });
                    setEditing(false);
                  });
                }}
              >
                <label>
                  显示标题
                  <input
                    aria-label="显示标题"
                    value={title}
                    maxLength={200}
                    placeholder="留空恢复原帖标题"
                    onInput={(e) => setTitle(e.currentTarget.value)}
                  />
                </label>
                <label>
                  合集封面
                  <select aria-label="合集封面" value={cover} onChange={(e) => setCover(Number(e.currentTarget.value))}>
                    <option value={0}>使用默认视频封面</option>
                    {item.videos.map((v, i) => (
                      <option key={v.media.id} value={v.media.id}>
                        {videoTitle(v, i)}
                      </option>
                    ))}
                  </select>
                </label>
                <div class="LibraryActions">
                  <button class="LibraryButton primary" type="submit" disabled={busy}>
                    保存
                  </button>
                  <button class="LibraryButton secondary" type="button" onClick={() => setEditing(false)}>
                    取消
                  </button>
                </div>
              </form>
            )}
            <section>
              <div class="LibrarySection-head">
                <h2>
                  {item.videos.length > 1 ? '合集内容' : '视频文件'}
                  <small>{item.videos.length} 个视频</small>
                </h2>
                <span>按原帖顺序</span>
              </div>
              <div class="LibraryEpisodes">
                {item.videos.map((v, i) => (
                  <article
                    class={`LibraryEpisode${v.progress.position > 0 && !v.progress.completed ? ' current' : ''}`}
                    key={`${v.message.id}:${v.media.id}`}
                  >
                    <span class="LibraryEpisode-index">{String(i + 1).padStart(2, '0')}</span>
                    <LibraryLink
                      class="LibraryEpisode-cover"
                      to={{
                        name: 'video-play',
                        itemId: item.id,
                        mediaId: v.media.id,
                      }}
                      label={`播放：${videoTitle(v, i)}`}
                    >
                      <VideoCover video={v} />
                      <span class="LibraryProgress">
                        <i
                          style={{
                            width: `${v.progress.completed ? 100 : Math.min(100, (v.progress.position / (v.progress.duration || v.media.duration || 1)) * 100)}%`,
                          }}
                        />
                      </span>
                    </LibraryLink>
                    <div class="LibraryEpisode-text">
                      <LibraryLink
                        to={{
                          name: 'video-play',
                          itemId: item.id,
                          mediaId: v.media.id,
                        }}
                      >
                        {videoTitle(v, i)}
                      </LibraryLink>
                      <p>
                        {v.media.width > 0 ? `${v.media.width} × ${v.media.height} · ` : ''}
                        {formatSize(v.media.size)} ·{' '}
                        {v.media.state !== 'done'
                          ? v.media.state === 'pending'
                            ? '等待下载'
                            : '下载失败'
                          : v.progress.completed
                            ? '已观看'
                            : v.progress.position > 0
                              ? `已看 ${formatDuration(v.progress.position)}`
                              : '尚未观看'}
                      </p>
                    </div>
                    <span class="LibraryEpisode-duration">{formatDuration(v.media.duration || v.progress.duration)}</span>
                    <button
                      class={`LibraryIconButton${v.progress.completed ? ' accent' : ''}`}
                      aria-label={v.progress.completed ? '标记为未看' : '标记为已看'}
                      disabled={busy}
                      onClick={() => void mark(v)}
                    >
                      <CircleCheck size={21} />
                    </button>
                    <a class="LibraryIconButton" href={mediaUrl(v.media.id, true)} download aria-label={`下载：${videoTitle(v, i)}`}>
                      <Download size={18} />
                    </a>
                  </article>
                ))}
              </div>
              <p class="LibraryDetail-note">
                <Info size={15} />
                {item.videos.length > 1
                  ? '此合集由同一帖子中的视频自动组成，观看进度按视频分别保存。'
                  : '观看进度独立保存，不影响频道消息的已读状态。'}
              </p>
            </section>
            {tagging && (
              <TagDialog
                initial={item.message.favorite?.tags.map((t) => t.name) || []}
                onClose={() => setTagging(false)}
                onSave={async (tags) => {
                  await store.api.setTags(item.message.id, tags);
                  invalidateLibraryCache();
                  await load();
                }}
              />
            )}
          </>
        )}
      </div>
    </div>
  );
}
