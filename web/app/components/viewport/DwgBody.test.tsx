import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { parseRef } from "~/lib/refs";
import { runEscape } from "~/lib/shortcuts";
import type { ResolveResponse } from "~/lib/types";
import { server } from "~/mocks/node";
import { renderWithRouter } from "../../../test/render";
import { DwgBody } from "./DwgBody";

// A spy engine stands in for the canvas engine: it records the calls DwgBody makes and
// keeps just enough state (entities, meta, counts) to answer the way the real one does.
const eng = vi.hoisted(() => {
  type Hooks = { progress?: (n: number) => void };
  const state = {
    ents: [] as unknown[],
    meta: undefined as unknown,
    summary: undefined as unknown,
    counts: [] as number[],
    muted: new Set<number>(),
    solo: new Set<number>(),
  };
  let hooks: Hooks = {};
  let names: string[] = [];
  const batches: number[] = [];
  const api = {
    init: vi.fn((o: { hooks?: Hooks }) => {
      hooks = o.hooks ?? {};
    }),
    destroy: vi.fn(),
    reset: vi.fn(),
    setLayers: vi.fn((rows: Array<{ name: string }>) => {
      names = rows.map((r) => r.name);
      state.counts = rows.map(() => 0);
    }),
    ingest: vi.fn((lines: string[]) => {
      batches.push(lines.length);
      let n = 0;
      for (const l of lines) {
        const o = JSON.parse(l) as { type?: string; file?: unknown; summary?: unknown; layer?: string };
        if (o.file && !o.type) state.meta = o;
        else if (o.summary) state.summary = o.summary;
        else if (o.type) {
          state.ents.push(o);
          const i = names.indexOf(o.layer ?? "");
          if (i >= 0) state.counts[i] += 1;
          n += 1;
        }
      }
      hooks.progress?.(state.ents.length);
      return n;
    }),
    fit: vi.fn(),
    refit: vi.fn(),
    toggleMute: vi.fn(() => true),
    toggleSolo: vi.fn(() => true),
    setSolo: vi.fn(),
    clearFilters: vi.fn(),
    setHover: vi.fn(),
    setModelspace: vi.fn(),
    get state() {
      return state;
    },
  };
  function reset() {
    for (const f of Object.values(api)) if (typeof f === "function" && "mockClear" in f) (f as ReturnType<typeof vi.fn>).mockClear();
    state.ents = [];
    state.meta = undefined;
    state.summary = undefined;
    state.counts = [];
    state.muted = new Set();
    state.solo = new Set();
    hooks = {};
    names = [];
    batches.length = 0;
  }
  return { api, state, batches, reset };
});

const created = vi.hoisted(() => ({ n: 0 }));
vi.mock("./cad/engine", () => ({
  createEngine: () => {
    created.n += 1;
    return eng.api;
  },
}));

const acts = [
  { action: "app", label: "Open in default app", command: "open -- /x" },
  { action: "finder", label: "Reveal in Finder", command: "open -R /x" },
];

function resolved(path: string): ResolveResponse {
  return {
    ref: "alpha:file:" + path, type: "file", projectId: "alpha", title: path, subtitle: "", icon: "file", date: null,
    obj: { path, size: 3_100_000, mtime: "2026-10-01T10:00:00Z", exists: true },
    nativeLabel: acts[0].label, nativeActions: acts, notes: [], tasks: [],
  };
}

function renderDwg(path = "plans/site.dwg") {
  const refStr = "alpha:file:" + path;
  return renderWithRouter(<DwgBody refStr={refStr} refObj={parseRef(refStr)!} resolved={resolved(path)} />);
}

const META = '{"format_version":2,"file":{"name":"site.dwg","version":"AC1024","release":"R2010"},"space":"model"}';
const SUMMARY = (n: number) => `{"summary":{"emitted":${n},"skipped":0}}`;
const LAYERS = {
  format_version: 2,
  layers: [
    { name: "A-WALL", color: { index: 1 }, entities: { model: 4000 } },
    { name: "A-DOOR", color: { index: 3 }, entities: { model: 1000 } },
    { name: "EMPTY", color: { index: 5 }, entities: { model: 0, blocks: 12 } },
  ],
};

function entityLines(n: number): string[] {
  return Array.from({ length: n }, (_, i) =>
    `{"type":"LINE","handle":"H${i}","layer":"${i % 5 === 0 ? "A-DOOR" : "A-WALL"}","space":"model","start":[${i},0,0],"end":[${i},1,0]}`);
}

