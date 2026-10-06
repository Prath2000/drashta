// Small pure helpers for the Inbox screen (dates, colours, note refs).
// Times follow the prototype: the day groups and the HH:MM use the browser's
// local time, and "now" is the server time from /boot (so the page never reads
// the clock by itself).
import { formatRef, parseRef } from "~/lib/refs";
import type { DayEvent, Note, Verdict } from "~/lib/types";

export const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
export const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

export function pad(n: number): string {
  return (n < 10 ? "0" : "") + n;
}

/** Local day key "YYYY-MM-DD". */
export function dayKey(d: Date): string {
  return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate());
}

/** "Thu 1 Oct". */
export function dateLabel(d: Date): string {
  return WEEKDAYS[d.getDay()] + " " + d.getDate() + " " + MONTHS[d.getMonth()];
}

/** "02:48" in local time. */
export function clock(d: Date): string {
  return pad(d.getHours()) + ":" + pad(d.getMinutes());
}

/** "as of Thu 1 Oct 02:48" (the CSS upper-cases it). */
export function asOfLabel(nowMs: number): string {
  const d = new Date(nowMs);
  return Number.isNaN(d.getTime()) ? "" : "as of " + dateLabel(d) + " " + clock(d);
}

/** "just now", "5m ago", "7h ago", "yesterday", "3d ago". Empty when the date is bad. */
export function relTime(iso: string | null | undefined, nowMs: number): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  const diff = (nowMs - t) / 60000;
  if (Number.isNaN(diff)) return "";
  if (diff < 1) return "just now";
  if (diff < 60) return Math.round(diff) + "m ago";
  if (diff < 1440) return Math.round(diff / 60) + "h ago";
  const days = Math.round(diff / 1440);
  return days === 1 ? "yesterday" : days + "d ago";
}

/** Signal colour (CSS variable) per item kind. Same table as the prototype. */
export function typeColor(t: string): string {
  const map: Record<string, string> = {
    commit: "var(--amber)",
    session: "var(--violet)",
    snapshot: "var(--ivory)",
    server: "var(--teal)",
    file: "var(--muted)",
    image: "var(--muted)",
    task: "var(--violet)",
    note: "var(--text)",
  };
  return map[t] ?? "var(--muted)";
}

export function verdictColor(v: string): string {
  return v === "approved" ? "var(--teal)" : v === "changes" ? "var(--coral)" : "var(--muted)";
}

export function verdictLabel(v: Verdict | string): string {
  return v === "approved" ? "Approved" : v === "changes" ? "Changes" : "Pending";
}

export function isVerdict(v: unknown): v is Verdict {
  return v === "pending" || v === "approved" || v === "changes";
}

// ---------- notes ----------
/** Stable key for a note. Ids (N-1) are only unique inside one project. */
export function noteKey(n: { projectId: string; id: string }): string {
  return n.projectId + "/" + n.id;
}

/** "line 30" or "pin 50% 90%" (empty when the note is not pinned). */
export function noteWhere(n: Note): string {
  if (typeof n.line === "number") return "line " + n.line;
  if (typeof n.x === "number" && typeof n.y === "number") return "pin " + Math.round(n.x * 100) + "% " + Math.round(n.y * 100) + "%";
  const frag = parseRef(n.ref, n.projectId)?.frag;
  if (frag) {
    const l = frag.match(/^L(\d+)(?:-L?(\d+))?$/);
    if (l) return l[2] ? "lines " + l[1] + "-" + l[2] : "line " + l[1];
    const p = frag.match(/^x=([\d.]+),y=([\d.]+)$/);
    if (p) return "pin " + Math.round(Number(p[1]) * 100) + "% " + Math.round(Number(p[2]) * 100) + "%";
  }
  return "";
}

/** The ref type of the item a note is on ("file", "commit", ...). */
export function noteType(n: Note): string {
  return parseRef(n.ref, n.projectId)?.type ?? "item";
}

/**
 * The ref to preview for a note: always the full form, and with the line or
 * pin as a fragment so the viewport can scroll to it (04 §2.4).
 */
export function noteOpenRef(n: Note): string {
  const r = parseRef(n.ref, n.projectId);
  if (!r) return n.ref;
  if (!r.frag) {
    if (typeof n.line === "number") r.frag = "L" + n.line;
    else if (typeof n.x === "number" && typeof n.y === "number") r.frag = "x=" + n.x + ",y=" + n.y;
  }
  return formatRef(r);
}

/** Full-form ref for an event, so the viewport URL always has the project. */
export function fullRef(ref: string, projectId: string): string {
  const r = parseRef(ref, projectId);
  return r ? formatRef(r) : ref;
}

// ---------- feed ----------
export interface DayGroup {
  key: string;
  label: string;
  sub: string;
  events: { ev: DayEvent; date: Date }[];
}

/** Groups newest-first events by local day, with Today / Yesterday labels. */
export function groupByDay(events: DayEvent[], nowMs: number): DayGroup[] {
  const now = new Date(nowMs);
  const todayKey = dayKey(now);
  const yKey = dayKey(new Date(nowMs - 86400000));
  const groups: DayGroup[] = [];
  const byKey = new Map<string, DayGroup>();
  for (const ev of events) {
    const date = new Date(ev.ts);
    if (Number.isNaN(date.getTime())) continue;
    const k = dayKey(date);
    let g = byKey.get(k);
    if (!g) {
      const recent = k === todayKey || k === yKey;
      g = { key: k, label: k === todayKey ? "Today" : k === yKey ? "Yesterday" : dateLabel(date), sub: recent ? dateLabel(date) : String(date.getFullYear()), events: [] };
      byKey.set(k, g);
      groups.push(g);
    }
    g.events.push({ ev, date });
  }
  return groups;
}

export function eventKey(e: DayEvent): string {
  return [e.ts, e.kind, e.projectId, e.ref, e.label].join("|");
}

/** Keeps the first item for each key. */
export function dedupe<T>(items: T[], key: (t: T) => string): T[] {
  const seen = new Set<string>();
  const out: T[] = [];
  for (const it of items) {
    const k = key(it);
    if (seen.has(k)) continue;
    seen.add(k);
    out.push(it);
  }
  return out;
}

export function plural(n: number, one: string, many: string): string {
  return n === 1 ? one : many;
}
