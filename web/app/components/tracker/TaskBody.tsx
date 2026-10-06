import { useEffect, useId, useRef, useState, type CSSProperties, type FormEvent } from "react";
import type { ViewportBodyProps } from "~/components/viewport/registry";
import { api, ApiError } from "~/lib/api";
import { STATUS_LABEL } from "~/lib/format";
import { invalidate, setCached, useApi } from "~/lib/query";
import { formatRef, parseRef } from "~/lib/refs";
import { toast } from "~/lib/store";
import type { Note, SnapshotsResponse, Task, TaskDetailResponse, TaskStatus, TrackerResponse } from "~/lib/types";
import { useGo, useItem } from "~/lib/url";
import { TRACKER_KEY, refreshTask, snapshotsKey, taskDetailKey, useTaskPatch, type TaskFields } from "./actions";
import { STATUSES, VERDICT_COLOR, VERDICT_LABEL, fmtDay, snapRef, sortVersions, suggestsDone, taskRef } from "./model";
import "./tracker.css";

/** A text field that saves on blur (and on Enter for one-line fields). */
function BlurField({
  label,
  value,
  multiline,
  required,
  wide,
  onCommit,
}: {
  label: string;
  value: string;
  multiline?: boolean;
  required?: boolean;
  wide?: boolean;
  onCommit: (v: string) => void;
}) {
  const id = useId();
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const commit = () => {
    const v = multiline ? draft : draft.trim();
    if (required && !v) {
      setDraft(value);
      return;
    }
    if (v !== value) onCommit(v);
  };
  return (
    <div className={"tk-tb-field" + (wide ? " wide" : "")}>
      <label className="tk-field" htmlFor={id}>
        {label}
      </label>
      {multiline ? (
        <textarea id={id} className="tk-textarea tk-tb-desc" value={draft} onChange={(e) => setDraft(e.target.value)} onBlur={commit} />
      ) : (
        <input
          id={id}
          className="tk-input"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => {
            if (e.key === "Enter") e.currentTarget.blur();
            else if (e.key === "Escape") {
              setDraft(value);
              e.stopPropagation();
            }
          }}
        />
      )}
    </div>
  );
}

function errText(e: unknown, fallback: string): string {
  return e instanceof ApiError || e instanceof Error ? e.message : fallback;
}

/**
 * The task preview with edit fields (02-ui-spec §2.4 and §6.3): status, feature,
 * milestone, assignee, links, snapshot strip, notes, log. Title, assignee and
 * description save on blur, status and feature on change, always with `If-Match`.
 * Register it in the viewport registry as the `task` body.
 */
