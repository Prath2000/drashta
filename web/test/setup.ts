// Vitest setup: jest-dom matchers, MSW server, clean state between tests.
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterAll, afterEach, beforeAll, beforeEach } from "vitest";
import { resetApiState } from "~/lib/api";
import { clearCache } from "~/lib/query";
import { resetStore } from "~/lib/store";
import { resetDb } from "~/mocks/handlers";
import { server } from "~/mocks/node";

// Node 26 has its own (empty, file-less) localStorage global that hides jsdom's.
// Put jsdom's storage back so app code and tests use a working one.
{
  const dom = (globalThis as unknown as { jsdom?: { window: Window } }).jsdom;
  if (dom) {
    for (const k of ["localStorage", "sessionStorage"] as const) {
      Object.defineProperty(globalThis, k, { configurable: true, value: dom.window[k] });
    }
  }
}

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
beforeEach(() => {
  resetDb();
  resetApiState();
  clearCache();
  document.documentElement.className = "";
  resetStore();
  try {
    localStorage.clear();
  } catch {
    /* ignore */
  }
});
afterEach(() => {
  cleanup();
  server.resetHandlers();
});
afterAll(() => server.close());
