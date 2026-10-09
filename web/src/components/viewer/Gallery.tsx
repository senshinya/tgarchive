// The lazily loaded half of the media viewer (spec §3.1): one PhotoSwipe instance over the items,
// Vidstack players on video slides, and the Preact overlay portalled into PhotoSwipe's root.
import PhotoSwipe, { type SlideData } from 'photoswipe';
import 'photoswipe/style.css';
import { createPortal } from 'preact/compat';
import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import { silent } from '../../lib/silent';
import { playUrl } from '../media/util';
import type { ViewerItem } from '../../state/store';
import { createVideoSlide, onPlayerControl, type VideoSlide } from './videoSlide';
import { NARROW, slidePadding, turn, turned, videoSlideSize } from './viewerLayout';
import { viewerKeyAction } from './viewerKeys';
import { ViewerOverlay } from './ViewerOverlay';

type Slide = NonNullable<PhotoSwipe['currSlide']>;
type Content = Slide['content'];

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

const viewport = () => ({ x: window.innerWidth, y: window.innerHeight });

/** A video slide's size for the current viewport, with the picture turned `quarters` times. */
function videoSize(it: ViewerItem, quarters: number) {
  const { width, height } = turned(it.width ?? 0, it.height ?? 0, quarters);
  return videoSlideSize(viewport(), width, height);
}

function slideOf(it: ViewerItem, quarters: number): SlideData {
  if (it.kind !== 'photo') {
    const { width, height } = videoSize(it, quarters);
    return {
      width,
      height,
      src: mediaUrl(it.mediaId),
      msrc: it.thumbId ? mediaUrl(it.thumbId) : undefined,
      type: 'video',
      item: it,
      quarters,
      sized: true,
    };
  }
  const { width, height } = turned(
    it.width && it.width > 0 ? it.width : FALLBACK_W,
    it.height && it.height > 0 ? it.height : FALLBACK_H,
    quarters,
  );
  return {
    width,
    height,
    src: mediaUrl(it.mediaId),
    msrc: it.thumbId ? mediaUrl(it.thumbId) : undefined,
    type: 'image',
    item: it,
    quarters,
    sized: !!(it.width && it.height),
  };
}

