// Folder preview body: the folder's entries, like a small Finder window.
//
// - One GET /projects/{p}/tree?dir=&depth=1 (the same key the Files tab uses, so they share the cache).
// - A click on an entry opens it in this viewport (a sub-folder lists in turn; ↑ goes to the parent).
// - Collapsed folders (node_modules, .git, ...) are listed but not opened.
// - Open in Finder / Terminal / editor sit on top.
import { formatRef } from "~/lib/refs";
import { useApi } from "~/lib/query";
import type { TreeResponse } from "~/lib/types";
import { useItem } from "~/lib/url";
import type { ViewportBodyProps } from "./registry";
import { baseRef, ErrorLine, Loading, MediaNotes, NativeButtons, useNotes } from "./common";
import { fmtSize, rel } from "./fmt";
import type { FileObj } from "./vtypes";

const enc = encodeURIComponent;

export function FolderBody({ refStr, refObj, resolved }: ViewportBodyProps) {
  const p = resolved.projectId || refObj.projectId;
  const obj = (resolved.obj ?? {}) as FileObj;
  const dir = obj.path ?? refObj.id;
  const ref = baseRef(refObj);
  const notes = useNotes(ref, resolved.notes ?? []);
  const { openItem } = useItem();
  const q = useApi<TreeResponse>("/projects/" + enc(p) + "/tree?dir=" + enc(dir) + "&depth=1");

  const prefix = dir ? dir + "/" : "";
  const entries = (q.data?.entries ?? [])
    .filter((e) => e.path.startsWith(prefix) && !e.path.slice(prefix.length).includes("/"))
    .sort((a, b) => Number(b.dir) - Number(a.dir) || a.path.localeCompare(b.path));
  const parent = dir.includes("/") ? dir.slice(0, dir.lastIndexOf("/")) : "";
  const open = (path: string) => openItem(formatRef({ type: "file", projectId: p, id: path }));

  return (
    <div className="vp-scroll" data-testid="folder-body">
      <div className="vp-pad">
        <div className="vp-stack">
          <NativeButtons refStr={refStr} actions={resolved.nativeActions ?? []} />
          <div className="vp-label">
            Folder · {q.data ? entries.length + (entries.length === 1 ? " item" : " items") : "—"}
          </div>
          {q.loading && !q.data ? (
            <Loading what="folder" />
          ) : q.error && !q.data ? (
            <ErrorLine error={q.error} what="the folder" />
          ) : (
            <div className="vp-col" style={{ gap: 0 }}>
              {parent ? (
                <button type="button" className="vp-folder-row" onClick={() => open(parent)}>
                  <span className="ico">↑</span>
                  <span className="name">{parent.split("/").pop()}</span>
                  <span className="vp-faint">parent</span>
                </button>
              ) : null}
              {entries.length === 0 ? <div className="vp-faint" style={{ padding: "8px 0" }}>Empty folder.</div> : null}
              {entries.map((e) => (
                <button
                  key={e.path}
                  type="button"
                  className="vp-folder-row"
                  disabled={e.collapsed}
                  title={e.collapsed ? "drashta does not open this folder" : e.path}
                  onClick={() => open(e.path)}
                >
                  <span className="ico">{e.dir ? "▸" : "·"}</span>
                  <span className="name">{e.path.slice(prefix.length) + (e.dir ? "/" : "")}</span>
                  <span className="vp-faint">{e.dir ? (e.collapsed ? "skipped" : "") : fmtSize(e.size)}</span>
                  <span className="vp-faint">{e.mtime ? rel(e.mtime) : ""}</span>
                </button>
              ))}
            </div>
          )}
        </div>
        <MediaNotes refStr={ref} projectId={p} notes={notes} />
      </div>
    </div>
  );
}
