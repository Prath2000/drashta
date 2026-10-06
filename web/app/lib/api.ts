// Typed client for the drashta HTTP API (docs/spec/04-data-model.md §6).
//
// - Every path here is relative to `/api`.
// - Mutating calls (POST, PATCH, PUT, DELETE) send `X-Drashta-Token`.
//   The token comes from `<meta name="drashta-token">` (prod) or `GET /api/session` (dev).
// - A 403 on a mutating call usually means the server restarted and the token
//   changed. The client then fetches the new token from `GET /api/session` and
//   retries the call once, so open drafts survive a restart. Only when that
//   fails too does the page reload, once (a sessionStorage flag stops a loop).
// - Error bodies `{error:{code,message}}` become an `ApiError`.
import type * as T from "./types";

export const API_BASE = "/api";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

// ---------- token ----------
let tokenCache: string | null = null;
let tokenPromise: Promise<string> | null = null;

function readMetaToken(): string | null {
  if (typeof document === "undefined") return null;
  const m = document.querySelector('meta[name="drashta-token"]');
  const v = m?.getAttribute("content");
  // "__DRASHTA_TOKEN__" is the unreplaced placeholder (vite dev): fall back to /api/session.
  return v && v !== "__DRASHTA_TOKEN__" ? v : null;
}

/** Returns the CSRF token (meta tag first, then `/api/session`). */
export async function getToken(): Promise<string> {
  if (tokenCache) return tokenCache;
  const meta = readMetaToken();
  if (meta) {
    tokenCache = meta;
    return meta;
  }
  if (!tokenPromise) {
    tokenPromise = fetch(API_BASE + "/session", { headers: { Accept: "application/json" } })
      .then(async (r) => {
        if (!r.ok) throw await toError(r);
        const s = (await r.json()) as T.Session;
        tokenCache = s.token;
        return s.token;
      })
      .finally(() => {
        tokenPromise = null;
      });
  }
  return tokenPromise;
}

/**
 * Asks the server for its current token (after a restart it is a new one) and
 * keeps it. Many calls failing at once share one request. Returns null when the
 * server cannot be asked.
 */
let freshPromise: Promise<string | null> | null = null;
export function refreshToken(): Promise<string | null> {
  if (!freshPromise) {
    freshPromise = fetch(API_BASE + "/session", { headers: { Accept: "application/json" }, cache: "no-store" })
      .then(async (r) => {
        if (!r.ok) return null;
        const s = (await r.json()) as Partial<T.Session>;
        if (!s || typeof s.token !== "string" || !s.token) return null;
        tokenCache = s.token;
        return s.token;
      })
      .catch(() => null)
      .finally(() => {
        freshPromise = null;
      });
  }
  return freshPromise;
}

// ---------- reload once on 403 ----------
const RELOAD_FLAG = "drashta.reloadedFor403";
let reloadHook: () => void = () => window.location.reload();

/** Tests swap the reload function. */
export function setReloadHook(fn: () => void): void {
  reloadHook = fn;
}

function reloadOnce(): boolean {
  try {
    if (sessionStorage.getItem(RELOAD_FLAG)) return false;
    sessionStorage.setItem(RELOAD_FLAG, "1");
  } catch {
    // no storage: still reload once per page life
    if (reloadedThisPage) return false;
  }
  reloadedThisPage = true;
  reloadHook();
  return true;
}
let reloadedThisPage = false;

/** Called after a good mutating call so a later 403 may reload again. */
function clearReloadFlag(): void {
  try {
    sessionStorage.removeItem(RELOAD_FLAG);
  } catch {
    /* ignore */
  }
}

/** Test helper: forget the token and the reload guard. */
export function resetApiState(): void {
  tokenCache = null;
  tokenPromise = null;
  freshPromise = null;
  reloadedThisPage = false;
  try {
    sessionStorage.removeItem(RELOAD_FLAG);
  } catch {
    /* ignore */
  }
}

