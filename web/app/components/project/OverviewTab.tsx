// Overview tab (02-ui-spec §5.4): vitals, continue where you left off, open
// notes, working copy, README, GitHub block (decision A6) and milestones.
import type { CSSProperties } from "react";
import { parseRef } from "~/lib/refs";
import { useApi } from "~/lib/query";
import type { Commit, DayResponse, DirtyFile, Note, ProjectDetail, Server, SessionSummary, Task, TrackerResponse } from "~/lib/types";
import { githubReason, keys, ref, type GithubData } from "./data";
import { base, dayParts, fmtTime, IMAGE_RE, kindColor, kindGlyph, ms, pill, rel, statusColor, statusLabel } from "./fmt";
import { C, h2Style, label10, Panel } from "./ui";

const STATUSES = ["backlog", "todo", "in_progress", "review", "done"];

const tile: CSSProperties = { padding: "14px 16px", borderRight: "1px solid " + C.hair, borderBottom: "1px solid " + C.hair, minWidth: 0 };
const big = (color: string): CSSProperties => ({ fontSize: 22, color, marginTop: 6 });
const sub: CSSProperties = { fontSize: 11, color: C.muted, marginTop: 2 };

const rowBtn: CSSProperties = {
  width: "100%",
  background: "transparent",
  border: 0,
  borderBottom: "1px solid " + C.row,
  color: C.text,
  textAlign: "left",
  cursor: "pointer",
};

/** Whether a working-copy path can be previewed (deleted files cannot). */
export function dirtyOpenable(d: DirtyFile): boolean {
  return !/^(d|deleted|missing)\b/i.test(String(d.state).trim());
}

export function fileRef(p: string, path: string): string {
  return ref(p, IMAGE_RE.test(path) ? "image" : "file", path);
}

/** "Working copy · uncommitted": ± state + path, click → file preview. */
export function DirtyPanel(props: {
  p: string;
  files: DirtyFile[];
  diff?: string;
  versioned: boolean;
  openItem: (r: string) => void;
  style?: CSSProperties;
}) {
  const { p, files, versioned, openItem } = props;
  const n = files.length;
  const diffLines = props.diff ? props.diff.split("\n").length : 0;
  return (
    <section aria-label="Working copy" style={{ border: "1px dashed " + C.amber, background: C.panel, ...props.style }}>
      <div
        style={{
          padding: "10px 16px",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "baseline",
          gap: 12,
          borderBottom: "1px solid " + C.hair,
        }}
      >
        <span style={{ fontSize: 10, letterSpacing: ".12em", textTransform: "uppercase", color: C.amber }}>Working copy · uncommitted</span>
        <span style={{ fontSize: 11, color: C.muted }}>
          {n + " path" + (n === 1 ? "" : "s") + (diffLines ? " · " + diffLines + " diff lines" : "")}
        </span>
      </div>
      {files.map((d) => {
        const ok = dirtyOpenable(d);
        return (
          <button
            key={d.path}
            type="button"
            className="pj-hover"
            disabled={!ok}
            title={ok ? "Preview " + d.path : d.path + " cannot be previewed"}
            onClick={() => ok && openItem(fileRef(p, d.path))}
            style={{
              ...rowBtn,
              minHeight: 36,
              display: "grid",
              gridTemplateColumns: "72px minmax(0, 1fr) 24px",
              gap: 12,
              alignItems: "center",
              padding: "4px 16px",
              color: ok ? C.text : C.muted,
              cursor: ok ? "pointer" : "default",
            }}
          >
            <span className="pj-trunc" style={{ color: C.amber }}>
              ± {d.state}
            </span>
            <span className="pj-trunc">{d.path}</span>
            <span style={{ color: C.dim, textAlign: "right" }}>{ok ? "→" : ""}</span>
          </button>
        );
      })}
      {n === 0 ? (
        <div style={{ padding: "12px 16px", fontSize: 14, color: C.muted }}>
          {versioned ? "Clean — nothing uncommitted." : "not under version control."}
        </div>
      ) : null}
    </section>
  );
}

