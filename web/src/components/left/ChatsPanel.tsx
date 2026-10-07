import { CircleAlert, Megaphone, Radio, Settings, Volume2, VolumeX } from 'lucide-preact';
import { avatarUrl } from '../../api/client';
import type { Chat } from '../../api/types';
import { botName, formatListTime, previewText, senderName } from '../../lib/format';
import { navigate, route, routeConvKey } from '../../lib/router';
import { CHANNELS_FILTER, useStore, type BotEntry, type ListMode } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { setSilent, silent } from '../../lib/silent';
import { IconButton } from '../../ui/Button';
import { Spinner } from '../../ui/Spinner';
import { Tabs } from '../../ui/Tabs';
import { DownloadsButton } from '../downloads/DownloadsPanel';
import { SearchBox } from './SearchBox';
import { SearchResults } from './SearchResults';
import './left.scss';

function BotTabs() {
  const store = useStore();
  const bots = store.bots.value.filter(
    (b) => b.status !== 'removed' || store.chats.value.some((c) => c.kind !== 'channel' && c.bot_id === b.id),
  );
  const channels = store.channelChats.value.length > 0;
  if (bots.length < 2 && !(channels && bots.length > 0)) return null;
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
    ...(channels
      ? [
          {
            key: CHANNELS_FILTER,
            label: (
              <>
                <Megaphone size={16} class="BotTabs-icon" />
                频道
              </>
            ),
          },
        ]
      : []),
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

/** A watched channel's conversation; it never merges into a bot. */
function ChannelItem({ chat, selected }: { chat: Chat; selected: boolean }) {
  const ch = chat.channel;
  const name = ch?.title || '频道';
  const failing = chat.watch?.enabled && chat.watch.status === 'error';
  return (
    <button
      type="button"
      class={`ChatItem ChannelItem${selected ? ' selected' : ''}`}
      aria-current={selected ? 'page' : undefined}
      onClick={() => navigate({ name: 'chat', chatId: chat.id }, { fromList: true })}
    >
      <Avatar name={name} peerId={ch?.channel_id ?? chat.id} src={ch?.has_avatar ? avatarUrl('channels', ch.channel_id) : null} size="large" />
      <span class="ChatItem-info">
        <span class="ChatItem-row">
          <span class="ChatItem-title">
            <Megaphone size={16} class="ChatItem-channel-icon" aria-label="频道" />
            {name}
          </span>
          {failing ? (
            <span class="ChatItem-error" title={chat.watch?.error}>
              <CircleAlert size={18} />
            </span>
          ) : (
            <span class="ChatItem-time">{formatListTime(chat.last_message_at)}</span>
          )}
        </span>
        <span class="ChatItem-subtitle">{chat.last_kind ? previewText(chat.last_kind, chat.last_text) : '暂无存档'}</span>
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
  const rows = store.botModeRows.value;
  const showBot = store.effectiveBotFilter.value === 0 && store.bots.value.length > 1;
  const empty = byBot ? rows.length === 0 : chats.length === 0;
  const searching = store.searchQuery.value.trim() !== '';
  return (
    <div class="ChatsPanel">
      <div class="left-header">
        <h3 class="left-header-title">tgarchive</h3>
        <DownloadsButton />
        <IconButton label="监听频道" class="watch-button" onClick={() => navigate({ name: 'settings-watch-new' })}>
          <Radio size={22} />
        </IconButton>
        <SilentButton />
        <ListModeSwitch />
      </div>
      <SearchBox />
      {searching ? (
        <SearchResults />
      ) : (
        <>
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
                <p>白名单用户发给机器人的消息、监听频道存档的帖子会出现在这里</p>
              </div>
            )}
            {byBot
              ? rows.map((r) =>
                  r.kind === 'bot' ? (
                    <BotItem key={`b${r.entry.bot.id}`} entry={r.entry} selected={-r.entry.bot.id === selectedKey} />
                  ) : (
                    <ChannelItem key={`c${r.chat.id}`} chat={r.chat} selected={r.chat.id === selectedKey} />
                  ),
                )
              : chats.map((c) =>
                  c.kind === 'channel' ? (
                    <ChannelItem key={c.id} chat={c} selected={c.id === selectedKey} />
                  ) : (
                    <ChatItem key={c.id} chat={c} selected={c.id === selectedKey} showBot={showBot} />
                  ),
                )}
          </div>
        </>
      )}
      <button type="button" class="FloatingActionButton" aria-label="管理" title="管理" onClick={() => navigate({ name: 'settings' })}>
        <Settings size={24} />
      </button>
    </div>
  );
}

/** 静音模式 switch: while on, the WebUI plays nothing with sound (see lib/silent). */
function SilentButton() {
  const store = useStore();
  const on = silent.value;
  return (
    <IconButton
      label={on ? '关闭静音模式' : '开启静音模式'}
      class={`silent-button${on ? ' active' : ''}`}
      pressed={on}
      onClick={() => {
        setSilent(!on);
        store.showToast(on ? '已关闭静音模式' : '已开启静音模式：所有视频与语音静音播放');
      }}
    >
      {on ? <VolumeX size={22} /> : <Volume2 size={22} />}
    </IconButton>
  );
}
