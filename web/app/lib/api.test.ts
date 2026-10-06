import { http, HttpResponse } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MOCK_TOKEN } from "~/mocks/handlers";
import { server } from "~/mocks/node";
import { api, ApiError, request, setReloadHook } from "./api";

function capture(method: "post" | "delete" | "patch", path: string, reply: () => Response) {
  const seen: { token: string | null; ct: string | null }[] = [];
  server.use(
    http[method]("*/api" + path, ({ request }) => {
      seen.push({ token: request.headers.get("X-Drashta-Token"), ct: request.headers.get("Content-Type") });
      return reply();
    }),
  );
  return seen;
}

afterEach(() => {
  document.head.querySelectorAll('meta[name="drashta-token"]').forEach((m) => m.remove());
  setReloadHook(() => window.location.reload());
});

describe("api client", () => {
  it("GETs JSON without a token", async () => {
    const boot = await api.boot();
    expect(boot.projects.length).toBeGreaterThan(0);
    expect(boot.counts).toHaveProperty("openNotes");
  });

  it("sends the token from /api/session on mutating calls", async () => {
    const seen = capture("post", "/refresh", () => HttpResponse.json({ started: true }));
    await api.refresh();
    expect(seen[0]).toEqual({ token: MOCK_TOKEN, ct: "application/json" });
  });

  it("prefers the <meta> token and ignores the unreplaced placeholder", async () => {
    const m = document.createElement("meta");
    m.name = "drashta-token";
    m.content = "__DRASHTA_TOKEN__";
    document.head.appendChild(m);
    const seen = capture("post", "/refresh", () => HttpResponse.json({ started: true }));
    await api.refresh();
    expect(seen[0].token).toBe(MOCK_TOKEN); // fell back to /api/session

    const { resetApiState } = await import("./api");
    resetApiState();
    m.content = "from-meta";
    await api.refresh();
    expect(seen[1].token).toBe("from-meta");
  });

  it("sends JSON content type and token on a DELETE with no body", async () => {
    const seen = capture("delete", "/projects/:p/tasks/:id/links", () => HttpResponse.json({ links: [] }));
    await api.unlinkTask("notes-app", "T-1", "notes-app:commit:b7dc920bef");
    expect(seen[0]).toEqual({ token: MOCK_TOKEN, ct: "application/json" });
  });

  it("parses {error:{code,message}} into an ApiError", async () => {
    const e = await api.resolve("acme-api:file:no/such/file.go").catch((x) => x);
    expect(e).toBeInstanceOf(ApiError);
    expect(e.status).toBe(404);
    expect(e.code).toBe("not_found");
    expect(e.message).toMatch(/not found/);
  });

  it("falls back to the HTTP status when the body is not JSON", async () => {
    server.use(http.get("*/api/boot", () => new HttpResponse("boom", { status: 500 })));
    const e = await api.boot().catch((x) => x);
    expect(e).toBeInstanceOf(ApiError);
    expect(e.code).toBe("http_500");
  });

  it("on a 403 fetches the new token from /api/session and retries once (no reload)", async () => {
    // Review I12: after a server restart the token in the page is old.
    const reload = vi.fn();
    setReloadHook(reload);
    const { resetApiState } = await import("./api");
    resetApiState();
    const m = document.createElement("meta");
    m.name = "drashta-token";
    m.content = "old-token";
    document.head.appendChild(m);
    server.use(http.get("*/api/session", () => HttpResponse.json({ token: "new-token" })));
    const seen = capture("post", "/refresh", () => HttpResponse.json({ started: true }));
    server.use(
      http.post("*/api/refresh", ({ request }) => {
        seen.push({ token: request.headers.get("X-Drashta-Token"), ct: request.headers.get("Content-Type") });
        return request.headers.get("X-Drashta-Token") === "new-token"
          ? HttpResponse.json({ started: true })
          : HttpResponse.json({ error: { code: "forbidden", message: "missing or wrong token" } }, { status: 403 });
      }),
    );
    await expect(api.refresh()).resolves.toEqual({ started: true });
    expect(seen.map((x) => x.token)).toEqual(["old-token", "new-token"]);
    // later calls use the new token at once
    await api.refresh();
    expect(seen.map((x) => x.token)).toEqual(["old-token", "new-token", "new-token"]);
    expect(reload).not.toHaveBeenCalled();
    resetApiState();
  });

  it("a presence 403 renews the token instead of reloading", async () => {
    const reload = vi.fn();
    setReloadHook(reload);
    const { resetApiState } = await import("./api");
    resetApiState();
    let current = "t1";
    const tokens: (string | null)[] = [];
    server.use(
      http.get("*/api/session", () => HttpResponse.json({ token: current })),
      http.post("*/api/presence", ({ request }) => {
        tokens.push(request.headers.get("X-Drashta-Token"));
        return request.headers.get("X-Drashta-Token") === current
          ? HttpResponse.json({ ok: true })
          : HttpResponse.json({ error: { code: "forbidden", message: "missing or wrong token" } }, { status: 403 });
      }),
    );
    await api.presence(true, "c1");
    current = "t2"; // the server restarted
    await expect(api.presence(true, "c2")).resolves.toEqual({ ok: true });
    expect(tokens).toEqual(["t1", "t1", "t2"]);
    expect(reload).not.toHaveBeenCalled();
    resetApiState();
  });

  it("reloads once only when the retry also fails", async () => {
    const reload = vi.fn();
    setReloadHook(reload);
    const { resetApiState } = await import("./api");
    resetApiState();
    let n = 0;
    server.use(http.get("*/api/session", () => HttpResponse.json({ token: "tok-" + ++n })));
    capture("post", "/refresh", () => HttpResponse.json({ error: { code: "bad_token", message: "stale token" } }, { status: 403 }));
    await expect(api.refresh()).rejects.toMatchObject({ status: 403, code: "bad_token" });
    await expect(api.refresh()).rejects.toBeInstanceOf(ApiError);
    expect(reload).toHaveBeenCalledTimes(1);
    resetApiState();
  });

  it("reloads once when /api/session cannot be read", async () => {
    const reload = vi.fn();
    setReloadHook(reload);
    const { resetApiState } = await import("./api");
    resetApiState();
    const m = document.createElement("meta");
    m.name = "drashta-token";
    m.content = "old-token";
    document.head.appendChild(m);
    server.use(http.get("*/api/session", () => HttpResponse.json({ error: { code: "forbidden", message: "no" } }, { status: 403 })));
    capture("post", "/refresh", () => HttpResponse.json({ error: { code: "bad_token", message: "stale token" } }, { status: 403 }));
    await expect(api.refresh()).rejects.toMatchObject({ status: 403 });
    expect(reload).toHaveBeenCalledTimes(1);
    resetApiState();
  });

  it("does not reload on a 403 that is not about the token (same token again)", async () => {
    const reload = vi.fn();
    setReloadHook(reload);
    const { resetApiState } = await import("./api");
    resetApiState();
    const seen = capture("post", "/refresh", () => HttpResponse.json({ error: { code: "forbidden", message: "outside" } }, { status: 403 }));
    await expect(api.refresh()).rejects.toMatchObject({ status: 403 });
    expect(seen.length).toBe(1); // the token did not change: no retry
    expect(reload).not.toHaveBeenCalled();
    resetApiState();
  });

  it("does not reload on a 403 from a GET", async () => {
    const reload = vi.fn();
    setReloadHook(reload);
    server.use(http.get("*/api/boot", () => HttpResponse.json({ error: { code: "forbidden", message: "no" } }, { status: 403 })));
    await expect(request("GET", "/boot")).rejects.toBeInstanceOf(ApiError);
    expect(reload).not.toHaveBeenCalled();
  });

  it("sends If-Match on task updates", async () => {
    let ifMatch: string | null = null;
    server.use(
      http.patch("*/api/projects/:p/tasks/:id", ({ request }) => {
        ifMatch = request.headers.get("If-Match");
        return HttpResponse.json({ task: {}, rev: "r2" });
      }),
    );
    await api.updateTask("notes-app", "T-1", "r1", { status: "done" });
    expect(ifMatch).toBe("r1");
  });
});

describe("file urls", () => {
  it("fileUrl puts the path in the URL, each segment encoded", () => {
    expect(api.fileUrl("a b", "x/y #1.glb")).toBe("/api/projects/a%20b/files/x/y%20%231.glb");
    expect(api.fileUrl("alpha", "models/robot.glb")).toBe("/api/projects/alpha/files/models/robot.glb");
    expect(api.fileUrl("alpha", "a?b/c%d.stl")).toBe("/api/projects/alpha/files/a%3Fb/c%25d.stl");
  });

  it("dwgLayersUrl and dwgEntitiesUrl carry the path as a query value", () => {
    expect(api.dwgLayersUrl("a b", "plans/floor 1.dwg")).toBe("/api/projects/a%20b/dwg/layers?path=plans%2Ffloor+1.dwg");
    expect(api.dwgEntitiesUrl("alpha", "plans/f#1.dwg")).toBe("/api/projects/alpha/dwg/entities?path=plans%2Ff%231.dwg");
  });
});
