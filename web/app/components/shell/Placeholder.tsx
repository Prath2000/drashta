// A titled placeholder used by stub routes until a screen agent fills them in.
export function Placeholder({ title }: { title: string }) {
  return (
    <div className="dr-placeholder" data-testid="route-placeholder">
      <div className="dr-cartouche">
        <span className="dr-head">{title}</span>
        <span className="dr-empty">this screen is not built yet.</span>
      </div>
    </div>
  );
}
