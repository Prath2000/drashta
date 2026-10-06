import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mockDb } from "~/mocks/handlers";
import { server } from "~/mocks/node";
import { fakeSession, githubBroken, githubUnavailable, githubWith, sessionsPaged } from "~/mocks/project";
import { renderApp } from "../../../test/render";

type Rec = { method: string; path: string; body: unknown };

/** Records every /api request the page makes (with JSON bodies). */
function record() {
  const list: Rec[] = [];
  const on = async ({ request }: { request: Request }) => {
    const u = new URL(request.url);
    let body: unknown = null;
    if (request.method !== "GET") {
      try {
        body = await request.clone().json();
      } catch {
        body = null;
      }
    }
    list.push({ method: request.method, path: u.pathname + u.search, body });
  };
  server.events.on("request:start", on);
  return { list, stop: () => server.events.removeListener("request:start", on) };
}

let rec: ReturnType<typeof record> | null = null;
afterEach(() => {
  rec?.stop();
  rec = null;
  vi.useRealTimers();
});

const where = (router: ReturnType<typeof renderApp>["router"]) => router.state.location.pathname + router.state.location.search;

describe("project page: header and overview", () => {
  it("shows the cartouche, native actions and the overview vitals", async () => {
    renderApp("/p/notes-app");
    const cart = await screen.findByTestId("cartouche");
    expect(within(cart).getByRole("heading", { level: 1 })).toHaveTextContent("notes-app");
    expect(cart).toHaveTextContent("/fx/projects/personal/notes-app");
    expect(cart).toHaveTextContent("fossil trunk");
    expect(cart).toHaveTextContent("A small notes app");
    const actions = screen.getByTestId("native-actions");
    await within(actions).findByRole("button", { name: "Open localhost:8765 in Safari" });
    expect(within(actions).getByRole("button", { name: "Stop the server on port 8765" })).toHaveTextContent("Stop server :8765");
    // helpers never get a stop button in the header
    expect(within(actions).queryByText(/:51738/)).toBeNull();
    await within(actions).findByText(/Resume Claude · \.claude-work/);
    const vitals = screen.getByTestId("vitals");
    expect(vitals).toHaveTextContent("Uncommitted");
    await waitFor(() => expect(vitals).toHaveTextContent("Tasks · 6 open"));
    expect(screen.getByRole("region", { name: "Working copy" })).toHaveTextContent("src/.DS_Store");
    expect(await screen.findByText(/Constellation lines slightly too bright/)).toBeInTheDocument();
  });

  it("tabs carry counts, change the URL and keep ?item", async () => {
    const { router, user } = renderApp("/p/notes-app?item=notes-app:commit:a1223b19ca");
    const tabs = await screen.findByRole("tablist", { name: "Project sections" });
    await waitFor(() => expect(within(tabs).getByRole("tab", { name: /Commits/ })).toHaveTextContent("Commits 5"));
    expect(within(tabs).getByRole("tab", { name: /Servers/ })).toHaveTextContent("Servers 4/4");
    expect(within(tabs).getByRole("tab", { name: /Overview/ })).toHaveAttribute("aria-selected", "true");
    await user.click(within(tabs).getByRole("tab", { name: /Sessions/ }));
    expect(router.state.location.pathname).toBe("/p/notes-app/sessions");
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:commit:a1223b19ca");
    expect(within(tabs).getByRole("tab", { name: /Sessions/ })).toHaveAttribute("aria-selected", "true");
    await user.click(within(tabs).getByRole("tab", { name: /Overview/ }));
    expect(router.state.location.pathname).toBe("/p/notes-app");
  });

  it("an unknown tab falls back to overview", async () => {
    renderApp("/p/notes-app/nonsense");
    expect(await screen.findByTestId("tab-overview")).toBeInTheDocument();
  });

  it("an unknown project shows a calm empty state, not a crash", async () => {
    const { user, router } = renderApp("/p/no_such_project");
    expect(await screen.findByTestId("project-missing")).toHaveTextContent("no project folder by this name");
    await user.click(screen.getByRole("button", { name: "pick a project →" }));
    expect(router.state.location.pathname).toBe("/p");
  });

  it("selecting a row opens the viewport by ref", async () => {
    const { router, user } = renderApp("/p/notes-app");
    const cont = await screen.findByRole("region", { name: "Continue" });
    const row = await within(cont).findByText("wire the refresh button to the server");
    await user.click(row);
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:session:121c3b19-ada3-495e-a518-7fe5acf0a71e");
  });

  it("native actions send only a ref, never a path or a command", async () => {
    rec = record();
    const { user } = renderApp("/p/notes-app");
    const actions = await screen.findByTestId("native-actions");
    await user.click(within(actions).getByRole("button", { name: "Editor" }));
    await user.click(within(actions).getByRole("button", { name: "Terminal" }));
    await user.click(await within(actions).findByRole("button", { name: "Open localhost:8765 in Safari" }));
    await waitFor(() => expect(rec!.list.filter((r) => r.path === "/api/open")).toHaveLength(3));
    const bodies = rec.list.filter((r) => r.path === "/api/open").map((r) => r.body);
    expect(bodies).toEqual([
      { ref: "notes-app:project:notes-app", action: "editor" },
      { ref: "notes-app:project:notes-app", action: "terminal" },
      { ref: "notes-app:server:8765", action: "safari" },
    ]);
  });

  it("hide from home writes the folder name to the hide list, and show on home removes it", async () => {
    rec = record();
    const { user } = renderApp("/p/notes-app");
    const btn = await screen.findByRole("button", { name: "hide from home" });
    await user.click(btn);
    await screen.findByRole("button", { name: "show on home" });
    expect(screen.getByTestId("cartouche")).toHaveTextContent("Hidden");
    expect(mockDb().hiddenFolders).toContain("notes-app");
    await user.click(screen.getByRole("button", { name: "show on home" }));
    await screen.findByRole("button", { name: "hide from home" });
    expect(mockDb().hiddenFolders).not.toContain("notes-app");
    const patches = rec.list.filter((r) => r.method === "PATCH" && r.path === "/api/settings");
    expect(patches).toHaveLength(2);
  });
});

