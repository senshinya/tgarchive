import { Pause, Play } from 'lucide-preact';
import { useMemo, useRef, useState } from 'preact/hooks';
import { mediaUrl } from '../../api/client';
import type { Message } from '../../api/types';
import { formatDuration } from '../../lib/format';
import { silent } from '../../lib/silent';
import { SPIKE_HEIGHT, SPIKE_STEP, SPIKE_WIDTH, barCount, decodeWaveform, resample, spikeHeights } from '../../lib/waveform';
import { Document } from './Document';
import { mainMedia } from './util';
import './media.scss';

/** Voice message: play button + 5-bit waveform; played part at full accent, the rest at 50%. */
export function Voice({ msg }: { msg: Message }) {
  const main = mainMedia(msg);
  const ref = useRef<HTMLAudioElement>(null);
  const [playing, setPlaying] = useState(false);
  const [time, setTime] = useState(0);
  const bars = useMemo(
    () => spikeHeights(resample(decodeWaveform(main?.waveform), barCount(main?.duration ?? 0))),
    [main?.waveform, main?.duration],
  );
  if (!main) return null;
  if (main.state !== 'done') return <Document msg={msg} />;
  const duration = main.duration || 1;
  const progress = Math.min(1, time / duration);
  const toggle = () => {
    const a = ref.current;
    if (!a) return;
    if (a.paused) void a.play();
    else a.pause();
  };
  const seek = (e: MouseEvent) => {
    const a = ref.current;
    const el = e.currentTarget as SVGElement;
    if (!a) return;
    const width = bars.length * SPIKE_STEP;
    const ratio = Math.min(1, Math.max(0, (e.clientX - el.getBoundingClientRect().left) / width));
    a.currentTime = ratio * duration;
    if (a.paused) void a.play();
  };
  return (
    <div class="Voice">
      <button type="button" class="toggle-play" aria-label={playing ? '暂停' : '播放'} onClick={toggle}>
        {playing ? <Pause size={24} fill="currentColor" /> : <Play size={24} fill="currentColor" />}
      </button>
      <div class="Voice-body">
        <svg
          class="Waveform"
          width={bars.length * SPIKE_STEP}
          height={SPIKE_HEIGHT}
          viewBox={`0 0 ${bars.length * SPIKE_STEP} ${SPIKE_HEIGHT}`}
          onClick={seek}
          role="slider"
          aria-label="播放进度"
          aria-valuenow={Math.round(progress * 100)}
        >
          {bars.map((h, i) => (
            <rect
              key={i}
              x={i * SPIKE_STEP}
              y={SPIKE_HEIGHT - h}
              width={SPIKE_WIDTH}
              height={h}
              rx={1}
              class={(i + 0.5) / bars.length <= progress ? 'played' : ''}
            />
          ))}
        </svg>
        <span class="Voice-time">{formatDuration(playing || time > 0 ? time : main.duration)}</span>
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
