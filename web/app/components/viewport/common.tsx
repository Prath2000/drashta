// Pieces shared by several viewport bodies: notes (read, add, resolve),
// the native "Open ↗" call, and token / inline text spans.
import { useState, type ReactNode } from "react";
import { api, qs } from "~/lib/api";
import { invalidate, useApi } from "~/lib/query";
import { formatRef } from "~/lib/refs";
import { useEscapeLayer } from "~/lib/shortcuts";
import { toast } from "~/lib/store";
import type { NativeAction, Note, NotesResponse, OpenAction, RefObj } from "~/lib/types";
import { fmtWhen, rel } from "./fmt";
import type { Seg } from "./highlight";
import type { Inline } from "./markdown";

/** The ref without its `#fragment` (notes are stored on the item, not the fragment). */
export function baseRef(refObj: RefObj): string {
  return formatRef({ type: refObj.type, projectId: refObj.projectId, id: refObj.id });
}

/** Refetch everything that shows notes after a note changed. */
export function invalidateNotes(projectId: string): void {
  invalidate("/notes");
  invalidate("/resolve");
  invalidate("/inbox");
  invalidate("/projects/" + encodeURIComponent(projectId));
}

/** Notes on one item (`GET /notes?ref=`). Falls back to `fallback` until it loads. */
export function useNotes(ref: string, fallback: Note[] = []): Note[] {
  const q = useApi<NotesResponse>("/notes" + qs({ ref }));
  return q.data?.notes ?? fallback;
}

/** POST /notes, then refresh. Returns the new note or null (an error toast is shown). */
export async function addNote(n: { ref: string; text: string; line?: number; x?: number; y?: number }, projectId: string): Promise<Note | null> {
  try {
    const out = await api.addNote(n);
    invalidateNotes(projectId);
    toast(n.line != null ? "Note pinned to line " + n.line : n.x != null ? "Note pinned" : "Note added");
    return out.note;
  } catch (e) {
    toast(e instanceof Error ? e.message : "could not add the note", { error: true });
    return null;
  }
}

/** PATCH a note's resolved flag. */
export async function toggleNote(n: Note): Promise<void> {
  try {
    await api.updateNote(n.projectId, n.id, { resolved: !n.resolved });
    invalidateNotes(n.projectId);
    toast(n.resolved ? "Note reopened" : "Note resolved");
  } catch (e) {
    toast(e instanceof Error ? e.message : "could not change the note", { error: true });
  }
}

/** Runs a native action through `POST /open {ref, action}` and shows the command it ran. */
export function useNative(refStr: string) {
  return async (action?: OpenAction) => {
    try {
      const out = await api.open(refStr, action);
      toast(out.ran ? "ran: " + out.ran : "opened");
    } catch (e) {
      toast(e instanceof Error ? e.message : "open failed", { error: true });
    }
  };
}

/** Every native action of an item as a button ("Open in default app ↗", "Reveal in Finder ↗"...). */
export function NativeButtons({ refStr, actions }: { refStr: string; actions: NativeAction[] }) {
  const native = useNative(refStr);
  if (!actions.length) return null;
  return (
    <div className="vp-row" style={{ gap: 6, flexWrap: "wrap" }} data-testid="native-buttons">
      {actions.map((a, i) => (
        <button key={a.action} type="button" className={"vp-btn" + (i === 0 ? " amber-text" : "")} title={a.command} onClick={() => void native(a.action as OpenAction)}>
          {a.label} ↗
        </button>
      ))}
    </div>
  );
}

/**
 * Full window for a viewer (drawing, 3D model, PDF). The caller puts `vp-stage-full` on the
 * SAME element when `full` is true, so a canvas or frame is never remounted: it only resizes.
 * Esc leaves full window first (layer "app"); the next Esc closes the drawer as before.
 */
export function useFullWindow(): { full: boolean; toggle: () => void } {
  const [full, setFull] = useState(false);
  useEscapeLayer("app", full, () => setFull(false));
  return { full, toggle: () => setFull((f) => !f) };
}

/** The button that goes with useFullWindow. */
export function FullWindowButton({ full, onToggle }: { full: boolean; onToggle: () => void }) {
  return (
    <button type="button" className="vp-btn sm" aria-pressed={full} title={full ? "Back to the drawer (Esc)" : "Fill the window"} onClick={onToggle}>
      {full ? "⤡ Exit full window" : "⤢ Full window"}
    </button>
  );
}

