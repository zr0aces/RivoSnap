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
