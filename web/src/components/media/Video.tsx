import { Play } from 'lucide-preact';
import { useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { fitMedia } from '../../lib/album';
import { formatDuration } from '../../lib/format';
import { MediaStatus } from './MediaStatus';
import { mainMedia, readyThumb } from './util';
import './media.scss';

interface Props {
  msg: Message;
  onOpen: () => void;
  fill?: boolean;
}

/** Video bubble: thumbnail + play button + duration badge; plays in the media viewer. */
export function Video({ msg, onOpen, fill }: Props) {
  const main = mainMedia(msg);
  const [revealed, setRevealed] = useState(false);
  if (!main) return null;
  const thumb = readyThumb(msg);
  const spoiler = msg.extra?.spoiler === true && !revealed;
  const size = fitMedia({ width: main.width, height: main.height });
  const style = fill ? undefined : { width: `${size.width}px`, aspectRatio: `${size.width} / ${size.height}` };
  const done = main.state === 'done';
  if (!done) {
    return (
      <div class={`media-inner Video${fill ? ' fill' : ''}`} style={style}>
        <MediaStatus msg={msg} media={main} thumb={thumb} />
      </div>
    );
  }
  return (
    <div class={`media-inner Video${fill ? ' fill' : ''}`} style={style}>
      {thumb ? (
        <img src={mediaUrl(thumb.id)} alt="" loading="lazy" class={spoiler ? 'media-spoiler-blur' : ''} />
      ) : (
        <video src={`${mediaUrl(main.id)}#t=0.1`} preload="metadata" muted playsInline class={spoiler ? 'media-spoiler-blur' : ''} />
      )}
      <button
        type="button"
        class="media-play"
        aria-label="播放视频"
        onClick={() => (spoiler ? setRevealed(true) : onOpen())}
      >
        <Play size={28} fill="currentColor" />
      </button>
      <span class="MediaBadge">{formatDuration(main.duration)}</span>
    </div>
  );
}
