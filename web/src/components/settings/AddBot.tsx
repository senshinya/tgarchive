import { Check, X } from 'lucide-preact';
import { useState } from 'preact/hooks';
import { ApiError, errorMessage } from '../../api/client';
import type { AddBotResult, AddBotStep } from '../../api/types';
import { navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { Button } from '../../ui/Button';
import { InputField } from '../../ui/InputField';
import { Description, Section, SettingsShell } from './common';

export const TOKEN_RE = /^\d+:[A-Za-z0-9_-]{30,}$/;

const STEP_LABELS: Record<string, string> = {
  getMe: '校验 token',
  logOut: '从云端 Bot API 登出',
  start: '开始接收消息',
};

function Steps({ steps }: { steps: AddBotStep[] }) {
  return (
    <ol class="AddBot-steps">
      {steps.map((s) => (
        <li key={s.step} class={s.ok ? 'ok' : 'failed'}>
          <span class="AddBot-step-icon">{s.ok ? <Check size={18} /> : <X size={18} />}</span>
          <span class="AddBot-step-text">
            <span class="AddBot-step-label">{STEP_LABELS[s.step] ?? s.step}</span>
            {s.detail && <span class="AddBot-step-detail">{s.detail}</span>}
          </span>
        </li>
      ))}
    </ol>
  );
}

export function AddBot() {
  const store = useStore();
  const [token, setToken] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<AddBotResult | null>(null);
  const [existing, setExisting] = useState(0);

  const submit = async (e: Event) => {
    e.preventDefault();
    const t = token.trim();
    setResult(null);
    setExisting(0);
    if (!TOKEN_RE.test(t)) {
      setError('token 格式不正确');
      return;
    }
    setError('');
    setBusy(true);
    try {
      const res = await store.api.addBot(t);
      setResult(res);
      setToken('');
      await Promise.all([store.loadBots(), store.loadChats()]);
    } catch (err) {
      setError(errorMessage(err));
      if (err instanceof ApiError && err.body && typeof err.body === 'object') {
        const body = err.body as Partial<AddBotResult> & { bot_id?: number };
        if (Array.isArray(body.steps)) setResult({ steps: body.steps, error: body.error });
        if (err.status === 409 && body.bot_id) setExisting(body.bot_id);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <SettingsShell title="添加机器人" back={{ name: 'settings' }}>
      <Section>
        <Description>
          在 @BotFather 创建机器人并复制 token。添加后机器人会从云端 Bot API 登出，改由本服务的本地 Bot API 接收消息。
        </Description>
        <form class="settings-form" onSubmit={(e) => void submit(e)}>
          <InputField label="Bot Token" value={token} onInput={setToken} error={error} autoComplete="off" disabled={busy} />
          <Button type="submit" loading={busy}>
            添加
          </Button>
        </form>
        {busy && <p class="settings-description">正在校验 token、从云端登出并启动，最长约 30 秒…</p>}
        {result && <Steps steps={result.steps} />}
        {result?.bot_id && (
          <Button variant="secondary" onClick={() => navigate({ name: 'settings-bot', botId: result.bot_id! })}>
            设置白名单
          </Button>
        )}
        {existing > 0 && (
          <Button variant="secondary" onClick={() => navigate({ name: 'settings-bot', botId: existing })}>
            查看已有机器人
          </Button>
        )}
      </Section>
    </SettingsShell>
  );
}
