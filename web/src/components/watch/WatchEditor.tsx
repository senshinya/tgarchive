import { Trash2 } from 'lucide-preact';
import { useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { avatarUrl, errorMessage } from '../../api/client';
import type { ChannelInfo, ChannelRef, CondGroup, TestPost, Watch } from '../../api/types';
import { navigate } from '../../lib/router';
import { MAX_WINDOW, MIN_WINDOW, defaultCond, validateCond } from '../../lib/watchCond';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { Button } from '../../ui/Button';
import { InputField } from '../../ui/InputField';
import { ListItem } from '../../ui/ListItem';
import { Modal } from '../../ui/Modal';
import { Spinner } from '../../ui/Spinner';
import { Switch } from '../../ui/Switch';
import { Description, Section, SettingsShell } from '../settings/common';
import { ChannelPicker } from './ChannelPicker';
import { CondEditor, type Reactions } from './CondEditor';
import { TestPreview } from './TestPreview';
import './watch.scss';

const TEST_DELAY = 400;

/** Adding (no watchId: pick a channel first) or editing a channel watch. */
export function WatchEditor({ watchId }: { watchId?: number }) {
  const store = useStore();
  const [watch, setWatch] = useState<Watch | null>(null);
  const [channel, setChannel] = useState<ChannelRef | null>(null);
  const [loadError, setLoadError] = useState('');

  useEffect(() => {
    if (!watchId) return;
    store.api.watch(watchId).then(
      (w) => {
        setWatch(w);
        setChannel(w.channel);
      },
      (e) => setLoadError(errorMessage(e)),
    );
  }, [watchId]);

  const pick = async (c: ChannelInfo) => {
    if (c.watched) {
      try {
        const existing = (await store.api.watches()).find((w) => w.channel.channel_id === c.channel_id);
        if (existing) {
          navigate({ name: 'settings-watch', watchId: existing.id }, { replace: true });
          return;
        }
      } catch (e) {
        store.showToast(errorMessage(e));
        return;
      }
    }
    setChannel({ channel_id: c.channel_id, title: c.title, username: c.username, has_avatar: true });
  };

  const title = watchId ? '监听设置' : '添加监听';
  if (watchId && !watch) {
    return (
      <SettingsShell title={title} back={{ name: 'settings-watches' }}>
        {loadError ? (
          <Description>{loadError}</Description>
        ) : (
          <div class="settings-loading">
            <Spinner size={32} />
          </div>
        )}
      </SettingsShell>
    );
  }
  return (
    <SettingsShell title={title} back={{ name: 'settings-watches' }}>
      {channel ? (
        <WatchForm key={channel.channel_id} channel={channel} watch={watch} onChangeChannel={watch ? undefined : () => setChannel(null)} />
      ) : (
        <ChannelPicker onPick={(c) => void pick(c)} />
      )}
    </SettingsShell>
  );
}

function WatchForm({ channel, watch, onChangeChannel }: { channel: ChannelRef; watch: Watch | null; onChangeChannel?: () => void }) {
  const store = useStore();
  const [windowText, setWindowText] = useState(String(watch?.window_minutes ?? 30));
  const [enabled, setEnabled] = useState(watch?.enabled ?? true);
  const [cond, setCond] = useState<CondGroup>(watch?.cond ?? defaultCond());
  const [posts, setPosts] = useState<TestPost[] | null>(null);
  const [reactions, setReactions] = useState<Reactions>({ all: false, list: [] });
  const [testError, setTestError] = useState('');
  const [testing, setTesting] = useState(false);
  const [busy, setBusy] = useState(false);
  const [showErrors, setShowErrors] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const seeded = useRef(Boolean(watch));

  const bad = useMemo(() => new Set<string>(), [cond]);
  const condError = useMemo(() => validateCond(cond, bad), [cond, bad]);
  const windowValue = Number(windowText);
  const windowError = Number.isInteger(windowValue) && windowValue >= MIN_WINDOW && windowValue <= MAX_WINDOW ? '' : '观察窗口须为 1–1440 分钟';

  // Judge the latest posts whenever the condition changes (a broken condition only fetches them).
  useEffect(() => {
    let cancelled = false;
    const t = setTimeout(async () => {
      setTesting(true);
      try {
        const res = await store.api.testWatch(channel.channel_id, condError ? null : cond);
        if (cancelled) return;
        setPosts(res.posts);
        setReactions(res.reactions_available);
        setTestError('');
        // A new watch starts from the channel's first allowed reaction rather than a guess.
        if (!seeded.current && res.reactions_available.list.length > 0) {
          seeded.current = true;
          setCond(defaultCond(res.reactions_available.list[0].key));
        }
      } catch (e) {
        if (!cancelled) {
          setTestError(errorMessage(e));
          setPosts([]);
        }
      } finally {
        if (!cancelled) setTesting(false);
      }
    }, posts === null ? 0 : TEST_DELAY);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [cond, channel.channel_id]);

  const save = async () => {
    setShowErrors(true);
    if (condError || windowError) {
      store.showToast(windowError || condError || '');
      return;
    }
    setBusy(true);
    const input = { channel_id: channel.channel_id, window_minutes: windowValue, cond, enabled };
    try {
      if (watch) {
        await store.api.updateWatch(watch.id, input);
        store.showToast('已保存');
        await store.loadChats();
      } else {
        const created = await store.api.createWatch(input);
        await store.loadChats();
        navigate({ name: 'chat', chatId: created.chat_id }, { replace: true });
      }
    } catch (e) {
      store.showToast(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (purge: boolean) => {
    if (!watch) return;
    setBusy(true);
    try {
      await store.api.deleteWatch(watch.id, purge);
      await store.loadChats();
      navigate({ name: 'settings-watches' }, { replace: true });
    } catch (e) {
      store.showToast(errorMessage(e));
      setBusy(false);
    }
  };

  return (
    <>
      <Section>
        <div class="WatchChannel">
          <Avatar name={channel.title} peerId={channel.channel_id} src={channel.has_avatar ? avatarUrl('channels', channel.channel_id) : null} size="large" />
          <span class="ListItem-text">
            <span class="WatchChannel-title">{channel.title}</span>
            <span class="ListItem-subtitle">{channel.username ? `@${channel.username}` : '私有频道'}</span>
          </span>
          {onChangeChannel && (
            <button type="button" class="link-button" onClick={onChangeChannel}>
              更换
            </button>
          )}
        </div>
        {watch?.status === 'error' && <Description>出错：{watch.error}</Description>}
        <div class="WatchFields">
          <InputField label="观察窗口（分钟）" inputMode="numeric" value={windowText} onInput={setWindowText} error={showErrors ? windowError : ''} />
          <div class="WatchEnabled">
            <span>启用</span>
            <Switch checked={enabled} label="启用监听" onChange={setEnabled} />
          </div>
        </div>
        <Description>新帖发布后的 {windowError ? 'x' : windowValue} 分钟内，任一时刻满足条件即存档；到期未满足则放弃。</Description>
      </Section>
      <Section title="条件">
        <CondEditor value={cond} onChange={setCond} reactions={reactions} bad={showErrors ? bad : new Set()} />
        {showErrors && condError && <p class="settings-description error">{condError}</p>}
      </Section>
      <Section title="最近帖子试算">
        <TestPreview posts={posts} loading={testing} error={testError} judged={!condError} />
      </Section>
      <Section>
        <div class="WatchActions">
          <Button loading={busy} onClick={() => void save()}>
            {watch ? '保存' : '开始监听'}
          </Button>
        </div>
        {watch && <ListItem icon={<Trash2 size={24} />} title="删除监听" danger onClick={() => setConfirmDelete(true)} />}
      </Section>
      {confirmDelete && (
        <Modal title="删除监听" onClose={() => setConfirmDelete(false)}>
          <p class="Modal-text">停止监听「{channel.title}」。已存档的帖子可以保留，也可以一起删除。</p>
          <div class="Modal-actions vertical">
            <button type="button" class="Modal-action" disabled={busy} onClick={() => void remove(false)}>
              仅停止监听
            </button>
            <button type="button" class="Modal-action danger" disabled={busy} onClick={() => void remove(true)}>
              同时删除已存档的帖子
            </button>
            <button type="button" class="Modal-action" onClick={() => setConfirmDelete(false)}>
              取消
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}
