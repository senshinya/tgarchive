import { useRef, useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { formatDuration } from '../../lib/format';
import { silent } from '../../lib/silent';
import { MediaStatus } from './MediaStatus';
import { mainMedia, playUrl, readyThumb } from './util';
import './media.scss';

const SIZE = 240;
const RADIUS = SIZE / 2 - 4;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;

/** Round video message: loops muted; click plays from the start with sound (unless 静音模式) and
 * shows a progress ring. */
export function VideoNote({ msg }: { msg: Message }) {
  const main = mainMedia(msg);
  const ref = useRef<HTMLVideoElement>(null);
  const [withSound, setWithSound] = useState(false);
  const [progress, setProgress] = useState(0);
  if (!main) return null;
  const thumb = readyThumb(msg);
  const toggle = () => {
    const v = ref.current;
    if (!v) return;
    if (withSound) {
      v.muted = true;
      v.loop = true;
      setWithSound(false);
      setProgress(0);
      return;
    }
    v.currentTime = 0;
    v.muted = silent.value;
    v.loop = false;
    setWithSound(true);
    void v.play();
  };
  return (
    <div class="VideoNote" style={{ width: `${SIZE}px`, height: `${SIZE}px` }}>
      {main.state === 'done' ? (
        <video
          ref={ref}
          src={playUrl(main.id, main.compat_codec)}
          poster={thumb ? mediaUrl(thumb.id) : undefined}
          autoplay
          loop
          muted
          playsInline
          disablePictureInPicture
          aria-label={withSound ? '停止播放' : '播放视频消息'}
          onClick={toggle}
          onTimeUpdate={(e) => {
            const v = e.currentTarget as HTMLVideoElement;
            if (withSound && v.duration) setProgress(v.currentTime / v.duration);
          }}
          onEnded={() => {
            const v = ref.current;
            if (v) {
              v.muted = true;
              v.loop = true;
              void v.play();
            }
            setWithSound(false);
            setProgress(0);
          }}
        />
      ) : (
        <MediaStatus msg={msg} media={main} thumb={thumb} />
      )}
      {withSound && (
        <svg class="VideoNote-ring" viewBox={`0 0 ${SIZE} ${SIZE}`} aria-hidden="true">
          <circle
            cx={SIZE / 2}
            cy={SIZE / 2}
            r={RADIUS}
            stroke-dasharray={`${CIRCUMFERENCE * progress} ${CIRCUMFERENCE}`}
          />
        </svg>
      )}
      <span class="MediaBadge">{formatDuration(main.duration)}</span>
    </div>
  );
}
