import { useMemo, useState } from "react";
import type { DayEvent } from "~/lib/types";
import { clock, groupByDay, isVerdict, plural, typeColor, verdictColor, verdictLabel } from "./util";

export type KindFilter = "all" | "commit" | "session" | "snapshot" | "task" | "note";

export const KINDS: { id: KindFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "commit", label: "Commits" },
  { id: "session", label: "Sessions" },
  { id: "snapshot", label: "Snapshots" },
  { id: "task", label: "Tasks" },
  { id: "note", label: "Notes" },
];

/** Rows drawn before the "Show all N" button. */
export const FEED_CAP = 250;

export interface ActivityFeedProps {
  kind: KindFilter;
  onKind: (k: KindFilter) => void;
  /** Loaded events for the chosen kind, newest first (hidden projects already removed). */
  events: DayEvent[];
  /** Count per kind over everything loaded. */
  counts: Record<KindFilter, number>;
  /** True when more pages exist (the counts then read "N+"). */
  partial: boolean;
  loading: boolean;
  error: unknown;
  onRetry: () => void;
  hasMore: boolean;
  loadingMore: boolean;
  moreError: string | null;
  onMore: () => void;
  nowMs: number;
  projectName: (id: string) => string;
  onOpen: (e: DayEvent) => void;
}

export function ActivityFeed(p: ActivityFeedProps) {
  const [showAll, setShowAll] = useState(false);
  const days = useMemo(() => groupByDay(showAll ? p.events : p.events.slice(0, FEED_CAP), p.nowMs), [p.events, p.nowMs, showAll]);
  const suffix = p.partial ? "+" : "";

  return (
    <>
      <div className="ix-activity-head">
        <span className="ix-head">Activity</span>
        <div role="group" aria-label="Activity kind" className="ix-kinds">
          {KINDS.map((k) => {
            const active = p.kind === k.id;
            return (
              <button
                key={k.id}
                type="button"
                aria-pressed={active}
                className={"ix-kind dr-hover" + (active ? " on" : "")}
                onClick={() => p.onKind(k.id)}
              >
                {k.id !== "all" ? <span className="ix-dot" style={{ background: typeColor(k.id) }} aria-hidden="true" /> : null}
                {k.label} <span className="ix-muted">{p.counts[k.id] + suffix}</span>
              </button>
            );
          })}
        </div>
      </div>
      <div className="ix-scroll ix-activity-scroll" data-testid="activity-scroll">
        {p.error && p.events.length === 0 ? (
          <div className="ix-empty" role="alert">
            <div className="ix-empty-title">Could not load the activity.</div>
            <div className="ix-empty-help">{p.error instanceof Error ? p.error.message : "request failed"}</div>
            <button type="button" className="ix-btn dr-hover" onClick={p.onRetry}>
              Retry
            </button>
          </div>
        ) : p.loading && p.events.length === 0 ? (
          <div className="ix-empty">
            <div className="ix-empty-help">Loading activity…</div>
          </div>
        ) : p.events.length === 0 ? (
          <div className="ix-none">
            {p.hasMore
              ? "None of this kind in the days loaded so far."
              : p.kind === "all"
                ? "No activity yet."
                : "No activity of this kind yet."}
          </div>
        ) : null}
        {days.map((d) => (
          <section key={d.key} aria-label={d.label + " " + d.sub}>
            <div className="ix-day">
              <span className="ix-day-label">{d.label}</span>
              <span className="ix-day-sub">{d.sub}</span>
              <span className="ix-day-rule" aria-hidden="true" />
              <span className="ix-muted">
                {d.events.length} {plural(d.events.length, "event", "events")}
              </span>
            </div>
            <ul className="ix-list">
              {d.events.map(({ ev, date }, i) => (
                <EventRow key={d.key + i + ev.ref + ev.ts + ev.label} ev={ev} time={clock(date)} projectName={p.projectName(ev.projectId)} onOpen={p.onOpen} />
              ))}
            </ul>
          </section>
        ))}
        {!showAll && p.events.length > FEED_CAP ? (
          <div className="ix-more">
            <button type="button" className="ix-btn dr-hover" onClick={() => setShowAll(true)}>
              Show all {p.events.length}
            </button>
          </div>
        ) : null}
        {p.hasMore ? (
          <div className="ix-more">
            <button type="button" className="ix-btn dr-hover" disabled={p.loadingMore} onClick={p.onMore}>
              {p.loadingMore ? "Loading…" : p.kind === "all" ? "Load more" : "Load more activity"}
            </button>
            {p.moreError ? <span className="ix-err">{p.moreError}</span> : null}
          </div>
        ) : null}
      </div>
    </>
  );
}

function EventRow({ ev, time, projectName, onOpen }: { ev: DayEvent; time: string; projectName: string; onOpen: (e: DayEvent) => void }) {
  const c = typeColor(ev.kind);
  const verdict = ev.kind === "snapshot" && isVerdict(ev.context) ? ev.context : null;
  const sub = verdict ? "" : ev.context ?? "";
  return (
    <li>
      <button type="button" className="ix-event dr-hover" data-testid="event-row" data-kind={ev.kind} onClick={() => onOpen(ev)}>
        <span className="ix-event-time">{time}</span>
        <span className="ix-tick" style={{ background: c }} aria-hidden="true" />
        <span className="ix-event-kind" style={{ color: c }}>
          {ev.kind}
        </span>
        <span className="ix-event-body">
          <span className="ix-event-line">
            <span className="ix-event-label">{ev.label}</span>
            {verdict ? (
              <span className="ix-chip" style={{ color: verdictColor(verdict), borderColor: verdictColor(verdict) }}>
                {verdictLabel(verdict)}
              </span>
            ) : null}
          </span>
          {sub ? <span className="ix-event-sub">{sub}</span> : null}
        </span>
        <span className="ix-event-proj">{projectName} ↗</span>
      </button>
    </li>
  );
}
