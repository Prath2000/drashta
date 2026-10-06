// Per-file unified diff blocks with syntax colours (commit and dirty diffs).
// Each file block is collapsible. Long files show the first 300 lines and a
// "Show all N lines" button (lists over ~200 rows are capped).
import { memo, useMemo, useState } from "react";
import { Segs } from "./common";
import { langOf, tokenize } from "./highlight";
import type { DiffFile } from "./patch";

const LINE_CAP = 300;

export interface DiffViewProps {
  files: DiffFile[];
  /** Open state per file index; missing = default (the first `openFirst` files open). */
  open: Record<number, boolean>;
  onToggle: (idx: number, open: boolean) => void;
  openFirst?: number;
}

export function DiffView({ files, open, onToggle, openFirst = 3 }: DiffViewProps) {
  return (
    <>
      {files.map((f, idx) => {
        const isOpen = open[idx] ?? idx < openFirst;
        return <DiffFileBlock key={f.path + idx} file={f} open={isOpen} onToggle={() => onToggle(idx, !isOpen)} />;
      })}
    </>
  );
}

const DiffFileBlock = memo(function DiffFileBlock({ file, open, onToggle }: { file: DiffFile; open: boolean; onToggle: () => void }) {
  const metaLabel = file.meta.length ? file.meta[0].replace(/ mode \d+$/, "") : "";
  return (
    <div className="vp-dfile" data-testid="diff-file">
      <button type="button" className="vp-dhead" aria-expanded={open} onClick={onToggle}>
        <span className="caret" aria-hidden="true">
          {open ? "▾" : "▸"}
        </span>
        <span className="p">{file.path}</span>
        {metaLabel ? <span className="m">{metaLabel}</span> : null}
        <span className="vp-teal">+{file.add}</span>
        <span className="vp-coral">−{file.del}</span>
      </button>
      {open ? <DiffBody file={file} /> : null}
    </div>
  );
});

function DiffBody({ file }: { file: DiffFile }) {
  const [all, setAll] = useState(false);
  const lang = langOf(file.path)[0];
  const total = file.hunks.reduce((a, h) => a + h.lines.length + 1, 0);
  // Tokenize each hunk on its own (a hunk can start inside a block).
  const toks = useMemo(() => file.hunks.map((h) => tokenize(h.lines.map((l) => (l.k === "\\" ? "" : l.t)), lang)), [file, lang]);
  if (!file.hunks.length) {
    return (
      <div className="vp-dbody">
        <div style={{ padding: "10px 12px", color: "var(--dim)" }}>{file.meta.length ? file.meta.join(" · ") : "No textual changes in the captured patch."}</div>
      </div>
    );
  }
  let budget = all ? Infinity : LINE_CAP;
  const out = [];
  for (let hi = 0; hi < file.hunks.length && budget > 0; hi++) {
    const h = file.hunks[hi];
    budget--;
    const rows = [];
    for (let li = 0; li < h.lines.length && budget > 0; li++, budget--) {
      const d = h.lines[li];
      const cls = d.k === "+" ? " add" : d.k === "-" ? " del" : d.k === "\\" ? " meta" : "";
      rows.push(
        <div key={li} className={"vp-dl" + cls}>
          <span className="no">{d.o}</span>
          <span className="no">{d.n}</span>
          <span className="sg">{d.k === "+" ? "+" : d.k === "-" ? "−" : ""}</span>
          <span className="tx">{d.k === "\\" ? d.t : <Segs segs={toks[hi][li]} />}</span>
        </div>,
      );
    }
    out.push(
      <div key={hi}>
        <div className="vp-hunkhead">{h.header}</div>
        {rows}
      </div>,
    );
  }
  return (
    <div className="vp-dbody">
      {out}
      {!all && total > LINE_CAP ? (
        <div className="vp-dmore">
          <span>
            showing {LINE_CAP} of {total} lines
          </span>
          <button type="button" className="vp-btn sm" style={{ marginLeft: "auto" }} onClick={() => setAll(true)}>
            Show all {total} lines
          </button>
        </div>
      ) : null}
    </div>
  );
}
