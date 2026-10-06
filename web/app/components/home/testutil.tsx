// Test helper for the Home folder: a memory router around one element.
// It does not import test/render.tsx, which pulls in every route (other agents' files).
import { render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { createMemoryRouter, RouterProvider } from "react-router";

export function renderWithRouter(ui: ReactNode, path = "/") {
  const router = createMemoryRouter([{ path: "*", element: ui }], { initialEntries: [path] });
  const user = userEvent.setup();
  const utils = render(<RouterProvider router={router} />);
  return { ...utils, router, user };
}
