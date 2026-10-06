// Pin the time zone so the HH:MM and day labels do not depend on the machine.
process.env.TZ = "Asia/Kolkata";

import { screen, waitFor, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { applyEvent } from "~/lib/events";
import { invalidate } from "~/lib/query";
import { getState } from "~/lib/store";
import type * as T from "~/lib/types";
import { mockDb } from "~/mocks/handlers";
import { inboxHandlers, resetInboxMocks, seedInboxMock } from "~/mocks/inbox";
import { server } from "~/mocks/node";
import { renderApp } from "../../../test/render";
import { relTime, noteOpenRef, groupByDay, noteWhere } from "./util";

beforeEach(() => {
  resetInboxMocks();
  server.use(...inboxHandlers);
});
afterEach(() => resetInboxMocks());

const rows = () => screen.queryAllByTestId("note-row");
const events = () => screen.queryAllByTestId("event-row");
const tab = (name: RegExp) => screen.getByRole("tab", { name });

async function loaded() {
  await waitFor(() => expect(rows().length).toBeGreaterThan(0));
  await waitFor(() => expect(events().length).toBeGreaterThan(0));
}

/** A custom /inbox answer for one test. */
function inboxAnswer(body: Partial<T.InboxResponse> | ((url: URL) => Partial<T.InboxResponse>)) {
  server.use(
    http.get("*/api/inbox", ({ request }) => {
      const b = typeof body === "function" ? body(new URL(request.url)) : body;
      return HttpResponse.json<T.InboxResponse>({ notes: [], unfiledSnapshots: [], proposals: [], feed: [], next: null, ...b });
    }),
  );
}

const ev = (over: Partial<T.DayEvent> = {}): T.DayEvent => ({
  ts: "2026-09-30T10:00:00.000Z",
  kind: "commit",
  ref: "acme-api:commit:abc1234",
  label: "a commit",
  projectId: "acme-api",
  ...over,
});

describe("layout and tiles", () => {
  it("shows the heading, the open notes and the four tiles", async () => {
    renderApp("/inbox");
    await loaded();
    expect(screen.getByRole("heading", { name: "Inbox" })).toBeInTheDocument();
    expect(screen.getByText("everything addressed to you")).toBeInTheDocument();
    expect(rows()).toHaveLength(5);
    await waitFor(() => expect(screen.getByTestId("tile-tasks")).toHaveTextContent("3"));
    expect(screen.getByTestId("tile-snaps")).toHaveTextContent("4snapshots awaiting review");
    expect(screen.getByTestId("tile-notes")).toHaveTextContent("5open notes");
    expect(screen.getByTestId("tile-dirty")).toHaveTextContent("6dirty working copies");
  });

  it("lists dirty working copies and skips hidden projects", async () => {
    renderApp("/inbox");
    await loaded();
    const group = screen.getByRole("group", { name: "Uncommitted working copies" });
    const names = within(group)
      .getAllByRole("button")
      .map((b) => b.textContent);
    expect(names).toHaveLength(6);
    expect(names).toContain("●widget-kit 12 files");
    expect(names).toContain("●sampler 1 file");
    expect(names.join("|")).not.toContain("widget-kit-v0.9.0");
  });

  it("sends both review tiles to the tracker review filter and the dirty chips to the project commits tab", async () => {
    const { router, user } = renderApp("/inbox");
    await loaded();
    await user.click(screen.getByTestId("tile-snaps"));
    expect(router.state.location.pathname + router.state.location.search).toBe("/tracker?filter=review");
    await user.click(screen.getByTestId("rail-inbox"));
    await loaded();
    await user.click(screen.getByTestId("tile-tasks"));
    expect(router.state.location.pathname + router.state.location.search).toBe("/tracker?filter=review");
    await user.click(screen.getByTestId("rail-inbox"));
    await loaded();
    await user.click(within(screen.getByRole("group", { name: "Uncommitted working copies" })).getByRole("button", { name: /acme-api/ }));
    expect(router.state.location.pathname).toBe("/p/acme-api/commits");
  });

  it("'Show open' on the notes tile switches back to the Open tab", async () => {
    const { user } = renderApp("/inbox");
    await loaded();
    await user.click(tab(/^Resolved/));
    await waitFor(() => expect(rows()).toHaveLength(1));
    await user.click(screen.getByTestId("tile-notes"));
    await waitFor(() => expect(rows()).toHaveLength(5));
  });

  it("says 'Nothing needs you.' when every count is 0 (and keeps the tiles)", async () => {
    const db = mockDb();
    db.notes = [];
    db.tasks.forEach((t) => {
      t.status = "done";
      t.latestSnapshot = null;
    });
    db.projects.forEach((p) => {
      p.dirtyCount = 0;
    });
    renderApp("/inbox");
    await screen.findByText("Nothing needs you.");
    expect(screen.getByTestId("tile-dirty")).toBeDisabled();
    expect(screen.getByTestId("tile-dirty")).toHaveTextContent("All clean");
  });
});

describe("notes: filters and states", () => {
  it("Open / Resolved / All show 5 / 1 / 6 notes with count badges", async () => {
    const { user } = renderApp("/inbox");
    await loaded();
    expect(tab(/^Open/)).toHaveTextContent("5");
    await waitFor(() => expect(tab(/^Resolved/)).toHaveTextContent("1"));
    await waitFor(() => expect(tab(/^All/)).toHaveTextContent("6"));

    await user.click(tab(/^Resolved/));
    await waitFor(() => expect(rows()).toHaveLength(1));
    expect(rows()[0]).toHaveTextContent("Keep this file as the single handoff between sessions.");
    expect(within(rows()[0]).getByRole("button", { name: /^Reopen/ })).toBeInTheDocument();
    expect(tab(/^Resolved/)).toHaveAttribute("aria-selected", "true");

    await user.click(tab(/^All/));
    await waitFor(() => expect(rows()).toHaveLength(6));
  });

  it("arrow keys move between the tabs", async () => {
    const { user } = renderApp("/inbox");
    await loaded();
    tab(/^Open/).focus();
    await user.keyboard("{ArrowRight}");
    expect(tab(/^Resolved/)).toHaveAttribute("aria-selected", "true");
    expect(tab(/^Resolved/)).toHaveFocus();
    await user.keyboard("{ArrowLeft}{ArrowLeft}");
    expect(tab(/^All/)).toHaveAttribute("aria-selected", "true");
  });

  it("draws one row per note: state dot, type chip, title, where, author, project", async () => {
    renderApp("/inbox");
    await loaded();
    const file = rows().find((r) => r.textContent?.includes("devNames"))!;
    expect(file).toHaveTextContent("file");
    expect(file).toHaveTextContent("discover.go");
    expect(file).toHaveTextContent("line 30");
    expect(file).toHaveTextContent("dev · 7h ago · acme-api");
    const pin = rows().find((r) => r.textContent?.includes("Constellation"))!;
    expect(pin).toHaveTextContent("pin 46% 20%");
    expect(pin).toHaveTextContent("snapshot");
  });

  it("shows the three empty states", async () => {
    mockDb().notes = [];
    const { user } = renderApp("/inbox");
    await screen.findByText("No open notes. All clear.");
    expect(screen.getByText("Notes pinned to files, commits, sessions and snapshots collect here.")).toBeInTheDocument();
    await user.click(tab(/^Resolved/));
    await screen.findByText("Nothing resolved yet.");
    await user.click(tab(/^All/));
    await screen.findByText("No notes yet.");
  });

  it("shows an error with Retry when the inbox cannot load", async () => {
    let fail = true;
    server.use(
      http.get("*/api/inbox", () => (fail ? HttpResponse.json({ error: { code: "boom", message: "index is busy" } }, { status: 500 }) : undefined)),
    );
    const { user } = renderApp("/inbox");
    const alerts = await screen.findAllByRole("alert");
    expect(alerts.length).toBeGreaterThan(0);
    expect(alerts[0]).toHaveTextContent("index is busy");
    fail = false;
    await user.click(within(alerts[0]).getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(rows()).toHaveLength(5));
  });
});

describe("notes: resolve and reopen", () => {
  it("resolve updates at once, sends {resolved:true}, then settles", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const seen: { body: unknown; token: string | null }[] = [];
    server.use(
      http.patch("*/api/projects/:p/notes/:id", async ({ request, params }) => {
        seen.push({ body: await request.json(), token: request.headers.get("X-Drashta-Token") });
        await gate;
        const n = mockDb().notes.find((x) => x.projectId === params.p && x.id === params.id)!;
        n.resolved = true;
        return HttpResponse.json({ note: n });
      }),
    );
    const { user } = renderApp("/inbox");
    await loaded();
    await user.click(screen.getByRole("button", { name: "Resolve note N-4 in acme-api" }));
    // optimistic: the row is gone from Open while the request is still pending
    await waitFor(() => expect(rows()).toHaveLength(4));
    expect(tab(/^Open/)).toHaveTextContent("4");
    expect(seen).toEqual([{ body: { resolved: true }, token: "mock-token" }]);
    release();
    await waitFor(() => expect(screen.getByTestId("toast")).toHaveTextContent("N-4 resolved"));
    await waitFor(() => expect(tab(/^Resolved/)).toHaveTextContent("2"));
    expect(rows()).toHaveLength(4);
    // and it now shows in Resolved with a Reopen button
    await user.click(tab(/^Resolved/));
    await waitFor(() => expect(rows()).toHaveLength(2));
    expect(screen.getByRole("button", { name: "Reopen note N-4 in acme-api" })).toBeInTheDocument();
  });

  it("puts the note back and shows an error toast when the request fails", async () => {
    server.use(
      http.patch("*/api/projects/:p/notes/:id", () => HttpResponse.json({ error: { code: "disk", message: "disk is full" } }, { status: 500 })),
    );
    const { user } = renderApp("/inbox");
    await loaded();
    await user.click(screen.getByRole("button", { name: "Resolve note N-4 in acme-api" }));
    await waitFor(() => expect(screen.getByTestId("toast")).toHaveTextContent("Could not resolve N-4: disk is full"));
    expect(screen.getByTestId("toast")).toHaveClass("error");
    expect(rows()).toHaveLength(5);
    expect(tab(/^Open/)).toHaveTextContent("5");
  });

  it("reopens a resolved note and moves it back to Open", async () => {
    const { user } = renderApp("/inbox");
    await loaded();
    await user.click(tab(/^Resolved/));
    await waitFor(() => expect(rows()).toHaveLength(1));
    await user.click(screen.getByRole("button", { name: "Reopen note N-5 in notes-app" }));
    await waitFor(() => expect(rows()).toHaveLength(0));
    await screen.findByText("Nothing resolved yet.");
    await waitFor(() => expect(getState().toast?.msg).toBe("N-5 reopened"));
    expect(mockDb().notes.find((n) => n.id === "N-5")!.resolved).toBe(false);
    await user.click(tab(/^Open/));
    await waitFor(() => expect(rows()).toHaveLength(6));
  });
});

