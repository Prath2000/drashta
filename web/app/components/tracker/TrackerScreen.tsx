import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router";
import { useBoot } from "~/lib/boot";
import { useApi } from "~/lib/query";
import { useStore } from "~/lib/store";
import type { TrackerResponse } from "~/lib/types";
import { useItem } from "~/lib/url";
import { TRACKER_KEY } from "./actions";
import { Board } from "./Board";
import { Seg } from "./Controls";
import { MilestonesView } from "./MilestonesView";
import { ReviewView } from "./ReviewView";
import { TimelineView } from "./TimelineView";
import { awaitingReview, buildModel, featureMap, type Filter, type Group, type View } from "./model";
import "./tracker.css";

const VIEWS: View[] = ["board", "milestones", "timeline"];

function parseView(v: string | null): View {
  return VIEWS.includes(v as View) ? (v as View) : "board";
}

/**
 * The Tracker screen (02-ui-spec §6). Filter, project, view and group live in the
 * URL (`?filter=review&project=acme-api&view=milestones&group=feature`), so a reload
 * and the top-bar `review` counter land on the same view.
 */
export function TrackerScreen() {
  const [params, setParams] = useSearchParams();
  const { item, openItem } = useItem();
  const { data: boot } = useBoot();
  const lastProject = useStore((s) => s.lastProjectId);
  const tr = useApi<TrackerResponse>(TRACKER_KEY);

  const filter: Filter = params.get("filter") === "review" ? "review" : "all";
  const project = params.get("project") || "all";
  const view = parseView(params.get("view"));
  const group: Group = params.get("group") === "feature" ? "feature" : "none";

  const set = useCallback(
    (patch: Record<string, string | null>) => {
      setParams(
        (prev) => {
          const p = new URLSearchParams(prev);
          for (const [k, v] of Object.entries(patch)) {
            if (v == null || v === "") p.delete(k);
            else p.set(k, v);
          }
          return p;
        },
        { replace: true },
      );
    },
    [setParams],
  );

  const bootProjects = boot?.projects;
  const names = useMemo(() => new Map((bootProjects ?? []).map((p) => [p.id, p.name])), [bootProjects]);
  const order = useMemo(() => (bootProjects ?? []).map((p) => p.id), [bootProjects]);
  const model = useMemo(() => buildModel(tr.data, project, order), [tr.data, project, order]);
  const allFeatures = tr.data?.features ?? [];
  const featMap = useMemo(() => featureMap(model.features), [model.features]);

  const now = boot ? Date.parse(boot.now) || Date.now() : Date.now();
  const open = model.tasks.filter((t) => t.status !== "done").length;
  const waiting = model.tasks.filter(awaitingReview).length;
  const msCount = model.milestones.length;
  const summary =
    model.tasks.length + " tasks · " + open + " open · " + waiting + " awaiting review · " + msCount + (msCount === 1 ? " milestone" : " milestones");

  const tabs = [{ key: "all", label: "All" }];
  for (const id of model.projectIds) tabs.push({ key: id, label: names.get(id) ?? id });
  if (project !== "all" && !model.projectIds.includes(project)) tabs.push({ key: project, label: names.get(project) ?? project });

  const formProjects = useMemo(() => (bootProjects ?? []).filter((p) => !p.hidden || model.projectIds.includes(p.id)).map((p) => ({ id: p.id, name: p.name })), [bootProjects, model.projectIds]);
  const defaultProject =
    project !== "all" ? project : lastProject && formProjects.some((p) => p.id === lastProject) ? lastProject : model.projectIds[0] ?? formProjects[0]?.id ?? "";

  const scopeName = project === "all" ? "any project" : names.get(project) ?? project;
  const showAll = useCallback(() => set({ filter: null }), [set]);
  const onOpen = useCallback((ref: string) => openItem(ref), [openItem]);

  let body;
  if (tr.error && !tr.data) {
    body = (
      <div className="tk-state" role="alert">
        <span className="dr-empty">could not load the tracker.</span>
        <button type="button" className="tk-ghost" onClick={tr.reload}>
          Try again
        </button>
      </div>
    );
  } else if (!tr.data) {
    body = (
      <div className="tk-state" role="status">
        <span className="dr-empty">loading the tracker…</span>
      </div>
    );
  } else if (filter === "review") {
    body = (
      <ReviewView tasks={model.tasks} features={featMap} names={names} now={now} scopeName={scopeName} onShowAll={showAll} onOpen={onOpen} />
    );
  } else if (view === "milestones") {
    body = (
      <MilestonesView
        model={model}
        allMilestones={tr.data.milestones}
        projects={formProjects}
        defaultProject={defaultProject}
        names={names}
        now={now}
        item={item}
        onOpen={onOpen}
      />
    );
  } else if (view === "timeline") {
    body = <TimelineView model={model} project={project} names={names} now={now} item={item} onOpen={onOpen} />;
  } else {
    body = (
      <Board
        model={model}
        allFeatures={allFeatures}
        group={group}
        names={names}
        projects={formProjects}
        defaultProject={defaultProject}
        item={item}
        onOpen={onOpen}
      />
    );
  }

  return (
    <div className="tk-root" data-testid="tracker">
      <div className="tk-head">
        <div className="tk-titlerow">
          <h1 className="tk-h1">Tracker</h1>
          <span className="tk-summary" data-testid="tk-summary">
            {summary}
          </span>
          <span className="tk-stored">stored as .drashta/tasks/*.md in each project</span>
        </div>
        <div className="tk-controls">
          <Seg<Filter>
            label="Show"
            aria="Task filter"
            value={filter}
            onPick={(k) => set({ filter: k === "review" ? "review" : null })}
            options={[
              { key: "all", label: "All tasks" },
              { key: "review", label: "Awaiting review (" + waiting + ")" },
            ]}
          />
          <Seg<string>
            label="Project"
            aria="Project filter"
            value={project}
            onPick={(k) => set({ project: k === "all" ? null : k })}
            options={tabs}
          />
          {filter === "all" ? (
            <Seg<View>
              label="View"
              aria="View"
              value={view}
              onPick={(k) => set({ view: k === "board" ? null : k })}
              options={[
                { key: "board", label: "Board" },
                { key: "milestones", label: "Milestones" },
                { key: "timeline", label: "Timeline" },
              ]}
            />
          ) : null}
          {filter === "all" && view === "board" ? (
            <Seg<Group>
              label="Group"
              aria="Group by"
              value={group}
              onPick={(k) => set({ group: k === "feature" ? "feature" : null })}
              options={[
                { key: "none", label: "None" },
                { key: "feature", label: "Feature" },
              ]}
            />
          ) : null}
        </div>
      </div>
      {body}
    </div>
  );
}
