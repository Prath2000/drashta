// Viewport body registry: ref type -> the component that draws the preview body.
//
// The shell (components/shell/Viewport.tsx) draws the drawer frame: header,
// Open ↗, history, task links, and the "missing item" state. It looks up the
// body here by the resolved type. Keep the export names.
//
// Bodies (all in this folder, all take ViewportBodyProps):
//   file, doc   FileBody      code with line numbers, line notes, rendered docs, dirty diff;
//                             audio, video and PDF play in MediaFileBody, 3D models in
//                             ModelBody, DWG drawings in DwgBody (chosen by viewers.ts)
//   folder      FolderBody    the folder's entries; a click opens one here
//   commit      CommitBody    header, files changed, per-file diff, notes
//   image       ImageBody     /raw image, zoom, x,y pin notes, make snapshot
//   snapshot    SnapshotBody  pins, annotate, versions, compare, verdict
//   session     SessionBody   transcript paged by `next`, role filter, tools, edited files
//   server      ServerBody    status, live app in an iframe (replaces Q8) or the last capture, Expand, kill / start with confirm
//   task        TaskBody      editable task preview (lives in components/tracker): status, links, snapshots, notes, log
//   others      GenericBody   "No preview for this kind of item." + notes
import type { ComponentType } from "react";
import type { RefObj, ResolveResponse } from "~/lib/types";
import "./viewport.css";
import { CommitBody } from "./CommitBody";
import { FileBody } from "./FileBody";
import { FolderBody } from "./FolderBody";
import { GenericBody } from "./GenericBody";
import { ImageBody } from "./ImageBody";
import { ServerBody } from "./ServerBody";
import { SessionBody } from "./SessionBody";
import { SnapshotBody } from "./SnapshotBody";
import { TaskBody } from "~/components/tracker/TaskBody";

export interface ViewportBodyProps {
  /** The ref string from the URL (?item=). */
  refStr: string;
  /** The parsed ref. */
  refObj: RefObj;
  /** GET /api/resolve answer for this ref. */
  resolved: ResolveResponse;
}

export type ViewportBody = ComponentType<ViewportBodyProps>;

/** Keyed by the RESOLVED type: a `file` ref to a folder resolves to `folder`. */
export const viewportBodies: Partial<Record<string, ViewportBody>> = {
  file: FileBody,
  folder: FolderBody,
  doc: FileBody,
  image: ImageBody,
  commit: CommitBody,
  session: SessionBody,
  server: ServerBody,
  snapshot: SnapshotBody,
  task: TaskBody,
  note: GenericBody,
  feature: GenericBody,
  milestone: GenericBody,
  project: GenericBody,
};

/** The body for a type, or the generic "no preview" body. */
export function bodyFor(type: string): ViewportBody {
  return viewportBodies[type] ?? GenericBody;
}
