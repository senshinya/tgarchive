import { Plus } from 'lucide-preact';
import { useEffect, useState } from 'preact/hooks';
import { avatarUrl, errorMessage } from '../../api/client';
import type { UserbotInfo, Watch } from '../../api/types';
import { formatAgo } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { Button } from '../../ui/Button';
import { InputField } from '../../ui/InputField';
import { ListItem } from '../../ui/ListItem';
import { Spinner } from '../../ui/Spinner';
import { Switch } from '../../ui/Switch';
import { Description, Section, SettingsShell, StatusDot } from '../settings/common';
import './watch.scss';

export function watchStatusText(w: Pick<Watch, 'enabled' | 'status' | 'error' | 'pending' | 'hits' | 'window_minutes'>): string {
  if (!w.enabled) return `已停用 · 已存 ${w.hits}`;
  if (w.status === 'error') return `出错：${w.error}`;
  return `观察中 ${w.pending} · 已存 ${w.hits} · 窗口 ${w.window_minutes} 分钟`;
}

/** Polls this many intervals overdue mark a watch as stalled. */
const STALL_ROUNDS = 3;
const RELOAD_MS = 30_000;

/** An enabled watch whose poller has not finished a round for a while, counting from its last
 * change at the earliest (a watch just created or re-enabled has had no chance to poll). */
export function watchStalled(w: Watch, now: number = Date.now() / 1000): boolean {
  if (!w.enabled || w.status === 'error') return false;
  return now - Math.max(w.last_polled_at, w.updated_at, w.created_at) > STALL_ROUNDS * w.poll_seconds;
}

function WatchRow({ w, onToggle }: { w: Watch; onToggle: (enabled: boolean) => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const stalled = watchStalled(w);
  const polled = w.last_polled_at ? `${formatAgo(w.last_polled_at)}轮询` : '尚未轮询';
  const hits = w.last_hit_at ? `24h 命中 ${w.hits_24h} · 7d 命中 ${w.hits_7d} · 最近 ${formatAgo(w.last_hit_at)}` : '尚无命中';
  return (
    <div class="ListItem BotRow">
      <button type="button" class="BotRow-main" onClick={() => navigate({ name: 'settings-watch', watchId: w.id })}>
        <Avatar name={w.channel.title} peerId={w.channel.channel_id} src={w.channel.has_avatar ? avatarUrl('channels', w.channel.channel_id) : null} size="tiny" />
        <span class="ListItem-text">
          <span class="ListItem-title">{w.channel.title}</span>
          <span class={`ListItem-subtitle${w.enabled && w.status === 'error' ? ' error' : stalled ? ' warning' : ''}`}>
            <StatusDot status={!w.enabled ? 'stopped' : w.status === 'error' ? 'error' : stalled ? 'warning' : 'running'} />
            {stalled ? (
              <span>轮询停滞 · {polled}</span>
            ) : (
              <>
                <span>{watchStatusText(w)}</span>
                {w.enabled && <span> · {polled}</span>}
              </>
            )}
          </span>
          <span class="ListItem-subtitle WatchRow-hits">{hits}</span>
        </span>
      </button>
      <Switch
        checked={w.enabled}
        disabled={busy}
        label={`启用 ${w.channel.title}`}
        onChange={async (v) => {
          setBusy(true);
          await onToggle(v);
          setBusy(false);
        }}
      />
    </div>
  );
}

/** Settings › 频道监听: every watch, and the poll interval. */
export function WatchList() {
  const store = useStore();
  const [watches, setWatches] = useState<Watch[] | null>(null);
  const [userbot, setUserbot] = useState<UserbotInfo | null>(null);
  const [poll, setPoll] = useState('');
  const [pollError, setPollError] = useState('');
  const [saving, setSaving] = useState(false);

  const reload = async () => {
    try {
      setWatches(await store.api.watches());
    } catch (e) {
      store.showToast(errorMessage(e));
      setWatches([]);
    }
  };
  useEffect(() => {
    void reload();
    store.api.watchSettings().then((s) => setPoll(String(s.poll_seconds)), (e) => store.showToast(errorMessage(e)));
    store.api.userbot().then(setUserbot, () => setUserbot(null));
    // Keeps "N 分钟前轮询" and the stalled check current; the poller does not announce rounds.
    const timer = setInterval(() => void reload(), RELOAD_MS);
    const off = store.onEvent((ev) => {
      if (ev.type === 'watch.updated' || ev.type === 'resync') void reload();
    });
    return () => {
      clearInterval(timer);
      off();
    };
  }, []);

  const toggle = async (w: Watch, enabled: boolean) => {
    if (!w.cond) return;
    try {
      const updated = await store.api.updateWatch(w.id, { channel_id: w.channel.channel_id, window_minutes: w.window_minutes, cond: w.cond, enabled });
      setWatches((ws) => ws?.map((x) => (x.id === w.id ? updated : x)) ?? null);
      await store.loadChats();
    } catch (e) {
      store.showToast(errorMessage(e));
    }
  };

  const savePoll = async () => {
    const n = Number(poll);
    if (!Number.isInteger(n) || n < 30 || n > 600) {
      setPollError('轮询间隔须为 30–600 秒');
      return;
    }
    setPollError('');
    setSaving(true);
    try {
      await store.api.saveWatchSettings(n);
      store.showToast('已保存');
    } catch (e) {
      store.showToast(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <SettingsShell title="频道监听" back={{ name: 'settings' }}>
      {userbot && userbot.state !== 'ready' && (
        <Section>
          <Description>代取账号未登录，监听已暂停。频道监听通过代取账号读取帖子与 reaction。</Description>
          <ListItem title="去登录代取账号" onClick={() => navigate({ name: 'settings-userbot' })} />
        </Section>
      )}
      <Section title="监听">
        {watches === null && (
          <div class="settings-loading">
            <Spinner size={32} />
          </div>
        )}
        {watches?.map((w) => (
          <WatchRow key={w.id} w={w} onToggle={(v) => toggle(w, v)} />
        ))}
        <ListItem icon={<Plus size={24} />} title="添加监听" onClick={() => navigate({ name: 'settings-watch-new' })} />
      </Section>
      <Section title="轮询">
        <Description>每隔多少秒检查一次所有频道的新帖与 reaction。每个频道每轮约 3 次请求。</Description>
        <div class="settings-form">
          <InputField label="轮询间隔（秒，30–600）" inputMode="numeric" value={poll} onInput={setPoll} error={pollError} />
          <Button loading={saving} onClick={() => void savePoll()}>
            保存
          </Button>
        </div>
      </Section>
    </SettingsShell>
  );
}
