import { useEffect, useState } from "react";
import { useStore } from "~/lib/store";

/**
 * One amber line that sweeps once across the section when the user starts a
 * rescan (02-ui-spec §2.3). It is removed when its animation ends.
 * With reduced motion the CSS hides it.
 */
export function Sweep() {
  const sweep = useStore((s) => s.scan.sweep);
  const [shown, setShown] = useState(0);
  useEffect(() => {
    if (sweep > 0) setShown(sweep);
  }, [sweep]);
  if (!shown) return null;
  return <div key={shown} className="dr-sweep" data-testid="sweep" onAnimationEnd={() => setShown(0)} />;
}
