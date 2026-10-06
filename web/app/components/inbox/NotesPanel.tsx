import { useRef, useState, type KeyboardEvent } from "react";
import { parseRef } from "~/lib/refs";
import type { Note } from "~/lib/types";
import type { NoteFilter } from "./useInbox";
import { noteKey, noteType, noteWhere, relTime, typeColor } from "./util";

const TABS: { id: NoteFilter; label: string }[] = [
  { id: "open", label: "Open" },
  { id: "resolved", label: "Resolved" },
  { id: "all", label: "All" },
];

const EMPTY_TITLE: Record<NoteFilter, string> = {
  open: "No open notes. All clear.",
  resolved: "Nothing resolved yet.",
  all: "No notes yet.",
};

/** Rows drawn before the "Show all N" button (the list can get long). */
export const NOTE_CAP = 200;

export interface NotesPanelProps {
  tab: NoteFilter;
  onTab: (t: NoteFilter) => void;
  /** Notes for this tab, newest first, with the resolved flag already applied. */
  notes: Note[];
  /** Badge text per tab (null = no badge yet). */
  counts: Record<NoteFilter, string | number | null>;
  loading: boolean;
  error: unknown;
  onRetry: () => void;
  hasMore: boolean;
  loadingMore: boolean;
  moreError: string | null;
  onMore: () => void;
  projectName: (id: string) => string;
  nowMs: number;
  onOpen: (n: Note) => void;
  onToggle: (n: Note) => void;
}

export function NotesPanel(p: NotesPanelProps) {
  const [showAll, setShowAll] = useState(false);
  const tabRefs = useRef<Partial<Record<NoteFilter, HTMLButtonElement | null>>>({});
  const shown = showAll ? p.notes : p.notes.slice(0, NOTE_CAP);

  // Arrow keys move between the tabs. The handler is on the tablist, not global.
  const onTabKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
    const i = TABS.findIndex((t) => t.id === p.tab);
    const n = TABS[(i + (e.key === "ArrowRight" ? 1 : TABS.length - 1)) % TABS.length];
    e.preventDefault();
    p.onTab(n.id);
    tabRefs.current[n.id]?.focus();
  };

  return (
    <section className="ix-notes" aria-label="Notes">
      <div className="ix-panel-head">
        <span className="ix-head">Notes</span>
        <div role="tablist" aria-label="Note filter" className="ix-seg" onKeyDown={onTabKey}>
          {TABS.map((t) => {
            const active = p.tab === t.id;
            const c = p.counts[t.id];
            return (
              <button
                key={t.id}
                ref={(el) => {
                  tabRefs.current[t.id] = el;
                }}
                type="button"
                role="tab"
                id={"ix-tab-" + t.id}
                aria-selected={active}
                aria-controls="ix-notes-list"
                tabIndex={active ? 0 : -1}
                className={"ix-seg-btn dr-hover" + (active ? " on" : "")}
                onClick={() => p.onTab(t.id)}
              >
                {t.label}
                {c !== null ? <span className="ix-count">{c}</span> : null}
              </button>
            );
          })}
        </div>
      </div>
      <div className="ix-scroll" id="ix-notes-list" role="tabpanel" aria-labelledby={"ix-tab-" + p.tab} data-testid="notes-scroll">
        {p.error && p.notes.length === 0 ? (
          <div className="ix-empty" role="alert">
            <div className="ix-empty-title">Could not load the notes.</div>
            <div className="ix-empty-help">{errMsg(p.error)}</div>
            <button type="button" className="ix-btn dr-hover" onClick={p.onRetry}>
              Retry
            </button>
          </div>
        ) : p.loading && p.notes.length === 0 ? (
          <div className="ix-empty">
            <div className="ix-empty-help">Loading notes…</div>
          </div>
        ) : p.notes.length === 0 ? (
          <div className="ix-empty">
            <div className="ix-empty-title">{EMPTY_TITLE[p.tab]}</div>
            <div className="ix-empty-help">Notes pinned to files, commits, sessions and snapshots collect here.</div>
          </div>
        ) : null}
        {shown.length ? (
          <ul className="ix-list" aria-label={p.tab + " notes"}>
            {shown.map((n) => (
              <NoteRow key={noteKey(n)} n={n} nowMs={p.nowMs} projectName={p.projectName(n.projectId)} onOpen={p.onOpen} onToggle={p.onToggle} />
            ))}
          </ul>
        ) : null}
        {!showAll && p.notes.length > NOTE_CAP ? (
          <div className="ix-more">
            <button type="button" className="ix-btn dr-hover" onClick={() => setShowAll(true)}>
              Show all {p.notes.length}
            </button>
          </div>
        ) : null}
        {p.hasMore ? (
          <div className="ix-more">
            <button type="button" className="ix-btn dr-hover" disabled={p.loadingMore} onClick={p.onMore}>
              {p.loadingMore ? "Loading…" : "Load more"}
            </button>
            {p.moreError ? <span className="ix-err">{p.moreError}</span> : null}
          </div>
        ) : null}
      </div>
    </section>
  );
}

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : "request failed";
}

function NoteRow({
  n,
  nowMs,
  projectName,
  onOpen,
  onToggle,
}: {
  n: Note;
  nowMs: number;
  projectName: string;
  onOpen: (n: Note) => void;
  onToggle: (n: Note) => void;
}) {
  const type = noteType(n);
  const where = noteWhere(n);
  const c = typeColor(type);
  const title = n.targetTitle || parseRef(n.ref, n.projectId)?.id || n.ref;
  return (
    <li className="ix-note" data-testid="note-row" data-resolved={n.resolved ? "1" : "0"}>
      <button type="button" className="ix-note-open dr-hover" onClick={() => onOpen(n)} aria-label={"Preview " + type + " " + title + ": " + n.text}>
        <span className="ix-note-top">
          <span className={"ix-state" + (n.resolved ? " done" : "")} aria-hidden="true" />
          <span className="ix-chip" style={{ color: c, borderColor: c }}>
            {type}
          </span>
          <span className="ix-note-title">{title}</span>
          {where ? <span className="ix-note-where">{where}</span> : null}
        </span>
        <span className={"ix-note-text" + (n.resolved ? " done" : "")}>{n.text}</span>
        <span className="ix-meta">
          {n.author} · {relTime(n.date, nowMs)} · {projectName}
        </span>
      </button>
      <div className="ix-note-act">
        <button
          type="button"
          className={"ix-toggle dr-hover" + (n.resolved ? " reopen" : "")}
          onClick={() => onToggle(n)}
          aria-label={(n.resolved ? "Reopen" : "Resolve") + " note " + n.id + " in " + projectName}
        >
          {n.resolved ? "Reopen" : "Resolve"}
        </button>
      </div>
    </li>
  );
}
