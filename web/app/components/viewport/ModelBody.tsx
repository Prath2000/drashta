// 3D model preview body (.glb .gltf .stl .obj .ply), drawn with three.js.
//
// - The bytes come from GET /projects/{p}/files/<path> (path-style, so a glTF's relative
//   .bin and texture files resolve next to it). The scene loads on demand (model/scene.ts).
// - A fresh canvas is made for each load, because a WebGL context lost on cleanup cannot be reused.
// - Leaving the page aborts the load and frees the WebGL context (Safari limits live contexts).
// - No WebGL, a corrupt file or an unknown format says so in a panel. The native apps stay one click away.
import { useEffect, useRef, useState } from "react";
import { api } from "~/lib/api";
import type { ViewportBodyProps } from "./registry";
import { baseRef, FullWindowButton, Loading, MediaNotes, NativeButtons, useFullWindow, useNotes } from "./common";
import { fmtDate, fmtSize } from "./fmt";
import { modelKindOf } from "./model/fit";
import { mountScene, webglAvailable, type ModelStats, type SceneHandle } from "./model/scene";
import "./model/model.css";
import type { FileObj } from "./vtypes";

type State =
  | { s: "loading" }
  | { s: "ready"; stats: SceneHandle["stats"] }
  | { s: "failed"; message: string }
  | { s: "nowebgl" };

const plural = (n: number, one: string, many: string) => n.toLocaleString("en-US") + " " + (n === 1 ? one : many);
const num = (n: number) => n.toLocaleString("en-US");

/** Parts listed in the polycount panel; the rest is summed in one line. */
export const PART_ROWS = 100;

/** The polycount panel: totals, then every mesh, most triangles first. */
function Polycount({ stats }: { stats: ModelStats }) {
  const shown = stats.parts.slice(0, PART_ROWS);
  const rest = stats.parts.slice(PART_ROWS);
  const share = (t: number) => (stats.triangles ? Math.round((t / stats.triangles) * 1000) / 10 : 0);
  return (
    <section className="vp-polycount" data-testid="model-polycount" aria-label="Polycount">
      <dl className="vp-polycount-totals">
        {(
          [
            ["Triangles", stats.triangles],
            ["Vertices", stats.vertices],
            ["Meshes", stats.meshes],
            ["Materials", stats.materials],
            ["Textures", stats.textures],
          ] as const
        ).map(([k, v]) => (
          <div key={k}>
            <dt>{k}</dt>
            <dd>{num(v)}</dd>
          </div>
        ))}
      </dl>
      {stats.parts.length > 1 ? (
        <table className="vp-polycount-parts">
          <thead>
            <tr>
              <th>Mesh</th>
              <th>Triangles</th>
              <th>Vertices</th>
              <th>Share</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((part, i) => (
              <tr key={i}>
                <td title={part.name}>{part.name}</td>
                <td>{num(part.triangles)}</td>
                <td>{num(part.vertices)}</td>
                <td>{share(part.triangles)}%</td>
              </tr>
            ))}
            {rest.length ? (
              <tr className="rest">
                <td>{num(rest.length)} more meshes</td>
                <td>{num(rest.reduce((a, r) => a + r.triangles, 0))}</td>
                <td>{num(rest.reduce((a, r) => a + r.vertices, 0))}</td>
                <td />
              </tr>
            ) : null}
          </tbody>
        </table>
      ) : null}
    </section>
  );
}

