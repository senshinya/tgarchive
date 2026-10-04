import { Play, X } from 'lucide-preact';
import { useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { errorMessage, mediaUrl } from '../../api/client';
import type { Message, SharedMediaType } from '../../api/types';
import { extractLinks } from '../../lib/entities';
import { formatDuration, formatMonth, hashString, monthKey, peerColor, previewText } from '../../lib/format';
import { useStore } from '../../state/store';
import { IconButton } from '../../ui/Button';
import { Spinner } from '../../ui/Spinner';
import { Tabs } from '../../ui/Tabs';
import { Document } from '../media/Document';
import { mainMedia, readyThumb } from '../media/util';
import './right.scss';

export const SHARED_PAGE = 100;

const TABS: { key: SharedMediaType; label: string; empty: string }[] = [
  { key: 'media', label: '媒体', empty: '暂无媒体' },
  { key: 'file', label: '文件', empty: '暂无文件' },
  { key: 'link', label: '链接', empty: '暂无链接' },
];

interface ListState {
  items: Message[]; // newest first, as the API returns them
  hasMore: boolean;
  loading: boolean;
  error: string;
}

const INITIAL: ListState = { items: [], hasMore: true, loading: false, error: '' };

function MediaTile({ msg }: { msg: Message }) {
  const store = useStore();
  const main = mainMedia(msg);
  const thumb = readyThumb(msg);
  if (!main) return null;
  const done = main.state === 'done';
  const src = main.kind === 'photo' && done ? mediaUrl(main.id) : thumb ? mediaUrl(thumb.id) : null;
  return (
    <button
      type="button"
      class="SharedMedia-tile"
      disabled={!done}
      aria-label={main.kind === 'photo' ? '查看照片' : '播放视频'}
      onClick={() => {
        store.viewer.value = { chatId: msg.chat_id, messageId: msg.id, mediaId: main.id };
      }}
    >
      {src ? (
        <img src={src} alt="" loading="lazy" decoding="async" />
      ) : done ? (
        <video src={`${mediaUrl(main.id)}#t=0.1`} preload="metadata" muted playsInline />
      ) : (
        <span class="SharedMedia-tile-state">{main.state === 'pending' ? '下载中' : '不可用'}</span>
      )}
      {main.kind !== 'photo' && (
        <span class="MediaBadge">
          <Play size={10} fill="currentColor" /> {main.kind === 'animation' ? 'GIF' : formatDuration(main.duration)}
        </span>
      )}
    </button>
  );
}

function LinkRows({ msg }: { msg: Message }) {
  const links = extractLinks(msg.text, msg.entities);
  return (
    <>
      {links.map((href) => {
        const host = new URL(href).hostname || href;
        return (
          <a class="SharedLink" key={`${msg.id}-${href}`} href={href} target="_blank" rel="noopener noreferrer">
            <span class="SharedLink-icon" style={{ background: peerColor(hashString(host)) }}>
              {Array.from(host.replace(/^www\./, ''))[0]?.toUpperCase()}
            </span>
            <span class="SharedLink-info">
              <span class="SharedLink-title">{host}</span>
              <span class="SharedLink-url">{href}</span>
              <span class="SharedLink-text">{previewText(msg.kind, msg.text)}</span>
            </span>
          </a>
        );
      })}
    </>
  );
}

/** Right column: shared media / files / links of the open chat, grouped by month, paged by message id. */
export function SharedMedia({ chatId }: { chatId: number }) {
  const store = useStore();
  const [tab, setTab] = useState<SharedMediaType>('media');
  const [list, setList] = useState<ListState>(INITIAL);
  const token = useRef(0);
  const stateRef = useRef(list);
  stateRef.current = list;

  const load = async (reset: boolean) => {
    const current = reset ? INITIAL : stateRef.current;
    if (!reset && (current.loading || !current.hasMore)) return;
    const my = ++token.current;
    setList({ ...current, loading: true, error: '' });
    try {
      const before = current.items.length ? current.items[current.items.length - 1].id : 0;
      const page = await store.api.chatMedia(chatId, tab, before, SHARED_PAGE);
      if (my !== token.current) return;
      setList({ items: [...current.items, ...page], hasMore: page.length >= SHARED_PAGE, loading: false, error: '' });
    } catch (e) {
      if (my !== token.current) return;
      setList({ ...current, loading: false, error: errorMessage(e) });
    }
  };

  useEffect(() => {
    void load(true);
  }, [chatId, tab]);

  const groups = useMemo(() => {
    const out: { key: string; label: string; items: Message[] }[] = [];
    for (const m of list.items) {
      const key = monthKey(m.date);
      if (out[out.length - 1]?.key !== key) out.push({ key, label: formatMonth(m.date), items: [] });
      out[out.length - 1].items.push(m);
    }
    return out;
  }, [list.items]);

  const onScroll = (e: Event) => {
    const el = e.currentTarget as HTMLElement;
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 300) void load(false);
  };

  const current = TABS.find((t) => t.key === tab)!;

  return (
    <div class="SharedMedia">
      <div class="right-header">
        <IconButton
          label="关闭"
          onClick={() => {
            store.sharedMediaOpen.value = false;
          }}
        >
          <X size={24} />
        </IconButton>
        <h3 class="right-header-title">共享媒体</h3>
      </div>
      <Tabs class="SharedMedia-tabs" items={TABS.map((t) => ({ key: t.key, label: t.label }))} active={tab} onChange={setTab} />
      <div class="SharedMedia-content custom-scroll" onScroll={onScroll}>
        {groups.map((g) => (
          <section key={g.key} class="SharedMedia-month">
            <h4 class="SharedMedia-month-title">{g.label}</h4>
            {tab === 'media' && (
              <div class="SharedMedia-grid">
                {g.items.map((m) => (
                  <MediaTile key={m.id} msg={m} />
                ))}
              </div>
            )}
            {tab === 'file' && (
              <div class="SharedMedia-files">
                {g.items.map((m) => (
                  <Document key={m.id} msg={m} />
                ))}
              </div>
            )}
            {tab === 'link' && (
              <div class="SharedMedia-links">
                {g.items.map((m) => (
                  <LinkRows key={m.id} msg={m} />
                ))}
              </div>
            )}
          </section>
        ))}
        {list.loading && (
          <div class="SharedMedia-notice">
            <Spinner size={28} />
          </div>
        )}
        {list.error && (
          <div class="SharedMedia-notice">
            <span>{list.error}</span>
            <button type="button" onClick={() => void load(list.items.length === 0)}>
              重试
            </button>
          </div>
        )}
        {!list.loading && !list.error && list.items.length === 0 && <div class="SharedMedia-notice">{current.empty}</div>}
      </div>
    </div>
  );
}
