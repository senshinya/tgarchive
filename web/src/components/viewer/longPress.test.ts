import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FAST_RATE, HOLD_MS, bindLongPressRate } from './longPress';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

function ptr(el: HTMLElement, type: string, x = 0, y = 0, pointerType = 'touch') {
  const e = new Event(type) as PointerEvent;
  Object.assign(e, { clientX: x, clientY: y, pointerId: 1, pointerType });
  el.dispatchEvent(e);
}

describe('bindLongPressRate', () => {
  it('plays at 2× while held and restores the previous speed on release', () => {
    const el = document.createElement('div');
    const media = { playbackRate: 1.25 };
    const onStart = vi.fn();
    const onEnd = vi.fn();
    const off = bindLongPressRate(el, media, { onStart, onEnd });
    ptr(el, 'pointerdown');
    vi.advanceTimersByTime(HOLD_MS - 1);
    expect(media.playbackRate).toBe(1.25);
    vi.advanceTimersByTime(1);
    expect(media.playbackRate).toBe(FAST_RATE);
    expect(onStart).toHaveBeenCalledOnce();
    ptr(el, 'pointermove', 40, 0); // moving while already fast keeps it
    expect(media.playbackRate).toBe(FAST_RATE);
    ptr(el, 'pointerup');
    expect(media.playbackRate).toBe(1.25);
    expect(onEnd).toHaveBeenCalledOnce();
    off();
  });

  it('a swipe before the hold fires cancels it', () => {
    const el = document.createElement('div');
    const media = { playbackRate: 1 };
    bindLongPressRate(el, media);
    ptr(el, 'pointerdown');
    ptr(el, 'pointermove', 30, 0);
    vi.advanceTimersByTime(HOLD_MS * 2);
    expect(media.playbackRate).toBe(1);
  });

  it('ignores the mouse, and cleanup restores the speed', () => {
    const el = document.createElement('div');
    const media = { playbackRate: 1 };
    const off = bindLongPressRate(el, media);
    ptr(el, 'pointerdown', 0, 0, 'mouse');
    vi.advanceTimersByTime(HOLD_MS);
    expect(media.playbackRate).toBe(1);
    ptr(el, 'pointerdown');
    vi.advanceTimersByTime(HOLD_MS);
    expect(media.playbackRate).toBe(FAST_RATE);
    off();
    expect(media.playbackRate).toBe(1);
  });
});
