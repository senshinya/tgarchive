// The lazily loaded half of the media viewer (spec §3.1): one PhotoSwipe instance over the items,
// Vidstack players on video slides, and the Preact overlay portalled into PhotoSwipe's root.
import PhotoSwipe, { type SlideData } from 'photoswipe';
import 'photoswipe/style.css';
import { createPortal } from 'preact/compat';
import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import type { ViewerItem } from '../../state/store';
import { createVideoSlide, type VideoSlide } from './videoSlide';
import { viewerKeyAction } from './viewerKeys';
import { ViewerOverlay } from './ViewerOverlay';

export interface GalleryProps {
  items: ViewerItem[];
  /** The item to open on. */
  mediaId: number;
  titleOf: (item: ViewerItem) => string;
  /** Where PhotoSwipe mounts its root (the shell's dialog element). */
  container: HTMLElement;
  /** PhotoSwipe has closed (button, Escape, drag down, pinch out); the shell pops its history entry. */
  onClosed: () => void;
}

const FALLBACK_W = 1280;
const FALLBACK_H = 720;
const MAX_ZOOM = 4;
const ZOOM_FACTOR = 1.5;
const HEADER_H = 56;
const THUMBS_H = 64;
const NARROW = 600;

function slideOf(it: ViewerItem): SlideData {
  const w = it.width && it.width > 0 ? it.width : FALLBACK_W;
  const h = it.height && it.height > 0 ? it.height : FALLBACK_H;
  return {
    width: w,
    height: h,
    src: mediaUrl(it.mediaId),
    msrc: it.thumbId ? mediaUrl(it.thumbId) : undefined,
    type: it.kind === 'photo' ? 'image' : 'video',
    item: it,
    sized: !!(it.width && it.height),
  };
}

