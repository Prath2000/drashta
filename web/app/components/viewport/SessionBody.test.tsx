import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { mockDb } from "~/mocks/handlers";
import { server } from "~/mocks/node";
import { pagedSession } from "~/mocks/viewport";
import { renderApp } from "../../../test/render";

const SID = "121c3b19-ada3-495e-a518-7fe5acf0a71e";
const S = "/?item=notes-app:session:" + SID;

describe("session body", () => {
  it("shows meta, tools histogram chips and the transcript", async () => {
    renderApp(S);
    const body = await screen.findByTestId("session-body");
    expect(body).toHaveTextContent("Claude session · .claude-work · 121c3b19");
    const chips = within(body).getByTestId("tool-chips");
    expect(chips.textContent).toMatch(/^ToolsRead 3/);
    const turns = within(body).getByTestId("turns");
    await waitFor(() => expect(turns).toHaveTextContent("sample request 1"));
    expect(within(body).getByRole("button", { name: /All 7/ })).toHaveAttribute("aria-pressed", "true");
  });

  it("filters by role", async () => {
    const { user } = renderApp(S);
    const turns = await screen.findByTestId("turns");
    await waitFor(() => expect(turns).toHaveTextContent("sample answer 2"));
    await user.click(screen.getByRole("button", { name: /^Tools/ }));
    expect(turns).not.toHaveTextContent("sample answer 2");
    expect(turns).toHaveTextContent("Bash");
    await user.click(screen.getByRole("button", { name: /^You/ }));
    expect(turns).toHaveTextContent("sample request 1");
    expect(turns).not.toHaveTextContent("Bash");
  });

  it("pages the transcript with Load more (cursor = next)", async () => {
    server.use(pagedSession(SID, 95));
    const { user } = renderApp(S);
    const turns = await screen.findByTestId("turns");
    await waitFor(() => expect(turns).toHaveTextContent("40 of 95 turns loaded"));
    await user.click(within(turns).getByRole("button", { name: "Load 40 more turns" }));
    await waitFor(() => expect(turns).toHaveTextContent("80 of 95 turns loaded"));
    expect(turns).toHaveTextContent("turn 79");
    await user.click(within(turns).getByRole("button", { name: "Load 40 more turns" }));
    await waitFor(() => expect(turns).toHaveTextContent("turn 94"));
    expect(within(turns).queryByRole("button", { name: /more turns/ })).toBeNull();
    expect(screen.getByRole("button", { name: /^All/ })).toHaveTextContent("All 95");
  });

  it("Resume runs POST /open with action resume", async () => {
    let body: unknown = null;
    server.events.on("request:start", async ({ request }) => {
      if (request.method === "POST" && request.url.endsWith("/api/open")) body = await request.clone().json();
    });
    const { user } = renderApp(S);
    await user.click(await screen.findByRole("button", { name: "Resume in terminal ↗" }));
    await waitFor(() => expect(body).toEqual({ ref: "notes-app:session:" + SID, action: "resume" }));
    await waitFor(() => expect(screen.getByTestId("toast")).toHaveTextContent("ran: cd "));
    server.events.removeAllListeners();
  });

  it("lists edited files; inside the project opens a preview, outside says so", async () => {
    const sess = mockDb().detail["notes-app"].sessions.find((x) => x.id === SID)!;
    sess.edited = ["/fx/projects/personal/notes-app/src/main.js", "~/.claude-work/plans/x.md"];
    const { user, router } = renderApp(S);
    const ed = await screen.findByTestId("edited");
    await waitFor(() => expect(ed).toHaveTextContent("Files edited · 2"));
    const [inside, outside] = within(ed).getAllByRole("button");
    expect(inside).toHaveTextContent("src/main.jsin notes-app");
    expect(outside).toHaveTextContent("outside project");
    await user.click(outside);
    expect(screen.getByTestId("toast")).toHaveTextContent("is outside notes-app");
    await user.click(inside);
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("notes-app:file:src/main.js");
  });

  it("adds a session note", async () => {
    const { user } = renderApp(S);
    const notes = await screen.findByTestId("item-notes");
    await user.type(within(notes).getByRole("textbox", { name: "Add a note" }), "good session");
    await user.click(within(notes).getByRole("button", { name: "Add" }));
    await waitFor(() => expect(within(screen.getByTestId("item-notes")).getByText("good session")).toBeInTheDocument());
  });
});
