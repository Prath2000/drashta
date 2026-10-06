import { act } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getState, modeFromClasses, setRange, startModeTracking, toast, TOAST_MS, dismissToast, setState } from "./store";

const html = () => document.documentElement;

describe("mode from the <html> class", () => {
  it("is browsing without the desktop marker", () => {
    expect(modeFromClasses(html())).toBe("browsing");
  });
  it("is readout with desktop and no Plash browsing class", () => {
    html().classList.add("desktop");
    expect(modeFromClasses(html())).toBe("readout");
  });
  it("is browsing with desktop and the Plash browsing class", () => {
    html().classList.add("desktop", "plash-is-browsing-mode");
    expect(modeFromClasses(html())).toBe("browsing");
  });
});

describe("startModeTracking", () => {
  let stop: (() => void) | null = null;
  afterEach(() => {
    stop?.();
    stop = null;
    window.history.replaceState(null, "", "/");
  });

  it("adds the desktop class for ?desktop=1 and follows Plash class changes", async () => {
    window.history.replaceState(null, "", "/?desktop=1");
    stop = startModeTracking(document);
    expect(html().classList.contains("desktop")).toBe(true);
    expect(getState().mode).toBe("readout");
    html().classList.add("plash-is-browsing-mode");
    await act(async () => {
      await Promise.resolve();
    });
    expect(getState().mode).toBe("browsing");
    html().classList.remove("plash-is-browsing-mode");
    await act(async () => {
      await Promise.resolve();
    });
    expect(getState().mode).toBe("readout");
  });

  it("stays browsing in a normal tab", () => {
    stop = startModeTracking(document);
    expect(html().classList.contains("desktop")).toBe(false);
    expect(getState().mode).toBe("browsing");
  });
});

describe("toasts", () => {
  afterEach(() => vi.useRealTimers());

  it("shows one at a time and leaves after the timeout", () => {
    vi.useFakeTimers();
    toast("first");
    toast("second");
    expect(getState().toast?.msg).toBe("second");
    vi.advanceTimersByTime(TOAST_MS + 10);
    expect(getState().toast).toBeNull();
  });

  it("keeps an error toast until dismissed", () => {
    vi.useFakeTimers();
    toast("open failed", { error: true });
    vi.advanceTimersByTime(TOAST_MS * 3);
    expect(getState().toast?.error).toBe(true);
    dismissToast();
    expect(getState().toast).toBeNull();
  });

  it("shows nothing in readout mode", () => {
    setState({ mode: "readout" });
    toast("hidden");
    expect(getState().toast).toBeNull();
  });
});

describe("range", () => {
  it("is 30 by default and is saved", () => {
    expect(getState().range).toBe(30);
    setRange(7);
    expect(getState().range).toBe(7);
    expect(localStorage.getItem("drashta.range")).toBe("7");
  });
});
