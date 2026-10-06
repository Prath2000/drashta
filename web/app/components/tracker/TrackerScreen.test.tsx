import { act, screen, waitFor, within } from "@testing-library/react";
import { http } from "msw";
import { beforeEach, describe, expect, it } from "vitest";
import { applyEvent } from "~/lib/events";
import { getState } from "~/lib/store";
import { mockDb } from "~/mocks/handlers";
import { server } from "~/mocks/node";
import { conflictHandler, trackerHandlers } from "~/mocks/tracker";
import { renderApp } from "../../../test/render";
import { resetActionState } from "./actions";
import { CELL_CAP } from "./Board";

beforeEach(() => {
  resetActionState();
  server.use(...trackerHandlers);
});

const STATUSES = ["backlog", "todo", "in_progress", "review", "done"] as const;

async function board(path = "/tracker") {
  const r = renderApp(path);
  await screen.findByTestId("tk-board");
  return r;
}

const cell = (s: string) => within(screen.getByTestId("cell-" + s));

describe("board", () => {
  it("shows five columns with the task counts", async () => {
    await board();
    const db = mockDb();
    for (const s of STATUSES) {
      expect(screen.getByTestId("count-" + s)).toHaveTextContent(String(db.tasks.filter((t) => t.status === s).length));
    }
    expect(screen.getByTestId("tk-summary")).toHaveTextContent("21 tasks · 11 open · 4 awaiting review · 4 milestones");
    expect(screen.getByTestId("card-acme-api-T-17")).toBeInTheDocument();
  });

  it("the same task id in two projects gives two cards", async () => {
    await board();
    expect(screen.getByTestId("card-notes-app-T-1")).toBeInTheDocument();
    expect(screen.getByTestId("card-widget-kit-T-1")).toBeInTheDocument();
  });

  it("lists only projects that have tracker data", async () => {
    await board();
    const group = within(screen.getByRole("group", { name: "Project filter" }));
    expect(group.getAllByRole("button").map((b) => b.textContent)).toEqual(["All", "notes-app", "acme-api", "widget-kit"]);
  });

  it("filters by project and keeps it in the URL", async () => {
    const { user, router } = await board();
    await user.click(within(screen.getByRole("group", { name: "Project filter" })).getByRole("button", { name: "acme-api" }));
    expect(router.state.location.search).toContain("project=acme-api");
    await waitFor(() => expect(screen.getByTestId("count-done")).toHaveTextContent("5"));
    expect(screen.queryByTestId("card-notes-app-T-1")).toBeNull();
    expect(screen.getByTestId("tk-summary")).toHaveTextContent("7 tasks");
  });

  it("opens on the project named in the URL, even without data", async () => {
    await board("/tracker?project=go-playground");
    const group = within(screen.getByRole("group", { name: "Project filter" }));
    expect(group.getByRole("button", { name: "go-playground" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByTestId("tk-summary")).toHaveTextContent("0 tasks");
    expect(screen.getAllByText("no tasks").length).toBe(5);
  });

  it("groups by feature into lanes", async () => {
    const { user } = await board("/tracker?project=notes-app");
    await user.click(within(screen.getByRole("group", { name: "Group by" })).getByRole("button", { name: "Feature" }));
    const lane = within(screen.getByTestId("lane-notes-app/F-1"));
    expect(lane.getByText("Card pages", { selector: ".tk-lanetitle" })).toBeInTheDocument();
    expect(lane.getByText("4 tasks")).toBeInTheDocument();
    expect(within(screen.getByTestId("lane-notes-app/F-4")).getByText("1 task")).toBeInTheDocument();
  });

  it("caps a long column and shows all on request", async () => {
    const db = mockDb();
    for (let i = 0; i < CELL_CAP + 10; i++) {
      db.tasks.push({ ...db.tasks[0], id: "T-" + (100 + i), title: "bulk " + i, status: "backlog", latestSnapshot: null, linkCount: 0, openNotes: 0 });
    }
    const { user } = await board("/tracker?project=notes-app");
    expect(cell("backlog").getAllByRole("button", { name: /^Open task/ })).toHaveLength(CELL_CAP);
    await user.click(cell("backlog").getByRole("button", { name: /^Show all/ }));
    expect(cell("backlog").getAllByRole("button", { name: /^Open task/ }).length).toBe(CELL_CAP + 10 + 2);
  });

  it("disables the arrows at both ends", async () => {
    await board();
    expect(screen.getByTestId("card-notes-app-T-9")).toBeInTheDocument();
    const first = within(screen.getByTestId("card-notes-app-T-9"));
    expect(first.getByRole("button", { name: "Already first status" })).toBeDisabled();
    const last = within(screen.getByTestId("card-notes-app-T-4"));
    expect(last.getByRole("button", { name: "Already done" })).toBeDisabled();
  });
});

describe("move a task", () => {
  it("moves the card at once, then confirms with a toast", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    // Hold the PATCH so we can look at the screen while it is in flight.
    server.use(http.patch("*/api/projects/:p/tasks/:id", async () => void (await gate)));
    const { user } = await board();
    expect(cell("in_progress").getByTestId("card-acme-api-T-17")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Move T-17 to Review" }));
    // optimistic: already in the Review column, no toast yet
    expect(cell("review").getByTestId("card-acme-api-T-17")).toBeInTheDocument();
    expect(cell("in_progress").queryByTestId("card-acme-api-T-17")).toBeNull();
    expect(getState().toast).toBeNull();
    release();
    await waitFor(() => expect(getState().toast?.msg).toBe("T-17 → review"));
    expect(mockDb().tasks.find((t) => t.projectId === "acme-api" && t.id === "T-17")?.status).toBe("review");
  });

  it("sends If-Match with the rev it has", async () => {
    let seen: string | null = null;
    server.use(
      http.patch("*/api/projects/:p/tasks/:id", ({ request }) => {
        seen = request.headers.get("If-Match");
      }),
    );
    const { user } = await board();
    await user.click(screen.getByRole("button", { name: "Move T-17 to Review" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("T-17 → review"));
    expect(seen).toBe("r1");
  });

  it("two quick moves of one task both succeed (no stale rev)", async () => {
    const { user } = await board();
    await user.click(screen.getByRole("button", { name: "Move T-17 to Review" }));
    await user.click(await within(screen.getByTestId("card-acme-api-T-17")).findByRole("button", { name: "Move T-17 to Done" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("T-17 → done"));
    expect(mockDb().tasks.find((t) => t.projectId === "acme-api" && t.id === "T-17")?.status).toBe("done");
  });

  it("on a 409 it drops the change, reloads and says so", async () => {
    server.use(conflictHandler());
    const { user } = await board();
    await user.click(screen.getByRole("button", { name: "Move T-17 to Review" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("T-17 changed on disk, reloaded"));
    // the card is back where the disk says it is
    await waitFor(() => expect(cell("in_progress").getByTestId("card-acme-api-T-17")).toBeInTheDocument());
    expect(cell("review").queryByTestId("card-acme-api-T-17")).toBeNull();
    expect(getState().toast?.error).toBe(false);
  });

  it("shows an error toast when the server fails", async () => {
    server.use(http.patch("*/api/projects/:p/tasks/:id", () => new Response(JSON.stringify({ error: { code: "boom", message: "disk full" } }), { status: 500, headers: { "Content-Type": "application/json" } })));
    const { user } = await board();
    await user.click(screen.getByRole("button", { name: "Move T-17 to Review" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("disk full"));
    expect(getState().toast?.error).toBe(true);
    await waitFor(() => expect(cell("in_progress").getByTestId("card-acme-api-T-17")).toBeInTheDocument());
  });
});

describe("create a task", () => {
  it("asks for a title, then adds the task to the column", async () => {
    const { user } = await board();
    await user.click(screen.getByRole("button", { name: "New task in To do" }));
    const form = within(screen.getByRole("form", { name: "New task" }));
    await user.click(form.getByRole("button", { name: "Add task" }));
    expect(form.getByRole("alert")).toHaveTextContent("Give the task a title.");

    await user.type(form.getByLabelText("Title"), "Paint the gate");
    await user.selectOptions(form.getByLabelText("Feature"), "Audio");
    await user.click(form.getByRole("button", { name: "Add task" }));

    await waitFor(() => expect(getState().toast?.msg).toBe("Task T-11 created"));
    await waitFor(() => expect(cell("todo").getByText("Paint the gate")).toBeInTheDocument());
    expect(screen.queryByRole("form", { name: "New task" })).toBeNull();
    const t = mockDb().tasks.find((x) => x.title === "Paint the gate");
    expect(t).toMatchObject({ projectId: "notes-app", featureId: "F-3", status: "todo" });
  });

  it("uses the status of the column it was opened in", async () => {
    const { user } = await board();
    await user.click(screen.getByRole("button", { name: "New task in Backlog" }));
    const form = within(screen.getByRole("form", { name: "New task" }));
    await user.type(form.getByLabelText("Title"), "Later");
    await user.click(form.getByRole("button", { name: "Add task" }));
    await waitFor(() => expect(cell("backlog").getByText("Later")).toBeInTheDocument());
  });

  it("allows no feature and says when a project has none", async () => {
    const { user } = await board();
    await user.click(screen.getByRole("button", { name: "New task in To do" }));
    const form = within(screen.getByRole("form", { name: "New task" }));
    await user.selectOptions(form.getByLabelText("Project"), "go-playground");
    expect(form.getByText("This project has no features yet.")).toBeInTheDocument();
    await user.type(form.getByLabelText("Title"), "Loose end");
    await user.click(form.getByRole("button", { name: "Add task" }));
    await waitFor(() => expect(mockDb().tasks.some((t) => t.title === "Loose end" && t.projectId === "go-playground" && t.featureId === null)).toBe(true));
  });

  it("cancel closes the form, and so does Escape", async () => {
    const { user } = await board();
    await user.click(screen.getByRole("button", { name: "New task in To do" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("form", { name: "New task" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "New task in To do" }));
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("form", { name: "New task" })).toBeNull();
  });
});

describe("suggestion chip on the board", () => {
  it("offers Done for an approved latest snapshot and moves only on click (A4)", async () => {
    const db = mockDb();
    const t5 = db.tasks.find((t) => t.projectId === "notes-app" && t.id === "T-5")!;
    t5.status = "review"; // approved v1, but not done
    const { user } = await board();
    const card = within(screen.getByTestId("card-notes-app-T-5"));
    expect(card.getByText("Approved. Move to Done?")).toBeInTheDocument();
    expect(t5.status).toBe("review"); // nothing moved by itself
    await user.click(card.getByRole("button", { name: "Move to Done" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("T-5 → done"));
    expect(t5.status).toBe("done");
    await waitFor(() => expect(within(screen.getByTestId("card-notes-app-T-5")).queryByText("Approved. Move to Done?")).toBeNull());
  });

  it("shows no chip for a pending snapshot", async () => {
    await board();
    expect(within(screen.getByTestId("card-notes-app-T-1")).queryByText("Approved. Move to Done?")).toBeNull();
  });
});

describe("snapshot strip on a card", () => {
  it("shows the latest version, its verdict and one tick per version", async () => {
    await board();
    const card = within(screen.getByTestId("card-notes-app-T-3"));
    const strip = await card.findByRole("button", { name: /^Open latest snapshot .* v4 \(Pending, 3 versions\)/ });
    expect(strip).toHaveTextContent("v4");
    expect(strip).toHaveTextContent("Pending");
  });

  it("opens the snapshot in the viewport", async () => {
    const { user, router } = await board();
    const card = within(screen.getByTestId("card-notes-app-T-3"));
    await user.click(await card.findByRole("button", { name: /^Open latest snapshot/ }));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:snapshot:T-3/v4");
  });

  it("a card click opens the task, and the card gets the selected border", async () => {
    const { user, router } = await board();
    await user.click(screen.getByRole("button", { name: "Open task T-9: Mute control" }));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:task:T-9");
    await waitFor(() => expect(screen.getByTestId("card-notes-app-T-9")).toHaveClass("sel"));
  });
});

describe("milestones view", () => {
  it("shows progress, days left and the features of a milestone", async () => {
    renderApp("/tracker?view=milestones&project=acme-api");
    const block = within(await screen.findByRole("region", { name: "Milestone Always-on service" }));
    expect(block.getByText("5 / 7 done · 71%")).toBeInTheDocument();
    expect(block.getByText("2 days left")).toBeInTheDocument();
    expect(block.getByText("Discovery")).toBeInTheDocument();
    expect(block.getByText("Service")).toBeInTheDocument();
    expect(block.getByText("Copy discovery into the next project")).toBeInTheDocument();
  });

  it("opens a task from a row", async () => {
    const { user, router } = renderApp("/tracker?view=milestones&project=acme-api");
    await user.click(await screen.findByRole("button", { name: /Copy discovery into the next project/ }));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("acme-api:task:T-17");
  });

  it("says so when a project has no milestones", async () => {
    renderApp("/tracker?view=milestones&project=go-playground");
    expect(await screen.findByText("No milestones for this project yet.")).toBeInTheDocument();
  });

  it("collects features outside milestones", async () => {
    mockDb().features.push({ id: "F-9", projectId: "acme-api", title: "Loose feature", milestoneId: null, rev: "r1" });
    renderApp("/tracker?view=milestones&project=acme-api");
    const block = within(await screen.findByRole("region", { name: "Outside milestones" }));
    expect(block.getByText("Loose feature")).toBeInTheDocument();
  });
});

describe("timeline view", () => {
  it("draws a bar per task with its status", async () => {
    renderApp("/tracker?view=timeline&project=acme-api");
    await screen.findByTestId("tk-timeline");
    expect(screen.getByRole("button", { name: "T-17 · Copy discovery into the next project · In progress" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "T-16 · Commit the fossil side (.fslckout untracked in git) · To do" })).toBeInTheDocument();
    expect(screen.getByText("Always-on service")).toBeInTheDocument(); // milestone diamond
    expect(screen.getByText("NOW")).toBeInTheDocument();
  });

  it("says so when there are no features", async () => {
    renderApp("/tracker?view=timeline&project=go-playground");
    expect(await screen.findByText("No features for this project yet.")).toBeInTheDocument();
  });
});

describe("states", () => {
  it("shows a retry when the tracker cannot load", async () => {
    server.use(http.get("*/api/tracker", () => new Response(JSON.stringify({ error: { code: "boom", message: "nope" } }), { status: 500, headers: { "Content-Type": "application/json" } })));
    renderApp("/tracker");
    expect(await screen.findByRole("alert")).toHaveTextContent("could not load the tracker.");
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("the view buttons set the view in the URL", async () => {
    const { user, router } = await board();
    await user.click(within(screen.getByRole("group", { name: "View" })).getByRole("button", { name: "Timeline" }));
    expect(router.state.location.search).toContain("view=timeline");
    await user.click(within(screen.getByRole("group", { name: "View" })).getByRole("button", { name: "Board" }));
    expect(router.state.location.search).not.toContain("view=");
  });

  it("fills its mount box and has a scroll region", async () => {
    await board();
    const root = screen.getByTestId("tracker");
    expect(root.className).toContain("tk-root");
    expect(root.parentElement?.className).toContain("dr-mount");
    expect(screen.getByTestId("tk-board").className).toContain("tk-scroll");
  });
});

describe("live updates", () => {
  it("a tracker event refreshes the touched card", async () => {
    await board();
    expect(cell("in_progress").getByTestId("card-acme-api-T-17")).toBeInTheDocument();
    // another window (or Claude) changed the task on disk
    mockDb().tasks.find((t) => t.projectId === "acme-api" && t.id === "T-17")!.status = "review";
    act(() => applyEvent({ type: "tracker", data: { projectId: "acme-api", kind: "task", id: "T-17" } }));
    await waitFor(() => expect(cell("review").getByTestId("card-acme-api-T-17")).toBeInTheDocument());
    expect(getState().toast).toBeNull(); // a background event never makes a toast
  });
});

describe("new milestone and feature", () => {
  it("creates a milestone", async () => {
    const { user } = renderApp("/tracker?view=milestones&project=acme-api");
    await user.click(await screen.findByRole("button", { name: "+ New milestone" }));
    const form = within(screen.getByRole("form", { name: "New milestone" }));
    await user.click(form.getByRole("button", { name: "Add milestone" }));
    expect(form.getByRole("alert")).toHaveTextContent("Give the milestone a title.");
    await user.type(form.getByLabelText("Title"), "Fresh target");
    await user.type(form.getByLabelText("Due"), "2026-11-01");
    await user.click(form.getByRole("button", { name: "Add milestone" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("Milestone M-4 created"));
    expect(mockDb().milestones.find((m) => m.title === "Fresh target")).toMatchObject({ projectId: "acme-api", due: "2026-11-01" });
    expect(await screen.findByRole("region", { name: "Milestone Fresh target" })).toBeInTheDocument();
  });

  it("creates a feature inside a milestone", async () => {
    const { user } = renderApp("/tracker?view=milestones&project=acme-api");
    await user.click(await screen.findByRole("button", { name: "+ New feature" }));
    const form = within(screen.getByRole("form", { name: "New feature" }));
    await user.type(form.getByLabelText("Title"), "Polish");
    await user.selectOptions(form.getByLabelText("Milestone"), "Always-on service");
    await user.click(form.getByRole("button", { name: "Add feature" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("Feature F-8 created"));
    const block = within(await screen.findByRole("region", { name: "Milestone Always-on service" }));
    expect(await block.findByText("Polish")).toBeInTheDocument();
  });
});
