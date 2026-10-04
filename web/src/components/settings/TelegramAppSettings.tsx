import { useEffect, useState } from 'preact/hooks';
import { errorMessage } from '../../api/client';
import type { TelegramApp } from '../../api/types';
import { useStore } from '../../state/store';
import { Button } from '../../ui/Button';
import { InputField } from '../../ui/InputField';
import { ListItem } from '../../ui/ListItem';
import { Description, SERVER_STATE, Section, SettingsShell } from './common';

const HASH_RE = /^[0-9a-fA-F]{32}$/;

export function TelegramAppSettings() {
  const store = useStore();
  const [app, setApp] = useState<TelegramApp | null>(null);
  const [apiId, setApiId] = useState('');
  const [apiHash, setApiHash] = useState('');
  const [errors, setErrors] = useState<{ id?: string; hash?: string }>({});
  const [busy, setBusy] = useState(false);

  const load = async () => {
    try {
      const a = await store.api.telegramApp();
      setApp(a);
      if (a.configured) setApiId(String(a.api_id));
    } catch (e) {
      store.showToast(errorMessage(e));
    }
  };

  useEffect(() => {
    void load();
  }, []);

  const save = async (e: Event) => {
    e.preventDefault();
    const next: { id?: string; hash?: string } = {};
    const id = Number(apiId.trim());
    if (!/^[1-9][0-9]{0,9}$/.test(apiId.trim()) || id > 2147483647) next.id = 'api_id 必须是正整数';
    if (!HASH_RE.test(apiHash.trim())) next.hash = 'api_hash 必须是 32 位十六进制字符串';
    setErrors(next);
    if (next.id || next.hash) return;
    setBusy(true);
    try {
      await store.api.saveTelegramApp(id, apiHash.trim());
      setApiHash('');
      store.showToast('已保存');
      await load();
    } catch (err) {
      setErrors({ hash: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  };

  let server = '加载中…';
  if (app) {
    if (!app.server.managed) server = '外部 Bot API（不由本服务托管）';
    else server = app.server.error ? `${SERVER_STATE[app.server.state] ?? app.server.state}：${app.server.error}` : SERVER_STATE[app.server.state] ?? app.server.state;
  }

  return (
    <SettingsShell title="API 凭据" back={{ name: 'settings' }}>
      <Section>
        <Description>
          在 my.telegram.org 申请的 api_id 与 api_hash，本地 Bot API 服务器与用户账号共用。api_hash 加密保存，之后不再显示；保存新凭据会重启 Bot API 服务器。
        </Description>
        <ListItem title="Bot API 服务器" subtitle={server} />
        <form class="settings-form" onSubmit={(e) => void save(e)}>
          <InputField label="api_id" value={apiId} onInput={setApiId} inputMode="numeric" error={errors.id} />
          <InputField
            label={app?.configured ? 'api_hash（已保存，修改时重新输入）' : 'api_hash'}
            value={apiHash}
            onInput={setApiHash}
            type="password"
            autoComplete="off"
            error={errors.hash}
          />
          <Button type="submit" loading={busy}>
            保存
          </Button>
        </form>
      </Section>
    </SettingsShell>
  );
}
