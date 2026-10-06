// Small invented data for the Home screen: tests use it with `server.use(...homeHandlers(fx))`
// so the numbers are known and the same on every machine. dev:mock keeps using the shared
// fixtures in handlers.ts, which already answer /boot, /home and /day.
//
// To also serve this data in dev:mock, the lead can add one line to handlers.ts:
//   import { homeHandlers, makeHomeFixture } from "./home";   ... ...homeHandlers(makeHomeFixture())
import { http, HttpResponse, type HttpHandler } from "msw";
import type { Boot, DayCell, DayEvent, DayResponse, Home, ProjectSummary } from "~/lib/types";

export const HOME_NOW = "2026-10-01T10:00:00+05:30";
export const HOME_TODAY = "2026-10-01";
const NOW_MS = Date.parse(HOME_NOW);

/** The last `n` day keys, oldest first, ending today. */
export function lastDays(n: number, today = HOME_TODAY): string[] {
  const t = Date.parse(today + "T00:00:00Z");
  const out: string[] = [];
  for (let i = n - 1; i >= 0; i--) out.push(new Date(t - i * 86400000).toISOString().slice(0, 10));
  return out;
}

export function cell(p: Partial<DayCell> = {}): DayCell {
  return { files: 0, commits: 0, linesPlus: 0, linesMinus: 0, sessions: 0, snapshots: 0, notes: 0, serverMinutes: 0, ...p };
}

/** `n` empty cells with a few overrides, where index 29 is today, 28 yesterday, and so on. */
function series(over: Record<number, Partial<DayCell>>, n = 30): DayCell[] {
  return Array.from({ length: n }, (_, i) => cell(over[i] ?? {}));
}

const agoIso = (minutes: number) => new Date(NOW_MS - minutes * 60000).toISOString();

type FixtureProject = ProjectSummary & { fileCount?: number };

export interface HomeFixture {
  boot: Boot;
  /** 30 cells per project id, oldest first. */
  cells: Record<string, DayCell[]>;
  live: Record<string, number[]>;
  lastChange: Record<string, string | null>;
  /** Events and files for one day of one project; override to change the readout. */
  day: (date: string, project: string | null) => DayResponse;
  /** Request URLs seen by the handlers, so tests can assert the calls. */
  calls: { home: string[]; day: string[] };
}

function project(id: string, group: string, extra: Partial<FixtureProject> = {}): FixtureProject {
  return {
    id,
    name: id,
    group,
    path: "~/projects/" + group + "/" + id,
    vcs: ["git"],
    branch: "main",
    lastChangeAt: null,
    hidden: false,
    surveyed: true,
    dirtyCount: 0,
    ...extra,
  };
}

export const TODAY_EVENTS: DayEvent[] = [
  // deliberately out of time order: the readout must sort them
  { ts: "2026-10-01T05:40:00+05:30", kind: "snapshot", ref: "alpha:snapshot:snap-1", label: "home screen v2", projectId: "alpha", context: "pending" },
  { ts: "2026-10-01T03:10:00+05:30", kind: "commit", ref: "alpha:commit:a1b2c3d4e5f6", label: "a1b2c3d fix the lane scale", projectId: "alpha", context: "+40 −3" },
  { ts: "2026-10-01T04:20:00+05:30", kind: "session", ref: "alpha:session:s-1", label: "rework the readout", projectId: "alpha", context: ".claude" },
  { ts: "2026-10-01T06:30:00+05:30", kind: "note", ref: "alpha:commit:a1b2c3d4e5f6", label: "check the log scale here", projectId: "alpha", context: "on commit" },
  { ts: "2026-10-01T07:00:00+05:30", kind: "task", ref: "alpha:task:T-1", label: "Task created: wire the seismograph", projectId: "alpha", context: "todo" },
];

export function makeHomeFixture(): HomeFixture {
  const projects: FixtureProject[] = [
    project("alpha", "personal", { dirtyCount: 2, lastChangeAt: agoIso(13) }),
    project("bravo", "personal", { lastChangeAt: agoIso(180) }),
    project("charlie", "work", { lastChangeAt: agoIso(2 * 1440) }),
    project("delta", "tools", { lastChangeAt: agoIso(40 * 1440) }),
    project("echo", "personal", { hidden: true, lastChangeAt: agoIso(60) }),
    project("foxtrot", "tools", { vcs: [], branch: null, lastChangeAt: null, fileCount: 0 }),
  ];
  const cells: Record<string, DayCell[]> = {
    alpha: series({
      29: { files: 12, commits: 2, linesPlus: 300, linesMinus: 40, sessions: 1, snapshots: 1, notes: 2 },
      28: { files: 3 },
      26: { files: 1, commits: 1, linesPlus: 100 },
      20: { commits: 1 }, // a commit with no line stats
    }),
    bravo: series({ 29: { files: 5, commits: 1, linesPlus: 60, linesMinus: 40 }, 27: { sessions: 1 } }),
    charlie: series({ 29: { files: 500 }, 25: { files: 20 }, 10: { files: 40 } }),
    delta: series({}),
    echo: series({ 29: { files: 50, commits: 3, linesPlus: 800, linesMinus: 200 } }),
    foxtrot: series({}),
  };
  const boot: Boot = {
    now: HOME_NOW,
    statuses: ["backlog", "todo", "in_progress", "review", "done"],
    profiles: [".claude"],
    author: "Test",
    counts: { liveServers: 2, openTasks: 1, awaitingReview: 0, openNotes: 0 },
    projects,
    lastScanAt: HOME_NOW,
  };
  const fx: HomeFixture = {
    boot,
    cells,
    live: { alpha: [3000, 3001, 3000], bravo: [] },
    lastChange: Object.fromEntries(projects.map((p) => [p.id, p.lastChangeAt])),
    day: (date, proj) => {
      if (date === HOME_TODAY && proj === "alpha") {
        return {
          date,
          events: TODAY_EVENTS,
          totals: { files: 12, commits: 2, linesPlus: 300, linesMinus: 40, sessions: 1, snapshots: 1, notes: 2 },
          files: ["src/a.ts", "src/b.ts", "docs/shot.png"],
          moreFiles: 9,
        };
      }
      return { date, events: [], totals: { files: 0, commits: 0, linesPlus: 0, linesMinus: 0, sessions: 0, snapshots: 0, notes: 0 }, files: [], moreFiles: 0 };
    },
    calls: { home: [], day: [] },
  };
  return fx;
}

/** MSW handlers for /boot, /home and /day answering from `fx` (read each time, so tests may change it). */
export function homeHandlers(fx: HomeFixture): HttpHandler[] {
  return [
    http.get("*/api/boot", () => HttpResponse.json<Boot>(fx.boot)),
    http.get("*/api/home", ({ request }) => {
      fx.calls.home.push(request.url);
      const range = new URL(request.url).searchParams.get("range") === "7" ? 7 : 30;
      const body: Home = {
        range,
        days: lastDays(range),
        lanes: fx.boot.projects.map((p) => ({
          projectId: p.id,
          days: (fx.cells[p.id] ?? series({})).slice(30 - range),
          live: { servers: fx.live[p.id] ?? [], sessions: [] },
          lastChangeAt: fx.lastChange[p.id] ?? null,
        })),
      };
      return HttpResponse.json<Home>(body);
    }),
    http.get("*/api/day", ({ request }) => {
      fx.calls.day.push(request.url);
      const u = new URL(request.url);
      return HttpResponse.json<DayResponse>(fx.day(u.searchParams.get("date") ?? HOME_TODAY, u.searchParams.get("project")));
    }),
  ];
}
