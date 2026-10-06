// Pure helpers for the Tracker screen: keys, refs, labels, grouping and the
// milestone / timeline maths. No React here, so tests can call it directly.
import type { Feature, Milestone, Snapshot, Task, TaskStatus, TrackerResponse, Verdict } from "~/lib/types";

export const STATUSES: TaskStatus[] = ["backlog", "todo", "in_progress", "review", "done"];

export const DAY = 86_400_000;

export type Filter = "all" | "review";
export type View = "board" | "milestones" | "timeline";
export type Group = "none" | "feature";

/** Task ids repeat across projects (T-1 exists in many), so every key carries the project. */
export const tkey = (projectId: string, id: string) => projectId + "/" + id;
export const fkey = tkey;

/** Full ref string of a task: `acme-api:task:T-12`. */
export const taskRef = (t: { projectId: string; id: string }) => t.projectId + ":task:" + t.id;

/** Full ref string of a snapshot: `notes-app:snapshot:T-1/v3`. */
export const snapRef = (projectId: string, taskId: string, version: number) =>
  projectId + ":snapshot:" + taskId + "/v" + version;

export const VERDICT_COLOR: Record<Verdict, string> = {
  approved: "var(--teal)",
  changes: "var(--coral)",
  pending: "var(--muted)",
};

export const VERDICT_LABEL: Record<Verdict, string> = {
  approved: "Approved",
  changes: "Changes requested",
  pending: "Pending",
};

export const STATUS_WORD: Record<TaskStatus, string> = {
  backlog: "backlog",
  todo: "to do",
  in_progress: "in progress",
  review: "review",
  done: "done",
};

/** The status one step before / after, or null at the ends. */
export function stepStatus(s: TaskStatus, dir: -1 | 1): TaskStatus | null {
  const i = STATUSES.indexOf(s) + dir;
  return i >= 0 && i < STATUSES.length ? STATUSES[i] : null;
}

/** True when the latest snapshot is approved but the task is not done: suggest "Move to Done" (A4). */
export function suggestsDone(t: Pick<Task, "status" | "latestSnapshot">): boolean {
  return t.status !== "done" && t.latestSnapshot?.verdict === "approved";
}

export function awaitingReview(t: Pick<Task, "latestSnapshot">): boolean {
  return t.latestSnapshot?.verdict === "pending";
}

