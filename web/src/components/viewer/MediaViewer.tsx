import { ChevronLeft, ChevronRight, Download, X, ZoomIn, ZoomOut } from 'lucide-preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import type { Media, Message } from '../../api/types';
import { formatFullDate, senderName } from '../../lib/format';
import { useStore, type ViewerTarget } from '../../state/store';
import { IconButton } from '../../ui/Button';
import { RichText } from '../message/RichText';
import { VISUAL_KINDS, mainMedia } from '../media/util';
import './viewer.scss';

export const VIEWER_PAGE = 100;
export const VIEWER_MAX_PAGES = 50;
const MIN_ZOOM = 1;
const MAX_ZOOM = 4;
const ZOOM_STEP = 0.5;

export interface ViewerItem {
  msg: Message;
  media: Media;
}

export function toViewerItems(msgs: Message[]): ViewerItem[] {
  const out: ViewerItem[] = [];
  for (const msg of msgs) {
    const media = mainMedia(msg);
    if (media && media.state === 'done' && VISUAL_KINDS.includes(msg.kind)) out.push({ msg, media });
  }
  return out.sort((a, b) => a.msg.id - b.msg.id);
}

function ViewerInner({ target }: { target: ViewerTarget }) {
  const store = useStore();
  const close = () => {
    store.viewer.value = null;
  };
  const seed = toViewerItems(store.conv(target.chatId).items.filter((m) => m.id === target.messageId));
  const [items, setItems] = useState<ViewerItem[]>(seed);
  const [mediaId, setMediaId] = useState(target.mediaId);
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null);

  // Load the whole chat's media (newest first, paged by id) so left/right walks all of it.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const all: Message[] = [];
      let before = 0;
      for (let i = 0; i < VIEWER_MAX_PAGES; i++) {
        const page = await store.api.chatMedia(target.chatId, 'media', before, VIEWER_PAGE);
        all.push(...page);
        if (page.length < VIEWER_PAGE) break;
        before = page[page.length - 1].id;
      }
      if (cancelled) return;
      const list = toViewerItems(all);
      if (list.some((it) => it.media.id === target.mediaId)) setItems(list);
      else if (seed.length === 0) close();
    })().catch(() => {
      if (!cancelled && seed.length === 0) close();
    });
    return () => {
      cancelled = true;
    };
  }, [target.chatId, target.mediaId]);

  const index = items.findIndex((it) => it.media.id === mediaId);
  const item = index >= 0 ? items[index] : undefined;

  const go = (delta: number) => {
    const next = items[index + delta];
    if (!next) return;
    setMediaId(next.media.id);
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
  const chat = store.chats.value.find((c) => c.id === target.chatId);
  const isPhoto = item.msg.kind === 'photo';

  return (
    <div class="MediaViewer" role="dialog" aria-modal="true" aria-label="媒体查看器">
      <div class="MediaViewer-head">
        <div class="MediaViewer-sender">
          <span class="MediaViewer-name">{chat ? senderName(chat.sender) : ''}</span>
          <span class="MediaViewer-date">
            {formatFullDate(item.msg.date)}
            {items.length > 1 && ` · ${index + 1} / ${items.length}`}
          </span>
        </div>
        <div class="MediaViewer-actions">
          {isPhoto && (
            <>
              <IconButton label="缩小" class="translucent-white" disabled={zoom <= MIN_ZOOM} onClick={() => setZoomClamped(zoom - ZOOM_STEP)}>
                <ZoomOut size={24} />
              </IconButton>
              <IconButton label="放大" class="translucent-white" disabled={zoom >= MAX_ZOOM} onClick={() => setZoomClamped(zoom + ZOOM_STEP)}>
                <ZoomIn size={24} />
              </IconButton>
            </>
          )}
          <a class="IconButton translucent-white" href={mediaUrl(item.media.id, true)} download aria-label="下载" title="下载">
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
      >
        {isPhoto ? (
          <img
            key={item.media.id}
            src={mediaUrl(item.media.id)}
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
            key={item.media.id}
            src={mediaUrl(item.media.id)}
            controls={item.msg.kind === 'video'}
            autoplay
            loop={item.msg.kind === 'animation'}
            muted={item.msg.kind === 'animation'}
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
      {item.msg.text && (
        <div class="MediaViewer-caption">
          <RichText text={item.msg.text} entities={item.msg.entities} />
        </div>
      )}
    </div>
  );
}

/** Full-screen viewer over all photos/videos/GIFs of the chat; opened by setting store.viewer. */
export function MediaViewer() {
  const store = useStore();
  const target = store.viewer.value;
  if (!target) return null;
  return <ViewerInner key={`${target.chatId}:${target.mediaId}`} target={target} />;
}