describe("notes and events open the viewport", () => {
  it("a note opens its item with the line as a fragment", async () => {
    const { router, user } = renderApp("/inbox");
    await loaded();
    const row = rows().find((r) => r.textContent?.includes("devNames"))!;
    await user.click(within(row).getByRole("button", { name: /^Preview/ }));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("acme-api:file:discover.go#L30");
  });

  it("a pinned note opens the snapshot with the pin as a fragment", async () => {
    const { router, user } = renderApp("/inbox");
    await loaded();
    const row = rows().find((r) => r.textContent?.includes("Constellation"))!;
    await user.click(within(row).getByRole("button", { name: /^Preview/ }));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:snapshot:T-1/v3#x=0.46,y=0.2");
  });

  it("an activity row opens its item", async () => {
    inboxAnswer({ feed: [ev({ ref: "acme-api:commit:bdcb99c", label: "Guard API against CSRF" })] });
    const { router, user } = renderApp("/inbox");
    await user.click(await screen.findByRole("button", { name: /Guard API against CSRF/ }));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("acme-api:commit:bdcb99c");
  });
});

describe("activity", () => {
  it("groups events by day with Today / Yesterday and shows HH:MM", async () => {
    renderApp("/inbox");
    await loaded();
    const days = screen.getAllByRole("region").filter((r) => /Today|Yesterday|\d{4}|\w{3} \d/.test(r.getAttribute("aria-label") ?? ""));
    expect(days[0]).toHaveAccessibleName("Today Thu 1 Oct");
    expect(within(days[0]).getByText("2 events")).toBeInTheDocument();
    expect(days[1]).toHaveAccessibleName("Yesterday Wed 30 Sep");
    expect(within(days[0]).getAllByTestId("event-row")[0]).toHaveTextContent("02:43");
    expect(within(days[0]).getAllByTestId("event-row")[0]).toHaveTextContent("session");
  });

  it("filters by kind and shows the empty sentence", async () => {
    inboxAnswer({
      feed: [ev({ label: "c1" }), ev({ label: "s1", kind: "session", ref: "acme-api:session:1" }), ev({ label: "c2", ts: "2026-09-30T09:00:00.000Z" })],
    });
    const { user } = renderApp("/inbox");
    await waitFor(() => expect(events()).toHaveLength(3));
    const kinds = screen.getByRole("group", { name: "Activity kind" });
    expect(within(kinds).getByRole("button", { name: /^All/ })).toHaveTextContent("3");
    expect(within(kinds).getByRole("button", { name: /^Commits/ })).toHaveTextContent("2");
    await user.click(within(kinds).getByRole("button", { name: /^Commits/ }));
    expect(events().map((e) => e.getAttribute("data-kind"))).toEqual(["commit", "commit"]);
    expect(within(kinds).getByRole("button", { name: /^Commits/ })).toHaveAttribute("aria-pressed", "true");
    await user.click(within(kinds).getByRole("button", { name: /^Notes/ }));
    expect(screen.getByText("No activity of this kind yet.")).toBeInTheDocument();
  });

  it("skips events of hidden projects and shows the verdict chip on snapshots", async () => {
    inboxAnswer({
      feed: [
        ev({ label: "visible commit" }),
        ev({ label: "secret commit", projectId: "venv", ref: "venv:commit:aaa1111" }),
        ev({ label: "Gate art v3", kind: "snapshot", ref: "notes-app:snapshot:T-1/v3", projectId: "notes-app", context: "changes" }),
      ],
    });
    renderApp("/inbox");
    await waitFor(() => expect(events()).toHaveLength(2));
    expect(screen.queryByText("secret commit")).toBeNull();
    const snap = events().find((e) => e.getAttribute("data-kind") === "snapshot")!;
    expect(snap).toHaveTextContent("Changes");
  });

  it("loads more with the cursor and appends without duplicates", async () => {
    seedInboxMock({ pageSize: 30 });
    const { user } = renderApp("/inbox");
    await waitFor(() => expect(events()).toHaveLength(30));
    expect(screen.getByRole("button", { name: /^All/ })).toHaveTextContent("30+");
    expect(rows()).toHaveLength(5);
    await user.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(events()).toHaveLength(60));
    expect(rows()).toHaveLength(5);
    // the keys are unique: no event row twice
    const keys = events().map((e) => e.textContent);
    expect(new Set(keys).size).toBeGreaterThan(50);
  });

  it("shows no Load more when the feed is complete", async () => {
    seedInboxMock({ pageSize: 100000 });
    renderApp("/inbox");
    await waitFor(() => expect(events().length).toBeGreaterThan(100));
    expect(screen.queryByRole("button", { name: /^Load more/ })).toBeNull();
  });

  it("caps a long feed and offers 'Show all N'", async () => {
    const many = Array.from({ length: 300 }, (_, i) => ev({ label: "commit " + i, ref: "acme-api:commit:c" + i, ts: new Date(Date.UTC(2026, 8, 30, 10, 0, 0) - i * 60000).toISOString() }));
    inboxAnswer({ feed: many });
    const { user } = renderApp("/inbox");
    await waitFor(() => expect(events()).toHaveLength(250));
    await user.click(screen.getByRole("button", { name: "Show all 300" }));
    expect(events()).toHaveLength(300);
  });

  it("shows 'No activity yet.' for an empty feed", async () => {
    inboxAnswer({});
    renderApp("/inbox");
    await screen.findByText("No activity yet.");
  });
});

