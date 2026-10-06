// Small text helpers shared by screens.

/** "just now", "4m ago", "3h ago", "2d ago"; "—" when the time is unknown. */
export function ago(iso: string | null | undefined, now: number = Date.now()): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "—";
  const m = Math.max(0, Math.round((now - t) / 60000));
  if (m < 1) return "just now";
  if (m < 60) return m + "m ago";
  if (m < 1440) return Math.floor(m / 60) + "h ago";
  return Math.floor(m / 1440) + "d ago";
}

/** "14:53" in the machine's local time. */
export function hhmm(iso: string | null | undefined): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const d = new Date(t); // local time of the machine, like the rest of the page
  return String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
}

/** Signal colour (CSS variable) per item / search kind. */
export const KIND_COLOR: Record<string, string> = {
  project: "var(--text)",
  file: "var(--muted)",
  doc: "var(--muted)",
  image: "var(--ivory)",
  commit: "var(--amber)",
  session: "var(--violet)",
  server: "var(--teal)",
  snapshot: "var(--ivory)",
  task: "var(--amber)",
  note: "var(--coral)",
  feature: "var(--muted)",
  milestone: "var(--muted)",
};

/** Task status colour (02-ui-spec §6.2). */
export const STATUS_COLOR: Record<string, string> = {
  backlog: "var(--st-backlog)",
  todo: "var(--st-todo)",
  in_progress: "var(--st-in-progress)",
  review: "var(--st-review)",
  done: "var(--st-done)",
};

export const STATUS_LABEL: Record<string, string> = {
  backlog: "Backlog",
  todo: "To do",
  in_progress: "In progress",
  review: "Review",
  done: "Done",
};
