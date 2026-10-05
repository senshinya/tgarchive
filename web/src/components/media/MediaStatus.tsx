import { CircleAlert, Clock, FileWarning, RotateCw } from 'lucide-preact';
import { mediaUrl } from '../../api/client';
import type { Media, Message } from '../../api/types';
import { formatProgress } from '../../lib/format';
import { useStore, type MediaProgress } from '../../state/store';
import { ProgressRing } from '../../ui/ProgressRing';
import './media.scss';

interface Props {
  msg: Message;
  media: Media;
  thumb?: Media;
}

/** Fraction for a progress ring: undefined (spinning) until the first byte or without a total. */
export function progressFraction(p: MediaProgress): number | undefined {
  return p.done > 0 && p.total > 0 ? p.done / p.total : undefined;
}

/** Placeholder for media that is not downloaded: downloading (with progress) or queued /
 * failed (with retry) / too_large. */
export function MediaStatus({ msg, media, thumb }: Props) {
  const store = useStore();
  const progress = store.progress.value.get(media.id);
  return (
    <div class={`MediaStatus state-${media.state}`}>
      {thumb && <img class="MediaStatus-thumb" src={mediaUrl(thumb.id)} alt="" />}
      <div class="MediaStatus-body">
        {media.state === 'pending' && progress && (
          <>
            <ProgressRing value={progressFraction(progress)} size={48} />
            <span class="MediaStatus-text">{formatProgress(progress.done, progress.total || media.size)}</span>
          </>
        )}
        {media.state === 'pending' && !progress && (
          <>
            <Clock size={32} />
            <span class="MediaStatus-text">排队中</span>
          </>
        )}
        {media.state === 'failed' && (
          <>
            <CircleAlert size={32} />
            <span class="MediaStatus-text">下载失败</span>
            {media.error && <span class="MediaStatus-error">{media.error}</span>}
            <button
              type="button"
              class="MediaStatus-retry"
              onClick={(e) => {
                e.stopPropagation();
                void store.retryMedia(msg, media.id);
              }}
            >
              <RotateCw size={16} />
              重试
            </button>
          </>
        )}
        {media.state === 'too_large' && (
          <>
            <FileWarning size={32} />
            <span class="MediaStatus-text">文件超过存档上限</span>
            <span class="MediaStatus-error">仅保存了消息记录</span>
          </>
        )}
      </div>
    </div>
  );
}

/** One-line status used inside file rows; null when the media is downloaded. */
export function inlineStatus(media: Media, progress?: MediaProgress): string | null {
  switch (media.state) {
    case 'pending':
      return progress ? formatProgress(progress.done, progress.total || media.size) : '排队中';
    case 'failed':
      return media.error ? `下载失败：${media.error}` : '下载失败';
    case 'too_large':
      return '文件超过存档上限';
    default:
      return null;
  }
}

export function RetryButton({ msg, media }: { msg: Message; media: Media }) {
  const store = useStore();
  return (
    <button
      type="button"
      class="RetryLink"
      onClick={(e) => {
        e.stopPropagation();
        void store.retryMedia(msg, media.id);
      }}
    >
      重试
    </button>
  );
}
