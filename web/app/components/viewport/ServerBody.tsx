// Server preview body (02-ui-spec §2.4 "server"; prototype ViewMedia.dc.html).
// Status, kind, process, pid, cmd, cwd; a browser-like frame; Open in Safari ↗;
// Kill / Start with a 5 s confirm; the project's processes.
//
// The frame shows the LIVE app in an iframe (this replaces decision Q8, which
// showed only the last capture). GET /servers/embed?port=N says what to draw:
//   - http and frameable   -> the iframe (Live), or the last capture (Capture toggle)
//   - http, not frameable  -> the last capture, plus one line saying why
//   - not http             -> "Not a web page" (a helper, or any non-HTTP listener)
//   - still loading        -> the last capture (no flash of an empty frame)
//   - not running          -> "This site can't be reached"
// Plash readout mode (the wallpaper) always gets the capture: it never runs app JS.
// The iframe sandbox has NO allow-top-navigation, so the app can never navigate
// the drashta window. Expand makes the SAME `.vp-browser` element fixed and full
// window (so the iframe keeps running); Esc collapses it (Esc layer "app").
import { useEffect, useRef, useState } from "react";
import { api, qs } from "~/lib/api";
import { findProject, useBoot } from "~/lib/boot";
import { invalidate, useApi } from "~/lib/query";
import { formatRef } from "~/lib/refs";
import { useEscapeLayer } from "~/lib/shortcuts";
import { toast, useStore } from "~/lib/store";
import type { EmbedInfo, Server, ServersResponse } from "~/lib/types";
import { useItem } from "~/lib/url";
import type { ViewportBodyProps } from "./registry";
import { baseRef, MediaNotes, useNative, useNotes } from "./common";
import { fmtWhen } from "./fmt";
import type { ServerObj } from "./vtypes";

export const CONFIRM_MS = 5000;

/** What the iframe may do. No allow-top-navigation: the app must never navigate the drashta / Plash window.
 *  No allow-popups / allow-modals / allow-downloads: nothing in drashta needs them for a previewed dev app. */
export const FRAME_SANDBOX = "allow-scripts allow-same-origin allow-forms";

