import type { CSSProperties } from "react";
import { STATUS_COLOR, STATUS_LABEL } from "~/lib/format";
import { sameRef } from "~/lib/refs";
import type { Feature, Milestone, Task } from "~/lib/types";
import { QuickAdd } from "./QuickAdd";
import { STATUSES, fmtDay, leftovers, milestoneBlocks, taskRef, type TrackerModel } from "./model";

function FeatureCell({ feature, tasks, item, onOpen }: { feature: { title: string; summary?: string }; tasks: Task[]; item: string | null; onOpen: (ref: string) => void }) {
  const done = tasks.filter((t) => t.status === "done").length;
  return (
    <div className="tk-ms-feature">
      <div className="tk-ms-feature-head">
        <span className="tk-ms-feature-title">{feature.title}</span>
        <span className="tk-grow" />
        <span className="tk-meta">
          {done}/{tasks.length} done
        </span>
      </div>
      {feature.summary ? <span className="tk-meta tk-ms-summary">{feature.summary}</span> : null}
      <div className="tk-ms-tasks">
        {tasks.map((t) => (
          <button
            key={t.projectId + "/" + t.id}
            type="button"
            className={"tk-ms-task dr-hover" + (item != null && sameRef(item, taskRef(t)) ? " sel" : "")}
            onClick={() => onOpen(taskRef(t))}
          >
            <span className="tk-pill fixed" style={{ "--c": STATUS_COLOR[t.status] } as CSSProperties}>
              {STATUS_LABEL[t.status]}
            </span>
            <span className="tk-ms-task-title">{t.title}</span>
            <span className="tk-meta">{t.id}</span>
          </button>
        ))}
        {tasks.length === 0 ? <span className="tk-meta">No tasks yet.</span> : null}
      </div>
    </div>
  );
}

/** Milestones view: one block per milestone with progress, its features and their tasks. */
export function MilestonesView({
  model,
  allMilestones,
  projects,
  defaultProject,
  names,
  now,
  item,
  onOpen,
}: {
  model: TrackerModel;
  /** Milestones of every project (the feature form picks one of the chosen project). */
  allMilestones: Milestone[];
  projects: { id: string; name: string }[];
  defaultProject: string;
  names: Map<string, string>;
  now: number;
  item: string | null;
  onOpen: (ref: string) => void;
}) {
  const blocks = milestoneBlocks(model, now);
  const rest = leftovers(model);
  const outside = rest.outside;
  const empty = blocks.length === 0;
  return (
    <div className="tk-scroll tk-ms" data-testid="tk-milestones">
      <QuickAdd projects={projects} milestones={allMilestones} defaultProject={defaultProject} />
      {empty ? <div className="tk-ms-none">No milestones for this project yet.</div> : null}
      {blocks.map((b) => {
        const m = b.milestone;
        const order = STATUSES.slice().reverse();
        const segs = b.tasks.slice().sort((x, y) => order.indexOf(x.status) - order.indexOf(y.status));
        const dueMs = m.due ? Date.parse(String(m.due).slice(0, 10) + "T00:00:00Z") : NaN;
        const tone = b.tight ? "coral" : b.days != null && b.days <= 7 ? "amber" : "";
        return (
          <section key={m.projectId + "/" + m.id} className="tk-ms-block" aria-label={"Milestone " + m.title}>
            <div className="tk-ms-head">
              <div className="tk-ms-col">
                <span className="tk-label">{(names.get(m.projectId) ?? m.projectId) + " · milestone"}</span>
                <h2 className="tk-ms-title">{m.title}</h2>
              </div>
              <div className="tk-ms-col">
                <span className="tk-label">Due</span>
                <span className="tk-ms-val">{Number.isNaN(dueMs) ? "no date" : fmtDay(dueMs).long}</span>
              </div>
              <div className="tk-ms-col">
                <span className="tk-label">Remaining</span>
                <span className={"tk-ms-val " + tone}>{b.daysLabel}</span>
              </div>
              <div className="tk-ms-progress">
                <div className="tk-ms-progress-top">
                  <span className="tk-label">Progress</span>
                  <span className="tk-ms-val">
                    {b.done} / {b.total} done · {b.pct}%
                  </span>
                </div>
                <div className="tk-ms-bar" role="img" aria-label={b.done + " of " + b.total + " tasks done"}>
                  {segs.map((t) => (
                    <span
                      key={t.projectId + "/" + t.id}
                      title={t.id + " · " + STATUS_LABEL[t.status]}
                      className={"tk-seg-cell" + (t.status === "done" ? " done" : "")}
                      style={{ "--c": STATUS_COLOR[t.status] } as CSSProperties}
                    />
                  ))}
                </div>
              </div>
            </div>
            <div className="tk-ms-grid">
              {b.features.map((f) => (
                <FeatureCell key={f.feature.id} feature={f.feature} tasks={f.tasks} item={item} onOpen={onOpen} />
              ))}
            </div>
          </section>
        );
      })}

      {outside.length > 0 ? (
        <section className="tk-ms-block" aria-label="Outside milestones">
          <div className="tk-ms-head">
            <div className="tk-ms-col">
              <span className="tk-label">features with no milestone</span>
              <h2 className="tk-ms-title">Outside milestones</h2>
            </div>
          </div>
          <div className="tk-ms-grid">
            {outside.map((f: { feature: Feature; tasks: Task[] }) => (
              <FeatureCell key={f.feature.projectId + "/" + f.feature.id} feature={f.feature} tasks={f.tasks} item={item} onOpen={onOpen} />
            ))}
          </div>
        </section>
      ) : null}

      {rest.unscheduled.length > 0 ? (
        <section className="tk-ms-block" aria-label="Unscheduled">
          <div className="tk-ms-head">
            <div className="tk-ms-col">
              <span className="tk-label">tasks with no feature</span>
              <h2 className="tk-ms-title">Unscheduled</h2>
            </div>
          </div>
          <div className="tk-ms-grid">
            <FeatureCell feature={{ title: "No feature" }} tasks={rest.unscheduled} item={item} onOpen={onOpen} />
          </div>
        </section>
      ) : null}
    </div>
  );
}
