// DWG drawing preview body (FileBody hands a .dwg over by extension).
//
// - chitralipi (run by the Go server) gives the layer table (GET /dwg/layers) and the model-space
//   entities as JSONL (GET /dwg/entities). Model space only; INSERTs draw as markers.
// - Both requests start at once. The entities are read as a stream and fed to the canvas engine
//   (cad/engine.js, copied from a sibling project) in batches of at most 2,000 lines, so a big drawing never
//   freezes the page. Batches that arrive before the layers are held until the layers are in.
// - The engine is fed in this order: init, setModelspace, setLayers, ingest batches, fit.
// - chitralipi missing, a file it cannot read, or any other failure shows a panel with the
//   native buttons. The first failure stops the other request. Leaving the page aborts both
//   requests and stops the engine's render loop.
import { useEffect, useRef, useState } from "react";
import { api } from "~/lib/api";
import type { ViewportBodyProps } from "./registry";
import { layerColour } from "./cad/aci";
import "./cad/cad.css";
import { createEngine } from "./cad/engine";
import type { CadEngine, CadLayerRow } from "./cad/engine";
import { baseRef, FullWindowButton, Loading, MediaNotes, NativeButtons, useFullWindow, useNotes } from "./common";
import { fmtDate, fmtSize } from "./fmt";
import type { FileObj } from "./vtypes";

/** Most JSONL lines handed to the engine in one ingest call. */
const BATCH_LINES = 2000;

type Phase = "loading" | "done" | "error";
type FailKind = "unavailable" | "bad" | "failed";

/** What went wrong, in terms of the panel to show. */
class DwgFailure extends Error {
  constructor(readonly kind: FailKind, message: string) {
    super(message);
  }
}

/** Every panel ends with "Open it in an app instead." (same line as the 3D viewer's panels). */
const PANEL: Record<FailKind, { big: string; why: string }> = {
  unavailable: { big: "Drawing preview is not available", why: "chitralipi, the program that reads DWG files, is missing." },
  bad: { big: "chitralipi could not read this drawing", why: "The file may be damaged, or not a DWG." },
  failed: { big: "The drawing could not be loaded", why: "" },
};

/** Turns a non-OK answer into the failure to show: 503 is "unavailable", 422 is "bad". */
async function failureOf(res: Response): Promise<DwgFailure> {
  let message = "the server answered " + res.status;
  try {
    const body = (await res.json()) as { error?: { message?: string } };
    if (body?.error?.message) message = body.error.message;
  } catch {
    /* not JSON: keep the status text */
  }
  return new DwgFailure(res.status === 503 ? "unavailable" : res.status === 422 ? "bad" : "failed", message);
}

async function fetchOk(url: string, signal: AbortSignal): Promise<Response> {
  let res: Response;
  try {
    res = await fetch(url, { signal });
  } catch (e) {
    if (signal.aborted) throw e;
    throw new DwgFailure("failed", "could not reach the server");
  }
  if (!res.ok) throw await failureOf(res);
  return res;
}

/** The layer table: `layers --as json` is `{layers:[...]}`; a bare array is accepted too. */
async function fetchLayers(url: string, signal: AbortSignal): Promise<CadLayerRow[]> {
  const res = await fetchOk(url, signal);
  let json: unknown;
  try {
    json = await res.json();
  } catch {
    if (signal.aborted) throw new DOMException("aborted", "AbortError");
    throw new DwgFailure("failed", "the layer list could not be read");
  }
  const rows = Array.isArray(json) ? json : (json as { layers?: unknown } | null)?.layers;
  if (!Array.isArray(rows)) throw new DwgFailure("failed", "the layer list has an unexpected shape");
  return rows as CadLayerRow[];
}

/**
 * Reads the JSONL answer as it arrives and calls onBatch with at most BATCH_LINES lines at a
 * time. A line cut by a chunk boundary waits for the rest of it.
 */
