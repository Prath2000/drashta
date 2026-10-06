// Extra MSW handlers and fixture builders for the viewport bodies.
//
// The shared handlers (mocks/handlers.ts) do not answer
// POST /projects/{p}/tasks/{id}/snapshots and never send the binary /
// redacted / truncated flags or a paged transcript. Tests add these with
// `server.use(...viewportHandlers)` or the single-purpose builders below.
// To use them in `npm run dev:mock` too, handlers.ts would need to spread
// `viewportHandlers` into its list (see the w-viewport report).
import { http, HttpResponse, type HttpHandler } from "msw";
import type * as T from "~/lib/types";
import { mockDb } from "./handlers";

const API = "*/api";

/** POST /projects/{p}/tasks/{id}/snapshots {path} -> a new pending snapshot version. */
export const snapshotCreateHandler = http.post(API + "/projects/:p/tasks/:id/snapshots", async ({ params, request }) => {
  const db = mockDb();
  const b = (await request.json()) as { path?: string; title?: string };
  const p = String(params.p);
  const id = String(params.id);
  const task = db.tasks.find((t) => t.projectId === p && t.id === id);
  if (!task) return HttpResponse.json({ error: { code: "not_found", message: "task not found" } }, { status: 404 });
  const version = db.snapshots.filter((s) => s.projectId === p && s.taskId === id).reduce((m, s) => Math.max(m, s.version), 0) + 1;
  const snap = {
    id: id + "/v" + version,
    projectId: p,
    taskId: id,
    version,
    title: b.title ?? task.title,
    path: b.path ?? "",
    url: "/api/projects/" + p + "/raw?path=" + encodeURIComponent(b.path ?? ""),
    captured: new Date().toISOString(),
    author: db.author,
    commit: null,
    verdict: "pending" as const,
    reviewNote: "",
    notes: [],
  };
  db.snapshots.push(snap);
  task.latestSnapshot = { version, verdict: "pending" };
  return HttpResponse.json<T.SnapshotResponse>({ snapshot: snap }, { status: 201 });
});

/** GET /projects/{p}/file answering with extra flags (binary, redacted, size...). */
export function fileOverride(patch: Partial<T.FileResponse> & { binary?: boolean; redacted?: boolean }): HttpHandler {
  return http.get(API + "/projects/:p/file", ({ request }) => {
    const path = new URL(request.url).searchParams.get("path") ?? "";
    return HttpResponse.json({
      path,
      size: 2048,
      mtime: mockDb().now,
      kind: path.split(".").pop() ?? "",
      isDoc: false,
      lines: 0,
      from: 1,
      to: 0,
      text: "",
      truncated: false,
      notes: [],
      ...patch,
    });
  });
}

/** GET /projects/{p}/file for a made-up file of `total` lines, honouring from/to like the Go server. */
export function longFile(total: number): HttpHandler {
  return http.get(API + "/projects/:p/file", ({ request }) => {
    const u = new URL(request.url);
    const path = u.searchParams.get("path") ?? "";
    const from = Number(u.searchParams.get("from") ?? "1") || 1;
    const to = Math.min(total, Number(u.searchParams.get("to") ?? "2000") || 2000);
    const lines = Array.from({ length: to - from + 1 }, (_, i) => "let line" + (from + i) + " = " + (from + i) + ";");
    return HttpResponse.json<T.FileResponse>({ path, size: total * 24, mtime: mockDb().now, kind: "rs", isDoc: false, lines: total, from, to, text: lines.join("\n"), truncated: to < total, notes: [] });
  });
}

/** GET /projects/{p}/file -> 403 (a secret file the server refuses to read). */
export const fileForbidden = http.get(API + "/projects/:p/file", () =>
  HttpResponse.json({ error: { code: "secret", message: "secret file" } }, { status: 403 }),
);

/** A fake transcript of `total` turns, served in pages of `limit` with a numeric `next` cursor. */
export function pagedSession(id: string, total: number): HttpHandler {
  const roles: T.TurnRole[] = ["user", "claude", "tool"];
  const turns: T.Turn[] = Array.from({ length: total }, (_, i) => ({
    i: i * 100,
    role: roles[i % 3],
    ts: new Date(Date.UTC(2026, 8, 30, 10, 0, i)).toISOString(),
    text: roles[i % 3] === "tool" ? "Read file-" + i + ".go" : "turn " + i,
    ...(roles[i % 3] === "tool" ? { tool: "Read" } : {}),
  }));
  return http.get(API + "/sessions/:id", ({ params, request }) => {
    const db = mockDb();
    let session: T.SessionSummary | undefined;
    for (const d of Object.values(db.detail)) session = session ?? d.sessions.find((s) => s.id === params.id);
    if (params.id !== id || !session) return HttpResponse.json({ error: { code: "not_found", message: "session not found" } }, { status: 404 });
    const u = new URL(request.url);
    const limit = Number(u.searchParams.get("limit") ?? "40") || 40;
    const start = Number(u.searchParams.get("cursor") ?? "0") || 0;
    const page = turns.slice(start, start + limit);
    return HttpResponse.json<T.SessionDetailResponse>({ session: { ...session, turns: total }, turns: page, next: start + limit < total ? String(start + limit) : null });
  });
}

/** Handlers a dev:mock build could add to the shared list. */
export const viewportHandlers: HttpHandler[] = [snapshotCreateHandler];
