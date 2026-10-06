import { useCallback, useMemo, useState, type CSSProperties, type FormEvent } from "react";
import { api } from "~/lib/api";
import { STATUS_COLOR, STATUS_LABEL } from "~/lib/format";
import { useApi } from "~/lib/query";
import { toast } from "~/lib/store";
import type { Feature, Snapshot, SnapshotsResponse, Task, Verdict } from "~/lib/types";
import { refreshTask, snapshotsKey, useTaskPatch, useVerdict } from "./actions";
import {
  VERDICT_COLOR,
  VERDICT_LABEL,
  awaitingReview,
  fkey,
  msOf,
  relTime,
  snapRef,
  sortVersions,
  taskRef,
  tkey,
} from "./model";
import { SnapFrame, pinsOf } from "./SnapFrame";
import { VerdictBar } from "./VerdictBar";

function vDot(v: Verdict) {
  return <span className={"tk-vdot " + v} aria-hidden="true" />;
}

/**
 * "Awaiting review": every task whose latest snapshot is pending, each with
 * its versions, the image with pins, and the verdict bar (02-ui-spec §6.4).
 * A task you just decided stays on screen until you dismiss it, so the
 * "Move to Done" suggestion (A4) has a place to live.
 */
export function ReviewView({
  tasks,
  features,
  names,
  now,
  scopeName,
  onShowAll,
  onOpen,
}: {
  tasks: Task[];
  features: Map<string, Feature>;
  names: Map<string, string>;
  now: number;
  scopeName: string;
  onShowAll: () => void;
  onOpen: (ref: string) => void;
}) {
  const [settled, setSettled] = useState<Set<string>>(() => new Set());
  const rows = useMemo(
    () =>
      tasks
        .filter((t) => awaitingReview(t) || settled.has(tkey(t.projectId, t.id)))
        .sort((a, b) => (msOf(b.updated ?? b.created) ?? 0) - (msOf(a.updated ?? a.created) ?? 0)),
    [tasks, settled],
  );
  const waiting = rows.filter((t) => awaitingReview(t)).length;
  const lead = waiting
    ? waiting + (waiting === 1 ? " task whose latest snapshot is" : " tasks whose latest snapshot is") + " waiting for a verdict"
    : "no pending snapshots in " + scopeName;

  const mark = useCallback((k: string) => setSettled((s) => new Set(s).add(k)), []);
  const dismiss = useCallback(
    (k: string) =>
      setSettled((s) => {
        const n = new Set(s);
        n.delete(k);
        return n;
      }),
    [],
  );

  return (
    <div className="tk-scroll tk-review" data-testid="tk-review">
      <div className="tk-reviewhead">
        <span className="tk-cart">Awaiting review</span>
        <span className="tk-reviewlead">{lead}</span>
        <span className="tk-ruler" aria-hidden="true" />
      </div>

      {rows.length === 0 ? (
        <div className="tk-emptybox">
          <svg width="120" height="28" viewBox="0 0 120 28" aria-hidden="true">
            <path d="M0 14 H120" stroke="var(--hairline)" strokeWidth="1" />
            <path d="M10 10 V18 M30 10 V18 M50 10 V18 M70 10 V18 M90 10 V18 M110 10 V18" stroke="var(--hairline)" strokeWidth="1" />
          </svg>
          <div className="tk-emptytitle">Nothing awaiting review.</div>
          <div className="tk-emptybody">
            No task in {scopeName} has a pending latest snapshot. When a session or a teammate submits a new version for a task, it shows up here.
          </div>
          <button type="button" className="tk-ghost" onClick={onShowAll}>
            Show all tasks
          </button>
        </div>
      ) : null}

      {rows.map((t) => (
        <ReviewRow
          key={tkey(t.projectId, t.id)}
          task={t}
          projectName={names.get(t.projectId) ?? t.projectId}
          featureTitle={(t.featureId ? features.get(fkey(t.projectId, t.featureId))?.title : undefined) ?? "No feature"}
          now={now}
          settled={settled.has(tkey(t.projectId, t.id))}
          onSettled={mark}
          onDismiss={dismiss}
          onOpen={onOpen}
        />
      ))}
    </div>
  );
}

