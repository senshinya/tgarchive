import { describe, expect, it } from 'vitest';
import { DARK_COLORS, LIGHT_COLORS, gradientPixels } from './wallpaper';

function at(px: Uint8ClampedArray, size: number, x: number, y: number) {
  const o = (Math.floor(y * size) * size + Math.floor(x * size)) * 4;
  return [px[o], px[o + 1], px[o + 2], px[o + 3]];
}

describe('gradientPixels', () => {
  it('is opaque and leans towards each colour at its key point', () => {
    const size = 64;
    const px = gradientPixels(LIGHT_COLORS, size);
    expect(px.length).toBe(size * size * 4);
    expect(at(px, size, 0.5, 0.5)[3]).toBe(255);
    // At each colour's resting key point that colour dominates: the pixel is nearer to it than to
    // any other stop.
    const stops = [[0x4f, 0x5b, 0xd5], [0x96, 0x2f, 0xbf], [0xdd, 0x6c, 0xb9], [0xfe, 0xc4, 0x96]];
    const dark = gradientPixels(DARK_COLORS, size);
    const points = [[0.265, 0.582], [0.415, 0.836], [0.735, 0.418], [0.585, 0.164]];
    points.forEach(([x, y], i) => {
      const p = at(dark, size, x, y);
      const d = stops.map((c) => Math.hypot(c[0] - p[0], c[1] - p[1], c[2] - p[2]));
      expect(d.indexOf(Math.min(...d))).toBe(i);
    });
  });

  it('is uniform for a single colour', () => {
    const px = gradientPixels(['#102030'], 8);
    for (let i = 0; i < px.length; i += 4) expect([px[i], px[i + 1], px[i + 2]]).toEqual([16, 32, 48]);
  });
});
