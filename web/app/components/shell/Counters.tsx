import { useStore } from "~/lib/store";
import { useGo } from "~/lib/url";

/**
 * The four live counters (02-ui-spec §2.1). Buttons in browsing mode,
 * plain read-only cells in readout mode. A count of 0 is muted.
 */
export function Counters() {
  const counts = useStore((s) => s.counts);
  const readout = useStore((s) => s.mode === "readout");
  const go = useGo();
  const cells = [
    { key: "live", label: "live", value: counts.liveServers, title: counts.liveServers + " servers running", go: () => go.home() },
    { key: "tasks", label: "tasks", value: counts.openTasks, title: counts.openTasks + " open tasks", go: () => go.tracker("all") },
    {
      key: "review",
      label: "review",
      value: counts.awaitingReview,
      title: counts.awaitingReview + " snapshots awaiting review",
      go: () => go.tracker("review"),
    },
    { key: "notes", label: "notes", value: counts.openNotes, title: counts.openNotes + " open notes", go: () => go.inbox() },
  ];
  return (
    <div role="group" aria-label="Live counters" className="dr-counters">
      {cells.map((c) =>
        readout ? (
          <div key={c.key} className={"dr-counter " + c.key} data-testid={"counter-" + c.key}>
            <span className="dr-label">{c.label}</span>
            <span className={"v" + (c.value ? " on" : "")}>{c.value}</span>
          </div>
        ) : (
          <button
            key={c.key}
            type="button"
            className={"dr-counter dr-hover " + c.key}
            data-testid={"counter-" + c.key}
            title={c.title}
            aria-label={c.title}
            onClick={c.go}
          >
            <span className="dr-label">{c.label}</span>
            <span className={"v" + (c.value ? " on" : "")}>{c.value}</span>
          </button>
        ),
      )}
    </div>
  );
}
