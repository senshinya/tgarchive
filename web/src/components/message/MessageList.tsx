import { ArrowDown, Copy, Download, Trash2 } from 'lucide-preact';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'preact/hooks';
import { avatarUrl, errorMessage, mediaUrl } from '../../api/client';
import type { Chat, Message } from '../../api/types';
import { senderName } from '../../lib/format';
import { groupMessages, type ListEntry } from '../../lib/grouping';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { ContextMenu, type MenuItem } from '../../ui/ContextMenu';
import { ConfirmDialog } from '../../ui/Modal';
import { Spinner } from '../../ui/Spinner';
import { mainMedia } from '../media/util';
import { MessageBubble, type SenderInfo } from './MessageBubble';
import './message.scss';

/** Load older history when the user scrolls within this many px of the top. */
export const LOAD_OLDER_THRESHOLD = 400;
const AT_BOTTOM_PX = 100;
const SHOW_DOWN_PX = 300;
/** How many older pages a jump from the downloads panel may load looking for its message. */
const JUMP_MAX_PAGES = 20;

function download(href: string) {
  const a = document.createElement('a');
  a.href = href;
  a.download = '';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

function senderInfo(chat: Chat | undefined, fallbackPeer: number): SenderInfo {
  if (chat?.kind === 'channel') return { name: chat.channel?.title ?? '', peerId: chat.channel?.channel_id ?? fallbackPeer };
  return chat ? { name: senderName(chat.sender), peerId: chat.sender.tg_user_id } : { name: '', peerId: fallbackPeer };
}

/** A conversation: one chat (positive chatId), or a bot's merged timeline (chatId = -botId), where
 * each group of messages is labelled with its sender, Telegram group-chat style. */
export function MessageList({ chatId }: { chatId: number }) {
  const store = useStore();
  const conv = store.conv(chatId);
  const merged = chatId < 0;
  const chatsById = new Map(store.chats.value.map((c) => [c.id, c]));
  const senderOf = (msgChatId: number) => senderInfo(chatsById.get(msgChatId), msgChatId);
  const entries = useMemo(() => groupMessages(conv.items), [conv.items]);
  // Each day's pill must stick only within its own day (Web A behaviour): nest it as the
  // first child of a per-day container so the next day's container pushes it out, instead of
  // every pill sticking to the same scroll-container top and stacking on scroll.
  const dayGroups = useMemo(() => {
    const out: { key: string; label: string; groups: Extract<ListEntry, { kind: 'group' }>[] }[] = [];
    for (const e of entries) {
      if (e.kind === 'date') out.push({ key: e.key, label: e.label, groups: [] });
      else out[out.length - 1]?.groups.push(e);
    }
    return out;
  }, [entries]);
  const ref = useRef<HTMLDivElement>(null);
  const snap = useRef({ firstId: 0, lastId: 0, height: 0, top: 0, atBottom: true });
  const [showDown, setShowDown] = useState(false);
  const [menu, setMenu] = useState<{ x: number; y: number; msg: Message } | null>(null);
  const [confirm, setConfirm] = useState<Message | null>(null);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    void store.refreshLatest(chatId);
  }, [chatId]);

  const record = () => {
    const el = ref.current;
    if (!el) return;
    const items = store.conv(chatId).items;
    snap.current = {
      firstId: items[0]?.id ?? 0,
      lastId: items[items.length - 1]?.id ?? 0,
      height: el.scrollHeight,
      top: el.scrollTop,
      atBottom: el.scrollHeight - el.scrollTop - el.clientHeight < AT_BOTTOM_PX,
    };
  };

  // Keep the viewport stable: bottom on first load, anchored when older pages are prepended,
  // following new messages only when the user is already at the bottom.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const first = conv.items[0]?.id ?? 0;
    const last = conv.items[conv.items.length - 1]?.id ?? 0;
    const s = snap.current;
    if (s.lastId === 0) el.scrollTop = el.scrollHeight;
    else if (first < s.firstId && last === s.lastId) el.scrollTop = el.scrollHeight - s.height + s.top;
    else if (last > s.lastId && s.atBottom) el.scrollTop = el.scrollHeight;
    record();
    // A first page shorter than the viewport never fires scroll events: keep filling.
    if (conv.loaded && conv.hasMore && el.scrollHeight - el.clientHeight < LOAD_OLDER_THRESHOLD) void store.loadOlder(chatId);
  }, [conv.items]);

  // The downloads panel asks for a message: load older pages until it shows up (up to
  // JUMP_MAX_PAGES), then scroll to it and flash it. A request for another conversation, or one
  // left over when this list goes away, is dropped.
  const jumpPages = useRef({ for: null as unknown, pages: 0 }); // pages loaded for the current jump
  const jump = store.jumpTo.value;
  useEffect(() => {
    if (!jump) return;
    if (jump.key !== chatId) return;
    if (jumpPages.current.for !== jump) jumpPages.current = { for: jump, pages: 0 };
    const el = ref.current?.querySelector(`[data-message-id="${jump.messageId}"]`)?.closest('.Message') as HTMLElement | null | undefined;
    if (conv.items.some((m) => m.id === jump.messageId) && el) {
      store.jumpTo.value = null;
      el.scrollIntoView?.({ block: 'center' });
      el.classList.remove('highlight');
      void el.offsetWidth;
      el.classList.add('highlight');
      return;
    }
    if (!conv.loaded || conv.loading) return;
    const oldest = conv.items[0]?.id ?? 0;
    if (conv.hasMore && jump.messageId < oldest && jumpPages.current.pages < JUMP_MAX_PAGES) {
      jumpPages.current.pages++;
      void store.loadOlder(chatId);
    } else if (!conv.items.some((m) => m.id === jump.messageId)) {
      store.jumpTo.value = null; // not in this conversation, deleted, or too far back
    }
  }, [conv.items, conv.loaded, conv.loading, jump]);
  useEffect(
    () => () => {
      if (store.jumpTo.value?.key === chatId) store.jumpTo.value = null;
    },
    [],
  );

  const onScroll = () => {
    const el = ref.current;
    if (!el) return;
    record();
    setShowDown(el.scrollHeight - el.scrollTop - el.clientHeight > SHOW_DOWN_PX);
    if (el.scrollTop < LOAD_OLDER_THRESHOLD) void store.loadOlder(chatId);
  };

  const menuItems = (msg: Message): MenuItem[] => {
    const items: MenuItem[] = [];
    const caption = msg.text || (msg.media_group_id ? conv.items.find((m) => m.media_group_id === msg.media_group_id && m.text)?.text : '');
    if (caption) {
      items.push({
        label: '复制文本',
        icon: <Copy size={20} />,
        onSelect: () => {
          if (!navigator.clipboard) {
            store.showToast('复制失败');
            return;
          }
          navigator.clipboard.writeText(caption).then(
            () => store.showToast('已复制'),
            () => store.showToast('复制失败'),
          );
        },
      });
    }
    const md = mainMedia(msg);
    if (md && md.state === 'done') {
      items.push({ label: '下载', icon: <Download size={20} />, onSelect: () => download(mediaUrl(md.id, true)) });
    }
    items.push({ label: '删除存档', icon: <Trash2 size={20} />, danger: true, onSelect: () => setConfirm(msg) });
    return items;
  };

  const doDelete = async () => {
    if (!confirm) return;
    setDeleting(true);
    try {
      await store.deleteMessage(confirm);
      setConfirm(null);
    } catch (e) {
      store.showToast(`删除失败：${errorMessage(e)}`);
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div class="MessageList-wrapper">
      <div class="MessageList custom-scroll" ref={ref} onScroll={onScroll}>
        <div class="messages-container">
          {conv.loading && conv.items.length > 0 && (
            <div class="history-loading">
              <Spinner size={28} />
            </div>
          )}
          {conv.error && (
            <div class="history-notice">
              <span>{conv.error}</span>
              <button type="button" onClick={() => void (conv.loaded ? store.loadOlder(chatId) : store.refreshLatest(chatId))}>
                重试
              </button>
            </div>
          )}
          {!conv.loaded && conv.loading && (
            <div class="history-notice">
              <Spinner size={32} />
            </div>
          )}
          {conv.loaded && conv.items.length === 0 && (
            <div class="history-notice">
              <span>暂无消息</span>
            </div>
          )}
          {dayGroups.map((d) => (
            <section class="message-date-group" key={d.key}>
              <div class="sticky-date">
                <span>{d.label}</span>
              </div>
              {d.groups.map((g) => {
                const first = g.bubbles[0];
                const groupChat = (first.kind === 'album' ? first.msgs[0] : first.msg).chat_id;
                const sender = senderOf(groupChat);
                const chat = chatsById.get(groupChat);
                return (
                  <div class={`message-group${merged ? ' with-avatar' : ''}`} key={g.key}>
                    {merged && (
                      <div class="message-group-avatar">
                        <Avatar
                          name={sender.name}
                          peerId={sender.peerId}
                          src={chat?.sender.has_avatar ? avatarUrl('senders', chat.sender.tg_user_id) : null}
                          size="small"
                        />
                      </div>
                    )}
                    {g.bubbles.map((b) => (
                      <MessageBubble
                        key={b.key}
                        bubble={b}
                        sender={sender}
                        convKey={chatId}
                        showName={merged && b.first}
                        onMenu={(x, y, msg) => setMenu({ x, y, msg })}
                      />
                    ))}
                  </div>
                );
              })}
            </section>
          ))}
        </div>
      </div>
      {showDown && (
        <button
          type="button"
          class="ScrollDown"
          aria-label="回到底部"
          onClick={() => ref.current?.scrollTo({ top: ref.current.scrollHeight, behavior: 'smooth' })}
        >
          <ArrowDown size={24} />
        </button>
      )}
      {menu && <ContextMenu x={menu.x} y={menu.y} items={menuItems(menu.msg)} onClose={() => setMenu(null)} />}
      {confirm && (
        <ConfirmDialog
          title="删除存档"
          text="将从存档中删除这条消息；没有其他消息引用的媒体文件会一并删除。此操作无法撤销。"
          confirmLabel="删除"
          danger
          busy={deleting}
          onConfirm={() => void doDelete()}
          onClose={() => setConfirm(null)}
        />
      )}
    </div>
  );
}
