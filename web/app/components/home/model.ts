// Pure data shaping for the Home screen: lane models, ordering, group counts,
// stable identities for live updates, and the day readout items.
// No React in here, so every rule can be tested with plain objects.
import { ago } from "~/lib/format";
import { formatRef, parseRef } from "~/lib/refs";
import type { DayEvent, DayResponse, Home, ProjectSummary } from "~/lib/types";
import { commitBarH, dayLabel, fileBarH, localHHMM, plural } from "./geometry";

export type Order = "recency" | "loudest";

/** Boot project plus two optional hints the API may send for the "empty directory" state. */
export type LaneProject = ProjectSummary & { empty?: boolean; fileCount?: number };

export interface ColModel {
  date: string;
  label: string;
  files: number;
  commits: number;
  sessions: number;
  snapshots: number;
  notes: number;
  plus: number;
  minus: number;
  /** Grey bar height in px (0 = none). */
  gh: number;
  /** Amber bar height in px (0 = none). */
  ah: number;
  aria: string;
}

export interface LaneModel {
  id: string;
  name: string;
  /** "personal", "work", "tools": the folder group, shown as is. */
  group: string;
  hidden: boolean;
  dirty: number;
  ago: string;
  live: number;
  /** No files, commits or sessions in the range. */
  flat: boolean;
  /** The folder has no files at all. */
  empty: boolean;
  /** File changes plus commit lines in the range (for ORDER loudest). */
  loud: number;
  lastMs: number;
  cols: ColModel[];
  /** Everything that changes how the lane looks. Equal sig = same drawing. */
  sig: string;
}

export const BASE_GROUPS = ["personal", "work", "tools"];

export function groupTag(group: string | undefined | null): string {
  return String(group ?? "");
}

/** The shared scale: the biggest file count and biggest line count over EVERY lane and day. */
export function sharedScale(home: Home): { maxFiles: number; maxLines: number } {
  let maxFiles = 1;
  let maxLines = 1;
  for (const l of home.lanes) {
    for (const c of l.days) {
      if (c.files > maxFiles) maxFiles = c.files;
      const lines = c.linesPlus + c.linesMinus;
      if (lines > maxLines) maxLines = lines;
    }
  }
  return { maxFiles, maxLines };
}

/** Count of distinct live ports. */
export function liveCount(ports: number[] | undefined | null): number {
  return new Set(ports ?? []).size;
}

function isEmptyDir(p: LaneProject | undefined, flat: boolean, lastChangeAt: string | null): boolean {
  if (!p) return false;
  if (p.empty === true || p.fileCount === 0) return true;
  // No hint from the server: a folder with no file ever touched, no VCS and no activity.
  const vcs = Array.isArray(p.vcs) ? p.vcs.length > 0 : !!p.vcs;
  return flat && !lastChangeAt && !vcs;
}

/** One model per lane in `home`, joined with the boot projects. */
export function buildLaneModels(home: Home, projects: ProjectSummary[], nowMs: number): LaneModel[] {
  const byId = new Map<string, LaneProject>();
  for (const p of projects) byId.set(p.id, p);
  const { maxFiles, maxLines } = sharedScale(home);
  return home.lanes.map((l) => {
    const p = byId.get(l.projectId);
    const name = p?.name ?? l.projectId;
    let rf = 0;
    let rc = 0;
    let rs = 0;
    let rl = 0;
    const cols: ColModel[] = home.days.map((date, i) => {
      const c = l.days[i];
      const files = c?.files ?? 0;
      const commits = c?.commits ?? 0;
      const sessions = c?.sessions ?? 0;
      const plus = c?.linesPlus ?? 0;
      const minus = c?.linesMinus ?? 0;
      const lines = plus + minus;
      rf += files;
      rc += commits;
      rs += sessions;
      rl += lines;
      const label = dayLabel(date);
      const parts = [plural(files, "file change"), plural(commits, "commit")];
      if (sessions) parts.push(plural(sessions, "session"));
      return {
        date,
        label,
        files,
        commits,
        sessions,
        snapshots: c?.snapshots ?? 0,
        notes: c?.notes ?? 0,
        plus,
        minus,
        gh: fileBarH(files, maxFiles),
        ah: commitBarH(commits, lines, maxLines),
        aria: name + ", " + label + ": " + parts.join(", "),
      };
    });
    const flat = rf + rc + rs === 0;
    const lastChangeAt = l.lastChangeAt ?? p?.lastChangeAt ?? null;
    const lastMs = lastChangeAt ? Date.parse(lastChangeAt) : NaN;
    const base = {
      id: l.projectId,
      name,
      group: groupTag(p?.group),
      hidden: !!p?.hidden,
      dirty: p?.dirtyCount ?? 0,
      ago: ago(lastChangeAt, nowMs),
      live: liveCount(l.live?.servers),
      flat,
      empty: isEmptyDir(p, flat, lastChangeAt),
      loud: rf + rl,
      lastMs: Number.isNaN(lastMs) ? 0 : lastMs,
      cols,
    };
    return { ...base, sig: JSON.stringify(base) };
  });
}

/**
 * Live updates: when new models arrive, a lane whose drawing did not change keeps
 * its old object. React.memo then skips it, so only the affected lane re-renders.
 */
export function stabilise(prev: ReadonlyMap<string, LaneModel>, next: LaneModel[]): LaneModel[] {
  return next.map((m) => {
    const old = prev.get(m.id);
    return old && old.sig === m.sig ? old : m;
  });
}

