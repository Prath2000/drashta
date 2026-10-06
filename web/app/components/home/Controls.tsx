import type { GroupChip, Order } from "./model";

export interface ControlsProps {
  chips: GroupChip[];
  group: string;
  onGroup: (g: string) => void;
  showHidden: boolean;
  hiddenCount: number;
  onShowHidden: (v: boolean) => void;
  order: Order;
  onOrder: (o: Order) => void;
  range: 7 | 30;
  onRange: (r: 7 | 30) => void;
}

const ORDERS: Order[] = ["recency", "loudest"];
const RANGES: (7 | 30)[] = [7, 30];

/** Control row (browsing mode only): group chips, show hidden, ORDER, RANGE. */
export function Controls(p: ControlsProps) {
  return (
    <div className="hm-controls" data-testid="home-controls">
      <div className="hm-group" role="group" aria-label="Group filter">
        {p.chips.map((c) => (
          <button key={c.key} type="button" className="hm-seg" aria-pressed={p.group === c.key} onClick={() => p.onGroup(c.key)}>
            {c.key + " " + c.count}
          </button>
        ))}
        <button
          type="button"
          className="hm-seg hidden-toggle"
          aria-pressed={p.showHidden}
          onClick={() => p.onShowHidden(!p.showHidden)}
        >
          {(p.showHidden ? "◉" : "○") + " show hidden (" + p.hiddenCount + ")"}
        </button>
      </div>
      <div className="hm-group right">
        <span className="hm-label">Order</span>
        <div className="hm-group" role="group" aria-label="Order">
          {ORDERS.map((o) => (
            <button key={o} type="button" className="hm-seg" aria-pressed={p.order === o} onClick={() => p.onOrder(o)}>
              {o}
            </button>
          ))}
        </div>
        <span className="hm-label range">Range</span>
        <div className="hm-group" role="group" aria-label="Range">
          {RANGES.map((r) => (
            <button key={r} type="button" className="hm-seg" aria-pressed={p.range === r} onClick={() => p.onRange(r)}>
              {r + "d"}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
