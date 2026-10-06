// An image with numbered point notes (x, y in 0..1) and an "annotate" layer.
// Used by the image and snapshot bodies. In annotate mode the frame is a
// 12 x 8 grid of buttons (keyboard reachable); a mouse click uses the exact
// point, a keyboard press uses the cell centre.
import { useState, type MouseEvent } from "react";
import type { Note } from "~/lib/types";
import { toggleNote } from "./common";
import { fmtWhen } from "./fmt";

export interface PinStageProps {
  src: string;
  alt: string;
  pinned: Note[];
  numOf: Map<string, number>;
  activeId: string | null;
  onPick: (id: string) => void;
  annotate: boolean;
  pending: { x: number; y: number } | null;
  pendingNum: number;
  onDrop: (pt: { x: number; y: number }) => void;
  /** "fit" (width 100%) or "full" (natural size). */
  zoom?: "fit" | "full";
  onLoad?: (w: number, h: number) => void;
}

const COLS = 12;
const ROWS = 8;
const pct = (v: number) => (v * 100).toFixed(2) + "%";

export function PinStage(p: PinStageProps) {
  const drop = (e: MouseEvent<HTMLButtonElement>, c: number, r: number) => {
    const grid = e.currentTarget.parentElement;
    if (e.detail > 0 && grid && e.clientX) {
      const b = grid.getBoundingClientRect();
      if (b.width > 0 && b.height > 0) {
        const x = Math.min(1, Math.max(0, (e.clientX - b.left) / b.width));
        const y = Math.min(1, Math.max(0, (e.clientY - b.top) / b.height));
        p.onDrop({ x: +x.toFixed(4), y: +y.toFixed(4) });
        return;
      }
    }
    p.onDrop({ x: +((c + 0.5) / COLS).toFixed(4), y: +((r + 0.5) / ROWS).toFixed(4) });
  };
  return (
    <div className={"vp-stage " + (p.zoom ?? "fit")} data-testid="pin-stage">
      <img
        src={p.src}
        alt={p.alt}
        onLoad={(e) => {
          const t = e.currentTarget;
          if (t.naturalWidth) p.onLoad?.(t.naturalWidth, t.naturalHeight);
        }}
      />
      {p.pinned.map((n) => (
        <button
          key={n.id}
          type="button"
          className={"vp-pin" + (n.resolved ? " resolved" : "") + (p.activeId === n.id ? " active" : "")}
          style={{ left: pct(n.x ?? 0), top: pct(n.y ?? 0) }}
          aria-label={"Note " + p.numOf.get(n.id) + ": " + n.text}
          onClick={() => p.onPick(n.id)}
        >
          {p.numOf.get(n.id)}
        </button>
      ))}
      {p.pending ? (
        <div className="vp-pin pending" style={{ left: pct(p.pending.x), top: pct(p.pending.y) }} aria-hidden="true">
          {p.pendingNum}
        </div>
      ) : null}
      {p.annotate ? (
        <div className="vp-annot" data-testid="annotate-grid">
          {Array.from({ length: COLS * ROWS }, (_, i) => {
            const c = i % COLS;
            const r = Math.floor(i / COLS);
            return (
              <button key={i} type="button" aria-label={"Drop pin at column " + (c + 1) + ", row " + (r + 1)} onClick={(e) => drop(e, c, r)} />
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

/** The "Pin n · x%, y%" composer under the frame. */
export function PinComposer({ num, pt, onCancel, onSave }: { num: number; pt: { x: number; y: number }; onCancel: () => void; onSave: (text: string) => Promise<void> }) {
  const [draft, setDraft] = useState("");
  const empty = !draft.trim();
  return (
    <div className="vp-composer coral" data-testid="pin-composer">
      <label htmlFor="vm-pin-draft" className="vp-label">
        Pin {num} · {Math.round(pt.x * 100)}%, {Math.round(pt.y * 100)}%
      </label>
      <textarea
        id="vm-pin-draft"
        className="vp-input"
        rows={2}
        autoFocus
        value={draft}
        placeholder="What should change here?"
        onChange={(e) => setDraft(e.target.value)}
      />
      <div className="vp-actions">
        <button type="button" className="vp-btn" onClick={onCancel}>
          Cancel
        </button>
        <button type="button" className={"vp-btn" + (empty ? "" : " coral-fill")} disabled={empty} onClick={() => void onSave(draft.trim())}>
          Add note
        </button>
      </div>
    </div>
  );
}

/** Numbered note rows (pins first). Clicking a row highlights its pin. */
export function PinNotesList({ ordered, numOf, activeId, onPick, emptyText }: { ordered: Note[]; numOf: Map<string, number>; activeId: string | null; onPick: (id: string) => void; emptyText: string }) {
  const open = ordered.filter((n) => !n.resolved).length;
  return (
    <div className="vp-col" data-testid="pin-notes">
      <div className="vp-label">
        Notes · {ordered.length}
        {open !== ordered.length ? " (" + open + " open)" : ""}
      </div>
      {ordered.length === 0 ? <div className="vp-muted" style={{ fontSize: 11, padding: "8px 0" }}>{emptyText}</div> : null}
      {ordered.map((n) => {
        const hasPin = n.x != null && n.y != null;
        return (
          <div key={n.id} className={"vp-snote" + (activeId === n.id ? " active" : "")}>
            <button
              type="button"
              className={"vp-pnum" + (hasPin ? " pin" : "") + (n.resolved ? " resolved" : "")}
              aria-label={hasPin ? "Show pin " + numOf.get(n.id) : "Whole-item note"}
              onClick={() => hasPin && onPick(n.id)}
            >
              {hasPin ? numOf.get(n.id) : "–"}
            </button>
            <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 2 }}>
              <div className={n.resolved ? "vp-faint" : undefined} style={n.resolved ? { textDecoration: "line-through", fontSize: 12 } : { whiteSpace: "pre-wrap" }}>
                {n.text}
              </div>
              <div style={{ fontSize: 10 }} className="vp-muted">
                {(n.author || "you") + " · " + fmtWhen(n.date)}
              </div>
            </div>
            <button type="button" className="vp-btn sm ghost" onClick={() => void toggleNote(n)}>
              {n.resolved ? "Reopen" : "Resolve"}
            </button>
          </div>
        );
      })}
    </div>
  );
}

/** Pinned notes first (numbered 1..n in order), then the rest. */
export function numberNotes(all: Note[]): { pinned: Note[]; ordered: Note[]; numOf: Map<string, number> } {
  const pinned = all.filter((n) => n.x != null && n.y != null);
  const loose = all.filter((n) => !(n.x != null && n.y != null));
  const ordered = pinned.concat(loose);
  const numOf = new Map<string, number>();
  ordered.forEach((n, i) => numOf.set(n.id, i + 1));
  return { pinned, ordered, numOf };
}
