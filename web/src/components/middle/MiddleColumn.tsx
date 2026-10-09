import { ArrowLeft, Images, Search, SlidersHorizontal } from 'lucide-preact';
import type { Chat } from '../../api/types';
import { avatarUrl } from '../../api/client';
import { botName, senderName } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { IconButton } from '../../ui/Button';
import { ArticleReader } from '../article/ArticleReader';
import { CommentsView } from '../comments/CommentsView';
import { MessageList } from '../message/MessageList';
import { Wallpaper } from './Wallpaper';
import './middle.scss';

/** Where the left column overlays the chat (layout.scss). */
const NARROW = '(max-width: 925px)';

/** Avatar, title and subtitle of the conversation: a sender's chat, or a bot's merged timeline. */
function HeaderPeer({ chatId }: { chatId: number }) {
  const store = useStore();
  if (chatId < 0) {
    const bot = store.botsById.value.get(-chatId);
    const senders = store.chats.value.filter((c) => c.bot_id === -chatId).length;
    const name = bot ? botName(bot) : '机器人';
    return (
      <>
        {bot && (
          <Avatar name={name} peerId={bot.tg_bot_id} src={bot.has_avatar ? avatarUrl('bots', bot.tg_bot_id) : null} size="medium" />
        )}
        <span class="MiddleHeader-text">
          <span class="MiddleHeader-title">{name}</span>
          <span class="MiddleHeader-status">{senders} 位发送人</span>
        </span>
      </>
    );
  }
  const chat = store.chats.value.find((c) => c.id === chatId);
  if (chat?.kind === 'channel') return <ChannelPeer chat={chat} />;
  const bot = chat ? store.botsById.value.get(chat.bot_id) : undefined;
  const name = chat ? senderName(chat.sender) : '会话';
  return (
    <>
      {chat && (
        <Avatar
          name={name}
          peerId={chat.sender.tg_user_id}
          src={chat.sender.has_avatar ? avatarUrl('senders', chat.sender.tg_user_id) : null}
          size="medium"
        />
      )}
      <span class="MiddleHeader-text">
        <span class="MiddleHeader-title">{name}</span>
        {bot && <span class="MiddleHeader-status">机器人 {bot.username ? `@${bot.username}` : botName(bot)}</span>}
      </span>
    </>
  );
}

/** The status line of a watched channel's conversation. */
export function channelStatus(chat: Chat): string {
  const w = chat.watch;
  if (!w) return '未监听';
  if (!w.enabled) return '已停用';
  if (w.status === 'error') return `出错：${w.error}`;
  return `监听中 · 观察 ${w.pending} 条 · 窗口 ${w.window_minutes} 分钟`;
}

function ChannelPeer({ chat }: { chat: Chat }) {
  const ch = chat.channel;
  const name = ch?.title || '频道';
  const failing = chat.watch?.enabled && chat.watch.status === 'error';
  return (
    <>
      <Avatar name={name} peerId={ch?.channel_id ?? chat.id} src={ch?.has_avatar ? avatarUrl('channels', ch.channel_id) : null} size="medium" />
      <span class="MiddleHeader-text">
        <span class="MiddleHeader-title">{name}</span>
        <span class={`MiddleHeader-status${failing ? ' error' : ''}`}>{channelStatus(chat)}</span>
      </span>
    </>
  );
}

function MiddleHeader({ chatId }: { chatId: number }) {
  const store = useStore();
  const toggleShared = () => {
    store.sharedMediaOpen.value = !store.sharedMediaOpen.value;
  };
  const watchId = store.chats.value.find((c) => c.id === chatId)?.watch?.id ?? 0;
  // A chat opened from the list pushed a `{ fromList: true }` history entry (ChatsPanel), so
  // going back lands on whatever was there before (usually "/") without adding a fresh entry —
  // keeping Android's hardware back button in sync. A deep link (no such marker) has nothing to
  // go back to, so it navigates to home instead.
  const goBack = () => {
    const state = history.state as { fromList?: boolean } | null;
    if (state?.fromList) history.back();
    else navigate({ name: 'home' });
  };
  // The left column holds the search; on narrow screens it sits behind the chat, so go back to it.
  const search = () => {
    store.openSearch(chatId);
    if (typeof matchMedia === 'function' && matchMedia(NARROW).matches) goBack();
  };
  return (
    <div class="MiddleHeader">
      <IconButton label="返回" class="back-button" onClick={goBack}>
        <ArrowLeft size={24} />
      </IconButton>
      <button type="button" class="MiddleHeader-info" onClick={toggleShared}>
        <HeaderPeer chatId={chatId} />
      </button>
      <IconButton label="搜索此会话" onClick={search}>
        <Search size={22} />
      </IconButton>
      {watchId > 0 && (
        <IconButton label="监听设置" onClick={() => navigate({ name: 'settings-watch', watchId })}>
          <SlidersHorizontal size={22} />
        </IconButton>
      )}
      <IconButton label="共享媒体" onClick={toggleShared}>
        <Images size={24} />
      </IconButton>
    </div>
  );
}

/** The conversation (a chat id, or -botId for a bot's merged timeline); with articleId, the
 * article reader on top of it; with commentsId, that archived post's comments instead. */
export function MiddleColumn({ chatId, articleId = 0, commentsId = 0 }: { chatId: number; articleId?: number; commentsId?: number }) {
  if (!chatId) {
    return (
      <div id="MiddleColumn" class="empty">
        <Wallpaper />
        <div class="empty-hint">
          <span>选择一个会话开始浏览存档</span>
        </div>
      </div>
    );
  }
  if (commentsId) {
    return (
      <div id="MiddleColumn">
        <Wallpaper />
        <CommentsView key={commentsId} chatId={chatId} postId={commentsId} />
      </div>
    );
  }
  return (
    <div id="MiddleColumn">
      <Wallpaper />
      <MiddleHeader chatId={chatId} />
      <MessageList key={chatId} chatId={chatId} />
      {articleId > 0 && <ArticleReader key={articleId} chatId={chatId} messageId={articleId} />}
    </div>
  );
}
