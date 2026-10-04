import { describe, expect, it } from 'vitest';
import { SIDE_BOTTOM, SIDE_LEFT, SIDE_RIGHT, SIDE_TOP, fitMedia, layoutAlbum, type AlbumLayout, type Size } from './album';

const wide: Size = { width: 1600, height: 900 };
const tall: Size = { width: 900, height: 1600 };
const square: Size = { width: 1000, height: 1000 };

function checkInvariants(l: AlbumLayout) {
  for (const t of l.tiles) {
    expect(t.w).toBeGreaterThan(0);
    expect(t.h).toBeGreaterThan(0);
    expect(t.x + t.w).toBeLessThanOrEqual(l.width);
    expect(t.y + t.h).toBeLessThanOrEqual(l.height);
    // Edge flags must match geometry exactly.
    expect(Boolean(t.sides & SIDE_LEFT)).toBe(t.x === 0);
    expect(Boolean(t.sides & SIDE_TOP)).toBe(t.y === 0);
    expect(Boolean(t.sides & SIDE_RIGHT)).toBe(t.x + t.w === l.width);
    expect(Boolean(t.sides & SIDE_BOTTOM)).toBe(t.y + t.h === l.height);
  }
  // No overlaps, and the tiles cover the whole canvas.
  let area = 0;
  l.tiles.forEach((a, i) => {
    area += a.w * a.h;
    l.tiles.slice(i + 1).forEach((b) => {
      const overlap = a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
      expect(overlap).toBe(false);
    });
  });
  expect(area).toBe(l.width * l.height);
}

describe('layoutAlbum', () => {
  it('handles every album size from 1 to 10 with mixed ratios', () => {
    const pool = [wide, tall, square, { width: 3000, height: 500 }, { width: 400, height: 2000 }];
    for (let n = 1; n <= 10; n++) {
      const sizes = Array.from({ length: n }, (_, i) => pool[(i * 3 + n) % pool.length]);
      const l = layoutAlbum(sizes);
      expect(l.tiles).toHaveLength(n);
      expect(l.width).toBe(464);
      checkInvariants(l);
    }
  });

  it('places two similar wide items on top of each other', () => {
    const l = layoutAlbum([wide, wide]);
    expect(l.tiles[0].x).toBe(0);
    expect(l.tiles[1].x).toBe(0);
    expect(l.tiles[1].y).toBe(l.tiles[0].h);
  });

  it('places two portrait items side by side', () => {
    const l = layoutAlbum([tall, tall]);
    expect(l.tiles[0].y).toBe(0);
    expect(l.tiles[1].y).toBe(0);
    expect(l.tiles[1].x).toBe(l.tiles[0].w);
  });

  it('uses a tall left column when the first of three is portrait', () => {
    const l = layoutAlbum([tall, square, square]);
    expect(l.tiles[0]).toMatchObject({ x: 0, y: 0, h: l.height });
    expect(l.tiles[1].x).toBe(l.tiles[0].w);
    expect(l.tiles[2].y).toBe(l.tiles[1].h);
  });

  it('puts a wide first item on top for four', () => {
    const l = layoutAlbum([wide, square, square, square]);
    expect(l.tiles[0]).toMatchObject({ x: 0, y: 0, w: 464 });
    expect(new Set(l.tiles.slice(1).map((t) => t.y)).size).toBe(1);
  });

  it('treats missing dimensions as square', () => {
    const l = layoutAlbum([{ width: 0, height: 0 }, { width: 0, height: 0 }]);
    checkInvariants(l);
    expect(l.tiles[0].w).toBe(232);
  });
});

describe('fitMedia', () => {
  it('fits large images into the bubble', () => {
    expect(fitMedia({ width: 4000, height: 2000 })).toEqual({ width: 464, height: 232 });
    expect(fitMedia({ width: 1000, height: 4000 })).toEqual({ width: 128, height: 512 });
  });

  it('keeps small images at natural size but not below the minimum width', () => {
    expect(fitMedia({ width: 200, height: 100 })).toEqual({ width: 200, height: 100 });
    expect(fitMedia({ width: 20, height: 20 })).toEqual({ width: 100, height: 100 });
  });

  it('falls back to a square for unknown sizes', () => {
    expect(fitMedia({ width: 0, height: 0 })).toEqual({ width: 464, height: 464 });
  });
});