/** Coloured code tokens. */
export function Segs({ segs }: { segs: Seg[] }) {
  if (!segs.length) return <>{" "}</>;
  return (
    <>
      {segs.map((s, i) => (
        <span key={i} className={"vp-tk-" + s.k}>
          {s.t}
        </span>
      ))}
    </>
  );
}

/** Markdown inline spans. */
export function InlineText({ segs }: { segs: Inline[] }) {
  return (
    <>
      {segs.map((s, i) =>
        s.k === "plain" ? (
          <span key={i}>{s.t}</span>
        ) : (
          <span key={i} className={"vp-in-" + s.k}>
            {s.t}
          </span>
        ),
      )}
    </>
  );
}

/** A note card in the code / commit style (author, time, where, Resolve). */
export function NoteCard({ note, variant = "inline", hint }: { note: Note; variant?: "inline" | "flat" | "block"; hint?: ReactNode }) {
  return (
    <div className={"vp-note" + (variant === "inline" ? "" : " " + variant) + (note.resolved ? " resolved" : "")} data-testid="note-card">
      <div className="vp-note-head">
        <span className="dot" aria-hidden="true">
          ●
        </span>
        <span className="who">{note.author || "you"}</span>
        <span>{rel(note.date)}</span>
        {note.line != null ? <span className="where">line {note.line}</span> : null}
        {hint}
        <button type="button" className="vp-btn sm ghost" onClick={() => void toggleNote(note)}>
          {note.resolved ? "Reopen" : "Resolve"}
        </button>
      </div>
      <div className="vp-note-text">{note.text}</div>
    </div>
  );
}

/** A textarea + "Add" button for a whole-item note. */
export function NoteComposer({
  refStr,
  projectId,
  placeholder,
  id,
  label,
  stacked,
  buttonLabel = "Add",
}: {
  refStr: string;
  projectId: string;
  placeholder: string;
  id: string;
  label: string;
  stacked?: boolean;
  buttonLabel?: string;
}) {
  const [draft, setDraft] = useState("");
  const empty = !draft.trim();
  const submit = async () => {
    const text = draft.trim();
    if (!text) return;
    const n = await addNote({ ref: refStr, text }, projectId);
    if (n) setDraft("");
  };
  const ta = (
    <textarea
      id={id}
      className="vp-input"
      rows={stacked ? 3 : 2}
      value={draft}
      placeholder={placeholder}
      onChange={(e) => setDraft(e.target.value)}
    />
  );
  const btn = (
    <button type="button" className={"vp-btn primary" + (stacked ? "" : " big")} disabled={empty} onClick={() => void submit()}>
      {buttonLabel}
    </button>
  );
  return (
    <>
      <label htmlFor={id} className="sr-only">
        {label}
      </label>
      {stacked ? (
        <>
          {ta}
          <div className="vp-actions">{btn}</div>
        </>
      ) : (
        <div className="vp-add-row">
          {ta}
          {btn}
        </div>
      )}
    </>
  );
}

/** Media-style notes block (image, session, server, task): list + add. */
export function MediaNotes({ refStr, projectId, notes }: { refStr: string; projectId: string; notes: Note[] }) {
  return (
    <div className="vp-col" style={{ borderTop: "1px solid var(--hairline)", paddingTop: 14 }} data-testid="item-notes">
      <div className="vp-label">Notes · {notes.length}</div>
      {notes.map((n) => (
        <div key={n.id} className={"vp-mnote" + (n.resolved ? " resolved" : "")}>
          <div className="body">
            <div className="txt">{n.text}</div>
            <div className="by">
              {(n.author || "you") + " · " + fmtWhen(n.date)}
              {n.x != null && n.y != null ? " · pin " + Math.round(n.x * 100) + "%, " + Math.round((n.y ?? 0) * 100) + "%" : ""}
            </div>
          </div>
          <button type="button" className="vp-btn sm ghost" onClick={() => void toggleNote(n)}>
            {n.resolved ? "Reopen" : "Resolve"}
          </button>
        </div>
      ))}
      <div style={{ marginTop: 4 }}>
        <NoteComposer refStr={refStr} projectId={projectId} id="vp-note" label="Add a note" placeholder="Add a note…" />
      </div>
    </div>
  );
}

/** Loading / error lines used while a body fetches. */
export function Loading({ what }: { what: string }) {
  return <div className="vp-loading">Loading {what}…</div>;
}

export function ErrorLine({ error, what }: { error: unknown; what: string }) {
  const msg = error instanceof Error ? error.message : String(error);
  return (
    <div className="vp-error" role="alert">
      Could not load {what}: {msg}
    </div>
  );
}
