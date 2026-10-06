import { useEffect, useRef } from 'preact/hooks';
import type { Message } from '../../api/types';
import { fitMedia, layoutAlbum } from '../../lib/album';
import { peerColor } from '../../lib/format';
import type { Bubble } from '../../lib/grouping';
import { useStore } from '../../state/store';
import { ArticleCard } from '../article/ArticleCard';
import { Album } from '../media/Album';
import { MessageMedia } from '../media/MessageMedia';
import { VISUAL_KINDS, extraString, mainMedia } from '../media/util';
import { PostFooter } from '../watch/PostFooter';
import { Appendix, ForwardHeader, MessageMeta, OriginHeader, ReplyQuote } from './MessageParts';
import { RichText } from './RichText';
import './message.scss';

export interface SenderInfo {
  name: string;
  peerId: number;
}

interface Props {
  bubble: Bubble;
  sender: SenderInfo;
  /** The conversation shown (a chat id, or -botId for a bot timeline); opens the viewer on it. */
  convKey: number;
  /** Label the bubble with the sender's name (first bubble of a group in a bot timeline). */
  showName?: boolean;
  onMenu: (x: number, y: number, msg: Message) => void;
}

const NO_BUBBLE = ['sticker', 'video_note', 'dice'];
const LONG_PRESS_MS = 500;
// Mobile browsers often fire a native `contextmenu` right after the long-press that already
// opened our menu; swallow it for a bit so the menu doesn't get asked to reopen/reposition.
const CONTEXTMENU_SUPPRESS_MS = 800;

/** Width the bubble takes when it shows a photo/video/album, so captions wrap to the media. */
function visualWidth(msgs: Message[], album: boolean): number {
  if (album) return layoutAlbum(msgs.map((m) => ({ width: mainMedia(m)?.width ?? 0, height: mainMedia(m)?.height ?? 0 }))).width;
  const main = mainMedia(msgs[0]);
  return fitMedia({ width: main?.width ?? 0, height: main?.height ?? 0 }).width;
}

export function MessageBubble({ bubble, sender, convKey, showName = false, onMenu }: Props) {
  const store = useStore();
  const press = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const suppressContextMenuUntil = useRef(0);
  useEffect(() => () => clearTimeout(press.current), []);
  const album = bubble.kind === 'album';
  const msgs = album ? bubble.msgs : [bubble.msg];
  const head = msgs[0];
  const last = msgs[msgs.length - 1];
  const caption = msgs.find((m) => m.text);
  const noBubble = !album && NO_BUBBLE.includes(head.kind);
  const visual = album ? msgs.every((m) => m.kind === 'photo' || m.kind === 'video') : VISUAL_KINDS.includes(head.kind);
  const hasHeader = showName || Boolean(head.forward_origin) || head.source === 'userbot_fetch';
  const hasReply = head.reply_to_tg_message_id > 0;
  // A watched channel post: its counters as archived, shown Telegram-channel style.
  const post = head.source === 'channel_watch' ? (last.stats ?? head.stats) : undefined;
  const hasFooter = Boolean(post) && !noBubble;
  const mediaOnly = visual && !caption && !hasHeader && !hasReply && !hasFooter;
  const unsupported = !album && head.kind === 'other';
  const solid = !noBubble && !mediaOnly;
  const editDate = Math.max(...msgs.map((m) => m.edit_date));
  const accent = peerColor(sender.peerId);

  const open = (m: Message) => {
    const md = mainMedia(m);
    if (md && md.state === 'done') store.viewer.value = { chatId: convKey, messageId: m.id, mediaId: md.id };
  };

  const pick = (target: EventTarget | null): Message => {
    const el = (target as HTMLElement | null)?.closest('[data-message-id]');
    const id = Number(el?.getAttribute('data-message-id'));
    return msgs.find((m) => m.id === id) ?? head;
  };

  const classes = ['Message', bubble.first && 'first-in-group', bubble.last && 'last-in-group', noBubble && 'no-bubble']
    .filter(Boolean)
    .join(' ');
  const contentClasses = [
    'message-content',
    solid && 'has-solid-background',
    mediaOnly && 'media-only',
    visual && 'has-visual',
    caption || unsupported ? 'has-text' : 'no-text',
    solid && bubble.last && 'has-appendix',
  ]
    .filter(Boolean)
    .join(' ');

  const metaProps = { date: last.date, editDate, views: post?.views, author: post ? extraString(head, 'post_author') : undefined };
  let meta;
  if (caption || unsupported) meta = null;
  else if ((visual || noBubble) && !hasFooter) meta = <MessageMeta {...metaProps} variant="overlay" />;
  else meta = <MessageMeta {...metaProps} variant="standalone" />;

  return (
    <div class={classes} data-message-id={head.id}>
      <div
        class={contentClasses}
        style={{
          '--accent-color': accent,
          '--accent-background-color': `color-mix(in srgb, ${accent} 10%, transparent)`,
          width: visual ? `${visualWidth(msgs, album)}px` : undefined,
        }}
        onContextMenu={(e) => {
          e.preventDefault();
          if (Date.now() < suppressContextMenuUntil.current) return;
          onMenu(e.clientX, e.clientY, pick(e.target));
        }}
        onTouchStart={(e) => {
          const t = e.touches[0];
          const target = e.target;
          press.current = setTimeout(() => {
            suppressContextMenuUntil.current = Date.now() + CONTEXTMENU_SUPPRESS_MS;
            onMenu(t.clientX, t.clientY, pick(target));
          }, LONG_PRESS_MS);
        }}
        onTouchEnd={() => clearTimeout(press.current)}
        onTouchMove={() => clearTimeout(press.current)}
      >
        {showName && !noBubble && (
          <div class="message-title sender-title" style={{ '--accent-color': accent }}>
            {sender.name || '未知用户'}
          </div>
        )}
        {head.source === 'userbot_fetch' && <OriginHeader msg={head} />}
        {head.forward_origin && <ForwardHeader origin={head.forward_origin} />}
        {hasReply && <ReplyQuote msg={head} senderName={sender.name} />}
        {album ? <Album msgs={msgs} onOpen={open} /> : <MessageMedia msg={head} onOpen={open} />}
        {(caption || unsupported) && (
          <div class="text-content" dir="auto">
            {caption ? <RichText text={caption.text} entities={caption.entities} /> : <span class="unsupported">不支持的消息类型</span>}
            {!album && head.article && <ArticleCard msg={head} />}
            <MessageMeta {...metaProps} variant="inline" />
          </div>
        )}
        {post && hasFooter && <PostFooter stats={post} />}
        {meta}
        {solid && bubble.last && <Appendix />}
      </div>
    </div>
  );
}
