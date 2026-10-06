// MSW handlers for the whole drashta API, answered from fixtures/db.json
// (generated, all invented, by scripts/gen-fixtures.mjs).
//
// Used by Vitest (mocks/node.ts) and by `npm run dev:mock` (mocks/browser.ts).
// Mutations change an in-memory copy, so the UI reacts as it would against the
// real server. Call resetDb() between tests.
import { http, HttpResponse, sse, type HttpHandler } from "msw";
import raw from "./fixtures/db.json";
import type * as T from "~/lib/types";

export const MOCK_TOKEN = "mock-token";
const API = "*/api";

// ---------- db ----------
type RawDb = typeof raw;
type Lane = { projectId: string; cells: T.DayCell[]; liveServers: number[]; lastChangeAt: string | null };
type MockTask = T.Task & { links: string[] };
type MockSnapshot = T.Snapshot & { id: string; projectId: string; taskId: string; path: string };
/** `embed` overrides the default GET /servers/embed answer for this server (tests tweak it). */
type MockServer = T.Server & { running: boolean; embed?: Partial<T.EmbedInfo> };
interface Detail {
  commits: T.Commit[];
  commitDetail: Record<string, { body: string; files: T.CommitDetailResponse["files"]; patch: string }>;
  tree: (T.TreeEntry & { parent: string })[];
  files: Record<string, { lines: number; text: string }>;
  dirty: T.DirtyFile[];
  diff: string;
  sessions: T.SessionSummary[];
  sessionTurns: Record<string, T.Turn[]>;
  docs: T.Doc[];
  dayFiles: Record<string, string[]>;
  readme: { path: string; text: string } | null;
}
export interface MockDb {
  now: string;
  nowDay: string;
  dayOffsetMinutes: number;
  statuses: T.TaskStatus[];
  profiles: string[];
  author: string;
  hiddenFolders: string[];
  days: string[];
  projects: (T.ProjectSummary & { detail: "full" | "light" })[];
  lanes: Lane[];
  detail: Record<string, Detail>;
  servers: MockServer[];
  events: T.DayEvent[];
  milestones: T.Milestone[];
  features: T.Feature[];
  tasks: MockTask[];
  snapshots: MockSnapshot[];
  notes: T.Note[];
}

let db: MockDb = clone(raw);
let seq = 0;
const freshDesktop = (): T.DesktopResponse => ({ available: true, mode: "wallpaper", modes: ["browse", "wallpaper", "blank"] });
let desktop = freshDesktop();
function clone(r: RawDb): MockDb {
  return JSON.parse(JSON.stringify(r)) as MockDb;
}

/** Restores the fixture data (call in beforeEach). */
export function resetDb(): void {
  db = clone(raw);
  seq = 0;
  desktop = freshDesktop();
}

/** Direct access for tests that need to tweak data. */
export function mockDb(): MockDb {
  return db;
}

// ---------- helpers ----------
const err = (status: number, code: string, message: string) => HttpResponse.json({ error: { code, message } }, { status });
const notFound = (what: string) => err(404, "not_found", what + " not found");
const proj = (id: string) => db.projects.find((p) => p.id === id);
const det = (id: string) => db.detail[id];
const dayOf = (iso: string) => new Date(Date.parse(iso) + db.dayOffsetMinutes * 60000).toISOString().slice(0, 10);

function counts(): T.Counts {
  return {
    liveServers: db.servers.filter((s) => s.running).length,
    openTasks: db.tasks.filter((t) => t.status !== "done").length,
    awaitingReview: db.tasks.filter((t) => t.latestSnapshot?.verdict === "pending").length,
    openNotes: db.notes.filter((n) => !n.resolved).length,
  };
}

function checkToken(req: Request) {
  if (req.headers.get("X-Drashta-Token") !== MOCK_TOKEN) return err(403, "bad_token", "missing or stale token");
  return null;
}

function rangeOf(url: URL): 7 | 30 {
  return url.searchParams.get("range") === "7" ? 7 : 30;
}

function stripServer(s: MockServer): T.Server {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { running, embed, ...rest } = s;
  return rest;
}

function parseRefStr(ref: string): { projectId: string; type: string; id: string } | null {
  const body = ref.split("#")[0];
  const a = body.indexOf(":");
  const b = body.indexOf(":", a + 1);
  if (a < 0 || b < 0) return null;
  return { projectId: body.slice(0, a), type: body.slice(a + 1, b), id: body.slice(b + 1) };
}

