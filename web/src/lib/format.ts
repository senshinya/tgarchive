import type { Bot, Chat, Sender } from '../api/types';

const pad = (n: number) => String(n).padStart(2, '0');
const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

function toDate(unix: number): Date {
  return new Date(unix * 1000);
}

function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

/** Whole local calendar days between `unix` and `now` (0 = same day). DST-safe via rounding. */
export function daysAgo(unix: number, now: Date = new Date()): number {
  return Math.round((startOfDay(now) - startOfDay(toDate(unix))) / 86_400_000);
}

/** "13:05" */
export function formatTime(unix: number): string {
  const d = toDate(unix);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** Local calendar day key, e.g. "2026-10-04". */
export function dayKey(unix: number): string {
  const d = toDate(unix);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** Date separator pill: 今天 / 昨天 / 10月4日 / 2025年10月4日. */
export function formatDayLabel(unix: number, now: Date = new Date()): string {
  const diff = daysAgo(unix, now);
  if (diff === 0) return '今天';
  if (diff === 1) return '昨天';
  const d = toDate(unix);
  if (d.getFullYear() === now.getFullYear()) return `${d.getMonth() + 1}月${d.getDate()}日`;
  return `${d.getFullYear()}年${d.getMonth() + 1}月${d.getDate()}日`;
}

/** Chat list time column: 13:05 / 周一 / 10/4 / 2025/10/4. */
export function formatListTime(unix: number, now: Date = new Date()): string {
  if (!unix) return '';
  const diff = daysAgo(unix, now);
  const d = toDate(unix);
  if (diff <= 0) return formatTime(unix);
  if (diff < 7) return WEEKDAYS[d.getDay()];
  if (d.getFullYear() === now.getFullYear()) return `${d.getMonth() + 1}/${d.getDate()}`;
  return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()}`;
}

/** "2026年10月4日 13:05" */
export function formatFullDate(unix: number): string {
  const d = toDate(unix);
  return `${d.getFullYear()}年${d.getMonth() + 1}月${d.getDate()}日 ${formatTime(unix)}`;
}

/** Shared-media month heading: "2026年10月". */
export function formatMonth(unix: number): string {
  const d = toDate(unix);
  return `${d.getFullYear()}年${d.getMonth() + 1}月`;
}

/** Month key used to group shared media: "2026-10". */
export function monthKey(unix: number): string {
  const d = toDate(unix);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}`;
}

/** 0 B / 512 B / 1.5 KB / 20 MB / 1.2 GB */
export function formatSize(bytes: number): string {
  if (!bytes || bytes < 0) return '0 B';
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1).replace(/\.0$/, '')} ${units[i]}`;
}

/** Download progress: "12.3 MB / 1.2 GB"; just the size so far when the total is unknown;
 * "下载中…" until the first byte. */
export function formatProgress(done: number, total: number): string {
  if (done <= 0) return total > 0 ? `下载中… · ${formatSize(total)}` : '下载中…';
  return total > 0 ? `${formatSize(Math.min(done, total))} / ${formatSize(total)}` : `已下载 ${formatSize(done)}`;
}

/** 0:07 / 12:34 / 1:02:03 */
export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds || 0));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  return h > 0 ? `${h}:${pad(m)}:${pad(sec)}` : `${m}:${pad(sec)}`;
}

const KIND_LABELS: Record<string, string> = {
  text: '消息',
  photo: '照片',
  video: '视频',
  animation: 'GIF',
  voice: '语音消息',
  audio: '音乐',
  document: '文件',
  sticker: '贴纸',
  video_note: '视频消息',
  location: '位置',
  venue: '地点',
  contact: '联系人',
  poll: '投票',
  dice: '骰子',
  other: '不支持的消息',
};

export function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? KIND_LABELS.other;
}

/** One-line preview used by the chat list and reply quotes. */
export function previewText(kind: string, text: string): string {
  const t = text.replace(/\s+/g, ' ').trim();
  if (t) return t;
  return kind ? kindLabel(kind) : '';
}

/** The display name of a conversation: its channel's title, or its sender's name. */
export function chatName(chat: Chat): string {
  return chat.kind === 'channel' ? chat.channel?.title || '频道' : senderName(chat.sender);
}

export function senderName(s: Pick<Sender, 'first_name' | 'last_name' | 'username' | 'tg_user_id'>): string {
  const full = `${s.first_name} ${s.last_name}`.trim();
  if (full) return full;
  if (s.username) return `@${s.username}`;
  return `用户 ${s.tg_user_id}`;
}

export function botName(b: Pick<Bot, 'name' | 'username' | 'id'>): string {
  return b.name || (b.username ? `@${b.username}` : `机器人 ${b.id}`);
}

/** Up to two initials taken from the first two words; surrogate-pair safe. */
export function initials(name: string): string {
  const words = name.replace(/^@/, '').trim().split(/\s+/).filter(Boolean);
  const chars = words.slice(0, 2).map((w) => Array.from(w)[0] ?? '');
  return chars.join('').toUpperCase();
}

/** Stable non-negative 31-bit hash (djb2) for peers that only have a name. */
export function hashString(s: string): number {
  let h = 5381;
  for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}

/** Stable peer color slot 0..6 (Web A fallback palette indexing). */
export function peerColorIndex(id: number): number {
  return Math.abs(Math.trunc(id)) % 7;
}

export function peerColor(id: number): string {
  return `var(--peer-${peerColorIndex(id)})`;
}

/** Extension shown on the document icon tile, at most 4 characters. */
export function fileExtension(name: string, mime: string): string {
  const m = /\.([A-Za-z0-9]{1,8})$/.exec(name);
  if (m) return m[1].toLowerCase().slice(0, 4);
  const sub = mime.split('/')[1] ?? '';
  return sub.replace(/^x-/, '').slice(0, 4).toLowerCase();
}

/** Icon tile color by extension: red pdf, green spreadsheets/apk, orange archives/slides, else primary. */
export function fileColor(ext: string): string {
  if (['pdf', 'xps'].includes(ext)) return 'var(--color-error)';
  if (['apk', 'xls', 'xlsx', 'ods'].includes(ext)) return 'var(--color-text-green)';
  if (['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz', 'ppt', 'pptx', 'odp'].includes(ext)) return 'var(--color-warning)';
  return 'var(--color-primary)';
}

/** How long ago unix time `at` was, coarsely: 刚刚, N 分钟前, N 小时前, N 天前. */
export function formatAgo(at: number, now: number = Date.now() / 1000): string {
  const s = Math.floor(now - at);
  if (s < 60) return '刚刚';
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  return `${Math.floor(s / 86400)} 天前`;
}
