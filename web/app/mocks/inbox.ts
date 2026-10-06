// Extra mock endpoints for the Inbox screen (own file; shared mocks stay untouched).
//
// The shared handlers answer `GET /inbox` with one page, no unfiled snapshots
// and no proposals, and they have no "file an unfiled drop" endpoint. These
// handlers add paging (cursor), unfiled snapshots, close_task proposals and
// the filing endpoint. They read the same in-memory db as the shared mocks.
//
//   Tests:     server.use(...inboxHandlers) and seedInboxMock({ unfiled, proposals, pageSize })
//   dev:mock:  routes/inbox.tsx calls registerInboxMocks(worker). Add ?inboxDemo=1 to the
//              URL to start with two unfiled drops and one proposal.
import { http, HttpResponse, type HttpHandler } from "msw";
import type * as T from "~/lib/types";
import { MOCK_TOKEN, mockDb } from "./handlers";

const API = "*/api";

export interface InboxMockState {
  unfiled: T.InboxResponse["unfiledSnapshots"];
  proposals: T.InboxProposal[];
  /** Feed events per page. */
  pageSize: number;
}

function fresh(): InboxMockState {
  return { unfiled: [], proposals: [], pageSize: 100 };
}

let state: InboxMockState = fresh();

export function resetInboxMocks(): void {
  state = fresh();
}

export function seedInboxMock(patch: Partial<InboxMockState>): void {
  state = { ...state, ...patch };
}

export function inboxMockState(): InboxMockState {
  return state;
}

/** Two drops and one proposal on the fixture data (for looking at the screen by hand). */
export function seedInboxDemo(): void {
  const db = mockDb();
  seedInboxMock({
    unfiled: [
      { name: "gate-art-try2.png", projectId: "notes-app", path: ".drashta/snapshots/inbox/gate-art-try2.png", date: db.now },
      { name: "hero-crop.png", projectId: "acme-api", path: ".drashta/snapshots/inbox/hero-crop.png", date: db.now },
    ],
    proposals: [{ kind: "close_task", taskRef: "notes-app:task:T-1", commitRef: "notes-app:commit:" + (db.detail["notes-app"]?.commits[0]?.id ?? "abc1234") }],
  });
}

// ---------- helpers ----------
const err = (status: number, code: string, message: string) => HttpResponse.json({ error: { code, message } }, { status });

function base(path: string): string {
  return path.split("/").pop() ?? path;
}

/** Title of the item a ref points at (the real server resolves this; the mock does the simple cases). */
function titleOf(ref: string): string {
  const body = ref.split("#")[0];
  const a = body.indexOf(":");
  const b = body.indexOf(":", a + 1);
  if (a < 0 || b < 0) return ref;
  const pid = body.slice(0, a);
  const type = body.slice(a + 1, b);
  const id = body.slice(b + 1);
  const db = mockDb();
  switch (type) {
    case "snapshot": {
      const s = db.snapshots.find((x) => x.projectId === pid && x.id === id);
      return s ? s.title + " · v" + s.version : id;
    }
    case "task":
      return db.tasks.find((t) => t.projectId === pid && t.id === id)?.title ?? id;
    case "commit":
      return db.detail[pid]?.commits.find((c) => c.id === id)?.msg ?? id;
    case "session":
      return db.detail[pid]?.sessions.find((s) => s.id === id)?.title ?? id;
    case "file":
    case "image":
    case "doc":
      return base(id);
  }
  return id;
}

function hiddenIds(): Set<string> {
  return new Set(
    mockDb()
      .projects.filter((p) => p.hidden)
      .map((p) => p.id),
  );
}

export const inboxHandlers: HttpHandler[] = [
  http.get(API + "/inbox", ({ request }) => {
    const u = new URL(request.url);
    const filter = u.searchParams.get("filter") ?? "open";
    const cursor = u.searchParams.get("cursor");
    const offset = cursor ? Number(cursor.replace(/^o/, "")) || 0 : 0;
    const db = mockDb();
    const hidden = hiddenIds();
    const events = db.events.filter((e) => !hidden.has(e.projectId)).sort((a, b) => Date.parse(b.ts) - Date.parse(a.ts));
    const feed = events.slice(offset, offset + state.pageSize);
    const next = offset + state.pageSize < events.length ? "o" + (offset + state.pageSize) : null;
    // Notes and the drops come with the first page only.
    const notes =
      offset > 0
        ? []
        : db.notes
            .filter((n) => (filter === "all" ? true : filter === "resolved" ? n.resolved : !n.resolved))
            .sort((a, b) => Date.parse(b.date) - Date.parse(a.date))
            .map((n) => ({ ...n, targetTitle: titleOf(n.ref) }));
    return HttpResponse.json<T.InboxResponse>({
      notes,
      unfiledSnapshots: offset > 0 ? [] : state.unfiled,
      proposals: offset > 0 ? [] : state.proposals,
      feed,
      next,
    });
  }),

  http.post(API + "/projects/:p/snapshots/inbox/:name/file", async ({ params, request }) => {
    if (request.headers.get("X-Drashta-Token") !== MOCK_TOKEN) return err(403, "bad_token", "missing or stale token");
    const pid = String(params.p);
    const name = decodeURIComponent(String(params.name));
    const b = (await request.json()) as { taskId?: string };
    const db = mockDb();
    const drop = state.unfiled.find((u) => u.projectId === pid && u.name === name);
    if (!drop) return err(404, "not_found", "that drop is not in the inbox folder");
    const task = db.tasks.find((t) => t.projectId === pid && t.id === b.taskId);
    if (!task) return err(422, "bad_task", "pick a task from this project");
    const version = db.snapshots.filter((s) => s.projectId === pid && s.taskId === task.id).reduce((m, s) => Math.max(m, s.version), 0) + 1;
    const snap = {
      id: task.id + "/v" + version,
      projectId: pid,
      taskId: task.id,
      version,
      title: task.title,
      path: drop.path,
      url: "/api/projects/" + pid + "/raw?path=" + encodeURIComponent(drop.path),
      captured: new Date().toISOString(),
      author: db.author,
      commit: null,
      verdict: "pending" as const,
      reviewNote: "",
      notes: [],
    };
    db.snapshots.push(snap);
    task.latestSnapshot = { version, verdict: "pending" };
    db.events.push({ ts: snap.captured, kind: "snapshot", ref: pid + ":snapshot:" + snap.id, label: snap.title + " v" + version, projectId: pid, context: "pending" });
    state = { ...state, unfiled: state.unfiled.filter((u) => u !== drop) };
    return HttpResponse.json<T.SnapshotResponse>({ snapshot: snap });
  }),
];

/** dev:mock only: adds the handlers to the browser worker (once). */
let registered = false;
export function registerInboxMocks(worker: { use: (...h: HttpHandler[]) => void }): void {
  if (registered) return;
  registered = true;
  if (typeof location !== "undefined" && new URLSearchParams(location.search).get("inboxDemo") === "1") seedInboxDemo();
  worker.use(...inboxHandlers);
}
