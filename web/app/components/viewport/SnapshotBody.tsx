// Snapshot preview body (02-ui-spec §2.4 "snapshot", §6.4; prototype ViewMedia.dc.html).
// Annotated image with numbered pins, Annotate mode, version strip, Compare
// with a second version side by side, verdict controls, notes and the task.
import { useState } from "react";
import { api } from "~/lib/api";
import { invalidate, useApi } from "~/lib/query";
import { formatRef } from "~/lib/refs";
import { toast } from "~/lib/store";
import { STATUS_COLOR, STATUS_LABEL } from "~/lib/format";
import type { Snapshot, SnapshotsResponse, TaskDetailResponse, Verdict } from "~/lib/types";
import { useItem } from "~/lib/url";
import type { ViewportBodyProps } from "./registry";
import { addNote, baseRef, useNotes } from "./common";
import { fmtWhen, VERDICT_COLOR, VERDICT_LABEL } from "./fmt";
import { numberNotes, PinComposer, PinNotesList, PinStage } from "./PinStage";
import type { SnapshotObj, VerdictAnswer } from "./vtypes";

const enc = encodeURIComponent;

/** Image URL for a snapshot: the project file through /raw when we know the path, else its url. */
export function snapSrc(projectId: string, s: Partial<Snapshot> & { path?: string }): string {
  if (s.path) return api.rawUrl(projectId, s.path);
  return s.url ?? "";
}

