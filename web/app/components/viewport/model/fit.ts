// Pure helpers for the 3D model viewer (no three.js, no DOM except `location` in allowedUrl).

export type ModelKind = "gltf" | "stl" | "obj" | "ply";

const KINDS: Record<string, ModelKind> = { glb: "gltf", gltf: "gltf", stl: "stl", obj: "obj", ply: "ply" };

/** The loader kind of a path by its extension (case-blind; glb and gltf are both "gltf"), or null. */
export function modelKindOf(path: string): ModelKind | null {
  const dot = path.lastIndexOf(".");
  if (dot < 0 || dot < path.lastIndexOf("/")) return null;
  return KINDS[path.slice(dot + 1).toLowerCase()] ?? null;
}

/**
 * Camera distance at which a sphere of `radius` fills the view.
 * `fovDeg` is the vertical field of view, `aspect` is width / height. A narrow view
 * (aspect below 1) is limited by its width, so the camera moves back. Includes a 15% margin.
 */
export function fitDistance(radius: number, fovDeg: number, aspect: number): number {
  const half = (fovDeg * Math.PI) / 180 / 2;
  return (radius / Math.sin(half)) * Math.max(1, 1 / aspect) * 1.15;
}

/**
 * True only for URLs a model may fetch: data: and blob: URLs, and same-origin URLs whose
 * path is under `base` (for example "/api/projects/<p>/files/"). A URL that cannot be parsed is refused.
 * The path is resolved first, so "../" cannot climb out of `base`.
 */
export function allowedUrl(url: string, base: string): boolean {
  let u: URL;
  try {
    u = new URL(url, location.origin);
  } catch {
    return false;
  }
  if (u.protocol === "data:" || u.protocol === "blob:") return true;
  return u.origin === location.origin && u.pathname.startsWith(base);
}
