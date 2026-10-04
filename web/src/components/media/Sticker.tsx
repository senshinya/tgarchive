import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { MediaStatus } from './MediaStatus';
import { TgsSticker } from './TgsSticker';
import { extraString, mainMedia } from './util';
import './media.scss';

/** Sticker without a bubble: webp image, webm video or tgs Lottie; 13rem box (11rem on phones). */
export function Sticker({ msg }: { msg: Message }) {
  const main = mainMedia(msg);
  if (!main) return null;
  const w = main.width || 512;
  const h = main.height || 512;
  const scale = Math.max(w, h);
  const style = { '--sticker-w': String(w / scale), '--sticker-h': String(h / scale) };
  const emoji = extraString(msg, 'emoji');
  let body;
  if (main.state !== 'done') body = <MediaStatus msg={msg} media={main} />;
  else if (main.mime === 'application/x-tgsticker') body = <TgsSticker src={mediaUrl(main.id)} fallback={emoji} />;
  else if (main.mime === 'video/webm')
    body = <video src={mediaUrl(main.id)} autoplay loop muted playsInline disablePictureInPicture aria-label={emoji || '贴纸'} />;
  else body = <img src={mediaUrl(main.id)} alt={emoji || '贴纸'} loading="lazy" decoding="async" />;
  return (
    <div class="Sticker" style={style} title={emoji || undefined}>
      {body}
    </div>
  );
}
