// Audio, video and PDF preview body (FileBody hands these over by extension).
//
// - The bytes come from GET /projects/{p}/raw?path= (Range requests work, so seeking works).
// - A file the browser cannot play (codec, damaged file) says so and offers the native apps.
// - Every native action is a button under the player: the default app first.
import { useEffect, useRef, useState } from "react";
import { api } from "~/lib/api";
import type { ViewportBodyProps } from "./registry";
import { baseRef, FullWindowButton, MediaNotes, NativeButtons, useFullWindow, useNotes } from "./common";
import { fmtDate, fmtSize } from "./fmt";
import type { FileObj } from "./vtypes";
import { viewerOf, type Media } from "./viewers";

export type { Media };

/** The media kind of a path by its extension (audio, video or pdf), or null. */
export function mediaOf(path: string): Media | null {
  const v = viewerOf(path);
  return v === "audio" || v === "video" || v === "pdf" ? v : null;
}

const LABEL: Record<Media, string> = { audio: "Audio", video: "Video", pdf: "PDF" };

/**
 * Plash (the desktop wallpaper app) sets muted=true on every audio and video, and again on each
 * page change (drashta re-renders every few seconds), so a player starts silent and goes silent
 * again. In the desktop modes, undo that, unless the player's own controls were just used.
 */
function useUnmutedInDesktop(active: boolean) {
  const ref = useRef<HTMLAudioElement & HTMLVideoElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!active || !el || !document.documentElement.classList.contains("desktop")) return;
    let touched = 0;
    const touch = () => {
      touched = Date.now();
    };
    const onVolume = () => {
      if (el.muted && Date.now() - touched > 500) el.muted = false;
    };
    el.muted = false;
    el.addEventListener("pointerdown", touch);
    el.addEventListener("keydown", touch);
    el.addEventListener("volumechange", onVolume);
    return () => {
      el.removeEventListener("pointerdown", touch);
      el.removeEventListener("keydown", touch);
      el.removeEventListener("volumechange", onVolume);
    };
  }, [active]);
  return ref;
}

export function MediaFileBody({ refStr, refObj, resolved, media }: ViewportBodyProps & { media: Media }) {
  const p = resolved.projectId || refObj.projectId;
  const obj = (resolved.obj ?? {}) as FileObj;
  const path = obj.path ?? refObj.id;
  const ref = baseRef(refObj);
  const notes = useNotes(ref, resolved.notes ?? []);
  const [failed, setFailed] = useState(false);
  const { full, toggle: toggleFull } = useFullWindow();
  const src = api.rawUrl(p, path);
  const fail = () => setFailed(true);
  const playerRef = useUnmutedInDesktop(!failed && (media === "audio" || media === "video"));

  return (
    <div className="vp-scroll" data-testid="media-body">
      <div className="vp-pad">
        <div className="vp-stack">
          <div className="vp-label">
            {LABEL[media]} · {fmtSize(obj.size ?? null)}
            {obj.mtime ? " · modified " + fmtDate(obj.mtime) : ""}
          </div>
          {failed ? (
            <div className="vp-panel" data-testid="media-failed">
              <span className="vp-label dim">No preview</span>
              <span className="big">This {LABEL[media].toLowerCase()} file cannot play here.</span>
              <span className="why">The browser does not know its format, or the file is damaged. Open it in an app instead.</span>
            </div>
          ) : media === "audio" ? (
            <audio key={src} ref={playerRef} src={src} controls preload="metadata" onError={fail} style={{ width: "100%" }} data-testid="media-audio" />
          ) : media === "video" ? (
            <video key={src} ref={playerRef} src={src} controls preload="metadata" playsInline onError={fail} style={{ width: "100%", maxHeight: "70vh", background: "#000", borderRadius: 6 }} data-testid="media-video" />
          ) : (
            <div className={"vp-pdf-stage" + (full ? " vp-stage-full" : "")} data-testid="pdf-stage">
              <iframe key={src} src={src} title={path} data-testid="media-pdf" />
              <div className="bar">
                <FullWindowButton full={full} onToggle={toggleFull} />
              </div>
            </div>
          )}
          <NativeButtons refStr={refStr} actions={resolved.nativeActions ?? []} />
        </div>
        <MediaNotes refStr={ref} projectId={p} notes={notes} />
      </div>
    </div>
  );
}
