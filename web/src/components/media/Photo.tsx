import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { fitMedia } from '../../lib/album';
import { MediaStatus } from './MediaStatus';
import { mainMedia, readyThumb } from './util';
import './media.scss';

interface Props {
  msg: Message;
  onOpen: () => void;
  /** Fill the parent (album tile) instead of sizing itself. */
  fill?: boolean;
}

export function Photo({ msg, onOpen, fill }: Props) {
  const main = mainMedia(msg);
  if (!main) return null;
  const size = fitMedia({ width: main.width, height: main.height });
  const style = fill ? undefined : { width: `${size.width}px`, aspectRatio: `${size.width} / ${size.height}` };
  return (
    <div class={`media-inner Photo${fill ? ' fill' : ''}`} style={style}>
      {main.state === 'done' ? (
        <img
          src={mediaUrl(main.id)}
          alt=""
          loading="lazy"
          decoding="async"
          onClick={onOpen}
        />
      ) : (
        <MediaStatus msg={msg} media={main} thumb={readyThumb(msg)} />
      )}
    </div>
  );
}
