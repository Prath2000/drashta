// Image preview body (02-ui-spec §2.4 "image"; prototype ViewMedia.dc.html).
// The bytes come from GET /projects/{p}/raw?path=. Fit / 100% zoom, point
// notes at x,y (Annotate), "Used in snapshots", "Make snapshot for task…".
import { useState } from "react";
import { api, qs } from "~/lib/api";
import { findProject, useBoot } from "~/lib/boot";
import { invalidate, useApi } from "~/lib/query";
import { toast } from "~/lib/store";
import type { SearchResponse, TrackerResponse } from "~/lib/types";
import { useItem } from "~/lib/url";
import type { ViewportBodyProps } from "./registry";
import { addNote, baseRef, NoteComposer, useNotes } from "./common";
import { fmtSize, fmtWhen, VERDICT_COLOR, VERDICT_LABEL } from "./fmt";
import { numberNotes, PinComposer, PinNotesList, PinStage } from "./PinStage";
import type { FileObj } from "./vtypes";

export function ImageBody({ refObj, resolved }: ViewportBodyProps) {
  const p = resolved.projectId || refObj.projectId;
  const obj = (resolved.obj ?? {}) as FileObj;
  const path = obj.path ?? refObj.id;
  const ref = baseRef(refObj);
  const { data: boot } = useBoot();
  const { openItem } = useItem();
  const notes = useNotes(ref, resolved.notes ?? []);
  const { pinned, ordered, numOf } = numberNotes(notes);

  const [zoom, setZoom] = useState<"fit" | "full">("fit");
  const [dims, setDims] = useState("");
  const [annotate, setAnnotate] = useState(false);
  const [pending, setPending] = useState<{ x: number; y: number } | null>(null);
  const [active, setActive] = useState<string | null>(null);
  const [taskPick, setTaskPick] = useState("");

  // Snapshots that use this image (search by path, kind snapshot).
  const used = useApi<SearchResponse>("/search" + qs({ q: path, project: p, kind: "snapshot", limit: 10 }));
  const usedIn = (used.data?.hits ?? []).filter((h) => h.kind === "snapshot" && h.projectId === p);
  const tracker = useApi<TrackerResponse>("/tracker" + qs({ project: p }));
  const tasks = (tracker.data?.tasks ?? []).filter((t) => t.projectId === p);

  const src = api.rawUrl(p, path);
  const makeSnapshot = async () => {
    const id = taskPick;
    if (!id) return;
    const t = tasks.find((x) => x.id === id);
    try {
      const out = await api.createSnapshot(p, id, { path });
      invalidate("/tracker");
      invalidate("/search");
      invalidate("/projects/" + encodeURIComponent(p) + "/tasks");
      toast("Snapshot v" + out.snapshot.version + " added to " + id + (t ? " · " + t.title : ""));
      setTaskPick("");
    } catch (e) {
      toast(e instanceof Error ? e.message : "could not make the snapshot", { error: true });
    }
  };

  return (
    <div className="vp-scroll" data-testid="image-body">
      <div className="vp-pad">
        <div className="vp-stack">
          <div className="vp-row" style={{ justifyContent: "space-between" }}>
            <div className="vp-label">Image · {dims || "—"}</div>
            <div className="vp-row" style={{ gap: 6 }}>
              <button
                type="button"
                className={"vp-btn" + (annotate ? " coral-fill" : "")}
                aria-pressed={annotate}
                onClick={() => {
                  setAnnotate((a) => !a);
                  if (annotate) setPending(null);
                }}
              >
                {annotate ? "● Annotating" : "Annotate"}
              </button>
              <div className="vp-seg light" role="group" aria-label="Zoom">
                <button type="button" aria-pressed={zoom === "fit"} onClick={() => setZoom("fit")}>
                  Fit
                </button>
                <button type="button" aria-pressed={zoom === "full"} onClick={() => setZoom("full")}>
                  100%
                </button>
              </div>
            </div>
          </div>
          <div className="vp-imgframe">
            <PinStage
              src={src}
              alt={path}
              zoom={zoom}
              pinned={pinned}
              numOf={numOf}
              activeId={active}
              onPick={(id) => setActive((a) => (a === id ? null : id))}
              annotate={annotate}
              pending={pending}
              pendingNum={pinned.length + 1}
              onDrop={setPending}
              onLoad={(w, h) => setDims(w + "×" + h)}
            />
          </div>
          {annotate ? <div className="vp-annot-hint" style={{ marginTop: -6 }}>Annotate mode — click anywhere on the image to drop a pin.</div> : null}
          {pending ? (
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
          <div className="vp-grid c2">
            <div>
              <div className="vp-label">Path</div>
              <div style={{ wordBreak: "break-all" }}>{path}</div>
            </div>
            <div>
              <div className="vp-label">Project</div>
              <div>{findProject(boot, p)?.name ?? p}</div>
            </div>
            <div>
              <div className="vp-label">Size</div>
              <div>{fmtSize(obj.size)}</div>
            </div>
            <div>
              <div className="vp-label">Modified</div>
              <div>{obj.mtime || resolved.date ? fmtWhen(obj.mtime ?? resolved.date) : "—"}</div>
            </div>
          </div>
          {usedIn.length ? (
            <div className="vp-col" data-testid="used-in">
              <div className="vp-label">Used in snapshots</div>
              {usedIn.map((h) => {
                const verdict = (["approved", "changes", "pending"] as const).find((v) => h.snippet.startsWith(v)) ?? "pending";
                return (
                  <button key={h.ref} type="button" className="vp-linkrow dr-hover" onClick={() => openItem(h.ref)}>
                    <span className="vp-dotc" style={{ background: VERDICT_COLOR[verdict] }} />
                    <span className="vp-ivory vp-ell">{h.title}</span>
                    <span className="end">{VERDICT_LABEL[verdict]} →</span>
                  </button>
                );
              })}
            </div>
          ) : null}
          <div className="vp-box">
            <label htmlFor="vm-task-pick" className="vp-label">
              Make snapshot for task…
            </label>
            <div className="vp-row" style={{ flexWrap: "nowrap" }}>
              <select id="vm-task-pick" className="vp-input" style={{ flex: 1 }} value={taskPick} onChange={(e) => setTaskPick(e.target.value)}>
                <option value="">Choose a task</option>
                {tasks.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.id + " · " + t.title}
                  </option>
                ))}
              </select>
              <button type="button" className={"vp-btn" + (taskPick ? " primary" : "")} disabled={!taskPick} onClick={() => void makeSnapshot()}>
                Make snapshot
              </button>
            </div>
          </div>
        </div>
        <PinNotesList ordered={ordered} numOf={numOf} activeId={active} onPick={(id) => setActive((a) => (a === id ? null : id))} emptyText="No notes yet. Turn on Annotate and click the image." />
        <WholeNote refStr={ref} projectId={p} />
      </div>
    </div>
  );
}


/** A whole-item note composer (no pin). */
export function WholeNote({ refStr, projectId }: { refStr: string; projectId: string }) {
  return <NoteComposer refStr={refStr} projectId={projectId} id="vm-note" label="Add a note" placeholder="Add a note to the whole image…" />;
}
