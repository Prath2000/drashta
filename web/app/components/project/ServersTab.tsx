// Servers tab: active listeners and recent (stopped) ones for this project.
// Preview opens the viewport, Safari runs POST /open, Stop needs a confirm click.
import type { CSSProperties } from "react";
import type { Server } from "~/lib/types";
import { ref, runNative } from "./data";
import { C, EmptyBox, KillButton, smallBtn, StartButton } from "./ui";

const COLS = "90px 70px 110px minmax(160px, 1fr) 70px 280px";

export function ServersTab(props: { p: string; active: Server[]; recent: Server[]; loading: boolean; openItem: (r: string) => void }) {
  const { p, openItem } = props;
  const activeKeys = new Set(props.active.map((s) => s.key));
  const rows = [
    ...props.active.map((s) => ({ s, running: true })),
    ...props.recent.filter((s) => !activeKeys.has(s.key)).map((s) => ({ s, running: false })),
  ].sort((a, b) => Number(b.running) - Number(a.running) || Number(a.s.kind === "helper") - Number(b.s.kind === "helper") || a.s.port - b.s.port);

  if (!rows.length)
    return props.loading ? (
      <div style={{ fontSize: 12, color: C.muted }}>reading servers…</div>
    ) : (
      <EmptyBox testId="servers-empty">No localhost listeners belong to this project.</EmptyBox>
    );

  return (
    <div data-testid="tab-servers" style={{ border: "1px solid " + C.hair, background: C.panel, overflowX: "auto" }}>
      <div
        style={{
          minWidth: 840,
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
        <span>Port</span>
        <span>PID</span>
        <span>Process</span>
        <span>Command</span>
        <span>Kind</span>
        <span style={{ textAlign: "right" }}>Actions</span>
      </div>
      {rows.map(({ s, running }) => {
        const btn: CSSProperties = { ...smallBtn, color: running ? C.text : C.muted, cursor: running ? "pointer" : "not-allowed" };
        return (
          <div
            key={s.key}
            data-testid={"server-" + s.port}
            style={{
              minWidth: 840,
              display: "grid",
              gridTemplateColumns: COLS,
              gap: 12,
              alignItems: "center",
              minHeight: 52,
              padding: "6px 16px",
              borderBottom: "1px solid " + C.row,
              opacity: s.kind === "helper" ? 0.6 : 1,
            }}
          >
            <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <span
                aria-hidden="true"
                style={{
                  width: 8,
                  height: 8,
                  borderRadius: "50%",
                  ...(running ? { background: C.teal } : { border: "1px solid " + C.dim }),
                }}
              />
              <span>:{s.port}</span>
              <span className="sr-only">{running ? "running" : "stopped"}</span>
            </span>
            <span style={{ color: C.muted }}>{running && s.pid ? s.pid : "—"}</span>
            <span className="pj-trunc">{s.name || "—"}</span>
            <span style={{ minWidth: 0 }}>
              <span className="pj-trunc" style={{ display: "block" }}>
                {s.title || "localhost:" + s.port}
              </span>
              <span className="pj-trunc" style={{ display: "block", color: C.muted, fontSize: 11 }}>
                {s.cmd}
                {!running && s.lastSeen ? (s.cmd ? " · " : "") + "stopped" : ""}
              </span>
            </span>
            <span style={{ color: C.muted }}>{s.kind || "dev"}</span>
            <span style={{ display: "flex", gap: 6, justifyContent: "flex-end" }}>
              <button
                type="button"
                className="pj-hover pj-dim"
                disabled={!running}
                onClick={() => openItem(ref(p, "server", s.port))}
                aria-label={"Preview localhost:" + s.port}
                style={btn}
              >
                Preview
              </button>
              <button
                type="button"
                className="pj-hover pj-dim"
                disabled={!running}
                onClick={() => runNative(ref(p, "server", s.port), "safari")}
                aria-label={"Open localhost:" + s.port + " in Safari"}
                style={btn}
              >
                Safari ↗
              </button>
              {running ? (
                <KillButton project={p} server={s} idle="Stop" style={{ ...smallBtn, color: C.coral, minWidth: 64 }} />
              ) : (
                <StartButton project={p} server={s} label="Start" style={{ ...smallBtn, color: C.teal, minWidth: 64 }} />
              )}
            </span>
          </div>
        );
      })}
    </div>
  );
}
