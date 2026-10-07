import { ArrowLeft, Bookmark, Copy, Download, Locate } from 'lucide-preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { ApiError, avatarUrl, errorMessage, mediaUrl } from '../../api/client';
import type { Message, TagCount } from '../../api/types';
import { chatName, formatFullDate } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { IconButton } from '../../ui/Button';
import { ContextMenu, type MenuItem } from '../../ui/ContextMenu';
import { ConfirmDialog } from '../../ui/Modal';
import { Spinner } from '../../ui/Spinner';
import { Tabs } from '../../ui/Tabs';
import { mainMedia } from '../media/util';
import { MessageBubble } from '../message/MessageBubble';
import { senderInfo } from '../message/MessageList';
import { Wallpaper } from '../middle/Wallpaper';
import { favoriteMenuItems } from './menu';
import { TagDialog } from './TagDialog';
import './favorites.scss';

const LOAD_MORE_PX = 400;
const LONG_PRESS_MS = 500;

function download(href: string) {
  const a = document.createElement('a');
  a.href = href;
  a.download = '';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

/** One favorite: where it is from, the message as in its conversation, and its tags. */
function FavoriteCard({ msg, onMenu }: { msg: Message; onMenu: (x: number, y: number, m: Message) => void }) {
  const store = useStore();
  const chat = store.chats.value.find((c) => c.id === msg.chat_id);
  const sender = senderInfo(chat, msg.chat_id);
  const src =
    chat?.kind === 'channel'
      ? chat.channel?.has_avatar
        ? avatarUrl('channels', chat.channel.channel_id)
        : null
      : chat?.sender.has_avatar
        ? avatarUrl('senders', chat.sender.tg_user_id)
        : null;
  const tags = msg.favorite?.tags ?? [];
  return (
    <div class="FavoriteCard">
      <div class="FavoriteCard-source">
        <Avatar name={sender.name} peerId={sender.peerId} src={src} size="mini" />
        <span class="FavoriteCard-chat">{chat ? chatName(chat) : '会话'}</span>
        <span class="FavoriteCard-date">{formatFullDate(msg.date)}</span>
        <button
          type="button"
          class="FavoriteCard-locate"
          aria-label="定位"
          title="在会话中查看"
          onClick={() => {
            store.jumpTo.value = { key: msg.chat_id, messageId: msg.id };
            navigate({ name: 'chat', chatId: msg.chat_id });
          }}
        >
          <Locate size={18} />
        </button>
      </div>
      <div class="message-group">
        <MessageBubble bubble={{ kind: 'message', key: `f${msg.id}`, msg, first: true, last: true }} sender={sender} convKey={msg.chat_id} onMenu={onMenu} />
      </div>
      {tags.length > 0 && (
        <div class="FavoriteCard-tags">
          {tags.map((t) => (
            <span key={t.id} class="TagChip">
              {t.name}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

/** 收藏: every favorite, most recently added first, filterable by tag. */
export function FavoritesView() {
  const store = useStore();
  const [tag, setTag] = useState(0);
  const [tags, setTags] = useState<TagCount[]>([]);
  const [items, setItems] = useState<Message[]>([]);
  const [next, setNext] = useState(0);
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [menu, setMenu] = useState<{ x: number; y: number; msg: Message } | null>(null);
  const [tagging, setTagging] = useState<Message | null>(null);
  const [dropTag, setDropTag] = useState<TagCount | null>(null);
  const seq = useRef(0);
  const press = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const load = async (before: number, forTag = tag) => {
    const my = ++seq.current;
    setLoading(true);
    try {
      const page = await store.api.favorites(forTag, before);
      if (my !== seq.current) return;
      const msgs = page.items.map((i) => i.message);
      setItems((prev) => (before ? [...prev, ...msgs] : msgs));
      setNext(page.next);
      setLoaded(true);
    } catch (e) {
      if (my === seq.current) store.showToast(errorMessage(e));
    } finally {
      if (my === seq.current) setLoading(false);
    }
  };
  const loadTags = () =>
    store.api.tags().then(
      (t) => {
        setTags(t);
        if (tag && !t.some((x) => x.id === tag)) setTag(0);
      },
      () => undefined,
    );

  useEffect(() => {
    void load(0, tag);
  }, [tag]);
  // The cards are this view's own copies: keep them in step with the archive.
  const shown = useRef<Message[]>([]);
  shown.current = items;
  const drop = (id: number) => setItems((prev) => prev.filter((m) => m.id !== id));
  const refresh = async (id: number) => {
    try {
      const m = await store.api.message(id);
      if (m.favorite) setItems((prev) => prev.map((x) => (x.id === id ? m : x)));
      else drop(id);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) drop(id);
    }
  };
  useEffect(() => {
    void loadTags();
    return store.onEvent((ev) => {
      const has = (id: number) => shown.current.some((m) => m.id === id);
      switch (ev.type) {
        case 'favorites.updated':
        case 'resync':
          void load(0);
          void loadTags();
          return;
        case 'message.deleted':
          if (!has(ev.data.message_id)) return;
          drop(ev.data.message_id);
          void loadTags();
          return;
        case 'message.updated':
          if (has(ev.data.message_id)) void refresh(ev.data.message_id);
          return;
        case 'media.updated':
          for (const id of ev.data.message_ids ?? []) if (has(id)) void refresh(id);
      }
    });
  }, [tag]);

  const goBack = () => {
    const state = history.state as { fromList?: boolean } | null;
    if (state?.fromList) history.back();
    else navigate({ name: 'home' });
  };

  const menuItems = (msg: Message): MenuItem[] => {
    const out: MenuItem[] = [];
    if (msg.text && navigator.clipboard) {
      out.push({
        label: '复制文本',
        icon: <Copy size={20} />,
        onSelect: () => navigator.clipboard.writeText(msg.text).then(() => store.showToast('已复制'), () => store.showToast('复制失败')),
      });
    }
    const md = mainMedia(msg);
    if (md && md.state === 'done') out.push({ label: '下载', icon: <Download size={20} />, onSelect: () => download(mediaUrl(md.id, true)) });
    return [...out, ...favoriteMenuItems(store, msg, setTagging)];
  };

  const tabs = [
    { key: 0, label: '全部' },
    ...tags.map((t) => ({
      key: t.id,
      label: (
        <span
          class="FavoritesTag"
          onContextMenu={(e) => {
            e.preventDefault();
            setDropTag(t);
          }}
          onTouchStart={() => {
            press.current = setTimeout(() => setDropTag(t), LONG_PRESS_MS);
          }}
          onTouchEnd={() => clearTimeout(press.current)}
          onTouchMove={() => clearTimeout(press.current)}
        >
          {t.name}
          <span class="FavoritesTag-count">{t.count}</span>
        </span>
      ),
    })),
  ];

  return (
    <div id="MiddleColumn" class="FavoritesColumn">
      <Wallpaper />
      <div class="MiddleHeader">
        <IconButton label="返回" class="back-button" onClick={goBack}>
          <ArrowLeft size={24} />
        </IconButton>
        <span class="MiddleHeader-info FavoritesHeader">
          <span class="FavoritesHeader-icon">
            <Bookmark size={22} />
          </span>
          <span class="MiddleHeader-text">
            <span class="MiddleHeader-title">收藏</span>
            <span class="MiddleHeader-status">{loaded ? `${items.length}${next ? '+' : ''} 条` : '加载中…'}</span>
          </span>
        </span>
      </div>
      <div
        class="Favorites custom-scroll"
        onScroll={(e) => {
          const el = e.currentTarget as HTMLElement;
          if (next && !loading && el.scrollHeight - el.scrollTop - el.clientHeight < LOAD_MORE_PX) void load(next);
        }}
      >
        <div class="Favorites-container">
          {tags.length > 0 && <Tabs class="FavoritesTags" items={tabs} active={tag} onChange={setTag} />}
          {items.map((m) => (
            <FavoriteCard key={m.id} msg={m} onMenu={(x, y, msg) => setMenu({ x, y, msg })} />
          ))}
          {loading && (
            <div class="history-notice">
              <Spinner size={28} />
            </div>
          )}
          {loaded && !loading && items.length === 0 && (
            <div class="Favorites-empty">
              <p class="Favorites-empty-title">还没有收藏的消息</p>
              <p>在消息上右键或长按即可收藏</p>
            </div>
          )}
        </div>
      </div>
      {menu && <ContextMenu x={menu.x} y={menu.y} items={menuItems(menu.msg)} onClose={() => setMenu(null)} />}
      {tagging && (
        <TagDialog
          initial={tagging.favorite?.tags.map((t) => t.name) ?? []}
          onSave={(t) => store.setTags(tagging, t)}
          onClose={() => setTagging(null)}
        />
      )}
      {dropTag && (
        <ConfirmDialog
          title="删除标签"
          text={`将从所有收藏中移除标签「${dropTag.name}」，收藏本身不受影响。`}
          confirmLabel="删除"
          danger
          onConfirm={() => {
            const t = dropTag;
            setDropTag(null);
            store.api.deleteTag(t.id).then(
              () => store.showToast('已删除标签'),
              (e) => store.showToast(errorMessage(e)),
            );
          }}
          onClose={() => setDropTag(null)}
        />
      )}
    </div>
  );
}