function ReviewRow({
  task,
  projectName,
  featureTitle,
  now,
  settled,
  onSettled,
  onDismiss,
  onOpen,
}: {
  task: Task;
  projectName: string;
  featureTitle: string;
  now: number;
  settled: boolean;
  onSettled: (key: string) => void;
  onDismiss: (key: string) => void;
  onOpen: (ref: string) => void;
}) {
  const k = tkey(task.projectId, task.id);
  const snaps = useApi<SnapshotsResponse>(snapshotsKey(task.projectId, task.id));
  const setVerdict = useVerdict();
  const patch = useTaskPatch();
  const all = useMemo(() => sortVersions(snaps.data?.snapshots ?? []), [snaps.data]);
  const latest = all[all.length - 1];
  const [selV, setSelV] = useState<number | null>(null);
  const [cmpV, setCmpV] = useState<number | null>(null);
  const [annotate, setAnnotate] = useState(false);
  const [draftPin, setDraftPin] = useState<{ x: number; y: number } | null>(null);
  const [pinText, setPinText] = useState("");
  const [hot, setHot] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const sel: Snapshot | undefined = all.find((s) => s.version === selV) ?? latest;
  const cmp = cmpV != null && cmpV !== sel?.version ? all.find((s) => s.version === cmpV) : undefined;
  const pins = pinsOf(sel?.notes);
  const openNotes = (sel?.notes ?? []).filter((n) => !n.resolved).length;
  const color = STATUS_COLOR[task.status] ?? "var(--muted)";
  const decided = !!latest && task.latestSnapshot != null && task.latestSnapshot.verdict !== "pending";

  const decide = async (v: Verdict, note?: string): Promise<boolean> => {
    if (!sel) return false;
    setBusy(true);
    const res = await setVerdict(task, sel, v, note);
    setBusy(false);
    if (res) {
      if (sel.version === latest?.version) onSettled(k);
      return true;
    }
    return false;
  };

  const addPin = async (e: FormEvent) => {
    e.preventDefault();
    const text = pinText.trim();
    if (!sel || !draftPin || !text) return;
    try {
      await api.addNote({ ref: snapRef(task.projectId, task.id, sel.version), text, x: draftPin.x, y: draftPin.y });
      toast("Note pinned");
      setDraftPin(null);
      setPinText("");
      refreshTask(task.projectId, task.id);
    } catch (err) {
      toast(err instanceof Error ? err.message : "could not pin the note", { error: true });
    }
  };

  const toggleNote = async (id: string, resolved: boolean, projectId: string) => {
    try {
      await api.updateNote(projectId, id, { resolved: !resolved });
      refreshTask(task.projectId, task.id);
    } catch (err) {
      toast(err instanceof Error ? err.message : "could not update the note", { error: true });
    }
  };

  const moveDone = async () => {
    if (await patch(task, { status: "done" })) onDismiss(k);
  };

  return (
    <section className="tk-rv" aria-label={"Task " + task.id + " awaiting review: " + task.title} data-testid={"review-" + task.projectId + "-" + task.id}>
      <div className="tk-rv-head">
        <div className="tk-rv-title">
          <span className="tk-label">
            {projectName} · {task.id} · {featureTitle}
          </span>
          <span className="tk-rv-name">{task.title}</span>
        </div>
        <span className="tk-pill" style={{ "--c": color } as CSSProperties}>
          {STATUS_LABEL[task.status]}
        </span>
        <button type="button" className="tk-ghost" onClick={() => onOpen(taskRef(task))}>
          Task detail →
        </button>
      </div>

      {!sel ? (
        <div className="tk-rv-loading">{snaps.error ? "could not load the versions." : "loading versions…"}</div>
      ) : (
        <>
          <div className="tk-rv-body">
            <div className="tk-stage">
              <div className="tk-stage-head">
                <span className="tk-stage-title">{sel.title}</span>
                <span className="tk-vchip">v{sel.version}</span>
                <span className="tk-vstatus" style={{ "--c": VERDICT_COLOR[sel.verdict] } as CSSProperties}>
                  {VERDICT_LABEL[sel.verdict]}
                </span>
                <span className="tk-grow" />
                <span className="tk-stage-by">
                  {sel.author} · {relTime(sel.captured, now)}
                </span>
              </div>

              <div className="tk-stage-tools">
                <button
                  type="button"
                  className="tk-tool"
                  aria-pressed={annotate}
                  disabled={!!cmp}
                  onClick={() => {
                    setAnnotate(!annotate);
                    setDraftPin(null);
                  }}
                >
                  Annotate
                </button>
                {all.length > 1 ? (
                  <label className="tk-cmp">
                    <span className="tk-label">Compare with</span>
                    <select
                      className="tk-input sm"
                      value={cmp ? String(cmp.version) : ""}
                      onChange={(e) => {
                        setCmpV(e.target.value ? Number(e.target.value) : null);
                        setAnnotate(false);
                        setDraftPin(null);
                      }}
                    >
                      <option value="">nothing</option>
                      {all
                        .filter((s) => s.version !== sel.version)
                        .map((s) => (
                          <option key={s.version} value={s.version}>
                            v{s.version} · {VERDICT_LABEL[s.verdict]}
                          </option>
                        ))}
                    </select>
                  </label>
                ) : null}
                {annotate ? <span className="tk-meta">Annotate mode — click anywhere on the frame to drop a pin.</span> : null}
              </div>

              {cmp ? (
                <div className="tk-compare" data-testid="compare">
                  <figure>
                    <figcaption>
                      v{sel.version} · {VERDICT_LABEL[sel.verdict]}
                    </figcaption>
                    <SnapFrame snap={sel} pins={pins} annotate={false} draftPin={null} hot={hot} onHot={setHot} onOpen={() => onOpen(snapRef(task.projectId, task.id, sel.version))} onDrop={() => undefined} />
                  </figure>
                  <figure>
                    <figcaption>
                      v{cmp.version} · {VERDICT_LABEL[cmp.verdict]}
                    </figcaption>
                    <SnapFrame snap={cmp} pins={pinsOf(cmp.notes)} annotate={false} draftPin={null} hot={hot} onHot={setHot} onOpen={() => onOpen(snapRef(task.projectId, task.id, cmp.version))} onDrop={() => undefined} />
                  </figure>
                </div>
              ) : (
                <SnapFrame
                  snap={sel}
                  pins={pins}
                  annotate={annotate}
                  draftPin={draftPin}
                  hot={hot}
                  onHot={setHot}
                  onOpen={() => onOpen(snapRef(task.projectId, task.id, sel.version))}
                  onDrop={(x, y) => setDraftPin({ x, y })}
                />
              )}

              {draftPin ? (
                <form className="tk-pinform" onSubmit={addPin} aria-label="New pin note">
                  <label className="sr-only" htmlFor={"pin-" + k}>
                    What should change here?
                  </label>
                  <input
                    id={"pin-" + k}
                    className="tk-input"
                    value={pinText}
                    placeholder="What should change here?"
                    autoFocus
                    onChange={(e) => setPinText(e.target.value)}
                  />
                  <button type="submit" className="tk-primary" disabled={!pinText.trim()}>
                    Pin note
                  </button>
                  <button type="button" className="tk-ghost" onClick={() => setDraftPin(null)}>
                    Cancel
                  </button>
                </form>
              ) : null}

              {sel.reviewNote ? (
                <div className="tk-reviewnote">
                  <span className="tk-label">Review note</span>
                  <span className="tk-quote">“{sel.reviewNote}”</span>
                </div>
              ) : null}

              {pins.length ? (
                <ol className="tk-pinlist" aria-label={"Notes pinned on v" + sel.version}>
                  {pins.map((p) => (
                    <li
                      key={p.note.id}
                      className={hot === p.note.id ? "hot" : ""}
                      onMouseEnter={() => setHot(p.note.id)}
                      onMouseLeave={() => setHot(null)}
                    >
                      <span className={"tk-pinno" + (p.note.resolved ? " resolved" : "")}>{p.n}</span>
                      <span className="tk-pintext">{p.note.text}</span>
                      <span className="tk-meta">{p.note.author}</span>
                      <button
                        type="button"
                        className="tk-ghost sm tk-hit"
                        onFocus={() => setHot(p.note.id)}
                        onBlur={() => setHot(null)}
                        onClick={() => void toggleNote(p.note.id, p.note.resolved, p.note.projectId)}
                      >
                        {p.note.resolved ? "Reopen" : "Resolve"}
                      </button>
                    </li>
                  ))}
                </ol>
              ) : null}
            </div>

            <div className="tk-versions">
              <div className="tk-versions-head">
                <span>Versions</span>
                <span>
                  {all.length} {all.length === 1 ? "version" : "versions"}
                </span>
              </div>
              <div className="tk-versions-list">
                {all
                  .slice()
                  .reverse()
                  .map((s) => {
                    const ns = (s.notes ?? []).filter((n) => !n.resolved).length;
                    return (
                      <button
                        key={s.version}
                        type="button"
                        className="tk-vrow"
                        aria-pressed={s.version === sel.version}
                        aria-label={"Show v" + s.version + ", " + VERDICT_LABEL[s.verdict]}
                        onClick={() => {
                          setSelV(s.version);
                          setDraftPin(null);
                        }}
                      >
                        <img className="tk-thumb lg" src={s.url} alt="" loading="lazy" />
                        <span className="tk-vrow-text">
                          <span className="tk-vrow-top">
                            <span className="tk-ivory">v{s.version + (s.version === latest?.version ? " · latest" : "")}</span>
                            <span className="tk-meta">{relTime(s.captured, now)}</span>
                          </span>
                          <span className="tk-vrow-bot">
                            {vDot(s.verdict)}
                            <span style={{ color: VERDICT_COLOR[s.verdict] }}>{VERDICT_LABEL[s.verdict]}</span>
                            {ns > 0 ? <span className="tk-meta">· {ns + (ns === 1 ? " open note" : " open notes")}</span> : null}
                          </span>
                        </span>
                      </button>
                    );
                  })}
              </div>
              <div className="tk-versions-foot">
                <span className="tk-meta">
                  {sel.notes?.length
                    ? openNotes + " open / " + sel.notes.length + (sel.notes.length === 1 ? " note" : " notes") + " pinned on v" + sel.version
                    : "No notes pinned on v" + sel.version + " yet"}
                </span>
                <button type="button" className="tk-ghost left" onClick={() => onOpen(snapRef(task.projectId, task.id, sel.version))}>
                  Open to annotate ↗
                </button>
              </div>
            </div>
          </div>

          <VerdictBar
            versionLabel={"v" + sel.version}
            verdict={sel.verdict}
            reviewNote={sel.reviewNote ?? ""}
            hint={sel.version === latest?.version ? "a verdict on the latest version takes this task off the review list" : "viewing an earlier version"}
            busy={busy}
            onSet={decide}
          />

          {settled && decided ? (
            <div className="tk-settled" role="status" data-testid={"settled-" + task.id}>
              {task.latestSnapshot!.verdict === "approved" ? (
                task.status === "done" ? (
                  <span>
                    v{task.latestSnapshot!.version} approved. {task.id} is done.
                  </span>
                ) : (
                  <>
                    <span>
                      v{task.latestSnapshot!.version} approved. Move {task.id} to Done?
                    </span>
                    <button type="button" className="tk-sbtn" onClick={() => void moveDone()}>
                      Move to Done
                    </button>
                  </>
                )
              ) : (
                <span>
                  Changes requested on v{task.latestSnapshot!.version}. {task.id} is now in {STATUS_LABEL[task.status]}.
                </span>
              )}
              <button type="button" className="tk-ghost sm tk-hit" onClick={() => onDismiss(k)}>
                Dismiss
              </button>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}