describe("project seismograph", () => {
  it("a day click fills the readout; read the day opens the day list in Overview", async () => {
    const { user, router } = renderApp("/p/notes-app/commits");
    const grid = await screen.findByTestId("seis-grid");
    await waitFor(() => expect(within(grid).getAllByRole("button").length).toBe(30));
    expect(screen.getByTestId("day-readout")).toHaveTextContent("click a day column above to read it.");
    const last = within(grid).getAllByRole("button")[29];
    await user.click(last);
    expect(last).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByTestId("day-readout")).toHaveTextContent("file changes");
    await user.click(screen.getByRole("button", { name: "read the day →" }));
    expect(router.state.location.pathname).toBe("/p/notes-app");
    const panel = await screen.findByRole("region", { name: "Read the day" });
    await waitFor(() => expect(within(panel).queryByText("reading the day…")).toBeNull());
    await user.click(within(panel).getByRole("button", { name: "Close day readout" }));
    expect(screen.queryByRole("region", { name: "Read the day" })).toBeNull();
    // clicking the selected day again clears it
    await user.click(last);
    expect(screen.getByTestId("day-readout")).toHaveTextContent("click a day column above to read it.");
  });

  it("Esc clears the selected day first, and only then closes the viewport", async () => {
    const { user, router } = renderApp("/p/notes-app?item=notes-app:commit:b7dc920bef");
    await screen.findByRole("complementary", { name: "Viewport" });
    const grid = await screen.findByTestId("seis-grid");
    await waitFor(() => expect(within(grid).getAllByRole("button").length).toBe(30));
    const last = within(grid).getAllByRole("button")[29];
    await user.click(last);
    expect(last).toHaveAttribute("aria-pressed", "true");

    await user.keyboard("{Escape}");
    expect(last).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByTestId("day-readout")).toHaveTextContent("click a day column above to read it.");
    expect(screen.getByRole("complementary", { name: "Viewport" })).toBeInTheDocument();
    expect(router.state.location.search).toContain("item=");

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("complementary", { name: "Viewport" })).toBeNull());
  });

  it("Esc also closes an open day list together with the selection", async () => {
    const { user } = renderApp("/p/notes-app");
    const grid = await screen.findByTestId("seis-grid");
    await waitFor(() => expect(within(grid).getAllByRole("button").length).toBe(30));
    await user.click(within(grid).getAllByRole("button")[29]);
    await user.click(screen.getByRole("button", { name: "read the day →" }));
    await screen.findByRole("region", { name: "Read the day" });
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("region", { name: "Read the day" })).toBeNull();
    expect(within(grid).getAllByRole("button")[29]).toHaveAttribute("aria-pressed", "false");
  });

  it("range 7d asks for 7 days", async () => {
    const { user } = renderApp("/p/notes-app");
    const grid = await screen.findByTestId("seis-grid");
    await waitFor(() => expect(within(grid).getAllByRole("button").length).toBe(30));
    await user.click(screen.getByRole("button", { name: "7d" }));
    await waitFor(() => expect(within(grid).getAllByRole("button").length).toBe(7));
    expect(screen.getByTestId("seis-total")).toHaveTextContent("in 7d");
  });
});

