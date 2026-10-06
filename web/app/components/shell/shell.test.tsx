import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { getState, setState } from "~/lib/store";
import { http, HttpResponse } from "msw";
import { mockDb } from "~/mocks/handlers";
import { server } from "~/mocks/node";
import { renderApp } from "../../../test/render";

function expectedCounts() {
  const db = mockDb();
  return {
    live: db.servers.filter((s) => s.running).length,
    tasks: db.tasks.filter((t) => t.status !== "done").length,
    review: db.tasks.filter((t) => t.latestSnapshot?.verdict === "pending").length,
    notes: db.notes.filter((n) => !n.resolved).length,
  };
}

describe("top bar", () => {
  it("shows the four counters from /api/boot", async () => {
    renderApp("/");
    const c = expectedCounts();
    await waitFor(() => expect(screen.getByTestId("counter-live")).toHaveTextContent("live" + c.live));
    expect(screen.getByTestId("counter-tasks")).toHaveTextContent("tasks" + c.tasks);
    expect(screen.getByTestId("counter-review")).toHaveTextContent("review" + c.review);
    expect(screen.getByTestId("counter-notes")).toHaveTextContent("notes" + c.notes);
  });

  it("counters go to their sections", async () => {
    const { router, user } = renderApp("/inbox");
    await user.click(screen.getByTestId("counter-review"));
    expect(router.state.location.pathname + router.state.location.search).toBe("/tracker?filter=review");
    await user.click(screen.getByTestId("counter-tasks"));
    expect(router.state.location.pathname + router.state.location.search).toBe("/tracker");
    await user.click(screen.getByTestId("counter-notes"));
    expect(router.state.location.pathname).toBe("/inbox");
    await user.click(screen.getByTestId("counter-live"));
    expect(router.state.location.pathname).toBe("/");
  });

  it("the app name is the only link, and it goes home", async () => {
    const { router, user } = renderApp("/tracker");
    const header = screen.getByRole("banner");
    const links = within(header).getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveAccessibleName("drashta, home");
    expect(links[0]).toHaveTextContent("द्रष्टा");
    await user.click(links[0]);
    expect(router.state.location.pathname).toBe("/");
  });

  it("shows the subtitle on Home and a breadcrumb elsewhere", async () => {
    const { user } = renderApp("/");
    expect(screen.getByText("every tremor in your projects, last 30 days")).toBeInTheDocument();
    await user.click(screen.getByTestId("rail-tracker"));
    expect(screen.queryByText(/every tremor/)).toBeNull();
    expect(screen.getByRole("navigation", { name: "Breadcrumb" })).toHaveTextContent("Tracker");
  });

  it("names the project and tab in the breadcrumb", async () => {
    renderApp("/p/acme-api/commits");
    const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
    await waitFor(() => expect(crumbs).toHaveTextContent("Project›acme-api›commits"));
  });
});

describe("rail", () => {
  it("shows Project, Tracker, Inbox with badges", async () => {
    renderApp("/");
    const c = expectedCounts();
    await waitFor(() => expect(screen.getByTestId("badge-tracker")).toHaveTextContent(String(c.review)));
    expect(screen.getByTestId("badge-inbox")).toHaveTextContent(String(c.notes));
    expect(screen.queryByTestId("badge-project")).toBeNull();
    const nav = screen.getByRole("navigation", { name: "Sections" });
    expect(within(nav).getAllByRole("button").map((b) => b.textContent)).toEqual(["Project", c.review + "Tracker", c.notes + "Inbox"]);
  });

  it("hides a badge at 0", async () => {
    const db = mockDb();
    db.notes.forEach((n) => (n.resolved = true));
    renderApp("/");
    await waitFor(() => expect(screen.getByTestId("badge-tracker")).toBeInTheDocument());
    expect(screen.queryByTestId("badge-inbox")).toBeNull();
  });

  it("routes and marks the active item", async () => {
    const { router, user } = renderApp("/");
    await user.click(screen.getByTestId("rail-inbox"));
    expect(router.state.location.pathname).toBe("/inbox");
    expect(screen.getByTestId("rail-inbox")).toHaveAttribute("aria-current", "page");
    await user.click(screen.getByTestId("rail-tracker"));
    expect(router.state.location.pathname).toBe("/tracker");
  });

  it("Project opens the picker first, then the last project", async () => {
    const { router, user } = renderApp("/");
    await user.click(screen.getByTestId("rail-project"));
    expect(router.state.location.pathname).toBe("/p");
    const list = await screen.findByRole("list", { name: "Projects" });
    await user.click(await within(list).findByText("acme-api"));
    expect(router.state.location.pathname).toBe("/p/acme-api");
    await user.click(screen.getByTestId("rail-inbox"));
    await user.click(screen.getByTestId("rail-project"));
    expect(router.state.location.pathname).toBe("/p/acme-api");
  });
});

