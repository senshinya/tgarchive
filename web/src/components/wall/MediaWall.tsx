import { Images, Play } from 'lucide-preact';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'preact/hooks';
import { ApiError, errorMessage, mediaUrl, WALL_PAGE } from '../../api/client';
import type { Message, WallSource, WallType } from '../../api/types';
import { formatDuration, formatMonth, monthKey } from '../../lib/format';
import { aspect, justify } from '../../lib/justify';
import { useStore } from '../../state/store';
import { Spinner } from '../../ui/Spinner';
import { Tabs } from '../../ui/Tabs';
import { mainMedia, playUrl, readyThumb } from '../media/util';
import { ViewHeader } from '../middle/ViewHeader';
import { Wallpaper } from '../middle/Wallpaper';
import { toViewerItems } from '../viewer/MediaViewer';
import './wall.scss';

const TYPES: { key: WallType; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'photo', label: '图片' },
  { key: 'video', label: '视频' },
];
const SOURCES: { key: WallSource; label: string }[] = [
  { key: 'all', label: '全部来源' },
  { key: 'private', label: '私聊' },
  { key: 'channel', label: '频道' },
];

const GAP = 4;
const LOAD_MORE_PX = 600;
/** Row height to aim for: smaller on phones so a row holds two or three items. */
const targetHeight = (width: number) => (width < 600 ? 120 : 180);
/** jsdom and the first render have no layout: lay out for a phone-sized column until measured. */
const FALLBACK_WIDTH = 600;

function Tile({ msg, width, height, onOpen }: { msg: Message; width: number; height: number; onOpen: () => void }) {
  const main = mainMedia(msg)!;
  const thumb = readyThumb(msg);
  const done = main.state === 'done';
  const spoiler = msg.extra?.spoiler === true;
  const src = main.kind === 'photo' && done ? mediaUrl(main.id) : thumb ? mediaUrl(thumb.id) : null;
  return (
    <button
      type="button"
      class="MediaWall-tile"
      style={{ width: `${width}px`, height: `${height}px` }}
      disabled={!done}
      aria-label={main.kind === 'photo' ? '查看照片' : '播放视频'}
      onClick={onOpen}
    >
      {src ? (
        <img src={src} alt="" loading="lazy" decoding="async" class={spoiler ? 'media-spoiler-blur' : ''} />
      ) : done ? (
        <video
          src={`${playUrl(main.id, main.compat_codec)}#t=0.1`}
          preload="metadata"
          muted
          playsInline
          class={spoiler ? 'media-spoiler-blur' : ''}
        />
      ) : (
        <span class="MediaWall-tile-state">{main.state === 'pending' ? '下载中' : '不可用'}</span>
      )}
      {main.kind !== 'photo' && (
        <span class="MediaBadge">
          <Play size={10} fill="currentColor" /> {main.kind === 'animation' ? 'GIF' : formatDuration(main.duration)}
        </span>
      )}
    </button>
  );
}

/** Middle column: every chat's photos, videos and GIFs, newest first, in justified rows grouped
 * by month; opening one walks the whole wall in the media viewer. */
