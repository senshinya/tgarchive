import { describe, expect, it } from 'vitest';
import { cumulative, heatmapGrid, quantileLevels, shiftDay } from './stats';

describe('shiftDay', () => {
  it('moves across month and year ends', () => {
    expect(shiftDay('2024-03-01', -1)).toBe('2024-02-29');
    expect(shiftDay('2023-12-31', 1)).toBe('2024-01-01');
  });
});

describe('heatmapGrid', () => {
  it('lays out 53 Monday-first weeks ending with the week of today, with no days after today', () => {
    // 2026-10-07 is a Wednesday.
    const grid = heatmapGrid(
      [
        { day: '2026-10-07', count: 4 },
        { day: '2026-10-05', count: 1 },
        { day: '2025-10-06', count: 2 },
        { day: '2020-01-01', count: 9 },
      ],
      '2026-10-07',
    );
    expect(grid).toHaveLength(53);
    for (const week of grid) expect(week).toHaveLength(7);
    const last = grid[52];
    expect(last[0]).toEqual({ day: '2026-10-05', count: 1 });
    expect(last[2]).toEqual({ day: '2026-10-07', count: 4 });
    expect(last.slice(3)).toEqual([null, null, null, null]);
    expect(grid[0][0]).toEqual({ day: '2025-10-06', count: 2 });
    expect(grid[0][1]).toEqual({ day: '2025-10-07', count: 0 });
  });
});

describe('quantileLevels', () => {
  it('maps zero to 0 and the rest to 1–4 by quartiles of the non-zero counts', () => {
    const level = quantileLevels([0, 1, 2, 3, 4, 5, 6, 7, 8]);
    expect(level(0)).toBe(0);
    expect([1, 2, 3, 4, 5, 6, 7, 8].map(level)).toEqual([1, 1, 2, 2, 3, 3, 4, 4]);
  });

  it('gives every non-zero count the top level when all are equal', () => {
    const level = quantileLevels([3, 3, 3]);
    expect(level(3)).toBe(4);
    expect(level(0)).toBe(0);
  });
});

describe('cumulative', () => {
  it('runs totals month by month, filling months without any', () => {
    expect(
      cumulative([
        { month: '2023-11', messages: 2, media_bytes: 10 },
        { month: '2024-02', messages: 3, media_bytes: 0 },
      ]),
    ).toEqual([
      { month: '2023-11', messages: 2, media_bytes: 10 },
      { month: '2023-12', messages: 2, media_bytes: 10 },
      { month: '2024-01', messages: 2, media_bytes: 10 },
      { month: '2024-02', messages: 5, media_bytes: 10 },
    ]);
    expect(cumulative([])).toEqual([]);
  });
});
