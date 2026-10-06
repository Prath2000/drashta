import { Link, useLocation } from "react-router";
import { findProject, useBoot } from "~/lib/boot";
import { useStore } from "~/lib/store";
import { projectFromPath, type Section } from "~/lib/url";
import { Counters } from "./Counters";
import { DesktopModes } from "./DesktopModes";
import { RefreshAll } from "./RefreshAll";
import { ScanLabel } from "./ScanLabel";
import { SearchBox } from "./SearchBox";

const NAMES: Record<Section, string> = { home: "Home", project: "Project", tracker: "Tracker", inbox: "Inbox" };

/**
 * Top bar, 56px. The app name, shown in Devanagari (द्रष्टा), is the only link
 * to Home (U2); its aria-label keeps the stable Latin name "drashta" for screen
 * readers and tests. Home shows the subtitle; other sections show a breadcrumb
 * in spaced uppercase. The desktop mode switch is hidden in readout, where
 * Plash passes no clicks to the page.
 */
export function TopBar({ section }: { section: Section }) {
  const mode = useStore((s) => s.mode);
  const range = useStore((s) => s.range);
  const readout = mode === "readout";
  return (
    <header className="dr-topbar">
      <div className="dr-brand">
        {readout ? (
          <span className="dr-name" lang="sa">
            द्रष्टा
          </span>
        ) : (
          <Link to="/" className="dr-name" aria-label="drashta, home">
            द्रष्टा
          </Link>
        )}
        {section === "home" ? (
          <span className="dr-subtitle">every tremor in your projects, last {range} days</span>
        ) : (
          <Crumbs section={section} />
        )}
      </div>
      <div className="dr-spacer" />
      {readout ? null : <SearchBox />}
      <ScanLabel />
      {readout ? null : <RefreshAll />}
      {readout ? null : <DesktopModes />}
      <Counters />
    </header>
  );
}

function Crumbs({ section }: { section: Section }) {
  const loc = useLocation();
  const { data: boot } = useBoot();
  const pp = section === "project" ? projectFromPath(loc.pathname) : null;
  const proj = pp ? findProject(boot, pp.project) : null;
  return (
    <nav aria-label="Breadcrumb" className="dr-crumbs">
      <span>{NAMES[section]}</span>
      {pp ? (
        <>
          <span className="sep">›</span>
          <span className="proj">{proj ? proj.name : pp.project}</span>
          <span className="sep">›</span>
          <span className="tab">{pp.tab}</span>
        </>
      ) : null}
    </nav>
  );
}
