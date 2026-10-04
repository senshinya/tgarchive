// Album mosaic layout written from the documented behaviour of Telegram's grouped layout
// (fixed templates for 2–4 items, row packing for 5+); not a port of any Telegram source.

export const SIDE_TOP = 1;
export const SIDE_RIGHT = 2;
export const SIDE_BOTTOM = 4;
export const SIDE_LEFT = 8;

export interface Size {
  width: number;
  height: number;
}

export interface Tile {
  x: number;
  y: number;
  w: number;
  h: number;
  sides: number;
}

export interface AlbumLayout {
  width: number;
  height: number;
  tiles: Tile[];
}

export const ALBUM_WIDTH = 464;
const MIN_TILE_WIDTH = 100;

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

function ratioOf(s: Size): number {
  return s.width > 0 && s.height > 0 ? s.width / s.height : 1;
}

/** Lays out rows of item indices; every row spans the full width. */
function rowsLayout(rows: number[][], ratios: number[], width: number): AlbumLayout {
  const tiles: Tile[] = new Array(ratios.length);
  let y = 0;
  rows.forEach((row, ri) => {
    const sum = row.reduce((acc, i) => acc + ratios[i], 0);
    const h = Math.max(1, Math.round(width / sum));
    let x = 0;
    row.forEach((idx, ci) => {
      const last = ci === row.length - 1;
      const w = last ? width - x : Math.round(h * ratios[idx]);
      let sides = 0;
      if (ri === 0) sides |= SIDE_TOP;
      if (ri === rows.length - 1) sides |= SIDE_BOTTOM;
      if (ci === 0) sides |= SIDE_LEFT;
      if (last) sides |= SIDE_RIGHT;
      tiles[idx] = { x, y, w, h, sides };
      x += w;
    });
    y += h;
  });
  return { width, height: y, tiles };
}

/** One tall item on the left, the others stacked in a column on the right. */
function leftColumnLayout(ratios: number[], width: number): AlbumLayout | null {
  const [first, ...rest] = ratios;
  const inv = rest.reduce((acc, r) => acc + 1 / r, 0);
  const rightW = Math.round(width / (1 + first * inv));
  const leftW = width - rightW;
  if (rightW < MIN_TILE_WIDTH * 0.6 || leftW < MIN_TILE_WIDTH * 0.6) return null;
  const tiles: Tile[] = [];
  let y = 0;
  const heights = rest.map((r) => Math.max(1, Math.round(rightW / r)));
  heights.forEach((h, i) => {
    let sides = SIDE_RIGHT;
    if (i === 0) sides |= SIDE_TOP;
    if (i === heights.length - 1) sides |= SIDE_BOTTOM;
    tiles.push({ x: leftW, y, w: rightW, h, sides });
    y += h;
  });
  tiles.unshift({ x: 0, y: 0, w: leftW, h: y, sides: SIDE_TOP | SIDE_BOTTOM | SIDE_LEFT });
  return { width, height: y, tiles };
}

/** All ways to split n items into consecutive rows of 2–4 items (n ≤ 10 keeps this tiny). */
function compositions(n: number): number[][] {
  if (n === 0) return [[]];
  const out: number[][] = [];
  for (const k of [3, 2, 4]) {
    if (k > n) continue;
    for (const tail of compositions(n - k)) out.push([k, ...tail]);
  }
  return out;
}

function packedLayout(ratios: number[], width: number): AlbumLayout {
  const avg = ratios.reduce((a, b) => a + b, 0) / ratios.length;
  const cropped = ratios.map((r) => (avg > 1.1 ? clamp(r, 1, 2.75) : clamp(r, 0.6667, 1)));
  let best: { score: number; rows: number[][] } | null = null;
  for (const counts of compositions(cropped.length)) {
    const rows: number[][] = [];
    let i = 0;
    for (const c of counts) {
      rows.push(Array.from({ length: c }, (_, k) => i + k));
      i += c;
    }
    let height = 0;
    let tooNarrow = false;
    for (const row of rows) {
      const h = width / row.reduce((acc, idx) => acc + cropped[idx], 0);
      height += h;
      if (row.some((idx) => h * cropped[idx] < MIN_TILE_WIDTH)) tooNarrow = true;
    }
    const monotonic = counts.every((c, k) => k === 0 || c >= counts[k - 1]) || counts.every((c, k) => k === 0 || c <= counts[k - 1]);
    const score = Math.abs(height - width) + (tooNarrow ? width * 10 : 0) + (monotonic ? 0 : width * 0.2);
    if (!best || score < best.score) best = { score, rows };
  }
  return rowsLayout(best!.rows, cropped, width);
}

/**
 * Computes the mosaic for 2+ media of an album. Coordinates are integers in a canvas of
 * `width` px; render with percentages so the album scales down on narrow screens.
 */
export function layoutAlbum(sizes: Size[], width: number = ALBUM_WIDTH): AlbumLayout {
  const raw = sizes.map(ratioOf);
  const n = raw.length;
  if (n === 0) return { width, height: 0, tiles: [] };
  const r = raw.map((v) => clamp(v, 0.5, 3));
  if (n === 1) return rowsLayout([[0]], r, width);
  if (n === 2) {
    const similarWide = r[0] > 1.2 && r[1] > 1.2 && Math.abs(r[0] - r[1]) / Math.max(r[0], r[1]) < 0.3;
    return similarWide ? rowsLayout([[0], [1]], r, width) : rowsLayout([[0, 1]], r, width);
  }
  if (n === 3) {
    if (r[0] < 0.8) return leftColumnLayout(r, width) ?? rowsLayout([[0], [1, 2]], r, width);
    return rowsLayout([[0], [1, 2]], r, width);
  }
  if (n === 4) {
    if (r[0] >= 1.2) return rowsLayout([[0], [1, 2, 3]], r, width);
    return leftColumnLayout(r, width) ?? rowsLayout([[0, 1], [2, 3]], r, width);
  }
  return packedLayout(raw, width);
}

/** Single photo/video bubble size: fit inside maxWidth × maxHeight, never narrower than minWidth. */
export function fitMedia(size: Size, maxWidth = 464, maxHeight = 512, minWidth = 100): Size {
  const ratio = ratioOf(size);
  let width = Math.min(maxWidth, size.width > 0 ? size.width : maxWidth);
  let height = width / ratio;
  if (height > maxHeight) {
    height = maxHeight;
    width = height * ratio;
  }
  if (width < minWidth) {
    width = minWidth;
    height = Math.min(maxHeight, width / ratio);
  }
  return { width: Math.round(width), height: Math.round(height) };
}
