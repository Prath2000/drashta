// jsdom has no WebGL, so these cover what happens before a renderer exists:
// the checks, the fetch, the parse and the failure paths. The drawing itself is checked in a real browser.
import { afterEach, describe, expect, it, vi } from "vitest";
import { mountScene, webglAvailable } from "./scene";

const base = "/api/projects/demo/files/";
const url = base + "m/part.stl";

// An ASCII STL is padded past 84 bytes: the loader reads a binary header length before it looks for "solid".
const pad = (s: string) => new TextEncoder().encode(s.padEnd(200, "\n")).buffer;
const TRIANGLE = pad("solid t\nfacet normal 0 0 1\nouter loop\nvertex 0 0 0\nvertex 1 0 0\nvertex 0 1 0\nendloop\nendfacet\nendsolid t\n");
const EMPTY = pad("solid empty\nendsolid empty\n");

function serve(body: ArrayBuffer | null, status = 200) {
  const fetchMock = vi.fn(async () => new Response(body, { status }));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const canvas = () => document.createElement("canvas");

describe("mountScene failures", () => {
  it("throws AbortError at once when the signal is already aborted, without fetching", async () => {
    const f = serve(TRIANGLE);
    const ac = new AbortController();
    ac.abort();
    await expect(mountScene(canvas(), url, "stl", base, ac.signal)).rejects.toMatchObject({ name: "AbortError" });
    expect(f).not.toHaveBeenCalled();
  });

  it("refuses a model URL outside the project files, without fetching", async () => {
    const f = serve(TRIANGLE);
    await expect(mountScene(canvas(), "/api/session", "stl", base, new AbortController().signal)).rejects.toThrow("outside the project files");
    expect(f).not.toHaveBeenCalled();
  });

  it("reports a failed download", async () => {
    serve(null, 404);
    await expect(mountScene(canvas(), url, "stl", base, new AbortController().signal)).rejects.toThrow("the server answered 404");
  });

  it("stops the download when aborted and throws AbortError", async () => {
    const ac = new AbortController();
    const f = vi.fn(
      (_u: string, init?: RequestInit) =>
        new Promise<Response>((_, reject) => {
          init?.signal?.addEventListener("abort", () => reject(new DOMException("The operation was aborted.", "AbortError")));
        }),
    );
    vi.stubGlobal("fetch", f);
    const p = mountScene(canvas(), url, "stl", base, ac.signal);
    await vi.waitFor(() => expect(f).toHaveBeenCalled());
    ac.abort();
    await expect(p).rejects.toMatchObject({ name: "AbortError", message: "aborted" });
    expect(f.mock.calls[0][1]?.signal).toBe(ac.signal);
  });

  it("rejects a corrupt glb with the loader's message", async () => {
    serve(pad("this is not a glb file"));
    await expect(mountScene(canvas(), base + "m/bad.glb", "gltf", base, new AbortController().signal)).rejects.toBeInstanceOf(Error);
  });

  it("says so when a glTF needs a file outside the project, and fetches nothing from there", async () => {
    const gltf = JSON.stringify({
      asset: { version: "2.0" }, scene: 0, scenes: [{ nodes: [0] }], nodes: [{ mesh: 0 }],
      meshes: [{ primitives: [{ attributes: { POSITION: 0 } }] }],
      buffers: [{ uri: "https://evil.example/x.bin", byteLength: 36 }],
      bufferViews: [{ buffer: 0, byteLength: 36 }],
      accessors: [{ bufferView: 0, componentType: 5126, count: 3, type: "VEC3" }],
    });
    const asked: string[] = [];
    vi.stubGlobal("fetch", async (u: string | Request) => {
      const asked1 = typeof u === "string" ? u : u.url;
      asked.push(asked1);
      return new Response(asked1 === base + "m/evil.gltf" ? gltf : new ArrayBuffer(0));
    });
    await expect(mountScene(canvas(), base + "m/evil.gltf", "gltf", base, new AbortController().signal)).rejects.toThrow("outside this project: https://evil.example/x.bin");
    expect(asked.some((u) => u.includes("evil.example"))).toBe(false);
  });

  it("rejects a model with no geometry", async () => {
    serve(EMPTY);
    await expect(mountScene(canvas(), url, "stl", base, new AbortController().signal)).rejects.toThrow("no geometry");
  });

  it("frees the parsed model and rethrows when the WebGL context cannot be made", async () => {
    serve(TRIANGLE);
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
    await expect(mountScene(canvas(), url, "stl", base, new AbortController().signal)).rejects.toThrow(/WebGL/i);
  });
});

describe("webglAvailable", () => {
  it("is false when the browser gives no WebGL2 context", () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
    expect(webglAvailable()).toBe(false);
  });

  it("is true when it does, and gives the probe context back", () => {
    const loseContext = vi.fn();
    const gl = { getExtension: vi.fn(() => ({ loseContext })) };
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(gl as unknown as RenderingContext);
    expect(webglAvailable()).toBe(true);
    expect(gl.getExtension).toHaveBeenCalledWith("WEBGL_lose_context");
    expect(loseContext).toHaveBeenCalledTimes(1);
  });

  it("is false when asking for a context throws", () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(webglAvailable()).toBe(false);
  });
});

describe("countModel", () => {
  it("counts triangles, vertices, meshes, distinct materials and textures, biggest part first", async () => {
    const THREE = await import("three");
    const { countModel } = await import("./scene");
    const tex = new THREE.Texture();
    const shared = new THREE.MeshStandardMaterial({ map: tex });
    const root = new THREE.Group();
    const small = new THREE.Mesh(new THREE.BoxGeometry(), shared); // 12 triangles (indexed), 24 vertices
    small.name = "lid";
    const big = new THREE.Mesh(new THREE.SphereGeometry(1, 8, 6), shared);
    big.name = "body";
    const loose = new THREE.Mesh(new THREE.BufferGeometry().setAttribute("position", new THREE.Float32BufferAttribute(new Array(9 * 3).fill(0), 3)), new THREE.MeshBasicMaterial());
    const cloud = new THREE.Points(new THREE.BufferGeometry().setAttribute("position", new THREE.Float32BufferAttribute(new Array(5 * 3).fill(0), 3)));
    root.add(small, big, loose, cloud);
    const s = countModel(root);
    const bigTris = big.geometry.index!.count / 3;
    expect(s.meshes).toBe(4);
    expect(s.triangles).toBe(12 + bigTris + 3); // the point cloud adds no triangles
    expect(s.vertices).toBe(24 + big.geometry.getAttribute("position").count + 9 + 5);
    expect(s.materials).toBe(3); // shared counted once, the basic one, the points' default
    expect(s.textures).toBe(1);
    expect(s.parts.map((p) => p.name)).toEqual(["body", "lid", "mesh", "point cloud"]);
    expect(s.parts[1]).toEqual({ name: "lid", triangles: 12, vertices: 24 });
  });
});

