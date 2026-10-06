// Project seismograph (02-ui-spec §5.3): own log scale, max · 0 gutter, two
// dashed gridlines, one column per day, and a one-line readout under it.
import { memo, useMemo, useRef, type CSSProperties, type KeyboardEvent } from "react";
import { useEscapeLayer } from "~/lib/shortcuts";
import type { ActivityDay } from "~/lib/types";
import { dayParts } from "./fmt";
import { C } from "./ui";

const H = 112;
const BASE = 14;

const lg = (v: number, m: number) => (m > 0 ? Math.log(1 + v) / Math.log(1 + m) : 0);
const linesOf = (a: ActivityDay) => (a.linesPlus || 0) + (a.linesMinus || 0);

export const Seismograph = memo(function Seismograph(props: {
  acts: ActivityDay[];
  range: 7 | 30;
  selDay: string | null;
  onPick: (day: string) => void;
  onRead: () => void;
  loading: boolean;
}) {
  const { acts, range, selDay, onPick, onRead } = props;
  const gridRef = useRef<HTMLDivElement>(null);
  const n = acts.length;

  const model = useMemo(() => {
    const maxF = Math.max(0, ...acts.map((a) => a.files || 0));
    const maxL = Math.max(0, ...acts.map(linesOf));
    let peakI = -1;
    let peakV = 0;
    let peakKind = "file changes";
    acts.forEach((a, i) => {
      if ((a.files || 0) > 0 && (a.files || 0) >= peakV) {
        peakV = a.files;
        peakI = i;
      }
    });
    if (peakI < 0) {
      peakKind = "commits";
      acts.forEach((a, i) => {
        if ((a.commits || 0) > 0 && (a.commits || 0) >= peakV) {
          peakV = a.commits;
          peakI = i;
        }
      });
    }
    let totC = 0;
    let totS = 0;
    let totF = 0;
    let anyActive = false;
    for (const a of acts) {
      totC += a.commits || 0;
      totS += a.sessions || 0;
      totF += a.files || 0;
      if (a.files || a.commits || a.sessions || a.snapshots || a.notes) anyActive = true;
    }
    const peakDp = peakI >= 0 ? dayParts(acts[peakI].day) : null;
    return { maxF, maxL, peakI, peakV, peakKind, totC, totS, totF, anyActive, peakDp };
  }, [acts]);

  const bw = range === 7 ? 18 : 8;
  const cols = "repeat(" + Math.max(1, n) + ", minmax(0, 1fr))";
  const gut: CSSProperties = {
    position: "absolute",
    right: 6,
    fontSize: 9,
    lineHeight: "10px",
    letterSpacing: ".06em",
    textTransform: "uppercase",
  };
  const selIndex = selDay ? acts.findIndex((a) => a.day === selDay) : -1;
  const focusIndex = selIndex >= 0 ? selIndex : n - 1;

  // Esc clears the selected day (after the search panel, before the viewport closes).
  // Picking the selected day again is how the page clears it.
  useEscapeLayer("day", selIndex >= 0, () => {
    if (selDay) onPick(selDay);
  });

  // Arrow keys move between day columns (one tab stop for the chart).
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight" && e.key !== "Home" && e.key !== "End") return;
    const btns = Array.from(gridRef.current?.querySelectorAll<HTMLButtonElement>("button[data-day]") ?? []);
    const cur = btns.indexOf(document.activeElement as HTMLButtonElement);
    let i = cur < 0 ? focusIndex : cur;
    if (e.key === "ArrowLeft") i = Math.max(0, i - 1);
    else if (e.key === "ArrowRight") i = Math.min(btns.length - 1, i + 1);
    else if (e.key === "Home") i = 0;
    else i = btns.length - 1;
    e.preventDefault();
    btns[i]?.focus();
  };

  const sel = selIndex >= 0 ? acts[selIndex] : null;
  const sp = sel ? dayParts(sel.day) : null;

  return (
    <section style={{ padding: "20px 32px 0" }} aria-label="Project seismograph">
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", gap: 16, marginBottom: 10, flexWrap: "wrap" }}>
        <div style={{ display: "flex", alignItems: "baseline", gap: 12, minWidth: 0, flexWrap: "wrap" }}>
          <span style={{ fontSize: 11, letterSpacing: ".22em", textTransform: "uppercase", color: C.text }}>Seismograph</span>
          <span style={{ fontSize: 11, color: C.muted }} data-testid="seis-peak">
            {model.peakDp ? "peak " + model.peakDp.d + " " + model.peakDp.mon + " · " + model.peakV + " " + model.peakKind : "no peak"}
          </span>
          <span style={{ fontSize: 11, color: C.muted }} data-testid="seis-total">
            {model.totF + " file changes · " + model.totC + " commits · " + model.totS + " sessions in " + range + "d"}
          </span>
        </div>
        <div
          style={{
            display: "flex",
            gap: 16,
            fontSize: 10,
            letterSpacing: ".10em",
            textTransform: "uppercase",
            color: C.muted,
            alignItems: "center",
            flexWrap: "wrap",
          }}
        >
          <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
            <span style={{ width: 8, height: 10, background: C.dim }} />
            file changes
          </span>
          <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
            <span style={{ width: 8, height: 10, background: C.amber }} />
            commits
          </span>
          <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
            <span style={{ width: 2, height: 9, background: C.violet }} />
            sessions
          </span>
          <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
            <span style={{ width: 6, height: 6, background: C.ivory, transform: "rotate(45deg)" }} />
            snapshots
          </span>
          <span>log scale · click a day</span>
        </div>
      </div>

      <div style={{ display: "flex", border: "1px solid " + C.hair, background: C.panel }}>
        <div aria-hidden="true" style={{ flex: "none", width: 54, borderRight: "1px solid " + C.faint }}>
          <div style={{ position: "relative", height: 150 }}>
            <span style={{ ...gut, bottom: BASE + H + 14, color: C.muted }}>max</span>
            <span style={{ ...gut, bottom: BASE + H + 2, color: C.muted }}>{model.maxF} f</span>
            <span style={{ ...gut, bottom: BASE + H - 10, color: C.amber }}>{model.maxL} ln</span>
            <span style={{ ...gut, bottom: BASE - 5, color: C.muted }}>0</span>
          </div>
          <div style={{ height: 22, borderTop: "1px solid " + C.faint }} />
        </div>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ position: "relative", height: 150 }}>
            <span aria-hidden="true" style={{ position: "absolute", left: 0, right: 0, bottom: BASE + H, height: 0, borderTop: "1px dashed " + C.hair }} />
            <span
              aria-hidden="true"
              style={{ position: "absolute", left: 0, right: 0, bottom: BASE + Math.round(H / 2), height: 0, borderTop: "1px dashed " + C.faint }}
            />
            <span aria-hidden="true" style={{ position: "absolute", left: 0, right: 0, bottom: BASE, height: 1, background: C.hair }} />
            <div
              ref={gridRef}
              role="group"
              aria-label="Days"
              onKeyDown={onKeyDown}
              style={{ position: "absolute", inset: 0, display: "grid", gridTemplateColumns: cols }}
              data-testid="seis-grid"
            >
              {acts.map((a, i) => {
                const dp = dayParts(a.day);
                const isSel = a.day === selDay;
                const f = a.files || 0;
                const c = a.commits || 0;
                const fh = f ? Math.max(2, Math.round(lg(f, model.maxF) * H)) : 0;
                const ch = c ? Math.max(4, Math.round(lg(linesOf(a), model.maxL) * H)) : 0;
                const showVal = (range === 7 || isSel || i === model.peakI) && (f > 0 || c > 0);
                const weekEdge = dp.dow === 1;
                return (
                  <button
                    key={a.day}
                    type="button"
                    data-day={a.day}
                    tabIndex={i === focusIndex ? 0 : -1}
                    onClick={() => onPick(a.day)}
                    aria-pressed={isSel}
                    aria-label={
                      dp.wd + " " + dp.d + " " + dp.mon + ": " + f + " file changes, " + c + " commits, " + (a.sessions || 0) + " sessions, " +
                      (a.snapshots || 0) + " snapshots"
                    }
                    style={{
                      position: "relative",
                      display: "block",
                      width: "100%",
                      height: 150,
                      padding: 0,
                      margin: 0,
                      border: 0,
                      borderLeft: "1px solid " + (range === 7 || weekEdge ? C.faint : "transparent"),
                      background: isSel ? C.select : "transparent",
                      boxShadow: isSel ? "inset 0 -1px 0 var(--amber)" : "none",
                      cursor: "pointer",
                      overflow: "visible",
                    }}
                  >
                    {showVal ? (
                      <span
                        style={{
                          position: "absolute",
                          left: 0,
                          right: 0,
                          bottom: BASE + Math.max(fh, ch) + 3,
                          display: "flex",
                          justifyContent: "center",
                          gap: 4,
                          fontSize: 9,
                          lineHeight: "12px",
                          whiteSpace: "nowrap",
                        }}
                      >
                        <span style={{ color: C.muted }}>{f ? f : ""}</span>
                        <span style={{ color: C.amber }}>{c ? c : ""}</span>
                      </span>
                    ) : null}
                    <span
                      style={{
                        position: "absolute",
                        bottom: BASE,
                        left: "50%",
                        marginLeft: -(bw + 1),
                        width: bw,
                        height: fh,
                        background: isSel ? "#8a857a" : C.dim,
                      }}
                    />
                    <span style={{ position: "absolute", bottom: BASE, left: "50%", marginLeft: 1, width: bw, height: ch, background: C.amber }} />
                    <span
                      style={{
                        position: "absolute",
                        left: 0,
                        right: 0,
                        bottom: 3,
                        display: "flex",
                        justifyContent: "center",
                        alignItems: "center",
                        gap: 2,
                      }}
                    >
                      {a.snapshots ? (
                        <span style={{ width: 5, height: 5, marginRight: 3, background: C.ivory, transform: "rotate(45deg)" }} />
                      ) : null}
                      {Array.from({ length: Math.min(4, a.sessions || 0) }, (_, k) => (
                        <span key={k} style={{ width: 2, height: 8, background: C.violet }} />
                      ))}
                    </span>
                  </button>
                );
              })}
            </div>
            {!props.loading && !model.anyActive ? (
              <span style={{ position: "absolute", left: 12, bottom: 22, fontSize: 11, color: C.muted }}>flat — no tremor in {range}d</span>
            ) : null}
          </div>
          <div aria-hidden="true" style={{ display: "grid", gridTemplateColumns: cols, height: 22, borderTop: "1px solid " + C.faint }}>
            {acts.map((a, i) => {
              const dp = dayParts(a.day);
              const isToday = i === n - 1;
              const nextDow = i + 1 < n ? dayParts(acts[i + 1].day).dow : -1;
              let label = "";
              if (isToday) label = "today";
              else if (range === 7) label = dp.wd + " " + dp.d;
              else if (dp.dow === 1 && i !== n - 2) label = dp.d + " " + dp.mon;
              else if (i === 0 && nextDow !== 1) label = dp.d + " " + dp.mon;
              return (
                <div
                  key={a.day}
                  style={{
                    height: 22,
                    padding: "5px 0 0 3px",
                    fontSize: 9,
                    letterSpacing: ".06em",
                    textTransform: "uppercase",
                    whiteSpace: "nowrap",
                    overflow: "visible",
                    textAlign: "left",
                    color: a.day === selDay ? C.amber : isToday ? C.text : C.muted,
                  }}
                >
                  {label}
                </div>
              );
            })}
          </div>
        </div>
      </div>

      <div
        data-testid="day-readout"
        style={{
          minHeight: 44,
          border: "1px solid " + C.hair,
          borderTop: 0,
          background: "#0f0f0d",
          padding: "8px 16px",
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          gap: "6px 18px",
        }}
      >
        {sel && sp ? (
          <>
            <span style={{ fontSize: 16, color: C.text, marginRight: 4 }}>{sp.wd + " " + sp.d + " " + sp.mon}</span>
            <span style={{ fontSize: 12, color: C.muted }}>
              file changes <span style={{ color: C.text }}>{sel.files || 0}</span>
            </span>
            <span style={{ fontSize: 12, color: C.muted }}>
              commits <span style={{ color: C.amber }}>{sel.commits || 0}</span>
            </span>
            <span style={{ fontSize: 12, color: C.muted }}>
              lines <span style={{ color: C.teal }}>+{sel.linesPlus || 0}</span> <span style={{ color: C.coral }}>−{sel.linesMinus || 0}</span>
            </span>
            <span style={{ fontSize: 12, color: C.muted }}>
              sessions <span style={{ color: C.violet }}>{sel.sessions || 0}</span>
            </span>
            <button
              type="button"
              className="pj-hover"
              onClick={onRead}
              style={{
                marginLeft: "auto",
                minHeight: 36,
                padding: "0 12px",
                background: "transparent",
                color: C.amber,
                border: "1px solid " + C.hair,
                borderRadius: 3,
                fontSize: 12,
                cursor: "pointer",
              }}
            >
              read the day →
            </button>
          </>
        ) : (
          <span style={{ fontSize: 14, color: C.muted }}>click a day column above to read it.</span>
        )}
      </div>
    </section>
  );
});
