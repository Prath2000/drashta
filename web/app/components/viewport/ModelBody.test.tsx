import { act, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "~/lib/api";
import { parseRef } from "~/lib/refs";
import { runEscape } from "~/lib/shortcuts";
import type { ResolveResponse } from "~/lib/types";
import { renderWithRouter } from "../../../test/render";
import { ModelBody } from "./ModelBody";
import * as scene from "./model/scene";

// jsdom has no WebGL: the scene module is replaced, so only the body's own logic runs here.
vi.mock("./model/scene", () => ({ webglAvailable: vi.fn(), mountScene: vi.fn() }));
const webglAvailable = vi.mocked(scene.webglAvailable);
const mountScene = vi.mocked(scene.mountScene);

const acts = [
  { action: "app", label: "Open in default app", command: "open -- /x" },
  { action: "finder", label: "Reveal in Finder", command: "open -R /x" },
];

function resolved(path: string): ResolveResponse {
  return {
    ref: "alpha:file:" + path, type: "file", projectId: "alpha", title: path, subtitle: "", icon: "file", date: null,
    obj: { path, size: 4_404_019, mtime: "2026-10-01T10:00:00Z", exists: true },
    nativeLabel: acts[0].label, nativeActions: acts, notes: [], tasks: [],
  };
}

function stats(over: Partial<scene.ModelStats> = {}): scene.ModelStats {
  return {
    meshes: 3, triangles: 12345, vertices: 7000, materials: 2, textures: 4,
    parts: [
      { name: "hull", triangles: 10000, vertices: 5000 },
      { name: "mast", triangles: 2000, vertices: 1500 },
      { name: "flag", triangles: 345, vertices: 500 },
    ],
    ...over,
  };
}

function handle(over: Partial<scene.SceneHandle> = {}): scene.SceneHandle {
  return { dispose: vi.fn(), reset: vi.fn(), setWireframe: vi.fn(), stats: stats(), ...over };
}

function renderBody(path = "models/robot arm #2.glb") {
  const refStr = "alpha:file:" + path;
  return renderWithRouter(<ModelBody refStr={refStr} refObj={parseRef(refStr)!} resolved={resolved(path)} />);
}

beforeEach(() => {
  webglAvailable.mockReset().mockReturnValue(true);
  mountScene.mockReset();
});

describe("model body", () => {
  it("says WebGL is needed and still offers the native apps", async () => {
    webglAvailable.mockReturnValue(false);
    renderBody();
    const panel = await screen.findByTestId("model-nowebgl");
    expect(panel).toHaveTextContent("3D preview needs WebGL");
    expect(panel).toHaveAttribute("role", "alert");
    expect(panel).toHaveTextContent("Open it in an app instead.");
    expect(screen.getByTestId("native-buttons")).toBeInTheDocument();
    expect(mountScene).not.toHaveBeenCalled();
  });

  it("shows the error and the native apps when the model cannot be loaded", async () => {
    mountScene.mockRejectedValue(new Error("bad glTF header"));
    renderBody();
    const panel = await screen.findByTestId("model-failed");
    expect(panel).toHaveTextContent("This model cannot be shown here");
    expect(panel).toHaveTextContent("bad glTF header");
    expect(panel).toHaveAttribute("role", "alert");
    expect(panel).toHaveTextContent("Open it in an app instead.");
    expect(screen.getByTestId("native-buttons")).toBeInTheDocument();
    expect(screen.queryByTestId("model-loading")).toBeNull();
  });

  it("shows a loading state while the model loads, then the stats and the view buttons", async () => {
    let done!: (h: scene.SceneHandle) => void;
    mountScene.mockReturnValue(new Promise<scene.SceneHandle>((r) => (done = r)));
    renderBody();
    const loading = await screen.findByTestId("model-loading");
    expect(loading).toHaveTextContent("Loading 4.2 MB model…");
    expect(loading).toHaveAttribute("role", "status");
    expect(within(loading).getByText(/^Loading .+…$/)).toHaveClass("vp-loading"); // the line DWG uses too
    expect(screen.getByTestId("model-body")).toHaveTextContent("3D · GLB · 4.2 MB");
    done(handle());
    expect(await screen.findByTestId("model-stats")).toHaveTextContent("3 meshes · 12,345 triangles");
    expect(screen.queryByTestId("model-loading")).toBeNull();
    for (const name of ["Reset view", "Wireframe"]) {
      const b = screen.getByRole("button", { name });
      expect(b).toHaveClass("vp-btn", "sm"); // the same small buttons as the DWG viewer
      expect(b).not.toHaveClass("ghost");
    }
    expect(screen.getByTestId("native-buttons")).toBeInTheDocument();
  });

  it("fills the window with the same canvas, and Esc brings it back", async () => {
    mountScene.mockResolvedValue(handle());
    const { user } = renderBody();
    await screen.findByTestId("model-stats");
    const canvas = screen.getByTestId("model-canvas");
    const stage = screen.getByTestId("model-stage");
    expect(stage).not.toHaveClass("vp-stage-full");
    await user.click(screen.getByRole("button", { name: "⤢ Full window" }));
    expect(stage).toHaveClass("vp-stage-full");
    expect(screen.getByTestId("model-canvas")).toBe(canvas); // not remounted, so the view keeps its camera
    expect(mountScene).toHaveBeenCalledTimes(1);
    act(() => void runEscape());
    expect(stage).not.toHaveClass("vp-stage-full");
    await user.click(screen.getByRole("button", { name: "⤢ Full window" }));
    await user.click(screen.getByRole("button", { name: "⤡ Exit full window" }));
    expect(stage).not.toHaveClass("vp-stage-full");
  });

  it("opens a polycount panel with the totals and every mesh, most triangles first", async () => {
    mountScene.mockResolvedValue(handle());
    const { user } = renderBody();
    await screen.findByTestId("model-stats");
    expect(screen.queryByTestId("model-polycount")).toBeNull();
    const btn = screen.getByRole("button", { name: "Polycount" });
    await user.click(btn);
    expect(btn).toHaveAttribute("aria-pressed", "true");
    const panel = screen.getByTestId("model-polycount");
    for (const t of ["Triangles12,345", "Vertices7,000", "Meshes3", "Materials2", "Textures4"]) expect(panel.textContent).toContain(t);
    const rows = within(panel).getAllByRole("row").slice(1).map((r) => r.textContent);
    expect(rows).toEqual(["hull10,0005,00081%", "mast2,0001,50016.2%", "flag3455002.8%"]);
    await user.click(btn);
    expect(screen.queryByTestId("model-polycount")).toBeNull();
  });

  it("sums the meshes past the first 100 in one line", async () => {
    const parts = Array.from({ length: 103 }, (_, i) => ({ name: "p" + i, triangles: 200 - i, vertices: 10 }));
    mountScene.mockResolvedValue(handle({ stats: stats({ meshes: 103, parts }) }));
    const { user } = renderBody();
    await screen.findByTestId("model-stats");
    await user.click(screen.getByRole("button", { name: "Polycount" }));
    const rows = within(screen.getByTestId("model-polycount")).getAllByRole("row");
    expect(rows).toHaveLength(1 + 100 + 1);
    expect(rows.at(-1)!.textContent).toBe("3 more meshes" + (100 + 99 + 98) + "30");
  });

  it("names the canvas for screen readers by the file name", async () => {
    mountScene.mockResolvedValue(handle());
    renderBody("models/robot arm #2.glb");
    await screen.findByTestId("model-stats");
    const canvas = screen.getByTestId("model-canvas");
    expect(canvas).toHaveAttribute("role", "img");
    expect(canvas).toHaveAttribute("aria-label", "3D view of robot arm #2.glb");
  });

  it("uses the singular for one mesh and one triangle", async () => {
    mountScene.mockResolvedValue(handle({ stats: stats({ meshes: 1, triangles: 1, parts: [{ name: "tri", triangles: 1, vertices: 3 }] }) }));
    renderBody();
    expect(await screen.findByTestId("model-stats")).toHaveTextContent("1 mesh · 1 triangle");
  });

  it("resets the view and toggles wireframe on and off", async () => {
    const h = handle();
    mountScene.mockResolvedValue(h);
    const { user } = renderBody();
    await user.click(await screen.findByRole("button", { name: "Reset view" }));
    expect(h.reset).toHaveBeenCalledTimes(1);
    const wire = screen.getByRole("button", { name: "Wireframe" });
    expect(wire).toHaveAttribute("aria-pressed", "false");
    await user.click(wire);
    expect(h.setWireframe).toHaveBeenLastCalledWith(true);
    expect(wire).toHaveAttribute("aria-pressed", "true");
    await user.click(wire);
    expect(h.setWireframe).toHaveBeenLastCalledWith(false);
  });

  it("hands the scene a fresh canvas, the file URL, its kind and the project files base", async () => {
    mountScene.mockResolvedValue(handle());
    renderBody("models/robot arm #2.glb");
    await screen.findByTestId("model-stats");
    expect(mountScene).toHaveBeenCalledTimes(1);
    const [canvas, url, kind, base, signal] = mountScene.mock.calls[0];
    expect(canvas).toBeInstanceOf(HTMLCanvasElement);
    expect(within(screen.getByTestId("model-frame")).getByTestId("model-canvas")).toBe(canvas);
    expect(url).toBe(api.fileUrl("alpha", "models/robot arm #2.glb"));
    expect(url).toBe("/api/projects/alpha/files/models/robot%20arm%20%232.glb");
    expect(kind).toBe("gltf");
    expect(base).toBe("/api/projects/alpha/files/");
    expect(signal.aborted).toBe(false);
  });

  it("maps each extension to its loader kind", async () => {
    mountScene.mockResolvedValue(handle());
    const { unmount } = renderBody("parts/bracket.STL");
    await screen.findByTestId("model-stats");
    expect(mountScene.mock.calls[0][2]).toBe("stl");
    unmount();
  });

  it("says so when the extension is not a known model format, without loading anything", async () => {
    renderBody("parts/bracket.step");
    expect(await screen.findByTestId("model-failed")).toHaveTextContent("This model cannot be shown here");
    expect(mountScene).not.toHaveBeenCalled();
  });

  it("disposes the scene once and aborts the load when it unmounts", async () => {
    const h = handle();
    mountScene.mockResolvedValue(h);
    const { unmount } = renderBody();
    await screen.findByTestId("model-stats");
    const signal = mountScene.mock.calls[0][4];
    expect(h.dispose).not.toHaveBeenCalled();
    unmount();
    expect(h.dispose).toHaveBeenCalledTimes(1);
    expect(signal.aborted).toBe(true);
  });

  it("aborts a load in progress on unmount, and disposes a scene that arrives too late", async () => {
    let done!: (h: scene.SceneHandle) => void;
    mountScene.mockReturnValue(new Promise<scene.SceneHandle>((r) => (done = r)));
    const { unmount } = renderBody();
    await screen.findByTestId("model-loading");
    const signal = mountScene.mock.calls[0][4];
    unmount();
    expect(signal.aborted).toBe(true);
    const h = handle();
    done(h);
    await waitFor(() => expect(h.dispose).toHaveBeenCalledTimes(1));
  });

  it("stays quiet when the load was aborted (unmount) and the scene rejects with AbortError", async () => {
    let fail!: (e: unknown) => void;
    mountScene.mockReturnValue(new Promise<scene.SceneHandle>((_, rej) => (fail = rej)));
    const { unmount } = renderBody();
    await screen.findByTestId("model-loading");
    unmount();
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    fail(new DOMException("aborted", "AbortError"));
    await Promise.resolve();
    expect(spy).not.toHaveBeenCalled();
    spy.mockRestore();
  });
});
