import { describe, expect, it } from 'vitest';
import { justify } from './justify';

const rowWidth = (items: { w: number; h: number }[], row: { start: number; count: number; height: number }, gap: number) => {
  let sum = 0;
  for (let i = row.start; i < row.start + row.count; i++) {
    const { w, h } = items[i];
    sum += Math.min(3, Math.max(0.5, w > 0 && h > 0 ? w / h : 1)) * row.height;
  }
  return sum + gap * (row.count - 1);
};

describe('justify', () => {
  it('fills full rows to the exact width and keeps their height at or under the target', () => {
    const items = [
      { w: 400, h: 300 },
      { w: 300, h: 400 },
      { w: 1600, h: 900 },
      { w: 500, h: 500 },
      { w: 300, h: 200 },
      { w: 800, h: 600 },
      { w: 600, h: 800 },
    ];
    const rows = justify(items, 600, 180, 4);
    expect(rows.reduce((n, r) => n + r.count, 0)).toBe(items.length);
    expect(rows[0].start).toBe(0);
    for (let i = 1; i < rows.length; i++) expect(rows[i].start).toBe(rows[i - 1].start + rows[i - 1].count);
    for (const r of rows.slice(0, -1)) {
      expect(Math.abs(rowWidth(items, r, 4) - 600)).toBeLessThan(1);
      expect(r.height).toBeLessThanOrEqual(180);
    }
  });

  it('leaves the last row at the target height instead of stretching it', () => {
    const rows = justify([{ w: 100, h: 100 }], 600, 180, 4);
    expect(rows).toEqual([{ start: 0, count: 1, height: 180 }]);
  });

  it('treats missing sizes as square and clamps extreme shapes', () => {
    const rows = justify(
      [
        { w: 0, h: 0 },
        { w: 10000, h: 10 },
        { w: 1, h: 1000 },
      ],
      1000,
      200,
      0,
    );
    // ratios 1 + 3 + 0.5 = 4.5 → 1000 / 4.5 ≈ 222 > 200: one unfinished row at the target height.
    expect(rows).toEqual([{ start: 0, count: 3, height: 200 }]);
    const tight = justify(
      [
        { w: 0, h: 0 },
        { w: 10000, h: 10 },
        { w: 1, h: 1000 },
      ],
      850,
      200,
      0,
    );
    expect(tight[0].count).toBe(3);
    expect(tight[0].height).toBeCloseTo(850 / 4.5);
  });

  it('puts every item on its own row when there is no width yet', () => {
    expect(
      justify(
        [
          { w: 1, h: 1 },
          { w: 2, h: 1 },
        ],
        0,
        120,
        2,
      ),
    ).toEqual([
      { start: 0, count: 1, height: 120 },
      { start: 1, count: 1, height: 120 },
    ]);
  });

  it('returns no rows for no items', () => {
    expect(justify([], 600, 180, 4)).toEqual([]);
  });
});
