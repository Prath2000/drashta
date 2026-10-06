import { act, screen, waitFor, within } from "@testing-library/react";
import { useState } from "react";
import { http, HttpResponse } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { applyEvent } from "~/lib/events";
import { useShortcuts } from "~/lib/shortcuts";
import { getState, setSearch, setSearchLit, setState } from "~/lib/store";
import { server } from "~/mocks/node";
import { cell, HOME_NOW, homeHandlers, makeHomeFixture, type HomeFixture } from "~/mocks/home";
import { renderWithRouter } from "./testutil";
import { fileBarH } from "./geometry";
import { Home } from "./Home";
import { Lane } from "./Lane";
import { buildLaneModels } from "./model";
import { PREFS_KEY } from "./prefs";

beforeEach(() => {
  // fix the clock so "13m ago" is the same on every machine (only Date is faked)
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date(HOME_NOW));
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

// The shell installs the one global key listener; tests that press Esc mount it too.
function Keys() {
  useShortcuts();
  return null;
}

function setup(path = "/", fx: HomeFixture = makeHomeFixture(), keys = false) {
  server.use(...homeHandlers(fx));
  const r = renderWithRouter(
    keys ? (
      <>
        <Home />
        <Keys />
      </>
    ) : (
      <Home />
    ),
    path,
  );
  return { fx, ...r };
}

async function ready() {
  await screen.findByTestId("lane-alpha");
}

const laneIds = () => Array.from(document.querySelectorAll("[data-lane]")).map((e) => e.getAttribute("data-lane"));
const lane = (id: string) => screen.getByTestId("lane-" + id);
const col = (name: string) => screen.getByRole("button", { name });
const colsOf = (id: string) => lane(id).querySelectorAll("button[data-i]");
const barH = (el: HTMLElement, which: "f" | "c") => (el.querySelector(".hm-bar." + which) as HTMLElement).style.height;
const search = (router: { state: { location: { search: string } } }) => decodeURIComponent(router.state.location.search);

describe("lanes", () => {
  it("draws one lane per visible project, newest first, with hidden folders out", async () => {
    setup();
    await ready();
    expect(laneIds()).toEqual(["alpha", "bravo", "charlie", "delta", "foxtrot"]);
    expect(screen.getByRole("region", { name: "Seismograph, every project, last 30 days" })).toBeInTheDocument();
  });

  it("has a full-size root, a flexible lanes region and the readout panel", async () => {
    // jsdom has no layout, so this checks the structure that gives the heights. The real
    // pixels (root = mount box, lanes region > 0, sticky axis) were measured in a headless
    // browser; see the report.
    setup();
    await ready();
    const root = screen.getByTestId("home-root");
    expect(root).toHaveClass("hm-root");
    expect(screen.getByTestId("home-lanes")).toHaveClass("hm-lanes");
    expect(within(root).getByRole("region", { name: "Day readout" })).toBeInTheDocument();
  });

  it("shows identity: name, group tag, uncommitted count, ago, live servers", async () => {
    setup();
    await ready();
    const a = within(lane("alpha"));
    expect(a.getByRole("button", { name: "Open alpha project page" })).toHaveTextContent("alpha");
    expect(a.getByText("personal")).toBeInTheDocument();
    expect(a.getByLabelText("2 uncommitted paths")).toHaveTextContent("±2");
    expect(a.getByText("13m ago")).toBeInTheDocument();
    // three entries in the list but two distinct ports
    expect(a.getByLabelText("2 servers live")).toHaveTextContent("● 2");
    expect(within(lane("charlie")).getByText("work")).toBeInTheDocument();
    expect(within(lane("bravo")).getByLabelText("no servers live")).toHaveTextContent("○");
    expect(within(lane("bravo")).queryByLabelText(/uncommitted/)).toBeNull();
    expect(within(lane("bravo")).getByText("3h ago")).toBeInTheDocument();
  });

  it("shows the flat and empty-directory notes", async () => {
    setup();
    await ready();
    expect(within(lane("delta")).getByText("flat — no tremor in 30d")).toBeInTheDocument();
    expect(within(lane("foxtrot")).getByText("empty directory")).toBeInTheDocument();
    expect(within(lane("alpha")).queryByText(/flat/)).toBeNull();
  });

  it("draws bars from the counts, on one scale shared by every lane", async () => {
    setup();
    await ready();
    const alphaToday = col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session");
    expect(barH(alphaToday, "f")).toBe(fileBarH(12, 500) + "px");
    // 500 files is the biggest day of all lanes: it reaches the full 24px
    expect(barH(col("charlie, 1 Oct: 500 file changes, 0 commits"), "f")).toBe("24px");
    // 100 lines in alpha (3 days ago) and 100 lines in bravo (today) draw the same amber bar
    const a = barH(col("alpha, 28 Sep: 1 file change, 1 commit"), "c");
    const b = barH(col("bravo, 1 Oct: 5 file changes, 1 commit"), "c");
    expect(a).toBe(b);
    expect(parseInt(a)).toBeGreaterThan(4);
    // no line stats: the 4px minimum; no commits: a 0px bar
    expect(barH(col("alpha, 22 Sep: 0 file changes, 1 commit"), "c")).toBe("4px");
    expect(barH(col("alpha, 30 Sep: 3 file changes, 0 commits"), "c")).toBe("0px");
  });

  it("marks the days with a Claude session by a violet tick", async () => {
    setup();
    await ready();
    expect(col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session").querySelector(".hm-sess")).not.toBeNull();
    expect(col("alpha, 30 Sep: 3 file changes, 0 commits").querySelector(".hm-sess")).toBeNull();
    expect(lane("bravo").querySelectorAll(".hm-sess")).toHaveLength(1);
  });

  it("has one tab stop per lane (roving tabindex), on today until a day is chosen", async () => {
    const { user } = setup();
    await ready();
    for (const id of laneIds()) {
      const stops = Array.from(colsOf(id!)).filter((b) => b.getAttribute("tabindex") === "0");
      expect(stops).toHaveLength(1);
      expect(stops[0].getAttribute("data-i")).toBe("29");
    }
    await user.click(col("alpha, 28 Sep: 1 file change, 1 commit"));
    const stop = Array.from(colsOf("alpha")).filter((b) => b.getAttribute("tabindex") === "0");
    expect(stop).toHaveLength(1);
    expect(stop[0].getAttribute("data-i")).toBe("26");
  });

  it("labels every lane group and hides the axis from screen readers", async () => {
    setup();
    await ready();
    expect(screen.getByRole("group", { name: "alpha, last 30 days" })).toBeInTheDocument();
    expect(screen.getByTestId("home-axis")).toHaveAttribute("aria-hidden", "true");
    expect(screen.getByTestId("home-axis")).toHaveTextContent("today");
  });

  it("goes to the project page from the name", async () => {
    const { router, user } = setup();
    await ready();
    await user.click(within(lane("bravo")).getByRole("button", { name: "Open bravo project page" }));
    expect(router.state.location.pathname).toBe("/p/bravo");
    expect(getState().lastProjectId).toBe("bravo");
  });

  it("says so when the group has no lanes, and when the call fails", async () => {
    const fx = makeHomeFixture();
    // Only hidden folders in "tools": the chip stays (they exist) but shows no lanes.
    fx.boot.projects = fx.boot.projects.map((p) => (p.group === "tools" ? { ...p, hidden: true } : p));
    const { user } = setup("/", fx);
    await ready();
    await user.click(screen.getByRole("button", { name: "tools 0" }));
    expect(screen.getByText("no projects in this group.")).toBeInTheDocument();
  });

  it("shows the first-run notice from /boot when there are no projects", async () => {
    const fx = makeHomeFixture();
    fx.boot.projects = [];
    (fx.boot as { notice?: string }).notice = "no projects found under /x; set projects_dir or roots in /c/config.toml";
    setup("/", fx);
    expect(await screen.findByText(/no projects found under \/x/)).toBeInTheDocument();
  });

  it("shows a calm error with a retry when /home fails", async () => {
    server.use(http.get("*/api/home", () => HttpResponse.json({ error: { code: "boom", message: "x" } }, { status: 500 })));
    const fx = makeHomeFixture();
    server.use(http.get("*/api/boot", () => HttpResponse.json(fx.boot)));
    const { user } = renderWithRouter(<Home />);
    expect(await screen.findByText("could not read the seismograph.")).toBeInTheDocument();
    server.use(...homeHandlers(fx));
    await user.click(screen.getByRole("button", { name: "try again" }));
    await ready();
  });
});

describe("long lists", () => {
  it("caps a very long lane list at 200 until you ask for all of it", async () => {
    const fx = makeHomeFixture();
    for (let i = 0; i < 250; i++) {
      const id = "p" + String(i).padStart(3, "0");
      fx.boot.projects.push({ id, name: id, group: "tools", path: "~/projects/tools/" + id, vcs: ["git"], branch: "main", lastChangeAt: null, hidden: false, surveyed: true });
      fx.cells[id] = Array.from({ length: 30 }, () => cell());
    }
    const { user } = setup("/", fx);
    await ready();
    expect(laneIds()).toHaveLength(200);
    expect(screen.getByTestId("home-cap")).toHaveTextContent("showing 200 of 255 projects");
    await user.click(screen.getByRole("button", { name: "show all 255" }));
    expect(laneIds()).toHaveLength(255);
    expect(screen.queryByTestId("home-cap")).toBeNull();
  }, 30_000);

  it("does not cap 200 lanes or fewer", async () => {
    setup();
    await ready();
    expect(screen.queryByTestId("home-cap")).toBeNull();
  });
});

describe("controls", () => {
  it("shows group chips with counts that leave hidden folders out", async () => {
    setup();
    await ready();
    for (const t of ["all 5", "personal 2", "work 1", "tools 2", "○ show hidden (1)"]) {
      expect(screen.getByRole("button", { name: t })).toBeInTheDocument();
    }
    expect(screen.getByRole("button", { name: "all 5" })).toHaveAttribute("aria-pressed", "true");
  });

  it("filters to one group", async () => {
    const { user } = setup();
    await ready();
    await user.click(screen.getByRole("button", { name: "tools 2" }));
    expect(laneIds()).toEqual(["delta", "foxtrot"]);
    expect(screen.getByRole("button", { name: "tools 2" })).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByRole("button", { name: "all 5" }));
    expect(laneIds()).toHaveLength(5);
  });

  it("show hidden adds the hidden lane at 60% with a HIDDEN tag and counts it", async () => {
    const { user } = setup();
    await ready();
    expect(within(lane("alpha")).queryByText("Hidden")).toBeNull();
    await user.click(screen.getByRole("button", { name: "○ show hidden (1)" }));
    expect(laneIds()).toEqual(["alpha", "echo", "bravo", "charlie", "delta", "foxtrot"]);
    expect(within(lane("echo")).getByText("Hidden")).toBeInTheDocument();
    expect(lane("echo")).toHaveClass("hidden-lane");
    expect(screen.getByRole("button", { name: "◉ show hidden (1)" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "all 6" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "personal 3" })).toBeInTheDocument();
  });

  it("orders by recency or loudest, and stays put on hover", async () => {
    const { user } = setup();
    await ready();
    expect(screen.getByRole("button", { name: "recency" })).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByRole("button", { name: "loudest" }));
    expect(laneIds()).toEqual(["charlie", "alpha", "bravo", "delta", "foxtrot"]);
    await user.hover(col("bravo, 1 Oct: 5 file changes, 1 commit"));
    await user.hover(lane("delta"));
    expect(laneIds()).toEqual(["charlie", "alpha", "bravo", "delta", "foxtrot"]);
    await user.click(screen.getByRole("button", { name: "recency" }));
    expect(laneIds()).toEqual(["alpha", "bravo", "charlie", "delta", "foxtrot"]);
  });

  it("keeps the order when the data is fetched again", async () => {
    const { fx } = setup();
    await ready();
    const before = laneIds();
    await act(async () => {
      applyEvent({ type: "resync", data: {} });
    });
    await waitFor(() => expect(fx.calls.home.length).toBeGreaterThan(1));
    expect(laneIds()).toEqual(before);
  });

  it("switches the range: fetches /home?range=7, draws 7 columns, keeps the choice", async () => {
    const { fx, user } = setup();
    await ready();
    expect(colsOf("alpha")).toHaveLength(30);
    expect(screen.getByRole("button", { name: "30d" })).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByRole("button", { name: "7d" }));
    await waitFor(() => expect(colsOf("alpha")).toHaveLength(7));
    expect(fx.calls.home.some((u) => u.includes("range=7"))).toBe(true);
    expect(getState().range).toBe(7);
    expect(localStorage.getItem("drashta.range")).toBe("7");
    expect(screen.getByRole("region", { name: "Seismograph, every project, last 7 days" })).toBeInTheDocument();
    expect(within(lane("delta")).getByText("flat — no tremor in 7d")).toBeInTheDocument();
    // at 7d every column has an axis label, the last one reads today
    expect(screen.getByTestId("home-axis").querySelectorAll(".lbl")).toHaveLength(7);
    // charlie only had activity 5 and 20 days ago: it is flat at 7d except today (500 files)
    expect(within(lane("charlie")).queryByText(/flat/)).toBeNull();
  });

  it("saves group, order and show hidden, and restores them on the next visit", async () => {
    const first = setup();
    await ready();
    await first.user.click(screen.getByRole("button", { name: "personal 2" }));
    await first.user.click(screen.getByRole("button", { name: "loudest" }));
    await first.user.click(screen.getByRole("button", { name: "○ show hidden (1)" }));
    expect(JSON.parse(localStorage.getItem(PREFS_KEY)!)).toEqual({ group: "personal", order: "loudest", showHidden: true });
    first.unmount();

    setup();
    await ready();
    expect(laneIds()).toEqual(["echo", "alpha", "bravo"]);
    expect(screen.getByRole("button", { name: "personal 3" })).toHaveAttribute("aria-pressed", "true");
  });

  it("works when storage throws", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const { user } = setup();
    await ready();
    await user.click(screen.getByRole("button", { name: "loudest" }));
    expect(laneIds()[0]).toBe("charlie");
    vi.restoreAllMocks();
  });

  it("falls back to all when the saved group is not a chip", async () => {
    localStorage.setItem(PREFS_KEY, JSON.stringify({ group: "nonsense", order: "recency", showHidden: false }));
    setup();
    await ready();
    expect(laneIds()).toHaveLength(5);
    expect(screen.getByRole("button", { name: "all 5" })).toHaveAttribute("aria-pressed", "true");
  });
});

