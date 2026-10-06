// Claude session preview body (02-ui-spec §2.4 "session"; prototype ViewMedia.dc.html).
// Live transcript: GET /sessions/{id}?cursor=&limit=40 (paged by byte offset;
// "Load more" follows `next`). Role filter (all | you | claude | tools) works on
// the loaded turns. Tools histogram chips, files edited, Resume ↗.
import { useEffect, useState } from "react";
import { api, qs } from "~/lib/api";
import { findProject, useBoot } from "~/lib/boot";
import { useApi } from "~/lib/query";
import { formatRef } from "~/lib/refs";
import { toast } from "~/lib/store";
import type { SessionDetailResponse, SessionSummary, Turn, TurnRole } from "~/lib/types";
import { useItem } from "~/lib/url";
import type { ViewportBodyProps } from "./registry";
import { baseRef, ErrorLine, Loading, MediaNotes, useNative, useNotes } from "./common";
import { fmtDur, fmtTime, fmtWhen } from "./fmt";

export const PAGE = 40;
type Filter = "all" | TurnRole;
const FILTERS: [Filter, string][] = [
  ["all", "All"],
  ["user", "You"],
  ["claude", "Claude"],
  ["tool", "Tools"],
];
const IMG = /\.(png|jpe?g|gif|webp|svg|avif)$/i;

