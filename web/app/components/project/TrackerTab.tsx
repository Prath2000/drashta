// Tracker tab: this project's milestones → features → tasks, with progress.
// "Open board →" and "Awaiting review →" go to the Tracker screen.
import type { Feature, Task, TrackerResponse } from "~/lib/types";
import { ref } from "./data";
import { dayParts, pill, statusColor, statusLabel } from "./fmt";
import { C, EmptyBox } from "./ui";

interface FeatVM {
  key: string;
  title: string;
  summary: string;
  tasks: Task[];
}

export function TrackerTab(props: {
  p: string;
  tracker: TrackerResponse | undefined;
  loading: boolean;
  openItem: (r: string) => void;
  goBoard: () => void;
  goReview: () => void;
}) {
  const { p, tracker, openItem } = props;
  if (props.loading && !tracker) return <div style={{ fontSize: 12, color: C.muted }}>reading the tracker…</div>;
  const tasks = tracker?.tasks ?? [];
  const feats = tracker?.features ?? [];
  const now = Date.now();
  const featVM = (f: Feature): FeatVM => ({ key: f.id, title: f.title, summary: f.summary ?? "", tasks: tasks.filter((t) => t.featureId === f.id) });
  const used = new Set<string>();
  const groups = (tracker?.milestones ?? [])
    .slice()
    .sort((a, b) => String(a.due ?? "9999").localeCompare(String(b.due ?? "9999")))
    .map((m) => {
      const fs = (m.featureIds ?? []).map((id) => feats.find((f) => f.id === id)).filter((f): f is Feature => !!f);
      feats.filter((f) => f.milestoneId === m.id && !fs.includes(f)).forEach((f) => fs.push(f));
      fs.forEach((f) => used.add(f.id));
      const ts = tasks.filter((t) => fs.some((f) => f.id === t.featureId));
      const done = ts.filter((t) => t.status === "done").length;
      let due = "no date";
      if (m.due) {
        const dd = dayParts(m.due);
        const [y, mo, d] = m.due.split("-").map(Number);
        const left = Math.round((Date.UTC(y, mo - 1, d) - now) / 86400000);
        due = "due " + dd.d + " " + dd.mon + (left >= 0 ? " (" + left + "d)" : " (overdue)");
      }
      return { key: m.id, kicker: "Milestone", title: m.title, due, progress: done + "/" + ts.length + " done", features: fs.map(featVM) };
    });
  const loose = feats.filter((f) => !used.has(f.id));
  const orphan = tasks.filter((t) => !feats.some((f) => f.id === t.featureId));
  if (loose.length || orphan.length) {
    const fsv = loose.map(featVM);
    if (orphan.length) fsv.push({ key: "_none", title: "No feature", summary: "", tasks: orphan });
    groups.push({ key: "_unscheduled", kicker: "Unscheduled", title: "Outside milestones", due: "no date", progress: "", features: fsv });
  }
  const open = tasks.filter((t) => t.status !== "done").length;
  const review = tasks.filter((t) => t.latestSnapshot?.verdict === "pending").length;

  return (
    <div data-testid="tab-tracker">
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: 12, flexWrap: "wrap", marginBottom: 14 }}>
        <div style={{ fontSize: 11, color: C.muted }}>
          {tasks.length} tasks · {open} open · {review} awaiting review
        </div>
        <div style={{ display: "flex", gap: 8 }}>
          <button
            type="button"
            className="pj-hover"
            onClick={props.goReview}
            style={{ height: 44, padding: "0 18px", background: "transparent", color: C.ivory, border: "1px solid " + C.hair, borderRadius: 3, cursor: "pointer" }}
          >
            Awaiting review →
          </button>
          <button
            type="button"
            className="pj-hover"
            onClick={props.goBoard}
            style={{ height: 44, padding: "0 18px", background: "transparent", color: C.amber, border: "1px solid " + C.amber, borderRadius: 3, cursor: "pointer" }}
          >
            Open board →
          </button>
        </div>
      </div>
      {groups.map((m) => (
        <section key={m.key} aria-label={m.title} style={{ border: "1px solid " + C.hair, background: C.panel, marginBottom: 14 }}>
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              alignItems: "baseline",
              gap: 12,
              padding: "12px 16px",
              borderBottom: "1px solid " + C.hair,
            }}
          >
            <span style={{ display: "flex", alignItems: "baseline", gap: 12, minWidth: 0 }}>
              <span style={{ fontSize: 10, letterSpacing: ".12em", textTransform: "uppercase", color: C.muted }}>{m.kicker}</span>
              <span className="pj-trunc" style={{ fontSize: 18, color: C.text }}>
                {m.title}
              </span>
            </span>
            <span style={{ fontSize: 11, color: C.muted, whiteSpace: "nowrap" }}>
              {m.due}
              {m.progress ? " · " + m.progress : ""}
            </span>
          </div>
          {m.features.map((f) => (
            <div key={f.key} style={{ padding: "10px 16px 6px", borderBottom: "1px solid " + C.row }}>
              <div style={{ display: "flex", gap: 12, alignItems: "baseline" }}>
                <span style={{ fontSize: 13 }}>{f.title}</span>
                <span className="pj-trunc" style={{ fontSize: 11, color: C.muted }}>
                  {f.summary}
                </span>
              </div>
              <div style={{ marginTop: 6 }}>
                {f.tasks.map((t) => (
                  <button
                    key={t.id}
                    type="button"
                    className="pj-hover"
                    onClick={() => openItem(ref(p, "task", t.id))}
                    style={{
                      width: "100%",
                      minHeight: 36,
                      display: "grid",
                      gridTemplateColumns: "110px minmax(0, 1fr) 140px 24px",
                      gap: 12,
                      alignItems: "center",
                      padding: "4px 0",
                      background: "transparent",
                      border: 0,
                      borderTop: "1px solid " + C.raised,
                      color: C.text,
                      textAlign: "left",
                      cursor: "pointer",
                    }}
                  >
                    <span style={pill(statusColor(t.status))}>{statusLabel(t.status)}</span>
                    <span className="pj-trunc">{t.title}</span>
                    <span className="pj-trunc" style={{ color: C.muted }}>
                      {t.assignee || "unassigned"}
                    </span>
                    <span style={{ color: C.dim, textAlign: "right" }}>→</span>
                  </button>
                ))}
                {f.tasks.length === 0 ? <div style={{ padding: "6px 0", color: C.muted, fontSize: 11 }}>No tasks yet.</div> : null}
              </div>
            </div>
          ))}
        </section>
      ))}
      {groups.length === 0 ? (
        <EmptyBox testId="tracker-empty">No milestones or tasks for this project yet — start them on the board.</EmptyBox>
      ) : null}
    </div>
  );
}
