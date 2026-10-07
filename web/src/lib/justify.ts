/** One row of a justified layout: items start..start+count-1, all shown at height px. */
export interface JustifiedRow {
  start: number;
  count: number;
  height: number;
}

const MIN_RATIO = 0.5;
const MAX_RATIO = 3;

/** An item's width / height, square when unknown, clamped so a panorama or a strip stays usable. */
export function aspect(w: number, h: number): number {
  return Math.min(MAX_RATIO, Math.max(MIN_RATIO, w > 0 && h > 0 ? w / h : 1));
}

/** Lays items out in rows of equal height that fill width exactly (with gap between items): a
 * row closes as soon as fitting it to the width brings it down to targetH or below. The last,
 * unfinished row keeps targetH rather than being stretched. */
export function justify(items: { w: number; h: number }[], width: number, targetH: number, gap: number): JustifiedRow[] {
  const rows: JustifiedRow[] = [];
  if (width <= 0) return items.map((_, i) => ({ start: i, count: 1, height: targetH }));
  let start = 0;
  let sum = 0;
  for (let i = 0; i < items.length; i++) {
    sum += aspect(items[i].w, items[i].h);
    const count = i - start + 1;
    const height = (width - gap * (count - 1)) / sum;
    if (height <= targetH) {
      rows.push({ start, count, height });
      start = i + 1;
      sum = 0;
    }
  }
  if (start < items.length) rows.push({ start, count: items.length - start, height: targetH });
  return rows;
}
