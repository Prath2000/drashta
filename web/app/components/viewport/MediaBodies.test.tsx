import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { parseRef } from "~/lib/refs";
import { setState } from "~/lib/store";
import type { EmbedInfo, ResolveResponse } from "~/lib/types";
import { mockDb } from "~/mocks/handlers";
import { server } from "~/mocks/node";
import { snapshotCreateHandler } from "~/mocks/viewport";
import { renderApp, renderWithRouter } from "../../../test/render";
import { ServerBody } from "./ServerBody";

const IMG = "/?item=notes-app:image:design/screenshots/ideas2.png";
const SNAP = "/?item=notes-app:snapshot:T-1/v3";

describe("image body", () => {
  it("loads the image from /raw, zooms, and lists the snapshots that use it", async () => {
    const { user } = renderApp(IMG);
    const body = await screen.findByTestId("image-body");
    const img = within(body).getByRole("img", { name: "design/screenshots/ideas2.png" });
    expect(img.getAttribute("src")).toBe("/api/projects/notes-app/raw?path=design%2Fscreenshots%2Fideas2.png");
    const fit = within(body).getByRole("button", { name: "Fit" });
    expect(fit).toHaveAttribute("aria-pressed", "true");
    await user.click(within(body).getByRole("button", { name: "100%" }));
    expect(fit).toHaveAttribute("aria-pressed", "false");
    expect(within(body).getByTestId("pin-stage").className).toContain("full");
    await waitFor(() => expect(within(body).getByTestId("used-in")).toHaveTextContent("Ideas page · v2"));
  });

  it("drops a point note at x,y with Annotate", async () => {
    let posted: Record<string, unknown> | null = null;
    server.events.on("request:start", async ({ request }) => {
      if (request.method === "POST" && request.url.endsWith("/api/notes")) posted = (await request.clone().json()) as Record<string, unknown>;
    });
    const { user } = renderApp(IMG);
    const body = await screen.findByTestId("image-body");
    await user.click(within(body).getByRole("button", { name: "Annotate" }));
    // keyboard-style press (detail 0) -> the cell centre
    fireEvent.click(within(body).getByRole("button", { name: "Drop pin at column 6, row 2" }), { detail: 0 });
    const comp = within(body).getByTestId("pin-composer");
    expect(comp).toHaveTextContent("Pin 1 · 46%, 19%");
    await user.type(within(comp).getByRole("textbox"), "too dark here");
    await user.click(within(comp).getByRole("button", { name: "Add note" }));
    await waitFor(() => expect(posted).toEqual({ ref: "notes-app:image:design/screenshots/ideas2.png", text: "too dark here", x: 0.4583, y: 0.1875 }));
    await waitFor(() => expect(within(body).getByRole("button", { name: "Note 1: too dark here" })).toBeInTheDocument());
    expect(within(body).getByTestId("pin-notes")).toHaveTextContent("too dark here");
    server.events.removeAllListeners();
  });

  it("makes a snapshot for a task from the image", async () => {
    server.use(snapshotCreateHandler);
    const { user } = renderApp(IMG);
    const body = await screen.findByTestId("image-body");
    const sel = within(body).getByRole("combobox", { name: "Make snapshot for task…" });
    await waitFor(() => expect(within(sel).getAllByRole("option").length).toBeGreaterThan(1));
    const btn = within(body).getByRole("button", { name: "Make snapshot" });
    expect(btn).toBeDisabled();
    await user.selectOptions(sel, "T-1");
    await user.click(btn);
    await waitFor(() => expect(screen.getByTestId("toast")).toHaveTextContent("Snapshot v4 added to T-1"));
    expect(mockDb().snapshots.some((s) => s.taskId === "T-1" && s.version === 4 && s.path === "design/screenshots/ideas2.png")).toBe(true);
  });
});

