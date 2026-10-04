import { useEffect, useRef, useState } from 'preact/hooks';
import { ApiError, errorMessage } from '../../api/client';
import type { UserbotInfo } from '../../api/types';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Button } from '../../ui/Button';
import { InputField } from '../../ui/InputField';
import { ListItem } from '../../ui/ListItem';
import { ConfirmDialog } from '../../ui/Modal';
import { Spinner } from '../../ui/Spinner';
import { Description, Section, SettingsShell, USERBOT_STATE } from './common';

/** While connecting, re-read the state this often. */
export const USERBOT_POLL_MS = 3000;

export function UserbotSettings() {
  const store = useStore();
  const [info, setInfo] = useState<UserbotInfo | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [phone, setPhone] = useState('');
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [editPhone, setEditPhone] = useState(false);
  const [confirmLogout, setConfirmLogout] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const load = async () => {
    try {
      setInfo(await store.api.userbot());
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setUnavailable(true);
      else store.showToast(errorMessage(e));
    }
  };

  useEffect(() => {
    void load();
    return () => clearTimeout(timer.current);
  }, []);

  useEffect(() => {
    clearTimeout(timer.current);
    if (info?.state === 'connecting') timer.current = setTimeout(() => void load(), USERBOT_POLL_MS);
  }, [info]);

  const step = async (call: () => Promise<UserbotInfo>, reset: () => void) => {
    setBusy(true);
    setError('');
    try {
      const next = await call();
      setInfo(next);
      setEditPhone(false);
      reset();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const logout = async () => {
    setBusy(true);
    try {
      await store.api.userbotLogout();
      setConfirmLogout(false);
      await load();
    } catch (e) {
      store.showToast(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const submitPhone = (e: Event) => {
    e.preventDefault();
    void step(() => store.api.userbotPhone(phone), () => setPhone(''));
  };
  const submitCode = (e: Event) => {
    e.preventDefault();
    void step(() => store.api.userbotCode(code), () => setCode(''));
  };
  const submitPassword = (e: Event) => {
    e.preventDefault();
    void step(() => store.api.userbotPassword(password), () => setPassword(''));
  };

  const state = info?.state;
  const showPhone = state === 'logged_out' || state === 'error' || editPhone;

  return (
    <SettingsShell title="用户账号" back={{ name: 'settings' }}>
      <Section>
        <Description>
          用户账号用于代取受保护群组 / 频道的消息（白名单中开启「代取」的用户发送消息链接时）。userbot 违反 Telegram 使用条款，账号存在受限风险；登录状态等同该账号的完整权限。
        </Description>
        {unavailable && <Description>当前服务未启用用户账号功能。</Description>}
        {!info && !unavailable && (
          <div class="settings-loading">
            <Spinner size={28} />
          </div>
        )}
        {info && (
          <ListItem
            title="状态"
            subtitle={
              state === 'ready'
                ? `${info.name || '已登录'}${info.phone ? ` · ${info.phone}` : ''}${info.tg_user_id ? ` · ID ${info.tg_user_id}` : ''}`
                : state === 'error' && info.error
                  ? `错误：${info.error}`
                  : USERBOT_STATE[info.state] ?? info.state
            }
            right={state === 'connecting' ? <Spinner size={20} /> : undefined}
          />
        )}
      </Section>

      {state === 'unconfigured' && (
        <Section>
          <Description>请先在「API 凭据」中填写 api_id 与 api_hash。</Description>
          <div class="settings-form">
            <Button variant="secondary" onClick={() => navigate({ name: 'settings-telegram-app' })}>
              填写 API 凭据
            </Button>
          </div>
        </Section>
      )}

      {showPhone && (
        <Section title="登录">
          <form class="settings-form" onSubmit={submitPhone}>
            <InputField label="手机号（含国家代码）" value={phone} onInput={setPhone} type="tel" inputMode="tel" autoComplete="tel" error={error} />
            <Button type="submit" loading={busy}>
              发送验证码
            </Button>
          </form>
        </Section>
      )}

      {state === 'code_sent' && !editPhone && (
        <Section title="验证码">
          <Description>验证码已发送到 {info?.phone || '该账号'} 的 Telegram 客户端。</Description>
          <form class="settings-form" onSubmit={submitCode}>
            <InputField label="验证码" value={code} onInput={setCode} inputMode="numeric" autoComplete="one-time-code" error={error} />
            <Button type="submit" loading={busy}>
              下一步
            </Button>
            <Button
              variant="secondary"
              onClick={() => {
                setEditPhone(true);
                setError('');
              }}
            >
              重新输入手机号
            </Button>
          </form>
        </Section>
      )}

      {state === 'password_needed' && !editPhone && (
        <Section title="二步验证">
          <Description>该账号开启了二步验证，请输入密码。</Description>
          <form class="settings-form" onSubmit={submitPassword}>
            <InputField label="密码" value={password} onInput={setPassword} type="password" autoComplete="current-password" error={error} />
            <Button type="submit" loading={busy}>
              登录
            </Button>
          </form>
        </Section>
      )}

      {state === 'ready' && (
        <Section>
          <div class="settings-form">
            <Button variant="danger" onClick={() => setConfirmLogout(true)}>
              登出
            </Button>
          </div>
        </Section>
      )}

      {confirmLogout && (
        <ConfirmDialog
          title="登出用户账号"
          text="登出后会清除保存的登录状态，代取功能将不可用，直到重新登录。"
          confirmLabel="登出"
          danger
          busy={busy}
          onConfirm={() => void logout()}
          onClose={() => setConfirmLogout(false)}
        />
      )}
    </SettingsShell>
  );
}
