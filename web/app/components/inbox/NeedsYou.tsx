import { useState } from "react";
import { api, ApiError } from "~/lib/api";
import { invalidate } from "~/lib/query";
import { parseRef } from "~/lib/refs";
import { toast } from "~/lib/store";
import type { InboxProposal, Task } from "~/lib/types";
import { errText, type Unfiled } from "./useInbox";
import { plural } from "./util";

const UNFILED_CAP = 6;
const PROPOSAL_CAP = 3;

export interface DirtyProject {
  id: string;
  name: string;
  count: number;
}

export interface NeedsYouProps {
  awaitingReview: number;
  tasksInReview: number;
  openNotes: number;
  /** Visible (not hidden) projects with uncommitted files. */
  dirty: DirtyProject[];
  unfiled: Unfiled[];
  proposals: InboxProposal[];
  /** Every task (to pick a target for a drop, and to name a proposal's task). */
  tasks: Task[];
  /** False while the task list is still loading. */
  tasksReady: boolean;
  projectName: (id: string) => string;
  onSnapshots: () => void;
  onTasks: () => void;
  onNotes: () => void;
  onDirty: (projectId: string) => void;
  onOpenRef: (ref: string) => void;
}

/** The "Needs you" block: four count tiles, the Uncommitted list, unfiled drops and close-task proposals. */
export function NeedsYou(p: NeedsYouProps) {
  const tiles = [
    {
      key: "snaps",
      count: p.awaitingReview,
      label: plural(p.awaitingReview, "snapshot awaiting review", "snapshots awaiting review"),
      cta: "Open in tracker",
      color: "var(--ivory)",
      go: p.onSnapshots,
    },
    {
      key: "tasks",
      count: p.tasksInReview,
      label: plural(p.tasksInReview, "task in review", "tasks in review"),
      cta: "Open tracker",
      color: "var(--violet)",
      go: p.onTasks,
    },
    {
      key: "notes",
      count: p.openNotes,
      label: plural(p.openNotes, "open note", "open notes"),
      cta: "Show open",
      color: "var(--amber)",
      go: p.onNotes,
    },
    {
      key: "dirty",
      count: p.dirty.length,
      label: plural(p.dirty.length, "dirty working copy", "dirty working copies"),
      cta: p.dirty.length ? "Review changes" : "All clean",
      color: "var(--amber)",
      go: () => p.dirty[0] && p.onDirty(p.dirty[0].id),
      disabled: p.dirty.length === 0,
    },
  ];
  const nothing = tiles.every((t) => t.count === 0) && p.unfiled.length === 0 && p.proposals.length === 0;

  return (
    <section className="ix-needs" aria-label="Needs you">
      <div className="ix-head ix-needs-head">Needs you</div>
      <div className="ix-tiles">
        {tiles.map((t) => (
          <button key={t.key} type="button" className="ix-tile dr-hover" data-testid={"tile-" + t.key} disabled={t.disabled} onClick={t.go}>
            <span className="ix-tile-num" style={{ color: t.count > 0 ? t.color : "var(--dim)" }}>
              {t.count}
            </span>
            <span className="ix-tile-label">{t.label}</span>
            <span className="ix-tile-cta">{t.cta} →</span>
          </button>
        ))}
      </div>
      {nothing ? <div className="ix-nothing">Nothing needs you.</div> : null}
      {p.dirty.length ? (
        <div className="ix-line" role="group" aria-label="Uncommitted working copies">
          <span className="ix-label">Uncommitted</span>
          {p.dirty.map((d) => (
            <button key={d.id} type="button" className="ix-pill dr-hover" onClick={() => p.onDirty(d.id)}>
              <span className="ix-amber" aria-hidden="true">
                ●
              </span>
              {d.name} <span className="ix-muted">{d.count + " " + plural(d.count, "file", "files")}</span>
            </button>
          ))}
        </div>
      ) : null}
      {p.unfiled.length ? <UnfiledBlock items={p.unfiled} tasks={p.tasks} tasksReady={p.tasksReady} projectName={p.projectName} /> : null}
      {p.proposals.length ? <ProposalsBlock items={p.proposals} tasks={p.tasks} onOpenRef={p.onOpenRef} /> : null}
    </section>
  );
}