function ndjson(lines: string[], chunk = 0): Response {
  const text = lines.join("\n") + "\n";
  if (!chunk) return new HttpResponse(text, { headers: { "Content-Type": "application/x-ndjson" } });
  const bytes = new TextEncoder().encode(text);
  return new HttpResponse(
    new ReadableStream({
      start(c) {
        for (let i = 0; i < bytes.length; i += chunk) c.enqueue(bytes.slice(i, i + chunk));
        c.close();
      },
    }),
    { headers: { "Content-Type": "application/x-ndjson" } },
  ) as unknown as Response;
}

function use(layers: () => Response | Promise<Response>, entities: () => Response | Promise<Response>) {
  server.use(
    http.get("/api/projects/:p/dwg/layers", () => layers() as never),
    http.get("/api/projects/:p/dwg/entities", () => entities() as never),
  );
}

/** Waits until the whole drawing has been read. */
const done = () => waitFor(() => expect(screen.getByTestId("dwg-viewer")).toHaveAttribute("data-phase", "done"));

const ok5000 = () => use(() => HttpResponse.json(LAYERS), () => ndjson([META, ...entityLines(5000), SUMMARY(5000)], 7919));

afterEach(() => {
  eng.reset();
  created.n = 0;
  vi.restoreAllMocks();
});

