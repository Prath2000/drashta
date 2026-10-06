// Test helper: render the shell with real routes in a memory router.
//
//   const { router, user } = renderApp("/tracker?filter=review");
//   await screen.findByText(...);
//
// The routes match app/routes.ts. MSW (test/setup.ts) answers /api.
import { render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { createMemoryRouter, Outlet, RouterProvider, type RouteObject } from "react-router";
import { Shell } from "~/components/shell/Shell";
import HomeRoute from "~/routes/home";
import InboxRoute from "~/routes/inbox";
import NotFound from "~/routes/not-found";
import PickerRoute from "~/routes/picker";
import ProjectRoute from "~/routes/project";
import TrackerRoute from "~/routes/tracker";

export const appRoutes: RouteObject[] = [
  {
    path: "/",
    element: (
      <Shell>
        <Outlet />
      </Shell>
    ),
    children: [
      { index: true, element: <HomeRoute /> },
      { path: "p", element: <PickerRoute /> },
      { path: "p/:project/:tab?", element: <ProjectRoute /> },
      { path: "tracker", element: <TrackerRoute /> },
      { path: "inbox", element: <InboxRoute /> },
      { path: "*", element: <NotFound /> },
    ],
  },
];

/** Renders the whole app (shell + routes) at `path`. */
export function renderApp(path = "/") {
  const router = createMemoryRouter(appRoutes, { initialEntries: [path] });
  const user = userEvent.setup();
  const utils = render(<RouterProvider router={router} />);
  return { ...utils, router, user };
}

/** Renders any element inside a router at `path` (no shell). */
export function renderWithRouter(ui: ReactNode, path = "/") {
  const router = createMemoryRouter([{ path: "*", element: ui }], { initialEntries: [path] });
  const user = userEvent.setup();
  const utils = render(<RouterProvider router={router} />);
  return { ...utils, router, user };
}
