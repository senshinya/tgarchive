import { KeyRound, Plus, Smartphone } from 'lucide-preact';
import { useEffect, useState } from 'preact/hooks';
import { ApiError, avatarUrl, errorMessage } from '../../api/client';
import type { Bot, TelegramApp, UserbotInfo } from '../../api/types';
import { botName } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { ListItem } from '../../ui/ListItem';
import { Switch } from '../../ui/Switch';
import { BOT_STATUS, SERVER_STATE, Section, SettingsShell, StatusDot, USERBOT_STATE } from './common';

function BotRow({ bot }: { bot: Bot }) {
  const store = useStore();
  const [busy, setBusy] = useState(false);
  const toggle = async (enabled: boolean) => {
    setBusy(true);
    try {
      const updated = await store.api.setBotEnabled(bot.id, enabled);
      store.bots.value = store.bots.value.map((b) => (b.id === bot.id ? updated : b));
    } catch (e) {
      store.showToast(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div class="ListItem BotRow">
      <button type="button" class="BotRow-main" onClick={() => navigate({ name: 'settings-bot', botId: bot.id })}>
        <Avatar name={botName(bot)} peerId={bot.tg_bot_id} src={bot.has_avatar ? avatarUrl('bots', bot.tg_bot_id) : null} size="tiny" />
        <span class="ListItem-text">
          <span class="ListItem-title">{botName(bot)}</span>
          <span class={`ListItem-subtitle${bot.status === 'error' ? ' error' : ''}`}>
            <StatusDot status={bot.status} />
            {bot.status === 'error' && bot.last_error ? bot.last_error : `${BOT_STATUS[bot.status] ?? bot.status}${bot.username ? ` · @${bot.username}` : ''}`}
          </span>
        </span>
      </button>
      <Switch checked={bot.enabled} disabled={busy} label={`启用 ${botName(bot)}`} onChange={(v) => void toggle(v)} />
    </div>
  );
}

export function SettingsHome() {
  const store = useStore();
  const [app, setApp] = useState<TelegramApp | null>(null);
  const [userbot, setUserbot] = useState<UserbotInfo | null | 'unavailable'>(null);

  useEffect(() => {
    void store.loadBots();
    store.api.telegramApp().then(setApp, (e) => store.showToast(errorMessage(e)));
    store.api.userbot().then(setUserbot, (e) => {
      if (e instanceof ApiError && e.status === 404) setUserbot('unavailable');
      else store.showToast(errorMessage(e));
    });
  }, []);

  const bots = store.bots.value.filter((b) => b.status !== 'removed');
  let appSubtitle = '加载中…';
  if (app) {
    const server = app.server.managed ? `Bot API ${SERVER_STATE[app.server.state] ?? app.server.state}` : '外部 Bot API';
    appSubtitle = app.configured ? `api_id ${app.api_id} · ${server}` : '未配置';
  }
  let userbotSubtitle = '加载中…';
  if (userbot === 'unavailable') userbotSubtitle = '未启用';
  else if (userbot) userbotSubtitle = userbot.state === 'ready' && userbot.name ? `已登录 · ${userbot.name}` : USERBOT_STATE[userbot.state] ?? userbot.state;

  return (
    <SettingsShell title="管理" back={{ name: 'home' }}>
      <Section title="机器人">
        {bots.map((b) => (
          <BotRow key={b.id} bot={b} />
        ))}
        <ListItem icon={<Plus size={24} />} title="添加机器人" onClick={() => navigate({ name: 'settings-add-bot' })} />
      </Section>
      <Section title="Telegram">
        <ListItem
          icon={<KeyRound size={24} />}
          title="API 凭据"
          subtitle={appSubtitle}
          onClick={() => navigate({ name: 'settings-telegram-app' })}
        />
        <ListItem
          icon={<Smartphone size={24} />}
          title="用户账号"
          subtitle={userbotSubtitle}
          onClick={() => navigate({ name: 'settings-userbot' })}
        />
      </Section>
    </SettingsShell>
  );
}
