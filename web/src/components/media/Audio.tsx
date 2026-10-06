import { Pause, Play } from 'lucide-preact';
import { useRef, useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { formatDuration } from '../../lib/format';
import { silent } from '../../lib/silent';
import { Document } from './Document';
import { extraString, mainMedia } from './util';
import './media.scss';

/** Music file: round play button, title/performer, seekable progress line while playing. */
export function Audio({ msg }: { msg: Message }) {
  const main = mainMedia(msg);
  const ref = useRef<HTMLAudioElement>(null);
  const [playing, setPlaying] = useState(false);
  const [time, setTime] = useState(0);
  if (!main) return null;
  if (main.state !== 'done') return <Document msg={msg} />;
  const title = extraString(msg, 'title') || main.file_name || '音乐';
  const performer = extraString(msg, 'performer');
  const duration = main.duration || ref.current?.duration || 0;
  const toggle = () => {
    const a = ref.current;
    if (!a) return;
    if (a.paused) void a.play();
    else a.pause();
  };
  const seek = (e: MouseEvent) => {
    const a = ref.current;
    const el = e.currentTarget as HTMLElement;
    if (!a || !duration) return;
    const ratio = Math.min(1, Math.max(0, (e.clientX - el.getBoundingClientRect().left) / el.clientWidth));
    a.currentTime = ratio * duration;
  };
  return (
    <div class="Audio">
      <button type="button" class="toggle-play" aria-label={playing ? '暂停' : '播放'} onClick={toggle}>
        {playing ? <Pause size={24} fill="currentColor" /> : <Play size={24} fill="currentColor" />}
      </button>
      <div class="Audio-info">
        <div class="Audio-title">{title}</div>
        {playing || time > 0 ? (
          <>
            <div class="Audio-progress" onClick={seek} role="slider" aria-label="播放进度" aria-valuenow={Math.round(time)}>
              <div class="Audio-progress-fill" style={{ width: `${duration ? (time / duration) * 100 : 0}%` }} />
            </div>
            <div class="Audio-meta">
              {formatDuration(time)} / {formatDuration(duration)}
            </div>
          </>
        ) : (
          <div class="Audio-meta">
            {performer ? `${performer} · ` : ''}
            {formatDuration(duration)}
          </div>
        )}
      </div>
      <audio
        ref={ref}
        src={mediaUrl(main.id)}
        muted={silent.value}
        preload="none"
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onEnded={() => {
          setPlaying(false);
          setTime(0);
        }}
        onTimeUpdate={(e) => setTime((e.currentTarget as HTMLAudioElement).currentTime)}
      />
    </div>
  );
}