describe("commits tab", () => {
  it("shows the first 200, then pages through the rest with show all", async () => {
    rec = record();
    const { user } = renderApp("/p/widget-kit/commits");
    const tab = await screen.findByTestId("tab-commits");
    const total = mockDb().detail["widget-kit"].commits.length;
    expect(total).toBeGreaterThan(200);
    const more = await within(tab).findByRole("button", { name: "show all " + total });
    expect(within(tab).getByText("newest 200 of " + total + " shown")).toBeInTheDocument();
    const rows = () => within(tab).getAllByTestId("commit-row");
    expect(rows().length).toBe(200);
    await user.click(more);
    await waitFor(() => expect(rows().length).toBe(total));
    expect(within(tab).queryByRole("button", { name: /show all/ })).toBeNull();
    const pages = rec.list.filter((r) => r.path.startsWith("/api/projects/widget-kit/commits?"));
    expect(pages.map((r) => new URL("http://x" + r.path).searchParams.get("cursor"))).toEqual([null, "200"]);
  });

  it("a commit row previews in the viewport", async () => {
    const { router, user } = renderApp("/p/notes-app/commits");
    const tab = await screen.findByTestId("tab-commits");
    await user.click(await within(tab).findByText("a1223b19ca"));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:commit:a1223b19ca");
  });

  it("a project with no commits says so", async () => {
    renderApp("/p/dotfiles/commits");
    expect(await screen.findByText("not under version control — no commits to read.")).toBeInTheDocument();
  });
});

describe("files tab", () => {
  it("loads the tree lazily, one folder at a time", async () => {
    rec = record();
    const { user, router } = renderApp("/p/notes-app/files");
    const tab = await screen.findByTestId("tab-files");
    const handoff = (await within(tab).findByText("design/")).closest("button")!;
    expect(handoff).toHaveAttribute("aria-expanded", "false");
    expect(within(tab).queryByText("HANDOFF.md")).toBeNull();
    // skipped folders are listed but do not open
    expect(within(tab).getByText("node_modules/").closest("button")).toBeDisabled();
    await user.click(handoff);
    expect(handoff).toHaveAttribute("aria-expanded", "true");
    await user.click(await within(tab).findByText("HANDOFF.md"));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:file:design/HANDOFF.md");
    const trees = rec.list.filter((r) => r.path.startsWith("/api/projects/notes-app/tree"));
    expect(trees.map((r) => new URL("http://x" + r.path).searchParams.get("dir"))).toEqual(["", "design"]);
    // closing and opening again uses the cache
    await user.click(handoff);
    await user.click(handoff);
    expect(rec.list.filter((r) => r.path.startsWith("/api/projects/notes-app/tree"))).toHaveLength(2);
  });

  it("marks dirty paths and searches files by name", async () => {
    const { user } = renderApp("/p/notes-app/files");
    const tab = await screen.findByTestId("tab-files");
    const src = (await within(tab).findByText("src/")).closest("button")!;
    expect(within(src).getByTitle("uncommitted change inside")).toBeInTheDocument();
    await user.type(within(tab).getByLabelText("Find file"), "HANDOFF");
    expect(await within(tab).findByText(/matches for “HANDOFF”/)).toBeInTheDocument();
    expect(await within(tab).findByText("HANDOFF.md")).toBeInTheDocument();
    // other projects' hits are dropped
    expect(within(tab).queryByText(/widget-kit/)).toBeNull();
  });
});

