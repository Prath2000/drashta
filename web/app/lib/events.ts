// Live updates (03-architecture §6, 04-data-model §6 "Server-Sent Events").
//
// - One EventSource on `/api/events`. The browser resends `Last-Event-ID`
//   on its own reconnects. When the stream is closed for good and we open a
//   new one, the last id goes in `?lastEventId=` (EventSource cannot set headers).
// - On a reconnect the server replays the missed events itself (from
//   Last-Event-ID or ?lastEventId=), or sends `resync`. So no extra catch-up
//   runs then: it would apply the same events twice.
// - When the page becomes visible again we call `GET /api/state?since=<last id>`:
//   it returns the missed events, or `{resync:true}` and then every cached query
//   is refetched. Events are applied once: a replayed id that was already
//   applied is skipped.
// - The first hello of the page refetches what is on screen once: a change
//   between the first data fetch and the stream opening would be missed otherwise.
// - `resync` from the stream also refetches everything.
// - `hello` carries `conn` (the server-side connection id). After EVERY hello
//   (also after a reconnect) and on each visibility change we post
//   `/api/presence {visible, conn}`, so the server can pick its lsof cadence (S8).
// - Event ids are big numbers (server start time in microseconds), not 1,2,3.
//   We never compare them; after a server restart the server sends `resync`.
// - No timers for correctness. The only timer is the reconnect back-off. Only
//   one stream is open at a time: opening a new one closes the old one and
//   cancels a pending back-off timer.
// - Counts: once the stream has sent counts (hello or counts), a /boot answer
//   no longer overwrites them (it may be older). The last scan time only moves
//   forward.
// - A `scan` event with a projectId is one project's refresh: it does not end or
//   toast the global scan. Only events without a project (or scope "all") do.
import { api } from "./api";
import { invalidate } from "./query";
import { getState, scanDone, scanSawChange, scanStarted, setCounts, setLastScan, setOnline } from "./store";
import type { Counts, HelloEventData, LiveEvent, ScanEventData } from "./types";

export const EVENT_TYPES = [
  "hello",
  "counts",
  "project",
  "activity",
  "servers",
  "session",
  "tracker",
  "scan",
  "resync",
] as const;

export interface EventsOptions {
  url?: string;
  /** EventSource constructor (tests pass a fake). */
  EventSourceImpl?: typeof EventSource;
  doc?: Document;
  /** Called for every event after the built-in handling. */
  onEvent?: (e: LiveEvent) => void;
  /** Back-off before opening a new stream after it closed (ms). */
  retryMs?: number;
}

export interface EventsHandle {
  stop: () => void;
  lastId: () => string;
  /** Connection id from the last `hello`. */
  conn: () => string | undefined;
  /** Runs the visibility catch-up by hand (used by tests). */
  catchUp: () => Promise<void>;
}

// True once the stream has delivered counts; /boot counts are then ignored.
let streamCounts = false;

/** Test helper: forget what the stream has delivered. */
export function resetEventsState(): void {
  streamCounts = false;
}

/** Applies one live event to the caches and the store. Exported for tests and reuse. */
export function applyEvent(ev: LiveEvent): void {
  const d = (ev.data ?? {}) as Record<string, unknown>;
  const pid = (d.projectId ?? d.project) as string | undefined;
  switch (ev.type) {
    case "hello": {
      const h = d as unknown as HelloEventData;
      if (h.counts) {
        streamCounts = true;
        setCounts(h.counts);
      }
      break;
    }
    case "counts":
      streamCounts = true;
      setCounts(d as unknown as Counts);
      break;
    case "project": {
      // Any `what` (files, commits, dirty, sessions, docs, or "all" after
      // coalescing) refetches that project's cached data. "all" is covered too.
      scanSawChange();
      invalidate("/boot");
      invalidate("/inbox");
      if (pid) invalidate("/projects/" + encodeURIComponent(pid));
      else invalidate("/projects/");
      invalidate("/search");
      invalidate("/resolve");
      // Transcripts live under /sessions/<id>; the server has no per-session
      // event, so any sessions change refetches the open ones (and drops the rest).
      if (d.what === "sessions" || d.what === "all") invalidate("/sessions/");
      break;
    }
    case "activity":
      scanSawChange();
      invalidate("/inbox");
      invalidate("/home");
      invalidate("/day");
      if (pid) invalidate("/projects/" + encodeURIComponent(pid) + "/activity");
      break;
    case "servers":
      invalidate("/servers");
      invalidate("/home");
      if (pid) invalidate("/projects/" + encodeURIComponent(pid));
      break;
    case "session":
      invalidate("/inbox");
      invalidate("/home");
      invalidate("/day");
      if (pid) invalidate("/projects/" + encodeURIComponent(pid) + "/sessions");
      break;
    case "tracker":
      invalidate("/tracker");
      invalidate("/inbox");
      invalidate("/notes");
      invalidate("/resolve");
      invalidate("/home");
      invalidate("/day");
      if (pid) invalidate("/projects/" + encodeURIComponent(pid) + "/tasks");
      break;
    case "scan": {
      const s = d as unknown as ScanEventData;
      // A per-project refresh is not the global scan: it must not end the
      // running state or make the "All projects rescanned" toast.
      if (s.projectId && s.scope !== "all") break;
      if (s.state === "running") scanStarted(false);
      else {
        scanDone(s.at ?? new Date().toISOString());
      }
      break;
    }
    case "resync":
      invalidate();
      break;
  }
}