export function Gallery({ items, mediaId, container, titleOf, onClosed }: GalleryProps) {
  const [pswp, setPswp] = useState<PhotoSwipe | null>(null);
  const [index, setIndex] = useState(() => Math.max(0, items.findIndex((it) => it.mediaId === mediaId)));
  const current = useRef(mediaId);
  const first = useRef(true);
  // The caption-and-strip block's height: desktop videos are laid out clear of it.
  const foot = useRef(0);
  const onClosedRef = useRef(onClosed);
  onClosedRef.current = onClosed;
  // Quarter turns per media id, kept while the viewer is open (also across a rebuild).
  const turns = useRef(new Map<number, number>());

  // One PhotoSwipe per item list. The list normally changes once, when the whole chat's media
  // arrives after opening on the tapped item; PhotoSwipe can't re-index a live gallery, so it is
  // rebuilt in place on the same item without the open animation.
  useLayoutEffect(() => {
    const start = Math.max(0, items.findIndex((it) => it.mediaId === current.current));
    const multi = items.length > 1;
    const videos = new Map<number, VideoSlide>(); // slide index → player
    let tearingDown = false;

    const slides = items.map((it) => slideOf(it, turns.current.get(it.mediaId) ?? 0));
    const p = new PhotoSwipe({
      dataSource: slides,
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
      paddingFn: (size, data) => slidePadding(size, data.type, multi, foot.current),
    });
    first.current = false;

    p.on('contentLoad', (e) => {
      const { content } = e;
      if (content.type !== 'video') return;
      e.preventDefault();
      const it = content.data.item as ViewerItem;
      const src = playUrl(it.mediaId, it.compatCodec);
      const v = createVideoSlide({
        mediaId: it.mediaId,
        kind: it.kind,
        src,
        // The copy is always MP4, whatever the original's container.
        mime: src === mediaUrl(it.mediaId) ? (it.mime ?? '') : 'video/mp4',
        poster: it.thumbId ? mediaUrl(it.thumbId) : undefined,
        title: titleOf(it),
        silent: silent.value,
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
    // Images without known dimensions (article media): size them from the loaded image. An image
    // preloaded before it had a slide only gets one later, so also check when it is appended.
    const fit = (content: Content, slide: Slide | undefined) => {
      const img = content.element as HTMLImageElement | undefined;
      if (!slide || content.data.sized || !img || img.tagName !== 'IMG' || !img.naturalWidth) return;
      content.data.sized = true;
      const { width, height } = turned(img.naturalWidth, img.naturalHeight, content.data.quarters ?? 0);
      content.data.width = width;
      content.data.height = height;
      resizeSlide(slide);
    };
    p.on('loadComplete', ({ content, slide }) => fit(content, slide));
    p.on('contentAppend', ({ content }) => {
      showTurn(content);
      fit(content, content.slide);
    });
    // A turned photo's <img> is drawn upright at the swapped size and turned to fill the box
    // PhotoSwipe gives it (viewer.scss), which needs that box's size.
    p.on('contentResize', ({ content, width, height }) => {
      content.element?.style.setProperty('--box-w', `${width}px`);
      content.element?.style.setProperty('--box-h', `${height}px`);
    });
    // Video slides follow the viewport across the phone breakpoint (rotating, resizing).
    p.on('beforeResize', () => {
      const sizeOf = (d: SlideData) => videoSize(d.item as ViewerItem, d.quarters ?? 0);
      for (const d of slides) {
        if (d.type === 'video') Object.assign(d, sizeOf(d));
      }
      for (const h of p.mainScroll.itemHolders) {
        if (h.slide?.data.type !== 'video') continue;
        const { width, height } = sizeOf(h.slide.data);
        h.slide.width = h.slide.content.width = width;
        h.slide.height = h.slide.content.height = height;
      }
    });

    // Gestures on a video's own controls (scrubbing, volume, menus) are not swipes, and taps on
    // the player belong to Vidstack (play/pause, show controls, double-tap seek).
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
      // A video may have idled the UI away; a photo starts with it shown.
      if (it && it.kind !== 'video') p.element?.classList.add('pswp--ui-visible');
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
      // Synchronous teardown: destroy() alone goes through close(), which PhotoSwipe ignores while
      // the opening fade runs, leaving this instance (and any playing video) behind.
      p.isDestroying = true;
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
      if (action === 'close') closeGallery(pswp);
      else if (action === 'prev') pswp.prev();
      else if (action === 'next') pswp.next();
      else if (action === 'rotateCw' || action === 'rotateCcw') rotate(pswp, turns.current, action === 'rotateCw' ? 1 : -1);
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
      onClose={() => closeGallery(pswp)}
      onPick={(i) => pswp.goTo(i)}
      onZoom={(dir) => zoom(pswp, dir)}
      onRotate={(dir) => rotate(pswp, turns.current, dir)}
      onFoot={(h) => {
        if (h === foot.current) return;
        foot.current = h;
        if (items[pswp.currIndex]?.kind !== 'photo' && window.innerWidth > NARROW) pswp.updateSize(true);
      }}
    />,
    pswp.element,
  );
}

/** PhotoSwipe ignores close() during the opening fade (and then never closes): defer it. */
function closeGallery(p: PhotoSwipe) {
  if (p.opener.isOpening) p.on('openingAnimationEnd', () => p.close());
  else p.close();
}

function zoom(p: PhotoSwipe, dir: 1 | -1) {
  const s = p.currSlide;
  if (!s?.isZoomable()) return;
  const { initial, max } = s.zoomLevels;
  const next = dir > 0 ? Math.min(max, s.currZoomLevel * ZOOM_FACTOR) : Math.max(initial, s.currZoomLevel / ZOOM_FACTOR);
  s.zoomTo(next, undefined, 250);
}

/** Marks a slide's media with its quarter turns for viewer.scss. The thumbnail placeholder
 * (scaled by PhotoSwipe's own transform) can't be turned, so it is hidden instead. */
function showTurn(content: Content) {
  const quarters = content.data.quarters ?? 0;
  const el = content.element;
  if (el && el.tagName !== 'IMG' && !el.classList.contains('ViewerVideo')) return; // error message
  if (quarters) el?.setAttribute('data-turn', String(quarters));
  else el?.removeAttribute('data-turn');
  const ph = content.placeholder?.element;
  if (ph) ph.style.visibility = quarters ? 'hidden' : '';
}

/** Re-lays a slide out at its data's size, back at the initial zoom. */
function resizeSlide(slide: Slide) {
  const { data, content } = slide;
  slide.width = content.width = data.width ?? 0;
  slide.height = content.height = data.height ?? 0;
  slide.calculateSize();
  slide.currentResolution = 0;
  slide.zoomAndPanToInitial();
  slide.applyCurrentZoomPan();
  slide.updateContentSize(true);
}

/** Turns the current item a quarter (clockwise for 1); the turn lasts while the viewer is open. */
function rotate(p: PhotoSwipe, turns: Map<number, number>, dir: 1 | -1) {
  const s = p.currSlide;
  const it = s?.data.item as ViewerItem | undefined;
  if (!s || !it) return;
  const quarters = turn(s.data.quarters ?? 0, dir);
  turns.set(it.mediaId, quarters);
  s.data.quarters = quarters;
  if (s.data.type === 'video') Object.assign(s.data, videoSize(it, quarters));
  else [s.data.width, s.data.height] = [s.data.height, s.data.width];
  showTurn(s.content);
  resizeSlide(s);
}
