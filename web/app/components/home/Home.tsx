import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useBoot } from "~/lib/boot";
import { useApi } from "~/lib/query";
import { useEscapeLayer } from "~/lib/shortcuts";
import { setRange, useStore } from "~/lib/store";
import type { DayResponse, Home as HomeData } from "~/lib/types";
import { useGo, useItem } from "~/lib/url";
import { Axis } from "./Axis";
import { Controls } from "./Controls";
import { DayReadout, type ReadoutSel } from "./DayReadout";
import { buildAxis, fitLanes, laneCapacity } from "./geometry";
import { Lane, type PickHow } from "./Lane";
import { buildLaneModels, groupChips, hiddenTotal, selectLanes, stabilise, type LaneModel } from "./model";
import { loadPrefs, savePrefs, type HomePrefs } from "./prefs";
import "./home.css";

/** More lanes than this are capped behind a "show all N" button (02-ui-spec 3.5). */
export const LANE_CAP = 200;

interface Sel {
  id: string;
  /** Day key, so the selection survives a refetch and a range switch can test it. */
  date: string;
}

/**
 * The Home seismograph (02-ui-spec section 3): control row, sticky date axis, one
 * 36px lane per project, and the 128px day readout.
 *
 * Data: GET /home?range=, GET /boot (names, groups, hidden, dirty), GET /day on a
 * selected day. Live updates come through lib/events.ts, which refetches /home;
 * models are made stable per lane so only the changed lane is drawn again.
 */