describe("snapshot body", () => {
  it("shows numbered pins from x,y notes, versions and the verdict", async () => {
    renderApp(SNAP);
    const body = await screen.findByTestId("snapshot-body");
    const pin1 = await within(body).findByRole("button", { name: /^Note 1: Constellation lines/ });
    expect(pin1.style.left).toBe("46%");
    expect(pin1.style.top).toBe("20%");
    expect(within(body).getByRole("button", { name: /^Note 2:/ })).toBeInTheDocument();
    await waitFor(() => expect(within(body).getByRole("button", { name: "Version 3, pending (current)" })).toHaveAttribute("aria-current", "true"));
    expect(within(body).getByTestId("verdict")).toHaveTextContent("Verdictpending");
  });

  it("compares with another version side by side and swaps", async () => {
    const { user } = renderApp(SNAP);
    const body = await screen.findByTestId("snapshot-body");
    await user.click(await within(body).findByRole("button", { name: "Compare" }));
    const cmp = within(body).getByTestId("compare");
    const imgs = within(cmp).getAllByRole("img");
    expect(imgs.map((i) => i.getAttribute("alt"))).toEqual(["v2", "v3"]);
    await user.click(within(cmp).getByRole("button", { name: "⇄ Swap" }));
    expect(within(cmp).getAllByRole("img").map((i) => i.getAttribute("alt"))).toEqual(["v3", "v2"]);
  });

  it("request changes needs a note, then sends the verdict", async () => {
    const { user } = renderApp(SNAP);
    const v = await screen.findByTestId("verdict");
    await user.click(within(v).getByRole("button", { name: "Request changes" }));
    const send = within(v).getByRole("button", { name: "Send back" });
    expect(send).toBeDisabled();
    await user.type(within(v).getByRole("textbox", { name: "What needs to change" }), "dim the lines");
    await user.click(send);
    await waitFor(() => expect(mockDb().snapshots.find((s) => s.id === "T-1/v3")!.verdict).toBe("changes"));
    expect(mockDb().snapshots.find((s) => s.id === "T-1/v3")!.reviewNote).toBe("dim the lines");
    await waitFor(() => expect(screen.getByTestId("toast")).toHaveTextContent("v3 → changes requested"));
  });

  it("annotates the snapshot frame", async () => {
    const { user } = renderApp(SNAP);
    const body = await screen.findByTestId("snapshot-body");
    await user.click(within(body).getByRole("button", { name: "Annotate" }));
    fireEvent.click(within(body).getByRole("button", { name: "Drop pin at column 1, row 1" }), { detail: 0 });
    const comp = within(body).getByTestId("pin-composer");
    expect(comp).toHaveTextContent("Pin 3 ·");
    await user.click(within(comp).getByRole("button", { name: "Cancel" }));
    expect(within(body).queryByTestId("pin-composer")).toBeNull();
  });
});

const SRV = "/?item=notes-app:server:8765";

/** Overrides the mock GET /servers/embed answer for one port. */
function embedOf(port: number, patch: Partial<EmbedInfo>) {
  mockDb().servers.find((s) => s.port === port)!.embed = patch;
}

/** Gives a mock server a last capture (decision Q8's capture view). */
function withCapture(port: number) {
  const real = mockDb().servers.find((s) => s.port === port)! as unknown as Record<string, unknown>;
  real.lastCapture = { path: "design/screenshots/ideas2.png", captured: "2026-09-30T10:00:00Z" };
}

