// Header: the cartouche (name, tags, meta line, description), the right
// cluster (range, scan label, refresh, hide) and the row of native actions
// (02-ui-spec §5.1, §5.2).
import { useState, type CSSProperties } from "react";
import { api } from "~/lib/api";
import { hhmm } from "~/lib/format";
import { invalidate, useApi } from "~/lib/query";
import { scanFailed, scanStarted, setRange, toast, useStore } from "~/lib/store";
import type { ProjectSummary, Server, SessionSummary, Settings } from "~/lib/types";
import { errText, keys, projectRef, ref, runNative } from "./data";
import { hostOf, rel } from "./fmt";
import { btn44, C, KillButton, StartButton } from "./ui";

export function vcsList(v: ProjectSummary["vcs"] | undefined): string[] {
  if (!v) return [];
  return Array.isArray(v) ? v : [v];
}

const tag: CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  height: 20,
  padding: "0 7px",
  fontSize: 10,
  letterSpacing: ".10em",
  textTransform: "uppercase",
  color: C.muted,
  border: "1px solid " + C.hair,
  borderRadius: 2,
};

const btnBase: CSSProperties = {
  height: 36,
  padding: "0 14px",
  borderRadius: 3,
  fontSize: 12,
  display: "inline-flex",
  alignItems: "center",
  gap: 8,
  background: "transparent",
  cursor: "pointer",
};

const Icon = {
  refresh: (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3" aria-hidden="true">
      <path d="M13.5 8A5.5 5.5 0 1 1 11.8 4M12 1.5V4.2H9.3" />
    </svg>
  ),
  editor: (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3" aria-hidden="true">
      <path d="M5 4 1.5 8 5 12M11 4l3.5 4L11 12M9.5 2.5l-3 11" />
    </svg>
  ),
  finder: (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3" aria-hidden="true">
      <path d="M1.5 4h5l1.5 1.5h6.5v8h-13z" />
    </svg>
  ),
  terminal: (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3" aria-hidden="true">
      <rect x="1.5" y="2.5" width="13" height="11" />
      <path d="m4 6 2 2-2 2M8 10.5h4" />
    </svg>
  ),
  play: (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3" aria-hidden="true">
      <path d="M3 2.5v11l10-5.5z" />
    </svg>
  ),
};

/** Folder names in the hide list that mean this project ("name", "group/name", "group.name"). */
function hideNames(p: ProjectSummary): string[] {
  const name = p.path.split("/").filter(Boolean).pop() ?? p.id;
  const grp = p.path.split("/").filter(Boolean).slice(-2, -1)[0] ?? p.group;
  return [name, grp + "/" + name, grp + "." + name];
}