// ---------- time ----------
export function dayKeyOf(ms: number): string {
  return new Date(ms).toISOString().slice(0, 10);
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

/** "5 Oct" and "Mon 5 Oct 2026" (UTC). */
export function fmtDay(ms: number): { short: string; long: string } {
  const d = new Date(ms);
  const mon = MONTHS[d.getUTCMonth()];
  return {
    short: d.getUTCDate() + " " + mon,
    long: WEEKDAYS[d.getUTCDay()] + " " + d.getUTCDate() + " " + mon + " " + d.getUTCFullYear(),
  };
}

/** "just now", "12m ago", "3h ago", "yesterday", "5d ago". */
export function relTime(iso: string | null | undefined, nowMs: number): string {
  const t = Date.parse(iso ?? "");
  if (Number.isNaN(t)) return "";
  const m = (nowMs - t) / 60000;
  if (m < 1) return "just now";
  if (m < 60) return Math.round(m) + "m ago";
  if (m < 1440) return Math.round(m / 60) + "h ago";
  const d = Math.round(m / 1440);
  return d === 1 ? "yesterday" : d + "d ago";
}

export function msOf(iso: string | null | undefined): number | null {
  const t = Date.parse(iso ?? "");
  return Number.isNaN(t) ? null : t;
}

/** Midnight UTC of the day of `ms`. */
export function startOfDay(ms: number): number {
  return Date.parse(dayKeyOf(ms) + "T00:00:00Z");
}

// ---------- model ----------
export interface TrackerModel {
  milestones: Milestone[];
  features: Feature[];
  tasks: Task[];
  /** Projects that have any tracker data, in `order` (boot order), plus `extra`. */
  projectIds: string[];
}

/**
 * Cuts the all-projects tracker answer to one project (or all) and lists the
 * projects that have tracker data. `order` is the project order from boot.
 */
export function buildModel(
  data: TrackerResponse | undefined,
  project: string,
  order: string[],
): TrackerModel {
  const tasks = data?.tasks ?? [];
  const features = data?.features ?? [];
  const milestones = data?.milestones ?? [];
  const has = new Set<string>();
  for (const t of tasks) has.add(t.projectId);
  for (const f of features) has.add(f.projectId);
  for (const m of milestones) has.add(m.projectId);
  const ids = order.filter((id) => has.has(id));
  for (const id of has) if (!ids.includes(id)) ids.push(id);
  const inP = (p: string) => project === "all" || p === project;
  return {
    tasks: tasks.filter((t) => inP(t.projectId)),
    features: features.filter((f) => inP(f.projectId)),
    milestones: milestones.filter((m) => inP(m.projectId)),
    projectIds: ids,
  };
}

/** Index of features by `project/id`. */
export function featureMap(features: Feature[]): Map<string, Feature> {
  const m = new Map<string, Feature>();
  for (const f of features) m.set(fkey(f.projectId, f.id), f);
  return m;
}

/** Groups tasks into one array per status (stable order inside each column). */
export function byStatus(tasks: Task[]): Record<TaskStatus, Task[]> {
  const out = { backlog: [], todo: [], in_progress: [], review: [], done: [] } as Record<TaskStatus, Task[]>;
  for (const t of tasks) (out[t.status] ?? out.backlog).push(t);
  return out;
}

// ---------- milestones ----------
export interface MilestoneBlock {
  milestone: Milestone;
  features: { feature: Feature; tasks: Task[] }[];
  tasks: Task[];
  done: number;
  total: number;
  pct: number;
  days: number | null;
  daysLabel: string;
  tight: boolean;
}

export function daysLabel(days: number): string {
  if (days > 1) return days + " days left";
  if (days === 1) return "1 day left";
  if (days === 0) return "due today";
  return -days + (days === -1 ? " day" : " days") + " overdue";
}

/** Higher status index first (done on top), as the prototype lists tasks. */
function statusDesc(a: Task, b: Task): number {
  return STATUSES.indexOf(b.status) - STATUSES.indexOf(a.status);
}

export function milestoneBlocks(m: TrackerModel, nowMs: number): MilestoneBlock[] {
  const today = startOfDay(nowMs);
  const tasksOf = new Map<string, Task[]>();
  for (const t of m.tasks) {
    if (!t.featureId) continue;
    const k = fkey(t.projectId, t.featureId);
    const arr = tasksOf.get(k);
    if (arr) arr.push(t);
    else tasksOf.set(k, [t]);
  }
  const feats = featureMap(m.features);
  return m.milestones
    .slice()
    .sort((a, b) => String(a.due ?? "9999").localeCompare(String(b.due ?? "9999")))
    .map((ms) => {
      const ids = new Set<string>(ms.featureIds ?? []);
      for (const f of m.features) if (f.projectId === ms.projectId && f.milestoneId === ms.id) ids.add(f.id);
      const features = [...ids]
        .map((id) => feats.get(fkey(ms.projectId, id)))
        .filter((f): f is Feature => !!f)
        .map((feature) => ({ feature, tasks: (tasksOf.get(fkey(ms.projectId, feature.id)) ?? []).slice().sort(statusDesc) }));
      const tasks = features.flatMap((f) => f.tasks);
      const done = tasks.filter((t) => t.status === "done").length;
      const total = tasks.length;
      const dueMs = ms.due ? Date.parse(String(ms.due).slice(0, 10) + "T00:00:00Z") : NaN;
      const days = Number.isNaN(dueMs) ? null : Math.round((dueMs - today) / DAY);
      return {
        milestone: ms,
        features,
        tasks,
        done,
        total,
        pct: total ? Math.round((done / total) * 100) : 0,
        days,
        daysLabel: days == null ? "no date" : daysLabel(days),
        tight: days != null && (days < 0 || (days <= 2 && done < total)),
      };
    });
}

/** Features that belong to no milestone, and tasks that have no feature. */
export function leftovers(m: TrackerModel): { outside: { feature: Feature; tasks: Task[] }[]; unscheduled: Task[] } {
  const inMs = new Set<string>();
  for (const ms of m.milestones) {
    for (const id of ms.featureIds ?? []) inMs.add(fkey(ms.projectId, id));
    for (const f of m.features) if (f.projectId === ms.projectId && f.milestoneId === ms.id) inMs.add(fkey(f.projectId, f.id));
  }
  const feats = featureMap(m.features);
  const outside = m.features
    .filter((f) => !inMs.has(fkey(f.projectId, f.id)))
    .map((feature) => ({ feature, tasks: m.tasks.filter((t) => t.projectId === feature.projectId && t.featureId === feature.id).sort(statusDesc) }));
  const unscheduled = m.tasks.filter((t) => !t.featureId || !feats.has(fkey(t.projectId, t.featureId)));
  return { outside, unscheduled };
}

// ---------- snapshots ----------
/** Versions oldest to newest. */
export function sortVersions(list: Snapshot[]): Snapshot[] {
  return list.slice().sort((a, b) => a.version - b.version);
}

// ---------- timeline ----------
export const TL_BACK_DAYS = 14;
export const TL_FWD_DAYS = 22;

export interface TimelineGeometry {
  winStart: number;
  winEnd: number;
  today: number;
  pos: (ms: number) => number;
}

export function timelineGeometry(nowMs: number): TimelineGeometry {
  const today = startOfDay(nowMs);
  const winStart = today - TL_BACK_DAYS * DAY;
  const winEnd = today + TL_FWD_DAYS * DAY;
  const span = winEnd - winStart;
  return { winStart, winEnd, today, pos: (ms) => Math.max(0, Math.min(100, ((ms - winStart) / span) * 100)) };
}

export interface TimelineBar {
  task: Task;
  startMs: number;
  endMs: number;
  /** Task is not done and a milestone due date lies after its end: draw a dashed ghost to it. */
  dueMs: number | null;
}

/** Start at created; a done task ends at `updated` (or a day after created); others run up to now. */
export function barFor(t: Task, nowMs: number, dueMs: number | null): TimelineBar {
  const start = msOf(t.created) ?? nowMs;
  let end = nowMs;
  if (t.status === "done") {
    const u = msOf(t.updated);
    end = u != null && u >= start ? u : start + DAY;
  }
  if (end < start) end = start;
  return { task: t, startMs: start, endMs: end, dueMs: t.status !== "done" ? dueMs : null };
}

/** Due time (noon UTC) of the milestone that holds a feature, or null. */
export function featureDue(projectId: string, featureId: string, milestones: Milestone[], features: Feature[]): number | null {
  const f = features.find((x) => x.projectId === projectId && x.id === featureId);
  const ms = milestones.find(
    (m) => m.projectId === projectId && ((m.featureIds ?? []).includes(featureId) || (f?.milestoneId != null && f.milestoneId === m.id)),
  );
  if (!ms?.due) return null;
  const t = Date.parse(String(ms.due).slice(0, 10) + "T12:00:00Z");
  return Number.isNaN(t) ? null : t;
}
