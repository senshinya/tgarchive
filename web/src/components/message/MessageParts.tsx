import { Lock } from 'lucide-preact';
import type { ForwardOrigin, Message } from '../../api/types';
import { safeHref } from '../../lib/entities';
import { formatFullDate, formatTime, hashString, peerColor, previewText } from '../../lib/format';
import { extraString } from '../media/util';

export type MetaVariant = 'inline' | 'overlay' | 'standalone';

/** Time + 已编辑; inline floats at the end of text, overlay sits on media, standalone gets its own row. */
export function MessageMeta({ date, editDate, variant }: { date: number; editDate: number; variant: MetaVariant }) {
  const title = editDate > 0 ? `${formatFullDate(date)}\n已编辑：${formatFullDate(editDate)}` : formatFullDate(date);
  return (
    <span class={`MessageMeta ${variant}`} title={title}>
      {editDate > 0 && <span class="message-edited">已编辑</span>}
      <span class="message-time">{formatTime(date)}</span>
    </span>
  );
}

function originPeer(o: ForwardOrigin): number {
  return o.user_id || o.chat_id || hashString(o.name);
}

export function ForwardHeader({ origin }: { origin: ForwardOrigin }) {
  const name = origin.name || '隐藏用户';
  let href: string | null = null;
  if (origin.username) {
    href = safeHref(
      origin.type === 'channel' && origin.message_id
        ? `https://t.me/${origin.username}/${origin.message_id}`
        : `https://t.me/${origin.username}`,
    );
  }
  return (
    <div class="message-title forward-title" style={{ '--accent-color': peerColor(originPeer(origin)) }}>
      <span class="forward-label">转发自 </span>
      {href ? (
        <a class="forward-name" href={href} target="_blank" rel="noopener noreferrer">
          {name}
        </a>
      ) : (
        <span class="forward-name">{name}</span>
      )}
      {origin.signature && <span class="forward-signature">（{origin.signature}）</span>}
    </div>
  );
}

/** Header for userbot-fetched protected content: 来自 <群名> · 受保护, linking to the original post. */
export function OriginHeader({ msg }: { msg: Message }) {
  const title = msg.origin_chat_title || '受保护的频道';
  const href = safeHref(msg.origin_link);
  const author = extraString(msg, 'post_author') || extraString(msg, 'author');
  return (
    <div class="message-title origin-title" style={{ '--accent-color': peerColor(hashString(title)) }}>
      <Lock size={14} class="origin-lock" />
      <span>来自 </span>
      {href ? (
        <a class="origin-name" href={href} target="_blank" rel="noopener noreferrer">
          {title}
        </a>
      ) : (
        <span class="origin-name">{title}</span>
      )}
      <span> · 受保护</span>
      {author && <span class="origin-author"> · {author}</span>}
    </div>
  );
}

/** Reply quote; clicking scrolls to the replied message when it is loaded and flashes it. */
export function ReplyQuote({ msg, senderName }: { msg: Message; senderName: string }) {
  const r = msg.reply;
  const jump = (e: MouseEvent) => {
    e.stopPropagation();
    if (!r) return;
    const el = document.querySelector(`[data-message-id="${r.id}"]`)?.closest('.Message');
    if (!el) return;
    el.scrollIntoView({ block: 'center', behavior: 'smooth' });
    el.classList.remove('highlight');
    void (el as HTMLElement).offsetWidth;
    el.classList.add('highlight');
  };
  return (
    <button type="button" class={`EmbeddedMessage${r ? '' : ' missing'}`} onClick={jump}>
      <span class="embedded-title">{r ? senderName : '回复'}</span>
      <span class="embedded-text">{r ? previewText(r.kind, r.text) : '原消息未存档'}</span>
    </button>
  );
}

/** Bubble tail for the last incoming bubble of a group. */
export function Appendix() {
  return (
    <svg class="svg-appendix" width="9" height="18" viewBox="0 0 9 18" aria-hidden="true">
      <path class="corner" d="M9 0v18H3.2c-1.9 0-2.7-1.3-1.6-2.6C4.3 12.4 8.1 8.2 9 0z" />
    </svg>
  );
}