export function ModelBody({ refStr, refObj, resolved }: ViewportBodyProps) {
  const p = resolved.projectId || refObj.projectId;
  const obj = (resolved.obj ?? {}) as FileObj;
  const path = obj.path ?? refObj.id;
  const ref = baseRef(refObj);
  const notes = useNotes(ref, resolved.notes ?? []);
  const kind = modelKindOf(path);
  const fileName = path.slice(path.lastIndexOf("/") + 1);
  const url = api.fileUrl(p, path);
  const base = api.fileUrl(p, ""); // "/api/projects/<p>/files/": the only place a model may read from

  const host = useRef<HTMLDivElement>(null);
  const scene = useRef<SceneHandle | null>(null);
  const [state, setState] = useState<State>({ s: "loading" });
  const [wire, setWire] = useState(false);
  const { full, toggle: toggleFull } = useFullWindow();
  const [counts, setCounts] = useState(false);

  useEffect(() => {
    setWire(false);
    if (!kind) {
      setState({ s: "failed", message: "The file extension is not a 3D format drashta can draw." });
      return;
    }
    if (!webglAvailable()) {
      setState({ s: "nowebgl" });
      return;
    }
    const mount = host.current;
    if (!mount) return;
    setState({ s: "loading" });
    const canvas = document.createElement("canvas");
    canvas.setAttribute("data-testid", "model-canvas");
    canvas.setAttribute("role", "img");
    canvas.setAttribute("aria-label", "3D view of " + fileName);
    mount.appendChild(canvas);
    const ac = new AbortController();
    let handle: SceneHandle | null = null;
    mountScene(canvas, url, kind, base, ac.signal).then(
      (h) => {
        if (ac.signal.aborted) {
          h.dispose();
          return;
        }
        handle = h;
        scene.current = h;
        setState({ s: "ready", stats: h.stats });
      },
      (e: unknown) => {
        if (ac.signal.aborted || (e instanceof DOMException && e.name === "AbortError")) return;
        setState({ s: "failed", message: e instanceof Error ? e.message : String(e) });
      },
    );
    return () => {
      ac.abort();
      handle?.dispose();
      handle = null;
      scene.current = null;
      canvas.remove();
    };
  }, [url, base, kind, fileName]);

  const label = "3D · " + (path.slice(path.lastIndexOf(".") + 1).toUpperCase() || "MODEL") + " · " + fmtSize(obj.size ?? null) + (obj.mtime ? " · modified " + fmtDate(obj.mtime) : "");
  const showFrame = state.s === "loading" || state.s === "ready";

  return (
    <div className="vp-scroll" data-testid="model-body">
      <div className="vp-pad">
        <div className="vp-stack">
          <div className="vp-label">{label}</div>
          {state.s === "nowebgl" ? (
            <div className="vp-panel" data-testid="model-nowebgl" role="alert">
              <span className="vp-label dim">No preview</span>
              <span className="big">3D preview needs WebGL</span>
              <span className="why">This browser has WebGL turned off or does not support it.</span>
              <span className="why">Open it in an app instead.</span>
            </div>
          ) : null}
          {state.s === "failed" ? (
            <div className="vp-panel" data-testid="model-failed" role="alert">
              <span className="vp-label dim">No preview</span>
              <span className="big">This model cannot be shown here</span>
              <span className="why">{state.message}</span>
              <span className="why">Open it in an app instead.</span>
            </div>
          ) : null}
          {/* The stage (frame + bar) is the element that goes full window; the canvas only resizes. */}
          <div className={"vp-model-stage" + (full && state.s === "ready" ? " vp-stage-full" : "")} data-testid="model-stage">
            {/* Always in the page (hidden on a failure) so the canvas host exists when the next file loads. */}
            <div className="vp-model-frame" data-testid="model-frame" hidden={!showFrame}>
              <div className="vp-model-canvas" ref={host} />
              {state.s === "loading" ? (
                <div className="vp-model-loading" role="status" data-testid="model-loading">
                  <Loading what={fmtSize(obj.size ?? null) + " model"} />
                </div>
              ) : null}
            </div>
            {state.s === "ready" ? (
              <div className="vp-model-bar">
                <span className="vp-model-stats" data-testid="model-stats">
                  {plural(state.stats.meshes, "mesh", "meshes")} · {plural(state.stats.triangles, "triangle", "triangles")}
                </span>
                <button type="button" className="vp-btn sm" aria-pressed={counts} aria-expanded={counts} onClick={() => setCounts((c) => !c)}>
                  Polycount
                </button>
                <button type="button" className="vp-btn sm" onClick={() => scene.current?.reset()}>
                  Reset view
                </button>
                <button
                  type="button"
                  className="vp-btn sm"
                  aria-pressed={wire}
                  onClick={() => {
                    const next = !wire;
                    setWire(next);
                    scene.current?.setWireframe(next);
                  }}
                >
                  Wireframe
                </button>
                <FullWindowButton full={full} onToggle={toggleFull} />
              </div>
            ) : null}
            {state.s === "ready" && counts ? <Polycount stats={state.stats} /> : null}
          </div>
          <NativeButtons refStr={refStr} actions={resolved.nativeActions ?? []} />
        </div>
        <MediaNotes refStr={ref} projectId={p} notes={notes} />
      </div>
    </div>
  );
}
