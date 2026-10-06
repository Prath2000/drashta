// Commit preview body (02-ui-spec §2.4 "commit"; prototype ViewCode.dc.html).
// Live detail: GET /projects/{p}/commits/{id} -> {commit, body, files, patch}.
// Header card, files changed (click -> file preview), per-file diff blocks with
// Expand all / Collapse all, a truncated-patch banner, and commit notes.
import { useMemo, useState } from "react";
import { useApi } from "~/lib/query";
import { formatRef } from "~/lib/refs";
import type { Commit } from "~/lib/types";
import { useItem } from "~/lib/url";
import type { ViewportBodyProps } from "./registry";
import { DiffView } from "./DiffView";
import { baseRef, ErrorLine, InlineText, NoteCard, NoteComposer, useNative, useNotes } from "./common";
import { fmtDate, rel } from "./fmt";
import { inline } from "./markdown";
import { parsePatch, splitMessage } from "./patch";
import type { CommitDetail } from "./vtypes";

const enc = encodeURIComponent;

export function CommitBody({ refStr, refObj, resolved }: ViewportBodyProps) {
  const p = resolved.projectId || refObj.projectId;
  const ref = baseRef(refObj);
  const { openItem } = useItem();
  const native = useNative(refStr);
  const q = useApi<CommitDetail>("/projects/" + enc(p) + "/commits/" + enc(refObj.id));
  const notes = useNotes(ref, resolved.notes ?? []);
  const [open, setOpen] = useState<Record<number, boolean>>({});

  const cm: Partial<Commit> = q.data?.commit ?? ((resolved.obj ?? {}) as Partial<Commit>);
  const patch = q.data?.patch ?? "";
  const parsed = useMemo(() => parsePatch(patch), [patch]);
  const { headline, bullets } = splitMessage(cm.msg ?? resolved.title ?? "");
  const body = (q.data?.body ?? "").trim();

  const seenAdd = parsed.reduce((a, f) => a + f.add, 0);
  const seenDel = parsed.reduce((a, f) => a + f.del, 0);
  const plus = cm.plus ?? seenAdd;
  const minus = cm.minus ?? seenDel;
  const tot = plus + minus;
  const addCells = tot ? Math.round((plus / tot) * 10) : 0;
  const hasPatch = patch.trim().length > 0;
  const seen = seenAdd + seenDel;
  const truncated = !!q.data?.truncated || (hasPatch && tot > 0 && seen < tot);
  const loading = q.loading && !q.data;
  const light = !!resolved.light && !hasPatch && !loading;
  const isFossil = cm.vcs === "fossil";

  const byPath = new Map(parsed.map((f) => [f.path, f]));
  const fileList = q.data?.files?.length ? q.data.files : parsed.map((f) => ({ path: f.path, plus: f.add, minus: f.del }));
  const openN = notes.filter((n) => !n.resolved).length;
  const nativeLabel = resolved.nativeLabel || "Open";
  const nativeCmd = resolved.nativeActions?.[0]?.command ?? "";

  const nativeBtn = (label: string, cls = "vp-btn amber-text") => (
    <button type="button" className={cls} onClick={() => void native()}>
      {label}
    </button>
  );

  let diffLabel = "";
  if (loading) diffLabel = isFossil ? "loading live via fossil…" : "loading…";
  else if (light) diffLabel = "light summary";
  else if (parsed.length) diffLabel = "+" + seenAdd + " −" + seenDel + " in preview";

  return (
    <div className="vp-scroll" data-testid="commit-body">
      <div className="vp-chead">
        <div className="kicker vp-label">
          <span className="vp-amber" aria-hidden="true">
            ▮
          </span>
          <span>{(cm.vcs || "git") + " commit"}</span>
          <span className="vp-faint">·</span>
          <span className="id">{cm.id ?? refObj.id}</span>
        </div>
        <div className="vp-headline">{headline}</div>
        {bullets.length ? (
          <div className="vp-col" style={{ gap: 5 }}>
            {bullets.map((b, i) => (
              <div key={i} className="vp-bullet">
                <span className="mk">–</span>
                <span style={{ minWidth: 0 }}>
                  <InlineText segs={inline(b)} />
                </span>
              </div>
            ))}
          </div>
        ) : null}
        {body ? <div className="vp-cbody">{body}</div> : null}
        <div className="vp-cmeta">
          <div>
            <span className="vp-label dim">Author</span>
            <span>{cm.author || "—"}</span>
          </div>
          <div>
            <span className="vp-label dim">Date</span>
            <span>{fmtDate(cm.ts) + " · " + rel(cm.ts)}</span>
          </div>
          <div>
            <span className="vp-label dim">Change</span>
            <div className="vp-row" style={{ gap: 10, flexWrap: "nowrap" }}>
              <span className="vp-teal">+{plus}</span>
              <span className="vp-coral">−{minus}</span>
              <div className="vp-ticks" aria-hidden="true">
                {Array.from({ length: 10 }, (_, k) => (
                  <span key={k} className={!tot ? "" : k < addCells ? "add" : "del"} />
                ))}
              </div>
            </div>
          </div>
        </div>
      </div>

      <div className="vp-csec">
        <div className="vp-section-head" style={{ marginBottom: 4 }}>
          <span className="vp-label">Files changed</span>
          <span className="vp-faint">
            {fileList.length ? fileList.length + " file" + (fileList.length > 1 ? "s" : "") + " · click to preview" : loading ? "loading…" : light ? "light summary" : "none recorded"}
          </span>
        </div>
        {fileList.map((f) => {
          const pf = byPath.get(f.path);
          const add = f.plus ?? pf?.add;
          const del = f.minus ?? pf?.del;
          return (
            <button
              key={f.path}
              type="button"
              className="vp-cfile dr-hover"
              title={"Preview " + f.path}
              onClick={() => openItem(formatRef({ type: "file", projectId: p, id: f.path }))}
            >
              <span className="p">{f.path}</span>
              <span className="vp-teal">{add != null ? "+" + add : ""}</span>
              <span className="vp-coral">{del != null ? "−" + del : ""}</span>
              <span className="arr" aria-hidden="true">
                →
              </span>
            </button>
          );
        })}
      </div>

      <div className="vp-csec diff">
        <div className="vp-section-head" style={{ alignItems: "center" }}>
          <span className="vp-label">Diff</span>
          <span className="vp-faint">{diffLabel}</span>
          {parsed.length ? (
            <div style={{ marginLeft: "auto", display: "flex", gap: 6 }}>
              <button
                type="button"
                className="vp-btn sm ghost"
                onClick={() => setOpen(Object.fromEntries(parsed.map((_, i) => [i, true])))}
              >
                Expand all
              </button>
              <button
                type="button"
                className="vp-btn sm ghost"
                onClick={() => setOpen(Object.fromEntries(parsed.map((_, i) => [i, false])))}
              >
                Collapse all
              </button>
            </div>
          ) : null}
        </div>

        {loading && isFossil ? (
          <div className="vp-panel tight" data-testid="commit-fossil-live">
            <span className="big">Diff loads live via fossil</span>
            <span className="why">This check-in lives in the fossil repository. drashta asks fossil for the patch now:</span>
            {nativeCmd ? <code>{nativeCmd}</code> : null}
          </div>
        ) : null}
        {q.error ? <ErrorLine error={q.error} what="the diff" /> : null}
        {light ? (
          <div className="vp-panel tight" data-testid="commit-light">
            <span className="big">Diff not loaded — light summary</span>
            <span className="why">Only the message, date and line counts are known for this commit. Open still runs the native command:</span>
            {nativeCmd ? <code>{nativeCmd}</code> : null}
            {nativeBtn(nativeLabel + " ↗", "vp-btn amber-text")}
          </div>
        ) : null}
        {!loading && !light && !q.error && !parsed.length ? (
          <div className="vp-panel tight" style={{ padding: 16 }}>
            <span className="why">No file changes in this commit.</span>
          </div>
        ) : null}

        <DiffView files={parsed} open={open} onToggle={(i, o) => setOpen((m) => ({ ...m, [i]: o }))} />

        {truncated ? (
          <div className="vp-banner" style={{ margin: 0 }} data-testid="commit-truncated">
            <span>{tot > 0 && seen < tot ? "Preview shows " + seen + " of " + tot + " changed lines (patch cut at 400 KB)" : "The patch was cut at 400 KB."}</span>
            <span style={{ marginLeft: "auto" }}>{nativeBtn("Full diff ↗", "vp-btn sm amber-text")}</span>
          </div>
        ) : null}
      </div>

      <div className="vp-notes-sec" style={{ margin: "14px 18px 26px" }} data-testid="commit-notes">
        <div className="vp-section-head">
          <span className="vp-label">Notes</span>
          <span className="vp-faint">{notes.length ? openN + " open · " + (notes.length - openN) + " resolved" : "none yet"}</span>
        </div>
        {notes.map((n) => (
          <NoteCard key={n.id} note={n} variant="flat" />
        ))}
        <NoteComposer refStr={ref} projectId={p} id="vc-commit-note" label="Note on this commit" placeholder="Note on this commit…" stacked buttonLabel="Add note" />
      </div>
    </div>
  );
}