export function ServerBody({ refStr, refObj, resolved }: ViewportBodyProps) {
  const p = resolved.projectId || refObj.projectId;
  const ref = baseRef(refObj);
  const obj = (resolved.obj ?? {}) as ServerObj;
  const port = Number(obj.port ?? refObj.id);
  const { openItem } = useItem();
  const { data: boot } = useBoot();
  const native = useNative(refStr);
  const notes = useNotes(ref, resolved.notes ?? []);
  const projName = findProject(boot, p)?.name ?? p;

  const list = useApi<ServersResponse>("/servers" + qs({ project: p }));
  const live = list.data?.active.find((s) => s.port === port);
  const recent = list.data?.recent.find((s) => s.port === port);
  const srv: Partial<Server> = live ?? recent ?? obj;
  const running = list.data ? !!live : !!(obj.running ?? obj.alive);
  const helper = srv.kind === "helper";

  // ---------- live frame ----------
  const readout = useStore((s) => s.mode === "readout");
  const embed = useApi<EmbedInfo>(running ? "/servers/embed" + qs({ port }) : null);
  const info = embed.data;
  // Not a web page: the server says so; if it cannot answer, fall back to the kind.
  const notWeb = running && (info ? !info.http : !!embed.error && helper);
  const frameable = running && !!info && info.http && info.frameable;
  const noEmbed = running && !!info && info.http && !info.frameable;
  const [view, setView] = useState<"live" | "capture">("live");
  const showFrame = frameable && !readout && view === "live";
  const [frameKey, setFrameKey] = useState(0);
  const [expanded, setExpanded] = useState(false);
  // Expand needs the answer first, so a helper never shows the button for a moment.
  const expandable = running && !notWeb && !readout && (!!info || !!embed.error);
  const full = expanded && expandable;
  // Expand / view are local: a new port starts normal and live.
  useEffect(() => {
    setExpanded(false);
    setView("live");
  }, [port]);
  // Esc collapses the expanded app first; the next Esc closes the drawer as before.
  useEscapeLayer("app", full, () => setExpanded(false));

  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);
  // Reset a pending confirm when the state flips.
  useEffect(() => {
    setConfirm(false);
  }, [running]);

  const refresh = () => {
    invalidate("/servers"); // also refetches /servers/embed
    invalidate("/resolve");
    invalidate("/projects/" + encodeURIComponent(p));
    invalidate("/home");
  };

  const act = async () => {
    if (!confirm) {
      setConfirm(true);
      if (timer.current) clearTimeout(timer.current);
      timer.current = setTimeout(() => setConfirm(false), CONFIRM_MS);
      return;
    }
    if (timer.current) clearTimeout(timer.current);
    setConfirm(false);
    setBusy(true);
    try {
      if (running) {
        await api.killServer(Number(srv.pid), String(srv.key ?? obj.key ?? ""));
        toast(":" + port + " stopped");
      } else {
        const out = await api.restartServer(String(srv.key ?? obj.key ?? ""));
        toast(":" + port + " starting · log " + out.log);
      }
      refresh();
    } catch (e) {
      toast(e instanceof Error ? e.message : "server action failed", { error: true });
    } finally {
      setBusy(false);
    }
  };

  const cap = obj.lastCapture ?? null;
  const capSrc = cap?.path ? api.rawUrl(p, cap.path) : cap?.url ?? "";
  const procs = [...(list.data?.active ?? []).map((s) => ({ s, on: true })), ...(list.data?.recent ?? []).filter((r) => !list.data?.active.some((a) => a.port === r.port)).map((s) => ({ s, on: false }))];

  let actLabel = running ? "■ Kill process" : "▶ Start";
  if (confirm) actLabel = running ? "Confirm kill :" + port : "Confirm start :" + port;

  return (
    <div className="vp-scroll" data-testid="server-body">
      <div className="vp-pad">
        <div className="vp-stack">
          <div className="vp-row" style={{ gap: 10 }}>
            <span className={"vp-status" + (running ? " on" : "")} data-testid="server-status">
              ● {running ? "running" : "stopped"}
            </span>
            <span className="vp-label">
              {helper ? "helper process" : "dev server"} · {srv.name ?? "—"} · pid {srv.pid ?? "—"}
            </span>
          </div>

          <div className={"vp-browser" + (full ? " full" : "")}>
            <div className="tabs">
              <span className="tl" />
              <span className="tl" />
              <span className="tl" />
              <span className="tab">{info?.title || srv.title || "localhost:" + port}</span>
            </div>
            <div className="bar">
              <button type="button" className="nav" aria-label="Back" disabled>
                ←
              </button>
              <button
                type="button"
                className="nav"
                aria-label={showFrame ? "Reload page" : "Reload status"}
                onClick={() => {
                  setFrameKey((k) => k + 1);
                  refresh();
                }}
              >
                ↻
              </button>
              <div className="url">
                <span className="vp-faint" style={{ fontSize: 12 }}>
                  http://
                </span>
                <span>localhost:{port}</span>
                <span className="vp-faint" style={{ fontSize: 12 }}>
                  /
                </span>
              </div>
              {frameable && !readout ? (
                <div className="vp-seg" role="group" aria-label="Preview">
                  <button type="button" aria-pressed={view === "live"} onClick={() => setView("live")}>
                    Live
                  </button>
                  <button type="button" aria-pressed={view === "capture"} onClick={() => setView("capture")}>
                    Capture
                  </button>
                </div>
              ) : null}
              {expandable ? (
                <button
                  type="button"
                  className="nav"
                  aria-label={full ? "Collapse" : "Expand"}
                  title={full ? "Collapse (Esc)" : "Expand to the full window"}
                  onClick={() => setExpanded(!full)}
                >
                  {full ? "⤡" : "⤢"}
                </button>
              ) : null}
            </div>
            <div className="page" data-testid="server-page">
              {!running ? (
                <div className="msg">
                  <div className="big">This site can’t be reached</div>
                  <div className="vp-muted" style={{ fontSize: 11 }}>
                    localhost refused to connect on port {port} — the process is stopped.
                  </div>
                </div>
              ) : notWeb ? (
                <div className="msg">
                  <div className="big muted">Not a web page</div>
                  <div className="vp-faint">
                    {srv.name ?? "This process"} is {helper ? "a helper " : ""}listening on {port}. Nothing to render.
                  </div>
                </div>
              ) : null}
              {noEmbed ? (
                <div className="note" data-testid="embed-note">
                  This app refuses to be embedded{info?.reason ? " (" + info.reason + ")" : ""} — use Open in Safari ↗
                </div>
              ) : null}
              {showFrame ? (
                <iframe
                  key={frameKey}
                  src={"http://localhost:" + port + "/"}
                  title={"localhost:" + port}
                  sandbox={FRAME_SANDBOX}
                  data-testid="server-frame"
                />
              ) : capSrc && !notWeb ? (
                <div>
                  <img src={capSrc} alt={"Last capture of localhost:" + port} />
                  <div className="cap">
                    <span>Last capture{cap?.path ? " · " + cap.path.split("/").pop() : ""}</span>
                    <span>{cap?.captured ? "captured " + fmtWhen(cap.captured) : ""}</span>
                  </div>
                </div>
              ) : running && !notWeb ? (
                <div className="msg">
                  <div className="big">No capture yet</div>
                  <div className="vp-muted" style={{ fontSize: 11 }}>
                    Open it in Safari to see the live page.
                  </div>
                </div>
              ) : null}
            </div>
          </div>

          <div className="vp-grid c2">
            <div>
              <div className="vp-label">Command</div>
              <div style={{ wordBreak: "break-all" }}>{srv.cmd ?? "—"}</div>
            </div>
            <div>
              <div className="vp-label">Folder</div>
              <div style={{ wordBreak: "break-all" }}>{srv.cwd ?? "—"}</div>
            </div>
          </div>

          <div className="vp-row">
            <button type="button" className="vp-btn big primary" onClick={() => void native("safari")}>
              Open in Safari ↗
            </button>
            <button
              type="button"
              className={"vp-btn big " + (running ? "danger" : "go") + (confirm ? " on" : "")}
              disabled={busy || (!running && !(srv.key ?? obj.key))}
              onClick={() => void act()}
            >
              {busy ? "…" : actLabel}
            </button>
          </div>

          <div className="vp-col" style={{ gap: 4 }} data-testid="procs">
            <div className="vp-label">Processes · {projName}</div>
            <div className="vp-procs head">
              <div>port</div>
              <div>pid</div>
              <div>cmd</div>
              <div>kind</div>
            </div>
            {procs.map(({ s, on }) => {
              const cur = s.port === port;
              return (
                <button
                  key={s.key + ":" + s.port}
                  type="button"
                  className={"vp-proc dr-hover" + (cur ? " cur" : "")}
                  aria-current={cur}
                  onClick={() => {
                    if (!cur) openItem(formatRef({ type: "server", projectId: p, id: String(s.port) }));
                  }}
                >
                  <span className={on ? "vp-teal" : "vp-faint"}>{s.port}</span>
                  <span>{s.pid}</span>
                  <span className="vp-ell">{s.cmd}</span>
                  <span className="vp-muted">{s.kind + (on ? "" : " · off")}</span>
                </button>
              );
            })}
          </div>
        </div>
        <MediaNotes refStr={ref} projectId={p} notes={notes} />
      </div>
    </div>
  );
}
