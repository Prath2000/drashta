import { describe, expect, it } from "vitest";
import {
  BAR_MAX,
  buildAxis,
  axisLabelAt,
  commitBarH,
  dayLabel,
  dayLabelPad,
  fileBarH,
  fitLanes,
  laneCapacity,
  localHHMM,
  logH,
} from "./geometry";

describe("bar heights", () => {
  it("draws nothing for zero and at least the minimum for anything above zero", () => {
    expect(fileBarH(0, 100)).toBe(0);
    expect(fileBarH(1, 100000)).toBe(2);
    expect(commitBarH(0, 500, 500)).toBe(0);
    expect(commitBarH(1, 1, 100000)).toBe(4);
  });

  it("the biggest value reaches the full bar height", () => {
    expect(fileBarH(500, 500)).toBe(BAR_MAX);
    expect(commitBarH(3, 900, 900)).toBe(BAR_MAX);
  });

  it("is a log scale: 1% of the biggest value already draws half height", () => {
    expect(logH(100, 10000, 24, 2)).toBe(12);
    expect(fileBarH(10, 500)).toBeLessThan(fileBarH(100, 500));
  });

  it("a commit with no line stats draws at the minimum", () => {
    expect(commitBarH(2, 0, 800)).toBe(4);
  });

  it("is monotonic", () => {
    let prev = 0;
    for (let v = 1; v <= 1000; v += 7) {
      const h = fileBarH(v, 1000);
      expect(h).toBeGreaterThanOrEqual(prev);
      prev = h;
    }
  });
});

describe("day labels and axis", () => {
  it("formats day keys", () => {
    expect(dayLabel("2026-09-04")).toBe("4 Sep");
    expect(dayLabelPad("2026-09-04")).toBe("04 Sep");
  });

  it("labels every 7th column at 30d (counting back from today) and every column at 7d", () => {
    expect(axisLabelAt(30)).toEqual([1, 8, 15, 22, 29]);
    expect(axisLabelAt(7)).toEqual([0, 1, 2, 3, 4, 5, 6]);
  });

  it("the last axis cell reads today and labelled cells get a taller tick", () => {
    const days = Array.from({ length: 30 }, (_, i) => "2026-09-" + String(i + 1).padStart(2, "0"));
    const ax = buildAxis(days);
    expect(ax).toHaveLength(30);
    expect(ax[29]).toMatchObject({ label: "today", hasLabel: true, tickH: 6 });
    expect(ax[1]).toMatchObject({ label: "02 Sep", hasLabel: true, tickH: 6 });
    expect(ax[2]).toMatchObject({ hasLabel: false, tickH: 3 });
  });
});

describe("readout capacity", () => {
  it("counts the rows that fit under the 16px axis", () => {
    expect(laneCapacity(16 + 36 * 5)).toBe(5);
    expect(laneCapacity(16 + 36 * 5 + 35)).toBe(5);
    expect(laneCapacity(20)).toBe(0);
  });

  it("an unmeasured height means no limit", () => {
    expect(laneCapacity(0)).toBe(Infinity);
  });

  it("keeps everything when it fits", () => {
    expect(fitLanes([1, 2, 3], 3)).toEqual({ shown: [1, 2, 3], more: 0 });
    expect(fitLanes([1, 2, 3], Infinity)).toEqual({ shown: [1, 2, 3], more: 0 });
  });

  it("gives the last slot to the +N row when it does not fit", () => {
    expect(fitLanes([1, 2, 3, 4, 5, 6], 4)).toEqual({ shown: [1, 2, 3], more: 3 });
    expect(fitLanes([1, 2], 0)).toEqual({ shown: [], more: 2 });
  });
});

describe("local time", () => {
  it("formats as HH:MM and survives bad input", () => {
    expect(localHHMM("2026-10-01T03:10:00+05:30")).toMatch(/^\d\d:\d\d$/);
    expect(localHHMM("")).toBe("");
    expect(localHHMM("nonsense")).toBe("");
  });
});