export function Gallery({ items, mediaId, container, titleOf, onClosed }: GalleryProps) {
  const [pswp, setPswp] = useState<PhotoSwipe | null>(null);
  const [index, setIndex] = useState(() => Math.max(0, items.findIndex((it) => it.mediaId === mediaId)));
  const current = useRef(mediaId);
  const first = useRef(true);
  const onClosedRef = useRef(onClosed);
  onClosedRef.current = onClosed;

  // One PhotoSwipe per item list. The list normally changes once, when the whole chat's media
  // arrives after opening on the tapped item; PhotoSwipe can't re-index a live gallery, so it is
  // rebuilt in place on the same item without the open animation.
  useLayoutEffect(() => {
    const start = Math.max(0, items.findIndex((it) => it.mediaId === current.current));
    const multi = items.length > 1;
    const videos = new Map<number, VideoSlide>(); // slide index → player
    let tearingDown = false;

    const p = new PhotoSwipe({
      dataSource: items.map(slideOf),
      appendToEl: container,
      index: start,
      bgOpacity: window.innerWidth <= NARROW ? 1 : 0.9, // phones: solid black, as Telegram
      loop: false,
      wheelToZoom: true,
      maxZoomLevel: MAX_ZOOM,
      secondaryZoomLevel: 2,
      closeOnVerticalDrag: true,
      pinchToClose: true,
      arrowKeys: false,
      escKey: false,
      returnFocus: false,
      preload: [1, 2],
      showHideAnimationType: first.current ? 'fade' : 'none',
      imageClickAction: 'zoom',
      bgClickAction: 'close',
      tapAction: 'toggle-controls',
      doubleTapAction: 'zoom',
      counter: false,
      zoom: false,
      close: false,
      arrowPrev: false,
      arrowNext: false,
      errorMsg: '无法加载',
      // Desktop keeps the media clear of the header and the thumbnail strip; phones go edge to
      // edge with the controls floating over it, as Telegram does.
      paddingFn: (viewport) =>
        viewport.x <= NARROW ? { top: 0, bottom: 0, left: 0, right: 0 } : { top: HEADER_H, bottom: multi ? THUMBS_H + 8 : 16, left: 64, right: 64 },
    });
    first.current = false;

    p.on('contentLoad', (e) => {
      const { content } = e;
      if (content.type !== 'video') return;
      e.preventDefault();
      const it = content.data.item as ViewerItem;
      const v = createVideoSlide({
        mediaId: it.mediaId,
        kind: it.kind,
        src: mediaUrl(it.mediaId),
        mime: it.mime ?? '',
        poster: it.thumbId ? mediaUrl(it.thumbId) : undefined,
        title: titleOf(it),
      });
      content.element = v.el;
      videos.set(content.index, v);
      // Mirror Vidstack's own controls visibility onto PhotoSwipe's UI (the header and strip), so
      // a tap on a video hides or shows everything together.
      v.player?.addEventListener('controls-change', (ev) => {
        if (p.currIndex === content.index) p.element?.classList.toggle('pswp--ui-visible', ev.detail);
      });
    });
    p.on('contentActivate', ({ content }) => videos.get(content.index)?.setActive(true));
    p.on('contentDeactivate', ({ content }) => videos.get(content.index)?.setActive(false));
    p.on('contentDestroy', ({ content }) => {
      videos.get(content.index)?.destroy();
      videos.delete(content.index);
    });
    // Images without known dimensions: size them once they load.
    p.on('loadComplete', ({ content, slide }) => {
      const img = content.element as HTMLImageElement | undefined;
      if (content.data.sized || !img || img.tagName !== 'IMG' || !img.naturalWidth) return;
      content.data.sized = true;
      content.width = img.naturalWidth;
      content.height = img.naturalHeight;
      content.data.width = img.naturalWidth;
      content.data.height = img.naturalHeight;
      slide.width = img.naturalWidth;
      slide.height = img.naturalHeight;
      slide.calculateSize();
      slide.zoomAndPanToInitial();
      slide.applyCurrentZoomPan();
    });

    // Gestures on a video's own controls (scrubbing, volume, menus) are not swipes, and taps on
    // the player belong to Vidstack (play/pause, show controls, double-tap seek).
    const onPlayerControl = (target: EventTarget | null) =>
      // Not .vds-controls itself: that layer covers the whole video, and swiping there must page.
      !!(target as Element | null)?.closest?.('button, [role="slider"], [role="menu"], media-menu-items, .vds-menu-items, .vds-slider');
    const inPlayer = (target: EventTarget | null) => !!(target as Element | null)?.closest?.('media-player');
    p.on('pointerDown', (e) => {
      if (onPlayerControl(e.originalEvent.target)) e.preventDefault();
    });
    for (const action of ['tapAction', 'doubleTapAction', 'imageClickAction', 'bgClickAction'] as const) {
      p.on(action, (e) => {
        if (inPlayer(e.originalEvent.target)) e.preventDefault();
      });
    }

    p.on('change', () => {
      const it = items[p.currIndex];
      if (it) current.current = it.mediaId;
      setIndex(p.currIndex);
    });
    p.on('destroy', () => {
      for (const v of videos.values()) v.destroy();
      videos.clear();
      if (!tearingDown) onClosedRef.current();
    });

    p.init();
    setPswp(p);
    setIndex(p.currIndex);
    return () => {
      tearingDown = true;
      p.destroy();
    };
  }, [items]);

  useEffect(() => {
    if (!pswp) return;
    const onKey = (e: KeyboardEvent) => {
      const it = items[pswp.currIndex];
      if (!it) return;
      const action = viewerKeyAction(e, it.kind);
      if (!action) return;
      e.preventDefault();
      if (action === 'close') pswp.close();
      else if (action === 'prev') pswp.prev();
      else if (action === 'next') pswp.next();
      else zoom(pswp, action === 'zoomIn' ? 1 : -1);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [pswp, items]);

  if (!pswp?.element) return null;
  const item = items[index];
  return createPortal(
    <ViewerOverlay
      items={items}
      index={index}
      title={item ? titleOf(item) : ''}
      onClose={() => pswp.close()}
      onPick={(i) => pswp.goTo(i)}
      onZoom={(dir) => zoom(pswp, dir)}
    />,
    pswp.element,
  );
}

function zoom(p: PhotoSwipe, dir: 1 | -1) {
  const s = p.currSlide;
  if (!s?.isZoomable()) return;
  const { initial, max } = s.zoomLevels;
  const next = dir > 0 ? Math.min(max, s.currZoomLevel * ZOOM_FACTOR) : Math.max(initial, s.currZoomLevel / ZOOM_FACTOR);
  s.zoomTo(next, undefined, 250);
}