describe("day readout", () => {
  it("shows the legend and the hint until a day is chosen", async () => {
    setup();
    await ready();
    const legend = screen.getByTestId("home-legend");
    expect(legend).toHaveTextContent("click any day in a lane to read its tremor here.");
    for (const t of ["file changes that day", "commits (height = lines changed, log scale)", "claude session that day", "servers live now", "uncommitted paths", "name → project page"]) {
      expect(legend).toHaveTextContent(t);
    }
  });

  it("asks /day for the selected day and project, then lists counts and items in time order", async () => {
    const { fx, user } = setup();
    await ready();
    await user.click(col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session"));
    expect(col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session")).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByTestId("readout-date")).toHaveTextContent("1 Oct");
    expect(screen.getByRole("region", { name: "Day readout" })).toHaveTextContent("alpha →");
    const counts = screen.getByTestId("readout-counts");
    expect(counts).toHaveTextContent("file changes12");
    expect(counts).toHaveTextContent("commits2");
    expect(counts).toHaveTextContent("lines+300 −40");
    expect(counts).toHaveTextContent("sessions1");
    expect(counts).toHaveTextContent("snapshots1");
    expect(counts).toHaveTextContent("notes2");

    const items = screen.getByTestId("readout-items");
    await within(items).findByText("fix the lane scale");
    expect(fx.calls.day).toHaveLength(1);
    const u = new URL(fx.calls.day[0]);
    expect(u.searchParams.get("date")).toBe("2026-10-01");
    expect(u.searchParams.get("project")).toBe("alpha");

    const rows = Array.from(items.querySelectorAll("button.hm-item")).map((b) => b.querySelector(".lead")!.textContent);
    expect(rows).toEqual(["a1b2c3d", "session", "snapshot", "note", "task"]);
    expect(items.querySelector("button.hm-item .g")!.textContent).toBe("◆");
    // files, then the rest as +N more
    expect(within(items).getByText("files changed · 12")).toBeInTheDocument();
    expect(within(items).getByText("src/a.ts")).toBeInTheDocument();
    expect(within(items).getByText("+9 more")).toBeInTheDocument();
    // the legend is gone
    expect(screen.queryByTestId("home-legend")).toBeNull();
  });

  it("opens an item in the viewport through ?item=", async () => {
    const { router, user } = setup();
    await ready();
    await user.click(col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session"));
    const items = screen.getByTestId("readout-items");
    await user.click(await within(items).findByRole("button", { name: /^commit \d\d:\d\d fix the lane scale$/ }));
    expect(search(router)).toBe("?item=alpha:commit:a1b2c3d4e5f6");
    await user.click(within(items).getByRole("button", { name: /^session .* rework the readout$/ }));
    expect(search(router)).toBe("?item=alpha:session:s-1");
    await user.click(within(items).getByRole("button", { name: /^snapshot / }));
    expect(search(router)).toBe("?item=alpha:snapshot:snap-1");
    await user.click(within(items).getByRole("button", { name: /^note / }));
    expect(search(router)).toBe("?item=alpha:commit:a1b2c3d4e5f6");
    await user.click(within(items).getByRole("button", { name: /^task / }));
    expect(search(router)).toBe("?item=alpha:task:T-1");
    await user.click(within(items).getByRole("button", { name: "file src/b.ts" }));
    expect(search(router)).toBe("?item=alpha:file:src/b.ts");
    await user.click(within(items).getByRole("button", { name: "file docs/shot.png" }));
    expect(search(router)).toBe("?item=alpha:image:docs/shot.png");
    // the page itself does not move
    expect(router.state.location.pathname).toBe("/");
  });

  it("links to the project page from the readout", async () => {
    const { router, user } = setup();
    await ready();
    await user.click(col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session"));
    const panel = screen.getByRole("region", { name: "Day readout" });
    await user.click(within(panel).getByRole("button", { name: "Open alpha project page" }));
    expect(router.state.location.pathname).toBe("/p/alpha");
  });

  it("clears on a second click on the same day and on Esc", async () => {
    const { user } = setup("/", makeHomeFixture(), true);
    await ready();
    const today = "alpha, 1 Oct: 12 file changes, 2 commits, 1 session";
    await user.click(col(today));
    expect(screen.queryByTestId("home-legend")).toBeNull();
    await user.click(col(today));
    expect(screen.getByTestId("home-legend")).toBeInTheDocument();
    expect(col(today)).toHaveAttribute("aria-pressed", "false");

    await user.click(col(today));
    expect(screen.queryByTestId("home-legend")).toBeNull();
    await user.keyboard("{Escape}");
    expect(screen.getByTestId("home-legend")).toBeInTheDocument();
  });

  it("moves the selection to another lane's day with one click", async () => {
    const { user } = setup();
    await ready();
    await user.click(col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session"));
    await user.click(col("bravo, 1 Oct: 5 file changes, 1 commit"));
    expect(col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session")).toHaveAttribute("aria-pressed", "false");
    expect(col("bravo, 1 Oct: 5 file changes, 1 commit")).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("region", { name: "Day readout" })).toHaveTextContent("bravo →");
  });

  it("says what a quiet day was", async () => {
    const { user } = setup();
    await ready();
    await user.click(col("charlie, 1 Oct: 500 file changes, 0 commits"));
    expect(await screen.findByText("files moved, nothing committed.")).toBeInTheDocument();
    expect(screen.getByText(/500 file changes — paths are not listed/)).toBeInTheDocument();
    await user.click(col("delta, 1 Oct: 0 file changes, 0 commits"));
    expect(await screen.findByText("a flat line — no tremor this day.")).toBeInTheDocument();
  });

  it("shows a quiet line when /day fails, and the counts still show", async () => {
    const fx = makeHomeFixture();
    const { user } = setup("/", fx);
    server.use(http.get("*/api/day", () => HttpResponse.json({ error: { code: "boom", message: "x" } }, { status: 500 })));
    await ready();
    await user.click(col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session"));
    expect(await screen.findByText("could not read this day.")).toBeInTheDocument();
    expect(screen.getByTestId("readout-counts")).toHaveTextContent("file changes12");
  });

  it("clears a selected day that falls outside a shorter range", async () => {
    const { user } = setup();
    await ready();
    await user.click(col("alpha, 22 Sep: 0 file changes, 1 commit")); // 10 days ago
    expect(screen.queryByTestId("home-legend")).toBeNull();
    await user.click(screen.getByRole("button", { name: "7d" }));
    await waitFor(() => expect(colsOf("alpha")).toHaveLength(7));
    expect(screen.getByTestId("home-legend")).toBeInTheDocument();
  });

  it("keeps a selected day that is still inside the shorter range", async () => {
    const { user } = setup();
    await ready();
    await user.click(col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session"));
    await user.click(screen.getByRole("button", { name: "7d" }));
    await waitFor(() => expect(colsOf("alpha")).toHaveLength(7));
    expect(screen.getByTestId("readout-date")).toHaveTextContent("1 Oct");
  });

  it("clears the selection when its lane is filtered away", async () => {
    const { user } = setup();
    await ready();
    await user.click(col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session"));
    await user.click(screen.getByRole("button", { name: "work 1" }));
    expect(screen.getByTestId("home-legend")).toBeInTheDocument();
  });
});

describe("keyboard", () => {
  it("arrow keys pick the neighbouring day and move focus; Home and End jump", async () => {
    const { user } = setup();
    await ready();
    const stop = Array.from(colsOf("alpha")).find((b) => b.getAttribute("tabindex") === "0") as HTMLElement;
    stop.focus();
    await user.keyboard("{ArrowLeft}");
    expect(screen.getByTestId("readout-date")).toHaveTextContent("30 Sep");
    expect(document.activeElement).toHaveAttribute("data-i", "28");
    await user.keyboard("{ArrowLeft}{ArrowLeft}");
    expect(screen.getByTestId("readout-date")).toHaveTextContent("28 Sep");
    await user.keyboard("{ArrowRight}");
    expect(screen.getByTestId("readout-date")).toHaveTextContent("29 Sep");
    await user.keyboard("{Home}");
    expect(screen.getByTestId("readout-date")).toHaveTextContent("2 Sep");
    expect(document.activeElement).toHaveAttribute("data-i", "0");
    await user.keyboard("{ArrowLeft}"); // already at the first day
    expect(document.activeElement).toHaveAttribute("data-i", "0");
    await user.keyboard("{End}");
    expect(screen.getByTestId("readout-date")).toHaveTextContent("1 Oct");
    await user.keyboard("{ArrowRight}"); // already at today
    expect(screen.getByTestId("readout-date")).toHaveTextContent("1 Oct");
  });

  it("Enter and Space read the day and never close it", async () => {
    const { user } = setup();
    await ready();
    const stop = Array.from(colsOf("bravo")).find((b) => b.getAttribute("tabindex") === "0") as HTMLElement;
    stop.focus();
    await user.keyboard("{Enter}");
    expect(screen.getByRole("region", { name: "Day readout" })).toHaveTextContent("bravo →");
    await user.keyboard("{Enter}");
    expect(screen.getByRole("region", { name: "Day readout" })).toHaveTextContent("bravo →");
    await user.keyboard(" ");
    expect(screen.getByRole("region", { name: "Day readout" })).toHaveTextContent("bravo →");
  });

  it("up and down move focus to the same day in the next lane, and take the selection along", async () => {
    const { user } = setup();
    await ready();
    const stop = Array.from(colsOf("alpha")).find((b) => b.getAttribute("tabindex") === "0") as HTMLElement;
    stop.focus();
    await user.keyboard("{ArrowDown}"); // nothing selected: focus only
    expect(document.activeElement).toBe(colsOf("bravo")[29]);
    expect(screen.getByTestId("home-legend")).toBeInTheDocument();
    await user.keyboard("{Enter}");
    await user.keyboard("{ArrowDown}"); // selected: the selection follows
    expect(document.activeElement).toBe(colsOf("charlie")[29]);
    expect(screen.getByRole("region", { name: "Day readout" })).toHaveTextContent("charlie →");
    await user.keyboard("{ArrowUp}{ArrowUp}");
    expect(document.activeElement).toBe(colsOf("alpha")[29]);
    await user.keyboard("{ArrowUp}"); // first lane: nothing happens
    expect(document.activeElement).toBe(colsOf("alpha")[29]);
  });
});

describe("search dim", () => {
  it("lights the lanes that match and dims the rest, then restores them", async () => {
    setup();
    await ready();
    act(() => {
      setSearch("alpha");
      setSearchLit(["alpha"]);
    });
    expect(lane("alpha")).toHaveClass("match");
    expect(within(lane("alpha")).getByRole("button", { name: "Open alpha project page" })).toHaveClass("match");
    expect(lane("bravo")).toHaveClass("dim");
    expect(lane("alpha")).not.toHaveClass("dim");
    act(() => setSearch(""));
    expect(lane("alpha")).not.toHaveClass("match");
    expect(lane("bravo")).not.toHaveClass("dim");
  });
});

describe("live updates", () => {
  it("an activity event refetches /home and only the changed lane is drawn again", async () => {
    const { fx } = setup();
    await ready();
    const before = fx.calls.home.length;
    fx.cells.alpha[29] = cell({ files: 13, commits: 2, linesPlus: 300, linesMinus: 40, sessions: 1, snapshots: 1, notes: 2 });
    await act(async () => {
      applyEvent({ type: "activity", data: { projectId: "alpha", day: "2026-10-01" } });
    });
    await screen.findByRole("button", { name: "alpha, 1 Oct: 13 file changes, 2 commits, 1 session" });
    expect(fx.calls.home.length).toBe(before + 1);
    expect(screen.getByRole("button", { name: "bravo, 1 Oct: 5 file changes, 1 commit" })).toBeInTheDocument();
  });

  it("a servers event updates the live count of the lane", async () => {
    const { fx } = setup();
    await ready();
    expect(within(lane("bravo")).getByLabelText("no servers live")).toBeInTheDocument();
    fx.live.bravo = [9000];
    await act(async () => {
      applyEvent({ type: "servers", data: { projectId: "bravo", active: [9000] } });
    });
    expect(await within(lane("bravo")).findByLabelText("1 server live")).toHaveTextContent("● 1");
  });

  it("a project event updates the uncommitted count from /boot", async () => {
    const { fx } = setup();
    await ready();
    expect(within(lane("bravo")).queryByLabelText(/uncommitted/)).toBeNull();
    fx.boot.projects = fx.boot.projects.map((p) => (p.id === "bravo" ? { ...p, dirtyCount: 3 } : p));
    await act(async () => {
      applyEvent({ type: "project", data: { projectId: "bravo", what: "dirty" } });
    });
    expect(await within(lane("bravo")).findByLabelText("3 uncommitted paths")).toHaveTextContent("±3");
  });

  it("a selected day stays selected and its readout is fetched again after an event", async () => {
    const { fx, user } = setup();
    await ready();
    await user.click(col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session"));
    await screen.findByText("fix the lane scale");
    await act(async () => {
      applyEvent({ type: "activity", data: { projectId: "alpha" } });
    });
    await waitFor(() => expect(fx.calls.day.length).toBeGreaterThan(1));
    expect(screen.getByTestId("readout-date")).toHaveTextContent("1 Oct");
    expect(await screen.findByText("fix the lane scale")).toBeInTheDocument();
  });
});

describe("a lane is memoised", () => {
  it("skips a redraw when its model and props are the same, redraws when the model changes", () => {
    const fx = makeHomeFixture();
    const home = {
      range: 30 as const,
      days: Array.from({ length: 30 }, (_, i) => "2026-09-" + String(i + 1).padStart(2, "0")),
      lanes: [{ projectId: "bravo", days: fx.cells.bravo, live: { servers: [], sessions: [] }, lastChangeAt: null }],
    };
    const m1 = buildLaneModels(home, fx.boot.projects, Date.parse(HOME_NOW))[0];
    // The lane reads `m.name` every time it draws. Counting those reads counts the draws.
    let draws = 0;
    const counted = new Proxy(m1, {
      get(t, k, r) {
        if (k === "name") draws++;
        return Reflect.get(t, k, r);
      },
    });
    const changed = { ...m1, sig: m1.sig + "x", name: "changed" };
    const noop = () => {};
    let setModel: (m: typeof m1) => void = () => {};
    let bump: () => void = () => {};
    function Wrapper() {
      const [m, set] = useState(counted);
      const [n, setN] = useState(0);
      setModel = set;
      bump = () => setN((x) => x + 1);
      return (
        <div data-n={n}>
          <Lane m={m} range={30} interactive selIdx={-1} match={false} dim={false} onOpen={noop} onPick={noop} />
        </div>
      );
    }
    renderWithRouter(<Wrapper />);
    const first = draws;
    expect(first).toBeGreaterThan(0);
    act(() => bump()); // the parent draws again, the lane gets the same props
    expect(draws).toBe(first);
    act(() => setModel(changed));
    expect(screen.getByText("changed")).toBeInTheDocument();
    act(() => setModel(counted));
    expect(draws).toBeGreaterThan(first);
  });
});

// ---------- readout mode ----------
class FakeResizeObserver {
  static instances: FakeResizeObserver[] = [];
  cb: ResizeObserverCallback;
  el: Element | null = null;
  constructor(cb: ResizeObserverCallback) {
    this.cb = cb;
    FakeResizeObserver.instances.push(this);
  }
  observe(el: Element) {
    this.el = el;
  }
  unobserve() {}
  disconnect() {
    FakeResizeObserver.instances = FakeResizeObserver.instances.filter((x) => x !== this);
  }
  fire(height: number) {
    this.cb([{ contentRect: { height } as DOMRectReadOnly, target: this.el! } as ResizeObserverEntry], this as unknown as ResizeObserver);
  }
}

function goReadout() {
  document.documentElement.classList.add("desktop");
  act(() => setState({ mode: "readout" }));
}

describe("readout mode", () => {
  beforeEach(() => {
    FakeResizeObserver.instances = [];
    vi.stubGlobal("ResizeObserver", FakeResizeObserver);
  });

  it("hides the controls, the day buttons and the hint, and shows the legend", async () => {
    setup();
    await ready();
    expect(screen.getByTestId("home-controls")).toBeInTheDocument();
    goReadout();
    expect(screen.queryByTestId("home-controls")).toBeNull();
    expect(document.querySelectorAll("button.hm-col")).toHaveLength(0);
    expect(lane("alpha").querySelectorAll(".hm-col .hm-bar").length).toBeGreaterThan(0);
    expect(screen.getByTestId("home-legend")).toBeInTheDocument();
    expect(screen.queryByText(/click any day/)).toBeNull();
    // names still link, ago and live still show
    expect(within(lane("alpha")).getByText("13m ago")).toBeInTheDocument();
    expect(within(lane("alpha")).getByLabelText("2 servers live")).toBeInTheDocument();
  });

  it("applies the saved filters and order", async () => {
    localStorage.setItem(PREFS_KEY, JSON.stringify({ group: "tools", order: "loudest", showHidden: false }));
    setup();
    await ready().catch(() => {});
    goReadout();
    await waitFor(() => expect(laneIds()).toEqual(["delta", "foxtrot"]));
  });

  it("drops a selected day when it changes to readout mode", async () => {
    const { user } = setup();
    await ready();
    await user.click(col("alpha, 1 Oct: 12 file changes, 2 commits, 1 session"));
    expect(screen.queryByTestId("home-legend")).toBeNull();
    goReadout();
    expect(screen.getByTestId("home-legend")).toBeInTheDocument();
    expect(screen.queryByTestId("readout-date")).toBeNull();
  });

  it("does not dim lanes for a search (the search box is hidden)", async () => {
    setup();
    await ready();
    act(() => {
      setSearch("alpha");
      setSearchLit(["alpha"]);
    });
    goReadout();
    expect(lane("bravo")).not.toHaveClass("dim");
  });

  it("measures its capacity from the container: lanes that do not fit end with +N quieter projects", async () => {
    setup();
    await ready();
    goReadout();
    await waitFor(() => expect(FakeResizeObserver.instances.length).toBe(1));
    // axis 16px + room for 3 rows (3 x 36): 2 lanes and the +N row
    act(() => FakeResizeObserver.instances[0].fire(16 + 3 * 36));
    expect(laneIds()).toEqual(["alpha", "bravo"]);
    expect(screen.getByTestId("home-more")).toHaveTextContent("+3 quieter projects");

    // taller: 4 rows -> 3 lanes + "+2"
    act(() => FakeResizeObserver.instances[0].fire(16 + 4 * 36 + 10));
    expect(laneIds()).toEqual(["alpha", "bravo", "charlie"]);
    expect(screen.getByTestId("home-more")).toHaveTextContent("+2 quieter projects");

    // one pixel short of five rows: still 3 lanes + "+2"
    act(() => FakeResizeObserver.instances[0].fire(16 + 5 * 36 - 1));
    expect(laneIds()).toEqual(["alpha", "bravo", "charlie", "delta"].slice(0, 3));
    expect(screen.getByTestId("home-more")).toHaveTextContent("+2 quieter projects");

    // tall enough for all five: no +N row
    act(() => FakeResizeObserver.instances[0].fire(16 + 5 * 36));
    expect(laneIds()).toHaveLength(5);
    expect(screen.queryByTestId("home-more")).toBeNull();
  });

  it("says '+1 quieter project' in the singular", async () => {
    localStorage.setItem(PREFS_KEY, JSON.stringify({ group: "work", order: "recency", showHidden: false }));
    setup();
    await ready().catch(() => {});
    goReadout();
    await waitFor(() => expect(FakeResizeObserver.instances.length).toBe(1));
    // room for the axis only: the one work lane does not fit
    act(() => FakeResizeObserver.instances[0].fire(20));
    expect(laneIds()).toEqual([]);
    expect(screen.getByTestId("home-more")).toHaveTextContent("+1 quieter project");
    expect(screen.getByTestId("home-more")).not.toHaveTextContent("projects");
  });

  it("stops observing when it leaves readout mode", async () => {
    setup();
    await ready();
    goReadout();
    await waitFor(() => expect(FakeResizeObserver.instances.length).toBe(1));
    act(() => setState({ mode: "browsing" }));
    expect(FakeResizeObserver.instances.length).toBe(0);
  });

  it("shows everything when the height is not known", async () => {
    setup();
    await ready();
    goReadout();
    expect(laneIds()).toHaveLength(5);
    expect(screen.queryByTestId("home-more")).toBeNull();
  });
});