export function SessionBody({ refStr, refObj, resolved }: ViewportBodyProps) {
  const p = resolved.projectId || refObj.projectId;
  const ref = baseRef(refObj);
  const id = refObj.id;
  const { openItem } = useItem();
  const { data: boot } = useBoot();
  const native = useNative(refStr);
  const notes = useNotes(ref, resolved.notes ?? []);
  const project = findProject(boot, p);

  const first = useApi<SessionDetailResponse>("/sessions/" + encodeURIComponent(id) + qs({ limit: PAGE }));
  const [more, setMore] = useState<Turn[]>([]);
  const [next, setNext] = useState<string | null | undefined>(undefined);
  const [loadingMore, setLoadingMore] = useState(false);
  const [filter, setFilter] = useState<Filter>("all");

  // A fresh first page (refetch) resets the extra pages.
  useEffect(() => {
    setMore([]);
    setNext(undefined);
  }, [first.data]);

  const s: Partial<SessionSummary> = first.data?.session ?? ((resolved.obj ?? {}) as Partial<SessionSummary>);
  const turns = [...(first.data?.turns ?? []), ...more];
  const cursor = next === undefined ? first.data?.next ?? null : next;

  const loadMore = async () => {
    if (!cursor) return;
    setLoadingMore(true);
    try {
      const page = await api.sessionDetail(id, { cursor, limit: PAGE });
      setMore((m) => [...m, ...page.turns]);
      setNext(page.next);
    } catch (e) {
      toast(e instanceof Error ? e.message : "could not load more turns", { error: true });
    } finally {
      setLoadingMore(false);
    }
  };

  const counts: Record<Filter, number> = { all: turns.length, user: 0, claude: 0, tool: 0 };
  for (const t of turns) counts[t.role] = (counts[t.role] ?? 0) + 1;
  const shown = turns.filter((t) => filter === "all" || t.role === filter);
  const tools = Object.entries(s.tools ?? {}).sort((a, b) => b[1] - a[1]);
  const maxTool = tools.length ? tools[0][1] : 1;
  const light = !!resolved.light && !first.data?.turns.length;
  const kb = s.kb ?? 0;
  const ms = s.end && s.start ? Date.parse(s.end) - Date.parse(s.start) : 0;
  const totalTurns = s.turns ?? turns.length;

  // Files edited: relative to the project when inside it.
  const root = project?.path ?? "";
  const edited = (s.edited ?? []).map((full) => {
    let rel: string | null = null;
    if (!full.startsWith("/") && !full.startsWith("~")) rel = full;
    else if (root && full.startsWith(root + "/")) rel = full.slice(root.length + 1);
    else if (root.startsWith("~/")) {
      // boot paths may use "~": match the part after the home folder.
      const tail = root.slice(1) + "/";
      const at = full.indexOf(tail);
      if (at >= 0 && (full.startsWith("~/") ? at === 1 : /^\/(Users|home)\/[^/]+$/.test(full.slice(0, at)))) rel = full.slice(at + tail.length);
    }
    return { full, rel };
  });

  return (
    <div className="vp-scroll" data-testid="session-body">
      <div className="vp-pad">
        <div className="vp-stack">
          <div className="vp-col">
            <div className="vp-label vp-violet">
              Claude session · {s.profile ?? "—"} · {id.slice(0, 8)}
            </div>
            <div className="vp-title" style={{ fontSize: 24, lineHeight: 1.15 }}>
              {s.title || "Untitled session"}
            </div>
          </div>
          <div className="vp-grid c4">
            <div>
              <div className="vp-label">Span</div>
              <div>{fmtWhen(s.start) + " → " + (s.end ? fmtTime(s.end) : "now")}</div>
            </div>
            <div>
              <div className="vp-label">Duration</div>
              <div>{fmtDur(ms)}</div>
            </div>
            <div>
              <div className="vp-label">Turns</div>
              <div>{totalTurns}</div>
            </div>
            <div>
              <div className="vp-label">Transcript</div>
              <div>{kb >= 1024 ? (kb / 1024).toFixed(1) + " MB" : kb + " KB"}</div>
            </div>
          </div>
          {tools.length ? (
            <div className="vp-chips" data-testid="tool-chips" aria-label="Tool use">
              <span className="vp-label" style={{ marginRight: 4 }}>
                Tools
              </span>
              {tools.map(([name, n]) => (
                <span key={name} className="vp-chip" title={name + ": " + n + " calls"}>
                  {name} <span className="c">{n}</span>
                  <span className="bar" style={{ width: Math.max(2, Math.round((n / maxTool) * 24)) }} aria-hidden="true" />
                </span>
              ))}
            </div>
          ) : null}
          <div className="vp-row">
            <button type="button" className="vp-btn big primary" onClick={() => void native("resume")}>
              Resume in terminal ↗
            </button>
          </div>

          {light ? (
            <div className="vp-panel tight" data-testid="session-light">
              <span className="big">Transcript not indexed — light summary</span>
              <span className="why">Only the title, time, profile and turn count are known. Resume still runs the native command.</span>
            </div>
          ) : (
            <div className="vp-stack">
              <div className="vp-seg violet" role="group" aria-label="Filter turns">
                {FILTERS.map(([f, label]) => (
                  <button key={f} type="button" aria-pressed={filter === f} onClick={() => setFilter(f)}>
                    {label} <span className="n">{counts[f]}</span>
                  </button>
                ))}
              </div>
              <div className="vp-turns" data-testid="turns">
                {first.loading && !first.data ? <Loading what="transcript" /> : null}
                {first.error ? <ErrorLine error={first.error} what="the transcript" /> : null}
                {shown.map((t) => (
                  <TurnView key={t.i} t={t} />
                ))}
                {first.data && shown.length === 0 ? <div className="vp-muted" style={{ fontSize: 11 }}>No turns of this kind in the loaded part.</div> : null}
                {cursor ? (
                  <div className="vp-more">
                    <span className="vp-muted" style={{ fontSize: 11 }}>
                      {turns.length} of {totalTurns} turns loaded
                    </span>
                    <button type="button" className="vp-btn" disabled={loadingMore} onClick={() => void loadMore()}>
                      {loadingMore ? "Loading…" : "Load " + PAGE + " more turns"}
                    </button>
                  </div>
                ) : null}
              </div>
            </div>
          )}

          <div className="vp-col" style={{ gap: 4 }} data-testid="edited">
            <div className="vp-label">Files edited · {edited.length}</div>
            {edited.length === 0 ? <div className="vp-muted" style={{ fontSize: 11 }}>No file edits in this session.</div> : null}
            {edited.map((e) => (
              <button
                key={e.full}
                type="button"
                className="vp-linkrow dr-hover"
                title={e.full}
                style={{ fontSize: 11 }}
                onClick={() => {
                  if (e.rel != null) openItem(formatRef({ type: IMG.test(e.rel) ? "image" : "file", projectId: p, id: e.rel }));
                  else toast(e.full + " is outside " + (project?.name ?? p) + " — not in the project tree");
                }}
              >
                <span style={{ width: 14, textAlign: "center" }} className={e.rel != null ? "vp-amber" : "vp-faint"} aria-hidden="true">
                  {e.rel != null ? "▤" : "◌"}
                </span>
                <span className="vp-ell" style={{ flex: 1 }}>
                  {e.rel ?? (e.full.length > 58 ? "…" + e.full.slice(-56) : e.full)}
                </span>
                <span className="vp-muted" style={{ fontSize: 10, whiteSpace: "nowrap" }}>
                  {e.rel != null ? "in " + (project?.name ?? p) : "outside project"}
                </span>
              </button>
            ))}
          </div>
        </div>
        <MediaNotes refStr={ref} projectId={p} notes={notes} />
      </div>
    </div>
  );
}

function TurnView({ t }: { t: Turn }) {
  if (t.role === "user")
    return (
      <div className="vp-turn vp-turn-user">
        <div>
          <div className="who">You</div>
          {t.text}
        </div>
      </div>
    );
  if (t.role === "claude")
    return (
      <div className="vp-turn vp-turn-claude">
        <div>
          <div className="who">Claude</div>
          {t.text}
        </div>
      </div>
    );
  const txt = String(t.text || "");
  const sp = txt.indexOf(" ");
  const name = t.tool || (sp < 0 ? txt : txt.slice(0, sp));
  const rest = t.tool ? (txt.startsWith(t.tool) ? txt.slice(t.tool.length).trim() : txt) : sp < 0 ? "" : txt.slice(sp + 1);
  return (
    <div className="vp-turn-tool" title={txt}>
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--dim)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M14.7 6.3a4 4 0 0 0-5.4 5.1L3 17.7V21h3.3l6.3-6.3a4 4 0 0 0 5.1-5.4l-2.6 2.6-2.4-.6-.6-2.4z" />
      </svg>
      <span className="name">{name || "tool"}</span>
      <span className="vp-ell" style={{ flex: 1 }}>
        {rest}
      </span>
    </div>
  );
}
