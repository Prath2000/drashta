// URL state helpers. Routes (04-data-model §7):
//   /                       home
//   /p                      project picker
//   /p/:project/:tab?       project page (tab defaults to overview)
//   /tracker?filter=review  tracker (filter all|review)
//   /inbox                  inbox
// The viewport item lives in `?item=<ref>` on any route.
import { useCallback } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";
import { histPush, setLastProject } from "./store";

export type Section = "home" | "project" | "tracker" | "inbox";

export const PROJECT_TABS = ["overview", "files", "commits", "sessions", "servers", "docs", "tracker"] as const;
export type ProjectTab = (typeof PROJECT_TABS)[number];

export function homePath(): string {
  return "/";
}

export function pickerPath(): string {
  return "/p";
}

export function projectPath(project: string, tab?: ProjectTab | string): string {
  const base = "/p/" + encodeURIComponent(project);
  return tab && tab !== "overview" ? base + "/" + encodeURIComponent(tab) : base;
}

export function trackerPath(filter?: "all" | "review", project?: string): string {
  const p = new URLSearchParams();
  if (filter === "review") p.set("filter", "review");
  if (project) p.set("project", project);
  const s = p.toString();
  return "/tracker" + (s ? "?" + s : "");
}

export function inboxPath(): string {
  return "/inbox";
}

/** Which rail section a pathname belongs to. */
export function sectionOf(pathname: string): Section {
  if (pathname === "/p" || pathname.startsWith("/p/")) return "project";
  if (pathname.startsWith("/tracker")) return "tracker";
  if (pathname.startsWith("/inbox")) return "inbox";
  return "home";
}

/** Parses `/p/:project/:tab?`; null when the path is not a project page. */
export function projectFromPath(pathname: string): { project: string; tab: string } | null {
  const m = pathname.match(/^\/p\/([^/]+)(?:\/([^/]+))?\/?$/);
  if (!m) return null;
  return { project: decodeURIComponent(m[1]), tab: m[2] ? decodeURIComponent(m[2]) : "overview" };
}

/** Returns a search string with `item` set (or removed when ref is null). */
export function withItem(search: string, ref: string | null): string {
  const p = new URLSearchParams(search);
  if (ref) p.set("item", ref);
  else p.delete("item");
  const s = p.toString();
  return s ? "?" + s : "";
}

/**
 * Viewport item in the URL.
 *   const { item, openItem, closeItem } = useItem();
 * openItem(ref) shows the item and records it in the viewport history.
 */
export function useItem() {
  const [params] = useSearchParams();
  const loc = useLocation();
  const navigate = useNavigate();
  const item = params.get("item");
  const openItem = useCallback(
    (ref: string, opts: { record?: boolean } = {}) => {
      if (opts.record !== false) histPush(ref);
      navigate({ pathname: loc.pathname, search: withItem(loc.search, ref) });
    },
    [loc.pathname, loc.search, navigate],
  );
  const closeItem = useCallback(() => {
    navigate({ pathname: loc.pathname, search: withItem(loc.search, null) });
  }, [loc.pathname, loc.search, navigate]);
  return { item, openItem, closeItem };
}

/** Navigation helpers that keep the rules in one place. */
export function useGo() {
  const navigate = useNavigate();
  return {
    home: () => navigate(homePath()),
    project: (id: string, tab?: string) => {
      setLastProject(id);
      navigate(projectPath(id, tab));
    },
    picker: () => navigate(pickerPath()),
    tracker: (filter?: "all" | "review", project?: string) => navigate(trackerPath(filter, project)),
    inbox: () => navigate(inboxPath()),
  };
}
