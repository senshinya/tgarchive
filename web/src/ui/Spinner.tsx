import './ui.scss';

export function Spinner({ size = 24, color = 'currentColor' }: { size?: number; color?: string }) {
  return (
    <svg class="Spinner" width={size} height={size} viewBox="0 0 24 24" role="progressbar" aria-label="加载中">
      <circle cx="12" cy="12" r="9" fill="none" stroke={color} stroke-width="2.5" stroke-linecap="round" stroke-dasharray="42 100" />
    </svg>
  );
}
