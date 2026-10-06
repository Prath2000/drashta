import { useBoot } from "~/lib/boot";
import { ago } from "~/lib/format";
import { useApi } from "~/lib/query";
import { useStore } from "~/lib/store";
import type { Home } from "~/lib/types";
import { useGo } from "~/lib/url";

/** Project picker: the Project rail item when no project was opened yet (02-ui-spec §2.1). */
export default function PickerRoute() {
  const { data: boot } = useBoot();
  const range = useStore((s) => s.range);
  const { data: home } = useApi<Home>("/home?range=" + range);
  const go = useGo();
  const all = boot?.projects ?? [];
  const shown = all
    .filter((p) => !p.hidden)
    .slice()
    .sort((a, b) => (Date.parse(b.lastChangeAt ?? "") || 0) - (Date.parse(a.lastChangeAt ?? "") || 0));
  const live = new Set((home?.lanes ?? []).filter((l) => l.live.servers.length > 0).map((l) => l.projectId));
  const hidden = all.length - shown.length;
  return (
    <div className="dr-placeholder">
      <div className="dr-picker">
        <div className="dr-cartouche" style={{ marginBottom: 16 }}>
          <span className="dr-head">Project</span>
          <span className="dr-empty">pick a project to open.</span>
        </div>
        <div role="list" aria-label="Projects" style={{ borderTop: "1px solid var(--hairline)" }}>
          {shown.map((p) => (
            <button key={p.id} type="button" role="listitem" className="dr-picker-row dr-hover" title={p.path} onClick={() => go.project(p.id)}>
              <span className="n">{p.name}</span>
              <span className="dr-meta" style={{ color: "var(--teal)", minWidth: 52 }}>
                {live.has(p.id) ? "● live" : ""}
              </span>
              <span className="dr-meta" style={{ minWidth: 28, textAlign: "right" }}>
                {p.dirtyCount ? "±" + p.dirtyCount : ""}
              </span>
              <span className="dr-tag">{p.group}</span>
              <span className="dr-meta" style={{ minWidth: 84, textAlign: "right", whiteSpace: "nowrap" }}>
                {ago(p.lastChangeAt)}
              </span>
            </button>
          ))}
        </div>
        {boot && shown.length === 0 ? <div className="dr-empty" style={{ padding: "24px 12px" }}>no projects found.</div> : null}
        {hidden > 0 ? (
          <div className="dr-meta" style={{ padding: 12 }}>
            {hidden} hidden folder{hidden === 1 ? "" : "s"} not listed
          </div>
        ) : null}
      </div>
    </div>
  );
}
