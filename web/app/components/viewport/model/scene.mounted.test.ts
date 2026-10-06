// The part of mountScene that runs once a renderer exists. jsdom has no WebGL, so the renderer
// is a fake that records its calls; the model parse, the camera and the controls are the real three.js.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const gl = vi.hoisted(() => ({
  made: [] as Array<{ setSize: ReturnType<typeof vi.fn>; dispose: ReturnType<typeof vi.fn>; forceContextLoss: ReturnType<typeof vi.fn> }>,
  setSizeThrows: false,
}));

vi.mock("three", async (importOriginal) => {
  const real = await importOriginal<typeof import("three")>();
  class FakeRenderer {
    setPixelRatio = vi.fn();
    setSize = vi.fn(() => {
      if (gl.setSizeThrows) throw new Error("setSize failed");
    });
    render = vi.fn();
    dispose = vi.fn();
    forceContextLoss = vi.fn();
    constructor() {
      gl.made.push(this);
    }
  }
  return { ...real, WebGLRenderer: FakeRenderer };
});

import { mountScene } from "./scene";

const base = "/api/projects/demo/files/";
const url = base + "m/part.stl";
const pad = (s: string) => new TextEncoder().encode(s.padEnd(200, "\n")).buffer;
const TRIANGLE = pad("solid t\nfacet normal 0 0 1\nouter loop\nvertex 0 0 0\nvertex 1 0 0\nvertex 0 1 0\nendloop\nendfacet\nendsolid t\n");

/** A ResizeObserver whose callback the test calls itself. */
const observers: Array<{ cb: () => void; observe: ReturnType<typeof vi.fn>; disconnect: ReturnType<typeof vi.fn> }> = [];

/** A canvas whose layout size the test sets. */
function sizedCanvas(w: number, h: number) {
  const c = document.createElement("canvas");
  const size = { w, h };
  Object.defineProperty(c, "clientWidth", { get: () => size.w });
  Object.defineProperty(c, "clientHeight", { get: () => size.h });
  return { canvas: c, size };
}

beforeEach(() => {
  gl.made.length = 0;
  gl.setSizeThrows = false;
  observers.length = 0;
  vi.stubGlobal("fetch", vi.fn(async () => new Response(TRIANGLE)));
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe = vi.fn();
      disconnect = vi.fn();
      constructor(public cb: () => void) {
        observers.push(this);
      }
    },
  );
  vi.stubGlobal("requestAnimationFrame", vi.fn(() => 1));
  vi.stubGlobal("cancelAnimationFrame", vi.fn());
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("mountScene once the renderer exists", () => {
  it("places the camera again at the first real size when the canvas was 0x0 at first, and only then", async () => {
    // Spy on the controls' update(): place() calls it once for each placement.
    const { OrbitControls } = await import("three/examples/jsm/controls/OrbitControls.js");
    const update = vi.spyOn(OrbitControls.prototype, "update");
    const { canvas, size } = sizedCanvas(0, 0);
    const handle = await mountScene(canvas, url, "stl", base, new AbortController().signal);
    const afterMount = update.mock.calls.length; // the placement at mount
    expect(afterMount).toBeGreaterThanOrEqual(1);

    expect(gl.made[0].setSize).not.toHaveBeenCalled(); // 0x0: nothing to size yet
    size.w = 640;
    size.h = 480;
    observers[0].cb(); // first non-zero size: place again
    expect(gl.made[0].setSize).toHaveBeenCalledWith(640, 480, false);
    const afterFirst = update.mock.calls.length;
    expect(afterFirst).toBeGreaterThan(afterMount);

    size.w = 700;
    observers[0].cb(); // later sizes: no new placement (the person may have orbited by now)
    expect(update.mock.calls.length).toBe(afterFirst);
    handle.dispose();
  });

  it("places once when the canvas already has a size", async () => {
    const { OrbitControls } = await import("three/examples/jsm/controls/OrbitControls.js");
    const update = vi.spyOn(OrbitControls.prototype, "update");
    const { canvas } = sizedCanvas(640, 480);
    const handle = await mountScene(canvas, url, "stl", base, new AbortController().signal);
    const afterMount = update.mock.calls.length;
    observers[0].cb(); // the observer's first call right after observe(): same size
    expect(update.mock.calls.length).toBe(afterMount);
    handle.dispose();
  });

  it("frees the renderer and the observer, and rethrows, when the setup throws", async () => {
    gl.setSizeThrows = true;
    const { canvas } = sizedCanvas(640, 480);
    await expect(mountScene(canvas, url, "stl", base, new AbortController().signal)).rejects.toThrow("setSize failed");
    const renderer = gl.made[0];
    expect(renderer.dispose).toHaveBeenCalledTimes(1);
    expect(renderer.forceContextLoss).toHaveBeenCalledTimes(1);
    expect(observers).toHaveLength(1);
    expect(observers[0].disconnect).toHaveBeenCalledTimes(1);
  });

  it("draws every mesh with one bright line material in wireframe, and puts the real one back", async () => {
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => (cb(0), 1)); // draw at once
    const { canvas } = sizedCanvas(640, 480);
    const handle = await mountScene(canvas, url, "stl", base, new AbortController().signal);
    const renderer = gl.made[0] as unknown as { render: ReturnType<typeof vi.fn> };
    const meshIn = () => {
      const scene = renderer.render.mock.calls.at(-1)![0] as import("three").Scene;
      let found: import("three").Mesh | undefined;
      scene.traverse((o) => {
        if ((o as import("three").Mesh).isMesh) found = o as import("three").Mesh;
      });
      return found!;
    };
    const real = meshIn().material;
    handle.setWireframe(true);
    const wire = meshIn().material as import("three").MeshBasicMaterial;
    expect(wire).not.toBe(real);
    expect(wire.wireframe).toBe(true);
    expect(wire.type).toBe("MeshBasicMaterial");
    handle.setWireframe(true); // twice on: the real material is still remembered
    handle.setWireframe(false);
    expect(meshIn().material).toBe(real);
    handle.dispose();
  });

  it("dispose is safe to call twice and frees the renderer once", async () => {
    const { canvas } = sizedCanvas(640, 480);
    const handle = await mountScene(canvas, url, "stl", base, new AbortController().signal);
    handle.dispose();
    handle.dispose();
    expect(gl.made[0].dispose).toHaveBeenCalledTimes(1);
    expect(gl.made[0].forceContextLoss).toHaveBeenCalledTimes(1);
    expect(observers[0].disconnect).toHaveBeenCalledTimes(1);
  });
});
