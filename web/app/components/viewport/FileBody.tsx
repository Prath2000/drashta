// File / doc preview body (02-ui-spec §2.4 "file"; prototype ViewCode.dc.html).
//
// - Live, windowed read: GET /projects/{p}/file?path=&from=1&to=N (N grows by 400 on "Show more").
// - Code with line numbers and syntax colours. Docs (.md/.txt) get Rendered / Source.
// - Line notes: click a line number -> composer -> POST /notes {ref, line, text}.
//   Existing notes sit under their line. If the note's `anchor` text no longer
//   matches that line, the card says "line moved?".
// - Binary, secret (redacted), missing and too-big files get a clear state.
// - A dirty file also shows its uncommitted diff (GET /projects/{p}/dirty).
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ApiError, qs } from "~/lib/api";
import { useApi } from "~/lib/query";
import type { Note, OpenAction, ProjectDetail } from "~/lib/types";
import type { ViewportBodyProps } from "./registry";
import { DiffView } from "./DiffView";
import { addNote, baseRef, ErrorLine, InlineText, Loading, NativeButtons, NoteCard, NoteComposer, Segs, useNative, useNotes } from "./common";
import { DwgBody } from "./DwgBody";
import { MediaFileBody, mediaOf } from "./MediaFileBody";
import { ModelBody } from "./ModelBody";
import { fmtDate, fmtSize, rel } from "./fmt";
import { langOf, tokenize, type Seg } from "./highlight";
import { inline as inlineCell, parseMd, parseText, type Block } from "./markdown";
import { parsePatch } from "./patch";
import { viewerOf } from "./viewers";
import type { DirtyDetail, FileObj, FileWindow } from "./vtypes";

/** Lines per window step. */
export const WINDOW = 400;
const BIG = 1 << 20;
const enc = encodeURIComponent;

/** `#L40` or `#L40-L52` -> {from, to}. */
export function lineFrag(frag: string | undefined): { from: number; to: number } | null {
  const m = frag?.match(/^L(\d+)(?:-L?(\d+))?$/);
  if (!m) return null;
  const from = +m[1];
  return { from, to: m[2] ? Math.max(from, +m[2]) : from };
}

/**
 * Picks the body by extension (viewers.ts): audio, video and PDF play in MediaFileBody, 3D models
 * in ModelBody, DWG drawings in DwgBody; everything else is read as text here. A file that is gone
 * from disk (exists === false) has nothing to draw or play, so it always falls to TextFileBody,
 * which says "Not on disk".
 */
export function FileBody(props: ViewportBodyProps) {
  const obj = (props.resolved.obj ?? {}) as FileObj;
  const path = obj.path ?? props.refObj.id;
  if (obj.exists === false) return <TextFileBody {...props} />;
  const viewer = viewerOf(path);
  if (viewer === "model") return <ModelBody {...props} />;
  if (viewer === "dwg") return <DwgBody {...props} />;
  const media = mediaOf(path);
  return media ? <MediaFileBody {...props} media={media} /> : <TextFileBody {...props} />;
}

