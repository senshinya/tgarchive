import { describe, expect, it } from 'vitest';
import { type KeyLike, viewerKeyAction } from './viewerKeys';

const k = (key: string, extra: Partial<KeyLike> = {}): KeyLike => ({
  key,
  shiftKey: false,
  ctrlKey: false,
  metaKey: false,
  altKey: false,
  target: document.body,
  ...extra,
});

describe('viewerKeyAction', () => {
  it('pages photos and GIFs with the arrows', () => {
    expect(viewerKeyAction(k('ArrowLeft'), 'photo')).toBe('prev');
    expect(viewerKeyAction(k('ArrowRight'), 'animation')).toBe('next');
  });

  it('leaves plain arrows on a video to the player (seek), Shift+arrows page', () => {
    expect(viewerKeyAction(k('ArrowLeft'), 'video')).toBeNull();
    expect(viewerKeyAction(k('ArrowRight'), 'video')).toBeNull();
    expect(viewerKeyAction(k('ArrowLeft', { shiftKey: true }), 'video')).toBe('prev');
    expect(viewerKeyAction(k('ArrowRight', { shiftKey: true }), 'video')).toBe('next');
    expect(viewerKeyAction(k('PageDown'), 'video')).toBe('next');
    expect(viewerKeyAction(k(' '), 'video')).toBeNull();
  });

  it('closes on Escape and zooms photos only', () => {
    expect(viewerKeyAction(k('Escape'), 'video')).toBe('close');
    expect(viewerKeyAction(k('+'), 'photo')).toBe('zoomIn');
    expect(viewerKeyAction(k('-'), 'photo')).toBe('zoomOut');
    expect(viewerKeyAction(k('+'), 'video')).toBeNull();
  });

  it('rotates with R (clockwise) and Shift+R, videos too', () => {
    expect(viewerKeyAction(k('r'), 'photo')).toBe('rotateCw');
    expect(viewerKeyAction(k('R', { shiftKey: true }), 'video')).toBe('rotateCcw');
  });

  it('ignores modified keys and typing in a field', () => {
    expect(viewerKeyAction(k('ArrowLeft', { metaKey: true }), 'photo')).toBeNull();
    const input = document.createElement('input');
    expect(viewerKeyAction(k('ArrowLeft', { target: input }), 'photo')).toBeNull();
    const menu = document.createElement('div');
    menu.setAttribute('role', 'menu');
    expect(viewerKeyAction(k('Escape', { target: menu }), 'video')).toBeNull();
  });
});
