import { Settings } from 'lucide-preact';
import { avatarUrl } from '../../api/client';
import type { Chat } from '../../api/types';
import { botName, formatListTime, previewText, senderName } from '../../lib/format';
import { navigate, route, routeConvKey } from '../../lib/router';
import { useStore, type BotEntry, type ListMode } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { Spinner } from '../../ui/Spinner';
import { Tabs } from '../../ui/Tabs';
import { DownloadsButton } from '../downloads/DownloadsPanel';
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
      onClick={() => navigate({ name: 'chat', chatId: chat.id }, { fromList: true })}
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

function BotItem({ entry, selected }: { entry: BotEntry; selected: boolean }) {
  const { bot, last } = entry;
  const name = botName(bot);
  return (
    <button
      type="button"
      class={`ChatItem${selected ? ' selected' : ''}`}
      aria-current={selected ? 'page' : undefined}
      onClick={() => navigate({ name: 'bot', botId: bot.id }, { fromList: true })}
    >
      <Avatar name={name} peerId={bot.tg_bot_id} src={bot.has_avatar ? avatarUrl('bots', bot.tg_bot_id) : null} size="large" />
      <span class="ChatItem-info">
        <span class="ChatItem-row">
          <span class="ChatItem-title">{name}</span>
          {last && <span class="ChatItem-time">{formatListTime(last.last_message_at)}</span>}
        </span>
        <span class="ChatItem-subtitle">
          {last ? (
            <>
              <span class="sender-name">{senderName(last.sender)}: </span>
              {previewText(last.last_kind, last.last_text)}
            </>
          ) : (
            '暂无消息'
          )}
        </span>
      </span>
    </button>
  );
}

const MODES: { key: ListMode; label: string }[] = [
  { key: 'people', label: '按人' },
  { key: 'bot', label: '按 bot' },
];

function ListModeSwitch() {
  const store = useStore();
  return (
    <div class="ListModeSwitch" role="group" aria-label="会话列表分组">
      {MODES.map((m) => (
        <button
          key={m.key}
          type="button"
          aria-pressed={store.listMode.value === m.key}
          class={store.listMode.value === m.key ? 'active' : ''}
          onClick={() => store.setListMode(m.key)}
        >
          {m.label}
        </button>
      ))}
    </div>
  );
}

/** Left column default view: list-mode switch, then either the bot switcher and chat list
 * (bot × sender), or one merged timeline per bot; settings button. */
export function ChatsPanel() {
  const store = useStore();
  const r = route.value;
  const selectedKey = routeConvKey(r);
  const byBot = store.listMode.value === 'bot';
  const chats = store.visibleChats.value;
  const entries = store.botEntries.value;
  const showBot = store.effectiveBotFilter.value === 0 && store.bots.value.length > 1;
  const empty = byBot ? entries.length === 0 : chats.length === 0;
  return (
    <div class="ChatsPanel">
      <div class="left-header">
        <h3 class="left-header-title">tgarchive</h3>
        <DownloadsButton />
        <ListModeSwitch />
      </div>
      {!byBot && <BotTabs />}
      <div class="chat-list custom-scroll">
        {!store.chatsLoaded.value && (
          <div class="chat-list-empty">
            <Spinner size={32} />
          </div>
        )}
        {store.chatsLoaded.value && empty && (
          <div class="chat-list-empty">
            <p class="chat-list-empty-title">暂无存档</p>
            <p>白名单用户发给机器人的消息会出现在这里</p>
          </div>
        )}
        {byBot
          ? entries.map((e) => <BotItem key={e.bot.id} entry={e} selected={-e.bot.id === selectedKey} />)
          : chats.map((c) => <ChatItem key={c.id} chat={c} selected={c.id === selectedKey} showBot={showBot} />)}
      </div>
      <button type="button" class="FloatingActionButton" aria-label="管理" title="管理" onClick={() => navigate({ name: 'settings' })}>
        <Settings size={24} />
      </button>
    </div>
  );
}
