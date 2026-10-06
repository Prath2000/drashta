// Ref strings (docs/spec/04-data-model.md §2.4): `[<project>:]<type>:<id>[#<fragment>]`.
// In URLs the viewport item is always the full form with the project, for
// example `?item=acme-api:commit:bdcb99c`.
import type { RefObj, RefType } from "./types";

export const REF_TYPES: RefType[] = [
  "file",
  "image",
  "commit",
  "session",
  "server",
  "snapshot",
  "task",
  "note",
  "feature",
  "milestone",
  "project",
  "doc",
];

const known = new Set<string>(REF_TYPES);

/** Parses a ref string. `defaultProject` fills a local ref. Returns null when it is not a ref. */
export function parseRef(s: string | null | undefined, defaultProject = ""): RefObj | null {
  if (!s) return null;
  let body = s;
  let frag: string | undefined;
  const h = s.indexOf("#");
  if (h >= 0) {
    body = s.slice(0, h);
    frag = s.slice(h + 1) || undefined;
  }
  const a = body.indexOf(":");
  if (a < 0) return null;
  const first = body.slice(0, a);
  let projectId = defaultProject;
  let rest = body.slice(a + 1);
  if (!known.has(first)) {
    projectId = first;
    const b = rest.indexOf(":");
    if (b < 0) return null;
    const type = rest.slice(0, b);
    if (!known.has(type)) return null;
    rest = rest.slice(b + 1);
    return rest ? withFrag({ type: type as RefType, projectId, id: rest }, frag) : null;
  }
  return rest ? withFrag({ type: first as RefType, projectId, id: rest }, frag) : null;
}

function withFrag(r: RefObj, frag?: string): RefObj {
  if (frag) r.frag = frag;
  return r;
}

/** Formats a ref object as the full string form `<project>:<type>:<id>[#frag]`. */
export function formatRef(r: RefObj): string {
  const base = (r.projectId ? r.projectId + ":" : "") + r.type + ":" + r.id;
  return r.frag ? base + "#" + r.frag : base;
}

/** True when two refs point at the same item (fragment ignored). */
export function sameRef(a: string | null | undefined, b: string | null | undefined): boolean {
  const x = parseRef(a);
  const y = parseRef(b);
  if (!x || !y) return false;
  return x.type === y.type && x.projectId === y.projectId && x.id === y.id;
}
