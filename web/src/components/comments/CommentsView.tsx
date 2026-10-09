import { ArrowDown, ArrowLeft, Copy, Download } from 'lucide-preact';
import { Fragment } from 'preact';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'preact/hooks';
import { errorMessage, mediaUrl } from '../../api/client';
import type { ArchiveEvent, Message } from '../../api/types';
import { commenterOf, groupMessages, type ListEntry } from '../../lib/grouping';
import { navigate } from '../../lib/router';
import { mergeById, useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { IconButton } from '../../ui/Button';
import { ContextMenu, type MenuItem } from '../../ui/ContextMenu';
import { Spinner } from '../../ui/Spinner';
import { mainMedia } from '../media/util';
import { senderInfo } from '../message/MessageList';
import { MessageBubble, type SenderInfo } from '../message/MessageBubble';
import { toViewerItems } from '../viewer/MediaViewer';
import { commenterAvatar, commentsLabel } from './CommentButton';
import '../message/message.scss';
import './comments.scss';

/** Comments loaded per page. */
export const COMMENTS_PAGE = 50;
const LOAD_MORE_PX = 400;
const AT_BOTTOM_PX = 100;
const SHOW_DOWN_PX = 300;

function download(href: string) {
  const a = document.createElement('a');
  a.href = href;
  a.download = '';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

/** An archived channel post's comments, Web A's comment thread: the post on top, "讨论开始", then
 * the comments oldest first as a group chat (avatars, names, reply quotes, reactions). */
export function CommentsView({ chatId, postId }: { chatId: number; postId: number }) {
  const store = useStore();
  const [post, setPost] = useState<Message[]>([]);
  const [items, setItems] = useState<Message[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [hasNewer, setHasNewer] = useState(false);
  const [error, setError] = useState('');
  const [showDown, setShowDown] = useState(false);
  const [menu, setMenu] = useState<{ x: number; y: number; msg: Message } | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const state = useRef({ items, hasNewer, loading, atBottom: false });
  state.current = { ...state.current, items, hasNewer, loading };

  const chat = store.chats.value.find((c) => c.id === chatId);
  const channel = senderInfo(chat, chatId);
  const head = post[0];
  const count = head?.stats?.replies ?? items.length;

  const loadPost = async () => {
    // The post and the rest of its album.
    const around = await store.api.messages(chatId, 0, 20, { around: postId });
    const self = around.find((m) => m.id === postId);
    if (!self) throw new Error('帖子已不在存档中');
    setPost(around.filter((m) => m.id === postId || (self.media_group_id !== '' && m.media_group_id === self.media_group_id)));
  };

  /** Comments after the newest loaded one (from the start when none are). */
  const loadNewer = async (reset = false) => {
    if (state.current.loading) return;
    setLoading(true);
    setError('');
    try {
      const have = reset ? [] : state.current.items;
      const after = have.length ? have[have.length - 1].id : postId;
      const page = await store.api.comments(chatId, postId, { after }, COMMENTS_PAGE);
      setItems(reset ? page : mergeById(state.current.items, page));
      setHasNewer(page.length >= COMMENTS_PAGE);
      setLoaded(true);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setLoading(false);
    }
  };

  const reload = async () => {
    try {
      await loadPost();
      await loadNewer(true);
    } catch (e) {
      setError(errorMessage(e));
      setLoaded(true);
    }
  };

  useEffect(() => {
    void reload();
    // New comments arrive as comments.updated.
    void store.api.refreshComments(chatId, postId).catch(() => undefined);
  }, [chatId, postId]);

  useEffect(
    () =>
      store.onEvent((ev: ArchiveEvent) => {
        switch (ev.type) {
          case 'comments.updated':
            if (ev.data.root_id !== postId) return;
            // Re-read what is loaded (edits, reactions) and append what is new, unless a page
            // further on is still unloaded (scrolling there loads it).
            if (!state.current.hasNewer) void loadNewer();
            return;
          case 'message.created':
          case 'message.updated':
            if (ev.data.chat_id === chatId && ev.data.message_id === postId) void loadPost().catch(() => undefined);
            return;
          case 'media.updated':
            for (const id of ev.data.message_ids ?? []) {
              if (state.current.items.some((m) => m.id === id)) {
                void store.api.message(id).then(
                  (m) => setItems((cur) => mergeById(cur, [m])),
                  () => undefined,
                );
              } else if (id === postId || post.some((m) => m.id === id)) {
                void loadPost().catch(() => undefined);
              }
            }
            return;
          case 'resync':
            void reload();
            return;
        }
      }),
    [chatId, postId, post],
  );

  // The view opens at the top (the post); new comments are followed only from the bottom.
  const lastCount = useRef(0);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (lastCount.current > 0 && items.length > lastCount.current && state.current.atBottom && !hasNewer) el.scrollTop = el.scrollHeight;
    lastCount.current = items.length;
    if (loaded && hasNewer && el.scrollHeight - el.clientHeight < LOAD_MORE_PX) void loadNewer();
  }, [items]);

  const onScroll = () => {
    const el = ref.current;
    if (!el) return;
    const below = el.scrollHeight - el.scrollTop - el.clientHeight;
    state.current.atBottom = below < AT_BOTTOM_PX;
    setShowDown(below > SHOW_DOWN_PX);
    if (below < LOAD_MORE_PX && state.current.hasNewer) void loadNewer();
  };

  const all = useMemo(() => [...post, ...items], [post, items]);
  const entries = useMemo(() => groupMessages(all), [all]);
  const dayGroups = useMemo(() => {
    const out: { key: string; label: string; groups: Extract<ListEntry, { kind: 'group' }>[] }[] = [];
    for (const e of entries) {
      if (e.kind === 'date') out.push({ key: e.key, label: e.label, groups: [] });
      else out[out.length - 1]?.groups.push(e);
    }
    return out;
  }, [entries]);

  const openMedia = (m: Message) => {
    const md = mainMedia(m);
    if (!md || md.state !== 'done') return;
    store.viewer.value = { list: toViewerItems(all), mediaId: md.id, title: commentsLabel(count) };
  };

  const goBack = () => {
    const st = history.state as { fromChat?: boolean } | null;
    if (st?.fromChat) history.back();
    else navigate({ name: 'chat', chatId });
  };

  const menuItems = (msg: Message): MenuItem[] => {
    const out: MenuItem[] = [];
    const caption = msg.text || (msg.media_group_id ? all.find((m) => m.media_group_id === msg.media_group_id && m.text)?.text : '');
    if (caption) {
      out.push({
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
    if (md && md.state === 'done') out.push({ label: '下载', icon: <Download size={20} />, onSelect: () => download(mediaUrl(md.id, true)) });
    return out;
  };
  const onMenu = (x: number, y: number, msg: Message) => {
    if (menuItems(msg).length) setMenu({ x, y, msg });
  };

  const toBottom = () => ref.current?.scrollTo({ top: ref.current.scrollHeight, behavior: 'smooth' });

  return (
    <>
      <div class="MiddleHeader CommentsHeader">
        <IconButton label="返回" class="back-button always" onClick={goBack}>
          <ArrowLeft size={24} />
        </IconButton>
        <div class="MiddleHeader-info">
          <span class="MiddleHeader-text">
            <span class="MiddleHeader-title">{loaded || head ? (count > 0 ? commentsLabel(count) : '评论') : '加载中…'}</span>
          </span>
        </div>
      </div>
      <div class="MessageList-wrapper">
        <div class="MessageList custom-scroll" ref={ref} onScroll={onScroll}>
          <div class="messages-container">
            {error && (
              <div class="history-notice">
                <span>{error}</span>
                <button type="button" onClick={() => void (head ? loadNewer() : reload())}>
                  重试
                </button>
              </div>
            )}
            {!loaded && !error && (
              <div class="history-notice">
                <Spinner size={32} />
              </div>
            )}
            {dayGroups.map((d) => (
              <section class="message-date-group" key={d.key}>
                <div class="sticky-date">
                  <span>{d.label}</span>
                </div>
                {d.groups.map((g) => {
                  const first = g.bubbles[0];
                  const m = first.kind === 'album' ? first.msgs[0] : first.msg;
                  const top = m.source === 'channel_watch';
                  const from = commenterOf(m);
                  const sender: SenderInfo = from ? { name: from.name, peerId: from.id } : channel;
                  return (
                    <Fragment key={g.key}>
                      <div class={`message-group${top ? ' comment-thread-top' : ' with-avatar'}`}>
                        {!top && (
                          <div class="message-group-avatar">
                            <Avatar name={sender.name} peerId={sender.peerId} src={from ? commenterAvatar(from) : null} size="small" />
                          </div>
                        )}
                        {g.bubbles.map((b) => (
                          <MessageBubble
                            key={b.key}
                            bubble={b}
                            sender={sender}
                            convKey={chatId}
                            showName={!top && b.first}
                            inThread
                            onOpenMedia={openMedia}
                            onMenu={onMenu}
                          />
                        ))}
                      </div>
                      {top && (
                        <div class="local-action-message">
                          <span>{loaded && items.length === 0 ? '暂无评论' : '讨论开始'}</span>
                        </div>
                      )}
                    </Fragment>
                  );
                })}
              </section>
            ))}
            {loading && loaded && (
              <div class="history-loading">
                <Spinner size={28} />
              </div>
            )}
          </div>
        </div>
        {showDown && (
          <button type="button" class="ScrollDown" aria-label="回到底部" onClick={toBottom}>
            <ArrowDown size={24} />
          </button>
        )}
        {menu && <ContextMenu x={menu.x} y={menu.y} items={menuItems(menu.msg)} onClose={() => setMenu(null)} />}
      </div>
    </>
  );
}
