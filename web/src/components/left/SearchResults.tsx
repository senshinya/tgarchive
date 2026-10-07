import { FileText, Newspaper } from 'lucide-preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { avatarUrl, errorMessage } from '../../api/client';
import type { Chat, SearchHit } from '../../api/types';
import { chatName, formatListTime } from '../../lib/format';
import { navigate } from '../../lib/router';
import { highlightParts } from '../../lib/search';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { Spinner } from '../../ui/Spinner';

const DEBOUNCE_MS = 300;
const LOAD_MORE_PX = 300;

function chatAvatar(chat: Chat | undefined, fallback: number) {
  if (chat?.kind === 'channel') {
    const ch = chat.channel;
    return {
      name: chatName(chat),
      peerId: ch?.channel_id ?? fallback,
      src: ch?.has_avatar ? avatarUrl('channels', ch.channel_id) : null,
    };
  }
  if (chat) {
    return {
      name: chatName(chat),
      peerId: chat.sender.tg_user_id,
      src: chat.sender.has_avatar ? avatarUrl('senders', chat.sender.tg_user_id) : null,
    };
  }
  return { name: '会话', peerId: fallback, src: null };
}

function Result({ hit }: { hit: SearchHit }) {
  const store = useStore();
  const m = hit.message;
  const a = chatAvatar(
    store.chats.value.find((c) => c.id === m.chat_id),
    m.chat_id,
  );
  return (
    <button
      type="button"
      class="ChatItem SearchResult"
      onClick={() => {
        store.jumpTo.value = { key: m.chat_id, messageId: m.id };
        navigate({ name: 'chat', chatId: m.chat_id }, { fromList: true });
      }}
    >
      <Avatar name={a.name} peerId={a.peerId} src={a.src} size="large" />
      <span class="ChatItem-info">
        <span class="ChatItem-row">
          <span class="ChatItem-title">{a.name}</span>
          <span class="ChatItem-time">{formatListTime(m.date)}</span>
        </span>
        <span class="ChatItem-subtitle">
          {hit.field === 'files' && <FileText size={14} class="SearchResult-field" aria-label="文件名" />}
          {hit.field === 'article' && <Newspaper size={14} class="SearchResult-field" aria-label="文章" />}
          {highlightParts(hit.snippet, hit.ranges).map((p, i) => (p.mark ? <mark key={i}>{p.text}</mark> : p.text))}
        </span>
      </span>
    </button>
  );
}

/** The messages matching the left column's search, newest first, a page at a time. */
export function SearchResults() {
  const store = useStore();
  const q = store.searchQuery.value.trim();
  const scope = store.searchScope.value;
  const [items, setItems] = useState<SearchHit[]>([]);
  const [next, setNext] = useState(0);
  const [loading, setLoading] = useState(false);
  const [done, setDone] = useState(false); // the first page arrived
  const [error, setError] = useState('');
  const seq = useRef(0);

  const fetchPage = async (before: number) => {
    const my = ++seq.current;
    setLoading(true);
    setError('');
    try {
      const page = await store.api.search(q, scope, before);
      if (my !== seq.current) return;
      setItems((prev) => (before ? [...prev, ...page.items] : page.items));
      setNext(page.next);
      setDone(true);
    } catch (e) {
      if (my === seq.current) setError(errorMessage(e));
    } finally {
      if (my === seq.current) setLoading(false);
    }
  };

  useEffect(() => {
    seq.current++;
    setItems([]);
    setNext(0);
    setDone(false);
    setLoading(false);
    if (!q) return;
    const t = setTimeout(() => void fetchPage(0), DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [q, scope]);

  const onScroll = (e: Event) => {
    const el = e.currentTarget as HTMLElement;
    if (next && !loading && el.scrollHeight - el.scrollTop - el.clientHeight < LOAD_MORE_PX) void fetchPage(next);
  };

  return (
    <div class="chat-list SearchResults custom-scroll" onScroll={onScroll}>
      {items.map((h) => (
        <Result key={h.message.id} hit={h} />
      ))}
      {loading && (
        <div class="chat-list-empty">
          <Spinner size={28} />
        </div>
      )}
      {error && <div class="chat-list-empty">{error}</div>}
      {done && !loading && !error && items.length === 0 && (
        <div class="chat-list-empty">
          <p class="chat-list-empty-title">没有找到相关消息</p>
        </div>
      )}
    </div>
  );
}
