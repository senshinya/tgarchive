import { Bookmark, Film, Layers, Play } from 'lucide-preact';
import type { ComponentChildren } from 'preact';
import { mediaUrl } from '../../api/client';
import type { LibraryVideo, VideoLibraryItem } from '../../api/types';
import { chatName, formatDuration } from '../../lib/format';
import { navigate, routePath, type Route } from '../../lib/router';
import { useStore } from '../../state/store';
import { coverDimensions, coverThumb, coverVideo, videoTitle } from './library';

export function LibraryLink({
  to,
  children,
  class: cls,
  label,
  title,
}: {
  to: Route;
  children: ComponentChildren;
  class?: string;
  label?: string;
  title?: string;
}) {
  return (
    <a
      href={routePath(to)}
      class={cls}
      aria-label={label}
      title={title}
      onClick={(e) => {
        if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        e.preventDefault();
        navigate(to);
      }}
    >
      {children}
    </a>
  );
}
export function VideoCover({ video, title = '', children }: { video?: LibraryVideo; title?: string; children?: ComponentChildren }) {
  const id = coverThumb(video);
  const { width, height } = coverDimensions(video);
  return (
    <>
      {id ? (
        <img src={mediaUrl(id)} width={width} height={height} alt={title} loading="lazy" />
      ) : (
        <span class="LibraryCover-placeholder">
          <Film size={32} />
          <small>{video?.media.state === 'pending' ? '等待下载' : '视频'}</small>
        </span>
      )}
      {children}
    </>
  );
}
export function itemSource(item: VideoLibraryItem, chats: ReturnType<typeof useStore>['chats']['value']): string {
  const chat = chats.find((c) => c.id === item.message.chat_id);
  return chat ? chatName(chat) : item.message.origin_chat_title || '存档会话';
}
export function LibraryCard({
  item,
  video,
  onFavorite,
}: {
  item: VideoLibraryItem;
  video?: LibraryVideo;
  onFavorite: (item: VideoLibraryItem) => void;
}) {
  const store = useStore();
  const v = video || coverVideo(item);
  const { width, height } = coverDimensions(v);
  const collection = !video && item.videos.length > 1;
  const title = video ? videoTitle(video, item.videos.indexOf(video)) : item.title;
  const to: Route = video ? { name: 'video-play', itemId: item.id, mediaId: video.media.id } : { name: 'video-detail', itemId: item.id };
  const p = v?.progress;
  const duration = p?.duration || v?.media.duration || 0;
  const percentage = p?.completed ? 100 : duration > 0 ? Math.min(100, ((p?.position || 0) / duration) * 100) : 0;
  return (
    <article class={`LibraryCard${collection ? ' collection' : ''}`} style={{ '--cover-ratio': width / height }}>
      <LibraryLink class="LibraryCover" to={to} label={`${video ? '继续播放' : '查看'}：${title}`}>
        <VideoCover video={v} />
        <span class="LibraryCover-play">
          <Play size={23} fill="currentColor" />
        </span>
        <span class="LibraryCover-badge">
          {collection ? (
            <>
              <Layers size={12} />
              {item.videos.length} 个视频
            </>
          ) : (
            formatDuration(v?.media.duration || duration)
          )}
        </span>
        {!collection && percentage > 0 && (
          <span class="LibraryProgress">
            <i style={{ width: `${percentage}%` }} />
          </span>
        )}
      </LibraryLink>
      <button
        type="button"
        class={`LibraryCard-favorite${item.message.favorite ? ' active' : ''}`}
        aria-label={item.message.favorite ? `取消收藏：${item.title}` : `收藏：${item.title}`}
        aria-pressed={!!item.message.favorite}
        onClick={() => onFavorite(item)}
      >
        <Bookmark size={16} fill={item.message.favorite ? 'currentColor' : 'none'} />
      </button>
      <LibraryLink class="LibraryCard-title" to={to} title={title}>
        {title}
      </LibraryLink>
      <div class="LibraryCard-meta">
        <span>{itemSource(item, store.chats.value)}</span>
        <span>·</span>
        {collection ? (
          <span>
            {item.videos.filter((v) => v.progress.completed).length} / {item.videos.length} 已看
          </span>
        ) : video ? (
          <span class="accent">剩余 {formatDuration(Math.max(0, duration - (p?.position || 0)))}</span>
        ) : (
          <span>
            {p?.completed
              ? '已看'
              : new Date(item.date * 1000).toLocaleDateString('zh-CN', {
                  month: 'numeric',
                  day: 'numeric',
                })}
          </span>
        )}
      </div>
    </article>
  );
}
