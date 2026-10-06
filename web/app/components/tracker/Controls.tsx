import type { ReactNode } from "react";

export interface SegOption<T extends string> {
  key: T;
  label: ReactNode;
  /** Plain text for the accessible name when `label` is not a string. */
  text?: string;
}

/** A labelled segmented control. The pressed button has aria-pressed="true". */
export function Seg<T extends string>({
  label,
  aria,
  value,
  options,
  onPick,
}: {
  label: string;
  aria: string;
  value: T;
  options: SegOption<T>[];
  onPick: (k: T) => void;
}) {
  return (
    <div className="tk-ctl">
      <span className="tk-ctl-label">{label}</span>
      <div role="group" aria-label={aria} className="tk-seg">
        {options.map((o) => (
          <button key={o.key} type="button" aria-pressed={value === o.key} onClick={() => onPick(o.key)}>
            {o.label}
          </button>
        ))}
      </div>
    </div>
  );
}
