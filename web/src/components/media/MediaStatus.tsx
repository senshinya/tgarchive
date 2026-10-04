import { CircleAlert, FileWarning, RotateCw } from 'lucide-preact';
import { mediaUrl } from '../../api/client';
import type { Media, Message } from '../../api/types';
import { useStore } from '../../state/store';
import { Spinner } from '../../ui/Spinner';
import './media.scss';

interface Props {
  msg: Message;
  media: Media;
  thumb?: Media;
}

/** Placeholder for media that is not downloaded: pending / failed (with retry) / too_large. */
export function MediaStatus({ msg, media, thumb }: Props) {
  const store = useStore();
  return (
    <div class={`MediaStatus state-${media.state}`}>
      {thumb && <img class="MediaStatus-thumb" src={mediaUrl(thumb.id)} alt="" />}
      <div class="MediaStatus-body">
        {media.state === 'pending' && (
          <>
            <Spinner size={32} />
            <span class="MediaStatus-text">正在下载…</span>
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
export function inlineStatus(media: Media): string | null {
  switch (media.state) {
    case 'pending':
      return '正在下载…';
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