function DayPanel(props: { p: string; day: string; onClose: () => void; openItem: (r: string) => void; filesCount: number }) {
  const { p, day, onClose, openItem } = props;
  const { data, loading, error } = useApi<DayResponse>(keys.day(p, day));
  const dp = dayParts(day);
  const evs = data?.events ?? [];
  const files = data?.files ?? [];
  const more = data?.moreFiles ?? 0;
  const nFiles = Math.max(props.filesCount, data?.totals.files ?? 0);
  return (
    <section aria-label="Read the day" style={{ border: "1px solid " + C.hair, background: C.panel, marginBottom: 18 }}>
      <div
        style={{
          padding: "12px 18px 10px",
          borderBottom: "1px solid " + C.hair,
          display: "flex",
          alignItems: "baseline",
          justifyContent: "space-between",
          gap: 12,
        }}
      >
        <h2 style={h2Style}>
          Read the day <span style={{ color: C.muted }}>· {dp.wd + " " + dp.d + " " + dp.mon}</span>
        </h2>
        <button
          type="button"
          className="pj-hover"
          onClick={onClose}
          aria-label="Close day readout"
          style={{ minHeight: 36, padding: "0 12px", background: "transparent", color: C.muted, border: "1px solid " + C.hair, borderRadius: 3, cursor: "pointer" }}
        >
          ✕ close
        </button>
      </div>
      {loading && !data ? <div style={{ padding: "14px 18px", fontSize: 12, color: C.muted }}>reading the day…</div> : null}
      {error ? <div style={{ padding: "14px 18px", fontSize: 12, color: C.coral }}>could not read this day.</div> : null}
      {evs.map((e, i) => (
        <button
          key={e.ref + i}
          type="button"
          className="pj-hover"
          onClick={() => openItem(e.ref)}
          style={{
            ...rowBtn,
            borderBottom: "1px solid " + C.faint,
            minHeight: 36,
            display: "grid",
            gridTemplateColumns: "56px 116px minmax(0, 1fr) 24px",
            alignItems: "center",
            gap: 12,
            padding: "4px 18px",
          }}
        >
          <span style={{ color: C.muted }}>{fmtTime(e.ts)}</span>
          <span style={{ fontSize: 11, letterSpacing: ".08em", textTransform: "uppercase", color: kindColor(e.kind), whiteSpace: "nowrap" }}>
            {kindGlyph(e.kind)} {e.kind}
          </span>
          <span className="pj-trunc">{e.label}</span>
          <span style={{ color: C.dim, textAlign: "right" }}>→</span>
        </button>
      ))}
      {data && !evs.length ? (
        <div style={{ padding: "14px 18px", fontSize: 14, color: C.muted }}>
          {nFiles ? "files moved, nothing committed." : "a flat line — no tremor this day."}
        </div>
      ) : null}
      {files.length ? (
        <>
          <div style={{ padding: "10px 18px 6px", ...label10, letterSpacing: ".14em" }}>
            Files changed · {files.length + " of " + Math.max(nFiles, files.length + more)}
          </div>
          {files.map((path) => (
            <button
              key={path}
              type="button"
              className="pj-hover"
              onClick={() => openItem(fileRef(p, path))}
              style={{
                ...rowBtn,
                borderBottom: "1px solid " + C.faint,
                minHeight: 32,
                display: "grid",
                gridTemplateColumns: "14px minmax(0, 1fr)",
                gap: 8,
                alignItems: "center",
                padding: "2px 18px",
              }}
            >
              <span style={{ color: C.dim }}>·</span>
              <span className="pj-trunc">{path}</span>
            </button>
          ))}
          {more > 0 ? <div style={{ padding: "8px 18px 12px", fontSize: 11, color: C.muted }}>+{more} more</div> : null}
        </>
      ) : data && nFiles ? (
        <div style={{ padding: "10px 18px 14px", fontSize: 11, color: C.muted }}>
          {nFiles} file change{nFiles === 1 ? "" : "s"} that day — file list not recorded.
        </div>
      ) : null}
    </section>
  );
}

