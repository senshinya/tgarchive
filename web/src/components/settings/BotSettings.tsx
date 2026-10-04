import { Pencil, Trash2, UserPlus } from 'lucide-preact';
import { useEffect, useState } from 'preact/hooks';
import { avatarUrl, errorMessage } from '../../api/client';
import type { RejectedSender, WhitelistEntry } from '../../api/types';
import { botName, formatListTime } from '../../lib/format';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { Button, IconButton } from '../../ui/Button';
import { Checkbox } from '../../ui/Checkbox';
import { InputField } from '../../ui/InputField';
import { ListItem } from '../../ui/ListItem';
import { ConfirmDialog, Modal } from '../../ui/Modal';
import { Switch } from '../../ui/Switch';
import { BOT_STATUS, Description, Section, SettingsShell, StatusDot } from './common';

const UID_RE = /^[1-9][0-9]{0,15}$/;

function EditEntry({ entry, onSave, onClose }: { entry: WhitelistEntry; onSave: (note: string, canFetch: boolean) => Promise<void>; onClose: () => void }) {
  const [note, setNote] = useState(entry.note);
  const [canFetch, setCanFetch] = useState(entry.can_fetch);
  const [busy, setBusy] = useState(false);
  return (
    <Modal title={`编辑 ${entry.tg_user_id}`} onClose={onClose}>
      <InputField label="备注" value={note} onInput={setNote} />
      <Checkbox label="允许代取受保护内容" checked={canFetch} onChange={setCanFetch} />
      <div class="Modal-actions">
        <button type="button" class="Modal-action" onClick={onClose}>
          取消
        </button>
        <button
          type="button"
          class="Modal-action"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            await onSave(note.trim(), canFetch);
            setBusy(false);
          }}
        >
          保存
        </button>
      </div>
    </Modal>
  );
}

