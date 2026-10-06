import { screen, waitFor, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ViewportBodyProps } from "~/components/viewport/registry";
import { api } from "~/lib/api";
import { getState } from "~/lib/store";
import type { ResolveResponse } from "~/lib/types";
import { mockDb } from "~/mocks/handlers";
import { server } from "~/mocks/node";
import { conflictHandler, trackerHandlers } from "~/mocks/tracker";
import { renderWithRouter } from "../../../test/render";
import { resetActionState } from "./actions";
import { TaskBody } from "./TaskBody";

beforeEach(() => {
  resetActionState();
  server.use(...trackerHandlers);
});

function props(project: string, id: string): ViewportBodyProps {
  const refStr = project + ":task:" + id;
  return { refStr, refObj: { type: "task", projectId: project, id }, resolved: {} as ResolveResponse };
}

async function open(project = "notes-app", id = "T-1") {
  const r = renderWithRouter(<TaskBody {...props(project, id)} />, "/tracker");
  await screen.findByTestId("task-body");
  await screen.findByLabelText("Title");
  return r;
}

const task = (p: string, id: string) => mockDb().tasks.find((t) => t.projectId === p && t.id === id)!;

describe("task body: fields", () => {
  it("shows the fields of the task", async () => {
    await open();
    expect(screen.getByLabelText("Title")).toHaveValue("Ideas page: constellation over a compass rose");
    expect(screen.getByLabelText("Status")).toHaveValue("review");
    await waitFor(() => expect(screen.getByLabelText("Feature")).toHaveValue("F-1"));
    expect(screen.getByLabelText("Assignee")).toHaveValue("dev + Claude");
    // milestone comes from the feature
    await waitFor(() => expect(screen.getByTestId("task-milestone")).toHaveTextContent("Pages v3"));
    expect(screen.getByTestId("task-created")).toHaveTextContent("Created Tue 29 Sep 2026");
  });

  it("saves the title on blur, with If-Match, and confirms", async () => {
    let seen: { ifMatch: string | null; body: unknown } | null = null;
    server.use(
      http.patch("*/api/projects/:p/tasks/:id", async ({ request }) => {
        seen = { ifMatch: request.headers.get("If-Match"), body: await request.clone().json() };
      }),
    );
    const { user } = await open();
    const title = screen.getByLabelText("Title");
    await user.clear(title);
    await user.type(title, "Ideas page v2");
    await user.tab();
    // optimistic: the new title is already there
    expect(title).toHaveValue("Ideas page v2");
    await waitFor(() => expect(getState().toast?.msg).toBe("T-1 saved"));
    expect(seen).toEqual({ ifMatch: "r1", body: { title: "Ideas page v2" } });
    expect(task("notes-app", "T-1").title).toBe("Ideas page v2");
  });

  it("does not save an empty title or an unchanged field", async () => {
    let calls = 0;
    server.use(
      http.patch("*/api/projects/:p/tasks/:id", () => {
        calls++;
      }),
    );
    const { user } = await open();
    const title = screen.getByLabelText("Title");
    await user.click(title);
    await user.tab(); // unchanged
    await user.clear(title);
    await user.tab(); // empty
    expect(title).toHaveValue("Ideas page: constellation over a compass rose");
    expect(calls).toBe(0);
  });

  it("saves the status on change", async () => {
    const { user } = await open();
    await user.selectOptions(screen.getByLabelText("Status"), "In progress");
    await waitFor(() => expect(getState().toast?.msg).toBe("T-1 → in progress"));
    expect(task("notes-app", "T-1").status).toBe("in_progress");
  });

  it("saves the feature and can clear it", async () => {
    const { user } = await open();
    await waitFor(() => expect(screen.getByLabelText("Feature")).toHaveValue("F-1"));
    await user.selectOptions(screen.getByLabelText("Feature"), "No feature");
    await waitFor(() => expect(task("notes-app", "T-1").featureId).toBeNull());
  });

  it("on a 409 it reloads the task and says so", async () => {
    server.use(conflictHandler());
    const { user } = await open();
    const title = screen.getByLabelText("Title");
    await user.clear(title);
    await user.type(title, "My edit");
    await user.tab();
    await waitFor(() => expect(getState().toast?.msg).toBe("T-1 changed on disk, reloaded"));
    // the disk version is back
    await waitFor(() => expect(screen.getByLabelText("Title")).toHaveValue("Ideas page: constellation over a compass rose"));
    expect(task("notes-app", "T-1").title).toBe("Ideas page: constellation over a compass rose");
  });

  it("uses the new rev for a second edit (no pointless 409)", async () => {
    const { user } = await open();
    await user.selectOptions(screen.getByLabelText("Status"), "In progress");
    await waitFor(() => expect(getState().toast?.msg).toBe("T-1 → in progress"));
    await user.selectOptions(screen.getByLabelText("Status"), "Done");
    await waitFor(() => expect(getState().toast?.msg).toBe("T-1 → done"));
    expect(task("notes-app", "T-1").status).toBe("done");
  });

  it("offers Done for an approved latest snapshot", async () => {
    const db = mockDb();
    task("notes-app", "T-5").status = "review";
    expect(db.tasks.length).toBeGreaterThan(0);
    const { user } = await open("notes-app", "T-5");
    const chip = within(screen.getByRole("group", { name: "Suggestion" }));
    expect(chip.getByText("v1 approved. Move T-5 to Done?")).toBeInTheDocument();
    expect(task("notes-app", "T-5").status).toBe("review");
    await user.click(chip.getByRole("button", { name: "Move to Done" }));
    await waitFor(() => expect(task("notes-app", "T-5").status).toBe("done"));
  });

  it("says when the task is not there", async () => {
    renderWithRouter(<TaskBody {...props("notes-app", "T-99")} />, "/tracker");
    expect(await screen.findByText("This task isn't in the latest scan.")).toBeInTheDocument();
  });
});