describe("files tab: Esc", () => {
  it("Esc clears the filter without closing the viewport; an Esc on an empty filter closes it", async () => {
    const { user, router } = renderApp("/p/notes-app/files?item=notes-app:commit:b7dc920bef");
    await screen.findByRole("complementary", { name: "Viewport" });
    const tab = await screen.findByTestId("tab-files");
    const input = within(tab).getByLabelText("Find file");
    await user.type(input, "HANDOFF");
    expect(await within(tab).findByText(/matches for “HANDOFF”/)).toBeInTheDocument();

    await user.keyboard("{Escape}");
    expect(input).toHaveValue("");
    expect(screen.getByRole("complementary", { name: "Viewport" })).toBeInTheDocument();
    expect(router.state.location.search).toContain("item=");

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("complementary", { name: "Viewport" })).toBeNull());
  });
});

describe("sessions tab", () => {
  it("lists own sessions, pages with next, and shows mentions separately", async () => {
    server.use(
      ...sessionsPaged(
        "acme-api",
        [fakeSession("s1", { title: "set up the probe" })],
        [fakeSession("s2", { title: "talk about acme-api from elsewhere", reason: "mention", cwd: "~/projects/tools/widget-kit" })],
      ),
    );
    const { user, router } = renderApp("/p/acme-api/sessions");
    const tab = await screen.findByTestId("tab-sessions");
    expect(await within(tab).findByText("set up the probe")).toBeInTheDocument();
    await user.click(within(tab).getByRole("button", { name: /show all/ }));
    const mentioned = await within(tab).findByTestId("sessions-mentioned");
    expect(mentioned).toHaveTextContent("↳ widget-kit");
    expect(mentioned).toHaveTextContent("talk about acme-api from elsewhere");
    await user.click(within(mentioned).getByText("talk about acme-api from elsewhere"));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("acme-api:session:s2");
  });

  it("empty state when no session ran here", async () => {
    renderApp("/p/acme-api/sessions");
    expect(await screen.findByTestId("sessions-empty")).toHaveTextContent("No Claude sessions ran in this folder.");
    expect(screen.getByText("No Claude sessions to resume")).toBeDisabled();
  });
});

