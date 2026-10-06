// The copied drawing engine, driven the way DwgBody drives it, on a fake 2D canvas
// (jsdom has no canvas). All coordinates are invented.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aciRGB, layerColour } from "./aci";
import { createEngine } from "./engine";

type Ctx = Record<string, unknown> & { beginPathCalls: number };

function fakeCanvas(width: number, height: number) {
  const ctx: Ctx = {
    beginPathCalls: 0,
    beginPath() {
      (this as Ctx).beginPathCalls++;
    },
    moveTo() {},
    lineTo() {},
    stroke() {},
    arc() {},
    ellipse() {},
    closePath() {},
    fillRect() {},
    fillText() {},
    strokeRect() {},
    save() {},
    restore() {},
    translate() {},
    rotate() {},
    setLineDash() {},
    setTransform() {},
    clearRect() {},
    rect() {},
    clip() {},
  };
  const canvas = {
    width,
    height,
    style: {},
    parentElement: null,
    getContext: () => ctx,
    getBoundingClientRect: () => ({ width, height, left: 0, top: 0 }),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  };
  return { canvas: canvas as unknown as HTMLCanvasElement, raw: canvas, ctx };
}

const META = '{"format_version":2,"file":{"name":"plan.dwg","version":"AC1024","release":"R2010"},"space":"model"}';
const LINE_A = '{"type":"LINE","handle":"A1","layer":"A-WALL","space":"model","color":{"index":1},"start":[0,0,0],"end":[10,0,0]}';
const LINE_B = '{"type":"LINE","handle":"A2","layer":"A-DOOR","space":"model","color":{"by":"layer"},"start":[10,0,0],"end":[10,5,0]}';
const SUMMARY = '{"summary":{"emitted":2,"skipped":0,"seen":2}}';
const LAYERS = [
  { name: "A-WALL", color: { index: 1 } },
  { name: "A-DOOR", color: { index: 3 } },
];

let frames: FrameRequestCallback[] = [];

beforeEach(() => {
  frames = [];
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
    frames.push(cb);
    return frames.length;
  });
  vi.stubGlobal("cancelAnimationFrame", vi.fn());
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("engine, as DwgBody drives it", () => {
  it("takes a meta line, a layer table and two LINE entities", () => {
    const eng = createEngine();
    const { canvas } = fakeCanvas(800, 600);
    eng.init({ canvas, hooks: {} });
    eng.setModelspace(true);
    eng.setLayers(LAYERS);
    const n = eng.ingest([META, LINE_A, LINE_B, SUMMARY]);

    expect(n).toBe(2);
    expect(eng.state.ents.length).toBe(2);
    expect((eng.state.meta as { file: { release: string } }).file.release).toBe("R2010");
    expect((eng.state.summary as { emitted: number }).emitted).toBe(2);
    expect(eng.state.counts).toEqual([1, 1]);
    expect(eng.layers[0]._css).toBe("#ff0000");
    expect(eng.layers[1]._css).toBe("#00ff00");
    eng.destroy();
  });

  it("calls the progress hook with the entity count after each ingest", () => {
    const eng = createEngine();
    const { canvas } = fakeCanvas(800, 600);
    const progress = vi.fn();
    eng.init({ canvas, hooks: { progress } });
    eng.setLayers(LAYERS);
    eng.ingest([META, LINE_A]);
    eng.ingest([LINE_B, SUMMARY]);
    expect(progress.mock.calls.map((c) => c[0])).toEqual([1, 2]);
    eng.destroy();
  });

  it("fits a drawing on a canvas without throwing, and draws both lines", () => {
    const eng = createEngine();
    const { canvas, ctx } = fakeCanvas(800, 600);
    eng.init({ canvas, hooks: {} });
    eng.setLayers(LAYERS);
    eng.ingest([LINE_A, LINE_B]);
    expect(() => eng.fit()).not.toThrow();
    expect(eng.extent).toEqual([0, 0, 10, 5]);

    // init() asked for a frame; run it.
    expect(frames.length).toBeGreaterThan(0);
    frames.shift()!(0);
    expect(ctx.beginPathCalls).toBeGreaterThanOrEqual(2);
    eng.destroy();
  });

  it("toggles mute and solo by layer row, and clears both", () => {
    const eng = createEngine();
    const { canvas } = fakeCanvas(800, 600);
    eng.init({ canvas, hooks: {} });
    eng.setLayers(LAYERS);
    eng.ingest([LINE_A, LINE_B]);

    eng.toggleMute(0);
    expect(eng.state.muted.has(0)).toBe(true);
    expect(eng.drawableShapes()).toBe(1);
    eng.toggleSolo(0);
    expect(eng.state.solo.has(0)).toBe(true);
    expect(eng.drawableShapes()).toBe(0); // mute beats solo
    eng.clearFilters();
    expect(eng.state.muted.size + eng.state.solo.size).toBe(0);
    expect(eng.drawableShapes()).toBe(2);
    eng.destroy();
  });

  it("destroy stops the render loop and removes the canvas listeners", () => {
    const eng = createEngine();
    const { canvas, raw } = fakeCanvas(800, 600);
    eng.init({ canvas, hooks: {} });
    expect(raw.addEventListener).toHaveBeenCalled();
    eng.destroy();
    expect(cancelAnimationFrame).toHaveBeenCalled();
    expect(raw.removeEventListener).toHaveBeenCalled();
  });

  it("reset forgets the drawing", () => {
    const eng = createEngine();
    const { canvas } = fakeCanvas(800, 600);
    eng.init({ canvas, hooks: {} });
    eng.setLayers(LAYERS);
    eng.ingest([META, LINE_A]);
    eng.reset();
    expect(eng.state.ents.length).toBe(0);
    expect(eng.extent).toBeNull();
    eng.destroy();
  });
});

describe("aci", () => {
  it("keeps the AutoCAD table's rounding", () => {
    expect(aciRGB(1)).toBe("#ff0000");
    expect(aciRGB(20)).toBe("#ff3f00"); // the hue ramp truncates
    expect(aciRGB(19)).toBe("#4f3535"); // the desaturated channel rounds
    expect(aciRGB(256)).toBeNull();
  });

  it("gives a layer no colour rather than a made-up one", () => {
    expect(layerColour({ color: null })).toBeNull();
    expect(layerColour({ color: { index: 5 } })).toBe("#0000ff");
  });
});
