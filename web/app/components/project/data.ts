// Data for the project page: API keys, thin typed wrappers and two small hooks.
// Everything goes through ~/lib/api and ~/lib/query (owned by the shell agent).
import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "~/lib/api";
import { invalidate, useApi } from "~/lib/query";
import { toast } from "~/lib/store";
import type { GithubResponse, OpenAction, Server } from "~/lib/types";

const enc = encodeURIComponent;

/** Cache keys (paths relative to /api). SSE invalidates by these prefixes. */
export const keys = {
  project: (p: string) => "/projects/" + enc(p),
  activity: (p: string, range: 7 | 30) => "/projects/" + enc(p) + "/activity?range=" + range,
  day: (p: string, date: string) => "/day?date=" + enc(date) + "&project=" + enc(p),
  tree: (p: string, dir: string) => "/projects/" + enc(p) + "/tree?dir=" + enc(dir) + "&depth=1",
  /** One page of up to COMMIT_PAGE commits (the prototype caps the list at 200). */
  commits: (p: string) => "/projects/" + enc(p) + "/commits?limit=" + COMMIT_PAGE,
  dirty: (p: string) => "/projects/" + enc(p) + "/dirty",
  sessions: (p: string) => "/projects/" + enc(p) + "/sessions",
  docs: (p: string) => "/projects/" + enc(p) + "/docs",
  github: (p: string) => "/projects/" + enc(p) + "/github",
  servers: (p: string) => "/servers?project=" + enc(p),
  tracker: (p: string) => "/tracker?project=" + enc(p),
  notes: (p: string) => "/notes?project=" + enc(p) + "&open=1",
  settings: () => "/settings",
  fileSearch: (p: string, q: string) => "/search?q=" + enc(q) + "&project=" + enc(p) + "&kind=file&limit=60",
};

export const COMMIT_PAGE = 200;
/** Lists longer than this show a "show all N" button (as the prototype does). */
export const CAP = 200;

/**
 * GET /projects/{p}/github. When `gh` is missing or logged out the server
 * answers `unavailable` (03-architecture §gh); the page shows a muted note.
 * lib/types.ts has no `unavailable` field yet, so it is added here.
 */
export type GithubData = Partial<GithubResponse> & { unavailable?: { reason: string } | string | null };

export function githubReason(g: GithubData | undefined): string | null {
  if (!g || !g.unavailable) return null;
  return typeof g.unavailable === "string" ? g.unavailable : g.unavailable.reason || "unavailable";
}

/** Ref strings in the full form `<project>:<type>:<id>`. */
export const ref = (p: string, type: string, id: string | number) => p + ":" + type + ":" + id;
export const projectRef = (p: string) => ref(p, "project", p);

export function errText(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return "request failed";
}

/** POST /open for a ref. The page never sends a path or a command (D4). */
export async function runNative(r: string, action?: OpenAction): Promise<void> {
  try {
    const res = await api.open(r, action);
    toast(res.ran ? "ran: " + res.ran : "opened");
  } catch (e) {
    toast(errText(e), { error: true });
  }
}

/** After a server kill/restart: refetch every view that shows servers. */
export function invalidateServers(p: string): void {
  invalidate("/servers");
  invalidate(keys.project(p));
}

export async function killServer(p: string, s: Server): Promise<void> {
  try {
    await api.killServer(s.pid, s.key);
    toast("stopped :" + s.port);
  } catch (e) {
    toast(errText(e), { error: true });
  }
  invalidateServers(p);
}

export async function restartServer(p: string, s: Server): Promise<void> {
  try {
    const r = await api.restartServer(s.key);
    toast("starting :" + s.port + (r.log ? " · log " + r.log : ""));
  } catch (e) {
    toast(errText(e), { error: true });
  }
  invalidateServers(p);
}

/**
 * A list read page by page with a `next` cursor. The first page comes from the
 * shared cache (so SSE refreshes it); `loadAll()` fetches the remaining pages.
 * When the first page changes, the extra pages are dropped.
 */
export function usePaged<R, I>(
  key: string | null,
  pick: (r: R) => { items: I[]; next: string | null },
  more: (cursor: string) => Promise<R>,
) {
  const first = useApi<R>(key);
  const [extra, setExtra] = useState<{ src: R | undefined; items: I[]; next: string | null; busy: boolean; error: unknown }>({
    src: undefined,
    items: [],
    next: null,
    busy: false,
    error: null,
  });
  const page = first.data ? pick(first.data) : null;
  const stale = extra.src !== first.data;
  const items = page ? (stale ? page.items : page.items.concat(extra.items)) : [];
  const next = page ? (stale ? page.next : extra.next) : null;

  useEffect(() => {
    if (stale) setExtra({ src: first.data, items: [], next: page ? page.next : null, busy: false, error: null });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [first.data]);

  const loadAll = useCallback(async () => {
    if (!first.data) return;
    const src = first.data;
    let cursor = stale ? pick(src).next : extra.next;
    let acc = stale ? [] : extra.items.slice();
    setExtra({ src, items: acc, next: cursor, busy: true, error: null });
    try {
      while (cursor) {
        const r = await more(cursor);
        const pg = pick(r);
        acc = acc.concat(pg.items);
        cursor = pg.next;
        setExtra({ src, items: acc, next: cursor, busy: !!cursor, error: null });
      }
    } catch (e) {
      setExtra({ src, items: acc, next: cursor, busy: false, error: e });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [first.data, extra, stale]);

  return {
    items,
    next,
    loading: first.loading && !first.data,
    error: first.error ?? (stale ? null : extra.error),
    busy: !stale && extra.busy,
    loadAll,
  };
}
