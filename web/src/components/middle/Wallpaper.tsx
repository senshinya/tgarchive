import { useEffect, useRef } from 'preact/hooks';
import { DARK_COLORS, LIGHT_COLORS, paintGradient } from '../../lib/wallpaper';

const DARK_QUERY = '(prefers-color-scheme: dark)';

/** Telegram Web A's default chat wallpaper (see lib/wallpaper.ts and middle.scss). */
export function Wallpaper() {
  const canvas = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const el = canvas.current;
    if (!el) return;
    const mq = typeof matchMedia === 'function' ? matchMedia(DARK_QUERY) : null;
    const paint = () => {
      if (paintGradient(el, mq?.matches ? DARK_COLORS : LIGHT_COLORS)) el.dataset.ready = 'true';
    };
    paint();
    mq?.addEventListener?.('change', paint);
    return () => mq?.removeEventListener?.('change', paint);
  }, []);
  return (
    <div class="Wallpaper" aria-hidden="true">
      <canvas ref={canvas} class="Wallpaper-gradient" />
    </div>
  );
}
