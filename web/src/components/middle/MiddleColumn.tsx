import { ArrowLeft, Images } from 'lucide-preact';
import { avatarUrl } from '../../api/client';
import { botName, senderName } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { IconButton } from '../../ui/Button';
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
  return (
    <div class="MiddleHeader">
      <IconButton label="返回" class="back-button" onClick={() => navigate({ name: 'home' })}>
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

export function MiddleColumn({ chatId }: { chatId: number }) {
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
    </div>
  );
}