function taskFor(projectId: string, id: string) {
  return db.tasks.find((t) => t.projectId === projectId && t.id === id);
}

function publicTask(t: MockTask): T.Task {
  return { ...t };
}

function resolve(ref: string): T.ResolveResponse | null {
  const r = parseRefStr(ref);
  if (!r) return null;
  const p = proj(r.projectId);
  const d = det(r.projectId);
  if (!p || !d) return null;
  const notes = db.notes.filter((n) => n.ref.split("#")[0] === ref.split("#")[0]);
  const tasks = db.tasks.filter((t) => t.links.includes(ref.split("#")[0])).map(publicTask);
  const base = { ref, projectId: p.id, notes, tasks, nativeActions: [] as T.NativeAction[] };
  const light = p.detail === "light";
  const act = (action: string, label: string, command: string) => [{ action, label, command }];
  switch (r.type) {
    case "file":
    case "doc":
    case "image": {
      const node = d.tree.find((n) => n.path === r.id);
      const fc = d.files[r.id];
      const isImg = /\.(png|jpe?g|gif|webp|svg|avif)$/i.test(r.id);
      if (!node && !fc && !(isImg && db.snapshots.some((s) => s.path === r.id))) return null;
      const name = r.id.split("/").pop() ?? r.id;
      if (isImg || r.type === "image")
        return { ...base, type: "image", title: name, subtitle: p.name, icon: "▣", date: node?.mtime ?? null, obj: { path: r.id, size: node?.size ?? null, url: "/api/projects/" + p.id + "/raw?path=" + encodeURIComponent(r.id) }, nativeLabel: "Open in Preview", nativeActions: act("default", "Open in Preview", "open -a Preview " + p.path + "/" + r.id), light };
      const isDoc = /\.(md|txt)$/i.test(r.id);
      return { ...base, type: "file", title: name, subtitle: (r.id.includes("/") ? r.id.slice(0, r.id.lastIndexOf("/")) : ".") + (fc ? " · " + fc.lines + " lines" : ""), icon: isDoc ? "¶" : "⌗", date: node?.mtime ?? null, obj: { path: r.id, size: node?.size ?? null, mtime: node?.mtime ?? null, isDoc }, nativeLabel: "Open in editor", nativeActions: act("editor", "Open in editor", "code -g " + p.path + "/" + r.id + ":1"), light };
    }
    case "commit": {
      const c = d.commits.find((x) => x.id === r.id);
      if (!c) return null;
      return { ...base, type: "commit", title: c.msg || c.id, subtitle: c.vcs + " " + c.id + (c.author ? " · " + c.author : "") + " · " + c.ts.slice(0, 16).replace("T", " ") + (c.plus != null ? " · +" + c.plus + " −" + c.minus : ""), icon: "◆", date: c.ts, obj: c, nativeLabel: c.vcs === "git" ? "git show" : "fossil diff", nativeActions: act("terminal", "git show", c.vcs === "git" ? "git -C " + p.path + " show " + c.id : "fossil diff --checkin " + c.id), light: light || !d.commitDetail[c.id] };
    }
    case "session": {
      const s = d.sessions.find((x) => x.id === r.id);
      if (!s) return null;
      return { ...base, type: "session", title: s.title, subtitle: "Claude · " + s.turns + " turns · " + s.profile + " · " + s.start.slice(0, 16).replace("T", " "), icon: "✦", date: s.start, obj: s, nativeLabel: "Resume session", nativeActions: act("resume", "Resume session", "cd " + p.path + " && claude --resume " + s.id), light: light || !d.sessionTurns[s.id] };
    }
    case "server": {
      const s = db.servers.find((x) => x.projectId === p.id && String(x.port) === r.id);
      if (!s) return null;
      return { ...base, type: "server", title: s.title || "localhost:" + s.port, subtitle: "localhost:" + s.port + " · " + s.name + " · pid " + s.pid + " · " + (s.running ? "running" : "stopped"), icon: "●", date: null, obj: s, nativeLabel: "Open in Safari", nativeActions: act("safari", "Open in Safari", "open -a Safari http://localhost:" + s.port) };
    }
    case "snapshot": {
      const s = db.snapshots.find((x) => x.projectId === p.id && x.id === r.id);
      if (!s) return null;
      return { ...base, type: "snapshot", title: s.title + " · v" + s.version, subtitle: "Snapshot · " + s.author + " · " + dayOf(s.captured) + " · " + s.verdict, icon: "◐", date: s.captured, obj: s, nativeLabel: "Open in Preview", nativeActions: act("default", "Open in Preview", "open -a Preview " + p.path + "/" + s.path) };
    }
    case "task": {
      const t = taskFor(p.id, r.id);
      if (!t) return null;
      const f = db.features.find((x) => x.id === t.featureId && x.projectId === p.id);
      return { ...base, type: "task", title: t.title, subtitle: t.id + " · " + t.status.replace("_", " ") + " · " + t.assignee + (f ? " · " + f.title : ""), icon: "▮", date: t.created, obj: t, nativeLabel: "Open task file", nativeActions: act("editor", "Open task file", "code " + p.path + "/.drashta/tasks/" + t.id + ".md") };
    }
    case "note": {
      const n = db.notes.find((x) => x.projectId === p.id && x.id === r.id);
      if (!n) return null;
      return resolve(n.ref);
    }
  }
  return null;
}