async function streamLines(res: Response, signal: AbortSignal, onBatch: (lines: string[]) => void): Promise<void> {
  const pending: string[] = [];
  const flush = (all: boolean) => {
    while (pending.length >= BATCH_LINES || (all && pending.length > 0)) onBatch(pending.splice(0, BATCH_LINES));
  };
  const push = (text: string) => {
    for (const line of text.split("\n")) if (line.trim()) pending.push(line);
  };
  if (!res.body) {
    push(await res.text());
    flush(true);
    return;
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let tail = "";
  try {
    for (;;) {
      let chunk: ReadableStreamReadResult<Uint8Array>;
      try {
        chunk = await reader.read();
      } catch (e) {
        if (signal.aborted) throw e;
        throw new DwgFailure("failed", "the drawing stopped arriving");
      }
      if (signal.aborted) throw new DOMException("aborted", "AbortError");
      if (chunk.done) break;
      const parts = (tail + decoder.decode(chunk.value, { stream: true })).split("\n");
      tail = parts.pop() ?? "";
      push(parts.join("\n"));
      flush(false);
    }
  } finally {
    void reader.cancel().catch(() => {});
  }
  push(tail + decoder.decode());
  flush(true);
}

function releaseOf(meta: unknown): string {
  const file = (meta as { file?: { release?: unknown } } | null | undefined)?.file;
  return typeof file?.release === "string" ? file.release : "";
}

/** A layer with no shape in model space has nothing to toggle: leave it out of the rail. */
function hasModelShapes(row: CadLayerRow): boolean {
  const n = (row.entities as { model?: unknown } | undefined)?.model;
  return typeof n !== "number" || n > 0;
}

function flip(set: Set<number>, i: number): Set<number> {
  const next = new Set(set);
  if (!next.delete(i)) next.add(i);
  return next;
}

interface Stats {
  count: number;
  release: string;
  counts: number[];
}
const NO_STATS: Stats = { count: 0, release: "", counts: [] };

function Viewer({ projectId, path }: { projectId: string; path: string }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const engRef = useRef<CadEngine | null>(null);
  const [phase, setPhase] = useState<Phase>("loading");
  const [failure, setFailure] = useState<DwgFailure | null>(null);
  const [layers, setLayers] = useState<CadLayerRow[]>([]);
  const [stats, setStats] = useState<Stats>(NO_STATS);
  // The page keeps its own copy of which rows are hidden or soloed: only this component drives the engine.
  const [muted, setMuted] = useState<Set<number>>(() => new Set());
  const [solo, setSolo] = useState<Set<number>>(() => new Set());
  const { full, toggle: toggleFull } = useFullWindow();

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctl = new AbortController();
    const eng = createEngine();
    engRef.current = eng;
    let stopped = false;
    const stop = () => {
      if (!stopped) {
        stopped = true;
        eng.destroy();
      }
    };
    const publish = () => {
      if (ctl.signal.aborted) return;
      setStats({ count: eng.state.ents.length, release: releaseOf(eng.state.meta), counts: eng.state.counts.slice() });
    };

    eng.init({ canvas, hooks: { progress: publish } });
    eng.setModelspace(true);

    void (async () => {
      const held: string[][] = []; // entity batches that came before the layers
      let layersIn = false;
      const feed = (lines: string[]) => {
        if (ctl.signal.aborted) return;
        if (layersIn) eng.ingest(lines);
        else held.push(lines);
      };

      // Both requests start now: the server reads the layers and the entities side by side.
      const layersJob = fetchLayers(api.dwgLayersUrl(projectId, path), ctl.signal).then((rows) => {
        if (ctl.signal.aborted) return;
        eng.setLayers(rows); // before the first entity: each entity finds its layer row as it arrives
        setLayers(rows);
        layersIn = true;
        for (const lines of held.splice(0)) eng.ingest(lines);
      });
      const entitiesJob = fetchOk(api.dwgEntitiesUrl(projectId, path), ctl.signal).then((res) => streamLines(res, ctl.signal, feed));
      try {
        await Promise.all([layersJob, entitiesJob]);
        if (ctl.signal.aborted) return;
        if (!eng.state.userMoved) eng.fit();
        publish();
        setPhase("done");
      } catch (e) {
        if (ctl.signal.aborted) return;
        ctl.abort(); // the first failure counts; the other request stops
        stop(); // no render loop behind a panel
        setFailure(e instanceof DwgFailure ? e : new DwgFailure("failed", e instanceof Error ? e.message : String(e)));
        setPhase("error");
      }
    })();

    return () => {
      ctl.abort();
      stop();
      engRef.current = null;
    };
  }, [projectId, path]);

  if (phase === "error" && failure) {
    return (
      <div className="vp-panel" data-testid="dwg-error" data-kind={failure.kind} role="alert">
        <span className="vp-label dim">No preview</span>
        <span className="big">{PANEL[failure.kind].big}</span>
        <span className="why">{failure.message}</span>
        {PANEL[failure.kind].why ? <span className="why">{PANEL[failure.kind].why}</span> : null}
        <span className="why">Open it in an app instead.</span>
      </div>
    );
  }

  const shown = layers.map((row, i) => ({ row, i })).filter(({ row }) => hasModelShapes(row));
  const filtered = muted.size + solo.size > 0;
  const status = `${stats.count.toLocaleString("en-US")} ${stats.count === 1 ? "entity" : "entities"}${stats.release ? " · " + stats.release : ""}`;

  const toggleLayer = (i: number, shift: boolean) => {
    const eng = engRef.current;
    if (!eng) return;
    if (shift) {
      eng.toggleSolo(i);
      setSolo((s) => flip(s, i));
    } else {
      eng.toggleMute(i);
      setMuted((s) => flip(s, i));
    }
  };
  const showAll = () => {
    engRef.current?.clearFilters();
    setMuted(new Set());
    setSolo(new Set());
  };

  // vp-cad-host is the size container: the rail sits beside the canvas when the body is wide, under it when narrow.
  return (
    <div className={"vp-cad-host" + (full ? " vp-stage-full" : "")} data-testid="dwg-stage">
      <div className={"vp-cad" + (shown.length ? " has-rail" : "")} data-testid="dwg-viewer" data-phase={phase}>
        <div className="vp-cad-main">
          <div className="vp-cad-frame">
            <canvas ref={canvasRef} className="vp-cad-canvas" role="img" aria-label={"Drawing " + path} />
            {phase === "loading" && (
              <div className="vp-cad-veil" role="status" data-testid="dwg-loading">
                <Loading what="the drawing" />
              </div>
            )}
            {phase === "done" && stats.count === 0 && (
              <div className="vp-cad-veil" data-testid="dwg-empty">
                No shapes in model space.
              </div>
            )}
          </div>
          <div className="vp-cad-bar">
            <span className="vp-cad-status" data-testid="dwg-status">
              {status}
            </span>
            <span className="vp-cad-actions">
              <button type="button" className="vp-btn sm" onClick={() => engRef.current?.fit()}>
                Zoom to extent
              </button>
              <FullWindowButton full={full} onToggle={toggleFull} />
            </span>
          </div>
        </div>
        {shown.length > 0 && (
          <aside className="vp-cad-rail" data-testid="dwg-rail" aria-label="Layers">
            <div className="vp-cad-rail-head">
              <span className="vp-label">Layers · {shown.length}</span>
              <button type="button" className="vp-btn sm" disabled={!filtered} onClick={showAll}>
                Show all
              </button>
            </div>
            <div className="vp-cad-rail-list">
              {shown.map(({ row, i }) => (
                <button
                  key={i}
                  type="button"
                  className="vp-cad-layer"
                  data-testid={"dwg-layer-" + row.name}
                  aria-pressed={!muted.has(i)}
                  data-solo={solo.has(i) ? "true" : undefined}
                  title="Click to hide or show. Shift-click to show only this layer."
                  onClick={(e) => toggleLayer(i, e.shiftKey)}
                  onMouseEnter={() => engRef.current?.setHover(i)}
                  onMouseLeave={() => engRef.current?.setHover(-1)}
                >
                  <span className="swatch" style={{ background: layerColour(row) ?? "transparent" }} />
                  <span className="name">{row.name || "(unnamed)"}</span>
                  <span className="n">{(stats.counts[i] ?? 0).toLocaleString("en-US")}</span>
                </button>
              ))}
            </div>
          </aside>
        )}
      </div>
    </div>
  );
}

export function DwgBody({ refStr, refObj, resolved }: ViewportBodyProps) {
  const p = resolved.projectId || refObj.projectId;
  const obj = (resolved.obj ?? {}) as FileObj;
  const path = obj.path ?? refObj.id;
  const ref = baseRef(refObj);
  const notes = useNotes(ref, resolved.notes ?? []);
  return (
    <div className="vp-scroll" data-testid="dwg-body">
      <div className="vp-pad">
        <div className="vp-stack">
          <div className="vp-label">
            Drawing · {fmtSize(obj.size ?? null)}
            {obj.mtime ? " · modified " + fmtDate(obj.mtime) : ""}
          </div>
          {/* Keyed by file: another drawing gets a fresh canvas and engine. */}
          <Viewer key={p + "\u0000" + path} projectId={p} path={path} />
          <NativeButtons refStr={refStr} actions={resolved.nativeActions ?? []} />
        </div>
        <MediaNotes refStr={ref} projectId={p} notes={notes} />
      </div>
    </div>
  );
}