export function MediaWall() {
  const store = useStore();
  const [type, setType] = useState<WallType>('all');
  const [source, setSource] = useState<WallSource>('all');
  const [items, setItems] = useState<Message[]>([]);
  const [hasMore, setHasMore] = useState(true);
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState('');
  const [width, setWidth] = useState(0);
  const seq = useRef(0);
  const shown = useRef<Message[]>([]);
  shown.current = items;
  const busy = useRef(false);
  const container = useRef<HTMLDivElement>(null);

  const load = async (reset: boolean) => {
    if (!reset && (busy.current || !hasMore)) return;
    const my = ++seq.current;
    busy.current = true;
    setLoading(true);
    setError('');
    const before = reset || shown.current.length === 0 ? 0 : shown.current[shown.current.length - 1].id;
    try {
      const page = await store.api.allMedia(type, source, before, WALL_PAGE);
      if (my !== seq.current) return;
      setItems((prev) => (reset ? page : [...prev, ...page]));
      setHasMore(page.length >= WALL_PAGE);
      setLoaded(true);
    } catch (e) {
      if (my === seq.current) setError(errorMessage(e));
    } finally {
      if (my === seq.current) {
        busy.current = false;
        setLoading(false);
      }
    }
  };

  useEffect(() => {
    setItems([]);
    setHasMore(true);
    setLoaded(false);
    void load(true);
  }, [type, source]);

  // The tiles are this view's own copies: keep them in step with downloads and deletions.
  useEffect(
    () =>
      store.onEvent((ev) => {
        const has = (id: number) => shown.current.some((m) => m.id === id);
        const drop = (id: number) => setItems((prev) => prev.filter((m) => m.id !== id));
        switch (ev.type) {
          case 'resync':
            void load(true);
            return;
          case 'message.deleted':
            if (has(ev.data.message_id)) drop(ev.data.message_id);
            return;
          case 'media.updated':
            for (const id of ev.data.message_ids ?? []) {
              if (!has(id)) continue;
              store.api.message(id).then(
                (m) => setItems((prev) => prev.map((x) => (x.id === id ? m : x))),
                (e) => e instanceof ApiError && e.status === 404 && drop(id),
              );
            }
        }
      }),
    [type, source],
  );

  useLayoutEffect(() => {
    const el = container.current;
    if (!el) return;
    const measure = () => setWidth(el.clientWidth);
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const layoutWidth = width || FALLBACK_WIDTH;
  const groups = useMemo(() => {
    const out: { key: string; label: string; items: Message[] }[] = [];
    for (const m of items) {
      if (!mainMedia(m)) continue;
      const key = monthKey(m.date);
      if (out[out.length - 1]?.key !== key) out.push({ key, label: formatMonth(m.date), items: [] });
      out[out.length - 1].items.push(m);
    }
    const h = targetHeight(layoutWidth);
    return out.map((g) => {
      const sizes = g.items.map((m) => {
        const md = mainMedia(m)!;
        return { w: md.width, h: md.height };
      });
      return { ...g, sizes, rows: justify(sizes, layoutWidth, h, GAP) };
    });
  }, [items, layoutWidth]);

  const open = (msg: Message) => {
    const md = mainMedia(msg)!;
    store.viewer.value = { wall: { type, source }, seed: toViewerItems(shown.current), mediaId: md.id };
  };

  return (
    <div id="MiddleColumn" class="ViewColumn">
      <Wallpaper />
      <ViewHeader icon={<Images size={22} />} title="媒体墙" status={loaded ? `${items.length}${hasMore ? '+' : ''} 项` : '加载中…'} />
      <div
        class="MediaWall custom-scroll"
        onScroll={(e) => {
          const el = e.currentTarget as HTMLElement;
          if (el.scrollHeight - el.scrollTop - el.clientHeight < LOAD_MORE_PX) void load(false);
        }}
      >
        <div class="MediaWall-container">
          <div class="MediaWall-filters">
            <Tabs class="MediaWall-tabs" items={TYPES} active={type} onChange={setType} />
            <Tabs class="MediaWall-tabs" items={SOURCES} active={source} onChange={setSource} />
          </div>
          <div class="MediaWall-body">
            <div class="MediaWall-grid" ref={container}>
              {groups.map((g) => (
                <section key={g.key} class="MediaWall-month">
                  <h4 class="MediaWall-month-title">{g.label}</h4>
                  {g.rows.map((row) => (
                    <div key={row.start} class="MediaWall-row" style={{ height: `${row.height}px` }}>
                      {g.items.slice(row.start, row.start + row.count).map((m, i) => {
                        const s = g.sizes[row.start + i];
                        return <Tile key={m.id} msg={m} width={aspect(s.w, s.h) * row.height} height={row.height} onOpen={() => open(m)} />;
                      })}
                    </div>
                  ))}
                </section>
              ))}
            </div>
          </div>
          {loading && (
            <div class="history-notice">
              <Spinner size={28} />
            </div>
          )}
          {error && (
            <div class="MediaWall-notice">
              <span>{error}</span>
              <button type="button" onClick={() => void load(items.length === 0)}>
                重试
              </button>
            </div>
          )}
          {loaded && !loading && !error && items.length === 0 && <div class="MediaWall-notice">暂无媒体</div>}
        </div>
      </div>
    </div>
  );
}
