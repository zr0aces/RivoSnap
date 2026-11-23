import { readFileSync } from "node:fs";
import { test, expect } from "@playwright/test";
import { mockApi, normal } from "./fixtures";

test("gallery filters and keyboard-accessible viewer", async ({ page }) => {
  await mockApi(page, normal);
  await page.goto("/gallery");
  await expect(page.getByRole("heading", { name: "Gallery" })).toBeVisible();
  const version = readFileSync(new URL("../VERSION", import.meta.url), "utf8").trimEnd();
  await expect(page.locator(".asideFooter").getByText(`v${version}`, { exact: true })).toBeVisible();
  await expect(page.locator(".frameNo")).toHaveText(["#003", "#002", "#001"]);
  const trigger = page.getByRole("button", { name: /Photo from Family/ });
  await trigger.focus();
  await trigger.press("Enter");
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(trigger).toBeFocused();
  await page
    .getByRole("combobox", { name: "Status", exact: true })
    .selectOption("FAILED");
  await expect(page.getByText("No images found")).toBeVisible();
  await expect(page).toHaveURL(/status=FAILED/);
});
test("deployed owner session and health", async ({ page }) => {
  test.skip(!process.env["E2E_SETUP_KEY"], "Requires disposable Compose stack");
  await page.goto("/");
  await page.getByLabel("Email", { exact: true }).fill("operator@example.org");
  await page
    .getByLabel("Password", { exact: true })
    .fill("fixture-owner-passphrase");
  if (await page.getByLabel("Setup key").count()) {
    await page.getByLabel("Setup key").fill(process.env["E2E_SETUP_KEY"]!);
    await page.getByRole("button", { name: "Create owner account" }).click();
  } else await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  await page.getByRole("link", { name: "System health" }).click();
  await expect(
    page.locator(".row").filter({ hasText: "worker" }),
  ).toContainText("Online");
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeVisible();
});

test("logged-out load shows the login form without a reload loop", async ({
  page,
}) => {
  let statsRequests = 0;
  let navigations = 0;
  page.on("framenavigated", (f) => {
    if (f === page.mainFrame() && f.url().startsWith("http")) navigations++;
  });
  await page.route("**/api/v1/**", async (r) => {
    const path = new URL(r.request().url()).pathname;
    if (path.endsWith("auth/status"))
      return r.fulfill({ json: { setup: false, authenticated: false } });
    if (path.endsWith("dashboard/stats")) statsRequests++;
    await r.fulfill({ status: 401, json: { error: "Unauthorized" } });
  });
  await page.goto("/");
  await expect(page.locator("form")).toBeVisible();
  // Vite may reload once after cold dependency optimisation; only count navigations once settled.
  await page.waitForLoadState("networkidle");
  navigations = 0;
  await page.waitForTimeout(2000);
  await expect(page.locator("form")).toBeVisible();
  expect(statsRequests).toBe(0);
  expect(navigations).toBe(0);
});

test("sign-out failure stays visible and allows retry", async ({ page }) => {
  let attempts = 0;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status"))
      return route.fulfill({ json: { setup: false, authenticated: attempts < 2 } });
    if (path.endsWith("/auth/logout") && ++attempts === 1)
      return route.fulfill({ status: 500, json: { error: "Revocation failed" } });
    return route.fulfill({ json: {} });
  });
  await page.goto("/health");
  const button = page.getByRole("button", { name: "Sign out", exact: true });
  await button.click();
  await expect(page.getByRole("alert")).toContainText("Sign-out could not be confirmed");
  await button.click();
  await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeVisible();
});

test("viewer keeps the selected photo when the list refreshes", async ({ page }) => {
  await page.clock.install();
  await mockApi(page, normal);
  await page.goto("/gallery");
  await expect(page.locator("button.photo")).toHaveCount(3);
  await page.locator("button.photo").nth(1).click();
  const viewer = page.getByRole("dialog");
  await expect(viewer.locator("img")).toHaveAttribute("src", /\/images\/trips\/content$/);
  await expect(viewer.getByText("2 / 3", { exact: true })).toBeVisible();

  // A newer photo arrives at the front; the 15 s poll refetches the same page.
  const fresh = { ...normal.images[0], id: "fresh" };
  await page.route("**/api/v1/images*", (route) =>
    route.fulfill({
      json: { items: [fresh, ...normal.images], total: normal.images.length + 1, page: 1 },
    }),
  );
  const refreshed = page.waitForResponse(
    (r) => new URL(r.url()).pathname === "/api/v1/images" && r.status() === 200,
  );
  await page.clock.fastForward(16_000);
  await refreshed;
  await expect(page.locator("button.photo")).toHaveCount(4);
  await expect(viewer.getByText("3 / 4", { exact: true })).toBeVisible();
  await expect(viewer.locator("img")).toHaveAttribute("src", /\/images\/trips\/content$/);
});

test("page past the end links to the last page", async ({ page }) => {
  await mockApi(page, normal);
  await page.goto("/gallery?page=9");
  await expect(page.locator(".empty h2")).toHaveText("Page 9 is past the end.");
  await expect(page.locator(".empty p")).toHaveText("Last page has the oldest matching items.");
  await page.getByRole("link", { name: "Last page" }).click();
  await expect(page).toHaveURL(/[?&]page=1(&|$)/);
  await expect(page.locator("button.photo")).toHaveCount(3);
});

test("viewer stays closed when its photo returns after leaving the page", async ({ page }) => {
  await page.clock.install();
  await mockApi(page, normal);
  await page.goto("/gallery");
  await page.locator("button.photo").nth(1).click();
  const viewer = page.getByRole("dialog");
  await expect(viewer.locator("img")).toHaveAttribute("src", /\/images\/trips\/content$/);

  // The photo leaves the list: the viewer closes.
  const withoutTrips = [normal.images[0], normal.images[2]];
  await page.route("**/api/v1/images*", (route) =>
    route.fulfill({ json: { items: withoutTrips, total: withoutTrips.length, page: 1 } }),
  );
  let refreshed = page.waitForResponse(
    (r) => new URL(r.url()).pathname === "/api/v1/images" && r.status() === 200,
  );
  await page.clock.fastForward(16_000);
  await refreshed;
  await expect(page.getByRole("dialog")).toHaveCount(0);

  // It comes back on a later refresh: nothing reopens the viewer.
  await page.route("**/api/v1/images*", (route) =>
    route.fulfill({ json: { items: normal.images, total: normal.images.length, page: 1 } }),
  );
  refreshed = page.waitForResponse(
    (r) => new URL(r.url()).pathname === "/api/v1/images" && r.status() === 200,
  );
  await page.clock.fastForward(16_000);
  await refreshed;
  await expect(page.locator("button.photo")).toHaveCount(3);
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("page parameter is validated like the API", async ({ page }) => {
  await mockApi(page, normal);
  await page.goto("/gallery");
  const status = await page.evaluate(async () => {
    const res = await fetch("/api/v1/images?page=0");
    return res.status;
  });
  expect(status).toBe(400);
});