describe("notes paging", () => {
  it("shows Load more under the notes when a full page came back", async () => {
    const notes = Array.from({ length: 50 }, (_, i): T.Note => ({
      id: "N-" + (i + 1),
      ref: "acme-api:file:a.go",
      projectId: "acme-api",
      text: "note " + (i + 1),
      author: "alice",
      date: new Date(Date.UTC(2026, 8, 30, 10, 0, 0) - i * 60000).toISOString(),
      resolved: false,
      line: i + 1,
    }));
    const more: T.Note[] = [{ ...notes[0], id: "N-51", text: "note 51", date: "2026-09-29T10:00:00.000Z" }];
    inboxAnswer((u) => (u.searchParams.get("cursor") ? { notes: more, feed: [] } : { notes, feed: [ev()], next: "page2" }));
    const { user } = renderApp("/inbox");
    await waitFor(() => expect(rows()).toHaveLength(50));
    const btn = within(screen.getByTestId("notes-scroll")).getByRole("button", { name: "Load more" });
    await user.click(btn);
    await waitFor(() => expect(rows()).toHaveLength(51));
  });

  it("caps a long notes list and offers 'Show all N'", async () => {
    const notes = Array.from({ length: 210 }, (_, i): T.Note => ({
      id: "N-" + (i + 1),
      ref: "acme-api:file:a.go",
      projectId: "acme-api",
      text: "note " + (i + 1),
      author: "alice",
      date: new Date(Date.UTC(2026, 8, 30, 10, 0, 0) - i * 60000).toISOString(),
      resolved: false,
    }));
    inboxAnswer({ notes });
    const { user } = renderApp("/inbox");
    await waitFor(() => expect(rows()).toHaveLength(200));
    await user.click(screen.getByRole("button", { name: "Show all 210" }));
    expect(rows()).toHaveLength(210);
  });
});

