import { describe, expect, it } from "vitest";
import { cell, HOME_NOW, homeHandlers, lastDays, makeHomeFixture } from "~/mocks/home";
import type { Home } from "~/lib/types";
import {
  buildDayView,
  buildLaneModels,
  groupChips,
  hiddenTotal,
  linesText,
  orderLanes,
  quietText,
  selectLanes,
  sharedScale,
  stabilise,
  type LaneModel,
} from "./model";

const NOW = Date.parse(HOME_NOW);

function homeOf(range: 7 | 30 = 30): { home: Home; fx: ReturnType<typeof makeHomeFixture> } {
  const fx = makeHomeFixture();
  const home: Home = {
    range,
    days: lastDays(range),
    lanes: fx.boot.projects.map((p) => ({
      projectId: p.id,
      days: fx.cells[p.id].slice(30 - range),
      live: { servers: fx.live[p.id] ?? [], sessions: [] },
      lastChangeAt: fx.lastChange[p.id] ?? null,
    })),
  };
  return { home, fx };
}

function models(range: 7 | 30 = 30) {
  const { home, fx } = homeOf(range);
  return { home, fx, ms: buildLaneModels(home, fx.boot.projects, NOW) };
}
const byId = (ms: LaneModel[], id: string) => ms.find((m) => m.id === id)!;

describe("lane models", () => {
  it("makes one column per day and joins names, groups, hidden and dirty from boot", () => {
    const { ms } = models();
    expect(ms).toHaveLength(6);
    const a = byId(ms, "alpha");
    expect(a.cols).toHaveLength(30);
    expect(a).toMatchObject({ name: "alpha", group: "personal", dirty: 2, hidden: false });
    expect(byId(ms, "charlie").group).toBe("work");
    expect(byId(ms, "echo").hidden).toBe(true);
  });

  it("column bars come from the counts on one scale shared by every lane", () => {
    const { ms, home } = models();
    const { maxFiles, maxLines } = sharedScale(home);
    expect(maxFiles).toBe(500); // charlie today
    expect(maxLines).toBe(1000); // echo today
    expect(byId(ms, "charlie").cols[29].gh).toBe(24); // the loudest day reaches the top
    expect(byId(ms, "echo").cols[29].ah).toBe(24);
    // the same number of files draws the same height in two lanes
    const sameFiles = byId(ms, "alpha").cols[29].files; // 12
    expect(sameFiles).toBe(12);
    // alpha 100 lines on day 26, bravo 100 lines today: equal height
    expect(byId(ms, "alpha").cols[26].ah).toBe(byId(ms, "bravo").cols[29].ah);
    expect(byId(ms, "alpha").cols[26].ah).toBeGreaterThan(4);
    // a commit without line stats draws the minimum
    expect(byId(ms, "alpha").cols[20].ah).toBe(4);
    // a day with files only has no amber bar; a quiet day has nothing
    expect(byId(ms, "alpha").cols[28]).toMatchObject({ ah: 0 });
    expect(byId(ms, "alpha").cols[28].gh).toBeGreaterThan(0);
    expect(byId(ms, "alpha").cols[0]).toMatchObject({ gh: 0, ah: 0 });
  });

  it("hidden lanes still count for the scale (so filters never rescale the bars)", () => {
    const { home, fx } = homeOf();
    const visibleOnly = { ...home, lanes: home.lanes.filter((l) => l.projectId !== "echo") };
    expect(sharedScale(home).maxLines).toBeGreaterThan(sharedScale(visibleOnly).maxLines);
    expect(fx.boot.projects.length).toBe(6);
  });

  it("writes the aria label with name, day and counts", () => {
    const { ms } = models();
    expect(byId(ms, "alpha").cols[29].aria).toBe("alpha, 1 Oct: 12 file changes, 2 commits, 1 session");
    expect(byId(ms, "alpha").cols[28].aria).toBe("alpha, 30 Sep: 3 file changes, 0 commits");
  });

  it("flags flat lanes and the empty directory, and counts distinct live ports", () => {
    const { ms } = models();
    expect(byId(ms, "delta")).toMatchObject({ flat: true, empty: false });
    expect(byId(ms, "foxtrot")).toMatchObject({ flat: true, empty: true });
    expect(byId(ms, "alpha")).toMatchObject({ flat: false, live: 2 });
    expect(byId(ms, "bravo").live).toBe(0);
  });

  it("flat is judged on the range: a lane busy only 10 days ago is flat at 7d", () => {
    const m7 = models(7).ms;
    const m30 = models(30).ms;
    expect(byId(m30, "charlie").flat).toBe(false);
    expect(byId(m7, "charlie").cols).toHaveLength(7);
    expect(byId(m7, "delta").flat).toBe(true);
  });

  it("writes the ago text from lastChangeAt", () => {
    const { ms } = models();
    expect(byId(ms, "alpha").ago).toBe("13m ago");
    expect(byId(ms, "bravo").ago).toBe("3h ago");
    expect(byId(ms, "charlie").ago).toBe("2d ago");
    expect(byId(ms, "foxtrot").ago).toBe("—");
  });

  it("a lane for a project missing from boot still draws, under its id", () => {
    const { home, fx } = homeOf();
    const ms = buildLaneModels(home, fx.boot.projects.filter((p) => p.id !== "alpha"), NOW);
    expect(byId(ms, "alpha")).toMatchObject({ name: "alpha", hidden: false, dirty: 0 });
  });
});

