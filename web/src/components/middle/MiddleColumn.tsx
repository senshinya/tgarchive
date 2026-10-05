import { ArrowLeft, Images } from 'lucide-preact';
import { avatarUrl } from '../../api/client';
import { botName, senderName } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { IconButton } from '../../ui/Button';
import { ArticleReader } from '../article/ArticleReader';
import { MessageList } from '../message/MessageList';
import './middle.scss';

function MiddleHeader({ chatId }: { chatId: number }) {
  const store = useStore();
  const chat = store.chats.value.find((c) => c.id === chatId);
  const bot = chat ? store.botsById.value.get(chat.bot_id) : undefined;
  const name = chat ? senderName(chat.sender) : '会话';
  const toggleShared = () => {
    store.sharedMediaOpen.value = !store.sharedMediaOpen.value;
  };
  // A chat opened from the list pushed a `{ fromList: true }` history entry (ChatsPanel), so
  // going back lands on whatever was there before (usually "/") without adding a fresh entry —
  // keeping Android's hardware back button in sync. A deep link (no such marker) has nothing to
  // go back to, so it navigates to home instead.
  const goBack = () => {
    const state = history.state as { fromList?: boolean } | null;
    if (state?.fromList) history.back();
    else navigate({ name: 'home' });
  };
  return (
    <div class="MiddleHeader">
      <IconButton label="返回" class="back-button" onClick={goBack}>
        <ArrowLeft size={24} />
      </IconButton>
      <button type="button" class="MiddleHeader-info" onClick={toggleShared}>
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
      </button>
      <IconButton label="共享媒体" onClick={toggleShared}>
        <Images size={24} />
      </IconButton>
    </div>
  );
}

/** The chat; with articleId, the article reader on top of it. */
export function MiddleColumn({ chatId, articleId = 0 }: { chatId: number; articleId?: number }) {
  if (!chatId) {
    return (
      <div id="MiddleColumn" class="empty">
        <div class="Wallpaper" aria-hidden="true" />
        <div class="empty-hint">
          <span>选择一个会话开始浏览存档</span>
        </div>
      </div>
    );
  }
  return (
    <div id="MiddleColumn">
      <div class="Wallpaper" aria-hidden="true" />
      <MiddleHeader chatId={chatId} />
      <MessageList key={chatId} chatId={chatId} />
      {articleId > 0 && <ArticleReader key={articleId} chatId={chatId} messageId={articleId} />}
    </div>
  );
}
