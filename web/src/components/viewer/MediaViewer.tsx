import { ChevronLeft, ChevronRight, Download, X, ZoomIn, ZoomOut } from 'lucide-preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { errorMessage, mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { formatFullDate, senderName } from '../../lib/format';
import { useStore, type ViewerItem, type ViewerTarget } from '../../state/store';
import { IconButton } from '../../ui/Button';
import { RichText } from '../message/RichText';
import { VISUAL_KINDS, mainMedia } from '../media/util';
import './viewer.scss';

export const VIEWER_PAGE = 100;
export const VIEWER_MAX_PAGES = 50;
const MIN_ZOOM = 1;
const MAX_ZOOM = 4;
const ZOOM_STEP = 0.5;
const SWIPE_H_THRESHOLD = 50; // px; horizontal drag past this (and past the vertical delta) navigates
const SWIPE_V_THRESHOLD = 80; // px; downward drag past this closes the viewer

/** A fresh, per-open id for the history entry the viewer pushes (see ViewerInner below): unique
 * enough that it never collides with a stale `viewer` marker left on an older entry (e.g. from
 * before a reload, or from a previous open that didn't get cleaned up). */
function newViewerToken(): string {
  return typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;
}

export function toViewerItems(msgs: Message[]): ViewerItem[] {
  const out: { id: number; item: ViewerItem }[] = [];
  for (const msg of msgs) {
    const media = mainMedia(msg);
    if (media && media.state === 'done' && VISUAL_KINDS.includes(msg.kind)) {
      out.push({ id: msg.id, item: { mediaId: media.id, kind: msg.kind, date: msg.date, text: msg.text, entities: msg.entities } });
    }
  }
  return out.sort((a, b) => a.id - b.id).map((x) => x.item);
}

