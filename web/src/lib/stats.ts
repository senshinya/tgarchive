import type { DayCount, Stats } from '../api/types';

const DAY_MS = 86_400_000;

/** A YYYY-MM-DD day moved by n days. */
export function shiftDay(day: string, n: number): string {
  return new Date(Date.parse(`${day}T00:00:00Z`) + n * DAY_MS).toISOString().slice(0, 10);
}

/** Today in the browser's time zone, as YYYY-MM-DD. */
export function localToday(now = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
}

/** The activity heatmap: 53 weeks (columns) of Monday-first days ending with the week of today;
 * days after today are null. */
export function heatmapGrid(daily: DayCount[], today: string): ({ day: string; count: number } | null)[][] {
  const counts = new Map(daily.map((d) => [d.day, d.count]));
  const weekday = (new Date(`${today}T00:00:00Z`).getUTCDay() + 6) % 7; // Monday 0
  const first = shiftDay(today, -weekday - 52 * 7);
  const grid: ({ day: string; count: number } | null)[][] = [];
  for (let w = 0; w < 53; w++) {
    const week: ({ day: string; count: number } | null)[] = [];
    for (let d = 0; d < 7; d++) {
      const day = shiftDay(first, w * 7 + d);
      week.push(day > today ? null : { day, count: counts.get(day) ?? 0 });
    }
    grid.push(week);
  }
  return grid;
}

/** Heatmap shades: 0 for none, then 1–4 by the quartiles of the non-zero counts. */
export function quantileLevels(counts: number[]): (n: number) => number {
  const sorted = counts.filter((n) => n > 0).sort((a, b) => a - b);
  if (sorted.length === 0) return () => 0;
  if (sorted[0] === sorted[sorted.length - 1]) return (n) => (n > 0 ? 4 : 0);
  const at = (p: number) => sorted[Math.max(0, Math.ceil(sorted.length * p) - 1)];
  const [q1, q2, q3] = [at(0.25), at(0.5), at(0.75)];
  return (n) => (n <= 0 ? 0 : n <= q1 ? 1 : n <= q2 ? 2 : n <= q3 ? 3 : 4);
}

type Month = Stats['monthly'][number];

function nextMonth(m: string): string {
  const [y, mo] = m.split('-').map(Number);
  return mo === 12 ? `${y + 1}-01` : `${y}-${String(mo + 1).padStart(2, '0')}`;
}

/** Running totals per month, oldest first, with the months in between that had nothing. */
export function cumulative(monthly: Month[]): Month[] {
  const out: Month[] = [];
  const by = new Map(monthly.map((m) => [m.month, m]));
  if (monthly.length === 0) return out;
  let messages = 0;
  let bytes = 0;
  const last = monthly[monthly.length - 1].month;
  for (let m = monthly[0].month; m <= last; m = nextMonth(m)) {
    messages += by.get(m)?.messages ?? 0;
    bytes += by.get(m)?.media_bytes ?? 0;
    out.push({ month: m, messages, media_bytes: bytes });
  }
  return out;
}
