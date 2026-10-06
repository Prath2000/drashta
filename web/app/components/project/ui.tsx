// Small shared pieces of the project page.
import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import type { Server } from "~/lib/types";
import { killServer, restartServer } from "./data";

export const C = {
  ground: "var(--ground)",
  panel: "var(--panel)",
  raised: "var(--raised)",
  hair: "var(--hairline)",
  faint: "var(--hairline-faint)",
  row: "#1f1e1b",
  select: "var(--select)",
  text: "var(--text)",
  muted: "var(--muted)",
  dim: "var(--dim)",
  amber: "var(--amber)",
  teal: "var(--teal)",
  violet: "var(--violet)",
  ivory: "var(--ivory)",
  coral: "var(--coral)",
};

export const label10: CSSProperties = { fontSize: 10, letterSpacing: ".12em", textTransform: "uppercase", color: C.muted };

export const h2Style: CSSProperties = {
  margin: 0,
  fontWeight: 400,
  fontSize: 11,
  letterSpacing: ".22em",
  textTransform: "uppercase",
  color: C.text,
};

/** A panel with a hairline border and an uppercase head line. */
export function Panel(props: { title: ReactNode; aside?: ReactNode; children: ReactNode; style?: CSSProperties; label?: string }) {
  return (
    <section aria-label={props.label} style={{ border: "1px solid " + C.hair, background: C.panel, ...props.style }}>
      <div
        style={{
          padding: "14px 18px 10px",
          borderBottom: "1px solid " + C.hair,
          display: "flex",
          alignItems: "baseline",
          justifyContent: "space-between",
          gap: 12,
        }}
      >
        <h2 style={h2Style}>{props.title}</h2>
        {props.aside != null ? <span style={{ ...label10, whiteSpace: "nowrap" }}>{props.aside}</span> : null}
      </div>
      {props.children}
    </section>
  );
}

/** Calm empty-state sentence in a dashed box (14px muted). */
export function EmptyBox({ children, testId }: { children: ReactNode; testId?: string }) {
  return (
    <div data-testid={testId} style={{ padding: "20px 22px", border: "1px dashed " + C.hair, fontSize: 14, lineHeight: 1.5, color: C.muted }}>
      {children}
    </div>
  );
}

export const smallBtn: CSSProperties = {
  minHeight: 36,
  padding: "0 12px",
  background: "transparent",
  border: "1px solid " + C.hair,
  borderRadius: 3,
  fontSize: 12,
  cursor: "pointer",
};

export const btn44: CSSProperties = {
  height: 44,
  padding: "0 16px",
  background: "transparent",
  color: C.text,
  border: "1px solid " + C.hair,
  borderRadius: 3,
  fontSize: 12,
  cursor: "pointer",
  display: "inline-flex",
  alignItems: "center",
  gap: 8,
};

/** "show all N" footer under a capped list. */
export function MoreRow(props: { label: string; total: number | string; onClick: () => void; color: string; busy?: boolean }) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 12,
        padding: "10px 16px",
        fontSize: 11,
        color: C.muted,
      }}
    >
      <span>{props.label}</span>
      <button type="button" className="pj-hover" onClick={props.onClick} disabled={props.busy} style={{ ...smallBtn, color: props.color }}>
        {props.busy ? "loading…" : "show all " + props.total}
      </button>
    </div>
  );
}

export const CONFIRM_MS = 5000;

/**
 * Stop button with a confirm step: the first click turns it into
 * "Confirm kill :5173" for 5 s; a second click inside that time kills
 * (POST /servers/kill with confirm:true). After 5 s it goes back.
 */
export function KillButton(props: { project: string; server: Server; idle: string; style: CSSProperties; armedStyle?: CSSProperties }) {
  const [armed, setArmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), CONFIRM_MS);
    return () => clearTimeout(t);
  }, [armed]);
  const onClick = async () => {
    if (busy) return;
    if (!armed) {
      setArmed(true);
      return;
    }
    setArmed(false);
    setBusy(true);
    await killServer(props.project, props.server);
    if (alive.current) setBusy(false);
  };
  const port = props.server.port;
  return (
    <button
      type="button"
      className="pj-hover"
      onClick={onClick}
      disabled={busy}
      aria-label={armed ? "Confirm: kill the server on port " + port : "Stop the server on port " + port}
      style={armed ? { ...props.style, ...(props.armedStyle ?? { background: "#2a1610" }) } : props.style}
    >
      {busy ? "stopping :" + port + "…" : armed ? "Confirm kill :" + port : props.idle}
    </button>
  );
}

/** Start (restart) a recent server: POST /servers/restart. */
export function StartButton(props: { project: string; server: Server; label: string; style: CSSProperties }) {
  const [busy, setBusy] = useState(false);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  return (
    <button
      type="button"
      className="pj-hover"
      disabled={busy}
      aria-label={"Start the server on port " + props.server.port}
      onClick={async () => {
        setBusy(true);
        await restartServer(props.project, props.server);
        if (alive.current) setBusy(false);
      }}
      style={props.style}
    >
      {busy ? "starting :" + props.server.port + "…" : props.label}
    </button>
  );
}
