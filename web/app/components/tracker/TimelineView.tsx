import { useMemo, type CSSProperties } from "react";
import { STATUS_COLOR, STATUS_LABEL } from "~/lib/format";
import { useApi } from "~/lib/query";
import { sameRef } from "~/lib/refs";
import type { Feature, Home, Task } from "~/lib/types";
import {
  DAY,
  STATUSES,
  barFor,
  dayKeyOf,
  featureDue,
  fkey,
  fmtDay,
  taskRef,
  timelineGeometry,
  type TimelineGeometry,
  type TrackerModel,
} from "./model";

const pct = (v: number) => v.toFixed(3) + "%";

/** Daily activity from /home, summed over the chosen project(s): [files-ish, commits]. */
function activity(home: Home | undefined, project: string): Map<string, { f: number; c: number }> {
  const out = new Map<string, { f: number; c: number }>();
  if (!home) return out;
  for (const lane of home.lanes) {
    if (project !== "all" && lane.projectId !== project) continue;
    home.days.forEach((day, i) => {
      const d = lane.days[i];
      if (!d) return;
      const a = out.get(day) ?? { f: 0, c: 0 };
      a.f += d.files + d.sessions * 2 + d.snapshots + d.notes;
      a.c += d.commits;
      out.set(day, a);
    });
  }
  return out;
}

/** The seismograph wiggle over the past 14 days, as polyline points in a 1000 x 40 box. */
function trace(act: Map<string, { f: number; c: number }>, key: "f" | "c", g: TimelineGeometry): string {
  let max = 1;
  for (const a of act.values()) max = Math.max(max, a[key]);
  const pts: string[] = [];
  const endX = g.pos(g.today) * 10;
  for (let i = 0; i < 15; i++) {
    const t = g.winStart + i * DAY;
    const a = act.get(dayKeyOf(t)) ?? { f: 0, c: 0 };
    const amp = (a[key] / max) * 18;
    const x0 = g.pos(t) * 10;
    const steps = 6;
    for (let k = 0; k < steps; k++) {
      const x = x0 + (k / steps) * (1000 / 36);
      if (x > endX) break;
      const w = (k % 2 ? 1 : -1) * amp * (1 - Math.abs(k - steps / 2) / (steps / 2 + 1));
      pts.push(x.toFixed(1) + "," + (20 + w).toFixed(1));
    }
  }
  pts.push(endX.toFixed(1) + ",20");
  return pts.join(" ");
}

interface Row {
  key: string;
  title: string;
  meta: string;
  tasks: Task[];
  dueMs: (t: Task) => number | null;
}

