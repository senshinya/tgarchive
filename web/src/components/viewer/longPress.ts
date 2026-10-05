// Telegram's hold-for-2× on videos (touch only): pressing still for HOLD_MS speeds playback up
// until release; moving more than SLOP_PX first (a swipe) cancels it.

export const HOLD_MS = 500;
export const SLOP_PX = 10;
export const FAST_RATE = 2;

export interface RateTarget {
  playbackRate: number;
}

export interface LongPressHooks {
  onStart?: () => void;
  onEnd?: () => void;
}

/** Wires hold-to-speed-up onto el; returns the cleanup. */
export function bindLongPressRate(el: HTMLElement, media: RateTarget, hooks: LongPressHooks = {}): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let start: { x: number; y: number; id: number } | null = null;
  let saved: number | null = null;

  const cancel = () => {
    clearTimeout(timer);
    timer = undefined;
    start = null;
    if (saved !== null) {
      media.playbackRate = saved;
      saved = null;
      hooks.onEnd?.();
    }
  };
  const down = (e: PointerEvent) => {
    if (e.pointerType === 'mouse' || start) return;
    start = { x: e.clientX, y: e.clientY, id: e.pointerId };
    timer = setTimeout(() => {
      timer = undefined;
      saved = media.playbackRate;
      hooks.onStart?.();
      media.playbackRate = FAST_RATE;
    }, HOLD_MS);
  };
  const move = (e: PointerEvent) => {
    if (!start || e.pointerId !== start.id || saved !== null) return;
    if (Math.hypot(e.clientX - start.x, e.clientY - start.y) > SLOP_PX) cancel();
  };
  const up = (e: PointerEvent) => {
    if (start && e.pointerId === start.id) cancel();
  };
  el.addEventListener('pointerdown', down);
  el.addEventListener('pointermove', move);
  el.addEventListener('pointerup', up);
  el.addEventListener('pointercancel', up);
  return () => {
    cancel();
    el.removeEventListener('pointerdown', down);
    el.removeEventListener('pointermove', move);
    el.removeEventListener('pointerup', up);
    el.removeEventListener('pointercancel', up);
  };
}