describe("task body: links", () => {
  it("lists links with a manual badge", async () => {
    await open();
    const list = within(await screen.findByRole("list", { name: "Links" }));
    expect(list.getAllByRole("listitem")).toHaveLength(4);
    expect(list.getAllByText("manual")).toHaveLength(4);
  });

  it("adds a manual link", async () => {
    const { user } = await open();
    await user.type(screen.getByLabelText("Link a ref"), "commit:abc1234");
    await user.click(screen.getByRole("button", { name: "Add link" }));
    await waitFor(() => expect(task("notes-app", "T-1").links).toContain("notes-app:commit:abc1234"));
    expect(await screen.findByText("notes-app:commit:abc1234", { selector: ".t" })).toBeInTheDocument();
    expect(screen.getByLabelText("Link a ref")).toHaveValue("");
  });

  it("refuses text that is not a ref", async () => {
    const { user } = await open();
    await user.type(screen.getByLabelText("Link a ref"), "hello world");
    await user.click(screen.getByRole("button", { name: "Add link" }));
    expect(getState().toast?.error).toBe(true);
    expect(getState().toast?.msg).toMatch(/not a ref/);
    expect(task("notes-app", "T-1").links).toHaveLength(4);
  });

  it("removes a link at once and on the server", async () => {
    const { user } = await open();
    const ref = "notes-app:file:design/HANDOFF.md";
    await user.click(await screen.findByRole("button", { name: "Unlink " + ref }));
    expect(screen.queryByRole("button", { name: "Unlink " + ref })).toBeNull();
    await waitFor(() => expect(task("notes-app", "T-1").links).not.toContain(ref));
    expect(getState().toast?.msg).toBe("Link removed");
  });

  it("shows an auto link with its reason, and hiding it calls the same delete", async () => {
    const deleted: string[] = [];
    server.use(
      http.get("*/api/projects/:p/tasks/:id", ({ params }) => {
        if (params.id !== "T-1") return;
        const t = task("notes-app", "T-1");
        return HttpResponse.json({
          task: t,
          rev: "r1",
          links: [
            { ref: "notes-app:commit:b7dc920bef", source: "auto", reason: "commit message", resolved: { title: "Add the chambers", subtitle: "commit", date: null } },
            { ref: "notes-app:file:plan/roadmap.md", source: "manual", resolved: null },
          ],
          snapshots: [],
          notes: [],
          log: [],
        });
      }),
      http.delete("*/api/projects/:p/tasks/:id/links", ({ request }) => {
        deleted.push(new URL(request.url).searchParams.get("ref") ?? "");
        return HttpResponse.json({ links: [] });
      }),
    );
    const { user } = await open();
    expect(await screen.findByText("auto: commit message")).toBeInTheDocument();
    expect(screen.getByText("manual")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Hide auto link notes-app:commit:b7dc920bef" }));
    await waitFor(() => expect(deleted).toEqual(["notes-app:commit:b7dc920bef"]));
    expect(getState().toast?.msg).toBe("Auto link hidden");
  });

  it("opens a link in the viewport", async () => {
    const { user, router } = await open();
    const list = within(await screen.findByRole("list", { name: "Links" }));
    await user.click(list.getAllByRole("listitem")[0].querySelector(".tk-tb-linkbtn") as HTMLElement);
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:commit:b7dc920bef");
  });
});

describe("task body: snapshots and notes", () => {
  it("shows one thumbnail per version with the verdict border", async () => {
    await open();
    const strip = within(await screen.findByTestId("task-snaps"));
    const btns = strip.getAllByRole("button");
    expect(btns.map((b) => b.getAttribute("aria-label"))).toEqual([
      "Open Ideas page v2 (Changes requested)",
      "Open Ideas page v3 (Pending)",
    ]);
    expect((btns[0].querySelector("img") as HTMLImageElement).style.getPropertyValue("--v")).toBe("var(--coral)");
  });

  it("goes to the review for a pending latest snapshot", async () => {
    const { user, router } = await open();
    await user.click(await screen.findByRole("button", { name: "Review v3 in the tracker →" }));
    expect(router.state.location.pathname + router.state.location.search).toBe("/tracker?filter=review&project=notes-app");
  });

  it("adds a snapshot from a path", async () => {
    const { user } = await open();
    await screen.findByTestId("task-snaps");
    await user.type(screen.getByLabelText("Snapshot URL or path"), "design/screenshots/ph4.png");
    await user.click(screen.getByRole("button", { name: "Add snapshot" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("Snapshot v4 added"));
    const added = mockDb().snapshots.find((s) => s.projectId === "notes-app" && s.id === "T-1/v4");
    expect(added).toMatchObject({ verdict: "pending", path: "design/screenshots/ph4.png" });
    expect(await screen.findByRole("button", { name: /^Open .* v4 \(Pending\)/ })).toBeInTheDocument();
  });

  it("uploads an image file as multipart", async () => {
    // jsdom cannot send a real multipart body through MSW, so look at what the client is asked to send.
    const spy = vi.spyOn(api, "createSnapshot").mockResolvedValue({
      snapshot: { version: 4, title: "x", url: "", captured: "", author: "", commit: null, verdict: "pending", reviewNote: "", notes: [] },
    });
    const { user } = await open();
    const file = new File(["png"], "shot.png", { type: "image/png" });
    await user.upload(screen.getByLabelText("Snapshot image file"), file);
    await waitFor(() => expect(getState().toast?.msg).toBe("Snapshot v4 added"));
    const [p, id, body] = spy.mock.calls[0];
    expect([p, id]).toEqual(["notes-app", "T-1"]);
    expect(body).toBeInstanceOf(FormData);
    expect((body as FormData).get("image")).toBeInstanceOf(File);
    expect(((body as FormData).get("image") as File).name).toBe("shot.png");
    spy.mockRestore();
  });

  it("adds, resolves and reopens a note", async () => {
    const { user } = await open();
    await user.type(screen.getByLabelText("Note text"), "Check the dome");
    await user.click(screen.getByRole("button", { name: "Add note" }));
    await waitFor(() => expect(mockDb().notes.some((n) => n.text === "Check the dome" && n.ref === "notes-app:task:T-1")).toBe(true));
    const item = await screen.findByText("Check the dome");
    const li = within(item.closest("li") as HTMLElement);
    await user.click(li.getByRole("button", { name: "Resolve" }));
    await waitFor(() => expect(mockDb().notes.find((n) => n.text === "Check the dome")?.resolved).toBe(true));
    await waitFor(() => expect(within(screen.getByText("Check the dome").closest("li") as HTMLElement).getByRole("button", { name: "Reopen" })).toBeInTheDocument());
  });
});
