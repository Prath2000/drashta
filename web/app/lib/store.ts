// Small client store for UI state that is not in the URL.
//
// - mode: "readout" | "browsing". Readout = `<html class="desktop">` without
//   `plash-is-browsing-mode` (D3, 02-ui-spec §4). The `desktop` class is set
//   once at start when the URL has `?desktop=1`. A MutationObserver on <html>
//   follows Plash's class changes, so the mode flips at once, with no reload.
// - range 7|30 (kept in localStorage, so the wallpaper shows the same view).
// - search text and the project ids its hits light up (Home dims the rest).
// - one toast at a time.
// - counters, scan state and stream state (fed by events.ts and boot).
// - viewport history (back / forward between previewed items).
import { useSyncExternalStore } from "react";
import type { Counts, IsoTime } from "./types";

export type Mode = "readout" | "browsing";
export type Range = 7 | 30;

export interface Toast {
  id: number;
  msg: string;
  error: boolean;
}

export interface ScanState {
  /** running = a rescan is in progress. */
  running: boolean;
  /** Time of the last finished scan, if known. */
  at: IsoTime | null;
  /** True after a scan finished while this page was open ("updated …"). */
  updatedHere: boolean;
  /** Bumped on each user-started rescan; the shell draws one sweep per value. */
  sweep: number;
  /** A rescan the user started is in progress (its end makes a toast). */
  userPending: boolean;
  /** Data-change events seen while userPending. */
  changes: number;
}

export interface State {
  mode: Mode;
  range: Range;
  search: string;
  /** Project ids that the current search hits (Home lights these lanes). null = no active search. */
  searchLit: string[] | null;
  toast: Toast | null;
  counts: Counts;
  scan: ScanState;
  online: boolean;
  lastProjectId: string | null;
  hist: { list: string[]; idx: number };
}

const RANGE_KEY = "drashta.range";
const LAST_PROJECT_KEY = "drashta.lastProject";