describe("DwgBody", () => {
  it("sets the layers first, then feeds 5,000 entities in batches of at most 2,000 lines", async () => {
    ok5000();
    renderDwg();
    await done();
    expect(screen.getByTestId("dwg-status")).toHaveTextContent("5,000 entities");

    expect(eng.api.init).toHaveBeenCalledTimes(1);
    expect(eng.api.setModelspace).toHaveBeenCalledWith(true);
    expect(eng.api.setLayers).toHaveBeenCalledTimes(1);
    const rows = eng.api.setLayers.mock.calls[0][0] as Array<{ name: string }>;
    expect(rows.map((r) => r.name)).toEqual(["A-WALL", "A-DOOR", "EMPTY"]); // the full table: row numbers must match the engine's
    expect(eng.api.setLayers.mock.invocationCallOrder[0]).toBeLessThan(eng.api.ingest.mock.invocationCallOrder[0]);

    expect(eng.batches.length).toBeGreaterThanOrEqual(3);
    expect(Math.max(...eng.batches)).toBeLessThanOrEqual(2000);
    expect(eng.batches.reduce((a, b) => a + b, 0)).toBe(5002); // meta + 5,000 entities + summary: none lost, none doubled
    expect(eng.state.ents).toHaveLength(5000);
    expect(eng.api.fit).toHaveBeenCalled();
  });

  it("asks for the file by its project path, each part encoded", async () => {
    const seen: string[] = [];
    server.use(
      http.get("/api/projects/:p/dwg/layers", ({ request }) => {
        seen.push(request.url);
        return HttpResponse.json(LAYERS);
      }),
      http.get("/api/projects/:p/dwg/entities", ({ request }) => {
        seen.push(request.url);
        return ndjson([META, ...entityLines(2)]) as never;
      }),
    );
    renderDwg("plans/Overall Layout #3 100%.dwg");
    await done();
    expect(seen).toHaveLength(2);
    for (const u of seen) expect(new URL(u).searchParams.get("path")).toBe("plans/Overall Layout #3 100%.dwg");
    expect(seen[0]).toContain("/dwg/layers?");
    expect(seen[1]).toContain("/dwg/entities?");
  });

  it("shows 'N entities · release' from the meta line", async () => {
    use(() => HttpResponse.json(LAYERS), () => ndjson([META, ...entityLines(3), SUMMARY(3)]));
    renderDwg();
    await done();
    expect(screen.getByTestId("dwg-status")).toHaveTextContent("3 entities · R2010");
    // The "reading" overlay is gone once the stream has ended.
    expect(screen.queryByTestId("dwg-loading")).toBeNull();
  });

  it("fills the window with the same canvas and engine, and Esc brings it back", async () => {
    use(() => HttpResponse.json(LAYERS), () => ndjson([META, ...entityLines(3), SUMMARY(3)]));
    const { user } = renderDwg();
    await done();
    const stage = screen.getByTestId("dwg-stage");
    const canvas = stage.querySelector("canvas");
    await user.click(screen.getByRole("button", { name: "⤢ Full window" }));
    expect(stage).toHaveClass("vp-stage-full");
    expect(stage.querySelector("canvas")).toBe(canvas);
    act(() => void runEscape());
    expect(stage).not.toHaveClass("vp-stage-full");
  });

  it("says so when model space is empty", async () => {
    use(() => HttpResponse.json(LAYERS), () => ndjson([META, SUMMARY(0)]));
    renderDwg();
    expect(await screen.findByTestId("dwg-empty")).toHaveTextContent(/no shapes/i);
  });

  it("accepts a bare array of layers", async () => {
    use(() => HttpResponse.json(LAYERS.layers), () => ndjson([META, ...entityLines(1)]));
    renderDwg();
    await screen.findByTestId("dwg-layer-A-WALL");
    expect((eng.api.setLayers.mock.calls[0][0] as unknown[]).length).toBe(3);
  });

  it("asks for the layers and the entities at the same time, and holds the entities until the layers are in", async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    let entitiesAsked = false;
    use(
      async () => {
        await gate;
        return HttpResponse.json(LAYERS);
      },
      () => {
        entitiesAsked = true;
        return ndjson([META, ...entityLines(3), SUMMARY(3)]);
      },
    );
    renderDwg();
    // The entities request goes out while the layers are still pending.
    await waitFor(() => expect(entitiesAsked).toBe(true));
    await new Promise((r) => setTimeout(r, 40)); // time enough for the whole entity answer to arrive
    expect(eng.api.setLayers).not.toHaveBeenCalled();
    expect(eng.api.ingest).not.toHaveBeenCalled(); // held: each entity needs its layer row
    expect(screen.getByTestId("dwg-viewer")).toHaveAttribute("data-phase", "loading");

    release();
    await done();
    expect(eng.api.setLayers).toHaveBeenCalledTimes(1);
    expect(eng.api.setLayers.mock.invocationCallOrder[0]).toBeLessThan(eng.api.ingest.mock.invocationCallOrder[0]);
    expect(eng.batches.reduce((a, b) => a + b, 0)).toBe(5); // meta + 3 entities + summary: none lost, none doubled
    expect(eng.state.ents).toHaveLength(3);
    expect(eng.api.fit).toHaveBeenCalled();
  });

  it("a layers failure shows the panel at once and stops the entities request", async () => {
    const signals: Array<{ url: string; signal: AbortSignal }> = [];
    const realFetch = globalThis.fetch;
    vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.href : (input as Request).url;
      if (init?.signal) signals.push({ url, signal: init.signal });
      return realFetch(input, init);
    });
    use(
      () => HttpResponse.json({ error: { code: "unavailable", message: "chitralipi not found" } }, { status: 503 }),
      () =>
        new HttpResponse(
          new ReadableStream({
            start(c) {
              c.enqueue(new TextEncoder().encode(META + "\n")); // an entity stream that stays open
            },
          }),
        ) as unknown as Response,
    );
    renderDwg();
    expect(await screen.findByTestId("dwg-error")).toHaveAttribute("data-kind", "unavailable");
    expect(eng.api.ingest).not.toHaveBeenCalled();
    await waitFor(() => expect(signals.find((s) => s.url.includes("/dwg/entities"))?.signal.aborted).toBe(true));
  });

  it("puts the canvas and the rail in one host, so the rail follows the drawer width", async () => {
    use(() => HttpResponse.json(LAYERS), () => ndjson([META, ...entityLines(4), SUMMARY(4)]));
    const { container } = renderDwg();
    await done();
    const host = container.querySelector(".vp-cad-host");
    expect(host).not.toBeNull();
    expect(host!.querySelector(".vp-cad-frame canvas")).not.toBeNull();
    expect(host).toContainElement(screen.getByTestId("dwg-rail"));
    // jsdom has no layout: the rule itself is read from the stylesheet.
    const css = readFileSync(join(__dirname, "cad", "cad.css"), "utf8");
    expect(css).toMatch(/\.vp-cad-host\s*\{[^}]*container-type:\s*inline-size/);
    expect(css).toMatch(/@container\s*\(min-width:\s*720px\)/);
    expect(css).not.toMatch(/@media[^{]*min-width/);
  });

  it("uses the same small buttons and the same loading line as the 3D viewer", async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    use(
      async () => {
        await gate;
        return HttpResponse.json(LAYERS);
      },
      () => ndjson([META, ...entityLines(2), SUMMARY(2)]),
    );
    renderDwg();
    const loading = await screen.findByTestId("dwg-loading");
    expect(loading).toHaveAttribute("role", "status");
    expect(within(loading).getByText(/^Loading .+…$/)).toHaveClass("vp-loading");
    release();
    await done();
    for (const name of ["Zoom to extent", "Show all"]) {
      const b = screen.getByRole("button", { name });
      expect(b).toHaveClass("vp-btn", "sm");
      expect(b).not.toHaveClass("ghost");
    }
  });

  describe("the layer rail", () => {
    async function ready() {
      use(() => HttpResponse.json(LAYERS), () => ndjson([META, ...entityLines(10), SUMMARY(10)]));
      const r = renderDwg();
      await done();
      return r;
    }

    it("lists the layers that have shapes in model space, with counts", async () => {
      await ready();
      const rail = screen.getByTestId("dwg-rail");
      expect(within(rail).getByText("A-WALL")).toBeInTheDocument();
      expect(within(rail).getByText("A-DOOR")).toBeInTheDocument();
      expect(within(rail).queryByText("EMPTY")).toBeNull(); // 0 shapes in model space: nothing to toggle
      expect(within(screen.getByTestId("dwg-layer-A-WALL")).getByText("8")).toBeInTheDocument();
      expect(within(screen.getByTestId("dwg-layer-A-DOOR")).getByText("2")).toBeInTheDocument();
    });

    it("a click toggles the layer on or off by its row in the full table", async () => {
      const { user } = await ready();
      const door = screen.getByTestId("dwg-layer-A-DOOR");
      expect(door).toHaveAttribute("aria-pressed", "true");
      await user.click(door);
      expect(eng.api.toggleMute).toHaveBeenCalledWith(1);
      expect(door).toHaveAttribute("aria-pressed", "false");
      await user.click(door);
      expect(eng.api.toggleMute).toHaveBeenCalledTimes(2);
      expect(door).toHaveAttribute("aria-pressed", "true");
      expect(eng.api.toggleSolo).not.toHaveBeenCalled();
    });

    it("shift-click shows only that layer", async () => {
      await ready();
      const wall = screen.getByTestId("dwg-layer-A-WALL");
      fireEvent.click(wall, { shiftKey: true });
      expect(eng.api.toggleSolo).toHaveBeenCalledWith(0);
      expect(eng.api.toggleMute).not.toHaveBeenCalled();
      expect(wall).toHaveAttribute("data-solo", "true");
    });

    it("'Show all' clears the filters, and is off while nothing is filtered", async () => {
      const { user } = await ready();
      const all = screen.getByRole("button", { name: "Show all" });
      expect(all).toBeDisabled();
      await user.click(screen.getByTestId("dwg-layer-A-WALL"));
      expect(all).toBeEnabled();
      await user.click(all);
      expect(eng.api.clearFilters).toHaveBeenCalledTimes(1);
      expect(all).toBeDisabled();
      expect(screen.getByTestId("dwg-layer-A-WALL")).toHaveAttribute("aria-pressed", "true");
    });

    it("hovering a layer dims the others", async () => {
      const { user } = await ready();
      await user.hover(screen.getByTestId("dwg-layer-A-DOOR"));
      expect(eng.api.setHover).toHaveBeenLastCalledWith(1);
      await user.unhover(screen.getByTestId("dwg-layer-A-DOOR"));
      expect(eng.api.setHover).toHaveBeenLastCalledWith(-1);
    });
  });

  it("'Zoom to extent' frames the whole drawing", async () => {
    use(() => HttpResponse.json(LAYERS), () => ndjson([META, ...entityLines(4)]));
    const { user } = renderDwg();
    await done();
    eng.api.fit.mockClear();
    await user.click(screen.getByRole("button", { name: "Zoom to extent" }));
    expect(eng.api.fit).toHaveBeenCalledTimes(1);
  });

  describe("when the drawing cannot be shown", () => {
    const json = (status: number, code: string, message: string) => HttpResponse.json({ error: { code, message } }, { status });

    it("503: the server's message, no canvas, and the native buttons", async () => {
      const msg = "/Users/x/.local/bin/chitralipi not found; set chitralipi in config.toml";
      use(() => json(503, "unavailable", msg), () => json(503, "unavailable", msg));
      renderDwg();
      const panel = await screen.findByTestId("dwg-error");
      expect(panel).toHaveAttribute("data-kind", "unavailable");
      expect(panel).toHaveAttribute("role", "alert");
      expect(panel).toHaveTextContent("chitralipi not found; set chitralipi in config.toml");
      expect(panel).toHaveTextContent("Open it in an app instead.");
      expect(screen.getByTestId("native-buttons")).toBeInTheDocument();
      expect(screen.queryByTestId("dwg-status")).toBeNull();
      expect(screen.queryByTestId("dwg-loading")).toBeNull();
      expect(eng.api.ingest).not.toHaveBeenCalled();
      expect(eng.api.destroy).toHaveBeenCalled(); // no render loop left behind
    });

    it("422: 'chitralipi could not read this drawing' with the message", async () => {
      use(() => HttpResponse.json(LAYERS), () => json(422, "bad_drawing", "x.dwg is not a DWG: it does not start with an AC version code"));
      renderDwg();
      const panel = await screen.findByTestId("dwg-error");
      expect(panel).toHaveAttribute("data-kind", "bad");
      expect(panel).toHaveTextContent("chitralipi could not read this drawing");
      expect(panel).toHaveTextContent("it does not start with an AC version code");
      expect(screen.getByTestId("native-buttons")).toBeInTheDocument();
      expect(eng.api.destroy).toHaveBeenCalled();
    });

    it("any other failure is a panel too, never a spinner", async () => {
      use(() => json(500, "internal", "drawing failed"), () => json(500, "internal", "drawing failed"));
      renderDwg();
      const panel = await screen.findByTestId("dwg-error");
      expect(panel).toHaveAttribute("data-kind", "failed");
      expect(panel).toHaveTextContent("drawing failed");
      expect(panel).toHaveTextContent("Open it in an app instead.");
      expect(screen.getByTestId("native-buttons")).toBeInTheDocument();
    });

    it("a network error is a panel", async () => {
      use(() => HttpResponse.error(), () => HttpResponse.error());
      renderDwg();
      const panel = await screen.findByTestId("dwg-error");
      expect(panel).toHaveAttribute("data-kind", "failed");
      expect(screen.getByTestId("native-buttons")).toBeInTheDocument();
    });

    it("a stream that breaks half way is a panel and stops the engine", async () => {
      let sent = false;
      use(
        () => HttpResponse.json(LAYERS),
        () =>
          new HttpResponse(
            new ReadableStream({
              pull(c) {
                if (sent) return c.error(new Error("connection reset"));
                sent = true;
                c.enqueue(new TextEncoder().encode(META + "\n" + entityLines(1)[0] + "\n"));
              },
            }),
          ) as unknown as Response,
      );
      renderDwg();
      expect(await screen.findByTestId("dwg-error")).toHaveAttribute("data-kind", "failed");
      expect(eng.api.destroy).toHaveBeenCalled();
    });
  });

  it("leaving the page stops the engine and aborts the entities request", async () => {
    const signals: Array<{ url: string; signal: AbortSignal }> = [];
    const realFetch = globalThis.fetch;
    vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.href : (input as Request).url;
      if (init?.signal) signals.push({ url, signal: init.signal });
      return realFetch(input, init);
    });
    // The entities stream sends the meta line and then stays open.
    use(
      () => HttpResponse.json(LAYERS),
      () =>
        new HttpResponse(
          new ReadableStream({
            start(c) {
              c.enqueue(new TextEncoder().encode(META + "\n"));
            },
          }),
        ) as unknown as Response,
    );
    const { unmount } = renderDwg();
    await waitFor(() => expect(eng.api.setLayers).toHaveBeenCalled());
    await waitFor(() => expect(signals.some((s) => s.url.includes("/dwg/entities"))).toBe(true));
    const entities = signals.find((s) => s.url.includes("/dwg/entities"))!;
    expect(entities.signal.aborted).toBe(false);

    await act(async () => {
      unmount();
    });
    expect(eng.api.destroy).toHaveBeenCalledTimes(1);
    expect(entities.signal.aborted).toBe(true);
    expect(signals.find((s) => s.url.includes("/dwg/layers"))!.signal.aborted).toBe(true);
  });

  it("leaving while the layers are still loading ends cleanly", async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    use(
      async () => {
        await gate;
        return HttpResponse.json(LAYERS);
      },
      () => ndjson([META, ...entityLines(2)]),
    );
    const { unmount } = renderDwg();
    await waitFor(() => expect(eng.api.init).toHaveBeenCalled());
    unmount();
    release();
    await new Promise((r) => setTimeout(r, 30));
    expect(eng.api.destroy).toHaveBeenCalledTimes(1);
    expect(eng.api.setLayers).not.toHaveBeenCalled();
    expect(eng.api.ingest).not.toHaveBeenCalled();
  });

  it("opening another drawing starts a fresh engine and stops the old one", async () => {
    use(() => HttpResponse.json(LAYERS), () => ndjson([META, ...entityLines(2)]));
    const first = renderDwg("a.dwg");
    await done();
    first.unmount();
    expect(eng.api.destroy).toHaveBeenCalledTimes(1);
    renderDwg("b.dwg");
    await waitFor(() => expect(created.n).toBe(2));
  });
});
