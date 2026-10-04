// Telegram voice waveforms are 5-bit samples packed little-endian into bytes; the Go API
// serialises the bytes as base64.

export function decodeWaveform(base64: string | undefined): number[] {
  if (!base64) return [];
  let bytes: Uint8Array;
  try {
    bytes = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0));
  } catch {
    return [];
  }
  const count = Math.floor((bytes.length * 8) / 5);
  const out: number[] = [];
  for (let i = 0; i < count; i++) {
    const bit = i * 5;
    const byte = bit >> 3;
    const word = bytes[byte] | ((bytes[byte + 1] ?? 0) << 8);
    out.push((word >> (bit & 7)) & 31);
  }
  return out;
}

/** Resamples to `bars` values by taking the peak of each bucket. */
export function resample(samples: number[], bars: number): number[] {
  if (bars <= 0) return [];
  if (samples.length === 0) return new Array(bars).fill(0);
  const out: number[] = [];
  for (let i = 0; i < bars; i++) {
    const start = Math.floor((i * samples.length) / bars);
    const end = Math.max(start + 1, Math.floor(((i + 1) * samples.length) / bars));
    let peak = 0;
    for (let j = start; j < end && j < samples.length; j++) peak = Math.max(peak, samples[j]);
    out.push(peak);
  }
  return out;
}

export const SPIKE_WIDTH = 2;
export const SPIKE_STEP = 4;
export const SPIKE_HEIGHT = 23;

/** Bar heights in px: proportional to the loudest bar, never below 2px. */
export function spikeHeights(values: number[]): number[] {
  const peak = Math.max(1, ...values);
  return values.map((v) => Math.max(2, Math.round((SPIKE_HEIGHT * v) / peak)));
}

/** Number of bars for a voice message of `duration` seconds (20–50). */
export function barCount(duration: number): number {
  return Math.max(20, Math.min(50, Math.round(duration * 2)));
}
