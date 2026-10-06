// Commits tab: working copy on top, then git + fossil commits newest first.
// The first page holds up to 200 commits; "show all N" reads the rest
// page by page with the `next` cursor.
import { useState } from "react";
import { api } from "~/lib/api";
import { useApi } from "~/lib/query";
import type { Commit, CommitsResponse, DirtyResponse } from "~/lib/types";
import { CAP, keys, ref, usePaged } from "./data";
import { fmtDT, pill } from "./fmt";
import { DirtyPanel } from "./OverviewTab";
import { C, EmptyBox, MoreRow } from "./ui";

const COLS = "64px minmax(200px, 1fr) 96px 120px 110px 90px";

export function useCommits(p: string) {
  return usePaged<CommitsResponse, Commit>(
    keys.commits(p),
    (r) => ({ items: r.commits, next: r.next }),
    (cursor) => api.commits(p, { cursor, limit: CAP }),
  );
}

export function CommitsTab(props: {
  p: string;
  vcs: string[];
  total: number;
  commits: ReturnType<typeof useCommits>;
  openItem: (r: string) => void;
}) {
  const { p, vcs, commits, openItem } = props;
  const dirty = useApi<DirtyResponse>(keys.dirty(p));
  const [all, setAll] = useState(false);
  const total = Math.max(props.total, commits.items.length);
  const shown = all ? commits.items : commits.items.slice(0, CAP);
  const hasMore = commits.next != null || (!all && commits.items.length > CAP);
  const showAll = () => {
    setAll(true);
    if (commits.next) void commits.loadAll();
  };
  return (
    <div data-testid="tab-commits">
      <DirtyPanel
        p={p}
        files={dirty.data?.files ?? []}
        diff={dirty.data?.diff}
        versioned={vcs.length > 0}
        openItem={openItem}
        style={{ marginBottom: 16 }}
      />
      {commits.loading ? <div style={{ fontSize: 12, color: C.muted }}>reading commits…</div> : null}
      {commits.error && !commits.items.length ? <EmptyBox>could not read commits.</EmptyBox> : null}
      {shown.length ? (
        <div style={{ border: "1px solid " + C.hair, background: C.panel, overflowX: "auto" }}>
          <div
            style={{
              minWidth: 780,
              display: "grid",
              gridTemplateColumns: COLS,
              gap: 12,
              padding: "10px 16px",
              borderBottom: "1px solid " + C.hair,
              fontSize: 10,
              letterSpacing: ".12em",
              textTransform: "uppercase",
              color: C.muted,
            }}
          >
            <span>VCS</span>
            <span>Message · {total + (vcs.length ? " across " + vcs.join(" + ") : "")}</span>
            <span>ID</span>
            <span>Author</span>
            <span>Date</span>
            <span style={{ textAlign: "right" }}>+ / −</span>
          </div>
          {shown.map((c) => (
            <button
              key={c.vcs + c.id}
              type="button"
              data-testid="commit-row"
              className="pj-hover"
              onClick={() => openItem(ref(p, "commit", c.id))}
              style={{
                width: "100%",
                minWidth: 780,
                minHeight: 40,
                display: "grid",
                gridTemplateColumns: COLS,
                gap: 12,
                alignItems: "center",
                padding: "6px 16px",
                background: "transparent",
                border: 0,
                borderBottom: "1px solid " + C.row,
                color: C.text,
                textAlign: "left",
                cursor: "pointer",
              }}
            >
              <span style={pill(c.vcs === "git" ? C.amber : "#e8b87a")}>{c.vcs}</span>
              <span className="pj-trunc">{c.msg || "commit"}</span>
              <span style={{ color: C.amber }}>{String(c.id).slice(0, 10)}</span>
              <span className="pj-trunc" style={{ color: C.muted }}>
                {c.author || "—"}
              </span>
              <span style={{ color: C.muted }}>{fmtDT(c.ts)}</span>
              <span style={{ textAlign: "right" }}>
                <span style={{ color: C.teal }}>{c.plus != null ? "+" + c.plus : ""}</span>{" "}
                <span style={{ color: C.coral }}>{c.minus != null ? "−" + c.minus : "—"}</span>
              </span>
            </button>
          ))}
          {hasMore && !(all && !commits.next) ? (
            <MoreRow
              label={"newest " + shown.length + " of " + total + " shown"}
              total={total}
              color={C.amber}
              busy={commits.busy}
              onClick={showAll}
            />
          ) : null}
        </div>
      ) : null}
      {!commits.loading && !commits.error && commits.items.length === 0 ? (
        <EmptyBox>{vcs.length ? "no commits on record." : "not under version control — no commits to read."}</EmptyBox>
      ) : null}
    </div>
  );
}
