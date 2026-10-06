import { ArrowDownToLine, File, Film, Image, Music, RotateCw } from 'lucide-preact';
import type { ComponentChildren } from 'preact';
import { useEffect } from 'preact/hooks';
import type { ActiveDownload, DownloadItem } from '../../api/types';
import { formatProgress, formatSize, kindLabel, peerColor, chatName } from '../../lib/format';
import { convRoute, navigate } from '../../lib/router';
import { useStore } from '../../state/store';
import { ProgressRing } from '../../ui/ProgressRing';
import { Spinner } from '../../ui/Spinner';
import { Section, SettingsShell } from '../settings/common';
import './downloads.scss';

function KindIcon({ kind }: { kind: string }) {
  if (kind === 'photo') return <Image size={22} />;
  if (kind === 'video' || kind === 'animation' || kind === 'video_note') return <Film size={22} />;
  if (kind === 'audio' || kind === 'voice') return <Music size={22} />;
  return <File size={22} />;
}

function DownloadRow({ item, progress, children }: { item: DownloadItem; progress?: { done: number; total: number }; children?: ComponentChildren }) {
  const store = useStore();
  const chat = store.chats.value.find((c) => c.id === item.chat_id);
  const who = chat ? chatName(chat) : '';
  // Opens the conversation the file belongs to — the bot timeline in bot mode — and asks it to
  // scroll to the message once it is shown.
  const open = () => {
    // Channel conversations belong to no bot: botKeyOf gives 0 and the chat itself opens.
    const key = (store.listMode.value === 'bot' && store.botKeyOf(item.chat_id)) || item.chat_id;
    store.jumpTo.value = { key, messageId: item.message_id };
    // fromList: the conversation's back button then returns here instead of going home.
    navigate(convRoute(key), { fromList: true });
  };
  const total = progress ? progress.total || item.size : item.size;
  const pct = progress && total > 0 ? Math.min(100, (progress.done / total) * 100) : 0;
  return (
    <div class="DownloadRow">
      <button type="button" class="DownloadRow-main" onClick={open}>
        <span class="DownloadRow-icon" style={{ background: peerColor(item.chat_id) }}>
          <KindIcon kind={item.kind} />
        </span>
        <span class="DownloadRow-info">
          <span class="DownloadRow-title">{item.file_name || kindLabel(item.kind)}</span>
          <span class={`DownloadRow-subtitle${item.error && !progress ? ' error' : ''}`}>
            {who && <span class="DownloadRow-sender">{who}</span>}
            {progress ? formatProgress(progress.done, total) : item.error || formatSize(item.size)}
          </span>
          {progress && (
            <span class={`DownloadRow-bar${progress.done > 0 && total > 0 ? '' : ' indeterminate'}`} aria-hidden="true">
              <span style={{ width: `${pct}%` }} />
            </span>
          )}
        </span>
      </button>
      {children}
    </div>
  );
}

/** Left header button: total progress ring while downloading, a red dot when something failed;
 * hidden when there is nothing to show. Opens the downloads panel. */
export function DownloadsButton() {
  const store = useStore();
  const s = store.downloadSummary.value;
  if (!s.active && !s.queued && !s.failed) return null;
  const parts = [s.active && `${s.active} 个下载中`, s.queued && `${s.queued} 个排队`, s.failed && `${s.failed} 个失败`].filter(Boolean);
  return (
    <button
      type="button"
      class={`DownloadsButton${s.active ? ' active' : ''}`}
      aria-label={`下载：${parts.join('，')}`}
      title={parts.join('，')}
      onClick={() => navigate({ name: 'downloads' })}
    >
      {s.active > 0 && <ProgressRing value={s.fraction} size={36} stroke={2.5} label="总下载进度" />}
      <ArrowDownToLine size={20} />
      {s.failed > 0 && <span class="DownloadsButton-dot" aria-hidden="true" />}
    </button>
  );
}

/** Left column view at /downloads: totals, what is downloading now, and what failed. */
export function DownloadsPanel() {
  const store = useStore();
  useEffect(() => {
    void store.loadDownloads();
  }, []);
  const snap = store.downloads.value;
  const live = store.progress.value;
  // The live progress map is authoritative for what is downloading; the snapshot supplies names.
  const active: ActiveDownload[] = (snap?.active ?? []).filter((a) => live.has(a.media_id) || live.size === 0);
  let remaining = snap?.queued.bytes ?? 0;
  for (const a of active) {
    const p = live.get(a.media_id) ?? a;
    remaining += Math.max(0, (p.total || a.size) - p.done);
  }
  const failed = snap?.failed ?? [];
  return (
    <SettingsShell title="下载" back={{ name: 'home' }}>
      {!snap ? (
        <div class="DownloadsPanel-loading">
          <Spinner size={32} />
        </div>
      ) : (
        <>
          <div class="DownloadsSummary">
            <div class="DownloadsSummary-stat">
              <span class="DownloadsSummary-value">{formatSize(snap.speed)}/s</span>
              <span class="DownloadsSummary-label">速度</span>
            </div>
            <div class="DownloadsSummary-stat">
              <span class="DownloadsSummary-value">{formatSize(remaining)}</span>
              <span class="DownloadsSummary-label">剩余</span>
            </div>
            <div class="DownloadsSummary-stat">
              <span class="DownloadsSummary-value">{snap.queued.count}</span>
              <span class="DownloadsSummary-label">排队</span>
            </div>
          </div>
          {active.length === 0 && failed.length === 0 && (
            <p class="DownloadsPanel-empty">{snap.queued.count > 0 ? '排队中的文件稍后开始下载' : '没有进行中的下载'}</p>
          )}
          {active.length > 0 && (
            <Section title={`进行中 · ${active.length}`}>
              {active.map((a) => (
                <DownloadRow key={a.media_id} item={a} progress={live.get(a.media_id) ?? { done: a.done, total: a.total }} />
              ))}
            </Section>
          )}
          {failed.length > 0 && (
            <Section title={`失败 · ${failed.length}`}>
              {failed.map((f) => (
                <DownloadRow key={f.media_id} item={f}>
                  <button type="button" class="DownloadRow-retry" aria-label="重试" onClick={() => void store.retryDownload(f.media_id)}>
                    <RotateCw size={20} />
                  </button>
                </DownloadRow>
              ))}
            </Section>
          )}
        </>
      )}
    </SettingsShell>
  );
}
