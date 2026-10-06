import { renderHook, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { server } from "~/mocks/node";
import { FakeEventSource } from "../../test/fake-eventsource";
import { applyBoot, resetEventsState, startEvents, type EventsHandle } from "./events";
import { useApi } from "./query";
import { getState, scanStarted, setLastScan } from "./store";

// Records every API request path (with query) that MSW sees.
function trackRequests() {
  const seen: string[] = [];
  const on = ({ request }: { request: Request }) => {
    const u = new URL(request.url);
    seen.push(request.method + " " + u.pathname + u.search);
  };
  server.events.on("request:start", on);
  return { seen, off: () => server.events.removeListener("request:start", on) };
}

function setVisibility(v: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => v });
  document.dispatchEvent(new Event("visibilitychange"));
}

let h: EventsHandle | null = null;
let track: ReturnType<typeof trackRequests>;
const start = (o: Parameters<typeof startEvents>[0] = {}) => {
  h = startEvents({ EventSourceImpl: FakeEventSource as unknown as typeof EventSource, retryMs: 5, ...o });
  return h;
};

beforeEach(() => {
  resetEventsState();
  FakeEventSource.reset();
  track = trackRequests();
  setVisibility("visible");
});
afterEach(() => {
  h?.stop();
  h = null;
  track.off();
  vi.useRealTimers();
});