/** recency: newest change first. loudest: biggest total first, then recency. Ties fall back to the name, so the order is stable. */
export function orderLanes(models: LaneModel[], order: Order): LaneModel[] {
  return models.slice().sort((a, b) => {
    if (order === "loudest" && b.loud !== a.loud) return b.loud - a.loud;
    if (b.lastMs !== a.lastMs) return b.lastMs - a.lastMs;
    return a.name.localeCompare(b.name);
  });
}

export interface GroupChip {
  key: string;
  count: number;
}

/** Chips: all, then the known groups, then any other group found. Counts leave hidden folders out unless `showHidden`. */
export function groupChips(models: LaneModel[], showHidden: boolean): GroupChip[] {
  const scoped = models.filter((m) => showHidden || !m.hidden);
  const present = new Set(models.map((m) => m.group).filter(Boolean));
  const extra = Array.from(present).filter((g) => !BASE_GROUPS.includes(g)).sort();
  // Only groups that exist get a chip (a new install has none of the owner's folders).
  return [{ key: "all", count: scoped.length }].concat(
    [...BASE_GROUPS.filter((g) => present.has(g)), ...extra].map((g) => ({ key: g, count: scoped.filter((m) => m.group === g).length })),
  );
}

export interface LaneFilter {
  group: string;
  order: Order;
  showHidden: boolean;
}

/** The lanes to draw: hidden folders out (unless shown), one group, in the saved order. */
export function selectLanes(models: LaneModel[], f: LaneFilter): LaneModel[] {
  const scoped = models.filter((m) => (f.showHidden || !m.hidden) && (f.group === "all" || m.group === f.group));
  return orderLanes(scoped, f.order);
}

export function hiddenTotal(models: LaneModel[]): number {
  return models.filter((m) => m.hidden).length;
}

// ---------- day readout ----------
export const GLYPH: Record<string, string> = {
  commit: "◆",
  session: "✦",
  snapshot: "▣",
  note: "✎",
  task: "▮",
  server: "●",
};

export const KIND_VAR: Record<string, string> = {
  commit: "var(--amber)",
  session: "var(--violet)",
  snapshot: "var(--ivory)",
  note: "var(--coral)",
  task: "var(--amber)",
  server: "var(--teal)",
};

export interface DayItem {
  key: string;
  /** Full ref for openItem, or null when the server sent something we cannot parse. */
  ref: string | null;
  kind: string;
  time: string;
  glyph: string;
  color: string;
  lead: string;
  label: string;
  ctx: string;
  aria: string;
}

export interface DayFile {
  path: string;
  ref: string;
  aria: string;
}

export interface DayView {
  items: DayItem[];
  /** Items beyond ITEM_CAP, shown as "+N more items". */
  moreItems: number;
  files: DayFile[];
  moreFiles: number;
}

/** A day with more items than this is cut (the rest is a count). */
export const ITEM_CAP = 200;

const IMG = /\.(png|jpe?g|gif|webp|svg|avif)$/i;

function itemOf(ev: DayEvent, projectId: string, i: number): DayItem {
  const kind = String(ev.kind || "").toLowerCase();
  const r = parseRef(ev.ref, ev.projectId || projectId);
  let lead = kind || "item";
  let label = String(ev.label ?? "");
  if (kind === "commit" && r) {
    lead = r.id.slice(0, 7);
    if (label.startsWith(lead)) label = label.slice(lead.length).replace(/^\s+/, "");
  }
  const time = localHHMM(ev.ts);
  return {
    key: i + ":" + ev.ref,
    ref: r ? formatRef(r) : null,
    kind,
    time,
    glyph: GLYPH[kind] ?? "·",
    color: KIND_VAR[kind] ?? "var(--muted)",
    lead,
    label,
    ctx: ev.context ?? "",
    aria: (kind + " " + time + " " + label).replace(/\s+/g, " ").trim(),
  };
}

/** Items in time order (files go to the files list), then the first 20 paths touched that day. */
export function buildDayView(day: DayResponse, projectId: string, fileTotal: number): DayView {
  const events = (day.events ?? [])
    .map((ev, i) => ({ ev, i, ms: Date.parse(ev.ts) || 0 }))
    .filter((x) => String(x.ev.kind).toLowerCase() !== "file")
    .sort((a, b) => a.ms - b.ms || a.i - b.i);
  const items = events.slice(0, ITEM_CAP).map((x, n) => itemOf(x.ev, projectId, n));
  const moreItems = Math.max(0, events.length - items.length);
  const paths = (day.files ?? []).slice(0, 20);
  const files: DayFile[] = paths.map((path) => ({
    path,
    ref: formatRef({ type: IMG.test(path) ? "image" : "file", projectId, id: path }),
    aria: "file " + path,
  }));
  const more = day.moreFiles != null ? day.moreFiles : Math.max(0, fileTotal - files.length);
  return { items, moreItems, files, moreFiles: files.length > 0 ? more : 0 };
}

/** The value for the `lines` row of the counts zone. */
export function linesText(c: Pick<ColModel, "commits" | "plus" | "minus">): string {
  if (c.commits === 0) return "—";
  if (c.plus + c.minus === 0) return "no line stats";
  return "+" + c.plus + " −" + c.minus;
}

/** The sentence for a day with nothing to list ("" when there is something to show). */
export function quietText(c: Pick<ColModel, "files" | "commits">, itemCount: number): string {
  if (itemCount > 0) return "";
  if (c.files === 0) return "a flat line — no tremor this day.";
  if (c.commits === 0) return "files moved, nothing committed.";
  return "";
}
