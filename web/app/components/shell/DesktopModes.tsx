import { useEffect, useState } from "react";
import { api, ApiError } from "~/lib/api";
import { invalidate, setCached, useApi } from "~/lib/query";
import { toast } from "~/lib/store";
import type { DesktopMode, DesktopResponse } from "~/lib/types";

const LABELS: Record<DesktopMode, string> = { browse: "browse", wallpaper: "wallpaper", blank: "normal" };
/** The Option-tap key of each mode; shown instead of the label on narrow windows. */
const KEYS: Record<DesktopMode, string> = { browse: "B", wallpaper: "W", blank: "N" };
const TITLES: Record<DesktopMode, string> = {
  browse: "drashta on the desktop, clickable (⌥ then B)",
  wallpaper: "drashta on the desktop, quiet readout (⌥ then W)",
  blank: "the normal macOS wallpaper (⌥ then N)",
};

/**
 * The Plash desktop mode switch (contrib/desktop/README.md): the same three modes as
 * the Option-tap keys. The lit button is the mode plash-mode last set, refetched
 * when the window gets focus (the keys may have changed it). Hidden when the
 * server has no desktop script.
 */
export function DesktopModes() {
  const { data } = useApi<DesktopResponse>("/desktop", api.desktop);
  const [busy, setBusy] = useState<DesktopMode | null>(null);

  useEffect(() => {
    const again = () => invalidate("/desktop");
    window.addEventListener("focus", again);
    return () => window.removeEventListener("focus", again);
  }, []);

  if (!data?.available) return null;
  const pick = async (m: DesktopMode) => {
    if (busy) return;
    setBusy(m);
    try {
      setCached("/desktop", await api.setDesktop(m));
    } catch (e) {
      toast(e instanceof ApiError ? e.message : "desktop switch failed", { error: true });
    } finally {
      setBusy(null);
    }
  };
  return (
    <div className="dr-desk" role="group" aria-label="Desktop mode">
      {data.modes.map((m) => (
        <button
          key={m}
          type="button"
          className={"dr-desk-btn" + (data.mode === m ? " on" : "")}
          aria-pressed={data.mode === m}
          aria-label={LABELS[m]}
          title={TITLES[m]}
          disabled={busy !== null}
          onClick={() => pick(m)}
        >
          {busy === m ? (
            "…"
          ) : (
            <>
              <span className="long">{LABELS[m]}</span>
              <span className="short" aria-hidden="true">
                {KEYS[m]}
              </span>
            </>
          )}
        </button>
      ))}
    </div>
  );
}
