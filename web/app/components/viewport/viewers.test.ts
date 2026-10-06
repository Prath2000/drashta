import { describe, expect, it } from "vitest";
import { viewerOf } from "./viewers";

describe("viewerOf", () => {
  it.each([
    ["music/song.MP3", "audio"],
    ["a/b.flac", "audio"],
    ["clip.mov", "video"],
    ["clip.webm", "video"],
    ["doc/spec.pdf", "pdf"],
    ["models/robot.glb", "model"],
    ["models/robot.GLTF", "model"],
    ["m.stl", "model"],
    ["m.obj", "model"],
    ["m.ply", "model"],
    ["plans/floor.dwg", "dwg"],
    ["plans/Floor.DWG", "dwg"],
  ])("%s -> %s", (path, want) => {
    expect(viewerOf(path)).toBe(want);
  });

  it.each(["main.go", "plans/floor.dxf", "movie.mkv", "model.step", "v1.glb/readme", "glb", ".glb/x", ""])("%s -> null", (path) => {
    expect(viewerOf(path)).toBeNull();
  });
});
