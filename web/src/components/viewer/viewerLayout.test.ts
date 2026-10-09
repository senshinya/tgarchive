import { describe, expect, it } from 'vitest';
import { slidePadding, turn, turned, videoSlideSize } from './viewerLayout';

const desktop = { x: 1280, y: 800 };
const phone = { x: 390, y: 844 };
const none = { top: 0, bottom: 0, left: 0, right: 0 };

describe('slidePadding', () => {
  it('keeps photos clear of the header and the strip on desktop', () => {
    expect(slidePadding(desktop, 'image', true, 200)).toEqual({ top: 56, bottom: 72, left: 64, right: 64 });
    expect(slidePadding(desktop, 'image', false, 0)).toEqual({ top: 56, bottom: 16, left: 64, right: 64 });
  });

  it('keeps desktop videos clear of the caption and strip, so their controls sit on the picture', () => {
    expect(slidePadding(desktop, 'video', true, 200)).toEqual({ top: 56, bottom: 208, left: 64, right: 64 });
    expect(slidePadding(desktop, 'video', false, 0)).toEqual({ top: 56, bottom: 16, left: 64, right: 64 });
  });

  it('lets everything go edge to edge on phones', () => {
    expect(slidePadding(phone, 'image', true, 200)).toEqual(none);
    expect(slidePadding(phone, 'video', true, 200)).toEqual(none);
  });
});

describe('videoSlideSize', () => {
  it('is the screen on phones, where the controls run along its bottom above the caption', () => {
    expect(videoSlideSize(phone, 480, 832)).toEqual({ width: 390, height: 844 });
  });

  it('is the video on desktop', () => {
    expect(videoSlideSize(desktop, 480, 832)).toEqual({ width: 480, height: 832 });
    expect(videoSlideSize(desktop, 0, 0)).toEqual({ width: 1280, height: 720 });
  });
});

describe('turn / turned', () => {
  it('counts quarter turns both ways and wraps', () => {
    expect(turn(0, 1)).toBe(1);
    expect(turn(3, 1)).toBe(0);
    expect(turn(0, -1)).toBe(3);
  });

  it('swaps width and height on odd turns only', () => {
    expect(turned(1920, 1080, 1)).toEqual({ width: 1080, height: 1920 });
    expect(turned(1920, 1080, 2)).toEqual({ width: 1920, height: 1080 });
    expect(turned(1920, 1080, 3)).toEqual({ width: 1080, height: 1920 });
  });
});
