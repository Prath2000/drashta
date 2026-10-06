// Files tab: a lazy tree (one GET /tree?dir=&depth=1 per opened folder) and
// a file search (GET /search with kind=file). Skipped folders (node_modules,
// .git, dist, target, venv) show as "(not indexed)" and do not open.
import { useEffect, useState, type CSSProperties } from "react";
import { useApi } from "~/lib/query";
import type { DirtyFile, Note, SearchResponse, TreeEntry, TreeResponse } from "~/lib/types";
import { CAP, keys } from "./data";
import { base, IMAGE_RE, rel, size } from "./fmt";
import { fileRef } from "./OverviewTab";
import { C, smallBtn } from "./ui";

const COLS = "minmax(0, 1fr) 70px 90px 110px";

const rowBase: CSSProperties = {
  width: "100%",
  minHeight: 32,
  display: "grid",
  gridTemplateColumns: COLS,
  gap: 12,
  alignItems: "center",
  padding: "3px 16px",
  background: "transparent",
  border: 0,
  borderBottom: "1px solid " + C.raised,
  fontSize: 12,
  textAlign: "left",
};

interface Ctx {
  p: string;
  expanded: Set<string>;
  toggle: (dir: string) => void;
  dirtyOf: (path: string) => string | undefined;
  dirtyUnder: (dir: string) => boolean;
  notesOf: (path: string) => number;
  openItem: (r: string) => void;
}

function FileRow({ e, depth, ctx }: { e: TreeEntry; depth: number; ctx: Ctx }) {
  const name = base(e.path);
  const isImg = IMAGE_RE.test(e.path);
  const nc = ctx.notesOf(e.path);
  const dirty = e.dirty || ctx.dirtyOf(e.path);
  return (
    <button type="button" className="pj-hover" onClick={() => ctx.openItem(fileRef(ctx.p, e.path))} style={{ ...rowBase, color: C.text, cursor: "pointer" }}>
      <span style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, paddingLeft: depth * 18 }}>
        <span style={{ width: 10, color: isImg ? C.ivory : C.dim }}>{isImg ? "◇" : "·"}</span>
        <span className="pj-trunc">{name}</span>
        {dirty ? (
          <span title="uncommitted change" style={{ color: C.amber }}>
            ±
          </span>
        ) : null}
      </span>
      <span style={{ textAlign: "right" }}>
        {nc > 0 ? (
          <span
            title={nc + " open note" + (nc === 1 ? "" : "s")}
            style={{
              display: "inline-block",
              minWidth: 18,
              padding: "1px 5px",
              border: "1px solid " + C.coral,
              color: C.coral,
              fontSize: 10,
              textAlign: "center",
              borderRadius: 2,
            }}
          >
            {nc}
          </span>
        ) : null}
      </span>
      <span style={{ textAlign: "right", color: C.muted }}>{size(e.size)}</span>
      <span style={{ textAlign: "right", color: C.muted }}>{e.mtime ? rel(e.mtime) : ""}</span>
    </button>
  );
}

function DirRow({ e, depth, ctx }: { e: TreeEntry; depth: number; ctx: Ctx }) {
  const notIndexed = !!e.collapsed;
  const open = !notIndexed && ctx.expanded.has(e.path);
  return (
    <>
      <button
        type="button"
        className="pj-hover"
        aria-expanded={notIndexed ? false : open}
        disabled={notIndexed}
        onClick={() => !notIndexed && ctx.toggle(e.path)}
        style={{ ...rowBase, color: notIndexed ? C.muted : C.text, cursor: notIndexed ? "default" : "pointer" }}
      >
        <span style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, paddingLeft: depth * 18 }}>
          <span style={{ width: 10, color: notIndexed ? C.dim : C.muted }}>{open ? "▾" : "▸"}</span>
          <span className="pj-trunc">{base(e.path)}/</span>
          {ctx.dirtyUnder(e.path) ? (
            <span title="uncommitted change inside" style={{ color: C.amber }}>
              ±
            </span>
          ) : null}
          {notIndexed ? <span style={{ color: C.muted, fontSize: 11 }}>(not indexed)</span> : null}
        </span>
        <span />
        <span />
        <span style={{ textAlign: "right", color: C.muted }}>{e.mtime ? rel(e.mtime) : ""}</span>
      </button>
      {open ? <Level dir={e.path} depth={depth + 1} ctx={ctx} /> : null}
    </>
  );
}

