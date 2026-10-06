import { describe, expect, it } from "vitest";
import { allowedUrl, fitDistance, modelKindOf } from "./fit";

describe("modelKindOf", () => {
  it("maps extensions", () => {
    expect(modelKindOf("a/b.GLB")).toBe("gltf");
    expect(modelKindOf("a/b.gltf")).toBe("gltf");
    expect(modelKindOf("m.stl")).toBe("stl");
    expect(modelKindOf("m.obj")).toBe("obj");
    expect(modelKindOf("m.ply")).toBe("ply");
    expect(modelKindOf("m.step")).toBeNull();
  });
  it("ignores a dot in a folder name and files without an extension", () => {
    expect(modelKindOf("v1.glb/readme")).toBeNull();
    expect(modelKindOf("model")).toBeNull();
  });
});

describe("fitDistance", () => {
  it("is larger for a narrow view and grows with radius", () => {
    expect(fitDistance(1, 45, 1)).toBeGreaterThan(2.4);
    expect(fitDistance(2, 45, 1)).toBeCloseTo(2 * fitDistance(1, 45, 1));
    expect(fitDistance(1, 45, 0.5)).toBeGreaterThan(fitDistance(1, 45, 1));
  });
  it("does not shrink for a wide view", () => {
    expect(fitDistance(1, 45, 2)).toBeCloseTo(fitDistance(1, 45, 1));
  });
});

describe("allowedUrl", () => {
  const base = "/api/projects/demo/files/";
  it("allows files under the project and data URIs", () => {
    expect(allowedUrl("http://localhost:5173/api/projects/demo/files/m/a.bin", base)).toBe(true);
    expect(allowedUrl("/api/projects/demo/files/m/a.bin", base)).toBe(true);
    expect(allowedUrl("data:application/octet-stream;base64,AAAA", base)).toBe(true);
    expect(allowedUrl("blob:http://localhost:5173/0a1b", base)).toBe(true);
  });
  it("refuses other hosts and other API paths", () => {
    expect(allowedUrl("https://evil.example/x.png", base)).toBe(false);
    expect(allowedUrl("http://localhost:5173/api/session", base)).toBe(false);
    expect(allowedUrl("http://localhost:5173/api/projects/other/files/m/a.bin", base)).toBe(false);
  });
  it("refuses a path that climbs out of the project folder", () => {
    expect(allowedUrl("/api/projects/demo/files/../../session", base)).toBe(false);
  });
  it("refuses a URL that cannot be parsed", () => {
    expect(allowedUrl("http://[bad", base)).toBe(false);
  });
});
