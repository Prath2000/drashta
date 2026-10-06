import { memo, type KeyboardEvent, type MouseEvent } from "react";
import type { ColModel, LaneModel } from "./model";
import { plural } from "./geometry";

export type PickHow = "toggle" | "select";

export interface LaneProps {
  m: LaneModel;
  /** Days on screen (7 or 30). Only used for the "flat" note and the 7d separators. */
  range: number;
  /** Browsing mode: day columns are buttons. Readout mode: plain bars, no handlers. */
  interactive: boolean;
  /** Index of the selected day in this lane, or -1. */
  selIdx: number;
  /** A search is active and this lane is one of its hits. */
  match: boolean;
  /** A search is active and this lane is not a hit. */
  dim: boolean;
  onOpen: (id: string) => void;
  onPick: (id: string, i: number, how: PickHow) => void;
}

function Bars({ c }: { c: ColModel }) {
  return (
    <>
      <span className="hm-bar f" style={{ height: c.gh }} />
      <span className="hm-bar c" style={{ height: c.ah }} />
      {c.sessions > 0 ? <span className="hm-sess" /> : null}
    </>
  );
}

function focusDay(host: HTMLElement, i: number): void {
  host.querySelector<HTMLElement>('[data-i="' + i + '"]')?.focus();
}

/**
 * One 36px lane: identity, one column per day, state. Memoised: it re-renders
 * only when its own model, its selection or its search state change.
 */
function LaneImpl({ m, range, interactive, selIdx, match, dim, onOpen, onPick }: LaneProps) {
  const n = m.cols.length;
  const rov = selIdx >= 0 ? selIdx : n - 1;

  const onClick = (e: MouseEvent<HTMLDivElement>) => {
    const b = (e.target as HTMLElement).closest<HTMLElement>("button[data-i]");
    if (b) onPick(m.id, Number(b.dataset.i), "toggle");
  };

  // Arrow keys pick the neighbouring day (roving tabindex, 02-ui-spec 8.5).
  // Up / down go to the same day in the lane above / below.
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const host = e.currentTarget;
    const t = (e.target as HTMLElement).closest<HTMLElement>("[data-i]");
    if (!t) return;
    const i = Number(t.dataset.i);
    const key = e.key;
    if (key === "ArrowLeft" || key === "ArrowRight" || key === "Home" || key === "End") {
      e.preventDefault();
      const ni = key === "Home" ? 0 : key === "End" ? n - 1 : Math.max(0, Math.min(n - 1, i + (key === "ArrowLeft" ? -1 : 1)));
      if (ni === i) return;
      onPick(m.id, ni, "select");
      focusDay(host, ni);
    } else if (key === "Enter" || key === " ") {
      // Reading a day twice must not close it: Enter always selects.
      e.preventDefault();
      onPick(m.id, i, "select");
    } else if (key === "ArrowUp" || key === "ArrowDown") {
      e.preventDefault();
      const all = Array.from(host.closest("[data-lanes]")?.querySelectorAll<HTMLElement>("[data-cols]") ?? []);
      const next = all[all.indexOf(host) + (key === "ArrowUp" ? -1 : 1)];
      if (!next) return;
      focusDay(next, i);
      if (selIdx >= 0 && next.dataset.cols) onPick(next.dataset.cols, i, "select");
    }
  };
  // Space also fires a click on key-up in some browsers.
  const onKeyUp = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === " ") e.preventDefault();
  };

  const cls = "hm-lane" + (dim ? " dim" : m.hidden ? " hidden-lane" : "") + (match ? " match" : "");
  return (
    <div className={cls} data-testid={"lane-" + m.id} data-lane={m.id}>
      <div className="hm-ident">
        <button
          type="button"
          className={"hm-name" + (match ? " match" : m.flat ? " quiet" : "")}
          aria-label={"Open " + m.name + " project page"}
          onClick={() => onOpen(m.id)}
        >
          <span>{m.name}</span>
        </button>
        <span className="hm-gtag">{m.group}</span>
        {m.dirty > 0 ? (
          <span className="hm-dirty" role="img" aria-label={plural(m.dirty, "uncommitted path")}>
            ±{m.dirty}
          </span>
        ) : null}
        {m.hidden ? <span className="hm-hidden-tag">Hidden</span> : null}
      </div>

      <div
        role="group"
        aria-label={m.name + ", last " + range + " days"}
        className="hm-cols"
        data-cols={m.id}
        onClick={interactive ? onClick : undefined}
        onKeyDown={interactive ? onKeyDown : undefined}
        onKeyUp={interactive ? onKeyUp : undefined}
      >
        {m.flat ? <div className="hm-flat">{m.empty ? "empty directory" : "flat — no tremor in " + range + "d"}</div> : null}
        {m.cols.map((c, i) =>
          interactive ? (
            <button
              key={c.date}
              type="button"
              className="hm-col"
              data-i={i}
              aria-label={c.aria}
              aria-pressed={selIdx === i}
              tabIndex={i === rov ? 0 : -1}
            >
              <Bars c={c} />
            </button>
          ) : (
            <div key={c.date} className="hm-col">
              <Bars c={c} />
            </div>
          ),
        )}
      </div>

      <div className="hm-state">
        <span className="hm-ago">{m.ago}</span>
        {m.live > 0 ? (
          <span className="hm-live" role="img" aria-label={plural(m.live, "server") + " live"}>
            ● {m.live}
          </span>
        ) : (
          <span className="hm-nolive" role="img" aria-label="no servers live">
            ○
          </span>
        )}
      </div>
    </div>
  );
}

export const Lane = memo(LaneImpl);
