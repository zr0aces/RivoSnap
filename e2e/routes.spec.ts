import { expect, test } from "@playwright/test";
import { mockApi, normal } from "./fixtures";

// AUD3-018: Groups and Albums are separate views, so only the mounted one polls /groups.
test("routes poll the groups list once per mounted view", async ({ page }) => {
  await page.clock.install();
  await mockApi(page, normal);
  await page.goto("/groups");
  const sidebar = page.locator("aside");
  await expect(page.locator(".groupgrid")).toBeVisible();

  for (let visit = 0; visit < 5; visit++) {
    await sidebar.getByRole("link", { name: "Albums", exact: true }).click();
    await expect(page.getByRole("group", { name: "Filter albums" })).toBeVisible();
    await sidebar.getByRole("link", { name: "LINE groups", exact: true }).click();
    await expect(page.locator(".groupgrid")).toBeVisible();
  }

  let polls = 0;
  page.on("request", (r) => {
    if (new URL(r.url()).pathname === "/api/v1/groups") polls += 1;
  });
  const polled = page.waitForResponse(
    (r) => new URL(r.url()).pathname === "/api/v1/groups",
  );
  await page.clock.fastForward(16_000);
  await polled;
  await page.waitForTimeout(300);
  expect(polls).toBe(1);
});
