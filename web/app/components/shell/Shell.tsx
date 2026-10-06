import type { ReactNode } from "react";
import { useLocation } from "react-router";
import { useBootSync } from "~/lib/boot";
import { useShortcuts } from "~/lib/shortcuts";
import { useStore } from "~/lib/store";
import { sectionOf } from "~/lib/url";
import HomeRoute from "~/routes/home";
import { ReadoutHint } from "./ReadoutHint";
import { Rail } from "./Rail";
import { Sweep } from "./Sweep";
import { Toasts } from "./Toasts";
import { TopBar } from "./TopBar";
import { Viewport } from "./Viewport";

const SECTION_LABEL = { home: "Home", project: "Project", tracker: "Tracker", inbox: "Inbox" } as const;

/**
 * The global frame: top bar, rail, section content, viewport drawer, toasts.
 * Readout mode (Plash wallpaper) shows only the name, counters, scan label and
 * Home; it hides search, refresh, rail, drawer, toasts and hover states.
 * It also installs the one global key listener (lib/shortcuts.ts); that listener
 * does nothing in readout mode.
 */
export function Shell({ children }: { children: ReactNode }) {
  useBootSync();
  useShortcuts();
  const mode = useStore((s) => s.mode);
  const loc = useLocation();
  const readout = mode === "readout";
  const section = readout ? "home" : sectionOf(loc.pathname);
  return (
    <div className={"dr-root " + (readout ? "dr-readout" : "dr-browsing")} data-mode={mode}>
      <TopBar section={section} />
      <div className="dr-body">
        {readout ? null : <Rail section={section} />}
        <main className="dr-main">
          <section className="dr-section dr-mount" aria-label={SECTION_LABEL[section]}>
            {readout ? null : <Sweep />}
            {/* readout always shows Home; the URL stays as it is */}
            {readout && sectionOf(loc.pathname) !== "home" ? <HomeRoute /> : children}
          </section>
          {readout ? null : <Viewport />}
        </main>
      </div>
      {readout ? <ReadoutHint /> : <Toasts />}
    </div>
  );
}
