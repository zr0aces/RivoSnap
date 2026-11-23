import { expect, test } from "@playwright/test";
import { mockApi, normal } from "./fixtures";

// AUD3-029: health cards use the shared labels and show the worker time in Bangkok.
test("health page labels dependencies and shows worker progress in Bangkok time", async ({ page }) => {
  await mockApi(page, normal);
  await page.route("**/api/v1/health", (route) =>
    route.fulfill({
      json: {
        database: "Online",
        redis: "Online",
        worker: "Online",
        diskPressure: false,
        lineCredentials: "Undecryptable",
        workerLastProgressAt: "2026-05-01T12:00:00Z",
      },
    }),
  );
  await page.goto("/health");
  const card = (label: string) =>
    page.locator(".healthCard").filter({ has: page.locator("b", { hasText: label }) });
  await expect(card("Database")).toBeVisible();
  await expect(card("LINE credentials").locator(".pill")).toHaveText("Undecryptable");
  await expect(card("Last worker progress").locator(".pill")).toHaveText("1 May 2026, 19:00");
});
