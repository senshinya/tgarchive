import { mediaUrl } from '../../api/client';
import type { ReactionStat } from '../../api/types';
import { TgsSticker } from '../media/TgsSticker';
import './watch.scss';

/** A reaction's glyph: the emoji, ⭐ for paid stars, or a custom emoji's sticker (❔ until it is known). */
export function ReactionIcon({ stat }: { stat: Pick<ReactionStat, 'key' | 'emoji' | 'media_id' | 'mime'> }) {
  if (stat.key === 'paid') return <span class="ReactionIcon">⭐</span>;
  if (!stat.key.startsWith('custom:')) return <span class="ReactionIcon">{stat.emoji || stat.key}</span>;
  if (!stat.media_id) return <span class="ReactionIcon" title="自定义表情">❔</span>;
  const src = mediaUrl(stat.media_id);
  let body;
  if (stat.mime === 'application/x-tgsticker') body = <TgsSticker src={src} fallback="❔" />;
  else if (stat.mime === 'video/webm') body = <video src={src} autoplay loop muted playsInline disablePictureInPicture />;
  else body = <img src={src} alt="" loading="lazy" decoding="async" />;
  return (
    <span class="ReactionIcon custom" title="自定义表情">
      {body}
    </span>
  );
}

/** A reaction's label in pickers and condition text. */
export function reactionLabel(key: string): string {
  if (key === 'paid') return '⭐ 付费星星';
  if (key.startsWith('custom:')) return '自定义表情';
  if (key === 'total') return 'reaction 总数';
  return key;
}
