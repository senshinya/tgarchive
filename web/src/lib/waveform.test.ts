import { describe, expect, it } from 'vitest';
import { barCount, decodeWaveform, resample, spikeHeights } from './waveform';

function pack(values: number[]): string {
  const bytes = new Uint8Array(Math.ceil((values.length * 5) / 8));
  values.forEach((v, i) => {
    const bit = i * 5;
    const word = (v & 31) << (bit & 7);
    bytes[bit >> 3] |= word & 0xff;
    if (word > 0xff) bytes[(bit >> 3) + 1] |= word >> 8;
  });
  return btoa(String.fromCharCode(...bytes));
}

describe('waveform', () => {
  it('decodes 5-bit little-endian packed samples', () => {
    const values = [0, 31, 7, 16, 1, 30, 12, 5];
    expect(decodeWaveform(pack(values))).toEqual(values);
  });

  it('returns no samples for missing or broken input', () => {
    expect(decodeWaveform(undefined)).toEqual([]);
    expect(decodeWaveform('***')).toEqual([]);
  });

  it('resamples by bucket peak', () => {
    expect(resample([1, 5, 2, 8, 3, 3], 3)).toEqual([5, 8, 3]);
    expect(resample([], 4)).toEqual([0, 0, 0, 0]);
    expect(resample([4], 3)).toEqual([4, 4, 4]);
  });

  it('scales to 23px with a 2px floor', () => {
    expect(spikeHeights([0, 15, 31])).toEqual([2, 11, 23]);
    expect(spikeHeights([0, 0])).toEqual([2, 2]);
  });

  it('chooses 20–50 bars by duration', () => {
    expect(barCount(1)).toBe(20);
    expect(barCount(15)).toBe(30);
    expect(barCount(600)).toBe(50);
  });
});
