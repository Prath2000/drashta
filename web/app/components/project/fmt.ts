import type { CSSProperties } from "react";

// Text helpers for the project page. They copy the frozen prototype
// (the frozen prototype (Project screen)) so the words and numbers match.

const MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const WD = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

function toDate(s: string | null | undefined): Date | null {
  if (!s) return null;
  const d = new Date(s);
  return Number.isNaN(d.getTime()) ? null : d;
}

export function ms(s: string | null | undefined): number {
  return toDate(s)?.getTime() ?? 0;
}

const pad = (n: number) => (n < 10 ? "0" : "") + n;

/** "14 Sep" (local time). */
export function fmtDate(s: string | null | undefined): string {
  const d = toDate(s);
  if (!d) return "—";
  return d.getDate() + " " + MON[d.getMonth()];
}

/** "14 Sep 09:41" (local time). */
export function fmtDT(s: string | null | undefined): string {
  const d = toDate(s);
  if (!d) return "—";
  return fmtDate(s) + " " + pad(d.getHours()) + ":" + pad(d.getMinutes());
}

/** "09:41" (local time). */
export function fmtTime(s: string | null | undefined): string {
  const d = toDate(s);
  if (!d) return "";
  return pad(d.getHours()) + ":" + pad(d.getMinutes());
}

/** "just now", "4m ago", "3h ago", "2d ago", then a date after two weeks. */
export function rel(s: string | null | undefined, now: number = Date.now()): string {
  const d = toDate(s);
  if (!d) return "—";
  const m = (now - d.getTime()) / 60000;
  if (m < 1) return "just now";
  if (m < 60) return Math.round(m) + "m ago";
  if (m < 60 * 24) return Math.round(m / 60) + "h ago";
  if (m < 60 * 24 * 14) return Math.round(m / 1440) + "d ago";
  return fmtDate(s);
}

/** Parts of a "YYYY-MM-DD" day key (UTC, so the key never shifts). */
export function dayParts(day: string): { d: number; mon: string; wd: string; dow: number } {
  const [y, m, d] = String(day).split("-").map(Number);
  const dt = new Date(Date.UTC(y || 1970, (m || 1) - 1, d || 1));
  return { d: dt.getUTCDate(), mon: MON[dt.getUTCMonth()], wd: WD[dt.getUTCDay()], dow: dt.getUTCDay() };
}

/** "820 B", "4.2 KB", "31 KB", "1.4 MB". */
export function size(n: number | null | undefined): string {
  if (n == null) return "";
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(n < 10240 ? 1 : 0) + " KB";
  return (n / 1048576).toFixed(1) + " MB";
}

/** "42m" or "3h 05m"; "—" without an end. */
export function dur(a: string | null | undefined, b: string | null | undefined): string {
  const s = toDate(a);
  const e = toDate(b);
  if (!s || !e) return "—";
  const m = Math.max(0, Math.round((e.getTime() - s.getTime()) / 60000));
  if (m < 60) return m + "m";
  return Math.floor(m / 60) + "h " + pad(m % 60) + "m";
}

/** Last path part. */
export function base(path: string): string {
  const i = path.lastIndexOf("/");
  return i < 0 ? path : path.slice(i + 1);
}

/** Host of a remote URL ("github.com"). */
export function hostOf(r: string | null | undefined): string {
  if (!r) return "";
  const m = String(r).match(/^(?:[a-z+]+:\/\/)?(?:[^@/]+@)?([^/:]+)/i);
  return m ? m[1] : String(r);
}

export const IMAGE_RE = /\.(png|jpe?g|gif|webp|svg|avif)$/i;
export const DOC_RE = /\.(md|txt)$/i;

/** Signal colour per item kind (02-ui-spec §8.1). */
export function kindColor(k: string): string {
  return (
    (
      {
        commit: "var(--amber)",
        session: "var(--violet)",
        snapshot: "var(--ivory)",
        note: "var(--coral)",
        task: "var(--amber)",
        file: "var(--muted)",
        image: "var(--ivory)",
        server: "var(--teal)",
      } as Record<string, string>
    )[k] ?? "var(--muted)"
  );
}

export function kindGlyph(k: string): string {
  return ({ commit: "◆", session: "✦", snapshot: "▣", note: "✎", task: "▮" } as Record<string, string>)[k] ?? "·";
}

export function statusColor(s: string): string {
  return (
    ({ backlog: "var(--dim)", todo: "var(--muted)", in_progress: "var(--amber)", review: "var(--violet)", done: "var(--teal)" } as Record<
      string,
      string
    >)[s] ?? "var(--muted)"
  );
}

export const statusLabel = (s: string) => String(s).replace("_", " ");

/** Style of a small outlined pill (status, vcs, kind). */
export function pill(color: string, dashed = false): CSSProperties {
  return {
    display: "inline-block",
    justifySelf: "start",
    padding: "2px 7px",
    border: "1px " + (dashed ? "dashed " : "solid ") + color,
    color,
    fontSize: 10,
    letterSpacing: ".1em",
    textTransform: "uppercase",
    borderRadius: 2,
    whiteSpace: "nowrap",
  };
}