describe("server body", () => {
  it("shows status, the no-capture page and the processes", async () => {
    embedOf(8765, { frameable: false, reason: "X-Frame-Options: DENY" });
    renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    await waitFor(() => expect(within(body).getByTestId("server-status")).toHaveTextContent("running"));
    expect(within(body).getByTestId("server-page")).toHaveTextContent("No capture yet");
    expect(within(body).getByTestId("procs")).toHaveTextContent("8765");
  });

  it("a helper is not a web page", async () => {
    renderApp("/?item=notes-app:server:51738");
    const body = await screen.findByTestId("server-body");
    await waitFor(() => expect(within(body).getByTestId("server-page")).toHaveTextContent("Not a web page"));
  });

  it("kill asks for a confirm, then stops the server; start brings it back", async () => {
    const { user } = renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    await waitFor(() => expect(within(body).getByTestId("server-status")).toHaveTextContent("running"));
    await user.click(within(body).getByRole("button", { name: "■ Kill process" }));
    expect(mockDb().servers.find((s) => s.port === 8765)!.running).toBe(true);
    await user.click(within(body).getByRole("button", { name: "Confirm kill :8765" }));
    await waitFor(() => expect(mockDb().servers.find((s) => s.port === 8765)!.running).toBe(false));
    await waitFor(() => expect(within(body).getByTestId("server-status")).toHaveTextContent("stopped"));
    expect(within(body).getByTestId("server-page")).toHaveTextContent("This site can’t be reached");
    expect(screen.getByTestId("toast")).toHaveTextContent(":8765 stopped");
    await user.click(within(body).getByRole("button", { name: "▶ Start" }));
    await user.click(within(body).getByRole("button", { name: "Confirm start :8765" }));
    await waitFor(() => expect(within(body).getByTestId("server-status")).toHaveTextContent("running"));
  });

  it("shows the last capture when the server knows one", async () => {
    withCapture(8765);
    embedOf(8765, { frameable: false, reason: "X-Frame-Options: DENY" });
    renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    const img = await within(body).findByRole("img", { name: "Last capture of localhost:8765" });
    expect(img.getAttribute("src")).toContain("/raw?path=design%2Fscreenshots%2Fideas2.png");
  });

  it("shows the live app in an iframe when it can be framed", async () => {
    renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    const frame = await within(body).findByTestId("server-frame");
    expect(frame.tagName).toBe("IFRAME");
    expect(frame).toHaveAttribute("src", "http://localhost:8765/");
    expect(frame).toHaveAttribute("title", "localhost:8765");
    expect(frame.closest(".vp-browser .page")).not.toBeNull();
    // the frame replaces the capture / "No capture yet"
    expect(within(body).getByTestId("server-page")).not.toHaveTextContent("No capture yet");
    expect(within(body).queryByTestId("embed-note")).toBeNull();
  });

  it("the iframe may run the app but can never navigate the drashta window", async () => {
    renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    const frame = await within(body).findByTestId("server-frame");
    const flags = (frame.getAttribute("sandbox") ?? "").split(/\s+/);
    expect(flags).toEqual(["allow-scripts", "allow-same-origin", "allow-forms"]);
    expect(flags.some((f) => f.startsWith("allow-top-navigation"))).toBe(false);
  });

  it("helpers are checked by the server, not by their kind: an HTTP helper gets the frame", async () => {
    embedOf(51738, { http: true, frameable: true, reason: "" });
    renderApp("/?item=notes-app:server:51738");
    const body = await screen.findByTestId("server-body");
    expect(await within(body).findByTestId("server-frame")).toHaveAttribute("src", "http://localhost:51738/");
    expect(body).toHaveTextContent("helper process");
    expect(within(body).getByTestId("server-page")).not.toHaveTextContent("Not a web page");
  });

  it("an app that is not HTTP is 'Not a web page', even for a dev server", async () => {
    embedOf(8765, { http: false, frameable: false, reason: "not an http server" });
    renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    await waitFor(() => expect(within(body).getByTestId("server-page")).toHaveTextContent("Not a web page"));
    expect(body).toHaveTextContent("dev server");
    expect(within(body).queryByTestId("server-frame")).toBeNull();
    expect(within(body).queryByTestId("embed-note")).toBeNull();
    expect(within(body).queryByRole("button", { name: "Expand" })).toBeNull();
    expect(within(body).queryByRole("img", { name: /Last capture/ })).toBeNull();
  });

  it("an app that refuses to be embedded shows the last capture and says why", async () => {
    withCapture(8765);
    embedOf(8765, { frameable: false, reason: "X-Frame-Options: DENY" });
    renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    const note = await within(body).findByTestId("embed-note");
    expect(note).toHaveTextContent("This app refuses to be embedded (X-Frame-Options: DENY) — use Open in Safari ↗");
    expect(within(body).getByRole("img", { name: "Last capture of localhost:8765" })).toBeInTheDocument();
    expect(within(body).queryByTestId("server-frame")).toBeNull();
    // nothing to toggle: there is no live view
    expect(within(body).queryByRole("group", { name: "Preview" })).toBeNull();
  });

  it("an app that refuses to be embedded and has no capture says 'No capture yet' plus why", async () => {
    embedOf(8765, { frameable: false, reason: "CSP frame-ancestors" });
    renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    expect(await within(body).findByTestId("embed-note")).toHaveTextContent("(CSP frame-ancestors)");
    expect(within(body).getByTestId("server-page")).toHaveTextContent("No capture yet");
    expect(within(body).queryByTestId("server-frame")).toBeNull();
  });

  it("shows the capture, not an empty frame, while the embed answer is loading", async () => {
    withCapture(8765);
    let release!: () => void;
    const gate = new Promise<void>((r) => {
      release = r;
    });
    server.use(
      http.get("*/api/servers/embed", async () => {
        await gate;
        return HttpResponse.json<EmbedInfo>({ http: true, frameable: true, reason: "", title: "slow app" });
      }),
    );
    renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    await within(body).findByRole("img", { name: "Last capture of localhost:8765" });
    expect(within(body).queryByTestId("server-frame")).toBeNull();
    expect(within(body).queryByRole("button", { name: "Expand" })).toBeNull();
    release();
    await within(body).findByTestId("server-frame");
    expect(within(body).queryByRole("img", { name: /Last capture/ })).toBeNull();
    expect(body).toHaveTextContent("slow app"); // the page title becomes the tab
  });

  it("a stopped server is unchanged: can't be reached, no frame, no Expand", async () => {
    mockDb().servers.find((s) => s.port === 8765)!.running = false;
    renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    await waitFor(() => expect(within(body).getByTestId("server-status")).toHaveTextContent("stopped"));
    expect(within(body).getByTestId("server-page")).toHaveTextContent("This site can’t be reached");
    expect(within(body).queryByTestId("server-frame")).toBeNull();
    expect(within(body).queryByRole("button", { name: "Expand" })).toBeNull();
  });

  it("the Live / Capture toggle swaps the frame for the last capture and back", async () => {
    withCapture(8765);
    const { user } = renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    await within(body).findByTestId("server-frame");
    const live = within(body).getByRole("button", { name: "Live" });
    const capture = within(body).getByRole("button", { name: "Capture" });
    expect(live).toHaveAttribute("aria-pressed", "true");
    expect(capture).toHaveAttribute("aria-pressed", "false");
    await user.click(capture);
    expect(within(body).queryByTestId("server-frame")).toBeNull();
    expect(within(body).getByRole("img", { name: "Last capture of localhost:8765" })).toBeInTheDocument();
    expect(capture).toHaveAttribute("aria-pressed", "true");
    await user.click(live);
    expect(within(body).getByTestId("server-frame")).toBeInTheDocument();
    expect(within(body).queryByRole("img", { name: /Last capture/ })).toBeNull();
  });

  it("reload reloads the frame (a new iframe) and asks the server again", async () => {
    let embedCalls = 0;
    server.events.on("request:start", ({ request }) => {
      if (request.url.includes("/api/servers/embed")) embedCalls++;
    });
    const { user } = renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    const before = await within(body).findByTestId("server-frame");
    await waitFor(() => expect(embedCalls).toBe(1));
    await user.click(within(body).getByRole("button", { name: "Reload page" }));
    await waitFor(() => expect(within(body).getByTestId("server-frame")).not.toBe(before));
    await waitFor(() => expect(embedCalls).toBeGreaterThanOrEqual(2));
    server.events.removeAllListeners();
  });

  it("Expand makes the same browser fixed and full; Esc collapses only, the next Esc closes the viewport", async () => {
    const { user, router } = renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    const frame = await within(body).findByTestId("server-frame");
    const browser = frame.closest(".vp-browser") as HTMLElement;
    expect(browser).not.toHaveClass("full");
    const expand = within(body).getByRole("button", { name: "Expand" });
    expect(expand).toHaveTextContent("⤢");

    await user.click(expand);
    expect(browser).toHaveClass("full");
    // the same DOM nodes: the app inside the frame is not reloaded
    expect(frame.closest(".vp-browser")).toBe(browser);
    expect(within(body).getByTestId("server-frame")).toBe(frame);
    expect(frame.isConnected).toBe(true);
    const collapse = within(body).getByRole("button", { name: "Collapse" });
    expect(collapse).toHaveTextContent("⤡");

    await user.keyboard("{Escape}");
    expect(browser).not.toHaveClass("full");
    expect(within(body).getByTestId("server-frame")).toBe(frame);
    expect(screen.getByTestId("viewport")).toBeInTheDocument();
    expect(router.state.location.search).toContain("item=");
    expect(within(body).getByRole("button", { name: "Expand" })).toBeInTheDocument();

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByTestId("viewport")).toBeNull());
    expect(router.state.location.search).toBe("");
  });

  it("the Collapse button also collapses", async () => {
    const { user } = renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    const frame = await within(body).findByTestId("server-frame");
    const browser = frame.closest(".vp-browser") as HTMLElement;
    await user.click(within(body).getByRole("button", { name: "Expand" }));
    expect(browser).toHaveClass("full");
    await user.click(within(body).getByRole("button", { name: "Collapse" }));
    expect(browser).not.toHaveClass("full");
  });

  it("Expand also works on the capture view; the toggle keeps it expanded", async () => {
    withCapture(8765);
    const { user } = renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    const frame = await within(body).findByTestId("server-frame");
    const browser = frame.closest(".vp-browser") as HTMLElement;
    await user.click(within(body).getByRole("button", { name: "Expand" }));
    await user.click(within(body).getByRole("button", { name: "Capture" }));
    expect(browser).toHaveClass("full");
    expect(within(body).getByRole("img", { name: /Last capture/ })).toBeInTheDocument();
  });

  it("expand resets when another server is opened, and Esc then closes the viewport at once", async () => {
    const { user, router } = renderApp(SRV);
    const body = await screen.findByTestId("server-body");
    await within(body).findByTestId("server-frame");
    await user.click(within(body).getByRole("button", { name: "Expand" }));
    expect(document.querySelector(".vp-browser.full")).not.toBeNull();
    // the process list sits under the fixed browser in the page; click through it
    await user.click(within(within(body).getByTestId("procs")).getByRole("button", { name: /51738/ }));
    await waitFor(() => expect(router.state.location.search).toContain("51738"));
    const next = await screen.findByTestId("server-body");
    await waitFor(() => expect(within(next).getByTestId("server-page")).toHaveTextContent("Not a web page"));
    expect(document.querySelector(".vp-browser.full")).toBeNull();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByTestId("viewport")).toBeNull());
  });

  it("readout mode (the Plash wallpaper) shows the capture and never runs the app in a frame", async () => {
    setState({ mode: "readout" });
    const done = new Promise<void>((resolve) => {
      server.events.on("response:mocked", ({ request }) => {
        if (request.url.includes("/api/servers/embed")) resolve();
      });
    });
    const ref = "notes-app:server:8765";
    const resolved: ResolveResponse = {
      ref,
      type: "server",
      projectId: "notes-app",
      title: "notes-app — handoff prototype",
      subtitle: "localhost:8765",
      icon: "●",
      date: null,
      obj: { port: 8765, running: true, lastCapture: { path: "design/screenshots/ideas2.png", captured: "2026-09-30T10:00:00Z" } },
      nativeLabel: "Open in Safari",
      nativeActions: [],
      notes: [],
      tasks: [],
    };
    renderWithRouter(<ServerBody refStr={ref} refObj={parseRef(ref)!} resolved={resolved} />);
    const body = await screen.findByTestId("server-body");
    await within(body).findByRole("img", { name: "Last capture of localhost:8765" });
    await done; // the frame answer is in, and still there is no frame
    await waitFor(() => expect(within(body).getByTestId("server-status")).toHaveTextContent("running"));
    expect(within(body).queryByTestId("server-frame")).toBeNull();
    expect(document.querySelector("iframe")).toBeNull();
    expect(within(body).queryByRole("group", { name: "Preview" })).toBeNull();
    expect(within(body).queryByRole("button", { name: "Expand" })).toBeNull();
    server.events.removeAllListeners();
  });
});

describe("other refs", () => {
  it("a feature has no preview, says so, and links to the tracker", async () => {
    server.use(
      http.get("*/api/resolve", ({ request }) => {
        const ref = new URL(request.url).searchParams.get("ref") ?? "";
        if (!ref.includes(":feature:")) return;
        return HttpResponse.json({ ref, type: "feature", projectId: "notes-app", title: "Card pages", subtitle: "feature · 3 tasks", icon: "◇", date: null, obj: {}, nativeLabel: "", nativeActions: [], notes: [], tasks: [] });
      }),
    );
    const { user, router } = renderApp("/?item=notes-app:feature:F-1");
    const np = await screen.findByTestId("no-preview");
    expect(np).toHaveTextContent("No preview for this kind of item.");
    expect(np).toHaveTextContent("feature · 3 tasks");
    await user.click(within(np).getByRole("button", { name: "Open in tracker →" }));
    expect(router.state.location.pathname).toBe("/tracker");
  });

  it("an unknown ref shows the shell's clear missing message", async () => {
    renderApp("/?item=notes-app:commit:doesnotexist");
    expect(await screen.findByTestId("viewport-missing")).toHaveTextContent("Nothing to show here.");
  });
});