describe("readout mode", () => {
  it("hides search, refresh, rail, drawer and toasts and shows the hint", async () => {
    document.documentElement.classList.add("desktop");
    setState({ mode: "readout" });
    renderApp("/?item=acme-api:commit:93e82aa");
    expect(screen.getByTestId("readout-hint")).toHaveTextContent("⌥⌘D to interact");
    expect(screen.queryByRole("navigation", { name: "Sections" })).toBeNull();
    expect(screen.queryByRole("textbox", { name: "Search" })).toBeNull();
    expect(screen.queryByText("↻ refresh all")).toBeNull();
    expect(screen.queryByTestId("viewport")).toBeNull();
    expect(screen.queryByRole("status")).toBeNull();
    // the name is not a link, counters are read-only
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.getByTestId("counter-live").tagName).toBe("DIV");
  });

  it("switches back to browsing at once", async () => {
    setState({ mode: "readout" });
    renderApp("/");
    expect(screen.queryByRole("navigation", { name: "Sections" })).toBeNull();
    act(() => setState({ mode: "browsing" }));
    expect(screen.getByRole("navigation", { name: "Sections" })).toBeInTheDocument();
    expect(screen.queryByTestId("readout-hint")).toBeNull();
  });

  it("always shows Home, whatever the route", () => {
    setState({ mode: "readout" });
    renderApp("/tracker");
    expect(screen.getByRole("region", { name: "Home" })).toBeInTheDocument();
    expect(screen.getByText(/every tremor/)).toBeInTheDocument();
  });
});

describe("viewport drawer", () => {
  it("opens from ?item= and closes on Esc", async () => {
    const { router } = renderApp("/?item=acme-api:commit:93e82aa");
    const aside = await screen.findByRole("complementary", { name: "Viewport" });
    await waitFor(() => expect(within(aside).getByRole("heading", { level: 2 })).toHaveTextContent(/launchd/));
    expect(within(aside).getByText("commit")).toBeInTheDocument();
    expect(within(aside).getByRole("button", { name: /git show ↗/ })).toHaveAttribute("title", expect.stringContaining("git -C"));
    fireEvent.keyDown(aside, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("complementary", { name: "Viewport" })).toBeNull());
    expect(router.state.location.search).toBe("");
  });

  it("closes with ✕", async () => {
    const { user, router } = renderApp("/tracker?item=acme-api:commit:93e82aa");
    await user.click(await screen.findByRole("button", { name: "Close viewport" }));
    expect(router.state.location.pathname + router.state.location.search).toBe("/tracker");
  });

  it("shows the missing state for an unknown item", async () => {
    renderApp("/?item=acme-api:file:gone/away.go");
    expect(await screen.findByText("Nothing to show here.")).toBeInTheDocument();
    expect(screen.getByTestId("viewport-missing")).toHaveTextContent("acme-api:file:gone/away.go");
  });

  it("renders the registered body and linked tasks", async () => {
    renderApp("/?item=notes-app:commit:b7dc920bef");
    expect(await screen.findByTestId("commit-body")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: /T-1 ·/ })).toBeInTheDocument());
  });

  it("keeps a back/forward history", async () => {
    const { user, router } = renderApp("/?item=acme-api:commit:93e82aa");
    await screen.findByRole("complementary", { name: "Viewport" });
    await act(async () => {
      await router.navigate("/?item=notes-app:commit:b7dc920bef");
    });
    const back = screen.getByRole("button", { name: "Back" });
    await waitFor(() => expect(back).toBeEnabled());
    await user.click(back);
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("acme-api:commit:93e82aa");
    expect(screen.getByRole("button", { name: "Forward" })).toBeEnabled();
  });

  it("Open ↗ posts /api/open and confirms with a toast", async () => {
    const { user } = renderApp("/?item=acme-api:commit:93e82aa");
    const btn = await screen.findByRole("button", { name: /git show ↗/ });
    await user.click(btn);
    await waitFor(() => expect(screen.getByTestId("toast")).toHaveTextContent("git show ↗"));
  });
});

