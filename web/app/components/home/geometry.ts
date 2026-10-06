// Pure helpers for the seismograph: bar heights, day labels, axis ticks, capacity.
// Kept free of React so they are easy to test.

export const LANE_H = 36;
export const AXIS_H = 16;
export const PANEL_H = 128;
/** Tallest bar in px. The lane is 36px; the top 11px keep the session tick clear. */
export const BAR_MAX = 24;

const MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** Log-scale height: 0 for v = 0, else at least `min`, at most `h` (at v = max). */
export function logH(v: number, max: number, h: number, min: number): number {
  if (!(v > 0)) return 0;
  const m = Math.max(1, max);
  return Math.max(min, Math.round((Math.log1p(v) / Math.log1p(m)) * h));
}

/** Grey bar: file changes that day. Min 2px when above 0. */
export function fileBarH(files: number, maxFiles: number): number {
  return files > 0 ? logH(files, maxFiles, BAR_MAX, 2) : 0;
}

/**
 * Amber bar: commits that day. Height = lines changed (plus + minus), log scale,
 * min 4px. A day with commits but no line stats still draws at the minimum.
 */
export function commitBarH(commits: number, lines: number, maxLines: number): number {
  return commits > 0 ? Math.max(4, logH(lines, maxLines, BAR_MAX, 4)) : 0;
}

/** "2026-09-14" -> "14 Sep". */
export function dayLabel(key: string): string {
  const month = MON[parseInt(key.slice(5, 7), 10) - 1] ?? "";
  return parseInt(key.slice(8, 10), 10) + " " + month;
}

/** "2026-09-03" -> "03 Sep" (the axis upper-cases it with CSS). */
export function dayLabelPad(key: string): string {
  const month = MON[parseInt(key.slice(5, 7), 10) - 1] ?? "";
  return key.slice(8, 10) + " " + month;
}

export function plural(n: number, one: string, many?: string): string {
  return n + " " + (n === 1 ? one : many ?? one + "s");
}

/** Which day columns carry an axis label: all at 7 days, else every 7th counting back from today. */
export function axisLabelAt(n: number): number[] {
  const out: number[] = [];
  for (let i = 0; i < n; i++) if (n <= 7 || (n - 1 - i) % 7 === 0) out.push(i);
  return out;
}

export interface AxisTick {
  label: string;
  hasLabel: boolean;
  tickH: number;
}

/** Axis cells for a list of day keys. The last cell reads "today". */
export function buildAxis(days: string[]): AxisTick[] {
  const at = new Set(axisLabelAt(days.length));
  return days.map((key, i) => {
    const has = at.has(i);
    return { hasLabel: has, label: i === days.length - 1 ? "today" : dayLabelPad(key), tickH: has ? 6 : 3 };
  });
}

/** "14:53" in the local clock (the server and the page run on the same Mac). */
export function localHHMM(ts: string | null | undefined): string {
  if (!ts) return "";
  const ms = Date.parse(ts);
  if (Number.isNaN(ms)) return "";
  const d = new Date(ms);
  return String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
}

/**
 * Readout mode never scrolls. If the lanes do not fit, the last slot becomes the
 * "+N quieter projects" row. `capacity` is how many 36px rows fit under the axis.
 */
export function fitLanes<T>(items: T[], capacity: number): { shown: T[]; more: number } {
  if (!Number.isFinite(capacity) || items.length <= capacity) return { shown: items, more: 0 };
  const keep = Math.max(0, capacity - 1);
  return { shown: items.slice(0, keep), more: items.length - keep };
}

/** Rows that fit in a lanes region of height `h` (the date axis takes the first 16px). Unknown height = no limit. */
export function laneCapacity(h: number): number {
  if (!(h > 0)) return Infinity;
  return Math.max(0, Math.floor((h - AXIS_H) / LANE_H));
}
