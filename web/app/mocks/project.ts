// Extra MSW handlers for the project page (owned by the project screen agent).
// The shared handlers in ./handlers.ts already answer every endpoint; these
// are per-test overrides: `server.use(...githubUnavailable("gh not logged in"))`.
import { http, HttpResponse, type HttpHandler } from "msw";
import type * as T from "~/lib/types";

const API = "*/api";

/** GET /projects/:p/github answers `unavailable` (gh missing or logged out). */
export function githubUnavailable(reason = "gh not logged in"): HttpHandler[] {
  return [http.get(API + "/projects/:p/github", () => HttpResponse.json({ unavailable: { reason } }))];
}

/** GET /projects/:p/github answers with PRs, issues and one CI run. */
export function githubWith(data?: Partial<T.GithubResponse>): HttpHandler[] {
  const body: T.GithubResponse = {
    prs: [{ number: 12, title: "Lazy file tree for big repos", state: "OPEN", url: "https://github.com/x/y/pull/12", updatedAt: "2026-09-30T10:00:00Z" }],
    issues: [{ number: 7, title: "Seismograph overflows at 7d", state: "OPEN", url: "https://github.com/x/y/issues/7" }],
    runs: [{ id: 99, name: "ci / test", status: "completed", conclusion: "success", url: "https://github.com/x/y/actions/runs/99" }],
    fetchedAt: "2026-09-30T21:00:00Z",
    ...data,
  };
  return [http.get(API + "/projects/:p/github", () => HttpResponse.json(body))];
}

/** GET /projects/:p/github fails with a server error. */
export function githubBroken(): HttpHandler[] {
  return [http.get(API + "/projects/:p/github", () => HttpResponse.json({ error: { code: "gh_failed", message: "gh failed" } }, { status: 500 }))];
}

/** Sessions endpoint with two pages and a "mention" session on page 2. */
export function sessionsPaged(project: string, first: T.SessionSummary[], second: T.SessionSummary[]): HttpHandler[] {
  return [
    http.get(API + "/projects/:p/sessions", ({ params, request }) => {
      if (params.p !== project) return undefined;
      const cursor = new URL(request.url).searchParams.get("cursor");
      return HttpResponse.json<T.SessionsResponse>(cursor === "p2" ? { sessions: second, next: null } : { sessions: first, next: "p2" });
    }),
  ];
}

/** A made-up session summary for tests. */
export function fakeSession(id: string, over: Partial<T.SessionSummary> = {}): T.SessionSummary {
  return {
    id,
    profile: ".claude",
    start: "2026-09-29T10:00:00Z",
    end: "2026-09-29T10:40:00Z",
    title: "session " + id,
    turns: 4,
    kb: 12,
    tools: { Read: 2 },
    edited: [],
    tasks: [],
    reason: "cwd",
    ...over,
  };
}