export function SnapshotBody({ refObj, resolved }: ViewportBodyProps) {
  const p = resolved.projectId || refObj.projectId;
  const s = (resolved.obj ?? {}) as SnapshotObj;
  const ref = baseRef(refObj);
  const taskId = s.taskId ?? refObj.id.split("/")[0];
  const version = s.version ?? Number((refObj.id.split("/v")[1] ?? "0").replace(/\D/g, ""));
  const { openItem } = useItem();
  const notes = useNotes(ref, resolved.notes ?? []);
  const { pinned, ordered, numOf } = numberNotes(notes);

  const sibQ = useApi<SnapshotsResponse>(taskId ? "/projects/" + enc(p) + "/tasks/" + enc(taskId) + "/snapshots" : null);
  const taskQ = useApi<TaskDetailResponse>(taskId ? "/projects/" + enc(p) + "/tasks/" + enc(taskId) : null);
  const siblings = [...(sibQ.data?.snapshots ?? [])].sort((a, b) => a.version - b.version);
  const others = siblings.filter((x) => x.version !== version);
  const task = taskQ.data?.task;

  const [annotate, setAnnotate] = useState(false);
  const [pending, setPending] = useState<{ x: number; y: number } | null>(null);
  const [active, setActive] = useState<string | null>(null);
  const [compare, setCompare] = useState(false);
  const [cmpV, setCmpV] = useState<number | null>(null);
  const [swapped, setSwapped] = useState(false);
  const [changesOpen, setChangesOpen] = useState(false);
  const [vDraft, setVDraft] = useState("");

  const cur = siblings.find((x) => x.version === version);
  const verdict: Verdict = (cur?.verdict ?? s.verdict ?? "pending") as Verdict;
  const reviewNote = cur?.reviewNote ?? s.reviewNote ?? "";

  const older = others.filter((x) => x.version < version);
  const defaultCmp = (older.length ? older[older.length - 1] : others[0])?.version ?? null;
  const cmpVersion = others.some((x) => x.version === cmpV) ? cmpV : defaultCmp;
  const cmpSnap = others.find((x) => x.version === cmpVersion);
  const compareOn = compare && !!cmpSnap;
  const self: Partial<Snapshot> & { path?: string } = cur ?? s;
  let L: Partial<Snapshot> & { path?: string } = cmpSnap ?? self;
  let R: Partial<Snapshot> & { path?: string } = self;
  if (cmpSnap && cmpSnap.version > version) {
    L = self;
    R = cmpSnap;
  }
  if (swapped) [L, R] = [R, L];

  const snapRef = (v: number) => formatRef({ type: "snapshot", projectId: p, id: taskId + "/v" + v });

  const setVerdict = async (v: Verdict, note?: string) => {
    try {
      const out = (await api.setVerdict(p, taskId, version, v, note)) as VerdictAnswer;
      invalidate("/tracker");
      invalidate("/resolve");
      invalidate("/projects/" + enc(p) + "/tasks");
      setChangesOpen(false);
      setVDraft("");
      let msg = "v" + version + " → " + VERDICT_LABEL[v];
      if (out.suggestion?.status === "done") msg += " · " + taskId + " can move to done";
      toast(msg);
    } catch (e) {
      toast(e instanceof Error ? e.message : "could not set the verdict", { error: true });
    }
  };

  return (
    <div className="vp-scroll" data-testid="snapshot-body">
      <div className="vp-pad">
        <div className="vp-stack">
          <div className="vp-row" style={{ justifyContent: "space-between", alignItems: "flex-end", gap: 12 }}>
            <div className="vp-col" style={{ gap: 2, minWidth: 0 }}>
              <div className="vp-label">
                Snapshot · v{version} · {s.author ?? "—"} · {fmtWhen(s.captured ?? resolved.date)}
              </div>
              <div className="vp-title">{s.title ?? resolved.title}</div>
            </div>
            <div className="vp-row" style={{ gap: 6 }}>
              <button
                type="button"
                className={"vp-btn" + (annotate ? " coral-fill" : "")}
                aria-pressed={annotate}
                onClick={() => {
                  setAnnotate((a) => !a);
                  setCompare(false);
                  if (annotate) setPending(null);
                }}
              >
                {annotate ? "● Annotating" : "Annotate"}
              </button>
              {others.length ? (
                <button
                  type="button"
                  className={"vp-btn" + (compareOn ? " ivory-fill" : "")}
                  aria-pressed={compareOn}
                  onClick={() => {
                    setCompare((c) => !c);
                    setAnnotate(false);
                    setPending(null);
                  }}
                >
                  Compare
                </button>
              ) : null}
            </div>
          </div>

          {!compareOn ? (
            <div className="vp-snapframe">
              <PinStage
                src={snapSrc(p, self)}
                alt={s.title ?? "snapshot"}
                pinned={pinned}
                numOf={numOf}
                activeId={active}
                onPick={(id) => setActive((a) => (a === id ? null : id))}
                annotate={annotate}
                pending={pending}
                pendingNum={pinned.length + 1}
                onDrop={setPending}
              />
              {annotate ? <div className="vp-annot-hint">Annotate mode — click anywhere on the frame to drop a pin.</div> : null}
            </div>
          ) : (
            <div className="vp-col" style={{ gap: 8 }} data-testid="compare">
              <div className="vp-row">
                <label htmlFor="vm-cmp" className="vp-label">
                  Compare with
                </label>
                <select id="vm-cmp" className="vp-input" value={String(cmpVersion ?? "")} onChange={(e) => setCmpV(Number(e.target.value))}>
                  {others.map((x) => (
                    <option key={x.version} value={x.version}>
                      {"v" + x.version + " · " + VERDICT_LABEL[x.verdict]}
                    </option>
                  ))}
                </select>
                <button type="button" className="vp-btn" style={{ marginLeft: "auto" }} onClick={() => setSwapped((w) => !w)}>
                  ⇄ Swap
                </button>
              </div>
              <div className="vp-cmp">
                {[L, R].map((x, i) => (
                  <div key={i}>
                    <img src={snapSrc(p, x)} alt={"v" + x.version} />
                    <div className="vp-row" style={{ gap: 6, fontSize: 11 }}>
                      <span className="vp-dotc" style={{ background: VERDICT_COLOR[x.verdict ?? "pending"] }} />
                      <span className="vp-ivory">{"v" + x.version + (x.version === version ? " · current" : "")}</span>
                      <span className="vp-muted" style={{ marginLeft: "auto" }}>
                        {VERDICT_LABEL[x.verdict ?? "pending"]}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {pending && !compareOn ? (
            <PinComposer
              num={pinned.length + 1}
              pt={pending}
              onCancel={() => setPending(null)}
              onSave={async (text) => {
                const n = await addNote({ ref, text, x: pending.x, y: pending.y }, p);
                if (n) {
                  setPending(null);
                  setActive(n.id);
                }
              }}
            />
          ) : null}

          <div className="vp-col">
            <div className="vp-label" style={{ display: "flex", justifyContent: "space-between" }}>
              <span>Versions · {siblings.length}</span>
              <span>{task ? "task " + task.id : ""}</span>
            </div>
            <div className="vp-versions">
              {siblings.map((x) => {
                const isCur = x.version === version;
                return (
                  <button
                    key={x.version}
                    type="button"
                    className="vp-thumb"
                    aria-current={isCur}
                    aria-label={"Version " + x.version + ", " + VERDICT_LABEL[x.verdict] + (isCur ? " (current)" : "")}
                    onClick={() => {
                      if (!isCur) openItem(snapRef(x.version));
                    }}
                  >
                    <img src={snapSrc(p, x)} alt="" />
                    <span className="cap">
                      <span className="vp-dotc" style={{ background: VERDICT_COLOR[x.verdict] }} />v{x.version}
                    </span>
                  </button>
                );
              })}
            </div>
          </div>

          <div className="vp-box" data-testid="verdict">
            <div className="vp-row" style={{ gap: 10 }}>
              <span className="vp-label">Verdict</span>
              <span className="vp-verdict" style={{ borderColor: VERDICT_COLOR[verdict], color: VERDICT_COLOR[verdict] }}>
                {VERDICT_LABEL[verdict]}
              </span>
              {reviewNote ? <span className="vp-review-note">“{reviewNote}”</span> : null}
            </div>
            <div className="vp-row">
              <button type="button" className={"vp-btn big go" + (verdict === "approved" ? " on" : "")} onClick={() => void setVerdict("approved")}>
                ✓ Approve
              </button>
              <button
                type="button"
                className={"vp-btn big danger" + (verdict === "changes" ? " on" : "")}
                onClick={() => {
                  setChangesOpen(true);
                  setVDraft((d) => d || reviewNote);
                }}
              >
                Request changes
              </button>
              <button type="button" className="vp-btn big ghost" onClick={() => void setVerdict("pending")}>
                Reset to pending
              </button>
            </div>
            {changesOpen ? (
              <div className="vp-col" style={{ gap: 8 }}>
                <label htmlFor="vm-verdict-note" className="vp-label vp-coral">
                  What needs to change
                </label>
                <textarea
                  id="vm-verdict-note"
                  className="vp-input"
                  rows={3}
                  value={vDraft}
                  placeholder="Review note for the next version"
                  onChange={(e) => setVDraft(e.target.value)}
                />
                <div className="vp-actions">
                  <button type="button" className="vp-btn" onClick={() => setChangesOpen(false)}>
                    Cancel
                  </button>
                  <button type="button" className="vp-btn coral-fill" disabled={!vDraft.trim()} onClick={() => void setVerdict("changes", vDraft.trim())}>
                    Send back
                  </button>
                </div>
              </div>
            ) : null}
          </div>

          <PinNotesList ordered={ordered} numOf={numOf} activeId={active} onPick={(id) => setActive((a) => (a === id ? null : id))} emptyText="No notes yet. Turn on Annotate and click the frame." />

          {task ? (
            <button
              type="button"
              className="vp-linkrow tall dr-hover"
              style={{ border: "1px solid var(--hairline)", padding: "10px 12px" }}
              onClick={() => openItem(formatRef({ type: "task", projectId: p, id: task.id }))}
            >
              <span className="vp-label">Task</span>
              <span className="vp-dotc" style={{ background: STATUS_COLOR[task.status] }} />
              <span className="vp-ell" style={{ flex: 1 }}>
                {task.title}
              </span>
              <span className="vp-muted">{STATUS_LABEL[task.status]} →</span>
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}