// ---------- core ----------
async function toError(r: Response): Promise<ApiError> {
  let code = "http_" + r.status;
  let message = r.statusText || "request failed";
  try {
    const body = (await r.json()) as Partial<T.ApiErrorBody>;
    if (body && body.error) {
      code = body.error.code || code;
      message = body.error.message || message;
    }
  } catch {
    /* not JSON */
  }
  return new ApiError(r.status, code, message);
}

export type Query = Record<string, string | number | boolean | null | undefined>;

export function qs(q?: Query): string {
  if (!q) return "";
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(q)) {
    if (v === undefined || v === null || v === "") continue;
    p.set(k, String(v));
  }
  const s = p.toString();
  return s ? "?" + s : "";
}

export interface RequestOptions {
  body?: unknown;
  form?: FormData;
  ifMatch?: string;
  signal?: AbortSignal;
}

/** Low-level call. `path` starts with `/` and is relative to `/api`. */
export async function request<R>(method: string, path: string, opts: RequestOptions = {}): Promise<R> {
  const mutating = method !== "GET" && method !== "HEAD";
  const headers: Record<string, string> = { Accept: "application/json" };
  let body: BodyInit | undefined;
  if (opts.form) {
    body = opts.form;
  } else if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(opts.body);
  } else if (mutating) {
    // The guard wants JSON + token on EVERY non-GET call, DELETE included.
    headers["Content-Type"] = "application/json";
    body = method === "DELETE" ? undefined : "{}";
  }
  if (opts.ifMatch) headers["If-Match"] = opts.ifMatch;
  const send = (token?: string) => {
    if (token !== undefined) headers["X-Drashta-Token"] = token;
    return fetch(API_BASE + path, { method, headers, body, signal: opts.signal });
  };
  const used = mutating ? await getToken() : undefined;
  let r = await send(used);
  if (r.status === 403 && mutating) {
    // Maybe the server restarted and has a new token: fetch it and retry once.
    const fresh = await refreshToken();
    if (fresh && fresh !== used) r = await send(fresh);
    // Reload only when the token could not be renewed, or the retry failed too.
    // The same token again means the 403 was about something else: no reload.
    if (r.status === 403 && (!fresh || fresh !== used)) reloadOnce();
  }
  if (!r.ok) throw await toError(r);
  if (mutating) clearReloadFlag();
  if (r.status === 204) return undefined as R;
  const ct = r.headers.get("Content-Type") || "";
  if (!ct.includes("json")) return (await r.text()) as unknown as R;
  return (await r.json()) as R;
}

/** GET a path relative to `/api` (used by the query cache). */
export function get<R>(path: string, signal?: AbortSignal): Promise<R> {
  return request<R>("GET", path, { signal });
}

const enc = encodeURIComponent;

