import { Settings } from 'lucide-preact';
import { avatarUrl } from '../../api/client';
import type { Chat } from '../../api/types';
import { botName, formatListTime, previewText, senderName } from '../../lib/format';
import { navigate, route } from '../../lib/router';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { Spinner } from '../../ui/Spinner';
import { Tabs } from '../../ui/Tabs';
import './left.scss';

function BotTabs() {
  const store = useStore();
  const bots = store.bots.value.filter((b) => b.status !== 'removed' || store.chats.value.some((c) => c.bot_id === b.id));
  if (bots.length < 2) return null;
  const items = [
    { key: 0, label: '全部' },
    ...bots.map((b) => ({
      key: b.id,
      label: (
        <>
          <Avatar name={botName(b)} peerId={b.tg_bot_id} src={b.has_avatar ? avatarUrl('bots', b.tg_bot_id) : null} size="mini" />
          {botName(b)}
        </>
      ),
    })),
  ];
  return (
    <Tabs
      class="BotTabs"
      items={items}
      active={store.effectiveBotFilter.value}
      onChange={(k) => {
        store.botFilter.value = k;
      }}
    />
  );
}

function ChatItem({ chat, selected, showBot }: { chat: Chat; selected: boolean; showBot: boolean }) {
  const store = useStore();
  const bot = store.botsById.value.get(chat.bot_id);
  const name = senderName(chat.sender);
  return (
    <button
      type="button"
      class={`ChatItem${selected ? ' selected' : ''}`}
      aria-current={selected ? 'page' : undefined}
      onClick={() => navigate({ name: 'chat', chatId: chat.id })}
    >
      <Avatar
        name={name}
        peerId={chat.sender.tg_user_id}
        src={chat.sender.has_avatar ? avatarUrl('senders', chat.sender.tg_user_id) : null}
        size="large"
      />
      <span class="ChatItem-info">
        <span class="ChatItem-row">
          <span class="ChatItem-title">{name}</span>
          <span class="ChatItem-time">{formatListTime(chat.last_message_at)}</span>
        </span>
        <span class="ChatItem-subtitle">
          {showBot && bot && <span class="sender-name">{botName(bot)}: </span>}
          {previewText(chat.last_kind, chat.last_text)}
        </span>
      </span>
    </button>
  );
}

/** Left column default view: bot switcher, chat list (bot × sender), settings button. */
export function ChatsPanel() {
  const store = useStore();
  const r = route.value;
  const selectedId = r.name === 'chat' ? r.chatId : 0;
  const chats = store.visibleChats.value;
  const showBot = store.effectiveBotFilter.value === 0 && store.bots.value.length > 1;
  return (
    <div class="ChatsPanel">
      <div class="left-header">
        <h3 class="left-header-title">tgarchive</h3>
      </div>
      <BotTabs />
      <div class="chat-list custom-scroll">
        {!store.chatsLoaded.value && (
          <div class="chat-list-empty">
            <Spinner size={32} />
          </div>
        )}
        {store.chatsLoaded.value && chats.length === 0 && (
          <div class="chat-list-empty">
            <p class="chat-list-empty-title">暂无存档</p>
            <p>白名单用户发给机器人的消息会出现在这里</p>
          </div>
        )}
        {chats.map((c) => (
          <ChatItem key={c.id} chat={c} selected={c.id === selectedId} showBot={showBot} />
        ))}
      </div>
      <button type="button" class="FloatingActionButton" aria-label="管理" title="管理" onClick={() => navigate({ name: 'settings' })}>
        <Settings size={24} />
      </button>
    </div>
  );
}