function laneDays(l: Lane, range: 7 | 30): T.DayCell[] {
  return l.cells.slice(l.cells.length - range);
}

function search(q: string): T.SearchHit[] {
  const s = q.trim().toLowerCase();
  if (!s) return [];
  const hits: T.SearchHit[] = [];
  const vis = new Set(db.projects.filter((p) => !p.hidden).map((p) => p.id));
  if (s.startsWith(":")) {
    const port = s.slice(1);
    if (!port) return [];
    for (const sv of db.servers)
      if (sv.projectId && vis.has(sv.projectId) && String(sv.port).includes(port))
        hits.push({ kind: "server", ref: sv.projectId + ":server:" + sv.port, projectId: sv.projectId, title: ":" + sv.port + " · " + (sv.title || sv.name), snippet: sv.cmd, ts: null });
    return hits;
  }
  const has = (x: unknown) => typeof x === "string" && x.toLowerCase().includes(s);
  for (const p of db.projects) {
    if (!vis.has(p.id)) continue;
    if (has(p.name) || has(p.description) || has(p.path) || (p.stack ?? []).some(has))
      hits.push({ kind: "project", ref: p.id + ":project:" + p.id, projectId: p.id, title: p.name, snippet: p.description || p.path, ts: p.lastChangeAt });
    const d = det(p.id);
    for (const n of d.tree) if (!n.dir && has(n.path)) hits.push({ kind: /\.(md|txt)$/i.test(n.path) ? "doc" : "file", ref: p.id + ":file:" + n.path, projectId: p.id, title: n.path.split("/").pop() ?? n.path, snippet: n.path, ts: n.mtime });
    for (const c of d.commits) if (has(c.msg) || has(c.id)) hits.push({ kind: "commit", ref: p.id + ":commit:" + c.id, projectId: p.id, title: c.msg || c.id, snippet: c.vcs + " " + c.id + " · " + c.ts.slice(0, 10), ts: c.ts });
    for (const se of d.sessions) if (has(se.title)) hits.push({ kind: "session", ref: p.id + ":session:" + se.id, projectId: p.id, title: se.title, snippet: se.turns + " turns · " + se.start.slice(0, 10), ts: se.start });
  }
  for (const t of db.tasks) if (vis.has(t.projectId) && (has(t.title) || has(t.id) || has(t.assignee))) hits.push({ kind: "task", ref: t.projectId + ":task:" + t.id, projectId: t.projectId, title: t.title, snippet: t.id + " · " + t.status.replace("_", " "), ts: t.created });
  for (const sn of db.snapshots) if (vis.has(sn.projectId) && (has(sn.title) || has(sn.path) || has(sn.reviewNote))) hits.push({ kind: "snapshot", ref: sn.projectId + ":snapshot:" + sn.id, projectId: sn.projectId, title: sn.title + " · v" + sn.version, snippet: sn.verdict + " · " + sn.path, ts: sn.captured });
  for (const n of db.notes) if (vis.has(n.projectId) && has(n.text)) hits.push({ kind: "note", ref: n.ref, projectId: n.projectId, title: n.text, snippet: (n.resolved ? "resolved" : "open") + " · on " + n.ref, ts: n.date });
  for (const sv of db.servers) if (sv.projectId && vis.has(sv.projectId) && (has(String(sv.port)) || has(sv.title) || has(sv.name) || has(sv.cmd))) hits.push({ kind: "server", ref: sv.projectId + ":server:" + sv.port, projectId: sv.projectId, title: ":" + sv.port + " · " + (sv.title || sv.name), snippet: sv.cmd, ts: null });
  return hits.slice(0, 60);
}