/** One function per endpoint (04 §6). */
export const api = {
  // session + home
  session: () => get<T.Session>("/session"),
  boot: () => get<T.Boot>("/boot"),
  home: (range: 7 | 30) => get<T.Home>("/home" + qs({ range })),
  day: (date: string, project?: string) => get<T.DayResponse>("/day" + qs({ date, project })),
  inbox: (filter: "open" | "resolved" | "all" = "open", cursor?: string) =>
    get<T.InboxResponse>("/inbox" + qs({ filter, cursor })),

  // project
  project: (p: string) => get<T.ProjectDetail>("/projects/" + enc(p)),
  activity: (p: string, range: 7 | 30 = 30) => get<T.ActivityResponse>("/projects/" + enc(p) + "/activity" + qs({ range })),
  tree: (p: string, dir = "", depth = 2) => get<T.TreeResponse>("/projects/" + enc(p) + "/tree" + qs({ dir, depth })),
  file: (p: string, path: string, from = 1, to = 400) =>
    get<T.FileResponse>("/projects/" + enc(p) + "/file" + qs({ path, from, to })),
  rawUrl: (p: string, path: string) => API_BASE + "/projects/" + enc(p) + "/raw" + qs({ path }),
  /** The same bytes as rawUrl, with the path in the URL (each segment encoded), so a .gltf finds its .bin by relative URL. */
  fileUrl: (p: string, path: string) =>
    API_BASE + "/projects/" + enc(p) + "/files/" + path.split("/").map(encodeURIComponent).join("/"),
  dwgLayersUrl: (p: string, path: string) => API_BASE + "/projects/" + enc(p) + "/dwg/layers" + qs({ path }),
  dwgEntitiesUrl: (p: string, path: string) => API_BASE + "/projects/" + enc(p) + "/dwg/entities" + qs({ path }),
  commits: (p: string, o: { cursor?: string; limit?: number; path?: string } = {}) =>
    get<T.CommitsResponse>("/projects/" + enc(p) + "/commits" + qs({ limit: 50, ...o })),
  commit: (p: string, id: string) => get<T.CommitDetailResponse>("/projects/" + enc(p) + "/commits/" + enc(id)),
  dirty: (p: string) => get<T.DirtyResponse>("/projects/" + enc(p) + "/dirty"),
  sessions: (p: string, cursor?: string) => get<T.SessionsResponse>("/projects/" + enc(p) + "/sessions" + qs({ cursor })),
  sessionDetail: (id: string, o: { cursor?: string; limit?: number; roles?: string } = {}) =>
    get<T.SessionDetailResponse>("/sessions/" + enc(id) + qs({ limit: 40, ...o })),
  docs: (p: string) => get<T.DocsResponse>("/projects/" + enc(p) + "/docs"),
  github: (p: string) => get<T.GithubResponse>("/projects/" + enc(p) + "/github"),

  // servers
  servers: (project?: string) => get<T.ServersResponse>("/servers" + qs({ project })),
  killServer: (pid: number, key: string) => request<T.OkResponse>("POST", "/servers/kill", { body: { pid, key, confirm: true } }),
  restartServer: (key: string) => request<T.RestartResponse>("POST", "/servers/restart", { body: { key, confirm: true } }),

  // tracker
  tracker: (o: { project?: string; filter?: "all" | "review"; feature?: string; status?: string } = {}) =>
    get<T.TrackerResponse>("/tracker" + qs(o)),
  task: (p: string, id: string) => get<T.TaskDetailResponse>("/projects/" + enc(p) + "/tasks/" + enc(id)),
  createTask: (
    p: string,
    t: { title: string; featureId?: string | null; status?: T.TaskStatus; assignee?: string; body?: string; links?: string[] },
  ) => request<T.TaskWriteResponse>("POST", "/projects/" + enc(p) + "/tasks", { body: t }),
  updateTask: (
    p: string,
    id: string,
    rev: string,
    patch: { status?: T.TaskStatus; title?: string; featureId?: string | null; assignee?: string; body?: string; milestoneId?: string | null },
  ) => request<T.TaskWriteResponse>("PATCH", "/projects/" + enc(p) + "/tasks/" + enc(id), { body: patch, ifMatch: rev }),
  linkTask: (p: string, id: string, ref: string) =>
    request<T.LinksResponse>("POST", "/projects/" + enc(p) + "/tasks/" + enc(id) + "/links", { body: { ref } }),
  unlinkTask: (p: string, id: string, ref: string) =>
    request<T.LinksResponse>("DELETE", "/projects/" + enc(p) + "/tasks/" + enc(id) + "/links" + qs({ ref })),
  createFeature: (p: string, f: { title: string; summary?: string; milestoneId?: string | null }) =>
    request<{ feature: T.Feature; rev: string }>("POST", "/projects/" + enc(p) + "/features", { body: f }),
  updateFeature: (p: string, id: string, rev: string, f: Partial<T.Feature>) =>
    request<{ feature: T.Feature; rev: string }>("PATCH", "/projects/" + enc(p) + "/features/" + enc(id), { body: f, ifMatch: rev }),
  createMilestone: (p: string, m: { title: string; due?: string | null }) =>
    request<{ milestone: T.Milestone; rev: string }>("POST", "/projects/" + enc(p) + "/milestones", { body: m }),
  updateMilestone: (p: string, id: string, rev: string, m: Partial<T.Milestone>) =>
    request<{ milestone: T.Milestone; rev: string }>("PATCH", "/projects/" + enc(p) + "/milestones/" + enc(id), { body: m, ifMatch: rev }),
  deleteItem: (p: string, kind: "tasks" | "features" | "milestones", id: string) =>
    request<T.OkResponse>("DELETE", "/projects/" + enc(p) + "/" + kind + "/" + enc(id)),

  // snapshots
  snapshots: (p: string, taskId: string) => get<T.SnapshotsResponse>("/projects/" + enc(p) + "/tasks/" + enc(taskId) + "/snapshots"),
  createSnapshot: (p: string, taskId: string, src: { url?: string; path?: string; title?: string; note?: string } | FormData) =>
    request<T.SnapshotResponse>(
      "POST",
      "/projects/" + enc(p) + "/tasks/" + enc(taskId) + "/snapshots",
      src instanceof FormData ? { form: src } : { body: src },
    ),
  setVerdict: (p: string, taskId: string, version: number, verdict: T.Verdict, note?: string) =>
    request<T.VerdictResponse>("PUT", "/projects/" + enc(p) + "/tasks/" + enc(taskId) + "/snapshots/" + version + "/verdict", {
      body: { verdict, note },
    }),
  fileInboxSnapshot: (p: string, name: string, taskId: string) =>
    request<T.SnapshotResponse>("POST", "/projects/" + enc(p) + "/snapshots/inbox/" + enc(name) + "/file", { body: { taskId } }),

  // notes, refs, search, actions
  notes: (o: { ref?: string; project?: string; open?: boolean } = {}) =>
    get<T.NotesResponse>("/notes" + qs({ ref: o.ref, project: o.project, open: o.open ? 1 : undefined })),
  addNote: (n: { ref: string; text: string; line?: number; x?: number; y?: number; replyTo?: string }) =>
    request<T.NoteResponse>("POST", "/notes", { body: n }),
  updateNote: (p: string, id: string, patch: { resolved?: boolean; text?: string }) =>
    request<T.NoteResponse>("PATCH", "/projects/" + enc(p) + "/notes/" + enc(id), { body: patch }),
  resolve: (ref: string) => get<T.ResolveResponse>("/resolve" + qs({ ref })),
  search: (q: string, o: { project?: string; kind?: string; limit?: number } = {}, signal?: AbortSignal) =>
    get<T.SearchResponse>("/search" + qs({ q, limit: 30, ...o }), signal),
  refresh: (projectId?: string) => request<T.RefreshResponse>("POST", "/refresh", { body: projectId ? { projectId } : {} }),
  open: (ref: string, action?: T.OpenAction) => request<T.OpenResponse>("POST", "/open", { body: { ref, action } }),
  desktop: () => get<T.DesktopResponse>("/desktop"),
  setDesktop: (mode: T.DesktopMode) => request<T.DesktopResponse>("POST", "/desktop", { body: { mode } }),
  settings: () => get<T.Settings>("/settings"),
  updateSettings: (s: Partial<T.Settings>) => request<T.Settings>("PATCH", "/settings", { body: s }),

  // live updates
  state: (since: string) => get<T.StateResponse>("/state" + qs({ since })),
  /** `conn` is the connection id from the SSE `hello` event. */
  presence: (visible: boolean, conn?: string) =>
    request<T.OkResponse>("POST", "/presence", { body: conn ? { visible, conn } : { visible } }),
};

export type Api = typeof api;