/** Timeline view: features as rows, tasks as bars on a date axis, milestone due dates as diamonds. */
export function TimelineView({
  model,
  project,
  names,
  now,
  item,
  onOpen,
}: {
  model: TrackerModel;
  project: string;
  names: Map<string, string>;
  now: number;
  item: string | null;
  onOpen: (ref: string) => void;
}) {
  const g = useMemo(() => timelineGeometry(now), [now]);
  const { data: home } = useApi<Home>("/home?range=30");
  const act = useMemo(() => activity(home, project), [home, project]);
  const tremor = useMemo(() => trace(act, "f", g), [act, g]);
  const tremorCommits = useMemo(() => trace(act, "c", g), [act, g]);

  const ticks: { left: number; monday: boolean; label?: string }[] = [];
  for (let i = 0; i <= 36; i++) {
    const t = g.winStart + i * DAY;
    const monday = new Date(t).getUTCDay() === 1;
    ticks.push({ left: g.pos(t), monday, label: monday && i < 36 ? fmtDay(t).short : undefined });
  }

  const rows: Row[] = model.features.map((f: Feature) => {
    const tasks = model.tasks
      .filter((t) => t.projectId === f.projectId && t.featureId === f.id)
      .sort((a, b) => (Date.parse(a.created) || 0) - (Date.parse(b.created) || 0));
    const done = tasks.filter((t) => t.status === "done").length;
    const due = featureDue(f.projectId, f.id, model.milestones, model.features);
    return {
      key: fkey(f.projectId, f.id),
      title: f.title,
      meta: (names.get(f.projectId) ?? f.projectId) + " · " + done + "/" + tasks.length + " done",
      tasks,
      dueMs: () => due,
    };
  });
  const known = new Set(model.features.map((f) => fkey(f.projectId, f.id)));
  const orphans = model.tasks.filter((t) => !t.featureId || !known.has(fkey(t.projectId, t.featureId)));
  if (orphans.length) {
    rows.push({ key: "none", title: "No feature", meta: orphans.length + " tasks", tasks: orphans, dueMs: () => null });
  }

  const mondays = ticks.filter((t) => t.monday && t.label);
  const nowP = g.pos(now);

  return (
    <div className="tk-scroll tk-tl" data-testid="tk-timeline">
      <div className="tk-tl-inner">
        <div className="tk-tl-axis">
          <div className="tk-tl-label">
            <span className="tk-label">Feature · task</span>
            <span className="tk-meta">
              {fmtDay(g.winStart).short} → {fmtDay(g.winEnd - DAY).short}
            </span>
          </div>
          <div className="tk-tl-track">
            <svg viewBox="0 0 1000 40" preserveAspectRatio="none" aria-hidden="true" className="tk-tl-trace">
              <polyline points={tremor} fill="none" stroke="var(--dim)" strokeWidth="1" vectorEffect="non-scaling-stroke" />
              <polyline points={tremorCommits} fill="none" stroke="var(--amber)" strokeWidth="1" vectorEffect="non-scaling-stroke" />
            </svg>
            {ticks.map((t, i) => (
              <span key={i} className={"tk-tl-tick" + (t.monday ? " mon" : "")} style={{ left: pct(t.left) }} />
            ))}
            {mondays.map((t) => (
              <span key={t.label} className="tk-tl-tlabel" style={{ left: pct(t.left) }}>
                {t.label}
              </span>
            ))}
            {model.milestones.map((m) => {
              const due = m.due ? Date.parse(String(m.due).slice(0, 10) + "T12:00:00Z") : NaN;
              if (Number.isNaN(due)) return null;
              return (
                <span key={m.projectId + "/" + m.id} className="tk-tl-diamond" title={m.title + " · due " + m.due} style={{ left: pct(g.pos(due)) }}>
                  <span className="tk-tl-dm" />
                  <span className="tk-tl-dmlabel">{m.title}</span>
                </span>
              );
            })}
            <span className="tk-tl-nowtag" style={{ left: pct(nowP) }}>
              NOW
            </span>
          </div>
        </div>

        {rows.length === 0 ? <div className="tk-ms-none">No features for this project yet.</div> : null}

        {rows.map((row) => (
          <div key={row.key} className="tk-tl-row">
            <div className="tk-tl-rowhead">
              <span className="tk-tl-rowtitle">{row.title}</span>
              <span className="tk-label">{row.meta}</span>
            </div>
            <div className="tk-tl-rowtrack">
              {ticks
                .filter((t) => t.monday && t.label)
                .map((t) => (
                  <span key={t.label} className="tk-tl-grid" style={{ left: pct(t.left) }} />
                ))}
              {(() => {
                const due = row.tasks.length ? row.dueMs(row.tasks[0]) : null;
                return due != null ? <span className="tk-tl-msline" style={{ left: pct(g.pos(due)) }} /> : null;
              })()}
              <span className="tk-tl-nowline" style={{ left: pct(nowP) }} />
              {row.tasks.map((t) => {
                const bar = barFor(t, now, row.dueMs(t));
                const c = STATUS_COLOR[t.status] ?? "var(--dim)";
                const l = g.pos(bar.startMs);
                let r = g.pos(bar.endMs);
                if (r - l < 0.5) r = Math.min(100, l + 0.5);
                const hollow = t.status === "backlog" || t.status === "todo";
                const hasGhost = bar.dueMs != null && bar.dueMs > bar.endMs;
                const labelLeft = Math.max(r, hasGhost ? g.pos(bar.dueMs as number) : r);
                const onLeft = labelLeft > 70;
                const selected = item != null && sameRef(item, taskRef(t));
                const aria = t.id + " · " + t.title + " · " + STATUS_LABEL[t.status];
                const barStyle: CSSProperties = { left: pct(l), width: pct(r - l), "--c": c } as CSSProperties;
                const labelStyle: CSSProperties = onLeft
                  ? { right: "calc(" + pct(100 - l) + " + 6px)", textAlign: "right" }
                  : { left: "calc(" + pct(labelLeft) + " + 6px)", textAlign: "left" };
                return (
                  <div key={t.projectId + "/" + t.id} className="tk-tl-bar">
                    {hasGhost ? (
                      <span
                        className="tk-tl-ghost"
                        style={{ left: pct(r), width: pct(Math.max(0, g.pos(bar.dueMs as number) - r)), "--c": c } as CSSProperties}
                      />
                    ) : null}
                    <button
                      type="button"
                      className={"tk-tl-b" + (hollow ? " hollow" : "") + (t.status === "done" ? " done" : "") + (selected ? " sel" : "") + (bar.startMs < g.winStart ? " clipped" : "")}
                      style={barStyle}
                      aria-label={aria}
                      title={aria}
                      onClick={() => onOpen(taskRef(t))}
                    />
                    <button type="button" className={"tk-tl-name" + (selected ? " sel" : "")} style={labelStyle} onClick={() => onOpen(taskRef(t))}>
                      {t.id + "  " + t.title}
                    </button>
                  </div>
                );
              })}
              {row.tasks.length === 0 ? <div className="tk-tl-empty">no tasks</div> : null}
            </div>
          </div>
        ))}

        <div className="tk-tl-legend">
          {STATUSES.map((s) => (
            <span key={s} className="tk-tl-lg">
              <span className={"tk-tl-sw" + (s === "backlog" || s === "todo" ? " hollow" : "")} style={{ "--c": STATUS_COLOR[s] } as CSSProperties} />
              {STATUS_LABEL[s]}
            </span>
          ))}
          <span className="tk-tl-lg">
            <span className="tk-tl-lgcommit" />
            commit
          </span>
          <span className="tk-tl-lg">
            <span className="tk-tl-lgmile" />
            milestone due
          </span>
        </div>
      </div>
    </div>
  );
}
