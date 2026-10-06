import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { api } from "~/lib/api";
import { KIND_COLOR } from "~/lib/format";
import { findProject, useBoot } from "~/lib/boot";
import { useEscapeLayer, useShortcutAction } from "~/lib/shortcuts";
import { setSearch, setSearchLit, useStore } from "~/lib/store";
import type { SearchHit, SearchKind } from "~/lib/types";
import { useGo, useItem } from "~/lib/url";

// Group order and names in the results panel (02-ui-spec §2.2).
const GROUPS: { name: string; kinds: SearchKind[]; color: string }[] = [
  { name: "Projects", kinds: ["project"], color: KIND_COLOR.project },
  { name: "Files", kinds: ["file", "doc"], color: KIND_COLOR.file },
  { name: "Commits", kinds: ["commit"], color: KIND_COLOR.commit },
  { name: "Sessions", kinds: ["session"], color: KIND_COLOR.session },
  { name: "Tasks", kinds: ["task"], color: KIND_COLOR.task },
  { name: "Snapshots", kinds: ["snapshot"], color: KIND_COLOR.snapshot },
  { name: "Notes", kinds: ["note"], color: KIND_COLOR.note },
  { name: "Servers", kinds: ["server"], color: KIND_COLOR.server },
];

export interface HitGroup {
  name: string;
  color: string;
  hits: SearchHit[];
}

/** Groups hits in the fixed kind order; drops empty groups. */
export function groupHits(hits: SearchHit[]): HitGroup[] {
  return GROUPS.map((g) => ({ name: g.name, color: g.color, hits: hits.filter((h) => g.kinds.includes(h.kind)) })).filter(
    (g) => g.hits.length > 0,
  );
}

/**
 * The one search field and its results panel (GET /api/search).
 * Updates as you type; nothing opens until you choose a result.
 * Keys on the field: Enter opens the selected (first) result, ↑/↓ move, Esc
 * closes the panel, a second Esc clears the query. The global keys (lib/shortcuts.ts)
 * use two hooks here: `/` focuses the field, and Esc closes the open panel even when
 * the field has lost focus. An Esc with nothing to close here goes on to the next layer.
 */
export function SearchBox() {
  const query = useStore((s) => s.search);
  const [open, setOpen] = useState(false);
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [sel, setSel] = useState(0);
  const ctrl = useRef<AbortController | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const { data: boot } = useBoot();
  const go = useGo();
  const { openItem } = useItem();

  const groups = useMemo(() => groupHits(hits), [hits]);
  const flat = useMemo(() => groups.flatMap((g) => g.hits), [groups]);
  const lit = useMemo(() => Array.from(new Set(hits.map((h) => h.projectId).filter(Boolean))), [hits]);

  useEffect(() => {
    const q = query.trim();
    ctrl.current?.abort();
    if (!q || q === ":") {
      setHits([]);
      setLoaded(false);
      return;
    }
    const c = new AbortController();
    ctrl.current = c;
    api.search(q, {}, c.signal).then(
      (r) => {
        if (c.signal.aborted) return;
        setHits(r.hits ?? []);
        setLoaded(true);
        setSel(0);
      },
      () => {
        if (c.signal.aborted) return;
        setHits([]);
        setLoaded(true);
      },
    );
    return () => c.abort();
  }, [query]);

  // Home reads the lit project ids to dim the other lanes.
  useEffect(() => {
    if (loaded) setSearchLit(lit);
  }, [lit, loaded]);

  const focusField = useCallback(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);
  useShortcutAction("focusSearch", focusField);
  useEscapeLayer("search", open, () => setOpen(false));

  const choose = (h: SearchHit | undefined) => {
    if (!h) return;
    setOpen(false);
    if (h.kind === "project") go.project(h.projectId);
    else openItem(h.ref);
  };

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Escape") {
      // Handle it here only when there is something to close. Otherwise leave it alone,
      // so the global Esc order goes on (selected day, then the viewport).
      if (open) {
        e.preventDefault();
        setOpen(false);
      } else if (query) {
        e.preventDefault();
        setSearch("");
      }
      return;
    }
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp" && e.key !== "Enter") return;
    if (!query.trim()) return;
    if (!open) {
      setOpen(true);
      return;
    }
    if (!flat.length) return;
    e.preventDefault();
    if (e.key === "Enter") {
      choose(flat[Math.max(0, Math.min(sel, flat.length - 1))]);
      return;
    }
    setSel((s) => Math.max(0, Math.min(s + (e.key === "ArrowDown" ? 1 : -1), flat.length - 1)));
  };

  const q = query.trim();
  const projName = (id: string) => findProject(boot, id)?.name ?? id;
  let i = 0;
  return (
    <>
      <label className={"dr-search" + (open ? " open" : "")}>
        <span className="dr-label">Search</span>
        <input
          ref={inputRef}
          type="text"
          value={query}
          aria-label="Search"
          aria-expanded={open}
          aria-controls="dr-search-results"
          placeholder="name, commit, doc, :port, stack"
          onChange={(e) => {
            setSearch(e.target.value);
            setSel(0);
            setOpen(true);
          }}
          onClick={() => setOpen(true)}
          onKeyDown={onKey}
        />
      </label>
      {open ? (
        <>
          <div className="dr-palette-backdrop" onClick={() => setOpen(false)} />
          <div role="dialog" aria-label="Search results" id="dr-search-results" className="dr-palette">
            <div className="dr-palette-head">
              <span className="q">{q ? "“" + q + "”" : "name, commit, doc, :port or stack"}</span>
              {q && loaded ? (
                <span className="dr-label">
                  {hits.length} {hits.length === 1 ? "match" : "matches"} · {lit.length} {lit.length === 1 ? "lane" : "lanes"} lit
                </span>
              ) : null}
              <button type="button" className="dr-icon-btn" aria-label="Close search" onClick={() => setOpen(false)}>
                ✕
              </button>
            </div>
            <div className="dr-palette-list">
              {!q ? (
                <div className="dr-palette-empty">type to search projects, files, commits, sessions, tasks, snapshots, notes and ports.</div>
              ) : loaded && hits.length === 0 ? (
                <div className="dr-palette-empty">nothing registers for that.</div>
              ) : null}
              {groups.map((g) => (
                <div key={g.name}>
                  <div className="dr-palette-group">
                    <span className="dr-label" style={{ color: g.color }}>
                      {g.name}
                    </span>
                    <span className="rule" />
                    <span className="dr-meta" style={{ fontSize: 10 }}>
                      {g.hits.length}
                    </span>
                  </div>
                  {g.hits.map((h) => {
                    const idx = i++;
                    return (
                      <button
                        key={h.kind + ":" + h.ref}
                        type="button"
                        className={"dr-hit dr-hover" + (idx === sel ? " sel" : "")}
                        onClick={() => choose(h)}
                      >
                        <span className="txt">
                          <span className="t">{h.title}</span>
                          <span className="s">{h.snippet}</span>
                        </span>
                        <span className="p">{h.kind === "project" ? "" : projName(h.projectId)}</span>
                      </button>
                    );
                  })}
                </div>
              ))}
            </div>
          </div>
        </>
      ) : null}
    </>
  );
}