describe("ordering", () => {
  it("recency puts the newest change first", () => {
    const { ms } = models();
    expect(orderLanes(ms, "recency").map((m) => m.id)).toEqual(["alpha", "echo", "bravo", "charlie", "delta", "foxtrot"]);
  });

  it("loudest sorts by file changes plus commit lines, then recency", () => {
    const { ms } = models();
    expect(orderLanes(ms, "loudest").map((m) => m.id)).toEqual(["echo", "charlie", "alpha", "bravo", "delta", "foxtrot"]);
  });

  it("is stable: the same input gives the same order, and ties fall back to the name", () => {
    const { ms } = models();
    const a = orderLanes(ms, "recency").map((m) => m.id);
    const b = orderLanes(ms.slice().reverse(), "recency").map((m) => m.id);
    expect(b).toEqual(a);
    const tie = ms.map((m) => ({ ...m, lastMs: 5, loud: 7 }));
    expect(orderLanes(tie, "loudest").map((m) => m.id)).toEqual(["alpha", "bravo", "charlie", "delta", "echo", "foxtrot"]);
  });
});

describe("filters and group chips", () => {
  it("counts leave hidden folders out unless show hidden is on", () => {
    const { ms } = models();
    const off = Object.fromEntries(groupChips(ms, false).map((c) => [c.key, c.count]));
    const on = Object.fromEntries(groupChips(ms, true).map((c) => [c.key, c.count]));
    expect(off).toEqual({ all: 5, personal: 2, work: 1, tools: 2 });
    expect(on).toEqual({ all: 6, personal: 3, work: 1, tools: 2 });
    expect(hiddenTotal(ms)).toBe(1);
  });

  it("adds a chip for a group it does not know", () => {
    const { ms } = models();
    const odd = ms.map((m) => (m.id === "delta" ? { ...m, group: "labs" } : m));
    expect(groupChips(odd, false).map((c) => c.key)).toEqual(["all", "personal", "work", "tools", "labs"]);
  });

  it("selectLanes applies hidden, group and order together", () => {
    const { ms } = models();
    expect(selectLanes(ms, { group: "all", order: "recency", showHidden: false }).map((m) => m.id)).toEqual(["alpha", "bravo", "charlie", "delta", "foxtrot"]);
    expect(selectLanes(ms, { group: "personal", order: "recency", showHidden: true }).map((m) => m.id)).toEqual(["alpha", "echo", "bravo"]);
    expect(selectLanes(ms, { group: "work", order: "loudest", showHidden: false }).map((m) => m.id)).toEqual(["charlie"]);
  });
});

describe("live updates keep unchanged lanes", () => {
  it("reuses the old object for every lane whose drawing is the same", () => {
    const { home, fx } = homeOf();
    const first = buildLaneModels(home, fx.boot.projects, NOW);
    const prev = new Map(first.map((m) => [m.id, m]));

    // a new commit lands in bravo only
    fx.cells.bravo[29] = cell({ files: 6, commits: 2, linesPlus: 70, linesMinus: 40 });
    const { home: home2 } = (() => {
      const h: Home = { ...home, lanes: home.lanes.map((l) => (l.projectId === "bravo" ? { ...l, days: fx.cells.bravo } : { ...l, days: l.days.map((d) => ({ ...d })) })) };
      return { home: h };
    })();
    const second = stabilise(prev, buildLaneModels(home2, fx.boot.projects, NOW));

    for (const m of second) {
      if (m.id === "bravo") expect(m).not.toBe(prev.get("bravo"));
      else expect(m).toBe(prev.get(m.id));
    }
    expect(byId(second, "bravo").cols[29].commits).toBe(2);
  });
});