/** One folder level: fetched when shown, capped at 200 rows with "show all N". */
function Level({ dir, depth, ctx }: { dir: string; depth: number; ctx: Ctx }) {
  const { data, loading, error } = useApi<TreeResponse>(keys.tree(ctx.p, dir));
  const [all, setAll] = useState(false);
  const pad: CSSProperties = { padding: "6px 16px 6px " + (16 + depth * 18 + 18) + "px", fontSize: 11 };
  if (loading && !data) return <div style={{ ...pad, color: C.muted }}>reading…</div>;
  if (error && !data) return <div style={{ ...pad, color: C.coral }}>could not read this folder.</div>;
  const prefix = dir ? dir + "/" : "";
  // only direct children (a server may send deeper levels too)
  const entries = (data?.entries ?? [])
    .filter((e) => e.path.startsWith(prefix) && !e.path.slice(prefix.length).includes("/") && e.path !== dir)
    .sort((a, b) => Number(b.dir) - Number(a.dir) || a.path.localeCompare(b.path));
  if (!entries.length) return <div style={{ ...pad, color: C.muted }}>{dir ? "empty folder." : "empty directory — nothing to list."}</div>;
  const shown = all ? entries : entries.slice(0, CAP);
  return (
    <>
      {shown.map((e) => (e.dir ? <DirRow key={e.path} e={e} depth={depth} ctx={ctx} /> : <FileRow key={e.path} e={e} depth={depth} ctx={ctx} />))}
      {entries.length > shown.length ? (
        <div style={{ ...pad, display: "flex", justifyContent: "space-between", alignItems: "center", color: C.muted }}>
          <span>
            first {shown.length} of {entries.length} shown
          </span>
          <button type="button" className="pj-hover" onClick={() => setAll(true)} style={{ ...smallBtn, color: C.amber }}>
            show all {entries.length}
          </button>
        </div>
      ) : null}
    </>
  );
}

function SearchResults({ p, q, ctx }: { p: string; q: string; ctx: Ctx }) {
  const { data, loading, error } = useApi<SearchResponse>(keys.fileSearch(p, q));
  if (loading && !data) return <div style={{ padding: "10px 16px", fontSize: 12, color: C.muted }}>searching…</div>;
  if (error && !data) return <div style={{ padding: "10px 16px", fontSize: 12, color: C.coral }}>search failed.</div>;
  const hits = (data?.hits ?? []).filter((h) => h.projectId === p && (h.kind === "file" || h.kind === "doc"));
  if (!hits.length) return <div style={{ padding: "14px 16px", fontSize: 14, color: C.muted }}>no file matches “{q}”.</div>;
  return (
    <>
      {hits.map((h) => {
        const path = h.ref.split(":file:")[1] ?? h.snippet ?? h.title;
        return <FileRow key={h.ref} e={{ path, dir: false, size: null, mtime: h.ts, hasChildren: false }} depth={0} ctx={ctx} />;
      })}
    </>
  );
}

export function FilesTab(props: { p: string; total: number; dirty: DirtyFile[]; notes: Note[]; openItem: (r: string) => void }) {
  const { p, openItem } = props;
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const [text, setText] = useState("");
  const [q, setQ] = useState("");
  useEffect(() => {
    const t = setTimeout(() => setQ(text.trim()), 200);
    return () => clearTimeout(t);
  }, [text]);

  const dirtyMap = new Map(props.dirty.map((d) => [d.path, d.state]));
  const noteCount = new Map<string, number>();
  for (const n of props.notes) {
    if (n.resolved) continue;
    const m = n.ref.match(/^[^:]+:(?:file|image):([^#]+)/);
    if (m) noteCount.set(m[1], (noteCount.get(m[1]) ?? 0) + 1);
  }
  const ctx: Ctx = {
    p,
    expanded,
    toggle: (dir) =>
      setExpanded((s) => {
        const n = new Set(s);
        if (n.has(dir)) n.delete(dir);
        else n.add(dir);
        return n;
      }),
    dirtyOf: (path) => dirtyMap.get(path),
    dirtyUnder: (dir) => props.dirty.some((d) => d.path.startsWith(dir + "/")),
    notesOf: (path) => noteCount.get(path) ?? 0,
    openItem,
  };

  return (
    <div data-testid="tab-files">
      <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 12, flexWrap: "wrap" }}>
        <label htmlFor="pj-file-search" style={{ fontSize: 10, letterSpacing: ".14em", textTransform: "uppercase", color: C.muted }}>
          Find file
        </label>
        <input
          id="pj-file-search"
          className="pj-search"
          type="search"
          value={text}
          placeholder="name or path"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            // Clear the filter first. Only an Esc with nothing to clear goes on to the
            // global order (selected day, then the viewport).
            if (e.key === "Escape" && text) {
              e.preventDefault();
              setText("");
            }
          }}
        />
        {q ? (
          <button type="button" className="pj-hover" onClick={() => setText("")} style={{ ...smallBtn, color: C.muted }}>
            clear
          </button>
        ) : null}
      </div>
      <div style={{ border: "1px solid " + C.hair, background: C.panel }}>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: COLS,
            gap: 12,
            padding: "10px 16px",
            borderBottom: "1px solid " + C.hair,
            fontSize: 10,
            letterSpacing: ".12em",
            textTransform: "uppercase",
            color: C.muted,
          }}
        >
          <span>Path · {q ? "matches for “" + q + "”" : props.total + " indexed files"}</span>
          <span style={{ textAlign: "right" }}>Notes</span>
          <span style={{ textAlign: "right" }}>Size</span>
          <span style={{ textAlign: "right" }}>Modified</span>
        </div>
        {q ? <SearchResults p={p} q={q} ctx={ctx} /> : <Level dir="" depth={0} ctx={ctx} />}
      </div>
    </div>
  );
}