// ---------- live events (SSE) ----------
type Client = { send: (m: { id?: string; event: string; data: string }) => void };
const clients = new Set<Client>();
function push(event: string, data: unknown) {
  const id = String(++seq);
  for (const c of clients) {
    try {
      c.send({ id, event, data: JSON.stringify(data) });
    } catch {
      clients.delete(c);
    }
  }
}

// ---------- handlers ----------
export const handlers: HttpHandler[] = [
  http.get(API + "/session", () => HttpResponse.json({ token: MOCK_TOKEN })),
  http.get(API + "/boot", () =>
    HttpResponse.json<T.Boot>({
      now: db.now,
      statuses: db.statuses,
      profiles: db.profiles,
      author: db.author,
      counts: counts(),
      lastScanAt: db.now,
      projects: db.projects.map((p) => ({ ...p })),
    }),
  ),
  http.get(API + "/home", ({ request }) => {
    const range = rangeOf(new URL(request.url));
    return HttpResponse.json<T.Home>({
      range,
      days: db.days.slice(db.days.length - range),
      lanes: db.lanes.map((l) => ({
        projectId: l.projectId,
        days: laneDays(l, range),
        live: { servers: db.servers.filter((s) => s.projectId === l.projectId && s.running).map((s) => s.port), sessions: [] },
        lastChangeAt: l.lastChangeAt,
      })),
    });
  }),
  http.get(API + "/day", ({ request }) => {
    const u = new URL(request.url);
    const date = u.searchParams.get("date") ?? db.nowDay;
    const project = u.searchParams.get("project");
    const evs = db.events.filter((e) => dayOf(e.ts) === date && (!project || e.projectId === project));
    const totals: T.DayTotals = { files: 0, commits: 0, linesPlus: 0, linesMinus: 0, sessions: 0, snapshots: 0, notes: 0 };
    const i = db.days.indexOf(date);
    for (const l of db.lanes) {
      if (project && l.projectId !== project) continue;
      const c = i >= 0 ? l.cells[i] : null;
      if (!c) continue;
      totals.files += c.files;
      totals.commits += c.commits;
      totals.linesPlus += c.linesPlus;
      totals.linesMinus += c.linesMinus;
      totals.sessions += c.sessions;
      totals.snapshots += c.snapshots;
      totals.notes += c.notes;
    }
    const files = project ? det(project)?.dayFiles[date] ?? [] : [];
    return HttpResponse.json<T.DayResponse>({ date, events: evs, totals, files: files.slice(0, 20), moreFiles: Math.max(0, files.length - 20) });
  }),
  http.get(API + "/inbox", ({ request }) => {
    const f = new URL(request.url).searchParams.get("filter") ?? "open";
    const notes = db.notes.filter((n) => (f === "all" ? true : f === "resolved" ? n.resolved : !n.resolved)).map((n) => ({ ...n, targetTitle: resolve(n.ref)?.title ?? n.ref }));
    return HttpResponse.json<T.InboxResponse>({ notes, unfiledSnapshots: [], proposals: [], feed: db.events.slice().reverse().slice(0, 200), next: null });
  }),

  // project
  http.get(API + "/projects/:p", ({ params }) => {
    const p = proj(String(params.p));
    const d = det(String(params.p));
    if (!p || !d) return notFound("project");
    return HttpResponse.json<T.ProjectDetail>({
      project: { ...p },
      overview: {
        readme: d.readme,
        counts: { files: d.tree.filter((n) => !n.dir).length, commits: d.commits.length, sessions: d.sessions.length, openTasks: db.tasks.filter((t) => t.projectId === p.id && t.status !== "done").length, openNotes: db.notes.filter((n) => n.projectId === p.id && !n.resolved).length },
        dirty: d.dirty,
        servers: db.servers.filter((s) => s.projectId === p.id && s.running).map(stripServer),
        milestones: db.milestones.filter((m) => m.projectId === p.id),
      },
    });
  }),
  http.get(API + "/projects/:p/activity", ({ params, request }) => {
    const l = db.lanes.find((x) => x.projectId === params.p);
    if (!l) return notFound("project");
    const range = rangeOf(new URL(request.url));
    const keys = db.days.slice(db.days.length - range);
    return HttpResponse.json<T.ActivityResponse>({ days: laneDays(l, range).map((c, i) => ({ day: keys[i], ...c })) });
  }),
  http.get(API + "/projects/:p/tree", ({ params, request }) => {
    const d = det(String(params.p));
    if (!d) return notFound("project");
    const u = new URL(request.url);
    const dir = u.searchParams.get("dir") ?? "";
    const depth = Number(u.searchParams.get("depth") ?? "2") || 2;
    const level = (path: string) => (path ? path.split("/").length : 0);
    const base = level(dir);
    const entries = d.tree
      .filter((n) => (dir ? n.path.startsWith(dir + "/") : true) && level(n.path) - base <= depth)
      .map((n) => {
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        const { parent, ...e } = n;
        return { ...e, dirty: d.dirty.find((x) => x.path === n.path)?.state };
      });
    return HttpResponse.json<T.TreeResponse>({ dir, entries });
  }),
  http.get(API + "/projects/:p/file", ({ params, request }) => {
    const d = det(String(params.p));
    const u = new URL(request.url);
    const path = u.searchParams.get("path") ?? "";
    const fc = d?.files[path];
    const node = d?.tree.find((n) => n.path === path);
    if (!d || (!fc && !node)) return notFound("file");
    const from = Number(u.searchParams.get("from") ?? "1") || 1;
    const to = Number(u.searchParams.get("to") ?? "400") || 400;
    const lines = (fc?.text ?? "").split("\n");
    const text = lines.slice(from - 1, to).join("\n");
    return HttpResponse.json<T.FileResponse>({ path, size: node?.size ?? (fc?.text.length ?? 0), mtime: node?.mtime ?? db.now, kind: path.split(".").pop() ?? "", isDoc: /\.(md|txt)$/i.test(path), lines: fc?.lines ?? lines.length, from, to: Math.min(to, lines.length), text, truncated: lines.length > to, notes: db.notes.filter((n) => n.ref === params.p + ":file:" + path) });
  }),
  http.get(API + "/projects/:p/raw", ({ request }) => {
    const path = new URL(request.url).searchParams.get("path") ?? "";
    const label = path.split("/").pop() ?? "image";
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="750" viewBox="0 0 1200 750"><rect width="1200" height="750" fill="#121210"/><rect x="0.5" y="0.5" width="1199" height="749" fill="none" stroke="#2c2b27"/><text x="600" y="380" fill="#a39e92" font-family="monospace" font-size="28" text-anchor="middle">${label.replace(/[<&>]/g, "")}</text></svg>`;
    return new HttpResponse(svg, { headers: { "Content-Type": "image/svg+xml" } });
  }),
  http.get(API + "/projects/:p/commits", ({ params, request }) => {
    const d = det(String(params.p));
    if (!d) return notFound("project");
    const u = new URL(request.url);
    const limit = Number(u.searchParams.get("limit") ?? "50") || 50;
    const start = Number(u.searchParams.get("cursor") ?? "0") || 0;
    const list = d.commits.slice(start, start + limit);
    return HttpResponse.json<T.CommitsResponse>({ commits: list, next: start + limit < d.commits.length ? String(start + limit) : null });
  }),
  http.get(API + "/projects/:p/commits/:id", ({ params }) => {
    const d = det(String(params.p));
    const c = d?.commits.find((x) => x.id === params.id);
    if (!d || !c) return notFound("commit");
    const cd = d.commitDetail[c.id] ?? { body: "", files: [], patch: "" };
    return HttpResponse.json<T.CommitDetailResponse>({ commit: c, ...cd });
  }),
  http.get(API + "/projects/:p/dirty", ({ params }) => {
    const d = det(String(params.p));
    if (!d) return notFound("project");
    return HttpResponse.json<T.DirtyResponse>({ files: d.dirty, diff: d.diff });
  }),
  http.get(API + "/projects/:p/sessions", ({ params }) => {
    const d = det(String(params.p));
    if (!d) return notFound("project");
    return HttpResponse.json<T.SessionsResponse>({ sessions: d.sessions, next: null });
  }),
  http.get(API + "/sessions/:id", ({ params, request }) => {
    for (const d of Object.values(db.detail)) {
      const s = d.sessions.find((x) => x.id === params.id);
      if (!s) continue;
      const roles = (new URL(request.url).searchParams.get("roles") ?? "user,claude,tool").split(",");
      return HttpResponse.json<T.SessionDetailResponse>({ session: s, turns: (d.sessionTurns[s.id] ?? []).filter((t) => roles.includes(t.role)), next: null });
    }
    return notFound("session");
  }),
  http.get(API + "/projects/:p/docs", ({ params }) => {
    const d = det(String(params.p));
    if (!d) return notFound("project");
    return HttpResponse.json<T.DocsResponse>({ docs: d.docs });
  }),
  http.get(API + "/projects/:p/github", ({ params }) => {
    if (!proj(String(params.p))) return notFound("project");
    return HttpResponse.json<T.GithubResponse>({ prs: [], issues: [], runs: [], fetchedAt: db.now });
  }),

  // servers
  http.get(API + "/servers", ({ request }) => {
    const p = new URL(request.url).searchParams.get("project");
    const list = db.servers.filter((s) => !p || s.projectId === p);
    return HttpResponse.json<T.ServersResponse>({ active: list.filter((s) => s.running).map(stripServer), recent: list.filter((s) => !s.running).map((s) => ({ ...stripServer(s), lastSeen: db.now })) });
  }),
  // Can the live app be shown in an iframe? Helpers are not web pages; dev servers can be framed.
  http.get(API + "/servers/embed", ({ request }) => {
    const port = Number(new URL(request.url).searchParams.get("port"));
    const s = db.servers.find((x) => x.port === port && x.running);
    if (!s) return notFound("server");
    const web = s.kind !== "helper";
    return HttpResponse.json<T.EmbedInfo>({ http: web, frameable: web, reason: web ? "" : "not an http server", title: s.title, ...s.embed });
  }),
  http.post(API + "/servers/kill", async ({ request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    const b = (await request.json()) as { pid: number; key: string; confirm?: boolean };
    if (!b.confirm) return err(422, "confirm_required", "confirm the kill");
    const s = db.servers.find((x) => x.key === b.key && x.pid === b.pid && x.running);
    if (!s) return err(409, "not_live", "that server is not running");
    s.running = false;
    push("servers", { projectId: s.projectId, active: db.servers.filter((x) => x.running).map((x) => x.port) });
    push("counts", counts());
    return HttpResponse.json({ ok: true });
  }),
  http.post(API + "/servers/restart", async ({ request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    const b = (await request.json()) as { key: string; confirm?: boolean };
    if (!b.confirm) return err(422, "confirm_required", "confirm the restart");
    const s = db.servers.find((x) => x.key === b.key);
    if (!s) return notFound("server");
    s.running = true;
    push("servers", { projectId: s.projectId, active: db.servers.filter((x) => x.running).map((x) => x.port) });
    push("counts", counts());
    return HttpResponse.json({ ok: true, log: "~/.drashta/logs/" + s.port + ".log" });
  }),

  // tracker
  http.get(API + "/tracker", ({ request }) => {
    const u = new URL(request.url);
    const project = u.searchParams.get("project");
    const filter = u.searchParams.get("filter");
    const feature = u.searchParams.get("feature");
    const status = u.searchParams.get("status");
    const tasks = db.tasks.filter((t) => (!project || t.projectId === project) && (filter !== "review" || t.latestSnapshot?.verdict === "pending") && (!feature || t.featureId === feature) && (!status || t.status === status));
    return HttpResponse.json<T.TrackerResponse>({ milestones: db.milestones.filter((m) => !project || m.projectId === project), features: db.features.filter((f) => !project || f.projectId === project), tasks: tasks.map(publicTask) });
  }),
  http.get(API + "/projects/:p/tasks/:id", ({ params }) => {
    const t = taskFor(String(params.p), String(params.id));
    if (!t) return notFound("task");
    const self = t.projectId + ":task:" + t.id;
    return HttpResponse.json<T.TaskDetailResponse>({
      task: publicTask(t),
      rev: t.rev ?? "r1",
      links: t.links.map((ref) => {
        const r = resolve(ref);
        return { ref, source: "manual", resolved: r ? { title: r.title, subtitle: r.subtitle, date: r.date } : null };
      }),
      snapshots: db.snapshots.filter((s) => s.projectId === t.projectId && s.taskId === t.id).map((s) => ({ ...s, notes: db.notes.filter((n) => n.ref === s.projectId + ":snapshot:" + s.id) })),
      notes: db.notes.filter((n) => n.ref === self),
      log: [{ ts: t.created, author: t.assignee, text: "created" }],
    });
  }),
  http.post(API + "/projects/:p/tasks", async ({ params, request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    const b = (await request.json()) as { title?: string; featureId?: string | null; status?: T.TaskStatus; assignee?: string; links?: string[] };
    if (!b.title?.trim()) return err(422, "title_required", "Give the task a title.");
    const pid = String(params.p);
    if (!proj(pid)) return notFound("project");
    const n = db.tasks.filter((t) => t.projectId === pid).reduce((m, t) => Math.max(m, Number(t.id.replace(/\D/g, "")) || 0), 0) + 1;
    const t: MockTask = { id: "T-" + n, projectId: pid, featureId: b.featureId ?? null, title: b.title.trim(), status: b.status ?? "todo", assignee: b.assignee ?? db.author, created: new Date().toISOString(), rev: "r1", links: b.links ?? [], latestSnapshot: null, linkCount: (b.links ?? []).length, openNotes: 0 };
    db.tasks.push(t);
    push("tracker", { projectId: pid, kind: "task", id: t.id });
    push("counts", counts());
    return HttpResponse.json<T.TaskWriteResponse>({ task: publicTask(t), rev: "r1" }, { status: 201 });
  }),
  http.patch(API + "/projects/:p/tasks/:id", async ({ params, request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    const t = taskFor(String(params.p), String(params.id));
    if (!t) return notFound("task");
    const ifMatch = request.headers.get("If-Match");
    if (ifMatch && ifMatch !== t.rev) return err(409, "rev_conflict", "the task changed since you opened it");
    const b = (await request.json()) as Partial<T.Task>;
    Object.assign(t, b);
    t.rev = "r" + (Number(String(t.rev ?? "r1").slice(1)) + 1);
    push("tracker", { projectId: t.projectId, kind: "task", id: t.id });
    push("counts", counts());
    return HttpResponse.json<T.TaskWriteResponse>({ task: publicTask(t), rev: t.rev });
  }),
  http.post(API + "/projects/:p/tasks/:id/links", async ({ params, request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    const t = taskFor(String(params.p), String(params.id));
    if (!t) return notFound("task");
    const b = (await request.json()) as { ref: string };
    if (!t.links.includes(b.ref)) t.links.push(b.ref);
    t.linkCount = t.links.length;
    push("tracker", { projectId: t.projectId, kind: "task", id: t.id });
    return HttpResponse.json<T.LinksResponse>({ links: t.links.map((ref) => ({ ref, source: "manual", resolved: null })) });
  }),
  http.delete(API + "/projects/:p/tasks/:id/links", ({ params, request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    const t = taskFor(String(params.p), String(params.id));
    if (!t) return notFound("task");
    const ref = new URL(request.url).searchParams.get("ref");
    t.links = t.links.filter((l) => l !== ref);
    t.linkCount = t.links.length;
    push("tracker", { projectId: t.projectId, kind: "task", id: t.id });
    return HttpResponse.json<T.LinksResponse>({ links: t.links.map((r) => ({ ref: r, source: "manual", resolved: null })) });
  }),
  http.get(API + "/projects/:p/tasks/:id/snapshots", ({ params }) => {
    const list = db.snapshots.filter((s) => s.projectId === params.p && s.taskId === params.id).map((s) => ({ ...s, notes: db.notes.filter((n) => n.ref === s.projectId + ":snapshot:" + s.id) }));
    return HttpResponse.json<T.SnapshotsResponse>({ snapshots: list });
  }),
  http.put(API + "/projects/:p/tasks/:id/snapshots/:v/verdict", async ({ params, request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    const s = db.snapshots.find((x) => x.projectId === params.p && x.taskId === params.id && String(x.version) === params.v);
    const t = taskFor(String(params.p), String(params.id));
    if (!s || !t) return notFound("snapshot");
    const b = (await request.json()) as { verdict: T.Verdict; note?: string };
    if (b.verdict === "changes" && !b.note?.trim() && !s.reviewNote) return err(422, "note_required", "What needs to change?");
    s.verdict = b.verdict;
    if (b.note != null) s.reviewNote = b.note;
    const latest = db.snapshots.filter((x) => x.projectId === t.projectId && x.taskId === t.id).sort((a, c) => c.version - a.version)[0];
    t.latestSnapshot = latest ? { version: latest.version, verdict: latest.verdict } : null;
    push("tracker", { projectId: t.projectId, kind: "snapshot", id: s.id });
    push("counts", counts());
    return HttpResponse.json<T.VerdictResponse>({ snapshot: { ...s, notes: [] }, task: publicTask(t) });
  }),

  // notes
  http.get(API + "/notes", ({ request }) => {
    const u = new URL(request.url);
    const ref = u.searchParams.get("ref");
    const project = u.searchParams.get("project");
    const open = u.searchParams.get("open") === "1";
    return HttpResponse.json<T.NotesResponse>({ notes: db.notes.filter((n) => (!ref || n.ref.split("#")[0] === ref.split("#")[0]) && (!project || n.projectId === project) && (!open || !n.resolved)) });
  }),
  http.post(API + "/notes", async ({ request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    const b = (await request.json()) as { ref: string; text: string; line?: number; x?: number; y?: number };
    const r = parseRefStr(b.ref);
    if (!r || !b.text?.trim()) return err(422, "bad_note", "a note needs a ref and text");
    const n = db.notes.reduce((m, x) => Math.max(m, Number(x.id.replace(/\D/g, "")) || 0), 0) + 1;
    const note: T.Note = { id: "N-" + n, ref: b.ref, projectId: r.projectId, text: b.text, author: db.author, date: new Date().toISOString(), resolved: false, ...(b.line != null ? { line: b.line } : {}), ...(b.x != null ? { x: b.x, y: b.y } : {}) };
    db.notes.push(note);
    push("tracker", { projectId: r.projectId, kind: "note", id: note.id });
    push("counts", counts());
    return HttpResponse.json<T.NoteResponse>({ note });
  }),
  http.patch(API + "/projects/:p/notes/:id", async ({ params, request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    const n = db.notes.find((x) => x.projectId === params.p && x.id === params.id);
    if (!n) return notFound("note");
    const b = (await request.json()) as { resolved?: boolean; text?: string };
    if (b.resolved != null) n.resolved = b.resolved;
    if (b.text != null) n.text = b.text;
    push("tracker", { projectId: n.projectId, kind: "note", id: n.id });
    push("counts", counts());
    return HttpResponse.json<T.NoteResponse>({ note: n });
  }),

  // refs, search, actions
  http.get(API + "/resolve", ({ request }) => {
    const ref = new URL(request.url).searchParams.get("ref") ?? "";
    const r = resolve(ref);
    return r ? HttpResponse.json<T.ResolveResponse>(r) : notFound("item");
  }),
  http.get(API + "/search", ({ request }) => {
    const q = new URL(request.url).searchParams.get("q") ?? "";
    return HttpResponse.json<T.SearchResponse>({ hits: search(q) });
  }),
  http.post(API + "/refresh", async ({ request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    push("scan", { state: "running", at: new Date().toISOString() });
    setTimeout(() => push("scan", { state: "done", at: new Date().toISOString() }), 900);
    return HttpResponse.json<T.RefreshResponse>({ started: true });
  }),
  http.post(API + "/open", async ({ request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    const b = (await request.json()) as { ref: string; action?: string };
    const r = resolve(b.ref);
    if (!r) return err(400, "bad_ref", "nothing to open for " + b.ref);
    return HttpResponse.json<T.OpenResponse>({ ok: true, ran: r.nativeActions[0]?.command ?? "" });
  }),
  http.get(API + "/desktop", () => HttpResponse.json<T.DesktopResponse>(desktop)),
  http.post(API + "/desktop", async ({ request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    const b = (await request.json()) as { mode?: string };
    if (!desktop.modes.includes(b.mode as T.DesktopMode)) return err(400, "bad_request", "unknown desktop mode");
    desktop.mode = b.mode as T.DesktopMode;
    return HttpResponse.json<T.DesktopResponse>(desktop);
  }),
  http.post(API + "/presence", ({ request }) => checkToken(request) ?? HttpResponse.json({ ok: true })),
  http.get(API + "/settings", () => HttpResponse.json<T.Settings>({ hiddenFolders: db.hiddenFolders, author: db.author, editorCmd: "code" })),
  http.patch(API + "/settings", async ({ request }) => {
    const bad = checkToken(request);
    if (bad) return bad;
    const b = (await request.json()) as Partial<T.Settings>;
    if (b.hiddenFolders) {
      db.hiddenFolders = b.hiddenFolders;
      for (const p of db.projects) p.hidden = b.hiddenFolders.includes(p.path.split("/").pop() ?? p.id);
      push("project", { projectId: "", what: "files" });
    }
    if (b.author) db.author = b.author;
    return HttpResponse.json<T.Settings>({ hiddenFolders: db.hiddenFolders, author: db.author, editorCmd: "code" });
  }),
  http.get(API + "/state", () => HttpResponse.json<T.StateResponse>({ resync: true })),
  // SSE needs EventSource (browser). Node tests use a fake EventSource instead.
  ...(typeof EventSource !== "undefined"
    ? [
        sse(API + "/events", ({ client }) => {
          const c: Client = { send: (m) => client.send(m as never) };
          clients.add(c);
          client.send({ id: String(seq), event: "hello", data: JSON.stringify({ now: new Date().toISOString(), counts: counts(), conn: "mock-conn-" + clients.size }) } as never);
        }),
      ]
    : []),
];
