import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { fitMedia } from '../../lib/album';
import { MediaStatus } from './MediaStatus';
import { mainMedia, playUrl, readyThumb } from './util';
import './media.scss';

interface Props {
  msg: Message;
  onOpen: () => void;
  fill?: boolean;
}

/** GIF: loops muted and inline, like Telegram. */
export function Animation({ msg, onOpen, fill }: Props) {
  const main = mainMedia(msg);
  if (!main) return null;
  const size = fitMedia({ width: main.width, height: main.height });
  const style = fill ? undefined : { width: `${size.width}px`, aspectRatio: `${size.width} / ${size.height}` };
  const thumb = readyThumb(msg);
  return (
    <div class={`media-inner Animation${fill ? ' fill' : ''}`} style={style}>
      {main.state === 'done' ? (
        <video
          src={playUrl(main.id, main.compat_codec)}
          poster={thumb ? mediaUrl(thumb.id) : undefined}
          autoplay
          loop
          muted
          playsInline
          disablePictureInPicture
          onClick={onOpen}
        />
      ) : (
        <MediaStatus msg={msg} media={main} thumb={thumb} />
      )}
      <span class="MediaBadge">GIF</span>
    </div>
  );
}