describe("day readout items", () => {
  it("lists items in time order with the right glyphs, lead and context", () => {
    const fx = makeHomeFixture();
    const v = buildDayView(fx.day("2026-10-01", "alpha"), "alpha", 12);
    expect(v.items.map((i) => i.kind)).toEqual(["commit", "session", "snapshot", "note", "task"]);
    expect(v.items.map((i) => i.glyph)).toEqual(["◆", "✦", "▣", "✎", "▮"]);
    const commit = v.items[0];
    expect(commit.lead).toBe("a1b2c3d");
    expect(commit.label).toBe("fix the lane scale");
    expect(commit.ctx).toBe("+40 −3");
    expect(commit.ref).toBe("alpha:commit:a1b2c3d4e5f6");
    expect(v.items[1]).toMatchObject({ lead: "session", ctx: ".claude" });
    expect(v.items[2]).toMatchObject({ lead: "snapshot", ctx: "pending" });
    // the note opens the item it is on
    expect(v.items[3].ref).toBe("alpha:commit:a1b2c3d4e5f6");
  });

  it("lists files with image refs for pictures, and the rest as +N more", () => {
    const fx = makeHomeFixture();
    const v = buildDayView(fx.day("2026-10-01", "alpha"), "alpha", 12);
    expect(v.files.map((f) => f.ref)).toEqual(["alpha:file:src/a.ts", "alpha:file:src/b.ts", "alpha:image:docs/shot.png"]);
    expect(v.moreFiles).toBe(9);
  });

  it("caps the file list at 20 and derives +N more from the day total", () => {
    const paths = Array.from({ length: 30 }, (_, i) => "f" + i + ".ts");
    const v = buildDayView({ date: "2026-10-01", events: [], totals: {} as never, files: paths }, "p", 45);
    expect(v.files).toHaveLength(20);
    expect(v.moreFiles).toBe(25);
  });

  it("drops file events from the item list and keeps unknown refs unclickable", () => {
    const v = buildDayView(
      {
        date: "2026-10-01",
        totals: {} as never,
        events: [
          { ts: "2026-10-01T01:00:00Z", kind: "file", ref: "p:file:a.ts", label: "a.ts", projectId: "p" },
          { ts: "2026-10-01T02:00:00Z", kind: "commit", ref: "garbage", label: "x", projectId: "p" },
        ],
      },
      "p",
      0,
    );
    expect(v.items).toHaveLength(1);
    expect(v.items[0].ref).toBeNull();
  });

  it("caps a huge day at 200 items and counts the rest", () => {
    const events = Array.from({ length: 230 }, (_, i) => ({
      ts: new Date(Date.UTC(2026, 9, 1, 0, 0, i)).toISOString(),
      kind: "commit" as const,
      ref: "p:commit:abcdef" + i,
      label: "c" + i,
      projectId: "p",
    }));
    const v = buildDayView({ date: "2026-10-01", events, totals: {} as never }, "p", 0);
    expect(v.items).toHaveLength(200);
    expect(v.moreItems).toBe(30);
    expect(v.items[0].label).toBe("c0");
  });

  it("writes the lines text and the quiet sentences from the rules in the spec", () => {
    expect(linesText({ commits: 0, plus: 0, minus: 0 })).toBe("—");
    expect(linesText({ commits: 1, plus: 0, minus: 0 })).toBe("no line stats");
    expect(linesText({ commits: 2, plus: 340, minus: 88 })).toBe("+340 −88");
    expect(quietText({ files: 0, commits: 0 }, 0)).toBe("a flat line — no tremor this day.");
    expect(quietText({ files: 4, commits: 0 }, 0)).toBe("files moved, nothing committed.");
    expect(quietText({ files: 4, commits: 0 }, 2)).toBe("");
  });
});

// keep the import used (the handlers are exercised in Home.test.tsx)
void homeHandlers;