function TextFileBody({ refStr, refObj, resolved }: ViewportBodyProps) {
  const p = resolved.projectId || refObj.projectId;
  const obj = (resolved.obj ?? {}) as FileObj;
  const path = obj.path ?? refObj.id;
  const ref = baseRef(refObj);
  const target = lineFrag(refObj.frag);
  const native = useNative(refStr);

  const [to, setTo] = useState(() => Math.max(WINDOW, target ? Math.ceil((target.to + 50) / WINDOW) * WINDOW : 0));
  const q = useApi<FileWindow>("/projects/" + enc(p) + "/file" + qs({ path, from: 1, to }));
  const last = useRef<FileWindow | undefined>(undefined);
  if (q.data) last.current = q.data;
  const data = q.data ?? last.current;

  const proj = useApi<ProjectDetail>("/projects/" + enc(p));
  const dirty = proj.data?.overview.dirty.find((d) => d.path === path);

  const notes = useNotes(ref, data?.notes ?? resolved.notes ?? []);
  const [lang, langLabel] = langOf(path);

  const nativeLabel = resolved.nativeLabel || "Open";
  const openBtn = (label = nativeLabel + " ↗") => (
    <button type="button" className="vp-btn amber-text" onClick={() => void native(resolved.nativeActions?.[0]?.action as OpenAction | undefined)}>
      {label}
    </button>
  );

  // ----- error and special states -----
  const err = q.error instanceof ApiError ? q.error : null;
  if (!data && q.loading) return <div className="vp-scroll"><Loading what="file" /></div>;
  if (!data && err && (err.status === 403 || err.code === "secret")) return <div className="vp-scroll"><Redacted path={path} openBtn={openBtn()} /></div>;
  if (!data && err && err.status === 404)
    return (
      <div className="vp-scroll">
        <div className="vp-panel" data-testid="file-missing">
          <span className="vp-label dim">Not on disk</span>
          <span className="big">This file cannot be read now.</span>
          <span className="why">{path} was in the last scan, but it may have moved or been deleted since.</span>
        </div>
      </div>
    );
  if (!data) return <div className="vp-scroll">{q.error ? <ErrorLine error={q.error} what="the file" /> : <Loading what="file" />}</div>;

  const size = data.size ?? obj.size ?? null;
  const meta = (
    <MetaStrip
      size={size}
      mtime={data.mtime ?? obj.mtime ?? null}
      lines={data.redacted || data.binary ? null : data.lines}
      langLabel={langLabel}
      dirtyState={dirty?.state}
      notes={notes}
    />
  );

  if (data.redacted)
    return (
      <div className="vp-scroll">
        {meta}
        <Redacted path={path} openBtn={openBtn()} />
        <FileNotes refStr={ref} projectId={p} loose={notes} showCode={false} />
      </div>
    );
  if (data.binary)
    return (
      <div className="vp-scroll">
        {meta}
        <div className="vp-panel" data-testid="file-binary">
          <span className="vp-label dim">No preview</span>
          <span className="big">Binary file — no text preview</span>
          <span className="why">
            {path} · {fmtSize(size)} · modified {fmtDate(data.mtime)}
          </span>
          <NativeButtons refStr={refStr} actions={resolved.nativeActions ?? []} />
        </div>
        <FileNotes refStr={ref} projectId={p} loose={notes} showCode={false} />
      </div>
    );

  return (
    <TextFile
      key={ref}
      refStr={ref}
      projectId={p}
      data={data}
      lang={lang}
      notes={notes}
      meta={meta}
      tooBig={size != null && size > BIG}
      target={target}
      dirtyState={dirty?.state}
      path={path}
      loadingMore={q.loading}
      onMore={() => setTo((t) => t + WINDOW)}
      openBtn={openBtn}
    />
  );
}

function Redacted({ path, openBtn }: { path: string; openBtn: ReactNode }) {
  return (
    <div className="vp-panel" data-testid="file-redacted">
      <span className="vp-label dim">Hidden</span>
      <span className="big">Secret file — contents are never shown</span>
      <span className="why">{path} looks like a secret (.env, *.pem, *.key, id_*). drashta lists it but never reads it into the page.</span>
      {openBtn}
    </div>
  );
}

function MetaStrip(p: { size: number | null; mtime: string | null; lines: number | null; langLabel: string; dirtyState?: string; notes: Note[] }) {
  const open = p.notes.filter((n) => !n.resolved).length;
  const state = p.dirtyState ? (p.dirtyState.includes("?") ? "untracked" : "modified " + p.dirtyState) : "clean";
  return (
    <div className="vp-meta" data-testid="file-meta">
      <Cell label="Size" value={fmtSize(p.size)} />
      <Cell label="Modified" value={p.mtime ? rel(p.mtime) : "—"} />
      <Cell label="Lines" value={p.lines == null ? "—" : String(p.lines)} />
      <Cell label="Language" value={p.langLabel} />
      <Cell label="State" value={state} cls={p.dirtyState ? "vp-coral" : "vp-teal"} />
      {p.notes.length ? <Cell label="Notes" value={open + " open / " + p.notes.length} cls={open ? "vp-coral" : "vp-muted"} /> : null}
    </div>
  );
}

function Cell({ label, value, cls }: { label: string; value: string; cls?: string }) {
  return (
    <div>
      <span className="vp-label dim">{label}</span>
      <span className={cls}>{value}</span>
    </div>
  );
}

interface TextFileProps {
  refStr: string;
  projectId: string;
  data: FileWindow;
  lang: ReturnType<typeof langOf>[0];
  notes: Note[];
  meta: ReactNode;
  tooBig: boolean;
  target: { from: number; to: number } | null;
  dirtyState?: string;
  path: string;
  loadingMore: boolean;
  onMore: () => void;
  openBtn: (label?: string) => ReactNode;
}

