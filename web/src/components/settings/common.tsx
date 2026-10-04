import { ArrowLeft } from 'lucide-preact';
import type { ComponentChildren } from 'preact';
import { navigate, type Route } from '../../lib/router';
import { IconButton } from '../../ui/Button';
import './settings.scss';

export function SettingsShell({ title, back, children }: { title: string; back: Route; children: ComponentChildren }) {
  return (
    <div class="SettingsPanel">
      <div class="left-header settings-header">
        <IconButton label="返回" onClick={() => navigate(back)}>
          <ArrowLeft size={24} />
        </IconButton>
        <h3 class="settings-header-title">{title}</h3>
      </div>
      <div class="settings-content custom-scroll">{children}</div>
    </div>
  );
}

export function Section({ title, children }: { title?: string; children: ComponentChildren }) {
  return (
    <section class="settings-section">
      {title && <h4 class="settings-section-title">{title}</h4>}
      {children}
    </section>
  );
}

export function Description({ children }: { children: ComponentChildren }) {
  return <p class="settings-description">{children}</p>;
}

export function StatusDot({ status }: { status: string }) {
  return <span class={`StatusDot status-${status}`} aria-hidden="true" />;
}

export const BOT_STATUS: Record<string, string> = {
  running: '运行中',
  stopped: '已停止',
  error: '错误',
  removed: '已删除',
};

export const SERVER_STATE: Record<string, string> = {
  running: '运行中',
  restarting: '重启中',
  unconfigured: '未配置',
};

export const USERBOT_STATE: Record<string, string> = {
  unconfigured: '未配置 API 凭据',
  connecting: '连接中…',
  logged_out: '未登录',
  code_sent: '等待验证码',
  password_needed: '等待二步验证密码',
  ready: '已登录',
  error: '错误',
};