// ---------- unfiled snapshot drops ----------
const unfiledKey = (u: Unfiled) => u.projectId + "/" + u.name;

function UnfiledBlock({ items, tasks, tasksReady, projectName }: { items: Unfiled[]; tasks: Task[]; tasksReady: boolean; projectName: (id: string) => string }) {
  const [open, setOpen] = useState<string | null>(null);
  const [all, setAll] = useState(false);
  const shown = all ? items : items.slice(0, UNFILED_CAP);
  const sel = items.find((u) => unfiledKey(u) === open) ?? null;
  return (
    <div className="ix-block">
      <div className="ix-line" role="group" aria-label="Unfiled snapshots">
        <span className="ix-label">Unfiled</span>
        {shown.map((u) => {
          const k = unfiledKey(u);
          return (
            <button
              key={k}
              type="button"
              className={"ix-pill dr-hover" + (open === k ? " on" : "")}
              aria-expanded={open === k}
              onClick={() => setOpen(open === k ? null : k)}
            >
              <span className="ix-ivory" aria-hidden="true">
                ◐
              </span>
              {u.name} <span className="ix-muted">{projectName(u.projectId)}</span>
            </button>
          );
        })}
        {!all && items.length > UNFILED_CAP ? (
          <button type="button" className="ix-pill dr-hover" onClick={() => setAll(true)}>
            Show all {items.length}
          </button>
        ) : null}
      </div>
      {sel ? <FilePanel key={unfiledKey(sel)} item={sel} tasks={tasks} tasksReady={tasksReady} projectName={projectName(sel.projectId)} onClose={() => setOpen(null)} /> : null}
    </div>
  );
}

function FilePanel({ item, tasks, tasksReady, projectName, onClose }: { item: Unfiled; tasks: Task[]; tasksReady: boolean; projectName: string; onClose: () => void }) {
  const [taskId, setTaskId] = useState("");
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");
  const [noThumb, setNoThumb] = useState(false);
  const mine = tasks
    .filter((t) => t.projectId === item.projectId)
    .sort((a, b) => Number(a.status === "done") - Number(b.status === "done") || a.id.localeCompare(b.id, undefined, { numeric: true }));

  const file = async () => {
    if (!taskId) {
      setProblem("Pick a task to file it under.");
      return;
    }
    setBusy(true);
    setProblem("");
    try {
      const r = await api.fileInboxSnapshot(item.projectId, item.name, taskId);
      toast(item.name + " filed to " + taskId + (r.snapshot?.version ? " · v" + r.snapshot.version : ""));
      invalidate("/inbox");
      invalidate("/tracker");
      invalidate("/projects/" + encodeURIComponent(item.projectId));
      invalidate("/boot");
      onClose();
    } catch (e) {
      const m = errText(e);
      setProblem(m);
      toast("Could not file " + item.name + ": " + m, { error: true });
      setBusy(false);
    }
  };

  return (
    <div className="ix-file" role="group" aria-label={"File " + item.name}>
      {noThumb ? null : (
        <img className="ix-thumb" src={api.rawUrl(item.projectId, item.path)} alt="" onError={() => setNoThumb(true)} />
      )}
      <div className="ix-file-body">
        <div className="ix-file-title">
          File <span className="ix-text">{item.name}</span> from {projectName} into a task
        </div>
        {!tasksReady ? (
          <div className="ix-muted">Loading tasks…</div>
        ) : mine.length === 0 ? (
          <div className="ix-muted">{projectName} has no tasks yet. Create one in the tracker first.</div>
        ) : (
          <div className="ix-file-row">
            <select
              className="ix-select"
              aria-label={"Task for " + item.name}
              value={taskId}
              onChange={(e) => {
                setTaskId(e.target.value);
                setProblem("");
              }}
            >
              <option value="">Choose a task…</option>
              {mine.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.id} · {t.title} · {t.status.replace("_", " ")}
                </option>
              ))}
            </select>
            <button type="button" className="ix-btn ix-btn-amber dr-hover" disabled={busy} onClick={file}>
              {busy ? "Filing…" : "File snapshot"}
            </button>
            <button type="button" className="ix-btn dr-hover" disabled={busy} onClick={onClose}>
              Cancel
            </button>
          </div>
        )}
        {problem ? (
          <div className="ix-err" role="alert">
            {problem}
          </div>
        ) : null}
      </div>
    </div>
  );
}

