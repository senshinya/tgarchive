import { Download, Film, X, ZoomIn, ZoomOut } from 'lucide-preact';
import { useEffect, useRef } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import { formatFullDate } from '../../lib/format';
import type { ViewerItem } from '../../state/store';
import { IconButton } from '../../ui/Button';
import { RichText } from '../message/RichText';

interface Props {
  items: ViewerItem[];
  index: number;
  title: string;
  onClose: () => void;
  onPick: (index: number) => void;
  onZoom?: (dir: 1 | -1) => void;
}

/** The thumbnail strip (spec §3.6): shown for more than one item, current one highlighted and
 * kept in view. */
export function Thumbs({ items, index, onPick }: Pick<Props, 'items' | 'index' | 'onPick'>) {
  const strip = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const cur = strip.current?.children[index] as HTMLElement | undefined;
    cur?.scrollIntoView?.({ block: 'nearest', inline: 'center', behavior: 'smooth' });
  }, [index, items.length]);
  if (items.length < 2) return null;
  return (
    <div class="ViewerThumbs" ref={strip} role="tablist" aria-label="全部媒体">
      {items.map((it, i) => {
        const src = it.thumbId ? mediaUrl(it.thumbId) : it.kind === 'photo' ? mediaUrl(it.mediaId) : null;
        return (
          <button
            key={it.mediaId}
            type="button"
            role="tab"
            aria-selected={i === index}
            aria-label={`第 ${i + 1} 项`}
            class={`ViewerThumbs-item${i === index ? ' active' : ''}`}
            onClick={() => onPick(i)}
          >
            {src ? <img src={src} alt="" loading="lazy" draggable={false} /> : <Film size={20} />}
          </button>
        );
      })}
    </div>
  );
}

/** Header (title, date, position, zoom, download, close), caption and thumbnail strip drawn over
 * the gallery. Fades with PhotoSwipe's UI visibility (see viewer.scss). */
export function ViewerOverlay({ items, index, title, onClose, onPick, onZoom }: Props) {
  const item = items[index];
  if (!item) return null;
  return (
    <div class="ViewerOverlay" data-kind={item.kind}>
      <div class="MediaViewer-head">
        <div class="MediaViewer-sender">
          <span class="MediaViewer-name">{title}</span>
          <span class="MediaViewer-date">
            {formatFullDate(item.date)}
            {items.length > 1 && ` · ${index + 1} / ${items.length}`}
          </span>
        </div>
        <div class="MediaViewer-actions">
          {item.kind === 'photo' && onZoom && (
            <>
              <IconButton label="缩小" class="translucent-white zoom-btn" onClick={() => onZoom(-1)}>
                <ZoomOut size={24} />
              </IconButton>
              <IconButton label="放大" class="translucent-white zoom-btn" onClick={() => onZoom(1)}>
                <ZoomIn size={24} />
              </IconButton>
            </>
          )}
          <a class="IconButton translucent-white" href={mediaUrl(item.mediaId, true)} download aria-label="下载" title="下载">
            <Download size={24} />
          </a>
          <IconButton label="关闭" class="translucent-white" onClick={onClose}>
            <X size={24} />
          </IconButton>
        </div>
      </div>
      <div class="ViewerOverlay-foot">
        {item.text && (
          <div class="MediaViewer-caption">
            <RichText text={item.text} entities={item.entities} />
          </div>
        )}
        <Thumbs items={items} index={index} onPick={onPick} />
      </div>
    </div>
  );
}