function readLS(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
function writeLS(key: string, v: string) {
  try {
    localStorage.setItem(key, v);
  } catch {
    /* ignore */
  }
}

/** Mode from the <html> classes (the only source of truth for the mode). */
export function modeFromClasses(el: { classList: DOMTokenList } | null | undefined): Mode {
  if (!el) return "browsing";
  const desktop = el.classList.contains("desktop");
  const browsing = el.classList.contains("plash-is-browsing-mode");
  return desktop && !browsing ? "readout" : "browsing";
}

function initialState(): State {
  const r = readLS(RANGE_KEY);
  return {
    mode: typeof document !== "undefined" ? modeFromClasses(document.documentElement) : "browsing",
    range: r === "7" ? 7 : 30,
    search: "",
    searchLit: null,
    toast: null,
    counts: { liveServers: 0, openTasks: 0, awaitingReview: 0, openNotes: 0 },
    scan: { running: false, at: null, updatedHere: false, sweep: 0, userPending: false, changes: 0 },
    online: true,
    lastProjectId: readLS(LAST_PROJECT_KEY),
    hist: { list: [], idx: -1 },
  };
}

let state: State = initialState();
const listeners = new Set<() => void>();

export function getState(): State {
  return state;
}

export function setState(patch: Partial<State> | ((s: State) => Partial<State>)): void {
  const p = typeof patch === "function" ? patch(state) : patch;
  state = { ...state, ...p };
  for (const l of listeners) l();
}

export function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** React hook: select a slice of the store. Return a stable value (primitive or a store field). */
export function useStore<T>(sel: (s: State) => T): T {
  return useSyncExternalStore(
    subscribe,
    () => sel(state),
    () => sel(state),
  );
}

/** Test helper: reset to the initial state. */
export function resetStore(): void {
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = null;
  state = initialState();
  for (const l of listeners) l();
}

// ---------- mode ----------
let observer: MutationObserver | null = null;

/**
 * Starts mode tracking. Adds `desktop` to <html> when the URL has `?desktop=1`,
 * then follows class changes. Returns a stop function.
 */
export function startModeTracking(doc: Document = document): () => void {
  const html = doc.documentElement;
  const params = new URLSearchParams(doc.location?.search ?? "");
  if (params.get("desktop") === "1") html.classList.add("desktop");
  const sync = () => {
    const m = modeFromClasses(html);
    if (m !== state.mode) {
      // leaving browsing: drop the toast (readout shows none)
      setState(m === "readout" ? { mode: m, toast: null } : { mode: m });
    }
  };
  sync();
  observer?.disconnect();
  observer = new MutationObserver(sync);
  observer.observe(html, { attributes: true, attributeFilter: ["class"] });
  return () => {
    observer?.disconnect();
    observer = null;
  };
}

// ---------- range ----------
export function setRange(r: Range): void {
  writeLS(RANGE_KEY, String(r));
  setState({ range: r });
}

// ---------- search ----------
/** Sets the search text. An empty text clears the lit lanes; otherwise they stay until new hits arrive. */
export function setSearch(text: string): void {
  setState((s) => ({ search: text, searchLit: text.trim() ? s.searchLit : null }));
}

/** Project ids that the current hits light up (set by the search box when results arrive). */
export function setSearchLit(lit: string[] | null): void {
  setState((s) => ({ searchLit: s.search.trim() ? lit : null }));
}

// ---------- last project ----------
export function setLastProject(id: string): void {
  if (state.lastProjectId === id) return;
  writeLS(LAST_PROJECT_KEY, id);
  setState({ lastProjectId: id });
}

// ---------- toasts ----------
let toastSeq = 0;
let toastTimer: ReturnType<typeof setTimeout> | null = null;
export const TOAST_MS = 2200;

/**
 * Shows one toast (it replaces the current one). A normal toast leaves after ~2 s.
 * An error toast (coral) stays until dismissed. Readout mode shows no toasts.
 * Only call this after a user action; background events never make a toast.
 */
export function toast(msg: string, opts: { error?: boolean } = {}): void {
  if (state.mode === "readout") return;
  const t: Toast = { id: ++toastSeq, msg: String(msg), error: !!opts.error };
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = null;
  setState({ toast: t });
  if (!t.error) {
    toastTimer = setTimeout(() => {
      if (state.toast?.id === t.id) setState({ toast: null });
      toastTimer = null;
    }, TOAST_MS);
  }
}

export function dismissToast(): void {
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = null;
  setState({ toast: null });
}

// ---------- counts / scan / stream ----------
export function setCounts(c: Counts): void {
  setState({ counts: { ...c } });
}

export function setOnline(online: boolean): void {
  if (state.online !== online) setState({ online });
}

export function scanStarted(userStarted: boolean): void {
  setState((s) => ({
    scan: userStarted
      ? { ...s.scan, running: true, sweep: s.scan.sweep + 1, userPending: true, changes: 0 }
      : { ...s.scan, running: true },
  }));
}

/** Counts a data change while a user-started rescan runs. */
export function scanSawChange(): void {
  if (!state.scan.userPending) return;
  setState((s) => ({ scan: { ...s.scan, changes: s.scan.changes + 1 } }));
}

/**
 * Marks the scan as finished. When the user started it, one toast confirms it:
 * "All projects rescanned · no new changes" (02-ui-spec §2.3).
 */
export function scanDone(at: IsoTime | null): void {
  const { userPending, changes } = state.scan;
  setState((s) => ({ scan: { ...s.scan, running: false, at: at ?? s.scan.at, updatedHere: true, userPending: false, changes: 0 } }));
  if (userPending) {
    toast("All projects rescanned · " + (changes > 0 ? changes + " change" + (changes === 1 ? "" : "s") : "no new changes"));
  }
}

/** A user-started rescan could not start: clear the running state with no toast. */
export function scanFailed(): void {
  setState((s) => ({ scan: { ...s.scan, running: false, userPending: false, changes: 0 } }));
}

export function setLastScan(at: IsoTime | null): void {
  setState((s) => ({ scan: { ...s.scan, at } }));
}

// ---------- viewport history ----------
/** Records that `ref` is now open in the viewport (pushes it unless it is the current entry). */
export function histPush(ref: string): void {
  setState((s) => {
    if (s.hist.list[s.hist.idx] === ref) return {};
    const list = s.hist.list.slice(0, s.hist.idx + 1).concat([ref]).slice(-40);
    return { hist: { list, idx: list.length - 1 } };
  });
}

/** Moves in the viewport history; returns the ref to show, or null at the end. */
export function histMove(delta: -1 | 1): string | null {
  const { list, idx } = state.hist;
  const n = idx + delta;
  if (n < 0 || n >= list.length) return null;
  setState({ hist: { list, idx: n } });
  return list[n];
}
