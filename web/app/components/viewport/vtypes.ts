// Local types for the viewport bodies.
//
// Some fields the Go API sends are not (yet) in lib/types.ts. They are added
// here as optional extensions so the bodies can use them without editing the
// shared types file. See the w-viewport report, "Needs from other packages".
import type { CommitDetailResponse, DirtyResponse, FileResponse, Server, Snapshot, VerdictResponse } from "~/lib/types";

/** GET /projects/{p}/file, plus the flags tree.ReadFile sets (a-discovery report). */
export interface FileWindow extends FileResponse {
  /** NUL byte found in the first 8 KB: no text preview. */
  binary?: boolean;
  /** A secret file (.env, *.pem, id_*): the body is never sent. */
  redacted?: boolean;
}

/** GET /projects/{p}/commits/{id}, plus the 400 KB patch cap flag. */
export interface CommitDetail extends CommitDetailResponse {
  truncated?: boolean;
}

/** GET /projects/{p}/dirty, plus the 400 KB diff cap flag. */
export interface DirtyDetail extends DirtyResponse {
  truncated?: boolean;
}

/** The `obj` of a resolved file / doc / image ref. */
export interface FileObj {
  path?: string;
  size?: number | null;
  mtime?: string | null;
  isDoc?: boolean;
  url?: string;
  /** False when the file is gone from disk (the resolver's `exists`). Unset counts as present. */
  exists?: boolean;
}

/** The `obj` of a resolved server ref. Status fields differ between mock and Go, so all are optional. */
export interface ServerObj extends Partial<Server> {
  running?: boolean;
  alive?: boolean;
  /** Last capture of the page (decision Q8), if the server knows one. */
  lastCapture?: { url?: string; path?: string; captured?: string } | null;
}

/** The `obj` of a resolved snapshot ref. */
export interface SnapshotObj extends Partial<Snapshot> {
  id?: string;
  taskId?: string;
  path?: string;
}

/** PUT verdict answer; per decision A4 approving may only suggest done. */
export interface VerdictAnswer extends VerdictResponse {
  suggestion?: { status: string } | null;
}
