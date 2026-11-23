import { expect, test, type Page, type Response } from "@playwright/test";
import { mockApi, normal, signedInThenFailing, statusSequence } from "./fixtures";

// Waits for the refresh that fails and React Query's single retry, then lets the error state commit.
async function failedStatusChecks(page: Page, advance: () => Promise<void>) {
  const isFailedStatus = (r: Response) =>
    r.url().includes("/api/v1/auth/status") && r.status() === 503;
  const refresh = page.waitForResponse(isFailedStatus);
  await advance();
  await refresh;
  await page.waitForResponse(isFailedStatus);
  await page.waitForTimeout(250);
}

// AUD3-004: a failed /auth/status refetch must not discard a workspace or sign-in step that is already open.
test.describe("auth resilience", () => {
  test("open recovery-code preview survives a failed status refresh", async ({ page }) => {
    await page.clock.install();
    await mockApi(page, normal);
    await page.route("**/api/v1/profile", (route) =>
      route.fulfill({
        json: {
          email: "owner@example.org",
          totp: true,
          passkeys: {},
          recoveryRemaining: 7,
        },
      }),
    );
    await page.route("**/api/v1/profile/start", (route) =>
      route.fulfill({
        json: { confirmation: "fixture-confirmation", codes: ["AAAA-1111", "BBBB-2222"] },
      }),
    );
    await signedInThenFailing(page);
    await page.goto("/profile");
    await expect(page.getByText("7 unused recovery codes")).toBeVisible();

    await page.getByRole("button", { name: "Replace recovery codes" }).click();
    await page.getByLabel("Current password").fill("long-fixture-password");
    await page.getByLabel("Authenticator or recovery code").fill("123456");
    await page.getByRole("button", { name: "Continue", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "Save your recovery codes" }),
    ).toBeVisible();

    await failedStatusChecks(page, () => page.clock.fastForward(20_000));
    await expect(
      page.getByRole("heading", { name: "Save your recovery codes" }),
    ).toBeVisible();
    await expect(page.getByText("AAAA-1111")).toBeVisible();
    await expect(page.getByLabel("I saved these codes")).toBeVisible();
    await expect(page.getByText("Cannot reach the gallery")).toHaveCount(0);
  });

  test("sign-in verification step survives a failed status refresh", async ({ page }) => {
    await page.clock.install();
    await mockApi(page, normal, "login");
    await page.route("**/api/v1/auth/login", (route) =>
      route.fulfill({ json: { mfaRequired: true } }),
    );
    await statusSequence(page, [
      { status: 200, body: { authenticated: false, setup: false } },
      { status: 503 },
    ]);
    await page.goto("/");
    await page.getByLabel("Email", { exact: true }).fill("owner@example.org");
    await page.getByLabel("Password", { exact: true }).fill("long-fixture-password");
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page.getByLabel("Authenticator or recovery code")).toBeVisible();

    await failedStatusChecks(page, () => page.clock.fastForward(20_000));
    await expect(page.getByLabel("Authenticator or recovery code")).toBeVisible();
    await expect(page.getByText("Cannot reach the gallery")).toHaveCount(0);
  });

  test("first load without a status response still shows the blocking screen", async ({ page }) => {
    await mockApi(page, normal);
    await statusSequence(page, [{ status: 503 }]);
    await page.goto("/");
    await expect(page.getByRole("alert")).toContainText("Cannot reach the gallery");
    await expect(page.getByRole("button", { name: "Try again" })).toBeVisible();
  });
});
