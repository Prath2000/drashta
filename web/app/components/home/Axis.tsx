import { memo } from "react";
import type { AxisTick } from "./geometry";

/** Sticky date axis over the day columns. Labels are spaced uppercase, plus a scale tick per day. */
function AxisImpl({ ticks }: { ticks: AxisTick[] }) {
  return (
    <div className="hm-axis" aria-hidden="true" data-testid="home-axis">
      <div className="side-l" />
      <div className="days">
        {ticks.map((t, i) => (
          <div className="day" key={i}>
            <span className="tick" style={{ height: t.tickH }} />
            {t.hasLabel ? <span className="lbl">{t.label}</span> : null}
          </div>
        ))}
      </div>
      <div className="side-r" />
    </div>
  );
}

export const Axis = memo(AxisImpl);
