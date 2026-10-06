// Extra MSW handlers for the Tracker screen. The shared handlers.ts has no
// snapshot upload, no feature / milestone writes, and its verdict handler does not
// move a task back to in_progress on "changes" (04 §4). These add them.
//
// Tests: `server.use(...trackerHandlers)`.
// Dev mock: spread `trackerHandlers` into the `handlers` array in handlers.ts
// (a shared file, so the lead does it: see the w-tracker report).
import { http, HttpResponse, type HttpHandler } from "msw";
import type * as T from "~/lib/types";
import { MOCK_TOKEN, mockDb } from "./handlers";

const API = "*/api";

const err = (status: number, code: string, message: string) => HttpResponse.json({ error: { code, message } }, { status });

function badToken(req: Request) {
  return req.headers.get("X-Drashta-Token") === MOCK_TOKEN ? null : err(403, "bad_token", "missing or stale token");
}

type Db = ReturnType<typeof mockDb>;

function taskOf(db: Db, p: string, id: string) {
  return db.tasks.find((t) => t.projectId === p && t.id === id);
}

function nextNumber(ids: string[]): number {
  return ids.reduce((m, id) => Math.max(m, Number(id.replace(/\D/g, "")) || 0), 0) + 1;
}

export const trackerHandlers: HttpHandler[] = [
  // snapshot upload: multipart `image` or JSON {url|path}
  http.post(API + "/projects/:p/tasks/:id/snapshots", async ({ params, request }) => {
    const bad = badToken(request);
    if (bad) return bad;
    const db = mockDb();
    const p = String(params.p);
    const t = taskOf(db, p, String(params.id));
    if (!t) return err(404, "not_found", "task not found");
    let path = "upload.png";
    let title = t.title;
    if ((request.headers.get("content-type") ?? "").includes("multipart")) {
      // jsdom's FormData cannot always be read back in Node: keep the defaults then.
      const form = await request.formData().catch(() => null);
      const file = form?.get("image");
      if (file instanceof File) path = file.name;
      if (form?.get("title")) title = String(form.get("title"));
    } else {
      const b = (await request.json()) as { url?: string; path?: string; title?: string };
      path = b.path ?? b.url ?? path;
      title = b.title ?? title;
    }
    const mine = db.snapshots.filter((s) => s.projectId === p && s.taskId === t.id);
    const version = mine.reduce((m, s) => Math.max(m, s.version), 0) + 1;
    const snap = {
      id: t.id + "/v" + version,
      projectId: p,
      taskId: t.id,
      version,
      title,
      path,
      url: "/api/projects/" + p + "/raw?path=" + encodeURIComponent(path),
      captured: new Date().toISOString(),
      author: db.author,
      commit: null,
      verdict: "pending" as T.Verdict,
      reviewNote: "",
    };
    db.snapshots.push(snap as (typeof db.snapshots)[number]);
    t.latestSnapshot = { version, verdict: "pending" };
    if (t.status === "in_progress") t.status = "review"; // a new version moves the task to review (04 §4)
    return HttpResponse.json<T.SnapshotResponse>({ snapshot: { ...snap, notes: [] } }, { status: 201 });
  }),

  // verdict: like the shared one, plus "changes" moves the task back to in_progress.
  // Approving never changes the status (A4).
  http.put(API + "/projects/:p/tasks/:id/snapshots/:v/verdict", async ({ params, request }) => {
    const bad = badToken(request);
    if (bad) return bad;
    const db = mockDb();
    const t = taskOf(db, String(params.p), String(params.id));
    const s = db.snapshots.find((x) => x.projectId === params.p && x.taskId === params.id && String(x.version) === params.v);
    if (!t || !s) return err(404, "not_found", "snapshot not found");
    const b = (await request.json()) as { verdict: T.Verdict; note?: string };
    if (b.verdict === "changes" && !b.note?.trim() && !s.reviewNote) return err(422, "note_required", "What needs to change?");
    s.verdict = b.verdict;
    if (b.note != null) s.reviewNote = b.note;
    const latest = db.snapshots.filter((x) => x.projectId === t.projectId && x.taskId === t.id).sort((a, c) => c.version - a.version)[0];
    t.latestSnapshot = latest ? { version: latest.version, verdict: latest.verdict } : null;
    if (b.verdict === "changes" && latest && latest.version === s.version) t.status = "in_progress";
    return HttpResponse.json<T.VerdictResponse>({ snapshot: { ...s, notes: [] }, task: { ...t } });
  }),

  // features and milestones
  http.post(API + "/projects/:p/features", async ({ params, request }) => {
    const bad = badToken(request);
    if (bad) return bad;
    const db = mockDb();
    const p = String(params.p);
    const b = (await request.json()) as { title?: string; summary?: string; milestoneId?: string | null };
    if (!b.title?.trim()) return err(422, "title_required", "Give the feature a title.");
    const id = "F-" + nextNumber(db.features.filter((f) => f.projectId === p).map((f) => f.id));
    const feature: T.Feature = { id, projectId: p, title: b.title.trim(), summary: b.summary ?? "", milestoneId: b.milestoneId ?? null, rev: "r1" };
    db.features.push(feature);
    return HttpResponse.json({ feature, rev: "r1" }, { status: 201 });
  }),
  http.post(API + "/projects/:p/milestones", async ({ params, request }) => {
    const bad = badToken(request);
    if (bad) return bad;
    const db = mockDb();
    const p = String(params.p);
    const b = (await request.json()) as { title?: string; due?: string | null };
    if (!b.title?.trim()) return err(422, "title_required", "Give the milestone a title.");
    const id = "M-" + nextNumber(db.milestones.filter((m) => m.projectId === p).map((m) => m.id));
    const milestone: T.Milestone = { id, projectId: p, title: b.title.trim(), due: b.due ?? null, featureIds: [], rev: "r1" };
    db.milestones.push(milestone);
    return HttpResponse.json({ milestone, rev: "r1" }, { status: 201 });
  }),
];

/** A PATCH handler that always answers 409, as when the task file changed on disk. Use with `server.use`. */
export function conflictHandler(): HttpHandler {
  return http.patch(API + "/projects/:p/tasks/:id", () => err(409, "rev_conflict", "the task changed since you opened it"));
}
