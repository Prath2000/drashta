import type { ViewportBodyProps } from "~/components/viewport/registry";

/** Stand-in preview body until the viewport agent registers a real one. */
export function ViewportPlaceholder({ resolved, refStr }: ViewportBodyProps) {
  return (
    <div className="dr-missing" data-testid="viewport-placeholder">
      <div className="dr-label">{resolved.type} preview</div>
      <div className="why">the preview body for this kind is not built yet.</div>
      <div className="dr-meta">{refStr}</div>
    </div>
  );
}
