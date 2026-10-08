import { describe, expect, it } from 'vitest';
import { slidePadding, videoSlideSize } from './viewerLayout';

const desktop = { x: 1280, y: 800 };
const phone = { x: 390, y: 844 };
const none = { top: 0, bottom: 0, left: 0, right: 0 };

describe('slidePadding', () => {
  it('keeps photos clear of the header and the strip on desktop', () => {
    expect(slidePadding(desktop, 'image', true)).toEqual({ top: 56, bottom: 72, left: 64, right: 64 });
    expect(slidePadding(desktop, 'image', false)).toEqual({ top: 56, bottom: 16, left: 64, right: 64 });
  });

  it('lets photos go edge to edge on phones', () => {
    expect(slidePadding(phone, 'image', true)).toEqual(none);
  });

  it('lets videos fill the screen everywhere: their controls make room for the caption and strip', () => {
    expect(slidePadding(desktop, 'video', true)).toEqual(none);
    expect(slidePadding(phone, 'video', true)).toEqual(none);
  });
});

describe('videoSlideSize', () => {
  it('is the viewport, so the player and its controls span the whole screen', () => {
    expect(videoSlideSize(phone)).toEqual({ width: 390, height: 844 });
  });
});