/** Starts the live stream. Call once (root). Returns a handle with stop(). */
export function startEvents(opts: EventsOptions = {}): EventsHandle {
  const url = opts.url ?? "/api/events";
  const ES = opts.EventSourceImpl ?? (typeof EventSource !== "undefined" ? EventSource : undefined);
  const doc = opts.doc ?? document;
  const retryMs = opts.retryMs ?? 3000;
  let lastId = "";
  let es: EventSource | null = null;
  let stopped = false;
  let retry: ReturnType<typeof setTimeout> | null = null;
  let catching: Promise<void> | null = null;
  let conn: string | undefined;
  let firstHello = true;
  // Recently applied events (type + id), so a replay is not applied twice.
  const seen = new Set<string>();
  const SEEN_MAX = 512;
  const fresh = (ev: LiveEvent): boolean => {
    if (!ev.id || ev.type === "hello") return true;
    const k = ev.type + ":" + ev.id;
    if (seen.has(k)) return false;
    seen.add(k);
    if (seen.size > SEEN_MAX) seen.delete(seen.values().next().value as string);
    return true;
  };
  const visibleNow = () => doc.visibilityState === "visible";
  const sendPresence = () => {
    api.presence(visibleNow(), conn).catch(() => {});
  };

  const handle = (type: string, e: MessageEvent) => {
    if (e.lastEventId) lastId = e.lastEventId;
    let data: unknown = {};
    try {
      data = e.data ? JSON.parse(e.data) : {};
    } catch {
      data = {};
    }
    const ev: LiveEvent = { id: e.lastEventId || undefined, type, data };
    if (!fresh(ev)) return;
    applyEvent(ev);
    if (type === "hello") {
      // every hello (first connect and each reconnect) is a new server-side
      // connection: tell it whether this page is visible
      const c = (data as { conn?: string }).conn;
      if (c) conn = c;
      sendPresence();
      if (firstHello) {
        // first-load catch-up: data fetched before the stream opened may be old
        firstHello = false;
        invalidate();
      }
    }
    opts.onEvent?.(ev);
  };

  const catchUp = (): Promise<void> => {
    if (catching) return catching;
    if (!lastId) {
      // nothing seen yet: just refetch what is on screen
      invalidate();
      return Promise.resolve();
    }
    catching = api
      .state(lastId)
      .then((r) => {
        if (r.resync) {
          invalidate();
          return;
        }
        for (const ev of r.events ?? []) {
          if (ev.id) lastId = ev.id;
          if (!fresh(ev)) continue;
          applyEvent(ev);
          opts.onEvent?.(ev);
        }
      })
      .catch(() => {
        invalidate();
      })
      .finally(() => {
        catching = null;
      });
    return catching;
  };

  const open = () => {
    if (stopped || !ES) return;
    // Exactly one stream: cancel a pending back-off and close the old stream.
    if (retry) {
      clearTimeout(retry);
      retry = null;
    }
    if (es) {
      es.close();
      es = null;
    }
    const u = lastId ? url + (url.includes("?") ? "&" : "?") + "lastEventId=" + encodeURIComponent(lastId) : url;
    const src = new ES(u);
    es = src;
    src.onopen = () => {
      if (es !== src) return;
      // The server replays what we missed (or sends resync): no catch-up here.
      setOnline(true);
    };
    src.onerror = () => {
      if (es !== src) return; // an old stream we already replaced
      setOnline(false);
      if (src.readyState === 2 /* CLOSED */) {
        src.close();
        if (es === src) es = null;
        if (!stopped && !retry) {
          retry = setTimeout(() => {
            retry = null;
            open();
          }, retryMs);
        }
      }
    };
    src.onmessage = (e) => {
      if (es === src) handle("message", e);
    };
    for (const t of EVENT_TYPES)
      src.addEventListener(t, (e) => {
        if (es === src) handle(t, e as MessageEvent);
      });
  };

  const onVis = () => {
    const visible = visibleNow();
    sendPresence();
    if (visible) {
      if (!es) open();
      void catchUp();
    }
  };

  doc.addEventListener("visibilitychange", onVis);
  open();

  return {
    stop: () => {
      stopped = true;
      if (retry) clearTimeout(retry);
      es?.close();
      es = null;
      doc.removeEventListener("visibilitychange", onVis);
    },
    lastId: () => lastId,
    conn: () => conn,
    catchUp,
  };
}

const ms = (t: string | null | undefined) => {
  const v = t ? Date.parse(t) : NaN;
  return Number.isNaN(v) ? -Infinity : v;
};

/**
 * Reads boot data into the store (counts and last scan time). Counts are taken
 * only until the stream has sent its own (a /boot answer may be older), and the
 * last scan time only ever moves forward.
 */
export function applyBoot(b: { counts: Counts; lastScanAt?: string | null; now?: string }): void {
  if (!streamCounts && b.counts) setCounts(b.counts);
  const cur = getState().scan.at;
  // boot.now stands in only while nothing better is known
  const at = b.lastScanAt ?? (cur ? null : b.now ?? null);
  if (at && ms(at) > ms(cur)) setLastScan(at);
}