function GithubBlock(props: { p: string; remote: string | null | undefined; versioned: boolean }) {
  const isGithub = props.remote === undefined || /github\.com/i.test(String(props.remote ?? ""));
  const want = props.versioned && isGithub && props.remote !== null;
  const { data, error, loading } = useApi<GithubData>(want ? keys.github(props.p) : null);
  const reason = githubReason(data);
  const muted = (t: string) => (
    <div data-testid="github-note" style={{ padding: "14px 18px", fontSize: 12, color: C.muted }}>
      {t}
    </div>
  );
  let body;
  if (!want) body = muted(props.remote ? "remote is not on GitHub." : "no GitHub remote.");
  else if (loading && !data) body = muted("asking gh…");
  else if (reason) body = muted("GitHub: " + reason + ".");
  else if (error) body = muted("GitHub data unavailable right now.");
  else if (data) {
    const prs = data.prs ?? [];
    const issues = data.issues ?? [];
    const runs = data.runs ?? [];
    const run = runs[0];
    const runColor = run ? (run.conclusion === "success" ? C.teal : run.conclusion ? C.coral : C.amber) : C.muted;
    const line: CSSProperties = {
      display: "grid",
      gridTemplateColumns: "56px minmax(0, 1fr) auto",
      gap: 12,
      alignItems: "center",
      minHeight: 32,
      padding: "4px 18px",
      borderBottom: "1px solid " + C.row,
      fontSize: 12,
    };
    body = (
      <div data-testid="github-block">
        <div style={{ padding: "10px 18px 6px", ...label10 }}>Pull requests · {prs.length} open</div>
        {prs.slice(0, 8).map((x) => (
          <div key={"pr" + x.number} style={line}>
            <span style={{ color: C.violet }}>#{x.number}</span>
            <span className="pj-trunc">{x.title}</span>
            <span style={{ color: C.muted }}>{x.updatedAt ? rel(x.updatedAt) : x.state}</span>
          </div>
        ))}
        <div style={{ padding: "10px 18px 6px", ...label10 }}>Issues · {issues.length} open</div>
        {issues.slice(0, 8).map((x) => (
          <div key={"is" + x.number} style={line}>
            <span style={{ color: C.coral }}>#{x.number}</span>
            <span className="pj-trunc">{x.title}</span>
            <span style={{ color: C.muted }}>{x.updatedAt ? rel(x.updatedAt) : x.state}</span>
          </div>
        ))}
        <div style={{ padding: "10px 18px 6px", ...label10 }}>CI · latest run</div>
        {run ? (
          <div style={line}>
            <span style={{ color: runColor }}>{run.conclusion === "success" ? "✓" : run.conclusion ? "✕" : "…"}</span>
            <span className="pj-trunc">{run.name}</span>
            <span style={{ color: runColor }}>{run.conclusion ?? run.status}</span>
          </div>
        ) : (
          <div style={{ padding: "4px 18px 12px", fontSize: 12, color: C.muted }}>no runs.</div>
        )}
      </div>
    );
  }
  return (
    <Panel title="Remote" aside={data?.fetchedAt ? "gh · " + rel(data.fetchedAt) : "github"} label="GitHub">
      {body}
    </Panel>
  );
}

