import { screen, waitFor, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { mockDb } from "~/mocks/handlers";
import { server } from "~/mocks/node";
import { fileForbidden, fileOverride, longFile } from "~/mocks/viewport";
import { renderApp } from "../../../test/render";

const CODE = "/?item=acme-api:file:discover.go";

describe("file body", () => {
  it("shows code with line numbers, meta and the existing line note", async () => {
    renderApp(CODE);
    const code = await screen.findByTestId("code");
    expect(within(code).getByRole("button", { name: "Line 1 — add a note" })).toHaveTextContent("1");
    expect(code).toHaveTextContent("package main");
    // N-4 sits on line 30
    const l30 = within(code).getByRole("button", { name: "Line 30, 1 note — add a note" });
    expect(l30).toHaveTextContent("● 30");
    expect(within(code).getByText(/devNames list: add 'vite'/)).toBeInTheDocument();
    const meta = screen.getByTestId("file-meta");
    expect(meta).toHaveTextContent("Language" + "Go");
    expect(meta).toHaveTextContent("Stateclean");
    expect(meta).toHaveTextContent("Notes1 open / 1");
  });

  it("pins a note to a clicked line through POST /notes", async () => {
    let posted: unknown = null;
    server.events.on("request:start", async ({ request }) => {
      if (request.method === "POST" && request.url.endsWith("/api/notes")) posted = await request.clone().json();
    });
    const { user } = renderApp(CODE);
    const code = await screen.findByTestId("code");
    await user.click(within(code).getByRole("button", { name: "Line 12 — add a note" }));
    const box = screen.getByRole("textbox", { name: "Note on line 12" });
    const add = within(screen.getByTestId("line-composer")).getByRole("button", { name: "Add note" });
    expect(add).toBeDisabled();
    await user.type(box, "rename this import");
    await user.click(add);
    await waitFor(() => expect(posted).toEqual({ ref: "acme-api:file:discover.go", line: 12, text: "rename this import" }));
    await waitFor(() => expect(within(code).getByRole("button", { name: "Line 12, 1 note — add a note" })).toBeInTheDocument());
    expect(screen.queryByTestId("line-composer")).toBeNull();
    expect(screen.getByTestId("toast")).toHaveTextContent("Note pinned to line 12");
    expect(mockDb().notes.some((n) => n.line === 12 && n.text === "rename this import")).toBe(true);
    server.events.removeAllListeners();
  });

  it("cancel closes the line composer; a second click on the number does too", async () => {
    const { user } = renderApp(CODE);
    const code = await screen.findByTestId("code");
    const l5 = within(code).getByRole("button", { name: "Line 5 — add a note" });
    await user.click(l5);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByTestId("line-composer")).toBeNull();
    await user.click(l5);
    expect(screen.getByTestId("line-composer")).toBeInTheDocument();
    await user.click(l5);
    expect(screen.queryByTestId("line-composer")).toBeNull();
  });

  it("says 'line moved?' when the note's anchor text is no longer on its line", async () => {
    const n = mockDb().notes.find((x) => x.id === "N-4")!;
    n.anchor = "package main";
    renderApp(CODE);
    const hint = await screen.findByTestId("line-moved");
    expect(hint).toHaveTextContent("line moved? now line 1");
  });

  it("resolves a note from its card", async () => {
    const { user } = renderApp(CODE);
    const code = await screen.findByTestId("code");
    const card = within(code).getAllByTestId("note-card")[0];
    await user.click(within(card).getByRole("button", { name: "Resolve" }));
    await waitFor(() => expect(mockDb().notes.find((x) => x.id === "N-4")!.resolved).toBe(true));
    await waitFor(() => expect(within(screen.getByTestId("code")).getByRole("button", { name: "Reopen" })).toBeInTheDocument());
  });

  it("adds a whole-file note", async () => {
    const { user } = renderApp(CODE);
    await screen.findByTestId("code");
    await user.type(screen.getByRole("textbox", { name: "Note on the whole file" }), "split this file");
    await user.click(within(screen.getByTestId("file-notes")).getByRole("button", { name: "Add" }));
    await waitFor(() => expect(within(screen.getByTestId("file-notes")).getByText("split this file")).toBeInTheDocument());
    expect(mockDb().notes.find((x) => x.text === "split this file")?.line).toBeUndefined();
  });

  it("renders a markdown doc and switches to source", async () => {
    const { user } = renderApp("/?item=notes-app:file:design/HANDOFF.md");
    const r = await screen.findByTestId("rendered");
    expect(within(r).getByRole("heading", { level: 1 })).toHaveTextContent(/Handoff/);
    expect(screen.getByRole("button", { name: "Rendered" })).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByRole("button", { name: "Source" }));
    expect(await screen.findByTestId("code")).toHaveTextContent("# Handoff");
    expect(screen.queryByTestId("rendered")).toBeNull();
  });

  it("windows a long file and loads the next 400 lines", async () => {
    server.use(longFile(2758));
    const { user } = renderApp("/?item=widget-kit:file:src/app/api/worktrees.rs");
    const code = await screen.findByTestId("code", {}, { timeout: 4000 });
    expect(within(code).getAllByRole("button", { name: /^Line \d+/ })).toHaveLength(400);
    const foot = screen.getByTestId("file-truncated");
    expect(foot).toHaveTextContent("first 400 of 2758 lines");
    await user.click(within(foot).getByRole("button", { name: "Show next 400 lines" }));
    await waitFor(() => expect(within(screen.getByTestId("code")).getAllByRole("button", { name: /^Line \d+/ })).toHaveLength(800), { timeout: 4000 });
    expect(within(screen.getByTestId("file-truncated")).getByRole("button", { name: "Open ↗ for the rest" })).toBeInTheDocument();
  });

  it("shows the uncommitted diff of a dirty file", async () => {
    renderApp("/?item=widget-kit:file:src/app/api/worktrees.rs");
    const d = await screen.findByTestId("dirty-diff", {}, { timeout: 4000 });
    await waitFor(() => expect(within(d).getByTestId("diff-file")).toHaveTextContent("src/app/api/worktrees.rs"));
    expect(screen.getByTestId("file-meta")).toHaveTextContent("Statemodified M");
  });

  it("binary file: no text, a clear state and Open ↗", async () => {
    server.use(fileOverride({ binary: true, size: 9000 }));
    renderApp(CODE);
    const b = await screen.findByTestId("file-binary");
    expect(b).toHaveTextContent("Binary file — no text preview");
    expect(within(b).getByRole("button", { name: /Open in editor ↗/ })).toBeInTheDocument();
    expect(screen.queryByTestId("code")).toBeNull();
  });

  it("redacted file (flag or 403): the body is never shown", async () => {
    server.use(fileOverride({ redacted: true }));
    const first = renderApp(CODE);
    expect(await screen.findByTestId("file-redacted")).toHaveTextContent("Secret file");
    first.unmount();
    server.use(fileForbidden);
    renderApp("/?item=acme-api:file:main.go");
    expect(await screen.findByTestId("file-redacted")).toBeInTheDocument();
  });

  it("too big: a banner says only the first part is shown", async () => {
    server.use(fileOverride({ size: 3 * 1024 * 1024, text: "a\nb", lines: 2, to: 2, truncated: true }));
    renderApp(CODE);
    expect(await screen.findByTestId("file-toobig")).toHaveTextContent("Large file (3.0 MB)");
    expect(screen.getByTestId("file-truncated")).toHaveTextContent("first 2 lines");
  });

  it("missing on disk: a clear message", async () => {
    server.use(http.get("*/api/projects/:p/file", () => HttpResponse.json({ error: { code: "not_found", message: "file not found" } }, { status: 404 })));
    renderApp(CODE);
    expect(await screen.findByTestId("file-missing")).toHaveTextContent("This file cannot be read now.");
  });

  it("Open ↗ for the rest runs POST /open and toasts the command", async () => {
    server.use(longFile(900));
    const { user } = renderApp("/?item=widget-kit:file:src/app/api/worktrees.rs");
    const foot = await screen.findByTestId("file-truncated", {}, { timeout: 4000 });
    await user.click(within(foot).getByRole("button", { name: "Open ↗ for the rest" }));
    await waitFor(() => expect(screen.getByTestId("toast")).toHaveTextContent("ran: code -g"));
  });
});