describe("live events", () => {
  it("opens /api/events and applies hello counts", () => {
    start();
    const es = FakeEventSource.last();
    expect(es.url).toBe("/api/events");
    es.open();
    es.emit("hello", { now: "2026-10-01T00:00:00Z", counts: { liveServers: 3, openTasks: 4, awaitingReview: 1, openNotes: 2 }, conn: "c1" }, "1727740000000001");
    expect(getState().counts.liveServers).toBe(3);
    expect(h!.lastId()).toBe("1727740000000001");
    expect(h!.conn()).toBe("c1");
  });

  it("posts presence {visible, conn} after every hello and on visibility changes", async () => {
    const bodies: unknown[] = [];
    server.use(
      http.post("*/api/presence", async ({ request }) => {
        bodies.push(await request.json());
        return HttpResponse.json({ ok: true });
      }),
    );
    start();
    const es = FakeEventSource.last();
    es.open();
    es.emit("hello", { now: "x", counts: getState().counts, conn: "c1" }, "100");
    await waitFor(() => expect(bodies).toContainEqual({ visible: true, conn: "c1" }));
    setVisibility("hidden");
    await waitFor(() => expect(bodies).toContainEqual({ visible: false, conn: "c1" }));
    // reconnect: a new hello with a new conn re-sends the current visibility
    es.drop();
    es.open();
    es.emit("hello", { now: "x", counts: getState().counts, conn: "c2" }, "200");
    await waitFor(() => expect(bodies).toContainEqual({ visible: false, conn: "c2" }));
  });

  it("does not call /api/state after a reconnect: the server replays, and a replayed id is applied once", async () => {
    // Review M17(a): the stream replays from Last-Event-ID, so an extra
    // catch-up applied the same events twice.
    const seenEvents: string[] = [];
    start({ onEvent: (e) => seenEvents.push(e.type + ":" + (e.id ?? "")) });
    const es = FakeEventSource.last();
    es.open();
    es.emit("counts", { liveServers: 1, openTasks: 1, awaitingReview: 0, openNotes: 0 }, "1727740000000042");
    es.drop();
    expect(getState().online).toBe(false);
    es.open();
    expect(getState().online).toBe(true);
    // the browser reconnects with Last-Event-ID; the server replays 42 (seen) and 43 (new)
    es.emit("counts", { liveServers: 1, openTasks: 1, awaitingReview: 0, openNotes: 0 }, "1727740000000042");
    es.emit("counts", { liveServers: 9, openTasks: 1, awaitingReview: 0, openNotes: 0 }, "1727740000000043");
    expect(getState().counts.liveServers).toBe(9);
    expect(seenEvents).toEqual(["counts:1727740000000042", "counts:1727740000000043"]);
    expect(h!.lastId()).toBe("1727740000000043");
    await new Promise((r) => setTimeout(r, 20));
    expect(track.seen.some((x) => x.startsWith("GET /api/state"))).toBe(false);
  });

  it("the visibility catch-up skips events the stream already applied", async () => {
    const seenEvents: string[] = [];
    start({ onEvent: (e) => seenEvents.push(e.type + ":" + (e.id ?? "")) });
    const es = FakeEventSource.last();
    es.open();
    es.emit("counts", getState().counts, "50");
    server.use(
      http.get("*/api/state", () =>
        HttpResponse.json({
          resync: false,
          head: 51,
          events: [
            { id: "50", type: "counts", data: getState().counts },
            { id: "51", type: "counts", data: { liveServers: 4, openTasks: 0, awaitingReview: 0, openNotes: 0 } },
          ],
        }),
      ),
    );
    await h!.catchUp();
    expect(seenEvents).toEqual(["counts:50", "counts:51"]);
    expect(getState().counts.liveServers).toBe(4);
  });

  it("reopens a closed stream with ?lastEventId=", async () => {
    start();
    const es = FakeEventSource.last();
    es.open();
    es.emit("counts", getState().counts, "555");
    es.fail();
    await waitFor(() => expect(FakeEventSource.instances.length).toBe(2));
    expect(FakeEventSource.last().url).toBe("/api/events?lastEventId=555");
  });

  it("calls /api/state when the page becomes visible again", async () => {
    start();
    const es = FakeEventSource.last();
    es.open();
    es.emit("counts", getState().counts, "77");
    setVisibility("hidden");
    setVisibility("visible");
    await waitFor(() => expect(track.seen).toContain("GET /api/state?since=77"));
  });

  it("refetches everything on resync (stream event or state answer)", async () => {
    const { result } = renderHook(() => useApi("/boot"));
    await waitFor(() => expect(result.current.data).toBeTruthy());
    const before = track.seen.filter((s) => s === "GET /api/boot").length;
    start();
    const es = FakeEventSource.last();
    es.open();
    es.emit("resync", {}, "9");
    await waitFor(() => expect(track.seen.filter((s) => s === "GET /api/boot").length).toBe(before + 1));
    // the mock /api/state always answers {resync:true}
    setVisibility("visible");
    await waitFor(() => expect(track.seen.filter((s) => s === "GET /api/boot").length).toBe(before + 2));
  });

  it("refetches the whole project on a coalesced project event (what:'all')", async () => {
    const a = renderHook(() => useApi("/projects/acme-api"));
    const b = renderHook(() => useApi("/projects/acme-api/commits?limit=50"));
    await waitFor(() => expect(a.result.current.data && b.result.current.data).toBeTruthy());
    const n = () => track.seen.filter((s) => s.startsWith("GET /api/projects/acme-api")).length;
    const before = n();
    start();
    FakeEventSource.last().open();
    FakeEventSource.last().emit("project", { projectId: "acme-api", what: "all", parts: ["files", "commits"] }, "10");
    await waitFor(() => expect(n()).toBe(before + 2));
  });

  it("tracks scan state from scan events", () => {
    start();
    const es = FakeEventSource.last();
    es.open();
    es.emit("scan", { state: "running" }, "1");
    expect(getState().scan.running).toBe(true);
    es.emit("scan", { state: "done", at: "2026-10-01T09:00:00Z" }, "2");
    expect(getState().scan.running).toBe(false);
    expect(getState().scan.at).toBe("2026-10-01T09:00:00Z");
  });

  it("the first hello refetches what is on screen once (first-load catch-up)", async () => {
    const { result } = renderHook(() => useApi("/boot"));
    await waitFor(() => expect(result.current.data).toBeTruthy());
    const n = () => track.seen.filter((x) => x === "GET /api/boot").length;
    const before = n();
    start();
    const es = FakeEventSource.last();
    es.open();
    es.emit("hello", { now: "x", counts: getState().counts, conn: "c1" }, "100");
    await waitFor(() => expect(n()).toBe(before + 1));
    // a reconnect's hello does not refetch again
    es.drop();
    es.open();
    es.emit("hello", { now: "x", counts: getState().counts, conn: "c2" });
    await new Promise((r) => setTimeout(r, 20));
    expect(n()).toBe(before + 1);
  });

  it("error, then visible within the back-off: exactly one open stream (I13)", async () => {
    start({ retryMs: 40 });
    const first = FakeEventSource.last();
    first.open();
    first.fail(); // CLOSED: a back-off timer is pending
    setVisibility("hidden");
    setVisibility("visible"); // opens a stream now
    await new Promise((r) => setTimeout(r, 80)); // the old timer would have fired by now
    const open = FakeEventSource.instances.filter((x) => x.readyState !== 2);
    expect(FakeEventSource.instances.length).toBe(2);
    expect(open.length).toBe(1);
    h!.stop();
    expect(FakeEventSource.instances.every((x) => x.readyState === 2)).toBe(true);
  });

  it("a per-project scan event does not end or toast the global scan (I14)", () => {
    start();
    const es = FakeEventSource.last();
    es.open();
    scanStarted(true); // the user pressed rescan
    es.emit("scan", { projectId: "acme-api", state: "running" }, "1");
    es.emit("scan", { projectId: "acme-api", state: "done", at: "2026-10-01T09:00:00Z" }, "2");
    expect(getState().scan.running).toBe(true);
    expect(getState().toast).toBeNull();
    es.emit("scan", { state: "done", at: "2026-10-01T09:05:00Z" }, "3");
    expect(getState().scan.running).toBe(false);
    expect(getState().toast?.msg).toMatch(/All projects rescanned/);
    expect(getState().scan.at).toBe("2026-10-01T09:05:00Z");
  });

  it("a sessions change refetches open transcripts and drops closed ones (I15)", async () => {
    let turns = 1;
    server.use(
      http.get("*/api/sessions/:id", () =>
        HttpResponse.json({ session: { id: "s1" }, turns: Array.from({ length: turns }, (_, i) => ({ i, role: "user", text: "t" + i })), next: null }),
      ),
    );
    const { result } = renderHook(() => useApi<{ turns: unknown[] }>("/sessions/s1?limit=40"));
    await waitFor(() => expect(result.current.data?.turns.length).toBe(1));
    turns = 3; // the transcript grows
    start();
    FakeEventSource.last().open();
    FakeEventSource.last().emit("project", { projectId: "acme-api", what: "sessions" }, "20");
    await waitFor(() => expect(result.current.data?.turns.length).toBe(3));
  });

  it("a late /boot does not overwrite stream counts, and last scan only moves forward (M16)", () => {
    start();
    const es = FakeEventSource.last();
    es.open();
    es.emit("counts", { liveServers: 5, openTasks: 2, awaitingReview: 1, openNotes: 0 }, "1");
    applyBoot({ counts: { liveServers: 1, openTasks: 1, awaitingReview: 0, openNotes: 0 }, lastScanAt: "2026-10-01T09:00:00Z" });
    expect(getState().counts.liveServers).toBe(5);
    expect(getState().scan.at).toBe("2026-10-01T09:00:00Z");
    setLastScan("2026-10-01T10:00:00Z");
    applyBoot({ counts: getState().counts, lastScanAt: "2026-10-01T09:30:00Z", now: "2026-10-01T10:30:00Z" });
    expect(getState().scan.at).toBe("2026-10-01T10:00:00Z");
  });

  it("boot counts apply until the stream has sent its own", () => {
    applyBoot({ counts: { liveServers: 7, openTasks: 0, awaitingReview: 0, openNotes: 0 }, now: "2026-10-01T08:00:00Z" });
    expect(getState().counts.liveServers).toBe(7);
    expect(getState().scan.at).toBe("2026-10-01T08:00:00Z");
  });
});
