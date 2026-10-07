// The DOM for a video or GIF slide inside PhotoSwipe (spec §3.3). Videos get a Vidstack player
// with its default layout; GIFs stay a bare muted looping <video>. Built imperatively because
// PhotoSwipe owns the slide containers.
import 'vidstack/player';
import 'vidstack/player/layouts/default';
import 'vidstack/player/ui';
import 'vidstack/player/styles/default/theme.css';
import 'vidstack/player/styles/default/layouts/video.css';
import type { MediaPlayerElement, MediaVideoLayoutElement } from 'vidstack/elements';
import { sourceType } from '../media/util';
import { bindLongPressRate } from './longPress';
import { PlayerStorage } from './playerStorage';

export const PLAYBACK_RATES = [0.5, 0.75, 1, 1.25, 1.5, 2];
const GESTURE_QUIET_MS = 400;

const ZH: Record<string, string> = {
  'Audio': '音频',
  'Auto': '自动',
  'Captions': '字幕',
  'Chapters': '章节',
  'Default': '默认',
  'Disabled': '已禁用',
  'Download': '下载',
  'Enter Fullscreen': '全屏',
  'Exit Fullscreen': '退出全屏',
  'Enter PiP': '画中画',
  'Exit PiP': '退出画中画',
  'Fullscreen': '全屏',
  'Loop': '循环',
  'Mute': '静音',
  'Normal': '正常',
  'Off': '关',
  'Pause': '暂停',
  'Play': '播放',
  'Playback': '播放',
  'PiP': '画中画',
  'Quality': '画质',
  'Replay': '重播',
  'Reset': '重置',
  'Seek Backward': '快退',
  'Seek Forward': '快进',
  'Seek': '跳转',
  'Settings': '设置',
  'Speed': '倍速',
  'Unmute': '取消静音',
  'Volume': '音量',
};

/** Whether a pointer target is one of the player's own controls (buttons, sliders, menus). Not
 * .vds-controls itself: that layer covers the whole video, and swiping there must page. */
export function onPlayerControl(target: EventTarget | null): boolean {
  return !!(target as Element | null)?.closest?.('button, [role="slider"], [role="menu"], media-menu-items, .vds-menu-items, .vds-slider');
}

export interface VideoSlideOptions {
  mediaId: number;
  kind: string; // video / animation
  src: string;
  mime: string;
  poster?: string;
  title: string;
  /** 静音模式 is on: the video starts muted. */
  silent?: boolean;
}

export interface VideoSlide {
  el: HTMLDivElement;
  /** The Vidstack player (videos only). */
  player?: MediaPlayerElement;
  /** Current slide: keyboard shortcuts on, starts playing. Leaving: pauses. */
  setActive(active: boolean): void;
  destroy(): void;
}

export function createVideoSlide(o: VideoSlideOptions): VideoSlide {
  const el = document.createElement('div');
  el.className = 'pswp__content ViewerVideo';

  if (o.kind !== 'video') {
    const v = document.createElement('video');
    v.src = o.src;
    v.muted = true;
    v.loop = true;
    v.playsInline = true;
    v.autoplay = false;
    v.setAttribute('playsinline', '');
    el.appendChild(v);
    return {
      el,
      setActive(active) {
        if (active) v.play().catch(() => {});
        else v.pause();
      },
      destroy() {
        v.pause();
        v.removeAttribute('src');
        v.load();
      },
    };
  }

  const player = document.createElement('media-player') as MediaPlayerElement;
  const storage = new PlayerStorage(o.mediaId, () => player.state?.duration ?? 0);
  player.src = { src: o.src, type: sourceType(o.mime) as 'video/mp4' };
  player.title = o.title;
  player.playsInline = true;
  // Storage answers "muted" when the media loads; the attribute also covers the moment before
  // (a property set on a not-yet-connected player is dropped).
  storage.silent = !!o.silent;
  if (o.silent) player.setAttribute('muted', '');
  player.storage = storage;
  player.keyTarget = 'document';
  // PhotoSwipe detaches slides it pages past but may re-attach them from its cache; without
  // keep-alive Vidstack destroys a detached player for good. destroy() below tears it down.
  player.setAttribute('keep-alive', '');
  player.keyDisabled = true;
  if (o.poster) player.poster = o.poster;
  const provider = document.createElement('media-provider');
  const layout = document.createElement('media-video-layout') as MediaVideoLayoutElement;
  layout.translations = ZH;
  layout.playbackRates = PLAYBACK_RATES;
  layout.colorScheme = 'dark';
  layout.seekStep = 10;
  // Vidstack turns a horizontal drag anywhere on the video into scrubbing on touch screens;
  // here that drag pages the gallery (as in Telegram), and seeking stays on the time slider.
  layout.noScrubGesture = true;
  player.append(provider, layout);
  el.appendChild(player);

  const badge = document.createElement('div');
  badge.className = 'ViewerVideo-fast';
  badge.textContent = '2× ▸▸';
  el.appendChild(badge);
  // Releasing a hold is a tap too, and Vidstack's buttons act on pointerup before our own
  // pointerup listener runs. So from the moment a hold starts until shortly after it ends, drop
  // the player's play/pause requests, tap gestures (will-trigger doesn't bubble, hence capture)
  // and clicks.
  let quietUntil = 0;
  const QUIET = ['media-pause-request', 'media-play-request', 'will-trigger', 'click'];
  const muteGesture = (e: Event) => {
    if (performance.now() >= quietUntil) return;
    e.preventDefault();
    e.stopImmediatePropagation();
  };
  for (const t of QUIET) player.addEventListener(t, muteGesture, true);
  const unbind = bindLongPressRate(player, player, {
    ignore: onPlayerControl,
    onStart: () => {
      quietUntil = Infinity;
      storage.holdRate = true;
      el.classList.add('fast');
    },
    onEnd: () => {
      quietUntil = performance.now() + GESTURE_QUIET_MS;
      el.classList.remove('fast');
      // Vidstack saves the restored speed asynchronously; release the hold after it has.
      setTimeout(() => {
        storage.holdRate = false;
      }, 0);
    },
  });

  let active = false;
  return {
    el,
    player,
    setActive(next) {
      if (next === active) return;
      active = next;
      player.keyDisabled = !next;
      if (!next) {
        player.pause().catch(() => {});
        return;
      }
      // Vidstack's document-level shortcuts go to the first player in the DOM, or the focused one.
      player.focus({ preventScroll: true });
      // Opening (or paging) onto a video plays it, as Telegram does; before the source is ready
      // play() would just be rejected, so wait for it.
      if (player.state.canPlay) player.play().catch(() => {});
      else
        player.addEventListener(
          'can-play',
          () => {
            if (active) player.play().catch(() => {});
          },
          { once: true },
        );
    },
    destroy() {
      // Tearing down resets the player to 0s; that must not read as "watched from the start".
      storage.freeze();
      unbind();
      for (const t of QUIET) player.removeEventListener(t, muteGesture, true);
      player.pause().catch(() => {});
      player.destroy();
      el.remove();
    },
  };
}
