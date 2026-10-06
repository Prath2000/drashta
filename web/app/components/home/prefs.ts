// Group, order and show-hidden are kept in localStorage so the wallpaper (readout
// mode) shows the same view you chose in browsing mode. The range lives in the
// shared store (lib/store.ts). Storage can throw (private mode, blocked): every
// access is wrapped.
import type { Order } from "./model";

export const PREFS_KEY = "drashta.home.v1";

export interface HomePrefs {
  group: string;
  order: Order;
  showHidden: boolean;
}

export const DEFAULT_PREFS: HomePrefs = { group: "all", order: "recency", showHidden: false };

export function loadPrefs(): HomePrefs {
  const d = { ...DEFAULT_PREFS };
  try {
    const raw = window.localStorage.getItem(PREFS_KEY);
    if (!raw) return d;
    const o = JSON.parse(raw) as Partial<HomePrefs> | null;
    if (o && typeof o.group === "string" && o.group) d.group = o.group;
    if (o && (o.order === "recency" || o.order === "loudest")) d.order = o.order;
    if (o && o.showHidden === true) d.showHidden = true;
  } catch {
    /* storage can throw; the defaults are fine */
  }
  return d;
}

export function savePrefs(p: HomePrefs): void {
  try {
    window.localStorage.setItem(PREFS_KEY, JSON.stringify({ group: p.group, order: p.order, showHidden: p.showHidden }));
  } catch {
    /* ignore */
  }
}
