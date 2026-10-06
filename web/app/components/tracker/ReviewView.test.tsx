import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { http } from "msw";
import { beforeEach, describe, expect, it } from "vitest";
import { getState } from "~/lib/store";
import { mockDb } from "~/mocks/handlers";
import { server } from "~/mocks/node";
import { trackerHandlers } from "~/mocks/tracker";
import { renderApp } from "../../../test/render";
import { resetActionState } from "./actions";

beforeEach(() => {
  resetActionState();
  server.use(...trackerHandlers);
});

async function review(path = "/tracker?filter=review") {
  const r = renderApp(path);
  await screen.findByTestId("tk-review");
  return r;
}

/** The review row of a task, once its versions have loaded. */
async function row(project: string, id: string) {
  const el = await screen.findByTestId(`review-${project}-${id}`);
  await within(el).findAllByRole("button", { name: /^Show v\d/ });
  return within(el);
}

const task = (p: string, id: string) => mockDb().tasks.find((t) => t.projectId === p && t.id === id)!;

describe("awaiting review", () => {
  it("lists the tasks whose latest snapshot is pending", async () => {
    await review();
    await screen.findByTestId("review-notes-app-T-1");
    for (const id of ["T-1", "T-2", "T-3", "T-7"]) expect(screen.getByTestId("review-notes-app-" + id)).toBeInTheDocument();
    expect(screen.queryByTestId("review-notes-app-T-5")).toBeNull(); // approved
    expect(screen.getByText("4 tasks whose latest snapshot is waiting for a verdict")).toBeInTheDocument();
  });

  it("has no separate view controls, only the filter and the project", async () => {
    await review();
    expect(screen.queryByRole("group", { name: "View" })).toBeNull();
    expect(screen.getByRole("group", { name: "Task filter" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Awaiting review (4)" })).toHaveAttribute("aria-pressed", "true");
  });

  it("the filter buttons switch between the board and the review", async () => {
    const { user, router } = await review();
    await user.click(screen.getByRole("button", { name: "All tasks" }));
    expect(router.state.location.search).not.toContain("filter");
    await screen.findByTestId("tk-board");
    await user.click(screen.getByRole("button", { name: "Awaiting review (4)" }));
    expect(router.state.location.search).toContain("filter=review");
  });

  it("shows the versions, newest first, with the latest selected", async () => {
    const { user } = await review();
    const r = await row("notes-app", "T-3");
    const rows = r.getAllByRole("button", { name: /^Show v\d/ });
    expect(rows.map((b) => b.getAttribute("aria-label"))).toEqual(["Show v4, Pending", "Show v3, Approved", "Show v2, Changes requested"]);
    expect(rows[0]).toHaveAttribute("aria-pressed", "true");
    expect(r.getByText("3 versions")).toBeInTheDocument();
    await user.click(rows[2]);
    expect(r.getByText(/Drop the frame\. The map should take the whole screen\./)).toBeInTheDocument();
    expect(r.getByText("viewing an earlier version")).toBeInTheDocument();
  });

  it("numbers the pins and lists their notes", async () => {
    await review();
    const r = await row("notes-app", "T-1");
    const list = within(r.getByRole("list", { name: "Notes pinned on v3" }));
    expect(list.getAllByRole("listitem")).toHaveLength(2);
    expect(list.getByText("Constellation lines slightly too bright against the dome.")).toBeInTheDocument();
    expect(r.getByRole("button", { name: "Pin 1: Constellation lines slightly too bright against the dome." })).toBeInTheDocument();
    expect(r.getByText("2 open / 2 notes pinned on v3")).toBeInTheDocument();
  });

  it("highlights the note when its pin is focused", async () => {
    const { user } = await review();
    const r = await row("notes-app", "T-1");
    await user.hover(r.getByRole("button", { name: /^Pin 2:/ }));
    const items = r.getAllByRole("listitem");
    expect(items[1]).toHaveClass("hot");
    expect(items[0]).not.toHaveClass("hot");
  });

  it("opens the task and the snapshot in the viewport", async () => {
    const { user, router } = await review();
    const r = await row("notes-app", "T-1");
    await user.click(r.getByRole("button", { name: "Task detail →" }));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:task:T-1");
    await user.click(r.getByRole("button", { name: "Open to annotate ↗" }));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:snapshot:T-1/v3");
  });

  it("compares two versions side by side", async () => {
    const { user } = await review();
    const r = await row("notes-app", "T-3");
    await user.selectOptions(r.getByLabelText("Compare with"), "v3 · Approved");
    const cmp = within(screen.getByTestId("compare"));
    expect(cmp.getByText("v4 · Pending")).toBeInTheDocument();
    expect(cmp.getByText("v3 · Approved")).toBeInTheDocument();
    expect(r.getByRole("button", { name: "Annotate" })).toBeDisabled();
  });

  it("says so when nothing is waiting, and Show all tasks goes back", async () => {
    for (const s of mockDb().snapshots) s.verdict = "approved";
    for (const t of mockDb().tasks) if (t.latestSnapshot) t.latestSnapshot.verdict = "approved";
    const { user, router } = await review();
    expect(screen.getByText("Nothing awaiting review.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Show all tasks" }));
    expect(router.state.location.search).not.toContain("filter");
    await screen.findByTestId("tk-board");
  });

  it("names the project in the empty state", async () => {
    await review("/tracker?filter=review&project=acme-api");
    expect(screen.getByText("no pending snapshots in acme-api")).toBeInTheDocument();
    expect(screen.getByText(/No task in acme-api has a pending latest snapshot/)).toBeInTheDocument();
  });
});

describe("verdict flow", () => {
  it("approve only suggests Done: the task stays, and one click moves it", async () => {
    const { user } = await review();
    const r = await row("notes-app", "T-1");
    await user.click(r.getByRole("button", { name: "✓ Approve" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("Ideas page v3 · approved"));
    // A4: nothing moved by itself
    expect(task("notes-app", "T-1").status).toBe("review");
    expect(mockDb().snapshots.find((s) => s.id === "T-1/v3" && s.projectId === "notes-app")?.verdict).toBe("approved");

    // the row stays so the suggestion has a place
    const settled = within(await screen.findByTestId("settled-T-1"));
    expect(settled.getByText("v3 approved. Move T-1 to Done?")).toBeInTheDocument();
    await user.click(settled.getByRole("button", { name: "Move to Done" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("T-1 → done"));
    expect(task("notes-app", "T-1").status).toBe("done");
    await waitFor(() => expect(screen.queryByTestId("review-notes-app-T-1")).toBeNull());
  });

  it("Dismiss removes the row without moving the task", async () => {
    const { user } = await review();
    const r = await row("notes-app", "T-2");
    await user.click(r.getByRole("button", { name: "✓ Approve" }));
    const settled = within(await screen.findByTestId("settled-T-2"));
    await user.click(settled.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByTestId("review-notes-app-T-2")).toBeNull();
    expect(task("notes-app", "T-2").status).toBe("review");
  });

  it("request changes needs a note", async () => {
    const { user } = await review();
    const r = await row("notes-app", "T-2");
    await user.click(r.getByRole("button", { name: "± Changes" }));
    const form = within(r.getByRole("form", { name: "Request changes" }));
    await user.click(form.getByRole("button", { name: "Request changes" }));
    expect(form.getByRole("alert")).toHaveTextContent("What needs to change?");
    expect(mockDb().snapshots.find((s) => s.id === "T-2/v3" && s.projectId === "notes-app")?.verdict).toBe("pending");

    await user.type(form.getByPlaceholderText("What needs to change?"), "Book is too bright");
    await user.click(form.getByRole("button", { name: "Request changes" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("Diary page v3 · changes requested"));
    const snap = mockDb().snapshots.find((s) => s.id === "T-2/v3" && s.projectId === "notes-app")!;
    expect(snap).toMatchObject({ verdict: "changes", reviewNote: "Book is too bright" });
    expect(task("notes-app", "T-2").status).toBe("in_progress");
    const settled = await screen.findByTestId("settled-T-2");
    expect(settled).toHaveTextContent("Changes requested on v3. T-2 is now in In progress.");
  });

  it("reset to pending", async () => {
    const { user } = await review();
    const r = await row("notes-app", "T-3");
    // v3 is approved: select it, then reset it
    await user.click(r.getByRole("button", { name: "Show v3, Approved" }));
    await user.click(r.getByRole("button", { name: "◯ Pending" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("Travel page v3 · reset to pending"));
    expect(mockDb().snapshots.find((s) => s.id === "T-3/v3" && s.projectId === "notes-app")?.verdict).toBe("pending");
    // an earlier version: the task stays on the list as it was
    expect(screen.getByTestId("review-notes-app-T-3")).toBeInTheDocument();
  });

  it("shows an error toast when the verdict fails", async () => {
    server.use(
      http.put("*/api/projects/:p/tasks/:id/snapshots/:v/verdict", () => new Response(JSON.stringify({ error: { code: "boom", message: "cannot write" } }), { status: 500, headers: { "Content-Type": "application/json" } })),
    );
    const { user } = await review();
    const r = await row("notes-app", "T-1");
    await user.click(r.getByRole("button", { name: "✓ Approve" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("cannot write"));
    expect(getState().toast?.error).toBe(true);
    expect(screen.queryByTestId("settled-T-1")).toBeNull();
  });
});

describe("annotate", () => {
  const rect = { left: 0, top: 0, width: 200, height: 100, right: 200, bottom: 100, x: 0, y: 0, toJSON: () => ({}) };

  it("drops a pin where you click and saves it as a note with x and y", async () => {
    const { user } = await review();
    const r = await row("notes-app", "T-7");
    // without annotate mode a click opens the snapshot instead
    await user.click(r.getByRole("button", { name: "Annotate" }));
    expect(r.getByText("Annotate mode — click anywhere on the frame to drop a pin.")).toBeInTheDocument();
    const hit = r.getByRole("button", { name: "Drop a pin on Exit v1" });
    hit.getBoundingClientRect = () => rect as DOMRect;
    fireEvent.click(hit, { clientX: 50, clientY: 25 });
    const form = within(r.getByRole("form", { name: "New pin note" }));
    expect(form.getByRole("button", { name: "Pin note" })).toBeDisabled();
    await user.type(form.getByPlaceholderText("What should change here?"), "Fade is too slow");
    await user.click(form.getByRole("button", { name: "Pin note" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("Note pinned"));
    const note = mockDb().notes.find((n) => n.text === "Fade is too slow")!;
    expect(note).toMatchObject({ ref: "notes-app:snapshot:T-7/v1", x: 0.25, y: 0.25 });
    // the new pin and its note show up
    expect(await r.findByRole("button", { name: "Pin 1: Fade is too slow" })).toBeInTheDocument();
  });

  it("resolves and reopens a pinned note", async () => {
    const { user } = await review();
    const r = await row("notes-app", "T-1");
    const item = within(r.getAllByRole("listitem")[0]);
    await user.click(item.getByRole("button", { name: "Resolve" }));
    await waitFor(() => expect(mockDb().notes.find((n) => n.id === "N-1")?.resolved).toBe(true));
    await waitFor(() => expect(within(r.getAllByRole("listitem")[0]).getByRole("button", { name: "Reopen" })).toBeInTheDocument());
  });
});
