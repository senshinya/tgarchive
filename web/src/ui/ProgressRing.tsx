import './ui.scss';

interface Props {
  /** 0..1; undefined spins (progress unknown). */
  value?: number;
  size?: number;
  stroke?: number;
  label?: string;
}

/** Telegram-style circular progress: a track with an arc that fills clockwise from the top. */
export function ProgressRing({ value, size = 48, stroke = 3, label = '下载进度' }: Props) {
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const known = value !== undefined && Number.isFinite(value);
  const v = known ? Math.min(1, Math.max(0, value)) : 0.25;
  return (
    <svg
      class={`ProgressRing${known ? '' : ' indeterminate'}`}
      width={size}
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={known ? Math.round(v * 100) : undefined}
    >
      <circle class="ProgressRing-track" cx={size / 2} cy={size / 2} r={r} fill="none" stroke-width={stroke} />
      <circle
        class="ProgressRing-arc"
        cx={size / 2}
        cy={size / 2}
        r={r}
        fill="none"
        stroke-width={stroke}
        stroke-linecap="round"
        stroke-dasharray={`${c} ${c}`}
        stroke-dashoffset={c * (1 - v)}
        transform={`rotate(-90 ${size / 2} ${size / 2})`}
      />
    </svg>
  );
}
