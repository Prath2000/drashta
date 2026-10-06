import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { useState } from "react";
import { useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { server } from "~/mocks/node";
import { renderApp, renderWithRouter } from "../../test/render";
import { CHORD_MS, ESC_ORDER, isTypingTarget, runEscape, useEscapeLayer, useShortcutAction, useShortcuts } from "./shortcuts";
import { getState, setLastProject, setState } from "./store";

const ITEM_A = "acme-api:commit:93e82aa";
const ITEM_B = "notes-app:commit:b7dc920bef";

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

/** Presses a key. Returns false when the page called preventDefault (it "handled" the key). */
function key(k: string, init: KeyboardEventInit = {}, target: Element | Document = document.body): boolean {
  return fireEvent.keyDown(target, { key: k, ...init });
}

/** The smallest page that installs the listener: a place to type and a place to read the URL. */
function Host() {
  useShortcuts();
  const loc = useLocation();
  return (
    <>
      <div data-testid="loc">{loc.pathname + loc.search}</div>
      <input aria-label="field" />
      <textarea aria-label="area" />
      <select aria-label="pick">
        <option>a</option>
      </select>
      <div aria-label="edit" tabIndex={0} contentEditable suppressContentEditableWarning>
        <span data-testid="inside-edit">x</span>
      </div>
      <button type="button">plain</button>
    </>
  );
}

const loc = () => screen.getByTestId("loc").textContent;

describe("isTypingTarget", () => {
  const make = (tag: string, attrs: Record<string, string> = {}) => {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
    document.body.appendChild(el);
    return el;
  };
  it("is true for text fields, textareas, selects and contenteditable", () => {
    expect(isTypingTarget(make("input"))).toBe(true);
    expect(isTypingTarget(make("input", { type: "search" }))).toBe(true);
    expect(isTypingTarget(make("input", { type: "number" }))).toBe(true);
    expect(isTypingTarget(make("textarea"))).toBe(true);
    expect(isTypingTarget(make("select"))).toBe(true);
    expect(isTypingTarget(make("div", { contenteditable: "true" }))).toBe(true);
    expect(isTypingTarget(make("div", { contenteditable: "" }))).toBe(true);
    const inner = document.createElement("span");
    make("div", { contenteditable: "true" }).appendChild(inner);
    expect(isTypingTarget(inner)).toBe(true);
  });
  it("is false for buttons, checkboxes, links, the body and non-elements", () => {
    expect(isTypingTarget(make("button"))).toBe(false);
    expect(isTypingTarget(make("input", { type: "checkbox" }))).toBe(false);
    expect(isTypingTarget(make("input", { type: "radio" }))).toBe(false);
    expect(isTypingTarget(make("input", { type: "button" }))).toBe(false);
    expect(isTypingTarget(make("a", { href: "#" }))).toBe(false);
    expect(isTypingTarget(make("div", { contenteditable: "false" }))).toBe(false);
    expect(isTypingTarget(document.body)).toBe(false);
    expect(isTypingTarget(document)).toBe(false);
    expect(isTypingTarget(null)).toBe(false);
  });
});

describe("the one listener", () => {
  it("adds one keydown listener on mount, keeps it across navigation, removes it on unmount", async () => {
    const add = vi.spyOn(document, "addEventListener");
    const remove = vi.spyOn(document, "removeEventListener");
    const { router, unmount } = renderWithRouter(<Host />, "/");
    const adds = () => add.mock.calls.filter((c) => c[0] === "keydown");
    expect(adds()).toHaveLength(1);

    await act(async () => {
      await router.navigate("/tracker");
    });
    expect(loc()).toBe("/tracker");
    expect(adds()).toHaveLength(1);

    const fn = adds()[0][1];
    expect(remove.mock.calls.filter((c) => c[0] === "keydown")).toHaveLength(0);
    unmount();
    const removed = remove.mock.calls.filter((c) => c[0] === "keydown");
    expect(removed).toHaveLength(1);
    expect(removed[0][1]).toBe(fn);
  });

  it("does nothing after it is removed", () => {
    const calls: string[] = [];
    function Once() {
      useShortcuts();
      useEscapeLayer("viewport", true, () => calls.push("viewport"));
      return null;
    }
    const { unmount } = renderWithRouter(<Once />);
    key("Escape");
    expect(calls).toEqual(["viewport"]);
    unmount();
    key("Escape");
    expect(calls).toEqual(["viewport"]);
  });
});

describe("g chords", () => {
  it("g h / g p / g t / g i go to Home / Project picker / Tracker / Inbox", async () => {
    renderWithRouter(<Host />, "/inbox");
    expect(key("g")).toBe(false);
    expect(key("h")).toBe(false);
    await waitFor(() => expect(loc()).toBe("/"));

    key("g");
    key("t");
    await waitFor(() => expect(loc()).toBe("/tracker"));

    key("g");
    key("i");
    await waitFor(() => expect(loc()).toBe("/inbox"));

    key("g");
    key("p");
    await waitFor(() => expect(loc()).toBe("/p"));
  });

  it("the chord works again and again", async () => {
    renderWithRouter(<Host />, "/");
    for (const [k, path] of [
      ["t", "/tracker"],
      ["i", "/inbox"],
      ["h", "/"],
    ] as const) {
      key("g");
      key(k);
      await waitFor(() => expect(loc()).toBe(path));
    }
  });

  it("the second key must come within 1 s", async () => {
    const { router } = renderWithRouter(<Host />, "/inbox");
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    key("g");
    vi.advanceTimersByTime(CHORD_MS + 1);
    expect(key("h")).toBe(true); // too late: `h` alone is not a shortcut
    vi.useRealTimers();
    await act(async () => {});
    expect(router.state.location.pathname).toBe("/inbox");
  });

  it("a second key inside the window still works at the end of the window", async () => {
    renderWithRouter(<Host />, "/inbox");
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    key("g");
    vi.advanceTimersByTime(CHORD_MS - 1);
    expect(key("h")).toBe(false);
    vi.useRealTimers();
    await waitFor(() => expect(loc()).toBe("/"));
  });

  it("a stray key cancels the chord, and `g g` starts a new one", async () => {
    const { router } = renderWithRouter(<Host />, "/inbox");
    key("g");
    key("x");
    expect(key("h")).toBe(true);
    await act(async () => {});
    expect(router.state.location.pathname).toBe("/inbox");

    key("g");
    key("g");
    key("t");
    await waitFor(() => expect(loc()).toBe("/tracker"));
  });

  it("a stray key after g still does its own job", () => {
    const focus = vi.fn();
    function Page() {
      useShortcuts();
      useShortcutAction("focusSearch", focus);
      return null;
    }
    renderWithRouter(<Page />);
    key("g");
    expect(key("/")).toBe(false);
    expect(focus).toHaveBeenCalledTimes(1);
    expect(key("h")).toBe(true); // the chord was used up
  });

  it("Shift alone does not cancel the chord", async () => {
    renderWithRouter(<Host />, "/inbox");
    key("g");
    key("Shift");
    key("t");
    await waitFor(() => expect(loc()).toBe("/tracker"));
  });

  it("g p opens the last project, or the picker when there is none", async () => {
    renderApp("/inbox");
    await waitFor(() => expect(screen.getByTestId("badge-tracker")).toBeInTheDocument()); // boot is loaded
    key("g");
    key("p");
    await waitFor(() => expect(screen.getByRole("list", { name: "Projects" })).toBeInTheDocument());

    act(() => setLastProject("acme-api"));
    key("g");
    key("i");
    await waitFor(() => expect(screen.queryByRole("list", { name: "Projects" })).toBeNull());
    key("g");
    key("p");
    await waitFor(() => expect(screen.getByRole("navigation", { name: "Breadcrumb" })).toHaveTextContent("Project›acme-api"));
  });

  it("g p falls back to the picker when the last project is gone", async () => {
    renderApp("/inbox");
    await waitFor(() => expect(screen.getByTestId("badge-tracker")).toBeInTheDocument());
    act(() => setLastProject("no_such_project"));
    key("g");
    key("p");
    await waitFor(() => expect(screen.getByRole("list", { name: "Projects" })).toBeInTheDocument());
  });
});

describe("text fields", () => {
  it.each([
    ["input", "field"],
    ["textarea", "area"],
    ["select", "pick"],
    ["contenteditable", "edit"],
  ])("keys do nothing while a %s has focus", async (_name, label) => {
    const focus = vi.fn();
    const open = vi.fn();
    function Page() {
      useShortcuts();
      useShortcutAction("focusSearch", focus);
      useShortcutAction("openNative", open);
      return <Host />;
    }
    const { router } = renderWithRouter(<Page />, "/inbox");
    const el = screen.getByLabelText(label);
    el.focus();
    expect(el).toHaveFocus();
    for (const k of ["/", "o", "r", "[", "]", "g"]) expect(key(k, {}, el)).toBe(true);
    key("g", {}, el);
    expect(key("t", {}, el)).toBe(true);
    await act(async () => {});
    expect(router.state.location.pathname).toBe("/inbox");
    expect(focus).not.toHaveBeenCalled();
    expect(open).not.toHaveBeenCalled();
    expect(getState().scan.running).toBe(false);
  });

  it("an element inside a contenteditable counts as typing", () => {
    const focus = vi.fn();
    function Page() {
      useShortcuts();
      useShortcutAction("focusSearch", focus);
      return <Host />;
    }
    renderWithRouter(<Page />);
    expect(key("/", {}, screen.getByTestId("inside-edit"))).toBe(true);
    expect(focus).not.toHaveBeenCalled();
  });

  it("a g typed in a field does not start a chord that finishes outside it", async () => {
    const { router } = renderWithRouter(<Host />, "/inbox");
    key("g", {}, screen.getByLabelText("field"));
    expect(key("t")).toBe(true);
    await act(async () => {});
    expect(router.state.location.pathname).toBe("/inbox");
  });

  it("Esc still works in a text field", () => {
    const close = vi.fn();
    function Page() {
      useShortcuts();
      useEscapeLayer("viewport", true, close);
      return <Host />;
    }
    renderWithRouter(<Page />);
    const el = screen.getByLabelText("area");
    el.focus();
    expect(key("Escape", {}, el)).toBe(false);
    expect(close).toHaveBeenCalledTimes(1);
  });

  it("buttons and checkboxes are not text fields: keys still work on them", async () => {
    renderWithRouter(<Host />, "/inbox");
    const b = screen.getByRole("button", { name: "plain" });
    b.focus();
    key("g", {}, b);
    key("t", {}, b);
    await waitFor(() => expect(loc()).toBe("/tracker"));
  });
});

describe("modifiers, repeat, readout, handled keys", () => {
  it.each([["ctrlKey"], ["metaKey"], ["altKey"]] as const)("%s held: nothing happens and the key is not blocked", async (mod) => {
    const focus = vi.fn();
    const close = vi.fn();
    function Page() {
      useShortcuts();
      useShortcutAction("focusSearch", focus);
      useEscapeLayer("viewport", true, close);
      return <Host />;
    }
    const { router } = renderWithRouter(<Page />, "/inbox");
    expect(key("/", { [mod]: true })).toBe(true);
    expect(key("r", { [mod]: true })).toBe(true);
    expect(key("Escape", { [mod]: true })).toBe(true);
    expect(key("g", { [mod]: true })).toBe(true);
    expect(key("t", { [mod]: true })).toBe(true);
    await act(async () => {});
    expect(router.state.location.pathname).toBe("/inbox");
    expect(focus).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
    expect(getState().scan.running).toBe(false);
  });

  it("a held-down key does not repeat the action", () => {
    const close = vi.fn();
    function Page() {
      useShortcuts();
      useEscapeLayer("viewport", true, close);
      return null;
    }
    renderWithRouter(<Page />);
    key("Escape", { repeat: true });
    expect(close).not.toHaveBeenCalled();
  });

  it("does nothing in readout mode, Esc included", async () => {
    document.documentElement.classList.add("desktop");
    setState({ mode: "readout" });
    const focus = vi.fn();
    const close = vi.fn();
    function Page() {
      useShortcuts();
      useShortcutAction("focusSearch", focus);
      useEscapeLayer("viewport", true, close);
      return <Host />;
    }
    const { router } = renderWithRouter(<Page />, "/inbox");
    for (const k of ["/", "r", "o", "[", "]", "Escape", "g", "t"]) expect(key(k)).toBe(true);
    await act(async () => {});
    expect(router.state.location.pathname).toBe("/inbox");
    expect(focus).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
    expect(getState().scan.running).toBe(false);

    // and it works again at once when the mode flips back
    act(() => setState({ mode: "browsing" }));
    expect(key("Escape")).toBe(false);
    expect(close).toHaveBeenCalledTimes(1);
  });

  it("never blocks a key it does not handle", () => {
    renderWithRouter(<Host />, "/inbox");
    for (const k of ["a", "x", "Enter", " ", "Tab", "ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight", "?", "O", "R", "G", "{", "F5"]) {
      expect(key(k)).toBe(true);
    }
    // an Esc with nothing to close, `[`/`]` with no viewport, `/` and `o` with no target
    for (const k of ["Escape", "[", "]", "/", "o"]) expect(key(k)).toBe(true);
  });

  it("leaves a key alone when something else already handled it", () => {
    const close = vi.fn();
    function Page() {
      useShortcuts();
      useEscapeLayer("viewport", true, close);
      return <Host />;
    }
    renderWithRouter(<Page />);
    const field = screen.getByLabelText("field");
    field.addEventListener("keydown", (e) => e.preventDefault());
    key("Escape", {}, field);
    expect(close).not.toHaveBeenCalled();
  });
});

describe("Esc layers", () => {
  it("closes one layer per press, in the order app, search, day, viewport", () => {
    const calls: string[] = [];
    function Page() {
      useShortcuts();
      const [open, setOpen] = useState({ app: true, search: true, day: true, viewport: true });
      const close = (name: keyof typeof open) => () => {
        calls.push(name);
        setOpen((o) => ({ ...o, [name]: false }));
      };
      // registered in the "wrong" order on purpose
      useEscapeLayer("viewport", open.viewport, close("viewport"));
      useEscapeLayer("day", open.day, close("day"));
      useEscapeLayer("search", open.search, close("search"));
      useEscapeLayer("app", open.app, close("app"));
      return null;
    }
    renderWithRouter(<Page />);
    expect(ESC_ORDER).toEqual(["app", "search", "day", "viewport"]);
    expect(key("Escape")).toBe(false);
    expect(calls).toEqual(["app"]);
    expect(key("Escape")).toBe(false);
    expect(calls).toEqual(["app", "search"]);
    expect(key("Escape")).toBe(false);
    expect(calls).toEqual(["app", "search", "day"]);
    expect(key("Escape")).toBe(false);
    expect(calls).toEqual(["app", "search", "day", "viewport"]);
    expect(key("Escape")).toBe(true); // nothing left: the key is not blocked
    expect(calls).toHaveLength(4);
  });

  it("skips a layer that is not open", () => {
    const calls: string[] = [];
    function Page() {
      useShortcuts();
      useEscapeLayer("search", false, () => calls.push("search"));
      useEscapeLayer("day", false, () => calls.push("day"));
      useEscapeLayer("viewport", true, () => calls.push("viewport"));
      return null;
    }
    renderWithRouter(<Page />);
    key("Escape");
    expect(calls).toEqual(["viewport"]);
  });

  it("the layer that registered last is closed first, and the older one stays", () => {
    const calls: string[] = [];
    function Page() {
      useShortcuts();
      const [second, setSecond] = useState(false);
      useEscapeLayer("day", true, () => calls.push("first"));
      return (
        <>
          <button type="button" onClick={() => setSecond(true)}>
            add
          </button>
          {second ? <Second calls={calls} /> : null}
        </>
      );
    }
    function Second({ calls }: { calls: string[] }) {
      useEscapeLayer("day", true, () => calls.push("second"));
      return null;
    }
    renderWithRouter(<Page />);
    fireEvent.click(screen.getByRole("button", { name: "add" }));
    key("Escape");
    expect(calls).toEqual(["second"]);
  });

  it("runEscape reports whether it closed something", () => {
    expect(runEscape()).toBe(false);
  });
});

// ---------- the real app ----------
describe("in the app", () => {
  it("/ focuses the search box and selects its text", async () => {
    const { user } = renderApp("/");
    const input = screen.getByRole("textbox", { name: "Search" }) as HTMLInputElement;
    expect(input).not.toHaveFocus();
    expect(key("/")).toBe(false);
    expect(input).toHaveFocus();

    await user.keyboard("abc");
    expect(input).toHaveValue("abc");
    input.blur();
    key("/");
    expect(input).toHaveFocus();
    expect(input.selectionStart).toBe(0);
    expect(input.selectionEnd).toBe(3);
  });

  it("typing in the search box never triggers a shortcut", async () => {
    const { user, router } = renderApp("/");
    const input = screen.getByRole("textbox", { name: "Search" });
    await user.click(input);
    await user.type(input, "ghr/o]it gt");
    expect(input).toHaveValue("ghr/o]it gt");
    expect(router.state.location.pathname).toBe("/");
    expect(getState().scan.running).toBe(false);
  });

  it("g h / g t / g i / g p move between sections", async () => {
    const { router } = renderApp("/");
    for (const [k, path] of [
      ["t", "/tracker"],
      ["i", "/inbox"],
      ["p", "/p"],
      ["h", "/"],
    ] as const) {
      key("g");
      key(k);
      await waitFor(() => expect(router.state.location.pathname).toBe(path));
    }
  });

  it("o runs Open ↗ for the item in the viewport", async () => {
    renderApp("/?item=" + ITEM_A);
    await screen.findByRole("button", { name: /git show ↗/ });
    expect(key("o")).toBe(false);
    await waitFor(() => expect(screen.getByTestId("toast")).toHaveTextContent("git show ↗"));
  });

  it("o does nothing when no item is open or the item is missing", async () => {
    renderApp("/");
    expect(key("o")).toBe(true);
    expect(screen.queryByTestId("toast")).toBeNull();

    renderApp("/?item=acme-api:file:gone/away.go");
    await screen.findByTestId("viewport-missing");
    expect(key("o")).toBe(true);
  });

  it("[ and ] go back and forward in the viewport history", async () => {
    const { router } = renderApp("/?item=" + ITEM_A);
    await screen.findByRole("complementary", { name: "Viewport" });
    const item = () => new URLSearchParams(router.state.location.search).get("item");

    expect(key("[")).toBe(true); // at the start: nothing to go back to
    expect(item()).toBe(ITEM_A);

    await act(async () => {
      await router.navigate("/?item=" + ITEM_B);
    });
    await waitFor(() => expect(screen.getByRole("button", { name: "Back" })).toBeEnabled());

    expect(key("[")).toBe(false);
    await waitFor(() => expect(item()).toBe(ITEM_A));
    expect(screen.getByRole("button", { name: "Forward" })).toBeEnabled();

    expect(key("]")).toBe(false);
    await waitFor(() => expect(item()).toBe(ITEM_B));
    expect(key("]")).toBe(true); // at the end
  });

  it("[ and ] do nothing when the viewport is closed", () => {
    const { router } = renderApp("/");
    expect(key("[")).toBe(true);
    expect(key("]")).toBe(true);
    expect(router.state.location.search).toBe("");
  });

  it("r rescans all projects on Home and only the project on a project page", async () => {
    const bodies: unknown[] = [];
    server.use(
      http.post("*/api/refresh", async ({ request }) => {
        bodies.push(await request.json());
        return HttpResponse.json({ started: true });
      }),
    );
    const { router } = renderApp("/tracker");
    expect(key("r")).toBe(false);
    await waitFor(() => expect(bodies).toEqual([{}]));
    expect(getState().scan.running).toBe(true);
    expect(screen.getByTestId("scan-label")).toHaveTextContent("rescanning…");

    // one scan at a time
    key("r");
    await act(async () => {});
    expect(bodies).toHaveLength(1);

    // the scan ends; now a project page rescans that project only
    act(() => setState((s) => ({ scan: { ...s.scan, running: false } })));
    await act(async () => {
      await router.navigate("/p/acme-api/commits");
    });
    key("r");
    await waitFor(() => expect(bodies).toEqual([{}, { projectId: "acme-api" }]));
  });

  it("r shows an error toast when the server refuses", async () => {
    server.use(http.post("*/api/refresh", () => HttpResponse.json({ error: { code: "busy", message: "scanner is busy" } }, { status: 409 })));
    renderApp("/");
    key("r");
    await waitFor(() => expect(screen.getByTestId("toast")).toHaveTextContent("scanner is busy"));
    expect(getState().scan.running).toBe(false);
  });

  it("Esc closes the search panel, then the selected day, then the viewport", async () => {
    const { user, router } = renderApp("/?item=" + ITEM_A);
    await screen.findByRole("complementary", { name: "Viewport" });
    const day = await waitFor(() => {
      const b = document.querySelector<HTMLButtonElement>("button.hm-col[data-i]");
      expect(b).not.toBeNull();
      return b as HTMLButtonElement;
    });
    await user.click(day);
    expect(day).toHaveAttribute("aria-pressed", "true");
    expect(screen.queryByTestId("home-legend")).toBeNull();

    // open the search panel, then move focus away from the field
    const input = screen.getByRole("textbox", { name: "Search" });
    await user.type(input, "acme-api");
    expect(await screen.findByRole("dialog", { name: "Search results" })).toBeInTheDocument();
    act(() => input.blur());

    expect(key("Escape")).toBe(false); // 1: the panel
    expect(screen.queryByRole("dialog", { name: "Search results" })).toBeNull();
    expect(day).toHaveAttribute("aria-pressed", "true");
    expect(router.state.location.search).toContain("item=");
    expect(input).toHaveValue("acme-api"); // the query itself stays

    expect(key("Escape")).toBe(false); // 2: the day
    expect(day).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByTestId("home-legend")).toBeInTheDocument();
    expect(router.state.location.search).toContain("item=");

    expect(key("Escape")).toBe(false); // 3: the viewport
    await waitFor(() => expect(screen.queryByRole("complementary", { name: "Viewport" })).toBeNull());
    expect(router.state.location.search).toBe("");

    expect(key("Escape")).toBe(true); // 4: nothing left
  });

  it("Esc with the focus inside the viewport still deselects the day first", async () => {
    const { user, router } = renderApp("/?item=" + ITEM_A);
    const aside = await screen.findByRole("complementary", { name: "Viewport" });
    const day = await waitFor(() => {
      const b = document.querySelector<HTMLButtonElement>("button.hm-col[data-i]");
      expect(b).not.toBeNull();
      return b as HTMLButtonElement;
    });
    await user.click(day);
    act(() => aside.focus());
    expect(key("Escape", {}, aside)).toBe(false);
    expect(day).toHaveAttribute("aria-pressed", "false");
    expect(router.state.location.search).toContain("item=");
    key("Escape", {}, aside);
    await waitFor(() => expect(router.state.location.search).toBe(""));
  });

  it("Esc in the search box: first closes the panel, then clears the query, then moves on", async () => {
    const { user, router } = renderApp("/?item=" + ITEM_A);
    await screen.findByRole("complementary", { name: "Viewport" });
    const input = screen.getByRole("textbox", { name: "Search" });
    await user.type(input, "zzzqqqxx");
    expect(await screen.findByRole("dialog", { name: "Search results" })).toBeInTheDocument();

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: "Search results" })).toBeNull();
    expect(input).toHaveValue("zzzqqqxx");
    expect(router.state.location.search).toContain("item=");

    await user.keyboard("{Escape}");
    expect(input).toHaveValue("");
    expect(router.state.location.search).toContain("item=");

    // nothing is left in the field: the same Esc goes on to the viewport
    await user.keyboard("{Escape}");
    await waitFor(() => expect(router.state.location.search).toBe(""));
  });

  it("a click on Home's lane day still uses its own arrow keys (no duplicate handling)", async () => {
    const { user } = renderApp("/");
    const days = await waitFor(() => {
      const list = document.querySelectorAll<HTMLButtonElement>("button.hm-col[data-i]");
      expect(list.length).toBeGreaterThan(2);
      return list;
    });
    const first = days[0].closest("[data-cols]") as HTMLElement;
    const start = first.querySelector<HTMLButtonElement>('button[tabindex="0"]') as HTMLButtonElement;
    start.focus();
    const i = Number(start.dataset.i);
    await user.keyboard("{ArrowLeft}");
    expect(document.activeElement).toHaveAttribute("data-i", String(i - 1));
  });

  it("does nothing in readout mode, even with an item in the URL", async () => {
    document.documentElement.classList.add("desktop");
    setState({ mode: "readout" });
    const { router } = renderApp("/tracker?item=" + ITEM_A);
    for (const k of ["g", "t", "r", "o", "[", "]", "Escape", "/"]) expect(key(k)).toBe(true);
    await act(async () => {});
    expect(router.state.location.pathname + router.state.location.search).toBe("/tracker?item=" + encodeURIComponent(ITEM_A).replace(/%3A/g, ":"));
    expect(getState().scan.running).toBe(false);
  });
});
