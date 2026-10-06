// The project page (02-ui-spec §5): header cartouche + native actions,
// project seismograph, sticky tab bar and the seven tabs.
// URL: /p/:project/:tab? (tab defaults to overview). Every row previews in
// the viewport through useItem().openItem(ref).
import { useCallback, useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { ApiError } from "~/lib/api";
import { useApi } from "~/lib/query";
import { setLastProject, useStore } from "~/lib/store";
import type { ActivityResponse, DocsResponse, NotesResponse, ProjectDetail, ServersResponse, TrackerResponse } from "~/lib/types";
import { PROJECT_TABS, pickerPath, projectPath, trackerPath, useItem, type ProjectTab } from "~/lib/url";
import { CommitsTab, useCommits } from "./CommitsTab";
import { keys } from "./data";
import { DocsTab } from "./DocsTab";
import { FilesTab } from "./FilesTab";
import { Header, vcsList } from "./Header";
import { OverviewTab } from "./OverviewTab";
import { Seismograph } from "./Seismograph";
import { ServersTab } from "./ServersTab";
import { ownSessions, SessionsTab, useSessions } from "./SessionsTab";
import { TrackerTab } from "./TrackerTab";
import { C } from "./ui";
import "./project.css";

const TAB_LABEL: Record<ProjectTab, string> = {
  overview: "Overview",
  files: "Files",
  commits: "Commits",
  sessions: "Sessions",
  servers: "Servers",
  docs: "Docs",
  tracker: "Tracker",
};

export function normalTab(t: string | undefined): ProjectTab {
  return (PROJECT_TABS as readonly string[]).includes(t ?? "") ? (t as ProjectTab) : "overview";
}

export function ProjectPage({ project: p, tab: rawTab }: { project: string; tab?: string }) {
  const tab = normalTab(rawTab);
  const navigate = useNavigate();
  const loc = useLocation();
  const { openItem } = useItem();
  const range = useStore((s) => s.range);

  const detail = useApi<ProjectDetail>(keys.project(p));
  const activity = useApi<ActivityResponse>(keys.activity(p, range));
  const servers = useApi<ServersResponse>(keys.servers(p));
  const tracker = useApi<TrackerResponse>(keys.tracker(p));
  const notes = useApi<NotesResponse>(keys.notes(p));
  const docs = useApi<DocsResponse>(keys.docs(p));
  const commits = useCommits(p);
  const sessions = useSessions(p);

  const [day, setDay] = useState<string | null>(null);
  const [dayOpen, setDayOpen] = useState(false);

  useEffect(() => {
    if (detail.data) setLastProject(p);
  }, [p, detail.data]);

  const goTab = useCallback(
    (t: string) => navigate({ pathname: projectPath(p, t), search: loc.search }),
    [navigate, p, loc.search],
  );
  const goReview = useCallback(() => navigate(trackerPath("review", p)), [navigate, p]);
  const goBoard = useCallback(() => navigate(trackerPath("all", p)), [navigate, p]);

  const acts = activity.data?.days ?? [];
  const selDay = day && acts.some((a) => a.day === day) ? day : null;
  const onPick = useCallback(
    (d: string) => {
      if (d === selDay) {
        setDay(null);
        setDayOpen(false);
      } else setDay(d);
    },
    [selDay],
  );
  const onRead = useCallback(() => {
    setDayOpen(true);
    if (tab !== "overview") goTab("overview");
  }, [tab, goTab]);

  if (detail.error && !detail.data) {
    const nf = detail.error instanceof ApiError && detail.error.status === 404;
    return (
      <div className="pj-root" data-testid="project-page">
        <div style={{ padding: "40px 32px", maxWidth: 760 }}>
          <div style={{ border: "3px double " + C.hair, padding: "16px 20px" }}>
            <h1 style={{ margin: 0, fontWeight: 400, fontSize: 32, lineHeight: 1.1 }}>{p}</h1>
            <p style={{ margin: "10px 0 0", fontSize: 14, color: C.muted }} data-testid="project-missing">
              {nf ? "no project folder by this name under ~/projects." : "could not read this project right now."}
            </p>
          </div>
          <button
            type="button"
            className="pj-hover"
            onClick={() => navigate(pickerPath())}
            style={{ marginTop: 16, height: 44, padding: "0 16px", background: "transparent", color: C.amber, border: "1px solid " + C.hair, borderRadius: 3, cursor: "pointer" }}
          >
            pick a project →
          </button>
        </div>
      </div>
    );
  }

  if (!detail.data) {
    return (
      <div className="pj-root" data-testid="project-page">
        <div style={{ padding: "24px 32px" }}>
          <div style={{ border: "3px double " + C.hair, padding: "16px 20px" }}>
            <h1 style={{ margin: 0, fontWeight: 400, fontSize: 32, lineHeight: 1.1 }}>{p}</h1>
            <p style={{ margin: "10px 0 0", fontSize: 14, color: C.muted }}>reading the project…</p>
          </div>
        </div>
      </div>
    );
  }

  const d = detail.data;
  const vcs = vcsList(d.project.vcs);
  const active = servers.data?.active ?? d.overview.servers ?? [];
  const recent = servers.data?.recent ?? [];
  const own = ownSessions(sessions.items);
  const activeKeys = new Set(active.map((s) => s.key));
  const srvTotal = active.length + recent.filter((s) => !activeKeys.has(s.key)).length;
  const counts = d.overview.counts;
  const notesList = notes.data?.notes ?? [];

  const tabCount: Record<ProjectTab, string> = {
    overview: "",
    files: String(counts.files),
    commits: String(Math.max(counts.commits, commits.items.length)),
    sessions: String(Math.max(counts.sessions, own.length)),
    servers: active.length + "/" + srvTotal,
    docs: docs.data ? String(docs.data.docs.length) : "",
    tracker: tracker.data ? String(tracker.data.tasks.length) : "",
  };

  const selAct = selDay ? acts.find((a) => a.day === selDay) : null;

  return (
    <div className="pj-root" data-testid="project-page">
      <Header p={p} project={d.project} active={active} recent={recent} latestSession={own[0] ?? null} openItem={openItem} />

      <Seismograph acts={acts} range={range} selDay={selDay} onPick={onPick} onRead={onRead} loading={activity.loading && !activity.data} />

      <nav
        role="tablist"
        aria-label="Project sections"
        style={{
          position: "sticky",
          top: 0,
          zIndex: 2,
          background: C.ground,
          display: "flex",
          gap: 2,
          padding: "18px 32px 0",
          borderBottom: "1px solid " + C.hair,
          overflowX: "auto",
        }}
      >
        {PROJECT_TABS.map((id) => {
          const on = id === tab;
          return (
            <button
              key={id}
              type="button"
              role="tab"
              aria-selected={on}
              data-testid={"tab-" + id + "-btn"}
              onClick={() => goTab(id)}
              style={{
                height: 40,
                padding: "0 16px",
                background: on ? C.panel : "transparent",
                color: on ? C.text : C.muted,
                border: "1px solid " + (on ? C.hair : "transparent"),
                borderBottom: "2px solid " + (on ? C.amber : "transparent"),
                marginBottom: -1,
                borderRadius: "3px 3px 0 0",
                fontSize: 12,
                letterSpacing: ".06em",
                cursor: "pointer",
                display: "inline-flex",
                alignItems: "center",
                gap: 8,
                whiteSpace: "nowrap",
                flex: "none",
              }}
            >
              {TAB_LABEL[id]} <span style={{ fontSize: 10, color: on ? C.amber : C.muted }}>{tabCount[id]}</span>
            </button>
          );
        })}
      </nav>

      <div role="tabpanel" aria-label={TAB_LABEL[tab]} style={{ padding: "22px 32px 40px" }}>
        {tab === "overview" ? (
          <OverviewTab
            p={p}
            detail={d}
            versioned={vcs.length > 0}
            lastCommit={commits.items[0] ?? null}
            commitsLoading={commits.loading}
            sessions={own}
            tracker={tracker.data}
            notes={notesList}
            active={active}
            recent={recent}
            day={selDay && dayOpen ? { day: selDay, files: selAct?.files ?? 0 } : null}
            onCloseDay={() => setDayOpen(false)}
            openItem={openItem}
            goReview={goReview}
            goTab={goTab}
          />
        ) : null}
        {tab === "files" ? <FilesTab p={p} total={counts.files} dirty={d.overview.dirty} notes={notesList} openItem={openItem} /> : null}
        {tab === "commits" ? <CommitsTab p={p} vcs={vcs} total={counts.commits} commits={commits} openItem={openItem} /> : null}
        {tab === "sessions" ? <SessionsTab p={p} name={d.project.name} path={d.project.path} sessions={sessions} openItem={openItem} /> : null}
        {tab === "servers" ? (
          <ServersTab p={p} active={active} recent={recent} loading={servers.loading && !servers.data} openItem={openItem} />
        ) : null}
        {tab === "docs" ? <DocsTab p={p} docs={docs.data?.docs} loading={docs.loading} error={docs.error} openItem={openItem} /> : null}
        {tab === "tracker" ? (
          <TrackerTab p={p} tracker={tracker.data} loading={tracker.loading} openItem={openItem} goBoard={goBoard} goReview={goReview} />
        ) : null}
      </div>
    </div>
  );
}