describe("unfiled snapshots", () => {
  const drop = { name: "gate-try2.png", projectId: "notes-app", path: ".drashta/snapshots/inbox/gate-try2.png", date: "2026-09-30T20:00:00.000Z" };

  it("files a drop into a task and refreshes", async () => {
    seedInboxMock({ unfiled: [drop] });
    const filed: unknown[] = [];
    server.use(
      http.post("*/api/projects/:p/snapshots/inbox/:name/file", async ({ request }) => {
        filed.push(await request.clone().json());
        return undefined; // fall through to the mock handler
      }),
    );
    const { user } = renderApp("/inbox");
    const pill = await screen.findByRole("button", { name: /gate-try2\.png/ });
    expect(pill).toHaveAttribute("aria-expanded", "false");
    await user.click(pill);
    const panel = screen.getByRole("group", { name: "File gate-try2.png" });

    // no task yet: a message, nothing sent
    await user.click(within(panel).getByRole("button", { name: "File snapshot" }));
    expect(within(panel).getByRole("alert")).toHaveTextContent("Pick a task to file it under.");
    expect(filed).toEqual([]);

    await user.selectOptions(within(panel).getByRole("combobox", { name: "Task for gate-try2.png" }), "T-1");
    await user.click(within(panel).getByRole("button", { name: "File snapshot" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("gate-try2.png filed to T-1 · v4"));
    expect(filed).toEqual([{ taskId: "T-1" }]);
    expect(mockDb().snapshots.some((s) => s.projectId === "notes-app" && s.taskId === "T-1" && s.version === 4)).toBe(true);
    await waitFor(() => expect(screen.queryByRole("button", { name: /gate-try2\.png/ })).toBeNull());
  });

  it("shows the server error and keeps the drop when filing fails", async () => {
    seedInboxMock({ unfiled: [drop] });
    server.use(http.post("*/api/projects/:p/snapshots/inbox/:name/file", () => HttpResponse.json({ error: { code: "gone", message: "the file is gone" } }, { status: 404 })));
    const { user } = renderApp("/inbox");
    await user.click(await screen.findByRole("button", { name: /gate-try2\.png/ }));
    const panel = screen.getByRole("group", { name: "File gate-try2.png" });
    await user.selectOptions(within(panel).getByRole("combobox"), "T-2");
    await user.click(within(panel).getByRole("button", { name: "File snapshot" }));
    await waitFor(() => expect(within(panel).getByRole("alert")).toHaveTextContent("the file is gone"));
    expect(getState().toast?.error).toBe(true);
    expect(screen.getByRole("button", { name: /gate-try2\.png/ })).toBeInTheDocument();
  });

  it("says so when the project has no task", async () => {
    mockDb().tasks = mockDb().tasks.filter((t) => t.projectId !== "sampler");
    seedInboxMock({ unfiled: [{ ...drop, projectId: "sampler", name: "x.png" }] });
    const { user } = renderApp("/inbox");
    await user.click(await screen.findByRole("button", { name: /x\.png/ }));
    expect(screen.getByText(/has no tasks yet/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /x\.png/ }));
    expect(screen.queryByText(/has no tasks yet/)).toBeNull();
  });
});

describe("close-task proposals", () => {
  const proposal: T.InboxProposal = { kind: "close_task", taskRef: "notes-app:task:T-1", commitRef: "notes-app:commit:b7dc920bef" };

  it("asks first, then sends a PATCH with the rev and status done", async () => {
    seedInboxMock({ proposals: [proposal] });
    const patches: { body: unknown; ifMatch: string | null }[] = [];
    server.use(
      http.patch("*/api/projects/:p/tasks/:id", async ({ request }) => {
        patches.push({ body: await request.clone().json(), ifMatch: request.headers.get("If-Match") });
        return undefined;
      }),
    );
    const { user } = renderApp("/inbox");
    const row = await screen.findByTestId("proposal");
    expect(row).toHaveTextContent("T-1 Ideas page");
    expect(row).toHaveTextContent("commit b7dc920");

    // never silent: the button only asks
    await user.click(within(row).getByRole("button", { name: "Close task" }));
    expect(patches).toEqual([]);
    expect(within(row).getByText("Move T-1 to done?")).toBeInTheDocument();
    await user.click(within(row).getByRole("button", { name: "Not now" }));
    expect(patches).toEqual([]);
    expect(within(row).getByRole("button", { name: "Close task" })).toBeInTheDocument();

    await user.click(within(row).getByRole("button", { name: "Close task" }));
    await user.click(within(row).getByRole("button", { name: "Move to done" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("T-1 → done"));
    expect(patches).toEqual([{ body: { status: "done" }, ifMatch: "r1" }]);
    expect(mockDb().tasks.find((t) => t.id === "T-1" && t.projectId === "notes-app")!.status).toBe("done");
  });

  it("explains a 409 and leaves the task alone", async () => {
    seedInboxMock({ proposals: [proposal] });
    server.use(http.patch("*/api/projects/:p/tasks/:id", () => HttpResponse.json({ error: { code: "rev_conflict", message: "changed" } }, { status: 409 })));
    const { user } = renderApp("/inbox");
    const row = await screen.findByTestId("proposal");
    await user.click(within(row).getByRole("button", { name: "Close task" }));
    await user.click(within(row).getByRole("button", { name: "Move to done" }));
    await waitFor(() => expect(getState().toast?.msg).toBe("T-1 changed on disk, reloaded. Try again."));
    expect(getState().toast?.error).toBe(true);
    expect(mockDb().tasks.find((t) => t.id === "T-1")!.status).toBe("review");
  });

  it("opens the task and the commit in the viewport", async () => {
    seedInboxMock({ proposals: [proposal] });
    const { router, user } = renderApp("/inbox");
    const row = await screen.findByTestId("proposal");
    await user.click(within(row).getByRole("button", { name: /^T-1/ }));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:task:T-1");
    await user.click(within(row).getByRole("button", { name: /commit b7dc920/ }));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:commit:b7dc920bef");
  });
});

describe("live updates", () => {
  it("a tracker event adds a new note and a counts event moves the review tile", async () => {
    renderApp("/inbox");
    await loaded();
    mockDb().notes.push({ id: "N-9", ref: "acme-api:commit:bdcb99c", projectId: "acme-api", text: "brand new note", author: "Claude", date: "2026-09-30T21:00:00.000Z", resolved: false });
    applyEvent({ type: "tracker", data: { projectId: "acme-api", kind: "note", id: "N-9" } });
    await screen.findByText("brand new note");
    expect(rows()).toHaveLength(6);
    applyEvent({ type: "counts", data: { liveServers: 1, openTasks: 1, awaitingReview: 7, openNotes: 6 } });
    await waitFor(() => expect(screen.getByTestId("tile-snaps")).toHaveTextContent("7snapshots awaiting review"));
  });

  it("a project event fetches each /inbox page once, and does not loop (M18)", async () => {
    let calls = 0;
    server.use(
      http.get("*/api/inbox", () => {
        calls++;
        return undefined; // fall through to the mock handler
      }),
    );
    renderApp("/inbox");
    await loaded();
    const before = calls;
    applyEvent({ id: "1", type: "project", data: { projectId: "notes-app", what: "commits" } });
    await waitFor(() => expect(calls).toBeGreaterThan(before));
    await new Promise((r) => setTimeout(r, 300));
    const settled = calls;
    await new Promise((r) => setTimeout(r, 300));
    expect(calls).toBe(settled);
    expect(settled).toBe(before + 2); // open + resolved, once each (not again after /boot)
  });

  it("a /boot refetch alone does not refetch the inbox", async () => {
    let calls = 0;
    server.use(
      http.get("*/api/inbox", () => {
        calls++;
        return undefined;
      }),
    );
    renderApp("/inbox");
    await loaded();
    const before = calls;
    invalidate("/boot");
    await new Promise((r) => setTimeout(r, 300));
    expect(calls).toBe(before);
  });
});

describe("helpers", () => {
  it("relTime follows the prototype wording", () => {
    const now = Date.parse("2026-09-30T12:00:00Z");
    expect(relTime("2026-09-30T11:59:40Z", now)).toBe("just now");
    expect(relTime("2026-09-30T11:55:00Z", now)).toBe("5m ago");
    expect(relTime("2026-09-30T05:00:00Z", now)).toBe("7h ago");
    expect(relTime("2026-09-29T08:00:00Z", now)).toBe("yesterday");
    expect(relTime("2026-09-26T12:00:00Z", now)).toBe("4d ago");
    expect(relTime("nope", now)).toBe("");
  });

  it("noteOpenRef and noteWhere use the line, the pin or the ref fragment", () => {
    const base = { id: "N-1", projectId: "p", text: "t", author: "a", date: "2026-01-01T00:00:00Z", resolved: false } as const;
    expect(noteOpenRef({ ...base, ref: "file:src/x.go", line: 12 })).toBe("p:file:src/x.go#L12");
    expect(noteOpenRef({ ...base, ref: "p:image:a.png", x: 0.5, y: 0.25 })).toBe("p:image:a.png#x=0.5,y=0.25");
    expect(noteOpenRef({ ...base, ref: "p:file:a.go#L3-L9" })).toBe("p:file:a.go#L3-L9");
    expect(noteWhere({ ...base, ref: "p:file:a.go#L3-L9" })).toBe("lines 3-9");
    expect(noteWhere({ ...base, ref: "p:commit:abc1234" })).toBe("");
  });

  it("groupByDay labels Today and Yesterday", () => {
    const now = Date.parse("2026-09-30T12:00:00Z");
    const g = groupByDay([ev({ ts: "2026-09-30T10:00:00Z" }), ev({ ts: "2026-09-29T10:00:00Z" }), ev({ ts: "2026-09-20T10:00:00Z" })], now);
    expect(g.map((d) => d.label)).toEqual(["Today", "Yesterday", "Sun 20 Sep"]);
    expect(g[2].sub).toBe("2026");
  });
});
