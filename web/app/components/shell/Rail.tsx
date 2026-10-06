import { findProject, useBoot } from "~/lib/boot";
import { useStore } from "~/lib/store";
import { useGo, type Section } from "~/lib/url";

const ICONS = {
  project: "M2 5 H8 L10 7 H18 V16 H2 Z",
  tracker: "M3 3 H7 V17 H3 Z M9 3 H13 V12 H9 Z M15 3 H19 V9 H15 Z",
  inbox: "M2 11 L5 4 H15 L18 11 V16 H2 Z M2 11 H7 L8 13 H12 L13 11 H18",
} as const;

/**
 * Left rail, 72px: Project · Tracker · Inbox (U4). Home has no rail item; the
 * app name is the link to Home (U2).
 * Tracker badge = snapshots awaiting review; Inbox badge = open notes.
 * A badge with 0 is not shown, and badges never animate.
 */
export function Rail({ section }: { section: Section }) {
  const counts = useStore((s) => s.counts);
  const lastProjectId = useStore((s) => s.lastProjectId);
  const { data: boot } = useBoot();
  const go = useGo();
  const items = [
    {
      key: "project" as const,
      label: "Project",
      badge: 0,
      onClick: () => {
        // the last project you viewed, else the picker
        const p = findProject(boot, lastProjectId);
        if (p) go.project(p.id);
        else go.picker();
      },
    },
    { key: "tracker" as const, label: "Tracker", badge: counts.awaitingReview, onClick: () => go.tracker() },
    { key: "inbox" as const, label: "Inbox", badge: counts.openNotes, onClick: () => go.inbox() },
  ];
  return (
    <nav aria-label="Sections" className="dr-rail">
      {items.map((it) => {
        const active = section === it.key;
        const aria =
          it.label + (it.badge > 0 ? ", " + it.badge + (it.key === "tracker" ? " awaiting review" : " open notes") : "");
        return (
          <button
            key={it.key}
            type="button"
            className={"dr-rail-item dr-hover" + (active ? " active" : "")}
            aria-label={aria}
            aria-current={active ? "page" : undefined}
            data-testid={"rail-" + it.key}
            onClick={it.onClick}
          >
            {active ? <span className="bar" /> : null}
            <span className="ico">
              <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="square" aria-hidden="true">
                <path d={ICONS[it.key]} />
              </svg>
              {it.badge > 0 ? (
                <span className={"badge " + (it.key === "tracker" ? "review" : "notes")} data-testid={"badge-" + it.key}>
                  {it.badge}
                </span>
              ) : null}
            </span>
            <span className="lbl">{it.label}</span>
          </button>
        );
      })}
    </nav>
  );
}
