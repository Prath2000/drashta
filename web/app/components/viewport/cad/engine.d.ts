// Types for engine.js (copied from a sibling project; see the header of that file).
// Only the part DwgBody uses is declared; the engine has more (highlight, problems, marquee).

/** A row of the layer table. The engine adds `_css`, the CSS colour of the layer (or null). */
export interface CadLayerRow {
  name: string;
  color?: { rgb?: { r: number; g: number; b: number } | null; index?: number } | null;
  _css?: string | null;
  [key: string]: unknown;
}

export interface CadScaleInfo {
  metres: number;
  px: number;
  across: number;
}

export interface CadHooks {
  /** After every ingest call: how many entities the engine holds now. */
  progress?: (count: number) => void;
  /** After every repainted frame. */
  frame?: (info: CadScaleInfo) => void;
  click?: (hit: unknown) => void;
  marquee?: (handles: string[]) => void;
}

export interface CadState {
  ents: unknown[];
  /** The last `{"summary": ...}` line of the stream, or undefined. */
  summary: unknown;
  /** The first `{"file": ...}` line of the stream, or undefined. */
  meta: unknown;
  /** Entities seen per layer row (same index as the layer table). */
  counts: number[];
  muted: Set<number>;
  solo: Set<number>;
  /** True once a person has panned or zoomed: the engine stops reframing by itself. */
  userMoved: boolean;
}

export interface CadEngine {
  init(opts: { canvas: HTMLCanvasElement; hooks?: CadHooks }): void;
  /** Stops the render loop and every listener init() attached. */
  destroy(): void;
  /** Forgets the drawing (entities, counts, filters). */
  reset(): void;
  /** Must come before the first ingest: entities resolve their layer row when they arrive. */
  setLayers(rows: CadLayerRow[]): void;
  /** Feed JSONL lines. Returns how many were entities (the meta and summary lines are not). */
  ingest(lines: string[]): number;
  /** Frame the whole drawing. */
  fit(): void;
  /** Re-apply the last framing at the canvas's current size. */
  refit(): void;
  toggleMute(i: number): boolean;
  toggleSolo(i: number): boolean;
  setSolo(is: number[], on: boolean): void;
  clearFilters(): void;
  /** Dim every layer but i (pass -1 to clear). */
  setHover(i: number): void;
  /** The grid and the crosshair. */
  setModelspace(on: boolean): void;
  drawableShapes(): number;
  /** How many shapes lie outside the framed view. */
  outsideFrame(): number;
  scaleInfo(): CadScaleInfo;
  readonly entityCount: number;
  /** The true bounding box [minx, miny, maxx, maxy] of what has arrived, or null. */
  readonly extent: [number, number, number, number] | null;
  readonly layerCount: number[];
  readonly layers: CadLayerRow[];
  readonly meta: unknown;
  readonly summary: unknown;
  readonly state: CadState;
}

export function createEngine(): CadEngine;
