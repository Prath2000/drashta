import { api, ApiError } from "~/lib/api";
import { scanFailed, scanStarted, toast, useStore } from "~/lib/store";

/** The only filled amber button: asks the server to rescan every project. */
export function RefreshAll() {
  const running = useStore((s) => s.scan.running);
  const onClick = async () => {
    if (running) return;
    scanStarted(true);
    try {
      await api.refresh();
      // progress and the end arrive over SSE (`scan` events)
    } catch (e) {
      scanFailed();
      toast(e instanceof ApiError ? e.message : "refresh failed", { error: true });
    }
  };
  return (
    <button type="button" className="dr-btn-primary" onClick={onClick} disabled={running}>
      {running ? "↻ rescanning…" : "↻ refresh all"}
    </button>
  );
}