export function Home() {
  const mode = useStore((s) => s.mode);
  const range = useStore((s) => s.range);
  const searchLit = useStore((s) => s.searchLit);
  const readout = mode === "readout";

  const { data: boot } = useBoot();
  const homeQ = useApi<HomeData>("/home?range=" + range);
  // Keep the last answer while the other range loads, so the lanes do not blink empty.
  const lastHome = useRef<HomeData | undefined>(undefined);
  if (homeQ.data) lastHome.current = homeQ.data;
  const home = homeQ.data ?? lastHome.current;
  const days = home?.days;
  const dayKeys = useMemo(() => days ?? [], [days]);
  const viewRange: 7 | 30 = home ? (dayKeys.length === 7 ? 7 : 30) : range;

  const [prefs, setPrefs] = useState<HomePrefs>(loadPrefs);
  const [sel, setSel] = useState<Sel | null>(null);
  const [showAll, setShowAll] = useState(false);
  const [regionH, setRegionH] = useState(0);
  const regionRef = useRef<HTMLDivElement>(null);

  const go = useGo();
  const { openItem } = useItem();
  // Handlers read the latest router helpers through a ref, so the callbacks below
  // stay the same object and the memoised lanes are not redrawn.
  const latest = useRef({ go, openItem, dayKeys });
  latest.current = { go, openItem, dayKeys };

  // ---------- lane models ----------
  const prevModels = useRef(new Map<string, LaneModel>());
  const models = useMemo(() => {
    if (!home || !boot) return [];
    const stable = stabilise(prevModels.current, buildLaneModels(home, boot.projects, Date.now()));
    prevModels.current = new Map(stable.map((m) => [m.id, m]));
    return stable;
  }, [home, boot]);

  const chips = useMemo(() => groupChips(models, prefs.showHidden), [models, prefs.showHidden]);
  const group = chips.some((c) => c.key === prefs.group) ? prefs.group : "all";
  const ordered = useMemo(
    () => selectLanes(models, { group, order: prefs.order, showHidden: prefs.showHidden }),
    [models, group, prefs.order, prefs.showHidden],
  );

  // Readout never scrolls: measure the lanes region and fit as many rows as it holds.
  useLayoutEffect(() => {
    if (!readout) return;
    const el = regionRef.current;
    if (!el) return;
    setRegionH(el.clientHeight);
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver((entries) => {
      const last = entries[entries.length - 1];
      if (last) setRegionH(Math.round(last.contentRect.height));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [readout]);
  const capacity = readout ? laneCapacity(regionH) : Infinity;
  const fitted = useMemo(() => fitLanes(ordered, capacity), [ordered, capacity]);
  // Browsing: a very long list is cut at LANE_CAP until you ask for all of it.
  const capped = !readout && !showAll && fitted.shown.length > LANE_CAP;
  const shown = useMemo(() => (capped ? fitted.shown.slice(0, LANE_CAP) : fitted.shown), [fitted, capped]);
  const more = fitted.more;

  // ---------- selection ----------
  // A selected day outside the new range is cleared (spec 3.3).
  useEffect(() => {
    if (sel && days && !days.includes(sel.date)) setSel(null);
  }, [sel, days]);

  const selLane = !readout && sel ? shown.find((m) => m.id === sel.id) : undefined;
  const selIdx = selLane && sel ? dayKeys.indexOf(sel.date) : -1;
  const selCol = selLane && selIdx >= 0 ? selLane.cols[selIdx] : undefined;
  const readoutSel: ReadoutSel | null = selLane && selCol ? { id: selLane.id, name: selLane.name, col: selCol } : null;

  const dayQ = useApi<DayResponse>(
    readoutSel ? "/day?date=" + encodeURIComponent(readoutSel.col.date) + "&project=" + encodeURIComponent(readoutSel.id) : null,
  );

  // ---------- handlers (stable) ----------
  const onOpen = useCallback((id: string) => latest.current.go.project(id), []);
  const onOpenItem = useCallback((ref: string) => latest.current.openItem(ref), []);
  const onPick = useCallback((id: string, i: number, how: PickHow) => {
    const date = latest.current.dayKeys[i];
    if (!date) return;
    setSel((cur) => (how === "toggle" && cur && cur.id === id && cur.date === date ? null : { id, date }));
  }, []);
  const setPref = useCallback((patch: Partial<HomePrefs>) => {
    setPrefs((p) => {
      const next = { ...p, ...patch };
      savePrefs(next);
      return next;
    });
  }, []);
  // Esc clears the selected day (after the search panel, before the viewport).
  useEscapeLayer("day", readoutSel !== null, () => setSel(null));

  // ---------- search dim (browsing only; the search box is hidden in readout) ----------
  const lit = useMemo(() => (searchLit ? new Set(searchLit) : null), [searchLit]);
  const searching = !readout && lit !== null;

  const ticks = useMemo(() => buildAxis(dayKeys), [dayKeys]);
  const loading = !home || !boot;
  const failed = !home && !!homeQ.error;

  return (
    <div
      className="hm-root"
      role="region"
      aria-label={"Seismograph, every project, last " + viewRange + " days"}
      tabIndex={-1}
      data-mode={mode}
      data-testid="home-root"
    >
      {readout ? null : (
        <Controls
          chips={chips}
          group={group}
          onGroup={(g) => setPref({ group: g })}
          showHidden={prefs.showHidden}
          hiddenCount={hiddenTotal(models)}
          onShowHidden={(v) => setPref({ showHidden: v })}
          order={prefs.order}
          onOrder={(o) => setPref({ order: o })}
          range={range}
          onRange={setRange}
        />
      )}

      <div ref={regionRef} className={"hm-lanes" + (readout ? " readout" : "")} data-testid="home-lanes" aria-busy={loading && !failed}>
        <div className={"hm-lanes-in" + (viewRange === 7 ? " sep7" : "")} data-lanes="">
          <Axis ticks={ticks} />
          {shown.map((m) => (
            <Lane
              key={m.id}
              m={m}
              range={viewRange}
              interactive={!readout}
              selIdx={selLane && selLane.id === m.id ? selIdx : -1}
              match={searching && lit.has(m.id)}
              dim={searching && !lit.has(m.id)}
              onOpen={onOpen}
              onPick={onPick}
            />
          ))}
          {more > 0 ? (
            <div className="hm-more" data-testid="home-more">
              {"+" + more + " quieter " + (more === 1 ? "project" : "projects")}
            </div>
          ) : null}
          {capped ? (
            <div className="hm-more" data-testid="home-cap">
              <span>{"showing " + LANE_CAP + " of " + fitted.shown.length + " projects"}</span>
              <button type="button" className="dr-btn" onClick={() => setShowAll(true)}>
                {"show all " + fitted.shown.length}
              </button>
            </div>
          ) : null}
          {!loading && ordered.length === 0 ? (
            <div className="hm-nolanes">{models.length === 0 ? (boot?.notice ?? "no projects yet.") : "no projects in this group."}</div>
          ) : null}
          {failed ? (
            <div className="hm-error">
              <span>could not read the seismograph.</span>
              {readout ? null : (
                <button type="button" className="dr-btn" onClick={homeQ.reload}>
                  try again
                </button>
              )}
            </div>
          ) : null}
        </div>
      </div>

      <DayReadout
        sel={readoutSel}
        browsing={!readout}
        day={readoutSel ? dayQ.data : undefined}
        dayError={!!dayQ.error}
        onOpenProject={onOpen}
        onOpenItem={onOpenItem}
      />
    </div>
  );
}
