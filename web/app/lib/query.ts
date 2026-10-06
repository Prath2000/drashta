// A very small fetch cache. The key is the API path (for example "/home?range=30").
//
//   const { data, error, loading } = useApi<Home>("/home?range=30");
//   invalidate("/projects/acme-api");   // refetch every cached key that starts with this
//   invalidate();                     // refetch everything (SSE `resync`)
//
// Keys that a mounted component uses are refetched at once; the others are dropped.
// No timers, no polling: data changes only on mount, on invalidate, or on reload().
import { useCallback, useEffect, useSyncExternalStore } from "react";
import { get } from "./api";

interface Entry {
  data: unknown;
  error: unknown;
  loading: boolean;
  version: number;
  subs: number;
  fetcher: () => Promise<unknown>;
  ctrl: AbortController | null;
  snapshot: Snap<unknown>;
}

export interface Snap<D> {
  data: D | undefined;
  error: unknown;
  loading: boolean;
}

const cache = new Map<string, Entry>();
const listeners = new Set<() => void>();

function emit() {
  for (const l of listeners) l();
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

function makeSnap(e: Entry) {
  e.snapshot = { data: e.data, error: e.error, loading: e.loading };
}

function load(key: string, e: Entry) {
  e.ctrl?.abort();
  const ctrl = new AbortController();
  e.ctrl = ctrl;
  const v = ++e.version;
  e.loading = true;
  makeSnap(e);
  emit();
  e.fetcher().then(
    (d) => {
      if (e.version !== v) return;
      e.data = d;
      e.error = null;
      e.loading = false;
      e.ctrl = null;
      makeSnap(e);
      emit();
    },
    (err) => {
      if (e.version !== v) return;
      if ((err as { name?: string })?.name === "AbortError") return;
      e.error = err;
      e.loading = false;
      e.ctrl = null;
      makeSnap(e);
      emit();
    },
  );
}

function ensure(key: string, fetcher?: () => Promise<unknown>, autoload = true): Entry {
  let e = cache.get(key);
  if (!e) {
    e = {
      data: undefined,
      error: null,
      loading: false,
      version: 0,
      subs: 0,
      fetcher: fetcher ?? (() => get(key)),
      ctrl: null,
      snapshot: { data: undefined, error: null, loading: true },
    };
    cache.set(key, e);
    if (autoload) load(key, e);
  } else if (fetcher) {
    e.fetcher = fetcher;
  }
  return e;
}

const EMPTY: Snap<never> = { data: undefined, error: null, loading: false };

/** Reads (and fetches if needed) one API path. Pass `null` to skip. */
export function useApi<D>(key: string | null, fetcher?: () => Promise<D>): Snap<D> & { reload: () => void } {
  const snap = useSyncExternalStore(
    subscribe,
    () => (key ? (cache.get(key)?.snapshot as Snap<D> | undefined) ?? (EMPTY as Snap<D>) : (EMPTY as Snap<D>)),
    () => EMPTY as Snap<D>,
  );
  useEffect(() => {
    if (!key) return;
    const e = ensure(key, fetcher as (() => Promise<unknown>) | undefined);
    e.subs++;
    return () => {
      e.subs--;
    };
    // the fetcher is tied to the key; a new fetcher with the same key is not a new request
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  const reload = useCallback(() => {
    if (!key) return;
    const e = cache.get(key);
    if (e) load(key, e);
  }, [key]);
  if (key && !cache.has(key)) return { data: undefined, error: null, loading: true, reload };
  return { ...snap, reload };
}

/** Refetch every key that starts with `prefix` (all keys when no prefix). */
export function invalidate(prefix = ""): void {
  for (const [key, e] of cache) {
    if (!key.startsWith(prefix)) continue;
    if (e.subs > 0) load(key, e);
    else {
      e.ctrl?.abort();
      cache.delete(key);
    }
  }
  emit();
}

/** Put data into the cache by hand (for example after a mutation returns the new object). */
export function setCached<D>(key: string, data: D): void {
  const e = ensure(key, undefined, false);
  e.ctrl?.abort();
  e.version++;
  e.data = data;
  e.error = null;
  e.loading = false;
  makeSnap(e);
  emit();
}

/** Test helper: empty the cache. */
export function clearCache(): void {
  for (const e of cache.values()) e.ctrl?.abort();
  cache.clear();
  emit();
}