describe("search palette", () => {
  it("groups results, counts lit lanes, and Enter opens the first", async () => {
    const { user, router } = renderApp("/");
    const input = screen.getByRole("textbox", { name: "Search" });
    await user.type(input, "VCS stamping");
    const dlg = await screen.findByRole("dialog", { name: "Search results" });
    await waitFor(() => expect(within(dlg).getByText("Commits")).toBeInTheDocument());
    expect(within(dlg).getByText(/\d+ match(es)? · \d+ lanes? lit/)).toBeInTheDocument();
    await waitFor(() => expect(getState().searchLit).toContain("acme-api"));
    await user.keyboard("{Enter}");
    expect(router.state.location.pathname + router.state.location.search).toMatch(/item=.*%3Acommit%3A/);
    expect(screen.queryByRole("dialog", { name: "Search results" })).toBeNull();
  });

  it("says when nothing matches; Esc closes, a second Esc clears", async () => {
    const { user } = renderApp("/");
    const input = screen.getByRole("textbox", { name: "Search" });
    await user.type(input, "zzzqqqxx");
    expect(await screen.findByText("nothing registers for that.")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: "Search results" })).toBeNull();
    expect(input).toHaveValue("zzzqqqxx");
    await user.keyboard("{Escape}");
    expect(input).toHaveValue("");
  });

  it(":port matches servers only", async () => {
    const { user } = renderApp("/");
    await user.type(screen.getByRole("textbox", { name: "Search" }), ":8090");
    const dlg = await screen.findByRole("dialog", { name: "Search results" });
    await waitFor(() => expect(within(dlg).getByText("Servers")).toBeInTheDocument());
    expect(within(dlg).queryByText("Commits")).toBeNull();
  });
});

describe("refresh all", () => {
  it("starts a rescan and shows rescanning…", async () => {
    const { user } = renderApp("/");
    await user.click(screen.getByRole("button", { name: "↻ refresh all" }));
    expect(screen.getByRole("button", { name: "↻ rescanning…" })).toBeDisabled();
    expect(screen.getByTestId("scan-label")).toHaveTextContent("rescanning…");
    expect(screen.getByTestId("sweep")).toBeInTheDocument();
  });

  it("shows offline in coral when the stream is down", () => {
    setState({ online: false, scan: { running: false, at: "2026-10-01T14:53:00Z", updatedHere: false, sweep: 0, userPending: false, changes: 0 } });
    renderApp("/");
    const l = screen.getByTestId("scan-label");
    const at = new Date("2026-10-01T14:53:00Z"); // shown in the machine's local time
    const hm = String(at.getHours()).padStart(2, "0") + ":" + String(at.getMinutes()).padStart(2, "0");
    expect(l).toHaveTextContent("offline — last scan " + hm);
    expect(l).toHaveClass("offline");
  });
});

describe("desktop modes", () => {
  it("lights the current mode and switches on click", async () => {
    const { user } = renderApp("/");
    const group = await screen.findByRole("group", { name: "Desktop mode" });
    await waitFor(() => expect(within(group).getByRole("button", { name: "wallpaper" })).toHaveAttribute("aria-pressed", "true"));
    await user.click(within(group).getByRole("button", { name: "browse" }));
    await waitFor(() => expect(within(group).getByRole("button", { name: "browse" })).toHaveAttribute("aria-pressed", "true"));
    expect(within(group).getByRole("button", { name: "wallpaper" })).toHaveAttribute("aria-pressed", "false");
    expect(within(group).getByRole("button", { name: "normal" })).toBeInTheDocument();
  });

  it("is hidden when no desktop_script is configured", async () => {
    server.use(http.get("*/api/desktop", () => HttpResponse.json({ available: false, mode: "", modes: ["browse", "wallpaper", "blank"] })));
    renderApp("/");
    await new Promise((r) => setTimeout(r, 50)); // let the /desktop answer land
    await waitFor(() => expect(screen.queryByRole("group", { name: "Desktop mode" })).toBeNull());
  });

  it("is hidden in readout", () => {
    setState({ mode: "readout" });
    renderApp("/");
    expect(screen.queryByRole("group", { name: "Desktop mode" })).toBeNull();
  });
});
