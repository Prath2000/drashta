import { useEffect, useState } from "react";
import { ago, hhmm } from "~/lib/format";
import { useStore } from "~/lib/store";

/**
 * Scan status text (02-ui-spec §2.3, §3.6):
 *   last scan 14:53 UTC → rescanning… → updated just now → updated 4m ago
 *   offline — last scan 14:53   (coral, when the event stream is down)
 * The relative time is recomputed on render and when the page becomes
 * visible again. There is no timer.
 */
export function ScanLabel() {
  const scan = useStore((s) => s.scan);
  const online = useStore((s) => s.online);
  const [, setTick] = useState(0);
  useEffect(() => {
    const on = () => {
      if (document.visibilityState === "visible") setTick((t) => t + 1);
    };
    document.addEventListener("visibilitychange", on);
    return () => document.removeEventListener("visibilitychange", on);
  }, []);
  let text: string;
  let cls = "dr-scan";
  const hm = hhmm(scan.at);
  if (!online) {
    text = "offline — last scan " + (hm || "—");
    cls += " offline";
  } else if (scan.running) {
    text = "rescanning…";
    cls += " running";
  } else if (scan.updatedHere && scan.at) {
    text = "updated " + ago(scan.at);
  } else {
    text = hm ? "last scan " + hm : "last scan —";
  }
  return (
    <span className={cls} data-testid="scan-label">
      {text}
    </span>
  );
}