function TextFile(p: TextFileProps) {
  const { data, lang, notes } = p;
  const isDoc = !!data.isDoc || lang === "md";
  const [mode, setMode] = useState<"rendered" | "source">(isDoc && !p.target ? "rendered" : "source");
  const [composer, setComposer] = useState<number | null>(null);
  const [draft, setDraft] = useState("");
  const scrolled = useRef(false);

  const lines = useMemo(() => {
    const t = data.text.replace(/\r\n?/g, "\n");
    if (!t) return [] as string[];
    const l = t.split("\n");
    if (l.length > 1 && l[l.length - 1] === "") l.pop();
    return l;
  }, [data.text]);
  const from = data.from || 1;
  const shown = lines.length;
  const lastLine = from + shown - 1;
  const showRendered = isDoc && mode === "rendered";

  const byLine = useMemo(() => {
    const m = new Map<number, Note[]>();
    for (const n of notes) if (n.line != null && n.line >= from && n.line <= lastLine) m.set(n.line, [...(m.get(n.line) ?? []), n]);
    return m;
  }, [notes, from, lastLine]);

  const toks = useMemo(() => (showRendered ? [] : tokenize(lines, lang)), [lines, lang, showRendered]);
  const blocks = useMemo(() => (showRendered ? (lang === "md" ? parseMd(lines, from) : parseText(lines, from)) : []), [lines, lang, from, showRendered]);

  // Notes that are not shown under a line or a block go to "File notes".
  const used = new Set<string>();
  if (!showRendered) for (const list of byLine.values()) for (const n of list) used.add(n.id);
  const blockNotes = new Map<number, Note[]>();
  if (showRendered)
    blocks.forEach((b, bi) => {
      const bn: Note[] = [];
      for (let ln = b.from; ln <= Math.max(b.from, b.to); ln++)
        for (const n of byLine.get(ln) ?? [])
          if (!used.has(n.id)) {
            used.add(n.id);
            bn.push(n);
          }
      if (bn.length) blockNotes.set(bi, bn);
    });
  const loose = notes.filter((n) => !used.has(n.id));

  // Scroll the target line (#L40) into view once.
  useEffect(() => {
    if (scrolled.current || !p.target || showRendered) return;
    const el = document.getElementById("vp-L" + p.target.from);
    if (el && typeof el.scrollIntoView === "function") {
      el.scrollIntoView({ block: "center" });
      scrolled.current = true;
    }
  }, [p.target, showRendered, shown]);

  // Stable handlers (CodeLine is memoised), reading the latest state from a ref.
  const latest = useRef({ composer, draft });
  latest.current = { composer, draft };
  const onNum = useCallback((n: number) => {
    if (latest.current.composer === n) {
      setComposer(null);
      setDraft("");
    } else setComposer(n);
  }, []);
  const onCancel = useCallback(() => {
    setComposer(null);
    setDraft("");
  }, []);
  const { refStr, projectId } = p;
  const onAdd = useCallback(
    async (n: number) => {
      const text = latest.current.draft.trim();
      if (!text) return;
      const note = await addNote({ ref: refStr, line: n, text }, projectId);
      if (note) {
        setComposer(null);
        setDraft("");
      }
    },
    [refStr, projectId],
  );

  const gutterW = 26 + String(lastLine).length * 8;
  const truncated = data.truncated;

  return (
    <div className="vp-scroll" data-testid="file-body">
      {p.meta}
      <div className="vp-toolbar">
        {isDoc ? (
          <div className="vp-seg" role="group" aria-label="View mode">
            <button type="button" aria-pressed={showRendered} onClick={() => { setMode("rendered"); setComposer(null); setDraft(""); }}>
              Rendered
            </button>
            <button type="button" aria-pressed={!showRendered} onClick={() => setMode("source")}>
              Source
            </button>
          </div>
        ) : null}
        <span className="vp-faint">
          {showRendered ? "Notes appear under the block they point at · switch to Source to pin a note on a line" : "Click a line number to pin a note"}
        </span>
      </div>
      {p.tooBig ? (
        <div className="vp-banner" data-testid="file-toobig">
          Large file ({fmtSize(data.size)}): the preview reads the first part only. {p.openBtn("Open ↗ for all of it")}
        </div>
      ) : null}

      {showRendered ? (
        <Rendered blocks={blocks} blockNotes={blockNotes} />
      ) : shown === 0 ? (
        <div className="vp-banner">Empty file.</div>
      ) : (
        <div className="vp-code" data-testid="code">
          {lines.map((text, idx) => {
            const n = from + idx;
            return (
              <CodeLine
                key={n}
                n={n}
                segs={toks[idx] ?? []}
                text={text}
                lines={lines}
                from={from}
                notes={byLine.get(n)}
                gutterW={gutterW}
                hl={!!p.target && n >= p.target.from && n <= p.target.to}
                composing={composer === n}
                draft={composer === n ? draft : ""}
                onNum={onNum}
                onDraft={setDraft}
                onCancel={onCancel}
                onAdd={onAdd}
              />
            );
          })}
        </div>
      )}

      {truncated ? (
        <div className="vp-trunc" data-testid="file-truncated">
          <span>{data.lines > lastLine ? "first " + lastLine + " of " + data.lines + " lines" : "first " + lastLine + " lines"}</span>
          <span style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
            <button type="button" className="vp-btn sm" disabled={p.loadingMore} onClick={p.onMore}>
              {p.loadingMore ? "Loading…" : "Show next " + WINDOW + " lines"}
            </button>
            {p.openBtn("Open ↗ for the rest")}
          </span>
        </div>
      ) : null}

      {p.dirtyState ? <DirtySection projectId={p.projectId} path={p.path} /> : null}

      <FileNotes refStr={p.refStr} projectId={p.projectId} loose={loose} showCode={!showRendered} />
    </div>
  );
}

