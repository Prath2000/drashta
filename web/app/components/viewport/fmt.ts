// Small formatters used by the viewport bodies (ported from the prototype).

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const pad = (n: number) => (n < 10 ? "0" : "") + n;

function parse(s: string | null | undefined): Date | null {
  if (!s) return null;
  let x = String(s);
  if (/^\d{4}-\d\d-\d\d \d\d:\d\d(:\d\d)?$/.test(x)) x = x.replace(" ", "T") + "Z";
  const d = new Date(x);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** "30 Sep 2026, 13:06" (local time). */
export function fmtDate(s: string | null | undefined): string {
  const d = parse(s);
  if (!d) return s || "—";
  return d.getDate() + " " + MONTHS[d.getMonth()] + " " + d.getFullYear() + ", " + pad(d.getHours()) + ":" + pad(d.getMinutes());
}

/** "30 Sep 13:06" (local time). */
export function fmtWhen(s: string | null | undefined): string {
  const d = parse(s);
  if (!d) return s || "—";
  return d.getDate() + " " + MONTHS[d.getMonth()] + " " + pad(d.getHours()) + ":" + pad(d.getMinutes());
}

/** "13:06" (local time). */
export function fmtTime(s: string | null | undefined): string {
  const d = parse(s);
  return d ? pad(d.getHours()) + ":" + pad(d.getMinutes()) : "";
}

/** "4h 17m", "12m", "—". */
export function fmtDur(ms: number): string {
  if (!(ms > 0)) return "—";
  const m = Math.round(ms / 60000);
  if (m < 60) return m + "m";
  return Math.floor(m / 60) + "h " + pad(m % 60) + "m";
}

/** "4.2 KB". */
export function fmtSize(b: number | null | undefined): string {
  if (b == null) return "—";
  if (b < 1024) return b + " B";
  if (b < 1048576) return (b / 1024).toFixed(1) + " KB";
  return (b / 1048576).toFixed(1) + " MB";
}

/** "14h ago" style relative time (the prototype's rule: hours up to 48h). */
export function rel(s: string | null | undefined, now: number = Date.now()): string {
  const d = parse(s);
  if (!d) return "—";
  const m = Math.round((now - d.getTime()) / 60000);
  if (m < 1) return "just now";
  if (m < 60) return m + "m ago";
  const h = Math.round(m / 60);
  if (h < 48) return h + "h ago";
  const dd = Math.round(h / 24);
  if (dd < 60) return dd + "d ago";
  return Math.round(dd / 30) + "mo ago";
}

export const VERDICT_LABEL: Record<string, string> = { approved: "approved", changes: "changes requested", pending: "pending" };
export const VERDICT_COLOR: Record<string, string> = { approved: "var(--teal)", changes: "var(--coral)", pending: "var(--muted)" };
