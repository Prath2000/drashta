// Body for ref types without a dedicated preview (note, feature, milestone,
// project) and the fallback for anything unknown. It says plainly that there
// is no preview, offers the right place to go, and still takes notes.
import { useNavigate } from "react-router";
import { qs } from "~/lib/api";
import { projectPath } from "~/lib/url";
import type { ViewportBodyProps } from "./registry";
import { baseRef, MediaNotes, useNotes } from "./common";

export function GenericBody({ refObj, resolved }: ViewportBodyProps) {
  const p = resolved.projectId || refObj.projectId;
  const ref = baseRef(refObj);
  const navigate = useNavigate();
  const notes = useNotes(ref, resolved.notes ?? []);
  const type = resolved.type ?? refObj.type;
  const go =
    type === "project"
      ? { label: "Open project page →", to: projectPath(p) }
      : type === "feature" || type === "milestone"
        ? { label: "Open in tracker →", to: "/tracker" + qs({ project: p }) }
        : null;
  return (
    <div className="vp-scroll" data-testid="generic-body">
      <div className="vp-pad">
        <div className="vp-panel tight" data-testid="no-preview">
          <span className="vp-label dim">{type}</span>
          <span className="big">No preview for this kind of item.</span>
          <span className="why">{resolved.subtitle || "drashta knows this item but has nothing to draw for it here."}</span>
          {go ? (
            <button type="button" className="vp-btn" onClick={() => navigate(go.to)}>
              {go.label}
            </button>
          ) : null}
        </div>
        <MediaNotes refStr={ref} projectId={p} notes={notes} />
      </div>
    </div>
  );
}