function ViewerInner({ target }: { target: ViewerTarget }) {
  const store = useStore();
  // One unique id per open, not a bare boolean: history.state survives reload/forward, so a
  // stale `viewer` marker can already sit on the entry this instance is about to push onto (or,
  // after that push, on the entry it pops back to). Comparing against this specific token — not
  // mere truthiness — means we only ever treat *our own* pushed entry as "the viewer is open",
  // so a stale leftover marker elsewhere in history can't make the first Back/X press a no-op.
  const tokenRef = useRef<string | undefined>(undefined);
  if (tokenRef.current === undefined) tokenRef.current = newViewerToken();
  const token = tokenRef.current;

  // Closing via UI: if the top history entry is the one we pushed on open (below), go back
  // through it instead of closing directly — the popstate handler below then closes the viewer,
  // keeping the hardware/system back button and these UI controls doing the same thing.
  const close = () => {
    if ((history.state as { viewer?: string } | null)?.viewer === token) history.back();
    else store.viewer.value = null;
  };

  // Opening the viewer pushes one history entry for the SAME url (preserving existing state
  // fields), so the system back button closes only the viewer instead of also leaving whatever
  // was underneath it (the article reader or the chat). Popping past that entry — i.e. a
  // popstate where the current entry's `viewer` marker is no longer this open's token — closes
  // the viewer without navigating further; the router re-parses the same path on this pop, which
  // is a no-op since the path never changed.
  useEffect(() => {
    history.pushState({ ...(history.state ?? {}), viewer: token }, '', location.href);
    const onPopState = () => {
      if ((history.state as { viewer?: string } | null)?.viewer !== token) store.viewer.value = null;
    };
    window.addEventListener('popstate', onPopState);
    return () => {
      window.removeEventListener('popstate', onPopState);
      // Closed some other way (e.g. store.viewer cleared elsewhere) while our entry is still on
      // top: drop it, or the next system back would land on a dead "viewer" entry.
      if ((history.state as { viewer?: string } | null)?.viewer === token) history.back();
    };
  }, []);
  const listed = 'list' in target ? target : null;
  const inChat = 'list' in target ? null : target;
  const chatId = inChat?.chatId ?? 0;
  const seed = listed ? listed.list : toViewerItems(store.conv(chatId).items.filter((m) => m.id === inChat?.messageId));
  const [items, setItems] = useState<ViewerItem[]>(seed);
  const [mediaId, setMediaId] = useState(target.mediaId);
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null);
  const swipeStart = useRef<{ x: number; y: number } | null>(null);

  // Load the whole chat's media (newest first, paged by id) so left/right walks all of it. An
  // explicit list is already complete.
  useEffect(() => {
    if (listed) return;
    let cancelled = false;
    (async () => {
      const all: Message[] = [];
      let before = 0;
      for (let i = 0; i < VIEWER_MAX_PAGES; i++) {
        const page = await store.api.chatMedia(chatId, 'media', before, VIEWER_PAGE);
        all.push(...page);
        if (page.length < VIEWER_PAGE) break;
        before = page[page.length - 1].id;
      }
      if (cancelled) return;
      const list = toViewerItems(all);
      if (list.some((it) => it.mediaId === target.mediaId)) setItems(list);
      else if (seed.length === 0) close();
    })().catch((err) => {
      if (cancelled) return;
      if (seed.length > 0) store.showToast(errorMessage(err));
      else close();
    });
    return () => {
      cancelled = true;
    };
  }, [chatId, target.mediaId]);

  const index = items.findIndex((it) => it.mediaId === mediaId);
  const item = index >= 0 ? items[index] : undefined;

  const go = (delta: number) => {
    const next = items[index + delta];
    if (!next) return;
    setMediaId(next.mediaId);
    setZoom(1);
    setPan({ x: 0, y: 0 });
  };

  const setZoomClamped = (z: number) => {
    const v = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z));
    setZoom(v);
    if (v === 1) setPan({ x: 0, y: 0 });
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close();
      else if (e.key === 'ArrowLeft') go(-1);
      else if (e.key === 'ArrowRight') go(1);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  if (!item) return null;
  const chat = store.chats.value.find((c) => c.id === chatId);
  const title = listed ? listed.title : chat ? senderName(chat.sender) : '';
  const isPhoto = item.kind === 'photo';

  return (
    <div class="MediaViewer" role="dialog" aria-modal="true" aria-label="媒体查看器">
      <div class="MediaViewer-head">
        <div class="MediaViewer-sender">
          <span class="MediaViewer-name">{title}</span>
          <span class="MediaViewer-date">
            {formatFullDate(item.date)}
            {items.length > 1 && ` · ${index + 1} / ${items.length}`}
          </span>
        </div>
        <div class="MediaViewer-actions">
          {isPhoto && (
            <>
              <IconButton label="缩小" class="translucent-white zoom-btn" disabled={zoom <= MIN_ZOOM} onClick={() => setZoomClamped(zoom - ZOOM_STEP)}>
                <ZoomOut size={24} />
              </IconButton>
              <IconButton label="放大" class="translucent-white zoom-btn" disabled={zoom >= MAX_ZOOM} onClick={() => setZoomClamped(zoom + ZOOM_STEP)}>
                <ZoomIn size={24} />
              </IconButton>
            </>
          )}
          <a class="IconButton translucent-white" href={mediaUrl(item.mediaId, true)} download aria-label="下载" title="下载">
            <Download size={24} />
          </a>
          <IconButton label="关闭" class="translucent-white" onClick={close}>
            <X size={24} />
          </IconButton>
        </div>
      </div>
      <div
        class="MediaViewer-content"
        onClick={(e) => {
          if (e.target === e.currentTarget) close();
        }}
        onWheel={(e) => {
          if (!isPhoto) return;
          e.preventDefault();
          setZoomClamped(zoom + (e.deltaY < 0 ? ZOOM_STEP : -ZOOM_STEP));
        }}
        onPointerDown={(e) => {
          // zoom > 1 is the pan-drag's territory (handled on the <img> itself below); leave it alone.
          if (zoom > 1) return;
          // A drag on a video's native controls (scrubbing) is not a swipe.
          if ((e.target as HTMLElement).tagName === 'VIDEO') return;
          swipeStart.current = { x: e.clientX, y: e.clientY };
        }}
        onPointerUp={(e) => {
          const start = swipeStart.current;
          swipeStart.current = null;
          if (!start || zoom > 1) return;
          const dx = e.clientX - start.x;
          const dy = e.clientY - start.y;
          if (Math.abs(dx) > SWIPE_H_THRESHOLD && Math.abs(dx) > Math.abs(dy)) go(dx < 0 ? 1 : -1); // swipe left = next
          else if (dy > SWIPE_V_THRESHOLD && dy > Math.abs(dx)) close();
        }}
      >
        {isPhoto ? (
          <img
            key={item.mediaId}
            src={mediaUrl(item.mediaId)}
            alt=""
            draggable={false}
            class={zoom > 1 ? 'zoomed' : ''}
            style={{ transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})` }}
            onDblClick={() => setZoomClamped(zoom > 1 ? 1 : 2)}
            onPointerDown={(e) => {
              if (zoom <= 1) return;
              (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
              drag.current = { x: e.clientX, y: e.clientY, px: pan.x, py: pan.y };
            }}
            onPointerMove={(e) => {
              const d = drag.current;
              if (d) setPan({ x: d.px + e.clientX - d.x, y: d.py + e.clientY - d.y });
            }}
            onPointerUp={() => {
              drag.current = null;
            }}
          />
        ) : (
          <video
            key={item.mediaId}
            src={mediaUrl(item.mediaId)}
            controls={item.kind === 'video'}
            autoplay
            loop={item.kind === 'animation'}
            muted={item.kind === 'animation'}
            playsInline
          />
        )}
      </div>
      {index > 0 && (
        <button type="button" class="MediaViewer-nav prev" aria-label="上一个" onClick={() => go(-1)}>
          <ChevronLeft size={36} />
        </button>
      )}
      {index < items.length - 1 && (
        <button type="button" class="MediaViewer-nav next" aria-label="下一个" onClick={() => go(1)}>
          <ChevronRight size={36} />
        </button>
      )}
      {item.text && (
        <div class="MediaViewer-caption">
          <RichText text={item.text} entities={item.entities} />
        </div>
      )}
    </div>
  );
}

/** Full-screen viewer over all photos/videos/GIFs of the chat, or over an explicit list (an
 * article's media); opened by setting store.viewer. */
export function MediaViewer() {
  const store = useStore();
  const target = store.viewer.value;
  if (!target) return null;
  const key = 'list' in target ? `list:${target.mediaId}` : `${target.chatId}:${target.mediaId}`;
  return <ViewerInner key={key} target={target} />;
}
