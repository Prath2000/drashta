import { defineConfig, devices } from "@playwright/test";

// One headless smoke test against `npm run dev:mock` (MSW, no Go server).
export default defineConfig({
  testDir: "e2e",
  timeout: 30_000,
  retries: 0,
  reporter: "line",
  use: {
    baseURL: "http://localhost:5174",
    headless: true,
    viewport: { width: 1440, height: 900 },
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } }],
  webServer: {
    command: "npm run dev:mock",
    url: "http://localhost:5174",
    reuseExistingServer: true,
    timeout: 60_000,
  },
});