export function TaskBody({ refObj }: ViewportBodyProps) {
  const projectId = refObj.projectId;
  const taskId = refObj.id;
  const dkey = taskDetailKey(projectId, taskId);
  const detail = useApi<TaskDetailResponse>(dkey);
  const tracker = useApi<TrackerResponse>(TRACKER_KEY);
  const snaps = useApi<SnapshotsResponse>(snapshotsKey(projectId, taskId));
  const patch = useTaskPatch();
  const { openItem } = useItem();
  const go = useGo();
  const [linkText, setLinkText] = useState("");
  const [noteText, setNoteText] = useState("");
  const [snapText, setSnapText] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);

  const d = detail.data;
  if (!d) {
    const missing = detail.error instanceof ApiError && detail.error.status === 404;
    return (
      <div className="tk-tb" data-testid="task-body">
        <span className="dr-empty">{missing ? "This task isn't in the latest scan." : detail.error ? "could not load this task." : "loading the task…"}</span>
      </div>
    );
  }

  const task: Task = d.task;
  const features = (tracker.data?.features ?? []).filter((f) => f.projectId === projectId);
  const milestones = (tracker.data?.milestones ?? []).filter((m) => m.projectId === projectId);
  const feature = features.find((f) => f.id === task.featureId);
  const milestone = milestones.find((m) => (feature && (m.featureIds ?? []).includes(feature.id)) || (feature?.milestoneId != null && feature.milestoneId === m.id));
  const versions = sortVersions(snaps.data?.snapshots ?? d.snapshots ?? []);
  const latest = versions[versions.length - 1];

  const save = (fields: TaskFields) => patch(task, fields, { detail: { key: dkey, data: d }, rev: d.rev });

  const addLink = async (e: FormEvent) => {
    e.preventDefault();
    const text = linkText.trim();
    if (!text) return;
    const ref = parseRef(text, projectId);
    if (!ref) {
      toast("That is not a ref. Try commit:abc1234 or file:src/main.go#L12", { error: true });
      return;
    }
    try {
      await api.linkTask(projectId, taskId, formatRef(ref));
      toast("Linked " + formatRef(ref));
      setLinkText("");
      refreshTask(projectId, taskId);
    } catch (err) {
      toast(errText(err, "could not link"), { error: true });
    }
  };

  const unlink = async (ref: string, auto: boolean) => {
    // optimistic: the row goes at once
    setCached<TaskDetailResponse>(dkey, { ...d, links: d.links.filter((l) => l.ref !== ref) });
    try {
      await api.unlinkTask(projectId, taskId, ref);
      toast(auto ? "Auto link hidden" : "Link removed");
    } catch (err) {
      toast(errText(err, "could not remove the link"), { error: true });
    }
    refreshTask(projectId, taskId);
  };

  const addNote = async (e: FormEvent) => {
    e.preventDefault();
    const text = noteText.trim();
    if (!text) return;
    try {
      await api.addNote({ ref: taskRef(task), text });
      toast("Note added");
      setNoteText("");
      refreshTask(projectId, taskId);
    } catch (err) {
      toast(errText(err, "could not add the note"), { error: true });
    }
  };

  const toggleNote = async (n: Note) => {
    try {
      await api.updateNote(n.projectId || projectId, n.id, { resolved: !n.resolved });
      refreshTask(projectId, taskId);
    } catch (err) {
      toast(errText(err, "could not update the note"), { error: true });
    }
  };

  const snapshotAdded = (version: number) => {
    toast("Snapshot v" + version + " added");
    invalidate(snapshotsKey(projectId, taskId));
    refreshTask(projectId, taskId);
  };

  const addSnapFile = async (file: File) => {
    const form = new FormData();
    form.append("image", file);
    try {
      const res = await api.createSnapshot(projectId, taskId, form);
      snapshotAdded(res.snapshot.version);
    } catch (err) {
      toast(errText(err, "could not add the snapshot"), { error: true });
    }
  };

  const addSnapText = async (e: FormEvent) => {
    e.preventDefault();
    const text = snapText.trim();
    if (!text) return;
    try {
      const res = await api.createSnapshot(projectId, taskId, /^https?:\/\//i.test(text) ? { url: text } : { path: text });
      setSnapText("");
      snapshotAdded(res.snapshot.version);
    } catch (err) {
      toast(errText(err, "could not add the snapshot"), { error: true });
    }
  };

  const created = Date.parse(task.created);

  return (
    <div className="tk-tb" data-testid="task-body">
      {suggestsDone(task) ? (
        <div className="tk-tb-suggest" role="group" aria-label="Suggestion">
          <span>
            v{task.latestSnapshot!.version} approved. Move {task.id} to Done?
          </span>
          <button type="button" className="tk-sbtn" onClick={() => void save({ status: "done" })}>
            Move to Done
          </button>
        </div>
      ) : null}

      <div className="tk-tb-section">
        <div className="tk-tb-fields">
          <BlurField label="Title" value={task.title} required wide onCommit={(v) => void save({ title: v })} />
          <div className="tk-tb-field">
            <label className="tk-field" htmlFor={dkey + "-status"}>
              Status
            </label>
            <select
              id={dkey + "-status"}
              className="tk-input"
              value={task.status}
              onChange={(e) => void save({ status: e.target.value as TaskStatus })}
            >
              {STATUSES.map((s) => (
                <option key={s} value={s}>
                  {STATUS_LABEL[s]}
                </option>
              ))}
            </select>
          </div>
          <div className="tk-tb-field">
            <label className="tk-field" htmlFor={dkey + "-feature"}>
              Feature
            </label>
            <select
              id={dkey + "-feature"}
              className="tk-input"
              value={task.featureId ?? ""}
              onChange={(e) => void save({ featureId: e.target.value || null })}
            >
              <option value="">No feature</option>
              {features.map((f) => (
                <option key={f.id} value={f.id}>
                  {f.title}
                </option>
              ))}
            </select>
          </div>
          <div className="tk-tb-field">
            <span className="tk-field">Milestone</span>
            <span className="tk-tb-value" data-testid="task-milestone">
              {milestone ? milestone.title : "none"}
            </span>
          </div>
          <BlurField label="Assignee" value={task.assignee ?? ""} onCommit={(v) => void save({ assignee: v })} />
          <BlurField label="Description" value={task.body ?? ""} multiline wide onCommit={(v) => void save({ body: v })} />
        </div>
        <span className="tk-meta" data-testid="task-created">
          Created {Number.isNaN(created) ? "" : fmtDay(created).long}
          {task.updated ? " · updated " + (fmtDay(Date.parse(task.updated)).short) : ""}
        </span>
      </div>

      <div className="tk-tb-section">
        <span className="tk-label">Snapshots</span>
        {versions.length ? (
          <div className="tk-tb-snaps" data-testid="task-snaps">
            {versions.map((s) => (
              <button
                key={s.version}
                type="button"
                className="tk-tb-snap"
                aria-label={"Open " + s.title + " v" + s.version + " (" + VERDICT_LABEL[s.verdict] + ")"}
                onClick={() => openItem(snapRef(projectId, taskId, s.version))}
              >
                <img src={s.url} alt="" style={{ "--v": s.verdict === "pending" ? "var(--hairline)" : VERDICT_COLOR[s.verdict] } as CSSProperties} />
                <span>v{s.version}</span>
              </button>
            ))}
          </div>
        ) : (
          <span className="tk-meta">No snapshots yet.</span>
        )}
        {latest && latest.verdict === "pending" ? (
          <div>
            <button type="button" className="tk-ghost" onClick={() => go.tracker("review", projectId)}>
              Review v{latest.version} in the tracker →
            </button>
          </div>
        ) : null}
        <form className="tk-tb-add" onSubmit={addSnapText} aria-label="Add snapshot">
          <input ref={fileRef} type="file" accept="image/*" className="tk-file" aria-label="Snapshot image file" onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) void addSnapFile(f);
            e.target.value = "";
          }} />
          <button type="button" className="tk-ghost" onClick={() => fileRef.current?.click()}>
            Upload image
          </button>
          <input className="tk-input" value={snapText} placeholder="or a localhost URL or a file path" aria-label="Snapshot URL or path" onChange={(e) => setSnapText(e.target.value)} />
          <button type="submit" className="tk-ghost" disabled={!snapText.trim()}>
            Add snapshot
          </button>
        </form>
      </div>

      <div className="tk-tb-section">
        <span className="tk-label">Links</span>
        {d.links.length ? (
          <ul className="tk-tb-list" aria-label="Links">
            {d.links.map((l) => {
              const auto = l.source !== "manual";
              return (
                <li key={l.ref}>
                  <button type="button" className="tk-tb-linkbtn" onClick={() => {
                      const r = parseRef(l.ref, projectId);
                      openItem(r ? formatRef(r) : l.ref);
                    }}>
                    <span className="t">{l.resolved?.title ?? l.ref}</span>
                    <span className="tk-meta">{l.resolved?.subtitle ?? l.ref}</span>
                  </button>
                  <span className={"tk-badge" + (auto ? "" : " manual")}>{auto ? "auto: " + (l.reason ?? l.source) : "manual"}</span>
                  <button
                    type="button"
                    className="tk-ghost sm tk-hit"
                    aria-label={(auto ? "Hide auto link " : "Unlink ") + l.ref}
                    onClick={() => void unlink(l.ref, auto)}
                  >
                    {auto ? "Hide" : "Unlink"}
                  </button>
                </li>
              );
            })}
          </ul>
        ) : (
          <span className="tk-meta">Nothing linked yet.</span>
        )}
        <form className="tk-tb-add" onSubmit={addLink} aria-label="Add link">
          <input className="tk-input" value={linkText} placeholder="commit:abc1234 · file:src/main.go#L12 · server:8090" aria-label="Link a ref" onChange={(e) => setLinkText(e.target.value)} />
          <button type="submit" className="tk-ghost" disabled={!linkText.trim()}>
            Add link
          </button>
        </form>
      </div>

      <div className="tk-tb-section">
        <span className="tk-label">Notes</span>
        {d.notes.length ? (
          <ul className="tk-tb-list" aria-label="Notes">
            {d.notes.map((n) => (
              <li key={n.id}>
                <span className={"tk-pintext" + (n.resolved ? " tk-tb-note-resolved" : "")}>{n.text}</span>
                <span className="tk-meta">{n.author}</span>
                <button type="button" className="tk-ghost sm tk-hit" onClick={() => void toggleNote(n)}>
                  {n.resolved ? "Reopen" : "Resolve"}
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <span className="tk-meta">No notes on this task.</span>
        )}
        <form className="tk-tb-add" onSubmit={addNote} aria-label="Add note">
          <input className="tk-input" value={noteText} placeholder="Add a note" aria-label="Note text" onChange={(e) => setNoteText(e.target.value)} />
          <button type="submit" className="tk-ghost" disabled={!noteText.trim()}>
            Add note
          </button>
        </form>
      </div>

      {d.log?.length ? (
        <div className="tk-tb-section">
          <span className="tk-label">Log</span>
          <ul className="tk-tb-list tk-tb-log" aria-label="Log">
            {d.log.map((e, i) => (
              <li key={i}>
                {(e.ts ?? "").slice(0, 16).replace("T", " ")} {e.author} {e.text}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
