import { expect, test } from "@playwright/test";

test("shell loads, routes, and follows the Plash class", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("link", { name: "drashta, home" })).toBeVisible();
  await expect(page.getByText("every tremor in your projects, last 30 days")).toBeVisible();
  // counters come from the mocked /api/boot
  await expect(page.getByTestId("counter-review")).toContainText(/review\s*[1-9]/);

  // rail routes
  const rail = page.getByRole("navigation", { name: "Sections" });
  await rail.getByTestId("rail-tracker").click();
  await expect(page).toHaveURL(/\/tracker$/);
  await rail.getByTestId("rail-inbox").click();
  await expect(page).toHaveURL(/\/inbox$/);
  await rail.getByTestId("rail-project").click();
  await expect(page).toHaveURL(/\/p$/);

  // the name link goes home
  await page.getByRole("link", { name: "drashta, home" }).click();
  await expect(page).toHaveURL(/\/$/);

  // viewport opens from ?item= and closes on Esc
  await page.goto("/?item=acme-api:commit:93e82aa");
  const drawer = page.getByRole("complementary", { name: "Viewport" });
  await expect(drawer).toBeVisible();
  await drawer.press("Escape");
  await expect(drawer).toHaveCount(0);

  // readout: ?desktop=1 without the Plash class
  await page.goto("/?desktop=1");
  await expect(page.getByTestId("readout-hint")).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Sections" })).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "Search" })).toHaveCount(0);
  const pad = await page.locator(".dr-root").evaluate((el) => getComputedStyle(el).paddingTop);
  expect(pad).toBe("32px");

  // Plash turns browsing mode on: full command center at once, no reload
  await page.evaluate(() => document.documentElement.classList.add("plash-is-browsing-mode"));
  await expect(page.getByRole("navigation", { name: "Sections" })).toBeVisible();
  await expect(page.getByTestId("readout-hint")).toHaveCount(0);
  const pad2 = await page.locator(".dr-root").evaluate((el) => getComputedStyle(el).paddingTop);
  expect(pad2).toBe("0px");

  // and off again
  await page.evaluate(() => document.documentElement.classList.remove("plash-is-browsing-mode"));
  await expect(page.getByTestId("readout-hint")).toBeVisible();
  await page.screenshot({ path: "test-results/readout.png" });
});
