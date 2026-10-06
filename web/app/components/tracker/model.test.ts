import { describe, expect, it } from "vitest";
import type { Feature, Milestone, Task, TrackerResponse } from "~/lib/types";
import {
  barFor,
  buildModel,
  byStatus,
  daysLabel,
  featureDue,
  leftovers,
  milestoneBlocks,
  relTime,
  stepStatus,
  suggestsDone,
  timelineGeometry,
  DAY,
} from "./model";

const t = (over: Partial<Task>): Task => ({
  id: "T-1",
  projectId: "a",
  featureId: null,
  title: "x",
  status: "todo",
  assignee: "",
  created: "2026-09-29T00:00:00.000Z",
  ...over,
});

const feats: Feature[] = [
  { id: "F-1", projectId: "a", title: "One", milestoneId: "M-1" },
  { id: "F-2", projectId: "a", title: "Two" },
  { id: "F-1", projectId: "b", title: "Other one" },
];
const mss: Milestone[] = [{ id: "M-1", projectId: "a", title: "Alpha", due: "2026-10-05", featureIds: ["F-1"] }];

const data: TrackerResponse = {
  milestones: mss,
  features: feats,
  tasks: [
    t({ id: "T-1", featureId: "F-1", status: "done" }),
    t({ id: "T-2", featureId: "F-1", status: "in_progress" }),
    t({ id: "T-3", featureId: "F-2", status: "todo" }),
    t({ id: "T-4", featureId: null, status: "backlog" }),
    t({ id: "T-1", projectId: "b", featureId: "F-1", status: "review" }),
  ],
};

describe("model", () => {
  it("steps one status and stops at the ends", () => {
    expect(stepStatus("todo", 1)).toBe("in_progress");
    expect(stepStatus("todo", -1)).toBe("backlog");
    expect(stepStatus("backlog", -1)).toBeNull();
    expect(stepStatus("done", 1)).toBeNull();
  });

  it("suggests done only for an approved latest snapshot on a task that is not done", () => {
    expect(suggestsDone({ status: "review", latestSnapshot: { version: 3, verdict: "approved" } })).toBe(true);
    expect(suggestsDone({ status: "done", latestSnapshot: { version: 3, verdict: "approved" } })).toBe(false);
    expect(suggestsDone({ status: "review", latestSnapshot: { version: 3, verdict: "pending" } })).toBe(false);
    expect(suggestsDone({ status: "review", latestSnapshot: null })).toBe(false);
  });

  it("lists only projects that have tracker data, in boot order", () => {
    const m = buildModel(data, "all", ["z", "b", "a", "c"]);
    expect(m.projectIds).toEqual(["b", "a"]);
    expect(m.tasks).toHaveLength(5);
  });

  it("cuts the model to one project", () => {
    const m = buildModel(data, "b", ["a", "b"]);
    expect(m.tasks.map((x) => x.id)).toEqual(["T-1"]);
    expect(m.features).toHaveLength(1);
    expect(m.milestones).toHaveLength(0);
  });

  it("groups tasks by status", () => {
    const g = byStatus(data.tasks);
    expect(g.done).toHaveLength(1);
    expect(g.review).toHaveLength(1);
    expect(g.backlog).toHaveLength(1);
  });

  it("words the days left", () => {
    expect(daysLabel(5)).toBe("5 days left");
    expect(daysLabel(1)).toBe("1 day left");
    expect(daysLabel(0)).toBe("due today");
    expect(daysLabel(-1)).toBe("1 day overdue");
    expect(daysLabel(-3)).toBe("3 days overdue");
  });

  it("builds milestone progress per project (ids repeat across projects)", () => {
    const m = buildModel(data, "all", ["a", "b"]);
    const [b] = milestoneBlocks(m, Date.parse("2026-10-01T12:00:00Z"));
    expect(b.milestone.id).toBe("M-1");
    expect(b.total).toBe(2); // T-1 and T-2 of project a, not T-1 of project b
    expect(b.done).toBe(1);
    expect(b.pct).toBe(50);
    expect(b.days).toBe(4);
    expect(b.daysLabel).toBe("4 days left");
    expect(b.tight).toBe(false);
  });

  it("marks a milestone tight when it is late", () => {
    const m = buildModel(data, "a", ["a"]);
    const [b] = milestoneBlocks(m, Date.parse("2026-10-09T12:00:00Z"));
    expect(b.daysLabel).toBe("4 days overdue");
    expect(b.tight).toBe(true);
  });

  it("collects features outside milestones and tasks without a feature", () => {
    const m = buildModel(data, "all", ["a", "b"]);
    const r = leftovers(m);
    // F-2 of a has no milestone, F-1 of b has no milestone
    expect(r.outside.map((x) => x.feature.projectId + "/" + x.feature.id).sort()).toEqual(["a/F-2", "b/F-1"]);
    expect(r.unscheduled.map((x) => x.id)).toEqual(["T-4"]);
  });

  it("finds the due time of the milestone that holds a feature", () => {
    expect(featureDue("a", "F-1", mss, feats)).toBe(Date.parse("2026-10-05T12:00:00Z"));
    expect(featureDue("a", "F-2", mss, feats)).toBeNull();
  });

  it("draws a bar from created to now, and a done task up to updated", () => {
    const now = Date.parse("2026-10-01T00:00:00Z");
    const open = barFor(t({ status: "in_progress", created: "2026-09-29T00:00:00Z" }), now, null);
    expect(open.endMs).toBe(now);
    const done = barFor(t({ status: "done", created: "2026-09-29T00:00:00Z", updated: "2026-09-30T00:00:00Z" }), now, null);
    expect(done.endMs).toBe(Date.parse("2026-09-30T00:00:00Z"));
    const noUpdate = barFor(t({ status: "done", created: "2026-09-29T00:00:00Z" }), now, 123);
    expect(noUpdate.endMs).toBe(Date.parse("2026-09-29T00:00:00Z") + DAY);
    expect(noUpdate.dueMs).toBeNull(); // no ghost line for a done task
  });

  it("keeps timeline positions inside 0..100", () => {
    const g = timelineGeometry(Date.parse("2026-10-01T10:00:00Z"));
    expect(g.pos(g.winStart - DAY)).toBe(0);
    expect(g.pos(g.winEnd + DAY)).toBe(100);
    expect(g.pos(g.today)).toBeCloseTo((14 / 36) * 100, 5);
  });

  it("words relative time", () => {
    const now = Date.parse("2026-10-01T12:00:00Z");
    expect(relTime("2026-10-01T11:59:40Z", now)).toBe("just now");
    expect(relTime("2026-10-01T11:48:00Z", now)).toBe("12m ago");
    expect(relTime("2026-10-01T00:00:00Z", now)).toBe("12h ago");
    expect(relTime("2026-09-30T12:00:00Z", now)).toBe("yesterday");
    expect(relTime("2026-09-26T12:00:00Z", now)).toBe("5d ago");
    expect(relTime(null, now)).toBe("");
  });
});
