import { screen, waitFor, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { mockDb } from "~/mocks/handlers";
import { server } from "~/mocks/node";
import { renderApp } from "../../../test/render";

const C = "/?item=acme-api:commit:bdcb99c";

describe("commit body", () => {
  it("shows the header card, files changed and per-file diffs (first 3 open)", async () => {
    renderApp(C);
    const body = await screen.findByTestId("commit-body");
    expect(body).toHaveTextContent("git commit·bdcb99c");
    expect(body).toHaveTextContent("Guard API against CSRF and DNS rebinding");
    expect(body).toHaveTextContent("AuthorClaude Code");
    await waitFor(() => expect(within(body).getAllByTestId("diff-file")).toHaveLength(3));
    const files = within(body).getAllByRole("button", { name: /\.go/ }).filter((b) => b.className.includes("vp-cfile"));
    expect(files.map((b) => b.title)).toEqual(["Preview actions.go", "Preview main.go", "Preview server.go", "Preview server_test.go"]);
    const heads = within(body).getAllByRole("button", { expanded: true });
    expect(heads).toHaveLength(3);
    // syntax colours inside the diff
    expect(body.querySelector(".vp-dl .vp-tk-k")).not.toBeNull();
  });

  it("Collapse all / Expand all and a single toggle", async () => {
    const { user } = renderApp(C);
    const body = await screen.findByTestId("commit-body");
    await waitFor(() => expect(within(body).getAllByTestId("diff-file")).toHaveLength(3));
    await user.click(within(body).getByRole("button", { name: "Collapse all" }));
    expect(within(body).queryAllByRole("button", { expanded: true })).toHaveLength(0);
    expect(body.querySelector(".vp-dl")).toBeNull();
    await user.click(within(body).getByRole("button", { name: "Expand all" }));
    expect(within(body).getAllByRole("button", { expanded: true })).toHaveLength(3);
    const first = within(body).getAllByTestId("diff-file")[0];
    await user.click(within(first).getByRole("button", { expanded: true }));
    expect(within(first).getByRole("button", { expanded: false })).toBeInTheDocument();
  });

  it("shows the truncated banner when the patch holds fewer lines than the commit", async () => {
    renderApp(C);
    const t = await screen.findByTestId("commit-truncated");
    expect(t).toHaveTextContent(/Preview shows \d+ of 219 changed lines/);
    expect(within(t).getByRole("button", { name: "Full diff ↗" })).toBeInTheDocument();
  });

  it("shows the banner when the server says the patch was cut", async () => {
    server.use(
      http.get("*/api/projects/:p/commits/:id", () => {
        const c = mockDb().detail["acme-api"].commits.find((x) => x.id === "bdcb99c")!;
        return HttpResponse.json({ commit: { ...c, plus: 1, minus: 0 }, body: "", files: [{ path: "a.go", plus: 1, minus: 0 }], patch: "diff --git a/a.go b/a.go\n@@ -0,0 +1 @@\n+x\n", truncated: true });
      }),
    );
    renderApp(C);
    expect(await screen.findByTestId("commit-truncated")).toHaveTextContent("The patch was cut at 400 KB.");
  });

  it("a file in the list opens its preview", async () => {
    const { user, router } = renderApp(C);
    const body = await screen.findByTestId("commit-body");
    await user.click(within(body).getByTitle("Preview main.go"));
    expect(new URLSearchParams(router.state.location.search).get("item")).toBe("acme-api:file:main.go");
  });

  it("light summary when there is no patch", async () => {
    renderApp("/?item=sampler:commit:04069bd");
    expect(await screen.findByTestId("commit-light")).toHaveTextContent("light summary");
  });

  it("adds a commit note", async () => {
    const { user } = renderApp(C);
    const notes = await screen.findByTestId("commit-notes");
    await waitFor(() => expect(notes).toHaveTextContent("Carry this Host/Origin guard"));
    await user.type(within(notes).getByRole("textbox", { name: "Note on this commit" }), "port to drashta");
    await user.click(within(notes).getByRole("button", { name: "Add note" }));
    await waitFor(() => expect(within(screen.getByTestId("commit-notes")).getByText("port to drashta")).toBeInTheDocument());
    expect(screen.getByTestId("commit-notes")).toHaveTextContent("2 open · 0 resolved");
  });
});
