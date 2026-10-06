import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { parseRef } from "~/lib/refs";
import { runEscape } from "~/lib/shortcuts";
import type { ResolveResponse } from "~/lib/types";
import { server } from "~/mocks/node";
import { renderWithRouter } from "../../../test/render";
import { FileBody } from "./FileBody";
import { FolderBody } from "./FolderBody";
import { mediaOf } from "./MediaFileBody";

const acts = [
  { action: "app", label: "Open in default app", command: "open -- /x" },
  { action: "editor", label: "Open in editor", command: "subl /x:1" },
  { action: "finder", label: "Reveal in Finder", command: "open -R /x" },
];

function resolved(type: string, path: string): ResolveResponse {
  return {
    ref: "alpha:file:" + path, type: type as ResolveResponse["type"], projectId: "alpha", title: path, subtitle: "", icon: type, date: null,
    obj: { path, size: 2048, mtime: "2026-10-01T10:00:00Z", exists: true },
    nativeLabel: acts[0].label, nativeActions: acts, notes: [], tasks: [],
  };
}

function renderBody(Body: typeof FileBody, type: string, path: string) {
  const refStr = "alpha:file:" + path;
  return renderWithRouter(<Body refStr={refStr} refObj={parseRef(refStr)!} resolved={resolved(type, path)} />);
}

describe("mediaOf", () => {
  it("knows audio, video and PDF by extension, case-blind", () => {
    expect(mediaOf("music/song.MP3")).toBe("audio");
    expect(mediaOf("a/b.flac")).toBe("audio");
    expect(mediaOf("clip.mov")).toBe("video");
    expect(mediaOf("doc/spec.pdf")).toBe("pdf");
    expect(mediaOf("main.go")).toBeNull();
    expect(mediaOf("v1.mp3/readme")).toBeNull();
    expect(mediaOf("movie.mkv")).toBeNull();
  });
});

describe("media file body", () => {
  it("plays audio from /raw and offers every native app", async () => {
    let opened: unknown = null;
    server.use(
      http.post("/api/open", async ({ request }) => {
        opened = await request.json();
        return HttpResponse.json({ ok: true, ran: "open -- /x" });
      }),
    );
    const { user } = renderBody(FileBody, "file", "music/song.mp3");
    const body = await screen.findByTestId("media-body");
    expect(within(body).getByTestId("media-audio").getAttribute("src")).toBe("/api/projects/alpha/raw?path=music%2Fsong.mp3");
    const btns = within(body).getByTestId("native-buttons");
    expect(within(btns).getAllByRole("button").map((b) => b.textContent)).toEqual(["Open in default app ↗", "Open in editor ↗", "Reveal in Finder ↗"]);
    await user.click(within(btns).getByRole("button", { name: "Open in default app ↗" }));
    await waitFor(() => expect(opened).toEqual({ ref: "alpha:file:music/song.mp3", action: "app" }));
  });

  it("in the desktop modes, undoes Plash muting the player, unless the player's own controls were used", async () => {
    document.documentElement.classList.add("desktop");
    try {
      renderBody(FileBody, "file", "music/song.mp3");
      const audio = (await screen.findByTestId("media-audio")) as HTMLAudioElement;
      // Plash's injected script: muted = true on every audio and video, again on each page change.
      await act(async () => {
        audio.muted = true;
        await new Promise((r) => setTimeout(r, 0));
      });
      expect(audio.muted).toBe(false);
      // The user presses the player's mute button: that stays.
      fireEvent.pointerDown(audio);
      await act(async () => {
        audio.muted = true;
        await new Promise((r) => setTimeout(r, 0));
      });
      expect(audio.muted).toBe(true);
    } finally {
      document.documentElement.classList.remove("desktop");
    }
  });

  it("shows video and PDF players, and a clear state when the video cannot play", async () => {
    renderBody(FileBody, "file", "clips/demo.mp4");
    const video = await screen.findByTestId("media-video");
    fireEvent.error(video);
    expect(await screen.findByTestId("media-failed")).toHaveTextContent("cannot play here");
    expect(screen.getByTestId("native-buttons")).toBeInTheDocument();
  });

  it("frames a PDF", async () => {
    renderBody(FileBody, "file", "spec.pdf");
    expect((await screen.findByTestId("media-pdf")).getAttribute("src")).toBe("/api/projects/alpha/raw?path=spec.pdf");
  });

  it("fills the window with the same PDF frame, and Esc brings it back", async () => {
    const { user } = renderBody(FileBody, "file", "spec.pdf");
    const frame = await screen.findByTestId("media-pdf");
    await user.click(screen.getByRole("button", { name: "⤢ Full window" }));
    expect(screen.getByTestId("pdf-stage")).toHaveClass("vp-stage-full");
    expect(screen.getByTestId("media-pdf")).toBe(frame);
    act(() => void runEscape());
    expect(screen.getByTestId("pdf-stage")).not.toHaveClass("vp-stage-full");
  });
});

describe("folder body", () => {
  it("lists entries folders first and opens one in the viewport", async () => {
    server.use(
      http.get("/api/projects/:p/tree", () =>
        HttpResponse.json({
          dir: "src",
          entries: [
            { path: "src/b.go", dir: false, size: 120, mtime: null, hasChildren: false },
            { path: "src/lib", dir: true, size: null, mtime: null, hasChildren: true },
            { path: "src/node_modules", dir: true, size: null, mtime: null, hasChildren: true, collapsed: true },
          ],
        }),
      ),
    );
    const { user, router } = renderBody(FolderBody, "folder", "src");
    const body = await screen.findByTestId("folder-body");
    await waitFor(() => expect(body.querySelectorAll(".vp-folder-row").length).toBe(3));
    const names = [...body.querySelectorAll(".vp-folder-row .name")].map((n) => n.textContent);
    expect(names).toEqual(["lib/", "node_modules/", "b.go"]);
    expect(within(body).getByRole("button", { name: /node_modules/ })).toBeDisabled();
    await user.click(within(body).getByRole("button", { name: /b\.go/ }));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("alpha:file:src/b.go");
  });
});
