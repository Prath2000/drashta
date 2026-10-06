import { useCallback, useMemo, useState, type CSSProperties } from "react";
import { STATUS_COLOR, STATUS_LABEL } from "~/lib/format";
import { sameRef } from "~/lib/refs";
import type { Feature, Task, TaskStatus } from "~/lib/types";
import { useTaskPatch } from "./actions";
import { NewTaskForm } from "./NewTaskForm";
import { STATUSES, byStatus, fkey, featureMap, stepStatus, taskRef, type Group, type TrackerModel } from "./model";
import { TaskCard } from "./TaskCard";

/** A column shows this many cards, then a "Show all N" button (lists over ~200 rows are capped). */
export const CELL_CAP = 50;

interface Lane {
  key: string;
  title: string;
  projectName: string;
  countLabel: string;
  titled: boolean;
  tasks: Task[];
}

/**
 * Board view: five status columns, optional feature lanes, and the inline
 * "+ New task" form under each column header.
 */
export function Board({
  model,
  allFeatures,
  group,
  names,
  projects,
  defaultProject,
  item,
  onOpen,
}: {
  model: TrackerModel;
  allFeatures: Feature[];
  group: Group;
  names: Map<string, string>;
  projects: { id: string; name: string }[];
  defaultProject: string;
  /** The `?item=` ref that is open in the viewport (its card gets an amber border). */
  item: string | null;
  onOpen: (ref: string) => void;
}) {
  const patch = useTaskPatch();
  const [adding, setAdding] = useState<TaskStatus | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const feats = useMemo(() => featureMap(model.features), [model.features]);
  const totals = useMemo(() => byStatus(model.tasks), [model.tasks]);

  const move = useCallback(
    (t: Task, dir: -1 | 1) => {
      const next = stepStatus(t.status, dir);
      if (next) void patch(t, { status: next });
    },
    [patch],
  );
  const markDone = useCallback((t: Task) => void patch(t, { status: "done" }), [patch]);

  const lanes = useMemo<Lane[]>(() => {
    if (group !== "feature") {
      return [{ key: "all", title: "", projectName: "", countLabel: "", titled: false, tasks: model.tasks }];
    }
    const out: Lane[] = model.features.map((f) => {
      const tasks = model.tasks.filter((t) => t.projectId === f.projectId && t.featureId === f.id);
      return {
        key: fkey(f.projectId, f.id),
        title: f.title,
        projectName: names.get(f.projectId) ?? f.projectId,
        countLabel: tasks.length + (tasks.length === 1 ? " task" : " tasks"),
        titled: true,
        tasks,
      };
    });
    const orphans = model.tasks.filter((t) => !t.featureId || !feats.has(fkey(t.projectId, t.featureId)));
    if (orphans.length) {
      out.push({ key: "none", title: "No feature", projectName: "", countLabel: orphans.length + (orphans.length === 1 ? " task" : " tasks"), titled: true, tasks: orphans });
    }
    return out;
  }, [group, model.features, model.tasks, names, feats]);

  const featureTitle = (t: Task) => (t.featureId ? feats.get(fkey(t.projectId, t.featureId))?.title : undefined) ?? "No feature";

  return (
    <div className="tk-scroll" data-testid="tk-board">
      <div className="tk-board">
        <div className="tk-cols">
          {STATUSES.map((s) => (
            <div key={s} className="tk-colwrap" style={{ "--c": STATUS_COLOR[s] } as CSSProperties}>
              <div className="tk-colhead">
                <span className="tk-dot7" aria-hidden="true" />
                <h2 className="tk-colname">{STATUS_LABEL[s]}</h2>
                <span className="tk-colcount" data-testid={"count-" + s}>
                  {totals[s].length}
                </span>
                <button
                  type="button"
                  className="tk-newbtn tk-hit"
                  aria-label={"New task in " + STATUS_LABEL[s]}
                  aria-expanded={adding === s}
                  onClick={() => setAdding(adding === s ? null : s)}
                >
                  + New task
                </button>
              </div>
              {adding === s ? (
                <NewTaskForm
                  status={s}
                  defaultProject={defaultProject}
                  projects={projects}
                  features={allFeatures}
                  onClose={() => setAdding(null)}
                />
              ) : null}
            </div>
          ))}
        </div>

        {lanes.map((lane) => {
          const cols = byStatus(lane.tasks);
          return (
            <div key={lane.key} className="tk-lane" data-testid={"lane-" + lane.key}>
              {lane.titled ? (
                <div className="tk-lanehead">
                  <span className="tk-lanetitle">{lane.title}</span>
                  <span className="tk-lanemeta">{lane.projectName}</span>
                  <span className="tk-lanecount">{lane.countLabel}</span>
                </div>
              ) : null}
              <div className="tk-row">
                {STATUSES.map((s) => {
                  const all = cols[s];
                  const ek = lane.key + "|" + s;
                  const open = expanded.has(ek);
                  const shown = open ? all : all.slice(0, CELL_CAP);
                  return (
                    <div key={s} className="tk-cell" role="group" aria-label={STATUS_LABEL[s] + " tasks"} data-testid={"cell-" + s}>
                      {all.length === 0 ? (
                        <div className="tk-nothing" aria-hidden="true">
                          —
                        </div>
                      ) : null}
                      {all.length === 0 ? <span className="sr-only">no tasks</span> : null}
                      {shown.map((t) => (
                        <TaskCard
                          key={t.projectId + "/" + t.id}
                          task={t}
                          projectName={names.get(t.projectId) ?? t.projectId}
                          featureTitle={featureTitle(t)}
                          selected={item != null && sameRef(item, taskRef(t))}
                          onOpen={onOpen}
                          onMove={move}
                          onDone={markDone}
                        />
                      ))}
                      {all.length > CELL_CAP && !open ? (
                        <button
                          type="button"
                          className="tk-more"
                          onClick={() => setExpanded((prev) => new Set(prev).add(ek))}
                        >
                          Show all {all.length}
                        </button>
                      ) : null}
                    </div>
                  );
                })}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