interface CodeLineProps {
  n: number;
  segs: Seg[];
  text: string;
  lines: string[];
  from: number;
  notes: Note[] | undefined;
  gutterW: number;
  hl: boolean;
  composing: boolean;
  draft: string;
  onNum: (n: number) => void;
  onDraft: (s: string) => void;
  onCancel: () => void;
  onAdd: (n: number) => void;
}

/** One code line + its notes + the composer. Memoised: typing in one composer re-renders only that line. */
const CodeLine = memo(function CodeLine(p: CodeLineProps) {
  const notes = p.notes ?? [];
  const hasOpen = notes.some((x) => !x.resolved);
  const gutterCls = notes.length ? (hasOpen ? " open" : " done") : p.composing ? " composing" : "";
  const rowCls = p.composing ? " composing" : notes.length && hasOpen ? " open-notes" : p.hl ? " hl" : "";
  const aria = "Line " + p.n + (notes.length ? ", " + notes.length + " note" + (notes.length > 1 ? "s" : "") : "") + " — add a note";
  return (
    <div>
      <div className={"vp-line" + rowCls} id={"vp-L" + p.n}>
        <button
          type="button"
          className={"vp-gutter" + gutterCls}
          style={{ width: p.gutterW }}
          aria-label={aria}
          title="Add a note on this line"
          onClick={() => p.onNum(p.n)}
        >
          {notes.length ? "● " : ""}
          {p.n}
        </button>
        <div className="vp-src">
          <Segs segs={p.segs} />
        </div>
      </div>
      {notes.map((x) => (
        <NoteCard key={x.id} note={x} hint={<MovedHint note={x} lines={p.lines} from={p.from} />} />
      ))}
      {p.composing ? (
        <div className="vp-composer" data-testid="line-composer">
          <label htmlFor="vc-line-note" className="vp-label">
            Note on line {p.n}
          </label>
          <textarea
            id="vc-line-note"
            className="vp-input"
            rows={3}
            autoFocus
            value={p.draft}
            placeholder="What should change here?"
            onChange={(e) => p.onDraft(e.target.value)}
          />
          <div className="vp-actions">
            <button type="button" className="vp-btn ghost" onClick={p.onCancel}>
              Cancel
            </button>
            <button type="button" className="vp-btn primary" disabled={!p.draft.trim()} onClick={() => p.onAdd(p.n)}>
              Add note
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
});

/** "line moved?" when the note's anchor text is no longer on its line. */
export function MovedHint({ note, lines, from }: { note: Note; lines: string[]; from: number }) {
  const anchor = note.anchor?.trim();
  if (!anchor || note.line == null) return null;
  const cur = lines[note.line - from];
  if (cur != null && cur.trim() === anchor) return null;
  const at = lines.findIndex((l) => l.trim() === anchor);
  const title = "This note was written on: " + anchor;
  return (
    <span className="moved" title={title} data-testid="line-moved">
      {at >= 0 ? "line moved? now line " + (at + from) : "line moved?"}
    </span>
  );
}

function Rendered({ blocks, blockNotes }: { blocks: Block[]; blockNotes: Map<number, Note[]> }) {
  return (
    <div className="vp-md" data-testid="rendered">
      {blocks.map((b, i) => (
        <div key={i}>
          <BlockView b={b} />
          {(blockNotes.get(i) ?? []).map((n) => (
            <NoteCard key={n.id} note={n} variant="block" />
          ))}
        </div>
      ))}
    </div>
  );
}

function BlockView({ b }: { b: Block }) {
  switch (b.kind) {
    case "h1":
      return <h1><InlineText segs={b.segs} /></h1>;
    case "h2":
      return <h2><InlineText segs={b.segs} /></h2>;
    case "h3":
      return <h3><InlineText segs={b.segs} /></h3>;
    case "p":
      return <p className={b.pre ? "pre" : undefined}><InlineText segs={b.segs} /></p>;
    case "quote":
      return (
        <div className="quote">
          <span className="q">“</span>
          <span><InlineText segs={b.segs} /></span>
        </div>
      );
    case "hr":
      return <div className="hr" role="separator" />;
    case "code":
      return (
        <div className="codeblock">
          {b.code.map((segs, i) => (
            <div key={i}><Segs segs={segs} /></div>
          ))}
        </div>
      );
    case "table":
      return (
        <div className="tablewrap">
          <table>
            <tbody>
              {b.rows.map((r, ri) => (
                <tr key={ri} className={ri === 0 ? "head" : undefined}>
                  {r.map((c, ci) => (
                    <td key={ci}><InlineText segs={inlineCell(c)} /></td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
    case "list":
      return (
        <div className="list">
          {b.items.map((it, k) => (
            <div key={k} className="li" style={{ paddingLeft: it.indent * 18 }}>
              <span className="mk">{b.ordered ? it.marker : "–"}</span>
              <span className="tx"><InlineText segs={inlineCell(it.text)} /></span>
            </div>
          ))}
        </div>
      );
  }
}


/** The uncommitted diff of this one file (from GET /projects/{p}/dirty). */
function DirtySection({ projectId, path }: { projectId: string; path: string }) {
  const q = useApi<DirtyDetail>("/projects/" + enc(projectId) + "/dirty");
  const [open, setOpen] = useState<Record<number, boolean>>({});
  const files = useMemo(() => parsePatch(q.data?.diff).filter((f) => f.path === path), [q.data?.diff, path]);
  return (
    <div className="vp-csec diff" style={{ padding: "14px 14px 0" }} data-testid="dirty-diff">
      <div className="vp-section-head">
        <span className="vp-label">Uncommitted changes</span>
        <span className="vp-faint">{q.loading && !q.data ? "loading…" : files.length ? "+" + files[0].add + " −" + files[0].del + " vs last commit" : "no text diff (new or binary file)"}</span>
      </div>
      {q.error ? <ErrorLine error={q.error} what="the working-copy diff" /> : null}
      <DiffView files={files} open={open} onToggle={(i, o) => setOpen((m) => ({ ...m, [i]: o }))} openFirst={1} />
      {q.data?.truncated ? <div className="vp-faint">The working-copy diff was cut at 400 KB.</div> : null}
    </div>
  );
}

function FileNotes({ refStr, projectId, loose, showCode }: { refStr: string; projectId: string; loose: Note[]; showCode: boolean }) {
  return (
    <div className="vp-notes-sec" data-testid="file-notes">
      <div className="vp-section-head">
        <span className="vp-label">File notes</span>
        <span className="vp-faint">{loose.length ? loose.length + (showCode ? " on the file or beyond the preview" : "") : "none yet — pin one to the whole file"}</span>
      </div>
      {loose.map((n) => (
        <NoteCard key={n.id} note={n} variant="flat" />
      ))}
      <NoteComposer refStr={refStr} projectId={projectId} id="vc-file-note" label="Note on the whole file" placeholder="Note on the whole file…" />
    </div>
  );
}
