import { screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { describe, expect, it, vi } from "vitest";
import { parseRef } from "~/lib/refs";
import { server } from "~/mocks/node";
import type { ResolveResponse } from "~/lib/types";
import { renderWithRouter } from "../../../test/render";
import { FileBody } from "./FileBody";

// The model and drawing bodies are replaced by other tracks: only the choice is tested here.
vi.mock("./ModelBody", () => ({ ModelBody: () => <div data-testid="model-body" /> }));
vi.mock("./DwgBody", () => ({ DwgBody: () => <div data-testid="dwg-body" /> }));

function show(path: string, exists = true) {
  const refStr = "alpha:file:" + path;
  const resolved = {
    ref: refStr, type: "file", projectId: "alpha", title: path, subtitle: "", icon: "file", date: null,
    obj: { path, size: 2048, mtime: "2026-10-01T10:00:00Z", exists },
    nativeLabel: "Open in default app", nativeActions: [], notes: [], tasks: [],
  } as ResolveResponse;
  return renderWithRouter(<FileBody refStr={refStr} refObj={parseRef(refStr)!} resolved={resolved} />);
}

describe("FileBody dispatch by extension", () => {
  it.each(["models/robot.glb", "m.GLTF", "m.stl", "m.obj", "m.ply"])("%s -> ModelBody", async (path) => {
    show(path);
    expect(await screen.findByTestId("model-body")).toBeInTheDocument();
    expect(screen.queryByTestId("dwg-body")).toBeNull();
  });

  it("a .dwg goes to DwgBody", async () => {
    show("plans/floor.dwg");
    expect(await screen.findByTestId("dwg-body")).toBeInTheDocument();
    expect(screen.queryByTestId("model-body")).toBeNull();
  });

  it.each([
    ["music/song.mp3", "media-body"],
    ["spec.pdf", "media-body"],
  ])("%s still goes to MediaFileBody", async (path, id) => {
    show(path);
    expect(await screen.findByTestId(id)).toBeInTheDocument();
    expect(screen.queryByTestId("model-body")).toBeNull();
    expect(screen.queryByTestId("dwg-body")).toBeNull();
  });

  // A file that is gone from disk has nothing to draw, play or stream: the plain "Not on disk" state shows.
  it.each(["models/robot.glb", "plans/floor.dwg", "music/song.mp3", "spec.pdf"])("%s that is missing on disk shows 'Not on disk', not a viewer", async (path) => {
    server.use(http.get("*/api/projects/:p/file", () => HttpResponse.json({ error: { code: "not_found", message: "file not found" } }, { status: 404 })));
    show(path, false);
    expect(await screen.findByTestId("file-missing")).toHaveTextContent("Not on disk");
    expect(screen.queryByTestId("model-body")).toBeNull();
    expect(screen.queryByTestId("dwg-body")).toBeNull();
    expect(screen.queryByTestId("media-body")).toBeNull();
  });
});