// ---------- close-task proposals ----------
function ProposalsBlock({ items, tasks, onOpenRef }: { items: InboxProposal[]; tasks: Task[]; onOpenRef: (ref: string) => void }) {
  const [all, setAll] = useState(false);
  const shown = all ? items : items.slice(0, PROPOSAL_CAP);
  return (
    <div className="ix-block">
      <div className="ix-line" role="group" aria-label="Proposals">
        <span className="ix-label">Proposed</span>
        <div className="ix-props">
          {shown.map((p) => (
            <ProposalRow key={p.taskRef + "|" + p.commitRef} p={p} tasks={tasks} onOpenRef={onOpenRef} />
          ))}
          {!all && items.length > PROPOSAL_CAP ? (
            <button type="button" className="ix-pill dr-hover" onClick={() => setAll(true)}>
              Show all {items.length}
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function ProposalRow({ p, tasks, onOpenRef }: { p: InboxProposal; tasks: Task[]; onOpenRef: (ref: string) => void }) {
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const commit = parseRef(p.commitRef);
  const task = parseRef(p.taskRef, commit?.projectId ?? "");
  const found = task ? tasks.find((t) => t.projectId === task.projectId && t.id === task.id) : undefined;
  if (!task) return null;
  const taskRef = task.projectId + ":task:" + task.id;

  // A proposal never closes a task by itself: the button asks first, then
  // sends the status change with the task's current rev (04 §2.5).
  const close = async () => {
    setBusy(true);
    try {
      const d = await api.task(task.projectId, task.id);
      await api.updateTask(task.projectId, task.id, d.rev, { status: "done" });
      toast(task.id + " → done");
      invalidate("/inbox");
      invalidate("/tracker");
      invalidate("/projects/" + encodeURIComponent(task.projectId));
      invalidate("/boot");
      setConfirm(false);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        toast(task.id + " changed on disk, reloaded. Try again.", { error: true });
        invalidate("/tracker");
        invalidate("/inbox");
      } else {
        toast("Could not close " + task.id + ": " + errText(e), { error: true });
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="ix-prop" data-testid="proposal">
      <span className="ix-prop-text">
        <button type="button" className="ix-link dr-hover" onClick={() => onOpenRef(taskRef)}>
          {task.id}
          {found ? " " + found.title : ""}
        </button>
        <span className="ix-muted">
          {commit ? (
            <>
              {" "}
              looks done after{" "}
              <button type="button" className="ix-link dr-hover" onClick={() => onOpenRef(p.commitRef)}>
                commit {commit.id.slice(0, 7)}
              </button>
            </>
          ) : (
            " looks done"
          )}
        </span>
      </span>
      {confirm ? (
        <span className="ix-prop-act" role="group" aria-label={"Confirm closing " + task.id}>
          <span className="ix-text">Move {task.id} to done?</span>
          <button type="button" className="ix-btn ix-btn-amber dr-hover" disabled={busy} onClick={close}>
            {busy ? "Saving…" : "Move to done"}
          </button>
          <button type="button" className="ix-btn dr-hover" disabled={busy} onClick={() => setConfirm(false)}>
            Not now
          </button>
        </span>
      ) : (
        <button type="button" className="ix-btn ix-btn-amber dr-hover" onClick={() => setConfirm(true)}>
          Close task
        </button>
      )}
    </div>
  );
}
