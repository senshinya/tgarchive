import { describe, expect, it } from 'vitest';
import { slidePadding, videoSlideSize } from './viewerLayout';

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