export function Header(props: {
  p: string;
  project: ProjectSummary;
  active: Server[];
  recent: Server[];
  latestSession: SessionSummary | null;
  openItem: (r: string) => void;
}) {
  const { p, project, active, recent, latestSession, openItem } = props;
  const range = useStore((s) => s.range);
  const scanning = useStore((s) => s.scan.running);
  const scanAt = useStore((s) => s.scan.at);
  const settings = useApi<Settings>(keys.settings());
  const [hiding, setHiding] = useState(false);

  const groupLabel = String(project.group || "");
  const vcs = vcsList(project.vcs);
  const segs: { text: string; color: string }[] = [];
  const add = (text: string | null | undefined, color: string = C.muted) => {
    if (text) segs.push({ text: (segs.length ? " · " : "") + text, color });
  };
  add(project.path);
  if (project.stack?.length) add(project.stack.join(", "));
  if (vcs.length) vcs.forEach((v) => add(v === "fossil" ? "fossil trunk" : "git" + (project.branch ? " " + project.branch : ""), C.text));
  else add("not under version control");
  if (vcs.includes("git") && project.remote !== undefined) {
    if (project.remote) add(hostOf(project.remote), C.teal);
    else add("no remote");
  }
  const desc = String(project.description ?? "").trim();
  const hidden = !!project.hidden;

  const refresh = async () => {
    if (scanning) return;
    scanStarted(true);
    try {
      await api.refresh(p);
    } catch (e) {
      scanFailed();
      toast(errText(e), { error: true });
    }
  };

  const toggleHidden = async () => {
    if (hiding) return;
    setHiding(true);
    try {
      const cur = settings.data?.hiddenFolders ?? (await api.settings()).hiddenFolders ?? [];
      const names = hideNames(project);
      const nextList = hidden ? cur.filter((x) => !names.includes(x)) : cur.concat(cur.some((x) => names.includes(x)) ? [] : [names[0]]);
      await api.updateSettings({ hiddenFolders: nextList });
      toast(hidden ? project.name + " is back on home" : project.name + " hidden from home");
      invalidate(keys.settings());
      invalidate("/boot");
      invalidate("/home");
      invalidate(keys.project(p));
    } catch (e) {
      toast(errText(e), { error: true });
    } finally {
      setHiding(false);
    }
  };

  const devActive = active.filter((s) => s.kind !== "helper");
  const activeKeys = new Set(active.map((s) => s.key));
  const devRecent = recent.filter((s) => s.kind !== "helper" && !activeKeys.has(s.key));

  return (
    <header style={{ padding: "24px 32px 20px", borderBottom: "1px solid " + C.hair }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", gap: 24, flexWrap: "wrap" }}>
        <div style={{ flex: "1 1 420px", minWidth: 0, border: "3px double " + C.hair, padding: "16px 20px" }} data-testid="cartouche">
          <div style={{ display: "flex", alignItems: "center", gap: 12, flexWrap: "wrap" }}>
            <h1
              className="pj-trunc"
              style={{ margin: 0, fontWeight: 400, fontSize: 32, lineHeight: 1.1, color: C.text, minWidth: 0, maxWidth: "100%" }}
            >
              {project.name}
            </h1>
            {groupLabel ? <span style={tag}>{groupLabel}</span> : null}
            {hidden ? <span style={{ ...tag, color: C.text, borderColor: C.muted }}>Hidden</span> : null}
          </div>
          <div
            style={{
              display: "flex",
              flexWrap: "wrap",
              alignItems: "baseline",
              marginTop: 10,
              fontSize: 12,
              lineHeight: 1.6,
              color: C.muted,
              overflowWrap: "anywhere",
            }}
          >
            {segs.map((s, i) => (
              <span key={i} style={{ whiteSpace: "pre-wrap", color: s.color }}>
                {s.text}
              </span>
            ))}
          </div>
          {desc ? (
            <p style={{ margin: "10px 0 0", fontSize: 13, lineHeight: 1.55, color: C.text, maxWidth: 760 }}>{desc}</p>
          ) : (
            <p style={{ margin: "10px 0 0", fontSize: 14, lineHeight: 1.5, color: C.muted, maxWidth: 760 }}>no description recorded.</p>
          )}
        </div>

        <div style={{ flex: "none", display: "flex", flexDirection: "column", alignItems: "flex-end", gap: 10 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span style={{ fontSize: 10, letterSpacing: ".14em", textTransform: "uppercase", color: C.muted }}>Range</span>
            <div role="group" aria-label="Range" style={{ display: "flex", border: "1px solid " + C.hair, borderRadius: 3, overflow: "hidden" }}>
              {([7, 30] as const).map((r) => (
                <button
                  key={r}
                  type="button"
                  aria-pressed={range === r}
                  onClick={() => setRange(r)}
                  style={{
                    height: 36,
                    minWidth: 48,
                    padding: "0 12px",
                    border: 0,
                    borderRight: "1px solid " + C.hair,
                    fontSize: 12,
                    cursor: "pointer",
                    background: range === r ? C.raised : "transparent",
                    color: range === r ? C.amber : C.muted,
                  }}
                >
                  {r}d
                </button>
              ))}
            </div>
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 12 }}>
            <span style={{ width: 8, height: 8, borderRadius: "50%", background: scanning ? C.amber : C.teal }} />
            <span style={{ color: C.text }} data-testid="project-scan">
              {scanning ? "rescanning…" : "touched " + rel(project.lastChangeAt) + (hhmm(scanAt) ? " · last scan " + hhmm(scanAt) : "")}
            </span>
          </div>
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap", justifyContent: "flex-end" }}>
            <button
              type="button"
              className="pj-hover"
              onClick={refresh}
              disabled={scanning}
              style={{
                ...btnBase,
                border: "1px solid " + (scanning ? C.amber : C.hair),
                color: scanning ? C.amber : C.text,
                cursor: scanning ? "progress" : "pointer",
              }}
            >
              {Icon.refresh}
              {scanning ? "Rescanning…" : "Refresh"}
            </button>
            <button
              type="button"
              className="pj-hover"
              onClick={toggleHidden}
              disabled={hiding}
              aria-pressed={hidden}
              title={
                hidden
                  ? "This folder is off the home seismograph. Show it again."
                  : "Take this folder off the home seismograph. You can show it again here."
              }
              style={{ ...btnBase, color: hidden ? C.amber : C.muted, border: "1px solid " + (hidden ? C.amber : C.hair) }}
            >
              {hidden ? "show on home" : "hide from home"}
            </button>
          </div>
        </div>
      </div>

      <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 20, alignItems: "center" }} data-testid="native-actions">
        <button type="button" className="pj-hover" style={btn44} onClick={() => runNative(projectRef(p), "editor")}>
          {Icon.editor}Editor
        </button>
        <button type="button" className="pj-hover" style={btn44} onClick={() => runNative(projectRef(p), "finder")}>
          {Icon.finder}Finder
        </button>
        <button type="button" className="pj-hover" style={btn44} onClick={() => runNative(projectRef(p), "terminal")}>
          {Icon.terminal}Terminal
        </button>
        {devActive.map((s) => (
          <button
            key={"saf" + s.key}
            type="button"
            className="pj-hover"
            aria-label={"Open localhost:" + s.port + " in Safari"}
            onClick={() => runNative(ref(p, "server", s.port), "safari")}
            style={{ ...btn44, color: C.teal, borderColor: C.teal }}
          >
            <span style={{ width: 6, height: 6, borderRadius: "50%", background: C.teal }} />
            Open in Safari :{s.port} ↗
          </button>
        ))}
        {devActive.map((s) => (
          <KillButton
            key={"kill" + s.key}
            project={p}
            server={s}
            idle={"Stop server :" + s.port}
            style={{ ...btn44, color: C.coral, borderColor: C.coral }}
          />
        ))}
        {devRecent.map((s) => (
          <StartButton key={"start" + s.key} project={p} server={s} label={"Start server :" + s.port} style={btn44} />
        ))}
        {project.remote ? (
          <button type="button" className="pj-hover" style={btn44} onClick={() => runNative(projectRef(p), "safari")}>
            GitHub ↗
          </button>
        ) : null}
        {latestSession ? (
          <button
            type="button"
            className="pj-hover"
            onClick={() => {
              const r = ref(p, "session", latestSession.id);
              openItem(r);
              void runNative(r, "resume");
            }}
            style={{ ...btn44, color: C.violet, borderColor: C.violet, maxWidth: 460 }}
          >
            {Icon.play}
            <span className="pj-trunc">
              Resume Claude · {latestSession.profile || "claude"} · {rel(latestSession.end || latestSession.start)}
            </span>
          </button>
        ) : (
          <button type="button" disabled style={{ ...btn44, color: C.muted, border: "1px dashed " + C.hair, cursor: "not-allowed" }}>
            No Claude sessions to resume
          </button>
        )}
      </div>
    </header>
  );
}