export function OverviewTab(props: {
  p: string;
  detail: ProjectDetail;
  versioned: boolean;
  lastCommit: Commit | null;
  commitsLoading: boolean;
  sessions: SessionSummary[];
  tracker: TrackerResponse | undefined;
  notes: Note[];
  active: Server[];
  recent: Server[];
  day: { day: string; files: number } | null;
  onCloseDay: () => void;
  openItem: (r: string) => void;
  goReview: () => void;
  goTab: (t: string) => void;
}) {
  const { p, detail, lastCommit, sessions, notes, active, recent, openItem } = props;
  const now = Date.now();
  const tasks: Task[] = props.tracker?.tasks ?? [];
  const pending = tasks.filter((t) => t.latestSnapshot?.verdict === "pending");
  const latest = sessions[0] ?? null;
  const sessions30 = sessions.filter((s) => ms(s.start) >= now - 30 * 86400000).length;
  const dirty = detail.overview.dirty;
  const total = active.length + recent.filter((s) => !active.some((a) => a.key === s.key)).length;
  const tCount = tasks.length || 1;

  const cont: { kind: string; title: string; sub: string; style: CSSProperties; r: string }[] = [];
  if (latest)
    cont.push({
      kind: "session",
      title: latest.title || "Claude session",
      sub: [latest.profile, rel(latest.end || latest.start), latest.turns + " turns", (latest.edited?.length ?? 0) + " files edited"]
        .filter(Boolean)
        .join(" · "),
      style: pill(C.violet),
      r: ref(p, "session", latest.id),
    });
  if (lastCommit)
    cont.push({
      kind: "commit",
      title: lastCommit.msg || "commit",
      sub: [lastCommit.vcs + " " + String(lastCommit.id).slice(0, 10), lastCommit.author, rel(lastCommit.ts)].filter(Boolean).join(" · "),
      style: pill(C.amber),
      r: ref(p, "commit", lastCommit.id),
    });
  tasks
    .filter((t) => t.status === "in_progress")
    .forEach((t) => {
      const f = props.tracker?.features.find((x) => x.id === t.featureId);
      cont.push({
        kind: "in progress",
        title: t.title,
        sub: (f ? f.title + " · " : "") + (t.assignee || "unassigned"),
        style: pill(C.amber, true),
        r: ref(p, "task", t.id),
      });
    });

  const lastTouched = [detail.project.lastChangeAt, latest && (latest.end || latest.start), lastCommit?.ts]
    .filter((x): x is string => !!x)
    .sort((a, b) => ms(b) - ms(a))[0];

  const sortedNotes = notes.slice().sort((a, b) => ms(b.date) - ms(a.date));
  const noteTarget = (n: Note) => {
    const r = parseRef(n.ref, p);
    if (!r) return n.ref;
    if (r.type === "task") return tasks.find((t) => t.id === r.id)?.title ?? r.id;
    if (r.type === "file" || r.type === "image") return r.id + (n.line ? ":" + n.line : "");
    return r.id;
  };

  const milestones = (props.tracker?.milestones ?? detail.overview.milestones ?? [])
    .slice()
    .sort((a, b) => String(a.due ?? "9999").localeCompare(String(b.due ?? "9999")));
  const readme = detail.overview.readme;

  return (
    <div data-testid="tab-overview">
      {props.day ? <DayPanel p={p} day={props.day.day} filesCount={props.day.files} onClose={props.onCloseDay} openItem={openItem} /> : null}

      <div
        data-testid="vitals"
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(150px, 1fr))",
          borderTop: "1px solid " + C.hair,
          borderLeft: "1px solid " + C.hair,
          background: C.panel,
        }}
      >
        <div style={tile}>
          <div style={label10}>Last commit</div>
          <div className="pj-trunc" style={big(C.amber)}>
            {lastCommit ? rel(lastCommit.ts) : props.commitsLoading ? "…" : "never"}
          </div>
          <div className="pj-trunc" style={sub}>
            {lastCommit ? lastCommit.vcs + " · " + String(lastCommit.id).slice(0, 10) : props.versioned ? "no commits" : "no version control"}
          </div>
        </div>
        <div style={tile}>
          <div style={label10}>Uncommitted</div>
          <div style={big(dirty.length ? C.amber : C.text)}>{dirty.length}</div>
          <div className="pj-trunc" style={sub}>
            {!props.versioned ? "not versioned" : dirty.length ? "uncommitted ±" : "clean"}
          </div>
        </div>
        <div style={tile}>
          <div style={label10}>Sessions · 30d</div>
          <div style={big(C.violet)}>{sessions30}</div>
          <div className="pj-trunc" style={sub}>
            {latest ? "latest " + rel(latest.end || latest.start) : "none in this folder"}
          </div>
        </div>
        <div style={tile}>
          <div style={label10}>Servers</div>
          <div style={big(C.teal)}>{active.length + " / " + total}</div>
          <div className="pj-trunc" style={sub}>
            {active
              .filter((s) => s.kind !== "helper")
              .map((s) => ":" + s.port)
              .join(" ") || "nothing listening"}
          </div>
        </div>
        <div style={tile}>
          <div style={label10}>Tasks · {tasks.filter((t) => t.status !== "done").length} open</div>
          <div style={{ display: "flex", height: 8, marginTop: 14, background: C.raised }}>
            {STATUSES.map((s) => {
              const k = tasks.filter((t) => t.status === s).length;
              return (
                <span
                  key={s}
                  title={statusLabel(s) + ": " + k}
                  style={{ display: "block", height: "100%", width: ((k / tCount) * 100).toFixed(2) + "%", background: statusColor(s) }}
                />
              );
            })}
          </div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: "4px 10px", marginTop: 8, fontSize: 10, color: C.muted }}>
            {STATUSES.map((s) => (
              <span key={s}>
                <span style={{ display: "inline-block", width: 6, height: 6, background: statusColor(s) }} /> {statusLabel(s)}{" "}
                {tasks.filter((t) => t.status === s).length}
              </span>
            ))}
          </div>
        </div>
        <button
          type="button"
          className="pj-hover"
          onClick={props.goReview}
          aria-label="Show snapshots awaiting review in the tracker"
          style={{
            display: "block",
            minWidth: 0,
            padding: "14px 16px",
            background: "transparent",
            border: 0,
            borderRight: "1px solid " + C.hair,
            borderBottom: "1px solid " + C.hair,
            color: C.text,
            textAlign: "left",
            cursor: "pointer",
          }}
        >
          <span style={{ display: "block", ...label10 }}>Awaiting review →</span>
          <span style={{ display: "block", ...big(C.ivory) }}>{pending.length}</span>
          <span className="pj-trunc" style={{ display: "block", ...sub }}>
            {pending.length
              ? pending.map((t) => t.title + " v" + t.latestSnapshot!.version).join(", ")
              : tasks.some((t) => t.latestSnapshot)
                ? "all snapshots reviewed"
                : "no snapshots"}
          </span>
        </button>
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "minmax(0, 3fr) minmax(0, 2fr)", gap: 18, marginTop: 18, alignItems: "start" }}>
        <div style={{ display: "grid", gap: 18, minWidth: 0 }}>
          <Panel
            title={
              <>
                Continue <span style={{ color: C.muted }}>where you left off</span>
              </>
            }
            aside={"last touched " + (lastTouched ? rel(lastTouched) : "—")}
            label="Continue"
          >
            {cont.map((c) => (
              <button
                key={c.r}
                type="button"
                className="pj-hover"
                onClick={() => openItem(c.r)}
                style={{
                  ...rowBtn,
                  minHeight: 52,
                  display: "grid",
                  gridTemplateColumns: "96px minmax(0, 1fr) auto",
                  gap: 14,
                  alignItems: "center",
                  padding: "10px 18px",
                }}
              >
                <span style={c.style}>{c.kind}</span>
                <span style={{ minWidth: 0 }}>
                  <span className="pj-trunc" style={{ display: "block", fontSize: 13 }}>
                    {c.title}
                  </span>
                  <span className="pj-trunc" style={{ display: "block", fontSize: 11, color: C.muted, marginTop: 2 }}>
                    {c.sub}
                  </span>
                </span>
                <span style={{ color: C.muted, fontSize: 12 }}>→</span>
              </button>
            ))}
            {cont.length === 0 ? <div style={{ padding: "16px 18px", fontSize: 14, color: C.muted }}>nothing in flight yet.</div> : null}
          </Panel>

          <DirtyPanel p={p} files={dirty} versioned={props.versioned} openItem={openItem} />

          {readme ? (
            <Panel
              title={
                <>
                  Readme <span style={{ color: C.muted }}>· {readme.path}</span>
                </>
              }
              label="Readme"
            >
              <pre className="pj-readme">{readme.text.split("\n").slice(0, 80).join("\n")}</pre>
              <div style={{ padding: "0 18px 14px" }}>
                <button
                  type="button"
                  className="pj-hover"
                  onClick={() => openItem(ref(p, "file", readme.path))}
                  style={{ minHeight: 36, padding: "0 12px", background: "transparent", color: C.amber, border: "1px solid " + C.hair, borderRadius: 3, cursor: "pointer" }}
                >
                  read {base(readme.path)} →
                </button>
              </div>
            </Panel>
          ) : null}
        </div>

        <div style={{ display: "grid", gap: 18, minWidth: 0 }}>
          <Panel title="Open notes" aside={notes.length + " unresolved"} label="Open notes">
            {sortedNotes.map((n) => {
              const r = parseRef(n.ref, p);
              const type = r?.type ?? "note";
              return (
                <button
                  key={n.id}
                  type="button"
                  className="pj-hover"
                  onClick={() => openItem(n.ref)}
                  style={{ ...rowBtn, display: "block", padding: "10px 18px" }}
                >
                  <span style={{ display: "block", fontSize: 12, lineHeight: 1.5 }}>“{n.text}”</span>
                  <span className="pj-trunc" style={{ display: "block", fontSize: 11, color: C.muted, marginTop: 4 }}>
                    <span style={{ color: kindColor(type), textTransform: "uppercase", letterSpacing: ".1em", fontSize: 10 }}>{type}</span>{" "}
                    {noteTarget(n)} · {n.author} · {rel(n.date)}
                  </span>
                </button>
              );
            })}
            {notes.length === 0 ? <div style={{ padding: "16px 18px", fontSize: 14, color: C.muted }}>no open notes on this project.</div> : null}
          </Panel>

          <GithubBlock p={p} remote={detail.project.remote} versioned={props.versioned} />

          <Panel title="Milestones" aside={milestones.length + " planned"} label="Milestones">
            {milestones.map((m) => {
              const fids = new Set(m.featureIds ?? []);
              const ts = tasks.filter((t) => (t.featureId && fids.has(t.featureId)) || t.milestoneId === m.id);
              const done = ts.filter((t) => t.status === "done").length;
              let due = "no date";
              if (m.due) {
                const dd = dayParts(m.due);
                const [y, mo, d] = m.due.split("-").map(Number);
                const left = Math.round((Date.UTC(y, mo - 1, d) - now) / 86400000);
                due = "due " + dd.d + " " + dd.mon + (left >= 0 ? " (" + left + "d)" : " (overdue)");
              }
              return (
                <button
                  key={m.id}
                  type="button"
                  className="pj-hover"
                  onClick={() => props.goTab("tracker")}
                  style={{
                    ...rowBtn,
                    minHeight: 44,
                    display: "grid",
                    gridTemplateColumns: "minmax(0, 1fr) auto",
                    gap: 12,
                    alignItems: "center",
                    padding: "6px 18px",
                  }}
                >
                  <span className="pj-trunc" style={{ fontSize: 13 }}>
                    {m.title}
                  </span>
                  <span style={{ fontSize: 11, color: C.muted, whiteSpace: "nowrap" }}>
                    {due} · {done}/{ts.length} done
                  </span>
                </button>
              );
            })}
            {milestones.length === 0 ? <div style={{ padding: "16px 18px", fontSize: 14, color: C.muted }}>no milestones yet.</div> : null}
          </Panel>
        </div>
      </div>
    </div>
  );
}