describe("servers", () => {
  it("stop needs a confirm click within 5 s, then kills with confirm:true", async () => {
    rec = record();
    renderApp("/p/notes-app");
    const actions = await screen.findByTestId("native-actions");
    const btn = await within(actions).findByRole("button", { name: "Stop the server on port 8765" });
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    fireEvent.click(btn);
    expect(btn).toHaveTextContent("Confirm kill :8765");
    act(() => {
      vi.advanceTimersByTime(4999);
    });
    expect(btn).toHaveTextContent("Confirm kill :8765");
    act(() => {
      vi.advanceTimersByTime(2);
    });
    expect(btn).toHaveTextContent("Stop server :8765");
    expect(rec.list.some((r) => r.path === "/api/servers/kill")).toBe(false);
    fireEvent.click(btn);
    fireEvent.click(btn);
    vi.useRealTimers();
    await waitFor(() => expect(rec!.list.filter((r) => r.path === "/api/servers/kill")).toHaveLength(1));
    const body = rec.list.find((r) => r.path === "/api/servers/kill")!.body as { pid: number; key: string; confirm: boolean };
    expect(body).toEqual({ pid: 43661, key: "server-key:notes-app-8765", confirm: true });
    // the server is now recent: the header offers to start it again
    expect(await within(actions).findByRole("button", { name: "Start the server on port 8765" })).toHaveTextContent("Start server :8765");
  });

  it("servers tab lists active and recent rows and restarts a recent one", async () => {
    const db = mockDb();
    db.servers.find((s) => s.port === 8765)!.running = false;
    rec = record();
    const { user } = renderApp("/p/notes-app/servers");
    const tab = await screen.findByTestId("tab-servers");
    const row = await within(tab).findByTestId("server-8765");
    expect(row).toHaveTextContent("stopped");
    expect(within(row).getByRole("button", { name: "Preview localhost:8765" })).toBeDisabled();
    await user.click(within(row).getByRole("button", { name: "Start the server on port 8765" }));
    await waitFor(() => expect(rec!.list.filter((r) => r.path === "/api/servers/restart")).toHaveLength(1));
    expect(rec.list.find((r) => r.path === "/api/servers/restart")!.body).toEqual({ key: "server-key:notes-app-8765", confirm: true });
    await waitFor(() => expect(within(tab).getByTestId("server-8765")).toHaveTextContent("running"));
  });

  it("no listeners: calm empty state", async () => {
    renderApp("/p/widget-kit/servers");
    expect(await screen.findByTestId("servers-empty")).toHaveTextContent("No localhost listeners belong to this project.");
  });
});

describe("github block (decision A6)", () => {
  const withRemote = () => {
    const p = mockDb().projects.find((x) => x.id === "widget-kit")!;
    p.remote = "https://github.com/someone/widget-kit.git";
  };

  it("shows PRs, issues and CI when gh answers", async () => {
    withRemote();
    server.use(...githubWith());
    renderApp("/p/widget-kit");
    const block = await screen.findByTestId("github-block");
    expect(block).toHaveTextContent("Lazy file tree for big repos");
    expect(block).toHaveTextContent("Seismograph overflows at 7d");
    expect(block).toHaveTextContent("ci / test");
    expect(block).toHaveTextContent("success");
  });

  it("unavailable is a muted one-line note, not an error", async () => {
    withRemote();
    server.use(...githubUnavailable("gh not logged in"));
    renderApp("/p/widget-kit");
    expect(await screen.findByTestId("github-note")).toHaveTextContent("GitHub: gh not logged in.");
  });

  it("a failing gh call is also a muted note", async () => {
    withRemote();
    server.use(...githubBroken());
    renderApp("/p/widget-kit");
    await waitFor(() => expect(screen.getByTestId("github-note")).toHaveTextContent("GitHub data unavailable right now."));
  });

  it("no remote: no gh call", async () => {
    rec = record();
    renderApp("/p/notes-app");
    expect(await screen.findByTestId("github-note")).toHaveTextContent("no GitHub remote.");
    expect(rec.list.some((r) => r.path.endsWith("/github"))).toBe(false);
  });
});

describe("docs and tracker tabs", () => {
  it("docs: cards open the file", async () => {
    const { user, router } = renderApp("/p/notes-app/docs");
    const tab = await screen.findByTestId("tab-docs");
    await user.click(await within(tab).findByText("HANDOFF.md"));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:file:design/HANDOFF.md");
  });

  it("docs: empty state", async () => {
    renderApp("/p/acme-api/docs");
    expect(await screen.findByTestId("docs-empty")).toHaveTextContent("No .md or .txt files in the indexed tree.");
  });

  it("tracker: milestones with tasks, and the board links", async () => {
    const { user, router } = renderApp("/p/notes-app/tracker");
    const tab = await screen.findByTestId("tab-tracker");
    expect(await within(tab).findByText("Pages v3")).toBeInTheDocument();
    await user.click(within(tab).getByText("Ideas page: constellation over a compass rose"));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:task:T-1");
    await user.click(within(tab).getByRole("button", { name: "Open board →" }));
    expect(where(router)).toBe("/tracker?project=notes-app");
  });

  it("tracker: empty state", async () => {
    renderApp("/p/dotfiles/tracker");
    expect(await screen.findByTestId("tracker-empty")).toBeInTheDocument();
  });
});
