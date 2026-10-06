import { memo } from "react";
import { useApi } from "~/lib/query";
import type { SnapshotsResponse, Task } from "~/lib/types";
import { snapshotsKey } from "./actions";
import { VERDICT_COLOR, VERDICT_LABEL, snapRef, sortVersions, stepStatus, suggestsDone, taskRef } from "./model";
import { STATUS_LABEL } from "~/lib/format";

interface CardProps {
  task: Task;
  projectName: string;
  featureTitle: string;
  selected: boolean;
  onOpen: (ref: string) => void;
  onMove: (task: Task, dir: -1 | 1) => void;
  onDone: (task: Task) => void;
}

const svg = { width: 12, height: 12, viewBox: "0 0 12 12", fill: "none", stroke: "currentColor", strokeWidth: 1.3, "aria-hidden": true } as const;

function LinkIcon() {
  return (
    <svg {...svg}>
      <path d="M2.5 1h4.5l2.5 2.5v7.5h-7z" />
      <path d="M7 1v2.5h2.5" />
    </svg>
  );
}

function NoteIcon() {
  return (
    <svg {...svg}>
      <path d="M6 1c2.8 0 4.5 1.9 4.5 4.3 0 2.4-2.5 4-4.5 4-.7 0-1.3-.1-1.8-.3L1.5 10.5l.8-2.2C1.8 7.5 1.5 6.4 1.5 5.3 1.5 2.9 3.2 1 6 1z" />
    </svg>
  );
}

/** Cheap compare so a refetch that changes nothing does not redraw every card. */
function sameTask(a: Task, b: Task): boolean {
  return (
    a.id === b.id &&
    a.projectId === b.projectId &&
    a.title === b.title &&
    a.status === b.status &&
    a.featureId === b.featureId &&
    a.assignee === b.assignee &&
    a.rev === b.rev &&
    a.linkCount === b.linkCount &&
    a.openNotes === b.openNotes &&
    a.latestSnapshot?.version === b.latestSnapshot?.version &&
    a.latestSnapshot?.verdict === b.latestSnapshot?.verdict
  );
}

function sameProps(a: CardProps, b: CardProps): boolean {
  return (
    sameTask(a.task, b.task) &&
    a.projectName === b.projectName &&
    a.featureTitle === b.featureTitle &&
    a.selected === b.selected &&
    a.onOpen === b.onOpen &&
    a.onMove === b.onMove &&
    a.onDone === b.onDone
  );
}

/** One card on the board (02-ui-spec §6.2). */
export const TaskCard = memo(function TaskCard({ task, projectName, featureTitle, selected, onOpen, onMove, onDone }: CardProps) {
  const prev = stepStatus(task.status, -1);
  const next = stepStatus(task.status, 1);
  const links = task.linkCount ?? 0;
  const notes = task.openNotes ?? 0;
  return (
    <div className={"tk-card" + (selected ? " sel" : "")} data-testid={"card-" + task.projectId + "-" + task.id} data-status={task.status}>
      <button type="button" className="tk-card-main" aria-label={"Open task " + task.id + ": " + task.title} onClick={() => onOpen(taskRef(task))}>
        <span className="tk-card-title">{task.title}</span>
        <span className="tk-chips">
          <span className="tk-chip">{projectName}</span>
          <span className="tk-chip feat">{featureTitle}</span>
        </span>
        <span className="tk-meta-row">
          <span className={task.assignee ? "" : "tk-unassigned"}>{task.assignee || "unassigned"}</span>
          <span className="tk-counts">
            {links > 0 ? (
              <span className="tk-count" title="links">
                <LinkIcon />
                <span className="sr-only">links </span>
                {links}
              </span>
            ) : null}
            {notes > 0 ? (
              <span className="tk-count notes" title="open notes">
                <NoteIcon />
                <span className="sr-only">open notes </span>
                {notes}
              </span>
            ) : null}
          </span>
        </span>
      </button>
      {task.latestSnapshot ? <CardSnap task={task} onOpen={onOpen} /> : null}
      {suggestsDone(task) ? (
        <div className="tk-suggest" role="group" aria-label={"Suggestion for " + task.id}>
          <span>Approved. Move to Done?</span>
          <button type="button" className="tk-sbtn" onClick={() => onDone(task)}>
            Move to Done
          </button>
        </div>
      ) : null}
      <div className="tk-foot">
        <button
          type="button"
          className="tk-move"
          disabled={!prev}
          aria-label={prev ? "Move " + task.id + " to " + STATUS_LABEL[prev] : "Already first status"}
          title={prev ? "Move to " + STATUS_LABEL[prev] : "Already first status"}
          onClick={() => onMove(task, -1)}
        >
          ←
        </button>
        <span className="tk-id">{task.id}</span>
        <button
          type="button"
          className="tk-move"
          disabled={!next}
          aria-label={next ? "Move " + task.id + " to " + STATUS_LABEL[next] : "Already done"}
          title={next ? "Move to " + STATUS_LABEL[next] : "Already done"}
          onClick={() => onMove(task, 1)}
        >
          →
        </button>
      </div>
    </div>
  );
}, sameProps);

/** The snapshot strip: latest thumbnail, version, verdict and one tick per version. */
function CardSnap({ task, onOpen }: { task: Task; onOpen: (ref: string) => void }) {
  const { data } = useApi<SnapshotsResponse>(snapshotsKey(task.projectId, task.id));
  const list = sortVersions(data?.snapshots ?? []);
  const latest = list[list.length - 1];
  const ls = task.latestSnapshot!;
  const version = latest?.version ?? ls.version;
  // The task answer is fresher than the snapshot list while a verdict is in flight.
  const verdict = latest && latest.version === ls.version ? ls.verdict : latest?.verdict ?? ls.verdict;
  const label =
    "Open latest snapshot " + (latest?.title ?? "") + " v" + version + " (" + VERDICT_LABEL[verdict] + ", " + Math.max(list.length, 1) + (list.length > 1 ? " versions)" : " version)");
  return (
    <button type="button" className="tk-strip" aria-label={label} title={label} onClick={() => onOpen(snapRef(task.projectId, task.id, version))}>
      {latest ? <img className="tk-thumb" src={latest.url} alt="" loading="lazy" /> : <span className="tk-thumb" />}
      <span className="tk-strip-text">
        <span className="tk-strip-line">
          <span className="tk-ivory">v{version}</span>
          <span className={"tk-vdot " + verdict} aria-hidden="true" />
          <span style={{ color: VERDICT_COLOR[verdict] }}>{VERDICT_LABEL[verdict]}</span>
        </span>
        <span className="tk-ticks" aria-hidden="true">
          {(list.length ? list : [{ version, verdict }]).map((s) => (
            <span
              key={s.version}
              title={"v" + s.version + " · " + VERDICT_LABEL[s.verdict]}
              className={"tk-tick " + s.verdict + (s.version === version ? " latest" : "")}
            />
          ))}
        </span>
      </span>
      <span className="tk-strip-go" aria-hidden="true">
        ↗
      </span>
    </button>
  );
}
