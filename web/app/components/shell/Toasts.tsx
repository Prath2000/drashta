import { dismissToast, useStore } from "~/lib/store";

/**
 * One toast at a time, bottom centre (02-ui-spec §2.5).
 * A normal toast fades after ~2 s. An error toast has a coral border and stays
 * until you dismiss it.
 */
export function Toasts() {
  const t = useStore((s) => s.toast);
  return (
    <div className="dr-toasts" role="status" aria-live="polite">
      {t ? (
        <div key={t.id} className={"dr-toast" + (t.error ? " error" : "")} data-testid="toast">
          <span className="g" aria-hidden="true">
            ●
          </span>
          <span style={{ wordBreak: "break-word" }}>{t.msg}</span>
          {t.error ? (
            <button type="button" className="x" aria-label="Dismiss" onClick={dismissToast}>
              ✕
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
