import { useMemo } from "react";
import type { DayResponse } from "~/lib/types";
import { plural } from "./geometry";
import { buildDayView, linesText, quietText, type ColModel } from "./model";

export interface ReadoutSel {
  id: string;
  name: string;
  col: ColModel;
}

export interface DayReadoutProps {
  /** The selected day, or null (the legend shows). Always null in readout mode. */
  sel: ReadoutSel | null;
  /** Browsing mode shows the "click any day" sentence under no selection. */
  browsing: boolean;
  /** GET /day for the selected day (undefined until it arrives). */
  day: DayResponse | undefined;
  dayError: boolean;
  onOpenProject: (id: string) => void;
  onOpenItem: (ref: string) => void;
}

function Legend({ browsing }: { browsing: boolean }) {
  return (
    <div className="hm-legend-wrap" data-testid="home-legend">
      {browsing ? <div className="hm-hint">click any day in a lane to read its tremor here.</div> : null}
      <div className="hm-legend">
        <div>
          <span style={{ color: "var(--dim)" }}>▮</span> file changes that day
        </div>
        <div>
          <span style={{ color: "var(--amber)" }}>▮</span> commits (height = lines changed, log scale)
        </div>
        <div>
          <span className="tick" />
          claude session that day
        </div>
        <div>
          <span style={{ color: "var(--teal)" }}>●</span> servers live now
        </div>
        <div>
          <span style={{ color: "var(--amber)" }}>±n</span> uncommitted paths
        </div>
        <div>name → project page</div>
      </div>
    </div>
  );
}

/** The 128px day readout under the lanes. */
export function DayReadout({ sel, browsing, day, dayError, onOpenProject, onOpenItem }: DayReadoutProps) {
  const view = useMemo(() => (sel && day ? buildDayView(day, sel.id, sel.col.files) : null), [sel, day]);

  let body;
  if (!sel) {
    body = <Legend browsing={browsing} />;
  } else {
    const c = sel.col;
    const tint = (n: number, color: string) => (n > 0 ? color : "var(--muted)");
    const counts = [
      { k: "file changes", v: String(c.files), color: tint(c.files, "var(--text)") },
      { k: "commits", v: String(c.commits), color: tint(c.commits, "var(--amber)") },
      { k: "lines", v: linesText(c), color: c.commits ? "var(--text)" : "var(--muted)" },
      { k: "sessions", v: String(c.sessions), color: tint(c.sessions, "var(--violet)") },
      { k: "snapshots", v: String(c.snapshots), color: tint(c.snapshots, "var(--ivory)") },
      { k: "notes", v: String(c.notes), color: tint(c.notes, "var(--coral)") },
    ];
    const quiet = view ? quietText(c, view.items.length) : "";
    // Files exist only when the server lists them; otherwise say so once.
    const countLine = view && c.files > 0 && view.files.length === 0 ? plural(c.files, "file change") : "";
    body = (
      <>
        <div className="hm-z1">
          <div className="dr-label">Readout</div>
          <div className="hm-date" data-testid="readout-date">
            {c.label}
          </div>
          <button type="button" className="hm-proj-link" aria-label={"Open " + sel.name + " project page"} onClick={() => onOpenProject(sel.id)}>
            {sel.name} →
          </button>
        </div>
        <div className="hm-z2" data-testid="readout-counts">
          {counts.map((k) => (
            <div className="row" key={k.k}>
              <span className="k">{k.k}</span>
              <span style={{ color: k.color }}>{k.v}</span>
            </div>
          ))}
        </div>
        <div className="hm-z3" data-testid="readout-items" aria-busy={!view && !dayError}>
          {dayError && !view ? <div className="hm-quiet">could not read this day.</div> : null}
          {view?.items.map((it) => (
            <button
              key={it.key}
              type="button"
              className="hm-item"
              aria-label={it.aria}
              disabled={!it.ref}
              onClick={() => it.ref && onOpenItem(it.ref)}
            >
              <span className="t">{it.time}</span>
              <span className="g" aria-hidden="true" style={{ color: it.color }}>
                {it.glyph}
              </span>
              <span className="lead" style={{ color: it.color }}>
                {it.lead}
              </span>
              <span className="label">{it.label}</span>
              <span className="ctx">{it.ctx}</span>
            </button>
          ))}
          {view && view.moreItems > 0 ? <div className="hm-fmore">{"+" + view.moreItems + " more items"}</div> : null}
          {quiet ? <div className="hm-quiet">{quiet}</div> : null}
          {countLine ? <div className="hm-countline">{countLine} — paths are not listed for this day.</div> : null}
          {view && view.files.length > 0 ? (
            <>
              <div className="hm-fhead">{"files changed · " + c.files}</div>
              {view.files.map((f) => (
                <button key={f.path} type="button" className="hm-file" aria-label={f.aria} onClick={() => onOpenItem(f.ref)}>
                  <span>{f.path}</span>
                </button>
              ))}
              {view.moreFiles > 0 ? <div className="hm-fmore">{"+" + view.moreFiles + " more"}</div> : null}
            </>
          ) : null}
        </div>
      </>
    );
  }
  return (
    <div className="hm-panel" role="region" aria-label="Day readout">
      {body}
    </div>
  );
}
