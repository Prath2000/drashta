// Which preview body a file gets, by its extension (FileBody dispatches on this).
//
// - audio, video and pdf play in MediaFileBody (bytes from /raw).
// - model (3D) is drawn by ModelBody with three.js (bytes from /files/<path>).
// - dwg is drawn by DwgBody from the chitralipi routes (/dwg/layers, /dwg/entities).
// Any other file is read as text (or "No preview" when binary).

export type Viewer = "audio" | "video" | "pdf" | "model" | "dwg";

/** The viewers MediaFileBody plays. */
export type Media = Extract<Viewer, "audio" | "video" | "pdf">;

// The audio, video and pdf rows are in step with rawTypes in internal/probe/tree/read.go
// (the server sends these content types).
const VIEWERS: Record<string, Viewer> = {
  mp3: "audio", wav: "audio", m4a: "audio", aac: "audio", flac: "audio", ogg: "audio", oga: "audio", opus: "audio", aif: "audio", aiff: "audio",
  mp4: "video", mov: "video", webm: "video", m4v: "video", ogv: "video",
  pdf: "pdf",
  glb: "model", gltf: "model", stl: "model", obj: "model", ply: "model",
  dwg: "dwg",
};

/** The viewer of a path by its extension (case-blind), or null when the file is read as text. */
export function viewerOf(path: string): Viewer | null {
  const dot = path.lastIndexOf(".");
  if (dot < 0 || dot < path.lastIndexOf("/")) return null;
  return VIEWERS[path.slice(dot + 1).toLowerCase()] ?? null;
}
