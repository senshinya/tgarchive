import { ArrowLeft, Download, Locate, Play, SkipForward } from 'lucide-preact';
import { useLayoutEffect, useRef, useState } from 'preact/hooks';
import { errorMessage, mediaUrl } from '../../api/client';
import type { LibraryVideo, VideoLibraryItem } from '../../api/types';
import { formatDuration } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Spinner } from '../../ui/Spinner';
import { playUrl } from '../media/util';
import type { VideoSlide } from '../viewer/videoSlide';
import { coverThumb, videoTitle } from './library';
import { LibraryLink, VideoCover } from './LibraryCard';
import { LibraryError, invalidateLibraryCache } from './LibraryHome';
import { RemotePlayerStorage } from './progress';

const AUTO_KEY = 'tgarchive.library.autoplay';
function readAutoplay() {
  try {
    return localStorage.getItem(AUTO_KEY) !== 'false';
  } catch {
    return true;
  }
}
function PlayerSurface({ video, title, ended }: { video: LibraryVideo; title: string; ended: () => void }) {
  const store = useStore();
  const host = useRef<HTMLDivElement>(null);
  const resume = useRef(video.progress);
  const next = useRef(ended);
  next.current = ended;
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useLayoutEffect(() => {
    let cancelled = false;
    let slide: VideoSlide | undefined;
    let storage: RemotePlayerStorage | undefined;
    let unbind = () => {};
    setError('');
    void import('../viewer/videoSlide')
      .then(({ createVideoSlide }) => {
        if (cancelled || !host.current) return;
        storage = new RemotePlayerStorage(
          video.media.id,
          () => slide?.player?.state.duration || video.media.duration,
          resume.current,
          (p, keepalive) => store.api.saveVideoProgress(video.media.id, p, keepalive),
          () => store.showToast('观看进度保存失败，播放可继续；离开页面时会再次尝试'),
        );
        slide = createVideoSlide({
          mediaId: video.media.id,
          kind: 'video',
          src: playUrl(video.media.id, video.media.compat_codec),
          mime: video.media.mime,
          title,
          poster: coverThumb(video) ? mediaUrl(coverThumb(video)!) : undefined,
          storage,
          onEnded: () => next.current(),
        });
        host.current.appendChild(slide.el);
        const player = slide.player!;
        const started = () => storage?.start();
        const time = () => {
          void storage?.setTime(player.currentTime, player.state.ended);
        };
        const flush = () => {
          time();
          void storage?.flush();
        };
        const visibility = () => {
          if (document.visibilityState !== 'visible') flush();
        };
        const failure = () => setError('视频暂时无法播放，可尝试重载或下载原文件');
        player.addEventListener('playing', started);
        player.addEventListener('time-update', time);
        player.addEventListener('pause', flush);
        player.addEventListener('error', failure);
        window.addEventListener('pagehide', flush);
        document.addEventListener('visibilitychange', visibility);
        unbind = () => {
          player.removeEventListener('playing', started);
          player.removeEventListener('time-update', time);
          player.removeEventListener('pause', flush);
          player.removeEventListener('error', failure);
          window.removeEventListener('pagehide', flush);
          document.removeEventListener('visibilitychange', visibility);
        };
        slide.setActive(true);
      })
      .catch((e) => {
        if (!cancelled) setError(errorMessage(e));
      });
    return () => {
      cancelled = true;
      unbind();
      if (storage) resume.current = storage.snapshot();
      storage?.freeze();
      slide?.destroy();
      invalidateLibraryCache();
    };
  }, [video.media.id, attempt]);
  return (
    <>
      <div class="LibraryPlayer-surface" ref={host} />
      {error && <LibraryError text={error} retry={() => setAttempt((x) => x + 1)} />}
    </>
  );
}
export function LibraryPlayer({ itemId, mediaId }: { itemId: number; mediaId: number }) {
  const store = useStore();
  const [item, setItem] = useState<VideoLibraryItem | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const [autoplay, setAutoplay] = useState(readAutoplay);
  useLayoutEffect(() => {
    let active = true;
    let generation = 0;
    const load = () => {
      const mine = ++generation;
      store.api.videoLibraryItem(itemId).then(
        (it) => {
          if (active && mine === generation) {
            setItem(it);
            setError('');
          }
        },
        (e) => {
          if (active && mine === generation) setError(errorMessage(e));
        },
      );
    };
    load();
    const off = store.onEvent((ev) => {
      if (['resync', 'media.updated', 'message.deleted', 'message.updated'].includes(ev.type)) load();
    });
    return () => {
      active = false;
      off();
    };
  }, [itemId, attempt]);
  const index = item?.videos.findIndex((v) => v.media.id === mediaId) ?? -1;
  const video = item?.videos[index];
  const next = item?.videos.slice(index + 1).find((v) => v.media.state === 'done');
  const nextVideo = () => {
    if (next) navigate({ name: 'video-play', itemId, mediaId: next.media.id }, { replace: true });
  };
  return (
    <div class="LibraryScroll custom-scroll">
      <div class="LibraryContent">
        <LibraryLink class="LibraryBack" to={{ name: 'video-detail', itemId }}>
          <ArrowLeft size={18} />
          返回内容详情
        </LibraryLink>
        {error ? (
          <LibraryError text={error} retry={() => setAttempt((x) => x + 1)} />
        ) : !item ? (
          <div class="LibraryNotice">
            <Spinner size={28} />
          </div>
        ) : !video ? (
          <div class="LibraryNotice">
            视频已从此合集中移除。
            <LibraryLink class="LibraryButton secondary" to={{ name: 'video-detail', itemId }}>
              返回详情
            </LibraryLink>
          </div>
        ) : (
          <div class="LibraryWatch">
            <div class="LibraryWatch-main">
              {video.media.state === 'done' ? (
                <PlayerSurface
                  video={video}
                  title={videoTitle(video, index)}
                  ended={() => {
                    if (autoplay) nextVideo();
                  }}
                />
              ) : (
                <div class="LibraryNotice">
                  <Play size={30} />
                  <h2>{video.media.state === 'pending' ? '视频正在下载' : '视频尚不可播放'}</h2>
                  <p>{video.media.error || '下载完成后即可观看。'}</p>
                  {video.media.state === 'failed' && (
                    <button
                      class="LibraryButton secondary"
                      onClick={() =>
                        void store.api.retryMedia(mediaId).then(
                          () => setAttempt((x) => x + 1),
                          (e) => store.showToast(errorMessage(e)),
                        )
                      }
                    >
                      重试下载
                    </button>
                  )}
                </div>
              )}
              <div class="LibraryWatch-info">
                <h1>{videoTitle(video, index)}</h1>
                <LibraryLink class="LibraryWatch-collection" to={{ name: 'video-detail', itemId }}>
                  {item.title}
                </LibraryLink>
                <p class="LibraryDescription">{video.message.text || item.description}</p>
                <div class="LibraryActions">
                  <a class="LibraryButton secondary" href={mediaUrl(mediaId, true)} download>
                    <Download size={17} />
                    下载原文件
                  </a>
                  <button
                    class="LibraryButton secondary"
                    onClick={() => {
                      store.jumpTo.value = {
                        key: video.message.chat_id,
                        messageId: video.message.id,
                      };
                      navigate({ name: 'chat', chatId: video.message.chat_id });
                    }}
                  >
                    <Locate size={17} />
                    查看原帖
                  </button>
                  {next && (
                    <button class="LibraryButton secondary" onClick={nextVideo}>
                      <SkipForward size={17} />
                      下一条
                    </button>
                  )}
                </div>
              </div>
            </div>
            <aside class="LibraryQueue">
              <h2>{item.videos.length > 1 ? '正在播放的合集' : '当前视频'}</h2>
              <p>
                {item.title} · {index + 1} / {item.videos.length}
              </p>
              {item.videos.map((v, i) => (
                <LibraryLink
                  key={`${v.message.id}:${v.media.id}`}
                  class={`LibraryQueue-item${i === index ? ' active' : ''}`}
                  to={{ name: 'video-play', itemId, mediaId: v.media.id }}
                >
                  <span class="LibraryQueue-cover">
                    <VideoCover video={v} />
                  </span>
                  <span>
                    <strong>{videoTitle(v, i)}</strong>
                    <small>
                      {formatDuration(v.media.duration)}
                      {i === index ? ' · 正在观看' : v.progress.completed ? ' · 已看' : v.media.state !== 'done' ? ' · 未就绪' : ''}
                    </small>
                  </span>
                </LibraryLink>
              ))}
              <label class="LibraryAutoplay">
                <span>合集内自动播放下一条</span>
                <input
                  type="checkbox"
                  checked={autoplay}
                  onChange={(e) => {
                    setAutoplay(e.currentTarget.checked);
                    try {
                      localStorage.setItem(AUTO_KEY, String(e.currentTarget.checked));
                    } catch {}
                  }}
                />
              </label>
            </aside>
          </div>
        )}
      </div>
    </div>
  );
}
