import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MAX_POSITIONS, POSITIONS_KEY, PREFS_KEY, PlayerStorage } from './playerStorage';

beforeEach(() => localStorage.clear());
afterEach(() => vi.restoreAllMocks());

function positions() {
  return JSON.parse(localStorage.getItem(POSITIONS_KEY) ?? '{}');
}

describe('PlayerStorage', () => {
  it('shares volume, muted and speed across videos', async () => {
    const a = new PlayerStorage(1, () => 100);
    await a.setVolume(0.4);
    await a.setMuted(true);
    await a.setPlaybackRate(1.5);
    const b = new PlayerStorage(2, () => 100);
    expect(await b.getVolume()).toBe(0.4);
    expect(await b.getMuted()).toBe(true);
    expect(await b.getPlaybackRate()).toBe(1.5);
  });

  it('does not remember the temporary long-press speed', async () => {
    const s = new PlayerStorage(1, () => 100);
    await s.setPlaybackRate(1.25);
    s.holdRate = true;
    await s.setPlaybackRate(2);
    expect(await s.getPlaybackRate()).toBe(1.25);
  });

  it('keeps a position only after 10s and more than 10s before the end', async () => {
    let t = 0;
    const s = new PlayerStorage(7, () => 100, () => (t += 5000));
    await s.setTime(5);
    expect(await s.getTime()).toBeNull();
    await s.setTime(42.7);
    expect(await s.getTime()).toBe(42);
    await s.setTime(95); // within the tail: finished, forget it
    expect(await s.getTime()).toBeNull();
    await s.setTime(42);
    await s.setTime(50, true); // ended
    expect(await s.getTime()).toBeNull();
  });

  it('ignores the reset to 0s while the player is torn down', async () => {
    const s = new PlayerStorage(7, () => 100, () => 1e6);
    await s.setTime(30);
    s.freeze();
    await s.setTime(0);
    await s.setVolume(0);
    expect(await s.getTime()).toBe(30);
    expect(await s.getVolume()).toBeNull();
  });

  it('throttles position writes but always applies a clear', async () => {
    let t = 0;
    const s = new PlayerStorage(7, () => 100, () => t);
    t = 10_000;
    await s.setTime(20);
    t = 10_500;
    await s.setTime(21); // too soon
    expect(positions()['7'].t).toBe(20);
    await s.setTime(1); // rewound to the start: cleared at once
    expect(positions()['7']).toBeUndefined();
  });

  it(`evicts the oldest positions beyond ${MAX_POSITIONS}`, async () => {
    const seed: Record<string, { t: number; at: number }> = {};
    for (let i = 1; i <= MAX_POSITIONS; i++) seed[i] = { t: 30, at: i };
    localStorage.setItem(POSITIONS_KEY, JSON.stringify(seed));
    await new PlayerStorage(9999, () => 100, () => 1e9).setTime(30);
    const all = positions();
    expect(Object.keys(all)).toHaveLength(MAX_POSITIONS);
    expect(all['1']).toBeUndefined();
    expect(all['9999'].t).toBe(30);
  });

  it('treats corrupt data and a throwing localStorage as nothing remembered', async () => {
    localStorage.setItem(PREFS_KEY, '{nope');
    localStorage.setItem(POSITIONS_KEY, '[1,2]');
    const s = new PlayerStorage(1, () => 100);
    expect(await s.getVolume()).toBeNull();
    expect(await s.getTime()).toBeNull();
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied');
    });
    await expect(s.setVolume(0.5)).resolves.toBeUndefined();
    await expect(s.setTime(30)).resolves.toBeUndefined();
    expect(await s.getMuted()).toBeNull();
  });
});