export function BotSettings({ botId }: { botId: number }) {
  const store = useStore();
  const bot = store.bots.value.find((b) => b.id === botId);
  const [whitelist, setWhitelist] = useState<WhitelistEntry[]>([]);
  const [rejected, setRejected] = useState<RejectedSender[]>([]);
  const [uid, setUid] = useState('');
  const [note, setNote] = useState('');
  const [canFetch, setCanFetch] = useState(false);
  const [formError, setFormError] = useState('');
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState<WhitelistEntry | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [purge, setPurge] = useState(false);
  const [checked, setChecked] = useState(false);

  const reload = async () => {
    try {
      const [w, r] = await Promise.all([store.api.whitelist(botId), store.api.rejected(botId)]);
      setWhitelist(w);
      setRejected(r);
    } catch (e) {
      store.showToast(errorMessage(e));
    }
  };

  useEffect(() => {
    void store.loadBots().then(() => setChecked(true));
    void reload();
  }, [botId]);

  const put = async (id: number, n: string, fetch: boolean) => {
    try {
      await store.api.putWhitelist(botId, id, n, fetch);
      await reload();
      return true;
    } catch (e) {
      store.showToast(errorMessage(e));
      return false;
    }
  };

  const add = async (e: Event) => {
    e.preventDefault();
    const v = uid.trim();
    if (!UID_RE.test(v) || !Number.isSafeInteger(Number(v))) {
      setFormError('请输入正确的用户 ID');
      return;
    }
    setFormError('');
    setBusy(true);
    if (await put(Number(v), note.trim(), canFetch)) {
      setUid('');
      setNote('');
      setCanFetch(false);
    }
    setBusy(false);
  };

  const toggleEnabled = async (enabled: boolean) => {
    try {
      const updated = await store.api.setBotEnabled(botId, enabled);
      store.bots.value = store.bots.value.map((b) => (b.id === botId ? updated : b));
    } catch (e) {
      store.showToast(errorMessage(e));
    }
  };

  const remove = async () => {
    setBusy(true);
    try {
      await store.api.deleteBot(botId, purge);
      await Promise.all([store.loadBots(), store.loadChats()]);
      navigate({ name: 'settings' }, true);
    } catch (e) {
      store.showToast(errorMessage(e));
      setBusy(false);
    }
  };

  if (!bot) {
    return (
      <SettingsShell title="机器人" back={{ name: 'settings' }}>
        <Section>
          <Description>{checked ? '机器人不存在' : '加载中…'}</Description>
        </Section>
      </SettingsShell>
    );
  }

  const listed = new Set(whitelist.map((w) => w.tg_user_id));

  return (
    <SettingsShell title={botName(bot)} back={{ name: 'settings' }}>
      <Section>
        <div class="BotProfile">
          <Avatar name={botName(bot)} peerId={bot.tg_bot_id} src={bot.has_avatar ? avatarUrl('bots', bot.tg_bot_id) : null} size="jumbo" />
          <div class="BotProfile-name">{botName(bot)}</div>
          <div class="BotProfile-status">
            <StatusDot status={bot.status} />
            {BOT_STATUS[bot.status] ?? bot.status}
            {bot.username && ` · @${bot.username}`}
          </div>
          {bot.status === 'error' && bot.last_error && <div class="BotProfile-error">{bot.last_error}</div>}
        </div>
        <ListItem
          title="接收消息"
          subtitle="关闭后停止拉取新消息，不会从 Telegram 登出"
          right={<Switch checked={bot.enabled} label="接收消息" onChange={(v) => void toggleEnabled(v)} />}
        />
      </Section>

      <Section title="白名单">
        <Description>只有白名单中的用户发给此机器人的消息才会存档。开启「代取」后，该用户发来的受保护消息链接会由用户账号代取。</Description>
        {whitelist.map((w) => (
          <div class="ListItem WhitelistRow" key={w.tg_user_id}>
            <span class="ListItem-text">
              <span class="ListItem-title">{w.note || `用户 ${w.tg_user_id}`}</span>
              <span class="ListItem-subtitle">ID {w.tg_user_id}</span>
            </span>
            <span class="WhitelistRow-fetch">
              代取
              <Switch checked={w.can_fetch} label={`允许 ${w.tg_user_id} 代取`} onChange={(v) => void put(w.tg_user_id, w.note, v)} />
            </span>
            <IconButton label={`编辑 ${w.tg_user_id}`} onClick={() => setEditing(w)}>
              <Pencil size={20} />
            </IconButton>
            <IconButton
              label={`移除 ${w.tg_user_id}`}
              onClick={async () => {
                try {
                  await store.api.deleteWhitelist(botId, w.tg_user_id);
                  await reload();
                } catch (e) {
                  store.showToast(errorMessage(e));
                }
              }}
            >
              <Trash2 size={20} />
            </IconButton>
          </div>
        ))}
        {whitelist.length === 0 && <Description>白名单为空，所有消息都会被拒绝。</Description>}
        <form class="settings-form" onSubmit={(e) => void add(e)}>
          <InputField label="用户 ID" value={uid} onInput={setUid} inputMode="numeric" error={formError} />
          <InputField label="备注（可选）" value={note} onInput={setNote} />
          <Checkbox label="允许代取受保护内容" checked={canFetch} onChange={setCanFetch} />
          <Button type="submit" loading={busy}>
            加入白名单
          </Button>
        </form>
      </Section>

      <Section title="最近被拒绝">
        {rejected.filter((r) => !listed.has(r.tg_user_id)).length === 0 && <Description>暂无记录</Description>}
        {rejected
          .filter((r) => !listed.has(r.tg_user_id))
          .map((r) => (
            <div class="ListItem RejectedRow" key={r.tg_user_id}>
              <span class="ListItem-text">
                <span class="ListItem-title">{r.first_name || (r.username ? `@${r.username}` : `用户 ${r.tg_user_id}`)}</span>
                <span class="ListItem-subtitle">
                  ID {r.tg_user_id}
                  {r.username && ` · @${r.username}`} · {r.count} 次 · {formatListTime(r.last_seen_at)}
                </span>
              </span>
              <IconButton label={`将 ${r.tg_user_id} 加入白名单`} onClick={() => void put(r.tg_user_id, r.first_name || r.username, false)}>
                <UserPlus size={20} />
              </IconButton>
            </div>
          ))}
      </Section>

      <Section>
        <ListItem icon={<Trash2 size={24} />} title="删除机器人" danger onClick={() => setConfirmDelete(true)} />
      </Section>

      {editing && (
        <EditEntry
          entry={editing}
          onClose={() => setEditing(null)}
          onSave={async (n, f) => {
            if (await put(editing.tg_user_id, n, f)) setEditing(null);
          }}
        />
      )}
      {confirmDelete && (
        <ConfirmDialog
          title="删除机器人"
          text={`停止 ${botName(bot)} 的存档。默认保留已存档的消息。`}
          confirmLabel="删除"
          danger
          busy={busy}
          onConfirm={() => void remove()}
          onClose={() => setConfirmDelete(false)}
        >
          <Checkbox label="同时删除该机器人的全部存档" checked={purge} onChange={setPurge} />
        </ConfirmDialog>
      )}
    </SettingsShell>
  );
}
