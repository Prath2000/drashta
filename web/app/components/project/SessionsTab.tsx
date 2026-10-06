// Sessions tab: Claude sessions that ran in this folder or a subfolder,
// newest first, then the ones that only mention this project (reason
// "mention", decision S6).
import { useState } from "react";
import { api } from "~/lib/api";
import type { SessionSummary, SessionsResponse } from "~/lib/types";
import { CAP, keys, ref, usePaged } from "./data";
import { base, dur, fmtDT, ms } from "./fmt";
import { C, MoreRow } from "./ui";

const COLS = "minmax(200px, 1fr) 110px 110px 90px 60px 70px minmax(0, 200px)";

export function useSessions(p: string) {
  return usePaged<SessionsResponse, SessionSummary>(
    keys.sessions(p),
    (r) => ({ items: r.sessions, next: r.next }),
    (cursor) => api.sessions(p, cursor),
  );
}

export const isMention = (s: SessionSummary) => /^mention/i.test(s.reason || "");

/** Own sessions (not mentions), newest first. */
export function ownSessions(list: SessionSummary[]): SessionSummary[] {
  return list
    .filter((s) => !isMention(s))
    .slice()
    .sort((a, b) => ms(b.end || b.start) - ms(a.end || a.start));
}

export function SessionsTab(props: { p: string; name: string; path: string; sessions: ReturnType<typeof useSessions>; openItem: (r: string) => void }) {
  const { p, sessions, openItem } = props;
  const [all, setAll] = useState(false);
  const own = ownSessions(sessions.items);
  const mentioned = sessions.items.filter(isMention).sort((a, b) => ms(b.start) - ms(a.start));
  const shown = all ? own : own.slice(0, CAP);
  const more = sessions.next != null || (!all && own.length > CAP);
  const showAll = () => {
    setAll(true);
    if (sessions.next) void sessions.loadAll();
  };

  return (
    <div data-testid="tab-sessions">
      {sessions.loading ? <div style={{ fontSize: 12, color: C.muted }}>reading sessions…</div> : null}
      {sessions.error && !sessions.items.length ? (
        <div style={{ padding: "20px 22px", border: "1px dashed " + C.hair, fontSize: 14, color: C.muted }}>could not read sessions.</div>
      ) : null}
      {own.length ? (
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
            <span>Session · {own.length + (sessions.next ? "+" : "")} in this folder</span>
            <span>Profile</span>
            <span>Started</span>
            <span>Duration</span>
            <span style={{ textAlign: "right" }}>Turns</span>
            <span style={{ textAlign: "right" }}>Edited</span>
            <span>Tools</span>
          </div>
          {shown.map((s) => {
            const tools = Object.entries(s.tools || {})
              .sort((a, b) => b[1] - a[1])
              .slice(0, 3)
              .map(([k, v]) => k + " " + v)
              .join(" · ");
            const sub = s.reason && s.reason !== "cwd" ? s.reason + (s.cwd ? " " + base(s.cwd) : "") : "";
            return (
              <button
                key={s.id}
                type="button"
                className="pj-hover"
                onClick={() => openItem(ref(p, "session", s.id))}
                style={{
                  width: "100%",
                  minWidth: 780,
                  minHeight: 44,
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
                <span style={{ display: "flex", alignItems: "center", gap: 10, minWidth: 0 }}>
                  <span style={{ flex: "none", width: 2, height: 16, background: C.violet }} />
                  <span className="pj-trunc">{s.title || "Claude session"}</span>
                  {sub ? <span style={{ flex: "none", fontSize: 10, color: C.muted, letterSpacing: ".08em", textTransform: "uppercase" }}>{sub}</span> : null}
                </span>
                <span className="pj-trunc" style={{ color: C.violet }}>
                  {s.profile || "—"}
                </span>
                <span style={{ color: C.muted }}>{fmtDT(s.start)}</span>
                <span style={{ color: C.muted }}>{dur(s.start, s.end)}</span>
                <span style={{ textAlign: "right" }}>{s.turns}</span>
                <span style={{ textAlign: "right" }}>{s.edited?.length ?? 0}</span>
                <span className="pj-trunc" style={{ color: C.muted }}>
                  {tools || "—"}
                </span>
              </button>
            );
          })}
          {more && !(all && !sessions.next) ? (
            <MoreRow
              label={"newest " + shown.length + " of " + own.length + (sessions.next ? "+" : "") + " shown"}
              total={own.length + (sessions.next ? "+" : "")}
              color={C.violet}
              busy={sessions.busy}
              onClick={showAll}
            />
          ) : null}
        </div>
      ) : null}

      {!sessions.loading && !own.length && !sessions.error ? (
        <div data-testid="sessions-empty" style={{ border: "1px dashed " + C.hair, padding: "28px 28px 22px" }}>
          <div style={{ fontSize: 20, lineHeight: 1.3, color: C.text }}>
            No Claude sessions <span style={{ color: C.muted }}>ran in this folder.</span>
          </div>
          {mentioned.length ? (
            <p style={{ margin: "8px 0 0", color: C.muted, fontSize: 12, lineHeight: 1.6, maxWidth: 640 }}>
              drashta looks for transcripts under every Claude profile whose working directory is {props.path}. None matched, but {props.name} was
              discussed from elsewhere:
            </p>
          ) : null}
        </div>
      ) : null}

      {mentioned.length ? (
        <section aria-label="Mentioned in other sessions" style={{ marginTop: 18 }} data-testid="sessions-mentioned">
          <div style={{ fontSize: 10, letterSpacing: ".12em", textTransform: "uppercase", color: C.muted, marginBottom: 8 }}>
            Mentioned in other projects&apos; sessions · {mentioned.length}
          </div>
          <div style={{ borderTop: "1px solid " + C.hair }}>
            {mentioned.map((s) => (
              <button
                key={s.id}
                type="button"
                className="pj-hover"
                onClick={() => openItem(ref(p, "session", s.id))}
                style={{
                  width: "100%",
                  minHeight: 44,
                  display: "grid",
                  gridTemplateColumns: "140px minmax(0, 1fr) 130px 24px",
                  gap: 12,
                  alignItems: "center",
                  padding: "6px 0",
                  background: "transparent",
                  border: 0,
                  borderBottom: "1px solid " + C.row,
                  color: C.text,
                  textAlign: "left",
                  cursor: "pointer",
                }}
              >
                <span className="pj-trunc" style={{ color: C.violet }}>
                  ↳ {s.cwd ? base(s.cwd) : s.profile || "elsewhere"}
                </span>
                <span className="pj-trunc">{s.title || "Claude session"}</span>
                <span style={{ color: C.muted }}>{fmtDT(s.start)}</span>
                <span style={{ color: C.dim, textAlign: "right" }}>→</span>
              </button>
            ))}
          </div>
        </section>
      ) : null}
    </div>
  );
}
